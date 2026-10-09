// Package updater keeps the markets collection up to date: every
// POLL_INTERVAL it fetches markets updated since the last run and writes them to MongoDB.
package updater

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/db"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/polymarket"
)

const (
	// DefaultInterval is used when POLL_INTERVAL is not set. Updates come in
	// bursts that refresh the same markets every few minutes, so polling
	// more often mostly re-reads the same markets.
	DefaultInterval = 5 * time.Minute
	// defaultOverlap re-reads a bit of already processed history: the API
	// shows updates with a delay (~30–40 s) and many markets share one
	// updatedAt. Rewriting a market twice is harmless (upsert).
	defaultOverlap = 2 * time.Minute
	// catchUpPages: a cycle with more pages than this is logged at INFO.
	catchUpPages = 10
)

// Source is the Polymarket API (implemented by *polymarket.Client).
type Source interface {
	RecentlyUpdated(ctx context.Context, closed bool, fn func(page []polymarket.Market) (bool, error)) error
}

// Store is MongoDB (implemented by *db.MongoDB).
type Store interface {
	GetSyncState(ctx context.Context, id string) (*db.SyncState, error)
	SaveSyncState(ctx context.Context, st db.SyncState) error
	MaxUpdatedAt(ctx context.Context, closed bool) (time.Time, bool, error)
	UpsertMarkets(ctx context.Context, markets []polymarket.Market, syncedAt time.Time) (int, error)
	ListExcludedTags(ctx context.Context) ([]string, error)
	ListKeywords(ctx context.Context) ([]string, error)
	MarkAwesomeIDs(ctx context.Context, ids, excluded, keywords []string) (int64, error)
}

// Updater polls the API for updated markets.
type Updater struct {
	src      Source
	store    Store
	log      zerolog.Logger
	interval time.Duration
	overlap  time.Duration
	now      func() time.Time
}

// ParseInterval reads POLL_INTERVAL ("5m", "90s", ...); empty means DefaultInterval.
func ParseInterval(s string) (time.Duration, error) {
	if s == "" {
		return DefaultInterval, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid POLL_INTERVAL %q: %w", s, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("invalid POLL_INTERVAL %q: must be positive", s)
	}
	return d, nil
}

func New(src Source, store Store, log zerolog.Logger, interval time.Duration) *Updater {
	return &Updater{
		src:      src,
		store:    store,
		log:      log.With().Str("component", "updater").Logger(),
		interval: interval,
		overlap:  defaultOverlap,
		now:      time.Now,
	}
}

// Run polls until ctx is cancelled. A failed cycle is logged and retried on
// the next tick; it never stops the bot. Cycles never overlap: if one takes
// longer than the interval (catching up after downtime), the next starts
// right after it.
func (u *Updater) Run(ctx context.Context) error {
	u.log.Info().Dur("interval", u.interval).Msg("Market updater started")
	for {
		started := u.now()
		u.cycle(ctx)

		took := u.now().Sub(started)
		wait := u.interval - took
		if wait < 0 {
			wait = 0
			// Updates arrive faster than we process them: data lags more and
			// more. Raise POLL_INTERVAL or look for a slow API / MongoDB.
			if ctx.Err() == nil {
				u.log.Warn().
					Dur("took", took.Round(time.Second)).
					Dur("interval", u.interval).
					Msg("Market update cycle took longer than the interval; next cycle starts immediately")
			}
		}
		select {
		case <-ctx.Done():
			u.log.Info().Msg("Market updater stopped")
			return nil
		case <-time.After(wait):
		}
	}
}

// cycle updates open markets, then closed ones (closing shows up there).
func (u *Updater) cycle(ctx context.Context) {
	for _, closed := range []bool{false, true} {
		if err := u.pass(ctx, closed); err != nil {
			if ctx.Err() != nil {
				return // shutting down, not a failure
			}
			u.log.Error().Err(err).Bool("closed", closed).Msg("Market update failed; will retry next cycle")
		}
	}
}

func stateID(closed bool) string {
	if closed {
		return "markets_closed"
	}
	return "markets_open"
}

// pass fetches markets updated since the watermark, writes them, recomputes
// their awesome flag and moves the watermark. On error nothing is advanced,
// so the next cycle repeats the same range.
func (u *Updater) pass(ctx context.Context, closed bool) error {
	started := u.now()
	watermark, err := u.watermark(ctx, closed)
	if err != nil {
		return err
	}
	stopBefore := watermark.Add(-u.overlap)

	// Read once per pass: edits in the DB apply from the next cycle.
	excluded, err := u.store.ListExcludedTags(ctx)
	if err != nil {
		return err
	}
	keywords, err := u.store.ListKeywords(ctx)
	if err != nil {
		return err
	}

	newest := watermark
	total, pages := 0, 0
	err = u.src.RecentlyUpdated(ctx, closed, func(page []polymarket.Market) (bool, error) {
		pages++
		fresh := make([]polymarket.Market, 0, len(page))
		more := true
		for _, mk := range page {
			// Newest first: the first market older than the range ends the walk.
			if mk.UpdatedAt != nil && mk.UpdatedAt.Before(stopBefore) {
				more = false
				break
			}
			fresh = append(fresh, mk)
			if mk.UpdatedAt != nil && mk.UpdatedAt.After(newest) {
				newest = *mk.UpdatedAt
			}
		}
		if _, err := u.store.UpsertMarkets(ctx, fresh, u.now()); err != nil {
			return false, err
		}
		// Per page, not per cycle: after a long downtime a cycle covers
		// millions of markets, too many ids for one query.
		ids := make([]string, 0, len(fresh))
		for _, mk := range fresh {
			ids = append(ids, mk.ID)
		}
		if _, err := u.store.MarkAwesomeIDs(ctx, ids, excluded, keywords); err != nil {
			return false, err
		}
		total += len(fresh)
		return more, nil
	})
	if err != nil {
		return fmt.Errorf("fetch updated markets: %w", err)
	}

	if err := u.store.SaveSyncState(ctx, db.SyncState{
		ID:            stateID(closed),
		Watermark:     newest,
		LastSuccessAt: u.now(),
		LastMarkets:   total,
	}); err != nil {
		return err
	}

	ev := u.log.Debug()
	if pages > catchUpPages {
		ev = u.log.Info() // catching up after downtime
	}
	ev.Bool("closed", closed).
		Int("pages", pages).
		Int("markets", total).
		Time("watermark", newest).
		Dur("took", u.now().Sub(started).Round(time.Millisecond)).
		Msg("Markets updated")
	return nil
}

// watermark returns the stored watermark or, on the very first run, the
// newest updated_at already in the DB (from the full load). With an empty
// DB polling starts from now: the full load is a separate command.
func (u *Updater) watermark(ctx context.Context, closed bool) (time.Time, error) {
	st, err := u.store.GetSyncState(ctx, stateID(closed))
	if err != nil {
		return time.Time{}, err
	}
	if st != nil {
		return st.Watermark, nil
	}

	t, ok, err := u.store.MaxUpdatedAt(ctx, closed)
	if err != nil {
		return time.Time{}, err
	}
	if !ok {
		t = u.now()
	}
	u.log.Info().Bool("closed", closed).Time("watermark", t).Msg("No sync state yet; starting from the newest stored market")
	return t, nil
}

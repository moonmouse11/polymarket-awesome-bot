// Command load-markets performs a full load of all Polymarket markets (open
// and closed) into the "markets" collection. Run it manually: make markets-load.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/command"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/config"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/db"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/domain"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/logger"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/polymarket"
	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"
)

const (
	progressEveryPages = 10
	// pageBuffer is how many fetched pages may wait for the DB writer (~2 MB).
	// When it is full, fetching pauses until the writer catches up.
	pageBuffer = 10
)

// MarketLister is the Polymarket API (implemented by *polymarket.Client).
type MarketLister interface {
	AllMarkets(ctx context.Context, closed bool, fn func(page []domain.Market) error) error
}

// Upserter is MongoDB (implemented by *db.MongoDB).
type Upserter interface {
	UpsertMarkets(ctx context.Context, markets []domain.Market, syncedAt time.Time) (int, error)
}

func main() {
	cfg := config.MustLoad()
	log := logger.Must(cfg.LogLevel, cfg.LogFormat, os.Stdout)

	// Pages already written stay in the DB, so a rerun continues the load.
	command.Main("Market load", "written pages are kept, rerun to continue", log,
		func(ctx context.Context) error { return run(ctx, log, cfg) })
}

func run(ctx context.Context, log zerolog.Logger, cfg config.Config) error {
	mongoDB, err := db.Connect(ctx, cfg.MongoURI, cfg.MongoDBName)
	if err != nil {
		return fmt.Errorf("initialize MongoDB: %w", err)
	}
	defer mongoDB.CloseWithTimeout()

	// No timeout: building a new index on millions of markets takes minutes.
	if err := mongoDB.EnsureMarketIndexes(ctx); err != nil {
		return err
	}

	client := polymarket.NewClient(polymarket.DefaultBaseURL, log)

	// Open and closed passes run in parallel. A market that closes during the
	// load may come from both; UpsertMarkets keeps the closed copy.
	// If one pass fails, the shared context stops the other one too.
	g, gctx := errgroup.WithContext(ctx)
	for _, closed := range []bool{false, true} {
		g.Go(func() error {
			return loadPass(gctx, log, client, mongoDB, closed)
		})
	}
	return g.Wait()
}

// loadPass is a two-stage pipeline: one goroutine fetches pages from the API,
// another writes them to MongoDB, so a page is written while the next one is
// being fetched.
func loadPass(ctx context.Context, log zerolog.Logger, client MarketLister, store Upserter, closed bool) error {
	log = log.With().Bool("closed", closed).Logger()
	started := time.Now()
	pages, total, skipped := 0, 0, 0

	log.Info().Msg("Market load pass started")

	// Writer failure cancels gctx and stops the fetcher, and vice versa.
	g, gctx := errgroup.WithContext(ctx)
	pageCh := make(chan []domain.Market, pageBuffer)

	g.Go(func() error {
		defer close(pageCh)
		return client.AllMarkets(gctx, closed, func(page []domain.Market) error {
			select {
			case pageCh <- page:
				return nil
			case <-gctx.Done():
				return gctx.Err()
			}
		})
	})

	// Counters are touched only by the writer and read after g.Wait.
	g.Go(func() error {
		for page := range pageCh {
			n, err := store.UpsertMarkets(gctx, page, time.Now())
			if err != nil {
				return err
			}
			pages++
			total += len(page)
			skipped += n
			if pages%progressEveryPages == 0 {
				log.Info().Int("pages", pages).Int("markets", total).Msg("Loading markets")
			}
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		if ctx.Err() != nil {
			// Stopped by a signal or by a failure of the other pass.
			log.Info().Int("pages", pages).Int("markets", total).Msg("Market load pass stopped")
		}
		return fmt.Errorf("closed=%t pass after %d pages (%d markets): %w", closed, pages, total, err)
	}

	log.Info().
		Int("pages", pages).
		Int("markets", total).
		Int("skipped_closed", skipped).
		Dur("took", time.Since(started).Round(time.Second)).
		Msg("Market load pass finished")
	return nil
}

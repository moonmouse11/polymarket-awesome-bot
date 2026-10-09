package updater

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/db"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/polymarket"
)

var base = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func at(d time.Duration) *time.Time { t := base.Add(d); return &t }

// fakeSource serves fixed pages, newest first, like the API.
type fakeSource struct {
	pages [][]polymarket.Market
	err   error
	asked int // pages handed out
}

func (f *fakeSource) RecentlyUpdated(_ context.Context, _ bool, fn func([]polymarket.Market) (bool, error)) error {
	if f.err != nil {
		return f.err
	}
	for _, p := range f.pages {
		f.asked++
		more, err := fn(p)
		if err != nil || !more {
			return err
		}
	}
	return nil
}

type fakeStore struct {
	state      map[string]db.SyncState
	maxUpdated time.Time
	upserted   []string
	marked     [][]string
	onSave     func() // called after the closed pass is saved
}

func newFakeStore() *fakeStore { return &fakeStore{state: map[string]db.SyncState{}} }

func (f *fakeStore) GetSyncState(_ context.Context, id string) (*db.SyncState, error) {
	st, ok := f.state[id]
	if !ok {
		return nil, nil
	}
	return &st, nil
}

func (f *fakeStore) SaveSyncState(_ context.Context, st db.SyncState) error {
	f.state[st.ID] = st
	if f.onSave != nil && st.ID == "markets_closed" {
		f.onSave()
	}
	return nil
}

func (f *fakeStore) MaxUpdatedAt(context.Context, bool) (time.Time, bool, error) {
	return f.maxUpdated, !f.maxUpdated.IsZero(), nil
}

func (f *fakeStore) UpsertMarkets(_ context.Context, ms []polymarket.Market, _ time.Time) (int, error) {
	for _, m := range ms {
		f.upserted = append(f.upserted, m.ID)
	}
	return 0, nil
}

func (f *fakeStore) ListExcludedTags(context.Context) ([]string, error) {
	return []string{"Sports"}, nil
}
func (f *fakeStore) ListKeywords(context.Context) ([]string, error) { return []string{"alien"}, nil }

func (f *fakeStore) MarkAwesomeIDs(_ context.Context, ids, _, _ []string) (int64, error) {
	f.marked = append(f.marked, ids)
	return int64(len(ids)), nil
}

func newTestUpdater(src Source, store Store) *Updater {
	u := New(src, store, zerolog.Nop(), DefaultInterval)
	u.now = func() time.Time { return base.Add(time.Hour) }
	return u
}

func TestPass_BootstrapsFromDBAndStopsAtOverlap(t *testing.T) {
	store := newFakeStore()
	store.maxUpdated = base // newest market from the full load

	src := &fakeSource{pages: [][]polymarket.Market{
		{{ID: "new1", UpdatedAt: at(10 * time.Minute)}, {ID: "new2", UpdatedAt: at(5 * time.Minute)}},
		// -1m is inside the 2-minute overlap, -3m is older: the walk stops there.
		{{ID: "overlap", UpdatedAt: at(-time.Minute)}, {ID: "old", UpdatedAt: at(-3 * time.Minute)}},
		{{ID: "never", UpdatedAt: at(-time.Hour)}},
	}}

	if err := newTestUpdater(src, store).pass(context.Background(), false); err != nil {
		t.Fatalf("pass: %v", err)
	}

	if got := store.upserted; len(got) != 3 || got[0] != "new1" || got[1] != "new2" || got[2] != "overlap" {
		t.Errorf("upserted = %v, want [new1 new2 overlap]", got)
	}
	if src.asked != 2 {
		t.Errorf("pages asked = %d, want 2 (third page must not be fetched)", src.asked)
	}
	if len(store.marked) != 2 || len(store.marked[0]) != 2 || len(store.marked[1]) != 1 {
		t.Errorf("awesome recomputed per page = %v, want [[new1 new2] [overlap]]", store.marked)
	}

	st := store.state["markets_open"]
	if !st.Watermark.Equal(*at(10 * time.Minute)) || st.LastMarkets != 3 {
		t.Errorf("state = %+v, want watermark %v and 3 markets", st, *at(10 * time.Minute))
	}
}

func TestPass_UsesStoredWatermark(t *testing.T) {
	store := newFakeStore()
	store.maxUpdated = base.Add(-24 * time.Hour) // must be ignored
	store.state["markets_closed"] = db.SyncState{ID: "markets_closed", Watermark: base}

	src := &fakeSource{pages: [][]polymarket.Market{
		{{ID: "a", UpdatedAt: at(time.Minute)}, {ID: "b", UpdatedAt: at(-10 * time.Minute)}},
	}}
	if err := newTestUpdater(src, store).pass(context.Background(), true); err != nil {
		t.Fatalf("pass: %v", err)
	}
	if len(store.upserted) != 1 || store.upserted[0] != "a" {
		t.Errorf("upserted = %v, want [a]", store.upserted)
	}
}

func TestPass_ErrorKeepsWatermark(t *testing.T) {
	store := newFakeStore()
	store.state["markets_open"] = db.SyncState{ID: "markets_open", Watermark: base}

	src := &fakeSource{err: errors.New("API down")}
	if err := newTestUpdater(src, store).pass(context.Background(), false); err == nil {
		t.Fatal("pass succeeded, want error")
	}
	if st := store.state["markets_open"]; !st.Watermark.Equal(base) {
		t.Errorf("watermark moved to %v on error, want %v", st.Watermark, base)
	}
}

func TestPass_NoUpdatesKeepsWatermark(t *testing.T) {
	store := newFakeStore()
	store.state["markets_open"] = db.SyncState{ID: "markets_open", Watermark: base}

	src := &fakeSource{pages: [][]polymarket.Market{{{ID: "old", UpdatedAt: at(-time.Hour)}}}}
	if err := newTestUpdater(src, store).pass(context.Background(), false); err != nil {
		t.Fatalf("pass: %v", err)
	}
	if st := store.state["markets_open"]; !st.Watermark.Equal(base) || st.LastMarkets != 0 {
		t.Errorf("state = %+v, want unchanged watermark and 0 markets", st)
	}
}

func TestRun_StopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	u := New(&fakeSource{}, newFakeStore(), zerolog.Nop(), DefaultInterval)

	done := make(chan error, 1)
	go func() { done <- u.Run(ctx) }()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}

func TestParseInterval(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"", DefaultInterval, false},
		{"5m", 5 * time.Minute, false},
		{"90s", 90 * time.Second, false},
		{"5", 0, true}, // no unit
		{"0s", 0, true},
		{"-1m", 0, true},
	}
	for _, tt := range tests {
		got, err := ParseInterval(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("ParseInterval(%q) = %v, %v; want %v, err=%v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestRun_WarnsWhenCycleIsSlowerThanInterval(t *testing.T) {
	for _, tt := range []struct {
		name     string
		cycle    time.Duration
		wantWarn bool
	}{
		{"slow", 2 * DefaultInterval, true},
		{"fast", DefaultInterval / 2, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			u := New(&fakeSource{}, newFakeStore(), zerolog.New(&logs), DefaultInterval)
			// Each clock read advances time: the cycle "takes" tt.cycle.
			clock := base
			u.now = func() time.Time { clock = clock.Add(tt.cycle / 2); return clock }

			// Stop at the end of the cycle whose wait we check. The warning is
			// skipped while shutting down, so the slow case stops one cycle
			// later; the fast case must stop at once (its wait is real time).
			ctx, cancel := context.WithCancel(context.Background())
			stopAfter := 1
			if tt.wantWarn {
				stopAfter = 2
			}
			saves := 0
			u.store.(*fakeStore).onSave = func() {
				if saves++; saves == stopAfter {
					cancel()
				}
			}
			if err := u.Run(ctx); err != nil {
				t.Fatalf("Run: %v", err)
			}

			gotWarn := bytes.Contains(logs.Bytes(), []byte("took longer than the interval"))
			if gotWarn != tt.wantWarn {
				t.Errorf("warning logged = %v, want %v; logs:\n%s", gotWarn, tt.wantWarn, logs.String())
			}
		})
	}
}

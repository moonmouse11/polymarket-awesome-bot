package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/domain"
	"github.com/rs/zerolog"
)

// fakeSource sends `pages` one-market pages, then returns err. With endless
// set it keeps sending until fn fails, like a long load stopped by a writer error.
type fakeSource struct {
	pages   int
	err     error
	endless bool
	closed  bool
	stopErr error // what fn returned when the source stopped
}

func (f *fakeSource) AllMarkets(_ context.Context, closed bool, fn func(page []domain.Market) error) error {
	f.closed = closed
	for i := 0; f.endless || i < f.pages; i++ {
		if err := fn([]domain.Market{{ID: "m"}}); err != nil {
			f.stopErr = err
			return err
		}
	}
	return f.err
}

type fakeStore struct {
	mu      sync.Mutex
	written []domain.Market
	err     error
}

func (f *fakeStore) UpsertMarkets(_ context.Context, markets []domain.Market, _ time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	f.written = append(f.written, markets...)
	return 1, nil
}

func TestLoadPass_WritesAllPages(t *testing.T) {
	src := &fakeSource{pages: 25}
	store := &fakeStore{}
	if err := loadPass(context.Background(), zerolog.Nop(), src, store, true); err != nil {
		t.Fatalf("loadPass: %v", err)
	}
	if len(store.written) != 25 || !src.closed {
		t.Errorf("written %d markets, closed=%t; want 25, true", len(store.written), src.closed)
	}
}

func TestLoadPass_WriteErrorStopsFetching(t *testing.T) {
	src := &fakeSource{endless: true}
	store := &fakeStore{err: errors.New("mongo down")}
	err := loadPass(context.Background(), zerolog.Nop(), src, store, false)
	if err == nil || !strings.Contains(err.Error(), "mongo down") || !strings.Contains(err.Error(), "closed=false") {
		t.Errorf("err = %v, want the write error with the pass", err)
	}
	if !errors.Is(src.stopErr, context.Canceled) {
		t.Errorf("fetcher stopped with %v, want context.Canceled", src.stopErr)
	}
}

func TestLoadPass_FetchError(t *testing.T) {
	src := &fakeSource{pages: 2, err: errors.New("api 500")}
	store := &fakeStore{}
	err := loadPass(context.Background(), zerolog.Nop(), src, store, true)
	if err == nil || !strings.Contains(err.Error(), "api 500") || !strings.Contains(err.Error(), "closed=true") {
		t.Errorf("err = %v, want the fetch error with the pass", err)
	}
}

func TestRun_NeedsMongoURI(t *testing.T) {
	t.Setenv("MONGO_URI", "")
	if err := run(context.Background(), zerolog.Nop()); err == nil || !strings.Contains(err.Error(), "MONGO_URI") {
		t.Errorf("err = %v, want MONGO_URI is not set", err)
	}
}

func TestLoadPass_StoppedBySignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := loadPass(ctx, zerolog.Nop(), &fakeSource{endless: true}, &fakeStore{}, false)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

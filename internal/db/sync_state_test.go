package db

import (
	"context"
	"testing"
	"time"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/polymarket"
)

func TestSyncState_SaveAndGet(t *testing.T) {
	m := newTestMongo(t)
	ctx := context.Background()

	got, err := m.GetSyncState(ctx, "markets_open")
	if err != nil || got != nil {
		t.Fatalf("GetSyncState on empty = %+v, %v; want nil, nil", got, err)
	}

	wm := time.Date(2026, 10, 9, 2, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ { // the second save replaces the first
		st := SyncState{ID: "markets_open", Watermark: wm.Add(time.Duration(i) * time.Minute), LastSuccessAt: time.Now(), LastMarkets: 10 + i}
		if err := m.SaveSyncState(ctx, st); err != nil {
			t.Fatalf("SaveSyncState: %v", err)
		}
	}

	got, err = m.GetSyncState(ctx, "markets_open")
	if err != nil || got == nil {
		t.Fatalf("GetSyncState = %+v, %v", got, err)
	}
	if !got.Watermark.Equal(wm.Add(time.Minute)) || got.LastMarkets != 11 {
		t.Fatalf("state = %+v, want watermark %v and 11 markets", got, wm.Add(time.Minute))
	}
}

func TestMaxUpdatedAt(t *testing.T) {
	m := newTestMongo(t)
	ctx := context.Background()

	if _, ok, err := m.MaxUpdatedAt(ctx, false); err != nil || ok {
		t.Fatalf("MaxUpdatedAt on empty: ok=%v err=%v, want false nil", ok, err)
	}

	at := func(h int) *time.Time { t := time.Date(2026, 10, 9, h, 0, 0, 0, time.UTC); return &t }
	if _, err := m.UpsertMarkets(ctx, []polymarket.Market{
		{ID: "1", UpdatedAt: at(1)},
		{ID: "2", UpdatedAt: at(3)},
		{ID: "3", UpdatedAt: at(5), Closed: true}, // newer, but closed
		{ID: "4"}, // no updated_at
	}, time.Now()); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, ok, err := m.MaxUpdatedAt(ctx, false)
	if err != nil || !ok || !got.Equal(*at(3)) {
		t.Fatalf("MaxUpdatedAt(open) = %v %v %v, want %v", got, ok, err, *at(3))
	}
}

func TestMarkAwesomeIDs_OnlyGivenMarkets(t *testing.T) {
	m := newTestMongo(t)
	ctx := context.Background()

	if _, err := m.UpsertMarkets(ctx, []polymarket.Market{
		{ID: "a", Tags: []string{"Culture"}},
		{ID: "b", Tags: []string{"Culture"}},
	}, time.Now()); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := m.MarkAwesomeIDs(ctx, []string{"a"}, nil, nil); err != nil {
		t.Fatalf("MarkAwesomeIDs: %v", err)
	}

	if got := readAwesome(t, m, "a"); !got.IsAwesome {
		t.Errorf("a: not marked: %+v", got)
	}
	if got := readAwesome(t, m, "b"); got.CheckedAt != nil {
		t.Errorf("b: marked although not in ids: %+v", got)
	}
}

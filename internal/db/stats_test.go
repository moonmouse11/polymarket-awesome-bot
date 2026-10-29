package db

import (
	"context"
	"testing"
	"time"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/domain"
)

func TestStats(t *testing.T) {
	m := newTestMongo(t)
	ctx := context.Background()
	if err := m.EnsureMarketIndexes(ctx); err != nil {
		t.Fatalf("EnsureMarketIndexes: %v", err)
	}

	now := time.Now().UTC()
	recent, old := now.Add(-time.Hour), now.Add(-48*time.Hour)
	if _, err := m.UpsertMarkets(ctx, []domain.Market{
		{ID: "new", Question: "UFO?", CreatedAt: &recent, UpdatedAt: &recent},
		{ID: "old", Question: "Old?", CreatedAt: &old, UpdatedAt: &recent, Tags: []string{"Sports"}},
		{ID: "stale", Question: "Stale?", CreatedAt: &old, UpdatedAt: &old, Closed: true},
	}, now); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := m.MarkAwesome(ctx, FilterRules{DenyTags: []string{"Sports"}}); err != nil {
		t.Fatalf("MarkAwesome: %v", err)
	}
	if err := m.MarkNotified(ctx, "new", now, 5); err != nil {
		t.Fatalf("MarkNotified: %v", err)
	}

	st, err := m.Stats(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	want := Stats{
		Awesome: 2, AwesomeOpen: 1, // "new" and closed "stale"
		Updated: 2, Created: 1, BecameAwesome: 2, Announced: 1,
	}
	if st != want {
		t.Errorf("Stats = %+v\nwant    %+v", st, want)
	}
	if err := m.Ping(ctx); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

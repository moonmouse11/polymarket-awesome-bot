package db

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/polymarket"
)

func TestUpsertMarkets_ReloadDoesNotDuplicate(t *testing.T) {
	m := newTestMongo(t)
	ctx := context.Background()
	coll := m.db.Collection(marketsCollection)

	if err := m.EnsureMarketIndexes(ctx); err != nil {
		t.Fatalf("EnsureMarketIndexes: %v", err)
	}

	first := []polymarket.Market{
		{ID: "1", Question: "Aliens?", OutcomePrices: []float64{0.1, 0.9}},
		{ID: "2", Question: "UFO?"},
	}
	if _, err := m.UpsertMarkets(ctx, first, time.Now()); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Second load: market 1 changed, market 2 is the same, market 3 is new.
	second := []polymarket.Market{
		{ID: "1", Question: "Aliens?", Closed: true, OutcomePrices: []float64{0.0, 1.0}},
		{ID: "2", Question: "UFO?"},
		{ID: "3", Question: "Matrix?"},
	}
	if _, err := m.UpsertMarkets(ctx, second, time.Now()); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	count, err := coll.CountDocuments(ctx, bson.D{})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}

	var got polymarket.Market
	if err := coll.FindOne(ctx, bson.D{{Key: "_id", Value: "1"}}).Decode(&got); err != nil {
		t.Fatalf("find market 1: %v", err)
	}
	if !got.Closed || got.OutcomePrices[1] != 1.0 || got.SyncedAt.IsZero() {
		t.Fatalf("market 1 not replaced: %+v", got)
	}
}

func TestUpsertMarkets_ClosedWins(t *testing.T) {
	m := newTestMongo(t)
	ctx := context.Background()
	coll := m.db.Collection(marketsCollection)

	// The closed pass has already stored market 1 as closed.
	if _, err := m.UpsertMarkets(ctx, []polymarket.Market{
		{ID: "1", Question: "Aliens?", Closed: true},
	}, time.Now()); err != nil {
		t.Fatalf("closed upsert: %v", err)
	}

	// The open pass arrives later with a stale open copy of market 1
	// and a genuinely open market 2 in the same batch.
	skipped, err := m.UpsertMarkets(ctx, []polymarket.Market{
		{ID: "1", Question: "Aliens? (stale)"},
		{ID: "2", Question: "UFO?"},
	}, time.Now())
	if err != nil {
		t.Fatalf("open upsert: %v", err)
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}

	var got polymarket.Market
	if err := coll.FindOne(ctx, bson.D{{Key: "_id", Value: "1"}}).Decode(&got); err != nil {
		t.Fatalf("find market 1: %v", err)
	}
	if !got.Closed || got.Question != "Aliens?" {
		t.Fatalf("closed market 1 was overwritten: %+v", got)
	}

	count, err := coll.CountDocuments(ctx, bson.D{})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2 (open market 2 must still be inserted)", count)
	}
}

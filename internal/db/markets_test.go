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
	if err := m.UpsertMarkets(ctx, first, time.Now()); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Second load: market 1 changed, market 2 is the same, market 3 is new.
	second := []polymarket.Market{
		{ID: "1", Question: "Aliens?", Closed: true, OutcomePrices: []float64{0.0, 1.0}},
		{ID: "2", Question: "UFO?"},
		{ID: "3", Question: "Matrix?"},
	}
	if err := m.UpsertMarkets(ctx, second, time.Now()); err != nil {
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

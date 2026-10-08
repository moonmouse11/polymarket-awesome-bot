package db

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/polymarket"
)

const marketsCollection = "markets"

// EnsureMarketIndexes creates the indexes used by polling and lookups
// (no-op for indexes that already exist).
func (m *MongoDB) EnsureMarketIndexes(ctx context.Context) error {
	_, err := m.db.Collection(marketsCollection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "updated_at", Value: -1}}},
		{Keys: bson.D{{Key: "closed", Value: 1}}},
		{Keys: bson.D{{Key: "tags", Value: 1}}},
	})
	if err != nil {
		return fmt.Errorf("create markets indexes: %w", err)
	}
	return nil
}

// UpsertMarkets writes a page of markets in one round trip. Each market
// replaces the stored document with the same _id (Polymarket id) or is
// inserted, so loading the same markets again never creates duplicates.
func (m *MongoDB) UpsertMarkets(ctx context.Context, markets []polymarket.Market, syncedAt time.Time) error {
	if len(markets) == 0 {
		return nil
	}

	models := make([]mongo.WriteModel, 0, len(markets))
	for _, mk := range markets {
		mk.SyncedAt = syncedAt.UTC()
		models = append(models, mongo.NewReplaceOneModel().
			SetFilter(bson.D{{Key: "_id", Value: mk.ID}}).
			SetReplacement(mk).
			SetUpsert(true))
	}

	_, err := m.db.Collection(marketsCollection).BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	if err != nil {
		return fmt.Errorf("upsert markets: %w", err)
	}
	return nil
}

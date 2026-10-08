package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/polymarket"
)

const marketsCollection = "markets"

// duplicateKeyCode is the MongoDB error code for a unique index violation.
const duplicateKeyCode = 11000

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

// UpsertMarkets writes markets by _id: replaces stored documents and inserts
// new ones, so reloading never creates duplicates.
//
// A closed market is never overwritten by an open copy: closing is final on
// Polymarket, and an open copy can only be stale (fetched before it closed,
// e.g. by the open-markets pass running in parallel with the closed one).
// It returns how many such stale open copies were skipped.
func (m *MongoDB) UpsertMarkets(ctx context.Context, markets []polymarket.Market, syncedAt time.Time) (skipped int, err error) {
	if len(markets) == 0 {
		return 0, nil
	}

	models := make([]mongo.WriteModel, 0, len(markets))
	for _, mk := range markets {
		mk.SyncedAt = syncedAt.UTC()
		filter := bson.D{{Key: "_id", Value: mk.ID}}
		if !mk.Closed {
			// No match on a stored closed market → the upsert tries to insert
			// the same _id and gets a duplicate key error: that is the skip.
			filter = append(filter, bson.E{Key: "closed", Value: bson.D{{Key: "$ne", Value: true}}})
		}
		models = append(models, mongo.NewReplaceOneModel().
			SetFilter(filter).
			SetReplacement(mk).
			SetUpsert(true))
	}

	_, err = m.db.Collection(marketsCollection).BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	if n, ok := onlyDuplicateKeys(err); ok {
		return n, nil
	}
	if err != nil {
		return 0, fmt.Errorf("upsert markets: %w", err)
	}
	return 0, nil
}

// onlyDuplicateKeys reports whether err is a bulk write error made only of
// duplicate key errors, and how many there are.
func onlyDuplicateKeys(err error) (int, bool) {
	var bulkErr mongo.BulkWriteException
	if !errors.As(err, &bulkErr) || bulkErr.WriteConcernError != nil || len(bulkErr.WriteErrors) == 0 {
		return 0, false
	}
	for _, we := range bulkErr.WriteErrors {
		if we.Code != duplicateKeyCode {
			return 0, false
		}
	}
	return len(bulkErr.WriteErrors), true
}

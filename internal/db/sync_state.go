package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const syncStateCollection = "sync_state"

// SyncState is the progress of one polling stream (e.g. open markets).
type SyncState struct {
	ID string `bson:"_id"`
	// Watermark is the newest updatedAt already processed.
	Watermark     time.Time `bson:"watermark"`
	LastSuccessAt time.Time `bson:"last_success_at"`
	// LastMarkets is how many markets the last successful cycle wrote.
	LastMarkets int `bson:"last_markets"`
}

// GetSyncState returns the stored state, or nil if there is none yet.
func (m *MongoDB) GetSyncState(ctx context.Context, id string) (*SyncState, error) {
	var st SyncState
	err := m.db.Collection(syncStateCollection).FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&st)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get sync state %s: %w", id, err)
	}
	return &st, nil
}

// SaveSyncState stores the state, replacing the previous one.
func (m *MongoDB) SaveSyncState(ctx context.Context, st SyncState) error {
	st.Watermark = st.Watermark.UTC()
	st.LastSuccessAt = st.LastSuccessAt.UTC()
	_, err := m.db.Collection(syncStateCollection).ReplaceOne(ctx,
		bson.D{{Key: "_id", Value: st.ID}}, st, options.Replace().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("save sync state %s: %w", st.ID, err)
	}
	return nil
}

// MaxUpdatedAt returns the newest updated_at among open or closed markets;
// ok is false when there are none.
func (m *MongoDB) MaxUpdatedAt(ctx context.Context, closed bool) (t time.Time, ok bool, err error) {
	var doc struct {
		UpdatedAt *time.Time `bson:"updated_at"`
	}
	err = m.db.Collection(marketsCollection).FindOne(ctx,
		bson.D{{Key: "closed", Value: closed}, {Key: "updated_at", Value: bson.D{{Key: "$ne", Value: nil}}}},
		options.FindOne().SetSort(bson.D{{Key: "updated_at", Value: -1}}).SetProjection(bson.D{{Key: "updated_at", Value: 1}}),
	).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("max updated_at (closed=%t): %w", closed, err)
	}
	if doc.UpdatedAt == nil {
		return time.Time{}, false, nil
	}
	return doc.UpdatedAt.UTC(), true, nil
}

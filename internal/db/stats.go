package db

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

// Stats is what the /status admin command shows. Every count uses an index.
type Stats struct {
	PendingSends int64
	PendingEdits int64

	Awesome     int64
	AwesomeOpen int64

	// Over the last 24 hours.
	Updated       int64 // distinct markets with updated_at in the window
	Created       int64 // new markets
	BecameAwesome int64 // awesome_since in the window
	Announced     int64 // posted to the channel
}

// Ping checks the connection to MongoDB.
func (m *MongoDB) Ping(ctx context.Context) error {
	return m.client.Ping(ctx, nil)
}

// QueueSizes returns how many markets wait to be sent and to be edited.
func (m *MongoDB) QueueSizes(ctx context.Context) (sends, edits int64, err error) {
	coll := m.db.Collection(marketsCollection)
	if sends, err = coll.CountDocuments(ctx, bson.D{{Key: fieldNotifyPending, Value: true}}); err != nil {
		return 0, 0, fmt.Errorf("count pending sends: %w", err)
	}
	if edits, err = coll.CountDocuments(ctx, bson.D{{Key: fieldEditPending, Value: true}}); err != nil {
		return 0, 0, fmt.Errorf("count pending edits: %w", err)
	}
	return sends, edits, nil
}

// Stats counts queues, awesome markets and the activity since `since`.
func (m *MongoDB) Stats(ctx context.Context, since time.Time) (Stats, error) {
	var st Stats
	var err error
	if st.PendingSends, st.PendingEdits, err = m.QueueSizes(ctx); err != nil {
		return st, err
	}
	if st.Awesome, st.AwesomeOpen, err = m.CountAwesome(ctx); err != nil {
		return st, err
	}

	coll := m.db.Collection(marketsCollection)
	after := bson.D{{Key: "$gte", Value: since.UTC()}}
	for _, c := range []struct {
		field string
		dst   *int64
	}{
		{"updated_at", &st.Updated},
		{"created_at", &st.Created},
		{fieldAwesomeSince, &st.BecameAwesome},
		{fieldNotifiedAt, &st.Announced},
	} {
		n, err := coll.CountDocuments(ctx, bson.D{{Key: c.field, Value: after}})
		if err != nil {
			return st, fmt.Errorf("count markets by %s: %w", c.field, err)
		}
		*c.dst = n
	}
	return st, nil
}

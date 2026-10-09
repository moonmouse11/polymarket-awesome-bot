package db

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/polymarket"
)

// AwesomeMarket is a market waiting for a notification, with the reason it is awesome.
type AwesomeMarket struct {
	polymarket.Market `bson:",inline"`
	Reason            string    `bson:"awesome_reason"`
	Words             []string  `bson:"awesome_words,omitempty"`
	ExcludedBy        string    `bson:"awesome_excluded_tag,omitempty"` // overridden by Words
	Since             time.Time `bson:"awesome_since"`
}

// PendingNotifications returns up to limit markets that became awesome while
// open and were never announced, oldest first. The queue is filled by
// MarkAwesome (see notify_pending there).
func (m *MongoDB) PendingNotifications(ctx context.Context, limit int64) ([]AwesomeMarket, error) {
	cur, err := m.db.Collection(marketsCollection).Find(ctx,
		bson.D{{Key: fieldNotifyPending, Value: true}},
		options.Find().SetSort(bson.D{{Key: fieldAwesomeSince, Value: 1}}).SetLimit(limit),
	)
	if err != nil {
		return nil, fmt.Errorf("find pending notifications: %w", err)
	}
	var out []AwesomeMarket
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("decode pending notifications: %w", err)
	}
	return out, nil
}

// MarkNotified removes the market from the queue for good: notified_at
// prevents a second notification even if it stops and becomes awesome again.
// messageID is the channel message (0: none was posted).
func (m *MongoDB) MarkNotified(ctx context.Context, id string, at time.Time, messageID int) error {
	set := bson.D{{Key: fieldNotifiedAt, Value: at.UTC()}}
	if messageID != 0 {
		set = append(set, bson.E{Key: fieldMessageID, Value: messageID})
	}
	_, err := m.db.Collection(marketsCollection).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: id}},
		bson.D{
			{Key: "$set", Value: set},
			{Key: "$unset", Value: bson.D{{Key: fieldNotifyPending, Value: ""}}},
		},
	)
	if err != nil {
		return fmt.Errorf("mark market %s notified: %w", id, err)
	}
	return nil
}

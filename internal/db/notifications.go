package db

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/awesome"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/domain"
)

// AwesomeMarket is a market waiting for a notification, with the reason it is awesome.
type AwesomeMarket struct {
	domain.Market `bson:",inline"`
	Reason        string          `bson:"awesome_reason"`
	Matches       awesome.Matches `bson:"awesome_matches,omitempty"`
	Since         time.Time       `bson:"awesome_since"`
	MessageID     int             `bson:"telegram_message_id,omitempty"`
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

// PendingEdits returns up to limit announced markets whose channel message
// must be edited, oldest message first.
func (m *MongoDB) PendingEdits(ctx context.Context, limit int64) ([]AwesomeMarket, error) {
	cur, err := m.db.Collection(marketsCollection).Find(ctx,
		bson.D{{Key: fieldEditPending, Value: true}},
		options.Find().SetSort(bson.D{{Key: fieldMessageID, Value: 1}}).SetLimit(limit),
	)
	if err != nil {
		return nil, fmt.Errorf("find pending edits: %w", err)
	}
	var out []AwesomeMarket
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("decode pending edits: %w", err)
	}
	return out, nil
}

// MarkEdited removes the market from the edit queue.
func (m *MongoDB) MarkEdited(ctx context.Context, id string) error {
	_, err := m.db.Collection(marketsCollection).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: id}},
		bson.D{{Key: "$unset", Value: bson.D{{Key: fieldEditPending, Value: ""}}}},
	)
	if err != nil {
		return fmt.Errorf("mark market %s edited: %w", id, err)
	}
	return nil
}

// PendingDelete is a channel message waiting to be deleted.
type PendingDelete struct {
	ID        string `bson:"_id"`
	MessageID int    `bson:"telegram_message_id"`
}

// PendingDeletes returns up to limit messages of markets that are no longer
// awesome, oldest message first.
func (m *MongoDB) PendingDeletes(ctx context.Context, limit int64) ([]PendingDelete, error) {
	cur, err := m.db.Collection(marketsCollection).Find(ctx,
		bson.D{{Key: fieldDeletePending, Value: true}},
		options.Find().SetSort(bson.D{{Key: fieldMessageID, Value: 1}}).SetLimit(limit).
			SetProjection(bson.D{{Key: fieldMessageID, Value: 1}}),
	)
	if err != nil {
		return nil, fmt.Errorf("find pending deletes: %w", err)
	}
	var out []PendingDelete
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("decode pending deletes: %w", err)
	}
	return out, nil
}

// MarkDeleted forgets the deleted messages: the markets count as never
// announced, so one that becomes awesome again is posted anew with a new
// message id.
func (m *MongoDB) MarkDeleted(ctx context.Context, ids []string, at time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := m.db.Collection(marketsCollection).UpdateMany(ctx,
		bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}},
		bson.D{
			{Key: "$set", Value: bson.D{{Key: fieldDeletedAt, Value: at.UTC()}}},
			{Key: "$unset", Value: bson.D{
				{Key: fieldDeletePending, Value: ""},
				{Key: fieldEditPending, Value: ""},
				{Key: fieldMessageID, Value: ""},
				{Key: fieldNotifiedAt, Value: ""},
			}},
		},
	)
	if err != nil {
		return fmt.Errorf("mark %d messages deleted: %w", len(ids), err)
	}
	return nil
}

// MarkDeleteFailed takes the market out of the delete queue, keeping its
// message: Telegram refused to delete it (e.g. too old).
func (m *MongoDB) MarkDeleteFailed(ctx context.Context, id, reason string) error {
	_, err := m.db.Collection(marketsCollection).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: id}},
		bson.D{
			{Key: "$set", Value: bson.D{{Key: "telegram_delete_error", Value: reason}}},
			{Key: "$unset", Value: bson.D{{Key: fieldDeletePending, Value: ""}}},
		},
	)
	if err != nil {
		return fmt.Errorf("mark market %s delete failed: %w", id, err)
	}
	return nil
}

// RestoreNotified records a channel message found in a chat export (after
// the database was lost): the market counts as announced with that message,
// so it is not posted again and its message can be edited or deleted.
// Returns false when no market has the slug.
func (m *MongoDB) RestoreNotified(ctx context.Context, slug string, messageID int, at time.Time) (bool, error) {
	res, err := m.db.Collection(marketsCollection).UpdateOne(ctx,
		bson.D{{Key: "slug", Value: slug}},
		bson.D{
			{Key: "$set", Value: bson.D{
				{Key: fieldNotifiedAt, Value: at.UTC()},
				{Key: fieldMessageID, Value: messageID},
			}},
			{Key: "$unset", Value: bson.D{{Key: fieldNotifyPending, Value: ""}}},
		},
	)
	if err != nil {
		return false, fmt.Errorf("restore message %d of market %s: %w", messageID, slug, err)
	}
	return res.MatchedCount > 0, nil
}

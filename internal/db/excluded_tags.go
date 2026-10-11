package db

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const excludedTagsCollection = "excluded_tags"

// ExcludedTag is a Polymarket tag that makes a market not awesome.
type ExcludedTag struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"`
	Tag       string             `bson:"tag"`
	CreatedAt time.Time          `bson:"created_at"`
}

// EnsureExcludedTags creates a unique index on "tag". Tags live only in the
// database and are stored as given: they must match Polymarket labels
// exactly. Nothing is seeded; with no tags the updater pauses.
func (m *MongoDB) EnsureExcludedTags(ctx context.Context) error {
	_, err := m.db.Collection(excludedTagsCollection).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "tag", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return fmt.Errorf("create excluded tags index: %w", err)
	}
	return nil
}

// ListExcludedTags returns all excluded tags sorted alphabetically.
func (m *MongoDB) ListExcludedTags(ctx context.Context) ([]string, error) {
	cur, err := m.db.Collection(excludedTagsCollection).Find(ctx, bson.D{},
		options.Find().SetSort(bson.D{{Key: "tag", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("find excluded tags: %w", err)
	}

	var docs []ExcludedTag
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("decode excluded tags: %w", err)
	}

	tags := make([]string, 0, len(docs))
	for _, d := range docs {
		tags = append(tags, d.Tag)
	}
	return tags, nil
}

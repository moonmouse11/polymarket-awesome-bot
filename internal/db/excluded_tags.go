package db

import (
	"context"
	"fmt"
	"strings"
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

// EnsureExcludedTags creates a unique index on "tag" and seeds the defaults
// only if the collection is empty, so tags edited in the DB survive.
// Tags are stored as given: they must match Polymarket labels exactly.
func (m *MongoDB) EnsureExcludedTags(ctx context.Context, defaults []string) error {
	coll := m.db.Collection(excludedTagsCollection)

	_, err := coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "tag", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return fmt.Errorf("create excluded tags index: %w", err)
	}

	count, err := coll.CountDocuments(ctx, bson.D{})
	if err != nil {
		return fmt.Errorf("count excluded tags: %w", err)
	}
	if count > 0 || len(defaults) == 0 {
		return nil
	}

	now := time.Now().UTC()
	docs := make([]interface{}, 0, len(defaults))
	for _, t := range defaults {
		docs = append(docs, ExcludedTag{Tag: strings.TrimSpace(t), CreatedAt: now})
	}
	if _, err := coll.InsertMany(ctx, docs); err != nil {
		return fmt.Errorf("seed excluded tags: %w", err)
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

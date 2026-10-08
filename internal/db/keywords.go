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

const keywordsCollection = "keywords"

// Keyword is a trigger word used to mark a market as awesome.
type Keyword struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"`
	Word      string             `bson:"word"`
	CreatedAt time.Time          `bson:"created_at"`
}

// EnsureKeywords prepares the keywords collection on startup:
//   - creates a unique index on "word" (no-op if it already exists);
//   - seeds the defaults only if the collection is empty, so words edited
//     in the DB are never overwritten by a restart.
func (m *MongoDB) EnsureKeywords(ctx context.Context, defaults []string) error {
	coll := m.db.Collection(keywordsCollection)

	_, err := coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "word", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return fmt.Errorf("create keywords index: %w", err)
	}

	count, err := coll.CountDocuments(ctx, bson.D{})
	if err != nil {
		return fmt.Errorf("count keywords: %w", err)
	}
	if count > 0 {
		return nil
	}

	now := time.Now().UTC()
	docs := make([]interface{}, 0, len(defaults))
	for _, w := range defaults {
		docs = append(docs, Keyword{Word: strings.ToLower(strings.TrimSpace(w)), CreatedAt: now})
	}
	if len(docs) == 0 {
		return nil
	}

	if _, err := coll.InsertMany(ctx, docs); err != nil {
		return fmt.Errorf("seed keywords: %w", err)
	}
	return nil
}

package db

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

// newTestMongo connects to a real MongoDB as the test user (MONGO_TEST_URI) and
// uses a throwaway database, so the bot's "polymarket" database is never touched.
func newTestMongo(t *testing.T) *MongoDB {
	t.Helper()

	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		t.Skip("MONGO_TEST_URI is not set; see README → Тесты")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	m, err := NewMongoDB(ctx, uri, "polymarket_test")
	if err != nil {
		t.Skipf("MongoDB is not available (%v); start it with: docker compose up -d mongodb", err)
	}

	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = m.db.Drop(ctx)
	}
	cleanup()
	t.Cleanup(func() {
		cleanup()
		_ = m.Close(context.Background())
	})

	return m
}

func storedWords(t *testing.T, m *MongoDB) []string {
	t.Helper()

	cur, err := m.db.Collection(keywordsCollection).Find(context.Background(), bson.D{})
	if err != nil {
		t.Fatalf("find keywords: %v", err)
	}

	var docs []Keyword
	if err := cur.All(context.Background(), &docs); err != nil {
		t.Fatalf("decode keywords: %v", err)
	}

	words := make([]string, 0, len(docs))
	for _, d := range docs {
		words = append(words, d.Word)
	}
	sort.Strings(words)
	return words
}

func TestEnsureKeywords_SeedsEmptyCollection(t *testing.T) {
	m := newTestMongo(t)

	if err := m.EnsureKeywords(context.Background(), []string{"Alien", " ufo "}); err != nil {
		t.Fatalf("EnsureKeywords: %v", err)
	}

	got := storedWords(t, m)
	want := []string{"alien", "ufo"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("stored words = %v, want %v", got, want)
	}
}

func TestEnsureKeywords_DoesNotReseedOnRestart(t *testing.T) {
	m := newTestMongo(t)
	ctx := context.Background()

	if err := m.EnsureKeywords(ctx, []string{"alien", "ufo"}); err != nil {
		t.Fatalf("first EnsureKeywords: %v", err)
	}

	// The user edits the list in the DB: removes "ufo", adds "matrix".
	coll := m.db.Collection(keywordsCollection)
	if _, err := coll.DeleteOne(ctx, bson.M{"word": "ufo"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := coll.InsertOne(ctx, Keyword{Word: "matrix", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Bot restarts: the edits must survive, "ufo" must not come back.
	if err := m.EnsureKeywords(ctx, []string{"alien", "ufo"}); err != nil {
		t.Fatalf("second EnsureKeywords: %v", err)
	}

	got := storedWords(t, m)
	want := []string{"alien", "matrix"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("stored words = %v, want %v", got, want)
	}
}

func TestListKeywords_ReturnsStoredWords(t *testing.T) {
	m := newTestMongo(t)

	if err := m.EnsureKeywords(context.Background(), []string{"alien", "ufo"}); err != nil {
		t.Fatalf("EnsureKeywords: %v", err)
	}

	got, err := m.ListKeywords(context.Background())
	if err != nil {
		t.Fatalf("ListKeywords: %v", err)
	}
	sort.Strings(got)
	want := []string{"alien", "ufo"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ListKeywords = %v, want %v", got, want)
	}
}

func TestEnsureKeywords_RejectsDuplicateWords(t *testing.T) {
	m := newTestMongo(t)
	ctx := context.Background()

	if err := m.EnsureKeywords(ctx, []string{"alien"}); err != nil {
		t.Fatalf("EnsureKeywords: %v", err)
	}

	_, err := m.db.Collection(keywordsCollection).InsertOne(ctx, Keyword{Word: "alien", CreatedAt: time.Now()})
	if err == nil {
		t.Fatal("inserting a duplicate word succeeded, want unique index error")
	}
}

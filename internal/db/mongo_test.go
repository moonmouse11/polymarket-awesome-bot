package db

import (
	"context"
	"os"
	"testing"
	"time"
)

// newTestMongo connects to a real MongoDB as the test user (MONGO_TEST_URI) and
// uses a throwaway database, so the bot's "polymarket" database is never touched.
func newTestMongo(t *testing.T) *MongoDB {
	t.Helper()

	// Locally a missing MongoDB skips these tests; in CI it must fail,
	// otherwise a broken service container would pass as green.
	skip := t.Skipf
	if os.Getenv("CI") != "" {
		skip = t.Fatalf
	}

	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		skip("MONGO_TEST_URI is not set; see README → Тесты")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	m, err := NewMongoDB(ctx, uri, "polymarket_test")
	if err != nil {
		skip("MongoDB is not available (%v); start it with: docker compose up -d mongodb", err)
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

func TestSetup(t *testing.T) {
	skip := t.Skipf
	if os.Getenv("CI") != "" {
		skip = t.Fatalf
	}
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		skip("MONGO_TEST_URI is not set; see README → Тесты")
	}

	m, err := Setup(context.Background(), uri, "polymarket_test_setup")
	if err != nil {
		skip("MongoDB is not available (%v); start it with: docker compose up -d mongodb", err)
	}
	t.Cleanup(func() {
		_ = m.db.Drop(context.Background())
		m.CloseWithTimeout()
	})

	// Setup ensured the market indexes.
	indexes, err := m.db.Collection("markets").Indexes().List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for indexes.Next(context.Background()) {
		var spec struct {
			Name string `bson:"name"`
		}
		if err := indexes.Decode(&spec); err != nil {
			t.Fatal(err)
		}
		names = append(names, spec.Name)
	}
	if len(names) < 2 { // _id plus at least one market index
		t.Errorf("indexes = %v, want more than just _id", names)
	}
}

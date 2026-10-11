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

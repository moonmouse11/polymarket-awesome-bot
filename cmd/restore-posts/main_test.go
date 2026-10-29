package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/db"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/domain"
)

func TestParseExport(t *testing.T) {
	data := []byte(`{"name": "Channel", "messages": [
		{"id": 1, "type": "service", "date_unixtime": "1700000000", "text_entities": []},
		{"id": 2, "type": "message", "date_unixtime": "1700000060", "text_entities": [
			{"type": "bold", "text": "Will aliens land?"},
			{"type": "text_link", "text": "Open on Polymarket", "href": "https://polymarket.com/event/aliens/will-aliens-land"}]},
		{"id": 3, "type": "message", "date_unixtime": "1700000120", "text_entities": [
			{"type": "text_link", "text": "Open on Polymarket", "href": "https://polymarket.com/market/ufo-2027"}]},
		{"id": 4, "type": "message", "date_unixtime": "1700000180", "text_entities": [
			{"type": "text_link", "text": "event only", "href": "https://polymarket.com/event/aliens"},
			{"type": "text_link", "text": "other", "href": "https://example.com/event/a/b"}]},
		{"id": 5, "type": "message", "date_unixtime": "1700000240", "text_entities": [{"type": "plain", "text": "hello"}]}
	]}`)
	posts, err := parseExport(data)
	if err != nil {
		t.Fatalf("parseExport: %v", err)
	}
	want := []post{
		{MessageID: 2, At: time.Unix(1700000060, 0).UTC(), Slug: "will-aliens-land"},
		{MessageID: 3, At: time.Unix(1700000120, 0).UTC(), Slug: "ufo-2027"},
	}
	if len(posts) != len(want) {
		t.Fatalf("posts = %+v, want %+v", posts, want)
	}
	for i := range want {
		if posts[i] != want[i] {
			t.Errorf("posts[%d] = %+v, want %+v", i, posts[i], want[i])
		}
	}
}

func TestParseExport_BadJSON(t *testing.T) {
	if _, err := parseExport([]byte(`not json`)); err == nil {
		t.Fatal("want error")
	}
}

func TestParseExport_BadDate(t *testing.T) {
	data := []byte(`{"messages": [{"id": 7, "type": "message", "date_unixtime": "yesterday"}]}`)
	if _, err := parseExport(data); err == nil || !strings.Contains(err.Error(), "message 7") {
		t.Fatalf("err = %v, want bad date of message 7", err)
	}
}

// fakeStore knows the slugs in known; a slug in fail returns an error.
type fakeStore struct {
	known    map[string]bool
	fail     string
	restored []int
}

func (f *fakeStore) RestoreNotified(_ context.Context, slug string, messageID int, _ time.Time) (bool, error) {
	if slug == f.fail {
		return false, errors.New("mongo down")
	}
	if !f.known[slug] {
		return false, nil
	}
	f.restored = append(f.restored, messageID)
	return true, nil
}

func TestRestore_CountsFoundAndSkipsUnknown(t *testing.T) {
	s := &fakeStore{known: map[string]bool{"a": true, "c": true}}
	posts := []post{{MessageID: 1, Slug: "a"}, {MessageID: 2, Slug: "b"}, {MessageID: 3, Slug: "c"}}
	err := restore(context.Background(), zerolog.Nop(), s, posts)
	if err != nil || len(s.restored) != 2 || s.restored[0] != 1 || s.restored[1] != 3 {
		t.Errorf("restored %v, err %v; want [1 3], nil", s.restored, err)
	}
}

func TestRestore_StopsOnError(t *testing.T) {
	s := &fakeStore{known: map[string]bool{"a": true, "c": true}, fail: "b"}
	posts := []post{{MessageID: 1, Slug: "a"}, {MessageID: 2, Slug: "b"}, {MessageID: 3, Slug: "c"}}
	err := restore(context.Background(), zerolog.Nop(), s, posts)
	if err == nil || len(s.restored) != 1 {
		t.Errorf("restored %v, err %v; want [1] and the error", s.restored, err)
	}
}

func TestRun_FailsBeforeTouchingTheDatabase(t *testing.T) {
	t.Setenv("MONGO_URI", "")
	dir := t.TempDir()
	write := func(name, data string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	withPost := write("posts.json", `{"messages": [{"id": 1, "type": "message", "date_unixtime": "1700000000",
		"text_entities": [{"type": "text_link", "href": "https://polymarket.com/market/ufo"}]}]}`)

	for name, tc := range map[string]struct{ path, want string }{
		"missing file": {filepath.Join(dir, "nope.json"), "no such file"},
		"bad json":     {write("bad.json", "not json"), "parse export"},
		"no posts":     {write("empty.json", `{"messages": []}`), "no market links"},
		"no mongo uri": {withPost, "MONGO_URI"},
	} {
		if err := run(context.Background(), zerolog.Nop(), tc.path, "polymarket_test"); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}

// The whole command against the test database (MONGO_TEST_URI, as in internal/db).
func TestRun_RestoresPostsInMongo(t *testing.T) {
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("MONGO_TEST_URI is not set")
		}
		t.Skip("MONGO_TEST_URI is not set")
	}
	t.Setenv("MONGO_URI", uri)
	ctx := context.Background()

	m, err := db.NewMongoDB(ctx, uri, "polymarket_test")
	if err != nil {
		t.Fatalf("NewMongoDB: %v", err)
	}
	t.Cleanup(func() { _ = m.Close(context.Background()) })
	if _, err := m.UpsertMarkets(ctx, []domain.Market{{ID: "ufo-id", Slug: "ufo-2027"}}, time.Now()); err != nil {
		t.Fatalf("UpsertMarkets: %v", err)
	}

	path := filepath.Join(t.TempDir(), "result.json")
	export := `{"messages": [
		{"id": 1, "type": "message", "date_unixtime": "1700000000", "text_entities": [{"type": "text_link", "href": "https://polymarket.com/market/ufo-2027"}]},
		{"id": 2, "type": "message", "date_unixtime": "1700000060", "text_entities": [{"type": "text_link", "href": "https://polymarket.com/market/unknown"}]}
	]}`
	if err := os.WriteFile(path, []byte(export), 0o600); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	if err := run(ctx, zerolog.New(&logs), path, "polymarket_test"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := `"posts":2,"restored":1,"unmatched":1`; !strings.Contains(logs.String(), want) {
		t.Errorf("summary lacks %s:\n%s", want, logs.String())
	}
}

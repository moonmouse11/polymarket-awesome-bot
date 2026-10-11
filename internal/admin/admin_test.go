package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/rs/zerolog"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/db"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/health"
)

type fakeStore struct {
	stats   db.Stats
	err     error
	pingErr error
}

func (f *fakeStore) Stats(context.Context, time.Time) (db.Stats, error) { return f.stats, f.err }
func (f *fakeStore) Ping(context.Context) error                         { return f.pingErr }

type fakeAlerts []string

func (f fakeAlerts) Active() []string { return f }

func TestParseAdminIDs(t *testing.T) {
	ids, err := ParseAdminIDs(" 123, 456 ,")
	if err != nil || len(ids) != 2 || ids[0] != 123 || ids[1] != 456 {
		t.Errorf("ParseAdminIDs = %v, %v; want [123 456]", ids, err)
	}
	if ids, err := ParseAdminIDs(""); err != nil || len(ids) != 0 {
		t.Errorf("empty: %v, %v; want none", ids, err)
	}
	if _, err := ParseAdminIDs("123,@me"); err == nil {
		t.Error("invalid id accepted")
	}
}

func TestCommand(t *testing.T) {
	for in, want := range map[string]string{
		"/status":                  "/status",
		"/Status@polymarket_bot x": "/status",
		"hello":                    "",
		"":                         "",
	} {
		if got := command(in); got != want {
			t.Errorf("command(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNum(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1 000", 1204330: "1 204 330", -1500: "-1 500"} {
		if got := num(n); got != want {
			t.Errorf("num(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestStatus(t *testing.T) {
	reg := health.NewRegistry()
	reg.Cycle(1200, 78*time.Second, false, nil)
	store := &fakeStore{stats: db.Stats{PendingDeletes: 589, PendingSends: 2, PendingEdits: 6412, Awesome: 133372, AwesomeOpen: 8136,
		Updated: 151203, Created: 25120, BecameAwesome: 341, Announced: 338}}
	got := New(nil, store, reg, zerolog.Nop()).Answer(context.Background(), "/status")

	for _, want := range []string{
		"To delete: 589 · To send: 2 · To edit: 6 412",
		"Total: 133 372 · Open: 8 136",
		"Markets updated: 151 203\nNew markets: 25 120\nBecame awesome: 341 · Announced: 338",
		"Updates from API: 1 200 in 1 cycles (since start",
		"Last cycle: 0 s ago, took 1 min",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("/status lacks %q:\n%s", want, got)
		}
	}

	store.err = errors.New("server selection timeout")
	if got := New(nil, store, reg, zerolog.Nop()).Answer(context.Background(), "/status"); !strings.Contains(got, "🔴 MongoDB: server selection timeout") {
		t.Errorf("/status with MongoDB down:\n%s", got)
	}
}

func TestHealthCheck(t *testing.T) {
	reg := health.NewRegistry()
	reg.Cycle(10, time.Second, false, errors.New("fetch updated markets: <dns> failure"))
	reg.RateLimited(11 * time.Minute)
	c := New(nil, &fakeStore{}, reg, zerolog.Nop()).WithAlerts(fakeAlerts{"Telegram rate limit since …"})
	got := c.Answer(context.Background(), "/health_check")

	for _, want := range []string{
		"🟢 MongoDB · ping",
		"🔴 Updater · failing (1 in a row)",
		"errors since start: 1",
		"fetch updated markets: &lt;dns&gt; failure", // escaped HTML
		"⚪ Telegram · no data yet",
		"429: 1 since start",
		"penalty 11 min",
		"🔴 Telegram rate limit since …",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("/health_check lacks %q:\n%s", want, got)
		}
	}
}

func TestHandle_AnswersOnlyAdminsInPrivate(t *testing.T) {
	var replies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		replies = append(replies, r.FormValue("chat_id"))
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"date":0,"chat":{"id":1,"type":"private"}}}`))
	}))
	defer srv.Close()
	b, err := bot.New("1:token", bot.WithSkipGetMe(), bot.WithServerURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	c := New([]int64{42}, &fakeStore{}, health.NewRegistry(), zerolog.Nop())
	msg := func(from int64, chat models.ChatType, text string) *models.Update {
		return &models.Update{Message: &models.Message{
			From: &models.User{ID: from}, Chat: models.Chat{ID: from, Type: chat}, Text: text,
		}}
	}

	ctx := context.Background()
	c.Handle(ctx, b, msg(7, models.ChatTypePrivate, "/status"))    // stranger
	c.Handle(ctx, b, msg(7, models.ChatTypePrivate, "/start"))     // stranger: only logged
	c.Handle(ctx, b, msg(42, models.ChatTypeGroup, "/status"))     // admin, but in a group
	c.Handle(ctx, b, msg(42, models.ChatTypePrivate, "just text")) // not a command
	c.Handle(ctx, b, msg(42, models.ChatTypePrivate, "/status"))   // the only reply
	if len(replies) != 1 || replies[0] != "42" {
		t.Errorf("replies to chats %v, want only [42]", replies)
	}
}

type fakeSender struct {
	failFor map[int64]bool
	sent    []int64
}

func (f *fakeSender) SendTo(_ context.Context, chatID int64, _ string) error {
	if f.failFor[chatID] {
		return errors.New("forbidden: bot was never started")
	}
	f.sent = append(f.sent, chatID)
	return nil
}

func TestAlerter(t *testing.T) {
	ctx := context.Background()
	s := &fakeSender{failFor: map[int64]bool{2: true}}
	if err := NewAlerter(s, []int64{1, 2}).Alert(ctx, "x"); err != nil || len(s.sent) != 1 {
		t.Errorf("one admin reachable: err=%v sent=%v; want delivered to 1", err, s.sent)
	}
	if err := NewAlerter(s, []int64{2}).Alert(ctx, "x"); err == nil {
		t.Error("no admin reachable: want an error so the monitor retries")
	}
}

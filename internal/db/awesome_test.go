package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/polymarket"
)

// awesomeState is what MarkAwesome writes, read back from a market document.
type awesomeState struct {
	IsAwesome  bool       `bson:"is_awesome"`
	Reason     string     `bson:"awesome_reason"`
	Words      []string   `bson:"awesome_words"`
	ExcludedBy *string    `bson:"awesome_excluded_tag"`
	CheckedAt  *time.Time `bson:"awesome_checked_at"`
	Question   string     `bson:"question"`
	LastTrade  float64    `bson:"last_trade_price"`
}

func readAwesome(t *testing.T, m *MongoDB, id string) awesomeState {
	t.Helper()
	var got awesomeState
	err := m.db.Collection(marketsCollection).FindOne(context.Background(), bson.D{{Key: "_id", Value: id}}).Decode(&got)
	if err != nil {
		t.Fatalf("find market %s: %v", id, err)
	}
	return got
}

func TestMarkAwesome_ExcludedTags(t *testing.T) {
	m := newTestMongo(t)
	ctx := context.Background()

	if _, err := m.UpsertMarkets(ctx, []polymarket.Market{
		{ID: "sport", Tags: []string{"Sports", "Soccer"}},
		{ID: "elections", Tags: []string{"Politics", "Elections"}},
		{ID: "culture", Tags: []string{"Culture", "Movies"}},
		{ID: "notags"},
		{ID: "case", Tags: []string{"sports"}}, // tags match exactly, so this is awesome
	}, time.Now()); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	matched, err := m.MarkAwesome(ctx, []string{"Elections", "Sports"}, nil)
	if err != nil {
		t.Fatalf("MarkAwesome: %v", err)
	}
	if matched != 5 {
		t.Fatalf("matched = %d, want 5", matched)
	}

	for id, wantAwesome := range map[string]bool{"sport": false, "elections": false, "culture": true, "notags": true, "case": true} {
		got := readAwesome(t, m, id)
		if got.IsAwesome != wantAwesome {
			t.Errorf("%s: is_awesome = %v, want %v", id, got.IsAwesome, wantAwesome)
		}
		if wantReason := map[bool]string{true: ReasonTags, false: ReasonExcludedTag}[wantAwesome]; got.Reason != wantReason {
			t.Errorf("%s: awesome_reason = %q, want %q", id, got.Reason, wantReason)
		}
		if wantAwesome && got.ExcludedBy != nil {
			t.Errorf("%s: awesome market has awesome_excluded_tag %q", id, *got.ExcludedBy)
		}
		if got.CheckedAt == nil {
			t.Errorf("%s: awesome_checked_at is not set", id)
		}
	}
	if got := readAwesome(t, m, "sport"); got.ExcludedBy == nil || *got.ExcludedBy != "Sports" {
		t.Errorf("sport: awesome_excluded_tag = %v, want Sports", got.ExcludedBy)
	}

	// Removing a tag from the list and recomputing flips the market back.
	if _, err := m.MarkAwesome(ctx, []string{"Sports"}, nil); err != nil {
		t.Fatalf("second MarkAwesome: %v", err)
	}
	if got := readAwesome(t, m, "elections"); !got.IsAwesome || got.ExcludedBy != nil {
		t.Errorf("elections after recompute: %+v, want awesome without excluded tag", got)
	}

	total, open, err := m.CountAwesome(ctx)
	if err != nil {
		t.Fatalf("CountAwesome: %v", err)
	}
	if total != 4 || open != 4 {
		t.Errorf("CountAwesome = %d/%d, want 4/4", total, open)
	}
}

func TestMarkAwesome_KeywordsOverrideExcludedTags(t *testing.T) {
	m := newTestMongo(t)
	ctx := context.Background()

	if _, err := m.UpsertMarkets(ctx, []polymarket.Market{
		// Excluded tag, keyword in a different field each time (substring, any case).
		{ID: "q", Question: "Will LeBron be ARRESTED?", Tags: []string{"Sports"}},
		{ID: "event", EventTitle: "Zombies vs Aliens", Tags: []string{"Sports"}},
		{ID: "desc", Description: "Resolves if a UFO is seen.", Tags: []string{"Sports"}},
		{ID: "tag", Tags: []string{"Sports", "Conspiracy"}},
		// Excluded tag, no keyword.
		{ID: "boring", Question: "O/U 2.5 goals?", Tags: []string{"Sports"}},
		// Awesome by tags; found words are still recorded.
		{ID: "free", Question: "Alien contact?", Tags: []string{"Science"}},
	}, time.Now()); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if _, err := m.MarkAwesome(ctx, []string{"Sports"}, []string{"arrest", "alien", "ufo", "conspiracy", " ", "Zombie"}); err != nil {
		t.Fatalf("MarkAwesome: %v", err)
	}

	tests := []struct {
		id      string
		awesome bool
		reason  string
		words   []string
	}{
		{"q", true, ReasonWords, []string{"arrest"}},
		{"event", true, ReasonWords, []string{"alien", "zombie"}},
		{"desc", true, ReasonWords, []string{"ufo"}},
		{"tag", true, ReasonWords, []string{"conspiracy"}},
		{"boring", false, ReasonExcludedTag, nil},
		{"free", true, ReasonTags, []string{"alien"}},
	}
	for _, tt := range tests {
		got := readAwesome(t, m, tt.id)
		if got.IsAwesome != tt.awesome || got.Reason != tt.reason {
			t.Errorf("%s: is_awesome=%v reason=%q, want %v %q", tt.id, got.IsAwesome, got.Reason, tt.awesome, tt.reason)
		}
		if strings.Join(got.Words, ",") != strings.Join(tt.words, ",") {
			t.Errorf("%s: words = %v, want %v", tt.id, got.Words, tt.words)
		}
	}
	// The overridden excluded tag stays visible.
	if got := readAwesome(t, m, "q"); got.ExcludedBy == nil || *got.ExcludedBy != "Sports" {
		t.Errorf("q: awesome_excluded_tag = %v, want Sports", got.ExcludedBy)
	}
}

func TestUpsertMarkets_KeepsAwesomeFields(t *testing.T) {
	m := newTestMongo(t)
	ctx := context.Background()

	if _, err := m.UpsertMarkets(ctx, []polymarket.Market{{ID: "1", Question: "Aliens?"}}, time.Now()); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if _, err := m.MarkAwesome(ctx, nil, nil); err != nil {
		t.Fatalf("MarkAwesome: %v", err)
	}

	// A reload brings fresh API data; the awesome flag must survive.
	if _, err := m.UpsertMarkets(ctx, []polymarket.Market{{ID: "1", Question: "Aliens? (edited)", LastTradePrice: 0.7}}, time.Now()); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got := readAwesome(t, m, "1")
	if !got.IsAwesome || got.CheckedAt == nil {
		t.Errorf("awesome fields lost after reload: %+v", got)
	}
	if got.Question != "Aliens? (edited)" || got.LastTrade != 0.7 {
		t.Errorf("API fields not updated: %+v", got)
	}
}

func TestEnsureExcludedTags_SeedsOnlyEmpty(t *testing.T) {
	m := newTestMongo(t)
	ctx := context.Background()

	if err := m.EnsureExcludedTags(ctx, []string{"Sports", " Weather "}); err != nil {
		t.Fatalf("EnsureExcludedTags: %v", err)
	}
	// Second start with other defaults: the stored list must not change.
	if err := m.EnsureExcludedTags(ctx, []string{"Crypto"}); err != nil {
		t.Fatalf("second EnsureExcludedTags: %v", err)
	}

	got, err := m.ListExcludedTags(ctx)
	if err != nil {
		t.Fatalf("ListExcludedTags: %v", err)
	}
	if len(got) != 2 || got[0] != "Sports" || got[1] != "Weather" {
		t.Fatalf("ListExcludedTags = %v, want [Sports Weather]", got)
	}
}

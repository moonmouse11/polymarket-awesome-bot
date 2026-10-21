package db

import (
	"context"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/awesome"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/polymarket"
)

// awesomeState is what MarkAwesome writes, read back from a market document.
type awesomeState struct {
	IsAwesome bool             `bson:"is_awesome"`
	Reason    string           `bson:"awesome_reason"`
	Matches   *awesome.Matches `bson:"awesome_matches"`
	CheckedAt *time.Time       `bson:"awesome_checked_at"`
	Question  string           `bson:"question"`
	LastTrade float64          `bson:"last_trade_price"`
	// Replaced by awesome_matches; must be removed by a recompute.
	LegacyWords *[]string `bson:"awesome_words"`
	LegacyTag   *string   `bson:"awesome_excluded_tag"`
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

func TestMarkAwesome_FourListsAllowWins(t *testing.T) {
	m := newTestMongo(t)
	ctx := context.Background()

	if _, err := m.UpsertMarkets(ctx, []polymarket.Market{
		{ID: "sport", Question: "O/U 2.5 goals?", Tags: []string{"Sports", "Soccer"}},
		{ID: "case", Tags: []string{"sports"}}, // tags match exactly
		{ID: "culture", Question: "Best picture?", Tags: []string{"Culture"}},
		{ID: "ai-sport", Tags: []string{"Sports", "AI"}},                                            // allow tag beats deny tag
		{ID: "alien-sport", Question: "Aliens at the Super Bowl?", Tags: []string{"Sports"}},        // allow word beats deny tag
		{ID: "valiente", Question: "Joaquín Valiente: Anytime Goalscorer", Tags: []string{"Games"}}, // "alien" inside a word: no
		{ID: "musk", EventTitle: "ELON MUSK tweets", Tags: []string{"Elections"}},                   // phrase, any case, event title
		{ID: "desc", Description: "Resolves Yes if (alien) life is found.", Tags: []string{"Sports"}},
		{ID: "cpp", Question: "Is C++ dead?", Tags: []string{"Sports"}}, // regex characters are literal
		{ID: "both-words", Question: "Alien goalscorer?"},               // allow word beats deny word
	}, time.Now()); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// A market computed by the old pipeline: its fields must go.
	if _, err := m.db.Collection(marketsCollection).UpdateOne(ctx, bson.D{{Key: "_id", Value: "sport"}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "awesome_words", Value: bson.A{"x"}}, {Key: "awesome_excluded_tag", Value: "Sports"}}}}); err != nil {
		t.Fatalf("seed legacy fields: %v", err)
	}

	rules := FilterRules{
		AllowTags:  []string{"AI"},
		DenyTags:   []string{"Elections", "Sports"},
		AllowWords: []string{"alien", "c++", "elon musk", " "},
		DenyWords:  []string{"goalscorer"},
	}
	matched, err := m.MarkAwesome(ctx, rules)
	if err != nil {
		t.Fatalf("MarkAwesome: %v", err)
	}
	if matched != 10 {
		t.Fatalf("matched = %d, want 10", matched)
	}

	tests := []struct {
		id      string
		reason  string
		matches *awesome.Matches
	}{
		{"sport", awesome.ReasonDeny, &awesome.Matches{DenyTags: []string{"Sports"}}},
		{"case", awesome.ReasonDefault, nil},
		{"culture", awesome.ReasonDefault, nil},
		{"ai-sport", awesome.ReasonAllow, &awesome.Matches{AllowTags: []string{"AI"}, DenyTags: []string{"Sports"}}},
		{"alien-sport", awesome.ReasonAllow, &awesome.Matches{DenyTags: []string{"Sports"}, AllowWords: []string{"alien"}}},
		{"valiente", awesome.ReasonDeny, &awesome.Matches{DenyWords: []string{"goalscorer"}}},
		{"musk", awesome.ReasonAllow, &awesome.Matches{DenyTags: []string{"Elections"}, AllowWords: []string{"elon musk"}}},
		{"desc", awesome.ReasonAllow, &awesome.Matches{DenyTags: []string{"Sports"}, AllowWords: []string{"alien"}}},
		{"cpp", awesome.ReasonAllow, &awesome.Matches{DenyTags: []string{"Sports"}, AllowWords: []string{"c++"}}},
		{"both-words", awesome.ReasonAllow, &awesome.Matches{AllowWords: []string{"alien"}, DenyWords: []string{"goalscorer"}}},
	}
	for _, tt := range tests {
		got := readAwesome(t, m, tt.id)
		if got.Reason != tt.reason || got.IsAwesome != (tt.reason != awesome.ReasonDeny) {
			t.Errorf("%s: reason=%q is_awesome=%v, want %q", tt.id, got.Reason, got.IsAwesome, tt.reason)
		}
		if !reflect.DeepEqual(got.Matches, tt.matches) {
			t.Errorf("%s: matches = %+v, want %+v", tt.id, got.Matches, tt.matches)
		}
		if got.CheckedAt == nil {
			t.Errorf("%s: awesome_checked_at is not set", tt.id)
		}
	}
	if got := readAwesome(t, m, "sport"); got.LegacyWords != nil || got.LegacyTag != nil {
		t.Errorf("sport: legacy fields kept: %v %v", got.LegacyWords, got.LegacyTag)
	}

	// Dropping the deny tag flips the market back and clears its matches.
	rules.DenyTags = []string{"Elections"}
	if _, err := m.MarkAwesome(ctx, rules); err != nil {
		t.Fatalf("second MarkAwesome: %v", err)
	}
	if got := readAwesome(t, m, "sport"); !got.IsAwesome || got.Reason != awesome.ReasonDefault || got.Matches != nil {
		t.Errorf("sport after recompute: %+v, want awesome by default without matches", got)
	}

	total, open, err := m.CountAwesome(ctx)
	if err != nil {
		t.Fatalf("CountAwesome: %v", err)
	}
	if total != 9 || open != 9 {
		t.Errorf("CountAwesome = %d/%d, want 9/9 (only valiente is denied)", total, open)
	}
}

func TestUpsertMarkets_KeepsAwesomeFields(t *testing.T) {
	m := newTestMongo(t)
	ctx := context.Background()

	if _, err := m.UpsertMarkets(ctx, []polymarket.Market{{ID: "1", Question: "Aliens?"}}, time.Now()); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if _, err := m.MarkAwesome(ctx, FilterRules{}); err != nil {
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

func TestMarkAwesome_RuleValueIsLiteral(t *testing.T) {
	m := newTestMongo(t)
	ctx := context.Background()
	if _, err := m.UpsertMarkets(ctx, []polymarket.Market{{ID: "1", Question: "Will gas cost $5?"}}, time.Now()); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// A value starting with "$" would read as a field path without $literal.
	if _, err := m.MarkAwesome(ctx, FilterRules{DenyWords: []string{"$5"}}); err != nil {
		t.Fatalf("MarkAwesome: %v", err)
	}
	got := readAwesome(t, m, "1")
	if got.IsAwesome || got.Matches == nil || !reflect.DeepEqual(got.Matches.DenyWords, []string{"$5"}) {
		t.Errorf("market = %+v, want denied by \"$5\"", got)
	}
}

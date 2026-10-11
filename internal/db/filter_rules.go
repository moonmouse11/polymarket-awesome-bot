package db

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	filterRulesCollection = "filter_rules"
	filterLogCollection   = "filter_rules_log" // append-only history of edits
)

// Rule kinds and actions.
const (
	KindTag     = "tag"  // a Polymarket tag, matched exactly (case-sensitive)
	KindWord    = "word" // a lowercase word looked up in the market's text
	ActionAllow = "allow"
	ActionDeny  = "deny"
)

var (
	// ErrRuleExists: the same rule is already stored.
	ErrRuleExists = errors.New("rule already exists")
	// ErrRuleConflict: the value is already in the opposite list.
	ErrRuleConflict = errors.New("value is already in the opposite list")
	// ErrRuleNotFound: nothing to remove.
	ErrRuleNotFound = errors.New("no such rule")
)

// FilterRule decides whether a market is awesome. A value is in at most one
// list: (kind, value) is unique.
type FilterRule struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"`
	Kind      string             `bson:"kind"`
	Action    string             `bson:"action"`
	Value     string             `bson:"value"`
	CreatedAt time.Time          `bson:"created_at"`
	CreatedBy string             `bson:"created_by,omitempty"`
}

// RuleChange is one entry of the edit history.
type RuleChange struct {
	At     time.Time `bson:"at"`
	Op     string    `bson:"op"` // "add" or "remove"
	Kind   string    `bson:"kind"`
	Action string    `bson:"action"`
	Value  string    `bson:"value"`
	By     string    `bson:"by,omitempty"`
}

// FilterRules are the four lists, each sorted.
type FilterRules struct {
	AllowTags, DenyTags, AllowWords, DenyWords []string
}

// Empty reports whether nothing would be filtered out: with no deny rules
// every market is awesome.
func (r FilterRules) Empty() bool { return len(r.DenyTags) == 0 && len(r.DenyWords) == 0 }

// NormalizeRule validates kind and action and cleans the value: tags are
// trimmed, words are trimmed and lowercased. action "" is allowed (remove).
func NormalizeRule(kind, action, value string) (string, error) {
	if kind != KindTag && kind != KindWord {
		return "", fmt.Errorf("kind must be %q or %q, got %q", KindTag, KindWord, kind)
	}
	if action != "" && action != ActionAllow && action != ActionDeny {
		return "", fmt.Errorf("action must be %q or %q, got %q", ActionAllow, ActionDeny, action)
	}
	value = strings.TrimSpace(value)
	if kind == KindWord {
		value = strings.ToLower(value)
	}
	if value == "" {
		return "", errors.New("value is empty")
	}
	return value, nil
}

// EnsureFilterRules creates the unique (kind, value) index, which also keeps
// a value out of both lists.
func (m *MongoDB) EnsureFilterRules(ctx context.Context) error {
	_, err := m.db.Collection(filterRulesCollection).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "kind", Value: 1}, {Key: "value", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return fmt.Errorf("create filter rules index: %w", err)
	}
	return nil
}

// FilterRules reads all rules.
func (m *MongoDB) FilterRules(ctx context.Context) (FilterRules, error) {
	var rules FilterRules
	cur, err := m.db.Collection(filterRulesCollection).Find(ctx, bson.D{})
	if err != nil {
		return rules, fmt.Errorf("find filter rules: %w", err)
	}
	var docs []FilterRule
	if err := cur.All(ctx, &docs); err != nil {
		return rules, fmt.Errorf("decode filter rules: %w", err)
	}
	for _, d := range docs {
		switch {
		case d.Kind == KindTag && d.Action == ActionAllow:
			rules.AllowTags = append(rules.AllowTags, d.Value)
		case d.Kind == KindTag && d.Action == ActionDeny:
			rules.DenyTags = append(rules.DenyTags, d.Value)
		case d.Kind == KindWord && d.Action == ActionAllow:
			rules.AllowWords = append(rules.AllowWords, d.Value)
		case d.Kind == KindWord && d.Action == ActionDeny:
			rules.DenyWords = append(rules.DenyWords, d.Value)
		}
	}
	for _, l := range [][]string{rules.AllowTags, rules.DenyTags, rules.AllowWords, rules.DenyWords} {
		sort.Strings(l)
	}
	return rules, nil
}

// AddFilterRule stores a rule and logs it. The value is normalized first.
// It fails with ErrRuleExists or ErrRuleConflict (wrapped with the stored
// action) when the value is already listed.
func (m *MongoDB) AddFilterRule(ctx context.Context, kind, action, value, by string) (FilterRule, error) {
	value, err := NormalizeRule(kind, action, value)
	if err != nil {
		return FilterRule{}, err
	}
	if action == "" {
		return FilterRule{}, errors.New("action is required")
	}
	rule := FilterRule{Kind: kind, Action: action, Value: value, CreatedAt: time.Now().UTC(), CreatedBy: by}

	_, err = m.db.Collection(filterRulesCollection).InsertOne(ctx, rule)
	if mongo.IsDuplicateKeyError(err) {
		stored, findErr := m.findRule(ctx, kind, value)
		if findErr != nil {
			return FilterRule{}, findErr
		}
		if stored.Action == action {
			return stored, fmt.Errorf("%s %s %q: %w", action, kind, value, ErrRuleExists)
		}
		return stored, fmt.Errorf("%s %q is in the %s list; remove it first: %w", kind, value, stored.Action, ErrRuleConflict)
	}
	if err != nil {
		return FilterRule{}, fmt.Errorf("insert filter rule: %w", err)
	}
	return rule, m.logRule(ctx, "add", rule, by)
}

// RemoveFilterRule deletes the rule for (kind, value) from whichever list
// holds it and logs it.
func (m *MongoDB) RemoveFilterRule(ctx context.Context, kind, value, by string) (FilterRule, error) {
	value, err := NormalizeRule(kind, "", value)
	if err != nil {
		return FilterRule{}, err
	}
	var rule FilterRule
	err = m.db.Collection(filterRulesCollection).
		FindOneAndDelete(ctx, bson.D{{Key: "kind", Value: kind}, {Key: "value", Value: value}}).Decode(&rule)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return FilterRule{}, fmt.Errorf("%s %q: %w", kind, value, ErrRuleNotFound)
	}
	if err != nil {
		return FilterRule{}, fmt.Errorf("delete filter rule: %w", err)
	}
	return rule, m.logRule(ctx, "remove", rule, by)
}

// FilterRuleHistory returns the last n edits, newest first.
func (m *MongoDB) FilterRuleHistory(ctx context.Context, n int64) ([]RuleChange, error) {
	cur, err := m.db.Collection(filterLogCollection).Find(ctx, bson.D{},
		options.Find().SetSort(bson.D{{Key: "at", Value: -1}, {Key: "_id", Value: -1}}).SetLimit(n))
	if err != nil {
		return nil, fmt.Errorf("find filter rule history: %w", err)
	}
	var changes []RuleChange
	if err := cur.All(ctx, &changes); err != nil {
		return nil, fmt.Errorf("decode filter rule history: %w", err)
	}
	return changes, nil
}

// TagInUse reports whether any market has the tag, to catch typos: tags are
// case-sensitive. A miss scans the whole collection (seconds).
func (m *MongoDB) TagInUse(ctx context.Context, tag string) (bool, error) {
	err := m.db.Collection(marketsCollection).
		FindOne(ctx, bson.D{{Key: "tags", Value: tag}}, options.FindOne().SetProjection(bson.D{{Key: "_id", Value: 1}})).Err()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("find market by tag: %w", err)
	}
	return true, nil
}

func (m *MongoDB) findRule(ctx context.Context, kind, value string) (FilterRule, error) {
	var rule FilterRule
	err := m.db.Collection(filterRulesCollection).
		FindOne(ctx, bson.D{{Key: "kind", Value: kind}, {Key: "value", Value: value}}).Decode(&rule)
	if err != nil {
		return rule, fmt.Errorf("find filter rule: %w", err)
	}
	return rule, nil
}

func (m *MongoDB) logRule(ctx context.Context, op string, r FilterRule, by string) error {
	_, err := m.db.Collection(filterLogCollection).InsertOne(ctx, RuleChange{
		At: time.Now().UTC(), Op: op, Kind: r.Kind, Action: r.Action, Value: r.Value, By: by,
	})
	if err != nil {
		return fmt.Errorf("log filter rule change: %w", err)
	}
	return nil
}

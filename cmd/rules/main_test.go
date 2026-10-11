package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/db"
)

type fakeStore struct {
	rules   db.FilterRules
	addErr  error
	tagUsed bool
	by      string
}

func (f *fakeStore) FilterRules(context.Context) (db.FilterRules, error) { return f.rules, nil }
func (f *fakeStore) AddFilterRule(_ context.Context, kind, action, value, by string) (db.FilterRule, error) {
	f.by = by
	if f.addErr != nil {
		return db.FilterRule{}, f.addErr
	}
	f.rules.DenyTags = append(f.rules.DenyTags, value)
	return db.FilterRule{Kind: kind, Action: action, Value: value}, nil
}
func (f *fakeStore) RemoveFilterRule(_ context.Context, kind, value, _ string) (db.FilterRule, error) {
	return db.FilterRule{Kind: kind, Action: db.ActionDeny, Value: value}, nil
}
func (f *fakeStore) FilterRuleHistory(context.Context, int64) ([]db.RuleChange, error) {
	return nil, nil
}
func (f *fakeStore) TagInUse(context.Context, string) (bool, error) { return f.tagUsed, nil }

func TestRun_AddShowsRulesAndWarnsAboutUnknownTag(t *testing.T) {
	s := &fakeStore{}
	out, err := run(context.Background(), s, []string{"add", "tag", "deny", "soccer"}, "pots")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{
		`added: deny tag "soccer"`,
		`WARNING: no market has the tag "soccer"`,
		"deny tags:   soccer",
		"allow words: (none)",
		"run make markets-awesome",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if s.by != "pots" {
		t.Errorf("by = %q, want pots", s.by)
	}
}

func TestRun_ConflictIsAnError(t *testing.T) {
	s := &fakeStore{addErr: errors.New(`word "goalscorer" is in the deny list; remove it first`)}
	out, err := run(context.Background(), s, []string{"add", "word", "allow", "goalscorer"}, "")
	if err == nil || !strings.Contains(err.Error(), "deny list") || out != "" {
		t.Errorf("err = %v, output %q; want the conflict and no output", err, out)
	}
}

func TestRun_ListWarnsWithoutDenyRules(t *testing.T) {
	out, err := run(context.Background(), &fakeStore{}, []string{"list"}, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "market updates are paused") || strings.Contains(out, "markets-awesome") {
		t.Errorf("list output:\n%s", out)
	}
}

func TestRun_BadArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"add", "tag", "deny"}, {"drop"}, {"history", "-1"}} {
		if _, err := run(context.Background(), &fakeStore{}, args, ""); err == nil {
			t.Errorf("run(%q) succeeded, want an error", args)
		}
	}
}

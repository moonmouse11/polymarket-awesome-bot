package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/db"
)

type fakeStore struct {
	rules   db.FilterRules
	addErr  error
	tagUsed bool
	by      string
	history []db.RuleChange
	histErr error
	histN   int64
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
func (f *fakeStore) FilterRuleHistory(_ context.Context, n int64) ([]db.RuleChange, error) {
	f.histN = n
	return f.history, f.histErr
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

func TestRun_History(t *testing.T) {
	s := &fakeStore{history: []db.RuleChange{{
		At: time.Date(2026, 10, 9, 14, 5, 0, 0, time.UTC), Op: "add", Action: db.ActionDeny, Kind: db.KindTag, Value: "Sports", By: "pots",
	}}}
	out, err := run(context.Background(), s, []string{"history", "5"}, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := `2026-10-09 14:05  add    deny  tag  "Sports"  pots`; !strings.Contains(out, want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}
	if s.histN != 5 {
		t.Errorf("n = %d, want 5", s.histN)
	}
}

func TestRun_HistoryEmptyUsesDefault(t *testing.T) {
	s := &fakeStore{}
	out, err := run(context.Background(), s, []string{"history"}, "")
	if err != nil || out != "no changes yet\n" || s.histN != 20 {
		t.Errorf("out %q, err %v, n %d; want \"no changes yet\", nil, 20", out, err, s.histN)
	}
}

func TestRun_HistoryError(t *testing.T) {
	s := &fakeStore{histErr: errors.New("mongo down")}
	if out, err := run(context.Background(), s, []string{"history"}, ""); err == nil || out != "" {
		t.Errorf("out %q, err %v; want the error and no output", out, err)
	}
}

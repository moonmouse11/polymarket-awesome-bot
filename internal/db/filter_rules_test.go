package db

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestFilterRules_AddListRemoveAndHistory(t *testing.T) {
	m := newTestMongo(t)
	ctx := context.Background()
	if err := m.EnsureFilterRules(ctx); err != nil {
		t.Fatalf("EnsureFilterRules: %v", err)
	}

	if rules, err := m.FilterRules(ctx); err != nil || !rules.Empty() {
		t.Fatalf("new database: %+v, %v; want no rules", rules, err)
	}

	for _, r := range []struct{ kind, action, value string }{
		{KindTag, ActionDeny, " Soccer "},
		{KindTag, ActionDeny, "Basketball"},
		{KindTag, ActionAllow, "AI"},
		{KindWord, ActionAllow, " Elon Musk"},
		{KindWord, ActionDeny, "goalscorer"},
	} {
		if _, err := m.AddFilterRule(ctx, r.kind, r.action, r.value, "tester"); err != nil {
			t.Fatalf("add %+v: %v", r, err)
		}
	}

	got, err := m.FilterRules(ctx)
	if err != nil {
		t.Fatalf("FilterRules: %v", err)
	}
	want := FilterRules{
		AllowTags:  []string{"AI"},
		DenyTags:   []string{"Basketball", "Soccer"},
		AllowWords: []string{"elon musk"},
		DenyWords:  []string{"goalscorer"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FilterRules = %+v, want %+v", got, want)
	}

	// The same rule again, and the same value in the opposite list.
	if _, err := m.AddFilterRule(ctx, KindTag, ActionDeny, "Soccer", "tester"); !errors.Is(err, ErrRuleExists) {
		t.Errorf("duplicate: err = %v, want ErrRuleExists", err)
	}
	stored, err := m.AddFilterRule(ctx, KindWord, ActionAllow, "GOALSCORER", "tester")
	if !errors.Is(err, ErrRuleConflict) || stored.Action != ActionDeny {
		t.Errorf("conflict: err = %v, stored = %+v; want ErrRuleConflict with the deny rule", err, stored)
	}
	// A tag and a word may share a value: different kinds.
	if _, err := m.AddFilterRule(ctx, KindWord, ActionDeny, "soccer", "tester"); err != nil {
		t.Errorf("word with a tag's value: %v", err)
	}

	removed, err := m.RemoveFilterRule(ctx, KindTag, "Soccer", "tester")
	if err != nil || removed.Action != ActionDeny {
		t.Fatalf("remove: %+v, %v", removed, err)
	}
	if _, err := m.RemoveFilterRule(ctx, KindTag, "Soccer", "tester"); !errors.Is(err, ErrRuleNotFound) {
		t.Errorf("second remove: err = %v, want ErrRuleNotFound", err)
	}

	history, err := m.FilterRuleHistory(ctx, 2)
	if err != nil {
		t.Fatalf("FilterRuleHistory: %v", err)
	}
	if len(history) != 2 || history[0].Op != "remove" || history[0].Value != "Soccer" || history[0].By != "tester" ||
		history[1].Op != "add" || history[1].Value != "soccer" {
		t.Errorf("history = %+v, want the remove of tag Soccer, then the add of word soccer", history)
	}
}

func TestNormalizeRule(t *testing.T) {
	for _, c := range []struct {
		kind, action, value, want string
		ok                        bool
	}{
		{KindTag, ActionDeny, " Up or Down ", "Up or Down", true},
		{KindWord, ActionAllow, " Elon Musk ", "elon musk", true},
		{KindWord, "", "UFO", "ufo", true}, // remove: no action
		{KindTag, ActionDeny, "  ", "", false},
		{"topic", ActionDeny, "x", "", false},
		{KindTag, "block", "x", "", false},
	} {
		got, err := NormalizeRule(c.kind, c.action, c.value)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("NormalizeRule(%q, %q, %q) = %q, %v", c.kind, c.action, c.value, got, err)
		}
	}
}

func TestFilterRules_Empty(t *testing.T) {
	if !(FilterRules{AllowTags: []string{"AI"}, AllowWords: []string{"ufo"}}).Empty() {
		t.Error("allow rules only: want Empty, nothing is filtered out")
	}
	if (FilterRules{DenyWords: []string{"goalscorer"}}).Empty() {
		t.Error("a deny word: want not Empty")
	}
}

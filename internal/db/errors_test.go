package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/polymarket"
)

// Every query returns its error instead of a zero result: callers rely on
// it to retry instead of acting on missing data.
func TestMongoDB_ErrorsArePropagated(t *testing.T) {
	m := newTestMongo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // every query fails at once

	rules := FilterRules{DenyTags: []string{"Sports"}}
	markets := []polymarket.Market{{ID: "1"}}
	now := time.Now()
	for name, call := range map[string]func() error{
		"EnsureMarketIndexes": func() error { return m.EnsureMarketIndexes(ctx) },
		"UpsertMarkets":       func() error { _, err := m.UpsertMarkets(ctx, markets, now); return err },
		"EnsureFilterRules":   func() error { return m.EnsureFilterRules(ctx) },
		"FilterRules":         func() error { _, err := m.FilterRules(ctx); return err },
		"AddFilterRule":       func() error { _, err := m.AddFilterRule(ctx, KindTag, ActionDeny, "x", ""); return err },
		"RemoveFilterRule":    func() error { _, err := m.RemoveFilterRule(ctx, KindTag, "x", ""); return err },
		"FilterRuleHistory":   func() error { _, err := m.FilterRuleHistory(ctx, 1); return err },
		"TagInUse":            func() error { _, err := m.TagInUse(ctx, "x"); return err },
		"MarkAwesome":         func() error { _, err := m.MarkAwesome(ctx, rules); return err },
		"MarkAwesomeIDs":      func() error { _, err := m.MarkAwesomeIDs(ctx, []string{"1"}, rules); return err },
		"CountAwesome":        func() error { _, _, err := m.CountAwesome(ctx); return err },
		"Ping":                func() error { return m.Ping(ctx) },
		"QueueSizes":          func() error { _, _, err := m.QueueSizes(ctx); return err },
		"Stats":               func() error { _, err := m.Stats(ctx, now); return err },
		"GetSyncState":        func() error { _, err := m.GetSyncState(ctx, "x"); return err },
		"SaveSyncState":       func() error { return m.SaveSyncState(ctx, SyncState{ID: "x"}) },
		"MaxUpdatedAt":        func() error { _, _, err := m.MaxUpdatedAt(ctx, false); return err },
		"PendingNotifications": func() error {
			_, err := m.PendingNotifications(ctx, 1)
			return err
		},
		"MarkNotified":     func() error { return m.MarkNotified(ctx, "1", now, 1) },
		"PendingEdits":     func() error { _, err := m.PendingEdits(ctx, 1); return err },
		"MarkEdited":       func() error { return m.MarkEdited(ctx, "1") },
		"PendingDeletes":   func() error { _, err := m.PendingDeletes(ctx, 1); return err },
		"MarkDeleted":      func() error { return m.MarkDeleted(ctx, []string{"1"}, now) },
		"MarkDeleteFailed": func() error { return m.MarkDeleteFailed(ctx, "1", "refused") },
		"RestoreNotified":  func() error { _, err := m.RestoreNotified(ctx, "slug", 1, now); return err },
	} {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s = %v, want context.Canceled", name, err)
		}
	}
}

func TestTagInUse(t *testing.T) {
	m := newTestMongo(t)
	ctx := context.Background()
	if _, err := m.UpsertMarkets(ctx, []polymarket.Market{{ID: "1", Tags: []string{"Science"}}}, time.Now()); err != nil {
		t.Fatalf("UpsertMarkets: %v", err)
	}
	for tag, want := range map[string]bool{"Science": true, "science": false, "Sports": false} {
		if got, err := m.TagInUse(ctx, tag); err != nil || got != want {
			t.Errorf("TagInUse(%q) = %t, %v; want %t", tag, got, err, want)
		}
	}
}

func TestMarkAwesomeIDs_NoIDsNoQuery(t *testing.T) {
	var m MongoDB // no connection: an empty list must not reach the database
	if n, err := m.MarkAwesomeIDs(context.Background(), nil, FilterRules{}); n != 0 || err != nil {
		t.Errorf("MarkAwesomeIDs(nil) = %d, %v; want 0, nil", n, err)
	}
}

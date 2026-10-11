package db

import (
	"context"
	"sort"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/polymarket"
)

func pendingIDs(t *testing.T, m *MongoDB) []string {
	t.Helper()
	pending, err := m.PendingNotifications(context.Background(), 100)
	if err != nil {
		t.Fatalf("PendingNotifications: %v", err)
	}
	ids := make([]string, 0, len(pending))
	for _, p := range pending {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	return ids
}

func TestNotifications_QueueFollowsTransitions(t *testing.T) {
	m := newTestMongo(t)
	ctx := context.Background()
	if err := m.EnsureMarketIndexes(ctx); err != nil {
		t.Fatalf("EnsureMarketIndexes: %v", err)
	}
	rules := FilterRules{DenyTags: []string{"Sports"}}
	withZombie := FilterRules{DenyTags: []string{"Sports"}, AllowWords: []string{"zombie"}}

	// Already awesome before notifications existed (no awesome_since):
	// must not be announced.
	if _, err := m.UpsertMarkets(ctx, []polymarket.Market{{ID: "old", Question: "Aliens?"}}, time.Now()); err != nil {
		t.Fatalf("upsert old: %v", err)
	}
	if _, err := m.db.Collection(marketsCollection).UpdateOne(ctx, bson.D{{Key: "_id", Value: "old"}},
		bson.D{{Key: "$set", Value: bson.D{{Key: fieldIsAwesome, Value: true}}}}); err != nil {
		t.Fatalf("seed old: %v", err)
	}

	if _, err := m.UpsertMarkets(ctx, []polymarket.Market{
		{ID: "new", Question: "UFO?"},                                 // new and awesome
		{ID: "sport", Question: "Goals?", Tags: []string{"Sports"}},   // new, not awesome
		{ID: "closed", Question: "Matrix?", Closed: true},             // awesome but closed
		{ID: "later", Question: "Zombies?", Tags: []string{"Sports"}}, // becomes awesome via a keyword
	}, time.Now()); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := m.MarkAwesome(ctx, rules); err != nil {
		t.Fatalf("MarkAwesome: %v", err)
	}
	if got := pendingIDs(t, m); len(got) != 1 || got[0] != "new" {
		t.Fatalf("pending = %v, want [new]", got)
	}

	// A recompute without changes keeps the queue as is.
	if _, err := m.MarkAwesome(ctx, rules); err != nil {
		t.Fatalf("MarkAwesome again: %v", err)
	}
	// A new keyword turns "later" awesome: it joins the queue.
	if _, err := m.MarkAwesome(ctx, withZombie); err != nil {
		t.Fatalf("MarkAwesome with keyword: %v", err)
	}
	if got := pendingIDs(t, m); len(got) != 2 || got[0] != "later" || got[1] != "new" {
		t.Fatalf("pending = %v, want [later new]", got)
	}

	// Sent: leaves the queue and never comes back, even after flipping off and on.
	if err := m.MarkNotified(ctx, "new", time.Now(), 42); err != nil {
		t.Fatalf("MarkNotified: %v", err)
	}
	var msg struct {
		ID int `bson:"telegram_message_id"`
	}
	if err := m.db.Collection(marketsCollection).FindOne(ctx, bson.D{{Key: "_id", Value: "new"}}).Decode(&msg); err != nil || msg.ID != 42 {
		t.Fatalf("telegram_message_id = %d, %v; want 42", msg.ID, err)
	}
	if _, err := m.UpsertMarkets(ctx, []polymarket.Market{{ID: "new", Question: "UFO?", Tags: []string{"Science"}}}, time.Now()); err != nil {
		t.Fatalf("upsert new: %v", err)
	}
	if _, err := m.MarkAwesome(ctx, FilterRules{DenyTags: []string{"Sports", "Science"}, AllowWords: []string{"zombie"}}); err != nil { // "new" is off
		t.Fatalf("MarkAwesome: %v", err)
	}
	if _, err := m.MarkAwesome(ctx, withZombie); err != nil { // and on again
		t.Fatalf("MarkAwesome: %v", err)
	}
	if got := pendingIDs(t, m); len(got) != 1 || got[0] != "later" {
		t.Fatalf("pending = %v, want [later]", got)
	}

	// Closing before the notification is sent drops it from the queue.
	if _, err := m.UpsertMarkets(ctx, []polymarket.Market{{ID: "later", Question: "Zombies?", Tags: []string{"Sports"}, Closed: true}}, time.Now()); err != nil {
		t.Fatalf("upsert later closed: %v", err)
	}
	if _, err := m.MarkAwesomeIDs(ctx, []string{"later"}, withZombie); err != nil {
		t.Fatalf("MarkAwesomeIDs: %v", err)
	}
	if got := pendingIDs(t, m); len(got) != 0 {
		t.Fatalf("pending = %v, want none", got)
	}

	// awesome_since: set on the transition, kept while awesome, absent otherwise.
	var since struct {
		Since *time.Time `bson:"awesome_since"`
	}
	for id, want := range map[string]bool{"old": false, "new": true, "sport": false, "closed": true} {
		since.Since = nil
		if err := m.db.Collection(marketsCollection).FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&since); err != nil {
			t.Fatalf("find %s: %v", id, err)
		}
		if (since.Since != nil) != want {
			t.Errorf("%s: awesome_since = %v, want set=%v", id, since.Since, want)
		}
	}
}

func TestNotifications_EditQueue(t *testing.T) {
	m := newTestMongo(t)
	ctx := context.Background()
	if err := m.EnsureMarketIndexes(ctx); err != nil {
		t.Fatalf("EnsureMarketIndexes: %v", err)
	}
	if _, err := m.UpsertMarkets(ctx, []polymarket.Market{{ID: "a", Question: "A?"}, {ID: "b", Question: "B?"}}, time.Now()); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := m.MarkNotified(ctx, "a", time.Now(), 7); err != nil {
		t.Fatalf("MarkNotified: %v", err)
	}
	// What `make notify-reformat` does.
	if _, err := m.db.Collection(marketsCollection).UpdateMany(ctx,
		bson.D{{Key: fieldMessageID, Value: bson.D{{Key: "$exists", Value: true}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: fieldEditPending, Value: true}}}}); err != nil {
		t.Fatalf("queue edits: %v", err)
	}

	edits, err := m.PendingEdits(ctx, 10)
	if err != nil || len(edits) != 1 || edits[0].ID != "a" || edits[0].MessageID != 7 {
		t.Fatalf("PendingEdits = %+v, %v; want market a with message 7", edits, err)
	}
	if err := m.MarkEdited(ctx, "a"); err != nil {
		t.Fatalf("MarkEdited: %v", err)
	}
	if edits, _ := m.PendingEdits(ctx, 10); len(edits) != 0 {
		t.Fatalf("PendingEdits after edit = %v, want none", edits)
	}
}

func deleteIDs(t *testing.T, m *MongoDB) []string {
	t.Helper()
	pending, err := m.PendingDeletes(context.Background(), 100)
	if err != nil {
		t.Fatalf("PendingDeletes: %v", err)
	}
	ids := make([]string, 0, len(pending))
	for _, p := range pending {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	return ids
}

func TestNotifications_DeleteQueueAndRepost(t *testing.T) {
	m := newTestMongo(t)
	ctx := context.Background()
	if err := m.EnsureMarketIndexes(ctx); err != nil {
		t.Fatalf("EnsureMarketIndexes: %v", err)
	}
	none := FilterRules{DenyTags: []string{"Weather"}}
	soccer := FilterRules{DenyTags: []string{"Soccer"}}

	if _, err := m.UpsertMarkets(ctx, []polymarket.Market{
		{ID: "goal", Question: "Goals?", Tags: []string{"Soccer"}},                 // posted, then denied
		{ID: "unposted", Question: "Corners?", Tags: []string{"Soccer"}},           // denied, never posted
		{ID: "ufo", Question: "UFO?", Tags: []string{"Science"}},                   // posted, stays awesome
		{ID: "closed", Question: "Fouls?", Tags: []string{"Soccer"}, Closed: true}, // posted, closed, denied
	}, time.Now()); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := m.MarkAwesome(ctx, none); err != nil {
		t.Fatalf("MarkAwesome: %v", err)
	}
	for i, id := range []string{"goal", "ufo", "closed"} {
		if err := m.MarkNotified(ctx, id, time.Now(), 10+i); err != nil {
			t.Fatalf("MarkNotified %s: %v", id, err)
		}
	}

	// The updater's recompute of a few markets does not delete messages.
	if _, err := m.MarkAwesomeIDs(ctx, []string{"goal"}, soccer); err != nil {
		t.Fatalf("MarkAwesomeIDs: %v", err)
	}
	if got := deleteIDs(t, m); len(got) != 0 {
		t.Fatalf("delete queue after the updater = %v, want empty", got)
	}

	// The full recompute after a rule change does, closed markets included.
	if _, err := m.MarkAwesome(ctx, soccer); err != nil {
		t.Fatalf("MarkAwesome: %v", err)
	}
	if got := deleteIDs(t, m); len(got) != 2 || got[0] != "closed" || got[1] != "goal" {
		t.Fatalf("delete queue = %v, want [closed goal]", got)
	}

	// Awesome again before the delete: the message stays.
	if _, err := m.MarkAwesome(ctx, none); err != nil {
		t.Fatalf("MarkAwesome back: %v", err)
	}
	if got := deleteIDs(t, m); len(got) != 0 {
		t.Fatalf("delete queue after the rule was reverted = %v, want empty", got)
	}

	if _, err := m.MarkAwesome(ctx, soccer); err != nil {
		t.Fatalf("MarkAwesome: %v", err)
	}
	if err := m.MarkDeleted(ctx, []string{"goal"}, time.Now()); err != nil {
		t.Fatalf("MarkDeleted: %v", err)
	}
	if err := m.MarkDeleteFailed(ctx, "closed", "message can't be deleted"); err != nil {
		t.Fatalf("MarkDeleteFailed: %v", err)
	}
	if got := deleteIDs(t, m); len(got) != 0 {
		t.Fatalf("delete queue after deleting = %v, want empty", got)
	}

	// Awesome again after the delete: announced anew, with a new message id.
	if _, err := m.MarkAwesome(ctx, none); err != nil {
		t.Fatalf("MarkAwesome back: %v", err)
	}
	// "unposted" is queued as well: open, never announced, awesome again.
	if got := pendingIDs(t, m); len(got) != 2 || got[0] != "goal" || got[1] != "unposted" {
		t.Fatalf("pending = %v, want [goal unposted]: a deleted market is posted again", got)
	}
	if err := m.MarkNotified(ctx, "goal", time.Now(), 99); err != nil {
		t.Fatalf("MarkNotified: %v", err)
	}
	var got struct {
		MessageID int        `bson:"telegram_message_id"`
		DeletedAt *time.Time `bson:"telegram_deleted_at"`
	}
	if err := m.db.Collection(marketsCollection).FindOne(ctx, bson.D{{Key: "_id", Value: "goal"}}).Decode(&got); err != nil {
		t.Fatalf("find goal: %v", err)
	}
	if got.MessageID != 99 || got.DeletedAt == nil {
		t.Errorf("goal: message %d, deleted_at %v; want 99 and the time of the delete", got.MessageID, got.DeletedAt)
	}
}

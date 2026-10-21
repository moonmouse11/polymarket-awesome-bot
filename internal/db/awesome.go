package db

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/sync/errgroup"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/awesome"
)

// Fields written by MarkAwesome. They are not part of polymarket.Market, so
// the loader's $set never touches them.
const (
	fieldIsAwesome        = "is_awesome"
	fieldAwesomeReason    = "awesome_reason"  // awesome.Reason*
	fieldAwesomeMatches   = "awesome_matches" // every rule the market matched; absent when none
	fieldAwesomeCheckedAt = "awesome_checked_at"
	fieldAwesomeSince     = "awesome_since"       // when the market last became awesome; absent when not awesome
	fieldNotifyPending    = "notify_pending"      // true: an open market became awesome and waits for a notification
	fieldNotifiedAt       = "notified_at"         // when the notification was sent; never cleared, so no repeats
	fieldMessageID        = "telegram_message_id" // channel message about the market, to edit it later
	fieldEditPending      = "edit_pending"        // true: the channel message must be re-rendered and edited
	fieldDeletePending    = "delete_pending"      // true: the market stopped being awesome, its message must be deleted
	fieldDeletedAt        = "telegram_deleted_at" // when its last channel message was deleted
)

const (
	markBatch   = 5000 // markets per bulk write
	markWriters = 2    // bulk writes in flight; 4 is no faster
)

// Fields replaced by awesome_matches; a recompute removes them.
var legacyAwesomeFields = bson.A{"awesome_words", "awesome_excluded_tag"}

// MarkAwesome recomputes the awesome fields for every market. It is run
// after the rules change, so it also queues the channel messages of markets
// that are no longer awesome for deletion (delete_pending); MarkAwesomeIDs,
// run on every update, does not. The decision is awesome.Classify. It
// returns how many markets were checked.
func (m *MongoDB) MarkAwesome(ctx context.Context, rules FilterRules) (int64, error) {
	return m.markAwesome(ctx, bson.D{}, rules, true)
}

// MarkAwesomeIDs is MarkAwesome for the given market ids only.
func (m *MongoDB) MarkAwesomeIDs(ctx context.Context, ids []string, rules FilterRules) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	return m.markAwesome(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}}, rules, false)
}

// awesomeInput is the part of a market document Classify needs.
type awesomeInput struct {
	ID          string   `bson:"_id"`
	Question    string   `bson:"question"`
	EventTitle  string   `bson:"event_title"`
	Description string   `bson:"description"`
	Tags        []string `bson:"tags"`
}

// markAwesome reads the markets, classifies them and writes the verdicts in
// batches. Updating the collection while reading it is safe: an update
// keeps the document's place in the scan.
func (m *MongoDB) markAwesome(ctx context.Context, filter bson.D, rules FilterRules, deletePosted bool) (int64, error) {
	coll := m.db.Collection(marketsCollection)
	cur, err := coll.Find(ctx, filter, options.Find().SetBatchSize(markBatch).SetProjection(bson.D{
		{Key: "question", Value: 1}, {Key: "event_title", Value: 1}, {Key: "description", Value: 1}, {Key: "tags", Value: 1},
	}))
	if err != nil {
		return 0, fmt.Errorf("read markets to mark: %w", err)
	}
	defer func() { _ = cur.Close(context.Background()) }()

	// Markets of a batch with the same verdict share one update: there are
	// few distinct verdicts. Batches are written by markWriters goroutines
	// while the next ones are read: done in turn, reads and writes stall each
	// other and the recompute is 2x slower than with this overlap.
	type group struct {
		v   awesome.Verdict
		ids []string
	}
	g, gctx := errgroup.WithContext(ctx)
	batches := make(chan map[string]*group)
	for range markWriters {
		g.Go(func() error {
			for groups := range batches {
				models := make([]mongo.WriteModel, 0, len(groups))
				for _, gr := range groups {
					models = append(models, mongo.NewUpdateManyModel().
						SetFilter(bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: gr.ids}}}}).
						SetUpdate(verdictUpdate(gr.v, deletePosted)))
				}
				if _, err := coll.BulkWrite(gctx, models, options.BulkWrite().SetOrdered(false)); err != nil {
					return fmt.Errorf("mark awesome markets: %w", err)
				}
			}
			return nil
		})
	}

	var checked int64
	g.Go(func() error {
		defer close(batches)
		send := func(groups map[string]*group) error {
			select {
			case batches <- groups:
				return nil
			case <-gctx.Done():
				return gctx.Err()
			}
		}
		pending, groups := 0, map[string]*group{}
		for cur.Next(gctx) {
			var mk awesomeInput
			if err := cur.Decode(&mk); err != nil {
				return fmt.Errorf("decode market: %w", err)
			}
			v := awesome.Classify(awesome.Market{Question: mk.Question, EventTitle: mk.EventTitle, Description: mk.Description, Tags: mk.Tags}, rules)
			key := fmt.Sprintf("%q", v) // %q keeps list boundaries unambiguous
			gr := groups[key]
			if gr == nil {
				gr = &group{v: v}
				groups[key] = gr
			}
			gr.ids = append(gr.ids, mk.ID)
			checked++
			if pending++; pending == markBatch {
				if err := send(groups); err != nil {
					return err
				}
				pending, groups = 0, map[string]*group{}
			}
		}
		if err := cur.Err(); err != nil {
			return fmt.Errorf("read markets to mark: %w", err)
		}
		if pending > 0 {
			return send(groups)
		}
		return nil
	})
	err = g.Wait()
	return checked, err
}

// verdictUpdate stores a verdict and moves the market between the channel
// queues. The transitions depend on the document's previous state and run
// on the server, so a concurrent write by the notifier is never lost.
func verdictUpdate(v awesome.Verdict, deletePosted bool) mongo.Pipeline {
	// $literal: a rule value starting with "$" must not read as a field path.
	matches := interface{}("$$REMOVE")
	if !v.Matches.Empty() {
		matches = bson.D{{Key: "$literal", Value: v.Matches}}
	}
	isAwesome := v.Awesome()
	wasAwesome := bson.D{{Key: "$eq", Value: bson.A{"$" + fieldIsAwesome, true}}}

	return mongo.Pipeline{
		// Expressions in one $set see the values from before it, so
		// "$is_awesome" here is the previous flag: this is where a transition
		// to awesome is detected.
		{{Key: "$set", Value: bson.D{
			{Key: fieldAwesomeReason, Value: bson.D{{Key: "$literal", Value: v.Reason}}},
			{Key: fieldAwesomeMatches, Value: matches},
			{Key: fieldAwesomeCheckedAt, Value: "$$NOW"},
			{Key: fieldIsAwesome, Value: isAwesome},
			{Key: fieldAwesomeSince, Value: bson.D{{Key: "$cond", Value: bson.A{
				isAwesome,
				bson.D{{Key: "$cond", Value: bson.A{wasAwesome, "$" + fieldAwesomeSince, "$$NOW"}}},
				"$$REMOVE",
			}}}},
			{Key: fieldNotifyPending, Value: bson.D{{Key: "$switch", Value: bson.D{
				{Key: "branches", Value: bson.A{
					// Not awesome any more or closed: drop from the queue.
					bson.D{{Key: "case", Value: bson.D{{Key: "$or", Value: bson.A{
						!isAwesome,
						bson.D{{Key: "$eq", Value: bson.A{"$closed", true}}},
					}}}}, {Key: "then", Value: "$$REMOVE"}},
					// Just became awesome and was never announced: enqueue.
					bson.D{{Key: "case", Value: bson.D{{Key: "$and", Value: bson.A{
						bson.D{{Key: "$not", Value: bson.A{wasAwesome}}},
						bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: "$" + fieldNotifiedAt}}, "missing"}}},
					}}}}, {Key: "then", Value: true}},
				}},
				// Otherwise keep the current state (a missing field stays missing).
				{Key: "default", Value: "$" + fieldNotifyPending},
			}}}},
			// A posted market that is not awesome: delete its message (full
			// recompute only); awesome again before that: keep the message.
			{Key: fieldDeletePending, Value: bson.D{{Key: "$switch", Value: bson.D{
				{Key: "branches", Value: bson.A{
					bson.D{{Key: "case", Value: isAwesome}, {Key: "then", Value: "$$REMOVE"}},
					bson.D{{Key: "case", Value: bson.D{{Key: "$and", Value: bson.A{
						deletePosted,
						bson.D{{Key: "$ne", Value: bson.A{bson.D{{Key: "$type", Value: "$" + fieldMessageID}}, "missing"}}},
					}}}}, {Key: "then", Value: true}},
				}},
				{Key: "default", Value: "$" + fieldDeletePending},
			}}}},
		}}},
		{{Key: "$unset", Value: legacyAwesomeFields}},
	}
}

// CountAwesome returns how many markets are awesome, in total and still open.
func (m *MongoDB) CountAwesome(ctx context.Context) (total, open int64, err error) {
	coll := m.db.Collection(marketsCollection)
	if total, err = coll.CountDocuments(ctx, bson.D{{Key: fieldIsAwesome, Value: true}}); err != nil {
		return 0, 0, fmt.Errorf("count awesome markets: %w", err)
	}
	if open, err = coll.CountDocuments(ctx, bson.D{{Key: fieldIsAwesome, Value: true}, {Key: "closed", Value: false}}); err != nil {
		return 0, 0, fmt.Errorf("count open awesome markets: %w", err)
	}
	return total, open, nil
}

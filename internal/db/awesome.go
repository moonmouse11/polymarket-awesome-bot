package db

import (
	"context"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// Fields written by MarkAwesome. They are not part of polymarket.Market, so
// the loader's $set never touches them.
const (
	fieldIsAwesome        = "is_awesome"
	fieldAwesomeReason    = "awesome_reason"       // see Reason* below
	fieldAwesomeWords     = "awesome_words"        // keywords found in the market; absent when none
	fieldAwesomeExcludeBy = "awesome_excluded_tag" // first excluded tag found; absent when none
	fieldAwesomeCheckedAt = "awesome_checked_at"
	fieldAwesomeSince     = "awesome_since"       // when the market last became awesome; absent when not awesome
	fieldNotifyPending    = "notify_pending"      // true: an open market became awesome and waits for a notification
	fieldNotifiedAt       = "notified_at"         // when the notification was sent; never cleared, so no repeats
	fieldMessageID        = "telegram_message_id" // channel message about the market, to edit it later
	fieldEditPending      = "edit_pending"        // true: the channel message must be re-rendered and edited
)

// Values of awesome_reason.
const (
	ReasonTags        = "tags"         // awesome: no excluded tag
	ReasonWords       = "words"        // awesome: has an excluded tag, but a keyword overrides it
	ReasonExcludedTag = "excluded_tag" // not awesome
)

// awesomeTextField is a temporary field with the searchable text; it is
// removed in the same update and never stored.
const awesomeTextField = "_awesome_text"

// MarkAwesome recomputes the awesome fields for every market in one
// server-side update. A market is awesome unless it has an excluded tag;
// a keyword found as a substring (case-insensitive) in question, tags,
// event_title or description overrides the excluded tag.
// It returns how many markets were matched.
func (m *MongoDB) MarkAwesome(ctx context.Context, excluded, keywords []string) (int64, error) {
	return m.markAwesome(ctx, bson.D{}, excluded, keywords)
}

// MarkAwesomeIDs is MarkAwesome for the given market ids only.
func (m *MongoDB) MarkAwesomeIDs(ctx context.Context, ids, excluded, keywords []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	return m.markAwesome(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}}, excluded, keywords)
}

func (m *MongoDB) markAwesome(ctx context.Context, filter bson.D, excluded, keywords []string) (int64, error) {
	if excluded == nil {
		excluded = []string{} // null would break $filter
	}
	words := make([]string, 0, len(keywords))
	for _, w := range keywords {
		// An empty word would match every market.
		if w = strings.ToLower(strings.TrimSpace(w)); w != "" {
			words = append(words, w)
		}
	}

	ifNull := func(field string, def interface{}) bson.D {
		return bson.D{{Key: "$ifNull", Value: bson.A{field, def}}}
	}

	// First excluded tag present in the market's tags, or nothing.
	firstExcluded := bson.D{{Key: "$first", Value: bson.D{{Key: "$filter", Value: bson.D{
		{Key: "input", Value: excluded},
		{Key: "cond", Value: bson.D{{Key: "$in", Value: bson.A{"$$this", ifNull("$tags", bson.A{})}}}},
	}}}}}

	// question + event_title + description + tags, lowercased.
	text := bson.D{{Key: "$toLower", Value: bson.D{{Key: "$concat", Value: bson.A{
		ifNull("$question", ""), " ",
		ifNull("$event_title", ""), " ",
		ifNull("$description", ""), " ",
		bson.D{{Key: "$reduce", Value: bson.D{
			{Key: "input", Value: ifNull("$tags", bson.A{})},
			{Key: "initialValue", Value: ""},
			{Key: "in", Value: bson.D{{Key: "$concat", Value: bson.A{"$$value", " ", "$$this"}}}},
		}}},
	}}}}}

	foundWords := bson.D{{Key: "$filter", Value: bson.D{
		{Key: "input", Value: words},
		{Key: "cond", Value: bson.D{{Key: "$gte", Value: bson.A{
			bson.D{{Key: "$indexOfCP", Value: bson.A{"$" + awesomeTextField, "$$this"}}}, 0,
		}}}},
	}}}

	hasExcluded := bson.D{{Key: "$ne", Value: bson.A{bson.D{{Key: "$type", Value: "$" + fieldAwesomeExcludeBy}}, "missing"}}}
	hasWords := bson.D{{Key: "$gt", Value: bson.A{bson.D{{Key: "$size", Value: "$" + fieldAwesomeWords}}, 0}}}
	isAwesome := bson.D{{Key: "$ne", Value: bson.A{"$" + fieldAwesomeReason, ReasonExcludedTag}}}
	wasAwesome := bson.D{{Key: "$eq", Value: bson.A{"$" + fieldIsAwesome, true}}}

	pipeline := mongo.Pipeline{
		{{Key: "$set", Value: bson.D{
			{Key: awesomeTextField, Value: text},
			{Key: fieldAwesomeExcludeBy, Value: bson.D{{Key: "$ifNull", Value: bson.A{firstExcluded, "$$REMOVE"}}}},
		}}},
		{{Key: "$set", Value: bson.D{
			{Key: fieldAwesomeWords, Value: foundWords},
		}}},
		{{Key: "$set", Value: bson.D{
			{Key: fieldAwesomeReason, Value: bson.D{{Key: "$switch", Value: bson.D{
				{Key: "branches", Value: bson.A{
					bson.D{{Key: "case", Value: bson.D{{Key: "$not", Value: bson.A{hasExcluded}}}}, {Key: "then", Value: ReasonTags}},
					bson.D{{Key: "case", Value: hasWords}, {Key: "then", Value: ReasonWords}},
				}},
				{Key: "default", Value: ReasonExcludedTag},
			}}}},
			{Key: fieldAwesomeWords, Value: bson.D{{Key: "$cond", Value: bson.A{hasWords, "$" + fieldAwesomeWords, "$$REMOVE"}}}},
			{Key: fieldAwesomeCheckedAt, Value: "$$NOW"},
		}}},
		// Expressions in one $set see the values from before it, so
		// "$is_awesome" here is the previous flag: this is where a transition
		// to awesome is detected.
		{{Key: "$set", Value: bson.D{
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
						bson.D{{Key: "$not", Value: bson.A{isAwesome}}},
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
		}}},
		{{Key: "$unset", Value: awesomeTextField}},
	}

	res, err := m.db.Collection(marketsCollection).UpdateMany(ctx, filter, pipeline)
	if err != nil {
		return 0, fmt.Errorf("mark awesome markets: %w", err)
	}
	return res.MatchedCount, nil
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

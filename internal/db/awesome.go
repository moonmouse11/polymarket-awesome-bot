package db

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// Fields written by MarkAwesome. They are not part of polymarket.Market, so
// the loader's $set never touches them.
const (
	fieldIsAwesome        = "is_awesome"
	fieldAwesomeReason    = "awesome_reason"  // see Reason* below
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

// Values of awesome_reason.
const (
	ReasonAllow   = "allow"   // awesome: an allow tag or word matched (wins over deny rules)
	ReasonDeny    = "deny"    // not awesome: a deny rule matched and no allow rule did
	ReasonDefault = "default" // awesome: no rule matched
)

// Matches are the rules a market matched, stored in awesome_matches. Every
// list is kept, not only the deciding one, so the effect of a rule edit can
// be counted without rescanning the text.
type Matches struct {
	AllowTags  []string `bson:"allow_tags,omitempty"`
	DenyTags   []string `bson:"deny_tags,omitempty"`
	AllowWords []string `bson:"allow_words,omitempty"`
	DenyWords  []string `bson:"deny_words,omitempty"`
}

// Fields replaced by awesome_matches; a recompute removes them.
var legacyAwesomeFields = bson.A{"awesome_words", "awesome_excluded_tag"}

// Temporary fields, removed in the same update and never stored.
const (
	tmpText       = "_awesome_text"
	tmpAllowTags  = "_awesome_allow_tags"
	tmpDenyTags   = "_awesome_deny_tags"
	tmpAllowWords = "_awesome_allow_words"
	tmpDenyWords  = "_awesome_deny_words"
)

// wordPattern finds a word at the start of a word in the text: "alien"
// matches "alien" and "aliens", not "Valiente". The text is lowercased with
// $toLower, which handles ASCII only: a word with non-ASCII letters matches
// only lowercase text.
func wordPattern(word string) string {
	return `(?:^|[^\p{L}\p{N}])` + regexp.QuoteMeta(word)
}

// MarkAwesome recomputes the awesome fields for every market in one
// server-side update. It is run after the rules change, so it also queues
// the channel messages of markets that are no longer awesome for deletion
// (delete_pending); MarkAwesomeIDs, run on every update, does not.
//
// The decision:
//   - an allow tag or allow word matched → awesome (allow wins);
//   - otherwise a deny tag or deny word matched → not awesome;
//   - otherwise → awesome.
//
// Tags match exactly. Words match at the start of a word, any case, in
// question, event_title, description and tags. It returns how many markets
// were matched.
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

func (m *MongoDB) markAwesome(ctx context.Context, filter bson.D, rules FilterRules, deletePosted bool) (int64, error) {
	ifNull := func(field string, def interface{}) bson.D {
		return bson.D{{Key: "$ifNull", Value: bson.A{field, def}}}
	}
	size := func(field string) bson.D { return bson.D{{Key: "$size", Value: "$" + field}} }
	nonEmpty := func(field string) bson.D { return bson.D{{Key: "$gt", Value: bson.A{size(field), 0}}} }

	// Tags of the list present in the market's tags, in list order.
	tagsFound := func(list []string) bson.D {
		if list == nil {
			list = []string{} // null would break $filter
		}
		return bson.D{{Key: "$filter", Value: bson.D{
			{Key: "input", Value: list},
			{Key: "cond", Value: bson.D{{Key: "$in", Value: bson.A{"$$this", ifNull("$tags", bson.A{})}}}},
		}}}
	}
	// Words of the list found in the text, in list order. Each word is a
	// constant in the pipeline: a byte search first (cheap, rarely true),
	// then the word-start regex. Measured on 100k markets: 5 s, against 41 s
	// for $indexOfCP and 230 s for regexes alone.
	wordsFound := func(list []string) bson.D {
		found := bson.A{}
		for _, w := range list {
			// An empty word would match every market.
			if w = strings.ToLower(strings.TrimSpace(w)); w == "" {
				continue
			}
			found = append(found, bson.D{{Key: "$cond", Value: bson.A{
				bson.D{{Key: "$and", Value: bson.A{
					bson.D{{Key: "$gte", Value: bson.A{bson.D{{Key: "$indexOfBytes", Value: bson.A{"$" + tmpText, w}}}, 0}}},
					bson.D{{Key: "$regexMatch", Value: bson.D{
						{Key: "input", Value: "$" + tmpText}, {Key: "regex", Value: wordPattern(w)}, {Key: "options", Value: "i"},
					}}},
				}}},
				w, nil,
			}}})
		}
		return bson.D{{Key: "$filter", Value: bson.D{
			{Key: "input", Value: found},
			{Key: "cond", Value: bson.D{{Key: "$ne", Value: bson.A{"$$this", nil}}}},
		}}}
	}

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

	allowed := bson.D{{Key: "$or", Value: bson.A{nonEmpty(tmpAllowTags), nonEmpty(tmpAllowWords)}}}
	denied := bson.D{{Key: "$or", Value: bson.A{nonEmpty(tmpDenyTags), nonEmpty(tmpDenyWords)}}}
	keep := func(field string) bson.D { // the list, or nothing when empty
		return bson.D{{Key: "$cond", Value: bson.A{nonEmpty(field), "$" + field, "$$REMOVE"}}}
	}
	matchedAny := bson.D{{Key: "$gt", Value: bson.A{
		bson.D{{Key: "$add", Value: bson.A{size(tmpAllowTags), size(tmpDenyTags), size(tmpAllowWords), size(tmpDenyWords)}}}, 0,
	}}}

	isAwesome := bson.D{{Key: "$ne", Value: bson.A{"$" + fieldAwesomeReason, ReasonDeny}}}
	wasAwesome := bson.D{{Key: "$eq", Value: bson.A{"$" + fieldIsAwesome, true}}}

	pipeline := mongo.Pipeline{
		{{Key: "$set", Value: bson.D{
			{Key: tmpText, Value: text},
			{Key: tmpAllowTags, Value: tagsFound(rules.AllowTags)},
			{Key: tmpDenyTags, Value: tagsFound(rules.DenyTags)},
		}}},
		{{Key: "$set", Value: bson.D{
			{Key: tmpAllowWords, Value: wordsFound(rules.AllowWords)},
			{Key: tmpDenyWords, Value: wordsFound(rules.DenyWords)},
		}}},
		{{Key: "$set", Value: bson.D{
			{Key: fieldAwesomeReason, Value: bson.D{{Key: "$switch", Value: bson.D{
				{Key: "branches", Value: bson.A{
					bson.D{{Key: "case", Value: allowed}, {Key: "then", Value: ReasonAllow}},
					bson.D{{Key: "case", Value: denied}, {Key: "then", Value: ReasonDeny}},
				}},
				{Key: "default", Value: ReasonDefault},
			}}}},
			{Key: fieldAwesomeMatches, Value: bson.D{{Key: "$cond", Value: bson.A{
				matchedAny,
				bson.D{
					{Key: "allow_tags", Value: keep(tmpAllowTags)},
					{Key: "deny_tags", Value: keep(tmpDenyTags)},
					{Key: "allow_words", Value: keep(tmpAllowWords)},
					{Key: "deny_words", Value: keep(tmpDenyWords)},
				},
				"$$REMOVE",
			}}}},
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
		{{Key: "$unset", Value: append(bson.A{tmpText, tmpAllowTags, tmpDenyTags, tmpAllowWords, tmpDenyWords}, legacyAwesomeFields...)}},
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

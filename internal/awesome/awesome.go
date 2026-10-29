// Package awesome decides whether a market is awesome: worth posting to the
// channel. It is the domain rule only; storing the verdict and queueing the
// channel messages is the job of the db package.
package awesome

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/domain"
)

// Rules are the four lists, each sorted.
type Rules struct {
	AllowTags, DenyTags, AllowWords, DenyWords []string
}

// Empty reports whether nothing would be filtered out: with no deny rules
// every market is awesome.
func (r Rules) Empty() bool { return len(r.DenyTags) == 0 && len(r.DenyWords) == 0 }

// Values of Verdict.Reason.
const (
	ReasonAllow   = "allow"   // awesome: an allow tag or word matched (wins over deny rules)
	ReasonDeny    = "deny"    // not awesome: a deny rule matched and no allow rule did
	ReasonDefault = "default" // awesome: no rule matched
)

// Matches are the rules a market matched. Every list is kept, not only the
// deciding one, so the effect of a rule edit can be counted without
// rescanning the text.
type Matches struct {
	AllowTags  []string `bson:"allow_tags,omitempty"`
	DenyTags   []string `bson:"deny_tags,omitempty"`
	AllowWords []string `bson:"allow_words,omitempty"`
	DenyWords  []string `bson:"deny_words,omitempty"`
}

// Empty reports that no rule matched.
func (m Matches) Empty() bool {
	return len(m.AllowTags)+len(m.DenyTags)+len(m.AllowWords)+len(m.DenyWords) == 0
}

// Verdict is the decision for one market.
type Verdict struct {
	Reason  string
	Matches Matches
}

// Awesome reports whether the market goes to the channel.
func (v Verdict) Awesome() bool { return v.Reason != ReasonDeny }

// Classify decides:
//   - an allow tag or allow word matched → awesome (allow wins);
//   - otherwise a deny tag or deny word matched → not awesome;
//   - otherwise → awesome.
//
// Tags match exactly. Words match at the start of a word, any case, in the
// question, event title, description and tags: "alien" matches "Aliens",
// not "Valiente". Lists keep their order.
func Classify(m domain.Market, r Rules) Verdict {
	text := strings.ToLower(m.Question + " " + m.EventTitle + " " + m.Description + " " + strings.Join(m.Tags, " "))
	v := Verdict{Matches: Matches{
		AllowTags:  tagsFound(r.AllowTags, m.Tags),
		DenyTags:   tagsFound(r.DenyTags, m.Tags),
		AllowWords: wordsFound(r.AllowWords, text),
		DenyWords:  wordsFound(r.DenyWords, text),
	}}
	switch {
	case len(v.Matches.AllowTags) > 0 || len(v.Matches.AllowWords) > 0:
		v.Reason = ReasonAllow
	case len(v.Matches.DenyTags) > 0 || len(v.Matches.DenyWords) > 0:
		v.Reason = ReasonDeny
	default:
		v.Reason = ReasonDefault
	}
	return v
}

func tagsFound(list, tags []string) []string {
	var found []string
	for _, t := range list {
		for _, mt := range tags {
			if t == mt {
				found = append(found, t)
				break
			}
		}
	}
	return found
}

// wordsFound returns the words of the list found in the lowercased text.
func wordsFound(list []string, text string) []string {
	var found []string
	for _, w := range list {
		// An empty word would match every market.
		if w = strings.ToLower(strings.TrimSpace(w)); w != "" && hasWordStart(text, w) {
			found = append(found, w)
		}
	}
	return found
}

// hasWordStart reports whether w occurs in text at the start of a word:
// at the beginning or after a character that is not a letter or number
// (Unicode L and N, as the old MongoDB regex (?:^|[^\p{L}\p{N}])).
func hasWordStart(text, w string) bool {
	for from := 0; ; {
		i := strings.Index(text[from:], w)
		if i < 0 {
			return false
		}
		i += from
		prev, _ := utf8.DecodeLastRuneInString(text[:i])
		if i == 0 || !unicode.IsLetter(prev) && !unicode.IsNumber(prev) {
			return true
		}
		from = i + 1
	}
}

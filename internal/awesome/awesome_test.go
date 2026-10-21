package awesome

import (
	"reflect"
	"testing"
)

func TestClassify(t *testing.T) {
	rules := Rules{
		AllowTags:  []string{"AI"},
		DenyTags:   []string{"Elections", "Sports"},
		AllowWords: []string{"alien", "c++", "elon musk", " ", "ракета"},
		DenyWords:  []string{"goalscorer"},
	}
	tests := []struct {
		name    string
		market  Market
		reason  string
		matches Matches
	}{
		{"deny tag", Market{Question: "O/U 2.5 goals?", Tags: []string{"Sports", "Soccer"}}, ReasonDeny, Matches{DenyTags: []string{"Sports"}}},
		{"tags match exactly", Market{Tags: []string{"sports"}}, ReasonDefault, Matches{}},
		{"no rule", Market{Question: "Best picture?", Tags: []string{"Culture"}}, ReasonDefault, Matches{}},
		{"allow tag beats deny tag", Market{Tags: []string{"Sports", "AI"}}, ReasonAllow, Matches{AllowTags: []string{"AI"}, DenyTags: []string{"Sports"}}},
		{"allow word beats deny tag", Market{Question: "Aliens at the Super Bowl?", Tags: []string{"Sports"}}, ReasonAllow,
			Matches{DenyTags: []string{"Sports"}, AllowWords: []string{"alien"}}},
		{"word inside a word does not match", Market{Question: "Joaquín Valiente: Anytime Goalscorer"}, ReasonDeny, Matches{DenyWords: []string{"goalscorer"}}},
		{"phrase, any case, event title", Market{EventTitle: "ELON MUSK tweets", Tags: []string{"Elections"}}, ReasonAllow,
			Matches{DenyTags: []string{"Elections"}, AllowWords: []string{"elon musk"}}},
		{"description, after punctuation", Market{Description: "Resolves Yes if (alien) life is found."}, ReasonAllow, Matches{AllowWords: []string{"alien"}}},
		{"special characters are literal", Market{Question: "Is C++ dead?"}, ReasonAllow, Matches{AllowWords: []string{"c++"}}},
		{"allow word beats deny word", Market{Question: "Alien goalscorer?"}, ReasonAllow, Matches{AllowWords: []string{"alien"}, DenyWords: []string{"goalscorer"}}},
		{"second occurrence at a word start", Market{Question: "Valiente vs alien"}, ReasonAllow, Matches{AllowWords: []string{"alien"}}},
		{"word in tags", Market{Tags: []string{"Alien Life"}}, ReasonAllow, Matches{AllowWords: []string{"alien"}}},
		// Non-ASCII letters match in any case (MongoDB's $toLower handled ASCII only).
		{"non-ASCII any case", Market{Question: "Ракета долетит до Марса?"}, ReasonAllow, Matches{AllowWords: []string{"ракета"}}},
		{"non-ASCII letter before is a word", Market{Question: "суперракета"}, ReasonDefault, Matches{}},
		{"any Unicode number before is a word", Market{Question: "x²alien 2alien"}, ReasonDefault, Matches{}},
	}
	for _, tt := range tests {
		v := Classify(tt.market, rules)
		if v.Reason != tt.reason || v.Awesome() != (tt.reason != ReasonDeny) {
			t.Errorf("%s: reason %q, awesome %t; want %q", tt.name, v.Reason, v.Awesome(), tt.reason)
		}
		if !reflect.DeepEqual(v.Matches, tt.matches) || v.Matches.Empty() != (tt.reason == ReasonDefault) {
			t.Errorf("%s: matches %+v, want %+v", tt.name, v.Matches, tt.matches)
		}
	}
}

func TestRulesEmpty(t *testing.T) {
	if !(Rules{AllowTags: []string{"AI"}, AllowWords: []string{"x"}}).Empty() {
		t.Error("allow-only rules must be empty: nothing is filtered out")
	}
	if (Rules{DenyWords: []string{"x"}}).Empty() {
		t.Error("a deny word makes the rules non-empty")
	}
}

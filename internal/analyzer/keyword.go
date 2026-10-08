package analyzer

import (
	"context"
	"strings"
)

// DefaultKeywords is the initial trigger word list. It seeds the "keywords"
// collection when that collection is empty.
var DefaultKeywords = []string{
	"alien", "simulation", "fight", "drama", "ufo", "prison",
	"assassination", "nuclear", "zombie", "elon musk", "arrest",
	"conspiracy", "faked", "matrix", "boxing",
}

// KeywordAnalyzer uses predefined trigger words to catch obviously strange markets
type KeywordAnalyzer struct {
	TriggerWords []string
}

func NewKeywordAnalyzer() *KeywordAnalyzer {
	return &KeywordAnalyzer{
		TriggerWords: DefaultKeywords,
	}
}

func (k *KeywordAnalyzer) Analyze(ctx context.Context, event MarketEvent) (*AnalysisResult, error) {
	text := strings.ToLower(event.Title + " " + event.Description)
	
	for _, word := range k.TriggerWords {
		if strings.Contains(text, word) {
			return &AnalysisResult{
				IsStrange:  true,
				Reason:     "Found trigger word: " + word,
				Confidence: 1.0,
				AnalyzedBy: "keyword",
			}, nil
		}
	}
	
	return &AnalysisResult{
		IsStrange:  false,
		Reason:     "No trigger words found",
		Confidence: 1.0,
		AnalyzedBy: "keyword",
	}, nil
}

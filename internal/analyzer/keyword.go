package analyzer

import (
	"context"
	"strings"
)

// KeywordAnalyzer uses predefined trigger words to catch obviously awesome markets
type KeywordAnalyzer struct {
	TriggerWords []string
}

func NewKeywordAnalyzer(triggerWords []string) *KeywordAnalyzer {
	return &KeywordAnalyzer{
		TriggerWords: triggerWords,
	}
}

func (k *KeywordAnalyzer) Analyze(ctx context.Context, event MarketEvent) (*AnalysisResult, error) {
	text := strings.ToLower(event.Title + " " + event.Description)

	for _, word := range k.TriggerWords {
		if strings.Contains(text, word) {
			return &AnalysisResult{
				IsAwesome:  true,
				Reason:     "Found trigger word: " + word,
				Confidence: 1.0,
				AnalyzedBy: "keyword",
			}, nil
		}
	}

	return &AnalysisResult{
		IsAwesome:  false,
		Reason:     "No trigger words found",
		Confidence: 1.0,
		AnalyzedBy: "keyword",
	}, nil
}

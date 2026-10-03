package analyzer

import (
	"context"
	"log"
)

// HybridAnalyzer implements a fallback chain: Keyword -> Jev
type HybridAnalyzer struct {
	keywordAnalyzer *KeywordAnalyzer
	jevAnalyzer     *JevAnalyzer
}

func NewHybridAnalyzer(jevAPIKey string) *HybridAnalyzer {
	return &HybridAnalyzer{
		keywordAnalyzer: NewKeywordAnalyzer(),
		jevAnalyzer:     NewJevAnalyzer(jevAPIKey),
	}
}

func (h *HybridAnalyzer) Analyze(ctx context.Context, event MarketEvent) (*AnalysisResult, error) {
	// Step 1: Fast deterministic check
	res, err := h.keywordAnalyzer.Analyze(ctx, event)
	if err != nil {
		log.Printf("Keyword analysis failed: %v", err)
	} else if res.IsStrange {
		// If keyword matched, return immediately
		return res, nil
	}

	// Step 2: Fallback to AI (Jev) if it's available
	jevRes, err := h.jevAnalyzer.Analyze(ctx, event)
	if err != nil {
		// If Jev is not set up yet or failed, we just return the negative keyword result
		log.Printf("Jev analysis skipped/failed: %v", err)
		return res, nil
	}

	return jevRes, nil
}

package analyzer

import (
	"context"

	"github.com/rs/zerolog"
)

// HybridAnalyzer implements a fallback chain: Keyword -> Jev
type HybridAnalyzer struct {
	keywordAnalyzer *KeywordAnalyzer
	jevAnalyzer     *JevAnalyzer
	log             zerolog.Logger
}

func NewHybridAnalyzer(jevAPIKey string, log zerolog.Logger) *HybridAnalyzer {
	return &HybridAnalyzer{
		keywordAnalyzer: NewKeywordAnalyzer(),
		jevAnalyzer:     NewJevAnalyzer(jevAPIKey),
		log:             log,
	}
}

func (h *HybridAnalyzer) Analyze(ctx context.Context, event MarketEvent) (*AnalysisResult, error) {
	// Step 1: Fast deterministic check
	res, err := h.keywordAnalyzer.Analyze(ctx, event)
	if err != nil {
		h.log.Warn().Err(err).Str("analyzer", "keyword").Str("market_id", event.ID).Msg("analysis failed")
	} else if res.IsStrange {
		// If keyword matched, return immediately
		return res, nil
	}

	// Step 2: Fallback to AI (Jev) if it's available
	jevRes, err := h.jevAnalyzer.Analyze(ctx, event)
	if err != nil {
		// If Jev is not set up yet or failed, we just return the negative keyword result
		h.log.Warn().Err(err).Str("analyzer", "jev").Str("market_id", event.ID).Msg("analysis skipped or failed")
		return res, nil
	}

	return jevRes, nil
}

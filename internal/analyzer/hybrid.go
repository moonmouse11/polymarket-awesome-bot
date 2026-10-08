package analyzer

import (
	"context"

	"github.com/rs/zerolog"
)

type analyzeStep interface {
	Analyze(ctx context.Context, event MarketEvent) (*AnalysisResult, error)
}

// HybridAnalyzer implements a fallback chain: Keyword -> Jev
type HybridAnalyzer struct {
	keywordAnalyzer analyzeStep
	jevAnalyzer     analyzeStep
	log             zerolog.Logger
}

func NewHybridAnalyzer(jevAPIKey string, triggerWords []string, log zerolog.Logger) *HybridAnalyzer {
	return &HybridAnalyzer{
		keywordAnalyzer: NewKeywordAnalyzer(triggerWords),
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
		h.log.Warn().Err(err).Str("analyzer", "jev").Str("market_id", event.ID).Msg("analysis skipped or failed")
		if res == nil {
			return nil, err
		}
		return res, nil
	}

	return jevRes, nil
}

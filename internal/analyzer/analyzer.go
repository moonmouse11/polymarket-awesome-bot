package analyzer

import (
	"context"
)

// MarketEvent represents a single event/market from Polymarket
type MarketEvent struct {
	ID          string
	Title       string
	Description string
	Price       float64
}

// AnalysisResult contains the decision whether the event is awesome (strange/unexpected)
type AnalysisResult struct {
	IsAwesome   bool
	Reason      string
	Confidence  float64
	AnalyzedBy  string // "keyword", "jev", or "groq_fallback"
}

// Analyzer interface defines the contract for analyzing market events
type Analyzer interface {
	Analyze(ctx context.Context, event MarketEvent) (*AnalysisResult, error)
}

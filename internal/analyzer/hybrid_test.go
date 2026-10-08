package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/rs/zerolog"
)

func TestHybridAnalyzer_LogsWarningWhenJevFails(t *testing.T) {
	var buf bytes.Buffer
	h := NewHybridAnalyzer("", nil, zerolog.New(&buf)) // empty key: Jev returns an error

	res, err := h.Analyze(context.Background(), MarketEvent{ID: "7", Title: "Will it rain?"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result from keyword fallback")
	}
	if res.IsStrange || res.AnalyzedBy != "keyword" {
		t.Fatalf("want keyword not-strange result, got %+v", res)
	}

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("expected one JSON log record, got %q (%v)", buf.String(), err)
	}
	if rec["level"] != "warn" || rec["analyzer"] != "jev" || rec["market_id"] != "7" || rec["error"] == nil {
		t.Fatalf("unexpected log record: %v", rec)
	}
}

func TestHybridAnalyzer_KeywordMatchSkipsJev(t *testing.T) {
	var buf bytes.Buffer
	h := NewHybridAnalyzer("", nil, zerolog.New(&buf))

	res, err := h.Analyze(context.Background(), MarketEvent{ID: "1", Title: "Will aliens be found?"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !res.IsStrange || res.AnalyzedBy != "keyword" {
		t.Fatalf("want keyword strange result, got %+v", res)
	}
	if buf.Len() != 0 {
		t.Fatalf("Jev should not be called (no log expected), got %q", buf.String())
	}
}

func TestHybridAnalyzer_ReturnsJevErrorWhenKeywordFailed(t *testing.T) {
	h := &HybridAnalyzer{
		keywordAnalyzer: stubKeywordAnalyzer{err: errors.New("keyword failed")},
		jevAnalyzer:     NewJevAnalyzer(""),
		log:             zerolog.Nop(),
	}

	res, err := h.Analyze(context.Background(), MarketEvent{ID: "9", Title: "Plain market"})
	if err == nil {
		t.Fatalf("expected Jev error when keyword left no result, got res=%+v", res)
	}
	if res != nil {
		t.Fatalf("expected nil result with error, got %+v", res)
	}
}

func TestHybridAnalyzer_UsesDBTriggerWords(t *testing.T) {
	h := NewHybridAnalyzer("", []string{"customtoken"}, zerolog.Nop())

	res, err := h.Analyze(context.Background(), MarketEvent{ID: "2", Title: "customtoken event"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !res.IsStrange {
		t.Fatalf("expected DB trigger word match, got %+v", res)
	}
}

type stubKeywordAnalyzer struct {
	err error
}

func (s stubKeywordAnalyzer) Analyze(ctx context.Context, event MarketEvent) (*AnalysisResult, error) {
	return nil, s.err
}

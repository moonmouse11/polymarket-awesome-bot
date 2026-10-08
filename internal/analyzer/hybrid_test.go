package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/rs/zerolog"
)

func TestHybridAnalyzer_LogsWarningWhenJevFails(t *testing.T) {
	var buf bytes.Buffer
	h := NewHybridAnalyzer("", zerolog.New(&buf)) // empty key: Jev returns an error

	res, err := h.Analyze(context.Background(), MarketEvent{ID: "7", Title: "Will it rain?"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
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
	h := NewHybridAnalyzer("", zerolog.New(&buf))

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

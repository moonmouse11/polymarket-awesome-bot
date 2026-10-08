package logger

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNew_JSONByDefault(t *testing.T) {
	var buf bytes.Buffer

	log, err := New("", "", &buf)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	log.Info().Str("market_id", "42").Msg("hello")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("output is not JSON: %q (%v)", buf.String(), err)
	}
	if rec["level"] != "info" || rec["market_id"] != "42" || rec["message"] != "hello" {
		t.Fatalf("unexpected record: %v", rec)
	}
	if _, ok := rec["time"]; !ok {
		t.Fatalf("record has no time field: %v", rec)
	}
}

func TestNew_LevelFiltersLowerLevels(t *testing.T) {
	var buf bytes.Buffer

	log, err := New("warn", "json", &buf)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	log.Info().Msg("hidden")
	log.Warn().Msg("shown")

	out := buf.String()
	if strings.Contains(out, "hidden") || !strings.Contains(out, "shown") {
		t.Fatalf("level filter not applied, output: %q", out)
	}
}

func TestNew_ConsoleFormatIsNotJSON(t *testing.T) {
	var buf bytes.Buffer

	log, err := New("info", "console", &buf)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	log.Info().Msg("hello")

	if json.Valid(bytes.TrimSpace(buf.Bytes())) {
		t.Fatalf("console output looks like JSON: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "hello") {
		t.Fatalf("console output lost the message: %q", buf.String())
	}
}

func TestNew_RejectsInvalidValues(t *testing.T) {
	if _, err := New("loud", "json", &bytes.Buffer{}); err == nil {
		t.Error("invalid LOG_LEVEL accepted")
	}
	if _, err := New("info", "xml", &bytes.Buffer{}); err == nil {
		t.Error("invalid LOG_FORMAT accepted")
	}
}

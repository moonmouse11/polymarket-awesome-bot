package polymarket

import (
	"testing"
	"time"
)

func TestParseTime(t *testing.T) {
	tests := []struct {
		in   string
		want time.Time
	}{
		{"2025-07-03T20:25:56.889606Z", time.Date(2025, 7, 3, 20, 25, 56, 889606000, time.UTC)},
		{"2026-10-08 13:51:30.336956+00", time.Date(2026, 10, 8, 13, 51, 30, 336956000, time.UTC)},
		{"2021-07-08 22:21:47+00", time.Date(2021, 7, 8, 22, 21, 47, 0, time.UTC)},
	}
	for _, tt := range tests {
		got := parseTime(tt.in)
		if got == nil || !got.Equal(tt.want) {
			t.Errorf("parseTime(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}

	for _, bad := range []string{"", "yesterday"} {
		if got := parseTime(bad); got != nil {
			t.Errorf("parseTime(%q) = %v, want nil", bad, got)
		}
	}
}

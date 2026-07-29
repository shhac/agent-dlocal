package api

import (
	"net/http"
	"testing"
	"time"
)

func TestRetryAfterDelayParsing(t *testing.T) {
	future := time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat)
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)

	tests := []struct {
		name  string
		value string
		want  bool // want a positive delay
	}{
		{"seconds", "5", true},
		{"zero", "0", false},
		{"negative", "-3", false},
		{"whitespace", "  ", false},
		{"garbage", "soon", false},
		{"empty", "", false},
		{"http date in the future", future, true},
		{"http date in the past", past, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := retryAfterDelay(tc.value)
			if (got > 0) != tc.want {
				t.Fatalf("retryAfterDelay(%q) = %v, want positive=%v", tc.value, got, tc.want)
			}
		})
	}
}

// The backoff shift overflows int64 around attempt 36. The old guard turned a
// negative duration into a ZERO delay, i.e. an unbounded hot retry loop —
// reachable because --max-retries and `config set max_retries` accept any
// integer.
func TestRetryDelayNeverCollapsesToZeroOrRunsAway(t *testing.T) {
	for _, attempt := range []int{0, 1, 10, 35, 36, 37, 64, 1000} {
		got := retryDelay("", attempt)
		if got <= 0 {
			t.Errorf("retryDelay(attempt=%d) = %v; a non-positive delay is a hot loop", attempt, got)
		}
		if got > 2*maxRetryDelay {
			t.Errorf("retryDelay(attempt=%d) = %v, want it capped near %v", attempt, got, maxRetryDelay)
		}
	}
}

// A server must not be able to park the CLI for a day.
func TestRetryAfterIsCapped(t *testing.T) {
	if got := retryDelay("86400", 0); got > maxRetryDelay {
		t.Fatalf("retryDelay honoured Retry-After: 86400 as %v, want it capped at %v", got, maxRetryDelay)
	}
}

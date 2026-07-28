package api

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var retryBaseDelay = 250 * time.Millisecond

// shouldRetry covers 429 and 5xx. Every command in this CLI is a GET, so a
// retry is always safe — there is no mutation to replay.
func shouldRetry(status, attempt, maxRetries int) bool {
	if attempt >= maxRetries {
		return false
	}
	return status == http.StatusTooManyRequests || status >= 500
}

func retryDelay(retryAfter string, attempt int) time.Duration {
	if parsed := retryAfterDelay(retryAfter); parsed > 0 {
		return parsed
	}
	base := retryBaseDelay * time.Duration(1<<attempt)
	if base <= 0 {
		return 0
	}
	return base + randomJitter(base/2)
}

func retryAfterDelay(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if delay := time.Until(when); delay > 0 {
			return delay
		}
	}
	return 0
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryExhaustedHint(maxRetries int) string {
	if maxRetries <= 0 {
		return "Wait and retry; raise --max-retries to retry automatically"
	}
	return fmt.Sprintf("Retried %d time(s) already; wait longer before retrying", maxRetries)
}

func randomJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return max / 2
	}
	return time.Duration(n.Int64())
}

func nonNegative(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

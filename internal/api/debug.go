package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/shhac/agent-dlocal/internal/output"
)

// redactedHeaders never appear in debug output, whatever --expose says. The
// signature is derived from the secret key, so echoing it hands out an oracle;
// X-Login and X-Trans-Key are bare credentials. Unlike response fields these
// are not user data with a legitimate reason to be revealed, so they are masked
// unconditionally rather than through the expose-aware path.
var redactedHeaders = []string{"Authorization", "Payload-Signature", "X-Login", "X-Trans-Key"}

// SafeHeaders copies h with every credential-bearing value masked.
func SafeHeaders(h http.Header) map[string]string {
	safe := make(map[string]string, len(h))
	for name := range h {
		safe[name] = h.Get(name)
	}
	for _, name := range redactedHeaders {
		if _, ok := safe[http.CanonicalHeaderKey(name)]; ok {
			safe[http.CanonicalHeaderKey(name)] = output.RedactedString
		}
	}
	return safe
}

func (c *Client) logDebug(method, requestURL string, status int, body []byte) {
	entry := map[string]any{
		"@debug": "http",
		"method": method,
		"url":    requestURL,
		"status": status,
		"signer": c.signer.Name(),
	}
	var parsed any
	if json.Unmarshal(body, &parsed) == nil {
		entry["body"] = output.Redact(parsed, c.redaction)
	} else {
		entry["body_raw"] = output.RedactedString
	}
	writeDebug(entry)
}

func (c *Client) logRetry(method, requestURL string, status, attempt, maxRetries int, delay time.Duration) {
	writeDebug(map[string]any{
		"@debug":      "retry",
		"method":      method,
		"url":         requestURL,
		"status":      status,
		"attempt":     attempt,
		"max_retries": maxRetries,
		"delay_ms":    delay.Milliseconds(),
	})
}

func writeDebug(entry map[string]any) {
	enc := json.NewEncoder(output.Stderr())
	enc.SetEscapeHTML(false)
	_ = enc.Encode(entry)
}

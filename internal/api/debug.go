package api

import (
	"encoding/json"
	"time"

	"github.com/shhac/agent-dlocal/internal/output"
)

func (c *Client) logDebug(method, requestURL string, status int, body []byte) {
	entry := map[string]any{
		"@debug": "http",
		"method": method,
		"url":    requestURL,
		"status": status,
		"signer": SignatureScheme,
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

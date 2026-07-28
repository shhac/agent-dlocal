package api

import (
	"encoding/json"
	"fmt"
	"strings"

	agenterrors "github.com/shhac/agent-dlocal/internal/errors"
)

// dLocal error bodies are {"code": <int>, "message": "..."}; some endpoints
// instead return the status triple. Both shapes are decoded so the hint can
// carry whichever the endpoint chose to send.
type dlocalError struct {
	Code         int    `json:"code"`
	Message      string `json:"message"`
	Status       string `json:"status"`
	StatusCode   string `json:"status_code"`
	StatusDetail string `json:"status_detail"`
}

func extractError(status int, body []byte) (message string, code string) {
	var parsed dlocalError
	if err := json.Unmarshal(body, &parsed); err != nil {
		if len(body) > 0 && len(body) <= 200 {
			return fmt.Sprintf("HTTP %d: %s", status, strings.TrimSpace(string(body))), ""
		}
		return fmt.Sprintf("HTTP %d", status), ""
	}

	message = parsed.Message
	if message == "" {
		message = parsed.StatusDetail
	}
	if message == "" {
		message = fmt.Sprintf("HTTP %d", status)
	}

	switch {
	case parsed.Code != 0:
		code = fmt.Sprintf("%d", parsed.Code)
	case parsed.StatusCode != "":
		code = parsed.StatusCode
	}
	return message, code
}

func classifyHTTPError(status, maxRetries int, body []byte) *agenterrors.APIError {
	message, code := extractError(status, body)

	var hints []string
	if code != "" {
		hints = append(hints, "dLocal code: "+code)
	}

	switch {
	case status == 401:
		// X-Date is INSIDE the signed message, so a drifted clock produces a
		// well-formed signature that dLocal rejects. It is the most confusing
		// failure mode in this API, and naming it here saves the agent from
		// rediscovering it by trial and error.
		return withHint(agenterrors.New("Authentication failed: "+message, agenterrors.FixableByHuman),
			append(hints,
				"Three usual causes: wrong X-Login/X-Trans-Key/secret key; clock skew (X-Date is part of the signed message, so a drifted system clock invalidates an otherwise-correct signature); or the request body changing between signing and sending",
				"Verify the stored credentials with 'agent-dlocal auth check'")...)
	case status == 403:
		return withHint(agenterrors.New("Permission denied: "+message, agenterrors.FixableByHuman),
			append(hints, "The merchant may not be enabled for this country, currency, or payment method, or the credential lacks access to this API")...)
	case status == 404:
		return withHint(agenterrors.New("Not found: "+message, agenterrors.FixableByAgent),
			append(hints,
				"Check the id and the environment — live and sandbox are separate ledgers, so a sandbox id never resolves against live",
				"For 'payments status', note dLocal only serves status within 12 months of the payment's creation date")...)
	case status == 429:
		return withHint(agenterrors.New("Rate limited: "+message, agenterrors.FixableByRetry),
			append(hints, retryExhaustedHint(maxRetries))...)
	case status >= 500:
		return withHint(agenterrors.New("dLocal API error: "+message, agenterrors.FixableByRetry),
			append(hints, "dLocal returned a server error; retry later")...)
	default:
		return withHint(agenterrors.New(message, agenterrors.FixableByAgent), hints...)
	}
}

func withHint(err *agenterrors.APIError, hints ...string) *agenterrors.APIError {
	parts := make([]string, 0, len(hints))
	for _, hint := range hints {
		if hint != "" {
			parts = append(parts, hint)
		}
	}
	if len(parts) > 0 {
		err = err.WithHint(strings.Join(parts, "; "))
	}
	return err
}

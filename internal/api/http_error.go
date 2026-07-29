package api

import (
	"encoding/json"
	"fmt"
	"strings"

	agenterrors "github.com/shhac/agent-dlocal/internal/errors"
)

// dLocal error codes, as observed against the live sandbox rather than inferred
// from the docs. The HTTP status alone is not enough to classify a failure —
// a bad signature is a 400, not a 401 — so the CODE drives the classification
// and the status is only a fallback.
const (
	codeInvalidCredentials = 3001 // 403 — login/trans-key rejected, or caller IP not allowlisted
	codePaymentNotFound    = 4000 // 404 — also used for orders and chargebacks
	codeRefundNotFound     = 4001 // 404
	codeSignatureMismatch  = 5000 // 400 — the digest did not match
	codeInvalidParameter   = 5001 // 400 — missing or malformed parameter/header
)

// dLocal error bodies are {"code", "message"} and sometimes carry "param"
// naming the offending field. Some endpoints instead return the status triple.
type dlocalError struct {
	Code         int    `json:"code"`
	Message      string `json:"message"`
	Param        string `json:"param"`
	Status       string `json:"status"`
	StatusCode   string `json:"status_code"`
	StatusDetail string `json:"status_detail"`
}

func extractError(status int, body []byte) (parsed dlocalError, message string) {
	if err := json.Unmarshal(body, &parsed); err != nil {
		// Not every dLocal response is JSON: an unrouted path returns the bare
		// string NOT_FOUND, so a parse failure is expected rather than exotic.
		trimmed := strings.TrimSpace(string(body))
		if trimmed != "" && len(trimmed) <= 200 {
			return dlocalError{}, fmt.Sprintf("HTTP %d: %s", status, trimmed)
		}
		return dlocalError{}, fmt.Sprintf("HTTP %d", status)
	}

	message = parsed.Message
	if message == "" {
		message = parsed.StatusDetail
	}
	if message == "" {
		message = fmt.Sprintf("HTTP %d", status)
	}
	if parsed.Param != "" {
		message += " (param: " + parsed.Param + ")"
	}
	return parsed, message
}

func classifyHTTPError(status, maxRetries int, body []byte) *agenterrors.APIError {
	parsed, message := extractError(status, body)

	var hints []string
	switch {
	case parsed.Code != 0:
		hints = append(hints, fmt.Sprintf("dLocal code: %d", parsed.Code))
	case parsed.StatusCode != "":
		hints = append(hints, "dLocal code: "+parsed.StatusCode)
	}

	// Classify on the dLocal code where there is one — it is more precise than
	// the HTTP status and does not always agree with it.
	switch parsed.Code {
	case codeSignatureMismatch:
		return withHint(agenterrors.New("Signature rejected: "+message, agenterrors.FixableByHuman),
			append(hints,
				"dLocal recomputed HMAC-SHA256(secret, X-Login + X-Date + body) and got a different digest. The stored secret key is the usual cause — check it was copied in full with 'agent-dlocal auth update <profile> --form'",
				"Note the X-Date is signed as well as sent, so a skewed system clock does NOT cause this: the signature stays self-consistent")...)

	case codeInvalidCredentials:
		return withHint(agenterrors.New("Credentials rejected: "+message, agenterrors.FixableByHuman),
			append(hints,
				"dLocal returns this before checking the signature, so it means the caller was rejected outright. Check, in order: (1) is this machine's public IP on the dashboard's IP Whitelist for this product and environment? (2) does the profile point at the right host — sandbox credentials fail against live and vice versa, see 'agent-dlocal auth list'; (3) is the X-Login correct?")...)

	case codeInvalidParameter:
		hint := "A required parameter or header was missing or malformed"
		if parsed.Param != "" {
			hint = fmt.Sprintf("dLocal rejected the %q parameter as missing or malformed", parsed.Param)
		}
		return withHint(agenterrors.New(message, agenterrors.FixableByAgent), append(hints, hint)...)

	case codePaymentNotFound, codeRefundNotFound:
		return withHint(agenterrors.New("Not found: "+message, agenterrors.FixableByAgent),
			append(hints, notFoundHint())...)
	}

	// No recognized code — fall back to the HTTP status.
	switch {
	case status == 401 || status == 403:
		return withHint(agenterrors.New("Authentication failed: "+message, agenterrors.FixableByHuman),
			append(hints, "Verify the profile with 'agent-dlocal auth check'")...)
	case status == 404:
		return withHint(agenterrors.New("Not found: "+message, agenterrors.FixableByAgent),
			append(hints, notFoundHint())...)
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

func notFoundHint() string {
	return "Check the id and the environment — live and sandbox are separate ledgers, so an id from one never resolves against the other. " +
		"For 'payments status', dLocal only serves status within 12 months of the payment's creation date."
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

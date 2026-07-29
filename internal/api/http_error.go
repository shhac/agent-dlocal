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
	// 5000 is overloaded: "Signature not match" AND "Invalid request" for a
	// malformed path both use it, so the message has to disambiguate.
	codeSignatureOrBadRequest = 5000 // 400
	codeInvalidParameter      = 5001 // 400 — missing or malformed parameter/header
	codeCountryNotSupported   = 5003 // 400 — unknown or empty country
)

// dLocal error bodies are {"code", "message"} plus a key naming the offending
// field. The two APIs disagree on both halves: payins returns a NUMERIC code
// with "param", payouts returns a STRING code ("payout_not_found_id") with
// "field". A struct typed for one silently fails to parse the other, losing the
// message, so code is decoded leniently and both field names are accepted.
type dlocalError struct {
	Code         flexibleCode `json:"code"`
	Message      string       `json:"message"`
	Param        string       `json:"param"`
	Field        string       `json:"field"`
	Status       string       `json:"status"`
	StatusCode   string       `json:"status_code"`
	StatusDetail string       `json:"status_detail"`
}

// offender names the rejected field, whichever key the API used for it.
func (e dlocalError) offender() string {
	if e.Param != "" {
		return e.Param
	}
	return e.Field
}

// flexibleCode holds a dLocal error code that may arrive as a number (payins)
// or a string (payouts).
type flexibleCode struct {
	Number int
	Text   string
}

func (c *flexibleCode) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &c.Number); err == nil {
		return nil
	}
	// A non-numeric code is not an error — it is the payouts API's shape.
	return json.Unmarshal(data, &c.Text)
}

func (c flexibleCode) empty() bool { return c.Number == 0 && c.Text == "" }

func (c flexibleCode) String() string {
	if c.Text != "" {
		return c.Text
	}
	return fmt.Sprintf("%d", c.Number)
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
	if offender := parsed.offender(); offender != "" {
		message += " (param: " + offender + ")"
	}
	return parsed, message
}

func classifyHTTPError(status, maxRetries int, body []byte) *agenterrors.APIError {
	parsed, message := extractError(status, body)

	var hints []string
	switch {
	case !parsed.Code.empty():
		hints = append(hints, "dLocal code: "+parsed.Code.String())
	case parsed.StatusCode != "":
		hints = append(hints, "dLocal code: "+parsed.StatusCode)
	}

	// The payouts API uses string codes for the same conditions payins numbers.
	// Mapping them here keeps one classification table rather than two.
	switch parsed.Code.Text {
	case "authentication_failed":
		return withHint(agenterrors.New("Signature rejected: "+message, agenterrors.FixableByHuman),
			append(hints, "The payouts host verified the signature and rejected it — check the stored secret key")...)
	case "invalid_credentials", "unauthorized_internal_url_restricted":
		return withHint(agenterrors.New("Credentials rejected: "+message, agenterrors.FixableByHuman),
			append(hints, "The payouts host rejected the caller outright. Check the credential covers the Payouts product, and that the path exists on this host")...)
	}
	if strings.HasSuffix(parsed.Code.Text, "_not_found") || strings.Contains(parsed.Code.Text, "not_found") {
		return withHint(agenterrors.New("Not found: "+message, agenterrors.FixableByAgent),
			append(hints, notFoundHint())...)
	}

	// Classify on the numeric payins code where there is one — it is more
	// precise than the HTTP status and does not always agree with it.
	switch parsed.Code.Number {
	case codeSignatureOrBadRequest:
		// Telling someone to re-check their secret key because they typo'd a
		// path would be worse than saying nothing, so the two meanings of 5000
		// are separated on the message.
		if !strings.Contains(strings.ToLower(message), "signature") {
			return withHint(agenterrors.New("Bad request: "+message, agenterrors.FixableByAgent),
				append(hints, "dLocal rejected the request shape itself — usually a malformed or empty path segment, e.g. an id that resolved to nothing")...)
		}
		return withHint(agenterrors.New("Signature rejected: "+message, agenterrors.FixableByHuman),
			append(hints,
				"dLocal recomputed HMAC-SHA256(secret, X-Login + X-Date + body) and got a different digest. The stored secret key is the usual cause — check it was copied in full with 'agent-dlocal auth update <profile> --form'",
				"Note the X-Date is signed as well as sent, so a skewed system clock does NOT cause this: the signature stays self-consistent")...)

	case codeCountryNotSupported:
		return withHint(agenterrors.New(message, agenterrors.FixableByAgent),
			append(hints, "Pass a supported ISO 3166-1 alpha-2 code (two letters, e.g. BR, MX, CO). An empty or three-letter code lands here too")...)

	case codeInvalidCredentials:
		return withHint(agenterrors.New("Credentials rejected: "+message, agenterrors.FixableByHuman),
			append(hints,
				"dLocal returns this before checking the signature, so it means the caller was rejected outright. Check, in order: (1) is this machine's public IP on the dashboard's IP Whitelist for this product and environment? (2) does the profile point at the right host — sandbox credentials fail against live and vice versa, see 'agent-dlocal auth list'; (3) is the X-Login correct?")...)

	case codeInvalidParameter:
		hint := "A required parameter or header was missing or malformed"
		if offender := parsed.offender(); offender != "" {
			hint = fmt.Sprintf("dLocal rejected the %q parameter as missing or malformed", offender)
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

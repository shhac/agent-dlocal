package api

import (
	"strings"
	"testing"

	agenterrors "github.com/shhac/agent-dlocal/internal/errors"
)

// dLocal overloads code 5000: it means both "Signature not match" and
// "Invalid request" for a malformed path. Telling someone to re-check their
// secret key because they typo'd a URL would be worse than saying nothing.
func TestCode5000IsDisambiguatedByMessage(t *testing.T) {
	badPath := classifyHTTPError(400, 0, []byte(`{"code":5000,"message":"Invalid request"}`))
	if badPath.FixableBy != agenterrors.FixableByAgent {
		t.Errorf("Invalid request: fixable_by = %q, want agent", badPath.FixableBy)
	}
	if strings.Contains(badPath.Hint, "secret key") {
		t.Errorf("a malformed path should not send the reader to their secret key:\n%s", badPath.Hint)
	}

	badSig := classifyHTTPError(400, 0, []byte(`{"code":5000,"message":"Signature not match"}`))
	if badSig.FixableBy != agenterrors.FixableByHuman {
		t.Errorf("Signature not match: fixable_by = %q, want human", badSig.FixableBy)
	}
	if !strings.Contains(badSig.Hint, "secret key") {
		t.Errorf("a signature failure should point at the secret key:\n%s", badSig.Hint)
	}
}

func TestCountryNotSupportedIsAgentFixable(t *testing.T) {
	err := classifyHTTPError(400, 0, []byte(`{"code":5003,"message":"Country not supported"}`))

	if err.FixableBy != agenterrors.FixableByAgent {
		t.Errorf("fixable_by = %q, want agent", err.FixableBy)
	}
	if !strings.Contains(err.Hint, "alpha-2") {
		t.Errorf("hint should say what a valid country code looks like:\n%s", err.Hint)
	}
}

// The payouts API's string codes must classify alongside the payins numeric
// ones rather than falling through to a bare HTTP-status guess.
func TestPayoutsStringCodesClassify(t *testing.T) {
	for body, want := range map[string]agenterrors.FixableBy{
		`{"code":"authentication_failed","message":"Authentication failed"}`:       agenterrors.FixableByHuman,
		`{"code":"invalid_credentials","message":"invalid credentials"}`:           agenterrors.FixableByHuman,
		`{"code":"payout_not_found_id","message":"not found","field":"payout_id"}`: agenterrors.FixableByAgent,
	} {
		got := classifyHTTPError(403, 0, []byte(body))
		if got.FixableBy != want {
			t.Errorf("%s -> fixable_by %q, want %q", body, got.FixableBy, want)
		}
	}
}

// A payouts body names the offending field under "field", not "param".
func TestFieldKeyIsSurfacedLikeParam(t *testing.T) {
	err := classifyHTTPError(404, 0, []byte(`{"code":"payout_not_found_id","message":"not found","field":"payout_id"}`))

	if !strings.Contains(err.Error(), "payout_id") {
		t.Errorf("the \"field\" key was dropped: %s", err.Error())
	}
}

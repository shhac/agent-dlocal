package api

import (
	"net/http"
	"testing"
	"time"
)

// The known-good vector from dLocal's own docs: the "Make an authenticated GET
// request" example in https://docs.dlocal.com/docs/initial-settings pins
// x_login, the date, the secret, and an empty body. Reproducing dLocal's own
// published inputs is the only way to prove the message construction matches
// theirs rather than merely matching itself.
const (
	docsLogin  = "1955gdod"
	docsDate   = "2022-11-24T15:42:57.130Z"
	docsSecret = "secretKey123"
)

func TestPayinsSignatureMatchesDocsVector(t *testing.T) {
	// hex(HMAC_SHA256("secretKey123", "1955gdod" + "2022-11-24T15:42:57.130Z" + ""))
	got := hmacHex(docsSecret, []byte(docsLogin+docsDate))
	// Computed independently of this package:
	//   printf '%s' '1955gdod2022-11-24T15:42:57.130Z' \
	//     | openssl dgst -sha256 -hmac 'secretKey123' -hex
	const want = "98211fe059efc3606b831a1c30a3c6d7448e598b515a15456ae9ebc157314aa2"

	if got != want {
		t.Fatalf("payins signature over the docs vector = %s, want %s", got, want)
	}
}

func TestPayinsSignerSignsLoginDateBody(t *testing.T) {
	signer := PayinsSigner{
		Creds:     Credentials{Login: docsLogin, TransKey: "j81xh5", SecretKey: docsSecret},
		UserAgent: "agent-dlocal/test",
	}
	header := http.Header{}
	body := []byte(`{"amount":100}`)
	now := time.Date(2022, 11, 24, 15, 42, 57, 130_000_000, time.UTC)

	signer.Apply(header, body, now)

	if got := header.Get("X-Date"); got != docsDate {
		t.Fatalf("X-Date = %q, want %q", got, docsDate)
	}
	if got := header.Get("X-Version"); got != "2.1" {
		t.Fatalf("X-Version = %q, want 2.1", got)
	}
	want := "V2-HMAC-SHA256, Signature: " + hmacHex(docsSecret, []byte(docsLogin+docsDate+string(body)))
	if got := header.Get("Authorization"); got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
}

// Payouts use the SAME signer as payins — only the host differs. Verified
// against the live sandbox: the payouts host rejects Payload-Signature with
// 401 invalid_credentials and accepts this Authorization header (404
// payout_not_found_id, i.e. auth passed), while a corrupted signature gets
// 403 authentication_failed.
//
// This test exists to stop a well-meaning reader reinstating a Payload-Signature
// signer from the documentation.
func TestNoPayloadSignatureHeaderIsEverSent(t *testing.T) {
	header := http.Header{}
	PayinsSigner{Creds: Credentials{Login: docsLogin, SecretKey: docsSecret}, UserAgent: "agent-dlocal/test"}.
		Apply(header, []byte(`{"amount":100}`), time.Now())

	if header.Get("Payload-Signature") != "" {
		t.Fatal("a Payload-Signature header was set; the payouts host rejects that scheme with 401")
	}
	if header.Get("Authorization") == "" {
		t.Fatal("no Authorization header was set; that is the scheme both hosts accept")
	}
}

func TestFormatDateIsISO8601MillisUTC(t *testing.T) {
	// A non-UTC input must still render as UTC with a literal Z — Go's layout
	// treats "Z07:00" as an offset token, so a bare Z is easy to get wrong.
	zone := time.FixedZone("BRT", -3*60*60)
	got := FormatDate(time.Date(2026, 7, 29, 12, 0, 0, 500_000_000, zone))

	if want := "2026-07-29T15:00:00.500Z"; got != want {
		t.Fatalf("FormatDate = %q, want %q", got, want)
	}
}

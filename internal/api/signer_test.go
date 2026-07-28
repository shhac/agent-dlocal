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

// Payouts v2 hashes the payload ALONE. If someone "unifies" the two signers,
// this is the test that fails.
func TestPayoutsSignerSignsBodyOnly(t *testing.T) {
	creds := Credentials{Login: docsLogin, TransKey: "j81xh5", SecretKey: docsSecret}
	body := []byte(`{"amount":100}`)
	now := time.Date(2022, 11, 24, 15, 42, 57, 130_000_000, time.UTC)

	header := http.Header{}
	PayoutsSigner{Creds: creds, UserAgent: "agent-dlocal/test"}.Apply(header, body, now)

	if got, want := header.Get("Payload-Signature"), hmacHex(docsSecret, body); got != want {
		t.Fatalf("Payload-Signature = %q, want %q (HMAC over the body alone)", got, want)
	}
	if header.Get("Authorization") != "" {
		t.Fatal("payouts signer set an Authorization header; that is the payins scheme")
	}

	payinsHeader := http.Header{}
	PayinsSigner{Creds: creds, UserAgent: "agent-dlocal/test"}.Apply(payinsHeader, body, now)
	if payinsHeader.Get("Payload-Signature") != "" {
		t.Fatal("payins signer set a Payload-Signature header; that is the payouts scheme")
	}
}

// The two schemes must not coincidentally agree — if they did, a signer mix-up
// would be invisible in every other test.
func TestSignersProduceDifferentDigests(t *testing.T) {
	creds := Credentials{Login: docsLogin, TransKey: "j81xh5", SecretKey: docsSecret}
	body := []byte(`{"amount":100}`)
	now := time.Date(2022, 11, 24, 15, 42, 57, 130_000_000, time.UTC)

	payins, payouts := http.Header{}, http.Header{}
	PayinsSigner{Creds: creds}.Apply(payins, body, now)
	PayoutsSigner{Creds: creds}.Apply(payouts, body, now)

	payinsDigest := payins.Get("Authorization")[len("V2-HMAC-SHA256, Signature: "):]
	if payinsDigest == payouts.Get("Payload-Signature") {
		t.Fatal("payins and payouts digests are identical; the signed messages are supposed to differ")
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

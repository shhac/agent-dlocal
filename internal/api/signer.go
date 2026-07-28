package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"
)

// DateFormat is dLocal's X-Date: ISO-8601 with milliseconds, in UTC.
//
// The trailing Z is appended rather than written into the layout: Go's
// reference layout treats "Z07:00"/"Z0700" as offset tokens, and relying on a
// bare "Z" being literal is the kind of subtlety that produces a header that
// looks right and signs wrong.
const dateLayout = "2006-01-02T15:04:05.000"

// Credentials are the three secrets a signer needs. The key passphrase is not
// here: it belongs to the TLS layer, not the signing layer.
type Credentials struct {
	Login     string
	TransKey  string
	SecretKey string
}

// Signer applies the auth headers for one dLocal API family.
//
// Payins and payouts v2 are separate implementations rather than one signer
// with a flag, because they do not merely put the same digest in a different
// header — they hash DIFFERENT MESSAGES. Payins signs login+date+body; payouts
// v2 signs the body alone. Collapsing them into one type invites a boolean that
// silently produces a valid-looking signature over the wrong input.
type Signer interface {
	// Apply sets the auth headers for a request carrying exactly body.
	Apply(header http.Header, body []byte, now time.Time)
	// Name identifies the scheme in debug output.
	Name() string
}

func FormatDate(now time.Time) string {
	return now.UTC().Format(dateLayout) + "Z"
}

func hmacHex(secret string, message []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(message)
	return hex.EncodeToString(mac.Sum(nil))
}

// PayinsSigner implements dLocal's V2-HMAC-SHA256 scheme:
//
//	signature = hex_lower(HMAC_SHA256(secretKey, X-Login || X-Date || body))
//
// carried in Authorization as "V2-HMAC-SHA256, Signature: <hex>".
type PayinsSigner struct {
	Creds     Credentials
	UserAgent string
}

func (s PayinsSigner) Name() string { return "V2-HMAC-SHA256" }

func (s PayinsSigner) Apply(header http.Header, body []byte, now time.Time) {
	date := FormatDate(now)

	header.Set("X-Login", s.Creds.Login)
	header.Set("X-Trans-Key", s.Creds.TransKey)
	header.Set("X-Date", date)
	header.Set("X-Version", apiVersion)
	header.Set("User-Agent", s.UserAgent)
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "application/json")

	message := make([]byte, 0, len(s.Creds.Login)+len(date)+len(body))
	message = append(message, s.Creds.Login...)
	message = append(message, date...)
	message = append(message, body...)

	header.Set("Authorization", "V2-HMAC-SHA256, Signature: "+hmacHex(s.Creds.SecretKey, message))
}

// PayoutsSigner implements the Payouts v2 scheme:
//
//	signature = hex_lower(HMAC_SHA256(secretKey, body))
//
// carried in Payload-Signature. Note the message is the body ALONE — login and
// date are not part of it, unlike payins.
//
// For a GET the body is empty, so the digest is constant for a given secret.
// That is what the documentation specifies; it is recorded here because it
// looks like a bug and someone will otherwise "fix" it.
type PayoutsSigner struct {
	Creds     Credentials
	UserAgent string
}

func (s PayoutsSigner) Name() string { return "Payload-Signature" }

func (s PayoutsSigner) Apply(header http.Header, body []byte, now time.Time) {
	header.Set("X-Login", s.Creds.Login)
	header.Set("X-Trans-Key", s.Creds.TransKey)
	header.Set("X-Date", FormatDate(now))
	header.Set("X-Version", apiVersion)
	header.Set("User-Agent", s.UserAgent)
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "application/json")

	header.Set("Payload-Signature", hmacHex(s.Creds.SecretKey, body))
}

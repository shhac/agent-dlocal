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

// Credentials are the three secrets used to sign a request. The key passphrase
// is not here: it belongs to the TLS layer, not the signing layer.
type Credentials struct {
	Login     string
	TransKey  string
	SecretKey string
}

// SignatureScheme is the value dLocal expects in the Authorization header, and
// the name reported in debug output and by `auth check`.
const SignatureScheme = "V2-HMAC-SHA256"

func FormatDate(now time.Time) string {
	return now.UTC().Format(dateLayout) + "Z"
}

func hmacHex(secret string, message []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(message)
	return hex.EncodeToString(mac.Sum(nil))
}

// sign applies dLocal's V2-HMAC-SHA256 scheme:
//
//	signature = hex_lower(HMAC_SHA256(secretKey, X-Login || X-Date || body))
//
// carried in Authorization. Both the payins and the payouts host accept it —
// there is only one scheme.
//
// An earlier version modelled this as a Signer interface with two
// implementations, the second being the Payload-Signature scheme the
// documentation describes for Payouts v2. Live testing disproved it: the
// payouts host rejects Payload-Signature with 401 invalid_credentials and
// accepts this header (404 payout_not_found_id, i.e. auth passed), while a
// corrupted digest gets 403 authentication_failed — so the signature is
// genuinely verified. Payouts differ from payins by HOST only.
//
// The interface outlived its second implementation, justified on a hypothetical
// Payouts v3. That is not the seam v3 would need: OAuth2 bearer tokens require a
// token acquisition and refresh story, not a different Apply(header, body, now).
func (c *Client) sign(header http.Header, body []byte, now time.Time) {
	date := FormatDate(now)

	header.Set("X-Login", c.creds.Login)
	header.Set("X-Trans-Key", c.creds.TransKey)
	header.Set("X-Date", date)
	header.Set("X-Version", apiVersion)
	header.Set("User-Agent", c.userAgent)
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "application/json")

	message := make([]byte, 0, len(c.creds.Login)+len(date)+len(body))
	message = append(message, c.creds.Login...)
	message = append(message, date...)
	message = append(message, body...)

	header.Set("Authorization", SignatureScheme+", Signature: "+hmacHex(c.creds.SecretKey, message))
}

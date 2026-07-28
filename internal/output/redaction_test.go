package output

import (
	"encoding/json"
	"strings"
	"testing"
)

func redactToJSON(t *testing.T, payload string, expose ...string) string {
	t.Helper()
	var data any
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	redacted := Redact(data, RedactionOptions{Expose: expose})
	encoded, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("marshal redacted: %v", err)
	}
	return string(encoded)
}

// A dLocal payment carries a national ID number in payer.document. That, and
// the rest of the payer block, must never reach a transcript by default.
const paymentFixture = `{
  "id": "D-4-abc",
  "status": "REJECTED",
  "status_code": "300",
  "status_detail": "The payment was rejected",
  "amount": 285,
  "currency": "BRL",
  "country": "BR",
  "payer": {
    "name": "Thiago Gabriel",
    "email": "thiago@example.com",
    "document": "53033315550",
    "user_reference": "12345",
    "ip": "203.0.113.7",
    "device_id": "2fg3d4gf234",
    "address": {"state": "Rio de Janeiro", "city": "Volta Redonda", "zip_code": "27275-595", "street": "Servidao B-1", "number": "1106"}
  },
  "card": {"holder_name": "Thiago Gabriel", "number": "4111111111111111", "cvv": "123", "brand": "VI", "last4": "1111", "bin": "411111"}
}`

func TestRedactMasksPayerPII(t *testing.T) {
	got := redactToJSON(t, paymentFixture)

	for _, secret := range []string{
		"Thiago Gabriel", "thiago@example.com", "53033315550", "12345",
		"203.0.113.7", "2fg3d4gf234", "Volta Redonda", "27275-595",
		"Servidao B-1", "4111111111111111", "123",
	} {
		if strings.Contains(got, secret) {
			t.Fatalf("redacted output leaked %q:\n%s", secret, got)
		}
	}
}

// Triage runs on the status triple and the card's non-identifying descriptors.
// Over-redacting these would make the tool useless.
func TestRedactKeepsTriageFields(t *testing.T) {
	got := redactToJSON(t, paymentFixture)

	for _, keep := range []string{"REJECTED", "300", "The payment was rejected", "BRL", "VI", "1111", "411111", "D-4-abc"} {
		if !strings.Contains(got, keep) {
			t.Fatalf("redacted output dropped triage field %q:\n%s", keep, got)
		}
	}
}

// "name" is a person under payer/card, but a product under payments-methods.
// A blanket rule on the key would blank out every payment method's name.
func TestRedactKeepsPaymentMethodName(t *testing.T) {
	got := redactToJSON(t, `[{"id": "OS", "type": "WALLET", "name": "Smart Pix", "details": {"banks": [{"id": "1", "name": "Banco do Brasil S.A"}]}}]`)

	for _, keep := range []string{"Smart Pix", "Banco do Brasil S.A"} {
		if !strings.Contains(got, keep) {
			t.Fatalf("payment-method name was redacted as if it were PII:\n%s", got)
		}
	}
}

// The signature is derived from the secret key; echoing it into a debug
// transcript hands out an oracle.
func TestRedactMasksSigningMaterial(t *testing.T) {
	got := redactToJSON(t, `{"headers": {"Authorization": "V2-HMAC-SHA256, Signature: deadbeef", "X-Trans-Key": "fm12O7G9", "X-Login": "sak223k2wdksdl2"}, "payload_signature": "cafebabe"}`)

	for _, secret := range []string{"deadbeef", "fm12O7G9", "sak223k2wdksdl2", "cafebabe"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redacted output leaked signing material %q:\n%s", secret, got)
		}
	}
}

func TestExposeOptsOutPerField(t *testing.T) {
	got := redactToJSON(t, paymentFixture, "payer.email")

	if !strings.Contains(got, "thiago@example.com") {
		t.Fatalf("--expose payer.email did not reveal the field:\n%s", got)
	}
	if strings.Contains(got, "53033315550") {
		t.Fatalf("--expose payer.email also revealed payer.document:\n%s", got)
	}
}

func TestParseExposeNormalizesAndDedupes(t *testing.T) {
	got := ParseExpose([]string{"Payer.Email, payer.document", "payer.email", ".payer.ip."})
	want := "payer.email,payer.document,payer.ip"
	if strings.Join(got, ",") != want {
		t.Fatalf("ParseExpose = %v, want %s", got, want)
	}
}

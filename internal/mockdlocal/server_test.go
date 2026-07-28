package mockdlocal

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func signedRequest(t *testing.T, method, target string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	date := time.Now().UTC().Format("2006-01-02T15:04:05.000") + "Z"

	req.Header.Set("X-Login", DefaultLogin)
	req.Header.Set("X-Trans-Key", DefaultTransKey)
	req.Header.Set("X-Date", date)
	req.Header.Set("X-Version", "2.1")

	mac := hmac.New(sha256.New, []byte(DefaultSecretKey))
	mac.Write([]byte(DefaultLogin + date))
	req.Header.Set("Authorization", authPrefix+hex.EncodeToString(mac.Sum(nil)))
	return req
}

func do(t *testing.T, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	NewServer().ServeHTTP(rec, req)
	return rec
}

func TestCorrectlySignedRequestSucceeds(t *testing.T) {
	rec := do(t, signedRequest(t, http.MethodGet, "/payments/D-4-paid"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body)
	}
	var payment map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payment); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if payment["status"] != "PAID" || payment["status_code"] != "200" {
		t.Fatalf("fixture did not match the -paid suffix: %v", payment)
	}
}

// The mock's whole reason to exist: a wrong signature must be rejected, or a
// signing bug in the client would sail through every e2e test.
func TestWrongSignatureIsRejected(t *testing.T) {
	req := signedRequest(t, http.MethodGet, "/payments/D-4-paid")
	req.Header.Set("Authorization", authPrefix+strings.Repeat("0", 64))

	rec := do(t, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "signature mismatch") {
		t.Fatalf("401 body should distinguish a mismatch from a missing header:\n%s", rec.Body)
	}
}

func TestMissingHeaderNamesTheHeader(t *testing.T) {
	for _, header := range []string{"X-Login", "X-Trans-Key", "X-Date", "Authorization"} {
		req := signedRequest(t, http.MethodGet, "/payments/D-4-paid")
		req.Header.Del(header)

		rec := do(t, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s removed: status = %d, want 401", header, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), header) {
			t.Errorf("401 for a missing %s did not name it:\n%s", header, rec.Body)
		}
	}
}

// The real API folds X-Date into the signed message, so a drifted clock is a
// distinct failure. Reproducing it here is what makes the CLI's clock-skew hint
// testable.
func TestClockSkewIsRejected(t *testing.T) {
	stale := time.Now().Add(-time.Hour).UTC().Format("2006-01-02T15:04:05.000") + "Z"

	req := httptest.NewRequest(http.MethodGet, "/payments/D-4-paid", nil)
	req.Header.Set("X-Login", DefaultLogin)
	req.Header.Set("X-Trans-Key", DefaultTransKey)
	req.Header.Set("X-Date", stale)
	mac := hmac.New(sha256.New, []byte(DefaultSecretKey))
	mac.Write([]byte(DefaultLogin + stale))
	req.Header.Set("Authorization", authPrefix+hex.EncodeToString(mac.Sum(nil)))

	rec := do(t, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a stale X-Date", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "clock skew") {
		t.Fatalf("401 body should name clock skew:\n%s", rec.Body)
	}
}

// A payouts request signed the payins way (or vice versa) must fail, so the two
// signers cannot be confused without a test noticing.
func TestPayoutsRejectsThePayinsScheme(t *testing.T) {
	rec := do(t, signedRequest(t, http.MethodGet, "/v2/payouts/P-1-paid"))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a payins-signed payouts request", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "payins scheme") {
		t.Fatalf("401 body should name the scheme mix-up:\n%s", rec.Body)
	}
}

func TestPayoutsAcceptsPayloadSignature(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v2/payouts/P-1-delivered", nil)
	date := time.Now().UTC().Format("2006-01-02T15:04:05.000") + "Z"
	req.Header.Set("X-Login", DefaultLogin)
	req.Header.Set("X-Trans-Key", DefaultTransKey)
	req.Header.Set("X-Date", date)

	// Payouts v2 hashes the body ALONE — here, the empty string.
	mac := hmac.New(sha256.New, []byte(DefaultSecretKey))
	req.Header.Set("Payload-Signature", hex.EncodeToString(mac.Sum(nil)))

	rec := do(t, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body)
	}
	var payout map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &payout)
	if payout["status"] != "DELIVERED" || payout["status_code"] != "500" {
		t.Fatalf("payout fixture did not match the -delivered suffix: %v", payout)
	}
}

func TestUnknownIDReturnsDLocalShaped404(t *testing.T) {
	rec := do(t, signedRequest(t, http.MethodGet, "/payments/D-4-nosuchthing"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("404 body is not JSON: %v", err)
	}
	if _, ok := body["code"]; !ok {
		t.Fatalf("404 is not in dLocal's {code,message} shape: %v", body)
	}
}

func TestRouteInventoryIsUnauthenticated(t *testing.T) {
	rec := do(t, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200 without credentials", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "/payments/{id}") {
		t.Fatalf("route inventory is missing routes:\n%s", rec.Body)
	}
}

func TestPaymentStatusReturnsOnlyTheTriple(t *testing.T) {
	rec := do(t, signedRequest(t, http.MethodGet, "/payments/D-4-rejected/status"))

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if body["status"] != "REJECTED" {
		t.Fatalf("status = %v, want REJECTED", body["status"])
	}
	if _, ok := body["payer"]; ok {
		t.Fatal("the status endpoint returned a payer block; it should be the triple only")
	}
}

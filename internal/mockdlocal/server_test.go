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
//
// Note the status: the real API answers a bad signature with 400/5000, NOT a
// 401. An earlier version of this mock guessed 401 and was wrong.
func TestWrongSignatureIsRejected(t *testing.T) {
	req := signedRequest(t, http.MethodGet, "/payments/D-4-paid")
	req.Header.Set("Authorization", authPrefix+strings.Repeat("0", 64))

	rec := do(t, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (dLocal answers a bad signature with 400/5000)", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != float64(codeSignatureMismatch) {
		t.Fatalf("code = %v, want %d", body["code"], codeSignatureMismatch)
	}
}

// A missing header is a 400 "Invalid parameter", not a 401 — again matching the
// real API rather than what a REST-shaped guess would predict.
func TestMissingHeaderIsAnInvalidParameter(t *testing.T) {
	for _, header := range []string{"X-Login", "X-Trans-Key", "X-Date", "Authorization"} {
		req := signedRequest(t, http.MethodGet, "/payments/D-4-paid")
		req.Header.Del(header)

		rec := do(t, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s removed: status = %d, want 400", header, rec.Code)
			continue
		}
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body["code"] != float64(codeInvalidParameter) {
			t.Errorf("%s removed: code = %v, want %d", header, body["code"], codeInvalidParameter)
		}
	}
}

// An unknown merchant is 403/3001 — the SAME response the real API gives a
// caller whose IP is not allowlisted. That collision is why 3001 cannot be used
// to tell a bad credential from a blocked network path.
func TestUnknownLoginIsInvalidCredentials(t *testing.T) {
	req := signedRequest(t, http.MethodGet, "/payments/D-4-paid")
	req.Header.Set("X-Login", "somebody-else")

	rec := do(t, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != float64(codeInvalidCredentials) {
		t.Fatalf("code = %v, want %d", body["code"], codeInvalidCredentials)
	}
}

// A stale X-Date is ACCEPTED. This is the reverse of what the mock originally
// asserted, and the correction matters: because X-Date is signed as well as
// sent, a drifted clock produces a self-consistent signature that validates
// fine. Verified against the live sandbox with dates a year old and a month in
// the future, both 200. Clock skew is therefore not a real failure mode, and
// any hint claiming otherwise sends the reader hunting in the wrong place.
func TestStaleDateIsAcceptedBecauseItIsSigned(t *testing.T) {
	for _, offset := range []time.Duration{-365 * 24 * time.Hour, -time.Hour, 30 * 24 * time.Hour} {
		stamp := time.Now().Add(offset).UTC().Format("2006-01-02T15:04:05.000") + "Z"

		req := httptest.NewRequest(http.MethodGet, "/payments/D-4-paid", nil)
		req.Header.Set("X-Login", DefaultLogin)
		req.Header.Set("X-Trans-Key", DefaultTransKey)
		req.Header.Set("X-Date", stamp)
		mac := hmac.New(sha256.New, []byte(DefaultSecretKey))
		mac.Write([]byte(DefaultLogin + stamp))
		req.Header.Set("Authorization", authPrefix+hex.EncodeToString(mac.Sum(nil)))

		if rec := do(t, req); rec.Code != http.StatusOK {
			t.Errorf("X-Date offset %v: status = %d, want 200 — a signed stale date is valid\n%s", offset, rec.Code, rec.Body)
		}
	}
}

// The payouts host accepts the payins signature scheme — same header, same
// message. Only the host differs.
func TestPayoutsAcceptsThePayinsScheme(t *testing.T) {
	rec := do(t, signedRequest(t, http.MethodGet, "/v2/payouts/P-1-delivered"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body)
	}
	var payout map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &payout)
	if payout["status"] != "DELIVERED" || payout["status_code"] != "500" {
		t.Fatalf("payout fixture did not match the -delivered suffix: %v", payout)
	}
}

// Payload-Signature is what the docs describe and what the real host REJECTS.
func TestPayoutsRejectsPayloadSignature(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v2/payouts/P-1-paid", nil)
	date := time.Now().UTC().Format("2006-01-02T15:04:05.000") + "Z"
	req.Header.Set("X-Login", DefaultLogin)
	req.Header.Set("X-Trans-Key", DefaultTransKey)
	req.Header.Set("X-Date", date)
	mac := hmac.New(sha256.New, []byte(DefaultSecretKey))
	req.Header.Set("Payload-Signature", hex.EncodeToString(mac.Sum(nil)))

	rec := do(t, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 — the payouts host does not know this scheme", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "invalid_credentials" {
		t.Fatalf("code = %v, want the string \"invalid_credentials\"", body["code"])
	}
}

// The payouts host reports a bad signature as 403 authentication_failed with a
// STRING code, where payins uses 400/5000 with a numeric one.
func TestPayoutsBadSignatureUsesStringCode(t *testing.T) {
	req := signedRequest(t, http.MethodGet, "/v2/payouts/P-1-paid")
	req.Header.Set("Authorization", authPrefix+strings.Repeat("0", 64))

	rec := do(t, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "authentication_failed" {
		t.Fatalf("code = %v, want authentication_failed", body["code"])
	}
}

func TestUnknownIDReturnsDLocalShaped404(t *testing.T) {
	// Refunds use a different not-found code from everything else.
	for path, want := range map[string]int{
		"/payments/D-4-nosuchthing": codePaymentNotFound,
		"/refunds/nosuchthing":      codeRefundNotFound,
		"/chargebacks/nosuchthing":  codePaymentNotFound,
	} {
		rec := do(t, signedRequest(t, http.MethodGet, path))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, rec.Code)
			continue
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Errorf("%s: 404 body is not JSON: %v", path, err)
			continue
		}
		if body["code"] != float64(want) {
			t.Errorf("%s: code = %v, want %d", path, body["code"], want)
		}
	}
}

// An unrouted path returns the bare string NOT_FOUND, not JSON. That is worth
// reproducing because it is the client's only non-JSON error path.
func TestUnroutedPathReturnsPlainText(t *testing.T) {
	rec := do(t, signedRequest(t, http.MethodGet, "/not-a-real-route"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "NOT_FOUND" {
		t.Fatalf("body = %q, want the bare string NOT_FOUND", got)
	}
}

// The real response carries exactly five keys per entry.
func TestPaymentMethodsShapeMatchesTheRealAPI(t *testing.T) {
	rec := do(t, signedRequest(t, http.MethodGet, "/payments-methods?country=BR"))

	var methods []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &methods); err != nil {
		t.Fatalf("response is not a JSON array: %v", err)
	}
	for _, m := range methods {
		for _, key := range []string{"id", "type", "name", "logo", "allowed_flows"} {
			if _, ok := m[key]; !ok {
				t.Errorf("entry %v is missing %q", m["id"], key)
			}
		}
		for _, absent := range []string{"country", "details"} {
			if _, ok := m[absent]; ok {
				t.Errorf("entry %v carries %q, which the real API does not return", m["id"], absent)
			}
		}
	}
}

func TestMissingCountryNamesTheParam(t *testing.T) {
	rec := do(t, signedRequest(t, http.MethodGet, "/payments-methods"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["param"] != "country" {
		t.Fatalf("body does not name the offending param: %v", body)
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

// The real API rejects an unsupported country with 5003. A mock that accepted
// any two-letter string would let a bad code pass in tests and fail in
// production.
func TestUnsupportedCountryIsRejected(t *testing.T) {
	for _, country := range []string{"ZZ", "BRA", "", "b"} {
		rec := do(t, signedRequest(t, http.MethodGet, "/payments-methods?country="+country))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("country=%q: status = %d, want 400", country, rec.Code)
			continue
		}
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body["code"] != float64(codeCountryNotSupported) {
			t.Errorf("country=%q: code = %v, want %d", country, body["code"], codeCountryNotSupported)
		}
	}
	// A supported one still works.
	if rec := do(t, signedRequest(t, http.MethodGet, "/payments-methods?country=BR")); rec.Code != http.StatusOK {
		t.Errorf("country=BR: status = %d, want 200", rec.Code)
	}
}

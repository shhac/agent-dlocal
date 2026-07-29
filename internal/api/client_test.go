package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	agenterrors "github.com/shhac/agent-dlocal/internal/errors"
)

func testCreds() Credentials {
	return Credentials{Login: "login123", TransKey: "trans456", SecretKey: "secret789"}
}

func newTestClient(t *testing.T, baseURL string, creds Credentials) *Client {
	t.Helper()
	client, err := NewClient(Options{BaseURL: baseURL, Credentials: creds, UserAgent: "agent-dlocal/test", MaxRetries: 0})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.setClockForTest(func() time.Time {
		return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	})
	return client
}

// The load-bearing test for the whole signing design: the server recomputes the
// signature over THE BYTES IT ACTUALLY RECEIVED. If the client ever signed one
// serialization and sent another, this fails — which is precisely the
// intermittent-401 bug the single-slice design exists to prevent.
func TestSignatureVerifiesAgainstBytesOnTheWire(t *testing.T) {
	var verified bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}

		mac := hmac.New(sha256.New, []byte("secret789"))
		mac.Write([]byte(r.Header.Get("X-Login") + r.Header.Get("X-Date") + string(received)))
		want := "V2-HMAC-SHA256, Signature: " + hex.EncodeToString(mac.Sum(nil))

		if got := r.Header.Get("Authorization"); got != want {
			t.Errorf("signature does not cover the received bytes:\n got %q\nwant %q\nbody %q", got, want, received)
			return
		}
		verified = true
		_, _ = w.Write([]byte(`{"id":"D-4-abc","status":"PAID"}`))
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, testCreds())
	if _, err := client.Get(context.Background(), "/payments/D-4-abc", url.Values{}); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !verified {
		t.Fatal("handler never verified the signature")
	}
}

func TestGetSendsRequiredHeaders(t *testing.T) {
	var got http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, testCreds())
	if _, err := client.Get(context.Background(), "/payments/D-4-abc", url.Values{}); err != nil {
		t.Fatalf("Get: %v", err)
	}

	for header, want := range map[string]string{
		"X-Login":     "login123",
		"X-Trans-Key": "trans456",
		"X-Version":   "2.1",
		"User-Agent":  "agent-dlocal/test",
		"X-Date":      "2026-07-29T12:00:00.000Z",
	} {
		if got.Get(header) != want {
			t.Errorf("%s = %q, want %q", header, got.Get(header), want)
		}
	}
	if !strings.HasPrefix(got.Get("Authorization"), "V2-HMAC-SHA256, Signature: ") {
		t.Errorf("Authorization = %q, want a V2-HMAC-SHA256 signature", got.Get("Authorization"))
	}
}

func TestGetEncodesQueryParams(t *testing.T) {
	var gotPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, testCreds())
	if _, err := client.Get(context.Background(), "/payments-methods", url.Values{"country": {"BR"}}); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if gotPath != "/payments-methods?country=BR" {
		t.Fatalf("request URI = %q, want /payments-methods?country=BR", gotPath)
	}
}

// dLocal answers a bad signature with 400/5000 — not a 401 — so classification
// keys off the dLocal code rather than the HTTP status.
func TestSignatureMismatchIsHumanFixable(t *testing.T) {
	err := errFor(t, http.StatusBadRequest, `{"code":5000,"message":"Signature not match"}`)

	if err.FixableBy != agenterrors.FixableByHuman {
		t.Errorf("fixable_by = %q, want human", err.FixableBy)
	}
	if !strings.Contains(err.Hint, "secret key") {
		t.Errorf("hint should point at the stored secret key:\n%s", err.Hint)
	}
	// The hint used to blame clock skew. It is wrong: X-Date is signed as well
	// as sent, so a drifted clock stays self-consistent and validates. Verified
	// against the live sandbox with a date a year stale.
	if !strings.Contains(err.Hint, "does NOT cause this") {
		t.Errorf("hint should rule clock skew OUT rather than in:\n%s", err.Hint)
	}
}

// 3001 is returned before the signature is checked, so it cannot distinguish a
// bad credential from a caller whose IP is not allowlisted. The hint has to say
// so rather than pick one.
func TestInvalidCredentialsHintEnumeratesCauses(t *testing.T) {
	err := errFor(t, http.StatusForbidden, `{"code":3001,"message":"Invalid credentials"}`)

	if err.FixableBy != agenterrors.FixableByHuman {
		t.Errorf("fixable_by = %q, want human", err.FixableBy)
	}
	for _, want := range []string{"IP Whitelist", "sandbox"} {
		if !strings.Contains(err.Hint, want) {
			t.Errorf("hint does not mention %q:\n%s", want, err.Hint)
		}
	}
}

func TestInvalidParameterNamesTheParam(t *testing.T) {
	err := errFor(t, http.StatusBadRequest, `{"code":5001,"message":"Missing parameter: country","param":"country"}`)

	if err.FixableBy != agenterrors.FixableByAgent {
		t.Errorf("fixable_by = %q, want agent", err.FixableBy)
	}
	if !strings.Contains(err.Error(), "country") || !strings.Contains(err.Hint, "country") {
		t.Errorf("the offending param should surface in both message and hint:\n%s\n%s", err.Error(), err.Hint)
	}
}

// An unrouted path returns the bare string NOT_FOUND, so the classifier must
// survive a non-JSON body.
func TestNonJSONErrorBodyIsHandled(t *testing.T) {
	err := errFor(t, http.StatusNotFound, "NOT_FOUND")

	if !strings.Contains(err.Error(), "NOT_FOUND") {
		t.Errorf("non-JSON body was lost: %s", err.Error())
	}
	if err.FixableBy != agenterrors.FixableByAgent {
		t.Errorf("fixable_by = %q, want agent", err.FixableBy)
	}
}

// errFor drives a real request against a server returning the given body, so
// the classification is exercised through the client rather than in isolation.
func errFor(t *testing.T, status int, body string) *agenterrors.APIError {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server.URL, testCreds())
	_, err := client.Get(context.Background(), "/probe", url.Values{})
	if err == nil {
		t.Fatalf("expected an error for HTTP %d", status)
	}
	apiErr, ok := err.(*agenterrors.APIError)
	if !ok {
		t.Fatalf("error type = %T, want *agenterrors.APIError", err)
	}
	return apiErr
}

func TestNotFoundIsAgentFixable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":4000,"message":"Payment not found"}`))
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, testCreds())
	_, err := client.Get(context.Background(), "/payments/nope", url.Values{})

	apiErr, ok := err.(*agenterrors.APIError)
	if !ok {
		t.Fatalf("error type = %T, want *agenterrors.APIError", err)
	}
	if apiErr.FixableBy != agenterrors.FixableByAgent {
		t.Errorf("fixable_by = %q, want agent — a 404 is the agent's id to correct", apiErr.FixableBy)
	}
	if !strings.Contains(apiErr.Hint, "separate ledgers") {
		t.Errorf("404 hint should raise the live/sandbox split:\n%s", apiErr.Hint)
	}
}

func TestRetriesTransientFailuresThenSucceeds(t *testing.T) {
	retryBaseDelay = time.Millisecond
	t.Cleanup(func() { retryBaseDelay = 250 * time.Millisecond })

	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"id":"D-4-abc"}`))
	}))
	defer server.Close()

	client, err := NewClient(Options{BaseURL: server.URL, Credentials: testCreds(), MaxRetries: 2})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := client.Get(context.Background(), "/payments/D-4-abc", url.Values{}); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3 (two retries then success)", attempts)
	}
}

func TestRetriesAreBoundedByMaxRetries(t *testing.T) {
	retryBaseDelay = time.Millisecond
	t.Cleanup(func() { retryBaseDelay = 250 * time.Millisecond })

	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client, err := NewClient(Options{BaseURL: server.URL, Credentials: testCreds(), MaxRetries: 1})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := client.Get(context.Background(), "/payments/D-4-abc", url.Values{}); err == nil {
		t.Fatal("Get: want an error after retries are exhausted")
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2 (initial + one retry)", attempts)
	}
}

func TestMutualTLSRequiresBothCertAndKey(t *testing.T) {
	_, err := NewClient(Options{BaseURL: "https://example.test", CertPath: "/tmp/only-a-cert.pem"})
	if err == nil {
		t.Fatal("NewClient accepted a certificate with no key")
	}
	if !strings.Contains(err.Error(), "certificate and a key") {
		t.Fatalf("error should name the missing half: %v", err)
	}
}

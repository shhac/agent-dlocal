// Package mockdlocal is a local HTTP server returning dLocal-shaped JSON so
// agent-dlocal can be exercised end to end without credentials or network.
//
// Its defining feature is that it VERIFIES THE HMAC SIGNATURE on every request.
// A mock that accepted any Authorization header would let a signing bug ship;
// because this one recomputes HMAC(secret, X-Login || X-Date || body) and
// compares, a passing e2e test is real evidence the client signs correctly.
package mockdlocal

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultLogin     = "mocklogin"
	DefaultTransKey  = "mocktrans"
	DefaultSecretKey = "mocksecret"
	DefaultMaxSkew   = 5 * time.Minute

	authPrefix = "V2-HMAC-SHA256, Signature: "
)

type Options struct {
	Login     string
	TransKey  string
	SecretKey string
	// MaxSkew bounds how far X-Date may sit from now. The real API enforces a
	// window, and reproducing it here is what makes the CLI's clock-skew hint
	// testable rather than aspirational.
	MaxSkew time.Duration
}

type Server struct {
	opts Options
	mux  *http.ServeMux
}

func NewServer() http.Handler {
	return NewServerWithOptions(Options{})
}

func NewServerWithOptions(opts Options) http.Handler {
	if opts.Login == "" {
		opts.Login = DefaultLogin
	}
	if opts.TransKey == "" {
		opts.TransKey = DefaultTransKey
	}
	if opts.SecretKey == "" {
		opts.SecretKey = DefaultSecretKey
	}
	if opts.MaxSkew == 0 {
		opts.MaxSkew = DefaultMaxSkew
	}

	s := &Server{opts: opts, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	// GET / is the route inventory and is deliberately unauthenticated: it is
	// how a caller discovers the surface before having credentials.
	s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"routes": Routes()})
	})

	s.mux.Handle("GET /payments/{id}/status", s.payins(handlePaymentStatus))
	s.mux.Handle("GET /payments/{id}", s.payins(handlePayment))
	s.mux.Handle("GET /orders/{id}", s.payins(handleOrder))
	s.mux.Handle("GET /refunds/{id}", s.payins(handleRefund))
	s.mux.Handle("GET /chargebacks/{id}", s.payins(handleChargeback))
	s.mux.Handle("GET /payments-methods", s.payins(handlePaymentMethods))
	s.mux.Handle("GET /v2/payouts/{id}", s.payouts(handlePayout))

	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, 404, "Route "+r.Method+" "+r.URL.Path+" is not mocked")
	})
}

type handlerFunc func(w http.ResponseWriter, r *http.Request, body []byte)

// payins wraps a handler with the V2-HMAC-SHA256 (Authorization) scheme.
func (s *Server) payins(next handlerFunc) http.Handler {
	return s.authenticated(next, schemePayins)
}

// payouts wraps a handler with the Payload-Signature scheme, which hashes the
// body ALONE rather than login+date+body.
func (s *Server) payouts(next handlerFunc) http.Handler {
	return s.authenticated(next, schemePayouts)
}

type scheme int

const (
	schemePayins scheme = iota
	schemePayouts
)

func (s *Server) authenticated(next handlerFunc, sch scheme) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, 400, "could not read request body")
			return
		}

		if failure := s.verify(r, body, sch); failure != "" {
			writeError(w, http.StatusUnauthorized, 401, failure)
			return
		}
		next(w, r, body)
	})
}

// verify returns an empty string when the request authenticates, or a message
// naming exactly what is wrong. The mock's errors are a teaching tool: "missing
// header" and "signature mismatch" call for completely different fixes, so it
// never collapses them into a generic 401.
func (s *Server) verify(r *http.Request, body []byte, sch scheme) string {
	login := r.Header.Get("X-Login")
	transKey := r.Header.Get("X-Trans-Key")
	date := r.Header.Get("X-Date")

	for name, value := range map[string]string{"X-Login": login, "X-Trans-Key": transKey, "X-Date": date} {
		if value == "" {
			return "missing required header " + name
		}
	}

	if login != s.opts.Login {
		return "unknown X-Login"
	}
	if transKey != s.opts.TransKey {
		return "unknown X-Trans-Key"
	}

	parsed, err := time.Parse("2006-01-02T15:04:05.000Z07:00", date)
	if err != nil {
		if parsed, err = time.Parse(time.RFC3339, date); err != nil {
			return "X-Date is not an ISO-8601 datetime with timezone: " + date
		}
	}
	if skew := time.Since(parsed); skew > s.opts.MaxSkew || skew < -s.opts.MaxSkew {
		return "X-Date is outside the accepted clock-skew window (clock skew)"
	}

	if sch == schemePayouts {
		return s.verifyPayoutsSignature(r, body)
	}
	return s.verifyPayinsSignature(r, body, login, date)
}

func (s *Server) verifyPayinsSignature(r *http.Request, body []byte, login, date string) string {
	if r.Header.Get("Payload-Signature") != "" {
		return "payins request carried a Payload-Signature header; that is the payouts scheme"
	}

	header := r.Header.Get("Authorization")
	if header == "" {
		return "missing required header Authorization"
	}
	if !strings.HasPrefix(header, authPrefix) {
		return "Authorization is not in the form 'V2-HMAC-SHA256, Signature: <hex>'"
	}

	want := sign(s.opts.SecretKey, []byte(login+date+string(body)))
	if !hmac.Equal([]byte(strings.TrimPrefix(header, authPrefix)), []byte(want)) {
		return "signature mismatch: the digest does not match HMAC-SHA256(secret, X-Login + X-Date + body) over the received body"
	}
	return ""
}

func (s *Server) verifyPayoutsSignature(r *http.Request, body []byte) string {
	if r.Header.Get("Authorization") != "" {
		return "payouts request carried an Authorization header; that is the payins scheme"
	}

	header := r.Header.Get("Payload-Signature")
	if header == "" {
		return "missing required header Payload-Signature"
	}
	if !hmac.Equal([]byte(header), []byte(sign(s.opts.SecretKey, body))) {
		return "signature mismatch: the digest does not match HMAC-SHA256(secret, body) over the received body"
	}
	return ""
}

func sign(secret string, message []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(message)
	return hex.EncodeToString(mac.Sum(nil))
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}

func writeError(w http.ResponseWriter, status, code int, message string) {
	writeJSON(w, status, map[string]any{"code": code, "message": message})
}

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

	authPrefix = "V2-HMAC-SHA256, Signature: "
)

// dLocal error codes, observed against the live sandbox. Kept here rather than
// imported from internal/api so the mock stays an independent statement of what
// the API does — if the two ever disagree, a test fails instead of both moving
// together.
const (
	codeInvalidCredentials  = 3001 // 403
	codePaymentNotFound     = 4000 // 404, also used for orders and chargebacks
	codeRefundNotFound      = 4001 // 404
	codeSignatureMismatch   = 5000 // 400 — note: NOT a 401; also means "Invalid request"
	codeInvalidParameter    = 5001 // 400
	codeCountryNotSupported = 5003 // 400 — unknown, empty, or non-two-letter country
)

type Options struct {
	Login     string
	TransKey  string
	SecretKey string
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

	// An unrouted path returns the bare string NOT_FOUND, not JSON — matching
	// the real API, and exercising the client's non-JSON error path.
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("NOT_FOUND"))
	})
}

type handlerFunc func(w http.ResponseWriter, r *http.Request, body []byte)

// payins wraps a handler that reports rejections with numeric dLocal codes.
func (s *Server) payins(next handlerFunc) http.Handler {
	return s.authenticated(next, numericCodes)
}

// payouts wraps a handler that reports rejections the payouts way. The
// SIGNATURE scheme is identical to payins — verified against the live sandbox,
// which rejects Payload-Signature with 401 invalid_credentials and accepts the
// ordinary Authorization header. Only the error spelling differs.
func (s *Server) payouts(next handlerFunc) http.Handler {
	return s.authenticated(next, stringCodes)
}

// errorDialect selects how a rejection is spelled. It is NOT a signing scheme:
// both hosts accept the same Authorization header. Payins answers with numeric
// codes and a "param" key, payouts with string codes and "field".
type errorDialect int

const (
	numericCodes errorDialect = iota
	stringCodes
)

func (s *Server) authenticated(next handlerFunc, sch errorDialect) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, 400, "could not read request body")
			return
		}

		if failure := s.verify(r, body, sch); failure != nil {
			writeFailure(w, failure)
			return
		}
		next(w, r, body)
	})
}

// authFailure is a rejection expressed the way the real API expresses it:
// an HTTP status plus a dLocal code, which do not always agree (a bad signature
// is 400/5000, not a 401).
type authFailure struct {
	status  int
	code    int
	message string
	param   string
	// textCode is the payouts API's string code. That API returns
	// {"code":"authentication_failed"} where payins returns {"code":5000}, so
	// the mock carries both shapes rather than pretending they agree.
	textCode string
}

// verify returns nil when the request authenticates, or the rejection the real
// sandbox produces for that fault. Every status/code pair below was observed
// against https://sandbox.dlocal.com rather than inferred from the docs.
func (s *Server) verify(r *http.Request, body []byte, sch errorDialect) *authFailure {
	login := r.Header.Get("X-Login")
	date := r.Header.Get("X-Date")

	// A missing header is a 400 "Invalid parameter" naming the header — NOT a
	// 401. X-Version and User-Agent are documented as required but the API
	// serves requests without them, so the mock does not demand them either.
	if login == "" {
		return &authFailure{status: 400, code: codeInvalidParameter, message: "Invalid parameter", param: "X-Login"}
	}
	if r.Header.Get("X-Trans-Key") == "" {
		return &authFailure{status: 400, code: codeInvalidParameter, message: "Missing parameter(s) [or non valid values]. key"}
	}
	if date == "" {
		return &authFailure{status: 400, code: codeInvalidParameter, message: "Invalid parameter", param: "X-Date"}
	}
	if !isISO8601(date) {
		return &authFailure{status: 400, code: codeInvalidParameter, message: "Invalid parameter", param: "X-Date"}
	}

	// An unrecognized merchant is 403/3001 — the same response the real API
	// gives a caller whose IP is not allowlisted, which is why that error
	// cannot be used to tell those two causes apart.
	if login != s.opts.Login || r.Header.Get("X-Trans-Key") != s.opts.TransKey {
		return &authFailure{status: 403, code: codeInvalidCredentials, message: "Invalid credentials"}
	}

	// NOTE: X-Date is deliberately NOT checked for staleness. The real sandbox
	// accepts a date a year old or a month in the future, because the date is
	// signed as well as sent: a drifted clock produces a self-consistent
	// signature that validates. Enforcing a skew window here would make the
	// mock stricter than the thing it stands in for.

	return s.verifyPayinsSignature(r, body, login, date, sch)
}

func (s *Server) verifyPayinsSignature(r *http.Request, body []byte, login, date string, sch errorDialect) *authFailure {
	// The payouts host answers a Payload-Signature request with 401
	// invalid_credentials — it does not recognize that scheme at all.
	if sch == stringCodes && r.Header.Get("Payload-Signature") != "" && r.Header.Get("Authorization") == "" {
		return &authFailure{status: 401, message: "invalid credentials", textCode: "invalid_credentials"}
	}

	header := r.Header.Get("Authorization")
	if header == "" {
		if sch == stringCodes {
			return &authFailure{status: 401, message: "invalid credentials", textCode: "invalid_credentials"}
		}
		return &authFailure{status: 400, code: codeInvalidParameter, message: "Invalid parameter", param: "Authorization"}
	}
	if !strings.HasPrefix(header, authPrefix) {
		return &authFailure{status: 400, code: codeInvalidParameter, message: "Invalid parameter", param: "authorization"}
	}

	want := sign(s.opts.SecretKey, []byte(login+date+string(body)))
	if !hmac.Equal([]byte(strings.TrimPrefix(header, authPrefix)), []byte(want)) {
		// The two hosts report a bad signature differently.
		if sch == stringCodes {
			return &authFailure{status: 403, message: "Authentication failed", textCode: "authentication_failed"}
		}
		return &authFailure{status: 400, code: codeSignatureMismatch, message: "Signature not match"}
	}
	return nil
}

// isISO8601 accepts the two shapes dLocal's own examples use.
func isISO8601(value string) bool {
	for _, layout := range []string{"2006-01-02T15:04:05.000Z07:00", time.RFC3339} {
		if _, err := time.Parse(layout, value); err == nil {
			return true
		}
	}
	return false
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

func writeErrorParam(w http.ResponseWriter, status, code int, message, param string) {
	writeJSON(w, status, map[string]any{"code": code, "message": message, "param": param})
}

func writeFailure(w http.ResponseWriter, f *authFailure) {
	payload := map[string]any{"message": f.message}
	if f.textCode != "" {
		payload["code"] = f.textCode
		payload["field"] = f.param
	} else {
		payload["code"] = f.code
		if f.param != "" {
			payload["param"] = f.param
		}
	}
	writeJSON(w, f.status, payload)
}

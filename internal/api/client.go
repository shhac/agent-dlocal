package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	agenterrors "github.com/shhac/agent-dlocal/internal/errors"
	"github.com/shhac/agent-dlocal/internal/output"
)

const apiVersion = "2.1"

type Client struct {
	baseURL    string
	signer     Signer
	maxRetries int
	http       *http.Client
	debug      bool
	redaction  output.RedactionOptions
	now        func() time.Time
}

type Options struct {
	BaseURL    string
	Signer     Signer
	MaxRetries int
	// CertPath/KeyPath enable mutual TLS. dLocal turns client-certificate auth
	// on per merchant, layered on top of the HMAC headers — with neither set,
	// the client does plain TLS + HMAC.
	CertPath string
	KeyPath  string
}

func NewClient(opts Options) (*Client, error) {
	httpClient := &http.Client{}
	if opts.CertPath != "" || opts.KeyPath != "" {
		transport, err := mutualTLSTransport(opts.CertPath, opts.KeyPath)
		if err != nil {
			return nil, err
		}
		httpClient.Transport = transport
	}

	return &Client{
		baseURL:    strings.TrimRight(opts.BaseURL, "/"),
		signer:     opts.Signer,
		maxRetries: nonNegative(opts.MaxRetries),
		http:       httpClient,
		now:        time.Now,
	}, nil
}

func mutualTLSTransport(certPath, keyPath string) (*http.Transport, error) {
	if certPath == "" || keyPath == "" {
		return nil, agenterrors.New("mTLS needs both a certificate and a key", agenterrors.FixableByHuman).
			WithHint("Set both --cert <path> and --key <path> on the profile, or neither")
	}
	for label, path := range map[string]string{"certificate": certPath, "key": keyPath} {
		if _, err := os.Stat(path); err != nil {
			return nil, agenterrors.Newf(agenterrors.FixableByHuman, "client %s not readable at %s", label, path).
				WithHint("Check the path stored on the profile with 'agent-dlocal auth list'")
		}
	}

	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, agenterrors.Wrap(err, agenterrors.FixableByHuman).
			WithHint("The certificate and key did not load as a pair; check they match and that the key is unencrypted PEM")
	}
	return &http.Transport{
		TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{pair},
			MinVersion:   tls.VersionTLS12,
		},
	}, nil
}

func (c *Client) SetDebug(enabled bool) {
	c.debug = enabled
}

func (c *Client) SetDebugRedaction(opts output.RedactionOptions) {
	c.redaction = opts
}

// setClockForTest pins the signing clock so a signature is reproducible.
func (c *Client) setClockForTest(now func() time.Time) {
	c.now = now
}

func (c *Client) Get(ctx context.Context, path string, params url.Values) (json.RawMessage, error) {
	return c.do(ctx, http.MethodGet, buildPath(path, params), nil)
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) (json.RawMessage, error) {
	for attempt := 0; ; attempt++ {
		resp, err := c.sendOnce(ctx, method, path, body)
		if err != nil {
			return nil, err
		}

		if shouldRetry(resp.status, attempt, c.maxRetries) {
			delay := retryDelay(resp.retryAfter, attempt)
			if c.debug {
				c.logRetry(method, resp.url, resp.status, attempt+1, c.maxRetries, delay)
			}
			if err := sleepWithContext(ctx, delay); err != nil {
				return nil, agenterrors.Wrap(err, agenterrors.FixableByRetry).WithHint("Retry wait interrupted; re-run the command")
			}
			continue
		}

		if resp.status >= 400 {
			return nil, classifyHTTPError(resp.status, c.maxRetries, resp.body)
		}

		return json.RawMessage(resp.body), nil
	}
}

type responseEnvelope struct {
	status     int
	body       []byte
	url        string
	retryAfter string
}

func (c *Client) sendOnce(ctx context.Context, method, path string, body []byte) (*responseEnvelope, error) {
	req, err := c.buildRequest(ctx, method, path, body)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, agenterrors.Wrap(err, agenterrors.FixableByRetry).WithHint("Network error: check connectivity and retry")
	}

	respBody, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return nil, agenterrors.Wrap(readErr, agenterrors.FixableByRetry)
	}

	if c.debug {
		c.logDebug(method, req.URL.String(), resp.StatusCode, respBody)
	}

	return &responseEnvelope{
		status:     resp.StatusCode,
		body:       respBody,
		url:        req.URL.String(),
		retryAfter: resp.Header.Get("Retry-After"),
	}, nil
}

// buildRequest signs and sends THE SAME BYTES.
//
// body is a []byte, not an any to be marshalled downstream, and the identical
// slice is handed to the signer and to the request. dLocal's signature covers
// the exact bytes on the wire, so a design where one path marshals for signing
// and another marshals for sending would drift on map key ordering or
// whitespace and produce intermittent 401s that look random. Keeping the
// serialization at the caller — one marshal, one slice — makes that class of
// bug unrepresentable rather than merely untested.
func (c *Client) buildRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = strings.NewReader(string(body))
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, agenterrors.Wrap(err, agenterrors.FixableByAgent)
	}

	c.signer.Apply(req.Header, body, c.now())
	return req, nil
}

func buildPath(base string, params url.Values) string {
	if encoded := params.Encode(); encoded != "" {
		return base + "?" + encoded
	}
	return base
}

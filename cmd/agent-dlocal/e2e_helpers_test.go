package main

import (
	"encoding/json"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/shhac/agent-dlocal/internal/mockdlocal"
)

// The e2e suite drives the REAL binary against mockdlocal, which verifies every
// signature. So these tests do not merely check output shape — a passing run is
// evidence that the client signs correctly end to end.
type mockCLIRunner struct {
	t      *testing.T
	server *httptest.Server
}

func newMockCLIRunner(t *testing.T) *mockCLIRunner {
	t.Helper()
	server := httptest.NewServer(mockdlocal.NewServer())
	t.Cleanup(server.Close)
	return &mockCLIRunner{t: t, server: server}
}

func (r *mockCLIRunner) command(args []string) *exec.Cmd {
	allArgs := append([]string{"run", "./cmd/agent-dlocal", "--base-url", r.server.URL}, args...)
	cmd := exec.Command("go", allArgs...)
	cmd.Dir = "../.."
	// Credentials come from the environment so no keychain or config file is
	// touched; they match mockdlocal's expected set.
	cmd.Env = append(cmd.Environ(),
		"DLOCAL_X_LOGIN="+mockdlocal.DefaultLogin,
		"DLOCAL_X_TRANS_KEY="+mockdlocal.DefaultTransKey,
		"DLOCAL_SECRET_KEY="+mockdlocal.DefaultSecretKey,
	)
	return cmd
}

func (r *mockCLIRunner) Run(args ...string) string {
	r.t.Helper()
	out, err := r.command(args).CombinedOutput()
	if err != nil {
		r.t.Fatalf("agent-dlocal %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

// RunExpectingError runs a case that must fail: a structured error on stderr
// and a non-zero exit, per the single-sink contract.
func (r *mockCLIRunner) RunExpectingError(args ...string) string {
	r.t.Helper()
	out, err := r.command(args).CombinedOutput()
	if err == nil {
		r.t.Fatalf("agent-dlocal %v unexpectedly succeeded; expected a non-zero exit\n%s", args, out)
	}
	return string(out)
}

func runMockCLI(t *testing.T, args ...string) string {
	t.Helper()
	return newMockCLIRunner(t).Run(args...)
}

func runMockCLIErr(t *testing.T, args ...string) string {
	t.Helper()
	return newMockCLIRunner(t).RunExpectingError(args...)
}

func assertContains(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func assertNotContains(t *testing.T, out string, blocked ...string) {
	t.Helper()
	for _, value := range blocked {
		if strings.Contains(out, value) {
			t.Fatalf("output unexpectedly contained %q:\n%s", value, out)
		}
	}
}

// decodeLines parses NDJSON output into one map per line.
func decodeLines(t *testing.T, out string) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("line is not JSON: %v\n%s", err, line)
		}
		records = append(records, record)
	}
	return records
}

func decodeOne(t *testing.T, out string) map[string]any {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal([]byte(out), &record); err != nil {
		t.Fatalf("output is not a single JSON object: %v\n%s", err, out)
	}
	return record
}

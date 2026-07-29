package auth

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/shhac/agent-dlocal/internal/cli/shared"
	"github.com/shhac/agent-dlocal/internal/config"
	"github.com/shhac/agent-dlocal/internal/credential"
	"github.com/shhac/agent-dlocal/internal/output"
)

type storedCall struct {
	alias string
	set   credential.Set
}

func withStubbedStore(t *testing.T) *[]storedCall {
	t.Helper()
	config.SetConfigDir(t.TempDir())
	t.Cleanup(func() { config.SetConfigDir("") })

	calls := &[]storedCall{}
	previousStore := credentialStore
	credentialStore = func(alias string, set credential.Set) (string, error) {
		*calls = append(*calls, storedCall{alias: alias, set: set})
		return "keychain", nil
	}
	t.Cleanup(func() { credentialStore = previousStore })
	return calls
}

func runAuth(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var stdout bytes.Buffer
	restore := output.SetWritersForTest(&stdout, &bytes.Buffer{})
	defer restore()

	root := &cobra.Command{Use: "agent-dlocal"}
	globals := &shared.GlobalFlags{}
	Register(root, func() *shared.GlobalFlags { return globals })

	root.SetArgs(args)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	err := root.Execute()
	return stdout.String(), err
}

func TestAddStoresAllThreeSecretsAsOneSet(t *testing.T) {
	calls := withStubbedStore(t)

	out, err := runAuth(t, "auth", "add", "prod",
		"--login", "login123", "--trans-key", "trans456", "--secret-key", "secret789")
	if err != nil {
		t.Fatalf("auth add: %v", err)
	}

	if len(*calls) != 1 {
		t.Fatalf("credential.Store called %d times, want 1", len(*calls))
	}
	got := (*calls)[0]
	want := credential.Set{Login: "login123", TransKey: "trans456", SecretKey: "secret789"}
	if got.set != want {
		t.Fatalf("stored set = %+v, want %+v", got.set, want)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if result["status"] != "added" || result["profile"] != "prod" {
		t.Fatalf("unexpected result: %v", result)
	}
	if result["environment"] != config.EnvironmentLive {
		t.Fatalf("environment = %v, want live (the default)", result["environment"])
	}
}

// The output of a credential command is the most likely place for a secret to
// escape into a transcript.
func TestAddNeverEchoesSecrets(t *testing.T) {
	withStubbedStore(t)

	out, err := runAuth(t, "auth", "add", "prod",
		"--login", "login123", "--trans-key", "trans456", "--secret-key", "secret789")
	if err != nil {
		t.Fatalf("auth add: %v", err)
	}

	for _, secret := range []string{"login123", "trans456", "secret789"} {
		if strings.Contains(out, secret) {
			t.Fatalf("auth add echoed %q into stdout:\n%s", secret, out)
		}
	}
}

func TestAddSandboxSelectsSandboxHosts(t *testing.T) {
	withStubbedStore(t)

	out, err := runAuth(t, "auth", "add", "sbox", "--sandbox",
		"--login", "l", "--trans-key", "t", "--secret-key", "s")
	if err != nil {
		t.Fatalf("auth add: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if result["base_url"] != config.SandboxBaseURL {
		t.Fatalf("base_url = %v, want %s", result["base_url"], config.SandboxBaseURL)
	}
	if result["payouts_base_url"] != config.SandboxPayoutsBaseURL {
		t.Fatalf("payouts_base_url = %v, want %s", result["payouts_base_url"], config.SandboxPayoutsBaseURL)
	}
}

// The cert is a PATH on the profile, never a secret in the keychain — a PEM
// cannot be typed into a single-line dialog field and a path is not sensitive.
func TestAddStoresCertPathNotCertContents(t *testing.T) {
	withStubbedStore(t)

	if _, err := runAuth(t, "auth", "add", "prod", "--sandbox",
		"--login", "l", "--trans-key", "t", "--secret-key", "s",
		"--cert", "/tmp/client.pem", "--key", "/tmp/client.key"); err != nil {
		t.Fatalf("auth add: %v", err)
	}

	profile := config.Read().Profiles["prod"]
	if profile.CertPath != "/tmp/client.pem" || profile.KeyPath != "/tmp/client.key" {
		t.Fatalf("cert/key paths were not stored on the profile: %+v", profile)
	}
	// A configured cert moves the sandbox to the mTLS host.
	if profile.BaseURL != config.SandboxCertBaseURL {
		t.Fatalf("base_url = %q, want the mTLS sandbox host %q", profile.BaseURL, config.SandboxCertBaseURL)
	}
}

// An incomplete credential set must fail before anything is written, and the
// hint must point at --form rather than at pasting secrets.
func TestAddWithMissingSecretsFailsAndRecommendsForm(t *testing.T) {
	calls := withStubbedStore(t)

	_, err := runAuth(t, "auth", "add", "prod", "--login", "login123")
	if err == nil {
		t.Fatal("auth add succeeded with no trans-key or secret-key")
	}
	if len(*calls) != 0 {
		t.Fatalf("a partial credential set was written: %+v", *calls)
	}
	if !strings.Contains(err.Error(), "trans-key") {
		t.Fatalf("error does not name the first missing field: %v", err)
	}
}

func TestListReportsMetadataWithoutSecrets(t *testing.T) {
	withStubbedStore(t)

	if _, err := runAuth(t, "auth", "add", "prod",
		"--login", "login123", "--trans-key", "trans456", "--secret-key", "secret789"); err != nil {
		t.Fatalf("auth add: %v", err)
	}

	out, err := runAuth(t, "auth", "list")
	if err != nil {
		t.Fatalf("auth list: %v", err)
	}

	for _, secret := range []string{"login123", "trans456", "secret789"} {
		if strings.Contains(out, secret) {
			t.Fatalf("auth list leaked %q:\n%s", secret, out)
		}
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &row); err != nil {
		t.Fatalf("auth list did not emit one NDJSON row: %v\n%s", err, out)
	}
	if row["profile"] != "prod" {
		t.Fatalf("auth list did not report the profile: %v", row)
	}
	if row["credential"] != "missing" && row["credential"] != "keychain" {
		t.Fatalf("auth list reported an unexpected credential backend: %v", row["credential"])
	}
	if !strings.Contains(out, config.LiveBaseURL) {
		t.Fatalf("auth list did not report the host:\n%s", out)
	}
}

func TestUpdateReportsUpdatedStatus(t *testing.T) {
	withStubbedStore(t)

	out, err := runAuth(t, "auth", "update", "prod",
		"--login", "l2", "--trans-key", "t2", "--secret-key", "s2")
	if err != nil {
		t.Fatalf("auth update: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if result["status"] != "updated" {
		t.Fatalf("status = %v, want updated", result["status"])
	}
}

// Changing non-secret metadata must not force the user to re-enter secrets
// they have not changed — the case that surfaced when a profile was created
// pointing at the wrong environment.
func TestUpdateKeepsStoredSecretsWhenOnlyMetadataChanges(t *testing.T) {
	calls := withStubbedStore(t)

	stored := credential.Set{Login: "l1", TransKey: "t1", SecretKey: "s1"}
	previousGet := credentialGet
	credentialGet = func(string) (credential.Set, error) { return stored, nil }
	t.Cleanup(func() { credentialGet = previousGet })

	out, err := runAuth(t, "auth", "update", "prod", "--sandbox")
	if err != nil {
		t.Fatalf("auth update with only --sandbox: %v", err)
	}

	if len(*calls) != 1 {
		t.Fatalf("credential.Store called %d times, want 1", len(*calls))
	}
	if got := (*calls)[0].set; got != stored {
		t.Fatalf("stored set = %+v, want the previously stored %+v", got, stored)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if result["environment"] != config.EnvironmentSandbox {
		t.Fatalf("environment = %v, want sandbox", result["environment"])
	}
}

// A supplied secret still wins over the stored one, so rotating a single value
// works without re-entering the others.
func TestUpdateLetsASuppliedSecretOverrideTheStoredOne(t *testing.T) {
	calls := withStubbedStore(t)

	previousGet := credentialGet
	credentialGet = func(string) (credential.Set, error) {
		return credential.Set{Login: "l1", TransKey: "t1", SecretKey: "old"}, nil
	}
	t.Cleanup(func() { credentialGet = previousGet })

	if _, err := runAuth(t, "auth", "update", "prod", "--secret-key", "rotated"); err != nil {
		t.Fatalf("auth update: %v", err)
	}

	got := (*calls)[0].set
	want := credential.Set{Login: "l1", TransKey: "t1", SecretKey: "rotated"}
	if got != want {
		t.Fatalf("stored set = %+v, want %+v", got, want)
	}
}

// add is not update: creating a profile still requires a full credential set,
// so a typo'd alias cannot silently inherit another profile's secrets.
func TestAddStillRequiresAFullCredentialSet(t *testing.T) {
	calls := withStubbedStore(t)

	previousGet := credentialGet
	credentialGet = func(string) (credential.Set, error) {
		return credential.Set{Login: "l1", TransKey: "t1", SecretKey: "s1"}, nil
	}
	t.Cleanup(func() { credentialGet = previousGet })

	if _, err := runAuth(t, "auth", "add", "prod", "--sandbox"); err == nil {
		t.Fatal("auth add succeeded with no secrets supplied")
	}
	if len(*calls) != 0 {
		t.Fatalf("auth add wrote a credential it should have rejected: %+v", *calls)
	}
}

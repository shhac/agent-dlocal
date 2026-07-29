package shared

import (
	"strings"
	"testing"

	"github.com/shhac/agent-dlocal/internal/config"
	"github.com/shhac/agent-dlocal/internal/credential"
	agenterrors "github.com/shhac/agent-dlocal/internal/errors"
)

// ResolveProfile decides which merchant's credentials sign a request. Getting
// it wrong means signing with the WRONG merchant — potentially the live one
// when the operator believed they were on sandbox — so its precedence rules are
// worth pinning directly rather than only through the e2e suite, which always
// sets all three env vars at once.

func withStubbedCredentials(t *testing.T, set credential.Set) {
	t.Helper()
	config.SetConfigDir(t.TempDir())
	t.Cleanup(func() { config.SetConfigDir("") })

	previous := credentialGet
	credentialGet = func(string) (credential.Set, error) { return set, nil }
	t.Cleanup(func() { credentialGet = previous })
}

func completeSet() credential.Set {
	return credential.Set{Login: "l", TransKey: "t", SecretKey: "s"}
}

func TestEnvCredentialsWinOverTheStoredProfile(t *testing.T) {
	withStubbedCredentials(t, completeSet())
	t.Setenv("DLOCAL_X_LOGIN", "env-login")
	t.Setenv("DLOCAL_X_TRANS_KEY", "env-trans")
	t.Setenv("DLOCAL_SECRET_KEY", "env-secret")

	resolved, err := ResolveProfile(&GlobalFlags{})
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if resolved.CredentialSource != "env" {
		t.Fatalf("credential source = %q, want env", resolved.CredentialSource)
	}
	if resolved.Credentials.Login != "env-login" {
		t.Fatalf("login = %q, want the env one", resolved.Credentials.Login)
	}
}

// A PARTIAL env credential set is the dangerous case: a typo'd
// DLOCAL_SECRET_KEY silently falls through to the stored profile, so the
// request is signed with a different merchant's key than the operator
// intended. Whatever the behaviour is, it must be deliberate and pinned.
func TestPartialEnvCredentialsDoNotSilentlyMixWithTheProfile(t *testing.T) {
	withStubbedCredentials(t, credential.Set{Login: "stored", TransKey: "stored", SecretKey: "stored"})
	if err := config.StoreProfile("prod", config.Profile{}); err != nil {
		t.Fatalf("StoreProfile: %v", err)
	}
	// Only two of the three are set — a typo'd or forgotten third.
	t.Setenv("DLOCAL_X_LOGIN", "env-login")
	t.Setenv("DLOCAL_X_TRANS_KEY", "env-trans")

	resolved, err := ResolveProfile(&GlobalFlags{})
	if err != nil {
		return // rejecting the partial set outright is also acceptable
	}

	// If it falls back, it must fall back WHOLLY to the profile — never mix an
	// env login with a stored secret, which would sign as nobody.
	creds := resolved.Credentials
	if creds.Login == "env-login" && creds.SecretKey == "stored" {
		t.Fatalf("env and stored credentials were mixed: %+v — the request would be signed with a login and secret from different merchants", creds)
	}
	if resolved.CredentialSource != "keychain" {
		t.Fatalf("credential source = %q; a partial env set should not report as env", resolved.CredentialSource)
	}
}

func TestProfileFlagBeatsEnvBeatsDefault(t *testing.T) {
	withStubbedCredentials(t, completeSet())
	for _, alias := range []string{"from-default", "from-env", "from-flag"} {
		if err := config.StoreProfile(alias, config.Profile{}); err != nil {
			t.Fatalf("StoreProfile %s: %v", alias, err)
		}
	}
	if err := config.SetDefault("from-default"); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}

	got, err := ResolveProfile(&GlobalFlags{})
	if err != nil || got.Alias != "from-default" {
		t.Fatalf("with nothing set: alias = %q, err = %v; want from-default", got.GetAlias(), err)
	}

	t.Setenv("AGENT_DLOCAL_PROFILE", "from-env")
	got, err = ResolveProfile(&GlobalFlags{})
	if err != nil || got.Alias != "from-env" {
		t.Fatalf("with AGENT_DLOCAL_PROFILE: alias = %q, err = %v; want from-env", got.GetAlias(), err)
	}

	got, err = ResolveProfile(&GlobalFlags{Profile: "from-flag"})
	if err != nil || got.Alias != "from-flag" {
		t.Fatalf("with --profile: alias = %q, err = %v; want from-flag", got.GetAlias(), err)
	}
}

func TestNoProfileConfiguredSteersTowardsForm(t *testing.T) {
	config.SetConfigDir(t.TempDir())
	t.Cleanup(func() { config.SetConfigDir("") })

	_, err := ResolveProfile(&GlobalFlags{})
	if err == nil {
		t.Fatal("expected an error with no profile and no env credentials")
	}
	// The hint must not tell an agent to ask the user to paste secrets.
	if !strings.Contains(err.Error(), "--form") && !strings.Contains(hintOf(err), "--form") {
		t.Fatalf("hint should steer to --form: %v / %s", err, hintOf(err))
	}
}

func TestStoredButIncompleteCredentialNamesTheMissingFields(t *testing.T) {
	withStubbedCredentials(t, credential.Set{Login: "l"}) // no trans-key, no secret
	if err := config.StoreProfile("prod", config.Profile{}); err != nil {
		t.Fatalf("StoreProfile: %v", err)
	}

	_, err := ResolveProfile(&GlobalFlags{})
	if err == nil {
		t.Fatal("expected an error for an incomplete stored credential")
	}
	hint := hintOf(err)
	for _, want := range []string{"--trans-key", "--secret-key"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint does not name the missing field %q: %s", want, hint)
		}
	}
}

// GetAlias is nil-safe so a failed resolve can still be reported in a test
// message without panicking.
func (r *ResolvedProfile) GetAlias() string {
	if r == nil {
		return ""
	}
	return r.Alias
}

func hintOf(err error) string {
	if apiErr, ok := err.(*agenterrors.APIError); ok {
		return apiErr.Hint
	}
	return err.Error()
}

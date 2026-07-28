package config

import (
	"testing"
)

func TestNormalizeDefaultsToLive(t *testing.T) {
	got := Normalize(Profile{})

	if got.Environment != EnvironmentLive {
		t.Fatalf("Environment = %q, want %q", got.Environment, EnvironmentLive)
	}
	if got.BaseURL != LiveBaseURL {
		t.Fatalf("BaseURL = %q, want %q", got.BaseURL, LiveBaseURL)
	}
	if got.PayoutsBaseURL != LivePayoutsBaseURL {
		t.Fatalf("PayoutsBaseURL = %q, want %q", got.PayoutsBaseURL, LivePayoutsBaseURL)
	}
	if got.Country != DefaultCountry {
		t.Fatalf("Country = %q, want %q", got.Country, DefaultCountry)
	}
}

// Payouts lives on a separate marketplace host whose sandbox domain is
// dlocal-sbox.com, not dlocal.com — an easy thing to get wrong by pattern
// matching on the payins host.
func TestNormalizeSandboxPicksSeparatePayoutsHost(t *testing.T) {
	got := Normalize(Profile{Environment: EnvironmentSandbox})

	if got.BaseURL != SandboxBaseURL {
		t.Fatalf("BaseURL = %q, want %q", got.BaseURL, SandboxBaseURL)
	}
	if got.PayoutsBaseURL != SandboxPayoutsBaseURL {
		t.Fatalf("PayoutsBaseURL = %q, want %q", got.PayoutsBaseURL, SandboxPayoutsBaseURL)
	}
}

// mTLS moves the sandbox to a different host, so configuring a client
// certificate has to select it.
func TestNormalizeSandboxWithCertUsesCertHost(t *testing.T) {
	got := Normalize(Profile{Environment: EnvironmentSandbox, CertPath: "/tmp/client.pem"})

	if got.BaseURL != SandboxCertBaseURL {
		t.Fatalf("BaseURL = %q, want %q", got.BaseURL, SandboxCertBaseURL)
	}
}

func TestNormalizeKeepsExplicitBaseURL(t *testing.T) {
	got := Normalize(Profile{Environment: EnvironmentSandbox, BaseURL: "https://merchant.example.test"})

	if got.BaseURL != "https://merchant.example.test" {
		t.Fatalf("explicit BaseURL was overwritten: %q", got.BaseURL)
	}
}

func TestProfileRoundTripsThroughDisk(t *testing.T) {
	SetConfigDir(t.TempDir())
	t.Cleanup(func() { SetConfigDir("") })

	if err := StoreProfile("prod", Profile{Environment: EnvironmentSandbox, CertPath: "/tmp/c.pem", KeyPath: "/tmp/c.key"}); err != nil {
		t.Fatalf("StoreProfile: %v", err)
	}

	cfg := Read()
	if cfg.DefaultProfile != "prod" {
		t.Fatalf("first profile did not become default: %q", cfg.DefaultProfile)
	}
	profile := cfg.Profiles["prod"]
	if profile.PayoutsBaseURL != SandboxPayoutsBaseURL {
		t.Fatalf("stored profile was not normalized: %+v", profile)
	}
	if profile.KeyPath != "/tmp/c.key" {
		t.Fatalf("KeyPath = %q, want /tmp/c.key", profile.KeyPath)
	}
}

func TestRemoveProfileReassignsDefault(t *testing.T) {
	SetConfigDir(t.TempDir())
	t.Cleanup(func() { SetConfigDir("") })

	for _, alias := range []string{"a", "b"} {
		if err := StoreProfile(alias, Profile{}); err != nil {
			t.Fatalf("StoreProfile %s: %v", alias, err)
		}
	}
	if err := RemoveProfile("a"); err != nil {
		t.Fatalf("RemoveProfile: %v", err)
	}

	if got := Read().DefaultProfile; got != "b" {
		t.Fatalf("DefaultProfile after removing the default = %q, want b", got)
	}
}

func TestSetDefaultValueRejectsUnknownKey(t *testing.T) {
	SetConfigDir(t.TempDir())
	t.Cleanup(func() { SetConfigDir("") })

	if err := SetDefaultValue("nonsense", 1); err == nil {
		t.Fatal("SetDefaultValue accepted an unknown key")
	}
	if err := SetDefaultValue("max_retries", 5); err != nil {
		t.Fatalf("SetDefaultValue: %v", err)
	}
	if got := Read().Defaults.MaxRetries; got == nil || *got != 5 {
		t.Fatalf("MaxRetries = %v, want 5", got)
	}
}

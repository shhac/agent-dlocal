package config

import (
	"errors"
	"fmt"
	"sync"
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

// A failed write and an unknown key are different problems with different
// fixes. They used to be indistinguishable, so the CLI reported disk failures
// as "unknown config key" — naming a key that was in fact valid.
func TestUnknownKeyIsASentinelDistinctFromWriteFailures(t *testing.T) {
	SetConfigDir(t.TempDir())
	t.Cleanup(func() { SetConfigDir("") })

	if err := SetDefaultValue("nonsense", 1); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("SetDefaultValue with a bad key: err = %v, want ErrUnknownKey", err)
	}
	if err := UnsetDefaultValue("nonsense"); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("UnsetDefaultValue with a bad key: err = %v, want ErrUnknownKey", err)
	}
	if _, err := ReadDefaultValue("nonsense"); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("ReadDefaultValue with a bad key: err = %v, want ErrUnknownKey", err)
	}

	// A write failure must NOT masquerade as an unknown key.
	SetConfigDir("/proc/nonexistent-agent-dlocal-test")
	err := SetDefaultValue("max_retries", 5)
	if err == nil {
		t.Fatal("expected a write failure against an unwritable directory")
	}
	if errors.Is(err, ErrUnknownKey) {
		t.Fatalf("a write failure was reported as an unknown key: %v", err)
	}
}

// The key set has one definition; get/set/unset and the CLI's hint all read it.
func TestDefaultKeysCoversEveryAccessor(t *testing.T) {
	SetConfigDir(t.TempDir())
	t.Cleanup(func() { SetConfigDir("") })

	keys := DefaultKeys()
	if len(keys) != len(defaultAccessors) {
		t.Fatalf("DefaultKeys returned %d keys for %d accessors", len(keys), len(defaultAccessors))
	}
	for _, key := range keys {
		if err := SetDefaultValue(key, 7); err != nil {
			t.Fatalf("SetDefaultValue(%q): %v", key, err)
		}
		got, err := ReadDefaultValue(key)
		if err != nil || got == nil || *got != 7 {
			t.Fatalf("ReadDefaultValue(%q) = %v, %v; want 7", key, got, err)
		}
		if err := UnsetDefaultValue(key); err != nil {
			t.Fatalf("UnsetDefaultValue(%q): %v", key, err)
		}
		if got, _ := ReadDefaultValue(key); got != nil {
			t.Fatalf("ReadDefaultValue(%q) after unset = %v, want nil", key, got)
		}
	}
}

// Two CLI invocations at once each used to build their write from a snapshot
// taken before the other landed, so all but the last were erased — with twenty
// concurrent writers, one profile survived. StoreProfile now holds the store's
// lock across read, mutate and write, so every profile must survive.
func TestConcurrentStoreProfileKeepsEveryProfile(t *testing.T) {
	SetConfigDir(t.TempDir())
	t.Cleanup(func() { SetConfigDir("") })

	const writers = 20

	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			alias := fmt.Sprintf("merchant-%02d", i)
			if err := StoreProfile(alias, Profile{Environment: EnvironmentSandbox}); err != nil {
				errs <- fmt.Errorf("StoreProfile(%q): %w", alias, err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	// Read from disk, not the cache, so this measures what actually persisted.
	cfg := loadConfig()
	if len(cfg.Profiles) != writers {
		t.Fatalf("%d of %d profiles survived concurrent writes: %v", len(cfg.Profiles), writers, cfg.Profiles)
	}
	for i := range writers {
		alias := fmt.Sprintf("merchant-%02d", i)
		if _, ok := cfg.Profiles[alias]; !ok {
			t.Fatalf("profile %q was lost to a concurrent write", alias)
		}
	}
}

// The defaults block is a second read-modify-write against the same file, and
// it interleaves with profile writes in practice (`auth add` racing
// `config set`). Neither may erase the other.
func TestConcurrentDefaultAndProfileWritesBothSurvive(t *testing.T) {
	SetConfigDir(t.TempDir())
	t.Cleanup(func() { SetConfigDir("") })

	const writers = 20

	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				alias := fmt.Sprintf("merchant-%02d", i)
				if err := StoreProfile(alias, Profile{}); err != nil {
					errs <- fmt.Errorf("StoreProfile(%q): %w", alias, err)
				}
				return
			}
			if err := SetDefaultValue("timeout_ms", 1000+i); err != nil {
				errs <- fmt.Errorf("SetDefaultValue: %w", err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	cfg := loadConfig()
	if len(cfg.Profiles) != writers/2 {
		t.Fatalf("%d of %d profiles survived: %v", len(cfg.Profiles), writers/2, cfg.Profiles)
	}
	if cfg.Defaults.TimeoutMS == nil {
		t.Fatal("timeout_ms was erased by a concurrent profile write")
	}
}

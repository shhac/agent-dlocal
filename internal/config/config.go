package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"

	"github.com/shhac/lib-agent-cli/creds"
	"github.com/shhac/lib-agent-cli/xdg"
)

// Environment is a HOST distinction, not something derivable from a credential:
// dLocal keys carry no live/sandbox prefix, so the profile records it explicitly.
const (
	EnvironmentLive    = "live"
	EnvironmentSandbox = "sandbox"
)

// Base URLs per environment. Payouts v2 lives on a separate marketplace host
// with its own sandbox domain (dlocal-sbox.com, not dlocal.com).
const (
	LiveBaseURL           = "https://api.dlocal.com"
	SandboxBaseURL        = "https://sandbox.dlocal.com"
	SandboxCertBaseURL    = "https://sandbox-cert.dlocal.com"
	LivePayoutsBaseURL    = "https://marketplace-api.dlocal.com"
	SandboxPayoutsBaseURL = "https://marketplace-api.dlocal-sbox.com"
)

// DefaultCountry is used by `auth check` and as the fallback for
// payment-methods lookups. Brazil is dLocal's largest market.
const DefaultCountry = "BR"

type Config struct {
	DefaultProfile string             `json:"default_profile,omitempty"`
	Defaults       Defaults           `json:"defaults,omitempty"`
	Profiles       map[string]Profile `json:"profiles"`
}

type Defaults struct {
	TimeoutMS  *int `json:"timeout_ms,omitempty"`
	MaxRetries *int `json:"max_retries,omitempty"`
}

// Profile is the non-secret metadata for one dLocal merchant credential set.
// Nothing here is a secret: the three secrets and the optional key passphrase
// live as a single opaque keychain item (see internal/credential).
type Profile struct {
	Environment    string `json:"environment,omitempty"`
	BaseURL        string `json:"base_url,omitempty"`
	PayoutsBaseURL string `json:"payouts_base_url,omitempty"`
	// CertPath/KeyPath are paths, not contents. mTLS is optional and per
	// merchant; the key file stays where the user put it under their own
	// permissions rather than being copied into our config.
	CertPath string `json:"cert_path,omitempty"`
	KeyPath  string `json:"key_path,omitempty"`
	Country  string `json:"country,omitempty"`
}

var (
	cache       *Config
	cacheMu     sync.Mutex
	overrideDir string
)

func SetConfigDir(dir string) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	overrideDir = dir
	cache = nil
}

func ConfigDir() string {
	if overrideDir != "" {
		return overrideDir
	}
	return xdg.ConfigDir("agent-dlocal")
}

func ConfigPath() string {
	return filepath.Join(ConfigDir(), "config.json")
}

// store is config.json's file: 0600 writes into a 0700 parent, atomic
// replacement by rename, and Update for a locked read-modify-write. This was
// hand-rolled with os.ReadFile/os.WriteFile, which carried a lost-update race —
// two concurrent invocations (`auth add` racing `auth use`) each built their
// write from a snapshot taken before the other landed, so all but the last were
// erased.
//
// The 0700 parent is the reason the directory mode is not left to chance: it is
// shared with credentials.json, which holds real secrets whenever the keychain
// is unavailable.
func store() creds.Store {
	return creds.Store{Path: ConfigPath()}
}

func Read() *Config {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cache != nil {
		return cache
	}
	cache = loadConfig()
	return cache
}

// loadConfig reads config.json fresh from disk, bypassing the package cache. It
// is the single definition of "what a from-scratch read looks like", shared by
// Read (which caches the result) and updateConfig (which must never hand a
// mutate callback the stale in-memory cache while holding the store's lock).
func loadConfig() *Config {
	var cfg Config
	if err := store().Load(&cfg); err != nil {
		return defaultConfig()
	}
	if cfg.Profiles == nil {
		cfg.Profiles = make(map[string]Profile)
	}
	return &cfg
}

func Write(cfg *Config) error {
	if err := store().Save(cfg); err != nil {
		return err
	}
	cacheMu.Lock()
	cache = nil
	cacheMu.Unlock()
	return nil
}

// updateConfig applies mutate to a freshly loaded config under ONE exclusive
// lock spanning read, mutate and write, so two concurrent invocations serialize
// instead of each building its write from a stale snapshot. The package cache is
// bypassed entirely while the lock is held — mutate only ever sees what Update
// just read from disk — and invalidated afterwards so a later Read cannot hand
// back the pre-write value.
//
// A mutate that returns an error aborts the write, leaving the stored document
// untouched.
func updateConfig(mutate func(cfg *Config) error) error {
	var cfg Config
	err := store().Update(&cfg, func() error {
		if cfg.Profiles == nil {
			cfg.Profiles = make(map[string]Profile)
		}
		return mutate(&cfg)
	})

	cacheMu.Lock()
	cache = nil
	cacheMu.Unlock()

	return err
}

func defaultConfig() *Config {
	return &Config{
		Profiles: make(map[string]Profile),
	}
}

// Normalize fills a profile's derived fields from its environment. An explicit
// base URL always wins, so a merchant on a bespoke host keeps it.
func Normalize(profile Profile) Profile {
	if profile.Environment == "" {
		profile.Environment = EnvironmentLive
	}
	if profile.Country == "" {
		profile.Country = DefaultCountry
	}
	if profile.BaseURL == "" {
		profile.BaseURL = defaultBaseURL(profile)
	}
	if profile.PayoutsBaseURL == "" {
		profile.PayoutsBaseURL = defaultPayoutsBaseURL(profile.Environment)
	}
	return profile
}

// defaultBaseURL picks the payins host. Sandbox has two: the mTLS-enabled
// sandbox-cert host and the plain one — a profile with a client certificate
// configured needs the former, so the cert path selects it.
func defaultBaseURL(profile Profile) string {
	if profile.Environment != EnvironmentSandbox {
		return LiveBaseURL
	}
	if profile.CertPath != "" {
		return SandboxCertBaseURL
	}
	return SandboxBaseURL
}

func defaultPayoutsBaseURL(environment string) string {
	if environment == EnvironmentSandbox {
		return SandboxPayoutsBaseURL
	}
	return LivePayoutsBaseURL
}

func StoreProfile(alias string, profile Profile) error {
	return updateConfig(func(cfg *Config) error {
		cfg.Profiles[alias] = Normalize(profile)
		if cfg.DefaultProfile == "" {
			cfg.DefaultProfile = alias
		}
		return nil
	})
}

func RemoveProfile(alias string) error {
	return updateConfig(func(cfg *Config) error {
		delete(cfg.Profiles, alias)
		if cfg.DefaultProfile == alias {
			cfg.DefaultProfile = ""
			for name := range cfg.Profiles {
				cfg.DefaultProfile = name
				break
			}
		}
		return nil
	})
}

// SetDefault rejects an unconfigured alias from inside the mutate callback, so
// the error aborts Update before it writes rather than after.
func SetDefault(alias string) error {
	return updateConfig(func(cfg *Config) error {
		if _, ok := cfg.Profiles[alias]; !ok {
			return fmt.Errorf("profile %q is not configured", alias)
		}
		cfg.DefaultProfile = alias
		return nil
	})
}

// defaultAccessors is the single definition of the settable config keys.
//
// This used to be four parallel switches across two packages — get, set, unset,
// and a bare name list — so adding a key meant four coordinated edits and
// forgetting one failed silently rather than at compile time.
var defaultAccessors = map[string]struct {
	get func(Defaults) *int
	set func(*Defaults, *int)
}{
	"timeout_ms": {
		get: func(d Defaults) *int { return d.TimeoutMS },
		set: func(d *Defaults, v *int) { d.TimeoutMS = v },
	},
	"max_retries": {
		get: func(d Defaults) *int { return d.MaxRetries },
		set: func(d *Defaults, v *int) { d.MaxRetries = v },
	},
}

// ErrUnknownKey is returned for a key that is not settable. It is a sentinel so
// callers can tell an unknown key from a failed write with errors.Is, rather
// than reporting every failure as "unknown key" — which is what the CLI did
// while both came back as anonymous errors.
var ErrUnknownKey = errors.New("unknown config key")

// DefaultKeys lists the settable keys in a stable order, so the CLI can render
// them in hints without restating the set.
func DefaultKeys() []string {
	keys := make([]string, 0, len(defaultAccessors))
	for key := range defaultAccessors {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// ReadDefaultValue returns the stored value for key, or nil when unset.
func ReadDefaultValue(key string) (*int, error) {
	accessor, ok := defaultAccessors[key]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownKey, key)
	}
	return accessor.get(Read().Defaults), nil
}

func SetDefaultValue(key string, value int) error {
	return writeDefault(key, &value)
}

func UnsetDefaultValue(key string) error {
	return writeDefault(key, nil)
}

func writeDefault(key string, value *int) error {
	accessor, ok := defaultAccessors[key]
	if !ok {
		return fmt.Errorf("%w %q", ErrUnknownKey, key)
	}
	return updateConfig(func(cfg *Config) error {
		accessor.set(&cfg.Defaults, value)
		return nil
	})
}

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/shhac/lib-agent-cli/xdg"
)

// dLocal's API version header. Constant across every payins call.
const APIVersion = "2.1"

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

func configPath() string {
	return filepath.Join(ConfigDir(), "config.json")
}

func ConfigPath() string {
	return configPath()
}

func Read() *Config {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cache != nil {
		return cache
	}
	data, err := os.ReadFile(configPath())
	if err != nil {
		cache = defaultConfig()
		return cache
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		cache = defaultConfig()
		return cache
	}
	if cfg.Profiles == nil {
		cfg.Profiles = make(map[string]Profile)
	}
	cache = &cfg
	return cache
}

func Write(cfg *Config) error {
	cacheMu.Lock()
	cache = nil
	cacheMu.Unlock()

	// 0700: this directory is shared with credentials.json, which holds real
	// secrets whenever the keychain is unavailable.
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), append(data, '\n'), 0o644)
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
	cfg := Read()
	cfg.Profiles[alias] = Normalize(profile)
	if cfg.DefaultProfile == "" {
		cfg.DefaultProfile = alias
	}
	return Write(cfg)
}

func RemoveProfile(alias string) error {
	cfg := Read()
	delete(cfg.Profiles, alias)
	if cfg.DefaultProfile == alias {
		cfg.DefaultProfile = ""
		for name := range cfg.Profiles {
			cfg.DefaultProfile = name
			break
		}
	}
	return Write(cfg)
}

func SetDefault(alias string) error {
	cfg := Read()
	if _, ok := cfg.Profiles[alias]; !ok {
		return fmt.Errorf("profile %q is not configured", alias)
	}
	cfg.DefaultProfile = alias
	return Write(cfg)
}

func UpdateProfile(alias string, update func(Profile) Profile) error {
	cfg := Read()
	profile, ok := cfg.Profiles[alias]
	if !ok {
		return fmt.Errorf("profile %q is not configured", alias)
	}
	cfg.Profiles[alias] = Normalize(update(profile))
	return Write(cfg)
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
	cfg := Read()
	accessor.set(&cfg.Defaults, value)
	return Write(cfg)
}

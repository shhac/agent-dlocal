package shared

import (
	"context"
	"net/url"
	"os"
	"strings"

	"github.com/shhac/agent-dlocal/internal/api"
	"github.com/shhac/agent-dlocal/internal/config"
	"github.com/shhac/agent-dlocal/internal/credential"
	agenterrors "github.com/shhac/agent-dlocal/internal/errors"
)

// UserAgent is sent on every request and is part of dLocal's required header
// set. Set from the root command at startup so it carries the real version.
var UserAgent = "agent-dlocal/dev"

type ResolvedProfile struct {
	Alias            string
	Profile          config.Profile
	Credentials      credential.Set
	CredentialSource string
}

var credentialGet = credential.Get

// ResolveProfile finds the credentials and non-secret metadata for this
// invocation: an explicit env credential set wins, otherwise the named or
// default profile is read from the keychain.
func ResolveProfile(flags *GlobalFlags) (*ResolvedProfile, error) {
	cfg := config.Read()
	alias := firstNonEmpty(flags.Profile, os.Getenv("AGENT_DLOCAL_PROFILE"), cfg.DefaultProfile)

	if set, ok := credentialsFromEnv(); ok {
		return &ResolvedProfile{
			Alias:            alias,
			Profile:          profileFromEnv(cfg, alias),
			Credentials:      set,
			CredentialSource: "env",
		}, nil
	}

	if alias == "" {
		return nil, agenterrors.New("No dLocal profile configured", agenterrors.FixableByHuman).
			WithHint("Run 'agent-dlocal auth add <profile> --form' so the user enters their X-Login, X-Trans-Key, and secret key in a native dialog — do not ask them to paste credentials into chat")
	}

	profile, ok := cfg.Profiles[alias]
	if !ok {
		return nil, agenterrors.Newf(agenterrors.FixableByHuman, "Profile %q is not configured", alias).
			WithHint("Run 'agent-dlocal auth list' to see configured profiles")
	}

	set, err := credentialGet(alias)
	if err != nil {
		return nil, agenterrors.Wrap(err, agenterrors.FixableByHuman).
			WithHint("Re-add the profile with 'agent-dlocal auth add " + alias + " --form'")
	}
	if !set.Complete() {
		return nil, agenterrors.Newf(agenterrors.FixableByHuman, "Stored credential for profile %q is incomplete", alias).
			WithHint("Missing: " + joinMissing(set.Missing()) + ". Re-add with 'agent-dlocal auth add " + alias + " --form'")
	}

	return &ResolvedProfile{
		Alias:            alias,
		Profile:          config.Normalize(profile),
		Credentials:      set,
		CredentialSource: "keychain",
	}, nil
}

// credentialsFromEnv reads a full credential set from the environment. All
// three must be present — a partial set is a misconfiguration that would
// otherwise fail later as an opaque 401.
func credentialsFromEnv() (credential.Set, bool) {
	set := credential.Set{
		Login:         os.Getenv("DLOCAL_X_LOGIN"),
		TransKey:      os.Getenv("DLOCAL_X_TRANS_KEY"),
		SecretKey:     os.Getenv("DLOCAL_SECRET_KEY"),
		KeyPassphrase: os.Getenv("DLOCAL_KEY_PASSPHRASE"),
	}
	return set, set.Complete()
}

func profileFromEnv(cfg *config.Config, alias string) config.Profile {
	if profile, ok := cfg.Profiles[alias]; ok {
		return config.Normalize(profile)
	}
	return config.Normalize(config.Profile{})
}

// baseURL applies the override precedence: --base-url, then the env override,
// then the profile's host.
func baseURL(flags *GlobalFlags, profileURL string) string {
	return firstNonEmpty(flags.BaseURL, os.Getenv("AGENT_DLOCAL_BASE_URL"), profileURL)
}

// Host selects which dLocal service a command talks to. Payins and payouts
// differ by HOST ONLY — same credentials, same signing scheme (see the note in
// internal/api/signer.go) — so this is a data value rather than a parallel set
// of constructors.
//
// There used to be eight entry points here (With/WithPayouts/WithResolved/
// WithResolvedProfile × Get/GetPayout for entities and raw items), varying
// along this single axis. They were parallel constructors from when payouts
// genuinely needed a different signer as well as a different host; once the
// signer difference was disproved, only a URL field remained.
type Host int

const (
	HostPayins Host = iota
	HostPayouts
)

func (h Host) baseURL(profile config.Profile) string {
	if h == HostPayouts {
		return profile.PayoutsBaseURL
	}
	return profile.BaseURL
}

// Session is what a command needs to do its work: a configured client plus the
// non-secret profile metadata some commands report or default from.
type Session struct {
	Client  *api.Client
	Profile config.Profile
	Alias   string
	Source  string
	// BaseURL is the host actually in use after --base-url and the env override
	// are applied — not the profile's stored one. `auth check` reports it, and
	// reporting the stored URL while verifying against another is how a check
	// ends up naming the wrong ledger.
	BaseURL string
}

// WithSession resolves the profile, builds a client for the chosen host, and
// runs fn.
func WithSession(flags *GlobalFlags, host Host, fn func(context.Context, *Session) error) error {
	resolved, err := ResolveProfile(flags)
	if err != nil {
		return err
	}

	url := baseURL(flags, host.baseURL(resolved.Profile))
	if flags.Debug {
		// Nothing here is a secret: the profile alias, the backend that holds
		// the credentials, and the host. Never the credentials themselves.
		WriteDebug(map[string]any{
			"@debug":            "client",
			"profile":           resolved.Alias,
			"credential_source": resolved.CredentialSource,
			"environment":       resolved.Profile.Environment,
			"base_url":          url,
			"signer":            api.SignatureScheme,
			"mtls":              resolved.Profile.CertPath != "",
			"timeout_ms":        flags.TimeoutMS,
			"max_retries":       flags.MaxRetries,
		})
	}

	client, err := api.NewClient(api.Options{
		BaseURL:     url,
		Credentials: signingCredentials(resolved),
		UserAgent:   UserAgent,
		MaxRetries:  flags.MaxRetries,
		CertPath:    resolved.Profile.CertPath,
		KeyPath:     resolved.Profile.KeyPath,
	})
	if err != nil {
		return err
	}
	client.SetDebug(flags.Debug)
	client.SetDebugRedaction(RedactionOptions(flags))

	ctx, cancel := ContextWithTimeout(context.Background(), flags.TimeoutMS)
	defer cancel()

	return fn(ctx, &Session{
		Client:  client,
		Profile: resolved.Profile,
		Alias:   resolved.Alias,
		Source:  resolved.CredentialSource,
		BaseURL: url,
	})
}

// WithSessionResult is WithSession for a command that produces a value.
//
// Without it every caller has to declare an outer variable, assign it inside
// the closure and return it afterwards — a workaround for an error-only
// contract that four investigate commands each copied.
func WithSessionResult[T any](flags *GlobalFlags, host Host, fn func(context.Context, *Session) (T, error)) (T, error) {
	var result T
	err := WithSession(flags, host, func(ctx context.Context, session *Session) error {
		var err error
		result, err = fn(ctx, session)
		return err
	})
	return result, err
}

func signingCredentials(resolved *ResolvedProfile) api.Credentials {
	return api.Credentials{
		Login:     resolved.Credentials.Login,
		TransKey:  resolved.Credentials.TransKey,
		SecretKey: resolved.Credentials.SecretKey,
	}
}

// GetRawItem fetches one path and writes the redacted result.
func GetRawItem(flags *GlobalFlags, host Host, path string, params url.Values) error {
	return WithSession(flags, host, func(ctx context.Context, session *Session) error {
		item, err := FetchItem(ctx, session.Client, flags, path, params)
		if err != nil {
			return err
		}
		WriteItem(item, flags.Format)
		return nil
	})
}

func joinMissing(missing []string) string {
	flags := make([]string, len(missing))
	for i, name := range missing {
		flags[i] = "--" + name
	}
	return strings.Join(flags, ", ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

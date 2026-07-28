package shared

import (
	"context"
	"net/url"
	"os"

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

func WithClient(flags *GlobalFlags, fn func(context.Context, *api.Client) error) error {
	resolved, err := ResolveProfile(flags)
	if err != nil {
		return err
	}
	signer := api.PayinsSigner{Creds: signingCredentials(resolved), UserAgent: UserAgent}
	return withClient(flags, resolved, baseURL(flags, resolved.Profile.BaseURL), signer, fn)
}

// WithPayoutsClient targets the payouts host. It is a separate service on a
// separate domain with a different signing scheme, so it gets its own
// constructor rather than a flag on the payins one.
func WithPayoutsClient(flags *GlobalFlags, fn func(context.Context, *api.Client) error) error {
	resolved, err := ResolveProfile(flags)
	if err != nil {
		return err
	}
	signer := api.PayoutsSigner{Creds: signingCredentials(resolved), UserAgent: UserAgent}
	return withClient(flags, resolved, baseURL(flags, resolved.Profile.PayoutsBaseURL), signer, fn)
}

func WithResolvedClient(flags *GlobalFlags, resolved *ResolvedProfile, fn func(context.Context, *api.Client) error) error {
	signer := api.PayinsSigner{Creds: signingCredentials(resolved), UserAgent: UserAgent}
	return withClient(flags, resolved, baseURL(flags, resolved.Profile.BaseURL), signer, fn)
}

func signingCredentials(resolved *ResolvedProfile) api.Credentials {
	return api.Credentials{
		Login:     resolved.Credentials.Login,
		TransKey:  resolved.Credentials.TransKey,
		SecretKey: resolved.Credentials.SecretKey,
	}
}

func withClient(flags *GlobalFlags, resolved *ResolvedProfile, url string, signer api.Signer, fn func(context.Context, *api.Client) error) error {
	if flags.Debug {
		// Nothing here is a secret: the profile alias, the backend that holds
		// the credentials, and the host. Never the credentials themselves.
		WriteDebug(map[string]any{
			"@debug":            "client",
			"profile":           resolved.Alias,
			"credential_source": resolved.CredentialSource,
			"environment":       resolved.Profile.Environment,
			"base_url":          url,
			"signer":            signer.Name(),
			"mtls":              resolved.Profile.CertPath != "",
			"timeout_ms":        flags.TimeoutMS,
			"max_retries":       flags.MaxRetries,
		})
	}

	client, err := api.NewClient(api.Options{
		BaseURL:    url,
		Signer:     signer,
		MaxRetries: flags.MaxRetries,
		CertPath:   resolved.Profile.CertPath,
		KeyPath:    resolved.Profile.KeyPath,
	})
	if err != nil {
		return err
	}
	client.SetDebug(flags.Debug)
	client.SetDebugRedaction(RedactionOptions(flags))

	ctx, cancel := ContextWithTimeout(context.Background(), flags.TimeoutMS)
	defer cancel()
	return fn(ctx, client)
}

// GetRawItem fetches one path from the payins host and writes the redacted
// result.
func GetRawItem(flags *GlobalFlags, path string, params url.Values) error {
	return writeFetched(flags, path, params, WithClient)
}

// GetPayoutRawItem is GetRawItem against the payouts host and signer.
func GetPayoutRawItem(flags *GlobalFlags, path string, params url.Values) error {
	return writeFetched(flags, path, params, WithPayoutsClient)
}

func writeFetched(
	flags *GlobalFlags,
	path string,
	params url.Values,
	with func(*GlobalFlags, func(context.Context, *api.Client) error) error,
) error {
	return with(flags, func(ctx context.Context, client *api.Client) error {
		item, err := FetchItem(ctx, client, flags, path, params)
		if err != nil {
			return err
		}
		WriteItem(item, flags.Format)
		return nil
	})
}

func joinMissing(missing []string) string {
	out := ""
	for i, name := range missing {
		if i > 0 {
			out += ", "
		}
		out += "--" + name
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

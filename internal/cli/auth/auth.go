package auth

import (
	"context"
	"net/url"
	"sort"

	"github.com/spf13/cobra"

	"github.com/shhac/agent-dlocal/internal/api"
	"github.com/shhac/agent-dlocal/internal/cli/shared"
	"github.com/shhac/agent-dlocal/internal/config"
	"github.com/shhac/agent-dlocal/internal/credential"
)

var (
	credentialStore  = credential.Store
	credentialRemove = credential.Remove
	credentialGet    = credential.Get
)

func Register(root *cobra.Command, globals shared.GlobalsFunc) {
	auth := &cobra.Command{
		Use:   "auth",
		Short: "Manage dLocal credentials and profiles",
	}

	registerAdd(auth, globals, "add", "Add a dLocal profile with keychain-stored credentials")
	registerAdd(auth, globals, "update", "Replace the stored credentials for a dLocal profile")
	registerCheck(auth, globals)
	registerDefault(auth, globals)
	registerList(auth, globals)
	registerRemove(auth, globals)

	root.AddCommand(auth)
}

// credentialFlags are the non-interactive equivalents of the --form dialog.
// They exist for automation and tests; the README and SKILL steer humans and
// LLMs to --form so secrets never pass through a transcript.
type credentialFlags struct {
	login     string
	transKey  string
	secretKey string
	form      bool

	sandbox  bool
	baseURL  string
	certPath string
	keyPath  string
	country  string
}

func (f *credentialFlags) set() credential.Set {
	return credential.Set{
		Login:     f.login,
		TransKey:  f.transKey,
		SecretKey: f.secretKey,
	}
}

func (f *credentialFlags) profile() config.Profile {
	environment := config.EnvironmentLive
	if f.sandbox {
		environment = config.EnvironmentSandbox
	}
	return config.Profile{
		Environment: environment,
		BaseURL:     f.baseURL,
		CertPath:    f.certPath,
		KeyPath:     f.keyPath,
		Country:     f.country,
	}
}

func (f *credentialFlags) bind(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.BoolVar(&f.form, "form", false, "Prompt for each missing secret in a native OS dialog (the LLM never sees the input)")
	flags.StringVar(&f.login, "login", "", "X-Login value (prefer --form)")
	flags.StringVar(&f.transKey, "trans-key", "", "X-Trans-Key value (prefer --form)")
	flags.StringVar(&f.secretKey, "secret-key", "", "Secret key used for request signing (prefer --form)")
	flags.BoolVar(&f.sandbox, "sandbox", false, "Point this profile at dLocal sandbox instead of live")
	flags.StringVar(&f.baseURL, "base-url", "", "Explicit payins base URL, for a merchant on a bespoke host")
	flags.StringVar(&f.certPath, "cert", "", "Path to the mTLS client certificate (optional; the path is stored, not the file)")
	flags.StringVar(&f.keyPath, "key", "", "Path to the mTLS client key (optional; the path is stored, not the file)")
	flags.StringVar(&f.country, "country", config.DefaultCountry, "Default country for 'auth check' and payment-method lookups")
}

func registerAdd(parent *cobra.Command, globals shared.GlobalsFunc, use, short string) {
	flags := &credentialFlags{}

	cmd := &cobra.Command{
		Use:   use + " <profile>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			alias := args[0]

			set := flags.set()
			if flags.form {
				filled, err := promptCredentialsViaDialog(cmd.Context(), alias, set)
				if err != nil {
					return err
				}
				set = filled
			}

			// On update, anything the caller did not supply falls back to what
			// is already stored, so changing non-secret metadata (say, flipping
			// --sandbox) does not force the user to re-enter three secrets they
			// have not changed. Supplied values always win, so
			// `--form` still replaces and `--secret-key X` still rotates one.
			if use == "update" {
				set = fillFromStored(alias, set)
			}

			if !set.Complete() {
				return shared.RequireFlag(set.Missing()[0], "",
					"Prefer 'agent-dlocal auth "+use+" "+alias+" --form' so the user types the secrets into a native dialog. "+
						"Non-interactively, supply --login, --trans-key and --secret-key.")
			}

			storage, err := credentialStore(alias, set)
			if err != nil {
				return err
			}

			profile := config.Normalize(flags.profile())
			if err := config.StoreProfile(alias, profile); err != nil {
				return err
			}

			shared.WriteItem(map[string]any{
				"status":           statusFor(use),
				"profile":          alias,
				"storage":          storage,
				"environment":      profile.Environment,
				"base_url":         profile.BaseURL,
				"payouts_base_url": profile.PayoutsBaseURL,
				"country":          profile.Country,
				"mtls":             profile.CertPath != "",
			}, globals().Format)
			return nil
		},
	}
	flags.bind(cmd)
	parent.AddCommand(cmd)
}

func statusFor(use string) string {
	if use == "update" {
		return "updated"
	}
	return "added"
}

// fillFromStored backfills secrets the caller did not supply from the profile's
// existing credential set. A profile with nothing stored yet is not an error
// here — the completeness check downstream reports what is still missing.
func fillFromStored(alias string, set credential.Set) credential.Set {
	stored, err := credentialGet(alias)
	if err != nil {
		return set
	}
	if set.Login == "" {
		set.Login = stored.Login
	}
	if set.TransKey == "" {
		set.TransKey = stored.TransKey
	}
	if set.SecretKey == "" {
		set.SecretKey = stored.SecretKey
	}
	return set
}

// registerCheck verifies credentials end to end. dLocal has no /account
// endpoint, so there is no natural "who am I" call; payments-methods is the
// cheapest authenticated read and proves login, trans-key, secret, clock skew
// and signature construction are all correct in one round trip.
func registerCheck(parent *cobra.Command, globals shared.GlobalsFunc) {
	cmd := &cobra.Command{
		Use:   "check [profile]",
		Short: "Verify stored credentials with one authenticated read",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := globals()
			if len(args) > 0 {
				flags.Profile = args[0]
			}

			return shared.WithSession(flags, shared.HostPayins, func(ctx context.Context, session *shared.Session) error {
				// The global --country wins over the profile's stored one, so
				// probing a different market is the same flag here as everywhere.
				lookupCountry := flags.ResolveCountry("", session.Profile.Country)

				methods, err := shared.FetchItem(ctx, session.Client, flags, "/payments-methods", url.Values{"country": {lookupCountry}})
				if err != nil {
					return err
				}
				shared.WriteItem(map[string]any{
					"status":            "ok",
					"profile":           session.Alias,
					"credential_source": session.Source,
					"environment":       session.Profile.Environment,
					// The host actually used, not the profile's stored one:
					// --base-url overrides it, and reporting the stored URL while
					// verifying against another names the wrong ledger.
					"base_url":              session.BaseURL,
					"country":               lookupCountry,
					"payment_methods_found": countMethods(methods),
					"signature_scheme":      api.SignatureScheme,
					"verified_by":           "GET /payments-methods",
				}, flags.Format)
				return nil
			})
		},
	}
	parent.AddCommand(cmd)
}

func countMethods(methods any) int {
	list, ok := methods.([]any)
	if !ok {
		return 0
	}
	return len(list)
}

func registerDefault(parent *cobra.Command, globals shared.GlobalsFunc) {
	cmd := &cobra.Command{
		Use:   "default <profile>",
		Short: "Set the default profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			alias := args[0]
			if err := config.SetDefault(alias); err != nil {
				return err
			}
			shared.WriteItem(map[string]any{"status": "default_set", "profile": alias}, globals().Format)
			return nil
		},
	}
	parent.AddCommand(cmd)
}

// registerList reports profile metadata only. It calls credential.Storage,
// which reports WHERE a secret lives without reading it — there is no code path
// from `auth list` to a secret value.
func registerList(parent *cobra.Command, globals shared.GlobalsFunc) {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured profiles without exposing secrets",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Read()
			profiles := make([]map[string]any, 0, len(cfg.Profiles))
			for alias, profile := range cfg.Profiles {
				profile = config.Normalize(profile)
				storage, err := credential.Storage(alias)
				if err != nil {
					storage = "missing"
				}
				profiles = append(profiles, map[string]any{
					"profile":          alias,
					"default":          alias == cfg.DefaultProfile,
					"environment":      profile.Environment,
					"base_url":         profile.BaseURL,
					"payouts_base_url": profile.PayoutsBaseURL,
					"country":          profile.Country,
					"mtls":             profile.CertPath != "",
					"cert_path":        profile.CertPath,
					"credential":       storage,
				})
			}
			sort.Slice(profiles, func(i, j int) bool {
				return profiles[i]["profile"].(string) < profiles[j]["profile"].(string)
			})
			shared.WriteList(shared.ToAnySlice(profiles), globals().Format)
			return nil
		},
	}
	parent.AddCommand(cmd)
}

func registerRemove(parent *cobra.Command, globals shared.GlobalsFunc) {
	cmd := &cobra.Command{
		Use:   "remove <profile>",
		Short: "Remove a profile and its stored credentials",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			alias := args[0]
			if err := credentialRemove(alias); err != nil {
				return err
			}
			if err := config.RemoveProfile(alias); err != nil {
				return err
			}
			shared.WriteItem(map[string]any{"status": "removed", "profile": alias}, globals().Format)
			return nil
		},
	}
	parent.AddCommand(cmd)
}

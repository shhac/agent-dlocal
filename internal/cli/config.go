package cli

import (
	"strconv"

	"github.com/spf13/cobra"

	"github.com/shhac/agent-dlocal/internal/cli/shared"
	"github.com/shhac/agent-dlocal/internal/config"
	agenterrors "github.com/shhac/agent-dlocal/internal/errors"
	libcli "github.com/shhac/lib-agent-cli/cli"
)

// registerConfig builds the config group from the family's canonical
// ConfigCommand rather than hand-rolling get/set/unset/list — the boilerplate
// roughly eight sibling CLIs were each carrying their own copy of.
//
// It brings the uniform {key, value, set} record, --format handling via
// EmitItem, and the structured unknown-key error listing the valid names, so
// none of that is restated here. `show` and `path` are bespoke additions and
// stay.
//
// Credentials are never reachable from this group: they live in the credential
// backend and have no config representation at all.
func registerConfig(root *cobra.Command, globals shared.GlobalsFunc) {
	g := globals()
	cmd := libcli.ConfigCommand(&g.Globals, configKeys())

	cmd.AddCommand(
		&cobra.Command{
			Use:   "show",
			Short: "Show the effective configuration",
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg := config.Read()
				shared.WriteItem(map[string]any{
					"path":            config.ConfigPath(),
					"default_profile": cfg.DefaultProfile,
					"defaults":        cfg.Defaults,
					"profiles":        profileNames(cfg),
				}, globals().Format)
				return nil
			},
		},
		&cobra.Command{
			Use:   "path",
			Short: "Print the configuration file path",
			RunE: func(cmd *cobra.Command, args []string) error {
				shared.WriteItem(map[string]any{"path": config.ConfigPath()}, globals().Format)
				return nil
			},
		},
	)

	root.AddCommand(cmd)
}

// configKeys adapts internal/config's accessor table to the family's ConfigKey
// shape. The key set still has ONE definition — this reads it rather than
// restating it.
func configKeys() []libcli.ConfigKey {
	descriptions := map[string]string{
		"timeout_ms":  "Request timeout in milliseconds",
		"max_retries": "Automatic retries for transient 429/5xx responses",
	}

	names := config.DefaultKeys()
	keys := make([]libcli.ConfigKey, 0, len(names))
	for _, name := range names {
		keys = append(keys, libcli.ConfigKey{
			Name:        name,
			Description: descriptions[name],
			Get:         configGetter(name),
			Set:         configSetter(name),
			Unset:       configUnsetter(name),
		})
	}
	return keys
}

func configGetter(name string) func() (string, bool) {
	return func() (string, bool) {
		value, err := config.ReadDefaultValue(name)
		if err != nil || value == nil {
			return "", false
		}
		return strconv.Itoa(*value), true
	}
}

// configSetter parses before storing, so a non-integer is reported as such
// rather than surfacing as a storage failure.
func configSetter(name string) func(string) error {
	return func(raw string) error {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return agenterrors.Newf(agenterrors.FixableByAgent, "%q is not an integer", raw).
				WithHint("Every agent-dlocal config key takes an integer")
		}
		return config.SetDefaultValue(name, value)
	}
}

func configUnsetter(name string) func() error {
	return func() error { return config.UnsetDefaultValue(name) }
}

func profileNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	return names
}

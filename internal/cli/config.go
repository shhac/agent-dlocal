package cli

import (
	"strconv"

	"github.com/spf13/cobra"

	"github.com/shhac/agent-dlocal/internal/cli/shared"
	"github.com/shhac/agent-dlocal/internal/config"
	agenterrors "github.com/shhac/agent-dlocal/internal/errors"
)

// configKeys are the non-secret global defaults. Credentials are never
// reachable from this group — they live in the credential backend and have no
// config representation at all.
var configKeys = []string{"timeout_ms", "max_retries"}

func registerConfig(root *cobra.Command, globals shared.GlobalsFunc) {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and set non-secret global defaults",
	}

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
		&cobra.Command{
			Use:   "get <key>",
			Short: "Read one configuration key",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				value, err := readConfigKey(args[0])
				if err != nil {
					return err
				}
				shared.WriteItem(map[string]any{"key": args[0], "value": value}, globals().Format)
				return nil
			},
		},
		&cobra.Command{
			Use:   "set <key> <value>",
			Short: "Set one configuration key",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				value, err := strconv.Atoi(args[1])
				if err != nil {
					return agenterrors.Newf(agenterrors.FixableByAgent, "%q is not an integer", args[1]).
						WithHint("Both " + joinKeys() + " take integers")
				}
				if err := config.SetDefaultValue(args[0], value); err != nil {
					return unknownKeyError(args[0])
				}
				shared.WriteItem(map[string]any{"status": "set", "key": args[0], "value": value}, globals().Format)
				return nil
			},
		},
		&cobra.Command{
			Use:   "unset <key>",
			Short: "Clear one configuration key",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if err := config.UnsetDefaultValue(args[0]); err != nil {
					return unknownKeyError(args[0])
				}
				shared.WriteItem(map[string]any{"status": "unset", "key": args[0]}, globals().Format)
				return nil
			},
		},
	)

	root.AddCommand(cmd)
}

func readConfigKey(key string) (any, error) {
	defaults := config.Read().Defaults
	switch key {
	case "timeout_ms":
		return derefInt(defaults.TimeoutMS), nil
	case "max_retries":
		return derefInt(defaults.MaxRetries), nil
	default:
		return nil, unknownKeyError(key)
	}
}

func derefInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

// unknownKeyError replaces config's plain error with the structured contract,
// so a bad key reaches the agent as a fixable_by:agent hint listing the valid
// ones rather than as an opaque string.
func unknownKeyError(key string) error {
	return agenterrors.Newf(agenterrors.FixableByAgent, "unknown config key %q", key).
		WithHint("Valid keys: " + joinKeys())
}

func joinKeys() string {
	out := ""
	for i, key := range configKeys {
		if i > 0 {
			out += ", "
		}
		out += key
	}
	return out
}

func profileNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	return names
}

package cli

import (
	"errors"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/agent-dlocal/internal/cli/shared"
	"github.com/shhac/agent-dlocal/internal/config"
	agenterrors "github.com/shhac/agent-dlocal/internal/errors"
)

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
						WithHint("Both " + strings.Join(config.DefaultKeys(), ", ") + " take integers")
				}
				if err := config.SetDefaultValue(args[0], value); err != nil {
					return configError(err, args[0])
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
					return configError(err, args[0])
				}
				shared.WriteItem(map[string]any{"status": "unset", "key": args[0]}, globals().Format)
				return nil
			},
		},
	)

	root.AddCommand(cmd)
}

func readConfigKey(key string) (any, error) {
	value, err := config.ReadDefaultValue(key)
	if err != nil {
		return nil, configError(err, key)
	}
	if value == nil {
		return nil, nil
	}
	return *value, nil
}

// configError translates a config-package error into the structured contract.
//
// An unknown key is the agent's to fix and gets the valid-key list; anything
// else is a real failure — a failed write, a permissions problem — and must
// surface as itself. Reporting every failure as "unknown key" told the user to
// correct a key that was already correct.
func configError(err error, key string) error {
	if errors.Is(err, config.ErrUnknownKey) {
		return agenterrors.Newf(agenterrors.FixableByAgent, "unknown config key %q", key).
			WithHint("Valid keys: " + strings.Join(config.DefaultKeys(), ", "))
	}
	return agenterrors.Wrap(err, agenterrors.FixableByHuman).
		WithHint("Could not write " + config.ConfigPath() + "; check the path is writable")
}

func profileNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	return names
}

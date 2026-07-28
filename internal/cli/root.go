package cli

import (
	"github.com/spf13/cobra"

	"github.com/shhac/agent-dlocal/internal/cli/auth"
	"github.com/shhac/agent-dlocal/internal/cli/shared"
	"github.com/shhac/agent-dlocal/internal/config"
	"github.com/shhac/agent-dlocal/internal/output"
	libcli "github.com/shhac/lib-agent-cli/cli"
)

func newRootCmd(version string) *cobra.Command {
	globals := &shared.GlobalFlags{}
	globalsFunc := func() *shared.GlobalFlags {
		return globals
	}
	shared.UserAgent = "agent-dlocal/" + version

	var root *cobra.Command
	root = libcli.NewRoot(libcli.Options{
		Use:           "agent-dlocal",
		Short:         "dLocal payments investigation and triage CLI for AI agents",
		Version:       version,
		Globals:       &globals.Globals,
		DefaultFormat: output.FormatNDJSON,
		Redacts:       true,
		UnknownHint:   "run 'agent-dlocal usage' to see the available domains",
		// Precedence is explicit flag > persisted config > built-in default,
		// resolved here at the boundary so no command re-derives it.
		ConfigDefaults: func(*cobra.Command) {
			applyConfiguredDefaults(root, globals)
		},
	})

	pf := root.PersistentFlags()
	pf.StringVarP(&globals.Profile, "profile", "p", "", "dLocal profile alias (or AGENT_DLOCAL_PROFILE)")
	pf.StringVar(&globals.BaseURL, "base-url", "", "dLocal API base URL override (or AGENT_DLOCAL_BASE_URL)")
	pf.IntVar(&globals.MaxRetries, "max-retries", 2, "Maximum automatic retries for transient dLocal 429/5xx responses")
	_ = pf.MarkHidden("base-url")

	auth.Register(root, globalsFunc)
	registerConfig(root, globalsFunc)

	installGroupUnknownHandlers(root)

	return root
}

func applyConfiguredDefaults(root *cobra.Command, globals *shared.GlobalFlags) {
	if root == nil {
		return
	}
	cfg := config.Read()
	flags := root.PersistentFlags()
	if cfg.Defaults.TimeoutMS != nil && !flags.Changed("timeout") {
		globals.TimeoutMS = *cfg.Defaults.TimeoutMS
	}
	if cfg.Defaults.MaxRetries != nil && !flags.Changed("max-retries") {
		globals.MaxRetries = *cfg.Defaults.MaxRetries
	}
}

// installGroupUnknownHandlers gives every command group the same structured
// unknown-subcommand behavior libcli installs on the root: a fixable_by:agent
// error listing the valid subcommands, rather than cobra's usage text.
func installGroupUnknownHandlers(root *cobra.Command) {
	for _, group := range root.Commands() {
		if !group.HasSubCommands() || group.Run != nil || group.RunE != nil {
			continue
		}
		hint := "run '" + root.Name() + " " + group.Name() + " --help' to see its subcommands"
		libcli.HandleUnknownCommand(group, hint)
	}
}

// Execute builds the root command and runs it via the shared sink, which
// renders any bubbled error as the family's structured JSON on stderr exactly
// once and exits non-zero.
func Execute(version string) {
	libcli.Run(newRootCmd(version))
}

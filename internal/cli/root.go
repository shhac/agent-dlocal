package cli

import (
	"github.com/spf13/cobra"

	"github.com/shhac/agent-dlocal/internal/cli/auth"
	"github.com/shhac/agent-dlocal/internal/cli/shared"
	"github.com/shhac/agent-dlocal/internal/config"
	"github.com/shhac/agent-dlocal/internal/credential"
	"github.com/shhac/agent-dlocal/internal/output"
	libcli "github.com/shhac/lib-agent-cli/cli"
	agentmcp "github.com/shhac/lib-agent-mcp"
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
	pf.StringVar(&globals.Country, "country", "", "ISO 3166-1 alpha-2 country for this invocation, overriding the profile's")
	pf.IntVar(&globals.MaxRetries, "max-retries", 2, "Maximum automatic retries for transient dLocal 429/5xx responses")
	_ = pf.MarkHidden("base-url")

	registerUsageCommand(root, globalsFunc)
	auth.Register(root, globalsFunc)
	registerConfig(root, globalsFunc)
	registerPayments(root, globalsFunc)
	registerOrders(root, globalsFunc)
	registerRefunds(root, globalsFunc)
	registerChargebacks(root, globalsFunc)
	registerPayouts(root, globalsFunc)
	registerPaymentMethods(root, globalsFunc)
	registerInvestigate(root, globalsFunc)
	registerRawAPI(root, globalsFunc)

	installGroupUnknownHandlers(root)

	// Opt the agent-facing groups into the MCP tool surface: each becomes one
	// coarse tool dispatching its subcommands, so the surface is roughly
	// one-tool-per-group rather than one-per-leaf. auth, config and usage are
	// deliberately left out — they are operator tasks, and exposing auth to a
	// tool-calling loop is how credentials get written by something that should
	// not be writing them.
	exposeGroups(root,
		"api", "chargebacks", "investigate", "orders", "payment-methods",
		"payments", "payouts", "refunds")

	// Added LAST so the generated schema reflects the complete tree.
	// --color/--expose shape human output and are irrelevant to a tool call.
	root.AddCommand(agentmcp.Command(root,
		agentmcp.WithHiddenFlags("color", "expose"),
		agentmcp.WithOAuthKeyringService(credential.MCPKeychainService())))

	return root
}

// exposeGroups opts the named top-level commands into the MCP tool surface. A
// name with no matching command is skipped silently — the list is a curation of
// agent-facing groups, not a registration check.
func exposeGroups(root *cobra.Command, names ...string) {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	for _, c := range root.Commands() {
		if want[c.Name()] {
			agentmcp.Expose(c)
		}
	}
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

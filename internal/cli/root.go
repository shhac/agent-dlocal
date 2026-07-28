package cli

import (
	"github.com/spf13/cobra"

	"github.com/shhac/agent-dlocal/internal/cli/shared"
	"github.com/shhac/agent-dlocal/internal/output"
	libcli "github.com/shhac/lib-agent-cli/cli"
)

func newRootCmd(version string) *cobra.Command {
	globals := &shared.GlobalFlags{}

	root := libcli.NewRoot(libcli.Options{
		Use:           "agent-dlocal",
		Short:         "dLocal payments investigation and triage CLI for AI agents",
		Version:       version,
		Globals:       &globals.Globals,
		DefaultFormat: output.FormatNDJSON,
		Redacts:       true,
		UnknownHint:   "run 'agent-dlocal usage' to see the available domains",
	})

	pf := root.PersistentFlags()
	pf.StringVarP(&globals.Profile, "profile", "p", "", "dLocal profile alias (or AGENT_DLOCAL_PROFILE)")
	pf.StringVar(&globals.BaseURL, "base-url", "", "dLocal API base URL override (or AGENT_DLOCAL_BASE_URL)")
	pf.IntVar(&globals.MaxRetries, "max-retries", 2, "Maximum automatic retries for transient dLocal 429/5xx responses")
	_ = pf.MarkHidden("base-url")

	return root
}

// Execute builds the root command and runs it via the shared sink, which
// renders any bubbled error as the family's structured JSON on stderr exactly
// once and exits non-zero.
func Execute(version string) {
	libcli.Run(newRootCmd(version))
}

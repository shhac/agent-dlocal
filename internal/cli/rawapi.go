package cli

import (
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/agent-dlocal/internal/cli/shared"
	agenterrors "github.com/shhac/agent-dlocal/internal/errors"
)

// registerRawAPI is the escape hatch for endpoints not yet wrapped.
//
// It is GET-only BY CONSTRUCTION: the only subcommand is `get`, and there is no
// --method flag to point at POST. dLocal refunds and payouts move real money in
// markets where reversal is slow or impossible, so the read-only guarantee is
// enforced by the absence of a code path rather than by a check someone could
// later relax.
func registerRawAPI(root *cobra.Command, globals shared.GlobalsFunc) {
	group := &cobra.Command{
		Use:   "api",
		Short: "Call a dLocal GET endpoint that has no wrapper yet",
	}

	var query []string
	var payouts bool

	get := &cobra.Command{
		Use:   "get <path>",
		Short: "Issue a signed GET against an arbitrary dLocal path",
		Long: "Issue a signed GET against an arbitrary dLocal path, e.g. 'api get /payments/D-4-abc'.\n\n" +
			"This group is GET-only: there is no way to issue a write through agent-dlocal.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := globals()

			path := args[0]
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}

			params, err := parseQuery(query)
			if err != nil {
				return err
			}

			host := shared.HostPayins
			if payouts {
				host = shared.HostPayouts
			}
			return shared.GetRawItem(flags, host, path, params)
		},
	}
	get.Flags().StringArrayVar(&query, "query", nil, "Query parameter as key=value (repeatable)")
	get.Flags().BoolVar(&payouts, "payouts", false, "Target the payouts host and signing scheme instead of payins")
	group.AddCommand(get)

	root.AddCommand(group)
}

func parseQuery(pairs []string) (url.Values, error) {
	values := url.Values{}
	for _, pair := range pairs {
		key, value, found := strings.Cut(pair, "=")
		if !found || key == "" {
			return nil, agenterrors.Newf(agenterrors.FixableByAgent, "--query %q is not in key=value form", pair).
				WithHint("Pass each parameter as --query country=BR")
		}
		values.Add(key, value)
	}
	return values, nil
}

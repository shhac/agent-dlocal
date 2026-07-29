package cli

import (
	"context"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/shhac/agent-dlocal/internal/api"
	"github.com/shhac/agent-dlocal/internal/cli/shared"
)

// Every command here is a GET. dLocal's read surface is retrieve-by-id, so the
// verbs are `get` and `status` — there is no list endpoint to wrap, and
// inventing a client-side one would misrepresent the API.

// getSpec describes one retrieve-by-id subcommand. The five resource groups
// differ only in these fields, so they are data rather than five copies of the
// same eighteen lines — including the host, which is the whole of the
// payins/payouts difference.
type getSpec struct {
	use        string
	short      string
	long       string
	pathPrefix string
	pathSuffix string
	host       shared.Host
}

// newResourceGroup builds a command group and wires its usage subcommand, so
// the group/usage/AddCommand tail is written once rather than per resource.
func newResourceGroup(root *cobra.Command, globals shared.GlobalsFunc, short, long string, usage domainUsage) *cobra.Command {
	group := &cobra.Command{Use: usage.Domain, Short: short, Long: long}
	registerDomainUsage(group, globals, usage)
	root.AddCommand(group)
	return group
}

func addGetCommand(group *cobra.Command, globals shared.GlobalsFunc, spec getSpec) {
	group.AddCommand(&cobra.Command{
		Use:   spec.use,
		Short: spec.short,
		Long:  spec.long,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := globals()
			return shared.GetEntities(flags, spec.host, args, func(ctx context.Context, session *shared.Session, id string) (any, error) {
				return shared.FetchItem(ctx, session.Client, flags, spec.pathPrefix+url.PathEscape(id)+spec.pathSuffix, nil)
			})
		},
	})
}

func registerPayments(root *cobra.Command, globals shared.GlobalsFunc) {
	group := newResourceGroup(root, globals, "Retrieve dLocal payments", "", paymentsUsage)

	addGetCommand(group, globals, getSpec{
		use:        "get <payment_id>...",
		short:      "Retrieve full payment records by id",
		pathPrefix: "/payments/",
	})
	addGetCommand(group, globals, getSpec{
		use:   "status <payment_id>...",
		short: "Retrieve just the status triple for payments (cheaper than get)",
		long: "Retrieve just the status/status_code/status_detail triple.\n\n" +
			"dLocal serves this only within 12 months of the payment's creation date; " +
			"an older payment returns 404 here even though 'payments get' may still resolve it.",
		pathPrefix: "/payments/",
		pathSuffix: "/status",
	})
}

func registerOrders(root *cobra.Command, globals shared.GlobalsFunc) {
	group := newResourceGroup(root, globals, "Resolve merchant order ids to dLocal payments", "", ordersUsage)

	addGetCommand(group, globals, getSpec{
		use:   "get <order_id>...",
		short: "Retrieve orders by the merchant's own order id",
		long: "Retrieve an order by the id YOUR system assigned, not dLocal's.\n\n" +
			"This is the bridge from a merchant-side order reference to a dLocal payment_id, " +
			"which is what the rest of the commands take.",
		pathPrefix: "/orders/",
	})
}

func registerRefunds(root *cobra.Command, globals shared.GlobalsFunc) {
	group := newResourceGroup(root, globals, "Retrieve dLocal refunds", "", refundsUsage)

	addGetCommand(group, globals, getSpec{
		use:        "get <refund_id>...",
		short:      "Retrieve refund records by id",
		pathPrefix: "/refunds/",
	})
}

func registerChargebacks(root *cobra.Command, globals shared.GlobalsFunc) {
	group := newResourceGroup(root, globals, "Retrieve dLocal chargebacks", "", chargebacksUsage)

	addGetCommand(group, globals, getSpec{
		use:        "get <chargeback_id>...",
		short:      "Retrieve chargeback records by id",
		pathPrefix: "/chargebacks/",
	})
}

// registerPayouts targets a different HOST; the signing scheme is the same as
// payins, confirmed against the live sandbox. That difference is one field on
// the spec, which is why payouts can share the same registrar as everything else.
func registerPayouts(root *cobra.Command, globals shared.GlobalsFunc) {
	group := newResourceGroup(root, globals, "Retrieve dLocal payouts",
		"Retrieve dLocal payouts.\n\n"+
			"Payouts live on the marketplace-api host, using the same credentials and the same "+
			"signing scheme as payins — only the host differs. Their error bodies do differ: "+
			"codes are strings rather than numbers.", payoutsUsage)

	addGetCommand(group, globals, getSpec{
		use:        "get <payout_id>...",
		short:      "Retrieve payout records by id",
		pathPrefix: "/v2/payouts/",
		host:       shared.HostPayouts,
	})
}

// registerPaymentMethods is the one "list"-shaped group in the CLI, and neither
// subcommand is a paginated collection — dLocal has no list endpoints.
//
// Countries are POSITIONAL and repeatable, mirroring `get <id>...`: one record
// per country in input order. A merchant operating across several markets asks
// about them together far more often than one at a time, and making that the
// default shape means the output is the same whether you pass one country or ten.
func registerPaymentMethods(root *cobra.Command, globals shared.GlobalsFunc) {
	group := newResourceGroup(root, globals, "Look up enabled payment methods by country", "", paymentMethodsUsage)

	group.AddCommand(&cobra.Command{
		Use:   "list [COUNTRY...]",
		Short: "List the payment methods enabled for one or more countries",
		Long: "List the payment methods enabled for each country given.\n\n" +
			"With no argument, uses --country, then the profile's country.\n" +
			"Emits one record per country, in the order given.",
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := globals()
			return shared.WithSession(flags, shared.HostPayins, func(ctx context.Context, session *shared.Session) error {
				countries := args
				if len(countries) == 0 {
					countries = []string{flags.ResolveCountry("", session.Profile.Country)}
				}
				return shared.ListPaymentMethods(ctx, session.Client, flags, countries)
			})
		},
	})

	registerCountryDiscovery(group, globals)
}

// registerCountryDiscovery answers "which markets can this merchant take money
// in?" — a question dLocal exposes no endpoint for. There is no list-countries
// call, so the only way to find out is to ask per country and see which resolve.
//
// That makes this the one command that issues many requests for one answer. It
// is still read-only, and the alternative is the operator running the same
// probe by hand, so the fan-out belongs in the tool rather than in a shell loop
// somebody re-invents each time.
func registerCountryDiscovery(group *cobra.Command, globals shared.GlobalsFunc) {
	var concurrency int
	var supportedOnly bool

	cmd := &cobra.Command{
		Use:   "countries [COUNTRY...]",
		Short: "Discover which countries this merchant is enabled for",
		Long: "Probe each of dLocal's markets and report which resolve for these credentials.\n\n" +
			"dLocal has no list-countries endpoint, so this issues one GET per country " +
			"(" + strconv.Itoa(len(api.Markets)) + " by default) and reports the outcome of each. " +
			"Pass country codes to probe a specific set instead, including codes not on the built-in list.",
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := globals()
			targets := args
			if len(targets) == 0 {
				targets = api.Markets
			}

			return shared.WithSession(flags, shared.HostPayins, func(ctx context.Context, session *shared.Session) error {
				results := shared.ProbeCountries(ctx, session.Client, targets, concurrency)
				shared.SortProbes(results)
				shared.WriteList(selectProbes(results, supportedOnly), flags.Format)
				return nil
			})
		},
	}
	cmd.Flags().IntVar(&concurrency, "concurrency", 8, "Parallel probes in flight")
	cmd.Flags().BoolVar(&supportedOnly, "supported", false, "Emit only the countries that resolved")
	group.AddCommand(cmd)
}

// selectProbes applies --supported. Pure, so the filtering is testable without
// standing up a client.
func selectProbes(results []shared.CountryProbe, supportedOnly bool) []any {
	items := make([]any, 0, len(results))
	for _, result := range results {
		if supportedOnly && !result.Supported {
			continue
		}
		items = append(items, result)
	}
	return items
}

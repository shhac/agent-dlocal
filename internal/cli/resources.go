package cli

import (
	"context"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/shhac/agent-dlocal/internal/api"
	"github.com/shhac/agent-dlocal/internal/cli/shared"
	"github.com/shhac/agent-dlocal/internal/config"
)

// Every command here is a GET. dLocal's read surface is retrieve-by-id, so the
// verbs are `get` and `status` — there is no list endpoint to wrap, and
// inventing a client-side one would misrepresent the API.

func registerPayments(root *cobra.Command, globals shared.GlobalsFunc) {
	group := &cobra.Command{
		Use:   "payments",
		Short: "Retrieve dLocal payments",
	}

	group.AddCommand(&cobra.Command{
		Use:   "get <payment_id>...",
		Short: "Retrieve full payment records by id",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := globals()
			return shared.GetEntities(flags, args, func(ctx context.Context, client *api.Client, id string) (any, error) {
				return shared.FetchItem(ctx, client, flags, "/payments/"+url.PathEscape(id), nil)
			})
		},
	})

	group.AddCommand(&cobra.Command{
		Use:   "status <payment_id>...",
		Short: "Retrieve just the status triple for payments (cheaper than get)",
		Long: "Retrieve just the status/status_code/status_detail triple.\n\n" +
			"dLocal serves this only within 12 months of the payment's creation date; " +
			"an older payment returns 404 here even though 'payments get' may still resolve it.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := globals()
			return shared.GetEntities(flags, args, func(ctx context.Context, client *api.Client, id string) (any, error) {
				return shared.FetchItem(ctx, client, flags, "/payments/"+url.PathEscape(id)+"/status", nil)
			})
		},
	})

	registerDomainUsage(group, globals, paymentsUsage)
	root.AddCommand(group)
}

func registerOrders(root *cobra.Command, globals shared.GlobalsFunc) {
	group := &cobra.Command{
		Use:   "orders",
		Short: "Resolve merchant order ids to dLocal payments",
	}

	group.AddCommand(&cobra.Command{
		Use:   "get <order_id>...",
		Short: "Retrieve orders by the merchant's own order id",
		Long: "Retrieve an order by the id YOUR system assigned, not dLocal's.\n\n" +
			"This is the bridge from a merchant-side order reference to a dLocal payment_id, " +
			"which is what the rest of the commands take.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := globals()
			return shared.GetEntities(flags, args, func(ctx context.Context, client *api.Client, id string) (any, error) {
				return shared.FetchItem(ctx, client, flags, "/orders/"+url.PathEscape(id), nil)
			})
		},
	})

	registerDomainUsage(group, globals, ordersUsage)
	root.AddCommand(group)
}

func registerRefunds(root *cobra.Command, globals shared.GlobalsFunc) {
	group := &cobra.Command{
		Use:   "refunds",
		Short: "Retrieve dLocal refunds",
	}

	group.AddCommand(&cobra.Command{
		Use:   "get <refund_id>...",
		Short: "Retrieve refund records by id",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := globals()
			return shared.GetEntities(flags, args, func(ctx context.Context, client *api.Client, id string) (any, error) {
				return shared.FetchItem(ctx, client, flags, "/refunds/"+url.PathEscape(id), nil)
			})
		},
	})

	registerDomainUsage(group, globals, refundsUsage)
	root.AddCommand(group)
}

func registerChargebacks(root *cobra.Command, globals shared.GlobalsFunc) {
	group := &cobra.Command{
		Use:   "chargebacks",
		Short: "Retrieve dLocal chargebacks",
	}

	group.AddCommand(&cobra.Command{
		Use:   "get <chargeback_id>...",
		Short: "Retrieve chargeback records by id",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := globals()
			return shared.GetEntities(flags, args, func(ctx context.Context, client *api.Client, id string) (any, error) {
				return shared.FetchItem(ctx, client, flags, "/chargebacks/"+url.PathEscape(id), nil)
			})
		},
	})

	registerDomainUsage(group, globals, chargebacksUsage)
	root.AddCommand(group)
}

// registerPayouts targets a different HOST; the signing scheme is the same as
// payins, confirmed against the live sandbox.
func registerPayouts(root *cobra.Command, globals shared.GlobalsFunc) {
	group := &cobra.Command{
		Use:   "payouts",
		Short: "Retrieve dLocal payouts",
		Long: "Retrieve dLocal payouts.\n\n" +
			"Payouts live on the marketplace-api host, using the same credentials and the same " +
			"signing scheme as payins — only the host differs. Their error bodies do differ: " +
			"codes are strings rather than numbers.",
	}

	group.AddCommand(&cobra.Command{
		Use:   "get <payout_id>...",
		Short: "Retrieve payout records by id",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := globals()
			return shared.GetPayoutEntities(flags, args, func(ctx context.Context, client *api.Client, id string) (any, error) {
				return shared.FetchItem(ctx, client, flags, "/v2/payouts/"+url.PathEscape(id), nil)
			})
		},
	})

	registerDomainUsage(group, globals, payoutsUsage)
	root.AddCommand(group)
}

// registerPaymentMethods is the one "list"-shaped group in the CLI, and neither
// subcommand is a paginated collection — dLocal has no list endpoints.
//
// Countries are POSITIONAL and repeatable, mirroring `get <id>...`: one record
// per country in input order. A merchant operating across several markets asks
// about them together far more often than one at a time, and making that the
// default shape means the output is the same whether you pass one country or
// ten.
func registerPaymentMethods(root *cobra.Command, globals shared.GlobalsFunc) {
	group := &cobra.Command{
		Use:   "payment-methods",
		Short: "Look up enabled payment methods by country",
	}

	list := &cobra.Command{
		Use:   "list [COUNTRY...]",
		Short: "List the payment methods enabled for one or more countries",
		Long: "List the payment methods enabled for each country given.\n\n" +
			"With no argument, uses --country, then the profile's country.\n" +
			"Emits one record per country, in the order given.",
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := globals()
			return shared.WithResolvedProfile(flags, func(ctx context.Context, client *api.Client, profile config.Profile) error {
				countries := args
				if len(countries) == 0 {
					countries = []string{flags.ResolveCountry("", profile.Country)}
				}
				return shared.ListPaymentMethods(ctx, client, flags, countries)
			})
		},
	}
	group.AddCommand(list)

	registerCountryDiscovery(group, globals)
	registerDomainUsage(group, globals, paymentMethodsUsage)
	root.AddCommand(group)
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
			"(" + itoa(len(api.Markets)) + " by default) and reports the outcome of each. " +
			"Pass country codes to probe a specific set instead, including codes not on the built-in list.",
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := globals()
			targets := args
			if len(targets) == 0 {
				targets = api.Markets
			}

			return shared.WithClient(flags, func(ctx context.Context, client *api.Client) error {
				results := shared.ProbeCountries(ctx, client, targets, concurrency)
				shared.SortProbes(results)
				items := make([]any, 0, len(results))
				for _, r := range results {
					if supportedOnly && !r.Supported {
						continue
					}
					items = append(items, r)
				}
				shared.WriteList(items, flags.Format)
				return nil
			})
		},
	}
	cmd.Flags().IntVar(&concurrency, "concurrency", 8, "Parallel probes in flight")
	cmd.Flags().BoolVar(&supportedOnly, "supported", false, "Emit only the countries that resolved")
	group.AddCommand(cmd)
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

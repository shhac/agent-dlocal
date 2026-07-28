package cli

import (
	"context"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/shhac/agent-dlocal/internal/api"
	"github.com/shhac/agent-dlocal/internal/cli/shared"
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

	registerDomainUsage(group, "payments", paymentsUsage)
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

	registerDomainUsage(group, "orders", ordersUsage)
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

	registerDomainUsage(group, "refunds", refundsUsage)
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

	registerDomainUsage(group, "chargebacks", chargebacksUsage)
	root.AddCommand(group)
}

// registerPayouts targets a different host with a different signing scheme, so
// it routes through the payouts client rather than the payins one.
func registerPayouts(root *cobra.Command, globals shared.GlobalsFunc) {
	group := &cobra.Command{
		Use:   "payouts",
		Short: "Retrieve dLocal payouts",
		Long: "Retrieve dLocal payouts.\n\n" +
			"Payouts live on the marketplace-api host and are signed with the Payload-Signature " +
			"scheme rather than the payins Authorization header. Both come from the same profile.",
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

	registerDomainUsage(group, "payouts", payoutsUsage)
	root.AddCommand(group)
}

// registerPaymentMethods is the one "list"-shaped command in the CLI, and it is
// a per-country lookup rather than a paginated collection — hence a required
// --country instead of the family's usual --limit/cursor flags.
func registerPaymentMethods(root *cobra.Command, globals shared.GlobalsFunc) {
	group := &cobra.Command{
		Use:   "payment-methods",
		Short: "Look up enabled payment methods by country",
	}

	var country string
	list := &cobra.Command{
		Use:   "list",
		Short: "List the payment methods enabled for a country",
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := globals()
			if err := shared.RequireFlag("country", country, "Pass an ISO 3166-1 alpha-2 code, e.g. --country BR"); err != nil {
				return err
			}
			return shared.WithClient(flags, func(ctx context.Context, client *api.Client) error {
				item, err := shared.FetchItem(ctx, client, flags, "/payments-methods", url.Values{"country": {country}})
				if err != nil {
					return err
				}
				methods, ok := item.([]any)
				if !ok {
					shared.WriteItem(item, flags.Format)
					return nil
				}
				shared.WriteList(methods, flags.Format)
				return nil
			})
		},
	}
	list.Flags().StringVar(&country, "country", "", "ISO 3166-1 alpha-2 country code (required)")
	group.AddCommand(list)

	registerDomainUsage(group, "payment-methods", paymentMethodsUsage)
	root.AddCommand(group)
}

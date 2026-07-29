package cli

import (
	"context"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/shhac/agent-dlocal/internal/cli/shared"
	agenterrors "github.com/shhac/agent-dlocal/internal/errors"
)

// investigate chains several reads into one answer. The value is not the
// fetching — an agent could do that itself — but the synthesis: a verdict that
// says whether the thing is finished, whether it failed, and what to do about
// it, with the evidence attached so the conclusion can be checked.
//
// This file is the fetch orchestration only; the reasoning lives in
// investigate_explain.go so it can be tested without a client.

func registerInvestigate(root *cobra.Command, globals shared.GlobalsFunc) {
	group := &cobra.Command{
		Use:   "investigate",
		Short: "Answer incident questions by chaining several reads",
	}

	group.AddCommand(
		investigateCmd("payment", "<payment_id>", "Explain why a payment is in the state it is in", investigatePayment, globals),
		investigateCmd("order", "<order_id>", "Resolve a merchant order id and explain its payment", investigateOrder, globals),
		investigateCmd("refund", "<refund_id>", "Explain a refund against its parent payment", investigateRefund, globals),
		investigateCmd("payout", "<payout_id>", "Explain where a payout is", investigatePayout, globals),
	)

	registerDomainUsage(group, globals, investigateUsage)
	root.AddCommand(group)
}

func investigateCmd(
	name, arg, short string,
	run func(flags *shared.GlobalFlags, id string) (*finding, error),
	globals shared.GlobalsFunc,
) *cobra.Command {
	return &cobra.Command{
		Use:   name + " " + arg,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := globals()
			result, err := run(flags, args[0])
			if err != nil {
				return err
			}
			shared.WriteItem(result, flags.Format)
			return nil
		},
	}
}

func investigatePayment(flags *shared.GlobalFlags, id string) (*finding, error) {
	return shared.WithSessionResult(flags, shared.HostPayins, func(ctx context.Context, session *shared.Session) (*finding, error) {
		payment, err := fetchMap(ctx, session, flags, "/payments/"+url.PathEscape(id))
		if err != nil {
			return nil, err
		}
		return explainPayment(id, payment), nil
	})
}

func investigatePayout(flags *shared.GlobalFlags, id string) (*finding, error) {
	return shared.WithSessionResult(flags, shared.HostPayouts, func(ctx context.Context, session *shared.Session) (*finding, error) {
		payout, err := fetchMap(ctx, session, flags, "/v2/payouts/"+url.PathEscape(id))
		if err != nil {
			return nil, err
		}
		return explainPayout(id, payout), nil
	})
}

func investigateOrder(flags *shared.GlobalFlags, orderID string) (*finding, error) {
	return shared.WithSessionResult(flags, shared.HostPayins, func(ctx context.Context, session *shared.Session) (*finding, error) {
		order, err := fetchMap(ctx, session, flags, "/orders/"+url.PathEscape(orderID))
		if err != nil {
			return nil, err
		}

		paymentID, _ := order["payment_id"].(string)
		if paymentID == "" {
			return explainOrderWithoutPayment(orderID, order), nil
		}

		payment, err := fetchMap(ctx, session, flags, "/payments/"+url.PathEscape(paymentID))
		if err != nil {
			return nil, err
		}
		return asOrderFinding(explainPayment(paymentID, payment), orderID, order), nil
	})
}

func investigateRefund(flags *shared.GlobalFlags, id string) (*finding, error) {
	return shared.WithSessionResult(flags, shared.HostPayins, func(ctx context.Context, session *shared.Session) (*finding, error) {
		refund, err := fetchMap(ctx, session, flags, "/refunds/"+url.PathEscape(id))
		if err != nil {
			return nil, err
		}
		out := explainRefund(id, refund)

		paymentID, _ := refund["payment_id"].(string)
		if paymentID == "" {
			return out, nil
		}

		payment, lookupErr := fetchMap(ctx, session, flags, "/payments/"+url.PathEscape(paymentID))
		return attachParentPayment(out, refund, payment, lookupErr), nil
	})
}

func fetchMap(ctx context.Context, session *shared.Session, flags *shared.GlobalFlags, path string) (map[string]any, error) {
	item, err := shared.FetchItem(ctx, session.Client, flags, path, nil)
	if err != nil {
		return nil, err
	}
	record, ok := item.(map[string]any)
	if !ok {
		return nil, agenterrors.Newf(agenterrors.FixableByRetry, "unexpected response shape from %s", path)
	}
	return record, nil
}

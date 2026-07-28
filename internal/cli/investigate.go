package cli

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/shhac/agent-dlocal/internal/api"
	"github.com/shhac/agent-dlocal/internal/cli/shared"
	agenterrors "github.com/shhac/agent-dlocal/internal/errors"
)

// investigate chains several reads into one answer. The value is not the
// fetching — an agent could do that itself — but the synthesis: a verdict that
// says whether the thing is finished, whether it failed, and what to do about
// it, with the evidence attached so the conclusion can be checked.

type finding struct {
	Scenario  string         `json:"scenario"`
	Subject   string         `json:"subject"`
	Verdict   string         `json:"verdict"`
	Terminal  *bool          `json:"terminal,omitempty"`
	NextSteps []string       `json:"next_steps,omitempty"`
	Evidence  map[string]any `json:"evidence"`
}

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

	registerDomainUsage(group, "investigate", investigateUsage)
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
	var result *finding

	err := shared.WithClient(flags, func(ctx context.Context, client *api.Client) error {
		payment, err := fetchMap(ctx, client, flags, "/payments/"+url.PathEscape(id))
		if err != nil {
			return err
		}
		result = explainPayment(id, payment)
		return nil
	})
	return result, err
}

func explainPayment(id string, payment map[string]any) *finding {
	status, _ := payment["status"].(string)
	detail, _ := payment["status_detail"].(string)

	out := &finding{
		Scenario: "payment",
		Subject:  id,
		Evidence: map[string]any{"payment": payment},
	}

	meaning, known := api.ExplainPayinStatus(status)
	if !known {
		out.Verdict = "Payment is in an unrecognized state " + quoted(status) + ": " + detail
		out.NextSteps = []string{"Read status_detail directly; agent-dlocal's status table does not cover this value"}
		return out
	}

	terminal := meaning.Terminal
	out.Terminal = &terminal
	out.Verdict = meaning.Meaning + ". dLocal says: " + detail

	if meaning.Action != "" {
		out.NextSteps = append(out.NextSteps, meaning.Action)
	}

	// A REDIRECT payment sitting at PENDING almost always means the customer
	// never finished on dLocal's side — a distinction worth naming, since it
	// looks identical to a processing delay from the merchant's records.
	if flow, _ := payment["payment_method_flow"].(string); flow == "REDIRECT" && status == "PENDING" {
		out.NextSteps = append(out.NextSteps,
			"The flow is REDIRECT and the payment is still PENDING: the customer most likely never completed the hosted step. Check whether they reached redirect_url.")
	}

	if card, ok := payment["card"].(map[string]any); ok && status == "REJECTED" {
		out.NextSteps = append(out.NextSteps,
			"Card payment rejected — brand "+stringOr(card["brand"], "unknown")+", BIN "+stringOr(card["bin"], "unknown")+
				". An issuer decline is retryable with a different instrument; a validation rejection is not.")
	}

	if orderID, ok := payment["order_id"].(string); ok && orderID != "" {
		out.Evidence["order_id"] = orderID
	}
	return out
}

func investigateOrder(flags *shared.GlobalFlags, orderID string) (*finding, error) {
	var result *finding

	err := shared.WithClient(flags, func(ctx context.Context, client *api.Client) error {
		order, err := fetchMap(ctx, client, flags, "/orders/"+url.PathEscape(orderID))
		if err != nil {
			return err
		}

		paymentID, _ := order["payment_id"].(string)
		if paymentID == "" {
			result = &finding{
				Scenario: "order",
				Subject:  orderID,
				Verdict:  "The order exists but carries no dLocal payment id, so no payment was ever created for it",
				Evidence: map[string]any{"order": order},
				NextSteps: []string{
					"Check the merchant-side flow that submits the payment — dLocal never received one for this order",
				},
			}
			return nil
		}

		payment, err := fetchMap(ctx, client, flags, "/payments/"+url.PathEscape(paymentID))
		if err != nil {
			return err
		}

		result = explainPayment(paymentID, payment)
		result.Scenario = "order"
		result.Subject = orderID
		result.Evidence["order"] = order
		return nil
	})
	return result, err
}

func investigateRefund(flags *shared.GlobalFlags, id string) (*finding, error) {
	var result *finding

	err := shared.WithClient(flags, func(ctx context.Context, client *api.Client) error {
		refund, err := fetchMap(ctx, client, flags, "/refunds/"+url.PathEscape(id))
		if err != nil {
			return err
		}

		out := &finding{
			Scenario: "refund",
			Subject:  id,
			Evidence: map[string]any{"refund": refund},
		}

		status, _ := refund["status"].(string)
		detail, _ := refund["status_detail"].(string)
		out.Verdict = "Refund is " + status + ". dLocal says: " + detail

		switch status {
		case "PENDING":
			out.NextSteps = append(out.NextSteps,
				"PENDING is normal for cash and bank-transfer methods, which settle asynchronously — this is not stuck unless it has been days.")
		case "REJECTED":
			out.NextSteps = append(out.NextSteps,
				"Read status_detail: a rejected refund usually means the original payment is not in a refundable state, or the beneficiary details failed validation.")
		}

		// Comparing the refund against its parent is the part an agent cannot
		// do from the refund record alone, and it is what distinguishes "fully
		// refunded" from "partially refunded" — often the actual question.
		if paymentID, ok := refund["payment_id"].(string); ok && paymentID != "" {
			payment, err := fetchMap(ctx, client, flags, "/payments/"+url.PathEscape(paymentID))
			if err != nil {
				// The parent is context, not the answer. A failure to fetch it
				// degrades the verdict rather than losing the refund record.
				out.Evidence["payment_lookup_error"] = err.Error()
				result = out
				return nil
			}
			out.Evidence["payment"] = payment
			out.NextSteps = append(out.NextSteps, compareAmounts(refund["amount"], payment["amount"]))
		}

		result = out
		return nil
	})
	return result, err
}

func compareAmounts(refundAmount, paymentAmount any) string {
	refunded, okR := toFloat(refundAmount)
	paid, okP := toFloat(paymentAmount)
	if !okR || !okP || paid == 0 {
		return "Compare the refund amount against the payment amount to tell a partial refund from a full one."
	}
	if refunded >= paid {
		return "This is a FULL refund: the refund amount matches the payment amount."
	}
	return "This is a PARTIAL refund: only part of the payment was returned, so the remainder is still with the merchant."
}

func investigatePayout(flags *shared.GlobalFlags, id string) (*finding, error) {
	var result *finding

	err := shared.WithPayoutsClient(flags, func(ctx context.Context, client *api.Client) error {
		payout, err := fetchMap(ctx, client, flags, "/v2/payouts/"+url.PathEscape(id))
		if err != nil {
			return err
		}

		status, _ := payout["status"].(string)
		detail, _ := payout["status_detail"].(string)

		out := &finding{
			Scenario: "payout",
			Subject:  id,
			Evidence: map[string]any{"payout": payout},
		}

		meaning, known := api.ExplainPayoutStatus(status)
		if !known {
			out.Verdict = "Payout is in an unrecognized state " + quoted(status) + ": " + detail
			result = out
			return nil
		}

		terminal := meaning.Terminal
		out.Terminal = &terminal
		out.Verdict = meaning.Meaning + ". dLocal says: " + detail
		if meaning.Action != "" {
			out.NextSteps = append(out.NextSteps, meaning.Action)
		}

		result = out
		return nil
	})
	return result, err
}

func fetchMap(ctx context.Context, client *api.Client, flags *shared.GlobalFlags, path string) (map[string]any, error) {
	item, err := shared.FetchItem(ctx, client, flags, path, nil)
	if err != nil {
		return nil, err
	}
	record, ok := item.(map[string]any)
	if !ok {
		return nil, agenterrors.Newf(agenterrors.FixableByRetry, "unexpected response shape from %s", path)
	}
	return record, nil
}

func toFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func stringOr(value any, fallback string) string {
	if s, ok := value.(string); ok && s != "" {
		return s
	}
	return fallback
}

func quoted(s string) string {
	if s == "" {
		return `""`
	}
	return `"` + s + `"`
}

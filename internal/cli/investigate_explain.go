package cli

import (
	"encoding/json"

	"github.com/shhac/agent-dlocal/internal/api"
)

// The pure half of `investigate`: turning fetched records into a verdict.
//
// This is the product's actual value — the fetching is something an agent could
// do itself. Keeping it free of I/O means the reasoning is testable with
// literal maps instead of a live client, which is why it lives apart from the
// command wiring.

type finding struct {
	Scenario  string         `json:"scenario"`
	Subject   string         `json:"subject"`
	Verdict   string         `json:"verdict"`
	Terminal  *bool          `json:"terminal,omitempty"`
	NextSteps []string       `json:"next_steps,omitempty"`
	Evidence  map[string]any `json:"evidence"`
}

// statusTriple pulls dLocal's status/status_detail pair off any record. Both
// are best-effort: a record missing them yields empty strings and the caller's
// unknown-status path.
func statusTriple(record map[string]any) (status, detail string) {
	status, _ = record["status"].(string)
	detail, _ = record["status_detail"].(string)
	return status, detail
}

// explainStatus applies a status table to a record, producing the shared
// verdict/terminal/next-steps shape. Payins and payouts pass different lookups:
// the tables are deliberately separate because code 500 means DELIVERED for a
// payout and nothing at all for a payin.
func explainStatus(out *finding, noun string, record map[string]any, lookup func(string) (api.StatusMeaning, bool)) {
	status, detail := statusTriple(record)

	meaning, known := lookup(status)
	if !known {
		out.Verdict = noun + " is in an unrecognized state " + quoted(status) + ": " + detail
		out.NextSteps = append(out.NextSteps,
			"Read status_detail directly; agent-dlocal's status table does not cover this value")
		return
	}

	terminal := meaning.Terminal
	out.Terminal = &terminal
	out.Verdict = meaning.Meaning + ". dLocal says: " + detail
	if meaning.Action != "" {
		out.NextSteps = append(out.NextSteps, meaning.Action)
	}
}

func explainPayment(id string, payment map[string]any) *finding {
	out := &finding{
		Scenario: "payment",
		Subject:  id,
		Evidence: map[string]any{"payment": payment},
	}
	explainStatus(out, "Payment", payment, api.ExplainPayinStatus)

	status, _ := statusTriple(payment)

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

func explainPayout(id string, payout map[string]any) *finding {
	out := &finding{
		Scenario: "payout",
		Subject:  id,
		Evidence: map[string]any{"payout": payout},
	}
	explainStatus(out, "Payout", payout, api.ExplainPayoutStatus)
	return out
}

// explainOrderWithoutPayment covers an order dLocal knows about but that never
// produced a payment — a different problem from a rejected payment, and worth
// saying so plainly: the fault is merchant-side.
func explainOrderWithoutPayment(orderID string, order map[string]any) *finding {
	return &finding{
		Scenario: "order",
		Subject:  orderID,
		Verdict:  "The order exists but carries no dLocal payment id, so no payment was ever created for it",
		Evidence: map[string]any{"order": order},
		NextSteps: []string{
			"Check the merchant-side flow that submits the payment — dLocal never received one for this order",
		},
	}
}

// asOrderFinding relabels a payment verdict as the order the caller asked about,
// keeping the payment analysis intact underneath.
func asOrderFinding(out *finding, orderID string, order map[string]any) *finding {
	out.Scenario = "order"
	out.Subject = orderID
	out.Evidence["order"] = order
	return out
}

// explainRefund reads the refund alone. The parent payment is attached
// separately, because fetching it can fail and that must degrade the verdict
// rather than lose the refund record.
func explainRefund(id string, refund map[string]any) *finding {
	out := &finding{
		Scenario: "refund",
		Subject:  id,
		Evidence: map[string]any{"refund": refund},
	}

	status, detail := statusTriple(refund)
	out.Verdict = "Refund is " + status + ". dLocal says: " + detail

	switch status {
	case "PENDING":
		out.NextSteps = append(out.NextSteps,
			"PENDING is normal for cash and bank-transfer methods, which settle asynchronously — this is not stuck unless it has been days.")
	case "REJECTED":
		out.NextSteps = append(out.NextSteps,
			"Read status_detail: a rejected refund usually means the original payment is not in a refundable state, or the beneficiary details failed validation.")
	}
	return out
}

// attachParentPayment adds the comparison an agent cannot make from the refund
// record alone: partial versus full. A lookup failure is recorded as evidence,
// not raised — the refund verdict is still worth having without it.
func attachParentPayment(out *finding, refund, payment map[string]any, lookupErr error) *finding {
	if lookupErr != nil {
		out.Evidence["payment_lookup_error"] = lookupErr.Error()
		return out
	}
	if payment == nil {
		return out
	}
	out.Evidence["payment"] = payment
	out.NextSteps = append(out.NextSteps, compareAmounts(refund["amount"], payment["amount"]))
	return out
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

package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// These functions are the tool's money reasoning, and until they were split out
// of the fetch closures they could only be reached through a subprocess e2e run
// against the mock — which contributes no measured coverage and cannot express
// a table of edge cases.

func TestExplainPaymentVerdicts(t *testing.T) {
	tests := []struct {
		name         string
		payment      map[string]any
		wantTerminal *bool
		wantVerdict  string
		wantStep     string
	}{
		{
			name:         "paid is terminal",
			payment:      map[string]any{"status": "PAID", "status_detail": "The payment was paid"},
			wantTerminal: boolPtr(true),
			wantVerdict:  "paid",
		},
		{
			name:         "pending is not terminal",
			payment:      map[string]any{"status": "PENDING", "status_detail": "awaiting"},
			wantTerminal: boolPtr(false),
			wantVerdict:  "Received",
		},
		{
			// The distinction that matters most: a REDIRECT payin stuck at
			// PENDING is a customer who never finished, not a slow processor.
			name: "pending redirect names the abandoned hosted step",
			payment: map[string]any{
				"status": "PENDING", "status_detail": "awaiting", "payment_method_flow": "REDIRECT",
			},
			wantTerminal: boolPtr(false),
			wantStep:     "never completed the hosted step",
		},
		{
			name: "rejected card surfaces brand and BIN",
			payment: map[string]any{
				"status": "REJECTED", "status_detail": "declined",
				"card": map[string]any{"brand": "VI", "bin": "411111"},
			},
			wantTerminal: boolPtr(true),
			wantStep:     "brand VI, BIN 411111",
		},
		{
			name:         "unknown status does not claim terminality",
			payment:      map[string]any{"status": "SOMETHING_NEW", "status_detail": "?"},
			wantTerminal: nil,
			wantVerdict:  "unrecognized state",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := explainPayment("D-4-x", tc.payment)

			if !equalBoolPtr(got.Terminal, tc.wantTerminal) {
				t.Errorf("Terminal = %v, want %v", derefBool(got.Terminal), derefBool(tc.wantTerminal))
			}
			if tc.wantVerdict != "" && !strings.Contains(got.Verdict, tc.wantVerdict) {
				t.Errorf("Verdict = %q, want it to contain %q", got.Verdict, tc.wantVerdict)
			}
			if tc.wantStep != "" && !strings.Contains(strings.Join(got.NextSteps, " "), tc.wantStep) {
				t.Errorf("next_steps = %v, want one containing %q", got.NextSteps, tc.wantStep)
			}
		})
	}
}

// An unknown status must not be reported as terminal. Claiming a state is final
// when it is not is the error that causes a payout to be re-sent.
func TestUnknownStatusLeavesTerminalUnset(t *testing.T) {
	for _, f := range []*finding{
		explainPayment("D-4-x", map[string]any{"status": "WAT"}),
		explainPayout("P-1-x", map[string]any{"status": "WAT"}),
	} {
		if f.Terminal != nil {
			t.Errorf("%s: Terminal = %v for an unrecognized status, want unset", f.Scenario, *f.Terminal)
		}
	}
}

// DELIVERED is the payout status most often misread as final.
func TestExplainPayoutFlagsDeliveredAsInFlight(t *testing.T) {
	got := explainPayout("P-1-x", map[string]any{"status": "DELIVERED", "status_detail": "processing"})

	if got.Terminal == nil || *got.Terminal {
		t.Fatalf("DELIVERED reported as terminal: %v", derefBool(got.Terminal))
	}
	if !strings.Contains(strings.Join(got.NextSteps, " "), "in flight") {
		t.Fatalf("next_steps should say the money is in flight: %v", got.NextSteps)
	}
}

func TestCompareAmounts(t *testing.T) {
	tests := []struct {
		name         string
		refund, paid any
		want         string
	}{
		{"equal amounts are a full refund", 100.0, 100.0, "FULL"},
		{"refund below payment is partial", 40.0, 100.0, "PARTIAL"},
		{"refund above payment still reads as full", 120.0, 100.0, "FULL"},
		{"zero payment cannot be divided", 40.0, 0.0, "Compare the refund amount"},
		{"missing amounts degrade to advice", nil, 100.0, "Compare the refund amount"},
		// dLocal has returned amounts as strings on some endpoints; the verdict
		// degrades rather than guessing.
		{"string amounts degrade to advice", "40.00", "100.00", "Compare the refund amount"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := compareAmounts(tc.refund, tc.paid); !strings.Contains(got, tc.want) {
				t.Errorf("compareAmounts(%v, %v) = %q, want it to contain %q", tc.refund, tc.paid, got, tc.want)
			}
		})
	}
}

func TestToFloatAcceptsJSONNumber(t *testing.T) {
	var decoded map[string]any
	dec := json.NewDecoder(strings.NewReader(`{"amount": 285}`))
	dec.UseNumber()
	if err := dec.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if got, ok := toFloat(decoded["amount"]); !ok || got != 285 {
		t.Fatalf("toFloat(json.Number) = %v, %v; want 285, true", got, ok)
	}
}

// A failed parent lookup must degrade the refund verdict, never discard it.
func TestAttachParentPaymentDegradesOnLookupFailure(t *testing.T) {
	refund := map[string]any{"status": "PENDING", "amount": 40.0}
	out := attachParentPayment(explainRefund("REF-1", refund), refund, nil, errStub{})

	if out == nil || out.Evidence["refund"] == nil {
		t.Fatal("the refund record was lost when the parent lookup failed")
	}
	if out.Evidence["payment_lookup_error"] == nil {
		t.Fatal("the parent lookup failure was not recorded as evidence")
	}
}

func TestAttachParentPaymentAddsTheComparison(t *testing.T) {
	refund := map[string]any{"status": "SUCCESS", "amount": 40.0}
	payment := map[string]any{"amount": 100.0}
	out := attachParentPayment(explainRefund("REF-1", refund), refund, payment, nil)

	if out.Evidence["payment"] == nil {
		t.Fatal("the parent payment was not attached as evidence")
	}
	if !strings.Contains(strings.Join(out.NextSteps, " "), "PARTIAL") {
		t.Fatalf("next_steps should classify the refund: %v", out.NextSteps)
	}
}

func TestExplainOrderWithoutPaymentBlamesTheMerchantSide(t *testing.T) {
	got := explainOrderWithoutPayment("ORDER-1", map[string]any{"order_id": "ORDER-1"})

	if !strings.Contains(got.Verdict, "no payment was ever created") {
		t.Fatalf("verdict = %q", got.Verdict)
	}
	if !strings.Contains(strings.Join(got.NextSteps, " "), "merchant-side") {
		t.Fatalf("next_steps should point at the merchant-side flow: %v", got.NextSteps)
	}
}

func TestAsOrderFindingRelabelsButKeepsThePaymentAnalysis(t *testing.T) {
	payment := map[string]any{"status": "REJECTED", "status_detail": "declined"}
	got := asOrderFinding(explainPayment("D-4-x", payment), "ORDER-1", map[string]any{"order_id": "ORDER-1"})

	if got.Scenario != "order" || got.Subject != "ORDER-1" {
		t.Fatalf("relabelling failed: scenario=%q subject=%q", got.Scenario, got.Subject)
	}
	if got.Evidence["payment"] == nil || got.Evidence["order"] == nil {
		t.Fatal("both the payment and the order should remain as evidence")
	}
	if !strings.Contains(got.Verdict, "rejected") {
		t.Fatalf("the payment verdict was lost: %q", got.Verdict)
	}
}

type errStub struct{}

func (errStub) Error() string { return "lookup failed" }

func boolPtr(b bool) *bool { return &b }

func derefBool(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}

func equalBoolPtr(a, b *bool) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

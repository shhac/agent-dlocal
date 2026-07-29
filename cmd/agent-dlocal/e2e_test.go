package main

import (
	"strings"
	"testing"
)

func TestPaymentsGetReturnsRecord(t *testing.T) {
	out := runMockCLI(t, "payments", "get", "D-4-paid")

	records := decodeLines(t, out)
	if len(records) != 1 {
		t.Fatalf("expected 1 NDJSON record, got %d:\n%s", len(records), out)
	}
	if records[0]["status"] != "PAID" {
		t.Fatalf("status = %v, want PAID", records[0]["status"])
	}
}

// The multi-get contract: one record per id, IN INPUT ORDER, with misses as
// @unresolved lines on stdout and a zero exit. A 404 for one id must not lose
// the records for the others.
func TestPaymentsGetIsMultiCapableAndOrderPreserving(t *testing.T) {
	out := runMockCLI(t, "payments", "get", "D-4-rejected", "D-4-nosuch", "D-4-paid")

	records := decodeLines(t, out)
	if len(records) != 3 {
		t.Fatalf("expected 3 records for 3 ids, got %d:\n%s", len(records), out)
	}
	if records[0]["status"] != "REJECTED" {
		t.Fatalf("record 0 status = %v, want REJECTED", records[0]["status"])
	}
	if _, ok := records[1]["@unresolved"]; !ok {
		t.Fatalf("record 1 should be @unresolved for a missing id, got %v", records[1])
	}
	if records[2]["status"] != "PAID" {
		t.Fatalf("record 2 status = %v, want PAID", records[2]["status"])
	}
}

// Redaction is the property most likely to regress silently, and the one that
// matters most: these are real national ID numbers and card PANs in production.
func TestPayerPIIIsRedactedByDefault(t *testing.T) {
	out := runMockCLI(t, "payments", "get", "D-4-paid")

	assertNotContains(t, out,
		"Thiago Gabriel",     // payer.name / card.holder_name
		"thiago@example.com", // payer.email
		"53033315550",        // payer.document — a CPF
		"4111111111111111",   // card.number
		"203.0.113.7",        // payer.ip
		"Volta Redonda",      // payer.address.city
	)
	// Triage fields must survive, or the tool is useless.
	assertContains(t, out, `"last4":"1111"`, `"brand":"VI"`, `"bin":"411111"`, `"status":"PAID"`)
}

func TestExposeRevealsOneFieldOnly(t *testing.T) {
	out := runMockCLI(t, "payments", "get", "D-4-paid", "--expose", "payer.email")

	assertContains(t, out, "thiago@example.com")
	assertNotContains(t, out, "53033315550")
}

func TestPaymentsStatusReturnsOnlyTheTriple(t *testing.T) {
	out := runMockCLI(t, "payments", "status", "D-4-rejected")

	records := decodeLines(t, out)
	if records[0]["status"] != "REJECTED" || records[0]["status_code"] != "300" {
		t.Fatalf("unexpected status triple: %v", records[0])
	}
	if _, ok := records[0]["payer"]; ok {
		t.Fatal("payments status returned a payer block; it should be the triple only")
	}
}

func TestOrdersGetResolvesMerchantReference(t *testing.T) {
	out := runMockCLI(t, "orders", "get", "order-rejected")

	records := decodeLines(t, out)
	if records[0]["payment_id"] != "D-4-rejected" {
		t.Fatalf("order did not resolve to a payment id: %v", records[0])
	}
}

func TestRefundsAndChargebacksResolve(t *testing.T) {
	refund := decodeLines(t, runMockCLI(t, "refunds", "get", "REF-pending"))
	if refund[0]["status"] != "PENDING" {
		t.Fatalf("refund status = %v, want PENDING", refund[0]["status"])
	}

	chargeback := decodeLines(t, runMockCLI(t, "chargebacks", "get", "CHAR42342"))
	if chargeback[0]["payment_id"] != "D-4-chargeback" {
		t.Fatalf("chargeback did not link to a payment: %v", chargeback[0])
	}
}

// Payouts go to a different host with the same signer, so this proves the
// payouts path works end to end.
func TestPayoutsGetReachesThePayoutsHost(t *testing.T) {
	out := runMockCLI(t, "payouts", "get", "P-1-delivered")

	records := decodeLines(t, out)
	if records[0]["status"] != "DELIVERED" {
		t.Fatalf("payout status = %v, want DELIVERED", records[0]["status"])
	}
}

// Countries are positional and repeatable, mirroring the multi-id `get`
// contract: one record per country, in input order.
func TestPaymentMethodsListIsMultiCountry(t *testing.T) {
	out := runMockCLI(t, "payment-methods", "list", "PH", "BR", "VN")

	records := decodeLines(t, out)
	if len(records) != 3 {
		t.Fatalf("expected one record per country, got %d:\n%s", len(records), out)
	}
	for i, want := range []string{"PH", "BR", "VN"} {
		if records[i]["country"] != want {
			t.Fatalf("record %d country = %v, want %s — input order was not preserved", i, records[i]["country"], want)
		}
		if records[i]["count"] == nil {
			t.Fatalf("record %d has no method count: %v", i, records[i])
		}
	}
	// A payment method's name is a product, not PII, and must not be redacted.
	assertContains(t, out, "Smart Pix", "Credit Card")
}

// With no argument it falls back to --country, then the profile's country, so
// the common single-market case needs no argument at all.
func TestPaymentMethodsListFallsBackToTheCountryFlag(t *testing.T) {
	viaFlag := decodeLines(t, runMockCLI(t, "payment-methods", "list", "--country", "PH"))
	if viaFlag[0]["country"] != "PH" {
		t.Fatalf("--country was not honoured: %v", viaFlag[0])
	}

	// No country anywhere -> the profile default (BR).
	viaProfile := decodeLines(t, runMockCLI(t, "payment-methods", "list"))
	if viaProfile[0]["country"] != "BR" {
		t.Fatalf("expected the profile default BR, got %v", viaProfile[0])
	}
}

// An unsupported country among several must not lose the others.
func TestPaymentMethodsListReportsPerCountryFailures(t *testing.T) {
	out := runMockCLI(t, "payment-methods", "list", "PH", "ZZ", "BR")

	records := decodeLines(t, out)
	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d:\n%s", len(records), out)
	}
	if records[1]["reason"] == nil {
		t.Fatalf("the unsupported country should carry a reason: %v", records[1])
	}
	if records[2]["count"] == nil {
		t.Fatalf("a failure in the middle aborted the run: %v", records[2])
	}
}

// The discovery sweep takes positional candidates too.
func TestPaymentMethodsCountriesProbes(t *testing.T) {
	out := runMockCLI(t, "payment-methods", "countries", "PH", "ZZ", "BR")

	records := decodeLines(t, out)
	if len(records) != 3 {
		t.Fatalf("expected 3 probe records, got %d:\n%s", len(records), out)
	}
	var supported, unsupported int
	for _, r := range records {
		if r["supported"] == true {
			supported++
		} else {
			unsupported++
		}
	}
	if supported != 2 || unsupported != 1 {
		t.Fatalf("expected 2 supported and 1 not, got %d/%d:\n%s", supported, unsupported, out)
	}
}

func TestInvestigatePaymentExplainsRejection(t *testing.T) {
	result := decodeOne(t, runMockCLI(t, "investigate", "payment", "D-4-rejected", "--format", "json"))

	if result["scenario"] != "payment" {
		t.Fatalf("scenario = %v, want payment", result["scenario"])
	}
	if terminal, ok := result["terminal"].(bool); !ok || !terminal {
		t.Fatalf("a REJECTED payment should be terminal: %v", result["terminal"])
	}
	verdict, _ := result["verdict"].(string)
	if !strings.Contains(verdict, "rejected") {
		t.Fatalf("verdict does not explain the rejection: %q", verdict)
	}
	if _, ok := result["evidence"]; !ok {
		t.Fatal("investigate returned a verdict with no evidence attached")
	}
}

// DELIVERED is the payout status most often misread as terminal. The whole
// point of investigate is that it says so out loud.
func TestInvestigatePayoutFlagsDeliveredAsInFlight(t *testing.T) {
	result := decodeOne(t, runMockCLI(t, "investigate", "payout", "P-1-delivered", "--format", "json"))

	if terminal, ok := result["terminal"].(bool); !ok || terminal {
		t.Fatalf("DELIVERED must not be reported as terminal: %v", result["terminal"])
	}
	steps := strings.Join(toStrings(result["next_steps"]), " ")
	if !strings.Contains(steps, "in flight") {
		t.Fatalf("next_steps should say the money is in flight: %q", steps)
	}
}

func TestInvestigateRefundComparesAgainstParentPayment(t *testing.T) {
	result := decodeOne(t, runMockCLI(t, "investigate", "refund", "REF-pending", "--format", "json"))

	evidence, ok := result["evidence"].(map[string]any)
	if !ok {
		t.Fatalf("evidence missing: %v", result)
	}
	if _, ok := evidence["payment"]; !ok {
		t.Fatal("investigate refund did not fetch the parent payment for comparison")
	}
	steps := strings.Join(toStrings(result["next_steps"]), " ")
	if !strings.Contains(steps, "PARTIAL") && !strings.Contains(steps, "FULL") {
		t.Fatalf("next_steps should classify the refund as partial or full: %q", steps)
	}
}

func TestInvestigateOrderChainsToThePayment(t *testing.T) {
	result := decodeOne(t, runMockCLI(t, "investigate", "order", "order-rejected", "--format", "json"))

	if result["scenario"] != "order" || result["subject"] != "order-rejected" {
		t.Fatalf("unexpected subject: %v", result)
	}
	evidence, _ := result["evidence"].(map[string]any)
	if _, ok := evidence["payment"]; !ok {
		t.Fatal("investigate order did not chain through to the payment")
	}
}

func TestRawAPIIsGetOnly(t *testing.T) {
	out := runMockCLI(t, "api", "get", "/payments/D-4-paid", "--format", "json")
	assertContains(t, out, `"status": "PAID"`)

	// There is no --method flag, by construction: the read-only guarantee is
	// the absence of a code path, not a check that could be relaxed.
	help := runMockCLI(t, "api", "get", "--help")
	assertNotContains(t, help, "--method")
}

func TestUnknownSubcommandIsStructured(t *testing.T) {
	out := runMockCLIErr(t, "payments", "nonsense")
	assertContains(t, out, "fixable_by")
}

func TestUsageListsTheDomains(t *testing.T) {
	out := runMockCLI(t, "usage", "--format", "json")
	assertContains(t, out, "investigate", "payments", "orders", "payouts", "--form")
}

func TestDebugDoesNotLeakSigningMaterial(t *testing.T) {
	out := runMockCLI(t, "payments", "get", "D-4-paid", "--debug")

	assertContains(t, out, "@debug")
	assertNotContains(t, out,
		"mocksecret", // the signing secret
		"mocktrans",  // X-Trans-Key
	)
}

func toStrings(value any) []string {
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// Several commands used to pass a hardcoded "" as the format, silently
// discarding the user's --format flag. The flag is part of the CLI's advertised
// contract, so a command that ignores it is a broken command.
func TestFormatFlagIsHonouredEverywhere(t *testing.T) {
	for _, args := range [][]string{
		{"usage"},
		{"payments", "usage"},
		{"investigate", "usage"},
		{"config", "show"},
	} {
		out := runMockCLI(t, append(args, "--format", "yaml")...)
		if strings.HasPrefix(strings.TrimSpace(out), "{") {
			t.Errorf("%v --format yaml emitted JSON:\n%s", args, out)
		}
	}
}

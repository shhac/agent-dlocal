package cli

import (
	"github.com/spf13/cobra"

	"github.com/shhac/agent-dlocal/internal/cli/shared"
)

// usage is the agent-facing command map: what to reach for, phrased by the
// question being answered rather than by the endpoint being called. An agent
// reads this before guessing.

type usageEntry struct {
	Command string `json:"command"`
	Answers string `json:"answers"`
	Note    string `json:"note,omitempty"`
}

type domainUsage struct {
	Domain   string       `json:"domain"`
	Summary  string       `json:"summary"`
	Commands []usageEntry `json:"commands"`
	Notes    []string     `json:"notes,omitempty"`
}

var paymentsUsage = domainUsage{
	Domain:  "payments",
	Summary: "Retrieve dLocal payins by dLocal payment id.",
	Commands: []usageEntry{
		{Command: "agent-dlocal payments get <payment_id>...", Answers: "What is the full record for this payment — status, payer, card, amounts?"},
		{Command: "agent-dlocal payments status <payment_id>...", Answers: "Just the status triple, when the full record is more than you need.", Note: "Only served within 12 months of the payment's creation date."},
	},
	Notes: []string{
		"Payment ids look like D-4-<uuid>. If you have your OWN order reference instead, use 'orders get'.",
		"To explain a failure rather than just report it, prefer 'investigate payment'.",
	},
}

var ordersUsage = domainUsage{
	Domain:  "orders",
	Summary: "Resolve a merchant-side order id to a dLocal payment.",
	Commands: []usageEntry{
		{Command: "agent-dlocal orders get <order_id>...", Answers: "Which dLocal payment corresponds to the order id my system assigned?"},
	},
	Notes: []string{
		"order_id is YOUR reference, not dLocal's. This is the bridge from a merchant order to a payment_id.",
	},
}

var refundsUsage = domainUsage{
	Domain:  "refunds",
	Summary: "Retrieve refunds by dLocal refund id.",
	Commands: []usageEntry{
		{Command: "agent-dlocal refunds get <refund_id>...", Answers: "What is the state of this refund and which payment does it belong to?"},
	},
	Notes: []string{
		"A PENDING refund on a cash or bank-transfer method is normal — those settle asynchronously.",
		"To compare a refund against its parent payment, prefer 'investigate refund'.",
	},
}

var chargebacksUsage = domainUsage{
	Domain:  "chargebacks",
	Summary: "Retrieve chargebacks by dLocal chargeback id.",
	Commands: []usageEntry{
		{Command: "agent-dlocal chargebacks get <chargeback_id>...", Answers: "What is the state and reason for this chargeback?"},
	},
}

var payoutsUsage = domainUsage{
	Domain:  "payouts",
	Summary: "Retrieve payouts by dLocal payout id.",
	Commands: []usageEntry{
		{Command: "agent-dlocal payouts get <payout_id>...", Answers: "Where is this payout and what state is it in?"},
	},
	Notes: []string{
		"DELIVERED is NOT final and NOT a failure: the money is in flight at the beneficiary's bank. Do not re-send on the strength of that status.",
		"Payouts use a separate host and signing scheme, but the same profile credentials.",
		"To get the status explained rather than reported, prefer 'investigate payout'.",
	},
}

var paymentMethodsUsage = domainUsage{
	Domain:  "payment-methods",
	Summary: "Look up which payment methods a country supports.",
	Commands: []usageEntry{
		{Command: "agent-dlocal payment-methods list --country BR", Answers: "Which methods are enabled for this country?"},
		{Command: "agent-dlocal payment-methods countries --supported", Answers: "Which countries can this merchant take money in at all?", Note: "dLocal has no list-countries endpoint, so this probes each market — one request per country."},
	},
	Notes: []string{
		"These are the only list-shaped commands in the CLI, and neither is a paginated collection — dLocal has no list endpoints.",
		"Country codes are ISO 3166-1 alpha-2. dLocal does NOT support Singapore, South Korea, Taiwan, Hong Kong, Venezuela, or western Europe.",
	},
}

var investigateUsage = domainUsage{
	Domain:  "investigate",
	Summary: "Incident-language entry points that chain several reads into one answer.",
	Commands: []usageEntry{
		{Command: "agent-dlocal investigate payment <payment_id>", Answers: "Why did this payment fail?"},
		{Command: "agent-dlocal investigate order <order_id>", Answers: "The customer says they paid but our order shows unpaid — what happened?"},
		{Command: "agent-dlocal investigate refund <refund_id>", Answers: "What happened to this refund?"},
		{Command: "agent-dlocal investigate payout <payout_id>", Answers: "Where is this payout?"},
	},
	Notes: []string{
		"Prefer these over raw gets for incident questions: they return a verdict plus the evidence, so you make one call instead of correlating four.",
	},
}

func rootUsage() map[string]any {
	return map[string]any{
		"tool":    "agent-dlocal",
		"purpose": "Read-only dLocal investigation and triage for AI agents.",
		"start_here": []usageEntry{
			{Command: "agent-dlocal investigate payment <payment_id>", Answers: "Why did this payment fail?"},
			{Command: "agent-dlocal investigate payout <payout_id>", Answers: "Where is this payout?"},
			{Command: "agent-dlocal investigate refund <refund_id>", Answers: "What happened to this refund?"},
			{Command: "agent-dlocal orders get <order_id>", Answers: "I only have my own order reference, not a dLocal id."},
		},
		"domains": []domainUsage{
			investigateUsage, paymentsUsage, ordersUsage, refundsUsage,
			chargebacksUsage, payoutsUsage, paymentMethodsUsage,
		},
		"setup": []usageEntry{
			{Command: "agent-dlocal auth add <profile> --form", Answers: "Store credentials.", Note: "ALWAYS prefer --form: the user types the secrets into a native OS dialog, so they never enter the transcript. Never ask a user to paste an X-Login, X-Trans-Key or secret key into chat."},
			{Command: "agent-dlocal auth check", Answers: "Are the stored credentials working?"},
		},
		"conventions": []string{
			"Read-only: every command is a GET. Nothing here moves money.",
			"get takes multiple ids and returns one record per id in input order; a miss is an @unresolved line on stdout with exit 0.",
			"NDJSON by default; --format json|yaml available.",
			"Payer PII and card numbers are redacted by default; --expose <path> reveals a specific field when an investigation needs it.",
			"Live and sandbox are separate ledgers — an id from one never resolves against the other.",
		},
	}
}

func registerUsageCommand(root *cobra.Command, globals shared.GlobalsFunc) {
	root.AddCommand(&cobra.Command{
		Use:   "usage",
		Short: "Show the agent-facing command map",
		RunE: func(cmd *cobra.Command, args []string) error {
			shared.WriteItem(rootUsage(), globals().Format)
			return nil
		},
	})
}

// registerDomainUsage gives each group its own `usage` subcommand, so an agent
// already inside a domain can ask what that domain offers without re-reading
// the whole map.
func registerDomainUsage(group *cobra.Command, name string, usage domainUsage) {
	group.AddCommand(&cobra.Command{
		Use:   "usage",
		Short: "Show what the " + name + " domain is for",
		RunE: func(cmd *cobra.Command, args []string) error {
			shared.WriteItem(usage, "")
			return nil
		},
	})
}

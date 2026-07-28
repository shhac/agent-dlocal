package mockdlocal

// Routes is the single source of truth for the mocked surface: it backs
// `mockdlocal --routes`, the command's long help, and the GET / inventory, so
// the three can never drift apart.
func Routes() []string {
	return []string{
		"GET  /                                  route inventory (unauthenticated)",
		"GET  /payments/{id}                     payment record; id suffix picks the scenario",
		"GET  /payments/{id}/status              status triple only",
		"GET  /orders/{order_id}                 merchant order -> payment linkage",
		"GET  /refunds/{id}                      refund record",
		"GET  /chargebacks/{id}                  chargeback record (id must start with CHAR)",
		"GET  /payments-methods?country=XX       enabled payment methods for a country",
		"GET  /v2/payouts/{id}                   payout record (Payload-Signature scheme)",
		"",
		"Payin id suffixes:  -paid -pending -rejected -cancelled -expired -refunded -chargeback",
		"Payout id suffixes: -paid -pending -delivered -rejected -cancelled",
		"Anything else 404s in dLocal's error shape.",
		"",
		"Every route except GET / verifies the HMAC signature over the bytes received.",
	}
}

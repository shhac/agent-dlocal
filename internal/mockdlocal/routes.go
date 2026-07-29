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
		"GET  /v2/payouts/{id}                   payout record (same Authorization scheme, string error codes)",
		"",
		"Payin id suffixes:  -paid -pending -rejected -cancelled -expired -refunded -chargeback",
		"Payout id suffixes: -paid -pending -delivered -rejected -cancelled",
		"Anything else 404s in dLocal's error shape.",
		"",
		"Every route except GET / verifies the HMAC signature over the bytes received.",
		"",
		"Rejections mirror the real API (verified against sandbox.dlocal.com):",
		"  bad signature      400 {code:5000}   <- NOT a 401",
		"  missing header     400 {code:5001, param}",
		"  unknown login      403 {code:3001}   <- same as an IP-blocked caller",
		"  unknown payment    404 {code:4000}",
		"  unknown refund     404 {code:4001}",
		"  unrouted path      404 NOT_FOUND     <- plain text, not JSON",
		"A stale X-Date is ACCEPTED: the date is signed as well as sent, so clock",
		"skew is not a failure mode.",
	}
}

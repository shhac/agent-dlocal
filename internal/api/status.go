package api

// dLocal reports outcomes as a triple: status (a word), status_code (a number),
// and status_detail (a sentence). Turning that triple into something an agent
// can act on is most of this tool's value, so the mapping is a table rather
// than something each command improvises.

// StatusMeaning explains one status and says whether it is terminal — the
// distinction an agent most needs, because "not yet final" and "failed" call
// for opposite responses.
type StatusMeaning struct {
	Status   string `json:"status"`
	Code     string `json:"code"`
	Meaning  string `json:"meaning"`
	Terminal bool   `json:"terminal"`
	Action   string `json:"action,omitempty"`
}

var payinStatuses = map[string]StatusMeaning{
	"PENDING": {
		Status: "PENDING", Code: "100", Terminal: false,
		Meaning: "Received by dLocal and awaiting processing or a customer action",
		Action:  "For REDIRECT flows the customer has not finished paying yet; for cash or ticket methods the voucher is unpaid. Not a failure — re-check later or wait for the notification.",
	},
	"PAID": {
		Status: "PAID", Code: "200", Terminal: true,
		Meaning: "The payment was paid",
	},
	"REJECTED": {
		Status: "REJECTED", Code: "300", Terminal: true,
		Meaning: "The payment was rejected",
		Action:  "Read status_detail for the specific reason — it distinguishes an issuer decline from a validation or fraud-rule rejection, which need different follow-ups.",
	},
	"CANCELLED": {
		Status: "CANCELLED", Code: "400", Terminal: true,
		Meaning: "The payment was cancelled",
		Action:  "Cancellation is merchant- or customer-initiated rather than an issuer decision; check your own cancellation path before treating it as a dLocal failure.",
	},
	"EXPIRED": {
		Status: "EXPIRED", Code: "600", Terminal: true,
		Meaning: "The payment window elapsed before the customer paid",
		Action:  "Typical for cash and bank-transfer vouchers the customer never paid. A new payment is required; this one cannot be revived.",
	},
	"AUTHORIZED": {
		Status: "AUTHORIZED", Code: "", Terminal: false,
		Meaning: "Card authorization succeeded but the funds are not captured",
		Action:  "Awaiting capture. If it is stuck here, the capture step never ran.",
	},
	"VERIFIED": {
		Status: "VERIFIED", Code: "", Terminal: false,
		Meaning: "The card was verified without a charge",
	},
}

var payoutStatuses = map[string]StatusMeaning{
	"PENDING": {
		Status: "PENDING", Code: "100", Terminal: false,
		Meaning: "Received by dLocal and pending processing",
	},
	"DELIVERED": {
		Status: "DELIVERED", Code: "500", Terminal: false,
		Meaning: "Handed to the receiving institution and being processed",
		Action:  "NOT a final state and NOT a failure. The money is in flight at the beneficiary's bank. Do not re-send the payout on the basis of this status — wait for PAID or REJECTED.",
	},
	"PAID": {
		Status: "PAID", Code: "200", Terminal: true,
		Meaning: "The payout was paid",
	},
	"REJECTED": {
		Status: "REJECTED", Code: "300", Terminal: true,
		Meaning: "The payout was rejected",
		Action:  "Read status_detail — beneficiary bank-account validation failures are the most common cause and are fixable by correcting the account details and re-sending.",
	},
	"CANCELLED": {
		Status: "CANCELLED", Code: "400", Terminal: true,
		Meaning: "The payout was cancelled by the merchant",
	},
}

// ExplainPayinStatus returns the meaning of a payin status, and whether it is
// one the table knows.
func ExplainPayinStatus(status string) (StatusMeaning, bool) {
	meaning, ok := payinStatuses[status]
	return meaning, ok
}

// ExplainPayoutStatus returns the meaning of a payout status. The payin and
// payout tables are deliberately separate: code 500 means DELIVERED for a
// payout and nothing at all for a payin, so a shared table would mislead.
func ExplainPayoutStatus(status string) (StatusMeaning, bool) {
	meaning, ok := payoutStatuses[status]
	return meaning, ok
}

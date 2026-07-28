package mockdlocal

import (
	"net/http"
	"strings"
)

// Fixtures are selected by ID SUFFIX so a test names the scenario it wants
// ("D-4-rejected") instead of memorizing an opaque id.
type scenario struct {
	status       string
	statusCode   string
	statusDetail string
}

var payinScenarios = map[string]scenario{
	"paid":       {"PAID", "200", "The payment was paid"},
	"pending":    {"PENDING", "100", "The payment is pending"},
	"rejected":   {"REJECTED", "300", "The payment was rejected by the issuing bank"},
	"cancelled":  {"CANCELLED", "400", "The payment was cancelled"},
	"expired":    {"EXPIRED", "600", "The payment voucher expired before it was paid"},
	"refunded":   {"PAID", "200", "The payment was paid"},
	"chargeback": {"PAID", "200", "The payment was paid"},
}

var payoutScenarios = map[string]scenario{
	"paid":      {"PAID", "200", "The payout was paid"},
	"pending":   {"PENDING", "100", "The payout is pending"},
	"delivered": {"DELIVERED", "500", "The payout is being processed by the beneficiary bank"},
	"rejected":  {"REJECTED", "300", "The payout was rejected: invalid beneficiary bank account"},
	"cancelled": {"CANCELLED", "400", "The payout was cancelled by the merchant"},
}

func scenarioFor(id string, table map[string]scenario) (scenario, bool) {
	for suffix, sc := range table {
		if strings.HasSuffix(id, "-"+suffix) {
			return sc, true
		}
	}
	return scenario{}, false
}

// payerBlock is populated with realistic PII precisely so redaction tests have
// something real to assert is masked. Empty fields would let a redaction
// regression pass silently.
func payerBlock() map[string]any {
	return map[string]any{
		"name":           "Thiago Gabriel",
		"email":          "thiago@example.com",
		"document":       "53033315550",
		"user_reference": "12345",
		"ip":             "203.0.113.7",
		"device_id":      "2fg3d4gf234",
		"address": map[string]any{
			"state":    "Rio de Janeiro",
			"city":     "Volta Redonda",
			"zip_code": "27275-595",
			"street":   "Servidao B-1",
			"number":   "1106",
		},
	}
}

// cardBlock carries both the PAN and the triage-relevant descriptors, so tests
// can assert that brand/last4/bin survive redaction while number and cvv do not.
func cardBlock() map[string]any {
	return map[string]any{
		"holder_name":      "Thiago Gabriel",
		"number":           "4111111111111111",
		"cvv":              "123",
		"brand":            "VI",
		"last4":            "1111",
		"bin":              "411111",
		"expiration_month": 10,
		"expiration_year":  2040,
	}
}

func handlePayment(w http.ResponseWriter, r *http.Request, _ []byte) {
	id := r.PathValue("id")
	sc, ok := scenarioFor(id, payinScenarios)
	if !ok {
		writeError(w, http.StatusNotFound, 4000, "Payment not found: "+id)
		return
	}

	payment := map[string]any{
		"id":                  id,
		"amount":              285,
		"currency":            "BRL",
		"country":             "BR",
		"status":              sc.status,
		"status_code":         sc.statusCode,
		"status_detail":       sc.statusDetail,
		"payment_method_id":   "CARD",
		"payment_method_type": "CARD",
		"payment_method_flow": "DIRECT",
		"order_id":            "order-" + id,
		"notification_url":    "http://merchant.example.com/notifications",
		"created_date":        "2026-07-20T20:37:20.000+0000",
		"payer":               payerBlock(),
		"card":                cardBlock(),
	}
	if sc.status == "PAID" {
		payment["approved_date"] = "2026-07-20T20:37:25.000+0000"
	}
	if strings.HasSuffix(id, "-pending") {
		payment["redirect_url"] = "https://sandbox.dlocal.com/redirect/" + id
	}
	writeJSON(w, http.StatusOK, payment)
}

func handlePaymentStatus(w http.ResponseWriter, r *http.Request, _ []byte) {
	id := r.PathValue("id")
	sc, ok := scenarioFor(id, payinScenarios)
	if !ok {
		writeError(w, http.StatusNotFound, 4000, "Payment not found: "+id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":            id,
		"status":        sc.status,
		"status_code":   sc.statusCode,
		"status_detail": sc.statusDetail,
	})
}

func handleOrder(w http.ResponseWriter, r *http.Request, _ []byte) {
	orderID := r.PathValue("id")
	suffix := orderID[strings.LastIndex(orderID, "-")+1:]
	sc, ok := payinScenarios[suffix]
	if !ok {
		writeError(w, http.StatusNotFound, 4000, "Order not found: "+orderID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"order_id":      orderID,
		"payment_id":    "D-4-" + suffix,
		"currency":      "BRL",
		"amount":        285,
		"status":        sc.status,
		"status_code":   sc.statusCode,
		"status_detail": sc.statusDetail,
		"created_date":  "2026-07-20T20:37:20.000+0000",
	})
}

func handleRefund(w http.ResponseWriter, r *http.Request, _ []byte) {
	id := r.PathValue("id")
	sc, ok := scenarioFor(id, map[string]scenario{
		"paid":      {"SUCCESS", "200", "The refund was paid"},
		"pending":   {"PENDING", "100", "The refund is pending"},
		"rejected":  {"REJECTED", "300", "The refund was rejected"},
		"cancelled": {"CANCELLED", "400", "The refund was cancelled"},
	})
	if !ok {
		writeError(w, http.StatusNotFound, 4000, "Refund not found: "+id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":            id,
		"payment_id":    "D-4-refunded",
		"amount":        100,
		"currency":      "BRL",
		"status":        sc.status,
		"status_code":   sc.statusCode,
		"status_detail": sc.statusDetail,
		"created_date":  "2026-07-21T09:00:00.000+0000",
	})
}

func handleChargeback(w http.ResponseWriter, r *http.Request, _ []byte) {
	id := r.PathValue("id")
	if !strings.HasPrefix(id, "CHAR") {
		writeError(w, http.StatusNotFound, 4000, "Chargeback not found: "+id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":            id,
		"payment_id":    "D-4-chargeback",
		"amount":        285,
		"currency":      "BRL",
		"status":        "PENDING",
		"status_code":   "100",
		"status_detail": "The chargeback is under review",
		"reason":        "Cardholder does not recognize the transaction",
		"created_date":  "2026-07-22T11:00:00.000+0000",
	})
}

func handlePaymentMethods(w http.ResponseWriter, r *http.Request, _ []byte) {
	country := r.URL.Query().Get("country")
	if country == "" {
		writeError(w, http.StatusBadRequest, 5001, "country is required")
		return
	}
	writeJSON(w, http.StatusOK, []map[string]any{
		{
			"id":            "CARD",
			"type":          "CARD",
			"name":          "Credit Card",
			"logo":          "https://static.dlocal.com/images/providers/CARD.png",
			"allowed_flows": []string{"DIRECT"},
			"country":       country,
		},
		{
			"id":            "OS",
			"type":          "WALLET",
			"name":          "Smart Pix",
			"logo":          "https://static.dlocal.com/images/providers/OS.png",
			"allowed_flows": []string{"DIRECT", "REDIRECT"},
			"country":       country,
			"details": map[string]any{
				"banks": []map[string]any{
					{"id": "1", "name": "Banco do Brasil S.A"},
					{"id": "213", "name": "Banco Arbi S.A"},
				},
			},
		},
	})
}

func handlePayout(w http.ResponseWriter, r *http.Request, _ []byte) {
	id := r.PathValue("id")
	sc, ok := scenarioFor(id, payoutScenarios)
	if !ok {
		writeError(w, http.StatusNotFound, 4000, "Payout not found: "+id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":            id,
		"external_id":   "payout-" + id,
		"amount":        5000,
		"currency":      "BRL",
		"country":       "BR",
		"status":        sc.status,
		"status_code":   sc.statusCode,
		"status_detail": sc.statusDetail,
		"created_date":  "2026-07-23T08:00:00.000+0000",
		"beneficiary": map[string]any{
			"name":     "Ana Souza",
			"document": "20123456789",
		},
		"bank_account": map[string]any{
			"bank_code":      "001",
			"account_number": "1234567",
			"account_type":   "C",
		},
	})
}

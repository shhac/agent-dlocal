package api

// Markets is dLocal's supported country list, discovered empirically by probing
// GET /payments-methods for each candidate against the sandbox — the API has no
// "list countries" endpoint, so there is nothing to read it from.
//
// It exists because a merchant operating across several markets otherwise has
// to know every country code up front. `payment-methods countries` walks this
// list and reports which resolve, turning "which markets can I take money in?"
// from tribal knowledge into one command.
//
// Notable absences, all confirmed rejected with 5003: Singapore, South Korea,
// Taiwan, Hong Kong, Nepal, Cambodia, Venezuela, and the whole of western
// Europe. A country missing here is a candidate to add, not a bug — pass
// --country explicitly to probe one that is not on the list.
var Markets = []string{
	// South and Central America
	"AR", "BO", "BR", "CL", "CO", "CR", "DO", "EC", "GT", "HN",
	"MX", "NI", "PA", "PE", "PY", "SV", "UY",
	// Asia
	"BD", "CN", "ID", "IN", "JP", "LK", "MY", "PH", "PK", "TH", "VN",
	// Africa
	"CI", "CM", "EG", "GH", "KE", "MA", "NG", "RW", "SN", "TZ", "UG", "ZA", "ZM",
	// Other
	"TR", "US",
}

// IsKnownMarket reports whether a country code is on the list above.
func IsKnownMarket(country string) bool {
	for _, market := range Markets {
		if market == country {
			return true
		}
	}
	return false
}

package api

import "testing"

// The market list was discovered by probing the live sandbox. These spot-checks
// pin the finding so a future edit cannot quietly drop a real market or add a
// rejected one.
func TestMarketsMatchesWhatTheSandboxAccepts(t *testing.T) {
	for _, supported := range []string{"PH", "ID", "TH", "VN", "MY", "IN", "JP", "BR", "MX", "AR", "NG", "US"} {
		if !IsKnownMarket(supported) {
			t.Errorf("%s is accepted by dLocal but missing from Markets", supported)
		}
	}
	// Each of these was confirmed rejected with 5003.
	for _, rejected := range []string{"SG", "KR", "TW", "HK", "NP", "KH", "VE", "GB", "DE", "AU", "ZZ"} {
		if IsKnownMarket(rejected) {
			t.Errorf("%s is rejected by dLocal but listed as a market", rejected)
		}
	}
}

func TestMarketsHasNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range Markets {
		if seen[m] {
			t.Errorf("duplicate market %q", m)
		}
		seen[m] = true
	}
	if len(Markets) < 40 {
		t.Errorf("Markets has %d entries; the sandbox sweep found 43", len(Markets))
	}
}

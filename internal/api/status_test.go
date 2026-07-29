package api

import "testing"

// These tables encode money-critical semantics — most importantly that a payout
// DELIVERED is NOT terminal — and a one-character flip of any Terminal field
// would ship silently. Only two entries were reached at all before this test.
func TestPayinStatusTable(t *testing.T) {
	for _, tc := range []struct {
		status   string
		code     string
		terminal bool
	}{
		{"PENDING", "100", false},
		{"PAID", "200", true},
		{"REJECTED", "300", true},
		{"CANCELLED", "400", true},
		{"EXPIRED", "600", true},
		{"AUTHORIZED", "", false},
		{"VERIFIED", "", false},
	} {
		got, ok := ExplainPayinStatus(tc.status)
		if !ok {
			t.Errorf("%s is not in the payin table", tc.status)
			continue
		}
		if got.Code != tc.code {
			t.Errorf("%s code = %q, want %q", tc.status, got.Code, tc.code)
		}
		if got.Terminal != tc.terminal {
			t.Errorf("%s terminal = %v, want %v", tc.status, got.Terminal, tc.terminal)
		}
	}
}

func TestPayoutStatusTable(t *testing.T) {
	for _, tc := range []struct {
		status   string
		code     string
		terminal bool
	}{
		{"PENDING", "100", false},
		{"DELIVERED", "500", false}, // in flight at the beneficiary bank, NOT final
		{"PAID", "200", true},
		{"REJECTED", "300", true},
		{"CANCELLED", "400", true},
	} {
		got, ok := ExplainPayoutStatus(tc.status)
		if !ok {
			t.Errorf("%s is not in the payout table", tc.status)
			continue
		}
		if got.Code != tc.code {
			t.Errorf("%s code = %q, want %q", tc.status, got.Code, tc.code)
		}
		if got.Terminal != tc.terminal {
			t.Errorf("%s terminal = %v, want %v", tc.status, got.Terminal, tc.terminal)
		}
	}
}

// DELIVERED carries the instruction that stops a duplicate disbursement.
func TestDeliveredWarnsAgainstReSending(t *testing.T) {
	got, _ := ExplainPayoutStatus("DELIVERED")
	if got.Action == "" {
		t.Fatal("DELIVERED has no action; it is the status most often misread as final")
	}
}

// The tables are deliberately separate: code 500 means DELIVERED for a payout
// and nothing at all for a payin. Merging them would invent a payin status.
func TestTablesAreNotInterchangeable(t *testing.T) {
	if _, ok := ExplainPayinStatus("DELIVERED"); ok {
		t.Error("DELIVERED resolved against the payin table; the tables have been merged")
	}
	if _, ok := ExplainPayoutStatus("EXPIRED"); ok {
		t.Error("EXPIRED resolved against the payout table; the tables have been merged")
	}
	for _, unknown := range []string{"", "WAT", "paid"} {
		if _, ok := ExplainPayinStatus(unknown); ok {
			t.Errorf("payin table accepted %q", unknown)
		}
		if _, ok := ExplainPayoutStatus(unknown); ok {
			t.Errorf("payout table accepted %q", unknown)
		}
	}
}

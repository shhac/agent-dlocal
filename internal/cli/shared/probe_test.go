package shared

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shhac/agent-dlocal/internal/api"
)

func probeClient(t *testing.T, handler http.HandlerFunc) *api.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client, err := api.NewClient(api.Options{BaseURL: server.URL, Signer: api.PayinsSigner{}})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

// Concurrency must not reorder the output: two sweeps that cannot be diffed
// would defeat the point of running one.
func TestProbeCountriesPreservesInputOrder(t *testing.T) {
	client := probeClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{},{}]`))
	})

	countries := []string{"PH", "BR", "ID", "MX", "VN", "TH", "IN", "CO", "PE", "AR"}
	got := ProbeCountries(context.Background(), client, countries, 8)

	if len(got) != len(countries) {
		t.Fatalf("got %d results for %d countries", len(got), len(countries))
	}
	for i, want := range countries {
		if got[i].Country != want {
			t.Fatalf("result %d = %q, want %q — concurrency reordered the output", i, got[i].Country, want)
		}
	}
}

// An unsupported country is the ANSWER, not an error: it must not abort the
// sweep or the one failure would hide the other forty.
func TestProbeCountriesReportsFailuresAsResults(t *testing.T) {
	client := probeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("country") == "ZZ" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":5003,"message":"Country not supported"}`))
			return
		}
		_, _ = w.Write([]byte(`[{},{},{}]`))
	})

	got := ProbeCountries(context.Background(), client, []string{"PH", "ZZ", "BR"}, 4)

	if !got[0].Supported || got[0].PaymentMethods != 3 {
		t.Errorf("PH = %+v, want supported with 3 methods", got[0])
	}
	if got[1].Supported {
		t.Errorf("ZZ reported as supported: %+v", got[1])
	}
	if !strings.Contains(got[1].Reason, "Country not supported") {
		t.Errorf("ZZ reason does not explain the rejection: %q", got[1].Reason)
	}
	if !got[2].Supported {
		t.Errorf("BR = %+v — one failure aborted the sweep", got[2])
	}
}

func TestSortProbesPutsRichestMarketsFirst(t *testing.T) {
	probes := []CountryProbe{
		{Country: "ZZ"},
		{Country: "PH", Supported: true, PaymentMethods: 14},
		{Country: "BR", Supported: true, PaymentMethods: 49},
		{Country: "AA"},
	}
	SortProbes(probes)

	if probes[0].Country != "BR" || probes[1].Country != "PH" {
		t.Fatalf("supported markets not ordered by coverage: %+v", probes)
	}
	if probes[2].Country != "AA" || probes[3].Country != "ZZ" {
		t.Fatalf("unsupported markets should sort last, alphabetically: %+v", probes)
	}
}

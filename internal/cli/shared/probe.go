package shared

import (
	"context"
	"net/url"
	"sort"
	"sync"

	"github.com/shhac/agent-dlocal/internal/api"
)

// CountryProbe is one country's result from a discovery sweep.
type CountryProbe struct {
	Country        string `json:"country"`
	Supported      bool   `json:"supported"`
	PaymentMethods int    `json:"payment_methods,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

// ProbeCountries asks payments-methods about each country concurrently and
// reports which resolved. Results come back in the order the countries were
// given, so the output is stable across runs despite the concurrency — an
// unstable ordering would make two sweeps impossible to diff.
//
// A per-country failure is a RESULT, not an error: a merchant not being enabled
// somewhere is exactly what the caller is asking about, so it is reported
// rather than aborting the sweep.
func ProbeCountries(ctx context.Context, client *api.Client, countries []string, concurrency int) []CountryProbe {
	if concurrency < 1 {
		concurrency = 1
	}

	results := make([]CountryProbe, len(countries))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, country := range countries {
		wg.Add(1)
		go func(i int, country string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			probe := CountryProbe{Country: country}
			raw, err := client.Get(ctx, "/payments-methods", url.Values{"country": {country}})
			switch {
			case err != nil:
				probe.Reason = err.Error()
			default:
				probe.Supported = true
				probe.PaymentMethods = countJSONArray(raw)
			}
			results[i] = probe
		}(i, country)
	}
	wg.Wait()
	return results
}

// SortProbes orders results most-methods-first, so the markets with the richest
// coverage lead. Unsupported countries sort last, alphabetically.
func SortProbes(probes []CountryProbe) {
	sort.SliceStable(probes, func(i, j int) bool {
		if probes[i].Supported != probes[j].Supported {
			return probes[i].Supported
		}
		if probes[i].PaymentMethods != probes[j].PaymentMethods {
			return probes[i].PaymentMethods > probes[j].PaymentMethods
		}
		return probes[i].Country < probes[j].Country
	})
}

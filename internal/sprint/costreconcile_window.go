package sprint

import (
	"cmp"
	"math/big"
	"strings"
	"time"
)

// THE RELEASE'S WINDOW (docs/SPEC-RELEASE.md, "The store's spend matches the providers' own";
// the owner, 2026-10-05: "We should not make a release without verifying that we capture
// actual spend, not < 1/2 of it."). The cost reconciliation (cost_reconcile.go) sets a UTC
// day of the sprint's records beside the provider's count of that day; the release's spend
// check sets the sprint's records of a whole window (since the previous release) beside each
// provider's count of the same window. RecordedSpendBetween is the store's side of it: every
// consumer record on every primary, a work card's take or a read's run whatever its end,
// whose end stamp falls in the window, summed per provider at its charged figure (the
// harness's cost, else its tokens at the route's prices) and in tokens.

// WindowSpend is the sprint's records of one provider over a window.
type WindowSpend struct {
	USD      float64 `json:"usd"`      // the records' charged figures, summed
	Tokens   int64   `json:"tokens"`   // the records' tokens over the classes reported
	Records  int     `json:"records"`  // how many records ended in the window
	Unpriced int     `json:"unpriced"` // of them, how many carry no dollar figure
}

// RecordedSpendBetween is the sprint's records over [from, to), per provider (lower case):
// see above. A record with no provider is under "".
func RecordedSpendBetween(s *Snapshot, from, to time.Time) map[string]WindowSpend {
	out := map[string]WindowSpend{}
	if s == nil || s.Work == nil {
		return out
	}
	routes := map[string]string{}
	for _, r := range s.Routes {
		routes[r.Name] = r.Provider
	}
	sums := map[string]*big.Rat{}
	for _, c := range s.Work.Column(States...) {
		if IsSentinel(c) {
			continue
		}
		for _, con := range CardCostOf(c).Consumers {
			at, err := time.Parse(time.RFC3339, con.At)
			if err != nil || at.Before(from) || !at.Before(to) {
				continue
			}
			p := strings.ToLower(consumerProvider(routes, con))
			w := out[p]
			w.Records++
			w.Tokens += con.Usage.Tokens.Total()
			usd, err := amountOf(cmp.Or(con.Usage.Actual, con.Usage.Predicted))
			if err != nil || usd == nil {
				w.Unpriced++
			} else {
				if sums[p] == nil {
					sums[p] = new(big.Rat)
				}
				sums[p].Add(sums[p], usd)
			}
			out[p] = w
		}
	}
	for p, sum := range sums {
		w := out[p]
		w.USD, _ = sum.Float64()
		out[p] = w
	}
	return out
}

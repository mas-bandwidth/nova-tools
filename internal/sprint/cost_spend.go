package sprint

import (
	"cmp"
	"maps"
	"math/big"
	"slices"
	"strings"
	"time"
)

// THE SPRINT'S SPEND OVER A WINDOW (docs/SPEC-RELEASE.md, "The spend the store recorded
// matches each provider's own"; the owner, 2026-10-05: "We should not make a release without
// verifying that we capture actual spend, not < 1/2 of it."). The release's spend check sets
// what the store recorded of each paid provider over the release's window beside that
// provider's own count of the same window, and what the store recorded of each subscription
// friend's tokens beside the friend's harness receipts. These are the store's side: every
// consumer record on every primary of the work table (a take or a read, whatever its end)
// whose end stamp falls in the window, the dollars at each record's charged figure (the
// harness's cost, else its tokens at the route's prices), the tokens of a subscription run
// (no dollars) counted under the member or reader that ran it.

// subscriptionRecord says a record is a subscription run's: tokens, no dollars.
func subscriptionRecord(c Consumer) bool {
	return c.Usage.Unpriced == WhySubscription || subscriptionUsage(c.Usage)
}

// eachRecordIn calls f with every consumer record of the work table that ended in
// [from, to): sentinels aside, a record with no readable end stamp aside.
func eachRecordIn(s *Snapshot, from, to time.Time, f func(Consumer)) {
	if s.Work == nil {
		return
	}
	for _, c := range s.Work.Column(States...) {
		if IsSentinel(c) {
			continue
		}
		for _, con := range CardCostOf(c).Consumers {
			at, err := time.Parse(time.RFC3339, con.At)
			if err != nil || at.Before(from) || !at.Before(to) {
				continue
			}
			f(con)
		}
	}
}

// providerRoutes is each route's provider, by route name.
func providerRoutes(s *Snapshot) map[string]string {
	routes := map[string]string{}
	for _, r := range s.Routes {
		routes[r.Name] = r.Provider
	}
	return routes
}

// RecordedSpendIn is the store's dollars of a provider over [from, to): every priced
// record of that provider (its route's, else the provider of the model it reported) that
// ended in the window.
func RecordedSpendIn(s *Snapshot, provider string, from, to time.Time) float64 {
	routes := providerRoutes(s)
	sum := new(big.Rat)
	eachRecordIn(s, from, to, func(con Consumer) {
		if subscriptionRecord(con) || !strings.EqualFold(consumerProvider(routes, con), provider) {
			return
		}
		if usd, err := amountOf(cmp.Or(con.Usage.Actual, con.Usage.Predicted)); err == nil && usd != nil {
			sum.Add(sum, usd)
		}
	})
	f, _ := sum.Float64()
	return f
}

// RecordedProvidersIn is the paid providers the store knows of over [from, to), in name
// order: every route's provider, and the provider of every priced record in the window.
// A provider here is one the release's spend check must read.
func RecordedProvidersIn(s *Snapshot, from, to time.Time) []string {
	names := map[string]bool{}
	for _, r := range s.Routes {
		if r.Provider != "" {
			names[strings.ToLower(r.Provider)] = true
		}
	}
	routes := providerRoutes(s)
	eachRecordIn(s, from, to, func(con Consumer) {
		if subscriptionRecord(con) || cmp.Or(con.Usage.Actual, con.Usage.Predicted) == "" {
			return
		}
		if p := consumerProvider(routes, con); p != "" {
			names[strings.ToLower(p)] = true
		}
	})
	return slices.Sorted(maps.Keys(names))
}

// RecordedTokensIn is the store's tokens of each subscription friend over [from, to): every
// subscription record that ended in the window, its tokens under the member or reader that
// ran it ("-" for a record that names none).
func RecordedTokensIn(s *Snapshot, from, to time.Time) map[string]int64 {
	out := map[string]int64{}
	eachRecordIn(s, from, to, func(con Consumer) {
		if subscriptionRecord(con) {
			out[cmp.Or(con.Who, "-")] += max(con.Usage.Tokens.Total(), 0)
		}
	})
	return out
}

// RecordConsumer writes a consumer's record onto a primary in place, as a step that ends
// the run writes it (once per key, the primary's total with it): a world built outside a
// step, a release's spend check's tests among them, records its runs through it.
func RecordConsumer(pr *Card, c Consumer) {
	if pr.Fields == nil {
		pr.Fields = map[string]string{}
	}
	set := map[string]string{}
	addConsumer(pr, set, c)
	maps.Copy(pr.Fields, set)
}

package sprint

import (
	"cmp"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// Cost visibility (docs/SPEC-SPRINT.md section 1, the where view; the owner, 2026-10-04):
// what the sprint spends by tier and per landed card, counted by the tick from the
// sprint it reads anyway (the where record, store/where.go) and never from per-card
// reads at `where`: the cards' tiers from their briefs' tier lines, each stream's
// dollars per landed card, and each stream's spend split by the tier its attempts ran
// on (the cost records' tier, not the card's ceiling: a flash card escalated to pro
// shows both).

// TierCosts is one stream's tiers and spend as the where view carries them.
type TierCosts struct {
	// Tiers counts the stream's cards by the tier their briefs name (TierWord).
	Tiers map[string]int `json:"tiers,omitempty"`
	// PerLanded is spend over fully priced landed outcomes only (cost headline
	// coverage); partial or unpriced outcomes never lower its denominator.
	PerLanded        string                   `json:"per_landed"`
	LandedPricedCost string                   `json:"landed_priced_cost"`
	LegacyOutcomes   int                      `json:"legacy_outcomes,omitempty"`
	UnattributedRuns int                      `json:"unattributed_runs,omitempty"`
	Coverage         PriceCoverage            `json:"coverage"`
	LandedCoverage   PriceCoverage            `json:"landed_coverage"`
	TierCoverage     map[string]PriceCoverage `json:"tier_coverage,omitempty"`
	Routes           map[string]CostHeadline  `json:"routes,omitempty"`
	AccountingScope  string                   `json:"accounting_scope"`
	DeliveryStage    string                   `json:"delivery_stage"`
	VerifiedDev      int                      `json:"verified_dev"`
	PerVerifiedDev   string                   `json:"per_verified_dev"`
	VerifiedDevScope string                   `json:"verified_dev_scope"`
	MissingLineage   int                      `json:"missing_lineage,omitempty"`
	// CostByTier is the stream's spend by the tier each attempt and read ran on, dollars
	// and cents rounded up, over every card of the stream; a record with no tier is
	// "untiered".
	CostByTier map[string]string `json:"cost_by_tier,omitempty"`
	// TotalCost is the stream's complete recorded spend: every take and read of every card
	// of it in any column, landed or not, at each record's charged figure (the harness's
	// cost, else its tokens at the route's prices), dollars and cents rounded up; "" when
	// nothing of it was priced.
	TotalCost string `json:"total_cost,omitempty"`
	// WorkCost and ReadCost split TotalCost by kind: every take's charged figure and every
	// read's, dollars and cents rounded up; "" when nothing of that kind was priced. The
	// dashboard shows the reads as their own number beside the work.
	WorkCost string `json:"work_cost,omitempty"`
	ReadCost string `json:"read_cost,omitempty"`
	// ReadTokens is the tokens of the stream's subscription reads (WhySubscription), the
	// reads whose cost is their tokens; 0 when none.
	ReadTokens int64 `json:"read_tokens,omitempty"`
	// ReadsToday is the stream's reads that ended on the tick's UTC day, by the route that
	// priced them (WhySubscription for a subscription reader's, "-" for none): the exact
	// dollars, the tokens and the count, which ReadSpendLine sums over the streams.
	ReadsToday map[string]ReadDay `json:"reads_today,omitempty"`
	// UnpricedRuns counts the stream's records that carry no cost at all: runs whose usage
	// never reached the sprint (cardcost.WhyNoTokens), which the total cannot hold.
	UnpricedRuns int `json:"unpriced_runs,omitempty"`
	// ReadsNoTokens counts the stream's reads whose verdict was kept with a usage that
	// reported no token (cardcost.WhyNoTokens): one of UnpricedRuns each, counted apart
	// so a reader whose harness stops reporting is seen.
	ReadsNoTokens int `json:"reads_no_tokens,omitempty"`
	// Readers is each reader's spend on the stream's cards, by the reader that ran the read
	// (readers_spend.go): ReaderSpendsOf sums it over the streams for the readers table.
	Readers map[string]ReaderSpend `json:"readers,omitempty"`
	// Unreconciled is the SPRINT's, the same on every stream's record: what the providers
	// counted beyond the sprint's records since the epoch began (UnreconciledSpend,
	// cost_reconcile.go), dollars and cents rounded up; "" when nothing is.
	Unreconciled string `json:"unreconciled,omitempty"`
}

// TierWord is the tier a card's brief names on its line 1 (any word the brief carries),
// flash when it names none.
func TierWord(c *Card) string {
	m, _ := cardhdr.ReadModel(c.F("brief"))
	return cmp.Or(m.Tier, cardhdr.RouteFlash)
}

// TierCounts counts every primary on the work table (sentinels left out) by tier
// (TierWord).
func TierCounts(s *Snapshot) map[string]int {
	out := map[string]int{}
	for _, c := range s.Work.Column(States...) {
		if !IsSentinel(c) {
			out[TierWord(c)]++
		}
	}
	return out
}

// StreamTierCosts is each stream's TierCosts, by stream, over the work table's rows.
func StreamTierCosts(s *Snapshot) map[string]TierCosts {
	out := map[string]TierCosts{}
	for _, st := range s.Work.Rows() {
		out[st] = streamTierCosts(s, st)
	}
	return out
}

func streamTierCosts(s *Snapshot, stream string) TierCosts {
	t := TierCosts{Tiers: map[string]int{}, PerLanded: "-", CostByTier: map[string]string{}, ReadsToday: map[string]ReadDay{}, Readers: map[string]ReaderSpend{}, TierCoverage: map[string]PriceCoverage{}, Routes: map[string]CostHeadline{}, PerVerifiedDev: "unknown", DeliveryStage: "landed-on-recorded-base", AccountingScope: "completed-recorded-runs; placed-primary-records; work-and-reads; actual=harness-reported; estimated=route-prices; excludes-provider-gap", VerifiedDevScope: "all-stream-recorded-spend / verified-dev-outcomes"}
	byTier := map[string]*big.Rat{}
	workCost, readCost := new(big.Rat), new(big.Rat)
	pricedWork, pricedRead := false, false
	day := s.Now.UTC().Format(time.DateOnly)
	var landedCost []string
	var allCost []string
	for _, col := range States {
		for _, c := range s.Work.Cell(stream, col) {
			if IsSentinel(c) {
				continue
			}
			t.Tiers[TierWord(c)]++
			if col == Landed {
				t.LandedCoverage.All++
				usd, actual, priced := pricedOutcome(CardCostOf(c), c.F(FieldCost))
				if priced {
					if CardCostOf(c).Total.Records == 0 {
						t.LegacyOutcomes++
					}
					t.LandedCoverage.Priced++
					if actual {
						t.LandedCoverage.Actual++
					} else {
						t.LandedCoverage.Estimated++
					}
					landedCost = append(landedCost, usd)
				} else {
					t.LandedCoverage.Unpriced++
				}
			}
			// the card's whole record, whatever its column: every take and read behind it,
			// the records past the list's bound included (FieldCostTotal)
			tot := CardCostOf(c).Total
			t.Coverage.add(coverageOf(tot))
			t.UnattributedRuns += max(0, tot.Records-len(CardCostOf(c).Consumers))
			if tot.Charged != "" {
				allCost = append(allCost, tot.Charged)
			}
			t.UnpricedRuns += tot.Records - tot.ChargedOf
			for _, con := range CardCostOf(c).Consumers {
				tier := cmp.Or(con.Tier, "untiered")
				cov := t.TierCoverage[tier]
				cov.add(coverageOf(cardcost.NoTotal().Add(con.Usage)))
				t.TierCoverage[tier] = cov
				route := cmp.Or(con.Route, con.Usage.Route, "unattributed")
				headline := t.Routes[route]
				headline.add(con.Usage)
				t.Routes[route] = headline
				if con.Kind == "read" && con.Usage.Unpriced == WhySubscription {
					// a subscription read's cost is its tokens: priced, never a run unpriced
					t.UnpricedRuns--
					t.ReadTokens += con.Usage.Tokens.Total()
				}
				if con.Kind == "read" && con.Usage.Unpriced == cardcost.WhyNoTokens {
					t.ReadsNoTokens++
				}
				if con.Kind == "read" && strings.HasPrefix(con.At, day) {
					addReadDay(t.ReadsToday, con)
				}
				if con.Kind == "read" {
					rd := t.Readers[cmp.Or(con.Who, "-")]
					rd.addRead(con, s.Now)
					t.Readers[cmp.Or(con.Who, "-")] = rd
				}
				usd, err := amountOf(cmp.Or(con.Usage.Actual, con.Usage.Predicted))
				if err != nil || usd == nil {
					continue
				}
				byKind := workCost
				if con.Kind == "read" {
					byKind = readCost
				}
				byKind.Add(byKind, usd)
				if con.Kind == "read" {
					pricedRead = true
				} else {
					pricedWork = true
				}

				if byTier[tier] == nil {
					byTier[tier] = new(big.Rat)
				}
				byTier[tier].Add(byTier[tier], usd)
			}
		}
	}
	if sum, ok := cardcost.Sum(landedCost...); ok && t.LandedCoverage.Priced > 0 && len(landedCost) > 0 {
		if total, err := amountOf(sum); err == nil && total != nil {
			t.LandedPricedCost = cardcost.Cents(new(big.Rat).Set(total))
			t.PerLanded = cardcost.Cents(total.Quo(total, big.NewRat(int64(t.LandedCoverage.Priced), 1)))
		}
	}
	if sum, ok := cardcost.Sum(allCost...); ok && len(allCost) > 0 {
		if total, err := amountOf(sum); err == nil && total != nil {
			t.TotalCost = cardcost.Cents(total)
		}
	}
	if pricedWork {
		t.WorkCost = cardcost.Cents(workCost)
	}
	if pricedRead {
		t.ReadCost = cardcost.Cents(readCost)
	}
	if len(t.ReadsToday) == 0 {
		t.ReadsToday = nil
	}
	if len(t.Readers) == 0 {
		t.Readers = nil
	}
	// Aggregate totals retain records past the detail bound. Their price source
	// counts and known dollars stay visible in an unattributed bucket.
	var attributed PriceCoverage
	for _, v := range t.TierCoverage {
		attributed.add(v)
	}
	gap := PriceCoverage{All: max(0, t.Coverage.All-attributed.All), Priced: max(0, t.Coverage.Priced-attributed.Priced), Actual: max(0, t.Coverage.Actual-attributed.Actual), Estimated: max(0, t.Coverage.Estimated-attributed.Estimated), Unpriced: max(0, t.Coverage.Unpriced-attributed.Unpriced)}
	if gap.All > 0 {
		cov := t.TierCoverage["unattributed"]
		cov.add(gap)
		t.TierCoverage["unattributed"] = cov
		h := t.Routes["unattributed"]
		h.Coverage.add(gap)
		if sum, ok := cardcost.Sum(allCost...); ok && gap.Priced > 0 {
			remaining, err := amountOf(sum)
			if err == nil && remaining != nil {
				for _, v := range byTier {
					remaining.Sub(remaining, v)
				}
				if remaining.Sign() >= 0 {
					if byTier["unattributed"] == nil {
						byTier["unattributed"] = new(big.Rat)
					}
					byTier["unattributed"].Add(byTier["unattributed"], remaining)
					if prior, err := amountOf(h.USD); err == nil && prior != nil {
						remaining.Add(remaining, prior)
					}
					h.USD = remaining.RatString()
				}
			}
		}
		t.Routes["unattributed"] = h
	}
	tiers := make([]string, 0, len(byTier))
	for tier := range byTier {
		tiers = append(tiers, tier)
	}
	sort.Strings(tiers)
	for _, tier := range tiers {
		t.CostByTier[tier] = cardcost.Cents(byTier[tier])
	}
	for route, h := range t.Routes {
		t.Routes[route] = h.finish()
	}
	if unrec := UnreconciledSpend(s); unrec > 0 {
		t.Unreconciled = cardcost.Cents(new(big.Rat).SetFloat64(unrec))
	}
	return t
}

// errBadAmount is a dollar amount that is no decimal.
var errBadAmount = errors.New("not a decimal amount")

// amountOf is a decimal dollar string as a rational, nil for an empty one.
func amountOf(usd string) (*big.Rat, error) {
	if usd == "" {
		return nil, nil
	}
	r, ok := new(big.Rat).SetString(usd)
	if !ok {
		return nil, errBadAmount
	}
	return r, nil
}

// PerLandedOf refuses to infer dollar coverage from aggregate cells alone
// (docs/SPEC-SPRINT.md, cost headline coverage). Only the tick's explicit
// priced/all outcome counts support a per-outcome figure.
func PerLandedOf(costCell string, landed int) string { return "-" }

// ReadDay is one route's reads of a UTC day: the exact dollars charged (each read's
// actual cost where reported, else its predicted one; "" when none was priced), the
// tokens, and how many reads.
type ReadDay struct {
	USD    string `json:"usd,omitempty"`
	Tokens int64  `json:"tokens,omitempty"`
	Reads  int    `json:"reads"`
}

// addReadDay counts the read con into its route's day: the route that priced it, a
// subscription reader's under WhySubscription, "-" for a read no route priced; a read
// that reported nothing is not counted here (it is one of UnpricedRuns, and of
// ReadsNoTokens).
func addReadDay(days map[string]ReadDay, con Consumer) {
	u := con.Usage
	if !u.Tokens.Reported() && u.Actual == "" && u.Predicted == "" {
		return
	}
	route := cmp.Or(u.Route, "-")
	if u.Unpriced == WhySubscription {
		route = WhySubscription
	}
	d := days[route]
	d.Reads++
	d.Tokens += max(u.Tokens.Total(), 0)
	if usd := cmp.Or(u.Actual, u.Predicted); usd != "" {
		if sum, ok := cardcost.Sum(d.USD, usd); ok {
			d.USD = sum
		}
	}
	days[route] = d
}

// ReadSpendLine is the day's read spend per route over the streams' records, one line
// under the where view's summary: "reads today: pro-a $1.24 12 reads 3456789 tokens ·
// subscription tokens 3 reads 120000 tokens", routes in name order, a priced route's
// dollars rounded up to the cent, a route with nothing priced "unpriced"; "" when no
// read ended today.
func ReadSpendLine(streams map[string]TierCosts) string {
	days := map[string]ReadDay{}
	for _, tc := range streams {
		for route, d := range tc.ReadsToday {
			all := days[route]
			all.Reads += d.Reads
			all.Tokens += d.Tokens
			// a route with nothing priced keeps no dollars: the day's are the priced reads' alone
			if sum, ok := cardcost.Sum(all.USD, d.USD); ok && d.USD != "" {
				all.USD = sum
			}
			days[route] = all
		}
	}
	if len(days) == 0 {
		return ""
	}
	routes := make([]string, 0, len(days))
	for r := range days {
		routes = append(routes, r)
	}
	sort.Strings(routes)
	parts := make([]string, 0, len(routes))
	for _, r := range routes {
		d := days[r]
		cost := MoneyText(d.USD)
		if r == WhySubscription || d.USD == "" {
			cost = "unpriced"
			if r == WhySubscription {
				cost = "tokens"
			}
		}
		parts = append(parts, fmt.Sprintf("%s %s %d reads %d tokens", r, cost, d.Reads, d.Tokens))
	}
	return "reads today: " + strings.Join(parts, " · ")
}

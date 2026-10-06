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
	// PerLanded is the stream's cost per landed card priced whole: the landed cards that
	// have records and every record priced (LandedPriced), their costs summed, over their
	// count, dollars and cents rounded up (MoneyText). A landed card with any record
	// unpriced is in neither the sum nor the count, so an unpriced completion cannot make
	// the stream look cheaper. "-" with nothing landed, "unknown" (CostUnknown) when cards
	// landed and none of them was priced whole.
	PerLanded string `json:"per_landed"`
	// Landed and LandedPriced are PerLanded's denominators: every landed card of the
	// stream, and those of them priced whole. LandedCoverage is how the landed cards'
	// records were priced.
	Landed         int      `json:"landed"`
	LandedPriced   int      `json:"landed_priced"`
	LandedCoverage Coverage `json:"landed_coverage"`
	// SpendPerLanded is the stream's spend per verified dev outcome, in the scope
	// SpendPerLandedScope names: every recorded take and read of every card of the stream
	// on the table, in any column, the failed and returned ones included, over the cards
	// that landed. Its stage is the landing and its accounting is TotalCost's, so it is
	// compared only with another stream's SpendPerLanded. "unknown" while any of those
	// records is unpriced (Coverage.Unpriced), for an unpriced run would read as free;
	// "-" with nothing landed. A landed card with no record at all makes it "unknown" too.
	SpendPerLanded string `json:"spend_per_landed"`
	// SpendScope is SpendPerLandedScope, carried on the record so the figure is never
	// read without its scope.
	SpendScope string `json:"spend_scope"`
	// Coverage is how every record behind TotalCost was priced: what TotalCost holds
	// (actual, estimated) and what it cannot (unpriced, the unknown spend; subscription
	// tokens, which bill no dollar).
	Coverage Coverage `json:"coverage"`
	// CostByTier is the stream's spend by the tier each attempt and read ran on, dollars
	// and cents rounded up, over every card of the stream; a record with no tier is
	// "untiered".
	CostByTier map[string]string `json:"cost_by_tier,omitempty"`
	// TotalCost is the stream's complete recorded spend: every take and read of every card
	// of it in any column, landed or not, at each record's charged figure (the harness's
	// cost, else its tokens at the route's prices), dollars and cents rounded up; "" when
	// nothing of it was priced. It holds no unpriced record (Coverage.Unpriced): with any,
	// the spend is at least TotalCost and the rest is unknown.
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
	// never reached the sprint (cardcost.WhyNoTokens), which the total cannot hold
	// (Coverage.Unpriced).
	UnpricedRuns int `json:"unpriced_runs,omitempty"`
	// ReadsNoTokens counts the stream's reads whose verdict was kept with a usage that
	// reported no token (cardcost.WhyNoTokens): one of UnpricedRuns each, counted apart
	// so a reader whose harness stops reporting is seen.
	ReadsNoTokens int `json:"reads_no_tokens,omitempty"`
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

// CostUnknown is a cost headline whose records are not all priced: unknown spend shown
// as unknown, never as a figure an unpriced run made smaller.
const CostUnknown = "unknown"

// SpendPerLandedScope is TierCosts.SpendPerLanded's scope, carried with it.
const SpendPerLandedScope = "every recorded take and read of the stream's cards on the table, any column, over its landed cards"

// Coverage is how a cost headline's records were priced (docs/SPEC-SPRINT.md section 1,
// cost visibility): every record behind it (Records), those charged at a reported cost
// (Actual), at their tokens times the route's price sheet (Estimated), subscription runs
// that bill tokens and no dollar (Tokens), and those with no cost at all (Unpriced), the
// spend the headline cannot hold. The four parts sum to Records.
type Coverage struct {
	Records   int `json:"records"`
	Actual    int `json:"actual"`
	Estimated int `json:"estimated"`
	Tokens    int `json:"tokens,omitempty"`
	Unpriced  int `json:"unpriced"`
}

// Add is the two coverages counted together.
func (c Coverage) Add(o Coverage) Coverage {
	return Coverage{Records: c.Records + o.Records, Actual: c.Actual + o.Actual, Estimated: c.Estimated + o.Estimated,
		Tokens: c.Tokens + o.Tokens, Unpriced: c.Unpriced + o.Unpriced}
}

// Text is the coverage as a headline's sub-line prints it: "12 actual · 3 estimated ·
// 0 tokens · 2 unpriced of 17 records".
func (c Coverage) Text() string {
	return fmt.Sprintf("%d actual · %d estimated · %d tokens · %d unpriced of %d records", c.Actual, c.Estimated, c.Tokens, c.Unpriced, c.Records)
}

// coverageOf is the card's records' coverage, from the primary's total (every record, the
// ones past the list's bound included) and its list (the subscription reads).
func coverageOf(v CardCostView) Coverage {
	t := v.Total
	c := Coverage{Records: t.Records, Actual: t.ActualOf, Estimated: t.ChargedOf - t.ActualOf}
	for _, con := range v.Consumers {
		if con.Kind == "read" && con.Usage.Unpriced == WhySubscription {
			c.Tokens++
		}
	}
	c.Unpriced = max(c.Records-t.ChargedOf-c.Tokens, 0)
	return c
}

// perCard is usd over n cards, dollars and cents rounded up; "-" for no card.
func perCard(usd []string, n int) string {
	if n <= 0 {
		return "-"
	}
	total := new(big.Rat)
	if sum, ok := cardcost.Sum(usd...); ok && len(usd) > 0 {
		if r, err := amountOf(sum); err == nil && r != nil {
			total = r
		}
	}
	return cardcost.Cents(total.Quo(total, big.NewRat(int64(n), 1)))
}

// SprintTierCosts is the sprint's TierCosts: every stream of the work table counted as
// one, so its sums and its denominators are exact, never added up from rounded cells.
func SprintTierCosts(s *Snapshot) TierCosts { return streamTierCosts(s, s.Work.Rows()...) }

func streamTierCosts(s *Snapshot, streams ...string) TierCosts {
	t := TierCosts{Tiers: map[string]int{}, PerLanded: "-", SpendPerLanded: "-", SpendScope: SpendPerLandedScope,
		CostByTier: map[string]string{}, ReadsToday: map[string]ReadDay{}}
	byTier := map[string]*big.Rat{}
	workCost, readCost := new(big.Rat), new(big.Rat)
	pricedWork, pricedRead := false, false
	day := s.Now.UTC().Format(time.DateOnly)
	var landedCost []string
	var allCost []string
	for _, stream := range streams {
		for _, col := range States {
			for _, c := range s.Work.Cell(stream, col) {
				if IsSentinel(c) {
					continue
				}
				t.Tiers[TierWord(c)]++
				// the card's whole record, whatever its column: every take and read behind it,
				// the records past the list's bound included (FieldCostTotal)
				view := CardCostOf(c)
				tot, cover := view.Total, coverageOf(view)
				t.Coverage = t.Coverage.Add(cover)
				if col == Landed {
					t.Landed++
					t.LandedCoverage = t.LandedCoverage.Add(cover)
					// a landed card counts toward the per-landed figure only priced whole: one
					// with any record unpriced, or with no record at all, is in neither its sum
					// nor its count
					if cover.Records > 0 && cover.Unpriced == 0 {
						t.LandedPriced++
						if v := c.F(FieldCost); v != "" {
							landedCost = append(landedCost, v)
						}
					}
				}
				if tot.Charged != "" {
					allCost = append(allCost, tot.Charged)
				}
				for _, con := range view.Consumers {
					if con.Kind == "read" && con.Usage.Unpriced == WhySubscription {
						// a subscription read's cost is its tokens: priced, never a run unpriced
						t.ReadTokens += con.Usage.Tokens.Total()
					}
					if con.Kind == "read" && con.Usage.Unpriced == cardcost.WhyNoTokens {
						t.ReadsNoTokens++
					}
					if con.Kind == "read" && strings.HasPrefix(con.At, day) {
						addReadDay(t.ReadsToday, con)
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
					tier := cmp.Or(con.Tier, "untiered")
					if byTier[tier] == nil {
						byTier[tier] = new(big.Rat)
					}
					byTier[tier].Add(byTier[tier], usd)
				}
			}
		}
	}
	t.UnpricedRuns = t.Coverage.Unpriced
	switch {
	case t.Landed > 0 && t.LandedPriced == 0:
		t.PerLanded = CostUnknown
	case t.LandedPriced > 0:
		t.PerLanded = perCard(landedCost, t.LandedPriced)
	}
	switch {
	case t.Landed > 0 && (t.Coverage.Unpriced > 0 || t.LandedPriced < t.Landed):
		t.SpendPerLanded = CostUnknown
	case t.Landed > 0:
		t.SpendPerLanded = perCard(allCost, t.Landed)
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
	tiers := make([]string, 0, len(byTier))
	for tier := range byTier {
		tiers = append(tiers, tier)
	}
	sort.Strings(tiers)
	for _, tier := range tiers {
		t.CostByTier[tier] = cardcost.Cents(byTier[tier])
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

// PerLandedOf is a stream's dollars per landed card from the where view's cells alone
// (the cost cell, MoneyText, over the landed count): what the text table shows when the
// tick's record is not there; "-" with nothing landed or no cost.
func PerLandedOf(costCell string, landed int) string {
	if landed <= 0 || len(costCell) < 2 || costCell[0] != '$' {
		return "-"
	}
	r, err := amountOf(costCell[1:])
	if err != nil || r == nil {
		return "-"
	}
	return cardcost.Cents(r.Quo(r, big.NewRat(int64(landed), 1)))
}

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

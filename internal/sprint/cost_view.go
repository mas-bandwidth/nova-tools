package sprint

import (
	"cmp"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
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
	// PerLanded is the stream's landed cards' cost per landed card, dollars and cents
	// rounded up (MoneyText); "-" with nothing landed or nothing priced.
	PerLanded string `json:"per_landed"`
	// CostByTier is the stream's spend by the tier each attempt and read ran on, dollars
	// and cents allocated from the rounded-up total, over every card of the stream, every dollar of TotalCost in one
	// tier: a record with no tier takes its route's (runTier), and a card's records past
	// the list's bound (in its total, not its list) take the card's; "no tier" only when
	// none of these names one.
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
	// Reconciles is the SPRINT's too: each provider's latest reconciliation, its day, its
	// own figure, the records' and the gap (LatestReconciles, cost_reconcile.go).
	Reconciles []CostReconcileRecord `json:"reconciles,omitempty"`
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
	t := TierCosts{Tiers: map[string]int{}, PerLanded: "-", CostByTier: map[string]string{}, ReadsToday: map[string]ReadDay{}, Readers: map[string]ReaderSpend{}}
	byTier := map[string]*big.Rat{}
	workCost, readCost := new(big.Rat), new(big.Rat)
	pricedWork, pricedRead := false, false
	day := s.Now.UTC().Format(time.DateOnly)
	routes := map[string]Route{}
	for _, r := range s.Routes {
		routes[r.Name] = r
	}
	var landedCost []string
	var allCost []string
	landed := 0
	for _, col := range States {
		for _, c := range s.Work.Cell(stream, col) {
			if IsSentinel(c) {
				continue
			}
			t.Tiers[TierWord(c)]++
			if col == Landed {
				landed++
				if v := c.F(FieldCost); v != "" {
					landedCost = append(landedCost, v)
				}
			}
			// the card's whole record, whatever its column: every take and read behind it,
			// the records past the list's bound included (FieldCostTotal)
			tot := CardCostOf(c).Total
			if tot.Charged != "" {
				allCost = append(allCost, tot.Charged)
			}
			t.UnpricedRuns += tot.Records - tot.ChargedOf
			listed := new(big.Rat)
			for _, con := range CardCostOf(c).Consumers {
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
				listed.Add(listed, usd)
				addTier(byTier, runTier(routes, c, con), usd)
			}
			// the records past the list's bound: in the card's total and in no record of its
			// list, so on the card's own tier, that the tiers sum to the total
			if all, err := amountOf(tot.Charged); err == nil && all != nil && all.Cmp(listed) > 0 {
				addTier(byTier, attemptTier(c), new(big.Rat).Sub(all, listed))
			}
		}
	}
	if sum, ok := cardcost.Sum(landedCost...); ok && landed > 0 && len(landedCost) > 0 {
		if total, err := amountOf(sum); err == nil && total != nil {
			t.PerLanded = cardcost.Cents(total.Quo(total, big.NewRat(int64(landed), 1)))
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
	t.CostByTier = tierCostCents(byTier)
	if unrec := UnreconciledSpend(s); unrec > 0 {
		t.Unreconciled = cardcost.Cents(new(big.Rat).SetFloat64(unrec))
	}
	t.Reconciles = LatestReconciles(s)
	return t
}

// tierCostCents partitions the rounded-up stream total into displayed tier cents.
// Whole cents stay with their tier; remaining cents go to the largest fractional
// remainders, with alphabetical ties. Recorded exact amounts are never changed.
func tierCostCents(byTier map[string]*big.Rat) map[string]string {
	type allocation struct {
		tier     string
		whole    *big.Int
		fraction *big.Rat
	}
	tiers := make([]string, 0, len(byTier))
	for tier := range byTier {
		tiers = append(tiers, tier)
	}
	sort.Strings(tiers)
	parts := make([]allocation, 0, len(tiers))
	remainders := new(big.Rat)
	for _, tier := range tiers {
		cents := new(big.Rat).Mul(byTier[tier], big.NewRat(100, 1))
		whole := new(big.Int).Quo(cents.Num(), cents.Denom())
		fraction := new(big.Rat).Sub(cents, new(big.Rat).SetInt(whole))
		remainders.Add(remainders, fraction)
		parts = append(parts, allocation{tier, whole, fraction})
	}
	left := new(big.Int).Quo(remainders.Num(), remainders.Denom()).Int64()
	if !remainders.IsInt() {
		left++
	}
	sort.SliceStable(parts, func(i, j int) bool { return parts[i].fraction.Cmp(parts[j].fraction) > 0 })
	out := make(map[string]string, len(parts))
	for i, part := range parts {
		if int64(i) < left {
			part.whole.Add(part.whole, big.NewInt(1))
		}
		out[part.tier] = cardcost.Cents(new(big.Rat).SetFrac(part.whole, big.NewInt(100)))
	}
	return out
}

// runTier is the tier a record's run is counted under: the tier it recorded, else its
// route's (the route row's tier, else the route name's prefix: pro-*, flash-*, heavy-*,
// frontier-*), else its card attempt's (attemptTier); "no tier" only when none of these names one.
func runTier(routes map[string]Route, c *Card, con Consumer) string {
	if con.Tier != "" {
		return con.Tier
	}
	for _, name := range []string{con.Route, con.Usage.Route} {
		if name == "" {
			continue
		}
		if r, ok := routes[name]; ok && r.Tier != "" {
			return r.Tier
		}
		for _, tier := range cardhdr.Routes {
			if strings.HasPrefix(name, tier+"-") {
				return tier
			}
		}
	}
	return attemptTier(c)
}

// attemptTier is the tier the card's attempt is on as the card records it: the tier its
// last deal drew (FieldTierNow), else the tier pinned on it (FieldTier), else the tier its
// brief names; "no tier" when none does (never cardTier's default ceiling, which would
// name a tier nothing recorded).
func attemptTier(c *Card) string {
	m, _ := cardhdr.ReadModel(c.F("brief"))
	return cmp.Or(c.F(FieldTierNow), c.F(FieldTier), m.Tier, "no tier")
}

// addTier adds usd to the tier's sum.
func addTier(byTier map[string]*big.Rat, tier string, usd *big.Rat) {
	if byTier[tier] == nil {
		byTier[tier] = new(big.Rat)
	}
	byTier[tier].Add(byTier[tier], usd)
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

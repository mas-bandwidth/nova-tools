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

// TierCosts is one stream's tiers and spend as the where view carries them. Every cost
// headline carries its denominators and its coverage (docs/SPEC-SPRINT.md section 1, cost
// visibility): a figure an unpriced record would make smaller reads CostUnknown, never a
// number.
type TierCosts struct {
	// Tiers counts the stream's cards by the tier their briefs name (TierWord).
	Tiers map[string]int `json:"tiers,omitempty"`
	// PerLanded is the stream's cost per landed card priced whole (LandedPriced: a landed
	// card with records, every one priced), their landed costs summed over their count,
	// dollars and cents rounded up (MoneyText). A landed card with any record unpriced is in
	// neither the sum nor the count, so an unpriced completion cannot make the stream read
	// cheaper. "-" with nothing landed, CostUnknown when cards landed and none was priced
	// whole. Its scope is PerLandedScope.
	PerLanded string `json:"per_landed"`
	// Landed and LandedPriced are PerLanded's denominators: every landed card of the stream,
	// and those of them priced whole. LandedCoverage is how the landed cards' records were
	// priced.
	Landed         int      `json:"landed"`
	LandedPriced   int      `json:"landed_priced"`
	LandedCoverage Coverage `json:"landed_coverage"`
	// SpendPerLanded is the stream's spend per verified dev outcome (a card landed: read,
	// accepted and merged), in SpendPerLandedScope: TotalCost, every recorded take and read
	// of every card of the stream in any column with the cards it dropped or re-cut
	// (Dropped), over Landed. CostUnknown while any of those records is unpriced
	// (Coverage.Unpriced), for an unpriced run would read as free; "-" with nothing landed.
	SpendPerLanded string `json:"spend_per_landed"`
	// Coverage is how every record behind TotalCost was priced, the dropped cards' included:
	// what TotalCost holds (actual, estimated), what bills tokens and no dollar
	// (subscription reads), and what it cannot hold (unpriced, the unknown spend).
	Coverage Coverage `json:"coverage"`
	// Dropped is the spend of the stream's cards taken off the table (dropped, or re-cut and
	// so replaced by a twin): in TotalCost, Coverage and SpendPerLanded, so that hiding a
	// card's spend never improves the stream's figures. It is the control card's record
	// (DroppedSpendFields) once that record names any card, and otherwise the cards the
	// snapshot still holds off the table, attributed by the stream field. The two are never
	// added, so a card still in the snapshot and also written on the control card counts once.
	Dropped DroppedSpend `json:"dropped,omitzero"`
	// CostByTier is the stream's spend by the tier each attempt and read ran on, dollars
	// and cents rounded up, over every card of the stream on the table; a record with no
	// tier is "untiered".
	CostByTier map[string]string `json:"cost_by_tier,omitempty"`
	// UnpricedByTier counts, by the tier each ran on, the records CostByTier cannot hold
	// (no dollar charged, a subscription read's aside): a tier's spend with any is at least
	// its figure, so an unpriced run never makes a tier read cheaper unseen.
	UnpricedByTier map[string]int `json:"unpriced_by_tier,omitempty"`
	// TotalCost is the stream's complete recorded spend: every take and read of every card
	// of it in any column, landed or not, and of every card it dropped or re-cut (Dropped),
	// at each record's charged figure (the harness's cost, else its tokens at the route's
	// prices), dollars and cents rounded up; "" when nothing of it was priced. It holds no
	// unpriced record (Coverage.Unpriced): with any, the spend is at least TotalCost and the
	// rest is unknown.
	TotalCost string `json:"total_cost,omitempty"`
	// WorkCost and ReadCost split TotalCost by kind over the cards on the table: every
	// take's charged figure and every read's, dollars and cents rounded up; "" when nothing
	// of that kind was priced. The dashboard shows the reads as their own number beside the
	// work. The dropped cards' spend is in TotalCost alone (Dropped.Cost), unsplit.
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
	// (Coverage.Unpriced, the dropped cards' included).
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

// CostUnknown is a cost headline whose records are not all priced: unknown spend shown as
// unknown, never as the figure an unpriced run made smaller.
const CostUnknown = "unknown"

// The scopes of the two per-card figures. Each names the delivery stage it counts (a card
// landed) and the spend it holds; a figure is compared only with the same figure of another
// stream or of the sprint, never with the other one.
const (
	PerLandedScope      = "landed cards priced whole: each one's own takes and reads, over their count"
	SpendPerLandedScope = "every recorded take and read of the stream's cards, any column, dropped and re-cut ones included, over its landed cards"
)

// Coverage is how a cost headline's records were priced: every record behind it (Records),
// those charged at a cost the harness reported (Actual), at their tokens times the route's
// price sheet (Estimated), subscription reads that bill tokens and no dollar (Tokens), and
// those with no cost at all (Unpriced), the spend the headline cannot hold. The four parts
// sum to Records.
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

// Text is the coverage as a headline's sub-line prints it: "12 actual · 3 estimated · 0
// tokens · 2 unpriced of 17 records".
func (c Coverage) Text() string {
	return fmt.Sprintf("%d actual · %d estimated · %d tokens · %d unpriced of %d records", c.Actual, c.Estimated, c.Tokens, c.Unpriced, c.Records)
}

// String is the coverage as a control card keeps it: "records=17 actual=12 estimated=3
// tokens=0 unpriced=2" (parseCoverage).
func (c Coverage) String() string {
	return fmt.Sprintf("records=%d actual=%d estimated=%d tokens=%d unpriced=%d", c.Records, c.Actual, c.Estimated, c.Tokens, c.Unpriced)
}

// parseCoverage reads Coverage.String back; a word it does not know is left, a count it
// cannot read is 0.
func parseCoverage(line string) Coverage {
	var c Coverage
	for _, w := range strings.Fields(line) {
		k, v, _ := strings.Cut(w, "=")
		n := atoiOr(v)
		switch k {
		case "records":
			c.Records = n
		case "actual":
			c.Actual = n
		case "estimated":
			c.Estimated = n
		case "tokens":
			c.Tokens = n
		case "unpriced":
			c.Unpriced = n
		}
	}
	return c
}

// CoverageOf is a primary's records' coverage, from its total (every record, the ones past
// the list's bound included) and its list (the subscription reads, which the total counts
// as charged nothing).
func CoverageOf(v CardCostView) Coverage {
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

// The fields a stream's control card keeps its dropped cards' spend in (DroppedSpendFields).
const (
	FieldDroppedCost  = "dropped_cost"  // the exact decimal sum of their charged figures, absent when none was priced
	FieldDroppedCover = "dropped_cover" // their records' Coverage.String
	FieldDroppedCards = "dropped_cards" // how many dropped cards carried a record
)

// DroppedSpend is the spend of a stream's cards taken off the table, as its control card
// keeps it: how many cards with a record left, their charged figures summed (exact), and
// how their records were priced.
type DroppedSpend struct {
	Cards    int      `json:"cards,omitempty"`
	Cost     string   `json:"cost,omitempty"`
	Coverage Coverage `json:"coverage,omitzero"`
}

// DroppedSpendOf is the dropped spend the stream's control card keeps.
func DroppedSpendOf(ctl *Card) DroppedSpend {
	return DroppedSpend{Cards: ctl.Int(FieldDroppedCards), Cost: ctl.F(FieldDroppedCost), Coverage: parseCoverage(ctl.F(FieldDroppedCover))}
}

// DroppedSpendFields is the control card's dropped spend with the cards' added: the record
// a step can write as primaries leave the table, so their spend stays the stream's after
// the snapshot no longer holds them. A card with no record adds nothing; nil when none has
// one. While the control card records no card, streamTierCosts counts the same spend from
// the cards the snapshot still holds unplaced (unplacedSpend).
func DroppedSpendFields(ctl *Card, cards []*Card) map[string]string {
	d := DroppedSpendOf(ctl)
	added := false
	for _, c := range cards {
		v := CardCostOf(c)
		if v.Total.Records == 0 {
			continue
		}
		added = true
		d.Cards++
		d.Coverage = d.Coverage.Add(CoverageOf(v))
		if v.Total.Charged != "" {
			if sum, ok := cardcost.Sum(d.Cost, v.Total.Charged); ok {
				d.Cost = sum
			}
		}
	}
	if !added {
		return nil
	}
	set := map[string]string{FieldDroppedCards: itoa(d.Cards), FieldDroppedCover: d.Coverage.String()}
	if d.Cost != "" {
		set[FieldDroppedCost] = d.Cost
	}
	return set
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

// SprintTierCosts is the sprint's TierCosts: every stream of the work table counted as one,
// so its sums and its denominators are exact, never added up from rounded cells, and its
// figures are at the stages and in the scopes a stream's are.
func SprintTierCosts(s *Snapshot) TierCosts { return streamTierCosts(s, s.Work.Rows()...) }

// unplacedSpend is the spend of the stream's cards the snapshot still holds off the table,
// attributed by the stream field a primary keeps after its place is cleared. A card with no
// record adds nothing. It is not split into a tier or a kind: the same fold as the control
// card's dropped spend.
func unplacedSpend(s *Snapshot, stream string) DroppedSpend {
	if s == nil || s.Work == nil || stream == "" {
		return DroppedSpend{}
	}
	var d DroppedSpend
	var costs []string
	for _, c := range s.Work.Cards() {
		if c.Placed() || c.F("stream") != stream || IsSentinel(c) {
			continue
		}
		v := CardCostOf(c)
		if v.Total.Records == 0 {
			continue
		}
		d.Cards++
		d.Coverage = d.Coverage.Add(CoverageOf(v))
		if v.Total.Charged != "" {
			costs = append(costs, v.Total.Charged)
		}
	}
	if sum, ok := cardcost.Sum(costs...); ok && len(costs) > 0 {
		d.Cost = sum
	}
	return d
}

// perCard is the exact usd over n cards, dollars and cents rounded up; "-" for no card.
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

func streamTierCosts(s *Snapshot, streams ...string) TierCosts {
	t := TierCosts{Tiers: map[string]int{}, PerLanded: "-", SpendPerLanded: "-", CostByTier: map[string]string{},
		UnpricedByTier: map[string]int{}, ReadsToday: map[string]ReadDay{}, Readers: map[string]ReaderSpend{}}
	byTier := map[string]*big.Rat{}
	workCost, readCost := new(big.Rat), new(big.Rat)
	pricedWork, pricedRead := false, false
	day := s.Now.UTC().Format(time.DateOnly)
	var landedCost []string
	var allCost []string
	for _, stream := range streams {
		// off the table, still the stream's: the control card once it names any card, else
		// the cards this snapshot still holds unplaced. Never both.
		d := DroppedSpendOf(s.StreamCtl(stream))
		if d.Cards == 0 {
			d = unplacedSpend(s, stream)
		}
		t.Dropped.Cards += d.Cards
		t.Dropped.Coverage = t.Dropped.Coverage.Add(d.Coverage)
		if d.Cost != "" {
			if sum, ok := cardcost.Sum(t.Dropped.Cost, d.Cost); ok {
				t.Dropped.Cost = sum
			}
			allCost = append(allCost, d.Cost)
		}
		for _, col := range States {
			for _, c := range s.Work.Cell(stream, col) {
				if IsSentinel(c) {
					continue
				}
				t.Tiers[TierWord(c)]++
				// the card's whole record, whatever its column: every take and read behind it,
				// the records past the list's bound included (FieldCostTotal)
				view := CardCostOf(c)
				tot, cover := view.Total, CoverageOf(view)
				t.Coverage = t.Coverage.Add(cover)
				if col == Landed {
					t.Landed++
					t.LandedCoverage = t.LandedCoverage.Add(cover)
					// priced whole, or in neither the sum nor the count of PerLanded
					if v := c.F(FieldCost); v != "" && cover.Records > 0 && cover.Unpriced == 0 {
						t.LandedPriced++
						landedCost = append(landedCost, v)
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
					if con.Kind == "read" {
						rd := t.Readers[cmp.Or(con.Who, "-")]
						rd.addRead(con, s.Now)
						t.Readers[cmp.Or(con.Who, "-")] = rd
					}
					tier := cmp.Or(con.Tier, "untiered")
					usd, err := amountOf(cmp.Or(con.Usage.Actual, con.Usage.Predicted))
					if err != nil || usd == nil {
						if con.Usage.Unpriced != WhySubscription {
							t.UnpricedByTier[tier]++
						}
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
	}
	t.Coverage = t.Coverage.Add(t.Dropped.Coverage)
	t.UnpricedRuns = t.Coverage.Unpriced
	switch {
	case t.LandedPriced > 0:
		t.PerLanded = perCard(landedCost, t.LandedPriced)
	case t.Landed > 0:
		t.PerLanded = CostUnknown
	}
	switch {
	case t.Landed > 0 && t.Coverage.Unpriced > 0:
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
	if len(t.UnpricedByTier) == 0 {
		t.UnpricedByTier = nil
	}
	if len(t.Readers) == 0 {
		t.Readers = nil
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
// tokens, how many reads, and how many of them no dollar was charged for (Unpriced: a
// subscription read's tokens are its cost; any other is spend the dollars do not hold).
type ReadDay struct {
	USD      string `json:"usd,omitempty"`
	Tokens   int64  `json:"tokens,omitempty"`
	Reads    int    `json:"reads"`
	Unpriced int    `json:"unpriced,omitempty"`
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
	} else {
		d.Unpriced++
	}
	days[route] = d
}

// ReadSpendLine is the day's read spend per route over the streams' records, one line
// under the where view's summary: "reads today: pro-a $1.24 12 reads 3456789 tokens ·
// subscription tokens 3 reads 120000 tokens", routes in name order, a priced route's
// dollars rounded up to the cent, a route with nothing priced "unpriced"; a route with
// some reads charged no dollar says how many ("pro-a $1.24 12 reads (2 unpriced) ..."),
// so its dollars never read as every read's; "" when no read ended today.
func ReadSpendLine(streams map[string]TierCosts) string {
	days := map[string]ReadDay{}
	for _, tc := range streams {
		for route, d := range tc.ReadsToday {
			all := days[route]
			all.Reads += d.Reads
			all.Tokens += d.Tokens
			all.Unpriced += d.Unpriced
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
		unpriced := ""
		if r != WhySubscription && d.USD != "" && d.Unpriced > 0 {
			unpriced = fmt.Sprintf(" (%d unpriced)", d.Unpriced)
		}
		parts = append(parts, fmt.Sprintf("%s %s %d reads%s %d tokens", r, cost, d.Reads, unpriced, d.Tokens))
	}
	return "reads today: " + strings.Join(parts, " · ")
}

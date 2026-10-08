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
// complete dollars per landed card, and each stream's spend split by the tier its
// attempts ran on (the cost records' tier, not the card's ceiling: a flash card
// escalated to pro shows both).

// CostParts is the complete recorded spend in four parts, dollars and cents rounded
// up (cardcost.Cents): work attempts, reads, the lander's run, and runs that ended
// with no result. A part that priced nothing is "". TotalCost is those four summed
// exactly, then rounded.
type CostParts struct {
	CostWork       string `json:"cost_work,omitempty"`
	CostReads      string `json:"cost_reads,omitempty"`
	CostLand       string `json:"cost_land,omitempty"`
	CostUnanswered string `json:"cost_unanswered,omitempty"`
	TotalCost      string `json:"total_cost,omitempty"`
}

// TierCosts is one stream's tiers and spend as the where view carries them.
type TierCosts struct {
	// Tiers counts the stream's cards by the tier their briefs name (TierWord).
	Tiers map[string]int `json:"tiers,omitempty"`
	// PerLanded is the stream's complete recorded spend per landed card (a card landed:
	// read, accepted and merged), in PerLandedScope: TotalCost, every recorded take, read,
	// landing and unanswered run of every card of the stream in any column with the cards
	// it dropped or re-cut (Dropped), over Landed, dollars and cents rounded up. It is not
	// the landed cell, which is the work written at land. CostUnknown while any of those
	// records is unpriced (Coverage.Unpriced): an unpriced run would read as free, and the
	// figure would read smaller than the spend; "-" with nothing landed or no record at all.
	PerLanded string `json:"per_landed"`
	// Landed is PerLanded's denominator: every landed card of the stream.
	Landed int `json:"landed"`
	// Coverage is how every record behind TotalCost was priced, the dropped cards' included:
	// what TotalCost holds (actual, estimated), what bills tokens and no dollar
	// (subscription reads), and what it cannot hold (unpriced, the unknown spend).
	Coverage Coverage `json:"coverage"`
	// Dropped is the spend of the stream's cards taken off the table (dropped, or re-cut and
	// so replaced by a twin): in TotalCost, Coverage and PerLanded, so that hiding a
	// card's spend never improves the stream's figures. It is the control card's record
	// (DroppedSpendFields) once that record names any card, and otherwise the cards the
	// snapshot still holds off the table, attributed by the stream field. The two are never
	// added, so a card still in the snapshot and also written on the control card counts once.
	Dropped DroppedSpend `json:"dropped,omitzero"`
	// CostByTier is the stream's spend by the tier each attempt and read ran on, dollars
	// and cents allocated from the rounded-up total, over every card of the stream, every dollar of TotalCost in one
	// tier: a record with no tier takes its route's (runTier), and a card's records past
	// the list's bound (in its total, not its list) take the card's; "no tier" only when
	// none of these names one. Spend kept after a drop or a re-cut has no run tier; it is
	// the tier "lineage", so the displayed tiers still sum to TotalCost.
	CostByTier map[string]string `json:"cost_by_tier,omitempty"`
	// UnpricedByTier counts, by the tier each ran on (runTier), the records CostByTier cannot
	// hold (no dollar charged, a subscription read's aside): a tier's spend with any is at
	// least its figure, so an unpriced run never makes a tier read cheaper unseen.
	UnpricedByTier map[string]int `json:"unpriced_by_tier,omitempty"`
	// TotalCost is the stream's complete recorded spend: every take and read of every card
	// of it in any column, landed or not, and of every card it dropped or re-cut (Dropped),
	// at each record's charged figure (the harness's cost, else its tokens at the route's
	// prices), dollars and cents rounded up; "" when nothing of it was priced. It holds no
	// unpriced record (Coverage.Unpriced): with any, the spend is at least TotalCost and the
	// rest is unknown.
	TotalCost string `json:"total_cost,omitempty"`
	// WorkCost and ReadCost split TotalCost by kind: every take's charged figure and every
	// read's, dollars and cents rounded up; "" when nothing of that kind was priced. A
	// no-result run and a lander's run stay in WorkCost, because their kind is not read.
	// The dashboard shows the reads as their own number beside the work.
	WorkCost string `json:"work_cost,omitempty"`
	ReadCost string `json:"read_cost,omitempty"`
	// CostWork, CostReads, CostLand and CostUnanswered are the complete spend in four
	// parts (CostParts): a read, a lander's run (kind land), a run whose end begins
	// "no result", and every other priced run. A record past the list's bound stays in
	// TotalCost and is counted with CostWork, on the card attempt's tier, and so is the
	// dropped cards' spend (Dropped), which keeps no run of its own: the four parts sum to
	// TotalCost. "" when that part priced nothing.
	CostWork       string `json:"cost_work,omitempty"`
	CostReads      string `json:"cost_reads,omitempty"`
	CostLand       string `json:"cost_land,omitempty"`
	CostUnanswered string `json:"cost_unanswered,omitempty"`
	// ReadTokens is the tokens of the stream's subscription reads (WhySubscription), the
	// reads whose cost is their tokens; 0 when none.
	ReadTokens int64 `json:"read_tokens,omitempty"`
	// ReadsToday is the stream's reads that ended on the tick's UTC day, by the route that
	// priced them (WhySubscription for a subscription reader's, "-" for none): the exact
	// dollars, the tokens and the count, which ReadSpendLine sums over the streams.
	ReadsToday map[string]ReadDay `json:"reads_today,omitempty"`
	// UnpricedRuns counts the stream's records that carry no cost at all: runs whose usage
	// never reached the sprint (cardcost.WhyNoTokens), which the total cannot hold
	// (Coverage.Unpriced, the dropped cards' included). A subscription read's tokens are not
	// one of them.
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

// CostUnknown is a cost headline whose records are not all priced: unknown spend shown as
// unknown, never as the figure an unpriced run made smaller.
const CostUnknown = "unknown"

// PerLandedScope is the one per-card figure's scope: the delivery stage it counts (a card
// landed) and the spend it holds (the complete spend, TotalCost's). It is not the landed
// cell over the landed count (PerLandedOf), which is the work written at land.
const PerLandedScope = "every recorded take, read, landing and unanswered run of the stream's cards, any column, dropped and re-cut ones included, over its landed cards"

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
	streams, _ := CostSplits(s)
	return streams
}

// CostSplits is each stream's TierCosts and, beside it, the same four-part spend
// gathered by the tier the record ran on (every stream together). A record past the
// list's bound counts with the work, on the card attempt's tier; a dropped card's spend
// with the work, on the tier "lineage".
func CostSplits(s *Snapshot) (map[string]TierCosts, map[string]CostParts) {
	streams, out := map[string]TierCosts{}, map[string]CostParts{}
	if s == nil || s.Work == nil {
		return streams, out
	}
	fold := &costFold{tiers: map[string]*partSums{}}
	for _, st := range s.Work.Rows() {
		streams[st] = streamTierCosts(s, fold, st)
	}
	for name, p := range fold.tiers {
		out[name] = p.parts()
	}
	return streams, out
}

// SprintTierCosts is the sprint's TierCosts: every stream of the work table counted as one,
// so its sums and its denominators are exact, never added up from rounded cells, and its
// figures (the four parts, the total, per landed) are at the stages and in the scopes a
// stream's are.
func SprintTierCosts(s *Snapshot) TierCosts {
	if s == nil || s.Work == nil {
		return streamTierCosts(nil, nil)
	}
	return streamTierCosts(s, nil, s.Work.Rows()...)
}

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

// streamTierCosts counts the streams as one TierCosts; fold, when not nil, gathers the four
// parts by tier across the calls (CostSplits).
func streamTierCosts(s *Snapshot, fold *costFold, streams ...string) TierCosts {
	t := TierCosts{Tiers: map[string]int{}, PerLanded: "-", CostByTier: map[string]string{},
		UnpricedByTier: map[string]int{}, ReadsToday: map[string]ReadDay{}, Readers: map[string]ReaderSpend{}}
	if s == nil || s.Work == nil {
		return t
	}
	byTier := map[string]*big.Rat{}
	workCost, readCost := new(big.Rat), new(big.Rat)
	pricedWork, pricedRead := false, false
	parts := newPartSums()
	day := s.Now.UTC().Format(time.DateOnly)
	routes := map[string]Route{}
	for _, r := range s.Routes {
		routes[r.Name] = r
	}
	var allCost []string
	for _, stream := range streams {
		// off the table, still the stream's: the control card once it names any card, else
		// the cards this snapshot still holds unplaced. Never both. Merge nil has no control card.
		d := DroppedSpend{}
		if s.Merge != nil {
			d = DroppedSpendOf(s.StreamCtl(stream))
		}
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
			// no run tier remains once the card has left: the tier "lineage", so the displayed
			// tiers still sum to TotalCost, and no run kind: the four parts count it with the
			// work, so they sum to TotalCost too. Work and read stay the on-table listed consumers.
			if usd, err := amountOf(d.Cost); err == nil && usd != nil {
				addTier(byTier, "lineage", usd)
				parts.add(0, usd)
				fold.addPart("lineage", 0, usd)
			}
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
				}
				if tot.Charged != "" {
					allCost = append(allCost, tot.Charged)
				}
				listed := new(big.Rat)
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
					tier := runTier(routes, c, con)
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
					listed.Add(listed, usd)
					addTier(byTier, tier, usd)
					part := costPart(con)
					parts.add(part, usd)
					fold.addPart(tier, part, usd)
				}
				// the records past the list's bound: in the card's total and in no record of its
				// list, so on the card's own tier, that the tiers sum to the total. The four
				// parts count that remainder with the work.
				if all, err := amountOf(tot.Charged); err == nil && all != nil && all.Cmp(listed) > 0 {
					rem := new(big.Rat).Sub(all, listed)
					addTier(byTier, attemptTier(c), rem)
					parts.add(0, rem)
					fold.addPart(attemptTier(c), 0, rem)
				}
			}
		}
	}
	t.Coverage = t.Coverage.Add(t.Dropped.Coverage)
	t.UnpricedRuns = t.Coverage.Unpriced
	// the complete spend over the landed cards: unknown while any record is unpriced, "-"
	// with nothing landed or no record priced
	switch {
	case t.Landed == 0:
	case t.Coverage.Unpriced > 0:
		t.PerLanded = CostUnknown
	case len(allCost) > 0:
		t.PerLanded = perCard(allCost, t.Landed)
	}
	if sum, ok := cardcost.Sum(allCost...); ok && len(allCost) > 0 {
		if total, err := amountOf(sum); err == nil && total != nil {
			t.TotalCost = cardcost.Cents(total)
		}
	}
	t.CostWork, t.CostReads, t.CostLand, t.CostUnanswered = parts.money()
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
	t.CostByTier = tierCostCents(byTier)
	if unrec := UnreconciledSpend(s); unrec > 0 {
		t.Unreconciled = cardcost.Cents(new(big.Rat).SetFloat64(unrec))
	}
	t.Reconciles = LatestReconciles(s)
	return t
}

// costFold gathers the four-part spend by tier across streams while each is counted.
type costFold struct {
	tiers map[string]*partSums
}

// addPart adds usd to the tier's part; a nil fold gathers nothing.
func (f *costFold) addPart(tier string, part int, usd *big.Rat) {
	if f == nil {
		return
	}
	p := f.tiers[tier]
	if p == nil {
		p = newPartSums()
		f.tiers[tier] = p
	}
	p.add(part, usd)
}

// partSums is one complete cost in four parts, exact, before the cent rounding.
// 0 is work, 1 reads, 2 the lander's run, 3 a run that ended with no result.
type partSums struct {
	r   [4]*big.Rat
	saw [4]bool
}

func newPartSums() *partSums {
	p := &partSums{}
	for i := range p.r {
		p.r[i] = new(big.Rat)
	}
	return p
}

// costPart is which of the four parts a consumer's charged figure belongs to.
// A read is reads, a lander's run is landing, an end that begins "no result" is
// unanswered, and every other priced run is work. Kind wins over the end, so a
// read or a landing that ended with no result stays in its own part.
func costPart(con Consumer) int {
	switch {
	case con.Kind == "read":
		return 1
	case con.Kind == "land":
		return 2
	case strings.HasPrefix(con.End, cardhdr.EndNoResult):
		return 3
	default:
		return 0
	}
}

func (p *partSums) add(part int, usd *big.Rat) {
	p.r[part].Add(p.r[part], usd)
	p.saw[part] = true
}

// money is the four parts as the table shows them; a part that priced nothing is "".
func (p *partSums) money() (work, reads, land, unanswered string) {
	out := [4]string{}
	for i := range p.r {
		if p.saw[i] {
			out[i] = cardcost.Cents(p.r[i])
		}
	}
	return out[0], out[1], out[2], out[3]
}

// parts is the four parts and their exact sum, rounded up to the cent.
func (p *partSums) parts() CostParts {
	w, r, l, u := p.money()
	sum := new(big.Rat)
	any := false
	for i := range p.r {
		if p.saw[i] {
			any = true
			sum.Add(sum, p.r[i])
		}
	}
	total := ""
	if any {
		total = cardcost.Cents(sum)
	}
	return CostParts{CostWork: w, CostReads: r, CostLand: l, CostUnanswered: u, TotalCost: total}
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

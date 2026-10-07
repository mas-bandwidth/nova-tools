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
// exactly, then rounded; on a stream the where record's total_cost is the cards'
// charged totals, which is the same sum once a cut record is counted with the work.
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
	// PerLanded is the stream's complete recorded spend over its landed cards
	// (work, reads, landing, unanswered, every card of the stream), dollars and
	// cents rounded up; "-" with nothing landed or nothing priced. It is not the
	// landed cell, which is the work written at land.
	PerLanded string `json:"per_landed"`
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
	// read's, dollars and cents rounded up; "" when nothing of that kind was priced. A
	// no-result run and a lander's run stay in WorkCost, because their kind is not read.
	// The dashboard shows the reads as their own number beside the work.
	WorkCost string `json:"work_cost,omitempty"`
	ReadCost string `json:"read_cost,omitempty"`
	// CostWork, CostReads, CostLand and CostUnanswered are the complete spend in four
	// parts (CostParts): a read, a lander's run (kind land), a run whose end begins
	// "no result", and every other priced run. A record past the list's bound stays in
	// TotalCost and is counted with CostWork. "" when that part priced nothing.
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
	streams, _ := CostSplits(s)
	return streams
}

// CostSplits is each stream's TierCosts and, beside it, the same four-part spend
// gathered by the tier the record ran on (every stream together). A record with no
// tier, and a record past the list's bound, counts with the records that name none.
func CostSplits(s *Snapshot) (map[string]TierCosts, map[string]CostParts) {
	streams := map[string]TierCosts{}
	tiers := map[string]*partSums{}
	for _, st := range s.Work.Rows() {
		streams[st] = streamTierCosts(s, st, tiers)
	}
	out := map[string]CostParts{}
	for name, p := range tiers {
		out[name] = p.parts()
	}
	return streams, out
}

func streamTierCosts(s *Snapshot, stream string, tiers map[string]*partSums) TierCosts {
	t := TierCosts{Tiers: map[string]int{}, PerLanded: "-", CostByTier: map[string]string{}, ReadsToday: map[string]ReadDay{}, Readers: map[string]ReaderSpend{}}
	byTier := map[string]*big.Rat{}
	workCost, readCost := new(big.Rat), new(big.Rat)
	pricedWork, pricedRead := false, false
	parts := newPartSums()
	blankTier := "untiered" // a record that names no tier, the bucket CostByTier already uses
	day := s.Now.UTC().Format(time.DateOnly)
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
			}
			// the card's whole record, whatever its column: every take and read behind it,
			// the records past the list's bound included (the charged total)
			cc := CardCostOf(c)
			tot := cc.Total
			if tot.Charged != "" {
				allCost = append(allCost, tot.Charged)
			}
			t.UnpricedRuns += tot.Records - tot.ChargedOf
			listed := new(big.Rat)
			for _, con := range cc.Consumers {
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
				listed.Add(listed, usd)
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
				tier := cmp.Or(con.Tier, blankTier)
				if byTier[tier] == nil {
					byTier[tier] = new(big.Rat)
				}
				byTier[tier].Add(byTier[tier], usd)
				part := costPart(con)
				parts.add(part, usd)
				addTier(tiers, tier, part, usd)
			}
			// a record past the list's bound is in the charged total and not in the list:
			// it counts with the work, so the four parts are the total
			if cardTotal, err := amountOf(tot.Charged); err == nil && cardTotal != nil {
				rem := new(big.Rat).Sub(cardTotal, listed)
				if rem.Sign() > 0 {
					parts.add(0, rem)
					addTier(tiers, blankTier, 0, rem)
				}
			}
		}
	}
	if sum, ok := cardcost.Sum(allCost...); ok && len(allCost) > 0 {
		if total, err := amountOf(sum); err == nil && total != nil {
			t.TotalCost = cardcost.Cents(total)
			if landed > 0 {
				per := new(big.Rat).Quo(new(big.Rat).Set(total), big.NewRat(int64(landed), 1))
				t.PerLanded = cardcost.Cents(per)
			}
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
	if len(t.Readers) == 0 {
		t.Readers = nil
	}
	tierNames := make([]string, 0, len(byTier))
	for tier := range byTier {
		tierNames = append(tierNames, tier)
	}
	sort.Strings(tierNames)
	for _, tier := range tierNames {
		t.CostByTier[tier] = cardcost.Cents(byTier[tier])
	}
	if unrec := UnreconciledSpend(s); unrec > 0 {
		t.Unreconciled = cardcost.Cents(new(big.Rat).SetFloat64(unrec))
	}
	return t
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

func addTier(tiers map[string]*partSums, tier string, part int, usd *big.Rat) {
	if tiers == nil {
		return
	}
	p := tiers[tier]
	if p == nil {
		p = newPartSums()
		tiers[tier] = p
	}
	p.add(part, usd)
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

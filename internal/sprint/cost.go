package sprint

import (
	"cmp"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// What a card cost (docs/SPEC-SPRINT.md, "What a card cost"; the owner, 2026-10-01:
// "the producer card by the time it gets to landed, should have the history of
// consumer cards that did work for it ... how much each consumer card cost, total
// for producer"; and "The cost needs to be tracked IN THE CARD"). A producer card's
// consumers are its work cards' takes (each run on a fleet member) and its read
// cards' runs (each read by a reader). When a consumer ends, the step that ends it
// writes its usage record (cardcost.Usage: the tokens by class its member or reader
// reported, its waiting and running time, the predicted cost under the price sheet of
// the route that served it with the sheet copied beside it, the harness's own cost
// when it reported one) on the consumer card as before, and appends the consumer's
// record to the primary with the primary's total (addConsumer). The card's history,
// its totals and its landed cost are read from the primary alone (CardCostOf). It
// adds no column, line or row to any table: the records are fields of the cards.

// costRecord is a consumer's usage record at its end: what was reported (usage),
// timed from the stamp it was dealt or asked (from) through the one it was taken or
// begun (began) to now, and priced. A work card is priced by its route (route, by
// name; a pinned or unrouted card by the model it ran), a read by an enabled route of
// the provider/model its harness reported (onlyEnabled). A run that reported no token
// is not priced.
func costRecord(s *Snapshot, usage, route, model string, onlyEnabled bool, from, began string) string {
	u := cardcost.ParseUsage(usage).Timed(from, began, s.Now)
	if !u.Tokens.Reported() {
		u.Unpriced = cardcost.WhyNoTokens
		return u.String()
	}
	r, ok := s.priceRoute(route, cmp.Or(u.Model, model), onlyEnabled)
	if !ok {
		return u.Priced("", cardcost.Prices{}).String()
	}
	return u.Priced(r.Name, r.Prices).String()
}

// priceRoute is the route whose price sheet prices a run: the route of that name when
// the store still holds it, else the first route in name order that runs the model
// (provider/model), an enabled one when onlyEnabled.
func (s *Snapshot) priceRoute(name, model string, onlyEnabled bool) (Route, bool) {
	if name != "" && name != RoutePin {
		for _, r := range s.Routes {
			if r.Name == name {
				return r, true
			}
		}
	}
	for _, r := range s.Routes {
		if r.Provider+"/"+r.Model == model && (r.Enabled || !onlyEnabled) {
			return r, true
		}
	}
	return Route{}, false
}

// FieldReadTake is the record of one run of a read card that ended without a verdict
// (read --return), keyed by the run's number, 1 for the first, kept on the read card
// beside its usage (the run that gave the verdict); the producer's record of each run
// is on the primary (FieldCostRecord), keyed by the same number.
const FieldReadTake = "read_take_"

// MaxTakes bounds the numbered records of one card a reader looks for.
const MaxTakes = 64

// nextTake is the number the card's next record under prefix takes: one past the
// last it holds.
func nextTake(c *Card, prefix string) int {
	n := 1
	for n <= MaxTakes && c.F(prefix+itoa(n)) != "" {
		n++
	}
	return n
}

// The producer card carries what it cost (the owner, 2026-10-01: "The cost needs to
// be tracked IN THE CARD"): each consumer that ends appends one record to the primary,
// FieldCostRecord plus the consumer's key, in the same step that ends it, and the
// primary's running total (FieldCostTotal) is updated with it. Everything that shows
// or sums a card's cost reads the primary alone (CardCostOf), so no reader or member
// removed, no read card retired and no consumer record cleaned up can lose cost.
const (
	FieldCostRecord = "cost_record:" // + the consumer's key (Consumer.Key)
	FieldCostTotal  = "cost_total"   // cardcost.Total.String over every record
	FieldCostCut    = "cost_cut"     // how many records past MaxCostRecords were left out of the list
	// MaxCostRecords bounds the history on one card. A card's takes and reads are few;
	// past the bound a record is still added to the total, exactly, and counted in
	// FieldCostCut, and `card <id>` says the list was cut.
	MaxCostRecords = 64
)

// Consumer is one consumer of a producer card: a take of a work card or a run of a
// read card, who ran it (the member or the reader), on what route and model, how it
// ended, when, and its cost record.
type Consumer struct {
	Kind    string         `json:"kind"` // work or read
	Card    string         `json:"card"`
	Attempt int            `json:"attempt"`
	Take    int            `json:"take,omitempty"` // a work card's take the provider failed, a read's run returned; 0 for the card's own
	Gen     int            `json:"gen,omitempty"`  // a work card's generation when it ended
	Who     string         `json:"who"`            // the member or the reader
	Route   string         `json:"route,omitempty"`
	Model   string         `json:"model,omitempty"` // provider/model
	End     string         `json:"end"`
	At      string         `json:"at"`  // when it ended
	Key     string         `json:"key"` // the card and its run: one record per key, set once
	Usage   cardcost.Usage `json:"usage"`
}

// line is the record as the primary keeps it: the consumer's words, then its usage.
func (c Consumer) line() string {
	w := []string{"kind=" + c.Kind, "card=" + c.Card, "attempt=" + itoa(c.Attempt), "take=" + itoa(c.Take), "gen=" + itoa(c.Gen),
		"who=" + orDash(c.Who), "on_route=" + orDash(c.Route), "on_model=" + orDash(c.Model), "end=" + orDash(strings.ReplaceAll(c.End, " ", "-")), "at=" + orDash(c.At)}
	return strings.Join(w, " ") + " " + c.Usage.String()
}

// parseConsumer reads a record (line) back.
func parseConsumer(key, line string) Consumer {
	c := Consumer{Key: key}
	var rest []string
	undash := func(v string) string {
		if v == "-" {
			return ""
		}
		return v
	}
	for _, w := range strings.Fields(line) {
		k, v, _ := strings.Cut(w, "=")
		switch k {
		case "kind":
			c.Kind = v
		case "card":
			c.Card = v
		case "attempt":
			c.Attempt, _ = strconv.Atoi(v)
		case "take":
			c.Take, _ = strconv.Atoi(v)
		case "gen":
			c.Gen, _ = strconv.Atoi(v)
		case "who":
			c.Who = undash(v)
		case "on_route":
			c.Route = undash(v)
		case "on_model":
			c.Model = undash(v)
		case "end":
			c.End = strings.ReplaceAll(undash(v), "-", " ")
		case "at":
			c.At = undash(v)
		default:
			rest = append(rest, w)
		}
	}
	c.Usage = cardcost.ParseUsage(strings.Join(rest, " "))
	return c
}

// addConsumer adds the consumer's record to the primary's changes (set, the fields the
// step writes on it), and the record to the primary's total: once per key, whatever is
// already on the card or in set, so a step planned again never counts it twice.
func addConsumer(pr *Card, set map[string]string, c Consumer) {
	key := FieldCostRecord + c.Key
	if pr.F(key) != "" || set[key] != "" {
		return
	}
	total := cardcost.ParseTotal(cmp.Or(set[FieldCostTotal], pr.F(FieldCostTotal)))
	set[FieldCostTotal] = total.Add(c.Usage).String()
	n := 0
	for k := range pr.Fields {
		if strings.HasPrefix(k, FieldCostRecord) {
			n++
		}
	}
	for k := range set {
		if strings.HasPrefix(k, FieldCostRecord) && pr.F(k) == "" {
			n++
		}
	}
	if n >= MaxCostRecords {
		set[FieldCostCut] = itoa(max(pr.Int(FieldCostCut), atoiOr(set[FieldCostCut])) + 1)
		return
	}
	set[key] = c.line()
}

// atoiOr is a count field's value, 0 when unset.
func atoiOr(v string) int {
	n, _ := strconv.Atoi(v)
	return n
}

// workConsumer is a work card's take as it ends: its route, model, member and
// generation, the end, and the cost record (rec, costRecord's line).
func workConsumer(s *Snapshot, c *Card, take int, end, rec string) Consumer {
	u := cardcost.ParseUsage(rec)
	key := c.ID + "#g" + itoa(c.Int("gen"))
	return Consumer{Kind: "work", Card: c.ID, Attempt: c.Int("attempt"), Take: take, Gen: c.Int("gen"), Who: c.Row,
		Route: c.F(FieldRoute), Model: cmp.Or(u.Model, c.F(FieldModel)), End: end, At: stamp(s.Now), Key: key, Usage: u}
}

// readConsumer is a read card's run as it ends: run is the returned run's number, 0
// for the run that gave the verdict.
func readConsumer(s *Snapshot, c *Card, run int, end, rec string) Consumer {
	u := cardcost.ParseUsage(rec)
	key := c.ID + "#v"
	if run > 0 {
		key = c.ID + "#r" + itoa(run)
	}
	return Consumer{Kind: "read", Card: c.ID, Attempt: c.Int("attempt"), Take: run, Who: c.F("reader"), Route: u.Route, Model: u.Model,
		End: end, At: stamp(s.Now), Key: key, Usage: u}
}

// CardCostView is a producer card's cost: each consumer that ended, the totals, and
// how many records past the bound the list leaves out (Cut; the totals hold them).
type CardCostView struct {
	Consumers []Consumer     `json:"consumers"`
	Total     cardcost.Total `json:"total"`
	Cut       int            `json:"cut,omitempty"`
}

// CardCostOf is the producer's cost as the primary carries it: its records, in the
// order they ended, and its total.
func CardCostOf(pr *Card) CardCostView {
	v := CardCostView{Consumers: []Consumer{}, Total: cardcost.NoTotal()}
	if pr == nil {
		return v
	}
	for k, line := range pr.Fields {
		if key, ok := strings.CutPrefix(k, FieldCostRecord); ok {
			v.Consumers = append(v.Consumers, parseConsumer(key, line))
		}
	}
	sort.Slice(v.Consumers, func(i, j int) bool {
		a, b := v.Consumers[i], v.Consumers[j]
		if a.At != b.At {
			return a.At < b.At
		}
		return a.Key < b.Key
	})
	v.Total = cardcost.ParseTotal(pr.F(FieldCostTotal))
	v.Cut = pr.Int(FieldCostCut)
	return v
}

// takeStamps are the stamps a work card's current take was dealt and taken at: what
// its waiting and running time are measured from, recorded and shown, judging nothing
// (the one deadline is WorkDeadline's).
func takeStamps(c *Card) (dealt, taken string) { return c.F("dealt"), c.F("taken") }

// CostLines are the producer's cost as `card <id>` prints it, from the primary alone:
// one COST line per consumer and the COST TOTAL line (charged_usd: each consumer's
// actual cost where reported, else its predicted one, summed). A count or time not reported is "-", a cost not
// known is "-", never 0.
func (v CardCostView) CostLines() []string {
	var out []string
	for _, c := range v.Consumers {
		u := c.Usage
		take := ""
		if c.Take > 0 {
			take = " take=" + strconv.Itoa(c.Take)
		}
		actualBy := ""
		if u.Actual != "" {
			actualBy = u.ActualBy
		}
		out = append(out, fmt.Sprintf("COST kind=%s card=%s attempt=%d%s who=%s route=%s model=%s end=%s %s wait=%s run=%s predicted_usd=%s actual_usd=%s actual_by=%s cost=%s",
			c.Kind, c.Card, c.Attempt, take, orDash(c.Who), orDash(cmp.Or(c.Route, u.Route)), orDash(c.Model), strings.ReplaceAll(c.End, " ", "-"),
			tokenWords(u.Tokens), seconds(u.Wait), seconds(u.Run), orDash(u.Predicted), orDash(u.Actual), orDash(actualBy), u.Present()))
	}
	t := v.Total
	cut := ""
	if v.Cut > 0 {
		cut = fmt.Sprintf(" cut=%d", v.Cut) // records past the bound: in the totals, not in the list
	}
	out = append(out, fmt.Sprintf("COST TOTAL consumers=%d %s wait=%s run=%s predicted_usd=%s predicted_of=%d/%d actual_usd=%s actual_by=%s actual_of=%d/%d charged_usd=%s%s",
		t.Records, tokenWords(t.Tokens), seconds(t.Wait), seconds(t.Run), orDash(t.Predicted), t.PredOf, t.Records, orDash(t.Actual), orDash(t.ActualBy), t.ActualOf, t.Records,
		orDash(t.Charged), cut))
	return out
}

// tokenWords are the token classes and requests as a COST line prints them.
func tokenWords(t cardcost.Tokens) string {
	n := func(v int64) string {
		if v < 0 {
			return "-"
		}
		return strconv.FormatInt(v, 10)
	}
	return fmt.Sprintf("input=%s cache_read=%s cache_write=%s output=%s reasoning=%s requests=%s",
		n(t.Input), n(t.CacheRead), n(t.CacheWrite), n(t.Output), n(t.Reasoning), n(t.Requests))
}

// seconds is a time in seconds as a COST line prints it: "-" when not known.
func seconds(n int64) string {
	if n < 0 {
		return "-"
	}
	return strconv.FormatInt(n, 10) + "s"
}

// FieldCost is a landed primary's total cost (its total's charged figure, written as
// it lands) and a stream's control card's sum over its landed primaries: an exact decimal in USD, absent when nothing of it was priced.
// The work table's cost column shows the control card's (Cost; SyncMirrors).
const FieldCost = "cost"

// MoneyText is a cost as the work table's cost cell shows it: US dollars and cents,
// rounded up to the next cent ("$1.24" for 1.2345; the owner, 2026-10-01: "For money, I
// never care about anything past 2 decimal places (cents)." / "round up to cents"), "-"
// when there is none. The card keeps the exact figure (FieldCost, FieldCostTotal); only
// what is shown is rounded.
func MoneyText(usd string) string {
	if usd == "" {
		return "-"
	}
	r, ok := new(big.Rat).SetString(usd)
	if !ok {
		return "-"
	}
	return cardcost.Cents(r)
}

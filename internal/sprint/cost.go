package sprint

import (
	"cmp"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// What a card cost (docs/SPEC-SPRINT.md, "What a card cost"; the owner, 2026-10-01:
// "the producer card by the time it gets to landed, should have the history of
// consumer cards that did work for it ... how much each consumer card cost, total
// for producer"). A producer card's consumers are its work cards' takes (each run on
// a fleet member) and its read cards (each read by a reader). Each consumer keeps
// what it cost in its own usage field, one line (cardcost.Usage), written when it
// ends: the tokens by class its member or reader reported, its waiting and running
// time, the predicted cost under the price sheet of the route that served it with
// the sheet copied beside it, and the harness's own cost when it reported one. It
// adds no column, line or row to any table: the record rides in a field the card
// already has. The producer's history and totals are computed when the card is
// printed (CardCost), from its consumers' records.

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
// (read --return), keyed by the run's number, 1 for the first: the card's usage field
// holds only the run that gave the verdict, so a read returned and asked again keeps
// every run it had, and each counts in the producer's total.
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

// Consumer is one consumer of a producer card as `card <id>` prints it: a take of a
// work card or a read card, who ran it (the member or the reader), on what route and
// model, how it ended, and its cost record.
type Consumer struct {
	Kind    string         `json:"kind"` // work or read
	Card    string         `json:"card"`
	Attempt int            `json:"attempt"`
	Take    int            `json:"take,omitempty"` // a work card's take the provider failed, a read's run returned; 0 for the card's own
	Who     string         `json:"who"`            // the member or the reader
	Route   string         `json:"route,omitempty"`
	Model   string         `json:"model,omitempty"` // provider/model
	End     string         `json:"end"`
	Usage   cardcost.Usage `json:"usage"`
}

// CardCostView is a producer card's cost: each consumer that ended, and the totals.
type CardCostView struct {
	Consumers []Consumer     `json:"consumers"`
	Total     cardcost.Total `json:"total"`
}

// CardCost is the producer's cost from its work and read cards' records: for each
// work card, every take the provider failed (ProviderTake), then the card's own take
// when it ended (ok yes or no); for each read card, every run returned without a
// verdict (FieldReadTake), then the run that gave the verdict. A take still running, a launch refused at staging (no child ran) and a card
// withdrawn without a report have no record and are left out. A record that lacks
// its times (written before the record kept them) takes them from the card's stamps.
func CardCost(work, reads []*Card) CardCostView {
	v := CardCostView{Consumers: []Consumer{}}
	for _, w := range work {
		takes, numbers := ProviderTakes(w)
		for i, t := range takes {
			u := cardcost.ParseUsage(t.Usage)
			v.Consumers = append(v.Consumers, Consumer{Kind: "work", Card: w.ID, Attempt: w.Int("attempt"), Take: numbers[i], Who: t.Member,
				Route: t.Route, Model: cmp.Or(u.Model, t.Model), End: "provider failure", Usage: u})
		}
		if w.F("ok") == "" {
			continue
		}
		u := cardcost.ParseUsage(w.F(FieldUsage))
		if u.Wait < 0 && u.Run < 0 {
			dealt, taken := takeStamps(w)
			u = u.Timed(dealt, taken, stampTime(w.F("finished")))
		}
		end := "ok"
		if w.F("ok") == "no" {
			end = "failed"
		}
		v.Consumers = append(v.Consumers, Consumer{Kind: "work", Card: w.ID, Attempt: w.Int("attempt"), Who: w.F("member"),
			Route: w.F(FieldRoute), Model: cmp.Or(u.Model, w.F(FieldModel)), End: end, Usage: u})
	}
	for _, r := range reads {
		// every run returned without a verdict (FieldReadTake), then the run that gave one
		for n := 1; n < nextTake(r, FieldReadTake); n++ {
			u := cardcost.ParseUsage(r.F(FieldReadTake + itoa(n)))
			v.Consumers = append(v.Consumers, Consumer{Kind: "read", Card: r.ID, Attempt: r.Int("attempt"), Take: n, Who: r.F("reader"),
				Route: u.Route, Model: u.Model, End: "returned", Usage: u})
		}
		at := r.F("read")
		if at == "" {
			continue
		}
		u := cardcost.ParseUsage(r.F(FieldUsage))
		if u.Wait < 0 && u.Run < 0 {
			u = u.Timed(r.F("asked"), cmp.Or(r.F("begun"), at), stampTime(at))
		}
		v.Consumers = append(v.Consumers, Consumer{Kind: "read", Card: r.ID, Attempt: r.Int("attempt"), Who: r.F("reader"),
			Route: u.Route, Model: u.Model, End: r.F("verdict"), Usage: u})
	}
	us := make([]cardcost.Usage, len(v.Consumers))
	for i, c := range v.Consumers {
		us[i] = c.Usage
	}
	v.Total = cardcost.SumUsage(us)
	return v
}

// takeStamps are the stamps a work card's current take was dealt and taken at: what
// its waiting and running time are measured from, recorded and shown, judging nothing
// (the one deadline is WorkDeadline's).
func takeStamps(c *Card) (dealt, taken string) { return c.F("dealt"), c.F("taken") }

// stampTime reads a card's stamp; the zero time when it has none.
func stampTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// CostLines are the producer's cost as `card <id>` prints it: one COST line per
// consumer and the COST TOTAL line. A count or time not reported is "-", a cost not
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
	out = append(out, fmt.Sprintf("COST TOTAL consumers=%d %s wait=%s run=%s predicted_usd=%s predicted_of=%d/%d actual_usd=%s actual_by=%s actual_of=%d/%d",
		t.Records, tokenWords(t.Tokens), seconds(t.Wait), seconds(t.Run), orDash(t.Predicted), t.PredOf, t.Records, orDash(t.Actual), orDash(t.ActualBy), t.ActualOf, t.Records))
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

// FieldCost is a landed primary's total cost and a stream's control card's sum over
// its landed primaries: an exact decimal in USD, absent when nothing of it was priced.
// The work table's cost column shows the control card's (Cost; SyncMirrors).
const FieldCost = "cost"

// Charged is what the producer cost, one figure: over its consumers, each one's actual
// cost where one was reported, else its predicted one; a consumer with neither adds
// nothing. usd is "" when no consumer had either; priced is how many did.
func (v CardCostView) Charged() (usd string, priced int) {
	var vals []string
	for _, c := range v.Consumers {
		if x := cmp.Or(c.Usage.Actual, c.Usage.Predicted); x != "" {
			vals = append(vals, x)
		}
	}
	if len(vals) == 0 {
		return "", 0
	}
	sum, ok := cardcost.Sum(vals...)
	if !ok {
		return "", 0
	}
	return sum, len(vals)
}

// consumersOf are the work and read cards of the primary as the snapshot holds them,
// placed or kept as records (LandingExtras reads the kept ones): every attempt's work
// card, and every attempt's read card of every reader row.
func consumersOf(s *Snapshot, pr *Card) (work, reads []*Card) {
	for k := 1; k <= pr.Int("attempt"); k++ {
		if c := s.Fleet.Card(WorkCardID(pr.ID, k)); c != nil {
			work = append(work, c)
		}
		for _, rd := range s.Readers.Rows() {
			if c := s.Readers.Card(ReadCardID(pr.ID, k, rd)); c != nil {
				reads = append(reads, c)
			}
		}
	}
	return work, reads
}

// LandingExtras are the consumer cards a merge of the stream may land must read as
// records when they are not placed: every work and read card of each primary queued
// on the stream in the merge table (consumersOf), so a landing's total leaves no
// consumer out, a read returned and retired included. The merge table names them: the
// work table may still hold them in review under the queue the pump drains.
func LandingExtras(stream string) func(*Snapshot) map[string][]string {
	return func(s *Snapshot) map[string][]string {
		if s.Work == nil || s.Fleet == nil || s.Readers == nil || s.Merge == nil {
			return nil
		}
		var work, reads []string
		for _, mc := range s.Merge.Cell(stream, Queued) {
			pr := s.Work.Card(mc.ID)
			if pr == nil {
				continue
			}
			// named over the placed records only, as a fresh read names them: a
			// kept record an earlier step of the process showed in its twin's
			// table is still named, or the twin drops it and plans without it
			for k := 1; k <= pr.Int("attempt"); k++ {
				if id := WorkCardID(pr.ID, k); s.Fleet.Placed(id) == nil {
					work = append(work, id)
				}
				for _, rd := range s.Readers.Rows() {
					if id := ReadCardID(pr.ID, k, rd); s.Readers.Placed(id) == nil {
						reads = append(reads, id)
					}
				}
			}
		}
		out := map[string][]string{}
		if len(work) > 0 {
			out[Fleet] = work
		}
		if len(reads) > 0 {
			out[Readers] = reads
		}
		return out
	}
}

// landingCost is the total of the primary as it lands (FieldCost), from every one of
// its consumers' records; "" when none was priced.
func landingCost(s *Snapshot, pr *Card) string {
	if s.Fleet == nil || s.Readers == nil {
		return ""
	}
	work, reads := consumersOf(s, pr)
	usd, _ := CardCost(work, reads).Charged()
	return usd
}

// MoneyText is a cost as the work table's cost cell shows it: US dollars to four
// places ("$1.2345"), "-" when there is none.
func MoneyText(usd string) string {
	if usd == "" {
		return "-"
	}
	r, ok := new(big.Rat).SetString(usd)
	if !ok {
		return "-"
	}
	return "$" + r.FloatString(4)
}

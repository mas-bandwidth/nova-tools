package sprint

import (
	"cmp"
	"fmt"
	"math/big"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// What a card cost (docs/SPEC-SPRINT.md, "What a card cost"): the cost is tracked
// in the card itself, so by the time a producer card lands it holds the history
// of the consumer cards that did work for it, what each cost, and the total for
// the producer. A producer card's
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

// A read is priced as work is (the owner, 2026-10-05: "do we have the cost for readers
// properly calculated yet in nova sprint?"): by the route its ask drew, the read card's
// own route row, never by a route found again from the model its harness reported. A
// read card with a route is a routed read: its verdict (read --ok or --broken) carries
// the run's usage or is refused (ReadUsageMissing), so no routed reader can leave its
// read unpriced by saying nothing. A usage with no token count (a fleet harness that
// reported none) is permissive in what we read: the verdict is kept, its record says
// unpriced=no-tokens and the stream counts it (TierCosts.ReadsNoTokens). A subscription reader (a bud's claude -p on its own plan, which bills
// no dollar per token) says so in its usage, billing=subscription: its tokens are kept,
// with no dollar figure, and its cost is shown as tokens (CostTokens).
const (
	// UsageSubscription is the usage word a subscription reader adds to its --usage.
	UsageSubscription = "billing=subscription"
	// WhySubscription is a subscription run's unpriced word: its tokens, no dollars.
	WhySubscription = "subscription"
	// CostTokens is the cost a COST line shows for a subscription run: its tokens.
	CostTokens = "tokens"
)

// subscriptionUsage says the usage is a subscription reader's (UsageSubscription).
func subscriptionUsage(u cardcost.Usage) bool { return slices.Contains(u.Extra, UsageSubscription) }

// routedRead says the read card was drawn a route at its ask: what prices it.
func routedRead(c *Card) bool {
	r := c.F(FieldRoute)
	return r != "" && r != RoutePin
}

// readCostRecord is a read run's usage record at its end (costRecord): a routed read
// priced by its card's route, a subscription reader's tokens kept with no dollar figure
// (its harness's notional cost dropped, for its plan bills none), and a read with no
// route by an enabled route of the provider/model its harness reported, as before.
func readCostRecord(s *Snapshot, c *Card, usage, from, began string) string {
	u := cardcost.ParseUsage(usage)
	if subscriptionUsage(u) {
		u = u.Timed(from, began, s.Now)
		u.Actual, u.ActualBy = "", ""
		u.Unpriced = WhySubscription
		if !u.Tokens.Reported() {
			u.Unpriced = cardcost.WhyNoTokens
		}
		return u.String()
	}
	if routedRead(c) {
		return costRecord(s, usage, c.F(FieldRoute), c.F(FieldModel), false, from, began)
	}
	return costRecord(s, usage, "", "", true, from, began)
}

// ReadUsageMissing is why a read's verdict on the read card c is refused for its usage:
// a routed read with no --usage at all, which leaves nothing to price it by; "" otherwise.
// A usage with no token count is taken, never refused: a verdict is never lost for its
// harness's accounting (readCostRecord records it unpriced=no-tokens). Its remedy is the
// verdict again with the harness's own token report.
func ReadUsageMissing(c *Card, usage string, verdict string) string {
	if !routedRead(c) || strings.TrimSpace(usage) != "" {
		return ""
	}
	return fmt.Sprintf("a read on route %s is priced as work is, from the run's tokens, and this verdict has no --usage: run nova-sprint read --as %s --%s %s ... --usage '<the harness's own token report: input=<n> cache_read=<n> cache_write=<n> output=<n> model=<provider/model>>'; a subscription reader adds %s to its usage",
		c.F(FieldRoute), c.F("reader"), cmp.Or(verdict, "ok"), c.ID, UsageSubscription)
}

// CostWord is the cost a COST line shows for a record: which of the dollar figures it
// holds (Usage.Present), or tokens for a subscription run.
func CostWord(u cardcost.Usage) string {
	if u.Unpriced == WhySubscription {
		return CostTokens
	}
	return u.Present()
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

// The producer card carries what it cost, for the cost is tracked in the card:
// each consumer that ends appends one record to the primary,
// FieldCostRecord plus the consumer's key, in the same step that ends it, and the
// primary's running total (FieldCostTotal) is updated with it. Everything that shows
// or sums a card's cost reads the primary alone (CardCostOf), so no reader or member
// removed, no read card retired and no consumer record cleaned up can lose cost.
const (
	FieldCostRecord = "cost_record:" // + the consumer's key (Consumer.Key)
	FieldCostTotal  = "cost_total"   // cardcost.Total.String over every record
	FieldCostCut    = "cost_cut"     // how many records past MaxCostRecords were left out of the list
	// FieldCutRoute and FieldCutFriend hold the charges of the records the list bound dropped
	// (past MaxCostRecords), one line per record, "<hour-rfc3339> <name> <usd>", so the tick's
	// hourly cap sum (hourSpendOf) still counts them by route and by friend.
	FieldCutRoute  = "cost_cut_route"
	FieldCutFriend = "cost_cut_friend"
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
	Tier    string         `json:"tier,omitempty"`  // the tier its route was drawn from (flash first, pro on escalation)
	End     string         `json:"end"`
	At      string         `json:"at"`  // when it ended
	Key     string         `json:"key"` // the card and its run: one record per key, set once
	Usage   cardcost.Usage `json:"usage"`
	// Cap and Overrun are a capped take's lane cap and its wall past it (lane_cap.go), ""
	// for a take no cap ended.
	Cap     string `json:"cap,omitempty"`
	Overrun string `json:"overrun,omitempty"`
}

// line is the record as the primary keeps it: the consumer's words, then its usage.
func (c Consumer) line() string {
	w := []string{"kind=" + c.Kind, "card=" + c.Card, "attempt=" + itoa(c.Attempt), "take=" + itoa(c.Take), "gen=" + itoa(c.Gen),
		"who=" + orDash(c.Who), "on_route=" + orDash(c.Route), "on_model=" + orDash(c.Model), "on_tier=" + orDash(c.Tier), "end=" + orDash(strings.ReplaceAll(c.End, " ", "-")), "at=" + orDash(c.At)}
	if c.Cap != "" {
		w = append(w, "lane_cap="+c.Cap, "lane_overrun="+orDash(c.Overrun))
	}
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
		case "on_tier":
			c.Tier = undash(v)
		case "end":
			c.End = strings.ReplaceAll(undash(v), "-", " ")
		case "at":
			c.At = undash(v)
		case "lane_cap":
			c.Cap = undash(v)
		case "lane_overrun":
			c.Overrun = undash(v)
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
		addCutSummary(pr, set, c)
		return
	}
	set[key] = c.line()
}

// consumerUSD is a consumer record's charge: its actual cost where reported, else its
// predicted one, nil for a record with neither (a subscription run, an unpriced one).
func consumerUSD(c Consumer) *big.Rat {
	usd, err := cardcost.Decimal(c.Usage.Actual)
	if err != nil {
		if usd, err = cardcost.Decimal(c.Usage.Predicted); err != nil {
			return nil
		}
	}
	return usd
}

// consumerRoute is the route a consumer's record ran on, "" for none or a pin.
func consumerRoute(c Consumer) string {
	if r := cmp.Or(c.Route, c.Usage.Route); r != RoutePin {
		return r
	}
	return ""
}

// consumerFriend is the friend a consumer's record is charged to: a work consumer's Who
// is the member row (a friend's row names her), a read consumer's Who is the reader's name.
// "" for none.
func consumerFriend(c Consumer) string {
	if f, ok := FriendOfRow(c.Who); ok {
		return f
	}
	if c.Kind == "read" && c.Who != "" {
		return c.Who
	}
	return ""
}

// addCutSummary keeps the charges the record-list bound dropped (past MaxCostRecords) for
// the tick's hourly cap sum (hourSpendOf): one line per record under FieldCutRoute and
// FieldCutFriend, "<hour-rfc3339> <name> <usd>", so a card past the bound still sums
// exactly by route and friend. A record with no charge, no route or no friend adds nothing.
func addCutSummary(pr *Card, set map[string]string, c Consumer) {
	usd := consumerUSD(c)
	if usd == nil {
		return
	}
	at, err := time.Parse(time.RFC3339, c.At)
	// ignored: a record whose timestamp is not RFC3339 cannot be put to a clock hour, so it adds nothing to the hourly cap sum
	if err != nil {
		return
	}
	hour := at.UTC().Truncate(time.Hour).Format(time.RFC3339)
	appendLine := func(field, name string) {
		line := hour + " " + name + " " + cardcost.Text(usd)
		prev := set[field]
		if prev == "" {
			prev = pr.F(field)
		}
		if prev == "" {
			set[field] = line
			return
		}
		set[field] = prev + "\n" + line
	}
	if r := consumerRoute(c); r != "" {
		appendLine(FieldCutRoute, r)
	}
	if f := consumerFriend(c); f != "" {
		appendLine(FieldCutFriend, f)
	}
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
		Route: c.F(FieldRoute), Model: cmp.Or(u.Model, c.F(FieldModel)), Tier: c.F(FieldTier), End: end, At: stamp(s.Now), Key: key, Usage: u}
}

// readConsumer is a read card's run as it ends: run is the returned run's number, 0
// for the run that gave the verdict.
func readConsumer(s *Snapshot, c *Card, run int, end, rec string) Consumer {
	u := cardcost.ParseUsage(rec)
	key := c.ID + "#v"
	if run > 0 {
		key = c.ID + "#r" + itoa(run)
	}
	model := u.Model
	if !subscriptionUsage(u) {
		model = cmp.Or(u.Model, c.F(FieldModel)) // a routed read ran its route's model
	}
	return Consumer{Kind: "read", Card: c.ID, Attempt: c.Int("attempt"), Take: run, Who: c.F("reader"), Route: cmp.Or(u.Route, c.F(FieldRoute)), Model: model,
		Tier: c.F(FieldTier), End: end, At: stamp(s.Now), Key: key, Usage: u}
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
		out = append(out, fmt.Sprintf("COST kind=%s card=%s attempt=%d%s who=%s route=%s model=%s tier=%s end=%s %s wait=%s run=%s predicted_usd=%s actual_usd=%s actual_by=%s cost=%s",
			c.Kind, c.Card, c.Attempt, take, orDash(c.Who), orDash(cmp.Or(c.Route, u.Route)), orDash(c.Model), orDash(c.Tier), strings.ReplaceAll(c.End, " ", "-"),
			tokenWords(u.Tokens), seconds(u.Wait), seconds(u.Run), orDash(u.Predicted), orDash(u.Actual), orDash(actualBy), CostWord(u)))
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
// rounded up to the next cent ("$1.24" for 1.2345; nothing past the cent is
// shown), "-"
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

// hourSpend is what each route and each friend spent in one clock hour (docs/SPEC-SPRINT.md,
// spend-circuit-breakerb-bb.w8): the sum, over the consumer records the primaries carry that
// ended in [from, to), of each record's actual cost where reported, else its predicted one,
// exact, by the route it ran on and, for a friend's take, by her. A record with neither
// figure (a subscription run, an unpriced one) adds nothing.
type hourSpend struct {
	from, to      time.Time
	route, friend map[string]*big.Rat
}

// clockHour is the clock hour holding now, in UTC: [from, to).
func clockHour(now time.Time) (from, to time.Time) {
	from = now.UTC().Truncate(time.Hour)
	return from, from.Add(time.Hour)
}

// hourSpendOf is the spend of the clock hour holding s.Now, summed once from the work
// table's cost records (the primary alone holds a card's cost, CardCostOf), the record
// list's bound included (FieldCutRoute, FieldCutFriend). A snapshot with no work table
// sums nothing.
func hourSpendOf(s *Snapshot) *hourSpend {
	h := &hourSpend{route: map[string]*big.Rat{}, friend: map[string]*big.Rat{}}
	if s == nil {
		return h
	}
	h.from, h.to = clockHour(s.Now)
	if s.Work == nil {
		return h
	}
	add := func(m map[string]*big.Rat, k string, v *big.Rat) {
		if m[k] == nil {
			m[k] = new(big.Rat)
		}
		m[k].Add(m[k], v)
	}
	addCut := func(m map[string]*big.Rat, lines string) {
		for _, line := range strings.Split(lines, "\n") {
			f := strings.Fields(line)
			if len(f) != 3 {
				continue
			}
			at, err := time.Parse(time.RFC3339, f[0])
			if err != nil || at.Before(h.from) || !at.Before(h.to) {
				continue
			}
			usd, ok := new(big.Rat).SetString(f[2])
			if !ok {
				continue
			}
			add(m, f[1], usd)
		}
	}
	for _, pr := range s.Work.Cards() {
		for k, line := range pr.Fields {
			key, ok := strings.CutPrefix(k, FieldCostRecord)
			if !ok {
				continue
			}
			c := parseConsumer(key, line)
			at, err := time.Parse(time.RFC3339, c.At)
			if err != nil || at.Before(h.from) || !at.Before(h.to) {
				continue
			}
			usd := consumerUSD(c)
			if usd == nil {
				continue
			}
			if r := consumerRoute(c); r != "" {
				add(h.route, r, usd)
			}
			if f := consumerFriend(c); f != "" {
				add(h.friend, f, usd)
			}
		}
		// the charges the list bound dropped: still summed, by route and friend, for the hour
		addCut(h.route, pr.F(FieldCutRoute))
		addCut(h.friend, pr.F(FieldCutFriend))
	}
	return h
}

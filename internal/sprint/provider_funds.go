package sprint

import (
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A PROVIDER OUT OF FUNDS IS NEVER A MYSTERY FAILURE (nova-tools#5199; the owner,
// 2026-10-03: "provider out of funds should never be a mystery failure.").
//
//   - a take the provider refused for want of credit (class out-of-credit) or for its key
//     (class auth) rests the PROVIDER, every route of it, in the tick that sees it
//     (RestsDue): one fleet table property, provider_rest_<provider>, never a copy per
//     route; the deal then holds the provider's cards under the tier's one judgment, `no
//     route serves the tier`, whose words name the rest. A refusal is attributed to the
//     rest window its child launched in: one launched before a rest ended (funded, or a
//     balance) starts no new rest when it is refused after the end; one launched after does;
//   - the balance poll rests the provider too (balance.go): out of credit at a balance at
//     or under zero, low on funds at a balance over zero but not over one hour of its spend;
//   - each rest of the provider's funds holds until the provider is paid: funded, or a
//     payment the poll sees (balance.go says what each rest needs); a refused take's rest
//     ends ONLY on funded or a balance read higher than the read before it, or than the
//     balance at the refusal, since a provider that refuses can still read over zero;
//   - while the provider rests for its funds or its key, ONE judgment of it is open, never
//     one per card, decided by funded, ack or wait and never by a rework (a payment is not
//     the card's to fix): `provider <p> is out of funds`, `provider <p> is low on funds`, or
//     `provider <p> refuses the seat's key`;
//   - when every enabled route rests because its provider is OUT of credit, the tick stops
//     the machine (the owner, 2026-10-03: "if all providers are out, then you stop the
//     sprint."). Low on funds never counts toward it: that provider still has money.

// The provider's judgments, one per provider while its routes rest for the cause, subject
// ProviderSubject(provider).
const (
	NProviderFunds = "a provider is out of funds"
	NProviderLow   = "a provider is low on funds"
	NProviderKey   = "a provider refuses its key"
)

// ProviderSubject is the stream word a provider's judgment is filed under: no stream id has
// a colon, so it is never a stream's.
func ProviderSubject(provider string) string { return "provider:" + provider }

// causeRE reads a provider line as native and the member write it (internal/swarm
// ProviderCause.Reason): `provider: class=<c> status=<n|-> msg=<m>`.
var causeRE = regexp.MustCompile(`\bclass=(\S+) status=(\S+) msg=(.*)$`)

// refusal is the provider line of a take the provider refused for credit or for its key,
// trimmed; "" for any other take.
func refusal(line string) string {
	m := causeRE.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil || (m[1] != RestCredit && m[1] != RestAuth) {
		return ""
	}
	return strings.TrimSpace(line)
}

// staleRefusal is a provider's refusal of a take whose launch (taken) is before this process
// started (Snapshot.Start): it rests nothing on a cold start, and the tick names it in one
// happened note (NProviderStale).
type staleRefusal struct {
	provider, route, card string
	taken                 time.Time
}

// providerRestsDue is the rests a provider's refusal writes now, one per provider (Route
// "", PropProviderRest), in provider order, and the stale refusals it skipped. A provider
// not resting at s.Now is rested by its newest take refused for credit or its key whose
// child launched after its last rest ended (Until, which ending a rest sets to the moment
// it ended): a take launched before that end belongs to the rest window it launched in,
// however late its refusal arrives, and a record that holds no launch time starts no second
// rest. A refusal whose take launched before this process started (s.Start) rests nothing:
// a cold start judges no provider out of funds from a stored refusal. Out of credit rests it
// until it is paid (OpenUntil), its key for RouteRestFor; the rest names the take's card,
// its route and the provider's words, and keeps the balance the poll last read (the mark a
// payment is seen against, balance.go).
func providerRestsDue(s *Snapshot, rests map[string]RouteRest, ends map[string][]routeEnd) ([]RouteRest, []staleRefusal) {
	newest := map[string]routeEnd{}
	on := map[string]string{}
	var stale []staleRefusal
	for _, r := range s.Routes {
		last, had := rests[r.Provider]
		if r.Provider == "" || had && last.Resting(s.Now) {
			continue
		}
		for _, e := range ends[r.Name] {
			if e.refused == "" || had && !e.taken.After(last.Until) {
				continue
			}
			if e.taken.Before(s.Start) {
				stale = append(stale, staleRefusal{provider: r.Provider, route: r.Name, card: e.card, taken: e.taken})
				continue
			}
			if n, ok := newest[r.Provider]; !ok || cmpEnd(n, e) < 0 {
				newest[r.Provider], on[r.Provider] = e, r.Name
			}
		}
	}
	var out []RouteRest
	balances := ProviderBalances(s.Fleet)
	for _, p := range slices.Sorted(maps.Keys(newest)) {
		e := newest[p]
		m := causeRE.FindStringSubmatch(e.refused)
		cause, until := m[1], s.Now.Add(RouteRestFor)
		words := fmt.Sprintf("provider %s refused its key: card %s on route %s: class=%s status=%s msg=%s", p, e.card, on[p], m[1], m[2], m[3])
		if cause == RestCredit {
			until = OpenUntil
			words = fmt.Sprintf("out of credit: provider %s refused card %s on route %s: class=%s status=%s msg=%s", p, e.card, on[p], m[1], m[2], m[3])
		}
		b := balances[p]
		out = append(out, RouteRest{Provider: p, At: s.Now, Until: until, Cards: []string{e.card}, Cause: cause, Balance: b.Balance, HasBalance: b.Known, Why: oneLine(words)})
	}
	return out, stale
}

// oneLine is text as a rest's words hold it: its words joined by single blanks.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// FundsCause is the cause the machine's record carries when the tick stopped it because
// every provider is out of credit, and the words the judgment, the machine line and a
// refused start say (the owner, 2026-10-03: "if all providers are out, then you stop the
// sprint.").
const FundsCause = "every provider is out of credit"

// NProviderStale is the happened note the tick writes for a provider's stored refusal whose
// take launched before this process started (Snapshot.Start): on a cold start it rests
// nothing, and the note says so to the coordinator, once a process, never once a tick.
const NProviderStale = "a provider's stored refusal is from before this start"

// NAllOutOfCredit is the tick's judgment when it stops the machine for FundsCause: one,
// while every enabled route rests for its provider's funds.
const NAllOutOfCredit = "every provider is out of credit"

// AllOutOfCredit is the words of FundsCause when every enabled route of every tier rests at
// now because its provider is out of credit (RouteRest.Out: a take refused for credit, or a
// balance at or under zero), naming each provider's refusal and whether it is from before
// this start; "" when a route serves, or rests for another cause (a provider low on funds
// included: it still has money), or there is no enabled route. An unknown balance never
// counts as out: an out-of-credit rest that keeps no balance (HasBalance false) counts
// toward the stop only when it began in this process (at or after start), so a stored
// refusal from before this start rests nothing toward the stop.
func AllOutOfCredit(routes []Route, rests map[string]RouteRest, start, now time.Time) string {
	byProvider := map[string]RouteRest{}
	for _, r := range routes {
		if !r.Enabled {
			continue
		}
		rest, ok := rests[r.Name]
		if !ok || !rest.Resting(now) || !rest.Out() {
			return ""
		}
		if !rest.HasBalance && rest.At.Before(start) {
			return ""
		}
		if _, seen := byProvider[r.Provider]; !seen {
			byProvider[r.Provider] = rest
		}
	}
	if len(byProvider) == 0 {
		return ""
	}
	var named []string
	for _, p := range slices.Sorted(maps.Keys(byProvider)) {
		rest := byProvider[p]
		switch {
		case rest.Refused():
			when := "refused in this process"
			if rest.At.Before(start) {
				when = "refused before this start"
			}
			named = append(named, p+": card "+strings.Join(rest.Cards, ", ")+" "+when)
		default:
			named = append(named, p+": balance at or under zero")
		}
	}
	return FundsCause + " (" + strings.Join(named, "; ") + "): a payment is the owner's; the sprint is STOPPED until a provider is paid: a balance over zero the poll reads higher than the one before or than the balance at the refusal, a balance over zero that ends a refusal from before this start, or nova-sprint funded <provider>"
}

// OutOfCredit is AllOutOfCredit of the snapshot's routes and the fleet table's rests.
func (s *Snapshot) OutOfCredit() string {
	return AllOutOfCredit(s.Routes, RouteRests(s.Routes, s.Fleet), s.Start, s.Now)
}

// providerConds is the provider's judgments that hold at s.Now: one for each provider
// resting for its funds or its key (NProviderFunds out of credit, NProviderLow low on funds,
// NProviderKey), in provider order, and NAllOutOfCredit when every enabled route rests
// because its provider is out of credit (stop says so), from the rests a dealing step
// settled (withRests).
func providerConds(s *Snapshot) (conds []cond, stop string) {
	type held struct {
		routes []string
		rest   RouteRest
	}
	byProvider := map[string]*held{}
	for _, r := range s.Routes {
		rest, ok := s.rests[r.Name]
		if !ok || rest.Provider == "" || !rest.Funds() && rest.Cause != RestAuth {
			continue
		}
		h := byProvider[rest.Provider]
		if h == nil {
			h = &held{rest: rest}
			byProvider[rest.Provider] = h
		}
		h.routes = append(h.routes, r.Name)
	}
	balances := ProviderBalances(s.Fleet)
	for _, p := range slices.Sorted(maps.Keys(byProvider)) {
		h := byProvider[p]
		routes := strings.Join(slices.Sorted(slices.Values(h.routes)), ", ")
		b := balances[p]
		switch h.rest.Cause {
		case RestCredit:
			ends := "it reads a balance over zero"
			if h.rest.Refused() {
				ends = "it sees a payment (a balance over zero read higher than the read before it, or than the balance at the refusal)"
			}
			conds = append(conds, cond{typ: NProviderFunds, stream: ProviderSubject(p), streamLevel: true,
				decisions: []string{"funded " + p, "ack", "wait"},
				what: fmt.Sprintf("provider %s is out of funds (balance %s): a payment is the owner's; it is excluded: its routes %s rest until %s (%s); the balance poll ends the rest when %s, or nova-sprint funded %s once it is paid; nova-sprint where --json shows the balance",
					p, b.Said(), routes, h.rest.UntilSaid(), h.rest.Why, ends, p)})
		case RestBalance:
			conds = append(conds, cond{typ: NProviderLow, stream: ProviderSubject(p), streamLevel: true,
				decisions: []string{"funded " + p, "ack", "wait"},
				what: fmt.Sprintf("provider %s is low on funds (balance %s, not over one hour of its spend, %s an hour, measured before the rest): a payment is the owner's; it is excluded: its routes %s rest until the balance poll reads more than that hour of spend, or nova-sprint funded %s once it is paid (%s); it is not out of credit, and the sprint does not stop for it; nova-sprint where --json shows the balance",
					p, b.Said(), Dollars(b.SpendHour), routes, p, h.rest.Why)})
		default:
			conds = append(conds, cond{typ: NProviderKey, stream: ProviderSubject(p), streamLevel: true,
				what: fmt.Sprintf("provider %s refuses the seat's key: the key is the owner's; its routes %s rest until %s (%s); the deal draws none of them until then",
					p, routes, h.rest.UntilSaid(), h.rest.Why)})
		}
	}
	if stop = AllOutOfCredit(s.Routes, s.rests, s.Start, s.Now); stop != "" {
		conds = append(conds, cond{typ: NAllOutOfCredit, streamLevel: true, what: stop})
	}
	return conds, stop
}

// PropProviderBalance is the fleet table's property that holds a provider's last balance
// read (balance.go writes it).
func PropProviderBalance(provider string) string { return "provider_balance_" + provider }

// ProviderBalance is a provider's balance as the poll last read it: the dollars left
// (Known false when the provider has no balance the poll can read, Note saying why), when it
// was read, the provider's own count of dollars used (HasUsed false when it keeps none), and
// the spend in dollars an hour measured from that count (or the balance) between two reads.
type ProviderBalance struct {
	Provider  string
	Known     bool
	Balance   float64
	At        time.Time
	SpendHour float64
	HasUsed   bool
	Used      float64
	Note      string
}

// value is the balance as the property holds it:
// `<balance|unknown> <at> <spend/hour> <used|-> <note>`.
func (b ProviderBalance) value() string {
	bal, used := "unknown", "-"
	if b.Known {
		bal = strconv.FormatFloat(b.Balance, 'f', -1, 64)
	}
	if b.HasUsed {
		used = strconv.FormatFloat(b.Used, 'f', -1, 64)
	}
	return strings.TrimSpace(bal + " " + stamp(b.At) + " " + strconv.FormatFloat(b.SpendHour, 'f', -1, 64) + " " + used + " " + oneLine(b.Note))
}

// Said is the balance as a line says it: dollars and cents, rounded up, and when it was
// read; "unknown" and why when the poll could not read it, or has not.
func (b ProviderBalance) Said() string {
	switch {
	case b.At.IsZero():
		return "unknown: not polled yet"
	case !b.Known:
		return "unknown: " + b.Note
	}
	return Dollars(b.Balance) + " at " + stamp(b.At)
}

// Dollars is an amount as dollars and cents, rounded up to the cent, a negative one with
// its sign first (-$0.51).
func Dollars(v float64) string {
	c := math.Ceil(math.Round(v*1e6)/1e4) / 100 // the float's noise first, then up to the cent
	if c < 0 {
		return fmt.Sprintf("-$%.2f", -c)
	}
	return fmt.Sprintf("$%.2f", c+0) // +0: no "-0.00"
}

// ProviderBalances is the last balance the fleet table records for each provider polled.
func ProviderBalances(fleet *Table) map[string]ProviderBalance {
	out := map[string]ProviderBalance{}
	if fleet == nil {
		return out
	}
	for name, v := range fleet.Props() {
		p, ok := strings.CutPrefix(name, "provider_balance_")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) < 4 {
			continue
		}
		at, err := time.Parse(time.RFC3339, f[1])
		if err != nil {
			continue
		}
		b := ProviderBalance{Provider: p, At: at, Note: strings.Join(f[4:], " ")}
		b.SpendHour, _ = strconv.ParseFloat(f[2], 64)
		if x, err := strconv.ParseFloat(f[0], 64); err == nil {
			b.Known, b.Balance = true, x
		}
		if x, err := strconv.ParseFloat(f[3], 64); err == nil {
			b.HasUsed, b.Used = true, x
		}
		out[p] = b
	}
	return out
}

// FundedReq is the coordinator's word that a provider was paid (funded): every rest of its
// funds ends now, for a provider whose balance no poll can read (opencode) as for any.
type FundedReq struct {
	Provider string
	Reason   string
	Who      string
}

// Funded ends the provider's rest of its funds holding at s.Now (out of credit or low), one
// write, with a happened note to the coordinator; refused when the provider does not rest
// for its funds, or no reason is given. The provider's judgment closes at the next tick, and
// a machine the tick stopped because every provider was out of credit is then the
// coordinator's to start.
func Funded(s *Snapshot, r FundedReq) Plan {
	var p Plan
	if strings.TrimSpace(r.Reason) == "" {
		p.refuse(r.Provider, "funded wants --reason: the payment made, in a few words")
		return p
	}
	rest, ok := ProviderRests(s.Fleet)[r.Provider]
	if !ok || !rest.Resting(s.Now) || !rest.Funds() {
		p.refuse(r.Provider, "provider "+r.Provider+" does not rest for its funds: nothing to end (nova-sprint routes shows each route's rest)")
		return p
	}
	next := rest
	next.Until = s.Now // the rest's end: a refusal launched before it starts no new rest
	next.Why = oneLine(rest.Why + "; ended: funded by " + r.Who + ": " + r.Reason)
	was, _ := s.Fleet.Prop(PropProviderRest(r.Provider))
	p.Props = append(p.Props, PropWrite{Table: Fleet, Name: PropProviderRest(r.Provider), Value: next.value(), Was: was})
	routes := strings.Join(restedRoutes(rest, s.Routes), ", ")
	n := happened(NProviderFunded, ProviderSubject(r.Provider), s.Now)
	n.To, n.Who = s.Coordinator, r.Who
	n.What = fmt.Sprintf("provider %s serves again, its routes %s: it was paid (%s, by %s)", r.Provider, routes, oneLine(r.Reason), r.Who)
	p.Notes = append(p.Notes, n)
	p.Units = append(p.Units, Unit{Key: r.Provider, Moved: "provider " + r.Provider + " funded: " + routes + " serve again"})
	return p
}

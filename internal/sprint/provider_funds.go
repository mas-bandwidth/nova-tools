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
// Overnight 2026-10-02/03 two providers ran out of credit; every child on them ended at
// launch, each take was dealt again on the next route of the same provider, ci-03 was dealt
// 247 times and docsd-03 273 times, nothing landed, and the coordinator learned it in the
// morning. So:
//
//   - a take the provider refused for want of credit (class out-of-credit) or for its key
//     (class auth) rests EVERY route of that provider, not the one route, in the tick that
//     sees it (RestsDue): the deal then holds the provider's cards under the tier's one
//     judgment, `no route serves the tier`, whose words name the rest;
//   - a balance the poll read under one hour of the provider's measured spend rests them
//     too (cause balance, the poll's step, balance.go);
//   - while any route of a provider rests for its funds, ONE judgment of the provider is
//     open, never one per card: `provider <p> is out of funds (balance $x): a payment is
//     the owner's`, decided by ack or wait, never by a rework (a payment is not the card's
//     to fix); a key the provider refused is the same, `the key is the owner's`.

// The provider's judgments, one per provider while its routes rest for the cause, subject
// ProviderSubject(provider).
const (
	NProviderFunds = "a provider is out of funds"
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

// providerRestsDue is the rests a provider's refusal writes now, by route: for each provider,
// its newest take refused for credit or its key on a route not resting at s.Now, ended after
// that route's last rest began, rests every route of the provider not resting at s.Now from
// s.Now, naming the take's card and the provider's words: until a balance returns for want
// of credit (OpenUntil), for RouteRestFor for its key.
func providerRestsDue(s *Snapshot, rests map[string]RouteRest, ends map[string][]routeEnd) map[string]RouteRest {
	newest := map[string]routeEnd{}
	on := map[string]string{}
	for _, r := range s.Routes {
		last, had := rests[r.Name]
		if r.Provider == "" || had && last.Resting(s.Now) {
			continue
		}
		for _, e := range ends[r.Name] {
			if e.refused == "" || had && !e.at.After(last.At) {
				continue
			}
			if n, ok := newest[r.Provider]; !ok || cmpEnd(n, e) < 0 {
				newest[r.Provider], on[r.Provider] = e, r.Name
			}
		}
	}
	out := map[string]RouteRest{}
	for _, p := range slices.Sorted(maps.Keys(newest)) {
		e := newest[p]
		m := causeRE.FindStringSubmatch(e.refused)
		cause, until := m[1], s.Now.Add(RouteRestFor)
		words := fmt.Sprintf("provider %s refused its key: card %s on route %s: class=%s status=%s msg=%s", p, e.card, on[p], m[1], m[2], m[3])
		if cause == RestCredit {
			until = OpenUntil
			words = fmt.Sprintf("out of credit: provider %s refused card %s on route %s: class=%s status=%s msg=%s", p, e.card, on[p], m[1], m[2], m[3])
		}
		for _, r := range s.Routes {
			if last, had := rests[r.Name]; r.Provider != p || had && last.Resting(s.Now) {
				continue
			}
			out[r.Name] = RouteRest{Route: r.Name, At: s.Now, Until: until, Cards: []string{e.card}, Cause: cause, Why: oneLine(words)}
		}
	}
	return out
}

// oneLine is text as a rest's words hold it: its words joined by single blanks.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// FundsCause is the cause the machine's record carries when the tick stopped it because
// every provider is out of credit, and the words the judgment, the machine line and a
// refused start say (the owner, 2026-10-03: "if all providers are out, then you stop the
// sprint.").
const FundsCause = "every provider is out of credit"

// NAllOutOfCredit is the tick's judgment when it stops the machine for FundsCause: one,
// while every enabled route rests for its provider's funds.
const NAllOutOfCredit = "every provider is out of credit"

// AllOutOfCredit is the words of FundsCause when every enabled route of every tier rests at
// now for its provider's funds (a refused take for credit, or a balance), naming the
// providers; "" when a route serves, or rests for another cause, or there is no enabled
// route.
func AllOutOfCredit(routes []Route, rests map[string]RouteRest, now time.Time) string {
	providers := map[string]bool{}
	for _, r := range routes {
		if !r.Enabled {
			continue
		}
		rest, ok := rests[r.Name]
		if !ok || !rest.Resting(now) || !rest.Funds() {
			return ""
		}
		providers[r.Provider] = true
	}
	if len(providers) == 0 {
		return ""
	}
	return FundsCause + " (" + strings.Join(slices.Sorted(maps.Keys(providers)), ", ") + "): a payment is the owner's; the sprint is STOPPED until a balance returns"
}

// OutOfCredit is AllOutOfCredit of the snapshot's routes and the fleet table's rests.
func (s *Snapshot) OutOfCredit() string {
	return AllOutOfCredit(s.Routes, RouteRests(s.Routes, s.Fleet), s.Now)
}

// providerConds is the provider's judgments that hold at s.Now: one for each provider with a
// route resting for its funds (NProviderFunds) or its key (NProviderKey), in provider order,
// and NAllOutOfCredit when every enabled route rests for its funds (stop says so), from the
// rests a dealing step settled (withRests).
func providerConds(s *Snapshot) (conds []cond, stop string) {
	type held struct {
		routes []string
		rest   RouteRest
	}
	funds, key := map[string]*held{}, map[string]*held{}
	provider := map[string]string{}
	for _, r := range s.Routes {
		provider[r.Name] = r.Provider
	}
	for _, name := range slices.Sorted(maps.Keys(s.rests)) {
		rest, p := s.rests[name], provider[name]
		by := key
		switch {
		case p == "":
			continue
		case rest.Funds():
			by = funds
		case rest.Cause != RestAuth:
			continue
		}
		h := by[p]
		if h == nil {
			h = &held{rest: rest}
			by[p] = h
		}
		h.routes = append(h.routes, name)
		if rest.Until.After(h.rest.Until) {
			h.rest.Until = rest.Until
		}
	}
	balances := ProviderBalances(s.Fleet)
	for _, p := range slices.Sorted(maps.Keys(funds)) {
		h := funds[p]
		conds = append(conds, cond{typ: NProviderFunds, stream: ProviderSubject(p), streamLevel: true,
			decisions: []string{"funded " + p, "ack", "wait"},
			what: fmt.Sprintf("provider %s is out of funds (balance %s): a payment is the owner's; it is excluded: its routes %s rest until %s (%s); the balance poll ends the rest when it reads a balance again, or nova-sprint funded %s once it is paid; nova-sprint where --json shows the balance",
				p, balances[p].Said(), strings.Join(h.routes, ", "), h.rest.UntilSaid(), h.rest.Why, p)})
	}
	for _, p := range slices.Sorted(maps.Keys(key)) {
		h := key[p]
		conds = append(conds, cond{typ: NProviderKey, stream: ProviderSubject(p), streamLevel: true,
			what: fmt.Sprintf("provider %s refuses the seat's key: the key is the owner's; its routes %s rest until %s (%s); the deal draws none of them until then",
				p, strings.Join(h.routes, ", "), h.rest.UntilSaid(), h.rest.Why)})
	}
	if stop = AllOutOfCredit(s.Routes, s.rests, s.Now); stop != "" {
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

// Dollars is an amount as dollars and cents, rounded up to the cent.
func Dollars(v float64) string {
	c := math.Ceil(math.Round(v*1e6)/1e4) / 100 // the float's noise first, then up to the cent
	if c == 0 {
		c = 0 // no "-0.00"
	}
	return fmt.Sprintf("$%.2f", c)
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

// Funded ends every rest of the provider's funds holding at s.Now, each with a happened note
// to the coordinator; refused when the provider has no route resting for its funds, or no
// reason is given. The provider's judgment closes at the next tick, and a machine the tick
// stopped because every provider was out of credit is then the coordinator's to start.
func Funded(s *Snapshot, r FundedReq) Plan {
	var p Plan
	if strings.TrimSpace(r.Reason) == "" {
		p.refuse(r.Provider, "funded wants --reason: the payment made, in a few words")
		return p
	}
	rests := RouteRests(s.Routes, s.Fleet)
	var ended []string
	for _, route := range s.Routes {
		rest, ok := rests[route.Name]
		if route.Provider != r.Provider || !ok || !rest.Resting(s.Now) || !rest.Funds() {
			continue
		}
		next := rest
		next.Until = s.Now
		next.Why = oneLine(rest.Why + "; ended: funded by " + r.Who + ": " + r.Reason)
		was, _ := s.Fleet.Prop(PropRouteRest(route.Name))
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: PropRouteRest(route.Name), Value: next.value(), Was: was})
		n := happened(NProviderFunded, ProviderSubject(r.Provider), s.Now)
		n.To, n.Who = s.Coordinator, r.Who
		n.What = fmt.Sprintf("route %s serves again: provider %s was paid (%s, by %s)", route.Name, r.Provider, oneLine(r.Reason), r.Who)
		p.Notes = append(p.Notes, n)
		ended = append(ended, route.Name)
	}
	if len(ended) == 0 {
		p.refuse(r.Provider, "provider "+r.Provider+" has no route resting for its funds: nothing to end (nova-sprint routes shows each route's rest)")
		return p
	}
	p.Units = append(p.Units, Unit{Key: r.Provider, Moved: "provider " + r.Provider + " funded: " + strings.Join(ended, ", ") + " serve again"})
	return p
}

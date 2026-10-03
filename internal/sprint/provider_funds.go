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
// that route's last rest began, rests every route of the provider not resting at s.Now, from
// s.Now for RouteRestFor, naming the take's card and the provider's words.
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
		cause, words := m[1], fmt.Sprintf("provider %s refused card %s on route %s: class=%s status=%s msg=%s", p, e.card, on[p], m[1], m[2], m[3])
		for _, r := range s.Routes {
			if last, had := rests[r.Name]; r.Provider != p || had && last.Resting(s.Now) {
				continue
			}
			out[r.Name] = RouteRest{Route: r.Name, At: s.Now, Until: s.Now.Add(RouteRestFor), Cards: []string{e.card}, Cause: cause, Why: oneLine(words)}
		}
	}
	return out
}

// oneLine is text as a rest's words hold it: its words joined by single blanks.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// providerConds is the provider's judgments that hold at s.Now: one for each provider with a
// route resting for its funds (NProviderFunds) or its key (NProviderKey), in provider order,
// from the rests a dealing step settled (withRests).
func providerConds(s *Snapshot) []cond {
	type held struct {
		routes []string
		until  time.Time
		why    string
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
			h = &held{why: rest.Why}
			by[p] = h
		}
		h.routes = append(h.routes, name)
		if rest.Until.After(h.until) {
			h.until = rest.Until
		}
	}
	balances := ProviderBalances(s.Fleet)
	var out []cond
	for _, p := range slices.Sorted(maps.Keys(funds)) {
		h := funds[p]
		out = append(out, cond{typ: NProviderFunds, stream: ProviderSubject(p), streamLevel: true,
			what: fmt.Sprintf("provider %s is out of funds (balance %s): a payment is the owner's; its routes %s rest until %s (%s); the deal draws none of them until then, and nova-sprint where --json shows the balance",
				p, balances[p].Said(), strings.Join(h.routes, ", "), stamp(h.until), h.why)})
	}
	for _, p := range slices.Sorted(maps.Keys(key)) {
		h := key[p]
		out = append(out, cond{typ: NProviderKey, stream: ProviderSubject(p), streamLevel: true,
			what: fmt.Sprintf("provider %s refuses the seat's key: the key is the owner's; its routes %s rest until %s (%s); the deal draws none of them until then",
				p, strings.Join(h.routes, ", "), stamp(h.until), h.why)})
	}
	return out
}

// PropProviderBalance is the fleet table's property that holds a provider's last balance
// read (balance.go writes it).
func PropProviderBalance(provider string) string { return "provider_balance_" + provider }

// ProviderBalance is a provider's balance as the poll last read it: the dollars left
// (Known false when the provider has no balance the poll can read, Note saying why), when it
// was read, and the provider's measured spend in dollars an hour then.
type ProviderBalance struct {
	Provider  string
	Known     bool
	Balance   float64
	At        time.Time
	SpendHour float64
	Note      string
}

// value is the balance as the property holds it: `<balance|unknown> <at> <spend/hour> <note>`.
func (b ProviderBalance) value() string {
	bal := "unknown"
	if b.Known {
		bal = strconv.FormatFloat(b.Balance, 'f', -1, 64)
	}
	return strings.TrimSpace(bal + " " + stamp(b.At) + " " + strconv.FormatFloat(b.SpendHour, 'f', -1, 64) + " " + oneLine(b.Note))
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
		if len(f) < 3 {
			continue
		}
		at, err := time.Parse(time.RFC3339, f[1])
		if err != nil {
			continue
		}
		b := ProviderBalance{Provider: p, At: at, Note: strings.Join(f[3:], " ")}
		b.SpendHour, _ = strconv.ParseFloat(f[2], 64)
		if x, err := strconv.ParseFloat(f[0], 64); err == nil {
			b.Known, b.Balance = true, x
		}
		out[p] = b
	}
	return out
}

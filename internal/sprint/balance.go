package sprint

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// THE BALANCE POLL (nova-tools#5199; the owner, 2026-10-03: "provider out of funds should
// never be a mystery failure."). The run loop reads each provider's balance every
// BalancePollEvery through the seat's key (cmd/nova-sprint balance.go, internal/provbalance:
// the transport), and this step writes what it read: the fleet table's property
// provider_balance_<provider> (ProviderBalance), the spend an hour measured from the
// provider's own count of dollars used (else the balance's fall) since the read before,
// and the rests the balance calls for:
//
//   - a balance at or under zero rests every route of the provider "out of credit" (cause
//     out-of-credit), and one under one hour of the measured spend rests them too (cause
//     balance), each until a balance returns (OpenUntil), never for a time (the owner,
//     2026-10-03, 8:18 AM ET: "you'll need to detect when a provider runs out of credits,
//     and exclude that provider moving forward, and let me know."): the deal draws none of
//     them, and the provider's one judgment is open (provider_funds.go), before any take is
//     refused;
//   - a balance read over zero and over an hour of the spend (a payment came) ends at once
//     every rest of the provider's funds, a refused take's included; the judgment closes at
//     the next tick, and a machine the tick stopped because every provider was out of
//     credit is the coordinator's to start.
//
// A provider whose balance cannot be read (no endpoint, no key, an answer that is not the
// shape) is recorded unknown with why; nothing is rested or ended on it.

// BalancePollEvery is how often the run loop reads the providers' balances.
const BalancePollEvery = 10 * time.Minute

// ProviderRead is one balance the poll read: the provider, the dollars left (Known false
// with Note saying why when there is none to read), and its count of dollars used (HasUsed
// false when it keeps none).
type ProviderRead struct {
	Provider string
	Known    bool
	Balance  float64
	HasUsed  bool
	Used     float64
	Note     string
}

// BalanceReq is the reads of one poll.
type BalanceReq struct {
	Reads []ProviderRead
	Who   string
}

// NProviderFunded is the happened note of a rest of a provider's funds the poll ended.
const NProviderFunded = "a provider's routes serve again: its balance is back"

// Low says a balance calls for a rest: at or under zero, or under one hour of the spend.
func (b ProviderBalance) Low() bool { return b.Known && (b.Balance <= 0 || b.Balance < b.SpendHour) }

// spendSince is the spend an hour between the read before (was) and this one at now: from
// the provider's count used when both reads have one, else from the balance's fall; the
// spend measured before when neither says (the first read, a payment between the two).
func spendSince(was ProviderBalance, r ProviderRead, now time.Time) float64 {
	h := now.Sub(was.At).Hours()
	switch {
	case was.At.IsZero() || h <= 0:
		return 0
	case r.HasUsed && was.HasUsed && r.Used >= was.Used:
		return (r.Used - was.Used) / h
	case !r.HasUsed && r.Known && was.Known && r.Balance <= was.Balance:
		return (was.Balance - r.Balance) / h
	}
	return was.SpendHour
}

// Balance is the poll's step: each read written to its provider's property, and the rests it
// calls for written or ended on the provider's routes (see above), each with a happened note
// to the coordinator.
func Balance(s *Snapshot, r BalanceReq) Plan {
	var p Plan
	balances, rests := ProviderBalances(s.Fleet), RouteRests(s.Routes, s.Fleet)
	var said []string
	reads := slices.Clone(r.Reads)
	slices.SortFunc(reads, func(a, b ProviderRead) int { return strings.Compare(a.Provider, b.Provider) })
	for _, rd := range reads {
		was := balances[rd.Provider]
		b := ProviderBalance{Provider: rd.Provider, Known: rd.Known, Balance: rd.Balance, At: s.Now, HasUsed: rd.HasUsed, Used: rd.Used, Note: rd.Note,
			SpendHour: spendSince(was, rd, s.Now)}
		prop, had := s.Fleet.Prop(PropProviderBalance(rd.Provider))
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: PropProviderBalance(rd.Provider), Value: b.value(), Was: prop, WasAbsent: !had})
		said = append(said, rd.Provider+" "+b.Said())
		if !b.Known {
			continue
		}
		for _, route := range s.Routes {
			if route.Provider != rd.Provider {
				continue
			}
			rest, has := rests[route.Name]
			resting := has && rest.Resting(s.Now)
			var next RouteRest
			switch {
			case b.Low() && !resting:
				next = RouteRest{Route: route.Name, At: s.Now, Until: OpenUntil, Cause: RestBalance,
					Why: oneLine(fmt.Sprintf("provider %s balance %s is under one hour of its spend (%s an hour)", rd.Provider, b.Said(), Dollars(b.SpendHour)))}
				if b.Balance <= 0 {
					next.Cause, next.Why = RestCredit, oneLine(fmt.Sprintf("out of credit: provider %s balance %s", rd.Provider, b.Said()))
				}
			case !b.Low() && resting && rest.Funds():
				next = rest
				next.Until = s.Now
				next.Why = oneLine(rest.Why + "; ended: balance " + b.Said())
			default:
				continue
			}
			was, hadRest := s.Fleet.Prop(PropRouteRest(route.Name))
			p.Props = append(p.Props, PropWrite{Table: Fleet, Name: PropRouteRest(route.Name), Value: next.value(), Was: was, WasAbsent: !hadRest})
			n := happened(NRouteRested, TierSubject(route.Tier), s.Now)
			if !next.Resting(s.Now) {
				n = happened(NProviderFunded, ProviderSubject(rd.Provider), s.Now)
			}
			n.To, n.Who = s.Coordinator, r.Who
			n.What = fmt.Sprintf("route %s rested until %s: %s; the deal draws no work card on it until then; nova-sprint routes shows it", route.Name, next.UntilSaid(), next.Why)
			if !next.Resting(s.Now) {
				n.What = fmt.Sprintf("route %s serves again: provider %s's balance is %s, over one hour of its spend (%s an hour)", route.Name, rd.Provider, b.Said(), Dollars(b.SpendHour))
			}
			p.Notes = append(p.Notes, n)
		}
	}
	p.Units = append(p.Units, Unit{Key: "balance", Moved: "provider balances: " + strings.Join(said, "; ")})
	return p
}

// ProviderRow is one row of the providers table (where --json): the provider, its balance
// as the poll last read it, the spend an hour measured, and its state: serving, or resting
// until a time and why.
type ProviderRow struct {
	Name      string  `json:"name"`
	Balance   string  `json:"balance"` // dollars and cents rounded up, or "unknown"
	BalanceAt string  `json:"balance_at,omitempty"`
	SpendHour float64 `json:"spend_hour"`
	State     string  `json:"state"`
	Note      string  `json:"note,omitempty"` // why the balance is unknown
}

// ProviderRows is the providers table: a row for each provider the routes name, in name
// order, its balance from the fleet table's properties and its state from its routes' rests
// at now: resting when every route of it rests, serving with the resting ones named when
// some do, else serving; the latest end and the first cause said.
func ProviderRows(routes []Route, fleet *Table, now time.Time) []ProviderRow {
	balances, rests := ProviderBalances(fleet), RouteRests(routes, fleet)
	byProvider := map[string][]Route{}
	for _, r := range routes {
		if r.Provider != "" {
			byProvider[r.Provider] = append(byProvider[r.Provider], r)
		}
	}
	var out []ProviderRow
	for _, name := range slices.Sorted(maps.Keys(byProvider)) {
		b := balances[name]
		row := ProviderRow{Name: name, Balance: "unknown", SpendHour: b.SpendHour, State: "serving", Note: b.Note}
		if !b.At.IsZero() {
			row.BalanceAt = stamp(b.At)
		} else {
			row.Note = "not polled yet"
		}
		if b.Known {
			row.Balance, row.Note = Dollars(b.Balance), ""
		}
		var until time.Time
		var resting []string
		why := ""
		for _, r := range byProvider[name] {
			rest, ok := rests[r.Name]
			if !ok || !rest.Resting(now) {
				continue
			}
			resting = append(resting, r.Name)
			if rest.Until.After(until) {
				until = rest.Until
			}
			if why == "" {
				why = rest.Cause + ": " + rest.Said()
			}
		}
		switch {
		case len(resting) == len(byProvider[name]):
			row.State = "resting until " + untilSaid(until) + " (" + why + ")"
		case len(resting) > 0:
			row.State = "serving; resting " + strings.Join(resting, ", ") + " until " + untilSaid(until) + " (" + why + ")"
		}
		out = append(out, row)
	}
	return out
}

// untilSaid is a rest's end as a line says it (RouteRest.UntilSaid).
func untilSaid(t time.Time) string { return RouteRest{Until: t}.UntilSaid() }

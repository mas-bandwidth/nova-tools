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
// and the provider's rest (PropProviderRest), one of two, each until the poll reads enough
// (OpenUntil), never for a time (the owner, 2026-10-03, 8:18 AM ET: "you'll need to detect
// when a provider runs out of credits, and exclude that provider moving forward, and let me
// know."):
//
//   - OUT OF CREDIT (RestCredit) at a balance at or under zero: the provider has no money.
//     It counts toward stopping the sprint (AllOutOfCredit). It ends at a balance read over
//     zero: over the hour of spend it ends, not over it the provider is low on funds from
//     then, not out;
//   - LOW ON FUNDS (RestBalance) at a balance over zero but not over one hour of the spend:
//     the provider is excluded before it runs dry, but it still has money, so it never
//     counts toward stopping the sprint. It ends at a balance read over that hour of spend.
//
// A provider's rest a refused take began (out of credit, RouteRest.Refused) ends ONLY on
// funded or on a payment the poll sees: a balance read strictly higher than the read before
// it, or than the balance at the refusal (RouteRest.Balance); the read then decides as any
// other (it ends the rest, makes it low on funds, or leaves it out of credit). A balance
// over zero that is not higher never ends it: OpenRouter refuses with 402 a request whose
// estimated cost the balance cannot cover, so a provider that refuses can still read a
// small balance over zero, and ending its rest on that read would rest it again at the next
// refusal, a rest and a judgment every poll. It stays out of credit, its balance beside it
// on the providers table, and it counts toward stopping the sprint.
//
// Both exclude the provider from the deal and open its one judgment (provider_funds.go).
// The spend is measured before the rest began: while the provider rests for its funds it
// spends next to nothing, so the poll keeps the hour of spend it measured then, and the rest
// ends only when a balance is read over that figure (or at funded), never because the
// resting provider's spend fell. The judgment closes at the next tick, and a machine the
// tick stopped because every provider was out of credit is the coordinator's to start.
//
// A provider whose balance cannot be read (no endpoint, no key, an answer that is not the
// shape) is recorded unknown with why; its rest is neither written nor ended on it.

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

// paid says the poll saw a payment to a provider resting since it refused a take: the read
// b is strictly higher than the read before it (was) or than the balance at the refusal; or
// the refusal is from before this process started (rest.At before start), the read before it
// was unknown, and b reads over zero: a payment seen against nothing (a cold start).
func paid(rest RouteRest, was, b ProviderBalance, start time.Time) bool {
	return was.Known && b.Balance > was.Balance ||
		rest.HasBalance && b.Balance > rest.Balance ||
		rest.At.Before(start) && !was.Known && b.Known && b.Balance > 0
}

// Low says a balance calls for a rest of the provider's funds: at or under zero (out of
// credit), or not over one hour of the spend (low on funds).
func (b ProviderBalance) Low() bool { return b.Known && b.Balance <= max(0, b.SpendHour) }

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

// Balance is the poll's step: each read written to its provider's property, and the rest it
// calls for written, changed or ended on the provider (see above), each with a happened note
// to the coordinator.
func Balance(s *Snapshot, r BalanceReq) Plan {
	var p Plan
	balances, rests := ProviderBalances(s.Fleet), ProviderRests(s.Fleet)
	routesOf := map[string][]string{}
	for _, route := range s.Routes {
		routesOf[route.Provider] = append(routesOf[route.Provider], route.Name)
	}
	var said []string
	reads := slices.Clone(r.Reads)
	slices.SortFunc(reads, func(a, b ProviderRead) int { return strings.Compare(a.Provider, b.Provider) })
	for _, rd := range reads {
		was := balances[rd.Provider]
		rest, has := rests[rd.Provider]
		resting := has && rest.Resting(s.Now)
		spend := spendSince(was, rd, s.Now)
		if resting && rest.Funds() {
			spend = was.SpendHour // measured before the rest began: a resting provider spends next to nothing
		}
		b := ProviderBalance{Provider: rd.Provider, Known: rd.Known, Balance: rd.Balance, At: s.Now, HasUsed: rd.HasUsed, Used: rd.Used, Note: rd.Note, SpendHour: spend}
		prop, had := s.Fleet.Prop(PropProviderBalance(rd.Provider))
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: PropProviderBalance(rd.Provider), Value: b.value(), Was: prop, WasAbsent: !had})
		said = append(said, rd.Provider+" "+b.Said())
		if !b.Known {
			continue // an unknown balance writes and ends no rest
		}
		if resting && rest.Refused() && !paid(rest, was, b, s.Start) {
			continue // no payment seen: the refused take's rest holds, out of credit
		}
		cause := ""
		switch {
		case b.Balance <= 0:
			cause = RestCredit
		case b.Low():
			cause = RestBalance
		}
		var next RouteRest
		switch {
		case cause != "" && (!resting || rest.Funds() && rest.Cause != cause):
			next = RouteRest{Provider: rd.Provider, At: s.Now, Until: OpenUntil, Cause: cause,
				Why: oneLine(fmt.Sprintf("low on funds: provider %s balance %s is not over one hour of its spend (%s an hour)", rd.Provider, b.Said(), Dollars(b.SpendHour)))}
			if cause == RestCredit {
				next.Why = oneLine(fmt.Sprintf("out of credit: provider %s balance %s", rd.Provider, b.Said()))
			}
			if resting {
				next.At = rest.At // the same rest, its cause changed: its spend was measured before it began
			}
		case cause == "" && resting && rest.Funds():
			next = rest
			next.Until = s.Now // the rest's end: a refusal launched before it starts no new rest
			next.Why = oneLine(rest.Why + "; ended: balance " + b.Said())
		default:
			continue
		}
		prior, hadRest := s.Fleet.Prop(PropProviderRest(rd.Provider))
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: PropProviderRest(rd.Provider), Value: next.value(), Was: prior, WasAbsent: !hadRest})
		routes := strings.Join(routesOf[rd.Provider], ", ")
		n := happened(NProviderRested, ProviderSubject(rd.Provider), s.Now)
		n.What = fmt.Sprintf("provider %s rested until %s, its routes %s: %s; the deal draws no work card on them until then; nova-sprint routes shows it", rd.Provider, next.UntilSaid(), routes, next.Why)
		if !next.Resting(s.Now) {
			n = happened(NProviderFunded, ProviderSubject(rd.Provider), s.Now)
			n.What = fmt.Sprintf("provider %s serves again, its routes %s: its balance is %s, over one hour of its spend (%s an hour)", rd.Provider, routes, b.Said(), Dollars(b.SpendHour))
		}
		n.To, n.Who = s.Coordinator, r.Who
		p.Notes = append(p.Notes, n)
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

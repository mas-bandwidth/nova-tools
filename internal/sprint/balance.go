package sprint

import (
	"fmt"
	"maps"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/provbalance"
)

// THE BALANCE POLL (nova-tools#5199; the owner, 2026-10-03: "provider out of funds should
// never be a mystery failure."). The run loop reads each provider's balance every
// BalancePollEvery through the seat's key (cmd/nova-sprint balance.go, pkg/provbalance:
// the transport), and this step writes what it read: the fleet table's property
// provider_balance_<provider> (ProviderBalance), with the provider's spend over the last
// hour as the sprint's own cost records measure it (SpendLastHour).
//
// A BALANCE NEVER RESTS A ROUTE (the owner, 2026-10-10, through the seat: "this sounds like
// something the machine should not do. it should raise it to you as a thing to do, but not
// do it automatically."; tla/RouteRest.tla). A balance not over one hour of the spend (Low)
// is the coordinator's judgment, a provider is low on funds (providerConds), with the
// balance, the spend an hour, the hours left and the verbs to act on it (routes rest, wait,
// ack); its routes serve until the coordinator acts. Only the provider itself refusing a
// take for want of credit (a 402) rests it (providerRestsDue), and that rest ends on
// funded, routes wake, or a payment the poll sees: a balance read strictly higher than the
// read before it, or than the balance at the refusal (RouteRest.Balance). A balance over
// zero that is not higher never ends it: OpenRouter refuses with 402 a request whose
// estimated cost the balance cannot cover, so a provider that refuses can still read a
// small balance over zero.
//
// A provider whose balance cannot be read (no endpoint, no key, an answer that is not the
// shape) is recorded unknown with why; its spend is still measured from the records.

// BalancePollEvery is how often the run loop reads the providers' balances.
const BalancePollEvery = 10 * time.Minute

// ProviderRead is one balance the poll read: the provider, the dollars left (Known false
// with Note saying why when there is none to read), and its count of dollars used (HasUsed
// false when it keeps none). It is provbalance's, the transport the poll reads through.
type ProviderRead = provbalance.ProviderRead

// BalanceReq is the reads of one poll.
type BalanceReq struct {
	Reads []ProviderRead
	Who   string
}

// NProviderFunded is the happened note of a rest of a provider's funds the poll ended.
const NProviderFunded = "a provider's routes serve again: its balance is back"

// paid says the poll saw a payment to a provider resting since it refused a take: the read
// b is strictly higher than the read before it (was) or than the balance at the refusal;
// and, when neither mark is known (no read before it, none kept at the refusal: the cold
// start of nova-tools#5220, $942.68 read and the rest held), a read over zero.
func paid(rest RouteRest, was, b ProviderBalance) bool {
	if !was.Known && !rest.HasBalance {
		return b.Known && b.Balance > 0
	}
	return was.Known && b.Balance > was.Balance || rest.HasBalance && b.Balance > rest.Balance
}

// Low says a balance calls for the coordinator's judgment: at or under zero, or not over
// one hour of the spend. It rests nothing (providerConds).
func (b ProviderBalance) Low() bool { return b.Known && b.Balance <= max(0, b.SpendHour) }

// HoursLeft is how long the balance lasts at the spend an hour; ok is false with no spend
// measured (or no balance known).
func (b ProviderBalance) HoursLeft() (float64, bool) {
	if !b.Known || b.SpendHour <= 0 {
		return 0, false
	}
	return max(0, b.Balance) / b.SpendHour, true
}

// SpendWindow is the window the spend an hour is measured over: the last hour.
const SpendWindow = time.Hour

// SpendLastHour is the provider's spend over the hour up to s.Now, in dollars, as the
// sprint's own cost records measure it: every consumer record (a work take, a read run) on
// the work table's primaries that ended in (s.Now-SpendWindow, s.Now], ran on the provider
// (its route's, else its model's), and carries actual_usd, each record counted once by its
// key whatever card holds a copy of it. It is a sum over a full hour, never a rate scaled
// up from a short gap between two reads (the 2026-10-10 rests: $234.82 an hour against a
// measured $25), and never the provider's running total.
func SpendLastHour(s *Snapshot, provider string) float64 {
	if s.Work == nil {
		return 0
	}
	routes := map[string]string{}
	for _, r := range s.Routes {
		routes[r.Name] = r.Provider
	}
	from := s.Now.Add(-SpendWindow)
	seen := map[string]bool{}
	sum := new(big.Rat)
	for _, c := range s.Work.Column(States...) {
		if IsSentinel(c) {
			continue
		}
		for _, con := range CardCostOf(c).Consumers {
			if con.Usage.Actual == "" || seen[con.Key] {
				continue
			}
			at, err := time.Parse(time.RFC3339, con.At)
			if err != nil || !at.After(from) || at.After(s.Now) || !strings.EqualFold(consumerProvider(routes, con), provider) {
				continue
			}
			if usd, err := amountOf(con.Usage.Actual); err == nil && usd != nil {
				seen[con.Key] = true
				sum.Add(sum, usd)
			}
		}
	}
	f, _ := sum.Float64()
	return f
}

// Balance is the poll's step: each read written to its provider's property with the spend
// over the last hour, and a refused take's rest ended on a payment the poll sees, with a
// happened note to the coordinator. It writes no rest (see above).
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
		b := ProviderBalance{Provider: rd.Provider, Known: rd.Known, Balance: rd.Balance, At: s.Now, HasUsed: rd.HasUsed, Used: rd.Used, Note: rd.Note, SpendHour: SpendLastHour(s, rd.Provider)}
		prop, had := s.Fleet.Prop(PropProviderBalance(rd.Provider))
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: PropProviderBalance(rd.Provider), Value: b.value(), Was: prop, WasAbsent: !had})
		said = append(said, rd.Provider+" "+b.Said())
		rest, has := rests[rd.Provider]
		if !b.Known || !has || !rest.Resting(s.Now) || !rest.Refused() || !paid(rest, was, b) {
			continue // the poll ends only a refused take's rest, and only on a payment seen
		}
		next := rest
		next.Until = s.Now // the rest's end: a refusal launched before it starts no new rest
		next.Why = oneLine(rest.Why + "; ended: a payment seen, balance " + b.Said())
		prior, _ := s.Fleet.Prop(PropProviderRest(rd.Provider))
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: PropProviderRest(rd.Provider), Value: next.value(), Was: prior})
		n := happened(NProviderFunded, ProviderSubject(rd.Provider), s.Now)
		n.What = fmt.Sprintf("provider %s serves again, its routes %s: a payment seen, its balance is %s (%s an hour spent over the last hour)", rd.Provider, strings.Join(routesOf[rd.Provider], ", "), b.Said(), Dollars(b.SpendHour))
		n.To, n.Who = s.Coordinator, r.Who
		p.Notes = append(p.Notes, n)
	}
	p.Units = append(p.Units, Unit{Key: "balance", Moved: "provider balances: " + strings.Join(said, "; ")})
	return p
}

// ProviderRow is one row of the providers table (where --json): the provider, its balance
// as the poll last read it, the spend over the last hour, and its state: serving, or resting
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

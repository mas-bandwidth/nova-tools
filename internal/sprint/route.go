package sprint

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/harness"
)

// The card decides the model it runs on.
// Its brief's line 1 names a tier, and the deal resolves
// the tier through the routes nova-config applies to the store (the route kind,
// docs/nova-config/README.md) and the tier's route array (the tier kind: an ordered
// list of route names, a name repeated for more turns). The deal takes the array's
// entry at the tier's rolling index, a uint64 counter modulo the array's length,
// exactly as the deal takes a member (round.go), and moves the index by one for
// each card dealt. The route is written on the work card, so the packet hands the member
// the provider, model, budget and deadline it launches with. A `model:` header line
// pins the card, bypasses the array and leaves the index where it is. A redeal or a
// later attempt leaves out the routes already taken for the card while another
// remains: it takes the next entry that is not left out, and the index moves past
// the entries skipped. The model is tla/RouteIndex.tla; the route guard is
// tla/DirtyTick.tla's.
//
// The rules, in order:
//   - a pinned card runs on its pin (route "pin");
//   - a store with no route at all deals as before, with no route: the member runs
//     its own --model (a twin, a one-machine test);
//   - a frontier card with no pin is not dealt: it is the coordinator's, one
//     judgment for the tier;
//   - a card whose tier (flash when line 1 names none) has no enabled route in its
//     array is not dealt, one judgment for the tier;
//   - a route with first set is drawn before the others of its tier (preferFirst);
//   - a deal to a member draws only a route that member can launch (Launches): a route
//     whose harness is headless (claude, codex, grok) only when the member's control
//     card names it (fleet up --harnesses), else it is walked past as an unserved entry
//     is (fault 10, 2026-10-10; tla/RouteIndex.tla, NeverUnlaunchable).

// FieldHarnesses is a member's control card field: the headless harnesses
// (internal/harness) its PATH holds and the deal may launch there, comma-separated, as
// fleet up --harnesses declares them; absent, the member runs opencode routes only.
const FieldHarnesses = "harnesses"

// Launches says the member can launch a card on route r: any route for a deal that
// names no member (a check of the tier alone), an opencode route anywhere, and a route
// of a headless harness only on a member whose control card names that harness.
func (s *Snapshot) Launches(member string, r Route) bool {
	if member == "" || !harness.IsHeadless(r.Harness) {
		return true
	}
	ctl := s.MemberCtl(member)
	return ctl != nil && slices.Contains(Split(ctl.F(FieldHarnesses)), r.Harness)
}

// HarnessesWhy is why a --harnesses word is refused, "" when it is one: "", HarnessesNone,
// or a comma list of headless harnesses (internal/harness Headless).
func HarnessesWhy(words string) string {
	if words == "" || strings.TrimSpace(words) == HarnessesNone {
		return ""
	}
	for _, h := range Split(words) {
		if !harness.IsHeadless(h) {
			return "--harnesses names " + h + ", which is no headless harness (" + strings.Join(harness.Headless, ", ") + "; opencode runs on every member): a comma list of them, or " + HarnessesNone
		}
	}
	return ""
}

// launchersOf is the members of ms the deal can deal the primary c to: those for which
// routeOf draws a route (each a check, moving no index), wc the work card dealt again
// (nil for a new attempt); when none can, why is the first member's refusal. A member is
// chosen from these, never chosen first and then refused, so a card whose route only
// some members can launch goes to one of them (the second cold read of nova-tools#5576).
func (s *Snapshot) launchersOf(c, wc *Card, ms []string) (out []string, why string) {
	for _, m := range ms {
		if _, _, w, _ := s.routeOf(c, wc, nil, m); w == "" {
			out = append(out, m)
		} else if why == "" {
			why = w
		}
	}
	return out, why
}

// notLaunching is the members of up that cannot launch the work card c on the route it
// carries (Launches): what a move of a dealt card (a member down, the level, the
// rebalance), which keeps its route, avoids as it avoids a member that refused it.
func notLaunching(s *Snapshot, up []string, c *Card) []string {
	r := Route{Harness: c.F(FieldHarness)}
	var out []string
	for _, m := range up {
		if !s.Launches(m, r) {
			out = append(out, m)
		}
	}
	return out
}

// Route is one route of a model tier as the store holds it (config.RouteKey).
type Route struct {
	Name     string `json:"name"`
	Tier     string `json:"tier"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// Harness is the harness a card on it runs under (internal/harness: opencode, the
	// default, or a headless program of the heavy tier); the member launches by it.
	Harness string `json:"harness,omitempty"`
	Tokens  int    `json:"tokens"` // 0 is unmetered
	// USD is the route's dollar budget per card, a canonical decimal ("0.5"), "" for none:
	// the harness's reported cost at which native stops the card (nova-tools #5094).
	USD      string `json:"usd,omitempty"`
	Deadline int    `json:"deadline"` // seconds
	Enabled  bool   `json:"enabled"`
	// First is the route's first field. A route with it set is drawn before the
	// others of its tier (preferFirst; docs/SPEC-SPRINT.md, the deal).
	First bool `json:"first"`
	// Prices is the route's price sheet (cardcost.PricesOf), what a card that ran on it
	// is priced by (cost.go); every price "" when the route has none.
	Prices cardcost.Prices `json:"prices"`
}

// The work card's route fields, written at each deal and redeal: the route taken
// (or RoutePin), the model id the member launches (provider/model), its budget and
// its deadline in seconds; the primary's FieldRoutes is every route taken for it,
// in order, the exclusion's history.
const (
	FieldRoute    = "route"
	FieldModel    = "model"
	FieldTokens   = "tokens"
	FieldUSD      = "usd"
	FieldHarness  = "harness"
	FieldDeadline = "deadline"
	FieldRoutes   = "routes"
	FieldUsage    = "usage"
	RoutePin      = "pin"
	// FieldTier is the primary's tier when the coordinator pinned it to one (rework
	// --tier): every later deal and read of the card draws from it, over its brief's
	// line 1, and the machine never escalates it (cardTier, NextTier); on a work or read
	// card, the tier its route was drawn from, which its packet hands the child or reader,
	// its JOB.md names and its cost record keeps.
	FieldTier = "tier"
	// FieldTierNow is the tier the primary is on, written by every deal on a route:
	// flash first on every card, then the tier the machine escalated it to (NextTier);
	// the tier its brief's line 1 names is its ceiling. Add writes pro on a card whose
	// gate's measured wall is over the flash bound (gate_wall.go).
	FieldTierNow = "tier_now"
)

// PropRouteIndex is the fleet table's property that holds the tier's route index
// (route_index_flash, route_index_pro), beside deal_index: a uint64 counter read
// with the step's tables and written in its batch, guarded on the value read
// (round.go, roundWrites).
func PropRouteIndex(tier string) string { return "route_index_" + tier }

// NNoRoute is the tick's judgment of a tier no route serves, once per tier (its
// subject is the tier's, StreamSubject(TierSubject(tier))), closed when the tier is
// served or no card of it waits.
const NNoRoute = "no route serves the tier"

// TierSubject is the stream word a tier's judgment is filed under: no stream id has
// a colon, so it is never a stream's.
func TierSubject(tier string) string { return "tier:" + tier }

// noRoute is why a primary's tier is not served: "" when a route serves it (or the store
// has no route at all) or a friend up does (tierServed), else the tier it is judged under
// and the sentence. It moves no index.
func (s *Snapshot) noRoute(c *Card) (tier, why string) {
	_, tier, why, byFriend := s.routeOf(c, nil, nil, "")
	if byFriend {
		return tier, ""
	}
	return tier, why
}

// tierServed is the one check of every verb that validates a tier, asked once no enabled
// route of it can be drawn (rested: the tier's routes resting, said first). A tier is
// served by an enabled fleet route or by a friend up (friendDealable: not held, not down)
// whose row lists it (friendTakes; the owner, 2026-10-04: "Fleet flash only; pro to
// friends"). up is the friends up who serve it: its cards are then the friends' deal's,
// never a machine's, and why says so to a machine's deal. With none, why is the refusal,
// naming both ways and the friends whose row lists the tier but who are held or down.
func (s *Snapshot) tierServed(tier string, rested []string) (up []string, why string) {
	var off []string
	for _, f := range s.Friends {
		switch {
		case !friendTakes(s, f, tier):
		case friendDealable(s, f):
			up = append(up, f.Name)
		default:
			off = append(off, f.Name)
		}
	}
	if len(up) > 0 {
		return up, "no enabled route serves tier " + tier + " on a machine; a friend up serves it (" + strings.Join(up, ", ") + "): the friends' deal deals it"
	}
	why = "no enabled route and no up friend serves tier " + tier + ": enable a route (nova-config route add <name> --tier " + tier + " ..., name it in nova-config tier set " + tier + " --routes <name,...>, then nova-config apply) or bring up a friend whose row lists " + tier + "; or pin the card with a model: <provider>/<model> line"
	if len(rested) > 0 {
		why = "every enabled route of tier " + tier + " in its array rests (" + strings.Join(rested, "; ") + ") and no up friend serves it: the deal draws one when its rest ends; or run nova-config route add <name> --tier " + tier + " ..., bring up a friend whose row lists " + tier + ", or pin the card with a model: <provider>/<model> line"
	}
	for _, side := range [][3]string{{PropFleetTiers, "the fleet's", "--fleet-tiers"}, {PropFriendsTiers, "the friends'", "--friends-tiers"}} {
		if !s.sideTakes(side[0], tier) {
			v, _ := s.Work.Prop(side[0])
			why += " (" + side[1] + " tiers are " + v + ": nova-sprint set " + side[2] + " all, or a list with " + tier + ")"
		}
	}
	switch n := len(off); {
	case n > 0 && s.FriendsOff():
		why += " (the friends' work is off: nova-sprint set --friends on)"
	case n == 1:
		why += " (" + off[0] + " serves " + tier + " but is held/down)"
	case n > 1:
		why += " (" + strings.Join(off[:n-1], ", ") + " and " + off[n-1] + " serve " + tier + " but are held/down)"
	}
	return nil, why
}

// tierArray is the tier's route array as the deal reads it: the tier kind's list
// when nova-config applied one, else the tier's enabled routes in name order, each once
// (tla/RouteIndex.tla, Arr).
func (s *Snapshot) tierArray(tier string) []string {
	if a := s.Tiers[tier]; len(a) > 0 {
		return a
	}
	var out []string
	for _, r := range s.Routes {
		if r.Tier == tier && r.Enabled {
			out = append(out, r.Name)
		}
	}
	return out
}

// routeIndex is one tier's rolling index over its route array (tla/RouteIndex.tla,
// ridx): a counter round (round.go) in steps, and the steps each unit moved it.
type routeIndex struct {
	r     *round
	moves roundMoves
}

// routeIndexes are the tiers' route indexes a dealing step moves, by tier.
type routeIndexes map[string]*routeIndex

// routeIndexesOf is the route indexes at the counters the fleet table's properties hold.
func routeIndexesOf(s *Snapshot) routeIndexes {
	out := routeIndexes{}
	for _, t := range tierLadder {
		r := tableRound(s.Fleet, PropRouteIndex(t), nil)
		r.steps = true
		out[t] = &routeIndex{r: r, moves: roundMoves{}}
	}
	return out
}

// write writes where the plan's kept units left each tier's index, in its batch
// (roundWrites; tla/RouteIndex.tla, Deal and Redeal).
func (ri routeIndexes) write(p *Plan) {
	for _, t := range tierLadder {
		roundWrites(p, ri[t].r, ri[t].moves)
	}
}

// preferFirst is the entry a draw takes from the tier's array at counter at.
// A route with first set is drawn before the others of its tier: the walk takes
// the first served entry whose first is set, and an entry without it only when
// none such remains drawable. steps is every entry walked to reach the one
// taken, and the amount the index moves (docs/SPEC-SPRINT.md, the deal).
// RouteFair (tla/RouteIndex.tla) is this walk when no route of the tier has
// first set. hold leaves skip out while another entry remains drawable, as a
// redeal leaves out routes already taken.
func preferFirst(arr []string, served map[string]Route, skip []string, hold bool, at uint64) (r Route, steps uint64, ok bool) {
	n := uint64(len(arr))
	take := func(wantFirst bool) (Route, uint64, bool) {
		for i := uint64(0); i < n; i++ {
			cur, ok := served[arr[(at+i)%n]]
			if !ok || hold && contains(skip, cur.Name) {
				continue
			}
			if cur.First != wantFirst {
				continue
			}
			return cur, i + 1, true
		}
		return Route{}, 0, false
	}
	if r, steps, ok = take(true); ok {
		return r, steps, true
	}
	return take(false)
}

// routeOf is the route fields of one deal of the primary c; wc is the work card dealt
// again (nil for a new attempt), whose own route is left out too. With ri the tier's
// index moves past the entry taken and every entry skipped before it, recorded under
// c's unit; nil reads the index and moves nothing (tla/RouteIndex.tla: Deal, Redeal, Pin).
// An entry that names no enabled route of the tier (a route disabled or removed since
// the array was set) is skipped as an excluded one is. member is the member the deal
// draws for: an entry whose route that member cannot launch (Launches) is skipped as an
// unserved one is, and a tier with no route it can launch is not dealt to it, why naming
// the routes and the member; "" draws every route (a check of the tier alone).
func (s *Snapshot) routeOf(c, wc *Card, ri routeIndexes, member string) (set map[string]string, tier, why string, byFriend bool) {
	m, bad := cardhdr.ReadModel(c.F("brief"))
	tier = drawTier(c, m)
	if tier == "" {
		tier = s.startTier(c, m) // its first deal on a route: flash first, or its grade's pro (decide.go)
	}
	if bad != "" {
		tier = ceilingTier(c, m)
		// a card admitted before the lint read its lines: judged under the tier it
		// names (an unknown word too), else flash's
		return nil, tier, "its brief's model lines: " + bad, false
	}
	if tier == cardhdr.RouteFrontier && m.Pin == "" && len(s.Routes) > 0 {
		return nil, tier, "a frontier card waits for the coordinator: run it, or pin it with a model: <provider>/<model> line", false
	}
	if !s.FleetTakes(tier) {
		// the fleet's tiers leave it out (set --fleet-tiers): no machine draws it, whatever
		// its routes or a model pin (the set is the owner's switch); the friends' deal deals
		// it when a friend up serves it
		up, why := s.tierServed(tier, nil)
		return nil, tier, why, len(up) > 0
	}
	if m.Pin != "" {
		return map[string]string{FieldRoute: RoutePin, FieldModel: m.Pin, FieldTokens: m.Tokens, FieldUSD: "", FieldDeadline: strconv.Itoa(m.Deadline)}, "", "", false
	}
	if len(s.Routes) == 0 {
		return nil, "", "", false
	}
	// a resting route (rule 3, route_rest.go) serves no work card until its rest ends
	served := map[string]Route{}
	var rested, unlaunchable []string
	for _, r := range s.Routes {
		if r.Tier != tier || !r.Enabled {
			continue
		}
		if rest, ok := s.resting(r.Name); ok {
			rested = append(rested, r.Name+" until "+rest.UntilSaid()+": "+rest.Said())
			continue
		}
		if !s.Launches(member, r) {
			unlaunchable = append(unlaunchable, r.Name+" (runs under "+r.Harness+")")
			continue
		}
		served[r.Name] = r
	}
	arr := s.tierArray(tier)
	drawn := Split(c.F(FieldRoutes))
	if wc != nil && wc.F(FieldRoute) != "" {
		drawn = append(drawn, wc.F(FieldRoute))
	}
	// every entry served is left out: the exclusion lapses, as when the tier has one route
	fresh := false
	for _, name := range arr {
		_, ok := served[name]
		fresh = fresh || ok && !contains(drawn, name)
	}
	var at uint64
	if ri != nil {
		at = ri[tier].r.count
	} else {
		v, _ := s.Fleet.Prop(PropRouteIndex(tier))
		at, _ = strconv.ParseUint(v, 10, 64)
	}
	r, steps, ok := preferFirst(arr, served, drawn, fresh, at)
	if ok {
		if ri != nil {
			ri[tier].r.count += steps
			ri[tier].moves[c.ID] = strconv.FormatUint(steps, 10)
		}
		set := map[string]string{FieldRoute: r.Name, FieldModel: r.Provider + "/" + r.Model, FieldTokens: tokensWord(r.Tokens), FieldUSD: r.USD, FieldHarness: r.Harness,
			FieldDeadline: strconv.Itoa(r.Deadline), FieldTier: tier, FieldRoutes: strings.Join(append(Split(c.F(FieldRoutes)), r.Name), ",")}
		if !pinnedTier(c, m) {
			set[FieldTierNow] = tier // the primary is on the tier drawn (cardTier)
		}
		return set, tier, "", false
	}
	up, why := s.tierServed(tier, rested)
	if len(up) == 0 && len(unlaunchable) > 0 {
		why = "member " + member + " can launch no route of tier " + tier + " that serves: " + strings.Join(unlaunchable, ", ") + " and its control card names no such harness (fleet up " + member + " --harnesses <h,...> declares the ones on its PATH); the deal deals it to a member that can"
	}
	return nil, tier, why, len(up) > 0
}

// Flash first on every card (the owner, 2026-10-02, cost rule 1 of nova-tools#5174,
// agreed after "The cost of the sprint at $5,400 seems excessive.": "Flash first on
// every card; pro only on escalation"). The tier a brief's line 1 names is the card's
// ceiling, what it likely needs, never its first deal: every card is dealt on flash, and
// one that reaches its bound below its ceiling is escalated by the machine to the next
// tier of the ladder and dealt a new attempt there, no judgment raised; at its ceiling
// the bound is the coordinator's judgment as before. A frontier card is the
// coordinator's and is never dealt (it climbs no ladder); a pinned model runs on its pin;
// a tier the coordinator pinned (rework --tier, FieldTier) is the card's tier and its
// ceiling both.
var tierLadder = []string{cardhdr.RouteFlash, cardhdr.RoutePro, cardhdr.RouteHeavy}

// cardTier is the tier the primary c is on, what its reads, its read count and its
// escalation go by: the tier its last deal on a route drew (FieldTierNow, which every
// such deal writes: flash first, then the tier the machine escalated it to); before
// any (a card not dealt yet, or a store with no route, where its member runs its own
// model and no deal draws a tier) and for a pinned tier or model or a frontier card,
// its ceiling.
func cardTier(c *Card, m cardhdr.Model) string {
	if t := c.F(FieldTierNow); t != "" && !pinnedTier(c, m) {
		return t
	}
	return ceilingTier(c, m)
}

// drawTier is the tier the deal draws the primary c's route from: its ceiling when its
// tier is pinned (pinnedTier), else the tier the machine escalated it to, else "": its first
// deal on a route, whose tier is the snapshot's to say (startTier: flash first, or pro by
// its grade).
func drawTier(c *Card, m cardhdr.Model) string {
	if pinnedTier(c, m) {
		return ceilingTier(c, m)
	}
	return c.F(FieldTierNow)
}

// pinnedTier says the primary c climbs no ladder: the coordinator pinned its tier
// (FieldTier, rework --tier), its brief pins a model, or it is a frontier card.
func pinnedTier(c *Card, m cardhdr.Model) bool {
	return m.Tier == cardhdr.RouteFrontier || m.Pin != "" || c.F(FieldTier) != ""
}

// ceilingTier is the highest tier the machine escalates the primary c to: the tier the
// coordinator pinned, else the tier its brief's line 1 names, flash when it names none.
func ceilingTier(c *Card, m cardhdr.Model) string {
	if t := c.F(FieldTier); t != "" {
		return t
	}
	if t, ok := criticalTier(c, m); ok {
		return t // a critical card runs on pro from its first deal (weight.go)
	}
	if m.Tier == "" {
		return cardhdr.RouteFlash
	}
	return m.Tier
}

// CardTiers is the tier the primary is on (cardTier) and its ceiling, as `card` prints
// them.
func CardTiers(c *Card) (now, ceiling string) {
	m, _ := cardhdr.ReadModel(c.F("brief"))
	return cardTier(c, m), ceilingTier(c, m)
}

// cardTierOf is the tier the primary c is on (cardTier).
func cardTierOf(c *Card) string {
	now, _ := CardTiers(c)
	return now
}

// DealTier is the one tier resolution of a card every friend decision reads
// (docs/SPEC-SPRINT.md section 1, "One tier for every friend decision",
// friend-deal-one-tier-bb.w2): the friends' deal, the level, the take back of a re-tiered
// card (retierTakeBacks) and the packet's tier (DealtTier, through the primary's tier
// field) all call it, so no two of them can disagree about a card. It is the tier a deal
// draws (dealTierOf), and the dealer's default, flash, when there is no primary.
func (s *Snapshot) DealTier(c *Card) string {
	if c == nil {
		return cardhdr.RouteFlash
	}
	return s.dealTierOf(c)
}

// dealTierOf is the tier a deal of the primary c draws, as the machines' deal draws it
// (routeOf): the tier it is on once a deal drew one (drawTier), its ceiling when its tier
// is pinned, and before its first deal its start tier (startTier: flash first, pro for a
// brief that says pro), never its ceiling; a brief whose model lines cannot be read is
// judged under its ceiling. A card with no tier line is flash. The friends' deal and
// level gate on it (friendTakes), so a card a machine would be dealt on flash is a
// flash friend's too (docs/SPEC-SPRINT.md section 1, a friend's card; 2026-10-06: a
// friend of tier flash sat empty for an hour while every brief whose line 1 said
// tier: heavy was read as heavy for her and dealt on flash to the machines).
func (s *Snapshot) dealTierOf(c *Card) string {
	m, bad := cardhdr.ReadModel(c.F("brief"))
	if bad != "" {
		return ceilingTier(c, m)
	}
	if t := drawTier(c, m); t != "" {
		return t
	}
	return s.startTier(c, m)
}

// NextTier is the tier the primary c escalates to when it reaches its bound: the next tier
// of the ladder above the one it is on, up to its ceiling; "" at its ceiling (a pinned
// model or tier is on its ceiling, cardTier), for brief lines that cannot be read, and in
// a store with no route (a twin: its member runs its own model whatever the tier). One
// function decides it for every bound that escalates: the redeal bound (Deal) and the
// failure bounds.
func (s *Snapshot) NextTier(c *Card) string {
	m, bad := cardhdr.ReadModel(c.F("brief"))
	if bad != "" || len(s.Routes) == 0 {
		return ""
	}
	i, top := slices.Index(tierLadder, cardTier(c, m)), slices.Index(tierLadder, ceilingTier(c, m))
	if i < 0 || i >= top {
		return ""
	}
	return tierLadder[i+1]
}

// readTierOf is the tier a primary's reads are drawn from: the tier of the work
// being read, as the deal draws it (cardTier; the owner, 2026-10-01: "i think
// readers being conservatively the same tier as the work being done seems
// fine?"), raised to the read tier set for its stream or the sprint when that is
// stronger (settings.go; nova-tools#5096 item 27), never lowered. A card that pins
// a model and names no tier is read on flash. A heavy card is read on heavy (on pro under
// the interim rule below: the owner, 2026-10-06 7:41 PM ET, "let pro do it"). A frontier
// card, a tier no route serves, is read on heavy, the strongest tier a route serves: a
// read on pro would be weaker than the writer, which item 27 refuses; the one weaker read is
// the interim rule's, a pro card read on flash while no enabled route serves pro and one
// serves flash (the owner, 2026-10-06: "let flash read pro"). The value returned
// is that collapse: a route drawn for the card is named from it. The deal measures every
// reader, friend or member, against the tier before this collapse (friendReadTier,
// readTierDistance), so a member whose read is drawn lower ranks by how far below it
// really reads (docs/SPEC-SPRINT.md section 6, who reads).
func (s *Snapshot) readTierOf(pr *Card) string {
	m, _ := cardhdr.ReadModel(pr.F("brief"))
	t := cardTier(pr, m)
	if t == cardhdr.RouteFrontier {
		t = cardhdr.RouteHeavy
	}
	if set := s.readTierSetting(pr.Row); set != "" {
		t = stronger(t, set)
	}
	// Workaround (the coordinator, 2026-10-06 7:30 PM ET; the owner: 7:11 PM: "let flash read pro"): a pro
	// read is drawn on flash while no enabled route serves pro and one serves flash, so the
	// fleet's flash readers read pro cards while the pro routes are off. Read cards replace
	// this.
	if t == cardhdr.RoutePro && len(s.Routes) > 0 && !s.tierRouted(t) && s.tierRouted(cardhdr.RouteFlash) {
		t = cardhdr.RouteFlash
	}
	// Workaround (the owner, 2026-10-06 7:41 PM ET: "let pro do it"): a heavy card is read on pro,
	// so the fleet's pro readers are its second reader beside the heavy friend, who is asked
	// before this collapse (friendReadTier). Read cards replace this.
	if t == cardhdr.RouteHeavy {
		t = cardhdr.RoutePro
	}
	return t
}

// readRouteOf is the route fields of one read card the ask creates for the
// primary pr: the read is drawn as a work card is, from the array of pr's read tier
// (readTierOf, written on the read card as FieldTier) at that tier's rolling index, the index moved past the entry
// taken and every entry skipped (an entry naming no enabled route), the moves
// summed under pr's unit, so the deal and the reads of a tier share one
// rotation (tla/RouteIndex.tla, THE READS). The routes avoid (those a read of
// the primary returned on) are left out while another of the tier is served,
// as a redeal leaves out the routes already taken. The tier alone when the store holds
// no route or none serves the tier: the read carries no route and its reader runs its
// own --model.
func (s *Snapshot) readRouteOf(ri routeIndexes, pr *Card, avoid []string) map[string]string {
	tier, key := s.readTierOf(pr), pr.ID
	if len(s.Routes) == 0 || ri[tier] == nil {
		return map[string]string{FieldTier: tier}
	}
	served := map[string]Route{}
	for _, r := range s.Routes {
		if r.Tier == tier && r.Enabled {
			served[r.Name] = r
		}
	}
	arr := s.tierArray(tier)
	other := false
	for _, name := range arr {
		_, ok := served[name]
		other = other || ok && !contains(avoid, name)
	}
	at := ri[tier].r.count
	r, steps, ok := preferFirst(arr, served, avoid, other, at)
	if !ok {
		return map[string]string{FieldTier: tier}
	}
	ri[tier].r.count += steps
	was, _ := strconv.ParseUint(ri[tier].moves[key], 10, 64)
	ri[tier].moves[key] = strconv.FormatUint(was+steps, 10)
	return map[string]string{FieldRoute: r.Name, FieldModel: r.Provider + "/" + r.Model, FieldTokens: tokensWord(r.Tokens), FieldUSD: r.USD, FieldHarness: r.Harness,
		FieldDeadline: strconv.Itoa(r.Deadline), FieldTier: tier}
}

// readRouteMissing is the tier of the primary pr's reads (readTierOf), and why
// no reader can read it: "" when the store holds no route at all (reads run on the
// reader's own model), an enabled route of the tier is in its array, or a reader up
// that brings its own model (a friend's or a bud's, read_route.go) reads the tier:
// a read needs a reader, not a route; the member's side only while the fleet's tiers hold
// the read tier, a friend only while the friends' tiers do (set --fleet-tiers,
// --friends-tiers). The deal's tick raises the tier's judgment for the reads waiting
// (TickDeal, NNoRoute) only when neither serves it.
func (s *Snapshot) readRouteMissing(pr *Card) (tier, why string) {
	tier = s.readTierOf(pr)
	// a member reads only a read tier the fleet's tiers hold (set --fleet-tiers); a friend
	// only one the friends' tiers hold (tierServed, friendTakes)
	if s.FleetTakes(tier) && (s.tierRouted(tier) || s.ownModelReaderUp(tier)) {
		return tier, ""
	}
	up, why := s.tierServed(tier, nil)
	if len(up) > 0 {
		return tier, ""
	}
	return tier, why + "; " + tier + " is the tier of the work its reads read, and no reader up that brings its own model reads it, so its reads have no reader: or start a friend's or a bud's reader of tier " + tier
}

// tokensWord is a route's budget as native's --tokens takes it.
func tokensWord(n int) string {
	if n <= 0 {
		return "unmetered"
	}
	return strconv.Itoa(n)
}

// splitRoute moves the route fields of set (the deal's draw) to the work card's
// changes and the primary's: the history to the primary, the rest to the work card.
func splitRoute(route map[string]string) (work, primary map[string]string) {
	work, primary = map[string]string{}, map[string]string{}
	for k, v := range route {
		if k == FieldRoutes || k == FieldTierNow {
			primary[k] = v
			continue
		}
		work[k] = v
	}
	return work, primary
}

// TierRoutes is how many enabled routes each tier has, as `where` shows it:
// "flash=1 pro=3", "" when the store has none.
func TierRoutes(routes []Route) string {
	n := map[string]int{}
	for _, r := range routes {
		if r.Enabled {
			n[r.Tier]++
		}
	}
	if len(routes) == 0 {
		return ""
	}
	var out []string
	for _, t := range tierLadder {
		out = append(out, fmt.Sprintf("%s=%d", t, n[t]))
	}
	return strings.Join(out, " ")
}

// AttemptLine is one attempt's record as `card <id>` prints it: the work card's
// route, model and tier, its member, when it was dealt, taken and finished, how it
// ended, the head it pushed (head=, "-" when none) and what it spent.
func AttemptLine(wc *Card) string {
	end := "in flight (" + wc.Col + ")"
	switch {
	case wc.F("ok") == "yes":
		end = "ok"
	case wc.F("ok") == "no":
		end = "failed: " + wc.F("report")
		if len(end) > 160 {
			end = end[:160]
		}
	case wc.Col == Withdrawn:
		end = "withdrawn"
	case !wc.Placed():
		end = "retired"
	}
	return fmt.Sprintf("ATTEMPT %s card=%s gen=%s route=%s model=%s tier=%s member=%s dealt=%s taken=%s finished=%s head=%s%s usage=%s end=%s",
		orDash(wc.F("attempt")), wc.ID, orDash(wc.F("gen")), orDash(wc.F(FieldRoute)), orDash(wc.F(FieldModel)), orDash(wc.F(FieldTier)), orDash(wc.F("member")),
		orDash(wc.F("dealt")), orDash(wc.F("taken")), orDash(wc.F("finished")), orDash(PushedHead(wc)), DecidedWords(wc.F(FieldDecided), wc.F(FieldDecidedUsed) == "yes"), orDash(wc.F(FieldUsage)), end)
}

// DecidedWords is a work card's attempt decision as `card` prints it on its ATTEMPT line,
// " decided=<class>:<p>", with " decided_used=yes" when it routed the finish; "" when the
// card carries none.
func DecidedWords(line string, used bool) string {
	d, ok := decide.ParseDecided(line)
	if !ok {
		return ""
	}
	w := " decided=" + d.Value + ":" + strconv.FormatFloat(d.P, 'f', 2, 64)
	if used {
		w += " decided_used=yes"
	}
	return w
}

// ProviderTake is the record of one take of a work card the provider failed, kept on the
// card beside the takes after it (FieldProviderTake plus the take's number, the card's
// redeals when it ended plus one): the route and model it ran on, the member, when it
// ended, what it spent and the error line. `card <id>` prints one ATTEMPT line for each,
// and the route stats count it against its route. Taken is when the member took the card for
// the take, its child launched ("" in a record written before it was kept): a provider's
// refusal is read against the rest window its launch fell in (provider_funds.go).
type ProviderTake struct{ Route, Model, Member, Finished, Usage, Error, Taken string }

// String is the take as the card's field holds it: tab separated, the line, whose tabs are
// blanks, before the launch's time, so a record written before Taken reads as it did.
func (t ProviderTake) String() string {
	v := strings.Join([]string{t.Route, t.Model, t.Member, t.Finished, t.Usage, strings.ReplaceAll(t.Error, "\t", " ")}, "\t")
	if t.Taken != "" {
		v += "\t" + t.Taken
	}
	return v
}

// ProviderTakes is the takes of the work card the provider failed, in the order they ended,
// each with its number.
func ProviderTakes(wc *Card) (takes []ProviderTake, numbers []int) {
	for n := 1; n <= MaxRedeals+1; n++ {
		v := wc.F(FieldProviderTake + itoa(n))
		if v == "" {
			continue
		}
		takes, numbers = append(takes, parseTake(v)), append(numbers, n)
	}
	return takes, numbers
}

// AttemptLines is the work card's lines as `card <id>` prints them: one for each take of it
// that ended with no work to judge (end=provider failure: the line, or end=no result: the
// line, with the route, model, member and usage of that take), one for each launch refused at staging (end=staging refused: the
// reason, with its generation and member), then the card's own (AttemptLine), the take it is
// on or ended.
func AttemptLines(wc *Card) []string {
	var out []string
	takes, numbers := ProviderTakes(wc)
	for i, t := range takes {
		// a take whose child left no result carries its own kind in its line (takeEnded)
		end := cardhdr.EndProvider + ": " + t.Error
		if strings.HasPrefix(t.Error, cardhdr.EndNoResult+":") {
			end = t.Error
		}
		out = append(out, fmt.Sprintf("ATTEMPT %s card=%s take=%d route=%s model=%s member=%s finished=%s usage=%s end=%s",
			orDash(wc.F("attempt")), wc.ID, numbers[i], orDash(t.Route), orDash(t.Model), orDash(t.Member), orDash(t.Finished), orDash(t.Usage), end))
	}
	// each launch refused at staging (StagingTakes), at the generation it was dealt
	staged, gens := StagingTakes(wc)
	for i, t := range staged {
		out = append(out, fmt.Sprintf("ATTEMPT %s card=%s gen=%d route=%s model=%s member=%s finished=%s usage=%s end=%s: %s",
			orDash(wc.F("attempt")), wc.ID, gens[i], orDash(t.Route), orDash(t.Model), orDash(t.Member), orDash(t.Finished), orDash(t.Usage), cardhdr.EndStaging, t.Error))
	}
	return append(out, AttemptLine(wc))
}

// RouteStat is one route's record over the work cards dealt on it. A pinned
// model is a row of its own, Pinned, named pin:<model>.
type RouteStat struct {
	Route    Route  `json:"route"`
	Pinned   bool   `json:"pinned,omitempty"`
	Attempts int    `json:"attempts"`
	OK       int    `json:"ok"`
	Failed   int    `json:"failed"`
	Provider int    `json:"provider_failures"`
	MeanWall string `json:"mean_wall"` // taken to finished, over the finished; "-" when none
	// RestedUntil is when the route's rest ends while it rests (rule 3, route_rest.go):
	// RFC3339, "" when it does not rest; `routes` fills it at its clock.
	RestedUntil string `json:"rested_until,omitempty"`
	// RestedFor is why it rests (RouteRest.Cause and its words), and Balance and
	// BalanceAt its provider's balance as the balance poll last read it (balance.go):
	// dollars and cents rounded up, or "unknown"; "" before any read.
	RestedFor string `json:"rested_for,omitempty"`
	Balance   string `json:"balance,omitempty"`
	BalanceAt string `json:"balance_at,omitempty"`
}

// RouteStats is every route's record over the fleet table's work cards, the
// routes in name order, then each pinned model (pin:<model>) and each route the
// store no longer holds, as the cards name them, in the order first met.
func RouteStats(routes []Route, fleet *Table) []RouteStat {
	at := map[string]int{}
	out := make([]RouteStat, 0, len(routes))
	for _, r := range routes {
		at[r.Name] = len(out)
		out = append(out, RouteStat{Route: r})
	}
	walls := map[string][]time.Duration{}
	// statOf is the row of a route's name, made on first meeting.
	statOf := func(name string, pinned bool, model string) *RouteStat {
		i, ok := at[name]
		if !ok {
			i = len(out)
			at[name] = i
			provider, m, _ := strings.Cut(model, "/")
			out = append(out, RouteStat{Route: Route{Name: name, Provider: provider, Model: m}, Pinned: pinned})
		}
		return &out[i]
	}
	for _, c := range fleet.Column(Ready, Working, DoneOK, DoneFailed, DoneDefect, Withdrawn) {
		name := c.F(FieldRoute)
		if name == "" {
			continue
		}
		pinned := name == RoutePin
		if pinned {
			name = RoutePin + ":" + c.F(FieldModel)
		}
		// each take of the card the provider failed is an attempt of the route it ran on,
		// failed, and counted apart (ProviderTake; the card was dealt again after it)
		takes, _ := ProviderTakes(c)
		for _, t := range takes {
			if t.Route == "" {
				continue
			}
			f, model := name, c.F(FieldModel)
			if t.Route != RoutePin {
				f = t.Route
				if f != name {
					model = "" // a route the card was drawn before: its row is the store's
				}
			}
			st := statOf(f, pinned && t.Route == RoutePin, model)
			st.Attempts++
			st.Failed++
			st.Provider++
		}
		if c.Col == Withdrawn && c.F(FieldProviderError) != "" {
			continue // dealt again at the next deal: no take of it is on this route now
		}
		if c.Col == DoneDefect {
			continue // a brief defect (brief_defect.go): the brief's, never the route's
		}
		st := statOf(name, pinned, c.F(FieldModel))
		st.Attempts++
		switch c.F("ok") {
		case "yes":
			st.OK++
		case "no":
			st.Failed++
		}
		t0, e0 := time.Parse(time.RFC3339, c.F("taken"))
		t1, e1 := time.Parse(time.RFC3339, c.F("finished"))
		if e0 == nil && e1 == nil && !t1.Before(t0) {
			walls[name] = append(walls[name], t1.Sub(t0))
		}
	}
	for i := range out {
		out[i].MeanWall = "-"
		if ws := walls[out[i].Route.Name]; len(ws) > 0 {
			var sum time.Duration
			for _, w := range ws {
				sum += w
			}
			out[i].MeanWall = (sum / time.Duration(len(ws))).Round(time.Second).String()
		}
	}
	return out
}

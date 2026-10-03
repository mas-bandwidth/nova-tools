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
)

// The card decides the model it runs on (the owner, 2026-10-01: "the card should
// determine the model used"). Its brief's line 1 names a tier, and the deal resolves
// the tier through the routes nova-config applies to the store (the route kind,
// docs/nova-config/README.md) and the tier's route array (the tier kind: an ordered
// list of route names, a name repeated for more turns). The deal takes the array's
// entry at the tier's rolling index, a uint64 counter modulo the array's length,
// exactly as the deal takes a member (round.go), and moves the index by one for
// each card dealt (the owner, 2026-10-01: "model routing to use the same uint64
// modulo"); the route is written on the work card, so the packet hands the member
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
//   - a frontier card with no pin is not dealt: it is the coordinator's (the owner,
//     2026-09-30), one judgment for the tier;
//   - a card whose tier (flash when line 1 names none) has no enabled route in its
//     array is not dealt, one judgment for the tier.

// Route is one route of a model tier as the store holds it (config.RouteKey).
type Route struct {
	Name     string `json:"name"`
	Tier     string `json:"tier"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Tokens   int    `json:"tokens"` // 0 is unmetered
	// USD is the route's dollar budget per card, a canonical decimal ("0.5"), "" for none:
	// the harness's reported cost at which native stops the card (nova-tools #5094).
	USD      string `json:"usd,omitempty"`
	Deadline int    `json:"deadline"` // seconds
	Enabled  bool   `json:"enabled"`
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
	// the tier its brief's line 1 names is its ceiling.
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

// noRoute is why a primary has no route: "" when it has one (or the store has no
// route at all), else the tier it is judged under and the sentence. It moves no index.
func (s *Snapshot) noRoute(c *Card) (tier, why string) {
	_, tier, why = s.routeOf(c, nil, nil)
	return tier, why
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

// routeOf is the route fields of one deal of the primary c; wc is the work card dealt
// again (nil for a new attempt), whose own route is left out too. With ri the tier's
// index moves past the entry taken and every entry skipped before it, recorded under
// c's unit; nil reads the index and moves nothing (tla/RouteIndex.tla: Deal, Redeal, Pin).
// An entry that names no enabled route of the tier (a route disabled or removed since
// the array was set) is skipped as an excluded one is.
func (s *Snapshot) routeOf(c, wc *Card, ri routeIndexes) (set map[string]string, tier, why string) {
	m, bad := cardhdr.ReadModel(c.F("brief"))
	tier = drawTier(c, m)
	if tier == "" {
		tier = s.startTier(c, m) // its first deal on a route: flash first, or its grade's pro (decide.go)
	}
	if bad != "" {
		tier = ceilingTier(c, m)
		// a card admitted before the lint read its lines: judged under the tier it
		// names (an unknown word too), else flash's
		return nil, tier, "its brief's model lines: " + bad
	}
	if m.Pin != "" {
		return map[string]string{FieldRoute: RoutePin, FieldModel: m.Pin, FieldTokens: m.Tokens, FieldUSD: "", FieldDeadline: strconv.Itoa(m.Deadline)}, "", ""
	}
	if len(s.Routes) == 0 {
		return nil, "", ""
	}
	if tier == cardhdr.RouteFrontier {
		return nil, tier, "a frontier card waits for the coordinator: run it, or pin it with a model: <provider>/<model> line"
	}
	// a resting route (rule 3, route_rest.go) serves no work card until its rest ends
	served := map[string]Route{}
	var rested []string
	for _, r := range s.Routes {
		if r.Tier != tier || !r.Enabled {
			continue
		}
		if rest, ok := s.resting(r.Name); ok {
			rested = append(rested, r.Name+" until "+rest.UntilSaid()+": "+rest.Said())
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
	n := uint64(len(arr))
	for i := uint64(0); i < n; i++ {
		r, ok := served[arr[(at+i)%n]]
		if !ok || fresh && contains(drawn, r.Name) {
			continue
		}
		if ri != nil {
			ri[tier].r.count += i + 1
			ri[tier].moves[c.ID] = strconv.FormatUint(i+1, 10)
		}
		set := map[string]string{FieldRoute: r.Name, FieldModel: r.Provider + "/" + r.Model, FieldTokens: tokensWord(r.Tokens), FieldUSD: r.USD,
			FieldDeadline: strconv.Itoa(r.Deadline), FieldTier: tier, FieldRoutes: strings.Join(append(Split(c.F(FieldRoutes)), r.Name), ",")}
		if !pinnedTier(c, m) {
			set[FieldTierNow] = tier // the primary is on the tier drawn (cardTier)
		}
		return set, tier, ""
	}
	if len(rested) > 0 {
		return nil, tier, "every enabled route of tier " + tier + " in its array rests (" + strings.Join(rested, "; ") + "): the deal draws one when its rest ends; or run nova-config route add <name> --tier " + tier + " ..., or pin the card with a model: <provider>/<model> line"
	}
	return nil, tier, "no enabled route serves tier " + tier + ": run nova-config route add <name> --tier " + tier + " ..., name it in nova-config tier set " + tier + " --routes <name,...>, then nova-config apply; or pin the card with a model: <provider>/<model> line"
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
var tierLadder = []string{cardhdr.RouteFlash, cardhdr.RoutePro}

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
	if m.Tier == "" {
		return cardhdr.RouteFlash
	}
	return m.Tier
}

// CardTiers is the tier the primary is on (cardTier) and its ceiling, as `card` prints
// them.
func CardTiers(c *Card) (now, ceiling string) {
	m, _ := cardhdr.ReadModel(c.F("brief"))
	ceiling = ceilingTier(c, m)
	if c.F(FieldTierNow) == "" && !pinnedTier(c, m) {
		if g, ok := decide.ParseDecided(c.F(FieldGrade)); ok && g.Value == decide.GradePro {
			return cardhdr.RoutePro, ceiling
		}
		return cardhdr.RouteFlash, ceiling
	}
	return cardTier(c, m), ceiling
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
// a model and names no tier is read on flash; a frontier card, a tier no route
// serves, is read on pro.
func (s *Snapshot) readTierOf(pr *Card) string {
	m, _ := cardhdr.ReadModel(pr.F("brief"))
	t := cardTier(pr, m)
	if t == cardhdr.RouteFrontier {
		t = cardhdr.RoutePro
	}
	if set := s.readTierSetting(pr.Row); set != "" {
		t = stronger(t, set)
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
	n := uint64(len(arr))
	at := ri[tier].r.count
	for i := uint64(0); i < n; i++ {
		r, ok := served[arr[(at+i)%n]]
		if !ok || other && contains(avoid, r.Name) {
			continue
		}
		ri[tier].r.count += i + 1
		was, _ := strconv.ParseUint(ri[tier].moves[key], 10, 64)
		ri[tier].moves[key] = strconv.FormatUint(was+i+1, 10)
		return map[string]string{FieldRoute: r.Name, FieldModel: r.Provider + "/" + r.Model, FieldTokens: tokensWord(r.Tokens), FieldUSD: r.USD,
			FieldDeadline: strconv.Itoa(r.Deadline), FieldTier: tier}
	}
	return map[string]string{FieldTier: tier}
}

// readRouteMissing is the tier of the primary pr's reads (readTierOf), and why
// no read card of it can be drawn a route of that tier: "" when the store holds
// no route at all (reads run on the reader's own model) or an enabled route of
// the tier is in its array. The deal's tick raises the tier's judgment for the
// reads waiting (TickDeal, NNoRoute), as it does for work cards.
func (s *Snapshot) readRouteMissing(pr *Card) (tier, why string) {
	tier = s.readTierOf(pr)
	if len(s.Routes) == 0 {
		return tier, ""
	}
	for _, name := range s.tierArray(tier) {
		for _, r := range s.Routes {
			if r.Name == name && r.Tier == tier && r.Enabled {
				return tier, ""
			}
		}
	}
	return tier, "no enabled route serves tier " + tier + ", the tier of the work its reads read, so its reads have no route: run nova-config route add <name> --tier " + tier + " ..., name it in nova-config tier set " + tier + " --routes <name,...>, then nova-config apply"
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
	for _, t := range []string{cardhdr.RouteFlash, cardhdr.RoutePro} {
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
	for _, c := range fleet.Column(Ready, Working, DoneOK, DoneFailed, Withdrawn) {
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

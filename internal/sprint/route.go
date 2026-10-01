package sprint

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
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
	Tokens   int    `json:"tokens"`   // 0 is unmetered
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
	FieldDeadline = "deadline"
	FieldRoutes   = "routes"
	FieldUsage    = "usage"
	RoutePin      = "pin"
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
	for _, t := range []string{cardhdr.RouteFlash, cardhdr.RoutePro} {
		r := tableRound(s.Fleet, PropRouteIndex(t), nil)
		r.steps = true
		out[t] = &routeIndex{r: r, moves: roundMoves{}}
	}
	return out
}

// write writes where the plan's kept units left each tier's index, in its batch
// (roundWrites; tla/RouteIndex.tla, Deal and Redeal).
func (ri routeIndexes) write(p *Plan) {
	for _, t := range []string{cardhdr.RouteFlash, cardhdr.RoutePro} {
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
	if bad != "" {
		// a card admitted before the lint read its lines: judged under the tier it
		// names (an unknown word too), else flash's
		tier = m.Tier
		if tier == "" {
			tier = cardhdr.RouteFlash
		}
		return nil, tier, "its brief's model lines: " + bad
	}
	if m.Pin != "" {
		return map[string]string{FieldRoute: RoutePin, FieldModel: m.Pin, FieldTokens: m.Tokens, FieldDeadline: strconv.Itoa(m.Deadline)}, "", ""
	}
	if len(s.Routes) == 0 {
		return nil, "", ""
	}
	tier = m.Tier
	if tier == "" {
		tier = cardhdr.RouteFlash
	}
	if tier == cardhdr.RouteFrontier {
		return nil, tier, "a frontier card waits for the coordinator: run it, or pin it with a model: <provider>/<model> line"
	}
	served := map[string]Route{}
	for _, r := range s.Routes {
		if r.Tier == tier && r.Enabled {
			served[r.Name] = r
		}
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
		return map[string]string{FieldRoute: r.Name, FieldModel: r.Provider + "/" + r.Model, FieldTokens: tokensWord(r.Tokens),
			FieldDeadline: strconv.Itoa(r.Deadline), FieldRoutes: strings.Join(append(Split(c.F(FieldRoutes)), r.Name), ",")}, tier, ""
	}
	return nil, tier, "no enabled route serves tier " + tier + ": run nova-config route add <name> --tier " + tier + " ..., name it in nova-config tier set " + tier + " --routes <name,...>, then nova-config apply; or pin the card with a model: <provider>/<model> line"
}

// DefaultReaderTier is the tier a read card's route is drawn from when the
// sprint row names none.
const DefaultReaderTier = cardhdr.RoutePro

// ReadTier is the tier the ask draws read cards' routes from: the sprint row's
// reader_tier, else DefaultReaderTier.
func (s *Snapshot) ReadTier() string {
	if s.ReaderTier != "" {
		return s.ReaderTier
	}
	return DefaultReaderTier
}

// readRouteOf is the route fields of one read card the ask creates for the
// unit key: the read is drawn as a work card is, from the reader tier's array
// at that tier's rolling index, the index moved past the entry taken and every
// entry skipped (an entry naming no enabled route), the moves summed under the
// unit's key, so the deal and the reads of a tier share one rotation
// (tla/RouteIndex.tla, THE READS). nil when the store holds no route or none
// serves the reader tier: the read carries no route and its reader runs its
// own --model.
func (s *Snapshot) readRouteOf(ri routeIndexes, key string) map[string]string {
	tier := s.ReadTier()
	if len(s.Routes) == 0 || ri[tier] == nil {
		return nil
	}
	served := map[string]Route{}
	for _, r := range s.Routes {
		if r.Tier == tier && r.Enabled {
			served[r.Name] = r
		}
	}
	arr := s.tierArray(tier)
	n := uint64(len(arr))
	at := ri[tier].r.count
	for i := uint64(0); i < n; i++ {
		r, ok := served[arr[(at+i)%n]]
		if !ok {
			continue
		}
		ri[tier].r.count += i + 1
		was, _ := strconv.ParseUint(ri[tier].moves[key], 10, 64)
		ri[tier].moves[key] = strconv.FormatUint(was+i+1, 10)
		return map[string]string{FieldRoute: r.Name, FieldModel: r.Provider + "/" + r.Model, FieldTokens: tokensWord(r.Tokens),
			FieldDeadline: strconv.Itoa(r.Deadline)}
	}
	return nil
}

// readRouteMissing is the reader tier, and why no read card can be drawn a route
// of it: "" when the store holds no route at all (reads run on the reader's own
// model) or an enabled route of the tier is in its array. The deal's tick raises
// the tier's judgment for the reads waiting (TickDeal, NNoRoute), as it does for
// work cards.
func (s *Snapshot) readRouteMissing() (tier, why string) {
	tier = s.ReadTier()
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
	return tier, "no enabled route serves tier " + tier + ", the reader tier (the sprint row's reader_tier), so its reads have no route: run nova-config route add <name> --tier " + tier + " ..., name it in nova-config tier set " + tier + " --routes <name,...>, then nova-config apply; or nova-config sprint set --reader_tier <tier>"
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
		if k == FieldRoutes {
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
// route and model, its member, when it was dealt, taken and finished, how it
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
	return fmt.Sprintf("ATTEMPT %s card=%s gen=%s route=%s model=%s member=%s dealt=%s taken=%s finished=%s head=%s usage=%s end=%s",
		orDash(wc.F("attempt")), wc.ID, orDash(wc.F("gen")), orDash(wc.F(FieldRoute)), orDash(wc.F(FieldModel)), orDash(wc.F("member")),
		orDash(wc.F("dealt")), orDash(wc.F("taken")), orDash(wc.F("finished")), orDash(PushedHead(wc)), orDash(wc.F(FieldUsage)), end)
}

// ProviderTake is the record of one take of a work card the provider failed, kept on the
// card beside the takes after it (FieldProviderTake plus the take's number, the card's
// redeals when it ended plus one): the route and model it ran on, the member, when it
// ended, what it spent and the error line. `card <id>` prints one ATTEMPT line for each,
// and the route stats count it against its route.
type ProviderTake struct{ Route, Model, Member, Finished, Usage, Error string }

// String is the take as the card's field holds it: tab separated, the line last.
func (t ProviderTake) String() string {
	return strings.Join([]string{t.Route, t.Model, t.Member, t.Finished, t.Usage, strings.ReplaceAll(t.Error, "\t", " ")}, "\t")
}

// ProviderTakes is the takes of the work card the provider failed, in the order they ended,
// each with its number.
func ProviderTakes(wc *Card) (takes []ProviderTake, numbers []int) {
	for n := 1; n <= MaxRedeals+1; n++ {
		v := wc.F(FieldProviderTake + itoa(n))
		if v == "" {
			continue
		}
		f := append(strings.SplitN(v, "\t", 6), "", "", "", "", "", "")
		takes, numbers = append(takes, ProviderTake{Route: f[0], Model: f[1], Member: f[2], Finished: f[3], Usage: f[4], Error: f[5]}), append(numbers, n)
	}
	return takes, numbers
}

// AttemptLines is the work card's lines as `card <id>` prints them: one for each take of it
// the provider failed (end=provider failure: the line, with the route, model, member and
// usage of that take), one for each launch refused at staging (end=staging refused: the
// reason, with its generation and member), then the card's own (AttemptLine), the take it is
// on or ended.
func AttemptLines(wc *Card) []string {
	var out []string
	takes, numbers := ProviderTakes(wc)
	for i, t := range takes {
		out = append(out, fmt.Sprintf("ATTEMPT %s card=%s take=%d route=%s model=%s member=%s finished=%s usage=%s end=%s: %s",
			orDash(wc.F("attempt")), wc.ID, numbers[i], orDash(t.Route), orDash(t.Model), orDash(t.Member), orDash(t.Finished), orDash(t.Usage), cardhdr.EndProvider, t.Error))
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

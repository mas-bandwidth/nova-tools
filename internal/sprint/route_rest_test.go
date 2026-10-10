package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rule's window (tla/RouteRest.tla): the last RouteRestWindow ended takes on a route
// after its last rest began, in the order they ended. RouteRestAfter takes in it the
// provider failed TRANSIENTLY (a rate limit, a 5xx, a timeout) rest the route from the clock
// for RouteRestFor; a take that ended with no result never counts (it is the model's output,
// not the provider's: the card's attempt and the route's ok% carry it), nor does a provider
// failure of another class, an older end, an end before the last rest or an ok end; a route
// resting now is not rested again.
func TestRestsDueCountsTransientProviderFailuresNeverNoResults(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2030, 1, 2, 3, 0, 0, 0, time.UTC)
	at := func(m int) string { return stamp(t0.Add(time.Duration(m) * time.Minute)) }
	ended := func(m int, err string) string { return ProviderTake{Route: "a", Finished: at(m), Error: err}.String() }
	noResult := func(m int) string { return ended(m, "no result: no RESULT.md shape") }
	limited := func(m int) string { return ended(m, "provider: class=rate-limited status=429 msg=Rate limit exceeded") }
	outage := func(m int) string {
		return ended(m, "provider: class=provider-5xx status=503 msg=upstream unavailable")
	}
	slow := func(m int) string { return ended(m, "provider: class=timeout status=- msg=no answer in 600s") }
	other := func(m int) string {
		return ended(m, "provider: class=other status=- msg=the harness recorded no cause")
	}
	// fleet is the table of the cards: each a withdrawn card whose take on route a ended so,
	// or (ok) a card done ok on route a at that minute
	fleet := func(takes map[string]string, ok map[string]int) *Table {
		f := NewTable(Fleet)
		f.SetRows([]string{"m1"})
		for id, v := range takes {
			f.Put(&Card{ID: id, Row: "m1", Col: Withdrawn, Fields: map[string]string{FieldProviderTake + "1": v, FieldRoute: "b"}})
		}
		for id, m := range ok {
			f.Put(&Card{ID: id, Row: "m1", Col: DoneOK, Fields: map[string]string{FieldRoute: "a", "finished": at(m), "ok": "yes"}})
		}
		return f
	}
	oks := func(from, n int) map[string]int {
		out := map[string]int{}
		for i := range n {
			out["ok"+itoa(from+i)] = from + i
		}
		return out
	}
	was := func(from, to int) string { return at(from) + " " + at(to) + " x,y,z " + RestProvider }
	for _, tc := range []struct {
		name  string
		takes map[string]string
		ok    map[string]int
		rest  string // the property's value before
		cards []string
	}{
		{"three 429s", map[string]string{"c1": limited(1), "c2": limited(2), "c3": limited(3)}, nil, "", []string{"c1", "c2", "c3"}},
		{"a 429, a 5xx and a timeout", map[string]string{"c1": limited(1), "c2": outage(2), "c3": slow(3)}, nil, "", []string{"c1", "c2", "c3"}},
		{"three no results never rest", map[string]string{"c1": noResult(1), "c2": noResult(2), "c3": noResult(3)}, nil, "", nil},
		{"ten no results never rest", map[string]string{"c1": noResult(1), "c2": noResult(2), "c3": noResult(3), "c4": noResult(4), "c5": noResult(5), "c6": noResult(6), "c7": noResult(7), "c8": noResult(8), "c9": noResult(9), "c10": noResult(10)}, nil, "", nil},
		{"a no result is not the provider's", map[string]string{"c1": limited(1), "c2": noResult(2), "c3": outage(3)}, nil, "", nil},
		{"a failure of another class does not count", map[string]string{"c1": limited(1), "c2": other(2), "c3": outage(3)}, nil, "", nil},
		{"two", map[string]string{"c1": limited(1), "c2": limited(2)}, oks(3, 5), "", nil},
		{"three among ok ends, within ten", map[string]string{"c1": limited(1), "c2": outage(5), "c3": slow(9)}, oks(2, 3), "", []string{"c1", "c2", "c3"}},
		{"the first slid out of the window", map[string]string{"c1": limited(1), "c2": limited(20), "c3": limited(21)}, oks(2, 9), "", nil},
		{"ends before the last rest do not count", map[string]string{"c1": limited(1), "c2": limited(2), "c3": limited(40)}, nil, was(10, 30), nil},
		{"resting now", map[string]string{"c1": limited(41), "c2": limited(42), "c3": limited(43)}, nil, was(40, 70), nil},
		{"a rest ended, three after it", map[string]string{"c1": limited(31), "c2": limited(32), "c3": limited(33)}, nil, was(0, 30), []string{"c1", "c2", "c3"}},
		{"a retired no-result rest holds nothing", map[string]string{"c1": noResult(41)}, nil, at(40) + " " + at(70) + " x,y,z", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &Snapshot{Now: t0.Add(45 * time.Minute), Fleet: fleet(tc.takes, tc.ok), Routes: []Route{{Name: "a", Tier: "pro", Enabled: true}}}
			if tc.rest != "" {
				s.Fleet.SetProps(map[string]string{PropRule3Rest(""): "a " + tc.rest})
			}
			due := RestsDue(s)
			if tc.cards == nil {
				assert.Empty(t, due)
				return
			}
			assert.Equal(t, []RouteRest{{Route: "a", At: s.Now, Until: s.Now.Add(RouteRestFor), Cards: tc.cards, Cause: RestProvider}}, due)
			s.Fleet.SetProps(map[string]string{PropRule3Rest(""): "a " + due[0].value()})
			assert.Equal(t, due[0], RouteRests(s.Routes, s.Fleet)["a"], "the property reads back as written")
		})
	}
}

// A rest a retired rule wrote holds no route from the moment the code that reads it runs:
// rule 3's no-result rest (flash-deepseek41-direct, 2026-10-10 03:38Z: "no-result: its
// children ended with no result"), and a balance poll's low-on-funds rest (openrouter, every
// route, since 01:54:32Z: "is not over one hour of its spend ($234.82 an hour)") or its out
// of credit read off a balance, which names no refused card. A refused take's credit rest, a
// key's, a transient provider rest and the coordinator's still hold.
func TestARetiredRuleRestsNoRoute(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 10, 10, 3, 38, 45, 0, time.UTC)
	routes := []Route{
		{Name: "flash-deepseek41-direct", Tier: "pro", Provider: "deepseek", Enabled: true},
		{Name: "flash-glm53-openrouter", Tier: "flash", Provider: "openrouter", Enabled: true},
		{Name: "pro-grok47-openrouter", Tier: "pro", Provider: "openrouter", Enabled: true},
	}
	lowRest := "2026-10-10T01:54:32Z open - balance low on funds: provider openrouter balance $190.77 at 2026-10-10T01:54:32Z is not over one hour of its spend ($234.82 an hour)"
	for _, tc := range []struct {
		name    string
		props   map[string]string
		resting []string
	}{
		{"the night's two rests", map[string]string{
			PropRule3Rest("deepseek"):      "flash-deepseek41-direct 2026-10-10T03:38:45Z 2026-10-10T04:08:45Z c1,c2,c3 no-result",
			PropProviderRest("openrouter"): lowRest,
		}, nil},
		{"a rule-3 line written before the cause", map[string]string{PropRule3Rest("deepseek"): "flash-deepseek41-direct 2026-10-10T03:38:45Z 2026-10-10T04:08:45Z c1,c2,c3"}, nil},
		{"out of credit read off a balance", map[string]string{PropProviderRest("openrouter"): "2026-10-10T01:54:32Z open - out-of-credit out of credit: provider openrouter balance -$0.51"}, nil},
		{"a refused take's credit rest holds", map[string]string{PropProviderRest("openrouter"): "2026-10-10T01:54:32Z open c9 out-of-credit out of credit: provider openrouter refused card c9"}, []string{"flash-glm53-openrouter", "pro-grok47-openrouter"}},
		{"a transient provider rest holds", map[string]string{PropRule3Rest("deepseek"): "flash-deepseek41-direct 2026-10-10T03:38:45Z 2026-10-10T04:08:45Z c1,c2,c3 provider"}, []string{"flash-deepseek41-direct"}},
		{"the coordinator's rest holds", map[string]string{PropProviderRest("openrouter"): "2026-10-10T03:38:45Z open - coordinator rested by seat: low"}, []string{"flash-glm53-openrouter", "pro-grok47-openrouter"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := NewTable(Fleet)
			f.SetProps(tc.props)
			var got []string
			for name, r := range RouteRests(routes, f) {
				if r.Resting(t0.Add(time.Minute)) {
					got = append(got, name)
				}
			}
			assert.ElementsMatch(t, tc.resting, got)
			s := &Snapshot{Now: t0.Add(time.Minute), Fleet: f, Routes: routes}
			for _, route := range routes[1:] { // a take reads its provider's rest; a route's own is the tick's
				c := &Card{ID: "x", Fields: map[string]string{FieldRoute: route.Name, FieldModel: route.Provider + "/m"}}
				_, rests := cardRest(s, c)
				assert.Equal(t, contains(tc.resting, route.Name), rests, "a take on %s reads the same rest", route.Name)
			}
		})
	}
}

// A route_rest_<route> value is ignored. The route rests from the line in
// rule3_rest_<provider>, and from nowhere else.
func TestAnOldPerRouteRestPropertyIsIgnored(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2030, 1, 2, 3, 0, 0, 0, time.UTC)
	routes := []Route{{Name: "a", Tier: "pro", Provider: "p", Enabled: true}}
	f := NewTable(Fleet)
	old := stamp(t0) + " " + stamp(t0.Add(time.Hour)) + " c1,c2,c3"
	f.SetProps(map[string]string{"route_rest_a": old})
	assert.Empty(t, RouteRests(routes, f), "the old per-route property rests nothing")
	f.SetProp(PropRule3Rest("p"), "a "+old)
	assert.False(t, RouteRests(routes, f)["a"].Resting(t0.Add(time.Minute)), "a line with no cause is rule 3's, retired: it rests nothing")
	f.SetProp(PropRule3Rest("p"), "a "+old+" "+RestProvider)
	got := RouteRests(routes, f)["a"]
	assert.Equal(t, "a", got.Route)
	assert.True(t, got.Resting(t0.Add(time.Minute)))
	assert.Equal(t, []string{"c1", "c2", "c3"}, got.Cards)
}

// Two routes of one provider are one property. A later rest of a third route of
// that provider rewrites the same property and keeps the lines already there.
func TestTwoRoutesOfOneProviderAreOneProperty(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2030, 1, 2, 3, 0, 0, 0, time.UTC)
	s := &Snapshot{Now: t0, Fleet: NewTable(Fleet), Routes: []Route{
		{Name: "a", Provider: "p", Tier: "flash", Enabled: true},
		{Name: "b", Provider: "p", Tier: "flash", Enabled: true},
		{Name: "c", Provider: "q", Tier: "flash", Enabled: true},
	}}
	due := []RouteRest{
		{Route: "b", At: t0, Until: t0.Add(RouteRestFor), Cards: []string{"c2"}, Cause: RestProvider},
		{Route: "a", At: t0, Until: t0.Add(RouteRestFor), Cards: []string{"c1"}, Cause: RestProvider},
		{Route: "c", At: t0, Until: t0.Add(RouteRestFor), Cards: []string{"c3"}, Cause: RestProvider},
	}
	var plan Plan
	restWrites(&plan, s, due, "tick")
	require.Len(t, plan.Props, 2, "one property per provider, never one per route")
	assert.Equal(t, PropRule3Rest("p"), plan.Props[0].Name)
	assert.Equal(t, PropRule3Rest("q"), plan.Props[1].Name)
	assert.True(t, plan.Props[0].WasAbsent)
	lines := parseRule3(plan.Props[0].Value)
	assert.Equal(t, []string{"c2"}, lines["b"].Cards)
	assert.Equal(t, []string{"c1"}, lines["a"].Cards)
	assert.Equal(t, "a "+lines["a"].value()+"\nb "+lines["b"].value(), plan.Props[0].Value, "lines in route-name order")

	s.Fleet.SetProp(plan.Props[0].Name, plan.Props[0].Value)
	again := []RouteRest{{Route: "b", At: t0.Add(time.Hour), Until: t0.Add(2 * time.Hour), Cards: []string{"c9"}, Cause: RestProvider}}
	var next Plan
	restWrites(&next, s, again, "tick")
	require.Len(t, next.Props, 1)
	assert.Equal(t, plan.Props[0].Value, next.Props[0].Was)
	assert.False(t, next.Props[0].WasAbsent)
	kept := parseRule3(next.Props[0].Value)
	assert.Equal(t, []string{"c1"}, kept["a"].Cards, "the route not due now keeps its line")
	assert.Equal(t, []string{"c9"}, kept["b"].Cards)
}

// A provider's refusal (nova-tools#5199): a take refused for want of credit rests its
// provider, ONE rest of every route of it, until paid; one refused for the key
// for RouteRestFor, naming the take; a rate limit, an outage and a no-result take do not. A
// refusal is attributed to the rest window its child launched in: one launched before the
// provider's last rest ended (in flight when it began, or launched while it held) does not
// rest it again however late it arrives; one launched after the end does; a record with no
// launch time does not. A provider resting now is not rested again.
func TestProviderRestsDueRestEveryRouteOfTheRefusedProvider(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2030, 1, 2, 3, 0, 0, 0, time.UTC)
	at := func(m int) string { return stamp(t0.Add(time.Duration(m) * time.Minute)) }
	line := func(class, status string) string {
		return "provider: class=" + class + " status=" + status + " msg=the provider's words"
	}
	credit := line("out-of-credit", "402")
	ended := at(2) + " " + at(4) + " c0 out-of-credit x; ended: funded by coordinator: paid" // began 3:02, ended 3:04
	routes := []Route{{Name: "a", Tier: "flash", Provider: "p", Enabled: true}, {Name: "b", Tier: "pro", Provider: "p", Enabled: true}, {Name: "c", Tier: "flash", Provider: "q", Enabled: true}}
	for _, tc := range []struct {
		name          string
		err           string
		taken, finish int    // minutes after t0; taken -1 is a record with no launch time
		rest          string // provider p's rest before
		cause         string // "" rests nothing
	}{
		{"out of credit", credit, 0, 1, "", RestCredit},
		{"the key refused", line("auth", "401"), 0, 1, "", RestAuth},
		{"a rate limit", line("rate-limited", "429"), 0, 1, "", ""},
		{"an outage", line("provider-5xx", "503"), 0, 1, "", ""},
		{"no result", "no result: no RESULT.md shape", 0, 1, "", ""},
		{"launched before the last rest began", credit, 0, 1, ended, ""},
		{"in flight when the rest began, refused after it ended", credit, 1, 5, ended, ""},
		{"launched while the rest held, refused after it ended", credit, 3, 5, ended, ""},
		{"launched after the last rest ended", credit, 5, 6, ended, RestCredit},
		{"a record with no launch time", credit, -1, 6, ended, ""},
		{"resting now", credit, 0, 1, at(0) + " " + at(60) + " c0 out-of-credit x", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			take := ProviderTake{Route: "a", Finished: at(tc.finish), Error: tc.err}
			if tc.taken >= 0 {
				take.Taken = at(tc.taken)
			}
			f := NewTable(Fleet)
			f.SetRows([]string{"m1"})
			f.Put(&Card{ID: "c1", Row: "m1", Col: Withdrawn, Fields: map[string]string{FieldProviderTake + "1": take.String()}})
			if tc.rest != "" {
				f.SetProps(map[string]string{PropProviderRest("p"): tc.rest})
			}
			s := &Snapshot{Now: t0.Add(10 * time.Minute), Fleet: f, Routes: routes}
			due := RestsDue(s)
			if tc.cause == "" {
				assert.Empty(t, due)
				return
			}
			require.Len(t, due, 1, "one rest of the provider, never one per route")
			r := due[0]
			assert.Equal(t, "", r.Route)
			assert.Equal(t, "p", r.Provider)
			assert.Equal(t, tc.cause, r.Cause)
			assert.Equal(t, []string{"c1"}, r.Cards)
			words, until := "provider p refused its key: card c1 on route a: ", s.Now.Add(RouteRestFor)
			if tc.cause == RestCredit {
				// out of credit: excluded until paid, never for a time
				words, until = "out of credit: provider p refused card c1 on route a: ", OpenUntil
			}
			assert.Equal(t, words+strings.TrimPrefix(tc.err, "provider: "), r.Why)
			assert.Equal(t, until, r.Until)
			s.Fleet.SetProps(map[string]string{PropProviderRest("p"): r.value()})
			assert.Equal(t, r, ProviderRests(s.Fleet)["p"], "the property reads back as written")
			rests := RouteRests(routes, s.Fleet)
			for _, name := range []string{"a", "b"} {
				want := r
				want.Route = name
				assert.Equal(t, want, rests[name], "every route of the provider rests, read from the one property")
			}
			assert.NotContains(t, rests, "c", "another provider's route serves")
		})
	}
}

// Money as the judgment says it: dollars and cents, rounded up (a negative balance too).
func TestDollarsRoundUpToTheCent(t *testing.T) {
	t.Parallel()
	credits, usage := 1250.0, 1250.51 // openrouter's credits at 2026-10-03 07:49 ET
	for _, tc := range []struct {
		v    float64
		want string
	}{{credits - usage, "-$0.51"}, {1.231, "$1.24"}, {4.2, "$4.20"}, {0, "$0.00"}, {-0.001, "$0.00"}} {
		assert.Equal(t, tc.want, Dollars(tc.v), "%v", tc.v)
	}
}

// A provider's rest round-trips through its property, with the balance at a refused take
// (`balance=<x>`) and without it: a value written before the token (the older form) reads
// back with the same words and no balance, and writes back unchanged; a malformed token
// stays in the words (nova-tools#5205, the third cold read).
func TestARestRoundTripsWithAndWithoutItsBalance(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, value, why string
		has              bool
		balance          float64
	}{
		{"older, no balance", "2026-10-03T09:00:00Z open c1 out-of-credit out of credit: provider p refused card c1", "out of credit: provider p refused card c1", false, 0},
		{"a balance", "2026-10-03T09:00:00Z open c1 out-of-credit balance=0.3 out of credit: provider p refused card c1", "out of credit: provider p refused card c1", true, 0.3},
		{"a balance under zero", "2026-10-03T09:00:00Z open c1 out-of-credit balance=-0.51 out of credit", "out of credit", true, -0.51},
		{"a malformed balance", "2026-10-03T09:00:00Z open c1 out-of-credit balance=abc out of credit", "balance=abc out of credit", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, ok := parseRest(tc.value)
			require.True(t, ok)
			assert.Equal(t, at, r.At)
			assert.True(t, r.Open())
			assert.Equal(t, []string{"c1"}, r.Cards)
			assert.Equal(t, RestCredit, r.Cause)
			assert.True(t, r.Refused())
			assert.Equal(t, tc.why, r.Why)
			assert.Equal(t, tc.has, r.HasBalance)
			assert.InDelta(t, tc.balance, r.Balance, 1e-9)
			assert.Equal(t, tc.value, r.value(), "written back unchanged")
		})
	}
}

// A retired rule's rest holds no route but stays the mark the rules count after (the cold
// read of PR 5546, probe A): with openrouter's balance rest of 01:54Z in the fleet table, a
// take refused for credit at 2026-10-09 20:00Z, long before it, never rests the provider
// again on deploy, the rest open or ended; a refusal launched after the rest began does. A
// retired no-result rest of a route keeps its window: transient failures before it never
// count, three after it rest the route.
func TestARetiredRestKeepsItsMark(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 10, 6, 0, 0, 0, time.UTC)
	routes := []Route{
		{Name: "flash-glm53-openrouter", Tier: "flash", Provider: "openrouter", Enabled: true},
		{Name: "flash-deepseek41-direct", Tier: "pro", Provider: "deepseek", Enabled: true},
	}
	refused := func(id, taken string) *Card {
		take := ProviderTake{Route: "flash-glm53-openrouter", Taken: taken, Finished: taken, Error: "provider: class=out-of-credit status=402 msg=Insufficient credits"}
		return &Card{ID: id, Row: "m1", Col: DoneOK, Fields: map[string]string{FieldProviderTake + "1": take.String(), FieldRoute: "flash-glm53-openrouter", "finished": taken}}
	}
	limited := func(id, at string) *Card {
		take := ProviderTake{Route: "flash-deepseek41-direct", Taken: at, Finished: at, Error: "provider: class=rate-limited status=429 msg=slow down"}
		return &Card{ID: id, Row: "m1", Col: Withdrawn, Fields: map[string]string{FieldProviderTake + "1": take.String()}}
	}
	open := "2026-10-10T01:54:32Z open - balance low on funds: provider openrouter balance $190.77"
	ended := "2026-10-10T01:54:32Z 2026-10-10T03:00:00Z - balance low; ended: balance $500"
	noResult := "flash-deepseek41-direct 2026-10-10T03:38:45Z 2026-10-10T04:08:45Z c1,c2,c3 no-result"
	for _, tc := range []struct {
		name  string
		props map[string]string
		cards []*Card
		want  []string // "provider" or the route each due rest names
	}{
		{"an old 402 under an open balance rest", map[string]string{PropProviderRest("openrouter"): open}, []*Card{refused("old1", "2026-10-09T20:00:00Z")}, nil},
		{"an old 402 under an ended balance rest", map[string]string{PropProviderRest("openrouter"): ended}, []*Card{refused("old1", "2026-10-09T20:00:00Z")}, nil},
		{"a 402 launched after the balance rest began", map[string]string{PropProviderRest("openrouter"): open}, []*Card{refused("new1", "2026-10-10T05:30:00Z")}, []string{"provider"}},
		{"429s before a retired no-result rest", map[string]string{PropRule3Rest("deepseek"): noResult},
			[]*Card{limited("a1", "2026-10-10T03:00:00Z"), limited("a2", "2026-10-10T03:10:00Z"), limited("a3", "2026-10-10T03:20:00Z")}, nil},
		{"429s after a retired no-result rest", map[string]string{PropRule3Rest("deepseek"): noResult},
			[]*Card{limited("a1", "2026-10-10T05:00:00Z"), limited("a2", "2026-10-10T05:10:00Z"), limited("a3", "2026-10-10T05:20:00Z")}, []string{"flash-deepseek41-direct"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := NewTable(Fleet)
			f.SetRows([]string{"m1"})
			for _, c := range tc.cards {
				f.Put(c)
			}
			f.SetProps(tc.props)
			var got []string
			for _, r := range RestsDue(&Snapshot{Now: now, Fleet: f, Routes: routes}) {
				if r.Route == "" {
					got = append(got, "provider")
				} else {
					got = append(got, r.Route)
				}
			}
			assert.Equal(t, tc.want, got)
			for name, r := range RouteRests(routes, f) {
				assert.False(t, r.Resting(now), "%s: a retired rest holds nothing", name)
			}
		})
	}
}

// routes rest never replaces a rest that holds (the cold read of PR 5546, probe B;
// tla/RouteRest.tla, RestProvider and RestRoute): over a refused take's credit rest, a
// coordinator's rest for 30 minutes would end the credit rest with it and take the provider
// out of the all-out stop. It is refused, naming the rest, and the stop holds an hour on.
// A route's own rest is not replaced either; a route under its provider's rest may take
// its own.
func TestRoutesRestNeverReplacesARestThatHolds(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 10, 6, 0, 0, 0, time.UTC)
	routes := []Route{{Name: "flash-glm53-openrouter", Tier: "flash", Provider: "openrouter", Enabled: true}}
	f := NewTable(Fleet)
	f.SetProps(map[string]string{
		PropProviderRest("openrouter"): "2026-10-10T05:00:00Z open c9 out-of-credit out of credit: provider openrouter refused card c9",
	})
	s := &Snapshot{Now: now, Fleet: f, Routes: routes}
	require.NotEmpty(t, AllOutOfCredit(routes, RouteRests(routes, f), now))
	p := RestRoutes(s, RouteRestReq{Target: "openrouter", Reason: "x", Until: now.Add(30 * time.Minute), Who: "seat"})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "provider openrouter rests already until paid (out-of-credit: out of credit: provider openrouter refused card c9)")
	assert.Empty(t, p.Props)
	assert.NotEmpty(t, AllOutOfCredit(routes, RouteRests(routes, f), now.Add(time.Hour)), "the stop holds")

	p = RestRoutes(s, RouteRestReq{Target: "flash-glm53-openrouter", Reason: "its own", Who: "seat"})
	require.Empty(t, p.Refused, "a route under its provider's rest may take its own")
	f.SetProp(p.Props[0].Name, p.Props[0].Value)
	p = RestRoutes(s, RouteRestReq{Target: "flash-glm53-openrouter", Reason: "again", Until: now.Add(time.Minute), Who: "seat"})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "route flash-glm53-openrouter rests already until woken")
}

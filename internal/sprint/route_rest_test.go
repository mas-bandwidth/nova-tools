package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rule's window (rule 3): the last RouteRestWindow ended takes on a route after its last
// rest began, in the order they ended. RouteRestAfter takes with no result in it rest the
// route from the clock for RouteRestFor; older ends, ends before the last rest, ok ends and
// the provider's failures do not; a route resting now is not rested again.
func TestRestsDueCountsNoResultEndsInTheRoutesWindow(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2030, 1, 2, 3, 0, 0, 0, time.UTC)
	at := func(m int) string { return stamp(t0.Add(time.Duration(m) * time.Minute)) }
	noResult := func(m int) string {
		return ProviderTake{Route: "a", Finished: at(m), Error: "no result: no RESULT.md shape"}.String()
	}
	provider := func(m int) string { return ProviderTake{Route: "a", Finished: at(m), Error: "server_error"}.String() }
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
	for _, tc := range []struct {
		name  string
		takes map[string]string
		ok    map[string]int
		rest  string // the property's value before
		cards []string
	}{
		{"three with no result", map[string]string{"c1": noResult(1), "c2": noResult(2), "c3": noResult(3)}, nil, "", []string{"c1", "c2", "c3"}},
		{"two", map[string]string{"c1": noResult(1), "c2": noResult(2)}, oks(3, 5), "", nil},
		{"the provider's are not no result", map[string]string{"c1": noResult(1), "c2": provider(2), "c3": noResult(3)}, nil, "", nil},
		{"three among ok ends, within ten", map[string]string{"c1": noResult(1), "c2": noResult(5), "c3": noResult(9)}, oks(2, 3), "", []string{"c1", "c2", "c3"}},
		{"the first slid out of the window", map[string]string{"c1": noResult(1), "c2": noResult(20), "c3": noResult(21)}, oks(2, 9), "", nil},
		{"ends before the last rest do not count", map[string]string{"c1": noResult(1), "c2": noResult(2), "c3": noResult(40)}, nil, at(10) + " " + at(30) + " x,y,z", nil},
		{"resting now", map[string]string{"c1": noResult(41), "c2": noResult(42), "c3": noResult(43)}, nil, at(40) + " " + at(70) + " x,y,z", nil},
		{"a rest ended, three after it", map[string]string{"c1": noResult(31), "c2": noResult(32), "c3": noResult(33)}, nil, at(0) + " " + at(30) + " x,y,z", []string{"c1", "c2", "c3"}},
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
			assert.Equal(t, tc.cards, due[0].Cards)
			assert.Equal(t, RestNoResult, due[0].Cause)
			// Check that Until is within +/-20% of RouteRestFor (jitter)
			jittered := due[0].Until.Sub(due[0].At)
			assert.InDelta(t, RouteRestFor, jittered, 0.2*float64(RouteRestFor), "rest end within +/-20% of RouteRestFor")
			s.Fleet.SetProps(map[string]string{PropRule3Rest(""): "a " + due[0].value()})
			readBack := RouteRests(s.Routes, s.Fleet)["a"]
			assert.Equal(t, due[0].Route, readBack.Route)
			assert.Equal(t, due[0].Provider, readBack.Provider)
			assert.Equal(t, due[0].Cards, readBack.Cards)
			assert.Equal(t, due[0].Cause, readBack.Cause)
			// Compare times with tolerance for RFC3339 rounding
			assert.InDelta(t, due[0].At.UnixNano(), readBack.At.UnixNano(), 1e9)
			assert.InDelta(t, due[0].Until.UnixNano(), readBack.Until.UnixNano(), 1e9)
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
		{Route: "b", At: t0, Until: t0.Add(RouteRestFor), Cards: []string{"c2"}, Cause: RestNoResult},
		{Route: "a", At: t0, Until: t0.Add(RouteRestFor), Cards: []string{"c1"}, Cause: RestNoResult},
		{Route: "c", At: t0, Until: t0.Add(RouteRestFor), Cards: []string{"c3"}, Cause: RestNoResult},
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
	again := []RouteRest{{Route: "b", At: t0.Add(time.Hour), Until: t0.Add(2 * time.Hour), Cards: []string{"c9"}, Cause: RestNoResult}}
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
	ended := at(2) + " " + at(4) + " - out-of-credit x; ended: funded by coordinator: paid" // began 3:02, ended 3:04
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
		{"resting now", credit, 0, 1, at(0) + " " + at(60) + " - out-of-credit x", ""},
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
			words := "provider p refused its key: card c1 on route a: "
			if tc.cause == RestCredit {
				// out of credit: excluded until paid, never for a time
				words = "out of credit: provider p refused card c1 on route a: "
			}
			assert.Equal(t, words+strings.TrimPrefix(tc.err, "provider: "), r.Why)
			// For auth cause (key refused), check jittered time; for RestCredit, check OpenUntil
			if tc.cause == RestCredit {
				assert.Equal(t, OpenUntil, r.Until, "out of credit rests open")
			} else {
				// Check that Until is within +/-20% of RouteRestFor (jitter)
				jittered := r.Until.Sub(r.At)
				assert.InDelta(t, RouteRestFor, jittered, 0.2*float64(RouteRestFor), "rest end within +/-20% of RouteRestFor")
			}
			s.Fleet.SetProps(map[string]string{PropProviderRest("p"): r.value()})
			readBack := ProviderRests(s.Fleet)["p"]
			assert.Equal(t, r.Route, readBack.Route)
			assert.Equal(t, r.Provider, readBack.Provider)
			assert.Equal(t, r.Cards, readBack.Cards)
			assert.Equal(t, r.Cause, readBack.Cause)
			// Compare times with tolerance for RFC3339 rounding
			assert.InDelta(t, r.At.UnixNano(), readBack.At.UnixNano(), 1e9)
			assert.InDelta(t, r.Until.UnixNano(), readBack.Until.UnixNano(), 1e9)
			rests := RouteRests(routes, s.Fleet)
			for _, name := range []string{"a", "b"} {
				want := r
				want.Route = name
				assert.Equal(t, want.Route, rests[name].Route)
				assert.Equal(t, want.Provider, rests[name].Provider)
				assert.Equal(t, want.Cards, rests[name].Cards)
				assert.Equal(t, want.Cause, rests[name].Cause)
				assert.InDelta(t, want.At.UnixNano(), rests[name].At.UnixNano(), 1e9)
				assert.InDelta(t, want.Until.UnixNano(), rests[name].Until.UnixNano(), 1e9)
			}
			assert.NotContains(t, rests, "c", "another provider's route serves")
		})
	}
}

// Route rests end with +/-20% jitter deterministically derived from route name and rest start.
func TestARouteRestEndIsJittered(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2030, 1, 2, 3, 0, 0, 0, time.UTC)
	fleet := NewTable(Fleet)
	fleet.SetRows([]string{"m1"})
	// Two routes resting at the same instant.
	routes := []Route{
		{Name: "alpha", Tier: "flash", Provider: "p", Enabled: true},
		{Name: "beta", Tier: "flash", Provider: "p", Enabled: true},
	}
	s := &Snapshot{Now: t0, Fleet: fleet, Routes: routes}
	due := RestsDue(s)
	require.Equal(t, 0, len(due), "no rest due when no takes have ended")
	// Simulate takes that would cause rests; we test the jitter function directly.
	r1, r2 := "alpha", "beta"
	at := t0
	end1, end2 := routeRestEnd(at, r1), routeRestEnd(at, r2)
	// Same route and start always give the same end.
	end1Again := routeRestEnd(at, r1)
	require.Equal(t, end1, end1Again, "same route and start give same end")
	// Different routes resting at the same instant end at different times.
	require.NotEqual(t, end1, end2, "different routes end at different times")
	// Each end is within [0.8, 1.2] of RouteRestFor.
	jittered1 := end1.Sub(at)
	jittered2 := end2.Sub(at)
	require.InDelta(t, RouteRestFor, jittered1, 0.2*float64(RouteRestFor), "end1 within +/-20%% of RouteRestFor")
	require.InDelta(t, RouteRestFor, jittered2, 0.2*float64(RouteRestFor), "end2 within +/-20%% of RouteRestFor")
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

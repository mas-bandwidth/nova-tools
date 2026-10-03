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
				s.Fleet.SetProps(map[string]string{PropRouteRest("a"): tc.rest})
			}
			due := RestsDue(s)
			if tc.cards == nil {
				assert.Empty(t, due)
				return
			}
			assert.Equal(t, []RouteRest{{Route: "a", At: s.Now, Until: s.Now.Add(RouteRestFor), Cards: tc.cards, Cause: RestNoResult}}, due)
			s.Fleet.SetProps(map[string]string{PropRouteRest("a"): due[0].value()})
			assert.Equal(t, due[0], RouteRests(s.Routes, s.Fleet)["a"], "the property reads back as written")
		})
	}
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

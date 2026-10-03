package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
			assert.Equal(t, []RouteRest{{Route: "a", At: s.Now, Until: s.Now.Add(RouteRestFor), Cards: tc.cards}}, due)
			s.Fleet.SetProps(map[string]string{PropRouteRest("a"): due[0].value()})
			assert.Equal(t, due[0], RouteRests(s.Routes, s.Fleet)["a"], "the property reads back as written")
		})
	}
}

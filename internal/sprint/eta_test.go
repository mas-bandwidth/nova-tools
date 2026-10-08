package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The ETA states its basis (eta.go): the rate's window and sample, by LandingRate's rule
// and at its value, and the cards left with the held ones apart from the executing ones.

func TestTheETAStatesItsRateWindowAndSample(t *testing.T) {
	t.Parallel()
	now := t0
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	five := []time.Time{ago(50 * time.Minute), ago(40 * time.Minute), ago(30 * time.Minute), ago(20 * time.Minute), ago(10 * time.Minute)}
	cases := []struct {
		name   string
		landed []time.Time
		total  int64
		spans  []Span
		first  time.Time
		want   ETABasis
	}{
		{"five in the last hour: the window's rate, its five landings", five, 10, nil, ago(2 * time.Hour),
			ETABasis{Window: RateWindowRecent, Landings: 5, Hours: 1, PerHour: 5}},
		{"a 20-minute stop inside the window: the hour is of running time", []time.Time{ago(70 * time.Minute), ago(60 * time.Minute), ago(50 * time.Minute), ago(40 * time.Minute), ago(20 * time.Minute)},
			10, []Span{{From: ago(30 * time.Minute), To: ago(10 * time.Minute)}}, ago(2 * time.Hour),
			ETABasis{Window: RateWindowRecent, Landings: 5, Hours: 1, PerHour: 5}},
		{"three in the last hour: the whole sprint's average, its every landing", five[:3], 10, nil, ago(2 * time.Hour),
			ETABasis{Window: RateWindowAll, Landings: 10, Hours: 2, PerHour: 5}},
		{"nothing landed: no rate", nil, 0, nil, ago(2 * time.Hour), ETABasis{Window: RateWindowNone}},
		{"never started: no rate", five, 10, nil, time.Time{}, ETABasis{Window: RateWindowNone}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := LandingRateBasis(c.landed, c.total, c.spans, c.first, now)
			assert.Equal(t, c.want.Window, b.Window)
			assert.Equal(t, c.want.Landings, b.Landings)
			assert.InDelta(t, c.want.Hours, b.Hours, 1e-9)
			assert.InDelta(t, c.want.PerHour, b.PerHour, 1e-9)
			assert.Equal(t, LandingRate(c.landed, c.total, c.spans, c.first, now), b.PerHour, "the basis is the rate's own")
		})
	}
	assert.Equal(t, "5.0/h over last 1h of running time (5 landings)", LandingRateBasis(five, 10, nil, ago(2*time.Hour), now).Text())
	assert.Equal(t, "rate none", ETABasis{Window: RateWindowNone}.Text())
}

func TestTheETAShowsHeldWorkApartFromExecutingWork(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Now: t0, Fleet: NewTable(Fleet), Work: NewTable(Work)}
	s.Work.SetRows([]string{"s1"})
	put := func(id, col string, f map[string]string) {
		if f == nil {
			f = map[string]string{}
		}
		s.Work.Put(&Card{ID: id, Row: "s1", Col: col, Score: float64(len(s.Work.Cards())), Fields: f})
	}
	put("s1-1", Landed, nil)
	put("s1-2", Working, nil)
	put("s1-3", Review, nil)
	put("s1-4", Ready, nil)
	put("s1-5", Waiting, map[string]string{FieldHeld: stamp(t0)}) // admitted held
	put("s1-6", Waiting, map[string]string{"needs": "s1-5"})      // waits on the held one
	w := ETAWorkOf(s)
	assert.Equal(t, ETAWork{Left: 5, Held: 2, Executing: 2, Queued: 1}, w, "the landed card is in none; held, executing and queued sum to the cards left")
	assert.Equal(t, "left 5: held 2 executing 2 queued 1", w.Text())
}

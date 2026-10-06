package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// An ETA states what it stands on (docs/SPEC-SPRINT.md section 1, the ETA): the rate's
// window, its sample and its running hours, by LandingRate's rule and at its rate, and the
// cards left split held, executing and queued.
func TestTheETAStatesItsWindowSampleAndHeldWork(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 2, 22, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	opened := Span{From: ago(5 * time.Hour), To: ago(4 * time.Hour)} // every epoch begins STOPPED
	stamps := func(ds ...time.Duration) []time.Time {
		var out []time.Time
		for _, d := range ds {
			out = append(out, ago(d))
		}
		return out
	}
	m := time.Minute
	cases := []struct {
		name   string
		landed []time.Time
		total  int64
		first  time.Time
		want   ETABasis
	}{
		{"five in the last hour: the hour", stamps(m, 2*m, 3*m, 4*m, 5*m, 2*time.Hour), 6, ago(4 * time.Hour),
			ETABasis{Window: RateWindowRecent, Landings: 5, Hours: 1, PerHour: 5}},
		{"four in the last hour: the whole sprint's average", stamps(m, 2*m, 3*m, 4*m, 2*time.Hour, 3*time.Hour, 3*time.Hour, 3*time.Hour), 8, ago(4 * time.Hour),
			ETABasis{Window: RateWindowAll, Landings: 8, Hours: 4, PerHour: 2}},
		{"never started: no rate", stamps(m), 1, time.Time{}, ETABasis{Window: RateWindowNone}},
		{"nothing landed: no rate", nil, 0, ago(4 * time.Hour), ETABasis{Window: RateWindowNone}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := LandingRateBasis(c.landed, c.total, []Span{opened}, c.first, now)
			assert.Equal(t, c.want.Window, got.Window)
			assert.Equal(t, c.want.Landings, got.Landings, "the sample")
			assert.InDelta(t, c.want.Hours, got.Hours, 1e-9)
			assert.InDelta(t, c.want.PerHour, got.PerHour, 1e-9)
			assert.InDelta(t, LandingRate(c.landed, c.total, []Span{opened}, c.first, now), got.PerHour, 1e-9, "LandingRate's rate")
		})
	}

	t.Run("held apart from executing", func(t *testing.T) {
		t.Parallel()
		s := &Snapshot{Now: now, Work: NewTable(Work)}
		s.Work.SetRows([]string{"s1"})
		put := func(id string, col State, f map[string]string) {
			if f == nil {
				f = map[string]string{}
			}
			s.Work.Put(&Card{ID: id, Row: "s1", Col: col, Fields: f})
		}
		put("held", Waiting, map[string]string{FieldHeld: "yes"})
		put("queued", Waiting, nil)
		put("ready", Ready, nil)
		put("working", Working, nil)
		put("review", Review, nil)
		put("merging", Merging, nil)
		put("landed", Landed, nil)
		assert.Equal(t, ETAWork{Left: 6, Held: 1, Executing: 3, Queued: 2}, ETAWorkOf(s))
	})
}

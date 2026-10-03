package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// RecentLandings keeps the landings from the start of the last hour of running time
// on, oldest first: the window's start is computed here by hand for each row (the
// clock's hour back from now, pushed back by a STOPPED span inside it, or the first
// start when the machine has run less than an hour), never by the helper it checks.
// A stamp on the window's start is kept; one a second before it is not.
func TestRecentLandingsKeepsTheLastHourOfRunningTimeOldestFirst(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 2, 22, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	opened := Span{From: ago(5 * time.Hour), To: ago(4 * time.Hour)} // every epoch begins STOPPED
	cases := []struct {
		name   string
		landed []time.Time
		spans  []Span
		first  time.Time
		want   []time.Time
	}{
		{"running all the hour: the window starts an hour back; out of order in, oldest first out",
			[]time.Time{ago(59 * time.Minute), ago(61 * time.Minute), ago(time.Hour), ago(time.Hour + time.Second), ago(0)},
			[]Span{opened}, ago(4 * time.Hour),
			[]time.Time{ago(time.Hour), ago(59 * time.Minute), ago(0)}},
		{"a 20-minute stop inside the hour: the window starts 80 minutes back, the stop's own landings kept",
			[]time.Time{ago(80*time.Minute + time.Second), ago(80 * time.Minute), ago(20 * time.Minute), ago(5 * time.Minute)},
			[]Span{opened, {From: ago(30 * time.Minute), To: ago(10 * time.Minute)}}, ago(4 * time.Hour),
			[]time.Time{ago(80 * time.Minute), ago(20 * time.Minute), ago(5 * time.Minute)}},
		{"stopped now, for two hours: no running time passes, the window starts an hour of running back",
			[]time.Time{ago(3*time.Hour + time.Second), ago(3 * time.Hour), ago(150 * time.Minute)},
			[]Span{opened, {From: ago(2 * time.Hour)}}, ago(4 * time.Hour),
			[]time.Time{ago(3 * time.Hour), ago(150 * time.Minute)}},
		{"run for 30 minutes: the window starts at the first start",
			[]time.Time{ago(31 * time.Minute), ago(30 * time.Minute), ago(time.Minute)},
			[]Span{{From: ago(time.Hour), To: ago(30 * time.Minute)}}, ago(30 * time.Minute),
			[]time.Time{ago(30 * time.Minute), ago(time.Minute)}},
		{"never started: none", []time.Time{ago(time.Minute)}, nil, time.Time{}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, RecentLandings(c.landed, c.spans, c.first, now))
		})
	}
	t.Run("past the bound: the newest", func(t *testing.T) {
		t.Parallel()
		var landed []time.Time
		for i := range MaxRecentLandings + 5 {
			landed = append(landed, ago(time.Duration(i)*time.Millisecond))
		}
		got := RecentLandings(landed, []Span{opened}, ago(4*time.Hour), now)
		assert.Len(t, got, MaxRecentLandings)
		assert.Equal(t, now, got[len(got)-1], "the newest kept")
		assert.Equal(t, ago(time.Duration(MaxRecentLandings-1)*time.Millisecond), got[0], "the oldest five past the bound left out")
	})
}

package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestRateCoverLandingRate pins LandingRate: the landings per hour of running
// time (rate.go). The window's rate is returned when enough landings have
// landed in the last RateWindow of running time; otherwise the total is spread
// over all running time since the first start, or zero.
func TestRateCoverLandingRate(t *testing.T) {
	t.Parallel()
	now := coverT0
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	for _, tc := range []struct {
		name   string
		landed []time.Time
		total  int64
		spans  []Span
		first  time.Time
		now    time.Time
		want   float64
	}{
		{
			name:   "refusal: not started, first zero",
			landed: []time.Time{now},
			total:  1,
			spans:  nil,
			first:  time.Time{},
			now:    now,
			want:   0,
		},
		{
			name:   "refusal: now equal to first, no time advanced",
			landed: []time.Time{now},
			total:  1,
			spans:  nil,
			first:  now,
			now:    now,
			want:   0,
		},
		{
			name: "main: window rate with enough landings in the last hour",
			landed: []time.Time{
				ago(50 * time.Minute),
				ago(40 * time.Minute),
				ago(30 * time.Minute),
				ago(20 * time.Minute),
				ago(10 * time.Minute),
			},
			total: 10,
			spans: nil,
			first: ago(2 * time.Hour),
			now:   now,
			want:  5,
		},
		{
			name: "main: window rate with a stop shrinking running time",
			landed: []time.Time{
				ago(70 * time.Minute),
				ago(60 * time.Minute),
				ago(50 * time.Minute),
				ago(40 * time.Minute),
				ago(20 * time.Minute),
			},
			total: 10,
			spans: []Span{{From: ago(30 * time.Minute), To: ago(10 * time.Minute)}},
			first: ago(2 * time.Hour),
			now:   now,
			want:  5,
		},
		{
			name: "main: fallback to total over all running time",
			landed: []time.Time{
				ago(50 * time.Minute),
				ago(40 * time.Minute),
				ago(30 * time.Minute),
			},
			total: 10,
			spans: nil,
			first: ago(2 * time.Hour),
			now:   now,
			want:  5,
		},
		{
			name: "refusal: too few landings and no total over running time",
			landed: []time.Time{
				ago(50 * time.Minute),
				ago(40 * time.Minute),
			},
			total: 0,
			spans: nil,
			first: ago(2 * time.Hour),
			now:   now,
			want:  0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := LandingRate(tc.landed, tc.total, tc.spans, tc.first, tc.now)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestRateCoverRunningStart pins runningStart: the clock reading from which
// the running time to now spans w, walking STOPPED spans back from now
// (rate.go). The window starts at first when running time since first is less
// than w; otherwise it starts at now minus w, pushed back past any stops.
func TestRateCoverRunningStart(t *testing.T) {
	t.Parallel()
	now := coverT0
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	for _, tc := range []struct {
		name  string
		spans []Span
		first time.Time
		now   time.Time
		w     time.Duration
		want  time.Time
	}{
		{
			name:  "main: a stop inside the window pushes the start past the stop",
			spans: []Span{{From: ago(30 * time.Minute), To: ago(10 * time.Minute)}},
			first: ago(3 * time.Hour),
			now:   now,
			w:     time.Hour,
			want:  ago(80 * time.Minute),
		},
		{
			name:  "refusal: run less than w since first starts the window at first",
			spans: []Span{{From: ago(time.Hour), To: ago(30 * time.Minute)}},
			first: ago(30 * time.Minute),
			now:   now,
			w:     time.Hour,
			want:  ago(30 * time.Minute),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := runningStart(tc.spans, tc.first, tc.now, tc.w)
			assert.Equal(t, tc.want, got)
		})
	}
}

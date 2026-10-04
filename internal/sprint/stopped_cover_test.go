package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestStoppedCoverSumsClippedSpans pins StoppedBetween: a span inside the
// window adds its whole length, an open span (To zero) runs to the window's
// end, a span reaching past it or starting before it is clipped to the
// window, and a span outside the window adds nothing.
func TestStoppedCoverSumsClippedSpans(t *testing.T) {
	t.Parallel()
	at := func(minute int) time.Time { return time.Date(2026, 10, 1, 12, minute, 0, 0, time.UTC) }
	from, to := at(0), at(60)
	for _, tc := range []struct {
		name  string
		spans []Span
		want  time.Duration
	}{
		{
			"no spans stop no time",
			nil,
			0,
		},
		{
			"a span inside the window adds its whole length",
			[]Span{{From: at(10), To: at(20)}},
			10 * time.Minute,
		},
		{
			"an open span, To zero, runs to the window's end",
			[]Span{{From: at(30)}},
			30 * time.Minute,
		},
		{
			"a span ending after the window is clipped to it",
			[]Span{{From: at(30), To: at(90)}},
			30 * time.Minute,
		},
		{
			"a span starting before the window is clipped to it",
			[]Span{{From: at(-10), To: at(10)}},
			10 * time.Minute,
		},
		{
			"a span wholly before the window adds nothing",
			[]Span{{From: at(-30), To: at(-10)}},
			0,
		},
		{
			"a span wholly after the window adds nothing",
			[]Span{{From: at(70), To: at(80)}},
			0,
		},
		{
			"several spans add in sum",
			[]Span{{From: at(5), To: at(10)}, {From: at(20), To: at(35)}},
			20 * time.Minute,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, StoppedBetween(tc.spans, from, to))
		})
	}
}

package hostload

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProcStatCountsGuestTimeOnce: Linux's /proc/stat emits guest and guest_nice
// as separate trailing fields even though each is already accounted inside user
// and nice (kernel/sched/cputime.c account_guest_time adds each guest interval
// to both USER/NICE and GUEST/GUEST_NICE). Summing every field therefore counts
// guest intervals twice and inflates the total, which pulls the busy percent
// down. guest and guest_nice are omitted from the total while the guest tick
// stays inside user/nice, which is busy: guests are real busy CPU.
func TestProcStatCountsGuestTimeOnce(t *testing.T) {
	t.Parallel()

	// Primary case: a 50% busy interval with a nonzero guest tick that moved
	// alongside user. The guest tick must not be counted again in the total;
	// the current sum (which includes guest) reports 66.6667% instead.
	prev, ok := ParseProcStat("cpu 100 0 0 100 0 0 0 0 100 0\n")
	require.True(t, ok, "prev must parse")
	require.Equal(t, Ticks{Busy: 100, Total: 200}, prev, "prev must omit guest from total")
	cur, ok := ParseProcStat("cpu 150 0 0 150 0 0 0 0 150 0\n")
	require.True(t, ok, "cur must parse")
	require.Equal(t, Ticks{Busy: 150, Total: 300}, cur, "cur must omit guest from total")
	pct, good := BusyPercent(prev, cur)
	require.True(t, good, "an advancing interval must report")
	require.True(t, near(pct, 50.0), "busy percent = %v, want 50 (guest already in user, current code gives 66.6667)", pct)

	// Table: nonzero guest and guest_nice separately and together are dropped
	// from the total; the four-, five- and eight-field legacy forms are
	// unchanged; redundant zero guest fields are equivalent to the eight-field
	// form. One distinguishing row carries nonzero guest fields.
	cases := []struct {
		name      string
		input     string
		wantBusy  uint64
		wantTotal uint64
	}{
		{"four field legacy", "cpu 100 0 0 200\n", 100, 300},
		{"five field legacy", "cpu 100 0 0 200 50\n", 100, 350},
		{"eight field no guest", "cpu 100 0 0 200 50 0 0 0\n", 100, 350},
		{"nonzero guest only", "cpu 100 0 0 200 50 0 0 0 30 0\n", 100, 350},
		{"nonzero guest_nice only", "cpu 100 0 0 200 50 0 0 0 0 10\n", 100, 350},
		{"both guest and guest_nice", "cpu 100 0 0 200 50 0 0 0 30 10\n", 100, 350},
		{"redundant zero guest fields", "cpu 100 0 0 200 50 0 0 0 0 0\n", 100, 350},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := ParseProcStat(tc.input)
			if assert.True(t, ok, "input must parse: %q", tc.input) {
				assert.Equal(t, tc.wantBusy, got.Busy, "busy for %q", tc.input)
				assert.Equal(t, tc.wantTotal, got.Total, "total for %q", tc.input)
			}
		})
	}

	// Measure consumes the corrected interval through an injected Source with
	// no host reads: the first reading has no interval and falls back to the
	// load average; the second reports the corrected 50% CPU interval.
	reads := 0
	src := Source{NCPU: 8,
		ProcStat: func() (string, error) {
			s := "cpu 100 0 0 100 0 0 0 0 100 0\n"
			if reads > 0 {
				s = "cpu 150 0 0 150 0 0 0 0 150 0\n"
			}
			reads++
			return s, nil
		},
		Load1: func() (float64, bool) { return 2, true }}
	_, how, st, ok := Measure(src, State{}, t0)
	require.True(t, ok, "first reading has no interval: how=%q ok=%v", how, ok)
	require.Equal(t, HowLoad1, how, "first reading has no interval: how=%q ok=%v", how, ok)
	require.Equal(t, Ticks{Busy: 100, Total: 200}, *st.Ticks, "first reading keeps the corrected counters")
	pct, how, _, ok = Measure(src, st, t0.Add(time.Second))
	require.True(t, ok, "second reading: pct=%v how=%q ok=%v", pct, how, ok)
	require.Equal(t, HowCPU, how, "second reading: pct=%v how=%q ok=%v", pct, how, ok)
	require.True(t, near(pct, 50.0), "measure percent = %v, want 50", pct)
}

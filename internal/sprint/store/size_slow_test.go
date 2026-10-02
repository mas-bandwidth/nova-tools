//go:build slow

package store

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The size run on the in-memory store: stops by position at 10x and 100x
// the largest real stream give the right tables. The run's times are the
// smoke's size run to measure (CI holds no clock in a test).
func TestSentinelsBySize(t *testing.T) {
	t.Parallel()
	timed := func(t *testing.T, what string, f func()) {
		t.Helper()
		f()
	}
	t.Run("three streams of 30,000, a stop every 1,000, one step", func(t *testing.T) {
		h := newHarness(t)
		h.setup(0)
		timed(t, "add a,b,c --count 30000 --sentinel-every 1000", func() { h.sentinelsBy([]string{"a", "b", "c"}, 30000, 1000, false) })
		s := h.snap()
		if s.Work.Count("a", sprint.Ready) != 1000 || s.Work.Count("c", sprint.Waiting) != 29029 {
			require.Fail(t, fmt.Sprintf("a ready %d, c waiting %d", s.Work.Count("a", sprint.Ready), s.Work.Count("c", sprint.Waiting)))
		}
	})
	for _, n := range []int{10000, 100000} {
		t.Run("one stop at the end of a stream", func(t *testing.T) {
			h := newHarness(t)
			h.setup(0)
			h.must(AddStep(sprint.AddReq{Stream: "s1", Count: n}))
			timed(t, fmt.Sprintf("add --sentinel at the end of %d", n), func() {
				h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"end"}, Sentinel: true}))
			})
			sn := h.snap()
			if w := sprint.WaitsFor(sn, sn.Work.Card("end"), nil); len(w) != n {
				require.Fail(t, fmt.Sprintf("the stop waits for %d", len(w)))
			}
		})
	}
	t.Run("a stop into the middle of 10,000 ready cards", func(t *testing.T) {
		h := newHarness(t)
		h.setup(0)
		h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 10000}))
		timed(t, "add --sentinel --after s1-5000 of 10,000 ready", func() {
			h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"mid"}, Sentinel: true, After: "s1-5000"}))
		})
		if c := h.snap().Work.Count("s1", sprint.Waiting); c != 5001 {
			require.Fail(t, fmt.Sprintf("waiting %d", c))
		}
	})
}

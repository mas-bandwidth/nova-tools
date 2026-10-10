package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// A landing the lifecycle refuses satisfies no need: the needs rule is judged
// against the landings of the units the lifecycle kept.
func TestLawfulRefusedLandingSatisfiesNoNeed(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1"}}))
	s11, b := w.s.Work.Card("s1-1"), w.s.Work.Card("b") // s1-1 is ready
	p := Plan{Units: []Unit{
		{Key: "s1-1", Changes: []Change{change(Work, moveEntry(s11, s11.Row, Landed, nil))}},
		{Key: "b", Changes: []Change{change(Work, moveEntry(b, b.Row, Ready, nil))}},
	}}
	p.on(w.s)
	p = Lawful(p)
	require.Empty(t, p.Units, "a refused landing lent to a waiter: %+v", p)
	require.Len(t, p.Refused, 2, "a refused landing lent to a waiter: %+v", p)
	require.Contains(t, p.Refused[1].Why, "b needs s1-1", "a refused landing lent to a waiter: %+v", p)
}

// A work-table move whose expectation names no place is judged from the
// pre-state; one the pre-state does not hold is refused.
func TestLawfulMoveWithoutPlace(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1"}}))
	bare := func(id, row, col string) Unit {
		return Unit{Key: id, Changes: []Change{change(Work, ntable.BatchMemberEntry{ID: id,
			Expect: &ntable.MemberExpect{Revision: "1"}, Move: &ntable.MemberMoveOp{Row: row, Col: col}})}}
	}
	for _, c := range []struct {
		u   Unit
		pre bool
		why string
	}{
		{bare("b", "s2", Ready), true, "b needs s1-1, not landed"},
		{bare("b", "s2", Merging), true, "no move waiting -> merging"},
		{bare("s1-1", "s1", Landed), true, "no move ready -> landed"},
		{bare("ghost", "s2", Ready), true, "names no place"},
		{bare("b", "s2", Ready), false, "names no place"},
		{Unit{Key: "b", Changes: []Change{change(Work, ntable.BatchMemberEntry{ID: "b", Remove: true})}}, false, "names no place"},
	} {
		p := Plan{Units: []Unit{c.u}}
		if c.pre {
			p.on(w.s)
		}
		p = Lawful(p)
		require.Empty(t, p.Units, "%s (pre %v, want %q): %+v", c.u.Key, c.pre, c.why, p)
		require.Len(t, p.Refused, 1, "%s (pre %v, want %q): %+v", c.u.Key, c.pre, c.why, p)
		require.Contains(t, p.Refused[0].Why, c.why, "%s (pre %v, want %q): %+v", c.u.Key, c.pre, c.why, p)
	}
	// A move from a place the pre-state holds, by the lifecycle, is kept.
	p := Plan{Units: []Unit{bare("s1-1", "s1", Working)}}
	p.on(w.s)
	p = Lawful(p)
	require.Len(t, p.Units, 1, "ready -> working without a place: %+v", p)
}

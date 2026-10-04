package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The lifecycle lands a sentinel from waiting by release, or an auto one
// (IsAuto) by any step once its needs have landed: a plan that lands an auto
// sentinel whose need has not landed is refused (it waits), and one that
// lands a sentinel that is not auto outside release is refused; resolve does
// not land the auto one while its need is open.
func TestTheLifecycleLandsAnAutoSentinelOnlyWhenItsNeedsLanded(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Lawful(Add(w.s, AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true, Auto: true})))
	w.must(Lawful(Add(w.s, AddReq{Stream: "s2", IDs: []string{"gate"}, Sentinel: true, Needs: []string{"s1-1"}})))
	stop, gate := w.s.Work.Card("stop"), w.s.Work.Card("gate")
	require.True(t, IsAuto(stop), "stop: %v", stop.Fields)
	require.Equal(t, "auto", SentinelKind(stop))
	require.Equal(t, "manual", SentinelKind(gate))
	land := func(c *Card) Plan {
		var p Plan
		p.on(w.s)
		p.Units = []Unit{{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, moveEntry(c, c.Row, Landed, nil))}}}
		return Lawful(p)
	}
	p := land(stop)
	require.Len(t, p.Refused, 1, "%+v", p)
	require.Equal(t, "stop needs s1-1, not landed: it waits", p.Refused[0].Why)
	p = land(gate)
	require.Len(t, p.Refused, 1, "%+v", p)
	require.Equal(t, "the lifecycle lands from waiting only a sentinel, and only by release or, an auto sentinel, when its needs land", p.Refused[0].Why)
	require.False(t, landsSentinel(w.s, Resolve(w.s, ResolveReq{Who: MachineActor})), "resolve landed an auto sentinel whose need is open")
	require.Equal(t, 1, HeldBack(w.s), "only the manual sentinel holds back")
	require.Equal(t, 1, AutoWaiting(w.s))
}

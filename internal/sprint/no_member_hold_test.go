package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// noMemberNote is the tick's deal part's "no fleet member is up" judgment, as
// it is raised with the given beats; docs/SPEC-SPRINT.md, the judgment table.
func noMemberNote(t *testing.T, w *world, r TickReq) Note {
	t.Helper()
	p, _ := TickDeal(w.s, r)
	var got []Note
	for _, n := range p.Notes {
		if n.Type == NNoMember {
			got = append(got, n)
		}
	}
	require.Len(t, got, 1)
	return got[0]
}

// A fleet whose members all beat and are all held names the hold and fleet up;
// one that does not beat at all names the beat (docs/SPEC-SPRINT.md, the
// judgment "no fleet member is up").
func TestNoMemberJudgmentNamesTheHoldWhenEveryBeatingMemberIsHeld(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2"}))
	w.must(Add(w.s, AddReq{Stream: "s1", Count: 2}))
	w.must(FleetStep(w.s, FleetReq{Op: "hold", Member: "m1", Who: "coordinator"}))
	w.must(FleetStep(w.s, FleetReq{Op: "hold", Member: "m2", Who: "coordinator"}))
	require.NotEmpty(t, w.s.MemberCtl("m1").F("held"))

	held := noMemberNote(t, w, beating(w, "m1", "m2"))
	require.Contains(t, held.What, "held")
	require.Contains(t, held.What, "nova-sprint fleet up <member>")
	require.NotContains(t, held.What, "fleet beat")
	require.Equal(t, []string{"fleet up", "wait"}, held.Decisions)

	none := noMemberNote(t, w, beating(w))
	require.Contains(t, none.What, "nova-sprint fleet beat <member>")
	require.Equal(t, TickDecisions[NNoMember], none.Decisions)
}

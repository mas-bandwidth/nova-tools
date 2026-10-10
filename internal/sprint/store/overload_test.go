package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// A member whose cards are timing out is overloaded (overload.go; the owner, 2026-10-03:
// "the overload is defined as -- cards are timing out. not any CPU%"): three cards ended on
// a timeout of any kind within the window, counted from the finishes the member reported,
// raise "a member is overloaded" naming each card and its kind, offering half the width or
// a wait; two do not; the judgment closes once the window holds fewer than three. On the
// mem twin, the clock injected, no real time.
func TestTheTickRaisesOverloadedOnThreeTimeoutsInTheWindow(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"), route("flash-b", "flash"))
	h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: "m2"})) // every card goes to m1
	h.addReady("s1", 3, briefOf("flash", ""))
	h.startMachine()
	h.machine()
	for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
		require.Equal(t, sprint.Working, h.state(id))
	}
	const usageLine = "budget: unverifiable: the usage source stopped answering, tokens 12,345, $0.10: no RESULT.md shape"
	h.failTake("s1-1.w1", cardhdr.EndStaging+": "+sprint.TimeoutStaging)
	h.tick(time.Minute)
	h.failTake("s1-2.w1", "deadline: no RESULT.md shape")
	h.machine()
	assert.Empty(t, h.openOf(sprint.NOverloaded), "two timeouts are not an overload")
	h.tick(time.Minute)
	h.failTake("s1-3.w1", usageLine)
	h.machine()
	open := h.openOf(sprint.NOverloaded)
	require.Len(t, open, 1, "three timeouts within the window")
	n := open[0].Note
	assert.Equal(t, "m1 is overloaded: 3 cards ended on a timeout in the last 15m0s: s1-1.w1 (stage-timeout), s1-2.w1 (deadline), s1-3.w1 (usage source); halve its width: nova-sprint fleet up m1 --width 512, or wait 15m", n.What)
	assert.Equal(t, []string{"fleet up m1 --width 512", "wait 15m"}, n.Decisions)
	cmds := h.commandsOf(sprint.NOverloaded)
	require.Len(t, cmds, 2)
	assert.Equal(t, []string{"nova-sprint fleet up m1 --width 512"}, cmds[0].Lines)
	assert.Equal(t, "nova-sprint wait "+n.ID+" --for 15m", cmds[1].Lines[0])
	h.machine()
	assert.Len(t, h.openOf(sprint.NOverloaded), 1, "raised once while it holds")
	assert.Equal(t, 1, h.written(sprint.NOverloaded))
	// the window moves past the first timeout: two remain, and the judgment closes
	h.tick(sprint.OverloadWindow - time.Minute)
	h.machine()
	assert.Empty(t, h.openOf(sprint.NOverloaded), "the window holds two")
	o, ok := sprint.Overloaded(h.snap(), "m1")
	assert.False(t, ok, "%+v", o)
	assert.Len(t, sprint.MemberTimeouts(h.snap(), "m1"), 2)
	h.clean("overloaded, then not")
}

func TestTimeoutKindReadsTheMembersReports(t *testing.T) {
	t.Parallel()
	for report, kind := range map[string]string{
		"staging refused: stage-timeout": sprint.TimeoutStaging,
		"stage-timeout":                  sprint.TimeoutStaging,
		"deadline: no RESULT.md shape":   sprint.TimeoutDeadline,
		"budget: unverifiable: the usage source stopped answering, tokens 1, $0": sprint.TimeoutUsage,
		"staging refused: no bench mirror":                                       "",
		"provider failure: 529":                                                  "",
		"budget: tokens 100 of 100: no RESULT.md shape":                          "",
		"verdict not-done; tests red":                                            "",
	} {
		assert.Equal(t, kind, sprint.TimeoutKind(report), report)
	}
}

package store

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A card's deadline starts at its take (nova-tools#5096 item 22: nine "a work card is
// past its deadline" judgments at once on a bench whose 16 cards dealt ahead aged in
// ready). A member of width 1 holds two cards: it works one while the other waits in
// its ready queue, and the waiting card is the machine's queue, not the card's fault:
// no judgment while the member works within its take deadline. Only the dealt bound
// (the sprint's `set --dealt-max`, else three take deadlines) catches a card nobody
// takes, and its judgment says where it waits, with the fleet's answers. All on the
// tick's injected clock.
func TestACardWaitingInAMembersQueueIsLateOnlyPastTheDealtBound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.live = []string{"m1"}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 1}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 2}))
	h.startMachine()
	h.machine()
	ready := h.snap().Fleet.Cell("m1", sprint.Ready)
	require.Len(t, ready, 2, "a member of width 1 holds two cards dealt")
	h.must(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}, Who: "m1"}))
	waiting := h.snap().Fleet.Cell("m1", sprint.Ready)
	require.Len(t, waiting, 1, "one card waits behind the one taken")
	late := func() []sprint.Open {
		var out []sprint.Open
		for _, o := range h.openOf(sprint.NWorkLate) {
			if o.Note.Card == waiting[0].ID {
				out = append(out, o)
			}
		}
		return out
	}
	for range 6 { // an hour and a half of the member working its card
		h.tick(15 * time.Minute)
		h.machine()
	}
	require.Empty(t, late(), "a card waiting in its member's queue while the member works is not late")
	h.must(SetStep(sprint.SetReq{DealtMax: "2h", Who: h.st.Actor}))
	h.tick(29 * time.Minute)
	h.machine()
	require.Empty(t, late(), "1h59m dealt, under the dealt bound of 2h")
	h.tick(2 * time.Minute)
	h.machine()
	got := late()
	require.Len(t, got, 1, "past the dealt bound: one judgment")
	assert.True(t, strings.Contains(got[0].Note.What, waiting[0].ID+" dealt, never taken, at m1:ready"), "the judgment says where the card waits: %s", got[0].Note.What)
	assert.Equal(t, []string{"fleet level", "fleet down m1", "wait"}, got[0].Note.Decisions)
	for _, c := range h.snap().Fleet.Cell("m1", sprint.Working) {
		h.must(FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Head: "h1", Who: "m1"}))
	}
	h.must(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}, Who: "m1"}))
	h.tick(time.Second)
	h.machine()
	assert.Empty(t, late(), "the take closes it: the card's own deadline starts now")
}

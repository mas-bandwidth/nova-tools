package store

import (
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// longTick makes the harness's tick run long: after the work table's update
// (the pump, the tick's long part over a slow link) the clock moves on by d,
// once, and the members that beat beat again at the new time, as they do every
// few seconds while a tick runs.
func (h *harness) longTick(d time.Duration) {
	var once sync.Once
	slow := sprint.TickPartDef{Name: "slow", Fn: func(*sprint.Snapshot, sprint.TickReq) (sprint.Plan, int) {
		once.Do(func() { h.tick(d) })
		return sprint.Plan{}, 0
	}}
	var ups []sprint.TableUpdate
	for _, u := range sprint.TickTables {
		if u.Table == sprint.Work {
			u.Parts = append(append([]sprint.TickPartDef(nil), u.Parts...), slow)
		}
		ups = append(ups, u)
	}
	h.st.Updates = ups
}

// A tick that runs long never makes a member look dead (2026-10-01, the fleet
// store: ticks of 20-68 s over the tailnet downed members that beat every 3 s,
// two at a time, and redealt their cards): a beat's age is measured against the
// tick's read that saw it, so members whose beats were fresh at the read stay
// up and keep their cards when the clock is 60 s on by the presence part.
func TestALongTickDownsNoMemberWhoseBeatWasFreshAtTheRead(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(4)
	h.startMachine()
	h.longTick(60 * time.Second)
	h.machine()
	s := h.snap()
	for _, m := range []string{"m1", "m2"} {
		require.Equal(t, sprint.Up, s.MemberCtl(m).F("status"), m)
		assert.Empty(t, h.memberNotes(sprint.NMemberDown, m), m)
	}
	assert.Empty(t, s.Fleet.Column(sprint.Withdrawn))
	assert.Equal(t, 4, h.dealtTo()["m1"]+h.dealtTo()["m2"])
}

// A member whose beat is older than MissedBeatsDown windows at the tick's read
// is down, whatever the tick's length.
func TestAMemberStaleAtTheReadIsDown(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(4)
	h.startMachine()
	h.machine()
	h.setLive("m2")
	h.tick(pastDown)
	h.longTick(60 * time.Second)
	h.machine()
	assert.Equal(t, sprint.Down, h.snap().MemberCtl("m1").F("status"))
	assert.Equal(t, sprint.Up, h.snap().MemberCtl("m2").F("status"))
	assert.Zero(t, h.dealtTo()["m1"])
}

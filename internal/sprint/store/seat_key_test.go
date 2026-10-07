package store

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nova-tools/internal/sprint"
)

// The seat key follows the seat record (docs/SPEC-SPRINT.md, "Handing over
// the seat", seat-key-follows-record.w2). On 2026-10-03 the server's unit kept
// NOVA_SPRINT_ACTOR=stella after the seat went to rowan; each restart left the
// key saying stella while the record said rowan, and the holder's coordinator
// verbs were refused until the key was set by hand. On the twin store: a server
// started as another actor (its record, init by its actor, start and a tick)
// leaves the key as the record says; the seat's state shows the key, the
// record's holder and the server's actor, and a drift between them; a repair
// by the holder writes the key from the record, logged with who and why, and
// anyone else's, or one with nothing to repair, is refused with nothing written.
func TestServerStartLeavesTheSeatKeyAndSeatRepairRestoresIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(SeatStep(sprint.SeatReq{To: "rowan", Who: h.st.Actor, Reason: "approved by glenn"}))
	rec, ok, err := h.st.Seat(h.ctx)
	require.NoError(t, err)
	require.True(t, ok, "the handover writes the record")

	// the server starts as stella: its record, init as its unit runs it, start, a tick
	srv := *h.st
	srv.Actor = "stella"
	require.NoError(t, srv.SetServerActor(h.ctx, srv.Actor))
	name, err := srv.InitSeat(h.ctx, srv.Actor)
	require.NoError(t, err)
	assert.Equal(t, "rowan", name, "init writes the key from the record, not its actor")
	_, _, _, err = srv.SetMachine(h.ctx, true)
	require.NoError(t, err)
	_, err = srv.Tick(h.ctx)
	require.NoError(t, err)
	key, err := h.m.Coordinator(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, "rowan", key, "a server started as stella left the key as the record says")

	s, err := h.st.SeatCheck(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, "rowan", s.Holder, "the key")
	assert.Equal(t, "rowan", s.Record, "the record's holder")
	assert.Equal(t, "stella", s.Server, "the server's actor")
	assert.Contains(t, s.Drift, "NOVA_SPRINT_ACTOR=stella to NOVA_SPRINT_ACTOR=rowan", "the server's actor is a drift: %q", s.Drift)

	// the key drifts (the old build's restart, or a write by hand)
	require.NoError(t, h.m.SetCoordinator(h.ctx, "stella"))
	s, err = h.st.SeatCheck(h.ctx)
	require.NoError(t, err)
	assert.Contains(t, s.Drift, "the key says stella and the record rowan", "the key's drift: %q", s.Drift)

	// a repair wants a reason, and the record's holder or the owner
	for _, r := range []sprint.SeatRepairReq{
		{Who: "rowan"},
		{Who: "stella", Reason: "the key is wrong"},
	} {
		_, why, err := h.st.SeatRepairStep(h.ctx, r)
		require.NoError(t, err)
		assert.NotEmpty(t, why, "%+v was not refused", r)
	}
	_, why, err := h.st.SeatRepairStep(h.ctx, sprint.SeatRepairReq{Who: "glenn", Reason: "the key is wrong", Owner: "glenn"})
	require.NoError(t, err)
	assert.Empty(t, why, "the owner may repair it")
	key, err = h.m.Coordinator(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, "stella", key, "a refused repair, or a planned one not run, wrote the key")

	step, why, err := h.st.SeatRepairStep(h.ctx, sprint.SeatRepairReq{Who: "rowan", Reason: "the server's restart set it from its actor"})
	require.NoError(t, err)
	require.Empty(t, why)
	res := h.must(step)
	assert.Empty(t, res.Refused)
	key, err = h.m.Coordinator(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, "rowan", key, "seat --repair restores the key from the record")
	after, _, err := h.st.Seat(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, rec, after, "a repair writes the record back unchanged")
	var logged []string
	for _, l := range h.lines() {
		if r := sprint.Render(l); strings.Contains(r, "repaired") {
			logged = append(logged, r)
		}
	}
	require.Len(t, logged, 1, "one line of the repair")
	assert.Contains(t, logged[0], "repaired: the key said stella, the record rowan: the server's restart set it from its actor, by rowan")

	_, why, err = h.st.SeatRepairStep(h.ctx, sprint.SeatRepairReq{Who: "rowan", Reason: "again"})
	require.NoError(t, err)
	assert.Contains(t, why, "as the record does", "nothing to repair")

	// the server's actor changed in its unit and restarted: no drift
	require.NoError(t, srv.SetServerActor(h.ctx, "rowan"))
	s, err = h.st.SeatCheck(h.ctx)
	require.NoError(t, err)
	assert.Empty(t, s.Drift)
	assert.Equal(t, SeatCheck{SeatState: SeatState{Holder: "rowan", Generation: rec.Generation}, Record: "rowan", Server: "rowan"}, s)

	// a server's record older than ServerTTL is no server; the machine is no one
	h.tick(ServerTTL + ServerEvery)
	s, err = h.st.SeatCheck(h.ctx)
	require.NoError(t, err)
	assert.Empty(t, s.Server, "a stale server's record")
	assert.Empty(t, sprint.SeatDrift("rowan", "rowan", sprint.MachineActor), "a server acting as the machine is no drift")
}

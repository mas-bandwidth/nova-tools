package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidSyncRefusesWhatTheFleetRefuses: a name or width fleet up would
// refuse, and a name twice, are refused whole.
func TestValidSyncRefusesWhatTheFleetRefuses(t *testing.T) {
	t.Parallel()
	why := ValidSync([]SyncMember{{"m1", 4}, {"m2", MaxWidth}})
	require.Empty(t, why, "a valid sync: %s", why)
	why = ValidSync(nil)
	require.Empty(t, why, "an empty sync holds the fleet and is valid: %s", why)
	for _, c := range []struct {
		want []SyncMember
		say  string
	}{
		{[]SyncMember{{"m 1", 4}}, "a member name wants"},
		{[]SyncMember{{"m1", 0}}, "a width wants a whole number from 1"},
		{[]SyncMember{{"m1", MaxWidth + 1}}, "a width wants a whole number from 1"},
		{[]SyncMember{{"m1", 1}, {"m1", 2}}, "m1 is named twice"},
	} {
		why = ValidSync(c.want)
		assert.Contains(t, why, c.say, "%v: %q, want %q", c.want, why, c.say)
	}
}

// TestDriftSaysWhatASyncWouldWrite: one line for each kind of drift.
func TestDriftSaysWhatASyncWouldWrite(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		d   Drift
		say string
	}{
		{Drift{Member: "m1", Kind: DriftAdd, To: 4}, "m1 is a member of the inventory and not of the fleet: add it at width 4"},
		{Drift{Member: "m1", Kind: DriftWidth, From: 4, To: 6}, "m1 has width 4 and the inventory says 6"},
		{Drift{Member: "m1", Kind: DriftHold}, "m1 is a member of the fleet and not of the inventory: hold it down"},
	} {
		got := c.d.Line()
		assert.Equal(t, c.say, got, "%q, want %q", got, c.say)
	}
}

// syncHeld is a world with members m1 and m2 up, and m2 held by a sync that
// names m1 alone.
func syncHeld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, "reader-a")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2"}))
	w.must(FleetStep(w.s, FleetReq{Op: "sync", Who: "sync", Sync: []SyncMember{{"m1", DefaultWidth}}}))
	ctl := w.s.MemberCtl("m2")
	require.NotEmpty(t, ctl.F("held"), "m2 is not held by the sync: %v", ctl.Fields)
	require.Equal(t, HeldBySync, ctl.F(FieldHeldBy), "m2 is not held by the sync: %v", ctl.Fields)
	return w
}

// TestACoordinatorsHoldOnAMemberTheSyncHoldsClearsTheMark: fleet down on a
// member the sync already holds makes the hold the coordinator's, so the sync
// never releases it (fleetStep, downPlan).
func TestACoordinatorsHoldOnAMemberTheSyncHoldsClearsTheMark(t *testing.T) {
	t.Parallel()
	w := syncHeld(t)
	w.must(FleetStep(w.s, FleetReq{Op: "hold", Member: "m2", Who: "coordinator"}))
	ctl := w.s.MemberCtl("m2")
	require.NotEmpty(t, ctl.F("held"), "m2 after the coordinator's hold: %v", ctl.Fields)
	require.Empty(t, ctl.F(FieldHeldBy), "m2 after the coordinator's hold: %v", ctl.Fields)
	for _, d := range FleetDrift(w.s, []SyncMember{{"m1", DefaultWidth}, {"m2", DefaultWidth}}) {
		require.NotEqual(t, DriftRelease, d.Kind, "the sync would release the coordinator's hold: %+v", d)
	}
}

// TestFleetUpClearsTheSyncsMark: releasing a member the sync held leaves no
// mark behind for a later hold to inherit.
func TestFleetUpClearsTheSyncsMark(t *testing.T) {
	t.Parallel()
	w := syncHeld(t)
	w.must(FleetStep(w.s, FleetReq{Op: "release", Member: "m2", Who: "coordinator"}))
	ctl := w.s.MemberCtl("m2")
	require.Empty(t, ctl.F("held"), "m2 after fleet up: %v", ctl.Fields)
	require.Empty(t, ctl.F(FieldHeldBy), "m2 after fleet up: %v", ctl.Fields)
}

// TestTheSyncsReleaseClearsItsMark.
func TestTheSyncsReleaseClearsItsMark(t *testing.T) {
	t.Parallel()
	w := syncHeld(t)
	w.must(FleetStep(w.s, FleetReq{Op: "sync", Who: "sync", Sync: []SyncMember{{"m1", DefaultWidth}, {"m2", DefaultWidth}}}))
	ctl := w.s.MemberCtl("m2")
	require.Empty(t, ctl.F("held"), "m2 after the sync's release: %v", ctl.Fields)
	require.Empty(t, ctl.F(FieldHeldBy), "m2 after the sync's release: %v", ctl.Fields)
}

// TestAHoldClearsAStaleMark: a member with a mark and no hold (a card written
// before fleet up cleared marks) is held by the coordinator without the
// mark, so the sync cannot take the hold for its own.
func TestAHoldClearsAStaleMark(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	ctl := w.s.MemberCtl("m1")
	w.must(Plan{Units: []Unit{{Key: CtlID("m1"), Changes: []Change{change(Fleet, setEntry(ctl, map[string]string{FieldHeldBy: HeldBySync}))}}}})
	require.Equal(t, HeldBySync, w.s.MemberCtl("m1").F(FieldHeldBy), "the stale mark is not set: %v", w.s.MemberCtl("m1").Fields)
	require.Empty(t, w.s.MemberCtl("m1").F("held"), "the stale mark is not set: %v", w.s.MemberCtl("m1").Fields)
	w.must(FleetStep(w.s, FleetReq{Op: "hold", Member: "m1", Who: "coordinator"}))
	ctl = w.s.MemberCtl("m1")
	require.NotEmpty(t, ctl.F("held"), "m1 after the coordinator's hold: %v", ctl.Fields)
	require.Empty(t, ctl.F(FieldHeldBy), "m1 after the coordinator's hold: %v", ctl.Fields)
}

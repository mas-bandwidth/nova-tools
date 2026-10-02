package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// TestFleetSyncRemovesAMemberWithNoMachineRow (nova-tools#5091): a member whose
// machine row is gone from the inventory leaves the fleet once no card stays on
// it: the sync deals its ready and working cards away and, when none stays, takes
// its control card off and deletes its row in the same run, one MOVED line, so its
// width leaves the fleet's total; while cards stay on it (withdrawn, no room for
// them) it stays held and a NOTE says so. A second sync writes nothing. A machine
// row that comes back puts the member back, released by the sync; fleet up does
// the same for a member the sync removed.
func TestFleetSyncRemovesAMemberWithNoMachineRow(t *testing.T) {
	t.Parallel()
	t.Run("no card on it: removed at once", func(t *testing.T) {
		t.Parallel()
		ta, inv := syncApp(t)
		inv.set("m1", 4)
		inv.set("m2", 4)
		ta.ok("fleet sync")
		ta.ok("start")
		ta.ok("tick")
		inv.remove("m2")
		out := ta.ok("fleet sync")
		assert.Contains(t, out, "MOVED m2 removed: no machine row in the inventory, and no card stays on it; its row and its width leave the fleet")
		rows := ta.fleetRows()
		assert.Nil(t, rows["m2"], "m2's row is deleted: %v", rows)
		assert.Equal(t, "4", rows["m1"]["width"])
		assert.Contains(t, ta.ok("where"), "      |     0 |       0 |     4 |", "the fleet's width total drops to m1's 4")
		assert.Contains(t, ta.ok("fleet sync"), "nothing to do", "a sync after the removal")
		code, check, _ := ta.do("fleet sync --check")
		assert.Equal(t, 0, code, check)
		// the machine row comes back: the member rejoins, released, at its new width
		inv.set("m2", 6)
		out = ta.ok("fleet sync")
		assert.Contains(t, out, "NOTE m2 rejoins the fleet")
		assert.Contains(t, out, "MOVED m2 released, down until it beats, width=6 (was 4)")
		ta.ok("tick")
		assert.Equal(t, sprint.Up, ta.fleetRows()["m2"]["status"], "m2 back up when it beats")
		assert.Contains(t, ta.ok("fleet sync"), "nothing to do")
		ta.clean()
	})
	t.Run("its cards dealt away: removed in the same sync", func(t *testing.T) {
		t.Parallel()
		ta, inv := syncApp(t)
		inv.set("m1", 8)
		inv.set("m2", 8)
		ta.ok("fleet sync")
		ta.ok("add --stream s1 --count 8")
		ta.ok("start")
		ta.ok("tick")
		ta.ok("tick")
		require.NotEqual(t, "0", ta.fleetRows()["m2"]["ready"], "the test wants cards on m2: %v", ta.fleetRows())
		inv.remove("m2")
		out := ta.ok("fleet sync")
		assert.Contains(t, out, "-> m1:ready")
		assert.Contains(t, out, "m2 removed: no machine row in the inventory, and no card stays on it")
		rows := ta.fleetRows()
		assert.Nil(t, rows["m2"], "m2's row is deleted: %v", rows)
		assert.Equal(t, "8", rows["m1"]["ready"], "every card on m1: %v", rows)
		assert.Contains(t, ta.ok("fleet sync"), "nothing to do")
		ta.clean()
	})
	t.Run("cards stay on it: held until none does", func(t *testing.T) {
		t.Parallel()
		ta, inv := syncApp(t)
		inv.set("m1", 1)
		inv.set("m2", 8)
		ta.ok("fleet sync")
		ta.ok("add --stream s1 --count 8")
		ta.ok("start")
		ta.ok("tick")
		ta.ok("tick")
		inv.remove("m2")
		out := ta.ok("fleet sync")
		assert.Contains(t, out, "m2 held down")
		assert.Contains(t, out, "NOTE m2 has no machine row in the inventory and")
		assert.Equal(t, sprint.Held, ta.fleetRows()["m2"]["status"])
		assert.Contains(t, ta.ok("fleet sync"), "nothing to do", "held, a second sync writes nothing")
		// room on m1: the next tick deals the withdrawn cards there, and the sync removes m2
		inv.set("m1", 8)
		ta.ok("fleet sync")
		ta.ok("tick")
		out = ta.ok("fleet sync")
		assert.Contains(t, out, "MOVED m2 removed")
		assert.Nil(t, ta.fleetRows()["m2"])
		ta.clean()
	})
	t.Run("fleet up brings a removed member back", func(t *testing.T) {
		t.Parallel()
		ta, inv := syncApp(t)
		inv.set("m1", 4)
		inv.set("m2", 4)
		ta.ok("fleet sync")
		inv.remove("m2")
		ta.ok("fleet sync")
		require.Nil(t, ta.fleetRows()["m2"])
		ta.ok("fleet up m2")
		rows := ta.fleetRows()
		require.NotNil(t, rows["m2"], "fleet up of a removed member: %v", rows)
		assert.Equal(t, sprint.Up, rows["m2"]["status"])
		assert.Equal(t, "4", rows["m2"]["width"])
	})
}

// TestFleetSyncRemovalDeletesTheBeat: the removed member's beat record goes with
// its row, so teardown never meets a key of a row it cannot read.
func TestFleetSyncRemovalDeletesTheBeat(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	ta.live = []string{"m1"}
	inv.set("m1", 4)
	inv.set("m2", 4)
	ta.ok("fleet sync")
	ta.ok("fleet up m2") // a beat of m2
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	beats, err := st.Beats(context.Background(), []string{"m2"})
	require.NoError(t, err)
	require.Contains(t, beats, "m2", "the test wants a beat of m2")
	inv.remove("m2")
	ta.ok("fleet sync")
	beats, err = st.Beats(context.Background(), []string{"m2"})
	require.NoError(t, err)
	assert.NotContains(t, beats, "m2", "the removed member's beat is deleted")
}

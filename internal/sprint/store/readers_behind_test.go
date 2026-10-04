package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The readers are behind (readers_behind.go): reads asked and not begun on a reader up for
// the window raise one judgment naming review, each reader's reading beside its width (its
// machine row's, Snapshot.ReaderWidth) and what it needs; a reader reading under its width,
// a loop that kept the width it started with, is restarted; a reader reading its width waits
// on its machine; the judgment closes when the reads begin. Injected clock.
func TestTheTickRaisesReadersBehindWhenReadsWaitTheWindow(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"))
	require.NoError(t, h.m.RowsAdd(h.ctx, "t-readers", []string{"reader-m1"}))
	readersBehind(h, h.openOf, h.written)
}

// readersBehind drives the readers behind on a harness whose members m1 and m2 are up, with
// a flash route and the reader reader-m1: open and written read the store's judgments (the
// Mem's or the Redis's, readers_behind_functional_test.go).
func readersBehind(h *harness, open func(string) []sprint.Open, written func(string) int) {
	t := h.t
	h.beat()
	for _, r := range []string{"reader-a", "reader-b", "reader-c"} {
		require.NoError(t, h.st.SetReaderAway(h.ctx, r, true, "tester"))
	}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 6}))
	h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: "m2"}))
	h.addReady("s1", 6, briefOf("flash", ""))
	h.startMachine()
	h.machine()
	for i := 1; i <= 6; i++ {
		id := "s1-" + string(rune('0'+i))
		h.machine()
		h.finishAttempt(id, false, "h"+id)
	}
	h.machine()
	require.Len(t, h.snap().Readers.Cell("reader-m1", sprint.Asked), 6, "six reads asked of the one reader up, its width six")
	// its loop reads two, the width it started with
	h.must(ReadStep(sprint.ReadReq{As: "reader-m1", Begin: true, Sel: sprint.Sel{Limit: 2}, Who: "reader-m1"}))
	h.machine()
	assert.Empty(t, open(sprint.NReadersBehind), "not yet the window")
	h.tick(sprint.ReadersWindow)
	h.machine()
	behind := open(sprint.NReadersBehind)
	require.Len(t, behind, 1)
	assert.Equal(t, "the readers are behind: review 6, reads asked and not begun past 10m0s; the readers read 2 of width 6 (reader-m1 reads 2 of width 6, 4 waiting past the window: it reads under its width, restart its loop (nova-config loop show reader-m1))", behind[0].Note.What)
	assert.Equal(t, []string{"restart reader-m1", "wait 10m"}, behind[0].Note.Decisions)
	cmds := h.commandsOf(sprint.NReadersBehind)
	require.Len(t, cmds, 2)
	assert.Contains(t, cmds[0].Lines[0], "nova-config loop show reader-m1")
	assert.Equal(t, "nova-sprint wait "+behind[0].Note.ID+" --for 10m", cmds[1].Lines[0])
	// the machine is narrowed to two: the reader reads its width, and the machine is the bound
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 2}))
	h.machine()
	behind = open(sprint.NReadersBehind)
	require.Len(t, behind, 1, "updated in place")
	assert.Equal(t, 1, written(sprint.NReadersBehind))
	assert.Equal(t, "the readers are behind: review 6, reads asked and not begun past 10m0s; the readers read 2 of width 2 (reader-m1 reads 2 of width 2, 4 waiting past the window)", behind[0].Note.What)
	assert.Equal(t, []string{"wait 10m"}, behind[0].Note.Decisions, "at its width: nothing to restart")
	// the reader begins the rest: the readers are not behind
	h.must(ReadStep(sprint.ReadReq{As: "reader-m1", Begin: true, Sel: sprint.Sel{Limit: 4}, Who: "reader-m1"}))
	h.machine()
	assert.Empty(t, open(sprint.NReadersBehind))
	_, late := sprint.ReadersBehind(h.snap())
	assert.False(t, late)
	h.clean("the readers caught up")
}

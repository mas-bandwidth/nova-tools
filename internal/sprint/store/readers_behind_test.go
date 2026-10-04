package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The readers are behind (readers_behind.go): reads asked and not begun on a reader up for
// the window raise one judgment naming review, each reader's reading and width beside its
// machine's width, and what it needs; a reader reading below its widened machine's width
// lags it and is restarted; the judgment closes when the reads begin. Injected clock.
func TestTheTickRaisesReadersBehindWhenReadsWaitTheWindow(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"))
	require.NoError(t, h.m.RowsAdd(h.ctx, "t-readers", []string{"reader-m1"}))
	h.beat()
	for _, r := range []string{"reader-a", "reader-b", "reader-c"} {
		require.NoError(t, h.st.SetReaderAway(h.ctx, r, true, "tester"))
	}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 2}))
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
	require.Len(t, h.snap().Readers.Cell("reader-m1", sprint.Asked), 6, "six reads asked of the one reader up")
	h.must(ReadStep(sprint.ReadReq{As: "reader-m1", Begin: true, Sel: sprint.Sel{Limit: 2}, Who: "reader-m1"}))
	h.machine()
	assert.Empty(t, h.openOf(sprint.NReadersBehind), "not yet the window")
	h.tick(sprint.ReadersWindow)
	h.machine()
	open := h.openOf(sprint.NReadersBehind)
	require.Len(t, open, 1)
	assert.Equal(t, "the readers are behind: review 6, reads asked and not begun past 10m0s; the readers read 2 of width 2 (reader-m1 reads 2 of width 2 on a machine of width 2, 4 waiting past the window)", open[0].Note.What)
	assert.Equal(t, []string{"wait 10m"}, open[0].Note.Decisions, "at its machine's width: nothing to restart")
	// the machine is widened and the reader keeps reading two: its width lags its machine
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 4}))
	h.machine()
	open = h.openOf(sprint.NReadersBehind)
	require.Len(t, open, 1, "updated in place")
	assert.Equal(t, 1, h.written(sprint.NReadersBehind))
	assert.Equal(t, "the readers are behind: review 6, reads asked and not begun past 10m0s; the readers read 2 of width 2 (reader-m1 reads 2 of width 2 on a machine of width 4, 4 waiting past the window: its width lags its machine, restart its loop (nova-config loop show reader-m1))", open[0].Note.What)
	assert.Equal(t, []string{"restart reader-m1", "wait 10m"}, open[0].Note.Decisions)
	cmds := h.commandsOf(sprint.NReadersBehind)
	require.Len(t, cmds, 2)
	assert.Contains(t, cmds[0].Lines[0], "nova-config loop show reader-m1")
	assert.Equal(t, "nova-sprint wait "+open[0].Note.ID+" --for 10m", cmds[1].Lines[0])
	// the restarted reader begins the rest: the readers are not behind
	h.must(ReadStep(sprint.ReadReq{As: "reader-m1", Begin: true, Sel: sprint.Sel{Limit: 4}, Who: "reader-m1"}))
	h.machine()
	assert.Empty(t, h.openOf(sprint.NReadersBehind))
	_, behind := sprint.ReadersBehind(h.snap())
	assert.False(t, behind)
	h.clean("the readers caught up")
}

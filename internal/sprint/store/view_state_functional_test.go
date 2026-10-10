//go:build functional

package store

import (
	"fmt"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// liveViewLine is the summary line nova-table renders for the sprint's stored
// view, read from the store: ntable.SummaryLine over the view and its first
// table.
func liveViewLine(h *harness, c *redis.Client) string {
	h.t.Helper()
	v, err := ntable.ViewGet(h.ctx, c, h.st.Names.View())
	require.NoError(h.t, err)
	tb, err := ntable.Read(h.ctx, c, v.Tables[0])
	require.NoError(h.t, err)
	return ntable.SummaryLine(v, tb)
}

// The sprint's stored view on a real store (docs/SPEC-SPRINT.md sections 1
// and 14): its summary line is STOPPED, and nothing more, after init, stop
// and clear, and the progress line after start; the machine's record and the
// view's state are written in one MULTI/EXEC, so a transaction the store
// refuses writes neither; and a sprint whose view is gone still starts and
// stops.
func TestTheViewSaysStoppedAloneOnARealStore(t *testing.T) {
	t.Parallel()
	h, c := liveHarness(t)
	got := liveViewLine(h, c)
	require.Equal(t, "STOPPED", got, "after init: %q", got)
	_, _, _, err := h.st.SetMachine(h.ctx, true)
	require.NoError(t, err)
	got = liveViewLine(h, c)
	require.Equal(t, "0/0 0.0% -> ETA", got, "after start: %q", got)
	_, _, _, err = h.st.SetMachine(h.ctx, false)
	require.NoError(t, err)
	got = liveViewLine(h, c)
	require.Equal(t, "STOPPED", got, "after stop: %q", got)
	_, _, _, err = h.st.SetMachine(h.ctx, true)
	require.NoError(t, err)
	_, err = h.st.Clear(h.ctx)
	require.NoError(t, err)
	got = liveViewLine(h, c)
	require.Equal(t, "STOPPED", got, "after clear: %q", got)

	// A view stored without a state while STOPPED: stop writes it again.
	require.NoError(t, c.HDel(h.ctx, "view:"+h.st.Names.View(), "state").Err())
	_, _, _, err = h.st.SetMachine(h.ctx, false)
	require.NoError(t, err)
	got = liveViewLine(h, c)
	require.Equal(t, "STOPPED", got, "stop on a stopped machine, view without a state: %q", got)

	// Both or neither: a writer the store will not let call the view's
	// function has its whole transaction refused, the record included.
	require.NoError(t, c.ACLSetUser(h.ctx, "noview", "on", ">pw", "~*", "+@all", "-fcall").Err())
	opts := *c.Options()
	opts.Username, opts.Password = "noview", "pw"
	nc := redis.NewClient(&opts)
	t.Cleanup(func() { _ = nc.Close() })
	before := c.Get(h.ctx, h.st.Names.Key(keyMachine)).Val()
	denied := &Store{B: &Redis{C: nc, Names: h.st.Names, Now: h.st.Now}, Names: h.st.Names, Actor: "tester", Now: h.st.Now}
	_, _, _, err = denied.SetMachine(h.ctx, true)
	require.Error(t, err, "start with the view's write refused: no error")
	after := c.Get(h.ctx, h.st.Names.Key(keyMachine)).Val()
	require.Equal(t, before, after, "a refused transaction wrote the record:\n%s\n%s", before, after)
	got = liveViewLine(h, c)
	require.Equal(t, "STOPPED", got, "after the refused start: %q", got)

	// No view: nothing shows the machine, and start and stop still work.
	_, err = ntable.ViewDelete(h.ctx, c, h.st.Names.View())
	require.NoError(t, err)
	if _, after, _, err := h.st.SetMachine(h.ctx, true); err != nil || !after.Running() {
		require.Fail(t, fmt.Sprintf("start with no view: %v %+v", err, after))
	}
	if _, after, _, err := h.st.SetMachine(h.ctx, false); err != nil || after.Running() {
		require.Fail(t, fmt.Sprintf("stop with no view: %v %+v", err, after))
	}
}

//go:build functional

package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

// liveViewLine is the summary line nova-table renders for the sprint's stored
// view, read from the store: ntable.SummaryLine over the view and its first
// table.
func liveViewLine(h *harness, c *redis.Client) string {
	h.t.Helper()
	v, err := ntable.ViewGet(h.ctx, c, h.st.Names.View())
	if err != nil {
		h.t.Fatal(err)
	}
	tb, err := ntable.Read(h.ctx, c, v.Tables[0])
	if err != nil {
		h.t.Fatal(err)
	}
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
	if got := liveViewLine(h, c); got != "STOPPED" {
		t.Fatalf("after init: %q", got)
	}
	if _, _, _, err := h.st.SetMachine(h.ctx, true); err != nil {
		t.Fatal(err)
	}
	if got := liveViewLine(h, c); got != "0/0 0.0% -> ETA" {
		t.Fatalf("after start: %q", got)
	}
	if _, _, _, err := h.st.SetMachine(h.ctx, false); err != nil {
		t.Fatal(err)
	}
	if got := liveViewLine(h, c); got != "STOPPED" {
		t.Fatalf("after stop: %q", got)
	}
	if _, _, _, err := h.st.SetMachine(h.ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Clear(h.ctx); err != nil {
		t.Fatal(err)
	}
	if got := liveViewLine(h, c); got != "STOPPED" {
		t.Fatalf("after clear: %q", got)
	}

	// A view stored without a state while STOPPED: stop writes it again.
	if err := c.HDel(h.ctx, "view:"+h.st.Names.View(), "state").Err(); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := h.st.SetMachine(h.ctx, false); err != nil {
		t.Fatal(err)
	}
	if got := liveViewLine(h, c); got != "STOPPED" {
		t.Fatalf("stop on a stopped machine, view without a state: %q", got)
	}

	// Both or neither: a writer the store will not let call the view's
	// function has its whole transaction refused, the record included.
	if err := c.ACLSetUser(h.ctx, "noview", "on", ">pw", "~*", "+@all", "-fcall").Err(); err != nil {
		t.Fatal(err)
	}
	opts := *c.Options()
	opts.Username, opts.Password = "noview", "pw"
	nc := redis.NewClient(&opts)
	t.Cleanup(func() { _ = nc.Close() })
	before := c.Get(h.ctx, h.st.Names.Key(keyMachine)).Val()
	denied := &Store{B: &Redis{C: nc, Names: h.st.Names, Now: h.st.Now}, Names: h.st.Names, Actor: "tester", Now: h.st.Now}
	if _, _, _, err := denied.SetMachine(h.ctx, true); err == nil {
		t.Fatal("start with the view's write refused: no error")
	}
	if after := c.Get(h.ctx, h.st.Names.Key(keyMachine)).Val(); after != before {
		t.Fatalf("a refused transaction wrote the record:\n%s\n%s", before, after)
	}
	if got := liveViewLine(h, c); got != "STOPPED" {
		t.Fatalf("after the refused start: %q", got)
	}

	// No view: nothing shows the machine, and start and stop still work.
	if _, err := ntable.ViewDelete(h.ctx, c, h.st.Names.View()); err != nil {
		t.Fatal(err)
	}
	if _, after, _, err := h.st.SetMachine(h.ctx, true); err != nil || !after.Running() {
		t.Fatalf("start with no view: %v %+v", err, after)
	}
	if _, after, _, err := h.st.SetMachine(h.ctx, false); err != nil || after.Running() {
		t.Fatalf("stop with no view: %v %+v", err, after)
	}
}

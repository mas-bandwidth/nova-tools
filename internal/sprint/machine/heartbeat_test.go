package machine

import (
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
)

// readBeat is the heartbeat and the clock as `inbox` and `where` read them,
// with the read's wall time: no loop is involved.
func readBeat(t *testing.T, w *world) (Heartbeat, Clock, int64) {
	t.Helper()
	hb, _, err := w.tw.KeyQuery(sprintfn.KeyQ{Kind: sprintfn.KeyHeartbeat})
	if err != nil {
		t.Fatal(err)
	}
	cr, _, err := w.tw.KeyQuery(sprintfn.KeyQ{Kind: sprintfn.KeyClock})
	if err != nil {
		t.Fatal(err)
	}
	c := cr.(sprintfn.ClockResult)
	wall, _ := strconv.ParseInt(c.WallMS, 10, 64)
	return ParseHeartbeat(hb.(sprintfn.HeartbeatResult).Fields), ClockOf(c), wall
}

// TestMachineNotTicking: a RUNNING machine whose tick_at is older than 15 s
// is "not ticking", computed from the heartbeat and the clock at read time
// with no loop running (1.4.1); the view's line says NOT TICKING and the
// seconds since the last tick from 5 s, and never STOPPED for it.
func TestMachineNotTicking(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	k := &counting{c: w.tw}
	l := w.loop("a", []sprint.Rule{dealRule(64)}, Budget{})
	w.tick(l, k) // learns the cursor
	w.clk.add(TickEvery)
	w.tick(l, k) // ingests the row's line
	w.clk.add(TickEvery)
	w.tick(l, k) // writes the second tick's fields: nothing behind
	hb, c, wall := readBeat(t, w)
	if hb.TickAt == 0 || hb.Owner != "token-a" || hb.Ticks != 2 || hb.Backlog != 0 {
		t.Fatalf("the heartbeat after two ticks: %+v", hb)
	}
	if groups := InboxGroups(hb, c, wall); len(groups) != 0 {
		t.Fatalf("a ticking machine has groups: %+v", groups)
	}
	if line := MachineLine(hb, c, wall); line != "running" {
		t.Fatalf("the line of a ticking machine: %q", line)
	}
	// The loop stops; nobody ticks for 16 s.
	w.clk.add(16 * time.Second)
	hb, c, wall = readBeat(t, w)
	groups := InboxGroups(hb, c, wall)
	if len(groups) != 1 || groups[0].ID != GroupNotTicking || len(groups[0].Commands) != 2 ||
		groups[0].Commands[0].Decision != "start the run loop" || groups[0].Commands[1].Decision != "stop" {
		t.Fatalf("groups %+v", groups)
	}
	if line := MachineLine(hb, c, wall); line != "NOT TICKING 17s" {
		t.Fatalf("the line: %q", line)
	}
	// 5 s is where the line starts saying it; the group waits for 15.
	hb.TickAt = wall - 6000
	if line := MachineLine(hb, c, wall); line != "NOT TICKING 6s" || len(InboxGroups(hb, c, wall)) != 0 {
		t.Fatalf("at 6 s: %q, %+v", line, InboxGroups(hb, c, wall))
	}
	// Catching up is said at once (T3).
	hb.TickAt, hb.Backlog, hb.Agenda, hb.DueNow = wall, 12, "2000+", 3
	if line := MachineLine(hb, c, wall); line != "running (catching up: 12 lines, 2000+ keys, 3 due)" {
		t.Fatalf("catching up: %q", line)
	}
}

// TestNoLoopLooking: a STOPPED machine whose looked_at is older than 15 s has
// "no run loop looking" (R17 cannot name moves due), with no loop running; its
// line says STOPPED, never NOT TICKING (1.4.1).
func TestNoLoopLooking(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	w.step(&sprintfn.Request{Epoch: "0", Meta: sprintfn.Meta{Verb: "init", Actor: "coordinator"}, Clock: &sprintfn.ClockPart{Verb: sprintfn.ClockInit}})
	k := &counting{c: w.tw}
	l := w.loop("a", nil, Budget{})
	w.tick(l, k)
	w.clk.add(TickEvery)
	w.tick(l, k)
	hb, c, wall := readBeat(t, w)
	if !c.Stopped || hb.LookedAt == 0 {
		t.Fatalf("a STOPPED loop's heartbeat: %+v, clock %+v", hb, c)
	}
	if groups := InboxGroups(hb, c, wall); len(groups) != 0 {
		t.Fatalf("a looking loop has groups: %+v", groups)
	}
	w.clk.add(16 * time.Second)
	hb, c, wall = readBeat(t, w)
	groups := InboxGroups(hb, c, wall)
	if len(groups) != 1 || groups[0].ID != GroupNoLoop || groups[0].Commands[0].Decision != "start the run loop" {
		t.Fatalf("groups %+v", groups)
	}
	if line := MachineLine(hb, c, wall); line != "STOPPED" {
		t.Fatalf("the line of a STOPPED machine: %q", line)
	}
}

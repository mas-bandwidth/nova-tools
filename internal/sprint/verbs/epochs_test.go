package verbs

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// noticeLines is how many "the sprint was cleared" notices epoch e holds.
func noticeLines(w *world, e tset.Decimal) int {
	n := 0
	for _, l := range w.log.Lines(testPrefix, e) {
		if strings.Contains(string(l), noticeCleared) {
			n++
		}
	}
	return n
}

// TestClearStartBetweenParts: a start between clear's parts leaves no later
// part written and no "the sprint was cleared, and is STOPPED" line on a
// running machine: the part refuses MACHINESTATE from its read (clearRest),
// and a start between that read and the step is refused at apply by the clock
// guard, then read again and refused (errata 1; 1.3.5). After a stop, the same
// command with --op and --epoch goes on.
func TestClearStartBetweenParts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const members, streams = 250, 10
	for _, c := range []struct {
		name  string
		hook  func(f *faulty, start func())
		steps int
	}{
		{"before part 2's read", func(f *faulty, start func()) {
			f.after = func(n int) {
				if n == 1 {
					start()
				}
			}
		}, 1},
		{"between part 2's read and its step", func(f *faulty, start func()) {
			f.before = func(n int) {
				if n == 2 {
					start()
				}
			}
		}, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			w.initSprint()
			bigSprint(w, members, streams)
			f := &faulty{c: w.tw}
			c.hook(f, func() {
				if _, err := Start(ctx, w.at(1), ClockReq{}); err != nil {
					t.Errorf("the other start: %v", err)
				}
			})
			w.env.C = f
			_, err := Clear(ctx, w.env, ClearReq{Op: "c1", Confirm: testPrefix})
			rf := wantRefused(t, err, sprintfn.CodeMachineState)
			if rf.Part != 2 || !rf.Local || !strings.Contains(rf.Hint, "run the same command with --op c1 --epoch 0") {
				t.Fatalf("%v, want part 2 refused from its read, naming --op c1 --epoch 0", err)
			}
			if f.steps != c.steps {
				t.Fatalf("%d steps sent, want %d: part 1, and part 2 only when its read came before the start", f.steps, c.steps)
			}
			if got := noticeLines(w, "1"); got != 0 {
				t.Fatalf("%d cleared notices on a running machine", got)
			}
			if cl, _ := w.clock(1); !running(cl) {
				t.Fatal("the machine does not run")
			}
			// Stop, then the same command goes on from part 2.
			if _, err := Stop(ctx, w.at(1), ClockReq{}); err != nil {
				t.Fatal(err)
			}
			res, err := Clear(ctx, w.at(0), ClearReq{Op: "c1", Confirm: testPrefix})
			if err != nil || res.Resumed != 1 || res.Parts != 1 {
				t.Fatalf("resume after stop: %+v, %v; want part 2 after part 1", res, err)
			}
			ctlCount(t, w, members, streams)
			if got := noticeLines(w, "1"); got != 1 {
				t.Fatalf("%d cleared notices, want 1", got)
			}
		})
	}
}

// TestFreshOpTakesActiveEpoch: an op the caller gives, run from an Env whose
// epoch is behind the sprint's, takes the active epoch when done is absent
// from its look-back up to the active one: it never ran, so it is not refused
// (L1 5). A repeat from the old epoch finds its receipt at the new one.
func TestFreshOpTakesActiveEpoch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	w.initSprint()
	if _, err := Clear(ctx, w.env, ClearReq{Confirm: testPrefix}); err != nil {
		t.Fatal(err)
	}
	env := w.at(0)
	res, err := Start(ctx, env, ClockReq{Op: "fresh"})
	if err != nil || res.Replay || res.Epoch != 1 || env.Epoch != 1 || res.Trips != 4 {
		t.Fatalf("%+v, %v; want the start applied at epoch 1 (read, look-ahead, read, step)", res, err)
	}
	if c, _ := w.clock(1); !running(c) {
		t.Fatal("the start did not apply")
	}
	again, err := Start(ctx, w.at(0), ClockReq{Op: "fresh"})
	if err != nil || !again.Replay || again.Epoch != 1 {
		t.Fatalf("repeat: %+v, %v; want its receipt at epoch 1", again, err)
	}
}

// TestLookBackFloorsAtFirstEpoch: a sprint whose first kept epoch is 3 has
// no epoch marker at 1 or 2, and Layer 1 refuses a done query naming them
// EPOCHGONE; the look-back finds the first kept epoch and asks from it, so an
// --op verb runs, and a repeat after a clear finds its receipt (L1 5, 7).
func TestLookBackFloorsAtFirstEpoch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	newAt3 := func(t *testing.T) *world {
		w := newWorldAt(t, 3)
		w.initSprint()
		return w
	}
	t.Run("one step", func(t *testing.T) {
		t.Parallel()
		w := newAt3(t)
		res, err := Start(ctx, w.env, ClockReq{Op: "s"})
		if err != nil || res.Epoch != 3 || w.env.FirstEpoch != 3 {
			t.Fatalf("%+v, %v, first %d; want the start at epoch 3, the floor at 3", res, err, w.env.FirstEpoch)
		}
		if _, err := Stop(ctx, w.env, ClockReq{}); err != nil {
			t.Fatal(err)
		}
		if _, err := Clear(ctx, w.env, ClearReq{Confirm: testPrefix}); err != nil {
			t.Fatal(err)
		}
		again, err := Start(ctx, w.at(4), ClockReq{Op: "s"})
		if err != nil || !again.Replay || again.Epoch != 3 {
			t.Fatalf("repeat from epoch 4: %+v, %v; want its receipt at epoch 3", again, err)
		}
	})
	t.Run("in parts", func(t *testing.T) {
		t.Parallel()
		w := newAt3(t)
		res, err := w.env.Parts(ctx, "op-3", makeCards(4, 2))
		if err != nil || res.Parts != 2 || res.Epoch != 3 || w.env.FirstEpoch != 3 {
			t.Fatalf("%+v, %v, first %d; want 2 parts at epoch 3, the floor at 3", res, err, w.env.FirstEpoch)
		}
	})
}

// TestOpMovedWording: the refusals of an op whose epoch the sprint moved
// from name the op's epoch and the sprint's (L1 5; opMoved), on the read
// refusal paths of Do and Parts, and on Do's read that shows the sprint more
// epochs ahead than its look-ahead covers.
func TestOpMovedWording(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	check := func(t *testing.T, err error, code, msg string, opEpoch uint64) {
		t.Helper()
		rf := wantRefused(t, err, code)
		if !strings.Contains(rf.Refusal.Message, msg) || rf.OpEpoch != opEpoch || !strings.Contains(rf.Hint, "run the verb under a new op") {
			t.Fatalf("%v (op epoch %d), want %q", err, rf.OpEpoch, msg)
		}
	}
	t.Run("Do, the read refused: the op's epoch ahead", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.initSprint()
		_, err := Start(ctx, w.at(5), ClockReq{Op: "s"})
		check(t, err, sprintfn.CodeEpochAhead, "op s was at epoch 5; the sprint is at 0", 5)
	})
	t.Run("Parts, the read refused: the op's epoch ahead", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		_, err := w.at(5).Parts(ctx, "op-a", makeCards(4, 2))
		check(t, err, sprintfn.CodeEpochAhead, "op op-a was at epoch 5; the sprint is at 0", 5)
	})
	t.Run("Parts, a made op from an Env ahead plans at the active epoch", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		res, err := w.at(5).Parts(ctx, "", makeCards(4, 2))
		if err != nil || res.Epoch != 0 || res.Parts != 2 {
			t.Fatalf("%+v, %v; want 2 parts at epoch 0", res, err)
		}
	})
	t.Run("Do, the sprint past the look-ahead", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.initSprint()
		for i := 0; i < OpEpochsMax+1; i++ {
			if _, err := Clear(ctx, w.env, ClearReq{Confirm: testPrefix}); err != nil {
				t.Fatal(err)
			}
		}
		_, err := Start(ctx, w.at(0), ClockReq{Op: "s"})
		check(t, err, sprintfn.CodeStale, "op s was at epoch 0; the sprint is at 65", 0)
	})
}

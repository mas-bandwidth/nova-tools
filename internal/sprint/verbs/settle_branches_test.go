package verbs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// fenceOf is the fence of a step's identity, as the driver's fence sends it
// (L1 5): the same epoch, identity and intent, and nothing else.
func fenceOf(req *sprintfn.Request) *sprintfn.Request {
	return &sprintfn.Request{Fence: true, Epoch: req.Epoch, Meta: req.Meta,
		Body: sprintfn.Body{Op: &sprintfn.Op{ID: req.Body.Op.ID, Intent: req.Body.Op.Intent}}}
}

// wantUnknown holds err to an unknown outcome, never a refusal.
func wantUnknown(t *testing.T, err error) *Unknown {
	t.Helper()
	var u *Unknown
	var rf *Refused
	if !errors.As(err, &u) || !errors.Is(err, tset.ErrOutcomeUnknown) || errors.As(err, &rf) {
		t.Fatalf("err %v, want an unknown outcome, not a refusal", err)
	}
	return u
}

// wantRefused holds err to a final refusal of the code, never an unknown
// outcome.
func wantRefused(t *testing.T, err error, code string) *Refused {
	t.Helper()
	var rf *Refused
	var u *Unknown
	if !errors.As(err, &rf) || errors.As(err, &u) || rf.Code() != code {
		t.Fatalf("err %v, want a final %s refusal", err, code)
	}
	return rf
}

// TestLostStepSettledByDone: a step lost with the connection up is settled
// by done in the same run (L1 5, 8; settleLost): absent is not proof, so the
// outcome stays unknown; a fenced identity is final, FENCED; an identity
// applied with other arguments is final, OPCONFLICT.
func TestLostStepSettledByDone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	run := func(t *testing.T, lost func(w *world, req *sprintfn.Request)) (*world, *faulty, Result, error) {
		t.Helper()
		w := newWorld(t)
		w.initSprint()
		f := &faulty{c: w.tw, kill: func(n int) killMode {
			if n == 1 {
				return killLost
			}
			return killNone
		}}
		if lost != nil {
			f.lost = func(_ int, req *sprintfn.Request) { lost(w, req) }
		}
		w.env.C = f
		res, err := Start(ctx, w.env, ClockReq{Op: "s"})
		return w, f, res, err
	}
	t.Run("done absent: unknown", func(t *testing.T) {
		t.Parallel()
		w, _, res, err := run(t, nil)
		u := wantUnknown(t, err)
		if u.Op != "s" || u.Epoch != 0 || res.Trips != 3 {
			t.Fatalf("%+v, trips %d; want op s at epoch 0, settled by one done (3 trips)", u, res.Trips)
		}
		if c, _ := w.clock(0); running(c) {
			t.Fatal("a lost start moved the clock")
		}
	})
	t.Run("done fenced: FENCED, final", func(t *testing.T) {
		t.Parallel()
		_, _, _, err := run(t, func(w *world, req *sprintfn.Request) {
			if r := w.step(fenceOf(req)); r.Reply.Status != "fenced" {
				t.Errorf("the other fence: %q, want fenced", r.Reply.Status)
			}
		})
		if rf := wantRefused(t, err, "FENCED"); rf.OpEpoch != 0 || rf.Op != "s" {
			t.Fatalf("%+v, want op s fenced at epoch 0", rf)
		}
	})
	t.Run("done conflict: OPCONFLICT, final", func(t *testing.T) {
		t.Parallel()
		_, _, _, err := run(t, func(w *world, req *sprintfn.Request) {
			other, err := Intent(testNames, req.Epoch, "start", 0, map[string]any{"other": true})
			if err != nil {
				t.Error(err)
				return
			}
			cp := *req
			cp.Body.Op = &sprintfn.Op{ID: req.Body.Op.ID, Intent: other}
			w.step(&cp)
		})
		wantRefused(t, err, "OPCONFLICT")
	})
}

// TestFenceBranches: a refused send of a caller's op is fenced before the
// refusal is final (L1 5; fence). The fence's reply lost settles nothing;
// the fence refused STALE or OPCONFLICT settles it, and the send's refusal is
// final; a send refused STALE or OPCONFLICT is final with no fence sent.
func TestFenceBranches(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	run := func(t *testing.T, f *faulty) (*world, Result, error) {
		t.Helper()
		w := newWorld(t)
		w.initSprint()
		f.c = w.tw
		w.env.C = f
		res, err := Start(ctx, w.env, ClockReq{Op: "s"})
		return w, res, err
	}
	sendRefused := func(code string) func(int, *sprintfn.Request) *sprintfn.Refusal {
		return func(n int, req *sprintfn.Request) *sprintfn.Refusal {
			if n == 1 {
				return refusalOf(code)
			}
			return nil
		}
	}
	t.Run("the fence's reply lost: unknown", func(t *testing.T) {
		t.Parallel()
		f := &faulty{refuse: sendRefused(sprintfn.CodeMachineState), kill: func(n int) killMode {
			if n == 2 {
				return killLost
			}
			return killNone
		}}
		_, _, err := run(t, f)
		wantUnknown(t, err)
		if f.steps != 2 {
			t.Fatalf("%d steps, want the send and its fence", f.steps)
		}
	})
	for _, code := range []string{sprintfn.CodeStale, "OPCONFLICT"} {
		t.Run("the fence refused "+code+": the send's refusal is final", func(t *testing.T) {
			t.Parallel()
			f := &faulty{refuse: func(n int, req *sprintfn.Request) *sprintfn.Refusal {
				switch {
				case n == 1:
					return refusalOf(sprintfn.CodeMachineState)
				case req.Fence:
					return refusalOf(code)
				}
				return nil
			}}
			_, _, err := run(t, f)
			if rf := wantRefused(t, err, sprintfn.CodeMachineState); strings.Contains(rf.Hint, "fenced") {
				t.Fatalf("hint %q names a fence that did not win", rf.Hint)
			}
			if f.steps != 2 {
				t.Fatalf("%d steps, want the send and its fence", f.steps)
			}
		})
	}
	t.Run("a send refused OPCONFLICT: final, no fence", func(t *testing.T) {
		t.Parallel()
		f := &faulty{refuse: sendRefused("OPCONFLICT")}
		_, _, err := run(t, f)
		wantRefused(t, err, "OPCONFLICT")
		if f.steps != 1 {
			t.Fatalf("%d steps, want the send alone: OPCONFLICT settles the identity", f.steps)
		}
	})
	t.Run("a send refused STALE after the retries: final, no fence", func(t *testing.T) {
		t.Parallel()
		f := &faulty{refuse: func(n int, _ *sprintfn.Request) *sprintfn.Refusal {
			if n <= 5 {
				return refusalOf("REVISION")
			}
			return refusalOf(sprintfn.CodeStale)
		}}
		_, res, err := run(t, f)
		wantRefused(t, err, sprintfn.CodeStale)
		if f.steps != 6 || res.Retries != 5 {
			t.Fatalf("%d steps, %d retries; want 6 and 5: STALE settles the identity, no fence", f.steps, res.Retries)
		}
	})
}

// TestPartsSettleAndFence: a verb in parts settles a lost part by done, and
// fences a refused resend of a caller's op before the refusal is final (L1 5;
// Parts).
func TestPartsSettleAndFence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const n, chunk = 10, 3
	t.Run("a part's lost reply is settled by done", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		f := &faulty{c: w.tw, kill: func(k int) killMode {
			if k == 2 {
				return killReply
			}
			return killNone
		}}
		w.env.C = f
		res, err := w.env.Parts(ctx, "", makeCards(n, chunk))
		if err != nil || res.Parts != 4 || f.steps != 4 {
			t.Fatalf("%+v, %v, %d steps; want 4 parts, each sent once, part 2 settled by done", res, err, f.steps)
		}
		checkCards(t, w, n)
	})
	t.Run("a lost part with done absent is unknown, and the op resumes", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		f := &faulty{c: w.tw, kill: func(k int) killMode {
			if k == 2 {
				return killLost
			}
			return killNone
		}}
		w.env.C = f
		_, err := w.env.Parts(ctx, "op-l", makeCards(n, chunk))
		if u := wantUnknown(t, err); u.Part != 2 || !strings.Contains(err.Error(), "--op op-l --epoch 0") {
			t.Fatalf("%v, want part 2 unknown, naming --op op-l --epoch 0", err)
		}
		res, err := w.at(0).Parts(ctx, "op-l", makeCards(n, chunk))
		if err != nil || res.Resumed != 1 || res.Parts != 3 {
			t.Fatalf("resume: %+v, %v; want parts 2 to 4 after part 1", res, err)
		}
		checkCards(t, w, n)
	})
	// resend runs op op-r after a first run whose part 1 never reached the
	// store, with refuse deciding the resend's part 1 (n 1) and the fence.
	resend := func(t *testing.T, refuse func(w *world, n int, req *sprintfn.Request) *sprintfn.Refusal) (*world, *faulty, Result, error) {
		t.Helper()
		w := newWorld(t)
		w.env.C = &faulty{c: w.tw, kill: func(int) killMode { return killBefore }}
		if _, err := w.env.Parts(ctx, "op-r", makeCards(n, chunk)); !errors.Is(err, tset.ErrOutcomeUnknown) {
			t.Fatalf("first run: %v, want an unknown outcome", err)
		}
		f := &faulty{c: w.tw, refuse: func(k int, req *sprintfn.Request) *sprintfn.Refusal { return refuse(w, k, req) }}
		env := w.at(0)
		env.C = f
		res, err := env.Parts(ctx, "op-r", makeCards(n, chunk))
		return w, f, res, err
	}
	t.Run("a refused resend is fenced, and final", func(t *testing.T) {
		t.Parallel()
		_, f, _, err := resend(t, func(_ *world, k int, _ *sprintfn.Request) *sprintfn.Refusal {
			if k == 1 {
				return refusalOf("DRIFT")
			}
			return nil
		})
		if rf := wantRefused(t, err, "DRIFT"); !strings.Contains(rf.Hint, "op-r/p1 at epoch 0 is fenced") {
			t.Fatalf("hint %q, want the fence named", rf.Hint)
		}
		if len(f.sent) != 1 || !f.sent[0].Fence {
			t.Fatalf("sent %d, want the fence alone", len(f.sent))
		}
	})
	t.Run("a refused resend whose original applied goes on from its receipt", func(t *testing.T) {
		t.Parallel()
		w, f, res, err := resend(t, func(w *world, k int, req *sprintfn.Request) *sprintfn.Refusal {
			if k == 1 { // the copy in flight lands; this one is refused
				w.step(req)
				return refusalOf("DRIFT")
			}
			return nil
		})
		if err != nil || res.Parts != 4 || f.sent[0].Fence != true {
			t.Fatalf("%+v, %v; want part 1 replayed by the fence, then parts 2 to 4", res, err)
		}
		checkCards(t, w, n)
	})
}

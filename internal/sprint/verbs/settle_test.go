package verbs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// TestOpNeverChangesEpoch: an op runs at one epoch, and a repeat of it
// finds its receipt there, whichever epoch the repeat starts from; a sprint
// whose epoch moved from the op's refuses it, naming both epochs, and never
// runs it again at the new one (L1 5).
func TestOpNeverChangesEpoch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("a lost-reply clear repeated does not clear twice", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.initSprint()
		f := &faulty{c: w.tw, kill: func(int) killMode { return killAfter }}
		w.env.C = f
		_, err := Clear(ctx, w.env, ClearReq{Op: "c1", Confirm: testPrefix})
		var u *Unknown
		if !errors.As(err, &u) || u.Epoch != 0 || !strings.Contains(err.Error(), "at epoch 0") {
			t.Fatalf("err %v, want an unknown outcome naming op c1 at epoch 0", err)
		}
		// The command, given no --epoch, runs at the active epoch; and at the
		// op's own. Either finds the receipt, and neither clears again.
		for _, env := range []*Env{w.at(1), w.at(0)} {
			res, err := Clear(ctx, env, ClearReq{Op: "c1", Confirm: testPrefix})
			if err != nil || !res.Replay || res.EpochAfter != 1 || res.Epoch != 0 {
				t.Fatalf("repeat from epoch %d: %+v, %v; want the receipt at 0, epoch after 1", env.Epoch, res, err)
			}
		}
		if got := activeEpoch(w); got != "1" {
			t.Fatalf("the sprint is at %s, want 1: cleared once", got)
		}
	})
	t.Run("a start repeated after a clear does not apply again", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.initSprint()
		if _, err := Start(ctx, w.env, ClockReq{Op: "s1"}); err != nil {
			t.Fatal(err)
		}
		if _, err := Stop(ctx, w.env, ClockReq{}); err != nil {
			t.Fatal(err)
		}
		if _, err := Clear(ctx, w.env, ClearReq{Confirm: testPrefix}); err != nil {
			t.Fatal(err)
		}
		for _, env := range []*Env{w.at(1), w.at(0)} {
			res, err := Start(ctx, env, ClockReq{Op: "s1"})
			if err != nil || !res.Replay || res.Epoch != 0 {
				t.Fatalf("start --op s1 from epoch %d: %+v, %v; want its receipt at epoch 0", env.Epoch, res, err)
			}
		}
		if c, _ := w.clock(1); running(c) {
			t.Fatal("the repeated start applied again at epoch 1")
		}
		// An op of epoch 1, repeated from an Env behind it, is found ahead.
		if _, err := Start(ctx, w.at(1), ClockReq{Op: "s2"}); err != nil {
			t.Fatal(err)
		}
		res, err := Start(ctx, w.at(0), ClockReq{Op: "s2"})
		if err != nil || !res.Replay || res.Epoch != 1 {
			t.Fatalf("start --op s2 from epoch 0: %+v, %v; want its receipt at epoch 1", res, err)
		}
	})
	t.Run("a Parts op resumed after a clear is refused with its epoch", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.initSprint()
		const n, chunk = 10, 3
		f := &faulty{c: w.tw, kill: func(k int) killMode {
			if k == 3 {
				return killBefore
			}
			return killNone
		}}
		w.env.C = f
		if _, err := w.env.Parts(ctx, "op-make", makeCards(n, chunk)); err == nil {
			t.Fatal("the kill did not stop the op")
		}
		if len(f.sent) != 2 {
			t.Fatalf("%d parts applied before the kill, want 2", len(f.sent))
		}
		w.env.C = w.tw
		if _, err := Clear(ctx, w.env, ClearReq{Confirm: testPrefix}); err != nil {
			t.Fatal(err)
		}
		for _, env := range []*Env{w.at(1), w.at(0)} {
			res, err := env.Parts(ctx, "op-make", makeCards(n, chunk))
			var rf *Refused
			if !errors.As(err, &rf) || rf.Code() != sprintfn.CodeStale || rf.OpEpoch != 0 || res.Resumed != 2 ||
				!strings.Contains(rf.Refusal.Message, "op op-make was at epoch 0; the sprint is at 1; nothing was changed") ||
				!strings.Contains(rf.Hint, "parts 1 to 2 stay applied at epoch 0") {
				t.Fatalf("resume from epoch %d: %+v, %v; want STALE naming the op's epoch 0, parts 1 to 2 applied", env.Epoch, res, err)
			}
			if res.Parts != 0 {
				t.Fatalf("the resume applied %d parts, want none", res.Parts)
			}
		}
		rd := w.read(&sprintfn.ReadRequest{Epoch: "1", Tset: []tset.ReadQuery{{Kind: "count", Table: sprint.Readers, Cells: []string{"r1:asked"}}}})
		if got := rd.Tset[0].Counts; len(got) == 1 && got[0] != 0 {
			t.Fatalf("epoch 1 holds %v cards: the op restarted at part 1", got)
		}
	})
	t.Run("a move under an op is refused, not retried", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.initSprint()
		f := &faulty{c: w.tw, before: func(n int) {
			if n == 1 {
				if _, err := Clear(ctx, w.at(0), ClearReq{Confirm: testPrefix}); err != nil {
					t.Errorf("the other clear: %v", err)
				}
			}
		}}
		w.env.C = f
		_, err := Start(ctx, w.env, ClockReq{Op: "s3"})
		var rf *Refused
		if !errors.As(err, &rf) || rf.Code() != sprintfn.CodeStale || rf.Local || rf.OpEpoch != 0 ||
			!strings.Contains(rf.Refusal.Message, "op s3 was at epoch 0; the sprint is at 1") {
			t.Fatalf("err %v, want STALE naming op s3 at epoch 0", err)
		}
		if f.steps != 1 {
			t.Fatalf("%d steps, want 1: no retry at the new epoch, and no fence after STALE", f.steps)
		}
		if c, _ := w.clock(1); running(c) {
			t.Fatal("the op ran at the new epoch")
		}
	})
}

// TestLostReplyAndFence (L1 5): a lost reply is settled by done before it
// is reported; a refused send of a caller's op, which may repeat a copy still
// in flight, is final only once the fence settles it.
func TestLostReplyAndFence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("a lost reply is settled by done in the same run", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.initSprint()
		f := &faulty{c: w.tw, kill: func(int) killMode { return killReply }}
		w.env.C = f
		res, err := Start(ctx, w.env, ClockReq{Op: "s1"})
		if err != nil || res.Replay || res.Trips != 3 || f.steps != 1 {
			t.Fatalf("%+v, %v; want the start applied, settled by one done (3 trips, 1 step)", res, err)
		}
		if c, _ := w.clock(0); !running(c) {
			t.Fatal("the machine does not run")
		}
	})
	t.Run("a lost reply then a resend that finds done reports the original result", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.initSprint()
		w.env.C = &faulty{c: w.tw, kill: func(int) killMode { return killAfter }}
		if _, err := Clear(ctx, w.env, ClearReq{Op: "c1", Confirm: testPrefix}); !errors.Is(err, tset.ErrOutcomeUnknown) {
			t.Fatalf("err %v, want an unknown outcome", err)
		}
		res, err := Clear(ctx, w.at(0), ClearReq{Op: "c1", Confirm: testPrefix})
		if err != nil || !res.Replay || res.EpochAfter != 1 {
			t.Fatalf("resend: %+v, %v; want the original result, epoch after 1", res, err)
		}
	})
	// resend runs start --op s after a first send that never reached the
	// store, with refuse deciding the resend's step (n 1) and its fence (n 2).
	resend := func(t *testing.T, refuse func(w *world, n int, req *sprintfn.Request) *sprintfn.Refusal) (*world, *faulty, Result, error) {
		t.Helper()
		w := newWorld(t)
		w.initSprint()
		w.env.C = &faulty{c: w.tw, kill: func(int) killMode { return killBefore }}
		if _, err := Start(ctx, w.env, ClockReq{Op: "s"}); !errors.Is(err, tset.ErrOutcomeUnknown) {
			t.Fatalf("first send: %v, want an unknown outcome", err)
		}
		f := &faulty{c: w.tw, refuse: func(n int, req *sprintfn.Request) *sprintfn.Refusal { return refuse(w, n, req) }}
		w.env.C = f
		res, err := Start(ctx, w.env, ClockReq{Op: "s"})
		return w, f, res, err
	}
	t.Run("a refused resend with the fence open reports OUTCOMEUNKNOWN", func(t *testing.T) {
		t.Parallel()
		_, f, _, err := resend(t, func(_ *world, n int, req *sprintfn.Request) *sprintfn.Refusal {
			if n == 1 {
				return refusalOf(sprintfn.CodeMachineState)
			}
			if req.Fence {
				return refusalOf("BUDGET")
			}
			return nil
		})
		var u *Unknown
		var rf *Refused
		if !errors.As(err, &u) || !errors.Is(err, tset.ErrOutcomeUnknown) || errors.As(err, &rf) {
			t.Fatalf("err %v, want an unknown outcome, not a refusal", err)
		}
		if f.steps != 2 {
			t.Fatalf("%d steps, want the resend and its fence", f.steps)
		}
	})
	t.Run("a refused resend the fence settles is final, and the op is fenced", func(t *testing.T) {
		t.Parallel()
		w, f, _, err := resend(t, func(_ *world, n int, _ *sprintfn.Request) *sprintfn.Refusal {
			if n == 1 {
				return refusalOf(sprintfn.CodeMachineState)
			}
			return nil
		})
		var rf *Refused
		if !errors.As(err, &rf) || rf.Code() != sprintfn.CodeMachineState || !strings.Contains(rf.Hint, "fenced") {
			t.Fatalf("err %v, want MACHINESTATE, final, naming the fence", err)
		}
		if len(f.sent) != 1 || !f.sent[0].Fence {
			t.Fatalf("sent %d, want the fence alone", len(f.sent))
		}
		_, err = Start(ctx, w.at(0), ClockReq{Op: "s"})
		if !errors.As(err, &rf) || rf.Code() != "FENCED" {
			t.Fatalf("a repeat of the fenced op: %v, want FENCED", err)
		}
		if c, _ := w.clock(0); running(c) {
			t.Fatal("a fenced start moved the clock")
		}
	})
	t.Run("a refused resend whose original applied reports the original", func(t *testing.T) {
		t.Parallel()
		w, _, res, err := resend(t, func(w *world, n int, req *sprintfn.Request) *sprintfn.Refusal {
			if n == 1 { // the copy in flight lands; this one is refused
				w.step(req)
				return refusalOf(sprintfn.CodeMachineState)
			}
			return nil
		})
		if err != nil || !res.Replay {
			t.Fatalf("%+v, %v; want the original's receipt, replayed by the fence", res, err)
		}
		if c, _ := w.clock(0); !running(c) {
			t.Fatal("the original start did not apply")
		}
	})
}

// eachCard is makeCards with one create entry a card, so a part of more than
// 256 cards is past one step's entries and the step builder cuts it.
func eachCard(n, chunk int) PartsPlan {
	pp := makeCards(n, chunk)
	plan := pp.Plan
	pp.Plan = func(rd *sprintfn.ReadReply, cont string, chunk int) (Part, error) {
		p, err := plan(rd, cont, chunk)
		if err != nil {
			return p, err
		}
		var out []tset.Entry
		for _, en := range p.Req.Body.Entries {
			if en.Kind != "create" {
				out = append(out, en)
				continue
			}
			for i, id := range en.IDs {
				out = append(out, tset.Entry{Kind: "create", Table: en.Table, To: en.To, IDs: []string{id}, About: []string{id}, Scores: []string{en.Scores[i]}})
			}
		}
		p.Req.Body.Entries = out
		return p, nil
	}
	return pp
}

// TestPartsBuilderLimitHalves: a part the step builder cuts is planned
// again at half the chunk before anything is sent (1.3.6, 1.5.4).
func TestPartsBuilderLimitHalves(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := &faulty{c: w.tw}
	w.env.C = f
	res, err := w.env.Parts(context.Background(), "", eachCard(300, 300))
	if err != nil {
		t.Fatal(err)
	}
	if res.Chunk != 150 || res.Parts != 2 || f.steps != 2 {
		t.Fatalf("chunk %d, parts %d, steps sent %d; want 150, 2, 2 (the cut is the builder's, before any send)", res.Chunk, res.Parts, f.steps)
	}
	checkCards(t, w, 300)
}

// TestPartsRaceRetried: a part refused on a race is planned again on a
// fresh read, and every card is made once.
func TestPartsRaceRetried(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := &faulty{c: w.tw, refuse: func(n int, _ *sprintfn.Request) *sprintfn.Refusal {
		if n == 2 {
			return refusalOf("REVISION")
		}
		return nil
	}}
	w.env.C = f
	res, err := w.env.Parts(context.Background(), "", makeCards(10, 3))
	if err != nil {
		t.Fatal(err)
	}
	if res.Retries != 1 || res.Parts != 4 || f.steps != 5 || len(f.sent) != 4 {
		t.Fatalf("retries %d, parts %d, steps %d, applied %d; want 1, 4, 5, 4", res.Retries, res.Parts, f.steps, len(f.sent))
	}
	checkCards(t, w, 10)
}

// TestDoReloadsAfterStepRefusal: a step refused STALE moves the epoch to
// the one the refusal names before the next read, so the next read plans at
// it with no AL2 move (1.0: the caller reloads the epoch).
func TestDoReloadsAfterStepRefusal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	w.initSprint()
	f := &faulty{c: w.tw, before: func(n int) {
		if n == 1 {
			if _, err := Clear(ctx, w.at(0), ClearReq{Confirm: testPrefix}); err != nil {
				t.Errorf("the other clear: %v", err)
			}
		}
	}}
	cc := &Counting{C: f}
	w.env.C = cc
	res, err := Start(ctx, w.env, ClockReq{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Retries != 1 || cc.Trips() != 4 || w.env.Epoch != 1 {
		t.Fatalf("retries %d, trips %d, epoch %d; want 1, 4 (read, STALE step, read at 1, step), 1", res.Retries, cc.Trips(), w.env.Epoch)
	}
	if c, _ := w.clock(1); !running(c) {
		t.Fatal("the start did not apply at epoch 1")
	}
}

// TestResultNamesIDs: a verb's result names the ids it moved, over all
// its parts, and a refused part's refused and not-written ids (1.5.3); the
// report prints them in the command's shape.
func TestResultNamesIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("totals over the parts", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		res, err := w.env.Parts(ctx, "", makeCards(10, 3))
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Moved) != 10 || res.Moved[0] != "c001" || res.Moved[9] != "c010" {
			t.Fatalf("moved %v, want c001..c010: the total, not the last part's", res.Moved)
		}
		var out, errs bytes.Buffer
		if code := Report(&out, &errs, res, nil, 0); code != 0 {
			t.Fatalf("exit %d, want 0", code)
		}
		if got := strings.Count(out.String(), "MOVED "); got != 10 || !strings.Contains(out.String(), "MAKE OK moved=10 refused=0 notes=0 op="+res.Op+"\n") {
			t.Fatalf("report:\n%s", out.String())
		}
		out.Reset()
		Report(&out, &errs, res, nil, 4)
		if !strings.Contains(out.String(), "MORE kind=moved shown=4 total=10") {
			t.Fatalf("report at --max 4:\n%s", out.String())
		}
	})
	t.Run("a refused part names its ids", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.env.C = &faulty{c: w.tw, refuse: func(n int, _ *sprintfn.Request) *sprintfn.Refusal {
			if n == 2 {
				r := refusalOf("DRIFT")
				r.Detail.IDs = []string{"c004"}
				return r
			}
			return nil
		}}
		res, err := w.env.Parts(ctx, "", makeCards(10, 3))
		var rf *Refused
		if !errors.As(err, &rf) || rf.Part != 2 {
			t.Fatalf("err %v, want part 2 refused", err)
		}
		if fmt.Sprint(res.Moved) != "[c001 c002 c003]" || len(res.Refused) != 1 || res.Refused[0] != (RefusedID{ID: "c004", Code: "DRIFT", Why: "DRIFT; nothing was changed"}) ||
			fmt.Sprint(res.NotWritten) != "[c005 c006]" {
			t.Fatalf("moved %v, refused %v, not written %v", res.Moved, res.Refused, res.NotWritten)
		}
		var out, errs bytes.Buffer
		if code := Report(&out, &errs, res, err, 0); code != 1 {
			t.Fatalf("exit %d, want 1", code)
		}
		for _, want := range []string{"REFUSED c004: DRIFT DRIFT; nothing was changed\n", "NOTWRITTEN c005\n", "MAKE FAIL moved=3 refused=1 notes=0 notwritten=2 op=" + res.Op + " changed=some\n"} {
			if !strings.Contains(errs.String(), want) {
				t.Fatalf("report's stderr lacks %q:\n%s", want, errs.String())
			}
		}
	})
}

// shortDone answers every done query with no slots: a store function that
// answers another shape.
type shortDone struct{ c sprintfn.Client }

func (s shortDone) Pipeline(ctx context.Context, items []sprintfn.Item) ([]sprintfn.Result, error) {
	out, err := s.c.Pipeline(ctx, items)
	for i := range out {
		if out[i].Read == nil {
			continue
		}
		for j := range out[i].Read.Tset {
			if out[i].Read.Tset[j].Kind == "done" {
				out[i].Read.Tset[j].Done = nil
			}
		}
	}
	return out, err
}

// TestMalformedDoneRefused: a done answer that does not answer each identity
// is refused CONFIG, never indexed past its end (L1 5, 7).
func TestMalformedDoneRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	w.initSprint()
	w.env.C = shortDone{c: w.tw}
	if _, err := w.env.Parts(ctx, "op-x", makeCards(4, 2)); err == nil {
		t.Fatal("a resume on a malformed done answer went on")
	} else if code, _ := refusedCode(t, err); code != sprintfn.CodeConfig {
		t.Fatalf("parts: %s, want CONFIG", code)
	}
	_, err := Start(ctx, w.env, ClockReq{Op: "s1"})
	if code, _ := refusedCode(t, err); code != sprintfn.CodeConfig {
		t.Fatalf("start: %s, want CONFIG", code)
	}
}

// TestFirstRunWithOpIsNPlusOne: a verb in parts given an op on its first run
// asks done in part 1's read, so n parts cost n + 1 round trips, as with no op
// (1.5.4).
func TestFirstRunWithOpIsNPlusOne(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	res, err := w.env.Parts(context.Background(), "op-first", makeCards(10, 3))
	if err != nil {
		t.Fatal(err)
	}
	if res.Parts != 4 || w.cc.Trips() != 5 {
		t.Fatalf("parts %d, trips %d; want 4 and 5", res.Parts, w.cc.Trips())
	}
	checkCards(t, w, 10)
}

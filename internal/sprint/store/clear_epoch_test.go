package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// image is everything observable of the store: every key and every table's
// revision, and every epoch's inbox.
func (h *harness) image() string {
	var b strings.Builder
	for _, k := range h.m.Keys(h.st.Names) {
		b.WriteString(k + "\n")
	}
	for _, t := range All {
		b.WriteString(t + " rev " + itoa(h.m.Revision(h.st.Names.Table(t))) + "\n")
	}
	for e := uint64(0); e <= 5; e++ {
		st := h.st.At(e)
		v, err := st.Inbox(h.ctx, 0, 0, 10000)
		if err != nil {
			continue
		}
		b.WriteString("epoch " + itoa(uint64(e)) + " cursor " + v.Cursor + " last " + v.Last + " open " + itoa(uint64(len(v.Open))) + "\n")
		for _, o := range v.Open {
			b.WriteString("  " + o.Key + " review " + o.Note.Review.String() + "\n")
		}
	}
	return b.String()
}

func itoa(n uint64) string { return strings.TrimSpace(strings.Repeat(" ", 0) + fmtU(n)) }

func fmtU(n uint64) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

// Every writer holding the old epoch after a clear is refused, changes
// nothing, and says the sprint was cleared.
func TestLateWritersOfEveryKind(t *testing.T) {
	t.Parallel()
	h := midFlight(t)
	before := h.snap()
	openBefore, _ := h.st.Inbox(h.ctx, 0, 0, 1000)
	if len(openBefore.Open) == 0 {
		t.Fatalf("no open judgment mid-flight")
	}
	if _, err := h.st.Clear(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.clean("cleared")
	img := h.image()
	held := uint64(0)
	rc := before.Readers.Of("s1-4")
	steps := map[string]Step{
		"take":   TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{IDs: []string{"s1-6.w1"}}, Gens: map[string]int{"s1-6.w1": 1}}),
		"finish": FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{"s1-5.w1"}}, Gens: map[string]int{"s1-5.w1": 1}}),
		"begin":  ReadStep(sprint.ReadReq{As: rc[1].F("reader"), Begin: true, Sel: sprint.Sel{IDs: []string{rc[1].ID}}}),
		"report": ReadStep(sprint.ReadReq{As: rc[0].F("reader"), Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc[0].ID}}}),
		"merge":  MergeStep(sprint.MergeReq{Stream: "s1", Batch: 1}),
		"accept": AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{"s1-4"}}}),
		"rework": ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-4"}}, Fix: "x"}),
		"drop":   DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-7"}}, Reason: "x"}),
		"rank":   RankStep(sprint.RankReq{IDs: []string{"s1-7"}, First: true}),
		"resume": ResumeStep(sprint.ResumeReq{Stream: "s1", Did: "x"}),
		"fleet":  FleetStep(sprint.FleetReq{Op: "down", Member: "m1"}),
		"ack":    AckStep(sprint.AckReq{Notes: []string{openBefore.Open[0].Note.ID}, Reason: "x"}),
	}
	for name, step := range steps {
		step.Epoch = &held
		res, err := h.st.Run(h.ctx, step)
		if err != nil || len(res.Moved) != 0 || len(res.Refused) != 1 || !strings.Contains(res.Refused[0].Why, "cleared at") {
			t.Errorf("late %s: %+v %v", name, res, err)
		}
		if got := h.image(); got != img {
			t.Errorf("late %s changed the store:\n%s\nwas\n%s", name, got, img)
			img = got
		}
	}
	h.clean("after late writers")
}

// clearAt clears the sprint (through another store) just before the n-th
// Apply of the wrapped store.
type clearAt struct {
	Backend
	n, seen int
	once    sync.Once
	do      func()
}

func (c *clearAt) Apply(ctx context.Context, m ntable.BatchManifest) (ntable.Receipt, error) {
	c.seen++
	if c.seen == c.n {
		c.once.Do(c.do)
	}
	return c.Backend.Apply(ctx, m)
}

// A clear in the middle of a multi-table step, after its first write and
// before its last: the new epoch is empty, and neither repair nor the same
// verb again changes anything.
func TestClearInTheMiddleOfAStep(t *testing.T) {
	t.Parallel()
	for _, grace := range []string{"in flight", "writer dead"} {
		t.Run(grace, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(3)
			other := &Store{B: h.m, Names: h.st.Names, Actor: "clearer", Now: h.st.Now, NewID: h.st.NewID, Sleep: h.st.Sleep}
			if grace == "writer dead" {
				other.Grace = 1 // the clear finds the op old and finishes it
				h.tick(1e9)
			}
			var cres ClearResult
			var cerr error
			w := &clearAt{Backend: h.m, n: 2, do: func() { cres, cerr = other.Clear(h.ctx) }}
			st := *h.st
			st.B = w
			step := DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1", "s1-2"}}})
			res, err := st.Run(h.ctx, step)
			t.Logf("step: %+v err=%v", res, err)
			t.Logf("clear: %+v err=%v", cres, cerr)
			if cerr != nil {
				t.Fatalf("clear: %v", cerr)
			}
			if w.seen < 2 {
				t.Fatalf("the step wrote %d manifests", w.seen)
			}
			s := h.snap()
			if s.Epoch != 1 {
				t.Fatalf("epoch %d", s.Epoch)
			}
			for _, c := range s.Work.Cards() {
				if c.Placed() {
					t.Errorf("new epoch holds %s at %s", c.ID, c.Col)
				}
			}
			for _, c := range s.Fleet.Cards() {
				if c.Placed() && c.Col != sprint.Ctl {
					t.Errorf("new epoch fleet holds %s at %s", c.ID, c.Col)
				}
			}
			h.clean("after the clear mid-step")
			// the old epoch: is it one consistent state (both or neither)?
			old, err := h.st.At(0).Load(h.ctx, All, nil)
			if err != nil {
				t.Fatalf("old epoch: %v", err)
			}
			t.Logf("old epoch: s1-1 %s, s1-2 %s, fleet cards %d", old.StateOf("s1-1"), old.StateOf("s1-2"), len(old.Fleet.Cards()))
			if p := h.m.AtEpoch(0, true).(*Mem).Pending(); p != nil {
				t.Logf("old epoch's fence still holds %s", p.ID)
			}
			rr, err := h.st.Repair(h.ctx)
			t.Logf("repair: %+v %v", rr, err)
			img := h.image()
			res, err = h.st.Run(h.ctx, step)
			t.Logf("same verb again: %+v %v", res, err)
			if len(res.Moved) != 0 {
				t.Errorf("the same verb again moved %v in the new epoch", res.Moved)
			}
			if h.image() != img {
				t.Errorf("the same verb again changed the store")
			}
			h.clean("end")
		})
	}
}

// C5: init with nothing, then clears: an epoch that had no write has no
// definition at the table layer, and the store refuses a read of it as it
// was (NOTABLE), as the table layer does; the sprint reads it as empty, clears
// again, and teardown leaves the store as it was before init.
func TestEmptyClearsThenTeardown(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	m := NewMem()
	h.m, h.st.B = m, m
	before := m.Keys(h.st.Names)
	if err := h.st.Init(h.ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := h.st.Clear(h.ctx); err != nil {
			t.Fatal(err)
		}
		h.clean("empty clear")
	}
	if _, err := m.AtEpoch(1, true).Shapes(h.ctx, []string{h.st.Names.Table(sprint.Work)}); refusalCode(err) != "NOTABLE" {
		t.Fatalf("a read of epoch 1, which had no write: %v", err)
	}
	if _, err := m.AtEpoch(0, true).Shapes(h.ctx, []string{h.st.Names.Table(sprint.Work)}); err != nil {
		t.Fatalf("a read of epoch 0, which init wrote: %v", err)
	}
	if _, err := m.AtEpoch(4, true).Shapes(h.ctx, []string{h.st.Names.Table(sprint.Work)}); refusalCode(err) != "EPOCHAHEAD" {
		t.Fatalf("a read of epoch 4, ahead of the sprint: %v", err)
	}
	for e := uint64(0); e < 3; e++ {
		s, err := h.st.At(e).Load(h.ctx, All, nil)
		if err != nil {
			t.Fatalf("epoch %d unreadable: %v", e, err)
		}
		if len(s.Work.Rows())+len(s.Fleet.Rows())+len(s.Work.Cards()) != 0 {
			t.Fatalf("epoch %d is not empty", e)
		}
	}
	if _, err := h.st.Teardown(h.ctx); err != nil {
		t.Fatal(err)
	}
	if after := m.Keys(h.st.Names); !slices.Equal(after, before) {
		t.Fatalf("after teardown:\n%s", strings.Join(after, "\n"))
	}
}

// The old epoch's records are unchanged by the new epoch's run of the same
// ids.
func TestOldEpochUnchangedByReuse(t *testing.T) {
	t.Parallel()
	h := midFlight(t)
	if _, err := h.st.Clear(h.ctx); err != nil {
		t.Fatal(err)
	}
	dump := func() string {
		s, err := h.st.At(0).Load(h.ctx, All, nil)
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for _, tb := range []*sprint.Table{s.Work, s.Readers, s.Merge, s.Fleet} {
			for _, c := range tb.Cards() {
				lines = append(lines, tb.Name+" "+c.ID+" "+c.Row+":"+c.Col+" rev "+fmtU(c.Rev))
			}
		}
		slices.Sort(lines)
		var b strings.Builder
		b.WriteString(strings.Join(lines, "\n") + "\n")
		for _, id := range []string{"s1-1", "s1-2", "s1-3", "s1-4"} {
			ci, err := h.st.At(0).CardOf(h.ctx, id)
			if err != nil || ci.Primary == nil {
				t.Fatalf("card %s at 0: %v", id, err)
			}
			b.WriteString(id + " " + ci.Primary.Col + " rev " + fmtU(ci.Primary.Rev) + "\n")
		}
		return b.String()
	}
	was := dump()
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 4}))
	h.through("s1-1", "s1-2", "s1-3", "s1-4")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1"}))
	if s := h.snap(); s.StateOf("s1-4") != sprint.Landed {
		t.Fatalf("s1-4 %s", s.StateOf("s1-4"))
	}
	if now := dump(); now != was {
		t.Fatalf("the old epoch changed:\n%s\nwas\n%s", now, was)
	}
	h.clean("end")
}

// advanceHook runs a step at the old epoch just before the epoch advances.
type advanceHook struct {
	Backend
	do func()
}

func (a *advanceHook) AdvanceEpoch(ctx context.Context, from uint64, at time.Time) (bool, error) {
	if a.do != nil {
		a.do()
		a.do = nil
	}
	return a.Backend.AdvanceEpoch(ctx, from, at)
}

// C3: a member brought up (a new fleet row) and a member taken down between
// clear's start and its advance are both kept: the shape is read after the
// advance, from the old epoch, frozen from then on.
func TestRowAddedDuringClearIsKept(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	other := &Store{B: h.m, Names: h.st.Names, Actor: "other", Now: h.st.Now, NewID: func() string { return "o" }, Sleep: h.st.Sleep}
	st := *h.st
	st.B = &advanceHook{Backend: h.m, do: func() {
		for _, op := range []string{"up m3", "down m1"} {
			f := strings.Fields(op)
			if _, err := other.Run(h.ctx, FleetStep(sprint.FleetReq{Op: f[0], Member: f[1]})); err != nil {
				t.Error(err)
			}
		}
	}}
	if _, err := st.Clear(h.ctx); err != nil {
		t.Fatal(err)
	}
	s := h.snap()
	if !slices.Contains(s.Fleet.Rows(), "m3") || s.MemberCtl("m3").F("status") != sprint.Up {
		t.Errorf("a member brought up before the advance was lost by the clear: rows %v", s.Fleet.Rows())
	}
	if s.MemberCtl("m1").F("status") != sprint.Down {
		t.Errorf("m1 taken down before the advance is up again at the new epoch")
	}
	h.clean("after the clear")
}

// C3: a clear cut between its advance and its restore records only that the
// restore is owed; the next verb, or the next tick, performs it first. C6:
// the owed restore removes a fence the old epoch still holds.
func TestAnOwedRestoreIsPerformedFirst(t *testing.T) {
	t.Parallel()
	for _, next := range []string{"verb", "tick"} {
		t.Run(next, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(2)
			dead := OpRecord{ID: "dead-1", Verb: "take", At: t0}
			st := *h.st
			st.B = &advanceHook{Backend: h.m, do: func() {
				// a writer takes the old epoch's fence just before the advance,
				// and dies
				f, _ := h.m.ReadFence(h.ctx)
				if ok, err := h.m.Acquire(h.ctx, f.Gen, dead); !ok || err != nil {
					t.Errorf("acquire: %v %v", ok, err)
				}
			}}
			h.m.Fail = func(p string) error {
				if strings.HasPrefix(p, "apply ") {
					return errors.New("cut")
				}
				return nil
			}
			if _, err := st.Clear(h.ctx); err == nil {
				t.Fatalf("the clear was not cut")
			}
			h.m.Fail = nil
			if es, _ := h.m.Epoch(h.ctx); es.N != 1 || !es.Owed {
				t.Fatalf("after the cut: %+v", es)
			}
			if p := h.m.AtEpoch(0, true).(*Mem).Pending(); p == nil || p.ID != dead.ID {
				t.Fatalf("the old epoch's fence: %+v", p)
			}
			switch next {
			case "verb":
				h.must(AddStep(sprint.AddReq{Stream: "s2", Count: 1}))
			case "tick":
				h.machine()
			}
			if es, _ := h.m.Epoch(h.ctx); es.N != 1 || es.Owed {
				t.Fatalf("after the %s: %+v", next, es)
			}
			s := h.snap()
			if s.StreamCtl("s1").F("state") != sprint.StreamWaiting || s.MemberCtl("m2").F("status") != sprint.Up {
				t.Fatalf("the shape is not restored by the %s", next)
			}
			if p := h.m.AtEpoch(0, true).(*Mem).Pending(); p != nil {
				t.Fatalf("the old epoch still holds the fence of %s", p.ID)
			}
			h.clean("restored")
		})
	}
}

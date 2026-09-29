package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// midFlight is a sprint with cards in every column of every table: work
// waiting, ready, working, review, merging and landed; fleet ready, working
// and done; readers asked, reading, ok and broken; merge queued, merged and
// stuck.
func midFlight(t *testing.T) *harness {
	h := newHarness(t)
	h.setup(7)
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"later"}, Needs: []string{"s1-6"}}))
	h.through("s1-1", "s1-2", "s1-3")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 1}))
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Conflict: "s1-2"}))
	h.must(StartStep(sprint.StartReq{Sel: sprint.Sel{IDs: []string{"s1-4", "s1-5"}}}))
	s := h.snap()
	c := s.Fleet.Card("s1-4.w1")
	h.must(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: 1}}))
	h.must(FinishStep(sprint.FinishReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: 1}}))
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-4"}}}))
	rs := h.snap().Readers.Of("s1-4")
	h.must(ReadStep(sprint.ReadReq{As: rs[0].F("reader"), Begin: true, Sel: sprint.Sel{IDs: []string{rs[0].ID}}}))
	c = h.snap().Fleet.Card("s1-5.w1")
	h.must(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: 1}}))
	h.must(StartStep(sprint.StartReq{Sel: sprint.Sel{IDs: []string{"s1-6"}}}))
	h.clean("mid-flight")
	s = h.snap()
	for table, cols := range map[*sprint.Table][]string{
		s.Work:    {sprint.Waiting, sprint.Ready, sprint.Working, sprint.Review, sprint.Merging, sprint.Landed},
		s.Fleet:   {sprint.Ready, sprint.Working, sprint.Done},
		s.Readers: {sprint.Asked, sprint.Reading, sprint.OK},
		s.Merge:   {sprint.Queued, sprint.Merged, sprint.Stuck},
	} {
		for _, col := range cols {
			if len(table.Column(col)) == 0 {
				t.Fatalf("mid-flight: nothing in %s %s", table.Name, col)
			}
		}
	}
	return h
}

func TestClearStopsTheSprintAndClearsAllWork(t *testing.T) {
	t.Parallel()
	h := midFlight(t)
	before := h.snap()
	stopped := false
	h.st.Stop = func(context.Context) error { stopped = true; return nil }
	res, err := h.st.Clear(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !stopped || res.From != 0 || res.To != 1 || res.Held["primaries"] != 8 || res.Held["merge cards"] != 3 {
		t.Fatalf("clear: stopped %v %+v", stopped, res)
	}
	after := h.snap()
	if after.Epoch != 1 {
		t.Fatalf("epoch %d", after.Epoch)
	}
	for _, pair := range [][2]*sprint.Table{{before.Work, after.Work}, {before.Readers, after.Readers}, {before.Merge, after.Merge}, {before.Fleet, after.Fleet}} {
		if !slices.Equal(pair[0].Rows, pair[1].Rows) {
			t.Fatalf("%s rows %v, were %v", pair[1].Name, pair[1].Rows, pair[0].Rows)
		}
		for _, c := range pair[1].Cards {
			if c.Placed() && c.Col != sprint.Ctl {
				t.Fatalf("%s still holds %s at %s", pair[1].Name, c.ID, c.Col)
			}
		}
	}
	if after.StreamCtl("s1").F("state") != sprint.StreamWaiting || after.MemberCtl("m1").F("status") != sprint.Up || after.MemberCtl("m1").F("ok") != "0" {
		t.Fatalf("control cards: %v %v", after.StreamCtl("s1").Fields, after.MemberCtl("m1").Fields)
	}
	if open, _ := h.st.Inbox(h.ctx, 0, 0, 100); len(open.Groups) != 0 {
		t.Fatalf("the new epoch's inbox is not empty: %+v", open.Groups)
	}
	h.clean("cleared")

	// Every writer holding the old epoch is refused, naming the clear.
	held := uint64(0)
	for name, step := range map[string]Step{
		"finish": FinishStep(sprint.FinishReq{As: before.Fleet.Card("s1-5.w1").Row, Sel: sprint.Sel{IDs: []string{"s1-5.w1"}}, Gens: map[string]int{"s1-5.w1": 1}}),
		"read":   ReadStep(sprint.ReadReq{As: before.Readers.Of("s1-4")[1].F("reader"), Verdict: "ok", Sel: sprint.Sel{IDs: []string{before.Readers.Of("s1-4")[1].ID}}}),
		"merge":  MergeStep(sprint.MergeReq{Stream: "s1"}),
	} {
		step.Epoch = &held
		res, err := h.st.Run(h.ctx, step)
		if err != nil || len(res.Moved) != 0 || len(res.Refused) != 1 || !strings.Contains(res.Refused[0].Why, "cleared at") || !strings.Contains(res.Refused[0].Why, "epoch is now 1") {
			t.Fatalf("a late %s of the old epoch: %+v %v", name, res, err)
		}
	}

	// The old epoch stays readable.
	old, err := h.st.At(0).Load(h.ctx, All, nil)
	if err != nil || old.StateOf("s1-1") != sprint.Landed || old.Merge.Placed("s1-2").Col != sprint.Stuck {
		t.Fatalf("the old epoch: %v", err)
	}
	if card, err := h.st.At(0).CardOf(h.ctx, "s1-3"); err != nil || card.Primary == nil || card.Primary.Col != sprint.Merging {
		t.Fatalf("card at the old epoch: %+v %v", card, err)
	}
	if v, err := h.st.At(0).Inbox(h.ctx, 0, 0, 1000); err != nil || len(v.Groups) == 0 {
		t.Fatalf("the old epoch's inbox: %+v %v", v, err)
	}

	// The same ids run again, to landed, in the new epoch.
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 3}))
	h.through("s1-1", "s1-2", "s1-3")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1"}))
	if s := h.snap(); s.StateOf("s1-1") != sprint.Landed || s.StateOf("s1-3") != sprint.Landed {
		t.Fatalf("the same ids again: %s %s", s.StateOf("s1-1"), s.StateOf("s1-3"))
	}
	h.clean("landed again")

	// Clear twice in a row.
	for want := uint64(2); want <= 3; want++ {
		res, err := h.st.Clear(h.ctx)
		if err != nil || res.To != want {
			t.Fatalf("clear to %d: %+v %v", want, res, err)
		}
		h.clean("cleared again")
	}
}

// A clear cut after the epoch advanced, before its shape was restored, is
// finished by the next clear, which then clears again.
func TestACutClearIsFinishedByTheNext(t *testing.T) {
	t.Parallel()
	h := midFlight(t)
	h.m.Fail = func(p string) error {
		if strings.HasPrefix(p, "apply ") {
			return errors.New("cut")
		}
		return nil
	}
	if _, err := h.st.Clear(h.ctx); err == nil {
		t.Fatalf("not cut")
	}
	h.m.Fail = nil
	res, err := h.st.Clear(h.ctx)
	if err != nil || !res.Restored || res.From != 1 || res.To != 2 {
		t.Fatalf("the next clear: %+v %v", res, err)
	}
	if s := h.snap(); s.StreamCtl("s1").F("state") != sprint.StreamWaiting || s.MemberCtl("m2") == nil {
		t.Fatalf("the shape is not restored")
	}
	h.clean("finished and cleared")
}

// Teardown after clears names every epoch's keys: the store is left as it was
// before init.
func TestTeardownAfterClearsLeavesNoKey(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	m := NewMem()
	h.m, h.st.B = m, m
	before := m.Keys(h.st.Names)
	if err := h.st.Init(h.ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.RowsAdd(h.ctx, "t-readers", []string{"reader-a", "reader-b", "reader-c"}); err != nil {
		t.Fatal(err)
	}
	h.setup(2)
	h.through("s1-1")
	for i := 0; i < 2; i++ {
		if _, err := h.st.Clear(h.ctx); err != nil {
			t.Fatal(err)
		}
		h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 2}))
		h.through("s1-1")
	}
	if _, err := h.st.Teardown(h.ctx); err != nil {
		t.Fatal(err)
	}
	if after := m.Keys(h.st.Names); !slices.Equal(after, before) {
		t.Fatalf("after teardown:\n%s\nbefore init:\n%s", strings.Join(after, "\n"), strings.Join(before, "\n"))
	}
}

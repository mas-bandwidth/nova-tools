package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

var t0 = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

type harness struct {
	t   *testing.T
	st  *Store
	m   *Mem
	ctx context.Context
	mu  sync.Mutex
	now time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, m: NewMem(), ctx: context.Background(), now: t0}
	n := 0
	h.st = &Store{B: h.m, Names: sprint.Names{Prefix: "t-"}, Actor: "tester",
		Now:   func() time.Time { h.mu.Lock(); defer h.mu.Unlock(); return h.now },
		NewID: func() string { h.mu.Lock(); defer h.mu.Unlock(); n++; return fmt.Sprint(n) },
		Sleep: func(time.Duration) {}}
	if err := h.st.Init(h.ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.m.RowsAdd(h.ctx, "t-readers", []string{"reader-a", "reader-b", "reader-c"}); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) tick(d time.Duration) { h.mu.Lock(); h.now = h.now.Add(d); h.mu.Unlock() }

func (h *harness) run(step Step) Result {
	h.t.Helper()
	res, err := h.st.Run(h.ctx, step)
	if err != nil {
		h.t.Fatalf("%s: %v", step.Verb, err)
	}
	return res
}

func (h *harness) must(step Step) Result {
	h.t.Helper()
	res := h.run(step)
	if len(res.Refused) > 0 {
		h.t.Fatalf("%s refused: %v", step.Verb, res.Refused)
	}
	return res
}

func (h *harness) clean(when string) {
	h.t.Helper()
	rep, _, err := h.st.Check(h.ctx, 3)
	if err != nil {
		h.t.Fatalf("%s: check: %v", when, err)
	}
	if len(rep.Violations) > 0 {
		h.t.Fatalf("%s: %v", when, rep.Violations)
	}
}

func (h *harness) snap() *sprint.Snapshot {
	h.t.Helper()
	s, err := h.st.Load(h.ctx, All, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	return s
}

func (h *harness) state(id string) string { return h.snap().StateOf(id) }

// setup: two members up, n primaries in s1.
func (h *harness) setup(n int) {
	h.t.Helper()
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: n}))
	h.clean("setup")
}

// through drives primaries to merging queued.
func (h *harness) through(ids ...string) {
	h.t.Helper()
	h.must(StartStep(sprint.StartReq{Sel: sprint.Sel{IDs: ids}}))
	s := h.snap()
	for _, id := range ids {
		c := s.Fleet.Card(s.Work.Card(id).F("work"))
		h.must(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
		h.must(FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
	}
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: ids}}))
	s = h.snap()
	for _, id := range ids {
		for _, rc := range s.Readers.Of(id) {
			h.must(ReadStep(sprint.ReadReq{As: rc.F("reader"), Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc.ID}}}))
		}
	}
	h.must(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: ids}}))
}

func TestTheLifeOfAStreamThroughTheStore(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(3)
	h.through("s1-1", "s1-2", "s1-3")
	h.clean("accepted")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 2}))
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 2}))
	s := h.snap()
	if s.StreamCtl("s1").F("state") != sprint.StreamLanded || s.Work.Count("s1", sprint.Landed) != 3 {
		t.Fatalf("stream: %s, landed %d", s.StreamCtl("s1").F("state"), s.Work.Count("s1", sprint.Landed))
	}
	h.clean("landed")
	v, err := h.st.Inbox(h.ctx, time.Hour, time.Hour, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, g := range v.Groups {
		types = append(types, g.Type)
	}
	for _, want := range []string{sprint.NWorkOK, sprint.NReadyToAccept, sprint.NStartedMerging, sprint.NBatchLanded, sprint.NStreamLanded} {
		found := false
		for _, got := range types {
			found = found || got == want
		}
		if !found {
			t.Errorf("no %q in the inbox: %v", want, types)
		}
	}
	// the display cells
	shapes, _ := h.m.Shapes(h.ctx, []string{"t-merge", "t-fleet"})
	if shapes[0].Rows[0].Texts[sprint.StateCol] != sprint.StreamLanded || shapes[0].Rows[0].Texts[sprint.CI] != "green" {
		t.Errorf("merge display cells: %v", shapes[0].Rows[0].Texts)
	}
	if got := shapes[1].Rows[0].Texts[sprint.OkPct]; got != "100.0%" {
		t.Errorf("fleet ok%%: %q", got)
	}
}

// A verb over more cards than a manifest takes is one invocation: one
// operation, each table's part split into manifests under the bound.
func TestALargeSetIsOneOperationInChunks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(300)
	before := h.m.Calls["apply"]
	res := h.must(StartStep(sprint.StartReq{Sel: sprint.Sel{Limit: 1000}}))
	if len(res.Moved) != 300 || h.m.Calls["apply"]-before != 6 || h.m.Calls["acquire"] == 0 {
		t.Fatalf("moved %d in %d manifests", len(res.Moved), h.m.Calls["apply"]-before)
	}
	s := h.snap()
	if s.Work.Count("s1", sprint.Working) != 300 || s.Fleet.Count("m1", sprint.Ready) != 150 {
		t.Fatalf("working %d, m1 ready %d", s.Work.Count("s1", sprint.Working), s.Fleet.Count("m1", sprint.Ready))
	}
	h.clean("300 started")
	// add of 1000 in one invocation
	h.must(AddStep(sprint.AddReq{Stream: "s2", Count: 1000}))
	if h.snap().Work.Count("s2", sprint.Ready) != 1000 {
		t.Fatalf("add 1000")
	}
}

// D1: a pending operation (cut after its first table) is finished by the next
// mutating verb before it reads, and check shows it while it is pending.
func TestD1APendingOperationIsFinishedFirst(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.m.Fail = func(p string) error {
		if p == "apply t-work before" {
			return errors.New("connection reset")
		}
		return nil
	}
	_, err := h.st.Run(h.ctx, StartStep(sprint.StartReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	if !errors.Is(err, ErrUnknown) || h.m.Pending() == nil {
		t.Fatalf("a lost reply on the work table: %v, pending %v", err, h.m.Pending())
	}
	h.m.Fail = nil
	rep, _, err := h.st.Check(h.ctx, 1)
	if err != nil || rep.Pending == "" || !rep.InFlight || len(rep.Violations) != 0 {
		t.Fatalf("check with the start in flight: %+v %v", rep, err)
	}
	h.tick(2 * time.Minute)
	rep, _, err = h.st.Check(h.ctx, 3)
	if err != nil || rep.Pending == "" || rep.InFlight || len(rep.Violations) != 1 || rep.Violations[0].Rule != 10 {
		t.Fatalf("check with the start cut: %+v %v", rep, err)
	}
	// the next verb, whatever it is, finishes it first
	res := h.must(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}}))
	if len(res.Repaired) != 1 || !strings.Contains(res.Repaired[0], "finished") || h.m.Pending() != nil {
		t.Fatalf("the take did not finish the pending start: %+v", res)
	}
	if h.state("s1-1") != sprint.Working {
		t.Fatalf("s1-1 is %s", h.state("s1-1"))
	}
	h.clean("finished")
}

// Notifications are visible at the release, never before, and once.
func TestD1NotificationsAtTheCommitOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(StartStep(sprint.StartReq{Sel: sprint.Sel{Limit: 1}}))
	h.must(TakeStep(sprint.TakeReq{As: h.snap().Fleet.Card("s1-1.w1").Row, Sel: sprint.Sel{Limit: 1}}))
	h.m.Fail = func(p string) error {
		if p == "release" {
			return errors.New("lost")
		}
		return nil
	}
	_, err := h.st.Run(h.ctx, FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{"s1-1.w1"}}, Failed: true}))
	if !errors.Is(err, ErrUnknown) {
		t.Fatalf("a lost commit: %v", err)
	}
	if open, _ := h.m.OpenNotes(h.ctx); len(open) != 0 {
		t.Fatalf("a judgment is visible before the commit: %v", open)
	}
	h.m.Fail = nil
	rr, err := h.st.Repair(h.ctx)
	if err != nil || len(rr) != 1 || rr[0].Done != "finished" {
		t.Fatalf("repair: %+v %v", rr, err)
	}
	if open, _ := h.m.OpenNotes(h.ctx); len(open) != 1 {
		t.Fatalf("after repair: %v", open)
	}
	if rr, _ := h.st.Repair(h.ctx); len(rr) != 0 {
		t.Fatalf("repaired twice: %v", rr)
	}
	if open, _ := h.m.OpenNotes(h.ctx); len(open) != 1 {
		t.Fatalf("the judgment was written twice: %v", open)
	}
	h.clean("repaired")
}

// racer runs another writer's step at a chosen store call, once.
type racer struct {
	Backend
	at   string
	once sync.Once
	do   func()
}

func (r *racer) Acquire(ctx context.Context, gen uint64, op OpRecord) (bool, error) {
	if r.at == "acquire" {
		r.once.Do(r.do)
	}
	return r.Backend.Acquire(ctx, gen, op)
}

func (r *racer) Apply(ctx context.Context, m ntable.BatchManifest) (ntable.Receipt, error) {
	if r.at == "apply "+m.Table {
		r.once.Do(r.do)
	}
	return r.Backend.Apply(ctx, m)
}

// Another writer between the read and the acquisition moves the fence: the
// step reads again and plans on the fresh state.
func TestAnotherWriterBetweenReadAndWriteMeansAFreshPlan(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(3)
	other := &Store{B: h.m, Names: h.st.Names, Actor: "other", Now: h.st.Now, NewID: func() string { return "o" }, Sleep: h.st.Sleep}
	r := &racer{Backend: h.m, at: "acquire", do: func() {
		if _, err := other.Run(h.ctx, StartStep(sprint.StartReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}})); err != nil {
			t.Error(err)
		}
	}}
	st := *h.st
	st.B = r
	res, err := st.Run(h.ctx, StartStep(sprint.StartReq{Sel: sprint.Sel{IDs: []string{"s1-1", "s1-2"}}}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Attempts != 2 || len(res.Moved) != 1 || len(res.Refused) != 1 || res.Refused[0].Key != "s1-1" {
		t.Fatalf("after the race: %+v", res)
	}
	h.clean("raced")
}

// A later table's revision moved by a writer outside the fence (a display
// cell): the manifest is sent again when its members are as expected.
func TestALaterTableMovedByADisplayWriteIsSentAgain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	r := &racer{Backend: h.m, at: "apply t-work", do: func() {
		if err := h.m.RowSet(h.ctx, "t-merge", "s1", map[string]string{"ci": "x"}); err != nil {
			t.Error(err)
		}
		if err := h.m.RowsAdd(h.ctx, "t-work", []string{"s9"}); err != nil {
			t.Error(err)
		}
	}}
	st := *h.st
	st.B = r
	if _, err := st.Run(h.ctx, StartStep(sprint.StartReq{Sel: sprint.Sel{Limit: 1}})); err != nil {
		t.Fatal(err)
	}
	if h.state("s1-1") != sprint.Working {
		t.Fatalf("s1-1 is %s", h.state("s1-1"))
	}
	h.clean("resent")
}

// A later table's member changed under the step: the operation is cut, stays
// pending, and says why; nothing overwrites the newer work.
func TestALaterMemberChangedCutsAndStaysPending(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	r := &racer{Backend: h.m, at: "apply t-work", do: func() {
		s := h.snap()
		p := s.Work.Card("s1-1")
		_, err := h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-work", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Work.Revision),
			OperationID: "intruder", Members: []ntable.BatchMemberEntry{{ID: p.ID, Expect: &ntable.MemberExpect{Place: &ntable.PlaceExpect{Row: p.Row, Col: p.Col}}, Set: map[string]string{"brief": "changed"}}}})
		if err != nil {
			t.Error(err)
		}
	}}
	st := *h.st
	st.B = r
	_, err := st.Run(h.ctx, StartStep(sprint.StartReq{Sel: sprint.Sel{Limit: 1}}))
	var cut *CutError
	if !errors.As(err, &cut) || h.m.Pending() == nil {
		t.Fatalf("a member changed under a later table: %v", err)
	}
	rr, err := h.st.Repair(h.ctx)
	if err != nil || len(rr) != 1 || rr[0].Done != "open" || !strings.Contains(rr[0].Detail, "changed under the step") {
		t.Fatalf("repair of a cut operation: %+v %v", rr, err)
	}
	h.tick(2 * time.Minute)
	if _, err := h.st.Run(h.ctx, TakeStep(sprint.TakeReq{As: "m1"})); err == nil {
		t.Fatalf("a verb ran over a pending operation that cannot finish")
	} else if pe := (*PendingError)(nil); !errors.As(err, &pe) {
		t.Fatalf("not a pending refusal: %v", err)
	}
}

// A pending operation whose first manifest never applied is left to its writer
// within the grace and abandoned after it.
func TestAnUnappliedPendingOperationIsAbandonedAfterTheGrace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.m.Fail = func(p string) error {
		if p == "apply t-fleet before" {
			return errors.New("down")
		}
		return nil
	}
	if _, err := h.st.Run(h.ctx, StartStep(sprint.StartReq{Sel: sprint.Sel{Limit: 1}})); !errors.Is(err, ErrUnknown) {
		t.Fatalf("start: %v", err)
	}
	h.m.Fail = nil
	// another writer's card now holds the id the start was to create: its
	// first manifest can never apply
	s := h.snap()
	if _, err := h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-fleet", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Fleet.Revision),
		OperationID: "intruder", Members: manyCreates(0)}); err == nil {
		t.Fatalf("an empty manifest applied")
	}
	if _, err := h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-fleet", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Fleet.Revision),
		OperationID: "intruder", Members: []ntable.BatchMemberEntry{{ID: "s1-1.w1", Expect: &ntable.MemberExpect{Absent: true},
			Create: &ntable.MemberCreateOp{Row: "m1", Col: "done", Score: 1}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Run(h.ctx, FleetStep(sprint.FleetReq{Op: "up", Member: "m3"})); err == nil || h.m.Pending() == nil {
		t.Fatalf("within the grace the operation is its writer's: %v", err)
	}
	h.tick(2 * time.Minute)
	res := h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m3"}))
	if h.m.Pending() != nil || len(res.Repaired) != 1 || !strings.Contains(res.Repaired[0], "abandoned") {
		t.Fatalf("past the grace: %+v", res)
	}
	if h.state("s1-1") != sprint.Ready {
		t.Fatalf("an abandoned start moved its primary")
	}
}

// D3: a retried finish with the same operation id returns the original
// result, with no second counter or notification.
func TestD3ARetriedFinishReturnsTheOriginal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(StartStep(sprint.StartReq{Sel: sprint.Sel{Limit: 1}}))
	m := h.snap().Fleet.Card("s1-1.w1").Row
	h.must(TakeStep(sprint.TakeReq{As: m, Sel: sprint.Sel{Limit: 1}}))
	step := FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{"s1-1.w1"}}, Gens: map[string]int{"s1-1.w1": 1}, Failed: true})
	step.CallerOp = "worker-7"
	first := h.must(step)
	again := h.must(step)
	if !again.Replay || len(again.Moved) != len(first.Moved) || again.Op != first.Op {
		t.Fatalf("retry: %+v, first %+v", again, first)
	}
	if ctl := h.snap().MemberCtl(m); ctl.F("failed") != "1" {
		t.Fatalf("failed counted %s times", ctl.F("failed"))
	}
	if open, _ := h.m.OpenNotes(h.ctx); len(open) != 1 {
		t.Fatalf("notified %d times", len(open))
	}
}

// Every refusal of the in-memory batch, and nothing changes with any of them.
func TestMemRefusesAsTheBatchDoes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	s := h.snap()
	p := s.Work.Card("s1-1")
	rev := fmt.Sprint(s.Work.Revision)
	man := func(op string, es ...ntable.BatchMemberEntry) ntable.BatchManifest {
		return ntable.BatchManifest{Schema: 1, Table: "t-work", Epoch: "0", ExpectedTableRevision: rev, OperationID: op, Members: es}
	}
	place := &ntable.PlaceExpect{Row: "s1", Col: "ready"}
	cases := []struct {
		name, code string
		m          ntable.BatchManifest
	}{
		{"table revision", "REVISION", ntable.BatchManifest{Schema: 1, Table: "t-work", Epoch: "0", ExpectedTableRevision: "999", OperationID: "a",
			Members: []ntable.BatchMemberEntry{{ID: p.ID, Expect: &ntable.MemberExpect{Place: place}, Set: map[string]string{"x": "1"}}}}},
		{"member revision", "MEMBERREVISION", man("b", ntable.BatchMemberEntry{ID: p.ID, Expect: &ntable.MemberExpect{Revision: "99"}, Set: map[string]string{"x": "1"}})},
		{"place", "PLACEGUARD", man("c", ntable.BatchMemberEntry{ID: p.ID, Expect: &ntable.MemberExpect{Place: &ntable.PlaceExpect{Row: "s1", Col: "review"}}, Set: map[string]string{"x": "1"}})},
		{"field", "FIELDGUARD", man("d", ntable.BatchMemberEntry{ID: p.ID, Expect: &ntable.MemberExpect{Place: place, Fields: map[string]ntable.FieldGuard{"stream": {Equals: strp("s9")}}}, Set: map[string]string{"x": "1"}})},
		{"member exists", "MEMBEREXISTS", man("e", ntable.BatchMemberEntry{ID: p.ID, Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "s1", Col: "ready", Score: 1}})},
		{"no member", "NOTMEMBER", man("f", ntable.BatchMemberEntry{ID: "nobody", Expect: &ntable.MemberExpect{Revision: "1"}, Set: map[string]string{"x": "1"}})},
		{"bound", "LIMIT", man("g", manyCreates(129)...)},
	}
	for _, c := range cases {
		before := h.m.Revision("t-work")
		_, err := h.m.Apply(h.ctx, c.m)
		if refusalCode(err) != c.code {
			t.Errorf("%s: %v, want %s", c.name, err, c.code)
		}
		if h.m.Revision("t-work") != before {
			t.Errorf("%s: a refusal changed the table", c.name)
		}
	}
	// replay: the same bytes return the original receipt; other bytes conflict
	ok := man("h", ntable.BatchMemberEntry{ID: p.ID, Expect: &ntable.MemberExpect{Place: place}, Set: map[string]string{"x": "1"}})
	r1, err := h.m.Apply(h.ctx, ok)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := h.m.Apply(h.ctx, ok)
	if err != nil || !r2.Replay || r2.After != r1.After {
		t.Fatalf("replay: %+v %v", r2, err)
	}
	ok.Members[0].Set["x"] = "2"
	if _, err := h.m.Apply(h.ctx, ok); refusalCode(err) != "OPCONFLICT" {
		t.Fatalf("other bytes under the id: %v", err)
	}
	if _, err := h.m.ReadSet(h.ctx, "t-work", make([]string, ntable.LimitReadSetMembers+1)); refusalCode(err) != "LIMIT" {
		t.Fatalf("a read set over the bound: %v", err)
	}
}

func strp(s string) *string { return &s }

func manyCreates(n int) []ntable.BatchMemberEntry {
	out := make([]ntable.BatchMemberEntry, n)
	for i := range out {
		out[i] = ntable.BatchMemberEntry{ID: fmt.Sprintf("x%d", i), Expect: &ntable.MemberExpect{Absent: true},
			Create: &ntable.MemberCreateOp{Row: "s1", Col: "ready", Score: float64(i)}}
	}
	return out
}

// D7: every open judgment shows whatever the cursor; the cursor bounds the
// happened list; a wait sets the review time without hiding the judgment.
func TestD7InboxThroughTheStore(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.must(StartStep(sprint.StartReq{Sel: sprint.Sel{Limit: 2}}))
	s := h.snap()
	for _, id := range []string{"s1-1.w1", "s1-2.w1"} {
		h.must(TakeStep(sprint.TakeReq{As: s.Fleet.Card(id).Row, Sel: sprint.Sel{IDs: []string{id}}}))
	}
	h.must(FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{"s1-1.w1"}}, Failed: true}))
	h.must(FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{"s1-2.w1"}}}))
	v, err := h.st.Inbox(h.ctx, time.Hour, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.m.SetCursor(h.ctx, v.Last); err != nil {
		t.Fatal(err)
	}
	v, _ = h.st.Inbox(h.ctx, time.Hour, 0, 1000)
	if len(v.Groups) != 1 || v.Groups[0].Kind != sprint.Judgment {
		t.Fatalf("after the cursor: %+v", v.Groups)
	}
	nid := v.Groups[0].Notes[0]
	if err := h.m.SetReview(h.ctx, nid, t0.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	h.tick(2 * time.Hour)
	v, _ = h.st.Inbox(h.ctx, time.Hour, 0, 1000)
	if len(v.Groups) != 1 || v.Groups[0].Overdue {
		t.Fatalf("a waited judgment: %+v", v.Groups)
	}
}

// unreadable answers every batch with a receipt this build cannot read, after
// committing it, as a store with another build's function library does.
type unreadable struct{ Backend }

func (u unreadable) Apply(ctx context.Context, m ntable.BatchManifest) (ntable.Receipt, error) {
	if _, err := u.Backend.Apply(ctx, m); err != nil {
		return ntable.Receipt{}, err
	}
	return ntable.Receipt{}, errors.New(`table "t-fleet" batch "x": unmarshal batch delta: json: cannot unmarshal number into Go struct field rawMemberDelta.after_score of type string`)
}

func TestAReceiptThisBuildCannotReadIsACommit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	st := *h.st
	st.B = unreadable{h.m}
	if _, err := st.Run(h.ctx, FleetStep(sprint.FleetReq{Op: "up", Member: "m1"})); err != nil {
		t.Fatal(err)
	}
	if h.m.Pending() != nil || h.snap().MemberCtl("m1").F("status") != sprint.Up {
		t.Fatalf("not committed")
	}
}

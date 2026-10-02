package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

type harness struct {
	t   *testing.T
	st  *Store
	m   *Mem
	ctx context.Context
	mu  sync.Mutex
	now time.Time
	// live is the members that beat, at the start and at every step of the
	// clock: the fleet machines alive (a test that has one fall silent takes
	// it out).
	live []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, m: NewMem(), ctx: context.Background(), now: t0, live: []string{"m1", "m2"}}
	n := 0
	h.st = &Store{B: h.m, Names: sprint.Names{Prefix: "t-"}, Actor: "tester",
		Now:   func() time.Time { h.mu.Lock(); defer h.mu.Unlock(); return h.now },
		NewID: func() string { h.mu.Lock(); defer h.mu.Unlock(); n++; return fmt.Sprint(n) },
		Sleep: func(time.Duration) {}}
	// every part a tick plans on its twin is checked against a fresh read
	h.st.CheckTwin = checkTwin
	require.NoError(t, h.st.Init(h.ctx))
	require.NoError(t, h.m.RowsAdd(h.ctx, "t-readers", []string{"reader-a", "reader-b", "reader-c"}))
	// the harness acts as the sprint's coordinator: judgments are theirs
	require.NoError(t, h.m.SetCoordinator(h.ctx, h.st.Actor))
	h.beat()
	return h
}

func (h *harness) tick(d time.Duration) { h.mu.Lock(); h.now = h.now.Add(d); h.mu.Unlock(); h.beat() }

// beat is one beat of every live member, at load 0, and of every reader (the
// readers of a harness are always there; reader away holds one away).
func (h *harness) beat() {
	h.t.Helper()
	require.NoError(h.t, h.st.BeatReaders(h.ctx))
	h.mu.Lock()
	live := append([]string(nil), h.live...)
	h.mu.Unlock()
	zero := 0.0
	for _, m := range live {
		_, err := h.st.Beat(h.ctx, m, &zero, hostload.Source{})
		require.NoError(h.t, err)
	}
}

func (h *harness) run(step Step) Result {
	h.t.Helper()
	res, err := h.st.Run(h.ctx, step)
	require.NoError(h.t, err, "%s: %v", step.Verb, err)
	return res
}

func (h *harness) must(step Step) Result {
	h.t.Helper()
	res := h.run(step)
	require.Empty(h.t, res.Refused, "%s refused: %v", step.Verb, res.Refused)
	return res
}

func (h *harness) clean(when string) {
	h.t.Helper()
	rep, _, err := h.st.Check(h.ctx, 3)
	require.NoError(h.t, err, "%s: check: %v", when, err)
	require.Empty(h.t, rep.Violations, "%s: %v", when, rep.Violations)
}

// snap is the sprint as the next pump leaves its work table: the tables with
// the work table's queue applied (sprint.WithQueue), the state every step but
// the pump plans on. table is the tables as stored.
func (h *harness) snap() *sprint.Snapshot {
	h.t.Helper()
	s := h.table()
	pinned, err := h.st.pin(h.ctx)
	require.NoError(h.t, err)
	q, err := pinned.B.QueueRead(h.ctx)
	require.NoError(h.t, err)
	return sprint.WithQueue(s, q)
}

func (h *harness) table() *sprint.Snapshot {
	h.t.Helper()
	s, err := h.st.Load(h.ctx, All, nil)
	require.NoError(h.t, err)
	return s
}

func (h *harness) state(id string) string { return h.snap().StateOf(id) }

// setup: two members up, n primaries in s1.
func (h *harness) setup(n int) {
	h.t.Helper()
	if err := h.st.BeatReaders(h.ctx); err != nil { // a reader asks for its queue: it is up
		h.t.Fatal(err)
	}
	// a member brought up here is as wide as the set is large: the deal holds a member to its
	// width (tla/DirtyTick.tla, WidthRespected), so a verb that deals all n needs the room; a
	// member the test brought up before keeps its width
	width := min(max(n, sprint.DefaultWidth), sprint.MaxWidth)
	for _, m := range []string{"m1", "m2"} {
		w := width
		if h.snap().Fleet.HasRow(m) {
			w = 0
		}
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: m, Width: w}))
	}
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: n}))
	h.clean("setup")
}

// through drives primaries to merging queued.
func (h *harness) through(ids ...string) {
	h.t.Helper()
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: ids}}))
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

// assertConflict runs a step expecting an OpConflictError with the recorded verb
// and otherArgs flag, asserting no replay and no moved cards.
func (h *harness) assertConflict(step Step, wantRecorded string, wantOtherArgs bool, msg string) (*OpConflictError, Result) {
	h.t.Helper()
	res, err := h.st.Run(h.ctx, step)
	var ce *OpConflictError
	if !assert.ErrorAs(h.t, err, &ce, "%s: %+v %v", msg, res, err) {
		return nil, res
	}
	assert.Equal(h.t, wantRecorded, ce.Recorded, "%s: %+v %v", msg, res, err)
	assert.Equal(h.t, wantOtherArgs, ce.OtherArgs, "%s: %+v %v", msg, res, err)
	assert.Contains(h.t, err.Error(), wantRecorded, "%s: %+v %v", msg, res, err)
	assert.False(h.t, res.Replay, "%s: %+v %v", msg, res, err)
	assert.Empty(h.t, res.Moved, "%s: %+v %v", msg, res, err)
	return ce, res
}

// assertNoRevisionsWritten asserts that no table moved and no pending operation is held.
func (h *harness) assertNoRevisionsWritten(before map[string]uint64) {
	h.t.Helper()
	h.nothingWritten(before)
}

// workCard returns the card for id from the current snapshot.
func (h *harness) workCard(id string) *sprint.Card {
	h.t.Helper()
	return h.snap().Work.Card(id)
}

// seedMissingNeeds recreates already-persisted data from the old admission bug.
// Production verbs no longer create it; recovery must still surface it to the coordinator.
func (h *harness) seedMissingNeeds(id, needs string) {
	h.t.Helper()
	h.m.mu.Lock()
	defer h.m.mu.Unlock()
	table := h.m.tables["t-work"]
	c := table.members[id]
	c.fields["needs"] = needs
	c.rev++
	table.rev++
}

// seedMissingNeeds is a backward-compatible wrapper around harness.seedMissingNeeds.
func seedMissingNeeds(h *harness, id, needs string) {
	h.t.Helper()
	h.seedMissingNeeds(id, needs)
}

// openJudgments returns open judgment notes of the given type, optionally filtered by subject.
func (h *harness) openJudgments(typ, subject string) []sprint.Open {
	h.t.Helper()
	return h.nOpenOf(typ, subject)
}

// allJudgments returns all judgment notes of the given type.
func (h *harness) allJudgments(typ string) []sprint.Note {
	h.t.Helper()
	return h.nAllNotes(typ)
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
	require.Equal(t, string(sprint.StreamLanded), s.StreamCtl("s1").F("state"), "stream: %s, landed %d", s.StreamCtl("s1").F("state"), s.Work.Count("s1", sprint.Landed))
	require.Equal(t, 3, s.Work.Count("s1", sprint.Landed), "stream: %s, landed %d", s.StreamCtl("s1").F("state"), s.Work.Count("s1", sprint.Landed))
	h.clean("landed")
	v, err := h.st.Inbox(h.ctx, time.Hour, time.Hour, 1000)
	require.NoError(t, err)
	var types []string
	for _, g := range v.Groups {
		types = append(types, g.Type)
	}
	for _, want := range []string{sprint.NWorkOK, sprint.NStartedMerging, sprint.NBatchLanded, sprint.NStreamLanded} {
		found := false
		for _, got := range types {
			found = found || got == want
		}
		assert.True(t, found, "no %q in the inbox: %v", want, types)
	}
	// the display cells
	shapes, _ := h.m.Shapes(h.ctx, []string{"t-merge", "t-fleet"})
	if shapes[0].Rows[0].Texts[sprint.StateCol] != sprint.StreamLanded || shapes[0].Rows[0].Texts[sprint.CI] != "green" {
		t.Errorf("merge display cells: %v", shapes[0].Rows[0].Texts)
	}
	fleet := shapes[1]
	got := ntable.CellText(fleet.Columns, fleet.Rows[0], fleet.Column(sprint.OkPct))
	assert.Equal(t, "100.0%", got, "fleet ok%%: %q", got)
	_, written := fleet.Rows[0].Texts[sprint.OkPct]
	assert.False(t, written, "ok%% is written as text: %v", fleet.Rows[0].Texts)
}

// A verb over more cards than a manifest takes is one invocation: one
// operation, each table's part split into manifests under the bound.
func TestALargeSetIsOneOperationInChunks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(300)
	before := h.m.Calls["apply"]
	res := h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 1000}}))
	if len(res.Moved) != 300 || h.m.Calls["apply"]-before != 6 || h.m.Calls["acquire"] == 0 {
		t.Fatalf("moved %d in %d manifests", len(res.Moved), h.m.Calls["apply"]-before)
	}
	s := h.snap()
	require.Equal(t, 300, s.Work.Count("s1", sprint.Working), "working %d, m1 ready %d", s.Work.Count("s1", sprint.Working), s.Fleet.Count("m1", sprint.Ready))
	require.Equal(t, 150, s.Fleet.Count("m1", sprint.Ready), "working %d, m1 ready %d", s.Work.Count("s1", sprint.Working), s.Fleet.Count("m1", sprint.Ready))
	h.clean("300 started")
	// add of 1000 in one invocation
	h.must(AddStep(sprint.AddReq{Stream: "s2", Count: 1000}))
	require.Equal(t, 1000, h.snap().Work.Count("s2", sprint.Ready), "add 1000")
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
	_, err := h.st.Run(h.ctx, DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	require.ErrorIs(t, err, ErrUnknown, "a lost reply on the work table: %v, pending %v", err, h.m.Pending())
	require.NotNil(t, h.m.Pending(), "a lost reply on the work table: %v, pending %v", err, h.m.Pending())
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
	require.Equal(t, sprint.Working, h.state("s1-1"), "s1-1 is %s", h.state("s1-1"))
	h.clean("finished")
}

// Notifications are visible at the release, never before, and once.
func TestD1NotificationsAtTheCommitOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 1}}))
	h.must(TakeStep(sprint.TakeReq{As: h.snap().Fleet.Card("s1-1.w1").Row, Sel: sprint.Sel{Limit: 1}}))
	h.m.Fail = func(p string) error {
		if p == "release" {
			return errors.New("lost")
		}
		return nil
	}
	_, err := h.st.Run(h.ctx, FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{"s1-1.w1"}}, Gens: map[string]int{"s1-1.w1": 1}, Failed: true}))
	require.ErrorIs(t, err, ErrUnknown, "a lost commit: %v", err)
	open, _ := h.m.OpenNotes(h.ctx)
	require.Empty(t, open, "a judgment is visible before the commit: %v", open)
	h.m.Fail = nil
	rr, err := h.st.Repair(h.ctx)
	require.NoError(t, err, "repair: %+v %v", rr, err)
	require.Len(t, rr, 1, "repair: %+v %v", rr, err)
	require.Equal(t, "finished", rr[0].Done, "repair: %+v %v", rr, err)
	open, _ = h.m.OpenNotes(h.ctx)
	require.Len(t, open, 1, "after repair: %v", open)
	rr, _ = h.st.Repair(h.ctx)
	require.Empty(t, rr, "repaired twice: %v", rr)
	open, _ = h.m.OpenNotes(h.ctx)
	require.Len(t, open, 1, "the judgment was written twice: %v", open)
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
		if _, err := other.Run(h.ctx, DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}})); err != nil {
			t.Error(err)
		}
	}}
	st := *h.st
	st.B = r
	res, err := st.Run(h.ctx, DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1", "s1-2"}}}))
	require.NoError(t, err)
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
		assert.NoError(t, h.m.RowSet(h.ctx, "t-merge", "s1", map[string]string{"ci": "x"}))
		assert.NoError(t, h.m.RowsAdd(h.ctx, "t-work", []string{"s9"}))
	}}
	st := *h.st
	st.B = r
	_, err := st.Run(h.ctx, DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 1}}))
	require.NoError(t, err)
	require.Equal(t, sprint.Working, h.state("s1-1"), "s1-1 is %s", h.state("s1-1"))
	h.clean("resent")
}

// A later table's member changed under the step (a writer outside the
// fence): the operation is cut; repair applies what still holds, skips the
// changed member without overwriting it, writes one judgment listing the skip,
// and releases the fence, so every verb runs again.
func TestALaterMemberChangedIsSkippedByRepair(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	r := &racer{Backend: h.m, at: "apply t-work", do: func() {
		s := h.snap()
		p := s.Work.Card("s1-1")
		_, err := h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-work", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Work.Revision),
			OperationID: "intruder", Members: []ntable.BatchMemberEntry{{ID: p.ID, Expect: &ntable.MemberExpect{Place: &ntable.PlaceExpect{Row: p.Row, Col: p.Col}}, Set: map[string]string{"brief": "changed"}}}})
		assert.NoError(t, err)
	}}
	st := *h.st
	st.B = r
	_, err := st.Run(h.ctx, DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 1}}))
	var cut *CutError
	require.ErrorAs(t, err, &cut, "a member changed under a later table: %v", err)
	require.NotNil(t, h.m.Pending(), "a member changed under a later table: %v", err)
	rr, err := h.st.Repair(h.ctx)
	// the card skipped, and the work table's stream index its deal would have
	// moved (a manifest none of whose member changes applied leaves its
	// properties where they were)
	if err != nil || len(rr) != 1 || rr[0].Done != RepairSkipped || len(rr[0].Skipped) != 2 || !strings.Contains(rr[0].Skipped[0], "s1-1") ||
		!strings.Contains(rr[0].Skipped[1], "table property "+sprint.PropStreamIndex) {
		t.Fatalf("repair of a cut operation: %+v %v", rr, err)
	}
	require.Nil(t, h.m.Pending(), "repair left the fence held")
	if c := h.snap().Work.Card("s1-1"); c.F("brief") != "changed" || c.Col != sprint.Ready {
		t.Fatalf("repair overwrote the newer state: %s %s", c.Col, c.F("brief"))
	}
	h.tick(2 * time.Minute)
	_, err = h.st.Run(h.ctx, FleetStep(sprint.FleetReq{Op: "up", Member: "m3"}))
	require.NoError(t, err, "a verb after the repair: %v", err)
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
	_, err := h.st.Run(h.ctx, DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 1}}))
	require.ErrorIs(t, err, ErrUnknown, "start: %v", err)
	h.m.Fail = nil
	// another writer's card now holds the id the start was to create: its
	// first manifest can never apply
	s := h.snap()
	_, err = h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-fleet", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Fleet.Revision),
		OperationID: "intruder", Members: manyCreates(0)})
	require.Error(t, err, "an empty manifest applied")
	_, err = h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-fleet", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Fleet.Revision),
		OperationID: "intruder", Members: []ntable.BatchMemberEntry{{ID: "s1-1.w1", Expect: &ntable.MemberExpect{Absent: true},
			Create: &ntable.MemberCreateOp{Row: "m1", Col: sprint.DoneOK, Score: 1}}}})
	require.NoError(t, err)
	if _, err := h.st.Run(h.ctx, FleetStep(sprint.FleetReq{Op: "up", Member: "m3"})); err == nil || h.m.Pending() == nil {
		t.Fatalf("within the grace the operation is its writer's: %v", err)
	}
	h.tick(2 * time.Minute)
	res := h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m3"}))
	if h.m.Pending() != nil || len(res.Repaired) != 1 || !strings.Contains(res.Repaired[0], "abandoned") {
		t.Fatalf("past the grace: %+v", res)
	}
	require.Equal(t, sprint.Ready, h.state("s1-1"), "an abandoned deal moved its primary")
	notes, _, _ := h.m.NotesSince(h.ctx, "", 1000)
	found := false
	for _, n := range notes {
		found = found || n.Type == sprint.NAbandoned && strings.Contains(n.What, "(deal) by tester, 2m0s old")
	}
	require.True(t, found, "abandoned silently: %+v", notes)
}

// D3: a retried finish with the same operation id returns the original
// result, with no second counter or notification.
func TestD3ARetriedFinishReturnsTheOriginal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 1}}))
	m := h.snap().Fleet.Card("s1-1.w1").Row
	h.must(TakeStep(sprint.TakeReq{As: m, Sel: sprint.Sel{Limit: 1}}))
	step := FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{"s1-1.w1"}}, Gens: map[string]int{"s1-1.w1": 1}, Failed: true})
	step.CallerOp = "worker-7"
	first := h.must(step)
	again := h.must(step)
	if !again.Replay || len(again.Moved) != len(first.Moved) || again.Op != first.Op {
		t.Fatalf("retry: %+v, first %+v", again, first)
	}
	n := h.snap().Fleet.Count(m, sprint.DoneFailed)
	require.Equal(t, 1, n, "failed counted %d times", n)
	open, _ := h.m.OpenNotes(h.ctx)
	require.Len(t, open, 1, "notified %d times", len(open))
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
		assert.Equal(t, c.code, refusalCode(err), "%s: %v, want %s", c.name, err, c.code)
		assert.Equal(t, before, h.m.Revision("t-work"), "%s: a refusal changed the table", c.name)
	}
	// replay: the same bytes return the original receipt; other bytes conflict
	ok := man("h", ntable.BatchMemberEntry{ID: p.ID, Expect: &ntable.MemberExpect{Place: place}, Set: map[string]string{"x": "1"}})
	r1, err := h.m.Apply(h.ctx, ok)
	require.NoError(t, err)
	r2, err := h.m.Apply(h.ctx, ok)
	if err != nil || !r2.Replay || r2.After != r1.After {
		t.Fatalf("replay: %+v %v", r2, err)
	}
	ok.Members[0].Set["x"] = "2"
	_, err = h.m.Apply(h.ctx, ok)
	require.Equal(t, "OPCONFLICT", refusalCode(err), "other bytes under the id: %v", err)
	_, err = h.m.ReadSet(h.ctx, "t-work", make([]string, ntable.LimitReadSetMembers+1))
	require.Equal(t, "LIMIT", refusalCode(err), "a read set over the bound: %v", err)
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
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 2}}))
	s := h.snap()
	for _, id := range []string{"s1-1.w1", "s1-2.w1"} {
		h.must(TakeStep(sprint.TakeReq{As: s.Fleet.Card(id).Row, Sel: sprint.Sel{IDs: []string{id}}, Gens: map[string]int{id: 1}}))
	}
	h.must(FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{"s1-1.w1"}}, Gens: map[string]int{"s1-1.w1": 1}, Failed: true}))
	h.must(FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{"s1-2.w1"}}, Gens: map[string]int{"s1-2.w1": 1}}))
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}})) // asked: no move is due while STOPPED
	v, err := h.st.Inbox(h.ctx, time.Hour, 0, 1000)
	require.NoError(t, err)
	require.NoError(t, h.m.SetCursor(h.ctx, v.Last))
	v, _ = h.st.Inbox(h.ctx, time.Hour, 0, 1000)
	require.Len(t, v.Groups, 1, "after the cursor: %+v", v.Groups)
	require.Equal(t, string(sprint.Judgment), v.Groups[0].Kind, "after the cursor: %+v", v.Groups)
	nid := v.Groups[0].Notes[0]
	require.NoError(t, h.m.SetReview(h.ctx, nid, t0.Add(3*time.Hour), h.now))
	h.tick(2 * time.Hour)
	v, _ = h.st.Inbox(h.ctx, time.Hour, 0, 1000)
	require.Len(t, v.Groups, 1, "a waited judgment: %+v", v.Groups)
	require.False(t, v.Groups[0].Overdue, "a waited judgment: %+v", v.Groups)
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

func TestAReceiptThisBuildCannotReadIsUnknown(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	st := *h.st
	st.B = unreadable{h.m}
	_, err := st.Run(h.ctx, FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	if !errors.Is(err, ErrUnknown) || !strings.Contains(err.Error(), "do not match this build") || !strings.Contains(err.Error(), "nova-redis fn load") {
		t.Fatalf("an unreadable receipt: %v", err)
	}
	require.NotNil(t, h.m.Pending(), "an unreadable receipt was counted as applied: the operation left the fence")
}

// ack answers a judgment with one decided note: the coordinator's reason.
func TestAckWritesOneDecidedNote(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(CIStep(sprint.CIReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Red: true, Run: "r1"}))
	open, _ := h.m.OpenNotes(h.ctx)
	h.must(AckStep(sprint.AckReq{Notes: []string{open[0].Note.ID}, Reason: "a flaky runner"}))
	notes, _, _ := h.m.NotesSince(h.ctx, "", 1000)
	var decided []sprint.Note
	for _, n := range notes {
		if n.Kind == sprint.Decided {
			decided = append(decided, n)
		}
	}
	if len(decided) != 1 || decided[0].What != "ack: a flaky runner" || decided[0].Answers != open[0].Note.ID {
		t.Fatalf("decided notes: %+v", decided)
	}
}

// checkTwin is every harness's CheckTwin: the twin a part planned on is the
// state a fresh read of the same generation gives.
func checkTwin(twin, fresh *sprint.Snapshot) error {
	if d := TwinDiff(twin, fresh); d != "" {
		return errors.New(d)
	}
	return nil
}

// A decided note names the primaries it answers in order, whatever order the
// step that answered them took them in.
func TestADecidedNoteNamesItsPrimariesInOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(3)
	h.must(CIStep(sprint.CIReq{Sel: sprint.Sel{IDs: []string{"s1-3", "s1-1", "s1-2"}}, Red: true, Run: "r1"}))
	open, _ := h.m.OpenNotes(h.ctx)
	require.Len(t, open, 3, "the red run opened %+v, want one judgment on three primaries", open)
	require.Equal(t, open[2].Note.ID, open[0].Note.ID, "the red run opened %+v, want one judgment on three primaries", open)
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-3", "s1-1", "s1-2"}}}))
	notes, _, _ := h.m.NotesSince(h.ctx, "", 1000)
	for _, n := range notes {
		if n.Kind == sprint.Decided && n.Answers == open[0].Note.ID {
			if want := []string{"s1-1", "s1-2", "s1-3"}; !slices.Equal(n.Primaries, want) || n.Count != 3 {
				t.Fatalf("the decided note names %v (%d), want %v", n.Primaries, n.Count, want)
			}
			return
		}
	}
	t.Fatal("no decided note")
}

// A step that moves a stream's cards sets the stream's progress clock to the
// step's time, whatever notes it writes.
func TestAStepSetsTheProgressClockOfTheStreamsItMoved(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.tick(time.Minute)
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 2}}))
	progress, err := h.m.Progress(h.ctx)
	if at := progress["s1"]; err != nil || !at.Equal(h.now) {
		t.Fatalf("the progress of s1 is %v (%v), want the deal's time %v: %v", at, err, h.now, progress)
	}
}

// logReads is a store that counts the reads of its log.
type logReads struct {
	*Mem
	n int
}

func (l *logReads) LogSince(ctx context.Context, after string, max int) ([]sprint.Line, []string, error) {
	l.n++
	return l.Mem.LogSince(ctx, after, max)
}

// Check holds the log's rules (13, 14) to the whole log, and a check that
// leaves them to its caller reads none of it.
func TestCheckReadsTheLogOnlyWhenItHoldsTheLogsRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	l := &logReads{Mem: h.m}
	st := *h.st
	st.B = l
	for _, c := range []struct {
		name    string
		streams bool
		reads   bool
	}{
		{"the log's rules left to the caller", false, false},
		{"the log's rules held", true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			l.n = 0
			_, _, err := st.check(h.ctx, 1, c.streams)
			require.NoError(t, err)
			assert.Equal(t, c.reads, l.n > 0, "%s: the log was read %d times, want reads %v", c.name, l.n, c.reads)
		})
	}
}

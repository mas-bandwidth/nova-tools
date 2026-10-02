package store

// Repair never abandons an operation that applied, and a writer is never told
// its operation was cut when it was finished: a slow writer and the tick's
// repair, driven deterministically through hooks on Mem, and two tick loops
// racing workers.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// hooked is Mem with a hook before each apply, each read set, and around each
// release; n counts the calls of each.
type hooked struct {
	*Mem
	onApply   func(n int)
	onReadSet func(n int)
	onRelease func(before bool)
	na, nr    int
	mu        sync.Mutex
}

func (x *hooked) Apply(ctx context.Context, m ntable.BatchManifest) (ntable.Receipt, error) {
	x.mu.Lock()
	x.na++
	n := x.na
	x.mu.Unlock()
	if x.onApply != nil {
		x.onApply(n)
	}
	return x.Mem.Apply(ctx, m)
}

func (x *hooked) ReadSet(ctx context.Context, table string, ids []string) (ntable.ReadSetResult, error) {
	x.mu.Lock()
	x.nr++
	n := x.nr
	x.mu.Unlock()
	if x.onReadSet != nil {
		x.onReadSet(n)
	}
	return x.Mem.ReadSet(ctx, table, ids)
}

func (x *hooked) Release(ctx context.Context, op OpRecord, commit bool) error {
	if x.onRelease != nil {
		x.onRelease(true)
	}
	err := x.Mem.Release(ctx, op, commit)
	if x.onRelease != nil {
		x.onRelease(false)
	}
	return err
}

// A worker's finish --failed stalls past the grace after a display write
// moved the fleet table's revision; the tick's repair is refused on its first
// manifest, and the worker's refreshed send applies before the repair looks
// at the members. The repair finishes the operation with its notes: the
// judgment work came back failed is written once, and nothing says abandoned.
func TestRepairNeverAbandonsAnOperationThatApplied(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	h.run(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}, Who: "m1"}))
	c := h.snap().Fleet.Cell("m1", sprint.Working)[0]

	stalled := make(chan struct{})    // W holds the fence and has stalled past the grace
	wReleased := make(chan struct{})  // W may release
	bAtReadSet := make(chan struct{}) // B reached its member check
	wApplied := make(chan struct{})   // W's refreshed manifest applied
	W := &hooked{Mem: h.m}
	W.onApply = func(n int) {
		switch n {
		case 1: // a display write moves the table revision, and W stalls past the grace
			_ = h.m.RowSet(h.ctx, h.st.Names.Table(sprint.Fleet), "m1", map[string]string{"load": "x"})
			h.tick(2 * time.Minute)
			close(stalled)
		case 2: // W's refreshed send waits until B has been refused and reads
			<-bAtReadSet
		}
	}
	W.onRelease = func(before bool) {
		if before {
			close(wApplied)
			<-wReleased
		}
	}
	B := &hooked{Mem: h.m}
	B.onReadSet = func(n int) {
		if n == 1 {
			close(bAtReadSet)
			<-wApplied
		}
	}
	var once sync.Once
	B.onRelease = func(before bool) {
		if !before {
			once.Do(func() { close(wReleased) })
		}
	}
	ws := &Store{B: W, Names: h.st.Names, Actor: "m1", Now: h.st.Now, NewID: h.st.NewID, Sleep: func(time.Duration) {}}
	bs := &Store{B: B, Names: h.st.Names, Actor: "machine", Now: h.st.Now, NewID: h.st.NewID, Sleep: func(time.Duration) {}}

	var wres Result
	var werr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		wres, werr = ws.Run(h.ctx, FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Failed: true, Report: "boom", Who: "m1"}))
	}()
	<-stalled
	tres, terr := bs.Tick(h.ctx)
	<-done
	t.Logf("worker's finish: moved %v err %v; tick: repaired %+v err %v", wres.Moved, werr, tres.Repaired, terr)
	h.clean("after")
	require.Equal(t, sprint.Review, h.state("s1-1"), "s1-1 is %s", h.state("s1-1"))
	if n := h.written(sprint.NWorkFailed); n != 1 || len(h.openOf(sprint.NWorkFailed)) != 1 {
		t.Fatalf("the finish applied (s1-1 in review, failed): work came back failed written %d, open %d", n, len(h.openOf(sprint.NWorkFailed)))
	}
	n := h.written(sprint.NAbandoned)
	require.Equal(t, 0, n, "an operation that applied was released as abandoned (%d)", n)
	require.NoError(t, werr, "the worker was told")
	h.machine()
	h.tick(time.Hour)
	h.machine()
	require.Len(t, h.openOf(sprint.NWorkFailed), 1, "after an hour of ticks: %v", h.openOf(sprint.NWorkFailed))
}

// An operation whose first manifest never applied, and whose members moved,
// is abandoned past the grace: nothing of it happened.
func TestRepairAbandonsAnOperationNoneOfWhichApplied(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.run(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	h.run(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}, Who: "m1"}))
	c := h.snap().Fleet.Cell("m1", sprint.Working)[0]
	// The writer dies after acquiring the fence: its first manifest is never
	// sent. Then the work card moves under it, by a writer outside the fence.
	st := *h.st
	st.B = &failAt{Backend: h.m, at: "apply t-fleet"}
	if _, err := st.Run(h.ctx, FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Who: "m1"})); err == nil || h.m.Pending() == nil {
		t.Fatalf("the writer did not die with its operation pending: %v", err)
	}
	// Every member of its first manifest is changed by a writer outside the
	// fence.
	var outside []ntable.BatchMemberEntry
	fleet := h.snap().Fleet
	for _, e := range h.m.Pending().Manifests[0].Members {
		outside = append(outside, ntable.BatchMemberEntry{ID: e.ID, Expect: &ntable.MemberExpect{Revision: fmt.Sprint(fleet.Card(e.ID).Rev)},
			Set: map[string]string{"note": "outside"}})
	}
	if _, err := h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-fleet", Epoch: "0", ExpectedTableRevision: fmt.Sprint(fleet.Revision),
		OperationID: "outside", Members: outside}); err != nil {
		t.Fatal(err)
	}
	h.tick(2 * time.Minute)
	rr, err := h.st.Repair(h.ctx)
	if err != nil || len(rr) != 1 || rr[0].Done != RepairAbandoned {
		t.Fatalf("repair: %+v %v", rr, err)
	}
	if h.state("s1-1") != sprint.Working || h.written(sprint.NAbandoned) != 1 || len(h.skipNotes()) != 0 {
		t.Fatalf("s1-1 %s, abandoned %d, skips %d", h.state("s1-1"), h.written(sprint.NAbandoned), len(h.skipNotes()))
	}
	h.clean("abandoned")
}

// failAt is a backend whose applies at the named table never answer: a
// writer that died on the wire.
type failAt struct {
	Backend
	at string
}

func (f *failAt) Apply(ctx context.Context, m ntable.BatchManifest) (ntable.Receipt, error) {
	if "apply "+m.Table == f.at {
		return ntable.Receipt{}, fmt.Errorf("%w: the writer died", ntable.ErrUnknownOutcome)
	}
	return f.Backend.Apply(ctx, m)
}

// A dead writer's first manifest, one of whose members moved, past the grace:
// the entries that still hold apply, the moved one is skipped, and the
// operation is released with its notes and the one skip judgment.
func TestRepairAppliesWhatHoldsOfAFirstManifestPastTheGrace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.must(FleetStep(sprint.FleetReq{Op: "down", Member: "m2"}))
	h.run(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1", "s1-2"}}}))
	h.run(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 2}, Who: "m1"}))
	cards := h.snap().Fleet.Cell("m1", sprint.Working)
	c, other := cards[0], cards[1]
	st := *h.st
	st.B = &failAt{Backend: h.m, at: "apply t-fleet"}
	gens := map[string]int{c.ID: c.Int("gen"), other.ID: other.Int("gen")}
	if _, err := st.Run(h.ctx, FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{c.ID, other.ID}}, Gens: gens, Failed: true, Report: "boom", Who: "m1"})); err == nil || h.m.Pending() == nil {
		t.Fatalf("the writer did not die with its operation pending: %v", err)
	}
	// The work card moves under it: an outside writer sets a field on it.
	fleet := h.snap().Fleet
	card := fleet.Card(c.ID)
	if _, err := h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-fleet", Epoch: "0", ExpectedTableRevision: fmt.Sprint(fleet.Revision),
		OperationID: "outside", Members: []ntable.BatchMemberEntry{{ID: card.ID, Expect: &ntable.MemberExpect{Revision: fmt.Sprint(card.Rev)}, Set: map[string]string{"note": "outside"}}}}); err != nil {
		t.Fatal(err)
	}
	h.tick(2 * time.Minute)
	rr, err := h.st.Repair(h.ctx)
	if err != nil || len(rr) != 1 || rr[0].Done != RepairSkipped || len(rr[0].Skipped) == 0 || !strings.Contains(strings.Join(rr[0].Skipped, " "), c.ID) {
		t.Fatalf("repair: %+v %v", rr, err)
	}
	// The entry that held applied: the other card is in failed and its
	// primary in review.
	require.Equal(t, string(sprint.DoneFailed), h.snap().Fleet.Card(other.ID).Col, "the entry that held: %s %s", h.snap().Fleet.Card(other.ID).Col, h.state(other.F("primary")))
	require.Equal(t, sprint.Review, h.state(other.F("primary")), "the entry that held: %s %s", h.snap().Fleet.Card(other.ID).Col, h.state(other.F("primary")))
	skips := h.skipNotes()
	if h.written(sprint.NAbandoned) != 0 || len(skips) != 1 || !strings.Contains(skips[0].What, c.ID) {
		t.Fatalf("skip judgments %+v, abandoned %d", skips, h.written(sprint.NAbandoned))
	}
	// The card whose entry was skipped is left half-moved, named in the skip
	// judgment; check says so, and the judgment's drop restores the rules.
	rep, _, err := h.st.Check(h.ctx, 5)
	if err != nil || len(rep.Violations) == 0 || !strings.Contains(fmt.Sprint(rep.Violations), c.F("primary")) {
		t.Fatalf("check after the half move: %+v %v", rep.Violations, err)
	}
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{c.F("primary")}}, Reason: "half moved by repair", Answers: []string{skips[0].ID}}))
	h.clean("dropped the half-moved card")
}

// Within the grace the tick finishes a live writer's operation for it: the
// writer reports its recorded result, as a replay would, and is never told
// its operation was cut.
func TestAWriterIsNotToldCutWhenItsOperationWasFinished(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	h.run(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}, Who: "m1"}))
	c := h.snap().Fleet.Cell("m1", sprint.Working)[0]
	bDone := make(chan struct{})
	wWaiting := make(chan struct{})
	W := &hooked{Mem: h.m}
	sent := false
	W.onApply = func(n int) {
		if n == 1 { // a display write moved the table's revision
			_ = h.m.RowSet(h.ctx, h.st.Names.Table(sprint.Fleet), "m1", map[string]string{"load": "x"})
			sent = true
		}
	}
	W.onReadSet = func(n int) {
		if sent { // the writer's member check, after its first send was refused on the revision
			sent = false
			close(wWaiting)
			<-bDone
		}
	}
	ws := &Store{B: W, Names: h.st.Names, Actor: "m1", Now: h.st.Now, NewID: h.st.NewID, Sleep: func(time.Duration) {}}
	var wres Result
	var werr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		wres, werr = ws.Run(h.ctx, FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Who: "m1"}))
	}()
	<-wWaiting
	_, terr := h.st.Tick(h.ctx)
	close(bDone)
	<-done
	t.Logf("tick err %v; worker %+v err %v; s1-1 %s; pending %v", terr, wres, werr, h.state("s1-1"), h.m.Pending())
	h.clean("after")
	require.Equal(t, sprint.Review, h.state("s1-1"), "s1-1 %s, pending %v", h.state("s1-1"), h.m.Pending())
	require.Nil(t, h.m.Pending(), "s1-1 %s, pending %v", h.state("s1-1"), h.m.Pending())
	require.NoError(t, werr, "the worker's finish applied and was committed, but the worker was told")
	require.Len(t, wres.Moved, 1, "the worker's result: %+v", wres)
	require.Contains(t, wres.Moved[0], "s1-1", "the worker's result: %+v", wres)
}

// tagged is Mem shared by several writers in one test, each with its own
// store: nothing more than the backend, so every writer races on one store.
type tagged struct{ *Mem }

var zombieMembers = []string{"m1", "m2", "m3"}

// zombieSprint is three members up and forty primaries in three streams,
// with a chain, a diamond and cross-stream needs.
func zombieSprint(t *testing.T) *harness {
	h := newHarness(t)
	for _, m := range zombieMembers {
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: m}))
	}
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"c1"}}))
	for i := 2; i <= 6; i++ {
		h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{fmt.Sprintf("c%d", i)}, Needs: []string{fmt.Sprintf("c%d", i-1)}}))
	}
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 8}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"d0"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"d1", "d2"}, Needs: []string{"d0"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"d3"}, Needs: []string{"d1", "d2"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", Count: 9}))
	h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"x1"}, Needs: []string{"c3", "d3"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s3", Count: 9}))
	h.clean("setup")
	return h
}

// Two tick loops run while workers take and finish every card: every take
// and finish returns without error (a finished operation is reported as
// done, whoever finished it), and check holds at the end.
func TestTwoTickLoopsNeverTellAWriterItWasCut(t *testing.T) {
	t.Parallel()
	for trial := 0; trial < crScale.CutTrials; trial++ {
		h := zombieSprint(t)
		h.startMachine()
		mk := func(who string) *Store {
			return &Store{B: tagged{h.m}, Names: h.st.Names, Actor: who, Now: h.st.Now, NewID: h.st.NewID, Sleep: func(time.Duration) {}}
		}
		a, b := mk("machine-a"), mk("machine-b")
		var wg sync.WaitGroup
		stop := make(chan struct{})
		loop := func(st *Store) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = st.Tick(h.ctx)
			}
		}
		wg.Add(2)
		go loop(a)
		go loop(b)
		var bad error
		for r := 1; r <= crScale.CutRounds && bad == nil; r++ {
			for _, m := range zombieMembers {
				if _, err := h.st.Run(h.ctx, TakeStep(sprint.TakeReq{As: m, Sel: sprint.Sel{Limit: 100}, Who: m})); err != nil {
					bad = err
					break
				}
				s := h.snap()
				for _, c := range s.Fleet.Cell(m, sprint.Working) {
					if _, err := h.st.Run(h.ctx, FinishStep(sprint.FinishReq{As: m, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Who: m})); err != nil {
						bad = err
					}
				}
			}
			h.tick(time.Second)
		}
		close(stop)
		wg.Wait()
		require.NoError(t, bad, "trial %d: a writer was told: %v", trial, bad)
		h.clean(fmt.Sprintf("trial %d", trial))
	}
}

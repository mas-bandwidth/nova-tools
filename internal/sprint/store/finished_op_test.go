package store

// Repair never abandons an operation that applied: a slow writer and the
// tick's repair, driven deterministically through hooks on Mem.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
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
	if h.state("s1-1") != sprint.Review {
		t.Fatalf("s1-1 is %s", h.state("s1-1"))
	}
	if n := h.written(sprint.NWorkFailed); n != 1 || len(h.openOf(sprint.NWorkFailed)) != 1 {
		t.Fatalf("the finish applied (s1-1 in review, failed): work came back failed written %d, open %d", n, len(h.openOf(sprint.NWorkFailed)))
	}
	if n := h.written(sprint.NAbandoned); n != 0 {
		t.Fatalf("an operation that applied was released as abandoned (%d)", n)
	}
	if werr != nil {
		t.Fatalf("the worker was told: %v", werr)
	}
	h.machine()
	h.tick(time.Hour)
	h.machine()
	if len(h.openOf(sprint.NWorkFailed)) != 1 {
		t.Fatalf("after an hour of ticks: %v", h.openOf(sprint.NWorkFailed))
	}
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
	h.setup(1)
	h.run(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	h.run(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}, Who: "m1"}))
	c := h.snap().Fleet.Cell("m1", sprint.Working)[0]
	st := *h.st
	st.B = &failAt{Backend: h.m, at: "apply t-fleet"}
	if _, err := st.Run(h.ctx, FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Failed: true, Report: "boom", Who: "m1"})); err == nil || h.m.Pending() == nil {
		t.Fatalf("the writer did not die with its operation pending: %v", err)
	}
	// The member's control card, counted by the finish, moves under it.
	fleet := h.snap().Fleet
	ctl := fleet.Card(sprint.CtlID("m1"))
	if _, err := h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-fleet", Epoch: "0", ExpectedTableRevision: fmt.Sprint(fleet.Revision),
		OperationID: "outside", Members: []ntable.BatchMemberEntry{{ID: ctl.ID, Expect: &ntable.MemberExpect{Revision: fmt.Sprint(ctl.Rev)}, Set: map[string]string{"note": "outside"}}}}); err != nil {
		t.Fatal(err)
	}
	h.tick(2 * time.Minute)
	rr, err := h.st.Repair(h.ctx)
	if err != nil || len(rr) != 1 || rr[0].Done != RepairSkipped || len(rr[0].Skipped) != 1 || !strings.Contains(rr[0].Skipped[0], ctl.ID) {
		t.Fatalf("repair: %+v %v", rr, err)
	}
	if h.state("s1-1") != sprint.Review || h.snap().Fleet.Card(c.ID).Col != sprint.Done {
		t.Fatalf("s1-1 %s, its work card %s", h.state("s1-1"), h.snap().Fleet.Card(c.ID).Col)
	}
	if h.written(sprint.NWorkFailed) != 1 || len(h.skipNotes()) != 1 || h.written(sprint.NAbandoned) != 0 {
		t.Fatalf("work failed %d, skip judgments %d, abandoned %d", h.written(sprint.NWorkFailed), len(h.skipNotes()), h.written(sprint.NAbandoned))
	}
	h.clean("repaired")
}

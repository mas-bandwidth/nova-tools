package store

// Cold reader: the tick's repair (T5) abandons a live, slow writer's operation
// that has in fact applied: its notes are lost and "abandoned" is written.

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// hooked is finished_op_test.go's.

func TestCRTickRepairAbandonsALiveWritersAppliedOperation(t *testing.T) {
	skipUntilEngineRepair(t)
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	h.run(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}, Who: "m1"}))
	c := h.snap().Fleet.Cell("m1", sprint.Working)[0]

	wReleased := make(chan struct{})  // W may release
	bAtReadSet := make(chan struct{}) // B reached its stillExpected
	wApplied := make(chan struct{})   // W's refreshed manifest applied
	stepStarted := false
	W := &hooked{Mem: h.m}
	W.onApply = func(n int) {
		switch n {
		case 1: // W's first send: a mirror write moved the table revision, and W stalls past the grace
			if !stepStarted {
				stepStarted = true
				_ = h.m.RowSet(h.ctx, h.st.Names.Table(sprint.Fleet), "m1", map[string]string{"load": "x"})
				h.tick(2 * time.Minute)
			}
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
	B.onRelease = func(before bool) {
		if !before {
			close(wReleased)
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
	// B ticks once W has stalled with its operation in the fence
	for h.m.Pending() == nil || !stepStarted {
		time.Sleep(time.Millisecond)
	}
	tres, terr := bs.Tick(h.ctx)
	<-done
	t.Logf("worker's finish: moved %v err %v", wres.Moved, werr)
	t.Logf("tick: repaired %+v err %v", tres.Repaired, terr)
	h.clean("after")
	st := h.state("s1-1")
	failed := len(h.openOf(sprint.NWorkFailed))
	abandoned := h.written(sprint.NAbandoned)
	t.Logf("s1-1 is %s; work-failed judgments open %d; abandoned notes %d", st, failed, abandoned)
	if st == sprint.Review && failed == 0 {
		t.Errorf("A+D: the finish applied (s1-1 in review, failed) but its judgment was never written; 'abandoned' written %d", abandoned)
	}
	h.machine()
	h.tick(time.Hour)
	h.machine()
	if len(h.openOf(sprint.NWorkFailed)) == 0 && h.state("s1-1") == sprint.Review {
		t.Errorf("STALL: s1-1 in review, failed, no open judgment, after an hour of ticks")
	}
}

// Within the grace: the tick finishes a live writer's operation for it, and
// the writer is told its operation was cut (exit 2, "run: nova-sprint
// repair") although it applied and was committed.
func TestCRWriterToldCutWhenTheTickFinishedItsOperation(t *testing.T) {
	skipUntilEngineRepair(t)
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
		if n == 1 { // a mirror write moved the table's revision
			_ = h.m.RowSet(h.ctx, h.st.Names.Table(sprint.Fleet), "m1", map[string]string{"load": "x"})
			sent = true
		}
	}
	W.onReadSet = func(n int) {
		if sent { // the writer's stillExpected, after its first send was refused on the revision
			sent = false
			close(wWaiting)
			<-bDone
		}
	}
	ws := &Store{B: W, Names: h.st.Names, Actor: "m1", Now: h.st.Now, NewID: h.st.NewID, Sleep: func(time.Duration) {}}
	var werr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, werr = ws.Run(h.ctx, FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Who: "m1"}))
	}()
	<-wWaiting
	_, terr := h.st.Tick(h.ctx)
	close(bDone)
	<-done
	t.Logf("tick err %v; worker err %v; s1-1 %s; pending %v", terr, werr, h.state("s1-1"), h.m.Pending())
	h.clean("after")
	if werr != nil && h.state("s1-1") == sprint.Review && h.m.Pending() == nil {
		t.Errorf("the worker's finish applied and was committed (s1-1 in review, nothing pending) but the worker was told: %v", werr)
	}
}

// engineRepairFixed says the engine's repair (engine.go) keeps an operation
// that applied instead of abandoning it, and tells a writer whose operation
// the tick finished that it succeeded: the read's findings 3 and 6, fixed in
// the engine by another change. Until that change is merged the tests of
// those findings skip; set it true at integration.
const engineRepairFixed = true

func skipUntilEngineRepair(t *testing.T) {
	t.Helper()
	if !engineRepairFixed {
		t.Skip("needed at integration: the engine's repair (findings 3 and 6, engine.go)")
	}
}

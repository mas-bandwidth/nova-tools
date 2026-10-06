package sprint

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tick gate under load (certification run 37344601736, job tick-gate on
// hetzner: 45 ticks, max 2.032 s, 5 over 1 s at load 2.3 to 5.3; the slowest
// tick's work deal 836 ms). The deal's plan is the store's to run more than
// once a tick (the part's probe, then a plan for every attempt a writer between
// the read and the write costs it), so what one plan reads is paid several
// times on a loaded machine. The plan is bounded by the ready set it reads and
// the cards it deals, never by the history the fleet table keeps: a member's
// deadline is DeadlineK times its median run wall over its last
// DeadlineSamples ok attempts (deadline.go), and a deal that reads the member's
// whole done-ok cell for every card it deals costs dealt x history.
//
// The drive's tick-2 shape (cmd/nova-sprint/dirty_drive_functional_test.go):
// three streams of a thousand ready cards, eight members of width 64, three
// flash routes with a deadline; the fleet's history tickGateHistory ok attempts a
// member, as a long sprint leaves it. The cost is counted, not timed, so it
// holds on a loaded runner as on an idle bench: the deal over the aged
// snapshot, planned tickGatePlans times, measures each member's median run wall
// once for its done-ok cell (the memo's own count, medianWalls.measured),
// never once a card dealt; the plans are run with a CPU-bound sibling
// goroutine on every P, the runner's load as the gate meets it. Each plan's
// wall is in the gate line, read beside the count and never asserted on.
const (
	tickGateStreams   = 3
	tickGatePerStream = 1000
	tickGateMembers   = 8
	tickGateWidth     = 64
	tickGateHistory   = 1000 // ok attempts on the fleet table, each member's
	tickGatePlans     = 3    // plans of the deal each way, as the store runs it more than once a tick
	tickGateRouteSecs = 900  // the flash routes' deadline in seconds
)

func TestTheTickGateHoldsUnderLoad(t *testing.T) {
	t.Parallel()
	var members []string
	for i := 1; i <= tickGateMembers; i++ {
		// The measure count is the process's, a member: names no other test deals to.
		members = append(members, "tick-gate-m"+strconv.Itoa(i))
	}
	w := fleetWorld(t, tickGatePerStream, tickGateWidth, members...)
	for i := 2; i <= tickGateStreams; i++ {
		w.must(Add(w.s, AddReq{Stream: "s" + strconv.Itoa(i), Count: tickGatePerStream}))
	}

	fresh := tickGateSnapshot(w.s, members, 0)
	aged := tickGateSnapshot(w.s, members, tickGateHistory)
	require.Len(t, fresh.Work.Column(Ready), tickGateStreams*tickGatePerStream, "the drive's tick 2: every card ready")

	stop := loadEveryP()
	none, units := tickGateDeals(fresh)
	before := medianMeasures(members)
	some, agedUnits := tickGateDeals(aged)
	after := medianMeasures(members)
	stop()

	require.Equal(t, units, agedUnits, "the history changed what the deal planned")
	require.Positive(t, units, "the deal planned nothing")
	measured := 0
	for i := range members {
		measured += after[i] - before[i]
	}
	line := fmt.Sprintf("TICK GATE UNDER LOAD: %d ready, %d members of width %d, %d siblings busy: %d plans of %d units each in %s with no history, %s with %d ok attempts a member; %d median walls measured (the bound %d, one a member's cell)",
		tickGateStreams*tickGatePerStream, tickGateMembers, tickGateWidth, runtime.GOMAXPROCS(0), tickGatePlans, units, none, some, tickGateHistory, measured, tickGateMembers)
	fmt.Fprintln(os.Stderr, line)
	for i, m := range members {
		assert.LessOrEqual(t, after[i]-before[i], 1, "the deal measured %s's history more than once for one cell: %s", m, line)
	}
}

// medianMeasures is how many times each member's median run wall has been
// measured over a done-ok cell in this process (medianMemo.measured).
func medianMeasures(members []string) []int {
	medianWalls.mu.Lock()
	defer medianWalls.mu.Unlock()
	out := make([]int, len(members))
	for i, m := range members {
		out[i] = medianWalls.measured[m]
	}
	return out
}

// tickGateSnapshot is the snapshot with every waiting card ready, three flash
// routes with a deadline (a dealt card's deadline is the member's, deadline.go),
// and history ok attempts on the fleet table for each member, newest last.
func tickGateSnapshot(snap *Snapshot, members []string, history int) *Snapshot {
	v := *snap
	v.Work = snap.Work.Frozen()
	for _, c := range snap.Work.Column(Waiting) {
		cp := *c
		cp.Col = Ready
		v.Work.Put(&cp)
	}
	v.Routes = nil
	for i, name := range []string{"flash-a", "flash-b", "flash-c"} {
		v.Routes = append(v.Routes, Route{Name: name, Tier: "flash", Provider: "prov" + strconv.Itoa(i), Model: "m" + strconv.Itoa(i), Tokens: 100000, Deadline: tickGateRouteSecs, Enabled: true})
	}
	v.Tiers = map[string][]string{"flash": {"flash-a", "flash-b", "flash-c", "flash-c"}}
	v.Fleet = snap.Fleet.Frozen()
	for _, m := range members {
		for i := range history {
			v.Fleet.Put(&Card{ID: fmt.Sprintf("%s-done-%05d", m, i), Row: m, Col: DoneOK, Score: float64(i), Fields: map[string]string{
				"finished": snap.Now.Add(time.Duration(i-history) * time.Minute).Format(time.RFC3339Nano),
				FieldUsage: fmt.Sprintf("wall=%ds input=1000 output=100", 300+i%200),
			}})
		}
	}
	return &v
}

// tickGateDeals runs the tick's deal on the snapshot tickGatePlans times, and is each
// plan's wall (for the gate line) and the units the last one planned.
func tickGateDeals(s *Snapshot) ([]time.Duration, int) {
	var walls []time.Duration
	units := 0
	for range tickGatePlans {
		began := time.Now()
		p, _ := TickDeal(s, TickReq{})
		walls = append(walls, time.Since(began).Round(time.Millisecond))
		units = len(p.Units)
	}
	return walls, units
}

// loadEveryP starts a CPU-bound goroutine on every P, the runner's load as the
// gate meets it, and returns what stops them.
func loadEveryP() func() {
	var done atomic.Bool
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			x := uint64(1)
			for !done.Load() {
				for range 1 << 12 {
					x = x*6364136223846793005 + 1442695040888963407
				}
			}
			_ = x
		})
	}
	return func() { done.Store(true); wg.Wait() }
}

package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
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
// The drive's tick-2 shape (dirty_drive_functional_test.go): three streams of
// a thousand ready cards, eight members of width 64, three flash routes with a
// deadline; the fleet's history gateHistory ok attempts a member, as a long
// sprint leaves it. The cost is counted, not timed, so it holds on a loaded
// runner as on an idle bench: the deal over the aged snapshot, planned
// gatePlans times, measures each member's median run wall once for its done-ok
// cell (sprint.MedianWallMeasures), never once a card dealt; the plans are run
// with a CPU-bound sibling goroutine on every P, the runner's load as the gate
// meets it. Each plan's wall is in the gate line, read beside the count and
// never asserted on.
const (
	gateStreams   = 3
	gatePerStream = 1000
	gateMembers   = 8
	gateWidth     = 64
	gateHistory   = 1000 // ok attempts on the fleet table, each member's
	gatePlans     = 3    // plans of the deal each way, as the store runs it more than once a tick
	gateRouteSecs = 900  // the flash routes' deadline in seconds
)

func TestTheTickGateHoldsUnderLoad(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	var members, spec []string
	for i := 1; i <= gateMembers; i++ {
		// The measure count is the process's, a member: names no other test deals to.
		m := "tick-gate-m" + strconv.Itoa(i)
		members = append(members, m)
		spec = append(spec, m+":"+strconv.Itoa(gateWidth))
	}
	ta.live = members
	ta.ok("init --readers reader-a,reader-b,reader-c --members " + strings.Join(spec, ","))
	ta.ok("add --stream a,b,c --count " + strconv.Itoa(gatePerStream))
	ta.ok("start")
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	snap, err := st.Load(context.Background(), store.All, nil)
	require.NoError(t, err)

	fresh := gateSnapshot(snap, members, 0)
	aged := gateSnapshot(snap, members, gateHistory)
	require.Len(t, fresh.Work.Column(sprint.Ready), gateStreams*gatePerStream, "the drive's tick 2: every card ready")

	stop := loadEveryP()
	none, units := dealPlans(fresh)
	before := medianMeasures(members)
	some, agedUnits := dealPlans(aged)
	after := medianMeasures(members)
	stop()

	require.Equal(t, units, agedUnits, "the history changed what the deal planned")
	require.Positive(t, units, "the deal planned nothing")
	measured := 0
	for i := range members {
		measured += after[i] - before[i]
	}
	line := fmt.Sprintf("TICK GATE UNDER LOAD: %d ready, %d members of width %d, %d siblings busy: %d plans of %d units each in %s with no history, %s with %d ok attempts a member; %d median walls measured (the bound %d, one a member's cell)",
		gateStreams*gatePerStream, gateMembers, gateWidth, runtime.GOMAXPROCS(0), gatePlans, units, none, some, gateHistory, measured, gateMembers)
	fmt.Fprintln(os.Stderr, line)
	for i, m := range members {
		assert.LessOrEqual(t, after[i]-before[i], 1, "the deal measured %s's history more than once for one cell: %s", m, line)
	}
}

// medianMeasures is how many times each member's median run wall has been measured.
func medianMeasures(members []string) []int {
	out := make([]int, len(members))
	for i, m := range members {
		out[i] = sprint.MedianWallMeasures(m)
	}
	return out
}

// gateSnapshot is the store's snapshot with every waiting card ready, three flash
// routes with a deadline (a dealt card's deadline is the member's, deadline.go),
// and history ok attempts on the fleet table for each member, newest last.
func gateSnapshot(snap *sprint.Snapshot, members []string, history int) *sprint.Snapshot {
	v := *snap
	v.Work = snap.Work.Frozen()
	for _, c := range snap.Work.Column(sprint.Waiting) {
		cp := *c
		cp.Col = sprint.Ready
		v.Work.Put(&cp)
	}
	v.Routes = nil
	for i, name := range []string{"flash-a", "flash-b", "flash-c"} {
		v.Routes = append(v.Routes, sprint.Route{Name: name, Tier: "flash", Provider: "prov" + strconv.Itoa(i), Model: "m" + strconv.Itoa(i), Tokens: 100000, Deadline: gateRouteSecs, Enabled: true})
	}
	v.Tiers = map[string][]string{"flash": {"flash-a", "flash-b", "flash-c", "flash-c"}}
	v.Fleet = snap.Fleet.Frozen()
	for _, m := range members {
		for i := range history {
			v.Fleet.Put(&sprint.Card{ID: fmt.Sprintf("%s-done-%05d", m, i), Row: m, Col: sprint.DoneOK, Score: float64(i), Fields: map[string]string{
				"finished":        snap.Now.Add(time.Duration(i-history) * time.Minute).Format(time.RFC3339Nano),
				sprint.FieldUsage: fmt.Sprintf("wall=%ds input=1000 output=100", 300+i%200),
			}})
		}
	}
	return &v
}

// dealPlans runs the tick's deal on the snapshot gatePlans times, and is each
// plan's wall (for the gate line) and the units the last one planned.
func dealPlans(s *sprint.Snapshot) ([]time.Duration, int) {
	var walls []time.Duration
	units := 0
	for range gatePlans {
		began := time.Now()
		p, _ := sprint.TickDeal(s, sprint.TickReq{})
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

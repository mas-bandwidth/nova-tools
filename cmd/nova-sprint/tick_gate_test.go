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
// sprint leaves it. Measured with a CPU-bound sibling goroutine on every P, so
// the bound holds on a loaded runner and not only on an idle bench: the deal
// with that history costs no more than gateRatio times the deal with none.
const (
	gateStreams   = 3
	gatePerStream = 1000
	gateMembers   = 8
	gateWidth     = 64
	gateHistory   = 1000 // ok attempts on the fleet table, each member's
	gateRatio     = 2    // the deal with that history, over the deal with none
	gateTimings   = 3    // plans timed each way; the fastest is the plan's cost
)

func TestTheTickGateHoldsUnderLoad(t *testing.T) {
	ta := newTestApp(t)
	var members, spec []string
	for i := 1; i <= gateMembers; i++ {
		m := "m" + strconv.Itoa(i)
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
	none, units := dealCost(t, fresh)
	some, agedUnits := dealCost(t, aged)
	stop()

	require.Equal(t, units, agedUnits, "the history changed what the deal planned")
	require.Positive(t, units, "the deal planned nothing")
	line := fmt.Sprintf("TICK GATE UNDER LOAD: %d ready, %d members of width %d, %d siblings busy: the deal planned %d units in %s with no history, %s with %d ok attempts a member (%.1fx, the bound %dx)",
		gateStreams*gatePerStream, gateMembers, gateWidth, runtime.GOMAXPROCS(0), units, none.Round(time.Millisecond), some.Round(time.Millisecond), gateHistory, float64(some)/float64(max(none, 1)), gateRatio)
	fmt.Fprintln(os.Stderr, line)
	assert.LessOrEqual(t, some, gateRatio*none+20*time.Millisecond,
		"the deal reads the fleet's history for every card it deals: %s", line)
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
		v.Routes = append(v.Routes, sprint.Route{Name: name, Tier: "flash", Provider: "prov" + strconv.Itoa(i), Model: "m" + strconv.Itoa(i), Tokens: 100000, Deadline: 900, Enabled: true})
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

// dealCost is the fastest of gateTimings plans of the tick's deal on the snapshot,
// and the units it planned.
func dealCost(t *testing.T, s *sprint.Snapshot) (time.Duration, int) {
	t.Helper()
	best, units := time.Duration(0), 0
	for i := range gateTimings {
		began := time.Now()
		p, _ := sprint.TickDeal(s, sprint.TickReq{})
		took := time.Since(began)
		if i == 0 || took < best {
			best = took
		}
		units = len(p.Units)
	}
	return best, units
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

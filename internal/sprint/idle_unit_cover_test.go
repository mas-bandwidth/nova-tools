package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSprintIdleCoverRank tests the rank function ordering.
func TestSprintIdleCoverRank(t *testing.T) {
	t.Parallel()

	require.Equal(t, 0, rank(rootDropped))
	require.Equal(t, 1, rank(rootMissing))
	require.Equal(t, 2, rank(rootJudgment))
	require.Equal(t, 3, rank(rootSentinel))
	require.Equal(t, 4, rank(rootHeld))
	require.Equal(t, 5, rank(rootStopped))
	require.Equal(t, 6, rank(rootNoRoute))
	require.Equal(t, 7, rank(rootFlight))
	require.Equal(t, 8, rank(rootNext))
	require.Equal(t, -1, rank("unknown"))
}

// TestSprintIdleCoverTraceIdleNoCards tests TraceIdle with no waiting cards.
func TestSprintIdleCoverTraceIdleNoCards(t *testing.T) {
	t.Parallel()

	w := newWorld(t, "reader-a")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))

	got := TraceIdle(w.s, TickReq{})
	require.Equal(t, "", got)
}

// TestSprintIdleCoverTickIdleNoAlarm tests TickIdle with IdleAlarm false.
func TestSprintIdleCoverTickIdleNoAlarm(t *testing.T) {
	t.Parallel()

	w := newWorld(t, "reader-a")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(Add(w.s, AddReq{Stream: "s1", Count: 1}))

	plan, _ := TickIdle(w.s, TickReq{IdleAlarm: false})
	require.Empty(t, plan.Units)
}

// TestSprintIdleCoverTickIdleNilFleet tests TickIdle with nil fleet table.
func TestSprintIdleCoverTickIdleNilFleet(t *testing.T) {
	t.Parallel()

	w := newWorld(t, "reader-a")
	w.s.Fleet = nil

	plan, _ := TickIdle(w.s, TickReq{IdleAlarm: true})
	require.Empty(t, plan.Units)
}

// TestSprintIdleCoverTickIdleAfterSaidEpisode tests TickIdle after a said episode.
func TestSprintIdleCoverTickIdleAfterSaidEpisode(t *testing.T) {
	t.Parallel()

	w := newWorld(t, "reader-a")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(Add(w.s, AddReq{Stream: "s1", Count: 1}))
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: gensOf(w.s, "s1-1.w1")}))

	// Set up a said episode
	w.s.Fleet.props = map[string]string{
		"idle_since": "2030-01-02T03:04:05Z",
		"idle_said":  "2030-01-02T03:10:05Z",
	}

	// Advance and work at half width again
	w.s.Fleet.Card("s1-1.w1").Col = Working
	w.tick(10 * time.Minute)

	plan, _ := TickIdle(w.s, TickReq{IdleAlarm: true})
	require.Len(t, plan.Props, 2)
	require.NotEmpty(t, plan.Units[0].Notes)
	require.Contains(t, plan.Units[0].Notes[0].What, "working again")
}

// TestSprintIdleCoverTickIdleAfterUnsaidEpisode tests TickIdle after an unsaid episode.
func TestSprintIdleCoverTickIdleAfterUnsaidEpisode(t *testing.T) {
	t.Parallel()

	w := newWorld(t, "reader-a")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(Add(w.s, AddReq{Stream: "s1", Count: 1}))
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: gensOf(w.s, "s1-1.w1")}))

	// Set up an unsaid episode
	w.s.Fleet.props = map[string]string{"idle_since": "2030-01-02T03:04:05Z"}

	// Advance and work at half width again
	w.s.Fleet.Card("s1-1.w1").Col = Working
	w.tick(10 * time.Minute)

	plan, _ := TickIdle(w.s, TickReq{IdleAlarm: true})
	require.Len(t, plan.Props, 2)
	require.Empty(t, plan.Units[0].Notes)
}

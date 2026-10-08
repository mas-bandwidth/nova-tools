package main

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// holderRig is a lander over the in-memory store at the test's clock, with two benches
// (m1, m2) and the default width of one Go lane a machine: no git, no bench run.
func holderRig(t *testing.T) (*testApp, *lander) {
	t.Helper()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1,m2")
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	return ta, &lander{a: ta.a, st: st}
}

// stageOf waits, without a clock, until the flight's stage is step, and returns its proc.
func stageOf(f *landFlight, step string) string {
	for {
		f.mu.Lock()
		s, p := f.step, f.proc
		f.mu.Unlock()
		if s == step {
			return p
		}
		runtime.Gosched()
	}
}

// Each fork of the parallel pass holds a bench's Go lane under its own name
// (lander/<stream>), the base re-check under lander/base (docs/SPEC-SPRINT.md section 7):
// a sibling asking a bench another fork holds is refused there and steps to the next slot,
// one fork's give never frees a sibling's bench or its place, and lane list names the
// stream holding.
func TestEachStreamsGateHoldsABenchLaneUnderItsOwnName(t *testing.T) {
	t.Parallel()
	t.Run("two forks on one slot: the second is refused and steps to the next bench", func(t *testing.T) {
		t.Parallel()
		ta, l := holderRig(t)
		ctx := context.Background()
		ring := []string{"m1", "m2"} // both forks start at the same slot, m1
		s1, s2 := l.fork("s1"), l.fork("s2")
		assert.Equal(t, "lander/s1", s1.laneWho())
		assert.Equal(t, "lander/s2", s2.laneWho())

		h1, err := s1.takeGateLane(ctx, ring)
		require.NoError(t, err)
		assert.Equal(t, "m1", h1, "the first fork takes its slot")
		h2, err := s2.takeGateLane(ctx, ring)
		require.NoError(t, err)
		assert.Equal(t, "m2", h2, "the sibling's held lane is held to the second fork: it steps to the next bench")

		lanes := ta.ok("lane list")
		assert.Contains(t, lanes, "LANE go machine=m1 held=lander/s1 width=1 waiting=-", "a lane list line shows the stream holding: %s", lanes)
		assert.Contains(t, lanes, "LANE go machine=m2 held=lander/s2 width=1 waiting=-", "the second fork's place on m1 was given back: %s", lanes)

		s1.giveGateLane(h1)
		lanes = ta.ok("lane list")
		assert.NotContains(t, lanes, "machine=m1", "the first fork's give frees its own bench: %s", lanes)
		assert.Contains(t, lanes, "machine=m2 held=lander/s2", "and never the sibling's: %s", lanes)
		s2.giveGateLane(h2)
		assert.NotContains(t, ta.ok("lane list"), "lander", "both gave back")
	})
	t.Run("the first's give leaves the second's wait in place, and the stage names the stream", func(t *testing.T) {
		t.Parallel()
		ta, l := holderRig(t)
		ta.ok("add --stream s2 --count 1 --one")
		ctx := context.Background()
		b := ta.a.landState()
		f := &landFlight{queued: 1, step: "land", proc: "nova-sprint land", stream: "s2", coord: "coordinator", began: ta.a.now()}
		b.mu.Lock()
		b.flight = f
		b.mu.Unlock()
		s1, s2 := l.fork("s1"), l.fork("s2")
		h1, err := s1.takeGateLane(ctx, []string{"m1"})
		require.NoError(t, err)
		require.Equal(t, "m1", h1)

		got := make(chan string, 1)
		go func() {
			h, err := s2.takeGateLane(ctx, []string{"m1"}) // the one bench is the sibling's: s2 waits
			if err != nil {
				h = "error: " + err.Error()
			}
			got <- h
		}()
		proc := stageOf(f, "lane")
		assert.Equal(t, "lane take go --machine m1 --as lander/s2 (the gate of stream s2)", proc, "the stage names the stream whose gate waits")
		lanes := ta.ok("lane list")
		assert.Contains(t, lanes, "LANE go machine=m1 held=lander/s1 width=1 waiting=lander/s2", "the sibling is queued, not granted by the holder's name: %s", lanes)

		s1.giveGateLane("m1")
		lanes = ta.ok("lane list")
		assert.Contains(t, lanes, "LANE go machine=m1 held=lander/s2 width=1 waiting=-", "the first's give left the second's wait, and granted it: %s", lanes)
		ta.a.landPulse()
		assert.Equal(t, "m1", <-got, "the second fork claims the grant on the next cycle")

		f.mu.Lock()
		f.began = ta.a.now().Add(-LandDeadline - time.Minute)
		f.mu.Unlock()
		ta.a.raiseIfStuck(ctx, "mem:0", f)
		f.mu.Lock()
		judged := f.judged
		f.mu.Unlock()
		require.True(t, judged, "the stuck judgment was written")
		inbox := ta.ok("inbox")
		assert.Contains(t, inbox, "landing stuck at step=lane waiting on lane take go --machine m1 --as lander/s2 (the gate of stream s2)", "the stuck judgment names the stream: %s", inbox)
		s2.giveGateLane("m1")
	})
	t.Run("the base re-check holds its own name", func(t *testing.T) {
		t.Parallel()
		ta, l := holderRig(t)
		ctx := context.Background()
		base := l.fork("s1") // a lander the pass made; baseRecheck runs on the pass's own
		base.gatesBase("main")
		assert.Equal(t, landLaneBase, base.laneWho())
		assert.Equal(t, "the gate of the base main", base.gateWhose())
		hb, err := base.takeGateLane(ctx, []string{"m1", "m2"})
		require.NoError(t, err)
		assert.Equal(t, "m1", hb)
		h1, err := l.fork("s1").takeGateLane(ctx, []string{"m1", "m2"})
		require.NoError(t, err)
		assert.Equal(t, "m2", h1, "a stream's fork is not the base re-check's holder")
		lanes := ta.ok("lane list")
		assert.Contains(t, lanes, "machine=m1 held=lander/base width=1", lanes)
		assert.Contains(t, lanes, "machine=m2 held=lander/s1 width=1", lanes)
	})
	t.Run("a give takes back only the named holder's place", func(t *testing.T) {
		t.Parallel()
		now := t0
		ls := sprint.Lanes{}
		ls, a := ls.Take("m1", "lander/s1", 2, now)
		require.True(t, a.Granted)
		ls, a = ls.Take("m1", "lander/s2", 2, now)
		require.True(t, a.Granted, "a width of two grants the sibling its own lane")
		ls, a = ls.Take("m1", "lander/s3", 2, now)
		require.False(t, a.Granted)
		assert.Equal(t, 1, a.Place)

		ls, a = ls.Give("m1", "lander/s1", 2, now.Add(time.Second))
		assert.True(t, a.Gave)
		rows := ls.Rows(sprint.LaneGo, 2, now.Add(time.Second))
		require.Len(t, rows, 1)
		assert.Equal(t, []string{"lander/s2", "lander/s3"}, rows[0].Held, "s2 keeps its lane, s3's wait is granted")

		ls, a = ls.Give("m1", "lander", 2, now.Add(2*time.Second))
		assert.False(t, a.Gave, "the bare lander name gives back no fork's lane")
		rows = ls.Rows(sprint.LaneGo, 2, now.Add(2*time.Second))
		require.Len(t, rows, 1)
		assert.Equal(t, []string{"lander/s2", "lander/s3"}, rows[0].Held)

		assert.True(t, sprint.ValidLaneWho("lander/s1"))
		assert.True(t, sprint.ValidLaneWho("worker-1"))
		assert.False(t, sprint.ValidLaneWho("lander/s1/x"), "one / at most")
		assert.False(t, sprint.ValidLaneWho("lander/"), "both parts named")
		assert.False(t, sprint.ValidLaneWho("/s1"), "both parts named")
	})
}

package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func workLanes(n int) Lanes {
	var l Lanes
	for i := 0; i < n; i++ {
		l = l.Add(Lane{ID: fmt.Sprintf("w%d", i), Kind: KindWork})
	}
	return l
}

func addReads(l Lanes, n int) Lanes {
	for i := 0; i < n; i++ {
		l = l.Add(Lane{ID: fmt.Sprintf("r%d", i), Kind: KindRead})
	}
	return l
}

func queueIDs(prefix string, n int, kind string) []Card {
	out := make([]Card, n)
	for i := 0; i < n; i++ {
		out[i] = Card{ID: fmt.Sprintf("%s%d", prefix, i), Kind: kind}
	}
	return out
}

func startedIDs(cards []Card) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.ID
	}
	return out
}

// Width 16 with 10 busy (8 work + 4 reads) has 12 half-slots free: the next 6
// work cards, or the next 12 reads, in the queue's order.
func TestWidth16With10BusyFillsSixWorkOrTwelveReads(t *testing.T) {
	t.Parallel()
	lanes := addReads(workLanes(8), 4)
	require.Equal(t, 20, lanes.Busy(), "8 work + 4 reads = 10 slots")

	t.Run("work", func(t *testing.T) {
		t.Parallel()
		step, _ := Next(World{Lanes: lanes, Width: 16, Queue: queueIDs("q", 10, KindWork), BinaryVersion: "1", RunnerVersion: "1"})
		require.Equal(t, []string{"q0", "q1", "q2", "q3", "q4", "q5"}, startedIDs(step.Start))
		require.False(t, step.Drain)
		require.Empty(t, step.Exec)
		require.Equal(t, 32, step.Lanes.Busy())
	})

	t.Run("reads", func(t *testing.T) {
		t.Parallel()
		step, _ := Next(World{Lanes: lanes, Width: 16, Queue: queueIDs("d", 20, KindRead), BinaryVersion: "1", RunnerVersion: "1"})
		require.Len(t, step.Start, 12)
		require.Equal(t, startedIDs(queueIDs("d", 12, KindRead)), startedIDs(step.Start))
		for _, c := range step.Start {
			require.Equal(t, KindRead, c.Kind)
		}
		require.Equal(t, 32, step.Lanes.Busy())
	})

	t.Run("queue order", func(t *testing.T) {
		t.Parallel()
		// room is 12 half-slots. The queue is read, work, read, work, ...
		var q []Card
		for i := 0; i < 6; i++ {
			q = append(q, Card{ID: fmt.Sprintf("a%d", i), Kind: KindRead}, Card{ID: fmt.Sprintf("b%d", i), Kind: KindWork})
		}
		step, _ := Next(World{Lanes: lanes, Width: 16, Queue: q, BinaryVersion: "1", RunnerVersion: "1"})
		// 1+2 four times is 12, so four reads and the four works between them.
		require.Equal(t, []string{"a0", "b0", "a1", "b1", "a2", "b2", "a3", "b3"}, startedIDs(step.Start))
	})
}

// A width lowered under the lanes that are already busy kills nothing and
// starts nothing until the busy slots are under the new width.
func TestWidthLoweredDoesNotKillLanes(t *testing.T) {
	t.Parallel()
	lanes := workLanes(12)
	require.Equal(t, 24, lanes.Busy(), "12 work lanes are 12 slots")
	q := queueIDs("n", 4, KindWork)

	step, _ := Next(World{Lanes: lanes, Width: 8, Queue: q, BinaryVersion: "1", RunnerVersion: "1"})
	require.Equal(t, lanes.IDs(), step.Lanes.IDs())
	require.Empty(t, step.Start)
	require.Empty(t, step.Fail)
	require.Nil(t, step.Judgment)
	require.False(t, step.Drain)
	require.Empty(t, step.Exec)

	// one lane finished on its own; the other eleven stay, and 11 is still not under 8
	step, _ = Next(World{Lanes: lanes, Width: 8, Queue: q, Ended: []End{{ID: "w0", HasReport: true, Verdict: "LAND", Head: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}, BinaryVersion: "1", RunnerVersion: "1"})
	require.Equal(t, lanes.Without("w0").IDs(), step.Lanes.IDs())
	require.Empty(t, step.Start)

	// busy == width starts nothing; busy under it starts one
	at := workLanes(8)
	step, _ = Next(World{Lanes: at, Width: 8, Queue: q, BinaryVersion: "1", RunnerVersion: "1"})
	require.Empty(t, step.Start)
	under := workLanes(7)
	step, _ = Next(World{Lanes: under, Width: 8, Queue: q, BinaryVersion: "1", RunnerVersion: "1"})
	require.Equal(t, []string{"n0"}, startedIDs(step.Start))
}

// A lane with no report finishes FAIL with the harness-fault line. Three of
// the same line in a row raise one judgment, and a fourth does not raise another.
func TestNoReportFailsWithHarnessFaultAndThreeAlikeRaiseOneJudgment(t *testing.T) {
	t.Parallel()
	line := "opencode: boom"
	want := "harness fault: opencode: boom"
	lanes := workLanes(3)
	ends := []End{{ID: "w0", ErrLine: line}, {ID: "w1", ErrLine: line + "\nignored"}, {ID: "w2", ErrLine: "  " + line}}

	step, mem := Next(World{Friend: "ada", Lanes: lanes, Width: 16, Ended: ends[:2], BinaryVersion: "1", RunnerVersion: "1"})
	require.Equal(t, []Fail{{ID: "w0", Report: want}, {ID: "w1", Report: want}}, step.Fail)
	require.Nil(t, step.Judgment)
	require.Equal(t, 2, mem.FaultCount)

	step, mem = Next(World{Friend: "ada", Lanes: step.Lanes, Width: 16, Ended: ends[2:], Fault: mem.Fault, FaultCount: mem.FaultCount, Judged: mem.Judged, BinaryVersion: "1", RunnerVersion: "1"})
	require.Equal(t, []Fail{{ID: "w2", Report: want}}, step.Fail)
	require.NotNil(t, step.Judgment)
	require.Equal(t, want, step.Judgment.Fault)
	require.Contains(t, step.Judgment.Subject, "ada")
	require.Contains(t, step.Judgment.Body, want)
	require.True(t, mem.Judged)

	step, mem = Next(World{Friend: "ada", Lanes: Lanes{}.Add(Lane{ID: "w3", Kind: KindWork}), Width: 16, Ended: []End{{ID: "w3", ErrLine: line}}, Fault: mem.Fault, FaultCount: mem.FaultCount, Judged: mem.Judged, BinaryVersion: "1", RunnerVersion: "1"})
	require.Equal(t, want, step.Fail[0].Report)
	require.Nil(t, step.Judgment, "the same streak raises one judgment")

	// a different line starts a new streak, and a report between two faults breaks the row
	step, mem = Next(World{Friend: "ada", Lanes: workLanes(3), Width: 16, Ended: []End{{ID: "w0", ErrLine: "other"}, {ID: "w1", HasReport: true, Verdict: "LAND", Head: "abc"}, {ID: "w2", ErrLine: "other"}}, BinaryVersion: "1", RunnerVersion: "1"})
	require.Len(t, step.Fail, 2)
	require.Nil(t, step.Judgment)
	require.Equal(t, 1, mem.FaultCount)
	require.False(t, mem.Judged)
}

// A runner_version the binary is not drains: no new lane, nothing killed, and
// the exec happens only once the running lanes have ended.
func TestRunnerVersionChangeDrainsThenExecs(t *testing.T) {
	t.Parallel()
	lanes := workLanes(2)
	q := queueIDs("q", 4, KindWork)

	step, _ := Next(World{Lanes: lanes, Width: 16, Queue: q, BinaryVersion: "1", RunnerVersion: "2"})
	require.True(t, step.Drain)
	require.Empty(t, step.Exec)
	require.Empty(t, step.Start)
	require.Equal(t, lanes.IDs(), step.Lanes.IDs())

	step, _ = Next(World{Lanes: lanes, Width: 16, Queue: q, Ended: []End{{ID: "w0", HasReport: true, Verdict: "LAND", Head: "abc"}}, BinaryVersion: "1", RunnerVersion: "2"})
	require.True(t, step.Drain)
	require.Empty(t, step.Exec)
	require.Equal(t, []string{"w1"}, step.Lanes.IDs())

	step, _ = Next(World{Lanes: step.Lanes, Width: 16, Queue: q, Ended: []End{{ID: "w1", HasReport: true, Verdict: "HOLD", Head: "none"}}, BinaryVersion: "1", RunnerVersion: "2"})
	require.True(t, step.Drain)
	require.Equal(t, "2", step.Exec)
	require.Empty(t, step.Lanes.IDs())
	require.Empty(t, step.Start)

	same, _ := Next(World{Lanes: lanes, Width: 16, Queue: q, BinaryVersion: "1", RunnerVersion: "1"})
	require.False(t, same.Drain)
	require.NotEmpty(t, same.Start)
	none, _ := Next(World{Width: 16, Queue: q, BinaryVersion: "dev", RunnerVersion: ""})
	require.False(t, none.Drain)
	require.NotEmpty(t, none.Start)
	dash, _ := Next(World{Width: 16, Queue: q, BinaryVersion: "dev", RunnerVersion: "-"})
	require.False(t, dash.Drain)
}

func TestRowDueUsesTheClockItIsGiven(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 10, 7, 16, 0, 0, 0, time.UTC)
	require.True(t, RowDue(time.Time{}, t0))
	require.False(t, RowDue(t0, t0.Add(9*time.Second)))
	require.True(t, RowDue(t0, t0.Add(RowEvery)))
}

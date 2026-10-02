package refmodel_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
)

// The fixtures are built the way a sprint is: with the steps, on a world, so
// every card is in a place its lifecycle allows and carries the fields the
// planners read.

// coordinator and merger are who the fixtures act as.
const (
	coordinator = "coordinator"
	merger      = "merger"
)

// sprintOf is a world with three readers, the members named brought up, and
// no card.
func sprintOf(t *testing.T, members ...string) *world {
	t.Helper()
	w := newWorld("reader-a", "reader-b", "reader-c")
	for _, m := range members {
		w.must(t, sprint.FleetStep(w.s, sprint.FleetReq{Op: "up", Member: m, Who: coordinator}))
	}
	return w
}

// add admits n primaries into the stream, s1-1 and so on, needing nothing.
func (w *world) add(t *testing.T, stream string, n int, needs ...string) {
	t.Helper()
	w.must(t, sprint.Add(w.s, sprint.AddReq{Stream: stream, Count: n, Needs: needs, Who: coordinator}))
}

// addOne admits one named primary, with needs.
func (w *world) addOne(t *testing.T, stream, id string, needs ...string) {
	t.Helper()
	w.must(t, sprint.Add(w.s, sprint.AddReq{Stream: stream, IDs: []string{id}, Needs: needs, Who: coordinator}))
}

// sentinel admits one named sentinel into the stream.
func (w *world) sentinel(t *testing.T, stream, id string) {
	t.Helper()
	w.must(t, sprint.Add(w.s, sprint.AddReq{Stream: stream, IDs: []string{id}, Sentinel: true, Who: coordinator}))
}

// deal deals the primary to a member.
func (w *world) deal(t *testing.T, id string) {
	t.Helper()
	w.must(t, sprint.Deal(w.s, sprint.DealReq{Sel: sprint.Sel{IDs: []string{id}}, Who: sprint.MachineActor}))
}

// take has the member of the primary's live work card take it.
func (w *world) take(t *testing.T, id string) {
	t.Helper()
	wc := w.s.Work.Card(id).F("work")
	w.must(t, sprint.Take(w.s, sprint.TakeReq{As: w.s.Fleet.Card(wc).Row, Sel: sprint.Sel{IDs: []string{wc}}, Gens: map[string]int{wc: w.gen(wc)}, Who: "worker"}))
}

// finish has the member finish the primary's live work card, ok or failed.
func (w *world) finish(t *testing.T, id string, failed bool) {
	t.Helper()
	wc := w.s.Work.Card(id).F("work")
	w.must(t, sprint.Finish(w.s, sprint.FinishReq{As: w.s.Fleet.Card(wc).Row, Sel: sprint.Sel{IDs: []string{wc}}, Gens: map[string]int{wc: w.gen(wc)},
		Failed: failed, Head: "h-" + id, Report: "boom", Who: "worker"}))
}

// ask asks the readers of the primary.
func (w *world) ask(t *testing.T, id string) {
	t.Helper()
	w.must(t, sprint.Ask(w.s, sprint.AskReq{Sel: sprint.Sel{IDs: []string{id}}, Who: sprint.MachineActor}))
}

// report has every reader asked of the primary say the verdict.
func (w *world) report(t *testing.T, id, verdict string) {
	t.Helper()
	for _, rc := range w.s.Readers.Of(id) {
		w.must(t, sprint.Read(w.s, sprint.ReadReq{As: rc.Row, Verdict: verdict, Finding: "f", Sel: sprint.Sel{IDs: []string{rc.ID}}, Who: rc.Row}))
	}
}

// accept accepts the primary.
func (w *world) accept(t *testing.T, id string) {
	t.Helper()
	w.must(t, sprint.Accept(w.s, sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{id}}, Who: coordinator}))
}

// merge merges the head of the stream's queue.
func (w *world) merge(t *testing.T, stream string) {
	t.Helper()
	w.must(t, sprint.MergeStep(w.s, sprint.MergeReq{Stream: stream, Batch: 10, Who: merger}))
}

// drive takes a ready primary up through its lifecycle as far as the state
// named: working (dealt and taken), review (finished ok), merging (asked,
// read ok and accepted), landed (merged).
func (w *world) drive(t *testing.T, id string, to sprint.State) {
	t.Helper()
	w.deal(t, id)
	w.take(t, id)
	if to == sprint.Working {
		return
	}
	w.finish(t, id, false)
	if to == sprint.Review {
		return
	}
	w.ask(t, id)
	w.report(t, id, "ok")
	w.accept(t, id)
	if to == sprint.Merging {
		return
	}
	w.merge(t, w.s.Work.Card(id).Row)
}

// snapshot is the sprint as a tick reads it: the machine RUNNING since t0,
// every member's last beat as given (a member not named never beat).
func (w *world) snapshot(beats map[string]time.Duration) refmodel.Snapshot {
	bs := map[string]sprint.Beat{}
	for m, ago := range beats {
		bs[m] = sprint.Beat{At: t0.Add(-ago)}
	}
	return refmodel.Snapshot{Tables: w.s, Running: true, Since: t0.Add(-time.Hour), Beats: bs}
}

// fresh is every member of the fleet beating now.
func (w *world) fresh() map[string]time.Duration {
	out := map[string]time.Duration{}
	for _, m := range w.s.Fleet.Rows() {
		out[m] = 0
	}
	return out
}

// applyPart runs a part of the tick on the sprint with the clock after t0, and
// applies what it plans, as the store does: the judgments it raises are open
// afterwards, and what it moves has moved.
func (w *world) applyPart(t *testing.T, part string, after time.Duration) {
	t.Helper()
	for _, p := range sprint.TickParts {
		if p.Name == part {
			w.s.Now = later(after)
			plan, _ := p.Fn(w.s, sprint.TickReq{Who: sprint.MachineActor})
			w.must(t, sprint.Applied(w.s, plan))
			return
		}
	}
	t.Fatalf("the tick has no part %s", part)
}

// later is the sprint with the clock d on: a duty decides at that time.
func later(d time.Duration) time.Time { return t0.Add(d) }

// lines is the moves as their canonical lines, for a failure message.
func lines(ms []refmodel.Move) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.String()
	}
	return out
}

// show is the moves, one to a line.
func show(ms []refmodel.Move) string {
	s := ""
	for _, l := range lines(ms) {
		s += "\n  " + l
	}
	if s == "" {
		return " none"
	}
	return s
}

// land puts a merging primary in landed without the step that lands it, which
// would also move what waited on it: those are left for the tick to move.
func (w *world) land(t *testing.T, id string) {
	t.Helper()
	c := w.s.Work.Card(id)
	require.NotNil(t, c, "land %s: it is not merging", id)
	require.Equal(t, string(sprint.Merging), c.Col, "land %s: it is not merging", id)
	c.Col = sprint.Landed
	c.Rev++
	w.s.Work.Put(c)
}

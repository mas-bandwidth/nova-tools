package store

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A coordinator who is silent is visible: every judgment passes its due time
// in running time and the tick marks it overdue, once.

// overdueLines counts the tick's overdue lines by the judgment they name.
func (h *harness) overdueLines() map[string]int {
	h.t.Helper()
	notes, _, err := h.m.NotesSince(h.ctx, "", 1000000)
	require.NoError(h.t, err)
	out := map[string]int{}
	for _, n := range notes {
		if n.Kind == sprint.Happened && n.Type == sprint.NOverdue {
			id, _, _ := strings.Cut(n.What, " ")
			out[id]++
		}
	}
	return out
}

func TestASilentCoordinatorIsVisible(t *testing.T) {
	t.Parallel()
	for seed := uint64(1); seed <= 1; seed++ {
		w := crSprint(t, seed)
		h := w.h
		at := 3 + w.rng.IntN(10)
		w.silentCoord = at
		w.running = true
		h.startMachine()
		for r := 1; r <= at+crScale.SilentRounds; r++ { // rounds five minutes apart: past every due time
			w.round(r)
			if r < at {
				h.tick(time.Second)
			} else {
				h.tick(5 * time.Minute)
			}
		}
		// the work of the last round reaches review a tick later (the pump applies
		// its queue), so its judgments are a round younger than the rest: one
		// more quiet round, then the tick that marks them.
		h.machine()
		h.tick(5 * time.Minute)
		h.machine()
		open, err := h.m.OpenNotes(h.ctx)
		require.NoError(t, err)
		judgments, holds := sprint.SplitOpen(open)
		marked := map[string]bool{} // judgment id | subject
		for _, o := range holds {
			if o.Note.Type == sprint.NOverdue {
				marked[sprint.OpenKey(o.Note.What, o.Subject())] = true
			}
		}
		lines := h.overdueLines()
		m, _, _ := h.st.Machine(h.ctx)
		// named is the subjects of open judgments past their due time that
		// the tick marked overdue with one line.
		named := map[string]bool{}
		for _, o := range judgments {
			n := o.Note
			running := h.now.Sub(n.At) - m.StoppedBetween(n.At, h.now)
			if running <= sprint.DeadlineJudgment || !marked[sprint.OpenKey(n.ID, o.Subject())] {
				continue
			}
			assert.Equal(t, 1, lines[n.ID], "seed %d: judgment %s (%s) has %d overdue lines, want 1", seed, n.ID, n.Type, lines[n.ID])
			named[o.Subject()] = true
		}
		s := h.snap()
		needs := 0
		for _, st := range []string{"s1", "s2", "s3"} {
			for _, c := range s.Work.Cell(st, sprint.Review) {
				outstanding := false
				for _, rc := range readsAt(s, c) {
					outstanding = outstanding || rc.Col == sprint.Asked || rc.Col == sprint.Reading
				}
				if outstanding {
					continue
				}
				needs++
				assert.True(t, named[c.ID], "seed %d: %s in review waits on the coordinator, and no open overdue judgment names it", seed, c.ID)
			}
			for _, c := range s.Merge.Cell(st, sprint.Stuck) {
				needs++
				assert.True(t, named[sprint.StreamSubject(st)], "seed %d: %s is stuck in a stopped stream, and no open overdue judgment names the stream", seed, c.ID)
			}
			for _, c := range s.Work.Cell(st, sprint.Waiting) {
				if !named[c.ID] && len(sprint.Split(c.F("needs"))) > 0 {
					for _, n := range sprint.Split(c.F("needs")) {
						if nc := s.Work.Card(n); nc != nil && !nc.Placed() {
							assert.NotEqual(t, "dropped", nc.F("outcome"), "seed %d: %s is blocked on %s, dropped, and no open overdue judgment names it", seed, c.ID, n)
						}
					}
				}
			}
		}
		assert.NotEqual(t, 0, needs, "seed %d: nothing waits on the coordinator after five silent hours: the scene proves nothing", seed)
		// A second tick marks nothing again.
		h.tick(time.Minute)
		h.machine()
		for id, n := range h.overdueLines() {
			if lines[id] > 0 {
				assert.Equal(t, lines[id], n, "seed %d: judgment %s marked overdue again (%d lines)", seed, id, n)
			}
		}
		h.clean("silent coordinator")
	}
}

// A wait moves a judgment's due time on: the tick lifts the mark, and marks
// it again, once, when the review time passes; a judgment closed takes its
// mark with it.
func TestAnOverdueMarkFollowsTheDueTime(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	h.run(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}, Who: "m1"}))
	c := h.snap().Fleet.Cell("m1", sprint.Working)[0]
	h.must(FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Failed: true}))
	h.machine()
	o := h.openOf(sprint.NWorkFailed)
	require.Len(t, o, 1, "work failed: %d", len(o))
	id := o[0].Note.ID
	// Stopped time does not count.
	h.stopMachine()
	h.tick(time.Hour)
	h.startMachine()
	h.machine()
	n := h.overdueLines()[id]
	require.Equal(t, 0, n, "marked overdue from stopped time: %d", n)
	h.tick(sprint.DeadlineJudgment + time.Second)
	h.machine()
	h.tick(time.Minute + time.Second)
	h.machine()
	n = h.overdueLines()[id]
	require.Equal(t, 1, n, "overdue lines %d, want 1", n)
	require.NoError(t, h.m.SetReview(h.ctx, id, h.now.Add(time.Hour), h.now))
	h.tick(time.Minute + time.Second)
	h.machine()
	n = len(h.openOf(sprint.NOverdue))
	require.Equal(t, 0, n, "the mark outlived the wait: %d", n)
	h.tick(time.Hour)
	h.machine()
	n = h.overdueLines()[id]
	require.Equal(t, 2, n, "overdue lines after the review time passed: %d, want 2", n)
	h.run(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "fix"}))
	h.tick(time.Second)
	h.machine()
	n = len(h.openOf(sprint.NOverdue))
	require.Equal(t, 0, n, "the mark outlived its judgment: %d", n)
	h.clean("marks")
}

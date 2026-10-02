package store

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// The tick-end note (errata 3 amendment 8): a tick that addressed the
// coordinator nothing writes none; the tick that says the sprint is done
// writes one, addressed to the coordinator, counting every note for the
// coordinator since the last tick end (the done: a RUNNING machine's reads
// open no "ready to accept", the pump accepts), and a wait from before it wakes on it;
// the inbox does not list it; a start of the done sprint says done again and
// wakes once more; a wait from the tail runs out its time.
func TestTheTickEndWakesTheCoordinatorOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.startMachine()
	res := h.machine()
	require.Equal(t, 0, res.TickEnd, "a tick of an empty sprint addressed the coordinator nothing and wrote a tick end: %+v", res)
	require.Equal(t, 0, h.written(sprint.NTickEnd), "a tick of an empty sprint addressed the coordinator nothing and wrote a tick end: %+v", res)
	h.setup(2)
	_, from, err := h.m.Tails(h.ctx)
	require.NoError(t, err)
	h.landThrough("s1", "s1-1", "s1-2")
	res = h.machine()
	require.NotEmpty(t, res.Done, "the done tick: done %q, tick end %d, written %d", res.Done, res.TickEnd, h.written(sprint.NTickEnd))
	require.Equal(t, 1, res.TickEnd, "the done tick: done %q, tick end %d, written %d", res.Done, res.TickEnd, h.written(sprint.NTickEnd))
	require.Equal(t, 1, h.written(sprint.NTickEnd), "the done tick: done %q, tick end %d, written %d", res.Done, res.TickEnd, h.written(sprint.NTickEnd))
	notes, _, err := h.m.NotesSince(h.ctx, from, 1000)
	require.NoError(t, err)
	last := notes[len(notes)-1]
	require.Equal(t, sprint.NTickEnd, last.Type, "the tick end: %+v", last)
	require.Equal(t, sprint.Happened, last.Kind, "the tick end: %+v", last)
	require.Equal(t, h.st.Actor, last.To, "the tick end: %+v", last)
	require.Equal(t, "judgments=1", last.What, "the tick end: %+v", last)
	woke, err := h.st.WaitTickEnd(h.ctx, from, time.Minute)
	require.NoError(t, err, "a wait from before the done tick: %v %v", woke, err)
	require.True(t, woke, "a wait from before the done tick: %v %v", woke, err)
	v, err := h.st.Inbox(h.ctx, time.Hour, time.Hour, 100)
	require.NoError(t, err)
	for _, g := range v.Groups {
		require.NotEqual(t, string(sprint.NTickEnd), g.Type, "the inbox lists the tick end: %+v", g)
	}
	// the machine stopped: a start and a tick with nothing addressed since
	h.startMachine()
	res = h.machine()
	require.Equal(t, 1, res.TickEnd, "the tick after a start of a done sprint: %+v, %d tick ends", res, h.written(sprint.NTickEnd))
	require.Equal(t, 2, h.written(sprint.NTickEnd), "the tick after a start of a done sprint: %+v, %d tick ends", res, h.written(sprint.NTickEnd))
	_, tail, err := h.m.Tails(h.ctx)
	require.NoError(t, err)
	waiter := &Store{B: h.m, Names: h.st.Names, Actor: h.st.Actor, Now: h.st.Now, NewID: h.st.NewID, Sleep: h.tick}
	woke, err = waiter.WaitTickEnd(h.ctx, tail, time.Second)
	require.NoError(t, err, "a wait with nothing to come: %v %v", woke, err)
	require.False(t, woke, "a wait with nothing to come: %v %v", woke, err)
}

// The mark is of its epoch (the cold read of #4843, 1): after a clear the
// new epoch's notes are scanned from their first, so the second sprint's
// done tick wakes the coordinator as the first one's did.
func TestTheTickEndMarkStartsAgainAfterAClear(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.landThrough("s1", "s1-1")
	res := h.machine()
	require.NotEmpty(t, res.Done, "the first sprint's done tick: %+v", res)
	require.NotEqual(t, 0, res.TickEnd, "the first sprint's done tick: %+v", res)
	_, err := h.st.Clear(h.ctx)
	require.NoError(t, err)
	h.setup(1)
	var ids []string
	for _, c := range h.snap().Work.Cards() {
		ids = append(ids, c.ID)
	}
	h.startMachine()
	h.landThrough("s1", ids...)
	res = h.machine()
	require.NotEmpty(t, res.Done, "the second sprint's done tick after the clear: done %q, tick end %d", res.Done, res.TickEnd)
	require.NotEqual(t, 0, res.TickEnd, "the second sprint's done tick after the clear: done %q, tick end %d", res.Done, res.TickEnd)
}

// noteBetween is the Mem with a note for the coordinator written once, right
// after the first notes scan: a note that lands between the tick-end's scan
// and its write.
type noteBetween struct {
	*Mem
	write func()
}

func (b *noteBetween) NotesSince(ctx context.Context, after string, max int) ([]sprint.Note, []string, error) {
	notes, ids, err := b.Mem.NotesSince(ctx, after, max)
	if w := b.write; w != nil {
		b.write = nil
		w()
	}
	return notes, ids, err
}

// A note that lands between the tick-end's scan and its write is after the
// mark (the cold read of #4843, 2): the next tick-end counts it, once, and
// the one after counts nothing.
func TestANoteBetweenTheScanAndTheWriteIsCountedOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	addressed := func(st *Store) {
		_, err := st.Run(h.ctx, Step{Verb: "probe", Plan: func(s *sprint.Snapshot) sprint.Plan {
			return sprint.Plan{Notes: []sprint.Note{{Kind: sprint.Happened, Type: sprint.NSprintDone, To: h.st.Actor, At: s.Now, What: "probe"}}}
		}})
		require.NoError(t, err)
	}
	addressed(h.st)
	b := &noteBetween{Mem: h.m}
	st := &Store{B: b, Names: h.st.Names, Actor: h.st.Actor, Now: h.st.Now, NewID: h.st.NewID, Sleep: h.st.Sleep}
	b.write = func() {
		addressed(&Store{B: h.m, Names: h.st.Names, Actor: h.st.Actor, Now: h.st.Now, NewID: h.st.NewID, Sleep: h.st.Sleep})
	}
	for i, want := range []int{1, 1, 0} {
		n, err := st.tickEnd(h.ctx)
		require.NoError(t, err, "tick end %d: counted %d, want %d (%v)", i+1, n, want, err)
		require.Equal(t, want, n, "tick end %d: counted %d, want %d (%v)", i+1, n, want, err)
	}
	n := h.written(sprint.NTickEnd)
	require.Equal(t, 2, n, "%d tick ends written, want 2", n)
}

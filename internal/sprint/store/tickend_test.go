package store

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The tick-end note (errata 3 amendment 8): a tick that addressed the
// coordinator nothing writes none; the tick that says the sprint is done
// writes one, addressed to the coordinator, counting every note for the
// coordinator since the last tick end (the two "ready to accept" the verbs
// opened between ticks, and the done), and a wait from before it wakes on it;
// the inbox does not list it; a start of the done sprint says done again and
// wakes once more; a wait from the tail runs out its time.
func TestTheTickEndWakesTheCoordinatorOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.startMachine()
	if res := h.machine(); res.TickEnd != 0 || h.written(sprint.NTickEnd) != 0 {
		t.Fatalf("a tick of an empty sprint addressed the coordinator nothing and wrote a tick end: %+v", res)
	}
	h.setup(2)
	_, from, err := h.m.Tails(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	h.landThrough("s1", "s1-1", "s1-2")
	if res := h.machine(); res.Done == "" || res.TickEnd != 3 || h.written(sprint.NTickEnd) != 1 {
		t.Fatalf("the done tick: done %q, tick end %d, written %d", res.Done, res.TickEnd, h.written(sprint.NTickEnd))
	}
	notes, _, err := h.m.NotesSince(h.ctx, from, 1000)
	if err != nil {
		t.Fatal(err)
	}
	last := notes[len(notes)-1]
	if last.Type != sprint.NTickEnd || last.Kind != sprint.Happened || last.To != h.st.Actor || last.What != "judgments=3" {
		t.Fatalf("the tick end: %+v", last)
	}
	if woke, err := h.st.WaitTickEnd(h.ctx, from, time.Minute); err != nil || !woke {
		t.Fatalf("a wait from before the done tick: %v %v", woke, err)
	}
	v, err := h.st.Inbox(h.ctx, time.Hour, time.Hour, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range v.Groups {
		if g.Type == sprint.NTickEnd {
			t.Fatalf("the inbox lists the tick end: %+v", g)
		}
	}
	// the machine stopped: a start and a tick with nothing addressed since
	h.startMachine()
	if res := h.machine(); res.TickEnd != 1 || h.written(sprint.NTickEnd) != 2 {
		// the start says the sprint done again (the done part), and wakes once
		t.Fatalf("the tick after a start of a done sprint: %+v, %d tick ends", res, h.written(sprint.NTickEnd))
	}
	_, tail, err := h.m.Tails(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	waiter := &Store{B: h.m, Names: h.st.Names, Actor: h.st.Actor, Now: h.st.Now, NewID: h.st.NewID, Sleep: h.tick}
	if woke, err := waiter.WaitTickEnd(h.ctx, tail, time.Second); err != nil || woke {
		t.Fatalf("a wait with nothing to come: %v %v", woke, err)
	}
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
	if res := h.machine(); res.Done == "" || res.TickEnd == 0 {
		t.Fatalf("the first sprint's done tick: %+v", res)
	}
	if _, err := h.st.Clear(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.setup(1)
	var ids []string
	for _, c := range h.snap().Work.Cards() {
		ids = append(ids, c.ID)
	}
	h.startMachine()
	h.landThrough("s1", ids...)
	if res := h.machine(); res.Done == "" || res.TickEnd == 0 {
		t.Fatalf("the second sprint's done tick after the clear: done %q, tick end %d", res.Done, res.TickEnd)
	}
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
		if _, err := st.Run(h.ctx, Step{Verb: "probe", Plan: func(s *sprint.Snapshot) sprint.Plan {
			return sprint.Plan{Notes: []sprint.Note{{Kind: sprint.Happened, Type: sprint.NSprintDone, To: h.st.Actor, At: s.Now, What: "probe"}}}
		}}); err != nil {
			t.Fatal(err)
		}
	}
	addressed(h.st)
	b := &noteBetween{Mem: h.m}
	st := &Store{B: b, Names: h.st.Names, Actor: h.st.Actor, Now: h.st.Now, NewID: h.st.NewID, Sleep: h.st.Sleep}
	b.write = func() {
		addressed(&Store{B: h.m, Names: h.st.Names, Actor: h.st.Actor, Now: h.st.Now, NewID: h.st.NewID, Sleep: h.st.Sleep})
	}
	for i, want := range []int{1, 1, 0} {
		n, err := st.tickEnd(h.ctx)
		if err != nil || n != want {
			t.Fatalf("tick end %d: counted %d, want %d (%v)", i+1, n, want, err)
		}
	}
	if n := h.written(sprint.NTickEnd); n != 2 {
		t.Fatalf("%d tick ends written, want 2", n)
	}
}

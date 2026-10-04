package refmodel

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The tests in this file cover the machine of machine.go: SetMachine, Tick
// and its parts tickResolve, tickDeal, tickAsk, tickAccept, tickResume and
// tickDone, and Release and Clear. The per-function table of the unit tier
// (go tool cover -func) says 0.0% for every one of them before this file.
// Every test is named TestMachineCover... so -run TestMachineCover selects
// them, and each pins its function's main path and its refusal or guard.
// The machine is pure functions over State: nothing here sleeps, tells the
// time, forks, opens a socket or needs a store. The seams are New and the
// State fields, plus coverState and coverRead from state_cover_test.go.

// mcSprint is a sprint with stream s1 waiting and member m1 up, the shape
// the tick's deal reads. New leaves the streams map empty, so s1 is placed
// here.
func mcSprint() State {
	s := coverState()
	s.Streams["s1"] = Stream{State: SWaiting}
	s.Members["m1"] = Up
	return s
}

// mcNoUpSprint is mcSprint with every member down.
func mcNoUpSprint() State {
	s := coverState()
	s.Streams["s1"] = Stream{State: SWaiting}
	return s
}

// mcCard places id in the stream, in the cell, at attempt 1 with score sc,
// needing needs.
func mcCard(s State, stream, id, cell string, sc float64, needs ...string) State {
	s.Primaries[id] = Primary{Stream: stream, State: cell, Needs: needs, Score: sc, Attempt: 1}
	return s
}

// mcSentinel places id in the stream as a sentinel in the cell at score sc.
func mcSentinel(s State, stream, id, cell string, sc float64) State {
	s = mcCard(s, stream, id, cell, sc)
	pr := s.Primaries[id]
	pr.Kind = KindSentinel
	s.Primaries[id] = pr
	return s
}

// mcWorked places id of stream s1 in review with its attempt-1 work card
// finished ok on m1 and its head at attempt 1: the shape the tick's ask and
// accept read.
func mcWorked(s State, id string) State {
	s = mcCard(s, "s1", id, Review, 1)
	s.Work[WC(id, 1)] = WorkCard{Primary: id, Attempt: 1, Member: "m1", Place: FDone, Gen: 1, OK: "ok"}
	pr := s.Primaries[id]
	pr.Head = 1
	s.Primaries[id] = pr
	return s
}

// TestMachineCoverSetMachine covers SetMachine (machine.go:32): start and
// stop set the machine's cell, the state it already has changes nothing, and
// the step writes no refusal and moves no other field.
func TestMachineCoverSetMachine(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		from string
		run  bool
		want string
	}{
		"start sets a stopped machine running":  {from: Stopped, run: true, want: Running},
		"stop sets a running machine stopped":   {from: Running, run: false, want: Stopped},
		"running asked to run changes nothing":  {from: Running, run: true, want: Running},
		"stopped asked to stop changes nothing": {from: Stopped, run: false, want: Stopped},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := mcSprint()
			s.Machine = tc.from
			s.Epoch = 7
			got, err := SetMachine(s, tc.run)
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got.Machine)
			assert.Equal(t, uint64(7), got.Epoch, "the epoch is kept")
			assert.Equal(t, tc.from, s.Machine, "the input state is not written in place")
			if tc.want == tc.from {
				assert.Equal(t, s, got, "the state it already has changes nothing")
			}
		})
	}
}

// TestMachineCoverTick covers Tick (machine.go:55): a RUNNING machine runs
// its parts in order and its deal takes the named choice; the guards are a
// STOPPED machine, which moves nothing, and a pending operation (D1), which
// is refused with the state returned untouched.
func TestMachineCoverTick(t *testing.T) {
	t.Parallel()
	sprint1 := func() State {
		s := mcCard(mcSprint(), "s1", "p1", Ready, 1)
		s.Machine = Running
		return s
	}
	for name, tc := range map[string]struct {
		s       State
		ch      TickChoices
		wantErr bool
		cell    string // p1's cell after the tick
		machine string
		dealt   bool // a work card was dealt to m1
	}{
		"a running machine deals its ready card": {
			s: sprint1(), cell: Working, machine: Running, dealt: true,
		},
		"the deal's named choice places it": {
			s: func() State { s := sprint1(); s.Members["m2"] = Up; return s }(),
			ch: TickChoices{Deal: map[string]string{"p1": "m1"}},
			cell: Working, machine: Running, dealt: true,
		},
		"a stopped machine moves nothing": {
			s: func() State { s := sprint1(); s.Machine = Stopped; return s }(),
			cell: Ready, machine: Stopped,
		},
		"a pending operation is refused": {
			s: func() State { s := sprint1(); s.Pending = "take"; return s }(),
			wantErr: true, cell: Ready, machine: Running,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := Tick(tc.s, tc.ch)
			if tc.wantErr {
				assert.ErrorContains(t, err, "operation take is pending")
				assert.ErrorAs(t, err, new(*Refusal))
				assert.Equal(t, tc.s, got, "a refused tick returns the state untouched")
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.cell, got.Primaries["p1"].State)
			assert.Equal(t, tc.machine, got.Machine)
			if tc.dealt {
				w := got.Work[WC("p1", 1)]
				assert.Equal(t, "m1", w.Member)
				assert.Equal(t, FReady, w.Place)
			} else {
				assert.Empty(t, got.Work, "no work card was dealt")
			}
		})
	}
}

// TestMachineCoverTickDone covers tickDone (machine.go:88), the tick's last
// part: a RUNNING machine whose sprint is done stops; its guards are the
// sprint not done, an empty sprint, and a machine already stopped.
func TestMachineCoverTickDone(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		s    State
		want string
	}{
		"a done sprint stops the running machine": {
			s: func() State {
				s := mcCard(mcSprint(), "s1", "p1", Landed, 1)
				s = mcCard(s, "s1", "d1", Off, 2)
				s.Machine = Running
				return s
			}(),
			want: Stopped,
		},
		"an open card leaves the machine running": {
			s: func() State {
				s := mcCard(mcSprint(), "s1", "p1", Landed, 1)
				s = mcCard(s, "s1", "p2", Ready, 2)
				s.Machine = Running
				return s
			}(),
			want: Running,
		},
		"an empty sprint is not done": {
			s: func() State { s := mcSprint(); s.Machine = Running; return s }(),
			want: Running,
		},
		"a stopped machine does not tick": {
			s: mcCard(mcSprint(), "s1", "p1", Landed, 1),
			want: Stopped,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			n := tc.s.Clone()
			n.tickDone()
			assert.Equal(t, tc.want, n.Machine)
		})
	}
}

// TestMachineCoverTickResolve covers tickResolve (machine.go:98), T1: a
// waiting card whose needs all landed moves to ready; the guards are an
// unmet need, which keeps the card waiting, a sentinel, which is marked
// reached and never moved, a dropped need, which opens the blocked
// judgment once, and a card that is not waiting, which is skipped.
func TestMachineCoverTickResolve(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		s      State
		cells  map[string]string
		open   []Judgment
		closed []Judgment
	}{
		"a waiting card whose needs landed moves to ready": {
			s: func() State {
				s := mcSentinel(mcSprint(), "s1", "e1", Landed, 1)
				return mcCard(s, "s1", "p1", Waiting, 2, "e1")
			}(),
			cells: map[string]string{"p1": Ready},
		},
		"an unmet need keeps it waiting": {
			s: func() State {
				s := mcCard(mcSprint(), "s1", "p2", Ready, 1)
				return mcCard(s, "s1", "p1", Waiting, 2, "p2")
			}(),
			cells: map[string]string{"p1": Waiting},
		},
		"a sentinel is marked reached and never moves": {
			s: func() State {
				s := mcSprint()
				s.Streams["s2"] = Stream{State: SWaiting}
				return mcSentinel(s, "s2", "e1", Waiting, 1)
			}(),
			cells: map[string]string{"e1": Waiting},
			open:  []Judgment{{JReached, "e1"}},
		},
		"a dropped need opens the blocked judgment once": {
			s: func() State {
				s := mcCard(mcSprint(), "s1", "d1", Off, 1)
				s = mcCard(s, "s1", "p1", Waiting, 2, "d1")
				s.Open[Judgment{JBlocked, "p1"}] = true
				return s
			}(),
			cells: map[string]string{"p1": Waiting},
			open:  []Judgment{{JBlocked, "p1"}},
		},
		"a card out of waiting is not resolved": {
			s:     mcCard(mcSprint(), "s1", "p1", Review, 1),
			cells: map[string]string{"p1": Review},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			n := tc.s.Clone()
			n.tickResolve()
			n.tickResolve() // every judgment is held once, not doubled
			for id, want := range tc.cells {
				assert.Equal(t, want, n.Primaries[id].State, id)
			}
			for _, j := range tc.open {
				assert.True(t, n.Open[j], "open %s", j)
			}
			for _, j := range tc.closed {
				assert.False(t, n.Open[j], "closed %s", j)
			}
		})
	}
}

// TestMachineCoverTickResume covers tickResume (machine.go:115), T7: a
// stream stopped only on a cross need resumes once every stuck card's need
// has landed, its note closing; the guards leave a cross need not yet
// landed, a stuck card naming no need, a red stop, a stop with nothing
// stuck and a stream that is not stopped, untouched.
func TestMachineCoverTickResume(t *testing.T) {
	t.Parallel()
	stuck := func(cause, need, needCell string) State {
		s := mcCard(mcSprint(), "s1", "a1", needCell, 1)
		s.Streams["b"] = Stream{State: SStopped, Cause: cause}
		s = mcCard(s, "b", "b1", Merging, 1)
		s.Merge["b1"] = MergeCard{Place: Stuck, Need: need}
		s.Open[Judgment{JCross, StreamSubject("b")}] = true
		return s
	}
	for name, tc := range map[string]struct {
		s         State
		wantCell  string
		wantPlace string // b1's merge place
		wantOpen  bool
	}{
		"a landed cross need resumes the stop": {
			s: stuck(CCross, "a1", Landed), wantCell: SMerging, wantPlace: Queued, wantOpen: false,
		},
		"an unlanded cross need keeps the stop": {
			s: stuck(CCross, "a1", Ready), wantCell: SStopped, wantPlace: Stuck, wantOpen: true,
		},
		"a stuck card naming no need is not resumed": {
			s: stuck(CCross, "", Landed), wantCell: SStopped, wantPlace: Stuck, wantOpen: true,
		},
		"a red stop waits for the coordinator": {
			s: stuck(CRed, "a1", Landed), wantCell: SStopped, wantPlace: Stuck, wantOpen: true,
		},
		"a stop with nothing stuck is passed over": {
			s: func() State { s := stuck(CCross, "a1", Landed); delete(s.Merge, "b1"); return s }(),
			wantCell: SStopped, wantPlace: Gone, wantOpen: true,
		},
		"a stream not stopped is passed over": {
			s: func() State { s := stuck(CCross, "a1", Landed); s.Streams["b"] = Stream{State: SMerging}; return s }(),
			wantCell: SMerging, wantPlace: Stuck, wantOpen: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			n := tc.s.Clone()
			n.tickResume()
			assert.Equal(t, tc.wantCell, n.Streams["b"].State)
			assert.Equal(t, tc.wantPlace, n.Merge["b1"].Place)
			assert.Equal(t, tc.wantOpen, n.Open[Judgment{JCross, StreamSubject("b")}], "the cross note")
		})
	}
}

// TestMachineCoverTickDeal covers tickDeal (machine.go:144), T3: a ready
// card is dealt to the next up member with room, and the named choice
// places it when it is the round's; the refusals are a choice out of the
// round, which the deal answers with a *ChoiceError. The fleet's own guards
// are the no-member judgment, open while ready cards wait with no member up
// and cleared when one is, the bound card passed over and judged once and
// not opened when acknowledged, and a member full to its Room holding
// nothing more.
func TestMachineCoverTickDeal(t *testing.T) {
	t.Parallel()
	bound := func(atBound bool) State {
		s := mcCard(mcSprint(), "s1", "p1", Ready, 1)
		pr := s.Primaries["p1"]
		pr.Attempt = 2
		s.Primaries["p1"] = pr
		s.Work[WC("p1", 2)] = WorkCard{Primary: "p1", Attempt: 2, Member: "m1", Place: FWithdrawn,
			TakeEnded: atBound, Redeals: MaxRedeals, Gen: 4}
		return s
	}
	twoUp := func() State {
		s := mcCard(mcSprint(), "s1", "p1", Ready, 1)
		s.Members["m2"] = Up
		return s
	}
	full := func() State {
		s := mcCard(mcSprint(), "s1", "p1", Ready, 1)
		for i := 0; i < Room; i++ {
			s.Work[fmt.Sprintf("x%03d.w1", i)] = WorkCard{Primary: "x", Member: "m1", Place: FReady}
		}
		return s
	}
	for name, tc := range map[string]struct {
		s         State
		choice    map[string]string
		wantErr   bool
		choiceErr bool
		cell      string
		on        string // the member the card is dealt to, "" for none
		open      []Judgment
		closed    []Judgment
	}{
		"a ready card is dealt to the next up member": {
			s: mcCard(mcSprint(), "s1", "p1", Ready, 1), cell: Working, on: "m1",
		},
		"the named choice places it": {
			s: twoUp(), choice: map[string]string{"p1": "m1"}, cell: Working, on: "m1",
		},
		"a choice out of the round is refused": {
			s: twoUp(), choice: map[string]string{"p1": "m2"}, wantErr: true, choiceErr: true, cell: Ready,
		},
		"no member up opens the fleet's judgment": {
			s: mcCard(mcNoUpSprint(), "s1", "p1", Ready, 1), cell: Ready, open: []Judgment{{JNoMember, "fleet"}},
		},
		"a member up clears the fleet's judgment": {
			s: mcCard(mcSprint(), "s1", "p1", Ready, 1),
			cell: Working, on: "m1", closed: []Judgment{{JNoMember, "fleet"}},
		},
		"a bound card is passed over and judged": {
			s: bound(true), cell: Ready, open: []Judgment{{JBound, "p1"}},
		},
		"an acknowledged bound opens nothing": {
			s: func() State { s := bound(true); s.Acked[Judgment{JBound, "p1"}] = true; return s }(),
			cell: Ready, closed: []Judgment{{JBound, "p1"}},
		},
		"a bound that clears closes its judgment": {
			s: func() State {
				s := bound(false)
				s.Open[Judgment{JBound, "p1"}] = true
				return s
			}(),
			cell: Working, on: "m1", closed: []Judgment{{JBound, "p1"}},
		},
		"a member full to its Room holds the deal": {
			s: full(), cell: Ready,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			n := tc.s.Clone()
			err := n.tickDeal(tc.choice)
			if tc.wantErr {
				assert.Error(t, err)
				if tc.choiceErr {
					assert.ErrorAs(t, err, new(*ChoiceError))
				}
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.cell, n.Primaries["p1"].State)
			if tc.on != "" {
				pr := n.Primaries["p1"]
				assert.Equal(t, tc.on, n.Work[WC("p1", pr.Attempt)].Member)
			}
			for _, j := range tc.open {
				assert.True(t, n.Open[j], "open %s", j)
			}
			for _, j := range tc.closed {
				assert.False(t, n.Open[j], "closed %s", j)
			}
		})
	}
}

// TestMachineCoverTickAsk covers tickAsk (machine.go:209), T2: a primary in
// review with no read card and work that did not fail is asked of the next
// two readers, and the named pair is asked when it is the round's; the
// refusal is a pair that is one reader twice, answered with a
// *ChoiceError and no read card. The guards pass over a card with a live
// read and a card whose work came back failed.
func TestMachineCoverTickAsk(t *testing.T) {
	t.Parallel()
	failed := func() State {
		s := mcWorked(coverState(), "p1")
		w := s.Work[WC("p1", 1)]
		w.OK = "failed"
		s.Work[WC("p1", 1)] = w
		return s
	}
	live := func() State {
		s := mcWorked(coverState(), "p1")
		coverRead(&s, "p1", 1, "r3", Asked)
		return s
	}
	for name, tc := range map[string]struct {
		s       State
		choice  map[string][]string
		wantErr bool
		asked   int // read cards left asked
	}{
		"a primary in review is asked of the next two readers": {
			s: mcWorked(coverState(), "p1"), asked: 2,
		},
		"the named pair is asked": {
			s: mcWorked(coverState(), "p1"), choice: map[string][]string{"p1": {"r2", "r1"}}, asked: 2,
		},
		"a pair of one reader twice is refused": {
			s: mcWorked(coverState(), "p1"), choice: map[string][]string{"p1": {"r1", "r1"}}, wantErr: true, asked: 0,
		},
		"a card with a live read is passed over": {
			s: live(), asked: 1,
		},
		"failed work is not asked": {
			s: failed(), asked: 0,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			n := tc.s.Clone()
			err := n.tickAsk(tc.choice)
			if tc.wantErr {
				assert.Error(t, err)
				assert.ErrorAs(t, err, new(*ChoiceError))
			} else {
				assert.NoError(t, err)
			}
			var asked []string
			for id, c := range n.Reads {
				if c.Place == Asked {
					asked = append(asked, id)
				}
			}
			assert.Len(t, asked, tc.asked)
			if tc.asked == 2 {
				assert.Equal(t, Asked, n.Reads[RC("p1", 1, "r1")].Place)
				assert.Equal(t, Asked, n.Reads[RC("p1", 1, "r2")].Place)
				assert.Equal(t, []string{"r1", "r2"}, n.Primaries["p1"].Pair)
			}
		})
	}
}

// TestMachineCoverTickAccept covers tickAccept (machine.go:240): a primary
// in review with ok reads from two different readers, its work not failed
// and not held, moves to merging and into its stream's queue; the refusal is
// a pending operation, answered with a refusal and nothing moved. The
// guards pass over CI red at the head, returned at the attempt, one ok
// read, and a card already in the merge queue.
func TestMachineCoverTickAccept(t *testing.T) {
	t.Parallel()
	acceptable := func() State {
		s := mcWorked(coverState(), "p1")
		coverRead(&s, "p1", 1, "r1", OK)
		coverRead(&s, "p1", 1, "r2", OK)
		return s
	}
	ciRed := func() State {
		s := acceptable()
		pr := s.Primaries["p1"]
		pr.CI, pr.CIHead = "red", 1
		s.Primaries["p1"] = pr
		return s
	}
	returned := func() State {
		s := acceptable()
		pr := s.Primaries["p1"]
		pr.ReturnedAt = 1
		s.Primaries["p1"] = pr
		return s
	}
	queued := func() State {
		s := acceptable()
		s.Merge["p1"] = MergeCard{Place: Queued}
		return s
	}
	oneRead := func() State {
		s := mcWorked(coverState(), "p1")
		coverRead(&s, "p1", 1, "r1", OK)
		return s
	}
	for name, tc := range map[string]struct {
		s        State
		wantCell string
		wantErr  bool
		queued   bool
	}{
		"two ok reads accept it into the queue": {
			s: acceptable(), wantCell: Merging, queued: true,
		},
		"a returned merge record does not hold it": {
			s: func() State { s := acceptable(); s.Merge["p1"] = MergeCard{Place: Returned}; return s }(),
			wantCell: Merging, queued: true,
		},
		"ci red at its head is held": {
			s: ciRed(), wantCell: Review,
		},
		"returned at its attempt is held": {
			s: returned(), wantCell: Review,
		},
		"one ok read is not acceptable": {
			s: oneRead(), wantCell: Review,
		},
		"a card already queued is passed over": {
			s: queued(), wantCell: Review, queued: true,
		},
		"a pending operation is refused": {
			s: func() State { s := acceptable(); s.Pending = "merge"; return s }(),
			wantCell: Review, wantErr: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			n := tc.s.Clone()
			err := n.tickAccept()
			if tc.wantErr {
				assert.ErrorContains(t, err, "operation merge is pending")
				assert.ErrorAs(t, err, new(*Refusal))
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.wantCell, n.Primaries["p1"].State)
			assert.Equal(t, tc.queued, n.Merge["p1"].Place == Queued)
			if tc.wantCell == Merging {
				assert.Equal(t, SMerging, n.Streams["s1"].State, "the stream takes the queue")
			}
		})
	}
}

// TestMachineCoverRelease covers Release (machine.go:268): the coordinator
// lands a reached sentinel, which moves every waiting primary whose needs
// are now met to ready and closes the sentinel's note, and a sentinel with
// nothing before it and its needs met is released without the mark; the
// refusals are another actor, an empty name, no ids at all, a pending
// operation, and an id that is not a sentinel waiting unreached.
func TestMachineCoverRelease(t *testing.T) {
	t.Parallel()
	reached := func() State {
		s := mcSentinel(mcSprint(), "s1", "e1", Waiting, 1)
		pr := s.Primaries["e1"]
		pr.Reached = true
		s.Primaries["e1"] = pr
		s.Open[Judgment{JReached, "e1"}] = true
		return mcCard(s, "s1", "p1", Waiting, 2, "e1")
	}
	notReachedBefore := func() State {
		s := mcSentinel(mcSprint(), "s1", "e1", Waiting, 2)
		return mcCard(s, "s1", "p0", Waiting, 1)
	}
	for name, tc := range map[string]struct {
		s       State
		ids     []string
		who     string
		wantErr string
		landed  bool
		ready   bool // p1 (or the waiter) moved to ready
	}{
		"the coordinator lands a reached sentinel": {
			s: reached(), ids: []string{"e1"}, who: "coord", landed: true, ready: true,
		},
		"nothing before it releases without the mark": {
			s: func() State {
				s := mcSprint()
				s.Streams["s2"] = Stream{State: SWaiting}
				return mcSentinel(s, "s2", "e2", Waiting, 1)
			}(),
			ids: []string{"e2"}, who: "coord", landed: true,
		},
		"another actor is refused": {
			s: reached(), ids: []string{"e1"}, who: "worker", wantErr: "is not the sprint's coordinator",
		},
		"an empty name is refused": {
			s: reached(), ids: []string{"e1"}, who: "", wantErr: "is not the sprint's coordinator",
		},
		"release names nothing": {
			s: reached(), ids: nil, who: "coord", wantErr: "release names nothing",
		},
		"a primary is not a sentinel": {
			s: reached(), ids: []string{"p1"}, who: "coord", wantErr: "not a reached sentinel",
		},
		"an unreached sentinel with something before it is refused": {
			s: notReachedBefore(), ids: []string{"e1"}, who: "coord", wantErr: "not a reached sentinel",
		},
		"a landed sentinel is refused": {
			s: func() State {
				s := reached()
				pr := s.Primaries["e1"]
				pr.State = Landed
				s.Primaries["e1"] = pr
				return s
			}(),
			ids: []string{"e1"}, who: "coord", wantErr: "not a reached sentinel",
		},
		"a pending operation is refused": {
			s: func() State { s := reached(); s.Pending = "add"; return s }(),
			ids: []string{"e1"}, who: "coord", wantErr: "operation add is pending",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := Release(tc.s, tc.ids, tc.who)
			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr)
				assert.ErrorAs(t, err, new(*Refusal))
				assert.Equal(t, tc.s, got, "a refused release returns the state untouched")
				return
			}
			assert.NoError(t, err)
			if tc.landed {
				assert.Equal(t, Landed, got.Primaries[tc.ids[0]].State)
			}
			if tc.ready {
				assert.Equal(t, Ready, got.Primaries["p1"].State, "its waiter moves to ready")
				assert.False(t, got.Open[Judgment{JReached, "e1"}], "the sentinel's note closes")
			}
		})
	}
}

// TestMachineCoverClear covers Clear (machine.go:302): the machine is
// STOPPED, the epoch advanced once, every table empty with the streams'
// rows kept waiting, the members with their status and no work, the
// judgments and the fence empty, and the coordinator kept. A second row
// clears an untouched New.
func TestMachineCoverClear(t *testing.T) {
	t.Parallel()
	full := func() State {
		s := mcCard(mcSprint(), "s1", "p1", Working, 1)
		s = mcSentinel(s, "s1", "e1", Waiting, 2)
		s.Work[WC("p1", 1)] = WorkCard{Primary: "p1", Attempt: 1, Member: "m1", Place: FWorking, Gen: 1}
		coverRead(&s, "p1", 1, "r1", Asked)
		s.Merge["p1"] = MergeCard{Place: Queued}
		s.Streams["b"] = Stream{State: SStopped, Cause: CCross}
		s.Open[Judgment{JFailed, "p1"}] = true
		s.Acked[Judgment{JBound, "p1"}] = true
		s.Machine = Running
		s.Epoch = 3
		s.Pending = "add"
		return s
	}
	for name, tc := range map[string]struct {
		s        State
		wantEpo  uint64
		wantStreams map[string]Stream
	}{
		"a full sprint clears to the next epoch": {
			s: full(), wantEpo: 4,
			wantStreams: map[string]Stream{"s1": {State: SWaiting}, "b": {State: SWaiting}},
		},
		"an untouched sprint clears to epoch one": {
			s: coverState(), wantEpo: 1, wantStreams: map[string]Stream{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := Clear(tc.s)
			assert.NoError(t, err)
			assert.Equal(t, tc.wantEpo, got.Epoch)
			assert.Equal(t, Stopped, got.Machine, "the machine is stopped")
			assert.Empty(t, got.Primaries)
			assert.Empty(t, got.Work)
			assert.Empty(t, got.Reads)
			assert.Empty(t, got.Merge)
			assert.Empty(t, got.Open, "the judgments are the new epoch's own")
			assert.Empty(t, got.Acked)
			assert.Empty(t, got.Pending, "the fence is the new epoch's own")
			assert.Equal(t, tc.wantStreams, got.Streams, "the streams keep their rows, waiting")
			assert.Equal(t, tc.s.Members, got.Members, "every member keeps its status")
			assert.Equal(t, tc.s.Order, got.Order)
			assert.Equal(t, tc.s.Readers, got.Readers)
			assert.Equal(t, tc.s.Coordinator, got.Coordinator, "the coordinator is kept")
		})
	}
}

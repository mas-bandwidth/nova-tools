package sprint

import (
	"strings"
	"testing"
	"time"
)

// R17's look with the repairs errata 3 decides for it, each test named for its
// hole: stopclose (H7), stoprearm (H16) and stopinputs (H14, H17), in
// tla/SprintEvents.tla.

// stoppedWorld is a world whose machine has been STOPPED for an hour, and a
// look at it: the clock and whether the judgment is open are the world's.
type stoppedWorld struct {
	w      *timeWorld
	inputs StopInputs
}

func newStoppedWorld(t *testing.T) *stoppedWorld {
	w := newTimeWorld(t)
	w.clock = Clock{StoppedSinceMs: timeWall0 - timeHour}
	return &stoppedWorld{w: w}
}

// open says "the machine is STOPPED and moves are due" is open in the world.
func (sw *stoppedWorld) open() bool {
	_, ok := sw.w.j[subjectSprint+"|"+NStoppedWithDue+"|stopped"]
	return ok
}

// look is R17's look at wall with the dry plans given.
func (sw *stoppedWorld) look(dry []RulePlan, wall int64) RulePlan {
	return StoppedLook(dry, StopRead{Clock: sw.w.clock, Wall: wall, Open: sw.open(), Inputs: sw.inputs})
}

// step looks and applies the look's step, which its guard lets through.
func (sw *stoppedWorld) step(dry []RulePlan, wall int64) RulePlan {
	sw.w.t.Helper()
	p := sw.look(dry, wall)
	if why := StoppedApplies(p, sw.w.clock, sw.inputs); why != "" {
		sw.w.t.Fatalf("the look's own step is refused: %s", why)
	}
	if eff := sw.w.apply(p); eff.Refused != "" {
		sw.w.t.Fatalf("the look's step is refused: %s", eff.Refused)
	}
	return p
}

// H7: while the judgment is open, a look that finds no move due closes it,
// clears due_since_ms and stopraised_ms, and the next look writes nothing.
func TestStoppedLookClosesWhenDryPlanEmpty(t *testing.T) {
	t.Parallel()
	sw := newStoppedWorld(t)
	due := []RulePlan{dryPlan("a")}
	span := StoppedDueSpan.Milliseconds()
	sw.step(due, timeWall0)
	if p := sw.step(due, timeWall0+span); noteReq(p, requestOpen, NStoppedWithDue) == nil || !sw.open() {
		t.Fatalf("not raised at ten minutes: %+v", p.Notes)
	}
	// Moves still due: the judgment stays open, and nothing is written.
	if p := sw.look(due, timeWall0+span+10*timeSec); !silent(p) {
		t.Fatalf("a look with moves still due plans %+v", p)
	}
	// A verb emptied the dry plans: the next look closes the judgment.
	p := sw.step(nil, timeWall0+span+20*timeSec)
	if noteReq(p, requestClose, NStoppedWithDue) == nil || sw.open() {
		t.Fatalf("the judgment is not closed: %+v", p.Notes)
	}
	if c := p.Sprint.Clock; c == nil || !c.ClearDueSince || !c.ClearStopRaised {
		t.Fatalf("the close's clock writes: %+v", p.Sprint.Clock)
	}
	if sw.w.clock.DueSinceMs != 0 || sw.w.clock.StopRaisedMs != 0 {
		t.Fatalf("the clock after the close: %+v", sw.w.clock)
	}
	// The next look finds nothing to do.
	if p := sw.look(nil, timeWall0+span+30*timeSec); !silent(p) {
		t.Fatalf("the look after the close plans %+v", p)
	}
	// With the judgment not open, a look with no move due closes nothing.
	sw2 := newStoppedWorld(t)
	sw2.w.clock.DueSinceMs = timeWall0 - timeHour
	if p := sw2.look(nil, timeWall0); noteReq(p, requestClose, NStoppedWithDue) != nil || p.Sprint.Clock == nil || !p.Sprint.Clock.ClearDueSince {
		t.Fatalf("a look with no judgment open: %+v %+v", p.Notes, p.Sprint.Clock)
	}
}

// H16: a close clears stopraised_ms, so a move due again later in the same
// STOPPED span is named again, ten minutes after it became due: once per
// (span, close) pair, and never twice while the judgment is open.
func TestStoppedLookRaisesOncePerSpanAndClose(t *testing.T) {
	t.Parallel()
	sw := newStoppedWorld(t)
	due := []RulePlan{dryPlan("a")}
	span := StoppedDueSpan.Milliseconds()
	since := sw.w.clock.StoppedSinceMs
	raises := 0
	count := func(p RulePlan) {
		if noteReq(p, requestOpen, NStoppedWithDue) != nil {
			raises++
		}
	}
	// Due for a hundred spans: raised once.
	sw.step(due, timeWall0)
	for i := int64(1); i <= 100; i++ {
		count(sw.step(due, timeWall0+i*span))
	}
	if raises != 1 || sw.w.clock.StopRaisedMs != since {
		t.Fatalf("raised %d times in one span with no close: %+v", raises, sw.w.clock)
	}
	// Nothing due: closed. Due again, in the same span: raised again, from
	// when it became due again.
	at := timeWall0 + 101*span
	sw.step(nil, at)
	if sw.open() || sw.w.clock.StopRaisedMs != 0 {
		t.Fatalf("not closed: %+v", sw.w.clock)
	}
	sw.step(due, at+span)
	if p := sw.look(due, at+2*span-1); !silent(p) {
		t.Fatalf("raised before ten minutes from when moves were due again: %+v", p)
	}
	for i := int64(2); i <= 50; i++ {
		count(sw.step(due, at+i*span))
	}
	if raises != 2 || !sw.open() || sw.w.clock.StoppedSinceMs != since || sw.w.clock.StopRaisedMs != since {
		t.Fatalf("raised %d times, want 2 (once before the close, once after): %+v", raises, sw.w.clock)
	}
}

// stopInputsRead are the inputs of a look's dry plans as read: a card of the
// work table and one of the merge table, the counters, two members' control
// cards and their beats.
func stopInputsRead() StopInputs {
	return StopInputs{
		Cards:   map[CardRef]uint64{{Table: Work, ID: "a"}: 3, {Table: Merge, ID: "ctl-s1"}: 7, {Table: Work, ID: "gone"}: 0},
		Next:    40,
		Streams: 2,
		Members: map[string]uint64{"m1": 5, "m2": 9},
		Beats:   map[string]int64{"m1": timeWall0 + 15*timeSec, "m2": 0},
	}
}

// H14 and H17: R17's step is guarded on the version of every input of its dry
// plans as read. An input that moved since the read refuses the step XGUARD,
// the step writes nothing, and the next look plans on what is there now.
func TestStoppedApplyRefusesOnStaleInputs(t *testing.T) {
	t.Parallel()
	span := StoppedDueSpan.Milliseconds()
	for _, tc := range []struct {
		name string
		move func(in *StopInputs)
	}{
		{"a card's revision", func(in *StopInputs) { in.Cards[CardRef{Table: Work, ID: "a"}]++ }},
		{"a card removed", func(in *StopInputs) { delete(in.Cards, CardRef{Table: Merge, ID: "ctl-s1"}) }},
		{"a card created where the read found none", func(in *StopInputs) { in.Cards[CardRef{Table: Work, ID: "gone"}] = 1 }},
		{"the score and id counter, a card added or ranked", func(in *StopInputs) { in.Next++ }},
		{"the stream-set counter", func(in *StopInputs) { in.Streams++ }},
		{"a member's control card, its status", func(in *StopInputs) { in.Members["m1"]++ }},
		{"a member's control card, its stable_since", func(in *StopInputs) { in.Members["m2"]++ }},
		{"a beat record", func(in *StopInputs) { in.Beats["m1"] += 15 * timeSec }},
		{"a beat record, a first beat", func(in *StopInputs) { in.Beats["m2"] = timeWall0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sw := newStoppedWorld(t)
			sw.inputs = stopInputsRead()
			due := []RulePlan{dryPlan("a")}
			sw.step(due, timeWall0)
			// The look that raises, read on the inputs as they were.
			p := sw.look(due, timeWall0+span)
			if noteReq(p, requestOpen, NStoppedWithDue) == nil {
				t.Fatalf("the look does not raise: %+v", p.Notes)
			}
			for _, g := range stopInputsRead().guards() {
				if !hasGuard(p, g) {
					t.Fatalf("the step does not guard %+v: %+v", g, p.Guards)
				}
			}
			if why := StoppedApplies(p, sw.w.clock, stopInputsRead()); why != "" {
				t.Fatalf("nothing moved, and the step is refused: %s", why)
			}
			// An input moves between the read and the step: a verb that
			// emptied the dry plans, or a change of the fleet.
			now := stopInputsRead()
			now.Cards, now.Members, now.Beats = clone(now.Cards), clone(now.Members), clone(now.Beats)
			tc.move(&now)
			why := StoppedApplies(p, sw.w.clock, now)
			if !strings.HasPrefix(why, "XGUARD") {
				t.Fatalf("a moved input is not refused XGUARD: %q", why)
			}
			if sw.open() {
				t.Fatalf("the refused step opened the judgment")
			}
			// The next look plans on what is there now: the dry plans are
			// empty, and nothing is raised; its own step applies.
			sw.inputs = now
			q := sw.look(nil, timeWall0+span+10*timeSec)
			if noteReq(q, requestOpen, NStoppedWithDue) != nil {
				t.Fatalf("the next look raises on empty dry plans: %+v", q.Notes)
			}
			if why := StoppedApplies(q, sw.w.clock, now); why != "" {
				t.Fatalf("the next look's step is refused: %s", why)
			}
		})
	}
	// The clock fields as read guard the step as before.
	t.Run("a clock field", func(t *testing.T) {
		t.Parallel()
		sw := newStoppedWorld(t)
		sw.step([]RulePlan{dryPlan("a")}, timeWall0)
		p := sw.look([]RulePlan{dryPlan("a")}, timeWall0+span)
		c := sw.w.clock
		c.StopHoldMs = timeWall0 + timeHour
		if why := StoppedApplies(p, c, sw.inputs); !strings.HasPrefix(why, "XGUARD") {
			t.Fatalf("a wait between the read and the step is not refused XGUARD: %q", why)
		}
	})
	// A close is guarded the same way: a stale close is refused.
	t.Run("a close on stale inputs", func(t *testing.T) {
		t.Parallel()
		sw := newStoppedWorld(t)
		sw.inputs = stopInputsRead()
		due := []RulePlan{dryPlan("a")}
		sw.step(due, timeWall0)
		sw.step(due, timeWall0+span)
		p := sw.look(nil, timeWall0+span+10*timeSec)
		if noteReq(p, requestClose, NStoppedWithDue) == nil {
			t.Fatalf("no close: %+v", p.Notes)
		}
		now := stopInputsRead()
		now.Next++
		if why := StoppedApplies(p, sw.w.clock, now); !strings.HasPrefix(why, "XGUARD") {
			t.Fatalf("a close after a card was added is not refused XGUARD: %q", why)
		}
	})
}

// clone is a copy of a map.
func clone[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Read takes the cards a snapshot loaded, each at its revision: the members'
// control cards apart, and the first reading of a card kept.
func TestStopInputsReadsTheCardsLoaded(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	w.s.Work.Put(&Card{ID: "a", Row: "s1", Col: Waiting, Rev: 3})
	var in StopInputs
	in.Read(w.s)
	if got := in.Cards[CardRef{Table: Work, ID: "a"}]; got != 3 {
		t.Fatalf("card a read at %d, want 3: %+v", got, in.Cards)
	}
	if _, ok := in.Cards[CardRef{Table: Merge, ID: "ctl-s1"}]; !ok {
		t.Fatalf("the stream's control card is not read: %+v", in.Cards)
	}
	for _, m := range []string{"m1", "m2", "m3"} {
		if _, ok := in.Members[m]; !ok {
			t.Fatalf("member %s's control card is not read: %+v", m, in.Members)
		}
		if _, ok := in.Cards[CardRef{Table: Fleet, ID: CtlID(m)}]; ok {
			t.Fatalf("member %s's control card is read as a card", m)
		}
	}
	w.s.Work.Put(&Card{ID: "a", Row: "s1", Col: Ready, Rev: 4})
	in.Read(w.s)
	if got := in.Cards[CardRef{Table: Work, ID: "a"}]; got != 3 {
		t.Fatalf("a second reading replaced the first: %d", got)
	}
}

// The judgment says the look's time it is as of.
func TestStoppedJudgmentSaysAsOfTheLook(t *testing.T) {
	t.Parallel()
	sw := newStoppedWorld(t)
	due := []RulePlan{dryPlan("a")}
	span := StoppedDueSpan.Milliseconds()
	sw.step(due, timeWall0)
	p := sw.look(due, timeWall0+span)
	n := noteReq(p, requestOpen, NStoppedWithDue)
	asOf := "as of " + time.UnixMilli(timeWall0+span).UTC().Format(time.RFC3339)
	if n == nil || !strings.Contains(n.Text, asOf) {
		t.Fatalf("the judgment does not say %q: %+v", asOf, p.Notes)
	}
}

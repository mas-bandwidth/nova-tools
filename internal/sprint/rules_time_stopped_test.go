package sprint

import (
	"reflect"
	"slices"
	"strconv"
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

// stopReadSpec is the store as R17's look reads it: the cards of the work
// table by revision, the members' control cards by revision, the beat entries
// of the due set, and the two counters of {p}next@e. snapshot is the look's
// read of it, loaded as a plan's read is: cards as records, the beats as the
// range over the due set, the stream-set counter as a sprint key.
type stopReadSpec struct {
	next, streams uint64
	beats         map[string]int64
	cards         map[string]uint64
	members       map[string]uint64
	noBeats       bool // the read did not ask for the beats
	noStreams     bool // the read did not ask for the stream-set counter
}

func (sp stopReadSpec) clone() stopReadSpec {
	sp.beats, sp.cards, sp.members = clone(sp.beats), clone(sp.cards), clone(sp.members)
	return sp
}

func (sp stopReadSpec) snapshot(t testing.TB) *Snapshot {
	t.Helper()
	rp := ReadPlan{Sprint: []SprintQ{{Kind: QueryStreams, Limit: 1}}}
	ans := ReadAnswer{Epoch: "1", ActiveEpoch: "1", TimeMS: "1790000000123", Sprint: []Answer{{Kind: QueryStreams}}}
	if !sp.noStreams {
		rp.Sprint[0].Keys = []string{KeyNextStreams}
		ans.Sprint[0].Keys = []KeyAnswer{{Key: KeyNextStreams, N: sp.streams}}
	}
	if !sp.noBeats {
		rp.Ranges = []RangeQ{{Key: factBeats, Limit: MaxMembers}}
		a := TsetAnswer{Kind: AnswerRange}
		for _, m := range membersOf(sp.beats) {
			a.IDs = append(a.IDs, beatKeyPrefix+m)
			a.Scores = append(a.Scores, float64(sp.beats[m]))
		}
		ans.Tset = []TsetAnswer{a}
	}
	for _, id := range membersOf(sp.cards) {
		ans.Sprint[0].Records = append(ans.Sprint[0].Records, TableCard{Work, &Card{ID: id, Row: "s1", Col: Waiting, Rev: sp.cards[id]}})
	}
	for _, m := range membersOf(sp.members) {
		ans.Sprint[0].Records = append(ans.Sprint[0].Records, TableCard{Fleet, &Card{ID: CtlID(m), Row: m, Col: Ctl, Rev: sp.members[m]}})
	}
	s, err := loadPartial(rp, ans, false)
	if err != nil {
		t.Fatalf("the look's read: %v", err)
	}
	return s
}

func stopReadBase() stopReadSpec {
	return stopReadSpec{
		next: 40, streams: 2,
		beats:   map[string]int64{"m1": timeWall0 + 15*timeSec},
		cards:   map[string]uint64{"a": 3, "b": 1},
		members: map[string]uint64{"m1": 5, "m2": 9},
	}
}

// ReadStopInputs records what the read gave: the cards and the members' control
// cards, the stream-set counter, the beat of every member (0 for one with no
// beat entry), the score and id counter the caller gives, and the ids asked for
// and read as absent at revision 0.
func TestReadStopInputsRecordsTheRead(t *testing.T) {
	t.Parallel()
	sp := stopReadBase()
	s := sp.snapshot(t)
	in := ReadStopInputs(s, StopFacts{Next: 41, Absent: []CardRef{{Table: Work, ID: "later"}, {Table: Work, ID: "a"}}})
	if got := s.Unloaded(); len(got) != 0 {
		t.Fatalf("the read is refused: %v", got)
	}
	wantCards := map[CardRef]uint64{{Table: Work, ID: "a"}: 3, {Table: Work, ID: "b"}: 1, {Table: Work, ID: "later"}: 0}
	if !reflect.DeepEqual(in.Cards, wantCards) {
		t.Errorf("cards %v, want %v (a is read at 3 and asked absent: the read wins)", in.Cards, wantCards)
	}
	if in.Next != 41 || in.Streams != 2 {
		t.Errorf("counters next %d streams %d, want 41 and 2", in.Next, in.Streams)
	}
	if want := map[string]uint64{"m1": 5, "m2": 9}; !reflect.DeepEqual(in.Members, want) {
		t.Errorf("members %v, want %v", in.Members, want)
	}
	if want := map[string]int64{"m1": timeWall0 + 15*timeSec, "m2": 0}; !reflect.DeepEqual(in.Beats, want) {
		t.Errorf("beats %v, want %v (m2 has none: 0)", in.Beats, want)
	}
}

// Read then apply, through the producer, for each kind of guard R17's step
// carries: the look's inputs are read from the store as it is, the step is
// guarded on them, and the same read taken again at apply is refused XGUARD
// when the one input moved, and lets the step through when nothing moved.
func TestStoppedApplyOnAReadRefusesEachKindOfGuard(t *testing.T) {
	t.Parallel()
	span := StoppedDueSpan.Milliseconds()
	facts := StopFacts{Next: 40, Absent: []CardRef{{Table: Work, ID: "later"}}, Versions: map[string]uint64{Work: 3}}
	for _, tc := range []struct {
		name  string
		kind  string // the kind of guard that names the move
		move  func(sp *stopReadSpec, f *StopFacts)
		moved bool
	}{
		{"nothing moved", "", func(*stopReadSpec, *StopFacts) {}, false},
		{"a card's revision", guardRevs, func(sp *stopReadSpec, _ *StopFacts) { sp.cards["a"]++ }, true},
		{"a card removed", guardRevs, func(sp *stopReadSpec, _ *StopFacts) { delete(sp.cards, "b") }, true},
		{"a card asked for and absent, created", guardRevs, func(sp *stopReadSpec, _ *StopFacts) { sp.cards["later"] = 1 }, true},
		{"the score and id counter", guardCounter + " next ", func(_ *stopReadSpec, f *StopFacts) { f.Next++ }, true},
		{"the stream-set counter", guardCounter + " " + KeyNextStreams, func(sp *stopReadSpec, _ *StopFacts) { sp.streams++ }, true},
		{"a member's control card", guardCtl, func(sp *stopReadSpec, _ *StopFacts) { sp.members["m2"]++ }, true},
		{"a beat", guardBeat, func(sp *stopReadSpec, _ *StopFacts) { sp.beats["m1"] += 15 * timeSec }, true},
		{"a first beat", guardBeat, func(sp *stopReadSpec, _ *StopFacts) { sp.beats["m2"] = timeWall0 }, true},
		{"a table's version", guardVersion, func(_ *stopReadSpec, f *StopFacts) { f.Versions = map[string]uint64{Work: 4} }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sp := stopReadBase()
			sw := newStoppedWorld(t)
			sw.inputs = ReadStopInputs(sp.snapshot(t), facts)
			due := []RulePlan{dryPlan("a")}
			sw.step(due, timeWall0)
			p := sw.look(due, timeWall0+span)
			if noteReq(p, requestOpen, NStoppedWithDue) == nil {
				t.Fatalf("the look does not raise: %+v", p.Notes)
			}
			for _, kind := range []string{guardRevs, guardVersion, guardCounter, guardCtl, guardBeat} {
				if !slices.ContainsFunc(p.Guards, func(g XGuard) bool { return g.Kind == kind }) {
					t.Fatalf("the step carries no %s guard: %+v", kind, p.Guards)
				}
			}
			after, f := sp.clone(), facts
			tc.move(&after, &f)
			s := after.snapshot(t)
			now := ReadStopInputs(s, f)
			if got := s.Unloaded(); len(got) != 0 {
				t.Fatalf("the read at apply is refused: %v", got)
			}
			why := StoppedApplies(p, sw.w.clock, now)
			switch {
			case !tc.moved && why != "":
				t.Fatalf("nothing moved, and the step is refused: %s", why)
			case tc.moved && !strings.HasPrefix(why, "XGUARD: "+tc.kind):
				t.Fatalf("the move is refused %q, want XGUARD naming %q", why, tc.kind)
			}
		})
	}
}

// A read that did not ask for the stream-set counter or the beats is refused as
// a plan's read is: the inputs it gives compare 0 with 0 and never fire, so the
// caller does not use them.
func TestReadStopInputsRefusesAReadThatDidNotAsk(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(*stopReadSpec)
		want string
	}{
		{"the stream-set counter", func(sp *stopReadSpec) { sp.noStreams = true }, KeyNextStreams},
		{"the beats", func(sp *stopReadSpec) { sp.noBeats = true }, factBeats},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sp := stopReadBase()
			tc.edit(&sp)
			s := sp.snapshot(t)
			ReadStopInputs(s, StopFacts{})
			got := s.Unloaded()
			if len(got) != 1 || !strings.Contains(got[0], tc.want) {
				t.Fatalf("refused %v, want one refusal naming %q", got, tc.want)
			}
		})
	}
}

// The revisions of a table's cards are one version, and no two moves of them
// cancel: two cards that swap revisions, and one card written while another is
// removed, each move the fold. Each table has its own.
func TestRevFoldMovesOnEveryMove(t *testing.T) {
	t.Parallel()
	ref := func(table, id string) CardRef { return CardRef{Table: table, ID: id} }
	base := map[CardRef]uint64{ref(Work, "a"): 1, ref(Work, "b"): 2, ref(Work, "c"): 1, ref(Merge, "a"): 4}
	fold := revFold(base, Work)
	if fold < 0 {
		t.Fatalf("a fold is a non-negative score: %d", fold)
	}
	for name, moved := range map[string]map[CardRef]uint64{
		"two cards swap revisions":         {ref(Work, "a"): 2, ref(Work, "b"): 1, ref(Work, "c"): 1},
		"one written, one removed":         {ref(Work, "a"): 1, ref(Work, "b"): 3},
		"a card gone":                      {ref(Work, "a"): 1, ref(Work, "b"): 2},
		"a card appears at revision 0":     {ref(Work, "a"): 1, ref(Work, "b"): 2, ref(Work, "c"): 1, ref(Work, "d"): 0},
		"a card of the same id renamed":    {ref(Work, "a"): 1, ref(Work, "b"): 2, ref(Work, "c2"): 1},
		"a revision one higher":            {ref(Work, "a"): 1, ref(Work, "b"): 2, ref(Work, "c"): 2},
		"a revision carried into the next": {ref(Work, "a"): 1, ref(Work, "b"): 2, ref(Work, "c"): 1 + 256},
	} {
		if revFold(moved, Work) == fold {
			t.Errorf("%s: the fold did not move", name)
		}
	}
	other := clone(base)
	other[ref(Merge, "a")]++
	if revFold(other, Work) != fold || revFold(other, Merge) == revFold(base, Merge) {
		t.Errorf("a card of the merge table moved the fold of the work table, or not its own")
	}
	if revFold(map[CardRef]uint64{ref(Work, "b"): 2, ref(Work, "a"): 1, ref(Work, "c"): 1}, Work) != fold {
		t.Errorf("the fold depends on the order of the cards")
	}
	// however many cards are read, the step carries one guard for each table
	many := StopInputs{Cards: map[CardRef]uint64{}}
	for i := 0; i < 3000; i++ {
		many.Cards[ref(Work, "w"+strconv.Itoa(i))] = uint64(i)
	}
	many.Cards[ref(Merge, "m")] = 1
	revs := 0
	for _, g := range many.guards() {
		if g.Kind == guardRevs {
			revs++
		}
	}
	if revs != 2 {
		t.Errorf("%d fold guards for the cards of two tables, want 2", revs)
	}
}

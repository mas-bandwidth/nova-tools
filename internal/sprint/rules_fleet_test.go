package sprint

import (
	"strconv"
	"strings"
	"testing"
)

// The fleet rules' tests move a snapshot the way a store would. fleetT is the
// world of sim_test.go (the tables, and the plans applied to them with every
// place and revision guard checked) with what a RulePlan asks beyond the
// tables: the guards X checks at apply, checked here on the state a plan meets;
// the sprint's own keys the rules read (the facts); and a small J that answers
// the notes a plan asks for the way 1.3.4 says (one per cause; a hold keeps a
// cause closed; a close closes what is open).
//
// Every plan is made on a snapshot loaded from the rule's own read plan by
// LoadPartial, through the twin of rules_fleet_twin_test.go: a plan that reads a
// cell, count, row or field its read did not name panics, and a plan that
// leaves out a field its read did not carry (an unset it would have made) is not
// the plan made on the whole sprint, which fleetT.plan compares it with.

const fleetR0 int64 = 3_600_000 // R at the plans below, in ms

type fleetT struct {
	t      *testing.T
	w      *world
	facts  fleetFacts
	now    Now
	jopen  map[string]string // "<subject>|<type>|<cause>" -> the note, "h..." for a hold
	lines  []NoteReq         // the notes J wrote a line for, in order
	agenda map[string]bool   // the keys queued
	held   map[string]bool   // the keys held back
	seq    int
	// extra are records the read's first query also returns (fleetTwin.extra).
	extra []TableCard
	// partialOnly says the plan made on what the read loaded may differ from the
	// plan made on the whole sprint (a queue longer than its head).
	partialOnly bool
	// bounds are the read bounds the plans are read within.
	bounds ReadBounds
	// halvings are the halvings the reads are planned at (1.3.5).
	halvings int
	// mutate changes the read plan before it is answered: a test that asks
	// whether a field or a follow of a read is needed takes it out.
	mutate func(rp *ReadPlan)
}

// newFleetT is a sprint with the members up (rows and control cards) and n
// ready primaries in stream s1.
func newFleetT(t *testing.T, primaries int, members ...string) *fleetT {
	t.Helper()
	w := newWorld(t, "reader-a")
	for _, m := range members {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m}))
	}
	if primaries > 0 {
		w.must(Add(w.s, AddReq{Stream: "s1", Count: primaries}))
	}
	return newFleetOn(t, w)
}

// newFleetOn is the harness over a world already built.
func newFleetOn(t *testing.T, w *world) *fleetT {
	return &fleetT{t: t, w: w,
		facts:  fleetFacts{BeatDue: map[string]int64{}, Strangers: map[string]bool{}, Dropping: map[string]bool{}},
		now:    Now{R: fleetR0, Wall: t0.UnixMilli(), Running: true},
		jopen:  map[string]string{},
		agenda: map[string]bool{},
		held:   map[string]bool{},
		bounds: fleetBounds()}
}

func (f *fleetT) snap() *Snapshot { return f.w.s }

// agendaOf is agenda keys from their texts, in the order of the seqs that
// queued them. It is the one place a test builds a key from its parts, with
// keyRule and keySubject in rules_fleet.go.
func agendaOf(texts ...string) []AgendaKey {
	out := make([]AgendaKey, len(texts))
	for i, s := range texts {
		out[i] = AgendaKey{Key: s, Seq: uint64(i + 1)}
	}
	return out
}

// roomy gives the reads the room for several keys at the full limit: a read of
// the bounds of layer 1 holds one down key at 2,000 cards, and the tests that
// plan two members down in one step need both.
func (f *fleetT) roomy() *fleetT {
	f.bounds.Records = 10 * MaxReadRecords
	return f
}

// shape is the streams and members of the sprint, what R6's and R7's reads are
// named by.
func (f *fleetT) shape() fleetShape {
	return fleetShape{Streams: append([]string(nil), f.snap().Work.Rows()...), Members: append([]string(nil), f.snap().Fleet.Rows()...)}
}

// readPlan is the rule's read of the keys.
func (f *fleetT) readPlan(rule string, ks []AgendaKey) ReadPlan {
	f.t.Helper()
	var rp ReadPlan
	var left []AgendaKey
	switch rule {
	case ruleSeen:
		rp, left = readSeen(ks, f.bounds, f.halvings)
	case ruleDown:
		rp, left = readDown(ks, f.bounds, f.halvings)
	case ruleDeal:
		rp = dealReadFor(f.shape(), f.bounds, f.halvings)
	case ruleLevel:
		rp = levelReadFor(f.shape())
	default:
		f.t.Fatalf("no rule %s", rule)
	}
	if len(left) != 0 {
		f.t.Fatalf("the read left keys %v", keyTexts(left))
	}
	if f.mutate != nil {
		rp.Sprint = append([]SprintQ(nil), rp.Sprint...) // the queries are values; the plan is not shared
		f.mutate(&rp)
	}
	return rp
}

// planOn is the rule's plan on a snapshot.
func (f *fleetT) planOn(rule string, s *Snapshot, ks []AgendaKey) RulePlan {
	switch rule {
	case ruleSeen:
		return planSeenWith(s, ks, f.now, f.facts)
	case ruleDown:
		return planDownWith(s, ks, f.now, f.facts)
	case ruleDeal:
		return planDealWith(s, ks, f.now, f.facts)
	case ruleLevel:
		return planLevelWith(s, ks, f.now, f.facts)
	}
	f.t.Fatalf("no rule %s", rule)
	return RulePlan{}
}

// loaded is the snapshot the rule's read loads from the state: its read plan
// answered by the twin, and loaded.
func (f *fleetT) loaded(rule string, ks []AgendaKey) *Snapshot {
	f.t.Helper()
	rp := f.readPlan(rule, ks)
	s, err := LoadPartial(rp, fleetTwin{whole: f.snap(), extra: f.extra}.Answer(rp))
	if err != nil {
		f.t.Fatalf("%s: the twin's answer does not load: %v", rule, err)
	}
	return s
}

// plan is one rule's plan on the state, with the facts the test holds: made on
// the snapshot its read loads, which must read nothing else, and which must be
// the plan made on the whole sprint.
func (f *fleetT) plan(rule string, texts ...string) RulePlan {
	f.t.Helper()
	ks := agendaOf(texts...)
	s := f.loaded(rule, ks)
	rp := f.planOn(rule, s, ks)
	if err := s.UnloadedErr(); err != nil {
		f.t.Fatalf("%s: the plan read what its read did not load: %v", rule, err)
	}
	if !f.partialOnly {
		samePlan(f.t, rule, rp, f.planOn(rule, f.snap(), ks))
	}
	return rp
}

// run plans and applies.
func (f *fleetT) run(rule string, texts ...string) RulePlan {
	f.t.Helper()
	rp := f.plan(rule, texts...)
	f.apply(rp)
	return rp
}

// failedGuard is what X does at apply, on the state a plan meets: the first
// guard that does not hold, which refuses the step XGUARD, as text; "" when
// every guard holds.
func (f *fleetT) failedGuard(rp RulePlan) string {
	f.t.Helper()
	s := f.snap()
	for _, g := range rp.Guards {
		switch g.Kind {
		case guardMemberUp:
			if ctl := s.MemberCtl(g.Member); ctl == nil || ctl.F("status") != Up {
				return "member " + g.Member + " is not up"
			}
		case guardBeatStale:
			if fleetBeatFresh(f.facts, g.Member, f.now.R) {
				return g.Member + " has a beat above R"
			}
		case guardStranger:
			if f.facts.Strangers[g.Member] {
				return g.Member + " is already noticed"
			}
		case guardCount:
			if n := s.Fleet.Count(g.Member, Ready); int64(n) > g.Score {
				return g.Member + " holds " + itoa(n) + " ready, the read saw " + strconv.FormatInt(g.Score, 10)
			}
		case guardRCount:
			idx, most, _ := strings.Cut(g.Key, " ")
			stream := strings.TrimPrefix(idx, "sent:")
			hi, err := strconv.ParseFloat(most, 64)
			if err != nil {
				f.t.Fatalf("guard %q: %v", g.Key, err)
			}
			for _, c := range s.Work.Cell(stream, Waiting) {
				if IsSentinel(c) && c.Score <= hi {
					return "sentinel " + c.ID + " is at or below " + most
				}
			}
		default:
			f.t.Fatalf("a guard of kind %q", g.Kind)
		}
	}
	return ""
}

// failedExpect is what layer 1 does at apply, on the state a plan meets: the
// first entry whose card is not at the place or the revision the plan read it
// at, as text; "" when every entry holds. It changes nothing, where applying
// the plan would fail the test.
func (f *fleetT) failedExpect(rp RulePlan) string {
	for _, u := range rp.Plan.Units {
		for _, ch := range u.Changes {
			e := ch.Entry
			if e.Expect == nil || e.Expect.Absent {
				continue
			}
			c := f.snap().T(ch.Table).Card(e.ID)
			switch {
			case c == nil:
				return ch.Table + " " + e.ID + " is gone"
			case e.Expect.Revision != "" && e.Expect.Revision != u64(c.Rev):
				return ch.Table + " " + e.ID + " is at revision " + u64(c.Rev) + ", the plan read " + e.Expect.Revision
			case e.Expect.Place != nil && (e.Expect.Place.Row != c.Row || e.Expect.Place.Col != c.Col):
				return ch.Table + " " + e.ID + " moved to " + c.Row + ":" + c.Col
			}
		}
	}
	return ""
}

// apply checks the guards, applies the plan to the tables, lets J answer the
// notes, and settles the keys.
func (f *fleetT) apply(rp RulePlan) {
	f.t.Helper()
	if why := f.failedGuard(rp); why != "" {
		f.t.Fatalf("XGUARD: %s", why)
	}
	f.w.must(rp.Plan)
	for _, g := range rp.Guards {
		if g.Kind == guardStranger {
			f.facts.Strangers[g.Member] = true // X marks it noticed in the same step
		}
	}
	f.askJ(rp.Notes)
	for _, k := range rp.Done {
		delete(f.agenda, k.Key)
		delete(f.held, k.Key)
	}
	for _, k := range rp.Requeue {
		f.agenda[k.Key] = true
	}
	for _, k := range rp.HeldBack {
		f.agenda[k.Key] = true
		f.held[k.Key] = true
	}
}

func (f *fleetT) askJ(reqs []NoteReq) {
	for _, r := range reqs {
		switch r.Op {
		case "know":
			f.lines = append(f.lines, r)
		case "open":
			var opened bool
			for _, sub := range r.Subjects {
				k := sub + "|" + r.Type + "|" + r.Cause
				if _, there := f.jopen[k]; there {
					continue // one per cause, and a hold keeps the cause closed
				}
				f.seq++
				f.jopen[k] = "n" + itoa(f.seq)
				opened = true
			}
			if opened {
				f.lines = append(f.lines, r)
			}
		case "close":
			var closed bool
			for _, sub := range r.Subjects {
				k := sub + "|" + r.Type + "|" + r.Cause
				if _, there := f.jopen[k]; there {
					delete(f.jopen, k)
					closed = true
				}
			}
			if closed {
				f.lines = append(f.lines, r)
			}
		default:
			f.t.Fatalf("a note request of op %q", r.Op)
		}
	}
}

// linesOf is the notes J wrote a line for, of a type.
func (f *fleetT) linesOf(typ string) []NoteReq {
	var out []NoteReq
	for _, l := range f.lines {
		if l.Type == typ {
			out = append(out, l)
		}
	}
	return out
}

// edit changes a card's fields where it is, as a step outside the rules did.
func (f *fleetT) edit(tb *Table, id string, set map[string]string, unset ...string) *Card {
	f.t.Helper()
	c := tb.Card(id)
	if c == nil {
		f.t.Fatalf("no card %s in %s", id, tb.Name)
	}
	for k, v := range set {
		c.Fields[k] = v
	}
	for _, k := range unset {
		delete(c.Fields, k)
	}
	c.Rev++
	return c
}

// place moves a card to a place, as a step outside the rules did.
func (f *fleetT) place(tb *Table, id, row, col string) {
	f.t.Helper()
	c := tb.Card(id)
	if c == nil {
		f.t.Fatalf("no card %s in %s", id, tb.Name)
	}
	c.Row, c.Col = row, col
	c.Rev++
	tb.cells, tb.byPrimary = nil, nil
}

// putSentinel places a sentinel, waiting, at a score.
func (f *fleetT) putSentinel(stream, id string, score float64) {
	f.snap().Work.Put(&Card{ID: id, Row: stream, Col: Waiting, Score: score, Rev: 1,
		Fields: map[string]string{"kind": Sentinel, "stream": stream}})
}

// setMember sets a member's status, and its hold when held is not "".
func (f *fleetT) setMember(m, status, held string) {
	f.t.Helper()
	ctl := f.snap().MemberCtl(m)
	if ctl == nil {
		f.t.Fatalf("no member %s", m)
	}
	ctl.Fields["status"] = status
	if held != "" {
		ctl.Fields["held"] = held
	}
	ctl.Rev++
}

// take is a worker's take of a member's card as the tick of the design has it
// (1.2): working, first_taken_r set, its unfinished due set, and the untaken
// clock's fields unset.
func (f *fleetT) take(card string) {
	f.t.Helper()
	c := f.snap().Fleet.Card(card)
	f.w.must(Take(f.snap(), TakeReq{As: c.Row, Sel: Sel{IDs: []string{card}}, Gens: map[string]int{card: c.Int("gen")}}))
	f.edit(f.snap().Fleet, card, map[string]string{
		fleetFieldFirstTakenR:   fleetMs(f.now.R - 60_000),
		fleetFieldDueUnfinished: fleetMs(f.now.R - 60_000 + 2*3_600_000),
	}, fleetFieldUntakenR, fleetFieldDueUntaken)
}

// quiet fails unless a plan on the same keys, after the first was applied,
// writes nothing and raises nothing: no unit, no row, no intent, no guard, no
// note, and every key removed.
func quiet(t *testing.T, what string, rp RulePlan, texts ...string) {
	t.Helper()
	if len(rp.Plan.Units) != 0 || len(rp.Plan.Rows) != 0 || len(rp.Plan.Refused) != 0 || len(rp.Intents) != 0 ||
		len(rp.Guards) != 0 || len(rp.Notes) != 0 || len(rp.Requeue) != 0 || len(rp.HeldBack) != 0 || len(rp.Quarantine) != 0 {
		t.Fatalf("%s: the second step is not empty: %+v", what, rp)
	}
	var done []string
	for _, k := range rp.Done {
		done = append(done, k.Key)
	}
	if strings.Join(done, ",") != strings.Join(texts, ",") {
		t.Fatalf("%s: the second step removed %v, want %v", what, done, texts)
	}
}

func unitFor(t *testing.T, rp RulePlan, key string) Unit {
	t.Helper()
	for _, u := range rp.Plan.Units {
		if u.Key == key {
			return u
		}
	}
	t.Fatalf("no unit for %s in %+v", key, rp.Plan.Units)
	return Unit{}
}

func hasUnit(rp RulePlan, key string) bool {
	for _, u := range rp.Plan.Units {
		if u.Key == key {
			return true
		}
	}
	return false
}

func guardsOf(rp RulePlan, kind string) []XGuard {
	var out []XGuard
	for _, g := range rp.Guards {
		if g.Kind == kind {
			out = append(out, g)
		}
	}
	return out
}

func keyTexts(ks []AgendaKey) string {
	var out []string
	for _, k := range ks {
		out = append(out, k.Key)
	}
	return strings.Join(out, ",")
}

// ---- R1 seen

// R1 brings a member that is down up, guarded on its control card as read, and
// leaves a member that is held held, whatever it beats, and one already up
// alone: a held member is released only by fleet up.
func TestSeenGuardsDownNotHeld(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 0, "m1", "m2", "m3", "m4")
	f.setMember("m2", Down, "")
	f.setMember("m3", Held, "")
	f.setMember("m4", Down, "2030-01-02T03:04:05Z") // held as the record carried it before: down, and a hold
	f.jopen[sprintSubject+"|"+NNoMember+"|"+causeNoMember] = "n0"

	ctl := f.snap().MemberCtl("m2")
	rp := f.plan(ruleSeen, "seen:m1", "seen:m2", "seen:m3", "seen:m4")
	if len(rp.Plan.Units) != 1 || rp.Plan.Units[0].Key != ctl.ID {
		t.Fatalf("only m2 comes up: %+v", rp.Plan.Units)
	}
	e := rp.Plan.Units[0].Changes[0].Entry
	if e.Set["status"] != Up || e.Expect == nil || e.Expect.Revision != u64(ctl.Rev) || e.Expect.Place == nil ||
		e.Expect.Place.Row != "m2" || e.Expect.Place.Col != Ctl {
		t.Fatalf("m2's entry is not guarded on the control card as read, or does not set it up: %+v", e)
	}
	if keyTexts(rp.Done) != "seen:m1,seen:m2,seen:m3,seen:m4" || len(rp.Requeue) != 0 || len(rp.HeldBack) != 0 {
		t.Fatalf("keys: done %s requeue %d held %d", keyTexts(rp.Done), len(rp.Requeue), len(rp.HeldBack))
	}
	if len(rp.Guards) != 0 {
		t.Fatalf("a member's control card is guarded by its revision alone: %+v", rp.Guards)
	}
	if len(rp.Notes) != 2 || rp.Notes[0].Type != NMemberUp || rp.Notes[0].Op != "know" || strings.Join(rp.Notes[0].Subjects, ",") != "m2" ||
		rp.Notes[1].Type != NNoMember || rp.Notes[1].Op != "close" {
		t.Fatalf("notes: %+v", rp.Notes)
	}

	f.apply(rp)
	if st := f.snap().MemberCtl("m2").F("status"); st != Up {
		t.Fatalf("m2 is %s", st)
	}
	if st := f.snap().MemberCtl("m3").F("status"); st != Held {
		t.Fatalf("m3 was released: %s", st)
	}
	if c := f.snap().MemberCtl("m4"); c.F("status") != Down || c.F("held") == "" {
		t.Fatalf("m4's hold was undone: %+v", c.Fields)
	}
	if len(f.jopen) != 0 {
		t.Fatalf("\"no fleet member is up\" was not closed: %v", f.jopen)
	}
}

// A stranger, a machine that beats and is in no fleet row, is noticed once: the
// step is guarded on {p}strangers, and once it is marked noticed a second
// delivery writes nothing.
func TestSeenStrangerNoticedOnce(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 0, "m1")
	rp := f.plan(ruleSeen, "seen:zed")
	if len(rp.Plan.Units) != 0 {
		t.Fatalf("a stranger has no card to change: %+v", rp.Plan.Units)
	}
	g := guardsOf(rp, guardStranger)
	if len(g) != 1 || g[0].Member != "zed" || g[0].Key != strangersKey {
		t.Fatalf("stranger guards: %+v", rp.Guards)
	}
	if len(rp.Notes) != 1 || rp.Notes[0].Type != NUnknownMachine || rp.Notes[0].Op != "know" ||
		!strings.Contains(rp.Notes[0].Text, "zed") || !strings.Contains(rp.Notes[0].Text, "fleet up zed") {
		t.Fatalf("the notice: %+v", rp.Notes)
	}
	f.apply(rp)
	// the second delivery, with the mark as X wrote it
	quiet(t, "stranger", f.run(ruleSeen, "seen:zed"), "seen:zed")
	if n := len(f.linesOf(NUnknownMachine)); n != 1 {
		t.Fatalf("the notice was written %d times", n)
	}
}

// ---- R2 down

// A held member stays held: its cards are dealt again to the members that are
// up, its status is not written, no beat is looked for, and only fleet up
// releases it.
func TestDownLeavesHeldHeld(t *testing.T) {
	t.Parallel()
	for _, held := range []struct{ status, hold string }{{Held, ""}, {Down, "2030-01-02T03:04:05Z"}} {
		f := newFleetT(t, 4, "m1", "m2")
		f.run(ruleDeal, "deal")
		f.setMember("m1", held.status, held.hold)
		rp := f.plan(ruleDown, "down:m1")
		ctl := f.snap().MemberCtl("m1")
		for _, u := range rp.Plan.Units {
			if u.Key == ctl.ID && (u.Changes[0].Entry.Set != nil || u.Changes[0].Entry.Move != nil) {
				t.Fatalf("a held member's status was written: %+v", u)
			}
		}
		// its control card is guarded, not written, and the two cards are dealt away
		if len(rp.Plan.Units) != 3 || rp.Plan.Units[0].Key != ctl.ID || len(guardsOf(rp, guardBeatStale)) != 0 || len(f.linesOf(NMemberDown)) != 0 {
			t.Fatalf("units %d, beatstale %d", len(rp.Plan.Units), len(guardsOf(rp, guardBeatStale)))
		}
		for _, n := range rp.Notes {
			if n.Type == NMemberDown {
				t.Fatalf("the member was not marked down by this step: %+v", n)
			}
		}
		f.apply(rp)
		if c := f.snap().MemberCtl("m1"); c.F("status") != held.status || c.F("held") != held.hold {
			t.Fatalf("m1 after down: %+v", c.Fields)
		}
		if n := f.snap().Fleet.Count("m1", Ready) + f.snap().Fleet.Count("m1", Working); n != 0 {
			t.Fatalf("m1 still holds %d cards", n)
		}
		if n := f.snap().Fleet.Count("m2", Ready); n != 4 {
			t.Fatalf("m2 holds %d, want 4", n)
		}
		quiet(t, "held", f.run(ruleDown, "down:m1"), "down:m1")
	}
}

// A card that was ready keeps its redeals and its untaken clock when its member
// goes down: it was never taken. A card that was working ended a take without
// a finish: its count is raised, its unfinished clock ends and the untaken
// clock starts again at R. With no member up the counts are the same, at the
// withdrawal, and the later deal from withdrawn does not count again.
func TestDownRedealCountsOnlyTakes(t *testing.T) {
	t.Parallel()
	for _, nobody := range []bool{false, true} {
		f := newFleetT(t, 4, "m1", "m2").roomy()
		f.run(ruleDeal, "deal")
		// m1 holds s1-1 and s1-3; s1-1 is taken; both have been dealt again twice
		f.take("s1-1.w1")
		f.edit(f.snap().Fleet, "s1-1.w1", map[string]string{"redeals": "2"})
		f.edit(f.snap().Fleet, "s1-3.w1", map[string]string{"redeals": "2"})
		untaken := f.snap().Fleet.Card("s1-3.w1").F(fleetFieldUntakenR)
		due := f.snap().Fleet.Card("s1-3.w1").F(fleetFieldDueUntaken)
		if untaken == "" || due == "" {
			t.Fatalf("the deal did not start the untaken clock")
		}
		f.now.R += 500_000 // the beats lapsed 500 s of running time after the deal
		down := []string{"down:m1"}
		if nobody {
			down = append(down, "down:m2") // both marked down by the one step: nobody is left to deal to
		}
		rp := f.plan(ruleDown, down...)
		if len(guardsOf(rp, guardBeatStale)) != len(down) {
			t.Fatalf("beatstale guards: %+v", rp.Guards)
		}
		up, count := guardsOf(rp, guardMemberUp), guardsOf(rp, guardCount)
		switch {
		case nobody && (len(up) != 0 || len(count) != 0):
			t.Fatalf("nobody receives, so nothing guards a receiver: %+v", rp.Guards)
		case !nobody && (len(up) != 1 || up[0].Member != "m2" || len(count) != 1 || count[0].Key != "m2:ready" || count[0].Score != 2):
			t.Fatalf("m2 receives: it must be up and hold no more than the 2 ready it held: %+v", rp.Guards)
		}
		f.apply(rp)

		working := f.snap().Fleet.Card("s1-1.w1")
		ready := f.snap().Fleet.Card("s1-3.w1")
		if working.Int("redeals") != 3 {
			t.Fatalf("nobody=%v: a take that ended counts: redeals %d", nobody, working.Int("redeals"))
		}
		if ready.Int("redeals") != 2 || ready.F(fleetFieldUntakenR) != untaken || ready.F(fleetFieldDueUntaken) != due {
			t.Fatalf("nobody=%v: a card never taken keeps its count and its clock: %+v", nobody, ready.Fields)
		}
		if working.F(fleetFieldUntakenR) != fleetMs(f.now.R) || working.F(fleetFieldDueUntaken) != fleetMs(f.now.R+15*60_000) {
			t.Fatalf("nobody=%v: the untaken clock did not start again at R: %+v", nobody, working.Fields)
		}
		for _, name := range []string{fleetFieldFirstTakenR, fleetFieldDueUnfinished, "taken"} {
			if _, ok := working.Fields[name]; ok {
				t.Fatalf("nobody=%v: %s survived the end of the take", nobody, name)
			}
		}
		if working.Int("gen") != 2 || ready.Int("gen") != 2 {
			t.Fatalf("nobody=%v: generations %d and %d", nobody, working.Int("gen"), ready.Int("gen"))
		}
		if nobody {
			if working.Col != Withdrawn || ready.Col != Withdrawn {
				t.Fatalf("with nobody up the cards are withdrawn: %s, %s", working.Col, ready.Col)
			}
			for _, p := range []string{"s1-1", "s1-3"} {
				if st := f.w.state(p); st != Ready {
					t.Fatalf("%s is %s, want ready", p, st)
				}
				if _, ok := f.snap().Work.Card(p).Fields["work"]; ok {
					t.Fatalf("%s still names its work card", p)
				}
			}
			if n := f.linesOf(NWithdrawn); len(n) != 1 || strings.Join(n[0].Subjects, ",") != "s1-1,s1-3,s1-2,s1-4" {
				t.Fatalf("the notice of the return to ready: %+v", n)
			}
			// the deal from withdrawn, later, does not count again
			f.setMember("m2", Up, "")
			f.run(ruleDeal, "deal")
			if c := f.snap().Fleet.Card("s1-1.w1"); c.Int("redeals") != 3 || c.Col != Ready {
				t.Fatalf("dealt again from withdrawn: redeals %d at %s", c.Int("redeals"), c.Col)
			}
		} else {
			if working.Row != "m2" || ready.Row != "m2" || working.Col != Ready || ready.Col != Ready {
				t.Fatalf("dealt again to m2's ready: %s:%s and %s:%s", working.Row, working.Col, ready.Row, ready.Col)
			}
			if st := f.w.state("s1-1"); st != Working {
				t.Fatalf("its primary stays working: %s", st)
			}
			if k := f.linesOf(NMemberDown); len(k) != 1 || !strings.Contains(k[0].Text, "2 redealt, 0 withdrawn") {
				t.Fatalf("the notice: %+v", k)
			}
		}
	}
}

// A card past the bound is not dealt again: a working card whose count is the
// bound goes to withdrawn, its primary back to ready with bound = redeals, and
// the judgment names the primary. The count is not raised past the bound.
func TestDownAtBoundWithdraws(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 2, "m1", "m2")
	f.run(ruleDeal, "deal")
	f.take("s1-1.w1")
	f.edit(f.snap().Fleet, "s1-1.w1", map[string]string{"redeals": itoa(RuleMaxRedeals)})
	f.now.R += 500_000
	rp := f.plan(ruleDown, "down:m1")
	f.apply(rp)

	c := f.snap().Fleet.Card("s1-1.w1")
	if c.Col != Withdrawn || c.Int("redeals") != RuleMaxRedeals {
		t.Fatalf("at its bound the card is withdrawn with its count: %s redeals %d", c.Col, c.Int("redeals"))
	}
	pr := f.snap().Work.Card("s1-1")
	if pr.Col != Ready || pr.F("bound") != boundRedeals {
		t.Fatalf("the primary is %s, bound %q", pr.Col, pr.F("bound"))
	}
	if _, ok := pr.Fields["work"]; ok {
		t.Fatalf("the primary still names the withdrawn card")
	}
	if n := f.linesOf(NBound); len(n) != 1 || strings.Join(n[0].Subjects, ",") != "s1-1" || n[0].Cause != boundRedeals || n[0].Op != "open" {
		t.Fatalf("the bound judgment: %+v", n)
	}
	// out of again: R6 does not deal it, though m2 has room
	if rp := f.plan(ruleDeal, "deal"); hasUnit(rp, "s1-1") {
		t.Fatalf("R6 deals a primary at its bound: %+v", rp.Plan.Units)
	}
	quiet(t, "bound", f.run(ruleDown, "down:m1"), "down:m1")
}

// R2 reads the beat's freshness from the due set in running time, never from a
// wall stamp. A member the machine was STOPPED an hour with, and that has been
// silent since the stop, is found down 15 s of running time after start: its
// entry beat:<m> was moved to R + 15 s by its last beat, R stood still for the
// hour, and the wall clock never enters.
func TestDownBeatStaleGuardOnDueSet(t *testing.T) {
	t.Parallel()
	const sec = 1000
	// the clock of 1.2: R(t) = t - stopped - (t - since when STOPPED)
	running := func(stopped, since, wall int64) int64 {
		r := wall - stopped
		if since != 0 {
			r -= wall - since
		}
		return r
	}
	f := newFleetT(t, 2, "m1", "m2")
	f.run(ruleDeal, "deal")
	wall0 := f.now.Wall
	rStop := running(0, 0, wall0)
	// the last beat, at the moment of the stop: beat:m1 = R + 15 s
	f.facts.BeatDue["m1"] = rStop + 15*sec
	// an hour STOPPED: R has not moved; then start, and 15 s less a ms pass
	stopped := int64(3600 * sec)
	wallStart := wall0 + stopped
	if r := running(0, wall0, wallStart-1); r != rStop {
		t.Fatalf("R moved while STOPPED: %d, want %d", r, rStop)
	}
	for _, c := range []struct {
		after int64 // ms of running time after start
		down  bool
	}{{0, false}, {15*sec - 1, false}, {15 * sec, true}, {15*sec + 1, true}} {
		wall := wallStart + c.after
		f.now = Now{R: running(stopped, 0, wall), Wall: wall, Running: true}
		rp := f.plan(ruleDown, "down:m1")
		got := len(guardsOf(rp, guardBeatStale)) == 1
		if got != c.down {
			t.Fatalf("%d ms after start: marked down %v, want %v (R %d, beat %d)", c.after, got, c.down, f.now.R, f.facts.BeatDue["m1"])
		}
		if !c.down {
			quiet(t, "fresh beat", rp, "down:m1")
			continue
		}
		g := rp.Guards[0]
		if g.Member != "m1" || g.Key != "beat:m1" || g.Score != f.facts.BeatDue["m1"] {
			t.Fatalf("the beatstale guard: %+v", g)
		}
		break
	}
	// A beat raced the pop and moved the entry to R + 15 s: nothing is dealt.
	f.facts.BeatDue["m1"] = f.now.R + 15*sec
	quiet(t, "raced by a beat", f.plan(ruleDown, "down:m1"), "down:m1")
}

// A member of more cards than the chunk is dealt from a chunk at a time, the
// lowest scores first, and the key stays queued for the rest.
func TestDownCutsAtTheChunkAndKeepsKey(t *testing.T) {
	t.Parallel()
	f := newFleetOn(t, &world{t: t, s: benchDownT(t, fleetChunk+50)})
	rp := f.plan(ruleDown, "down:m1")
	if len(rp.Plan.Units) != fleetChunk+1 { // the control card, and a chunk of cards
		t.Fatalf("units %d, want %d", len(rp.Plan.Units), fleetChunk+1)
	}
	if keyTexts(rp.Requeue) != "down:m1" || len(rp.Done) != 0 || len(rp.HeldBack) != 0 {
		t.Fatalf("a cut plan's key: done %s requeue %s held %s", keyTexts(rp.Done), keyTexts(rp.Requeue), keyTexts(rp.HeldBack))
	}
	// the lowest scores first: p0 .. p1999 are dealt, p2000 .. p2049 are not
	for i := 0; i < fleetChunk+50; i++ {
		if got, want := hasUnit(rp, "p"+itoa(i)+".w1"), i < fleetChunk; got != want {
			t.Fatalf("p%d dealt: %v, want %v", i, got, want)
		}
	}
	// exactly a chunk is not a cut
	f = newFleetOn(t, &world{t: t, s: benchDownT(t, fleetChunk)})
	rp = f.plan(ruleDown, "down:m1")
	if keyTexts(rp.Done) != "down:m1" || len(rp.Requeue) != 0 {
		t.Fatalf("a chunk of cards is all of them: done %s requeue %s", keyTexts(rp.Done), keyTexts(rp.Requeue))
	}
}

// A card of a stream being dropped is left where it is, X would refuse it
// DROPPING, and the key stays: held back when that was all it found, kept when
// it dealt other cards too.
func TestDownSkipsDroppingKeepsKey(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 2, "m1", "m2")
	f.w.must(Add(f.snap(), AddReq{Stream: "s2", Count: 2}))
	f.run(ruleDeal, "deal") // s1-1, s1-2, s2-1, s2-2: two on each member
	f.facts.Dropping["s1"] = true
	f.now.R += 500_000
	rp := f.plan(ruleDown, "down:m1")
	var dealt []string
	for _, u := range rp.Plan.Units {
		dealt = append(dealt, u.Key)
	}
	for _, id := range dealt {
		if strings.HasPrefix(id, "s1-") {
			t.Fatalf("a card of a stream being dropped was dealt: %v", dealt)
		}
	}
	if !hasUnit(rp, "s2-1.w1") && !hasUnit(rp, "s2-2.w1") {
		t.Fatalf("the other stream's card was not dealt: %v", dealt)
	}
	if keyTexts(rp.Requeue) != "down:m1" || len(rp.Done) != 0 || len(rp.HeldBack) != 0 {
		t.Fatalf("done %s requeue %s held %s", keyTexts(rp.Done), keyTexts(rp.Requeue), keyTexts(rp.HeldBack))
	}
	f.apply(rp)
	// only the frozen stream's cards are left: held back
	rp = f.plan(ruleDown, "down:m1")
	if len(rp.Plan.Units) != 0 || keyTexts(rp.HeldBack) != "down:m1" || len(rp.Done) != 0 || len(rp.Requeue) != 0 {
		t.Fatalf("the key of a frozen stream's cards: %+v", rp)
	}
	// the mark clears: they are dealt, and the key goes
	f.facts.Dropping = map[string]bool{}
	rp = f.run(ruleDown, "down:m1")
	if keyTexts(rp.Done) != "down:m1" || f.snap().Fleet.Count("m1", Ready) != 0 {
		t.Fatalf("after the mark cleared: done %s, m1 holds %d", keyTexts(rp.Done), f.snap().Fleet.Count("m1", Ready))
	}
}

// ---- R6 deal

// A primary whose avoid names a member goes to another member when one has room,
// and to the avoided member only when no other has.
func TestDealAvoidsMemberWhenOtherHasRoom(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 1, "m1", "m2")
	f.edit(f.snap().Work, "s1-1", map[string]string{"avoid": "m1", "attempt": "1", "fix": "the finding"})
	f.place(f.snap().Work, "s1-1", "s1", Ready)
	// both have room: the card goes to m2, though m1 comes first
	f.run(ruleDeal, "deal") // no withdrawn card: the next attempt's card is cut
	c := f.snap().Fleet.Card("s1-1.w2")
	if c == nil || c.Row != "m2" || c.F("fix") != "the finding" {
		t.Fatalf("dealt to %+v", c)
	}

	// m2 full: the avoided member takes it
	g := newFleetT(t, 3, "m1", "m2")
	g.edit(g.snap().Work, "s1-3", map[string]string{"avoid": "m1", "attempt": "1"})
	g.place(g.snap().Work, "s1-3", "s1", Ready)
	// m2 holds two ready cards, m1 none
	for i, id := range []string{"x1", "x2"} {
		g.snap().Fleet.Put(&Card{ID: id + ".w1", Row: "m2", Col: Ready, Score: float64(50 + i), Rev: 1,
			Fields: map[string]string{"kind": "work", "primary": id, "stream": "s1", "attempt": "1", "gen": "1", "member": "m2"}})
	}
	g.place(g.snap().Work, "s1-1", "s1", Working)
	g.place(g.snap().Work, "s1-2", "s1", Working)
	rp := g.run(ruleDeal, "deal")
	if c := g.snap().Fleet.Card("s1-3.w2"); c == nil || c.Row != "m1" {
		t.Fatalf("only the avoided member has room: %+v", c)
	}
	if len(rp.Plan.Units) != 1 {
		t.Fatalf("units: %d", len(rp.Plan.Units))
	}
}

// A fresh card is dealt only below the first sentinel, and the deal guards it:
// no sentinel of the stream at or below the highest fresh score dealt, since
// the read. A card dealt again, one that was dealt before, needs no such guard.
func TestDealSentinelRcountGuard(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 4, "m1", "m2", "m3")
	f.putSentinel("s1", "gate", 2.5) // s1-3 and s1-4 are ready above it, not yet pulled back
	rp := f.plan(ruleDeal, "deal")
	if hasUnit(rp, "s1-3") || hasUnit(rp, "s1-4") || !hasUnit(rp, "s1-1") || !hasUnit(rp, "s1-2") {
		t.Fatalf("dealt: %+v", rp.Plan.Units)
	}
	g := guardsOf(rp, guardRCount)
	if len(g) != 1 || g[0].Key != "sent:s1 2" {
		t.Fatalf("the sentinel guard: %+v, want sent:s1 2 (the highest fresh score dealt)", g)
	}
	// a sentinel inserted at 1.5 since the read: the step is refused
	f.putSentinel("s1", "gate2", 1.5)
	if why := f.failedGuard(rp); !strings.Contains(why, "gate2") {
		t.Fatalf("a sentinel placed before a card dealt did not refuse the plan: %q", why)
	}

	// again cards are not held to σ: no sentinel guard for them
	h := newFleetT(t, 2, "m1", "m2")
	h.putSentinel("s1", "gate", 0.5)
	h.edit(h.snap().Work, "s1-1", map[string]string{"attempt": "1"})
	h.place(h.snap().Work, "s1-1", "s1", Ready)
	rp = h.plan(ruleDeal, "deal")
	if !hasUnit(rp, "s1-1") || len(guardsOf(rp, guardRCount)) != 0 {
		t.Fatalf("units %v, sentinel guards %+v", rp.Plan.Units, rp.Guards)
	}
}

// Every receiver must be up at apply, and no more than it held when read: one
// memberup and one count guard for each member a card is dealt to, and none for
// a member that gets nothing.
func TestDealMemberUpGuard(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 2, "m1", "m2", "m3")
	rp := f.plan(ruleDeal, "deal")
	var up, count []string
	for _, g := range rp.Guards {
		switch g.Kind {
		case guardMemberUp:
			up = append(up, g.Member)
		case guardCount:
			count = append(count, g.Member+"="+strconv.FormatInt(g.Score, 10)+"@"+g.Key)
		}
	}
	if strings.Join(up, ",") != "m1,m2" || strings.Join(count, ",") != "m1=0@m1:ready,m2=0@m2:ready" {
		t.Fatalf("memberup %v, count %v", up, count)
	}
	// a receiver marked down since the read refuses the step
	f.setMember("m2", Down, "")
	if why := f.failedGuard(rp); !strings.Contains(why, "m2 is not up") {
		t.Fatalf("a receiver that is down did not refuse the step: %q", why)
	}
}

// With no member up and something to deal, one judgment is asked of J, not one
// for each card, and J opens it once: a second run asks the same and writes
// nothing; a hold on it keeps it closed.
func TestDealNoMemberOnce(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 3, "m1", "m2")
	f.setMember("m1", Down, "")
	f.setMember("m2", Down, "")
	rp := f.plan(ruleDeal, "deal")
	if len(rp.Plan.Units) != 0 || len(rp.Guards) != 0 {
		t.Fatalf("with nobody up nothing is dealt: %+v", rp)
	}
	if len(rp.Notes) != 1 || rp.Notes[0].Op != "open" || rp.Notes[0].Type != NNoMember ||
		strings.Join(rp.Notes[0].Subjects, ",") != sprintSubject {
		t.Fatalf("notes: %+v", rp.Notes)
	}
	if keyTexts(rp.Done) != "deal" {
		t.Fatalf("the key stays with nothing to do: %+v", rp)
	}
	f.apply(rp)
	f.run(ruleDeal, "deal")
	f.run(ruleDeal, "deal")
	if n := f.linesOf(NNoMember); len(n) != 1 {
		t.Fatalf("the judgment was opened %d times", len(n))
	}
	// a hold: J raises nothing of the cause
	g := newFleetT(t, 3, "m1")
	g.setMember("m1", Down, "")
	g.jopen[sprintSubject+"|"+NNoMember+"|"+causeNoMember] = "h1"
	g.run(ruleDeal, "deal")
	if n := g.linesOf(NNoMember); len(n) != 0 {
		t.Fatalf("a held judgment was opened: %+v", n)
	}
	// nothing to deal, nobody up: nothing asked
	h := newFleetT(t, 0, "m1")
	h.setMember("m1", Down, "")
	quiet(t, "no work", h.run(ruleDeal, "deal"), "deal")
}

// A ready card of a stream being dropped is left alone, and the key stays: held
// back when that was all it found, kept when it dealt other cards too.
func TestDealSkipsDroppingKeepsKey(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 2, "m1", "m2")
	f.w.must(Add(f.snap(), AddReq{Stream: "s2", Count: 2}))
	f.facts.Dropping["s1"] = true
	rp := f.plan(ruleDeal, "deal")
	if hasUnit(rp, "s1-1") || hasUnit(rp, "s1-2") || !hasUnit(rp, "s2-1") || !hasUnit(rp, "s2-2") {
		t.Fatalf("units: %+v", rp.Plan.Units)
	}
	if keyTexts(rp.Requeue) != "deal" || len(rp.Done) != 0 || len(rp.HeldBack) != 0 {
		t.Fatalf("the key was dealt with as done: done %s requeue %s held %s", keyTexts(rp.Done), keyTexts(rp.Requeue), keyTexts(rp.HeldBack))
	}
	f.apply(rp)
	// what is left is the frozen stream's alone: the key is held back
	rp = f.plan(ruleDeal, "deal")
	if len(rp.Plan.Units) != 0 || keyTexts(rp.HeldBack) != "deal" || len(rp.Done) != 0 || len(rp.Requeue) != 0 {
		t.Fatalf("the key of a frozen stream's work: %+v", rp)
	}
	f.apply(rp)
	if !f.agenda["deal"] || !f.held["deal"] {
		t.Fatalf("the key is not kept held back")
	}
	// the mark clears: the frozen stream's cards are dealt, and the key goes
	f.facts.Dropping = map[string]bool{}
	rp = f.plan(ruleDeal, "deal")
	if !hasUnit(rp, "s1-1") || !hasUnit(rp, "s1-2") || keyTexts(rp.Done) != "deal" {
		t.Fatalf("after the mark cleared: units %+v, done %s", rp.Plan.Units, keyTexts(rp.Done))
	}
}

// A card dealt again from withdrawn keeps its count of redeals: its take, if
// any, was counted when it was withdrawn. It takes the next generation and its
// primary goes to working on it.
func TestDealAgainFromWithdrawnKeepsRedeals(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 1, "m1", "m2").roomy()
	f.run(ruleDeal, "deal")
	f.setMember("m1", Down, "")
	f.setMember("m2", Down, "")
	f.now.R += 100_000
	f.run(ruleDown, "down:m1", "down:m2")
	c := f.snap().Fleet.Card("s1-1.w1")
	if c.Col != Withdrawn {
		t.Fatalf("not withdrawn: %s", c.Col)
	}
	gen, redeals, untaken := c.Int("gen"), c.Int("redeals"), c.F(fleetFieldUntakenR)
	f.setMember("m2", Up, "")
	f.now.R += 100_000
	f.run(ruleDeal, "deal")
	c = f.snap().Fleet.Card("s1-1.w1")
	if c.Col != Ready || c.Row != "m2" || c.Int("gen") != gen+1 || c.Int("redeals") != redeals || c.F(fleetFieldUntakenR) != untaken {
		t.Fatalf("dealt again: %s:%s gen %d (was %d) redeals %d (was %d) untaken_r %s (was %s)", c.Row, c.Col, c.Int("gen"), gen, c.Int("redeals"), redeals, c.F(fleetFieldUntakenR), untaken)
	}
	if st := f.w.state("s1-1"); st != Working || f.snap().Work.Card("s1-1").F("work") != "s1-1.w1" {
		t.Fatalf("the primary is %s", st)
	}
}

// A primary whose next work card's id is taken by a record is refused in the
// rule's own step: refused is set on it and the judgment is asked, and the deal
// goes on with the others.
func TestDealRefusesACardWhoseWorkCardExists(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 3, "m1", "m2")
	// a work card of s1-1's first attempt exists as a kept record, off the table.
	// The read cannot ask for an id it does not know before its answer, so the
	// record reaches the plan only when the answer carries it (the twin's extra):
	// the planner refuses what it was shown, and X's absent guard refuses the rest.
	kept := &Card{ID: "s1-1.w1", Rev: 1, Fields: map[string]string{"kind": "work"}}
	f.snap().Fleet.Put(kept)
	f.extra = []TableCard{{Fleet, kept}}
	rp := f.plan(ruleDeal, "deal")
	u := unitFor(t, rp, "s1-1")
	e := u.Changes[0].Entry
	if e.Set["refused"] != "deal: work card s1-1.w1 exists already" || e.Move != nil || u.Changes[0].Table != Work {
		t.Fatalf("the refusal: %+v", e)
	}
	if len(rp.Notes) != 1 || rp.Notes[0].Type != typeCouldNotMove || rp.Notes[0].Cause != causeCouldNot ||
		strings.Join(rp.Notes[0].Subjects, ",") != "s1-1" {
		t.Fatalf("notes: %+v", rp.Notes)
	}
	if !hasUnit(rp, "s1-2") || !hasUnit(rp, "s1-3") {
		t.Fatalf("the others are dealt: %+v", rp.Plan.Units)
	}
	f.apply(rp)
	// out of fresh by its field, so a second run leaves it alone
	quiet(t, "refused", f.run(ruleDeal, "deal"), "deal")

	// A collision the read did not load is not seen by the plan: it deals the
	// card, and the create it makes is guarded absent, so X refuses it EXISTS.
	g := newFleetT(t, 1, "m1", "m2")
	g.snap().Fleet.Put(&Card{ID: "s1-1.w1", Rev: 1, Fields: map[string]string{"kind": "work"}})
	g.partialOnly = true
	rp = g.plan(ruleDeal, "deal")
	u = unitFor(t, rp, "s1-1")
	if c := u.Changes[0]; c.Table != Fleet || c.Entry.Expect == nil || !c.Entry.Expect.Absent || len(rp.Notes) != 0 {
		t.Fatalf("a work card the read did not load is created guarded absent: %+v %+v", c, rp.Notes)
	}
}

// The deal is limited by the room: 2 less each ready count, and never past it.
func TestDealNeverPastTheRoom(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 9, "m1", "m2")
	rp := f.run(ruleDeal, "deal")
	if len(rp.Plan.Units) != 2*MaxReadyPerMember {
		t.Fatalf("dealt %d, the room is %d", len(rp.Plan.Units), 2*MaxReadyPerMember)
	}
	for _, m := range []string{"m1", "m2"} {
		if n := f.snap().Fleet.Count(m, Ready); n != MaxReadyPerMember {
			t.Fatalf("%s holds %d", m, n)
		}
	}
	// the room lowest, in work order
	for _, id := range []string{"s1-1", "s1-2", "s1-3", "s1-4"} {
		if !hasUnit(rp, id) {
			t.Fatalf("%s was not dealt: %+v", id, rp.Plan.Units)
		}
	}
	if keyTexts(rp.Done) != "deal" || len(rp.Requeue) != 0 {
		t.Fatalf("room used, the key goes: done %s requeue %s", keyTexts(rp.Done), keyTexts(rp.Requeue))
	}
	// no room: nothing, and the key goes
	quiet(t, "no room", f.run(ruleDeal, "deal"), "deal")
}

// ---- R7 level

// readyCard puts a ready work card on a member, as the deal leaves one.
func readyCard(f *fleetT, id, member, stream string, score float64, extra map[string]string) {
	fields := map[string]string{"kind": "work", "primary": id, "stream": stream, "attempt": "1", "gen": "1", "member": member}
	for k, v := range extra {
		fields[k] = v
	}
	f.snap().Fleet.Put(&Card{ID: id + ".w1", Row: member, Col: Ready, Score: score, Rev: 1, Fields: fields})
}

// While the longest and shortest queues differ by more than one, the newest
// card of the longest goes to the shortest at the next generation, and the
// untaken clock and the count of redeals are left as they were.
func TestLevelNewestMoves(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 2, "m1", "m2", "m3")
	// two ready cards, both on m1 (score order s1-1, s1-2)
	for i := 1; i <= 2; i++ {
		id := "s1-" + itoa(i)
		readyCard(f, id, "m1", "s1", float64(i), map[string]string{"redeals": "1", fleetFieldUntakenR: "777", fleetFieldDueUntaken: "888"})
		f.place(f.snap().Work, id, "s1", Working)
	}
	rp := f.plan(ruleLevel, "level")
	// 2/0/0 -> the newest to the shortest (the first of the two, on the tie), and 1/1/0 stops
	if len(rp.Plan.Units) != 1 || rp.Plan.Units[0].Key != "s1-2.w1" {
		t.Fatalf("moves: %+v", rp.Plan.Units)
	}
	if e := rp.Plan.Units[0].Changes[0].Entry; e.Move == nil || e.Move.Row != "m2" || e.Move.Col != Ready {
		t.Fatalf("the move: %+v", e)
	}
	f.apply(rp)
	for m, want := range map[string]int{"m1": 1, "m2": 1, "m3": 0} {
		if n := f.snap().Fleet.Count(m, Ready); n != want {
			t.Fatalf("%s holds %d, want %d", m, n, want)
		}
	}
	moved := f.snap().Fleet.Card("s1-2.w1")
	if moved.Int("gen") != 2 || moved.F("member") != "m2" || moved.Int("redeals") != 1 ||
		moved.F(fleetFieldUntakenR) != "777" || moved.F(fleetFieldDueUntaken) != "888" {
		t.Fatalf("a moved card keeps its count and its clock: %+v", moved.Fields)
	}
	if up := guardsOf(rp, guardMemberUp); len(up) != 1 || up[0].Member != "m2" {
		t.Fatalf("memberup for the receiver alone: %+v", rp.Guards)
	}
	cg := guardsOf(rp, guardCount)
	if len(cg) != 2 || cg[0].Member != "m1" || cg[0].Score != 2 || cg[0].Key != "m1:ready" || cg[1].Member != "m2" || cg[1].Score != 0 || cg[1].Key != "m2:ready" {
		t.Fatalf("a count guard on each member whose queue changes, at what it held when read: %+v", rp.Guards)
	}
	quiet(t, "level", f.run(ruleLevel, "level"), "level")
}

// R7 moves a card only when the queues differ by more than one: a difference of
// one is level, and the threshold is the design's (2.3 R7: "more than one").
func TestLevelStopsAtADifferenceOfOne(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 0, "m1", "m2")
	readyCard(f, "p1", "m1", "s1", 1, nil)
	rp := f.plan(ruleLevel, "level")
	if len(rp.Plan.Units) != 0 || len(rp.Guards) != 0 || keyTexts(rp.Done) != "level" {
		t.Fatalf("1/0 is level: %+v", rp)
	}
	readyCard(f, "p2", "m1", "s1", 2, nil)
	rp = f.plan(ruleLevel, "level")
	if len(rp.Plan.Units) != 1 || rp.Plan.Units[0].Key != "p2.w1" {
		t.Fatalf("2/0 is not: %+v", rp.Plan.Units)
	}
	// three members: 2/1/0 differs by two, and the newest of the longest goes
	g := newFleetT(t, 0, "m1", "m2", "m3")
	readyCard(g, "p1", "m1", "s1", 1, nil)
	readyCard(g, "p2", "m1", "s1", 2, nil)
	readyCard(g, "p3", "m2", "s1", 3, nil)
	rp = g.plan(ruleLevel, "level")
	if len(rp.Plan.Units) != 1 || rp.Plan.Units[0].Key != "p2.w1" || rp.Plan.Units[0].Changes[0].Entry.Move.Row != "m3" {
		t.Fatalf("2/1/0: %+v", rp.Plan.Units)
	}
}

// A card of a stream being dropped is not moved: the newest card that is not
// frozen goes instead, and when none can move the key is held back.
func TestLevelSkipsDroppingKeepsKey(t *testing.T) {
	t.Parallel()
	mk := func() *fleetT {
		f := newFleetT(t, 0, "m1", "m2")
		readyCard(f, "p1", "m1", "s1", 1, nil)
		readyCard(f, "p2", "m1", "s2", 2, nil) // the newest is of another stream
		return f
	}
	f := mk()
	f.facts.Dropping["s2"] = true
	rp := f.plan(ruleLevel, "level")
	// 2/0: the newest, p2, is frozen; p1 goes, and 1/1 is level
	if len(rp.Plan.Units) != 1 || rp.Plan.Units[0].Key != "p1.w1" {
		t.Fatalf("moves: %+v", rp.Plan.Units)
	}
	if keyTexts(rp.Requeue) != "level" || len(rp.Done) != 0 {
		t.Fatalf("done %s requeue %s", keyTexts(rp.Done), keyTexts(rp.Requeue))
	}
	// everything frozen: nothing moves, the key is held back
	g := mk()
	g.facts.Dropping["s1"], g.facts.Dropping["s2"] = true, true
	rp = g.plan(ruleLevel, "level")
	if len(rp.Plan.Units) != 0 || keyTexts(rp.HeldBack) != "level" || len(rp.Done) != 0 || len(rp.Requeue) != 0 {
		t.Fatalf("a frozen queue: %+v", rp)
	}
}

// ---- all four, twice

// Every rule run a second time on the same keys, with nothing changed between,
// writes nothing and raises nothing (E7, ReplayNoop): its moves would fail
// their place, its creates their absence, its notes J's one per cause.
func TestFleetRulesTwiceSecondEmpty(t *testing.T) {
	t.Parallel()

	t.Run("seen", func(t *testing.T) {
		t.Parallel()
		f := newFleetT(t, 0, "m1", "m2")
		f.setMember("m2", Down, "")
		f.run(ruleSeen, "seen:m2", "seen:zed", "seen:m1")
		quiet(t, "seen", f.plan(ruleSeen, "seen:m2", "seen:zed", "seen:m1"), "seen:m2", "seen:zed", "seen:m1")
	})
	t.Run("down redeal", func(t *testing.T) {
		t.Parallel()
		f := newFleetT(t, 4, "m1", "m2")
		f.run(ruleDeal, "deal")
		f.take("s1-1.w1")
		f.now.R += 500_000
		f.run(ruleDown, "down:m1")
		quiet(t, "down", f.plan(ruleDown, "down:m1"), "down:m1")
	})
	t.Run("down withdraw", func(t *testing.T) {
		t.Parallel()
		f := newFleetT(t, 4, "m1", "m2").roomy()
		f.run(ruleDeal, "deal")
		f.setMember("m2", Down, "")
		f.now.R += 500_000
		f.run(ruleDown, "down:m1", "down:m2")
		quiet(t, "down withdraw", f.plan(ruleDown, "down:m1", "down:m2"), "down:m1", "down:m2")
	})
	t.Run("deal", func(t *testing.T) {
		t.Parallel()
		f := newFleetT(t, 4, "m1", "m2")
		f.run(ruleDeal, "deal")
		quiet(t, "deal", f.plan(ruleDeal, "deal"), "deal")
	})
	t.Run("deal with room left", func(t *testing.T) {
		t.Parallel()
		f := newFleetT(t, 2, "m1", "m2")
		rp := f.run(ruleDeal, "deal")
		// room 4, two cards: nothing more is dealable, and the next plan finds it so
		if keyTexts(rp.Requeue) != "deal" {
			t.Fatalf("the key of a deal that left room: done %s requeue %s", keyTexts(rp.Done), keyTexts(rp.Requeue))
		}
		quiet(t, "deal with room", f.plan(ruleDeal, "deal"), "deal")
	})
	t.Run("level", func(t *testing.T) {
		t.Parallel()
		f := newFleetT(t, 0, "m1", "m2")
		for i := 1; i <= 2; i++ {
			f.snap().Fleet.Put(&Card{ID: "p" + itoa(i) + ".w1", Row: "m1", Col: Ready, Score: float64(i), Rev: 1,
				Fields: map[string]string{"kind": "work", "primary": "p" + itoa(i), "stream": "s1", "gen": "1", "member": "m1"}})
		}
		f.run(ruleLevel, "level")
		quiet(t, "level", f.plan(ruleLevel, "level"), "level")
	})
}

// ---- the reads

func fleetBounds() ReadBounds {
	return ReadBounds{Queries: 1024, Records: 10_000, RangeIDs: 20_000, Bytes: 8 << 20}
}

// The four rules are registered, named as the rule of their keys, in the order
// of the round robin (1.4.2) and at the priorities of IT05's table; and every
// key that ingest makes for them, and the key R1's beat part makes, is served by
// a rule that is registered.
func TestFleetRulesRegistered(t *testing.T) {
	t.Parallel()
	want := []string{ruleSeen, ruleDown, ruleDeal, ruleLevel}
	var got []string
	for _, r := range RuleTable() {
		for _, w := range want {
			if r.Name == w {
				got = append(got, r.Name)
				p, ok := PriorityOf(r.Name)
				if r.Read == nil || r.Plan == nil || r.MaxSteps != 0 || !ok || r.Priority != p {
					t.Fatalf("rule %s: %+v, priority in the table %d %v", r.Name, r, p, ok)
				}
			}
		}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("the table holds %v, want %v in this order", got, want)
	}
	for _, r := range fleetRuleRows {
		if RuleOf(r.name+":x") != r.name || RuleOf(r.name) != r.name {
			t.Fatalf("%s is not the rule of its keys", r.name)
		}
	}
	registered := map[string]bool{}
	for _, r := range RuleTable() {
		registered[r.Name] = true
	}
	// the keys the fleet rules serve: down:<m> from a control card that goes down
	// or held, deal and level from a member up, a ready card or a sentinel, and
	// seen:<m> from the beat part
	for _, key := range []string{"seen:m1", "down:m1", "deal", "level"} {
		if rule := ServingRule(key); !registered[rule] || rule != RuleOf(key) {
			t.Errorf("the key %q is served by %q, registered: %v", key, rule, registered[rule])
		}
	}
	for _, word := range []string{ruleDown, ruleDeal, ruleLevel} {
		if !registered[ServingRule(word+":x")] {
			t.Errorf("ingest's word %q reaches no registered rule", word)
		}
	}
}

// The rules' bound is five, and counts only a take that ended without a finish.
// It is the rules' own number: today's machine keeps its constant, which the
// scanning tick and the reference model pin, until the switch.
func TestRuleMaxRedealsIsFive(t *testing.T) {
	t.Parallel()
	if RuleMaxRedeals != 5 {
		t.Fatalf("RuleMaxRedeals is %d", RuleMaxRedeals)
	}
}

// R1's read is one control card a key; the read is cut to the records it may
// hold, and each halving halves the keys down to one.
func TestSeenReadCutAndHalved(t *testing.T) {
	t.Parallel()
	ks := agendaOf("seen:m1", "seen:m2", "seen:m3", "seen:m4", "seen:m5")
	b := ReadBounds{Records: 4, Queries: 1024, RangeIDs: 20_000, Bytes: 8 << 20}
	for _, c := range []struct {
		halvings, kept int
	}{{0, 4}, {1, 2}, {2, 1}, {3, 1}} {
		rp, left := readSeen(ks, b, c.halvings)
		if len(rp.Sprint) != 1 || rp.Sprint[0].Kind != QueryRelated || rp.Sprint[0].Table != Fleet {
			t.Fatalf("halvings %d: the read is %+v", c.halvings, rp)
		}
		ids := rp.Sprint[0].Source.IDs
		if got := len(ids); got != c.kept || len(left) != len(ks)-c.kept {
			t.Fatalf("halvings %d: read %d control cards and left %d, want %d and %d", c.halvings, got, len(left), c.kept, len(ks)-c.kept)
		}
		if ids[0] != CtlID("m1") || rp.Cost().Records != c.kept {
			t.Fatalf("the ids: %v, cost %+v", ids, rp.Cost())
		}
	}
	if rp, left := readSeen(nil, fleetBounds(), 0); rp.Queries() != 0 || len(left) != 0 {
		t.Fatalf("no keys: %+v %v", rp, left)
	}
}

// R2 reads the first 2,000 cards of a member's cells; one key at that limit
// fits the 10,000 records of a read, a second does not, and each halving halves
// the keys and then the limit, down to one.
func TestDownReadFirst2000(t *testing.T) {
	t.Parallel()
	ks := agendaOf("down:m1", "down:m2", "down:m3")
	heads := func(rp ReadPlan) (qs []SprintQ) {
		for _, q := range rp.Sprint {
			if q.Kind == QueryRelated {
				qs = append(qs, q)
			}
		}
		return qs
	}
	rp, left := readDown(ks, fleetBounds(), 0)
	hs := heads(rp)
	if len(rp.Sprint) != 3 || rp.Sprint[0].Kind != QueryFleet || len(hs) != 2 || len(left) != 2 || keyTexts(left) != "down:m2,down:m3" {
		t.Fatalf("read %+v, left %s", rp, keyTexts(left))
	}
	if hs[0].Source.Limit != 2000 || hs[1].Source.Limit != 2000 || hs[0].Source.Key != "m1:ready" || hs[1].Source.Key != "m1:working" ||
		hs[0].Source.Kind != SourceHead || hs[0].Follow[0] != followPrimary {
		t.Fatalf("the heads: %+v", hs)
	}
	if got := rp.Cost().Records; got > fleetBounds().Records || got != MaxMembers+2*2*2000 {
		t.Fatalf("one key costs %d records, over the read's %d", got, fleetBounds().Records)
	}
	for halvings, want := range map[int]int{1: 1000, 2: 500, 3: 250, 11: 1, 20: 1} {
		rp, left := readDown(ks, fleetBounds(), halvings)
		hs := heads(rp)
		if len(hs) != 2 || len(left) != 2 || hs[0].Source.Limit != want {
			t.Fatalf("halvings %d: %d heads, limit %d, want 1 key and %d", halvings, len(hs), hs[0].Source.Limit, want)
		}
	}
	// small limits let more keys in
	if rp, _ := readDown(ks, ReadBounds{Records: 20_000, Queries: 1024}, 0); len(heads(rp)) != 4 {
		t.Fatalf("20,000 records hold %d heads of 2,000", len(heads(rp)))
	}
}

// R6's limit L is the design's, min(room, 64, 10,000 / 3s), taken against the
// records the answer may hold, so it always fits: the design's own formula
// overshoots 10,000 records where the fleet's members and every stream's σ
// record are counted (101 streams and 149 members: L = 33 answers 10,249).
func TestDealLimitFitsTheRead(t *testing.T) {
	t.Parallel()
	const records = 10_000
	if got := dealLimit(0, 250, 0, records); got != 13 {
		t.Fatalf("250 streams: L = %d, want 13", got)
	}
	if got := dealLimit(0, 1, 0, records); got != dealMaxL {
		t.Fatalf("one stream: L = %d, want %d", got, dealMaxL)
	}
	if got := dealLimit(5, 3, 2, records); got != 5 {
		t.Fatalf("room 5: L = %d, want 5", got)
	}
	for streams := 1; streams <= MaxStreams; streams++ {
		members := MaxStreams - streams
		l := dealLimit(0, streams, members, records)
		if l < 1 || l > dealMaxL || l > max(1, records/(3*streams)) {
			t.Fatalf("%d streams: L = %d is over the design's min(64, 10,000 / 3s) = %d", streams, l, records/(3*streams))
		}
		if l > 1 {
			if n := dealRecords(streams, members, l); n > records {
				t.Fatalf("%d streams and %d members at L = %d: %d records, over %d", streams, members, l, n, records)
			}
		}
	}
	if n := dealRecords(101, 149, 33); n != 10_249 || n <= records {
		t.Fatalf("the design's L at 101 streams and 149 members is %d records", n)
	}
	if dealLimit(0, 101, 149, records) >= 33 {
		t.Fatalf("L at 101 streams and 149 members is not below the design's 33")
	}
}

// names are n names with a prefix.
func names250(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = prefix + itoa(i)
	}
	return out
}

// R6's read of the largest sprint costs what the cost table says (the records
// of the answer are those of IT05's own deal read), fits layer 1's read in one
// piece at every split of 250 members and streams, and each halving halves L,
// down to one.
func TestDealReadHalvings(t *testing.T) {
	t.Parallel()
	limit := func(rp ReadPlan) int {
		for _, q := range rp.Sprint {
			if q.Kind == QueryFront {
				return q.Heads[0].Limit
			}
		}
		t.Fatalf("no front query: %+v", rp)
		return 0
	}
	// members and streams together are at most 250 (F1-20): 249 streams, one member
	sh := fleetShape{Streams: names250("s", MaxStreams-1), Members: names250("m", 1)}
	for halvings, want := range map[int]int{0: 13, 1: 7, 2: 4, 3: 2, 4: 1, 9: 1} {
		rp := dealReadFor(sh, fleetBounds(), halvings)
		if got := limit(rp); got != want {
			t.Fatalf("halvings %d: L = %d, want %d", halvings, got, want)
		}
		if n := rp.Cost().Records; n != 1+(MaxStreams-1)*(1+3*want) {
			t.Fatalf("halvings %d: the read costs %d records", halvings, n)
		}
	}
	for streams := 1; streams < MaxStreams; streams++ {
		sh := fleetShape{Streams: names250("s", streams), Members: names250("m", MaxStreams-streams)}
		rp := dealReadFor(sh, fleetBounds(), 0)
		if n := rp.Cost().Records; n > fleetBounds().Records && limit(rp) > 1 {
			t.Fatalf("%d streams: the read costs %d records at L = %d", streams, n, limit(rp))
		}
		if plans := rp.Split(fleetBounds()); len(plans) != 1 && limit(rp) > 1 {
			t.Fatalf("%d streams: the read is %d reads", streams, len(plans))
		}
	}
	// the registered read is given no streams: the fleet alone, and no key left
	rp, left := readDeal(agendaOf("deal"), fleetBounds(), 0)
	if len(left) != 0 || len(rp.Sprint) != 1 || rp.Sprint[0].Kind != QueryFleet {
		t.Fatalf("%+v %v", rp, left)
	}
	if rp, left := readDeal(agendaOf("level"), fleetBounds(), 0); rp.Queries() != 0 || len(left) != 1 {
		t.Fatalf("no deal key: %+v %v", rp, left)
	}
}

// R7 reads the members and at most two cards of each member's ready cell.
func TestLevelReadTwoEach(t *testing.T) {
	t.Parallel()
	rp := levelReadFor(fleetShape{Members: []string{"m1", "m2", "m3"}})
	if len(rp.Sprint) != 4 || rp.Sprint[0].Kind != QueryFleet || rp.Sprint[0].Units != 3 {
		t.Fatalf("%+v", rp)
	}
	for i, q := range rp.Sprint[1:] {
		want := "m" + itoa(i+1) + ":ready"
		if q.Kind != QueryRelated || q.Source.Kind != SourceHead || q.Source.Key != want || q.Source.Limit != MaxReadyPerMember {
			t.Fatalf("query %d: %+v", i+1, q)
		}
	}
	rp, left := readLevel(agendaOf("level"), fleetBounds(), 0)
	if len(left) != 0 || len(rp.Sprint) != 1 || rp.Sprint[0].Kind != QueryFleet {
		t.Fatalf("the registered read: %+v %v", rp, left)
	}
}

// ---- the limit, measured in Go time

// benchDownT is a sprint of one stream and three members up, and a member m1
// holding the cards, half ready and half working.
func benchDownT(b testing.TB, cards int) *Snapshot {
	b.Helper()
	return downSprintT(b, (cards+1)/2, cards/2)
}

// downSprintT is that sprint with m1 holding ready cards and working cards,
// the ready ones first in score order.
func downSprintT(b testing.TB, ready, working int) *Snapshot {
	b.Helper()
	s := &Snapshot{Now: t0, Work: NewTable(Work), Readers: NewTable(Readers), Merge: NewTable(Merge), Fleet: NewTable(Fleet)}
	s.Work.SetRows([]string{"s1"})
	s.Fleet.SetRows([]string{"m1", "m2", "m3"})
	for _, m := range s.Fleet.Rows() {
		s.Fleet.Put(&Card{ID: CtlID(m), Row: m, Col: Ctl, Rev: 1, Fields: map[string]string{"kind": "member", "status": Up}})
	}
	r, w := ready, working
	for i := 0; r+w > 0; i++ {
		id := "p" + itoa(i)
		col := Ready
		if r == 0 || w > 0 && i%2 == 1 {
			col = Working
		}
		if col == Ready {
			r--
		} else {
			w--
		}
		s.Work.Put(&Card{ID: id, Row: "s1", Col: Working, Score: float64(i), Rev: 1,
			Fields: map[string]string{"kind": "primary", "stream": "s1", "attempt": "1", "work": id + ".w1"}})
		s.Fleet.Put(&Card{ID: id + ".w1", Row: "m1", Col: col, Score: float64(i), Rev: 1,
			Fields: map[string]string{"kind": "work", "primary": id, "stream": "s1", "attempt": "1", "gen": "1", "member": "m1", "redeals": "1"}})
	}
	return s
}

// loadOrFail is the snapshot a read plan loads from a whole sprint through the
// twin.
func loadOrFail(b testing.TB, whole *Snapshot, rp ReadPlan) *Snapshot {
	b.Helper()
	s, err := LoadPartial(rp, fleetTwin{whole: whole}.Answer(rp))
	if err != nil {
		b.Fatalf("the twin's answer does not load: %v", err)
	}
	return s
}

// BenchmarkPlanDown2000 is the Go time of R2's plan for a member of 2,000
// cards (a down of 2,000, 8.1 IT07), on the snapshot its read loads. The limit
// of 25 ms is store time, measured by IT25's rows.
func BenchmarkPlanDown2000(b *testing.B) {
	ks := agendaOf("down:m1")
	rp, _ := readDown(ks, fleetBounds(), 0)
	s := loadOrFail(b, benchDownT(b, fleetChunk), rp)
	now := Now{R: fleetR0, Wall: t0.UnixMilli(), Running: true}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := planDownWith(s, ks, now, fleetFacts{}); len(got.Plan.Units) != fleetChunk+1 {
			b.Fatalf("units %d", len(got.Plan.Units))
		}
	}
}

// BenchmarkPlanDeal16 is the Go time of R6's plan for a deal of 16 (8.1 IT07),
// on the snapshot its read loads. The limit of 1 ms is store time, measured by
// IT25's rows.
func BenchmarkPlanDeal16(b *testing.B) {
	whole := &Snapshot{Now: t0, Work: NewTable(Work), Readers: NewTable(Readers), Merge: NewTable(Merge), Fleet: NewTable(Fleet)}
	whole.Work.SetRows([]string{"s1"})
	whole.Fleet.SetRows([]string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8"})
	for _, m := range whole.Fleet.Rows() {
		whole.Fleet.Put(&Card{ID: CtlID(m), Row: m, Col: Ctl, Rev: 1, Fields: map[string]string{"kind": "member", "status": Up}})
	}
	for i := 0; i < 64; i++ {
		whole.Work.Put(&Card{ID: "p" + itoa(i), Row: "s1", Col: Ready, Score: float64(i), Rev: 1,
			Fields: map[string]string{"kind": "primary", "stream": "s1"}})
	}
	rp := dealReadFor(fleetShape{Streams: []string{"s1"}, Members: whole.Fleet.Rows()}, fleetBounds(), 0)
	s := loadOrFail(b, whole, rp)
	ks := agendaOf("deal")
	now := Now{R: fleetR0, Wall: t0.UnixMilli(), Running: true}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := planDealWith(s, ks, now, fleetFacts{}); len(got.Plan.Units) != 16 {
			b.Fatalf("units %d", len(got.Plan.Units))
		}
	}
}

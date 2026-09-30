package sprint

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// The tests that pin what the reads carry and what the plans decide, each the
// test of a finding of the cold read of the fleet rules: that every field a
// read names is one its plan needs, that every follow is, that queue lengths
// are the cells' counts, that a member's control card is guarded whenever its
// cards are moved, and the rows of the design that no other test held.

// putWorkCard puts a work card on a member, and its primary in the work table:
// a card in ready or working is what the primary is working on; a withdrawn one
// has sent its primary back to ready.
func putWorkCard(f *fleetT, p, member, col string, score float64, extra map[string]string) {
	fields := map[string]string{"kind": "work", "primary": p, "stream": "s1", "attempt": "1", "gen": "2", "member": member,
		"dealt": stamp(t0), "untaken_since": stamp(t0), "redeals": "0"}
	for k, v := range extra {
		fields[k] = v
	}
	f.snap().Fleet.Put(&Card{ID: p + ".w1", Row: member, Col: col, Score: score, Rev: 1, Fields: fields})
	prim := map[string]string{"kind": "primary", "attempt": "1", "stream": "s1"}
	pcol := Working
	if col == Withdrawn {
		pcol = Ready
	} else {
		prim["work"] = p + ".w1"
	}
	f.snap().Work.Put(&Card{ID: p, Row: "s1", Col: pcol, Score: score, Rev: 1, Fields: prim})
}

// taken are the fields of a work card a worker has taken and not finished.
func taken(extra map[string]string) map[string]string {
	m := map[string]string{"taken": stamp(t0), fleetFieldFirstTakenR: "1000", fleetFieldDueUnfinished: "9000000"}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// fieldScenario is a state and the keys a rule plans on it, in which every
// field its read names is read by the plan.
type fieldScenario struct {
	name  string
	rule  string
	keys  []string
	build func(t *testing.T) *fleetT
}

func fieldScenarios() []fieldScenario {
	return []fieldScenario{
		{"seen", ruleSeen, []string{"seen:m1", "seen:m2", "seen:m3", "seen:m4", "seen:zed"}, func(t *testing.T) *fleetT {
			f := newFleetT(t, 0, "m1", "m2", "m3", "m4")
			f.setMember("m2", Down, "")
			f.setMember("m3", Held, "")
			f.setMember("m4", Down, "2030-01-02T03:04:05Z") // down, and a hold
			return f
		}},
		// m1 holds a card never taken, a taken one, and a taken one at the bound; m2 and m3 receive,
		// every member at width 1, so m2 (holding r1) is at its width and the width decides
		{"down, dealt again and at the bound", ruleDown, []string{"down:m1"}, func(t *testing.T) *fleetT {
			f := newFleetW(t, 1, 1, "m1", "m2", "m3")
			putWorkCard(f, "a1", "m1", Ready, 10, map[string]string{"redeals": "2"})
			putWorkCard(f, "a2", "m1", Working, 11, taken(map[string]string{"redeals": "2"}))
			putWorkCard(f, "a3", "m1", Working, 12, taken(map[string]string{"redeals": itoa(RuleMaxRedeals)}))
			putWorkCard(f, "r1", "m2", Ready, 13, nil)
			return f
		}},
		// nobody is left to receive: m1's cards are withdrawn and their primaries returned
		{"down, nobody left", ruleDown, []string{"down:m1"}, func(t *testing.T) *fleetT {
			f := newFleetT(t, 1, "m1", "m2")
			f.setMember("m2", Down, "")
			putWorkCard(f, "a1", "m1", Ready, 10, map[string]string{"redeals": "2"})
			putWorkCard(f, "a2", "m1", Working, 11, taken(map[string]string{"redeals": "2"}))
			return f
		}},
		// a fresh card, three cards dealt before (a fix and a failed result and an avoided member; a withdrawn card at
		// generation 3 with its own untaken clock; one with no clock), a refused card and one at its bound
		{"deal", ruleDeal, []string{"deal"}, func(t *testing.T) *fleetT {
			f := newFleetT(t, 6, "m1", "m2", "m3")
			f.edit(f.snap().Work, "s1-2", map[string]string{"attempt": "1", "avoid": "m2", "fix": "the finding", "result": "failed"})
			f.edit(f.snap().Work, "s1-3", map[string]string{"attempt": "1"})
			f.edit(f.snap().Work, "s1-4", map[string]string{"attempt": "1"})
			f.edit(f.snap().Work, "s1-5", map[string]string{"refused": "deal: something"})
			f.edit(f.snap().Work, "s1-6", map[string]string{"attempt": "1", "bound": boundRedeals})
			for i, id := range []string{"s1-3", "s1-4"} {
				fields := map[string]string{"kind": "work", "primary": id, "stream": "s1", "attempt": "1", "gen": "3", "redeals": "2",
					"untaken_since": stamp(t0), "withdrawn": stamp(t0)}
				if i == 0 {
					fields[fleetFieldUntakenR] = "42"
				}
				f.snap().Fleet.Put(&Card{ID: id + ".w1", Row: "m1", Col: Withdrawn, Score: float64(3 + i), Rev: 1, Fields: fields})
			}
			// the fleet table's deal_index starts the deal past the first
			// member (errata 3, amendment 5)
			f.snap().Fleet.SetProps(map[string]string{PropDealIndex: "m1"})
			return f
		}},
		{"level", ruleLevel, []string{"level"}, func(t *testing.T) *fleetT {
			f := newFleetT(t, 0, "m1", "m2")
			readyCard(f, "p1", "m1", "s1", 1, map[string]string{"untaken_since": stamp(t0)})
			readyCard(f, "p2", "m1", "s1", 2, map[string]string{"untaken_since": stamp(t0)})
			return f
		}},
	}
}

// readFieldsOf are the fields the queries of a read name.
func readFieldsOf(rp ReadPlan) []string {
	var out []string
	for _, q := range rp.Sprint {
		for _, f := range q.Fields {
			if !slices.Contains(out, f) {
				out = append(out, f)
			}
		}
	}
	slices.Sort(out)
	return out
}

// without is the fields with one taken out.
func without(fields []string, name string) []string {
	var out []string
	for _, f := range fields {
		if f != name {
			out = append(out, f)
		}
	}
	return out
}

// differs plans on what the read loads, and says the plan is not the plan made
// on the whole sprint, or that it read what the read did not load (a panic in a
// test build).
func (f *fleetT) differs(rule string, ks []AgendaKey) (differs bool) {
	f.t.Helper()
	defer func() {
		if recover() != nil {
			differs = true
		}
	}()
	s := f.loaded(rule, ks)
	got := f.planOn(rule, s, ks)
	want := f.oracle(rule, f.snap(), ks)
	got.Plan.pre, want.Plan.pre = nil, nil
	return !reflect.DeepEqual(got, want)
}

// Every field a read names is one its plan needs: a field taken out of every
// query of the read makes the plan on what was loaded differ from the plan on the
// whole sprint (an unset it would have made, a card it would have left in place)
// or read what was not loaded. So no read names a field its plan does not read,
// and, with the plans made on the read's own answer, none leaves one out.
func TestEveryFieldOfEveryReadIsNeededByItsPlan(t *testing.T) {
	t.Parallel()
	needed := map[string]map[string]bool{}
	fields := map[string][]string{}
	for _, sc := range fieldScenarios() {
		f := sc.build(t)
		ks := agendaOf(sc.keys...)
		if f.differs(sc.rule, ks) {
			t.Fatalf("%s: the plan on what the read loaded is not the plan on the whole sprint before any field is taken out", sc.name)
		}
		if needed[sc.rule] == nil {
			needed[sc.rule] = map[string]bool{}
		}
		for _, name := range readFieldsOf(f.readPlan(sc.rule, ks)) {
			if !slices.Contains(fields[sc.rule], name) {
				fields[sc.rule] = append(fields[sc.rule], name)
			}
			f.mutate = func(rp *ReadPlan) {
				for i := range rp.Sprint {
					rp.Sprint[i].Fields = without(rp.Sprint[i].Fields, name)
				}
			}
			if f.differs(sc.rule, ks) {
				needed[sc.rule][name] = true
			}
			f.mutate = nil
		}
	}
	for rule, names := range fields {
		for _, name := range names {
			if !needed[rule][name] {
				t.Errorf("%s: the read names %q and no plan of the scenarios needs it", rule, name)
			}
		}
	}
	if len(fields) != 4 {
		t.Fatalf("rules with scenarios: %v", fields)
	}
}

// Every follow of a read is one its plan needs: R2's primary of each card, and
// R6's withdrawn card of each again head. A read without it loads no such
// record, and the plan differs from the plan on the whole sprint. The read the
// rule makes must have the follow to begin with: a test that takes the follows out
// itself and asks only whether the plan differs passes when the read already
// lacks them.
func TestEveryFollowOfEveryReadIsNeededByItsPlan(t *testing.T) {
	t.Parallel()
	checked := 0
	for _, sc := range fieldScenarios() {
		if sc.rule != ruleDown && sc.rule != ruleDeal {
			continue
		}
		f := sc.build(t)
		ks := agendaOf(sc.keys...)
		followed := 0
		for _, q := range f.readPlan(sc.rule, ks).Sprint {
			followed += len(q.Follow)
			for _, h := range q.Heads {
				followed += len(h.Follow)
			}
		}
		if followed == 0 {
			t.Errorf("%s: the rule's read follows nothing", sc.name)
		}
		f.mutate = func(rp *ReadPlan) {
			for i := range rp.Sprint {
				rp.Sprint[i].Follow = nil
				heads := append([]HeadQ(nil), rp.Sprint[i].Heads...)
				for j := range heads {
					heads[j].Follow = nil
				}
				rp.Sprint[i].Heads = heads
			}
		}
		if !f.differs(sc.rule, ks) {
			t.Errorf("%s: the plan does not need the records its read follows", sc.name)
		}
		checked++
	}
	if checked != 3 {
		t.Fatalf("%d scenarios checked", checked)
	}
}

// A withdrawn card dealt again keeps the generation it had, plus one, and the
// untaken clock it carries, and loses its withdrawn mark: the plan reads all
// three from the card the read followed, where a read that carried none would
// deal it at generation 1 and start its clock again (1.2).
func TestDealAgainOfAWithdrawnCardAtGeneration3(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 1, "m1", "m2")
	f.edit(f.snap().Work, "s1-1", map[string]string{"attempt": "1", "avoid": "m2"})
	f.snap().Fleet.Put(&Card{ID: "s1-1.w1", Row: "m2", Col: Withdrawn, Score: 1, Rev: 1, Fields: map[string]string{
		"kind": "work", "primary": "s1-1", "stream": "s1", "attempt": "1", "gen": "3", "redeals": "2",
		"untaken_since": stamp(t0), fleetFieldUntakenR: "42", "withdrawn": stamp(t0)}})
	rp := f.plan(ruleDeal, "deal")
	u := unitFor(t, rp, "s1-1")
	e := u.Changes[0].Entry
	if e.Set["gen"] != "4" {
		t.Fatalf("the card is dealt at generation %q, want 4", e.Set["gen"])
	}
	for _, name := range []string{fleetFieldUntakenR, fleetFieldDueUntaken, "redeals"} {
		if v, ok := e.Set[name]; ok {
			t.Fatalf("a card dealt again keeps %s (42, 3 redeals): the plan set it to %q", name, v)
		}
	}
	if !slices.Contains(e.Unset, "withdrawn") {
		t.Fatalf("the withdrawn mark is not unset: %+v", e)
	}
	f.apply(rp)
	if c := f.snap().Fleet.Card("s1-1.w1"); c.F(fleetFieldUntakenR) != "42" || c.Int("gen") != 4 || c.Int("redeals") != 2 {
		t.Fatalf("after the deal: %+v", c.Fields)
	}
}

// A card with no untaken clock, dealt again from withdrawn, starts it at R, the
// running time, never the wall clock; and so does a card dealt for the first
// time (1.2, W3: a wall stamp would run through every hour the machine was
// STOPPED).
func TestDealStampsTheUntakenClockInRunningTime(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 2, "m1", "m2")
	if f.now.R == f.now.Wall {
		t.Fatalf("R and the wall clock are the same number: the test cannot tell them apart")
	}
	f.edit(f.snap().Work, "s1-2", map[string]string{"attempt": "1"})
	f.snap().Fleet.Put(&Card{ID: "s1-2.w1", Row: "m2", Col: Withdrawn, Score: 2, Rev: 1, Fields: map[string]string{
		"kind": "work", "primary": "s1-2", "stream": "s1", "attempt": "1", "gen": "1", "withdrawn": stamp(t0)}})
	rp := f.run(ruleDeal, "deal")
	for _, id := range []string{"s1-1.w1", "s1-2.w1"} {
		c := f.snap().Fleet.Card(id)
		if c.F(fleetFieldUntakenR) != fleetMs(f.now.R) || c.F(fleetFieldDueUntaken) != fleetMs(f.now.R+15*60_000) {
			t.Fatalf("%s: untaken_r %s, due_untaken %s, want R %d and R + 15 min", id, c.F(fleetFieldUntakenR), c.F(fleetFieldDueUntaken), f.now.R)
		}
	}
	if len(rp.Plan.Units) != 2 {
		t.Fatalf("units: %+v", rp.Plan.Units)
	}
}

// A read that is cut at its limit loads the first cards of a cell and no more;
// the plan takes them, and the cell's count says there are more: the key stays.
// A count taken from the cards loaded, where the cell holds more than the read's
// limit, would remove the key with cards left on a member that is down.
func TestDownCutIsFoundFromTheCountsNotTheHead(t *testing.T) {
	t.Parallel()
	// 2,500 ready cards in one cell: the head is 2,000
	f := newFleetOn(t, &world{t: t, s: downSprintT(t, 2500, 0)})
	rp := f.plan(ruleDown, "down:m1")
	if len(rp.Plan.Units) != fleetChunk+1 || keyTexts(rp.Requeue) != "down:m1" || len(rp.Done) != 0 {
		t.Fatalf("units %d, done %s requeue %s", len(rp.Plan.Units), keyTexts(rp.Done), keyTexts(rp.Requeue))
	}
	if !hasUnit(rp, "p1999.w1") || hasUnit(rp, "p2000.w1") {
		t.Fatalf("the lowest 2,000 are dealt")
	}

	// a read halved to 1,000 loads 1,000 of 1,500: the key stays for the rest
	g := newFleetOn(t, &world{t: t, s: downSprintT(t, 1500, 0)})
	g.halvings = 1
	g.partialOnly = true
	rp = g.plan(ruleDown, "down:m1")
	if len(rp.Plan.Units) != 1001 || keyTexts(rp.Requeue) != "down:m1" || len(rp.Done) != 0 {
		t.Fatalf("units %d, done %s requeue %s", len(rp.Plan.Units), keyTexts(rp.Done), keyTexts(rp.Requeue))
	}
	// and a cell of exactly its limit is not a cut
	h := newFleetOn(t, &world{t: t, s: downSprintT(t, 1000, 0)})
	h.halvings = 1
	rp = h.plan(ruleDown, "down:m1")
	if len(rp.Plan.Units) != 1001 || keyTexts(rp.Done) != "down:m1" || len(rp.Requeue) != 0 {
		t.Fatalf("units %d, done %s requeue %s", len(rp.Plan.Units), keyTexts(rp.Done), keyTexts(rp.Requeue))
	}
}

// The room of a deal is the receivers' ready counts, whatever a read loaded: a
// member holding more than the cap (R2 puts none on a receiver) has no room, and
// nothing is dealt to it.
func TestDealRoomIsTheCellCounts(t *testing.T) {
	t.Parallel()
	f := newFleetW(t, 3, 2, "m1", "m2")
	for i := 1; i <= 3; i++ {
		putWorkCard(f, "x"+itoa(i), "m1", Ready, float64(20+i), nil)
	}
	putWorkCard(f, "y1", "m2", Ready, 30, nil)
	rp := f.plan(ruleDeal, "deal")
	// m1 holds 3, over its cap: no room; m2 holds 1: room for one
	if len(rp.Plan.Units) != 1 {
		t.Fatalf("dealt %d cards, the room is 1: %+v", len(rp.Plan.Units), rp.Plan.Units)
	}
	if u := rp.Plan.Units[0]; u.Changes[0].Entry.Create == nil || u.Changes[0].Entry.Create.Row != "m2" {
		t.Fatalf("the card goes to m2: %+v", u)
	}
	if keyTexts(rp.Done) != "deal" {
		t.Fatalf("the room is used and the key goes: done %s requeue %s", keyTexts(rp.Done), keyTexts(rp.Requeue))
	}
}

// A deal plans on the heads its read took: a read at a limit of one card a head
// loads one primary of the stream, and the deal makes one move, and leaves the
// key, since room remains.
func TestDealPlansOnlyWhatTheHeadsHold(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 9, "m1", "m2")
	f.halvings = 12
	f.partialOnly = true
	rp := f.plan(ruleDeal, "deal")
	if len(rp.Plan.Units) != 1 || !hasUnit(rp, "s1-1") {
		t.Fatalf("units: %+v", rp.Plan.Units)
	}
	if keyTexts(rp.Requeue) != "deal" || len(rp.Done) != 0 {
		t.Fatalf("done %s requeue %s", keyTexts(rp.Done), keyTexts(rp.Requeue))
	}
}

// R2 guards a member's control card by its revision whenever it moves the
// member's cards, whether or not it writes the card: a held member (whose status
// only fleet up changes) and one an earlier chunk marked are only guarded, and a
// fleet up between the read and the apply refuses the step, so no card is dealt
// away from a member that is up again.
func TestDownGuardsTheControlCardWhenItMovesCards(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		status string
		hold   string
	}{{"held", Held, ""}, {"down and held", Down, "2030-01-02T03:04:05Z"}, {"already down", Down, ""}} {
		f := newFleetT(t, 4, "m1", "m2")
		f.run(ruleDeal, "deal")
		f.setMember("m1", c.status, c.hold)
		rp := f.plan(ruleDown, "down:m1")
		ctl := f.snap().MemberCtl("m1")
		first := rp.Plan.Units[0]
		e := first.Changes[0].Entry
		if first.Key != ctl.ID || first.Changes[0].Table != Fleet || len(first.Changes) != 1 || e.Set != nil || e.Move != nil || e.Unset != nil ||
			e.Expect == nil || e.Expect.Revision != u64(ctl.Rev) || e.Expect.Place == nil || e.Expect.Place.Row != "m1" || e.Expect.Place.Col != Ctl {
			t.Fatalf("%s: the control card is not guarded by its revision and place, and only guarded: %+v", c.name, first)
		}
		if why := f.failedExpect(rp); why != "" {
			t.Fatalf("%s: a plan on the state it read is refused: %s", c.name, why)
		}
		// fleet up lands between the read and the apply
		f.setMember("m1", Up, "")
		delete(ctl.Fields, "held")
		if why := f.failedExpect(rp); !strings.Contains(why, ctl.ID) {
			t.Fatalf("%s: after fleet up the step is not refused on the control card: %q", c.name, why)
		}
	}

	// the step that marks a member down guards the card by the entry that writes it
	g := newFleetT(t, 4, "m1", "m2")
	g.run(ruleDeal, "deal")
	rp := g.plan(ruleDown, "down:m1")
	ctl := g.snap().MemberCtl("m1")
	if e := rp.Plan.Units[0].Changes[0].Entry; rp.Plan.Units[0].Key != ctl.ID || e.Set["status"] != Down || e.Expect == nil || e.Expect.Revision != u64(ctl.Rev) {
		t.Fatalf("the marking entry: %+v", e)
	}
	g.setMember("m1", Up, "")
	if why := g.failedExpect(rp); !strings.Contains(why, ctl.ID) {
		t.Fatalf("a marked member that came up again does not refuse the step: %q", why)
	}

	// no card to move, nothing to guard: a held member with none has an empty step
	h := newFleetT(t, 0, "m1", "m2")
	h.setMember("m1", Held, "")
	quiet(t, "held with no cards", h.plan(ruleDown, "down:m1"), "down:m1")
}

// The cards of a member that went down go round the fleet from the deal's
// rolling index (2.3 R2; errata 3 amendment 5: every placement moves the
// index), each to the first up member with room, counting the cards the plan
// has dealt there already.
func TestDownDealsRoundTheFleet(t *testing.T) {
	t.Parallel()
	f := newFleetW(t, 1, 2, "m1", "m2", "m3") // every member at width 2
	putWorkCard(f, "r1", "m2", Ready, 1, nil) // m2 holds one, m3 none
	for i := 1; i <= 3; i++ {
		putWorkCard(f, "c"+itoa(i), "m1", Ready, float64(10+i), nil)
	}
	rp := f.plan(ruleDown, "down:m1")
	to := map[string]string{}
	for _, u := range rp.Plan.Units {
		for _, ch := range u.Changes {
			if ch.Entry.Move != nil && strings.HasPrefix(ch.Entry.ID, "c") && strings.HasSuffix(ch.Entry.ID, ".w1") {
				to[ch.Entry.ID] = ch.Entry.Move.Row
			}
		}
	}
	// from the first member (no index yet), m1 going down: m2 (1 < its width 2),
	// m3, then m2 is at its width and m3 takes the third (1 < 2); the shorter
	// queue does not choose
	want := map[string]string{"c1.w1": "m2", "c2.w1": "m3", "c3.w1": "m3"}
	if !reflect.DeepEqual(to, want) {
		t.Fatalf("dealt to %v, want %v", to, want)
	}
	if len(rp.Plan.Props) != 1 || rp.Plan.Props[0].Name != PropDealIndex || rp.Plan.Props[0].Value != "m3" {
		t.Fatalf("the deal's index: %+v, want it moved past m3", rp.Plan.Props)
	}
	f.apply(rp)
	if a, b := f.snap().Fleet.Count("m2", Ready), f.snap().Fleet.Count("m3", Ready); a != 2 || b != 2 {
		t.Fatalf("queues %d and %d", a, b)
	}
}

// Only a working card is at the bound: a ready card, never taken, is dealt again
// whatever its count says, with the count as it was, and a working card one
// below the bound is dealt again and reaches it (2.3 R2; departure 7).
func TestDownOnlyAWorkingCardIsAtTheBound(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 1, "m1", "m2")
	putWorkCard(f, "a1", "m1", Ready, 10, map[string]string{"redeals": itoa(RuleMaxRedeals)})
	putWorkCard(f, "a2", "m1", Working, 11, taken(map[string]string{"redeals": itoa(RuleMaxRedeals - 1)}))
	putWorkCard(f, "a3", "m1", Working, 12, taken(map[string]string{"redeals": itoa(RuleMaxRedeals)}))
	rp := f.plan(ruleDown, "down:m1")
	f.apply(rp)
	a1, a2, a3 := f.snap().Fleet.Card("a1.w1"), f.snap().Fleet.Card("a2.w1"), f.snap().Fleet.Card("a3.w1")
	if a1.Col != Ready || a1.Row != "m2" || a1.Int("redeals") != RuleMaxRedeals {
		t.Fatalf("a ready card at the count is dealt again, its count as it was: %s:%s redeals %d", a1.Row, a1.Col, a1.Int("redeals"))
	}
	if a2.Col != Ready || a2.Row != "m2" || a2.Int("redeals") != RuleMaxRedeals {
		t.Fatalf("a working card below the bound is dealt again and counted: %s:%s redeals %d", a2.Row, a2.Col, a2.Int("redeals"))
	}
	if a3.Col != Withdrawn || a3.Int("redeals") != RuleMaxRedeals {
		t.Fatalf("a working card at the bound is withdrawn: %s:%s redeals %d", a3.Row, a3.Col, a3.Int("redeals"))
	}
	if n := f.linesOf(NBound); len(n) != 1 || strings.Join(n[0].Subjects, ",") != "a3" {
		t.Fatalf("the judgment names the primary at the bound alone: %+v", n)
	}
}

// R6 never removes the key for work it skipped because a stream was dropping,
// even when it used all the room on the other streams (1.3.5).
func TestDealKeepsTheKeyWhenTheRoomIsUsedAndAStreamWasSkipped(t *testing.T) {
	t.Parallel()
	const width = 2
	f := newFleetW(t, 1, width, "m1", "m2")
	f.w.must(Add(f.snap(), AddReq{Stream: "s2", Count: 5}))
	f.facts.Dropping["s1"] = true
	rp := f.plan(ruleDeal, "deal")
	if len(rp.Plan.Units) != 2*width || hasUnit(rp, "s1-1") {
		t.Fatalf("units: %+v", rp.Plan.Units)
	}
	if keyTexts(rp.Requeue) != "deal" || len(rp.Done) != 0 || len(rp.HeldBack) != 0 {
		t.Fatalf("the room is used and s1's card was skipped: done %s requeue %s held %s", keyTexts(rp.Done), keyTexts(rp.Requeue), keyTexts(rp.HeldBack))
	}
}

// R7 takes the lengths of the queues from the cells' counts, not from the cards
// the read loaded of them, and guards each member it touches at the count it
// read. The read loads every card a queue can give (levelHeadLimit, the widest a
// member may be), so the plan moves the newest of the queue, and the guard says
// what the cell really held, so a step whose queue has grown since is refused.
func TestLevelUsesTheCellCountsAndGuardsTheRealCount(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 0, "m1", "m2")
	for i := 1; i <= 4; i++ {
		readyCard(f, "p"+itoa(i), "m1", "s1", float64(i), nil)
	}
	f.partialOnly = true
	rp := f.plan(ruleLevel, "level")
	// 4/0: p4 (the newest) goes, then p3: 2/2
	if len(rp.Plan.Units) != 2 || rp.Plan.Units[0].Key != "p4.w1" || rp.Plan.Units[1].Key != "p3.w1" {
		t.Fatalf("moves: %+v", rp.Plan.Units)
	}
	cg := guardsOf(rp, guardCount)
	if len(cg) != 2 || cg[0].Member != "m1" || cg[0].Score != 4 || cg[1].Member != "m2" || cg[1].Score != 0 {
		t.Fatalf("the count guards are the counts the read gave, m1 4 and m2 0: %+v", cg)
	}
	if keyTexts(rp.Done) != "level" || len(rp.Requeue) != 0 {
		t.Fatalf("level after its moves: done %s requeue %s", keyTexts(rp.Done), keyTexts(rp.Requeue))
	}
	if why := f.failedGuard(rp); why != "" {
		t.Fatalf("the plan is refused on the state it read: %s", why)
	}
	// a fifth card since the read: the count guard refuses the step
	readyCard(f, "p5", "m1", "s1", 5, nil)
	if why := f.failedGuard(rp); !strings.Contains(why, "m1 holds 5") {
		t.Fatalf("a queue that grew since the read does not refuse the step: %q", why)
	}
}

// A queue longer than the cards its read loaded (a spill past the widest a
// member may be) is levelled a read at a time: the plan moves what it has and
// leaves its key, and a plan on the next read moves more, until the queues are
// level and the next plan writes nothing. A queue the read holds whole is
// levelled in the one plan (errata 3 amendment 10).
func TestLevelConvergesOnAQueueLongerThanItsRead(t *testing.T) {
	t.Parallel()
	// three members at the widest width: m1's spill of n cards goes to the two
	// others, a read of levelHeadLimit at a time, until the three hold n/3 each
	f := newFleetW(t, 0, MaxWidth, "m1", "m2", "m3")
	n := 2*levelHeadLimit + 4
	for i := 1; i <= n; i++ {
		readyCard(f, "p"+itoa(i), "m1", "s1", float64(i), nil)
	}
	f.partialOnly = true
	moves := 0
	for round := 1; ; round++ {
		if round > 5 {
			t.Fatalf("the queues are not level after %d reads: %d, %d and %d", round-1, f.snap().Fleet.Count("m1", Ready), f.snap().Fleet.Count("m2", Ready), f.snap().Fleet.Count("m3", Ready))
		}
		rp := f.run(ruleLevel, "level")
		moves += len(rp.Plan.Units)
		if round == 1 && (keyTexts(rp.Requeue) != "level" || len(rp.Plan.Units) != levelHeadLimit) {
			t.Fatalf("the first read holds %d of %d: %d moves, done %s requeue %s", levelHeadLimit, n, len(rp.Plan.Units), keyTexts(rp.Done), keyTexts(rp.Requeue))
		}
		if keyTexts(rp.Done) == "level" {
			break
		}
	}
	if a, b, c := f.snap().Fleet.Count("m1", Ready), f.snap().Fleet.Count("m2", Ready), f.snap().Fleet.Count("m3", Ready); a != n/3 || b != n/3 || c != n/3 || moves != n-n/3 {
		t.Fatalf("queues %d, %d and %d after %d moves", a, b, c, moves)
	}
	f.partialOnly = false
	quiet(t, "level", f.plan(ruleLevel, "level"), "level")
}

// ---- the second read of the cold read: keys a short read must not end, facts
// from the read, and the rows of the design that no test held

// panicsWith fails unless fn panics with a message naming want.
func panicsWith(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		t.Helper()
		r := recover()
		if r == nil {
			t.Fatalf("no panic, want one naming %q", want)
		}
		if msg, _ := r.(string); !strings.Contains(msg, want) {
			t.Fatalf("panic %v, want one naming %q", r, want)
		}
	}()
	fn()
}

// shortPlan is the registered rule's plan on the snapshot its registered read
// loads the way a release build loads it, and that snapshot: what the plan read
// that the read did not load is in the snapshot's log.
func (f *fleetT) shortPlan(rule string, texts ...string) (RulePlan, *Snapshot) {
	f.t.Helper()
	ks := agendaOf(texts...)
	s := f.load(rule, ks, false)
	return registered(f.t, rule).Plan(s, ks, f.now), s
}

// keptWhole fails unless the plan left every key where it was, wrote nothing and
// raised nothing, and its snapshot says the read was short with the text named.
func keptWhole(t *testing.T, what string, rp RulePlan, s *Snapshot, unreadText string, texts ...string) {
	t.Helper()
	if len(rp.Plan.Units) != 0 || len(rp.Plan.Rows) != 0 || len(rp.Intents) != 0 || len(rp.Guards) != 0 || len(rp.Notes) != 0 ||
		len(rp.Done) != 0 || len(rp.HeldBack) != 0 || keyTexts(rp.Requeue) != strings.Join(texts, ",") {
		t.Fatalf("%s: a plan on a short read wrote or ended something: %+v", what, rp)
	}
	err := s.UnloadedErr()
	if !errors.Is(err, ErrUnloaded) || !strings.Contains(err.Error(), unreadText) {
		t.Fatalf("%s: the read was short and the snapshot says %v, want %q", what, err, unreadText)
	}
}

// R6 and R7 are read, as registered, with no name to read by: no stream's front(s)
// and no member's ready head. A plan on such a read must not end its key when work
// is left behind what it could not see, and must end it when the state has nothing
// (the first round of this item removed the key with four cards left to deal and
// two members up, and a queue of four beside an empty one).
func TestRegisteredReadsOfDealAndLevelKeepTheirKeys(t *testing.T) {
	t.Parallel()
	deal, level := agendaOf("deal"), agendaOf("level")

	// R6: four dealable primaries, two members up with room
	f := newFleetT(t, 4, "m1", "m2")
	f.byRegistered = true
	if whole := f.oracle(ruleDeal, f.snap(), deal); len(whole.Plan.Units) != 4 {
		t.Fatalf("the state has four cards to deal: %+v", whole.Plan.Units)
	}
	rp, s := f.shortPlan(ruleDeal, "deal")
	keptWhole(t, "deal with work", rp, s, "front of s1", "deal")
	panicsWith(t, "front of s1", func() { registered(t, ruleDeal).Plan(f.loaded(ruleDeal, deal), deal, f.now) })

	// R6: nobody up, and cards that would raise the judgment
	g := newFleetT(t, 3, "m1")
	g.byRegistered = true
	g.setMember("m1", Down, "")
	if whole := g.oracle(ruleDeal, g.snap(), deal); len(whole.Notes) != 1 {
		t.Fatalf("the state has a judgment to raise: %+v", whole.Notes)
	}
	rp, s = g.shortPlan(ruleDeal, "deal")
	keptWhole(t, "deal with nobody up", rp, s, "front of s1", "deal")

	// R6: no room, nothing can be dealt, and the key goes: room frees queue it again
	h := newFleetW(t, 4, 2, "m1")
	h.run(ruleDeal, "deal") // m1 at its width of two; two primaries wait
	h.byRegistered = true
	rp, s = h.shortPlan(ruleDeal, "deal")
	quiet(t, "deal with no room", rp, "deal")
	if err := s.UnloadedErr(); err != nil {
		t.Fatalf("with no room the plan reads no front: %v", err)
	}

	// R6: no stream, nothing to deal: the list of streams says so, and the key goes
	e := newFleetT(t, 0, "m1", "m2")
	e.byRegistered = true
	rp, s = e.shortPlan(ruleDeal, "deal")
	quiet(t, "deal with no stream", rp, "deal")
	if err := s.UnloadedErr(); err != nil {
		t.Fatalf("a sprint with no stream is a read the plan can end: %v", err)
	}

	// R7: a queue of four beside an empty one: the plan cannot see a card of it
	l := newFleetT(t, 0, "m1", "m2")
	l.byRegistered = true
	for i := 1; i <= 4; i++ {
		readyCard(l, "p"+itoa(i), "m1", "s1", float64(i), nil)
	}
	if whole := l.oracle(ruleLevel, l.snap(), level); len(whole.Plan.Units) == 0 {
		t.Fatalf("the state has cards to move")
	}
	rp, s = l.shortPlan(ruleLevel, "level")
	keptWhole(t, "level with uneven queues", rp, s, "m1:ready head", "level")
	panicsWith(t, "m1:ready head", func() { registered(t, ruleLevel).Plan(l.loaded(ruleLevel, level), level, l.now) })

	// R7: the queues are level by the counts, which the fleet read gave: the key goes
	k := newFleetT(t, 0, "m1", "m2")
	k.byRegistered = true
	readyCard(k, "p1", "m1", "s1", 1, nil)
	rp, s = k.shortPlan(ruleLevel, "level")
	quiet(t, "level with level queues", rp, "level")
	if err := s.UnloadedErr(); err != nil {
		t.Fatalf("level queues need no head: %v", err)
	}
}

// A plan on a read that was short keeps every key of its rule and writes nothing,
// for every rule: what it decided on less than its state holds is never the end of
// a key. The read is short of the sprint's own keys, which every rule's plan takes
// from its read.
func TestAPlanOnAShortReadKeepsEveryKey(t *testing.T) {
	t.Parallel()
	for _, sc := range fieldScenarios() {
		f := sc.build(t)
		f.mutate = func(rp *ReadPlan) { rp.Ranges = nil }
		rp, s := f.shortPlan(sc.rule, sc.keys...)
		keptWhole(t, sc.name, rp, s, unloadedKeyMessage, sc.keys...)
		ks := agendaOf(sc.keys...)
		panicsWith(t, unloadedKeyMessage, func() { registered(t, sc.rule).Plan(f.loaded(sc.rule, ks), ks, f.now) })
	}
}

// factScenario is a state whose plan depends on one sprint key, and the facts that
// make it so.
type factScenario struct {
	name  string
	rule  string
	key   string // the fact, as ruleFacts has it
	keys  []string
	build func(t *testing.T) *fleetT
}

func factScenarios() []factScenario {
	return []factScenario{
		// zed is noticed already: R1 writes nothing for it, and for a stranger not noticed it would guard and notice
		{"seen, a noticed stranger", ruleSeen, factStrangers, []string{"seen:zed"}, func(t *testing.T) *fleetT {
			f := newFleetT(t, 0, "m1")
			f.facts.Strangers["zed"] = true
			return f
		}},
		// m1's beat raced the pop: R2 leaves it up
		{"down, a beat raced the pop", ruleDown, factBeats, []string{"down:m1"}, func(t *testing.T) *fleetT {
			f := newFleetT(t, 2, "m1", "m2")
			f.run(ruleDeal, "deal")
			f.now.R += 500_000
			f.facts.BeatDue["m1"] = f.now.R + 15_000
			return f
		}},
		{"down, a stream being dropped", ruleDown, factDropping, []string{"down:m1"}, func(t *testing.T) *fleetT {
			f := newFleetT(t, 2, "m1", "m2")
			f.run(ruleDeal, "deal")
			f.now.R += 500_000
			f.facts.Dropping["s1"] = true
			return f
		}},
		{"deal, a stream being dropped", ruleDeal, factDropping, []string{"deal"}, func(t *testing.T) *fleetT {
			f := newFleetT(t, 2, "m1", "m2")
			f.w.must(Add(f.snap(), AddReq{Stream: "s2", Count: 2}))
			f.facts.Dropping["s1"] = true
			return f
		}},
		{"level, a stream being dropped", ruleLevel, factDropping, []string{"level"}, func(t *testing.T) *fleetT {
			f := newFleetT(t, 0, "m1", "m2")
			readyCard(f, "p1", "m1", "s1", 1, nil)
			readyCard(f, "p2", "m1", "s2", 2, nil)
			f.facts.Dropping["s2"] = true
			return f
		}},
	}
}

// Every sprint key a rule's read asks for is one its plan takes from the answer,
// and every one its plan takes is asked: the reads' ranges are exactly ruleFacts,
// and a read with one left out makes the plan differ or read what was not loaded.
// The plan is the registered rule's, made on the snapshot its read loaded, and the
// facts are the store's (the twin answers them), never given to the plan.
func TestEveryFactOfEveryReadIsNeededByItsPlan(t *testing.T) {
	t.Parallel()
	asked := map[string]bool{}
	for _, sc := range factScenarios() {
		f := sc.build(t)
		ks := agendaOf(sc.keys...)
		rp := f.plan(sc.rule, sc.keys...) // on the whole read: the same as the plan on the whole sprint
		_ = rp
		var keys []string
		for _, r := range f.readPlan(sc.rule, ks).Ranges {
			if r.Table != "" || r.Cell != "" || r.Key == "" || r.Limit != factLimit[r.Key] {
				t.Fatalf("%s: a range that is not a whole sprint key at its limit: %+v", sc.name, r)
			}
			keys = append(keys, r.Key)
		}
		if strings.Join(keys, ",") != strings.Join(ruleFacts[sc.rule], ",") {
			t.Fatalf("%s: the read asks for %v, the rule's facts are %v", sc.name, keys, ruleFacts[sc.rule])
		}
		f.mutate = func(rp *ReadPlan) {
			var rest []RangeQ
			for _, r := range rp.Ranges {
				if r.Key != sc.key {
					rest = append(rest, r)
				}
			}
			rp.Ranges = rest
		}
		if !f.differs(sc.rule, ks) {
			t.Errorf("%s: the plan does not need %q, which its read asks for", sc.name, sc.key)
		}
		asked[sc.rule+" "+sc.key] = true
	}
	for rule, kinds := range ruleFacts {
		for _, k := range kinds {
			if !asked[rule+" "+k] {
				t.Errorf("%s: no scenario for its fact %q", rule, k)
			}
		}
	}
}

// A fact that its answer cut (more members than it was read for), or a beat that
// came with no score, is not read: a member not in it is not known to be absent,
// and a fresh beat with no score is no beat. The plan says so and keeps its keys.
func TestAFactThatWasNotReadWholeIsUnread(t *testing.T) {
	t.Parallel()
	// 251 machines noticed, and a read for the most a sprint has
	f := newFleetT(t, 0, "m1")
	for i := 0; i <= MaxMembers; i++ {
		f.facts.Strangers["x"+itoa(i)] = true
	}
	rp, s := f.shortPlan(ruleSeen, "seen:x0", "seen:zed")
	keptWhole(t, "strangers cut", rp, s, factStrangers+" holds more than the read did", "seen:x0", "seen:zed")
	delete(f.facts.Strangers, "x0")
	if rp, s = f.shortPlan(ruleSeen, "seen:x1"); s.UnloadedErr() != nil || keyTexts(rp.Done) != "seen:x1" {
		t.Fatalf("exactly the most is read whole: %+v %v", rp, s.UnloadedErr())
	}

	// a beat answered with no scores
	g := newFleetT(t, 2, "m1", "m2")
	g.run(ruleDeal, "deal")
	g.facts.BeatDue["m1"] = g.now.R + 15_000
	ks := agendaOf("down:m1")
	read := g.readPlan(ruleDown, ks)
	ans := fleetTwin{whole: g.snap(), facts: g.facts}.Answer(read)
	for i := range ans.Tset {
		ans.Tset[i].Scores = nil
	}
	s, err := loadPartial(read, ans, false)
	if err != nil {
		t.Fatalf("%v", err)
	}
	keptWhole(t, "a beat with no score", registered(t, ruleDown).Plan(s, ks, g.now), s, factBeats+" without its scores", "down:m1")
}

// The facts the plan sees are the answer's: the beat entries with their scores, the
// machines noticed and the streams being dropped, as the twin gives them.
func TestFactsAreTheAnswersOfTheRead(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 2, "m1", "m2")
	f.facts.BeatDue["m1"], f.facts.BeatDue["m2"] = 40_000, 30_000
	f.facts.Strangers["zed"], f.facts.Strangers["old"] = true, false
	f.facts.Dropping["s9"] = true
	for rule, want := range map[string]fleetFacts{
		ruleSeen:  {BeatDue: map[string]int64{}, Strangers: map[string]bool{"zed": true}, Dropping: map[string]bool{}},
		ruleDown:  {BeatDue: map[string]int64{"m1": 40_000, "m2": 30_000}, Strangers: map[string]bool{}, Dropping: map[string]bool{"s9": true}},
		ruleDeal:  {BeatDue: map[string]int64{}, Strangers: map[string]bool{}, Dropping: map[string]bool{"s9": true}},
		ruleLevel: {BeatDue: map[string]int64{}, Strangers: map[string]bool{}, Dropping: map[string]bool{"s9": true}},
	} {
		s := f.loaded(rule, agendaOf(map[string]string{ruleSeen: "seen:m1", ruleDown: "down:m1", ruleDeal: "deal", ruleLevel: "level"}[rule]))
		if got := factsOf(s, rule); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: facts %+v, want %+v", rule, got, want)
		}
	}
	// a snapshot built whole holds no sprint key
	if got := factsOf(f.snap(), ruleDown); len(got.BeatDue)+len(got.Strangers)+len(got.Dropping) != 0 {
		t.Fatalf("a whole snapshot has facts: %+v", got)
	}
}

// A shape that names streams and no members reads every member the fleet has, where
// the design's most is what a listing over no units is: the plan deals as it does on
// the whole sprint. A shape that names fewer members than the fleet has reads a
// listing that says it has more, and a plan on it is refused, never planned on the
// few.
func TestReadOfAShapeWithNoMembersReadsThemAll(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 4, "m1", "m2", "m3")
	if u := (fleetShape{Streams: []string{"s1"}}).units(); u != 0 {
		t.Fatalf("units of a shape with no members: %d, want 0 (every member)", u)
	}
	ks := agendaOf("deal")
	rp := dealReadFor(fleetShape{Streams: []string{"s1"}}, f.bounds, 0)
	if rp.Sprint[0].Kind != QueryFleet || rp.Sprint[0].Units != 0 {
		t.Fatalf("the fleet query: %+v", rp.Sprint[0])
	}
	s, err := LoadPartial(rp, fleetTwin{whole: f.snap(), facts: f.facts}.Answer(rp))
	if err != nil {
		t.Fatalf("%v", err)
	}
	got := registered(t, ruleDeal).Plan(s, ks, f.now)
	if len(got.Plan.Units) != 4 || len(s.UpMembers()) != 3 {
		t.Fatalf("dealt %d of 4 to %v", len(got.Plan.Units), s.UpMembers())
	}
	samePlan(t, "deal", got, f.oracle(ruleDeal, f.snap(), ks))

	// one member named of three: the listing is cut and says so
	rp = dealReadFor(fleetShape{Streams: []string{"s1"}, Members: []string{"m1"}}, f.bounds, 0)
	if rp.Sprint[0].Units != 1 {
		t.Fatalf("the fleet query: %+v", rp.Sprint[0])
	}
	ans := fleetTwin{whole: f.snap(), facts: f.facts}.Answer(rp)
	if a := ans.Sprint[0]; !a.HasMore || len(a.Rows) != 1 {
		t.Fatalf("a listing cut at its units must say there are more: %+v", a)
	}
	s, err = LoadPartial(rp, ans)
	if err != nil {
		t.Fatalf("%v", err)
	}
	panicsWith(t, "fleet rows", func() { registered(t, ruleDeal).Plan(s, ks, f.now) })
	s, err = loadPartial(rp, ans, false)
	if err != nil {
		t.Fatalf("%v", err)
	}
	keptWhole(t, "a shape that names one member of three", registered(t, ruleDeal).Plan(s, ks, f.now), s, "fleet rows", "deal")
}

// A member an earlier chunk marked down, and not held, has its remaining cards
// dealt away by a step that carries beatstale like the one that marked it: a beat
// that lands before the step applies refuses it (2.3 R2: X, when the key came from
// the pop and m is not held). A held member's key came from fleet down, and has none.
func TestDownBeatStaleGuardWhenAnEarlierChunkMarkedTheMember(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 4, "m1", "m2")
	f.run(ruleDeal, "deal")
	f.setMember("m1", Down, "") // an earlier chunk marked it down, and cards are left
	rp := f.plan(ruleDown, "down:m1")
	g := guardsOf(rp, guardBeatStale)
	if len(g) != 1 || g[0].Member != "m1" || g[0].Key != "beat:m1" || g[0].Score != 0 {
		t.Fatalf("the beatstale guard of an unheld member whose cards move: %+v", rp.Guards)
	}
	if why := f.failedGuard(rp); why != "" {
		t.Fatalf("a plan on the state it read is refused: %s", why)
	}
	f.facts.BeatDue["m1"] = f.now.R + 15_000 // a beat lands before the step applies
	if why := f.failedGuard(rp); why != "m1 has a beat above R" {
		t.Fatalf("a beat since the read does not refuse the step: %q", why)
	}
	// the same member, held: no beat is looked for
	h := newFleetT(t, 4, "m1", "m2")
	h.run(ruleDeal, "deal")
	h.setMember("m1", Down, "2030-01-02T03:04:05Z")
	if rp := h.plan(ruleDown, "down:m1"); len(guardsOf(rp, guardBeatStale)) != 0 || len(rp.Plan.Units) == 0 {
		t.Fatalf("a held member's step: %+v", rp.Guards)
	}
}

// A withdrawn card is dealt again to the up member with the shortest queue whatever
// its primary's avoid names: the avoid is written for a new work card, and today's
// redeal does not look at it (2.3 R6).
func TestDealAgainIgnoresTheAvoidMember(t *testing.T) {
	t.Parallel()
	f := newFleetT(t, 1, "m1", "m2")
	putWorkCard(f, "s1-1", "m2", Withdrawn, 1, map[string]string{"withdrawn": stamp(t0)})
	f.edit(f.snap().Work, "s1-1", map[string]string{"avoid": "m1"})
	putWorkCard(f, "x1", "m2", Ready, 50, nil) // m2 holds one: m1, the avoided member, is the shortest queue
	f.run(ruleDeal, "deal")
	if c := f.snap().Fleet.Card("s1-1.w1"); c.Row != "m1" || c.Col != Ready || c.Int("gen") != 3 {
		t.Fatalf("dealt again to %s:%s gen %d, want m1:ready gen 3 (the avoided member has the shortest queue)", c.Row, c.Col, c.Int("gen"))
	}
	// a new work card for the same primary at attempt 1 would have gone to m2
	g := newFleetT(t, 1, "m1", "m2")
	g.edit(g.snap().Work, "s1-1", map[string]string{"avoid": "m1", "attempt": "1"})
	putWorkCard(g, "x1", "m2", Ready, 50, nil)
	g.run(ruleDeal, "deal")
	if c := g.snap().Fleet.Card("s1-1.w2"); c == nil || c.Row != "m2" {
		t.Fatalf("a new card honours avoid: %+v", c)
	}
}

// The numbers of the rules are the design's, the rules' own.
func TestRuleConstantsAreTheDesigns(t *testing.T) {
	t.Parallel()
	if DefaultWidth != 64 || RuleUntakenDeadline != 15*time.Minute || RuleBeatDeadline != 15*time.Second {
		t.Fatalf("default width %d, untaken deadline %v, beat deadline %v", DefaultWidth, RuleUntakenDeadline, RuleBeatDeadline)
	}
	if got := untakenDue(1000); got != 1000+15*60_000 {
		t.Fatalf("a card dealt at R = 1000 is due at %d", got)
	}
}

// A full chunk of R2 is more than one step holds: 2,000 working cards at the bound
// and nobody up is 2,001 units (the control card, and a unit a card) and 4,001
// changed members, each withdrawn card moving its primary too, against the step's
// bound of 2,000 changed members. The step builder always cuts it, and the control
// card's guard is one unit: open question 4.
func TestDownAFullChunkIsMoreThanOneStep(t *testing.T) {
	t.Parallel()
	s := downSprintT(t, 0, fleetChunk)
	for _, m := range []string{"m2", "m3"} {
		s.MemberCtl(m).Fields["status"] = Down
	}
	for _, c := range s.Fleet.LoadedCards() {
		if c.Col == Working {
			c.Fields["redeals"] = itoa(RuleMaxRedeals)
		}
	}
	f := newFleetOn(t, &world{t: t, s: s})
	rp := f.plan(ruleDown, "down:m1")
	changes := 0
	for _, u := range rp.Plan.Units {
		changes += len(u.Changes)
	}
	if len(rp.Plan.Units) != fleetChunk+1 || changes != 2*fleetChunk+1 || keyTexts(rp.Done) != "down:m1" {
		t.Fatalf("%d units, %d changes, done %s", len(rp.Plan.Units), changes, keyTexts(rp.Done))
	}
}

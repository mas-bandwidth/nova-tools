package machine

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The real rules' steps on the twin, end to end as the loop runs them: a rule
// of the real RuleTable reads through its registered Read (the twin answers
// the read, sprint.LoadPartial loads it), plans through its registered Plan,
// builds through StepBuilder, and each body applies, the rule's writes to the
// sprint's own keys (TimePart) on the first request. The closed gaps each
// show here: R3 and R15 read their jopen field (gap a) and their notes carry
// a cause J accepts (d); R3's release and R6's deal carry X's sent guard (b);
// R3 and R15's rcount set guards are Layer 1 rcount entries and R15's counter
// X's counter guard (e); R17's clock fields ride the sprint part (c).

// realRule is the rule of the real RuleTable of the name.
func realRule(t *testing.T, name string) sprint.Rule {
	t.Helper()
	for _, r := range sprint.RuleTable() {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no rule %s in the rule table", name)
	return sprint.Rule{}
}

// realLoop is a loop over the world with the rules given (nil is the real
// RuleTable) and the real builder, StepBuilder: the loop the machine runs.
func (w *world) realLoop(rules []sprint.Rule) *Loop {
	w.t.Helper()
	l, err := NewLoop(Config{Names: testNames, Owner: "token-a", Name: "a", Rules: rules})
	if err != nil {
		w.t.Fatal(err)
	}
	return l
}

// leased is a real loop that holds the lease on the world, for the generation
// its rule steps carry.
func (w *world) leased() *Loop {
	w.t.Helper()
	l := w.realLoop(nil)
	w.tick(l, &counting{c: w.tw})
	if l.gen == 0 {
		w.t.Fatal("the loop took no lease")
	}
	return l
}

// now is the world's running time: the twin keeps no stopped time here, so R
// is the wall time.
func (w *world) now() sprint.Now {
	ms := w.clk.now().UnixMilli()
	return sprint.Now{R: ms, Wall: ms, Running: true}
}

// readReal reads a real rule's keys through its registered Read, as the loop
// sends it (readRequest, readAnswer), and loads the snapshot; every key is
// read.
func (w *world) readReal(rule sprint.Rule, keys ...sprint.AgendaKey) *sprint.Snapshot {
	w.t.Helper()
	rp, left := rule.Read(keys, sprint.L1ReadBounds(), 0)
	if len(left) != 0 {
		w.t.Fatalf("%s left keys unread: %v", rule.Name, left)
	}
	rr, err := readRequest(testNames, "0", rp)
	if err != nil {
		w.t.Fatalf("%s's read: %v", rule.Name, err)
	}
	res, err := sprintfn.Read(context.Background(), w.tw, rr)
	if err != nil || res.Read == nil {
		w.t.Fatalf("%s's read: %v %+v", rule.Name, err, res.Refusal)
	}
	ans, err := readAnswer(rp, res.Read)
	if err != nil {
		w.t.Fatal(err)
	}
	snap, err := sprint.LoadPartial(rp, ans)
	if err != nil {
		w.t.Fatalf("%s's read does not load: %v", rule.Name, err)
	}
	return snap
}

// planReal plans a real rule's keys on its own read, and fails on a plan that
// read what it did not ask for.
func (w *world) planReal(rule sprint.Rule, keys ...sprint.AgendaKey) sprint.RulePlan {
	w.t.Helper()
	snap := w.readReal(rule, keys...)
	rp := rule.Plan(snap, keys, w.now())
	if err := snap.UnloadedErr(); err != nil {
		w.t.Fatalf("%s planned on what it did not read: %v", rule.Name, err)
	}
	return rp
}

// cutReal cuts a plan into requests through the loop's own cut(), as a tick
// does (its builder, StepBuilder; the time part on the first request); a plan
// it cannot cut gives no requests and parks the keys, as the report says.
func (w *world) cutReal(l *Loop, rule string, rp sprint.RulePlan) (*planned, Report) {
	w.t.Helper()
	rep := Report{Read: map[string]int{}, Left: map[string]int{}, Refused: map[string]int{}, Halved: map[string]int{}}
	p, err := l.cut(sprint.Rule{Name: rule}, Batch{Rule: rule, Keys: rp.Done}, rp, &rep)
	if err != nil {
		w.t.Fatalf("%s's cut: %v", rule, err)
	}
	return p, rep
}

// applyReal cuts a plan through the loop's cut() and applies each request on
// the world, in order; it returns the requests.
func (w *world) applyReal(l *Loop, rule string, rp sprint.RulePlan) []*sprintfn.Request {
	w.t.Helper()
	p, rep := w.cutReal(l, rule, rp)
	if p == nil || len(p.reqs) == 0 {
		w.t.Fatalf("%s built no requests: parked %v, owed %+v", rule, rep.Parked, l.owed.notes)
	}
	for _, req := range p.reqs {
		w.step(req)
	}
	return p.reqs
}

func keyOf(text string) sprint.AgendaKey { return sprint.AgendaKey{Key: text, Seq: 1} }

// noteOf is the plan's note request of the op and type, nil when none.
func noteOf(rp sprint.RulePlan, op, typ string) *sprint.NoteReq {
	for i, n := range rp.Notes {
		if n.Op == op && n.Type == typ {
			return &rp.Notes[i]
		}
	}
	return nil
}

func hasGuardKind(gs []sprint.XGuard, kind string) bool {
	return slices.ContainsFunc(gs, func(g sprint.XGuard) bool { return g.Kind == kind })
}

func hasEntryKind(es []tset.Entry, kind string) bool {
	return slices.ContainsFunc(es, func(e tset.Entry) bool { return e.Kind == kind })
}

// TestRealRuleResolveApplies (R3, 2.3): on stream s1 a waiting primary below
// the sentinel is released under X's sent guard; on s2 the sentinel with
// nothing open before it is reached, under a Layer 1 rcount entry, and
// "sentinel reached" opens on it with its cause. Read again, jopen:G says it
// is open, and R3 plans no second open (gaps a, b, d, e).
func TestRealRuleResolveApplies(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1", "s2")
	w.verb(create("s1:waiting", waiting(), "p1"))
	w.verb(tset.Entry{Kind: "create", Table: sprint.Work, To: "s1:waiting", IDs: []string{"g1"}, Scores: []string{"20"},
		Set: map[string]string{"kind": "sentinel", "open": "0"}, About: []string{"g1"}})
	w.verb(tset.Entry{Kind: "create", Table: sprint.Work, To: "s2:waiting", IDs: []string{"g2"}, Scores: []string{"5"},
		Set: map[string]string{"kind": "sentinel", "open": "0"}, About: []string{"g2"}})
	l := w.leased()
	resolve := realRule(t, "resolve")
	rp := w.planReal(resolve, keyOf("resolve:s1"), keyOf("resolve:s2"))
	if n := noteOf(rp, "open", sprint.NSentinelReached); n == nil || n.Cause != sprint.ReachedCause || !slices.Equal(n.Subjects, []string{"g2"}) {
		t.Fatalf("the reach of g2: %+v", rp.Notes)
	}
	reqs := w.applyReal(l, "resolve", rp)
	first := reqs[0].Body
	if !hasGuardKind(first.Guards, sprintfn.XGuardSent) || !hasEntryKind(first.Entries, "rcount") {
		t.Fatalf("R3's guards as the store checks them: %+v %+v", first.Guards, first.Entries)
	}
	if got := w.place("p1"); got != "s1:ready" {
		t.Fatalf("p1 is at %q", got)
	}
	if v := w.hash("jopen:g2@0")[sprint.NSentinelReached+"|"+sprint.ReachedCause]; !strings.HasPrefix(v, "n") {
		t.Fatalf("jopen:g2: %v", w.hash("jopen:g2@0"))
	}
	again := w.planReal(resolve, keyOf("resolve:s2"))
	if n := noteOf(again, "open", sprint.NSentinelReached); n != nil {
		t.Fatalf("R3 read its open judgment and opened it again: %+v", again.Notes)
	}
}

// TestRealRuleDealApplies (R6, 2.3): the deal of two fresh primaries, planned
// by the rule table's deal on the world's cards, and its step, with the count
// entry over the member's ready cell and X's sent guard on s1, applies whole
// (gap b). R6's registered Read names no stream's front (Rule.Read is given no
// streams: rules_fleet.go readDeal, open question 4), so a plan on it deals
// nothing and keeps its key; the snapshot here is the world's, whole.
func TestRealRuleDealApplies(t *testing.T) {
	t.Parallel()
	w, s, deal := dealWorld(t, "p1", "p2")
	l := w.leased()
	rp := deal.Plan(s, []sprint.AgendaKey{keyOf("deal")}, w.now())
	if len(rp.Plan.Units) != 2 {
		t.Fatalf("the deal planned %d units: %+v", len(rp.Plan.Units), rp)
	}
	reqs := w.applyReal(l, "deal", rp)
	if b := reqs[0].Body; !hasGuardKind(b.Guards, sprintfn.XGuardSent) || !hasEntryKind(b.Entries, "count") {
		t.Fatalf("R6's guards as the store checks them: %+v %+v", b.Guards, b.Entries)
	}
	for _, p := range []string{"p1", "p2"} {
		if got, card := w.place(p), w.placeIn(sprint.Fleet, p+".w1"); got != "s1:working" || card != "m1:ready" {
			t.Fatalf("%s is at %q and its work card at %q", p, got, card)
		}
	}
}

// TestRealRuleDoneApplies (R15, 2.3): with every card of s1 landed, "the sprint
// is done" opens on the sprint under a Layer 1 rcount entry of the open cells
// and X's counter guard on next.streams; read again, jopen:sprint says it is
// open and R15 plans nothing; with a card added, it closes (gaps a, d, e).
func TestRealRuleDoneApplies(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	w.verb(create("s1:landed", map[string]string{"kind": "primary"}, "d1"))
	l := w.leased()
	done := realRule(t, "done")
	rp := w.planReal(done, keyOf("done"))
	if n := noteOf(rp, "open", sprint.NSprintDone); n == nil || n.Cause != sprint.SprintDoneCause {
		t.Fatalf("the sprint is done: %+v", rp.Notes)
	}
	reqs := w.applyReal(l, "done", rp)
	if b := reqs[0].Body; !hasGuardKind(b.Guards, sprintfn.XGuardCounter) || !hasEntryKind(b.Entries, "rcount") {
		t.Fatalf("R15's guards as the store checks them: %+v %+v", b.Guards, b.Entries)
	}
	field := sprint.NSprintDone + "|" + sprint.SprintDoneCause
	if v := w.hash("jopen:" + sprint.SprintSubject + "@0")[field]; !strings.HasPrefix(v, "n") {
		t.Fatalf("jopen of the sprint: %v", w.hash("jopen:"+sprint.SprintSubject+"@0"))
	}
	if again := w.planReal(done, keyOf("done")); len(again.Notes) != 0 {
		t.Fatalf("R15 read its open judgment and wrote again: %+v", again.Notes)
	}
	w.verb(create("s1:waiting", waiting(), "p9"))
	rp = w.planReal(done, keyOf("done"))
	if n := noteOf(rp, "close", sprint.NSprintDone); n == nil {
		t.Fatalf("work was added: %+v", rp.Notes)
	}
	w.applyReal(l, "done", rp)
	if _, open := w.hash("jopen:" + sprint.SprintSubject + "@0")[field]; open {
		t.Fatalf("the judgment is still open: %v", w.hash("jopen:"+sprint.SprintSubject+"@0"))
	}
}

// TestRealRuleLateApplies (R11, 2.3): the rule table's late, on the world's
// cards: a stream idle past its span has due_idle unset and is told once, and
// a verb in parts whose cut clock ran out is judged, its guard that no cut
// entry of the op lies above the wall time checked by X. R11's registered Read
// names the dropping marks and the cut entries, which IT30's queries do not
// answer yet (the all-marks read and the cut read are owed), so the snapshot
// here is the world's, whole.
func TestRealRuleLateApplies(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	w.verb(tset.Entry{Kind: "rows", Table: sprint.Merge, Add: []string{"s1"}})
	ctl := sprint.CtlID("s1")
	w.verb(tset.Entry{Kind: "create", Table: sprint.Merge, To: "s1:ctl", IDs: []string{ctl}, Scores: []string{"0"},
		Set: map[string]string{"state": sprint.StreamMerging, "due_idle": "1"}, About: []string{ctl}})
	l := w.leased()
	now := w.now()
	s := &sprint.Snapshot{Now: w.clk.now(), Work: sprint.NewTable(sprint.Work), Readers: sprint.NewTable(sprint.Readers),
		Merge: sprint.NewTable(sprint.Merge), Fleet: sprint.NewTable(sprint.Fleet)}
	s.Work.SetRows([]string{"s1"})
	s.Merge.SetRows([]string{"s1"})
	s.Merge.Put(&sprint.Card{ID: ctl, Row: "s1", Col: "ctl", Rev: 1, Fields: map[string]string{"state": sprint.StreamMerging, "due_idle": "1"}})
	rp := realRule(t, "late").Plan(s, []sprint.AgendaKey{keyOf("idle:s1"), keyOf("late:cut:op1")}, now)
	if len(rp.Plan.Units) != 1 || noteOf(rp, "open", sprint.NCutStopped) == nil || len(rp.Guards) == 0 {
		t.Fatalf("R11's plan: %+v", rp)
	}
	w.applyReal(l, "late", rp)
	if _, set := w.fields(sprint.Merge, ctl)["due_idle"]; set {
		t.Fatalf("due_idle is still set: %v", w.fields(sprint.Merge, ctl))
	}
	if v := w.hash("jopen:op1@0")[sprint.NCutStopped+"|cut"]; !strings.HasPrefix(v, "n") {
		t.Fatalf("jopen:op1: %v", w.hash("jopen:op1@0"))
	}
}

// fields are a card's fields on the world.
func (w *world) fields(table, id string) map[string]string {
	w.t.Helper()
	res, err := sprintfn.Read(context.Background(), w.tw, &sprintfn.ReadRequest{Epoch: "0",
		Tset: []tset.ReadQuery{{Kind: "ids", Table: table, IDs: []string{id}}}})
	if err != nil || res.Read == nil {
		w.t.Fatalf("read %s: %v %+v", id, err, res.Refusal)
	}
	out := map[string]string{}
	for name, f := range res.Read.Tset[0].Records[0].Fields {
		if f.Present {
			out[name] = f.Value
		}
	}
	return out
}

// stoppedWorld is a world whose machine was initialised STOPPED, and the clock
// as R17 reads it.
func stoppedWorld(t *testing.T) (*world, sprint.Clock) {
	t.Helper()
	w := newWorld(t)
	w.step(&sprintfn.Request{Epoch: "0", Meta: sprintfn.Meta{Verb: "init", Actor: "coordinator"}, Clock: &sprintfn.ClockPart{Verb: sprintfn.ClockInit}})
	return w, sprint.Clock{StoppedSinceMs: w.clk.now().UnixMilli()}
}

// TestRealRuleStoppedLookApplies (R17, 2.3; errata 3 H7, H16): a look that
// finds a move due sets due_since_ms; ten minutes later it raises "the machine
// is STOPPED and moves are due" and sets stopraised_ms; a look with no move due
// closes it and clears both. Each step is guarded on the clock fields as read
// and on the counters (X's clock and counter guards) and writes the clock
// through the sprint part's Time (gap c).
func TestRealRuleStoppedLookApplies(t *testing.T) {
	t.Parallel()
	w, c := stoppedWorld(t)
	l := w.leased()
	due := sprint.RulePlan{Plan: sprint.Plan{Units: []sprint.Unit{{Key: "p1", Changes: []sprint.Change{{Table: sprint.Work,
		Entry: ntable.BatchMemberEntry{ID: "p1", Move: &ntable.MemberMoveOp{Row: "s1", Col: "ready"}}}}}}}}
	inputs := sprint.StopInputs{Next: 100000000, Streams: 1}
	look := func(dry []sprint.RulePlan, c sprint.Clock, open bool) sprint.RulePlan {
		return sprint.StoppedLook(dry, sprint.StopRead{Clock: c, Wall: w.clk.now().UnixMilli(), Open: open, Inputs: inputs})
	}
	rp := look([]sprint.RulePlan{due}, c, false)
	if rp.Sprint.Clock == nil || rp.Sprint.Clock.DueSince == nil {
		t.Fatalf("the first look: %+v", rp.Sprint)
	}
	reqs := w.applyReal(l, "stopped", rp)
	if reqs[0].Sprint == nil || reqs[0].Sprint.Time == nil || !hasGuardKind(reqs[0].Body.Guards, sprintfn.XGuardCounter) {
		t.Fatalf("R17's step: %+v %+v", reqs[0].Sprint, reqs[0].Body.Guards)
	}
	since := w.clk.now().UnixMilli()
	if got := w.hash("clock")["due_since_ms"]; got != strconv.FormatInt(since, 10) {
		t.Fatalf("due_since_ms is %q", got)
	}
	c.DueSinceMs = since
	w.clk.add(11 * time.Minute)
	rp = look([]sprint.RulePlan{due}, c, false)
	if noteOf(rp, "open", sprint.NStoppedWithDue) == nil || rp.Sprint.Clock == nil || rp.Sprint.Clock.StopRaised == nil {
		t.Fatalf("the raise: %+v %+v", rp.Notes, rp.Sprint)
	}
	w.applyReal(l, "stopped", rp)
	if got := w.hash("clock")["stopraised_ms"]; got != strconv.FormatInt(c.StoppedSinceMs, 10) {
		t.Fatalf("stopraised_ms is %q", got)
	}
	c.StopRaisedMs = c.StoppedSinceMs
	rp = look(nil, c, true)
	if noteOf(rp, "close", sprint.NStoppedWithDue) == nil {
		t.Fatalf("the close: %+v", rp.Notes)
	}
	w.applyReal(l, "stopped", rp)
	if h := w.hash("clock"); h["due_since_ms"] != "" || h["stopraised_ms"] != "" {
		t.Fatalf("the clock after the close: %v", h)
	}
}

// TestRealRuleRemindApplies (R14, 2.3): the rule table's remind, on a goal read
// as it is (IT30 has no goal query yet: the answer is the read's words, given),
// moves remind:<person> to R + 5 min and claims the goal record at R with the
// step's lease generation, through the sprint part's Time, under X's
// due guard on the popped entry, absent as read (gap c).
func TestRealRuleRemindApplies(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	l := w.leased()
	remind := realRule(t, "remind")
	keys := []sprint.AgendaKey{keyOf("remind:ann")}
	plan, _ := remind.Read(keys, sprint.L1ReadBounds(), 0)
	ans := sprint.ReadAnswer{Epoch: "0", ActiveEpoch: "0", TimeMS: sprint.Decimal(strconv.FormatInt(w.clk.now().UnixMilli(), 10))}
	for _, q := range plan.Sprint {
		ans.Sprint = append(ans.Sprint, sprint.Answer{Kind: q.Kind, Time: &sprint.TimeAnswer{Goals: map[string]sprint.GoalFact{"ann": {Exists: true}}}})
	}
	snap, err := sprint.LoadPartial(plan, ans)
	if err != nil {
		t.Fatal(err)
	}
	now := w.now()
	rp := remind.Plan(snap, keys, now)
	if len(rp.Sprint.Due) != 1 || len(rp.Sprint.Goal) != 1 {
		t.Fatalf("R14's writes: %+v", rp.Sprint)
	}
	w.applyReal(l, "remind", rp)
	if at := w.zset("due@0")["remind:ann"]; int64(at) != rp.Sprint.Due[0].At {
		t.Fatalf("remind:ann is at %v", at)
	}
	if g := w.hash("goal:ann"); g["claimed_r"] != strconv.FormatInt(now.R, 10) || g["claimed_gen"] != strconv.FormatUint(l.gen, 10) {
		t.Fatalf("ann's claim: %v", g)
	}
}

// TestParkedKeyApplies (1.3.5): OnBug's park of a key is the sprint part's
// park, with the rule and the code, and the key leaves the agenda through the
// part, the park written first (A1).
func TestParkedKeyApplies(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	l := w.leased()
	rp, _ := sprint.OnBug("deal", keyOf("deal"), "REQUEST", "1 entry", 0)
	reqs := w.applyReal(l, "deal", rp)
	if len(reqs[0].Body.Done) != 0 {
		t.Fatalf("a parked key also finished: %v", reqs[0].Body.Done)
	}
	if v := w.hash("parked@0")["deal"]; !strings.HasPrefix(v, "REQUEST\tdeal") {
		t.Fatalf("the park: %q", v)
	}
}

// timeRule is a real time rule as a tick runs it, its read answered here: IT30
// has no goal or tick query yet, so the rule's own Read is not sent. The tick
// sends a read the twin answers (one work id), and the rule's Plan runs on the
// snapshot its own Read plans, loaded with what answer gives for each of its
// sprint queries. Everything else is the loop's: the pop, the dispatch, cut()
// with StepBuilder and the time part, the steps.
func (w *world) timeRule(name string, answer func(q sprint.SprintQ) sprint.Answer) sprint.Rule {
	w.t.Helper()
	r := realRule(w.t, name)
	read := func(keys []sprint.AgendaKey, _ sprint.ReadBounds, _ int) (sprint.ReadPlan, []sprint.AgendaKey) {
		return sprint.ReadPlan{IDs: map[string][]string{sprint.Work: {"none"}}}, nil
	}
	plan := func(_ *sprint.Snapshot, keys []sprint.AgendaKey, now sprint.Now) sprint.RulePlan {
		rp, left := r.Read(keys, sprint.L1ReadBounds(), 0)
		if len(left) != 0 {
			w.t.Errorf("%s left keys unread: %v", name, left)
		}
		ans := sprint.ReadAnswer{Epoch: "0", ActiveEpoch: "0", TimeMS: sprint.Decimal(strconv.FormatInt(now.Wall, 10))}
		for _, q := range rp.Sprint {
			ans.Sprint = append(ans.Sprint, answer(q))
		}
		snap, err := sprint.LoadPartial(rp, ans)
		if err != nil {
			w.t.Errorf("%s's answer does not load: %v", name, err)
			return sprint.RulePlan{}
		}
		return r.Plan(snap, keys, now)
	}
	return sprint.Rule{Name: r.Name, Priority: r.Priority, Read: read, Plan: plan}
}

// TestRealRuleRemindThroughATick (R14, 2.3; M3): remind:ann due now is popped
// by the lease step, dispatched to the rule table's remind, planned, cut by the
// loop's cut() with its time part on the first request, and applied in RT3: the
// entry is at R + 5 min and ann's goal record is claimed at R with the loop's
// generation. The time part rides only through cut(): no test helper attaches it.
func TestRealRuleRemindThroughATick(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	var planned sprint.Now
	remind := w.timeRule("remind", func(q sprint.SprintQ) sprint.Answer {
		g := sprint.GoalFact{Exists: true}
		if at, ok := w.zset("due@0")["remind:ann"]; ok {
			g.Entry, g.At = true, int64(at)
		}
		return sprint.Answer{Kind: q.Kind, Time: &sprint.TimeAnswer{Goals: map[string]sprint.GoalFact{"ann": g}}}
	})
	plan := remind.Plan
	remind.Plan = func(s *sprint.Snapshot, keys []sprint.AgendaKey, now sprint.Now) sprint.RulePlan {
		planned = now
		return plan(s, keys, now)
	}
	l := w.realLoop([]sprint.Rule{remind})
	k := &counting{c: w.tw}
	w.tick(l, k)
	at := w.clk.now().UnixMilli()
	w.step(&sprintfn.Request{Epoch: "0", Meta: sprintfn.Meta{Verb: "seed", Actor: "coordinator"},
		Sprint: &sprintfn.SprintPart{Time: &sprintfn.SprintTime{Due: []sprintfn.DueAt{{Key: "remind:ann", At: tset.Decimal(strconv.FormatInt(at, 10))}}}}})
	w.clk.add(time.Second)
	rep := w.tick(l, k)
	if st := rep.Rules["remind"]; st == nil || st.Applied != 1 || planned.R == 0 {
		t.Fatalf("remind through the tick: %+v, %+v", rep.Rules["remind"], rep)
	}
	if got := int64(w.zset("due@0")["remind:ann"]); got != planned.R+int64(sprint.RuleRemindEvery/time.Millisecond) {
		t.Fatalf("remind:ann is at %d; R was %d", got, planned.R)
	}
	if g := w.hash("goal:ann"); g["claimed_r"] != strconv.FormatInt(planned.R, 10) || g["claimed_gen"] != strconv.FormatUint(l.gen, 10) {
		t.Fatalf("ann's claim: %v (R %d, gen %d)", g, planned.R, l.gen)
	}
}

// TestRealRuleBehindThroughTicks (R18, 2.3; M1, L4): a backlog of B armed by
// the tick end fires on B-1 and arms again with the new backlog: its step
// clears behind_n, and the next tick end records B-1 and a fresh entry; the
// next fire on B-1 is judged, "the machine is falling behind". A page of one
// line a tick, and one line added a tick, hold the backlog where the test puts
// it; the tick answer is the store's behind_n, entry and judgment and the
// backlog the tick recorded.
func TestRealRuleBehindThroughTicks(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	var l *Loop
	behind := w.timeRule("behind", func(q sprint.SprintQ) sprint.Answer {
		n, _ := strconv.Atoi(w.hash("tick@0")["behind_n"])
		at, entry := w.zset("due@0")["behind"]
		backlog, _ := strconv.Atoi(l.hb["backlog"])
		_, judged := w.hash("jopen:sprint@0")[fallingBehind+"|behind"]
		return sprint.Answer{Kind: q.Kind, Time: &sprint.TimeAnswer{Tick: &sprint.TickFact{Backlog: backlog, BehindN: n,
			Entry: entry, EntryAt: int64(at), Judged: judged}}}
	})
	b := DefaultBudget()
	b.PageLines = 1
	var err error
	if l, err = NewLoop(Config{Names: testNames, Owner: "token-a", Name: "a", Rules: []sprint.Rule{behind}, Budget: b}); err != nil {
		t.Fatal(err)
	}
	k := &counting{c: w.tw}
	w.tick(l, k)
	n := 0
	line := func() { n++; w.verb(create("s1:waiting", waiting(), fmt.Sprintf("c%d", n))) }
	for range 5 {
		line()
	}
	armed := w.tick(l, k).Backlog
	if got := w.hash("tick@0")["behind_n"]; armed < 2 || got != strconv.FormatUint(armed, 10) {
		t.Fatalf("the tick end armed %q at a backlog of %d", got, armed)
	}
	w.clk.add(sprint.BehindSpan + time.Second)
	rep := w.tick(l, k) // the pop, a page of one line: the backlog is one smaller
	if rep.Backlog != armed-1 || rep.Rules["behind"] == nil || rep.Rules["behind"].Applied != 1 {
		t.Fatalf("the first fire, on %d: %+v", rep.Backlog, rep)
	}
	if v, set := w.hash("tick@0")["behind_n"]; set {
		t.Fatalf("behind_n after the re-arm: %q", v)
	}
	line()
	if rep = w.tick(l, k); rep.Backlog != armed-1 || w.hash("tick@0")["behind_n"] != strconv.FormatUint(armed-1, 10) {
		t.Fatalf("the tick end after the re-arm: backlog %d, behind_n %q", rep.Backlog, w.hash("tick@0")["behind_n"])
	}
	if _, there := w.zset("due@0")["behind"]; !there {
		t.Fatalf("the tick end armed no entry: %v", w.zset("due@0"))
	}
	line()
	w.clk.add(sprint.BehindSpan + time.Second)
	if rep = w.tick(l, k); rep.Backlog != armed-1 {
		t.Fatalf("the second fire: backlog %d", rep.Backlog)
	}
	if _, judged := w.hash("jopen:sprint@0")[fallingBehind+"|behind"]; !judged {
		t.Fatalf("a backlog that stalled at %d was not judged: %v %+v %+v", armed-1, w.hash("jopen:sprint@0"), rep, rep.Rules["behind"])
	}
}

// fallingBehind is R18's judgment (2.5), the field of jopen:sprint its cause
// "behind" follows.
const fallingBehind = "the machine is falling behind"

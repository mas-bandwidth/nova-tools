package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

func (h *harness) lines() []sprint.Line {
	h.t.Helper()
	ls, err := h.st.Log(h.ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	return ls
}

// linesOf is the rendered lines about a card, in order.
func (h *harness) linesOf(id string) []string {
	var out []string
	for _, l := range h.lines() {
		if l.About(id) {
			out = append(out, sprint.Render(l))
		}
	}
	return out
}

// The log: every change of every card is a line written with the step, the
// machine's own moves (a redeal when a member goes silent, a level move)
// among them; replaying it gives every card's place (rule 13, which check
// holds after every step here).
func TestTheLogHoldsEveryMoveAndReplaysToTheTables(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.live = []string{"m1", "m2"}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"p1"}, Brief: "do the thing"}))
	h.startMachine()
	h.machine() // p1 dealt to m1
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
	h.live = []string{"m2"} // m1 silent: its card is taken back and redealt
	for i := 0; i < 3; i++ {
		h.tick(10 * time.Second)
		h.machine()
	}
	h.clean("redealt")
	c := h.snap().Fleet.Card("p1.w1")
	if c == nil || c.Row != "m2" {
		t.Fatalf("redealt to m2: %+v", c)
	}
	h.run(TakeStep(sprint.TakeReq{As: "m2", Sel: sprint.Sel{IDs: []string{"p1.w1"}}, Gens: map[string]int{"p1.w1": c.Int("gen")}, Who: "m2"}))
	h.must(FinishStep(sprint.FinishReq{As: "m2", Sel: sprint.Sel{IDs: []string{"p1.w1"}}, Gens: map[string]int{"p1.w1": c.Int("gen")}, Failed: true, Report: "the tests went red", Who: "m2"}))
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"p1"}}, Answers: []string{h.openOf(sprint.NWorkFailed)[0].Note.ID}}))
	h.clean("reworked")
	got := strings.Join(h.linesOf("p1"), "\n")
	for _, want := range []string{
		"p1 added to s1 by tester",
		"attempt 1 dealt to m1",
		"attempt 1 redealt from m1 to m2 by the machine",
		"m2 took attempt 1",
		"m2 finished attempt 1: FAILED",
		"judgment: work came back failed",
		"p1 reworked by tester: attempt 2",
		"tester answered \"work came back failed\": rework",
		"attempt 2 dealt to",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the log of p1 has no %q:\n%s", want, got)
		}
	}
	var report, brief bool
	for _, l := range h.lines() {
		report = report || l.Text["report"] == "the tests went red"
		brief = brief || l.Text["brief"] == "do the thing"
	}
	if !report || !brief {
		t.Fatalf("the words given are not on their lines: report %v, brief %v", report, brief)
	}
	if v := sprint.LogViolations(h.snap(), h.lines()); len(v) != 0 {
		t.Fatalf("the log does not replay: %v", v)
	}
}

// Rule 13 holds the log to the tables: a card moved by a writer outside the
// steps, with no line, is a violation naming it.
func TestAMoveTheLogNeverSawIsRule13(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	s := h.snap()
	wc := s.Fleet.Card("s1-1.w1")
	if _, err := h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-fleet", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Fleet.Revision),
		OperationID: "outside-move", Members: []ntable.BatchMemberEntry{{ID: wc.ID, Expect: &ntable.MemberExpect{Revision: fmt.Sprint(wc.Rev)},
			Move: &ntable.MemberMoveOp{Row: wc.Row, Col: sprint.Withdrawn}}}}); err != nil {
		t.Fatal(err)
	}
	rep, _, err := h.st.Check(h.ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range rep.Violations {
		found = found || v.Rule == 13 && strings.Contains(v.Detail, "s1-1.w1")
	}
	if !found {
		t.Fatalf("an unlogged move: %v", rep.Violations)
	}
}

// A clear keeps the old epoch's log, readable at that epoch, and the new
// epoch's log starts empty.
func TestAClearKeepsTheOldEpochsLog(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	before := len(h.lines())
	if before == 0 {
		t.Fatalf("no lines before the clear")
	}
	if _, err := h.st.Clear(h.ctx); err != nil {
		t.Fatal(err)
	}
	st, err := h.st.Pinned(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	now, err := st.Log(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range now {
		if l.Kind == sprint.LineMove && !strings.HasSuffix(l.To, ":"+sprint.Ctl) {
			t.Fatalf("the new epoch's log has a move line: %+v", l)
		}
	}
	old, err := st.At(0).Log(h.ctx)
	if err != nil || len(old) != before {
		t.Fatalf("the old epoch's log: %d lines, want %d (%v)", len(old), before, err)
	}
}

// The flapping judgment: a lateness raised on an attempt stays raised
// until the attempt ends or the coordinator answers it. A card taken, its
// member silent past the not-finished deadline (withdrawn), brought back and
// taken three times more has one not-finished judgment, never closed by the
// redeal or the return to ready, updated in place with where the card is;
// each update is a line of the log.
func TestALatenessStaysRaisedUntilItsAttemptEnds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.live = []string{"m1"}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 1}))
	h.startMachine()
	h.machine()
	take := func() {
		c := h.snap().Fleet.Card("s1-1.w1")
		h.must(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Who: "m1"}))
	}
	take()
	h.live = nil // m1 silent two hours: withdrawn, late not finished
	for i := 0; i < 125; i++ {
		h.tick(time.Minute)
		h.machine()
	}
	late := func() []sprint.Open {
		var out []sprint.Open
		for _, o := range h.openOf(sprint.NWorkLate) {
			if strings.Contains(o.Note.What, "not finished") {
				out = append(out, o)
			}
		}
		return out
	}
	first := late()
	if len(first) != 1 {
		t.Fatalf("withdrawn two hours after its take: %d not-finished judgments", len(first))
	}
	for i := 0; i < 3; i++ {
		h.live = []string{"m1"}
		h.tick(time.Second)
		h.machine() // back: its presence is the fleet's update, after the pump
		h.tick(time.Second)
		h.machine() // the next pump redeals the card: ready
		if l := late(); len(l) != 1 || l[0].Note.ID != first[0].Note.ID {
			t.Fatalf("lap %d, redealt: the lateness %+v, want %s still open", i, l, first[0].Note.ID)
		}
		take()
		h.tick(time.Second)
		h.machine()
		if l := late(); len(l) != 1 || l[0].Note.ID != first[0].Note.ID {
			t.Fatalf("lap %d, taken again: the lateness %+v, want %s still open", i, l, first[0].Note.ID)
		}
		h.live = nil
		for j := 0; j < 3; j++ {
			h.tick(10 * time.Second)
			h.machine()
		}
	}
	written := 0
	updates := 0
	for _, l := range h.lines() {
		if l.Note != nil && l.Note.Type == sprint.NWorkLate && strings.Contains(l.Note.What, "not finished") {
			if l.Verb == "updated" {
				updates++
			} else {
				written++
			}
		}
	}
	if written != 1 || updates == 0 {
		t.Fatalf("the not-finished judgment written %d times, updated %d times", written, updates)
	}
	if w := late()[0].Note.What; !strings.Contains(w, "; at ") {
		t.Fatalf("the open lateness does not say where the card is: %q", w)
	}
}

// The take-and-abandon loop: a card taken and abandoned over and over is redealt at most
// MaxRedeals times in its attempt (the take does not reset the count, and each
// redeal's line says "redeal n of 3"); the next abandonment leaves it
// withdrawn, its primary ready and dealt no more, and one bound judgment
// names it until the coordinator reworks it with a fix (a new attempt, its
// count at zero) or drops it.
func TestTheRedealBoundEndsTheTakeAndAbandonLoop(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.live = []string{"m1", "m2"}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 1}))
	h.startMachine()
	h.machine()
	for lap := 0; lap < 10; lap++ {
		c := h.snap().Fleet.Card("s1-1.w1")
		if c.Col != sprint.Ready {
			break
		}
		holder := c.Row
		h.run(TakeStep(sprint.TakeReq{As: holder, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Who: holder}))
		other := map[string]string{"m1": "m2", "m2": "m1"}[holder]
		h.live = []string{other} // the taker abandons it
		for i := 0; i < 3; i++ {
			h.tick(10 * time.Second)
			h.machine()
		}
		h.live = []string{"m1", "m2"}
		h.tick(time.Second)
		h.machine()
	}
	c := h.snap().Fleet.Card("s1-1.w1")
	if c.Int("redeals") != sprint.MaxRedeals || c.Col != sprint.Withdrawn || c.Int("gen") > sprint.MaxRedeals+2 {
		t.Fatalf("after ten abandonments: %s at %s:%s gen %d redeals %d", c.ID, c.Row, c.Col, c.Int("gen"), c.Int("redeals"))
	}
	if st := h.state("s1-1"); st != sprint.Ready {
		t.Fatalf("s1-1 is %s at its bound", st)
	}
	bound := h.openOf(sprint.NBound)
	if len(bound) != 1 || bound[0].Note.Card != "s1-1.w1" || !strings.Contains(bound[0].Note.What, "redealt 3 times") {
		t.Fatalf("the bound judgment: %+v", bound)
	}
	var redeals []string
	for _, l := range h.lines() {
		if l.Card == "s1-1.w1" && strings.Contains(sprint.Render(l), "redeal ") {
			redeals = append(redeals, sprint.Render(l))
		}
	}
	if len(redeals) != 3 || !strings.Contains(redeals[2], "redeal 3 of 3") {
		t.Fatalf("the redeal lines: %q", redeals)
	}
	h.tick(time.Minute)
	h.machine()
	if c2 := h.snap().Fleet.Card("s1-1.w1"); c2.Int("gen") != c.Int("gen") || c2.Col != sprint.Withdrawn {
		t.Fatalf("dealt again past its bound: %s:%s gen %d", c2.Row, c2.Col, c2.Int("gen"))
	}
	if res := h.run(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Answers: []string{bound[0].Note.ID}})); len(res.Refused) == 0 {
		t.Fatalf("rework at the bound with no fix: %+v", res)
	}
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "run it on a quieter machine", Answers: []string{bound[0].Note.ID}}))
	w2 := h.snap().Fleet.Card("s1-1.w2")
	if w2 == nil || w2.Col != sprint.Ready || w2.Int("redeals") != 0 || h.state("s1-1") != sprint.Working || len(h.openOf(sprint.NBound)) != 0 {
		t.Fatalf("reworked at the bound: %+v, s1-1 %s, bound open %d", w2, h.state("s1-1"), len(h.openOf(sprint.NBound)))
	}
	h.clean("reworked at the bound")
}

// Rule 14: every notification is in both streams, the same. One written to
// the inbox alone, or to the log alone, is a violation naming it.
func TestTheLogAndTheInboxAgree(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.clean("agree")
	rule14 := func() []string {
		rep, _, err := h.st.Check(h.ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, v := range rep.Violations {
			if v.Rule == 14 {
				out = append(out, v.Detail)
			}
		}
		return out
	}
	n := sprint.Note{ID: "outside-1.1", Kind: sprint.Happened, Type: "an outside note", What: "only in the inbox", At: h.now}
	h.m.mu.Lock()
	h.m.seq++
	lg := h.m.log()
	lg.inbox = append(lg.inbox, memNote{fmt.Sprintf("%d-0", h.m.seq), n})
	h.m.mu.Unlock()
	if v := rule14(); len(v) != 1 || !strings.Contains(v[0], "outside-1.1") || !strings.Contains(v[0], "not in the log") {
		t.Fatalf("an inbox entry with no line: %v", v)
	}
	m := sprint.Note{ID: "outside-2.1", Kind: sprint.Happened, Type: "an outside note", What: "only in the log", At: h.now}
	h.m.appendLine(sprint.NoteLine(m, "outside-2"))
	if v := rule14(); len(v) != 2 || !strings.Contains(strings.Join(v, "\n"), "outside-2.1 (happened, an outside note): only in the log is in the log and not in the inbox") {
		t.Fatalf("a line with no inbox entry: %v", v)
	}
}

// Glenn's drive: a sentinel every 100 cards of a stream of 1,000 ready
// cards, each inserted in front of the cards after it. Each step's record
// stays bounded (a store that takes at most 4 MB in one write takes them
// all), a line's cause is its own card's and short, and the log replays.
func TestSentinelsInsertedIntoALongStreamAreBoundedRecords(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.m.MaxWrite = 4 << 20
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 1000}))
	for k := 1; k <= 9; k++ {
		gate := fmt.Sprintf("s1-gate-%d", k)
		res, err := h.st.Run(h.ctx, AddStep(sprint.AddReq{Stream: "s1", IDs: []string{gate}, Sentinel: true, After: fmt.Sprintf("s1-%d", k*100)}))
		if err != nil || len(res.Refused) > 0 {
			t.Fatalf("the sentinel after s1-%d: %v %+v", k*100, err, res.Refused)
		}
	}
	for _, l := range h.lines() {
		if len(l.Cause) > sprint.MaxCause {
			t.Fatalf("a line's cause of %d bytes: %.200s", len(l.Cause), l.Cause)
		}
	}
	h.clean("nine sentinels")
}

// A write the store does not take (its bulk length bound) leaves the fence
// empty: the step says nothing was changed and why, never changed=unknown.
func TestARecordTheStoreRefusesChangesNothingAndSaysSo(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(0)
	h.m.MaxWrite = 512
	res, err := h.st.Run(h.ctx, AddStep(sprint.AddReq{Stream: "s1", Count: 20}))
	if err == nil && len(res.Refused) == 0 {
		t.Fatalf("a record over the store's bound was taken: %+v", res)
	}
	if errors.Is(err, ErrUnknown) {
		t.Fatalf("changed=unknown for a write the store did not take: %v", err)
	}
	why := fmt.Sprint(err, res.Refused)
	if !strings.Contains(why, "nothing was changed") || !strings.Contains(why, "broken pipe") {
		t.Fatalf("the refusal does not say nothing changed and the store's reason: %s", why)
	}
	if n := len(h.snap().Work.Column(sprint.Ready)); n != 0 {
		t.Fatalf("%d cards added by a refused write", n)
	}
	h.clean("refused")
}

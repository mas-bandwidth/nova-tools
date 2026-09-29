package store

// A cold reader's sequences against the in-memory store, with the section 9
// check after every step.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

type probe struct {
	*harness
	seen int // notes already logged
}

func newProbe(t *testing.T) *probe { return &probe{harness: newHarness(t)} }

// inv runs check and reports every violation (non-fatal), returning them.
func (p *probe) inv(when string) []sprint.Violation {
	p.t.Helper()
	rep, _, err := p.st.Check(p.ctx, 3)
	if err != nil {
		p.t.Errorf("%s: check: %v", when, err)
		return nil
	}
	for _, v := range rep.Violations {
		p.t.Errorf("INVARIANT after %s: %v", when, v)
	}
	return rep.Violations
}

// newNotes is the notes written since the last call.
func (p *probe) newNotes() []sprint.Note {
	all, _, _ := p.m.NotesSince(p.ctx, "", 100000)
	out := all[p.seen:]
	p.seen = len(all)
	return out
}

func fmtNotes(ns []sprint.Note) string {
	var b []string
	for _, n := range ns {
		b = append(b, fmt.Sprintf("[%s %q stream=%s prim=%v marked=%v sl=%v ans=%s dec=%v]", n.Kind, n.Type, n.Stream, n.Primaries, n.Marked, n.StreamLevel, n.Answers, n.Decisions))
	}
	return strings.Join(b, " ")
}

// do runs a step, logs what moved/refused/notified, and checks the invariants.
func (p *probe) do(when string, step Step) Result {
	p.t.Helper()
	res, err := p.st.Run(p.ctx, step)
	if err != nil {
		p.t.Logf("%-28s ERR %v", when, err)
	} else {
		p.t.Logf("%-28s moved=%v refused=%v notes=%s", when, res.Moved, res.Refused, fmtNotes(p.newNotes()))
	}
	p.inv(when)
	return res
}

func (p *probe) card(table, id string) *sprint.Card {
	s, err := p.st.Load(p.ctx, All, func(*sprint.Snapshot) map[string][]string { return map[string][]string{table: {id}} })
	if err != nil {
		p.t.Fatal(err)
	}
	return s.T(table).Card(id)
}

func (p *probe) open() []sprint.Open { o, _ := p.m.OpenNotes(p.ctx); return o }

func (p *probe) openOn(subject string) []sprint.Open {
	var out []sprint.Open
	for _, o := range p.open() {
		if o.Subject() == subject {
			out = append(out, o)
		}
	}
	return out
}

func ids(xs ...string) sprint.Sel { return sprint.Sel{IDs: xs} }

// toReview starts, takes and finishes ok the named primaries.
func (p *probe) toReview(head string, xs ...string) {
	p.t.Helper()
	p.do("start "+strings.Join(xs, ","), StartStep(sprint.StartReq{Sel: ids(xs...)}))
	s := p.snap()
	for _, id := range xs {
		c := s.Fleet.Card(s.Work.Card(id).F("work"))
		g := map[string]int{c.ID: c.Int("gen")}
		p.do("take "+c.ID, TakeStep(sprint.TakeReq{As: c.Row, Sel: ids(c.ID), Gens: g}))
		p.do("finish "+c.ID, FinishStep(sprint.FinishReq{As: c.Row, Sel: ids(c.ID), Gens: g, Head: head}))
	}
}

func (p *probe) read(reader, card, verdict string) Result {
	return p.do("read "+card+" "+verdict, ReadStep(sprint.ReadReq{As: reader, Verdict: verdict, Sel: ids(card)}))
}

// A member goes down, its work card is redealt and taken by another, the
// first member's finish arrives, then the second's.
func TestARedealThenBothFinishes(t *testing.T) {
	t.Parallel()
	p := newProbe(t)
	p.setup(1)
	p.do("start", StartStep(sprint.StartReq{Sel: ids("s1-1")}))
	c := p.snap().Fleet.Card("s1-1.w1")
	first := c.Row
	p.do("take by first", TakeStep(sprint.TakeReq{As: first, Sel: ids("s1-1.w1"), Gens: map[string]int{"s1-1.w1": 1}}))
	p.do("fleet down first", FleetStep(sprint.FleetReq{Op: "down", Member: first}))
	c = p.snap().Fleet.Card("s1-1.w1")
	second := c.Row
	p.do("take by second", TakeStep(sprint.TakeReq{As: second, Sel: ids("s1-1.w1"), Gens: map[string]int{"s1-1.w1": 2}}))
	// the first member's finish, in each form a worker could send it
	for _, f := range []sprint.FinishReq{
		{As: first, Gens: map[string]int{"s1-1.w1": 1}},
		{Gens: map[string]int{"s1-1.w1": 1}},
		{As: first},
	} {
		f.Sel = ids("s1-1.w1")
		f.Head = "first-head"
		res := p.do("first's finish", FinishStep(f))
		if len(res.Moved) != 0 {
			t.Errorf("a stale finish moved: %+v", res)
		}
	}
	res := p.do("second's finish", FinishStep(sprint.FinishReq{As: second, Sel: ids("s1-1.w1"), Gens: map[string]int{"s1-1.w1": 2}, Head: "second-head"}))
	if len(res.Moved) != 1 || p.card(sprint.Work, "s1-1").F("head") != "second-head" {
		t.Errorf("the live finish: %+v head=%s", res, p.card(sprint.Work, "s1-1").F("head"))
	}
	if ctl := p.snap().MemberCtl(first); ctl.F("ok") != "0" {
		t.Errorf("the first member was credited: %v", ctl.Fields)
	}
}

// The generation is only checked when the worker names it: the stale
// worker on a member the card came back to finishes by --as alone.
func TestAFinishWithoutItsGenerationIsRefused(t *testing.T) {
	t.Parallel()
	p := newProbe(t)
	p.setup(1)
	p.do("start", StartStep(sprint.StartReq{Sel: ids("s1-1")}))
	first := p.snap().Fleet.Card("s1-1.w1").Row
	p.do("take by first (gen 1)", TakeStep(sprint.TakeReq{As: first, Sel: ids("s1-1.w1"), Gens: map[string]int{"s1-1.w1": 1}}))
	p.do("fleet down first", FleetStep(sprint.FleetReq{Op: "down", Member: first}))
	second := p.snap().Fleet.Card("s1-1.w1").Row
	p.do("fleet up first", FleetStep(sprint.FleetReq{Op: "up", Member: first}))
	p.do("fleet down second", FleetStep(sprint.FleetReq{Op: "down", Member: second}))
	c := p.snap().Fleet.Card("s1-1.w1")
	t.Logf("card now at %s:%s gen %s", c.Row, c.Col, c.F("gen"))
	p.do("take by first's new worker", TakeStep(sprint.TakeReq{As: first, Sel: ids("s1-1.w1"), Gens: map[string]int{"s1-1.w1": c.Int("gen")}}))
	// the old worker (holding generation 1) reports with --as only
	res := p.do("old worker's finish, --as only", FinishStep(sprint.FinishReq{As: first, Sel: ids("s1-1.w1"), Failed: true, Report: "stale"}))
	if len(res.Moved) != 0 {
		t.Errorf("a finish from generation 1 was accepted for a card at generation %s because the request named no generation: %v", c.F("gen"), res.Moved)
	}
	// and by selection (no id at all)
}

// A multi-table step cut after each of its writes in turn; every mutating
// verb tried before repair; repair; the same operation replayed.
func TestACutAfterEachWriteIsFinishedByRepair(t *testing.T) {
	t.Parallel()
	type multi struct {
		name   string
		prep   func(p *probe)
		step   func(p *probe) Step
		tables []string // the tables it writes, in order
	}
	cases := []multi{
		{"start", func(p *probe) {}, func(p *probe) Step { return StartStep(sprint.StartReq{Sel: ids("s1-1")}) }, []string{"t-fleet", "t-work"}},
		{"finish", func(p *probe) {
			p.do("start", StartStep(sprint.StartReq{Sel: ids("s1-1")}))
			c := p.snap().Fleet.Card("s1-1.w1")
			p.do("take", TakeStep(sprint.TakeReq{As: c.Row, Sel: ids(c.ID), Gens: map[string]int{c.ID: 1}}))
		}, func(p *probe) Step {
			return FinishStep(sprint.FinishReq{Sel: ids("s1-1.w1"), Gens: map[string]int{"s1-1.w1": 1}, Failed: true})
		}, []string{"t-fleet", "t-work"}},
		{"accept", func(p *probe) { p.through("s1-1"); p.do("return", ReturnStep(sprint.ReturnReq{Sel: ids("s1-1")})) },
			func(p *probe) Step { return AcceptStep(sprint.AcceptReq{Sel: ids("s1-1")}) }, []string{"t-readers", "t-merge", "t-work"}},
		{"rework", func(p *probe) {
			p.toReview("h1", "s1-1")
			p.do("ask", AskStep(sprint.AskReq{Sel: ids("s1-1")}))
		}, func(p *probe) Step { return ReworkStep(sprint.ReworkReq{Sel: ids("s1-1"), Fix: "f"}) }, []string{"t-fleet", "t-readers", "t-work"}},
		{"merge", func(p *probe) { p.through("s1-1") }, func(p *probe) Step { return MergeStep(sprint.MergeReq{Stream: "s1"}) }, []string{"t-merge", "t-work"}},
		{"drop", func(p *probe) { p.through("s1-1") }, func(p *probe) Step { return DropStep(sprint.DropReq{Sel: ids("s1-1"), Reason: "x"}) }, []string{"t-merge", "t-work"}},
		{"withdraw", func(p *probe) {
			p.do("start", StartStep(sprint.StartReq{Sel: ids("s1-1")}))
			p.do("down m2", FleetStep(sprint.FleetReq{Op: "down", Member: "m2"}))
		}, func(p *probe) Step { return FleetStep(sprint.FleetReq{Op: "down", Member: "m1"}) }, []string{"t-fleet", "t-work"}},
	}
	for _, c := range cases {
		for cut := 1; cut < len(c.tables); cut++ {
			t.Run(fmt.Sprintf("%s-cut-before-%s", c.name, c.tables[cut]), func(t *testing.T) {
				p := newProbe(t)
				p.setup(2)
				c.prep(p)
				p.newNotes()
				next := c.tables[cut]
				p.m.Fail = func(pt string) error {
					if pt == "apply "+next+" before" {
						return errors.New("cut")
					}
					return nil
				}
				step := c.step(p)
				step.CallerOp = "caller-" + c.name
				_, err := p.st.Run(p.ctx, step)
				if err == nil || p.m.Pending() == nil {
					t.Fatalf("not cut: %v pending=%v", err, p.m.Pending())
				}
				t.Logf("cut: %v", err)
				p.inv("cut (pending)")
				// every mutating verb, the store still unable to write the table
				before := snapKey(p)
				for name, st := range everyVerb() {
					_, err := p.st.Run(p.ctx, st)
					if err == nil {
						t.Errorf("%s ran over a pending operation", name)
					}
				}
				p.tick(2 * time.Minute)
				for name, st := range everyVerb() {
					_, err := p.st.Run(p.ctx, st)
					var pe *PendingError
					if err == nil || !errors.As(err, &pe) {
						t.Errorf("past the grace, %s over a pending operation that cannot finish: %v", name, err)
					}
				}
				if after := snapKey(p); after != before {
					t.Errorf("a verb changed the state while an operation was pending:\n%s\n%s", before, after)
				}
				p.m.Fail = nil
				rr, err := p.st.Repair(p.ctx)
				t.Logf("repair: %+v %v", rr, err)
				p.inv("repaired")
				notes := p.newNotes()
				t.Logf("notes at repair: %s", fmtNotes(notes))
				again, err := p.st.Run(p.ctx, step)
				if err != nil || !again.Replay {
					t.Errorf("the replay of the caller's op: %+v %v", again, err)
				}
				if n := p.newNotes(); len(n) != 0 {
					t.Errorf("the replay wrote notes: %s", fmtNotes(n))
				}
				p.inv("replayed")
			})
		}
	}
}

func snapKey(p *probe) string {
	s := p.snap()
	var b []string
	for _, t := range []*sprint.Table{s.Work, s.Readers, s.Merge, s.Fleet} {
		for _, c := range t.Column(append(append([]string{}, sprint.States...), sprint.Asked, sprint.Reading, sprint.OK, sprint.Broken, sprint.Queued, sprint.Merged, sprint.Stuck, sprint.Returned, sprint.Done, sprint.Ctl)...) {
			b = append(b, fmt.Sprintf("%s/%s:%s:%d", t.Name, c.ID, c.Col, c.Rev))
		}
	}
	return strings.Join(b, " ")
}

func everyVerb() map[string]Step {
	return map[string]Step{
		"add":      AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"z1"}}),
		"resolve":  ResolveStep(sprint.ResolveReq{}),
		"start":    StartStep(sprint.StartReq{Sel: sprint.Sel{Limit: 5}}),
		"take":     TakeStep(sprint.TakeReq{As: "m1"}),
		"finish":   FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{Limit: 5}}),
		"ask":      AskStep(sprint.AskReq{}),
		"read":     ReadStep(sprint.ReadReq{As: "reader-a", Verdict: "ok"}),
		"accept":   AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{Stream: "s1"}}),
		"rework":   ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{Stream: "s1"}, Fix: "x"}),
		"return":   ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{Stream: "s1"}}),
		"drop":     DropStep(sprint.DropReq{Sel: ids("s1-2"), Reason: "x"}),
		"rank":     RankStep(sprint.RankReq{IDs: []string{"s1-2"}, First: true}),
		"merge":    MergeStep(sprint.MergeReq{Stream: "s1"}),
		"resume":   ResumeStep(sprint.ResumeReq{Stream: "s1"}),
		"fleet up": FleetStep(sprint.FleetReq{Op: "up", Member: "m9"}),
		"level":    FleetStep(sprint.FleetReq{Op: "level"}),
		"ci":       CIStep(sprint.CIReq{Sel: ids("s1-2"), Red: true, Run: "r"}),
	}
}

// accept over named ids, then over a selection with the same three.
func TestAcceptNamedThenSelection(t *testing.T) {
	t.Parallel()
	p := newProbe(t)
	p.setup(3)
	// s1-1: eligible (a, b ok at head)
	p.toReview("h", "s1-1")
	p.do("ask 1", AskStep(sprint.AskReq{Sel: ids("s1-1")}))
	for _, rc := range p.snap().Readers.Of("s1-1") {
		p.read(rc.F("reader"), rc.ID, "ok")
	}
	// s1-2: the same reader twice: a ok at attempt 1, b broken; rework; a ok at attempt 2, b not yet
	p.toReview("h1", "s1-2")
	p.do("ask 2", AskStep(sprint.AskReq{Sel: ids("s1-2")}))
	rs := p.snap().Readers.Of("s1-2")
	p.read(rs[0].F("reader"), rs[0].ID, "ok")
	p.read(rs[1].F("reader"), rs[1].ID, "broken")
	p.do("rework 2", ReworkStep(sprint.ReworkReq{Sel: ids("s1-2"), Fix: "f"}))
	c := p.snap().Fleet.Card("s1-2.w2")
	p.do("take 2.w2", TakeStep(sprint.TakeReq{As: c.Row, Sel: ids(c.ID), Gens: map[string]int{c.ID: 1}}))
	p.do("finish 2.w2", FinishStep(sprint.FinishReq{As: c.Row, Sel: ids(c.ID), Gens: map[string]int{c.ID: 1}, Head: "h2"}))
	p.read(rs[0].F("reader"), sprint.ReadCardID("s1-2", 2, rs[0].F("reader")), "ok")
	// s1-3: one reader ok
	p.toReview("h", "s1-3")
	p.do("ask 3", AskStep(sprint.AskReq{Sel: ids("s1-3")}))
	r3 := p.snap().Readers.Of("s1-3")
	p.read(r3[0].F("reader"), r3[0].ID, "ok")
	res := p.do("accept named 1,2,3", AcceptStep(sprint.AcceptReq{Sel: ids("s1-1", "s1-2", "s1-3")}))
	if len(res.Moved) != 0 || len(res.Refused) != 3 {
		t.Errorf("named all-or-nothing: %+v", res)
	}
	res = p.do("accept stream", AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{Stream: "s1"}}))
	if len(res.Moved) != 1 || p.state("s1-1") != sprint.Merging || p.state("s1-2") != sprint.Review || p.state("s1-3") != sprint.Review {
		t.Errorf("selection: %+v", res)
	}
	// Named-twice in a named set.
	res = p.do("accept named dup", AcceptStep(sprint.AcceptReq{Sel: ids("s1-2", "s1-2")}))
	t.Logf("dup: %+v", res)
}

// A third reader asked with --another (no broken read needed) is still
// asked or reading when the other two say ok: accept moves the primary to
// merging with a read card outstanding (rule 3).
func TestAcceptRetiresAReadOutstanding(t *testing.T) {
	t.Parallel()
	p := newProbe(t)
	p.setup(1)
	p.toReview("h", "s1-1")
	p.do("ask", AskStep(sprint.AskReq{Sel: ids("s1-1")}))
	p.do("ask another", AskStep(sprint.AskReq{Sel: ids("s1-1"), Another: true}))
	rs := p.snap().Readers.Of("s1-1")
	t.Logf("read cards: %d", len(rs))
	p.read(rs[0].F("reader"), rs[0].ID, "ok")
	p.read(rs[1].F("reader"), rs[1].ID, "ok")
	p.do("accept", AcceptStep(sprint.AcceptReq{Sel: ids("s1-1")}))
	if v := p.inv("accepted with a third read outstanding"); len(v) > 0 {
		t.Errorf("a legal sequence breaks rule 3")
	}
	p.do("merge", MergeStep(sprint.MergeReq{Stream: "s1"}))
	p.read(rs[2].F("reader"), rs[2].ID, "broken")
}

// The same with a broken read first (the model's own AskAnother guard):
// four readers.
func TestAcceptAfterAskingAnotherTwice(t *testing.T) {
	t.Parallel()
	p := newProbe(t)
	if err := p.m.RowsAdd(p.ctx, "t-readers", []string{"reader-d"}); err != nil {
		t.Fatal(err)
	}
	p.setup(1)
	p.toReview("h", "s1-1")
	p.do("ask", AskStep(sprint.AskReq{Sel: ids("s1-1")}))
	rs := p.snap().Readers.Of("s1-1")
	p.read(rs[0].F("reader"), rs[0].ID, "broken")
	p.do("ask another", AskStep(sprint.AskReq{Sel: ids("s1-1"), Another: true}))
	p.do("ask another", AskStep(sprint.AskReq{Sel: ids("s1-1"), Another: true}))
	rs = p.snap().Readers.Of("s1-1")
	var oks int
	for _, rc := range rs {
		if rc.Col == sprint.Asked && oks < 2 {
			p.read(rc.F("reader"), rc.ID, "ok")
			oks++
		}
	}
	p.do("accept", AcceptStep(sprint.AcceptReq{Sel: ids("s1-1")}))
	p.inv("accepted")
}

// One ok and one broken; rework; a late report on the retired card; the
// fix returns; who is asked at which head; two reworks in a row.
func TestReworkTwice(t *testing.T) {
	t.Parallel()
	p := newProbe(t)
	p.setup(1)
	p.toReview("h1", "s1-1")
	p.do("ask", AskStep(sprint.AskReq{Sel: ids("s1-1")}))
	rs := p.snap().Readers.Of("s1-1")
	a, b := rs[0].F("reader"), rs[1].F("reader")
	p.read(a, rs[0].ID, "ok")
	p.do("b begins", ReadStep(sprint.ReadReq{As: b, Begin: true, Sel: ids(rs[1].ID)}))
	p.read(b, rs[1].ID, "broken")
	nid := p.openOn("s1-1")[0].Note.ID
	p.do("rework 1", ReworkStep(sprint.ReworkReq{Sel: ids("s1-1"), Fix: "fix1", Answers: []string{nid}}))
	if len(p.openOn("s1-1")) != 0 {
		t.Errorf("rework left the broken judgment open")
	}
	late := p.read(a, rs[0].ID, "ok")
	if len(late.Moved) != 0 || len(late.Refused) != 1 || !strings.Contains(late.Refused[0].Why, "retired") {
		t.Errorf("a late report on a retired card: %+v", late)
	}
	c := p.snap().Fleet.Card("s1-1.w2")
	p.do("take w2", TakeStep(sprint.TakeReq{As: c.Row, Sel: ids(c.ID), Gens: map[string]int{c.ID: 1}}))
	p.do("finish w2 ok h2", FinishStep(sprint.FinishReq{As: c.Row, Sel: ids(c.ID), Gens: map[string]int{c.ID: 1}, Head: "h2"}))
	again := p.snap().Readers.Of("s1-1")
	for _, rc := range again {
		t.Logf("asked again: %s reader=%s head=%s col=%s", rc.ID, rc.F("reader"), rc.F("head"), rc.Col)
	}
	if len(again) != 2 || again[0].F("head") != "h2" {
		t.Errorf("not asked of the same two at h2")
	}
	p.read(b, sprint.ReadCardID("s1-1", 2, b), "broken")
	for _, o := range p.openOn("s1-1") {
		t.Logf("second broken: marked=%v before=%d decisions=%v", o.Note.Marked, o.Note.Before, o.Note.Decisions)
		if !o.Note.Marked {
			t.Errorf("the second broken read for the same cause is not marked")
		}
	}
	p.do("rework 2", ReworkStep(sprint.ReworkReq{Sel: ids("s1-1"), Fix: "fix2"}))
	pr := p.card(sprint.Work, "s1-1")
	t.Logf("after two reworks: attempt=%s reworks=%s broken_reads=%s asked=%s state=%s", pr.F("attempt"), pr.F("reworks"), pr.F("broken_reads"), pr.F("asked"), pr.Col)
	// fail twice: the second failure is marked
	c = p.snap().Fleet.Card("s1-1.w3")
	p.do("take w3", TakeStep(sprint.TakeReq{As: c.Row, Sel: ids(c.ID), Gens: map[string]int{c.ID: 1}}))
	p.do("finish w3 failed", FinishStep(sprint.FinishReq{As: c.Row, Sel: ids(c.ID), Gens: map[string]int{c.ID: 1}, Failed: true}))
	p.do("rework 3", ReworkStep(sprint.ReworkReq{Sel: ids("s1-1"), Fix: "fix3"}))
	c = p.snap().Fleet.Card("s1-1.w4")
	p.do("take w4", TakeStep(sprint.TakeReq{As: c.Row, Sel: ids(c.ID), Gens: map[string]int{c.ID: 1}}))
	p.do("finish w4 failed", FinishStep(sprint.FinishReq{As: c.Row, Sel: ids(c.ID), Gens: map[string]int{c.ID: 1}, Failed: true}))
	for _, o := range p.openOn("s1-1") {
		if o.Note.Type == sprint.NWorkFailed && !o.Note.Marked {
			t.Errorf("second failure not marked")
		}
	}
	// After a failure, the fixed work that comes back ok: who is asked?
	p.do("rework 4", ReworkStep(sprint.ReworkReq{Sel: ids("s1-1"), Fix: "fix4"}))
	c = p.snap().Fleet.Card("s1-1.w5")
	p.do("take w5", TakeStep(sprint.TakeReq{As: c.Row, Sel: ids(c.ID), Gens: map[string]int{c.ID: 1}}))
	p.do("finish w5 ok", FinishStep(sprint.FinishReq{As: c.Row, Sel: ids(c.ID), Gens: map[string]int{c.ID: 1}, Head: "h5"}))
	for _, rc := range p.snap().Readers.Of("s1-1") {
		t.Logf("at attempt 5: %s head=%s", rc.ID, rc.F("head"))
	}
}

// Merge in batches; a conflict mid-batch; resume; a cross-stream need on
// a stopped stream; a reworked card re-entering keeps its place.
func TestMergeOrderConflictAndCrossNeed(t *testing.T) {
	t.Parallel()
	p := newProbe(t)
	p.setup(5)
	p.through("s1-1", "s1-2", "s1-3", "s1-4", "s1-5")
	p.do("merge batch 2", MergeStep(sprint.MergeReq{Stream: "s1", Batch: 2}))
	if p.state("s1-1") != sprint.Landed || p.state("s1-2") != sprint.Landed || p.state("s1-3") != sprint.Merging {
		t.Errorf("batch not in score order")
	}
	// conflict on the second card of the next batch (s1-4 of s1-3,s1-4)
	p.do("merge conflict s1-4", MergeStep(sprint.MergeReq{Stream: "s1", Batch: 2, Conflict: "s1-4"}))
	t.Logf("open on stream: %d", len(p.openOn("stream:s1")))
	p.do("merge while stopped", MergeStep(sprint.MergeReq{Stream: "s1"}))
	// return s1-3 (it re-enters later) while stopped
	p.do("return s1-3", ReturnStep(sprint.ReturnReq{Sel: ids("s1-3")}))
	p.do("rework s1-3", ReworkStep(sprint.ReworkReq{Sel: ids("s1-3"), Fix: "f"}))
	p.do("resume (conflict)", ResumeStep(sprint.ResumeReq{Stream: "s1", Did: "rebased"}))
	if len(p.openOn("stream:s1")) != 0 {
		t.Errorf("resume left the stream judgment open")
	}
	// s1-3 comes back through review and is accepted again: its place
	c := p.snap().Fleet.Card("s1-3.w2")
	p.do("take", TakeStep(sprint.TakeReq{As: c.Row, Sel: ids(c.ID), Gens: map[string]int{c.ID: 1}}))
	p.do("finish", FinishStep(sprint.FinishReq{As: c.Row, Sel: ids(c.ID), Gens: map[string]int{c.ID: 1}, Head: "h2"}))
	for _, rc := range p.snap().Readers.Of("s1-3") {
		if rc.Col == sprint.Asked {
			p.read(rc.F("reader"), rc.ID, "ok")
		}
	}
	p.do("accept s1-3 again", AcceptStep(sprint.AcceptReq{Sel: ids("s1-3")}))
	q := p.snap().Merge.Cell("s1", sprint.Queued)
	var order []string
	for _, c := range q {
		order = append(order, c.ID)
	}
	t.Logf("queued order: %v", order)
	if len(order) == 0 || order[0] != "s1-3" {
		t.Errorf("the reworked card did not keep its place: %v", order)
	}
	// cross-stream: s2's b1 stuck on conflict (stopped); s1-4 needs b1
	p.do("add s2", AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"b1", "b2"}}))
	p.through("b1", "b2")
	p.do("s2 conflict b1", MergeStep(sprint.MergeReq{Stream: "s2", Conflict: "b1"}))
	p.do("s1 cross s1-3=b1", MergeStep(sprint.MergeReq{Stream: "s1", Cross: "s1-3=b1"}))
	p.do("resume s1 (b1 unlanded)", ResumeStep(sprint.ResumeReq{Stream: "s1"}))
	p.do("resume s2", ResumeStep(sprint.ResumeReq{Stream: "s2", Did: "fixed"}))
	p.do("merge s2", MergeStep(sprint.MergeReq{Stream: "s2", Batch: 1}))
	p.do("resume s1 (b1 landed)", ResumeStep(sprint.ResumeReq{Stream: "s1"}))
	p.do("merge s1", MergeStep(sprint.MergeReq{Stream: "s1"}))
	p.do("merge s1 again", MergeStep(sprint.MergeReq{Stream: "s1"}))
	p.do("merge s2 again", MergeStep(sprint.MergeReq{Stream: "s2"}))
	for _, id := range []string{"s1-3", "s1-4", "s1-5", "b1", "b2"} {
		t.Logf("%s: %s", id, p.state(id))
	}
}

// Cross facts the step does not check: a need naming a card of the same
// stream, the card itself, or no card at all.
func TestACrossFactIsChecked(t *testing.T) {
	t.Parallel()
	for _, other := range []string{"s1-2", "s1-1", "nosuch"} {
		t.Run(other, func(t *testing.T) {
			t.Parallel()
			p := newProbe(t)
			p.setup(2)
			p.through("s1-1", "s1-2")
			res := p.do("cross s1-1="+other, MergeStep(sprint.MergeReq{Stream: "s1", Cross: "s1-1=" + other}))
			if len(res.Refused) == 0 {
				t.Errorf("NOTE: the cross fact s1-1=%s was accepted: %v", other, res.Moved)
			}
			res = p.do("resume", ResumeStep(sprint.ResumeReq{Stream: "s1"}))
			t.Logf("resume: %+v", res.Refused)
		})
	}
}

// Red: resume without taking the suspect off.
func TestResumeAtOnceAfterRedIsRefused(t *testing.T) {
	t.Parallel()
	p := newProbe(t)
	p.setup(2)
	p.through("s1-1", "s1-2")
	p.do("merge red", MergeStep(sprint.MergeReq{Stream: "s1", Red: true}))
	res := p.do("resume at once", ResumeStep(sprint.ResumeReq{Stream: "s1"}))
	if len(res.Moved) != 0 {
		t.Errorf("resume after red without --did moved: %v", res.Moved)
	}
}

// No member up: withdrawal; members return; order preserved.
func TestWithdrawAndReturn(t *testing.T) {
	t.Parallel()
	p := newProbe(t)
	p.setup(4)
	p.do("start all", StartStep(sprint.StartReq{Sel: sprint.Sel{Limit: 4}}))
	c := p.snap().Fleet.Card("s1-1.w1")
	p.do("take s1-1", TakeStep(sprint.TakeReq{As: c.Row, Sel: ids(c.ID), Gens: map[string]int{c.ID: 1}}))
	p.do("down m1", FleetStep(sprint.FleetReq{Op: "down", Member: "m1"}))
	p.do("down m2", FleetStep(sprint.FleetReq{Op: "down", Member: "m2"}))
	for _, id := range []string{"s1-1", "s1-2", "s1-3", "s1-4"} {
		if p.state(id) != sprint.Ready {
			t.Errorf("%s is %s after withdrawal", id, p.state(id))
		}
	}
	res := p.do("old worker finish", FinishStep(sprint.FinishReq{As: c.Row, Sel: ids(c.ID), Gens: map[string]int{c.ID: 2}}))
	t.Logf("finish on a withdrawn card: %+v", res.Refused)
	p.do("up m2", FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
	p.do("up m1", FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	p.do("start all again", StartStep(sprint.StartReq{Sel: sprint.Sel{Limit: 4}}))
	s := p.snap()
	for _, m := range []string{"m1", "m2"} {
		var o []string
		for _, c := range s.Fleet.Cell(m, sprint.Ready) {
			o = append(o, fmt.Sprintf("%s(%v)", c.ID, c.Score))
		}
		t.Logf("%s ready: %v", m, o)
	}
	for _, m := range []string{"m1", "m2"} {
		res := p.do("take head "+m, TakeStep(sprint.TakeReq{As: m}))
		t.Logf("%s takes %v", m, res.Moved)
	}
}

// Notifications: a group of three failed, one answered; the cursor past
// an open judgment; a large group (more than MaxListed) of failures.
func TestAnsweringOneCardOfAGroup(t *testing.T) {
	t.Parallel()
	p := newProbe(t)
	p.setup(3)
	p.do("start 3", StartStep(sprint.StartReq{Sel: sprint.Sel{Limit: 3}}))
	s := p.snap()
	var cs []string
	for _, c := range s.Fleet.Column(sprint.Ready) {
		p.do("take "+c.ID, TakeStep(sprint.TakeReq{As: c.Row, Sel: ids(c.ID), Gens: map[string]int{c.ID: 1}}))
		cs = append(cs, c.ID)
	}
	gens := map[string]int{}
	for _, id := range cs {
		gens[id] = 1
	}
	p.do("finish 3 failed", FinishStep(sprint.FinishReq{Sel: ids(cs...), Gens: gens, Failed: true, Report: "same"}))
	open := p.open()
	t.Logf("open: %d", len(open))
	nid := open[0].Note.ID
	p.do("rework one, answers", ReworkStep(sprint.ReworkReq{Sel: ids("s1-2"), Fix: "f", Answers: []string{nid}}))
	if len(p.open()) != 2 {
		t.Errorf("answering one card of a group closed %d", 3-len(p.open()))
	}
	v, _ := p.st.Inbox(p.ctx, time.Hour, time.Hour, 1000)
	_ = p.m.SetCursor(p.ctx, v.Last)
	v, _ = p.st.Inbox(p.ctx, time.Hour, time.Hour, 1000)
	for _, g := range v.Groups {
		t.Logf("inbox after cursor: %s %q count=%d prim=%v", g.Kind, g.Type, g.Count, g.Primaries)
	}
	if len(v.Groups) == 0 || v.Groups[0].Count != 2 {
		t.Errorf("open judgments hidden by the cursor: %+v", v.Groups)
	}
}

func TestALargeFailedGroupKeepsEverySubject(t *testing.T) {
	t.Parallel()
	p := newProbe(t)
	p.setup(60)
	p.do("start 60", StartStep(sprint.StartReq{Sel: sprint.Sel{Limit: 60}}))
	gens := map[string]int{}
	var cards []string
	for _, m := range []string{"m1", "m2"} {
		p.do("take 30 "+m, TakeStep(sprint.TakeReq{As: m, Sel: sprint.Sel{Limit: 30}}))
	}
	for _, c := range p.snap().Fleet.Column(sprint.Working) {
		gens[c.ID] = c.Int("gen")
		cards = append(cards, c.ID)
	}
	p.do("finish 60 failed", FinishStep(sprint.FinishReq{Sel: ids(cards...), Gens: gens, Failed: true, Report: "same"}))
	if n := len(p.open()); n != 60 {
		t.Errorf("60 failures, %d open judgments", n)
	}
}

// Concurrency at the store level.
func TestInterleavedWriters(t *testing.T) {
	t.Parallel()
	// (a) another writer reworks the primary between A's read and A's acquire
	t.Run("rework-under-accept", func(t *testing.T) {
		t.Parallel()
		p := newProbe(t)
		p.setup(1)
		p.through("s1-1")
		p.do("return", ReturnStep(sprint.ReturnReq{Sel: ids("s1-1")}))
		other := &Store{B: p.m, Names: p.st.Names, Actor: "other", Now: p.st.Now, NewID: func() string { return "o" }}
		r := &racer{Backend: p.m, at: "acquire", do: func() {
			if _, err := other.Run(p.ctx, ReworkStep(sprint.ReworkReq{Sel: ids("s1-1"), Fix: "x"})); err != nil {
				t.Error(err)
			}
		}}
		st := *p.st
		st.B = r
		res, err := st.Run(p.ctx, AcceptStep(sprint.AcceptReq{Sel: ids("s1-1")}))
		t.Logf("accept under rework: %+v %v", res, err)
		if err != nil || len(res.Refused) != 1 || res.Refused[0].Key != "s1-1" {
			t.Errorf("the refusal does not name the card: %+v %v", res, err)
		}
		p.inv("raced")
	})
	// (b) another writer runs its whole step inside A's first apply: it finds
	// A's operation in the fence and finishes it; A's own sends replay.
	t.Run("finish-inside-apply", func(t *testing.T) {
		t.Parallel()
		p := newProbe(t)
		p.setup(2)
		p.do("start 1", StartStep(sprint.StartReq{Sel: ids("s1-1")}))
		c := p.snap().Fleet.Card("s1-1.w1")
		p.do("take", TakeStep(sprint.TakeReq{As: c.Row, Sel: ids(c.ID), Gens: map[string]int{c.ID: 1}}))
		p.newNotes()
		other := &Store{B: p.m, Names: p.st.Names, Actor: "other", Now: p.st.Now, NewID: func() string { return "o" }}
		var oerr error
		r := &racer{Backend: p.m, at: "apply t-fleet", do: func() {
			_, oerr = other.Run(p.ctx, StartStep(sprint.StartReq{Sel: ids("s1-2")}))
		}}
		st := *p.st
		st.B = r
		res, err := st.Run(p.ctx, FinishStep(sprint.FinishReq{Sel: ids(c.ID), Gens: map[string]int{c.ID: 1}, Failed: true}))
		t.Logf("A: %+v %v; B: %v", res, err, oerr)
		ns := p.newNotes()
		t.Logf("notes: %s", fmtNotes(ns))
		fails := 0
		for _, n := range ns {
			if n.Type == sprint.NWorkFailed {
				fails++
			}
		}
		if fails != 1 {
			t.Errorf("the failed judgment was written %d times", fails)
		}
		if p.state("s1-1") != sprint.Review || p.state("s1-2") != sprint.Working {
			t.Errorf("states %s %s", p.state("s1-1"), p.state("s1-2"))
		}
		p.inv("interleaved")
	})
	// (c) an outside write changes a member the later manifest expects: the
	// operation is cut and cannot finish; every verb refuses from then on.
	t.Run("expectation-no-longer-true", func(t *testing.T) {
		t.Parallel()
		p := newProbe(t)
		p.setup(1)
		r := &racer{Backend: p.m, at: "apply t-work", do: func() {
			s := p.snap()
			pr := s.Work.Card("s1-1")
			_, oerr := p.m.Apply(p.ctx, ntable.BatchManifest{Schema: 1, Table: "t-work", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Work.Revision),
				OperationID: "outside", Members: []ntable.BatchMemberEntry{{ID: pr.ID, Expect: &ntable.MemberExpect{Place: &ntable.PlaceExpect{Row: pr.Row, Col: pr.Col}}, Set: map[string]string{"brief": "x"}}}})
			if oerr != nil {
				t.Error(oerr)
			}
		}}
		st := *p.st
		st.B = r
		_, err := st.Run(p.ctx, StartStep(sprint.StartReq{Sel: ids("s1-1")}))
		t.Logf("cut: %v", err)
		p.inv("cut by an outside write")
		p.tick(time.Hour)
		_, err = p.st.Run(p.ctx, CIStep(sprint.CIReq{Sel: ids("s1-1"), Run: "r"}))
		t.Logf("NOTE: after the cut every verb: %v", err)
		rr, _ := p.st.Repair(p.ctx)
		t.Logf("repair: %+v", rr)
	})
}

var _ = context.Background

// ci red on a merging primary lists "return" as a decision; return acts
// on the card; the judgment stays open and --answers naming it is refused.
func TestReturnAnswersCIRed(t *testing.T) {
	t.Parallel()
	p := newProbe(t)
	p.setup(1)
	p.through("s1-1")
	p.do("ci red", CIStep(sprint.CIReq{Sel: ids("s1-1"), Red: true, Run: "r1"}))
	o := p.openOn("s1-1")
	if len(o) != 1 {
		t.Fatalf("open: %v", o)
	}
	t.Logf("decisions: %v", o[0].Note.Decisions)
	res := p.do("return --answers", ReturnStep(sprint.ReturnReq{Sel: ids("s1-1"), Answers: []string{o[0].Note.ID}}))
	// ci red is answered; what is open is the returned primary's own judgment.
	if left := p.openOn("s1-1"); len(left) != 1 || left[0].Note.Type != sprint.NReturned {
		t.Errorf("return, a listed decision of %q, acted on the card and left open %v; refused: %v", sprint.NCIRed, left, res.Refused)
	}
}

// The model's "readsexhausted": both readers broken, ask another answers
// both judgments, the third says ok: one ok, nothing outstanding, no open
// judgment on the primary.
func TestReadsExhaustedIsAJudgment(t *testing.T) {
	t.Parallel()
	p := newProbe(t)
	p.setup(1)
	p.toReview("h", "s1-1")
	p.do("ask", AskStep(sprint.AskReq{Sel: ids("s1-1")}))
	rs := p.snap().Readers.Of("s1-1")
	p.read(rs[0].F("reader"), rs[0].ID, "broken")
	p.read(rs[1].F("reader"), rs[1].ID, "broken")
	p.do("ask another", AskStep(sprint.AskReq{Sel: ids("s1-1"), Another: true}))
	for _, rc := range p.snap().Readers.Of("s1-1") {
		if rc.Col == sprint.Asked {
			p.read(rc.F("reader"), rc.ID, "ok")
		}
	}
	o := p.openOn("s1-1")
	if len(o) != 1 || o[0].Note.Type != sprint.NReadsExhausted {
		t.Errorf("s1-1 sits in review with one ok, nothing outstanding, and open judgments %v", o)
	}
}

package sprint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math/rand/v2"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// rvNow is the clocks the review rules plan at in these tests: R is not the
// wall time, so a rule that stamped one with the other shows.
func rvNow() Now { return Now{R: 5_000_000, Wall: t0.UnixMilli(), Running: true} }

// rvKeys is agenda keys, in the order given.
func rvKeys(keys ...string) []AgendaKey {
	out := make([]AgendaKey, len(keys))
	for i, k := range keys {
		out[i] = AgendaKey{Key: k, Seq: uint64(i + 1)}
	}
	return out
}

// rvEmpty says a rule plan changes nothing and asks for nothing: what a rule
// run a second time on the same keys, with nothing changed, must make. The
// keys it finishes are not a change.
func rvEmpty(rp RulePlan) bool {
	return len(rp.Plan.Units) == 0 && len(rp.Plan.Refused) == 0 && len(rp.Plan.Notes) == 0 && len(rp.Plan.Closes) == 0 &&
		len(rp.Intents) == 0 && len(rp.Guards) == 0 && len(rp.Notes) == 0 && len(rp.Requeue) == 0 &&
		len(rp.Quarantine) == 0 && len(rp.HeldBack) == 0
}

// rvApply applies a rule plan to the world as the store and J would: its
// entries, and its requests to open and close a judgment. A request to know
// writes no judgment.
func (w *world) rvApply(rp RulePlan) {
	w.t.Helper()
	w.do(rp.Plan)
	for _, nr := range rp.Notes {
		switch nr.Op {
		case reviewOpen:
			w.seq++
			n := Note{ID: fmt.Sprintf("rv%d", w.seq), Kind: Judgment, Type: nr.Type, Primaries: nr.Subjects, Count: len(nr.Subjects), What: nr.Text}
			for _, sub := range nr.Subjects {
				w.s.Open = append(w.s.Open, Open{Key: OpenKey(n.ID, sub), Note: n})
			}
		case reviewClose:
			var keep []Open
			for _, o := range w.s.Open {
				if o.Note.Type != nr.Type || !contains(nr.Subjects, o.Subject()) {
					keep = append(keep, o)
				}
			}
			w.s.Open = keep
		}
	}
}

// rvReview is a world with primaries s1-1 to s1-n in review, the first ok of
// them whose work came back ok and the rest failed, at attempt 1.
func rvReview(t *testing.T, ok, failed int) *world {
	t.Helper()
	w := setup(t, ok+failed)
	w.must(Deal(w.s, DealReq{Sel: Sel{Limit: ok + failed}}))
	for _, m := range []string{"m1", "m2"} {
		w.must(Take(w.s, TakeReq{As: m, Sel: Sel{Limit: 100}}))
	}
	var okIDs, failedIDs []string
	for i := 1; i <= ok+failed; i++ {
		id := fmt.Sprintf("s1-%d.w1", i)
		if i <= ok {
			okIDs = append(okIDs, id)
		} else {
			failedIDs = append(failedIDs, id)
		}
	}
	if len(okIDs) > 0 {
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: okIDs}, Gens: gensOf(w.s, okIDs...)}))
	}
	if len(failedIDs) > 0 {
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: failedIDs}, Gens: gensOf(w.s, failedIDs...), Failed: true, Report: "tests red"}))
	}
	w.clean("review")
	return w
}

// rvPutRead puts a read card of a primary at its attempt in the readers table,
// as a reader leaves it, and lists it in the primary's rcards. An empty column
// is a retired card: a record kept, with no place.
func rvPutRead(w *world, primary string, attempt int, reader, col string) *Card {
	pr := w.s.Work.Card(primary)
	c := &Card{ID: ReadCardID(primary, attempt, reader), Score: pr.Score, Rev: 1, Fields: map[string]string{
		"kind": "read", "primary": primary, "stream": pr.Row, "reader": reader, "attempt": itoa(attempt), "head": pr.F("head")}}
	if col != "" {
		c.Row, c.Col = reader, col
	}
	w.s.Readers.Put(c)
	pr.Fields["rcards"] = strings.Join(append(Split(pr.F("rcards")), c.ID), ",")
	return c
}

// rvAsk asks every primary in review that is due, by R8.
func rvAsk(w *world) {
	w.t.Helper()
	w.rvApply(rvPlan(w, ruleAsk, "ask@1"))
}

// rvReaders is the readers a primary was asked of at its attempt, in reader
// row order.
func rvReaders(w *world, id string) []string {
	var out []string
	for _, rd := range w.s.Readers.Rows() {
		if rc := w.s.Readers.Card(ReadCardID(id, 1, rd)); rc != nil {
			out = append(out, rd)
		}
	}
	return out
}

// rvReads is the read cards of a primary at its attempt that its rcards lists.
func rvReads(w *world, id string) []*Card {
	return newReviewCtx(w.s).readsOf(w.s.Work.Card(id))
}

// rvSay is a reader's report on its read of a primary, by the readers' own
// verb.
func rvSay(w *world, primary, reader, verdict, finding string) {
	w.t.Helper()
	w.must(Read(w.s, ReadReq{As: reader, Verdict: verdict, Finding: finding, Sel: Sel{IDs: []string{ReadCardID(primary, 1, reader)}}}))
}

// rvAllOK asks every primary in review by R8 and has each reader of the ones
// named say ok.
func rvAllOK(w *world, ids ...string) {
	w.t.Helper()
	rvAsk(w)
	for _, id := range ids {
		for _, rd := range rvReaders(w, id) {
			rvSay(w, id, rd, "ok", "")
		}
	}
}

// rvCreates is the entries of a plan that create a card in the table.
func rvCreates(p Plan, table string) []ntable.BatchMemberEntry {
	var out []ntable.BatchMemberEntry
	for _, u := range p.Units {
		for _, c := range u.Changes {
			if c.Table == table && c.Entry.Create != nil {
				out = append(out, c.Entry)
			}
		}
	}
	return out
}

// rvEntries is the entries of a plan on a card of a table.
func rvEntries(p Plan, table, id string) []ntable.BatchMemberEntry {
	var out []ntable.BatchMemberEntry
	for _, u := range p.Units {
		for _, c := range u.Changes {
			if c.Table == table && c.Entry.ID == id {
				out = append(out, c.Entry)
			}
		}
	}
	return out
}

// rvNotes is the requests of a plan for a note of a type, of an op.
func rvNotes(rp RulePlan, op, typ string) []NoteReq {
	var out []NoteReq
	for _, n := range rp.Notes {
		if n.Op == op && n.Type == typ {
			out = append(out, n)
		}
	}
	return out
}

func rvDone(rp RulePlan) []string {
	var out []string
	for _, k := range rp.Done {
		out = append(out, k.Key)
	}
	return out
}

// rvTwin answers a read plan of the review rules from a world, and holds to
// what the plan named: it returns each record with only the fields the query
// projected, the ids of a line or of the head of askwait as the window the
// query asked for, and the rows and counts of the readers and the fleet. A rule
// is planned on the snapshot LoadPartial makes of that answer and never on the
// whole world, so a plan that reads a cell, a count, a row or a field its read
// did not ask for panics where it reads it (1.5.2). The judgments open on the
// subjects a read followed with `jopen` are set on the snapshot by the caller,
// as the tick does from the answer; nothing else the world holds is given.
type rvTwin struct {
	// s is the whole snapshot the answers are read from.
	s *Snapshot
	// lines is what each line of the log names, its cards in order. A line the
	// test gave none names every primary the world holds, in work order.
	lines map[uint64][]string
	// fail stops the test, or the benchmark, on a read the twin cannot answer.
	fail func(format string, a ...any)
}

// rvProject is the record with only the fields the query named; a query naming
// none reads whole records.
func rvProject(c *Card, fields []string) *Card {
	cc := &Card{ID: c.ID, Row: c.Row, Col: c.Col, Score: c.Score, Rev: c.Rev, Fields: map[string]string{}}
	for k, v := range c.Fields {
		if len(fields) == 0 || slices.Contains(fields, k) {
			cc.Fields[k] = v
		}
	}
	return cc
}

func (tw rvTwin) lineIDs(seq uint64) []string {
	if ids, ok := tw.lines[seq]; ok {
		return ids
	}
	var cs []*Card
	for _, c := range tw.s.Work.Cards() {
		if c.Placed() {
			cs = append(cs, c)
		}
	}
	SortCards(cs)
	var ids []string
	for _, c := range cs {
		ids = append(ids, c.ID)
	}
	return ids
}

// askwait is the primaries a "cannot ask" judgment is open or held on, in work
// order: the set R8's askwait key reads the head of.
func (tw rvTwin) askwait() []string {
	s := tw.s
	seen := map[string]bool{}
	var cs []*Card
	for _, list := range [][]Open{s.Open, s.Acked} {
		for _, o := range list {
			if c := s.Work.Card(o.Subject()); o.Note.Type == NCannotAsk && c.Placed() && !seen[c.ID] {
				seen[c.ID] = true
				cs = append(cs, c)
			}
		}
	}
	SortCards(cs)
	var ids []string
	for _, c := range cs {
		ids = append(ids, c.ID)
	}
	return ids
}

func (tw rvTwin) query(q SprintQ) Answer {
	s := tw.s
	a := Answer{Kind: q.Kind}
	add := func(table string, c *Card) {
		if c != nil {
			a.Records = append(a.Records, TableCard{Table: table, Card: rvProject(c, q.Fields)})
		}
	}
	switch q.Kind {
	case QueryReaders:
		a.Rows = append([]string(nil), s.Readers.Rows()...)
		for _, rd := range s.Readers.Rows() {
			for _, col := range []string{Asked, Reading, OK, Broken} {
				a.Counts = append(a.Counts, CellCount{Row: rd, Col: col, N: s.Readers.Count(rd, col)})
			}
		}
		a.Props = propsAnswer(s.Readers, q.Props)
	case QueryFleet:
		a.Rows = append([]string(nil), s.Fleet.Rows()...)
		for _, m := range s.Fleet.Rows() {
			for _, col := range []string{Ready, Working, DoneOK, DoneFailed, Withdrawn} {
				a.Counts = append(a.Counts, CellCount{Row: m, Col: col, N: s.Fleet.Count(m, col)})
			}
			add(Fleet, s.Fleet.Card(CtlID(m)))
		}
		a.Props = propsAnswer(s.Fleet, q.Props)
	case QueryRelated:
		if q.Table != Work {
			tw.fail("the review rules read related records of %q", q.Table)
		}
		var ids []string
		switch q.Source.Kind {
		case SourceIDs:
			ids = q.Source.IDs
		case SourceLine:
			all := tw.lineIDs(q.Source.Seq)
			from := min(max(q.Source.Offset, 0), len(all))
			to := len(all)
			if q.Source.Limit > 0 {
				to = min(to, from+q.Source.Limit)
			}
			ids = all[from:to]
			a.IDs = ids
		case SourceHead:
			if q.Source.Key != reviewHeadAskwait {
				tw.fail("the review rules read the head of %q", q.Source.Key)
			}
			all := tw.askwait()
			ids = all[:len(all)]
			if q.Source.Limit > 0 {
				ids = all[:min(len(all), q.Source.Limit)]
			}
			a.IDs = ids
		}
		for _, id := range ids {
			c := s.Work.Card(id)
			if c == nil {
				continue
			}
			add(Work, c)
			for _, f := range q.Follow {
				switch f {
				case FollowRCards:
					for _, rid := range Split(c.Fields["rcards"]) {
						add(Readers, s.Readers.Card(rid))
					}
				case FollowMerge:
					add(Merge, s.Merge.Card(c.ID))
				case FollowControl:
					add(Merge, s.Merge.Card(CtlID(c.Row)))
				case FollowWork:
					add(Fleet, reviewWorkCard(s, c))
				case FollowJOpen: // set on the snapshot by the caller
				default:
					tw.fail("the twin does not follow %q", f)
				}
			}
		}
	default:
		tw.fail("the twin does not answer a %q query", q.Kind)
	}
	return a
}

func (tw rvTwin) answer(rp ReadPlan) ReadAnswer {
	if n := len(rp.TsetSlots()); n != 0 {
		tw.fail("the review reads ask %d Layer 1 queries, and only composite queries", n)
	}
	ans := ReadAnswer{Epoch: Decimal(itoa(int(tw.s.Epoch))), ActiveEpoch: Decimal(itoa(int(tw.s.Epoch))), TimeMS: Decimal(strconv.FormatInt(t0.UnixMilli(), 10))}
	for _, q := range rp.Sprint {
		ans.Sprint = append(ans.Sprint, tw.query(q))
	}
	return ans
}

// rvRun is one rule run through its read: the plan it made, the read plan it
// asked, the keys it left for later, and the snapshot it planned on.
type rvRun struct {
	Plan RulePlan
	Read ReadPlan
	Left []AgendaKey
	S    *Snapshot
}

// rvJudgments is the judgments open, and those held by an acknowledgement, on
// the subjects the read followed with jopen: what the tick sets on the
// snapshot from the read's answer.
func rvJudgments(ws *Snapshot, rp ReadPlan, ans ReadAnswer) (open, acked []Open) {
	subjects := map[string]bool{}
	for i, q := range rp.Sprint {
		if q.Kind != QueryRelated || !slices.Contains(q.Follow, FollowJOpen) {
			continue
		}
		ids := q.Source.IDs
		if q.Source.Kind != SourceIDs {
			ids = ans.Sprint[i].IDs
		}
		for _, id := range ids {
			subjects[id] = true
		}
	}
	for _, o := range ws.Open {
		if subjects[o.Subject()] {
			open = append(open, o)
		}
	}
	for _, o := range ws.Acked {
		if subjects[o.Subject()] {
			acked = append(acked, o)
		}
	}
	return open, acked
}

// rvRuleOf is the registered rule of that name.
func rvRuleOf(name string) (Rule, bool) {
	for _, r := range RuleTable() {
		if r.Name == name {
			return r, true
		}
	}
	return Rule{}, false
}

// rvLoad is the snapshot a rule plans on, made as the tick makes it: the rule's
// read plans the keys within the bounds, the twin answers the plan from the whole
// snapshot ws, and LoadPartial loads that answer and nothing else. It returns the
// read plan and the keys the read left for a later tick.
func rvLoad(ws *Snapshot, lines map[uint64][]string, r Rule, keys []AgendaKey, b ReadBounds, halvings int, fail func(string, ...any)) (*Snapshot, ReadPlan, []AgendaKey) {
	rp, left := r.Read(keys, b, halvings)
	if !within(rp.Queries(), rp.Cost(), b) {
		fail("%s: the read costs %+v in %d queries, over the bounds %+v", r.Name, rp.Cost(), rp.Queries(), b)
	}
	ans := rvTwin{s: ws, lines: lines, fail: fail}.answer(rp)
	s, err := LoadPartial(rp, ans)
	if err != nil {
		fail("%s: the answer does not load: %v", r.Name, err)
	}
	s.Coordinator, s.Actor = ws.Coordinator, ws.Actor
	s.Open, s.Acked = rvJudgments(ws, rp, ans)
	return s, rp, left
}

// rvExec runs a rule as the tick does: it loads the snapshot (rvLoad) and the
// rule plans the keys its read took on it.
func rvExec(w *world, lines map[uint64][]string, rule string, now Now, keys []AgendaKey, b ReadBounds, halvings int) rvRun {
	t := w.t
	t.Helper()
	r, found := rvRuleOf(rule)
	if !found {
		t.Fatalf("no rule %q is registered", rule)
	}
	s, rp, left := rvLoad(w.s, lines, r, keys, b, halvings, t.Fatalf)
	plan := r.Plan(s, keys[:len(keys)-len(left)], now)
	if err := s.UnloadedErr(); err != nil {
		t.Fatalf("%s: the plan read what its read did not load: %v", rule, err)
	}
	return rvRun{Plan: plan, Read: rp, Left: left, S: s}
}

// rvPlanKeys is a rule's plan of keys, through its read at layer 1's bounds.
func rvPlanKeys(w *world, rule string, now Now, keys []AgendaKey) RulePlan {
	w.t.Helper()
	run := rvExec(w, nil, rule, now, keys, L1ReadBounds(), 0)
	if len(run.Left) != 0 {
		w.t.Fatalf("%s: the read left %v of the keys for a later tick", rule, run.Left)
	}
	return run.Plan
}

// rvPlanAt is a rule's plan of the keys named, at the clocks given.
func rvPlanAt(w *world, rule string, now Now, keys ...string) RulePlan {
	w.t.Helper()
	return rvPlanKeys(w, rule, now, rvKeys(keys...))
}

// rvPlan is a rule's plan of the keys named, at rvNow.
func rvPlan(w *world, rule string, keys ...string) RulePlan {
	w.t.Helper()
	return rvPlanAt(w, rule, rvNow(), keys...)
}

// R8: two different readers, the next round the readers (errata 3 amendment
// 5), the ones it names first.
func TestAskTwoDifferentReaders(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 3, 0)
	// reader-a is already asked of s1-3; the ask's rolling index is 0 (no stream
	// counts an ask), and a queue's length does not choose
	rvPutRead(w, "s1-3", 1, "reader-a", Asked)
	w.clean("a read of s1-3")

	rp := rvPlan(w, ruleAsk, "ask@7")
	if len(rp.Plan.Units) != 2 || len(rp.Plan.Refused) != 0 {
		t.Fatalf("units %d, refused %v: s1-3 has a read standing and is not asked again", len(rp.Plan.Units), rp.Plan.Refused)
	}
	want := map[string][2]string{"s1-1": {"reader-a", "reader-b"}, "s1-2": {"reader-c", "reader-a"}}
	for _, u := range rp.Plan.Units {
		var got []string
		for _, e := range rvCreates(Plan{Units: []Unit{u}}, Readers) {
			got = append(got, e.Create.Row)
			if e.Create.Col != Asked || e.Create.Score != w.s.Work.Card(u.Key).Score || e.Set["reader"] != e.Create.Row || e.Set["head"] != w.s.Work.Card(u.Key).F("head") {
				t.Errorf("%s: the read card is %+v", u.Key, e)
			}
		}
		if len(got) != 2 || got[0] == got[1] || [2]string{got[0], got[1]} != want[u.Key] {
			t.Errorf("%s is asked of %v, want the next two different readers round the readers, %v", u.Key, got, want[u.Key])
		}
	}
	if got := rvDone(rp); len(got) != 1 || got[0] != "ask@7" || len(rp.Requeue) != 0 {
		t.Errorf("the key: done %v, requeue %v", got, rp.Requeue)
	}
	w.rvApply(rp)
	w.clean("ask")
	for _, id := range []string{"s1-1", "s1-2"} {
		if n := len(rvReads(w, id)); n != 2 {
			t.Errorf("%s has %d read cards after the ask", id, n)
		}
	}
}

// The readers a primary names are asked first; the stamps of a new read are in
// the clocks it was given, R for the deadline and the wall for the reader.
func TestAskNamedReadersFirstAndStamps(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 1, 0)
	w.s.Work.Card("s1-1").Fields["asked"] = "reader-c,reader-a"
	now := rvNow()
	rp := rvPlanAt(w, ruleAsk, now, "ask:s1-1")
	creates := rvCreates(rp.Plan, Readers)
	if len(creates) != 2 || creates[0].Create.Row != "reader-c" || creates[1].Create.Row != "reader-a" {
		t.Fatalf("asked of %+v, want the readers it names, in the order it names them", creates)
	}
	for _, e := range creates {
		if e.Set["asked_r"] != "5000000" || e.Set["due_unbegun"] != fmt.Sprint(5_000_000+DeadlineUnbegun.Milliseconds()) || e.Set["asked"] != stamp(t0) {
			t.Errorf("the stamps of %s: %v", e.ID, e.Set)
		}
	}
	if set := rvEntries(rp.Plan, Work, "s1-1")[0].Set; set["asked"] != "reader-c,reader-a" {
		t.Errorf("the primary's readers: %v", set)
	}
}

// A reader that has a card at the attempt has read it, retired or not, so a
// read that replaces another goes to a reader not yet asked; with fewer than
// two such readers the primary cannot be asked.
func TestAskNeverAsksAReaderTwiceAtAnAttempt(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 1, 0)
	// reader-a read attempt 1 and its card was retired: the record is kept, and
	// nothing stands; reader-b is the one reader left
	w.s.Readers.SetRows([]string{"reader-a", "reader-b"})
	rvPutRead(w, "s1-1", 1, "reader-a", "")
	rp := rvPlan(w, ruleAsk, "ask:s1-1")
	creates := rvCreates(rp.Plan, Readers)
	if len(creates) != 0 || len(rp.Plan.Units) != 0 {
		t.Fatalf("asked of %+v with one reader able", creates)
	}
	notes := rvNotes(rp, reviewOpen, NCannotAsk)
	if len(notes) != 1 || len(notes[0].Subjects) != 1 || notes[0].Subjects[0] != "s1-1" || notes[0].Cause != reviewCauses[NCannotAsk] {
		t.Fatalf("notes %+v, want one \"cannot ask\" on s1-1", rp.Notes)
	}
	// a new reader is a second able reader
	w.s.Readers.SetRows(append(w.s.Readers.Rows(), "reader-d"))
	rp = rvPlan(w, ruleAsk, "ask:s1-1")
	var asked []string
	for _, e := range rvCreates(rp.Plan, Readers) {
		asked = append(asked, e.Create.Row)
	}
	if len(asked) != 2 || contains(asked, "reader-a") {
		t.Fatalf("asked of %v: reader-a has read the attempt and is not asked again", asked)
	}
	if len(rvNotes(rp, reviewOpen, NCannotAsk)) != 0 {
		t.Errorf("cannot ask raised though the primary was asked: %+v", rp.Notes)
	}
}

// Each id is appended to rcards, after the read cards of the attempts before
// it; a primary with 15 read cards cannot have more, and is refused by name.
func TestAskAppendsRcards(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 2, 0)
	w.s.Work.Card("s1-1").Fields["rcards"] = "s1-1.r0.reader-a,s1-1.r0.reader-b"
	var many []string
	for i := 0; i < MaxRCards-1; i++ {
		many = append(many, fmt.Sprintf("old%d", i))
	}
	w.s.Work.Card("s1-2").Fields["rcards"] = strings.Join(many, ",")

	rp := rvPlan(w, ruleAsk, "ask@3")
	set := rvEntries(rp.Plan, Work, "s1-1")
	if len(set) != 1 || set[0].Set["rcards"] != "s1-1.r0.reader-a,s1-1.r0.reader-b,s1-1.r1.reader-a,s1-1.r1.reader-b" {
		t.Fatalf("s1-1's rcards: %+v", set)
	}
	if len(rvCreates(rp.Plan, Readers)) != 2 {
		t.Errorf("s1-2 was asked past the 15 read cards a primary has: %+v", rp.Plan.Units)
	}
	if len(rp.Plan.Refused) != 1 || rp.Plan.Refused[0].Key != "s1-2" || !strings.Contains(rp.Plan.Refused[0].Why, "at most 15") {
		t.Fatalf("refused %+v, want s1-2 named with the bound", rp.Plan.Refused)
	}
	refused := rvEntries(rp.Plan, Work, "s1-2")
	if len(refused) != 1 || refused[0].Set["refused"] != "ask: "+rp.Plan.Refused[0].Why {
		t.Errorf("s1-2 is not marked refused: %+v", refused)
	}
	if n := rvNotes(rp, reviewOpen, typeCouldNotMove); len(n) != 1 || n[0].Cause != "ask" || n[0].Subjects[0] != "s1-2" {
		t.Errorf("no judgment for the refused card: %+v", rp.Notes)
	}
}

// R9: ok from two different readers at the head, and nothing less.
func TestAcceptTwoDifferentReaders(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 4, 0)
	rvAllOK(w, "s1-1", "s1-3", "s1-4")
	// s1-2: one reader did not say ok; s1-3: one ok is of another head; s1-4: an
	// ok card that names one reader and sits on another's row counts for none
	two := rvReaders(w, "s1-2")
	rvSay(w, "s1-2", two[0], "broken", "off by one")
	rvSay(w, "s1-2", two[1], "ok", "")
	w.s.Readers.Card(ReadCardID("s1-3", 1, rvReaders(w, "s1-3")[1])).Fields["head"] = "an older head"
	w.s.Readers.Card(ReadCardID("s1-4", 1, rvReaders(w, "s1-4")[1])).Fields["reader"] = "reader-c"
	// s1-1 has a third read, still asked: accept retires it
	third := rvPutRead(w, "s1-1", 1, "reader-c", Asked)

	rp := rvPlan(w, ruleAccept, "accept@9")
	if len(rp.Plan.Units) != 1 || rp.Plan.Units[0].Key != "s1-1" || len(rp.Plan.Refused) != 0 {
		t.Fatalf("units %+v, refused %v: only s1-1 has ok from two different readers at its head", rp.Plan.Units, rp.Plan.Refused)
	}
	if got := rvDone(rp); len(got) != 1 || got[0] != "accept@9" {
		t.Errorf("the key is not finished: %v", got)
	}
	for _, rd := range []string{"reader-a", "reader-b"} {
		id := ReadCardID("s1-1", 1, rd)
		es := rvEntries(rp.Plan, Readers, id)
		if len(es) != 1 || es[0].Expect == nil || es[0].Expect.Revision != u64(w.s.Readers.Card(id).Rev) || es[0].Expect.Place.Col != OK {
			t.Errorf("the ok read %s is not guarded at its place and revision: %+v", id, es)
		}
	}
	if es := rvEntries(rp.Plan, Readers, third.ID); len(es) != 1 || !es[0].Remove || es[0].Set["retired_by"] != "accept" {
		t.Errorf("the outstanding read is not retired: %+v", es)
	}
	if es := rvEntries(rp.Plan, Merge, "s1-1"); len(es) != 1 || es[0].Create == nil || es[0].Create.Col != Queued || es[0].Create.Score != w.s.Work.Card("s1-1").Score {
		t.Errorf("the merge card: %+v", es)
	}
	now := rvNow()
	ctl := rvEntries(rp.Plan, Merge, CtlID("s1"))
	if len(ctl) != 1 || ctl[0].Set["state"] != StreamMerging || ctl[0].Set["due_mergeidle"] != fmt.Sprint(now.R+DeadlineMergeIdle.Milliseconds()) || ctl[0].Set["since"] == "" {
		t.Errorf("the stream: %+v", ctl)
	}
	acc := rvNotes(rp, reviewKnow, typeAccepted)
	if len(acc) != 1 || acc[0].Text != "s1-1 (ok from reader-a and reader-b)" || len(acc[0].Subjects) != 1 {
		t.Errorf("accepted notice: %+v", acc)
	}
	if st := rvNotes(rp, reviewKnow, NStartedMerging); len(st) != 1 || st[0].Subjects[0] != StreamSubject("s1") {
		t.Errorf("started merging notice: %+v", st)
	}
	w.rvApply(rp)
	// the forged card is the one thing the check finds, and the rule left it as it was
	if v := Check(w.s, nil); len(v) != 1 || !strings.Contains(v[0].Detail, "s1-4.r1.reader-b") {
		t.Errorf("check after the accept: %v", v)
	}
	w.s.Readers.Card(ReadCardID("s1-4", 1, rvReaders(w, "s1-4")[1])).Fields["reader"] = rvReaders(w, "s1-4")[1]
	w.clean("accept")
	if w.state("s1-1") != Merging || w.s.Merge.Placed("s1-1").Col != Queued || w.s.Work.Card("s1-1").F("readers") != "reader-a,reader-b" {
		t.Fatalf("s1-1 is %s, merge %v, readers %q", w.state("s1-1"), w.s.Merge.Placed("s1-1"), w.s.Work.Card("s1-1").F("readers"))
	}
	if w.s.Readers.Placed(third.ID) != nil {
		t.Errorf("the outstanding read stands after the accept")
	}
	for _, id := range []string{"s1-2", "s1-3", "s1-4"} {
		if w.state(id) != Review {
			t.Errorf("%s is %s", id, w.state(id))
		}
	}
}

// A primary accepted into a stream already merging leaves the stream as it is
// and guards its control card at its revision, so a stop since the read refuses
// the step.
func TestAcceptGuardsAStreamAlreadyMerging(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 2, 0)
	rvAllOK(w, "s1-1", "s1-2")
	ctl := w.s.StreamCtl("s1")
	ctl.Fields["state"] = StreamMerging
	rp := rvPlan(w, ruleAccept, "accept:s1-1", "accept:s1-2")
	if len(rp.Plan.Units) != 2 {
		t.Fatalf("units %d", len(rp.Plan.Units))
	}
	es := rvEntries(rp.Plan, Merge, CtlID("s1"))
	if len(es) != 1 || len(es[0].Set) != 0 || es[0].Expect == nil || es[0].Expect.Revision != u64(ctl.Rev) {
		t.Errorf("the control card is written or not guarded: %+v", es)
	}
	if len(rvNotes(rp, reviewKnow, NStartedMerging)) != 0 {
		t.Errorf("started merging said of a stream that was merging")
	}
	if n := rvNotes(rp, reviewKnow, typeAccepted); len(n) != 1 || len(n[0].Subjects) != 2 {
		t.Errorf("one notice a stream naming both: %+v", n)
	}
	w.rvApply(rp)
	w.clean("accept into a merging stream")
}

// A primary whose CI is red at its head is skipped and its key removed: the
// coordinator decides it. A red CI of an older head does not hold it.
func TestAcceptSkipsCIRed(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 5, 0)
	rvAllOK(w, "s1-1", "s1-2", "s1-3", "s1-4", "s1-5")
	set := func(id string, kv ...string) {
		for i := 0; i < len(kv); i += 2 {
			w.s.Work.Card(id).Fields[kv[i]] = kv[i+1]
		}
	}
	head := func(id string) string { return w.s.Work.Card(id).F("head") }
	set("s1-1", "ci", "red", "ci_head", head("s1-1"))
	set("s1-2", "ci", "red", "ci_head", "an older head")
	set("s1-3", "ci", "green", "ci_head", head("s1-3"))
	set("s1-4", "ci", "red") // red, and it names no head: the current one's
	rp := rvPlan(w, ruleAccept, "accept:s1-1", "accept:s1-2", "accept:s1-3", "accept:s1-4", "accept:s1-5")
	var got []string
	for _, u := range rp.Plan.Units {
		got = append(got, u.Key)
	}
	if strings.Join(got, ",") != "s1-2,s1-3,s1-5" {
		t.Fatalf("accepted %v, want s1-2, s1-3 and s1-5", got)
	}
	if len(rp.Done) != 5 || len(rp.Requeue) != 0 || len(rp.Plan.Refused) != 0 {
		t.Errorf("the skipped keys are removed: done %v, requeue %v, refused %v", rp.Done, rp.Requeue, rp.Plan.Refused)
	}
	if AcceptDue(w.s, w.s.Work.Card("s1-1")) || AcceptDue(w.s, w.s.Work.Card("s1-4")) || !AcceptDue(w.s, w.s.Work.Card("s1-2")) {
		t.Errorf("AcceptDue does not say what the rule does")
	}
	// a green report at the head, as the CI verb records it, lets it go
	set("s1-1", "ci", "green")
	if !AcceptDue(w.s, w.s.Work.Card("s1-1")) {
		t.Errorf("a green CI at its head holds it")
	}
}

// A primary with "returned to review" open is the coordinator's to decide.
func TestAcceptSkipsReturned(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 2, 0)
	rvAllOK(w, "s1-1", "s1-2")
	returned := Note{ID: "n900", Kind: Judgment, Type: NReturned, Stream: "s1", Primaries: []string{"s1-1"}, Count: 1}
	w.s.Open = append(w.s.Open, Open{Key: OpenKey(returned.ID, "s1-1"), Note: returned})
	rp := rvPlan(w, ruleAccept, "accept:s1-1", "accept:s1-2")
	if len(rp.Plan.Units) != 1 || rp.Plan.Units[0].Key != "s1-2" || len(rp.Done) != 2 {
		t.Fatalf("units %+v, done %v: s1-1 is returned and its key is finished", rp.Plan.Units, rp.Done)
	}
	w.s.Open = nil
	if rp = rvPlan(w, ruleAccept, "accept:s1-1"); len(rp.Plan.Units) != 1 || rp.Plan.Units[0].Key != "s1-1" {
		t.Errorf("s1-1 is not accepted once the judgment is closed: %+v", rp.Plan.Units)
	}
}

// A merge record that is not absent or returned, and a stream with no control
// card, are refused by name and the card is marked, once.
func TestAcceptRefusesAMergeRecordElsewhere(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 1, 0)
	rvAllOK(w, "s1-1")
	pr := w.s.Work.Card("s1-1")
	w.s.Merge.Put(&Card{ID: "s1-1", Row: "s1", Col: Queued, Score: pr.Score, Rev: 1, Fields: map[string]string{"kind": "merge", "primary": "s1-1", "stream": "s1"}})
	rp := rvPlan(w, ruleAccept, "accept:s1-1")
	if len(rp.Plan.Refused) != 1 || !strings.Contains(rp.Plan.Refused[0].Why, "merge record is s1:queued") {
		t.Fatalf("refused %+v", rp.Plan.Refused)
	}
	if len(rvCreates(rp.Plan, Merge)) != 0 || w.state("s1-1") != Review {
		t.Errorf("a plan that moves it: %+v", rp.Plan.Units)
	}
	if es := rvEntries(rp.Plan, Work, "s1-1"); len(es) != 1 || es[0].Set["refused"] != "accept: "+rp.Plan.Refused[0].Why || es[0].Move != nil {
		t.Errorf("s1-1 is not marked refused and left in review: %+v", es)
	}
	if n := rvNotes(rp, reviewOpen, typeCouldNotMove); len(n) != 1 || n[0].Cause != "accept" {
		t.Errorf("no judgment: %+v", rp.Notes)
	}
	w.rvApply(rp)
	if second := rvPlan(w, ruleAccept, "accept:s1-1"); !rvEmpty(second) {
		t.Errorf("a refused primary is planned again: %+v", second)
	}
	// no control card for the stream
	w2 := rvReview(t, 1, 0)
	rvAllOK(w2, "s1-1")
	w2.s.Merge.Card(CtlID("s1")).Col = ""
	w2.s.Merge.Put(w2.s.Merge.Card(CtlID("s1")))
	rp = rvPlan(w2, ruleAccept, "accept:s1-1")
	if len(rp.Plan.Refused) != 1 || !strings.Contains(rp.Plan.Refused[0].Why, "has no merge row") {
		t.Errorf("refused %+v", rp.Plan.Refused)
	}
}

// R10: the attempt is dealt at once, to a member other than the one whose work
// it redoes, with avoid set and rereads back to 0.
func TestReworkSetsAvoidResetsRereads(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 0, 1)
	pr := w.s.Work.Card("s1-1")
	pr.Fields["rereads"] = "2"
	worked := w.s.Fleet.Card(pr.F("work")).Row
	other := "m1"
	if worked == "m1" {
		other = "m2"
	}
	now := rvNow()
	rp := rvPlanAt(w, ruleRework, now, "rework:s1-1")
	if len(rp.Plan.Units) != 1 || len(rp.Plan.Refused) != 0 {
		t.Fatalf("units %+v, refused %v", rp.Plan.Units, rp.Plan.Refused)
	}
	move := rvEntries(rp.Plan, Work, "s1-1")
	if len(move) != 1 || move[0].Move == nil || move[0].Move.Col != Working {
		t.Fatalf("the primary's entry: %+v", move)
	}
	for k, v := range map[string]string{"avoid": worked, "rereads": "0", "fix": "tests red", "attempt": "2", "work": "s1-1.w2", "reworks": "1"} {
		if move[0].Set[k] != v {
			t.Errorf("%s = %q, want %q", k, move[0].Set[k], v)
		}
	}
	if !contains(move[0].Unset, "result") {
		t.Errorf("the failed result stays: %v", move[0].Unset)
	}
	card := rvCreates(rp.Plan, Fleet)
	if len(card) != 1 || card[0].ID != "s1-1.w2" || card[0].Create.Row != other || card[0].Create.Col != Ready {
		t.Fatalf("the next attempt is %+v, want s1-1.w2 dealt to %s, not %s", card, other, worked)
	}
	if card[0].Set["untaken_r"] != "5000000" || card[0].Set["due_untaken"] != fmt.Sprint(now.R+DeadlineUntaken.Milliseconds()) || card[0].Set["fix"] != "tests red" || card[0].Set["gen"] != "1" {
		t.Errorf("the work card's fields: %v", card[0].Set)
	}
	if len(rp.Guards) != 1 || rp.Guards[0] != (XGuard{Kind: reviewGuardMemberUp, Member: other}) {
		t.Errorf("guards %+v, want the receiving member up", rp.Guards)
	}
	if g := rvEntries(rp.Plan, Fleet, pr.F("work")); len(g) != 1 || g[0].Expect == nil || g[0].Expect.Place.Col != DoneFailed {
		t.Errorf("the failed work card is not guarded at its place: %+v", g)
	}
	if n := rvNotes(rp, reviewKnow, typeReworked); len(n) != 1 || n[0].Text != "s1-1, attempt 2, because tests red" {
		t.Errorf("notice %+v", n)
	}
	w.rvApply(rp)
	w.clean("rework")
	if w.state("s1-1") != Working || w.s.Work.Card("s1-1").F("avoid") != worked || w.s.Work.Card("s1-1").F("rereads") != "0" {
		t.Errorf("after: %s avoid %q rereads %q", w.state("s1-1"), w.s.Work.Card("s1-1").F("avoid"), w.s.Work.Card("s1-1").F("rereads"))
	}

	// the other member has no room: avoid takes it, only then
	w = rvReview(t, 0, 1)
	worked = w.s.Fleet.Card(w.s.Work.Card("s1-1").F("work")).Row
	other = map[string]string{"m1": "m2", "m2": "m1"}[worked]
	rvFillReady(w, other, w.s.Width(other)) // at its width (width.go)
	if c := rvCreates(rvPlanAt(w, ruleRework, now, "rework:s1-1").Plan, Fleet); len(c) != 1 || c[0].Create.Row != worked {
		t.Errorf("with %s full, dealt to %+v, want %s", other, c, worked)
	}
	// nobody has room: the primary goes back to ready with avoid, into again
	rvFillReady(w, worked, w.s.Width(worked))
	rp = rvPlanAt(w, ruleRework, now, "rework:s1-1")
	move = rvEntries(rp.Plan, Work, "s1-1")
	if len(rvCreates(rp.Plan, Fleet)) != 0 || len(move) != 1 || move[0].Move == nil || move[0].Move.Col != Ready || move[0].Set["avoid"] != worked || move[0].Set["rereads"] != "0" {
		t.Errorf("with no room: %+v", rp.Plan.Units)
	}
	if len(rp.Guards) != 0 {
		t.Errorf("guards %+v for no member", rp.Guards)
	}
	// nobody is up: the same
	w = rvReview(t, 0, 1)
	for _, m := range []string{"m1", "m2"} {
		w.s.MemberCtl(m).Fields["status"] = Down
	}
	move = rvEntries(rvPlanAt(w, ruleRework, now, "rework:s1-1").Plan, Work, "s1-1")
	if len(move) != 1 || move[0].Move == nil || move[0].Move.Col != Ready || move[0].Set["avoid"] == "" {
		t.Errorf("with no member up: %+v", move)
	}
}

// rvFillReady puts n ready work cards of primaries the table does not hold in a
// member's ready cell: a queue as long as the member's cap.
func rvFillReady(w *world, member string, n int) {
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("fill-%s-%d.w1", member, i)
		w.s.Fleet.Put(&Card{ID: id, Row: member, Col: Ready, Rev: 1, Fields: map[string]string{"kind": "work", "primary": "fill", "member": member}})
	}
}

// R10 reworks a read found broken on its finding, retires the attempt's reads
// and keeps the readers it names, so the fixed work is asked of the same ones.
func TestReworkBrokenReadTakesTheFinding(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 1, 0)
	w.rvApply(rvPlan(w, ruleAsk, "ask:s1-1"))
	a, b := ReadCardID("s1-1", 1, "reader-a"), ReadCardID("s1-1", 1, "reader-b")
	w.must(Read(w.s, ReadReq{As: "reader-a", Verdict: "broken", Finding: "off by one", Sel: Sel{IDs: []string{a}}}))
	w.must(Read(w.s, ReadReq{As: "reader-b", Verdict: "ok", Sel: Sel{IDs: []string{b}}}))
	if !ReworkDue(w.s, w.s.Work.Card("s1-1")) {
		t.Fatalf("a broken read does not make a rework due")
	}
	rp := rvPlan(w, ruleRework, "rework:s1-1")
	move := rvEntries(rp.Plan, Work, "s1-1")
	if len(move) != 1 || move[0].Set["fix"] != "off by one" || move[0].Set["asked"] != "reader-a,reader-b" || move[0].Set["broken_reads"] != "1" {
		t.Fatalf("the primary's entry: %+v", move)
	}
	for _, id := range []string{a, b} {
		if es := rvEntries(rp.Plan, Readers, id); len(es) != 1 || !es[0].Remove || es[0].Set["retired_by"] != "rework" || es[0].Expect.Revision != u64(w.s.Readers.Card(id).Rev) {
			t.Errorf("read %s is not retired at its revision: %+v", id, es)
		}
	}
	w.rvApply(rp)
	w.clean("rework of a broken read")
	for _, id := range []string{a, b} {
		if w.s.Readers.Card(id) == nil || w.s.Readers.Placed(id) != nil {
			t.Errorf("the attempt's read %s is not retired and kept", id)
		}
	}
}

// A primary that has failed MaxAttempts times stays in review with its bound
// set and the judgment named; only the coordinator's rework takes it further,
// and unsets the bound.
func TestReworkAtAttemptsBound(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 0, 1)
	for attempt := 1; attempt < MaxAttempts; attempt++ {
		rp := rvPlan(w, ruleRework, "rework:s1-1")
		if len(rvCreates(rp.Plan, Fleet)) != 1 {
			t.Fatalf("attempt %d is not reworked below the bound: %+v", attempt, rp.Plan.Units)
		}
		w.rvApply(rp)
		card := WorkCardID("s1-1", attempt+1)
		member := w.s.Fleet.Card(card).Row
		w.must(Take(w.s, TakeReq{As: member, Sel: Sel{IDs: []string{card}}, Gens: gensOf(w.s, card)}))
		w.must(Finish(w.s, FinishReq{As: member, Sel: Sel{IDs: []string{card}}, Gens: gensOf(w.s, card), Failed: true, Report: fmt.Sprintf("still red at attempt %d", attempt+1)}))
	}
	pr := w.s.Work.Card("s1-1")
	if pr.Int("attempt") != MaxAttempts || w.state("s1-1") != Review {
		t.Fatalf("attempt %d, %s", pr.Int("attempt"), w.state("s1-1"))
	}
	rp := rvPlan(w, ruleRework, "rework:s1-1")
	if len(rp.Plan.Units) != 1 || len(rvCreates(rp.Plan, Fleet)) != 0 {
		t.Fatalf("at the bound: %+v", rp.Plan.Units)
	}
	es := rvEntries(rp.Plan, Work, "s1-1")
	if len(es) != 1 || es[0].Move != nil || es[0].Set["bound"] != BoundAttempts || len(es[0].Set) != 1 {
		t.Fatalf("the bound is one field of the primary in review: %+v", es)
	}
	n := rvNotes(rp, reviewOpen, NBound)
	if len(n) != 1 || n[0].Cause != BoundAttempts || n[0].Subjects[0] != "s1-1" || !strings.Contains(n[0].Text, "nova-sprint log --card s1-1") {
		t.Fatalf("the judgment: %+v", rp.Notes)
	}
	if len(rvNotes(rp, reviewKnow, typeReworked)) != 0 || len(rp.Guards) != 0 {
		t.Errorf("a rework said or guarded at the bound: %+v %+v", rp.Notes, rp.Guards)
	}
	w.rvApply(rp)
	if ReworkDue(w.s, w.s.Work.Card("s1-1")) || AskDue(w.s, w.s.Work.Card("s1-1")) || !rvEmpty(rvPlan(w, ruleRework, "rework:s1-1")) {
		t.Errorf("the machine acts on a primary at its bound")
	}
	// the coordinator's rework goes on, with its fix, and unsets the bound
	p := ReworkAt(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "start from the failing test", Who: "coordinator"}, rvNow())
	if len(p.Refused) != 0 {
		t.Fatalf("refused %+v", p.Refused)
	}
	w.must(p)
	pr = w.s.Work.Card("s1-1")
	if w.state("s1-1") != Working || pr.Int("attempt") != MaxAttempts+1 || pr.F("bound") != "" || pr.F("fix") != "start from the failing test" || pr.F("rereads") != "0" || pr.F("avoid") == "" {
		t.Errorf("after the coordinator's rework: %s attempt %d bound %q fix %q avoid %q", w.state("s1-1"), pr.Int("attempt"), pr.F("bound"), pr.F("fix"), pr.F("avoid"))
	}
	for _, o := range w.openOn("s1-1") {
		if o.Note.Type == NBound {
			t.Errorf("the coordinator's rework left \"a card reached its bound\" open: %+v", o)
		}
	}
	w.clean("rework at the bound")
	// a coordinator's rework of a primary with no evidence and no fix is refused by name
	w = rvReview(t, 1, 0)
	if p := ReworkAt(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Who: "coordinator"}, rvNow()); len(p.Refused) != 1 || !strings.Contains(p.Refused[0].Why, "--fix") || len(p.Units) != 0 {
		t.Errorf("no fix: %+v", p)
	}
}

// A primary in ready at its redeal bound is reworked by the coordinator: the
// withdrawn work card is retired, the next attempt is dealt to a member other
// than the last, and the bound is unset. The machine's rework does not take it.
func TestReworkAtRedealBoundInReady(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Deal(w.s, DealReq{Sel: Sel{Limit: 1}}))
	pr := w.s.Work.Card("s1-1")
	wc := w.s.Fleet.Card(pr.F("work"))
	last := wc.Row
	wc.Col = Withdrawn
	wc.Fields["redeals"] = itoa(reviewMaxRedeals)
	wc.Fields["member"] = last
	w.s.Fleet.Put(wc)
	pr.Col = Ready
	delete(pr.Fields, "work")
	pr.Fields["bound"] = "redeals"
	w.s.Work.Put(pr)
	if AtRedealBound(w.s, pr) == nil {
		t.Fatal("the primary is not at its redeal bound")
	}
	if ReworkDue(w.s, pr) || AskDue(w.s, pr) || AcceptDue(w.s, pr) {
		t.Errorf("a review rule takes a primary in ready")
	}
	p := ReworkAt(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "it never ran to its end", Who: "coordinator"}, rvNow())
	if len(p.Refused) != 0 || len(p.Units) != 1 {
		t.Fatalf("refused %+v, units %d", p.Refused, len(p.Units))
	}
	if es := rvEntries(p, Fleet, wc.ID); len(es) != 1 || !es[0].Remove || es[0].Set["retired_by"] != "rework" {
		t.Errorf("the withdrawn card is not retired: %+v", es)
	}
	card := rvCreates(p, Fleet)
	if len(card) != 1 || card[0].ID != "s1-1.w2" || card[0].Create.Row == last {
		t.Errorf("the next attempt %+v, want s1-1.w2 dealt to a member other than %s", card, last)
	}
	w.must(p)
	pr = w.s.Work.Card("s1-1")
	if w.state("s1-1") != Working || pr.F("bound") != "" || pr.F("avoid") != last || pr.F("work") != "s1-1.w2" || pr.Int("attempt") != 2 {
		t.Errorf("after: %s bound %q avoid %q work %q attempt %d", w.state("s1-1"), pr.F("bound"), pr.F("avoid"), pr.F("work"), pr.Int("attempt"))
	}
	if w.s.Fleet.Placed(wc.ID) != nil {
		t.Errorf("the withdrawn card stands")
	}
	w.clean("rework in ready")
}

// The coordinator's rework takes a selection as it takes named ids: the primaries
// of a stream in work order up to the limit, each with the fix it was given; a
// primary that is not in review is refused by name, and ReworkV21 plans at the
// snapshot's time.
func TestReworkV21TakesASelectionAndRefusesByName(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 3, 0)
	p := ReworkV21(w.s, ReworkReq{Sel: Sel{Stream: "s1", Limit: 2}, Fix: "cover the empty case", Who: "coordinator"})
	if len(p.Units) != 2 || len(p.Refused) != 0 || p.Units[0].Key != "s1-1" || p.Units[1].Key != "s1-2" {
		t.Fatalf("units %+v, refused %+v: the first two of the stream, in work order", p.Units, p.Refused)
	}
	card := rvCreates(p, Fleet)
	if len(card) != 2 || card[0].Set["untaken_r"] != fmt.Sprint(t0.UnixMilli()) || card[0].Set["fix"] != "cover the empty case" {
		t.Errorf("the cards dealt: %+v", card)
	}
	w.must(p)
	w.clean("rework of a selection")
	// s1-1 is working now, and s1-3 is in review with no evidence and no fix
	p = ReworkAt(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1", "s1-3"}}, Who: "coordinator"}, rvNow())
	if len(p.Units) != 0 || len(p.Refused) != 2 || !strings.Contains(p.Refused[0].Why, "not review (it is working)") || !strings.Contains(p.Refused[1].Why, "--fix") {
		t.Errorf("refusals %+v, units %d", p.Refused, len(p.Units))
	}
	// a merging primary is returned first
	w2 := rvReview(t, 1, 0)
	rvAllOK(w2, "s1-1")
	w2.rvApply(rvPlan(w2, ruleAccept, "accept:s1-1"))
	if p = ReworkAt(w2.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "x", Who: "coordinator"}, rvNow()); len(p.Refused) != 1 || !strings.Contains(p.Refused[0].Why, "nova-sprint return s1-1") {
		t.Errorf("merging: %+v", p.Refused)
	}
}

// A notice lists at most MaxListed primaries and says how many more; a fix in a
// notice is cut at a rune boundary.
func TestReviewNoticesAreBounded(t *testing.T) {
	t.Parallel()
	var lines []string
	for i := 0; i < MaxListed+1; i++ {
		lines = append(lines, fmt.Sprintf("p%d", i))
	}
	if got := reviewList(lines); !strings.HasSuffix(got, "; and 1 more") || strings.Count(got, ";") != MaxListed {
		t.Errorf("a list of %d: %q", len(lines), got)
	}
	if got := reviewList(lines[:3]); got != "p0; p1; p2" {
		t.Errorf("a short list: %q", got)
	}
	if got := reviewCut("h\u00e9llo", 2); got != "h..." {
		t.Errorf("a cut inside a rune: %q", got)
	}
	if got := reviewCut("short", 200); got != "short" {
		t.Errorf("a short text: %q", got)
	}
	items := []reviewItem{{"a", "s2", "a"}, {"b", "s1", "b"}, {"c", "s2", "c"}}
	n := reviewNotices(typeAccepted, items)
	if len(n) != 2 || strings.Join(n[0].Subjects, ",") != "a,c" || strings.Join(n[1].Subjects, ",") != "b" || n[0].Text != "a; c" {
		t.Errorf("one notice a stream, in the order the streams first appear: %+v", n)
	}
}

// Cards refused in one plan share one judgment, which says how many and where
// each one's reason is.
func TestRefusedCardsShareOneJudgment(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 2, 0)
	var many []string
	for i := 0; i < MaxRCards-1; i++ {
		many = append(many, fmt.Sprintf("old%d", i))
	}
	for _, id := range []string{"s1-1", "s1-2"} {
		w.s.Work.Card(id).Fields["rcards"] = strings.Join(many, ",")
	}
	rp := rvPlan(w, ruleAsk, "ask@1")
	n := rvNotes(rp, reviewOpen, typeCouldNotMove)
	if len(rp.Plan.Units) != 2 || len(n) != 1 || strings.Join(n[0].Subjects, ",") != "s1-1,s1-2" || !strings.Contains(n[0].Text, "2 cards could not be moved") {
		t.Fatalf("units %d, notes %+v", len(rp.Plan.Units), rp.Notes)
	}
	for _, u := range rp.Plan.Units {
		if es := rvEntries(Plan{Units: []Unit{u}}, Work, u.Key); len(es) != 1 || !strings.HasPrefix(es[0].Set["refused"], "ask: ") {
			t.Errorf("%s: %+v", u.Key, es)
		}
	}
}

// A snapshot that loaded nothing gives the rules nothing to do and nothing to
// trip on, and a read that named nothing reads nothing (no readers, no fleet);
// their keys are finished.
func TestReviewRulesOnAnEmptySnapshot(t *testing.T) {
	t.Parallel()
	partial, err := LoadPartial(ReadPlan{}, ReadAnswer{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{ruleAsk, ruleAccept, ruleRework} {
		plan := rvRule(t, name).Plan // the registered rule's
		for _, s := range []*Snapshot{{}, {Work: NewTable(Work)}, {Work: NewTable(Work), Readers: NewTable(Readers), Merge: NewTable(Merge), Fleet: NewTable(Fleet)}, partial} {
			rp := plan(s, rvKeys(name+":p", name+"@4"), rvNow())
			if !rvEmpty(rp) || len(rp.Done) != 2 {
				t.Errorf("%s: %+v", name, rp)
			}
		}
		// the read of keys that name nothing asks nothing, and the plan on it finishes them
		w := rvReview(t, 1, 0)
		if run := rvExec(w, nil, name, rvNow(), rvKeys(name+"@x", name+"@9+2000"), L1ReadBounds(), 0); len(run.Read.Sprint) != 0 || !rvEmpty(run.Plan) || len(run.Plan.Done) != 2 {
			t.Errorf("%s: keys that name nothing: %+v, %+v", name, run.Read.Sprint, run.Plan)
		}
	}
}

// Every rule run a second time on the same keys, with nothing changed since,
// writes nothing and raises nothing (E7).
func TestReviewRulesTwiceSecondEmpty(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 3, 2)
	// R8: s1-1 to s1-3 are asked; s1-4 and s1-5 failed and are not
	keys := rvKeys("ask@11")
	first := rvPlanKeys(w, ruleAsk, rvNow(), keys)
	if len(first.Plan.Units) != 3 {
		t.Fatalf("first ask: %d units", len(first.Plan.Units))
	}
	w.rvApply(first)
	if second := rvPlanKeys(w, ruleAsk, rvNow(), keys); !rvEmpty(second) || len(second.Done) != 1 {
		t.Errorf("second ask: %+v", second)
	}
	// R9: s1-1 is read ok twice; s1-2 has one broken read, and s1-3 nothing yet
	for _, rd := range []string{"reader-a", "reader-b", "reader-c"} {
		if rc := w.s.Readers.Card(ReadCardID("s1-1", 1, rd)); rc != nil {
			w.must(Read(w.s, ReadReq{As: rd, Verdict: "ok", Sel: Sel{IDs: []string{rc.ID}}}))
		}
	}
	for _, rc := range rvReads(w, "s1-2") {
		w.must(Read(w.s, ReadReq{As: rc.F("reader"), Verdict: "broken", Finding: "missing test", Sel: Sel{IDs: []string{rc.ID}}}))
	}
	keys = rvKeys("accept:s1-1")
	first = rvPlanKeys(w, ruleAccept, rvNow(), keys)
	if len(first.Plan.Units) != 1 {
		t.Fatalf("first accept: %d units", len(first.Plan.Units))
	}
	w.rvApply(first)
	w.clean("accept")
	if second := rvPlanKeys(w, ruleAccept, rvNow(), keys); !rvEmpty(second) || len(second.Done) != 1 {
		t.Errorf("second accept: %+v", second)
	}
	// R10: s1-2 (broken read) and s1-4 and s1-5 (failed work)
	keys = rvKeys("rework@12")
	first = rvPlanKeys(w, ruleRework, rvNow(), keys)
	if len(first.Plan.Units) != 3 {
		t.Fatalf("first rework: %d units", len(first.Plan.Units))
	}
	w.rvApply(first)
	w.clean("rework")
	if second := rvPlanKeys(w, ruleRework, rvNow(), keys); !rvEmpty(second) || len(second.Done) != 1 {
		t.Errorf("second rework: %+v", second)
	}
	// cannot ask, then asked: the line's key raises the judgment, askwait asks
	w = rvReview(t, 2, 0)
	w.s.Readers.SetRows([]string{"reader-a"})
	keys = rvKeys("ask@11")
	first = rvPlanKeys(w, ruleAsk, rvNow(), keys)
	if len(first.Plan.Units) != 0 || len(rvNotes(first, reviewOpen, NCannotAsk)) != 1 || len(first.Requeue) != 0 {
		t.Fatalf("cannot ask: %+v", first)
	}
	w.rvApply(first)
	if second := rvPlanKeys(w, ruleAsk, rvNow(), keys); !rvEmpty(second) || len(second.Done) != 1 {
		t.Errorf("second cannot ask: %+v", second)
	}
	// with no reader added, askwait finds the two primaries still held and asks none
	wait := rvKeys("askwait")
	if again := rvPlanKeys(w, ruleAsk, rvNow(), wait); !rvEmpty(again) || len(again.Done) != 1 {
		t.Errorf("askwait with no reader able: %+v", again)
	}
	// a reader is added: askwait asks both and closes the judgment; its chunk held both, so it is finished
	w.s.Readers.SetRows([]string{"reader-a", "reader-b", "reader-c"})
	first = rvPlanKeys(w, ruleAsk, rvNow(), wait)
	closing := rvNotes(first, reviewClose, NCannotAsk)
	if len(first.Plan.Units) != 2 || len(closing) != 1 || len(closing[0].Subjects) != 2 || len(first.Requeue) != 0 || len(first.Done) != 1 {
		t.Fatalf("askwait after a reader is added: %+v", first)
	}
	w.rvApply(first)
	if len(w.openOn("s1-1")) != 0 && w.openOn("s1-1")[0].Note.Type == NCannotAsk {
		t.Errorf("cannot ask stays open on an asked primary")
	}
	if second := rvPlanKeys(w, ruleAsk, rvNow(), wait); !rvEmpty(second) || len(second.Done) != 1 {
		t.Errorf("askwait a second time: %+v", second)
	}
	if second := rvPlanKeys(w, ruleAsk, rvNow(), keys); !rvEmpty(second) || len(second.Done) != 1 {
		t.Errorf("the line's key a second time: %+v", second)
	}
}

// The rules are registered, in the order of the tick's round robin: ask, accept
// and rework after level and before late (1.4.2).
func TestReviewRulesRegistered(t *testing.T) {
	t.Parallel()
	pos := map[string]int{}
	prio := map[string]int{}
	for i, r := range RuleTable() {
		pos[r.Name], prio[r.Name] = i, r.Priority
		if r.Read == nil || r.Plan == nil {
			t.Errorf("rule %s has no read or no plan", r.Name)
		}
	}
	for name, p := range map[string]int{ruleAsk: 8, ruleAccept: 9, ruleRework: 10} {
		if _, ok := pos[name]; !ok || prio[name] != p {
			t.Errorf("rule %s: priority %d (registered %v), want %d", name, prio[name], ok, p)
		}
	}
	if !(pos[ruleAsk] < pos[ruleAccept] && pos[ruleAccept] < pos[ruleRework]) {
		t.Errorf("order: ask %d, accept %d, rework %d", pos[ruleAsk], pos[ruleAccept], pos[ruleRework])
	}
}

func TestReviewKeyParse(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		key  string
		want reviewKey
		ok   bool
	}{
		{"ask:s1-4", reviewKey{kind: keyPrimary, subject: "s1-4"}, true},
		{"accept:p", reviewKey{kind: keyPrimary, subject: "p"}, true},
		{"rework@48213", reviewKey{kind: keyLine, line: 48213}, true},
		{"ask@48213+400", reviewKey{kind: keyLine, line: 48213, offset: 400}, true},
		{"askwait", reviewKey{kind: keyHead}, true},
		{"ask", reviewKey{}, false},
		{"ask:", reviewKey{}, false},
		{"ask@x", reviewKey{}, false},
		{"ask@4+y", reviewKey{}, false},
		{"ask@4+-1", reviewKey{}, false},
		{"", reviewKey{}, false},
	} {
		got, ok := parseReviewKey(AgendaKey{Key: c.key})
		if ok != c.ok || ok && got != c.want {
			t.Errorf("%q: %+v %v, want %+v %v", c.key, got, ok, c.want, c.ok)
		}
	}
}

// rvNamed is the ids the related queries of a read plan name: the ids they list,
// and the Limit of each line or head window.
func rvNamed(rp ReadPlan) int {
	n := 0
	for _, q := range rp.Sprint {
		if q.Kind != QueryRelated {
			continue
		}
		if q.Source.Kind == SourceIDs {
			n += len(q.Source.IDs)
		} else {
			n += q.Source.Limit
		}
	}
	return n
}

// rvRule is the registered rule of that name: the read and the plan the tick
// runs, never the table the registry was made from.
func rvRule(t *testing.T, name string) Rule {
	t.Helper()
	r, ok := rvRuleOf(name)
	if !ok {
		t.Fatalf("no rule %q is registered", name)
	}
	return r
}

// A read is sized by the declared cost of what it asks, against every bound of
// layer 1: keys are cut to fit and the rest are left, in order; a key that names
// a line or askwait is read from its offset up to what is left of the read; a
// halving halves the ids the read names, down to one.
func TestReviewReadSizesKeys(t *testing.T) {
	t.Parallel()
	r := rvRule(t, ruleAsk)
	b := L1ReadBounds()
	var keys []string
	for i := 0; i < 1000; i++ {
		keys = append(keys, fmt.Sprintf("ask:s1-%d", i))
	}
	rp, left := r.Read(rvKeys(keys...), b, 0)
	fit := rvNamed(rp)
	if fit < 100 || fit+len(left) != 1000 || !within(rp.Queries(), rp.Cost(), b) {
		t.Fatalf("read %d keys and left %d, costing %+v in %d queries", fit, len(left), rp.Cost(), rp.Queries())
	}
	if left[0].Key != fmt.Sprintf("ask:s1-%d", fit) {
		t.Errorf("the first key left is %s: keys are taken in order", left[0].Key)
	}
	if last := rp.Sprint[len(rp.Sprint)-1]; last.Kind != QueryReaders {
		t.Errorf("the readers are not read: %+v", rp.Sprint)
	}
	// one key more would not fit: the read took as many as the bounds hold
	more, _ := r.Read(rvKeys(keys[:fit+1]...), ReadBounds{}, 0)
	if within(more.Queries(), more.Cost(), b) {
		t.Errorf("%d keys fit the bounds, and the read took %d", fit+1, fit)
	}
	// each halving halves what is named, down to one key
	for h := 1; h <= 12; h++ {
		if rp, _ := r.Read(rvKeys(keys...), b, h); rvNamed(rp) != Halved(fit, h) {
			t.Errorf("halvings %d: %d keys, want %d", h, rvNamed(rp), Halved(fit, h))
		}
	}
	if rp, left := r.Read(rvKeys(keys...), b, 40); rvNamed(rp) != 1 || len(left) != 999 {
		t.Errorf("halvings 40: %d keys, %d left, want one", rvNamed(rp), len(left))
	}

	// a line is read from its offset, up to what is left of the read
	rp, left = r.Read(rvKeys("ask@9+500", "ask:s1-1", "askwait"), b, 0)
	if src := rp.Sprint[0].Source; len(left) != 2 || len(rp.Sprint) != 2 || src.Kind != SourceLine || src.Seq != 9 || src.Offset != 500 || src.Limit != fit {
		t.Errorf("a line that does not fit the read: %+v, left %v, want a window of %d ids from 500", rp.Sprint, left, fit)
	}
	// a line that ends before the read does leaves room for the keys behind it
	rp, left = r.Read(rvKeys("ask@9+1990", "ask:s1-1", "askwait"), b, 0)
	if len(left) != 0 || len(rp.Sprint) != 4 {
		t.Fatalf("a line of ten ids and keys behind it: %d queries, left %v", len(rp.Sprint), left)
	}
	if ids := rp.Sprint[0]; ids.Source.Kind != SourceIDs || len(ids.Source.IDs) != 1 || ids.Source.IDs[0] != "s1-1" {
		t.Errorf("the named primaries are one query, first: %+v", ids)
	}
	if line := rp.Sprint[1].Source; line.Kind != SourceLine || line.Offset != 1990 || line.Limit != 10 {
		t.Errorf("the line: %+v", line)
	}
	if head := rp.Sprint[2].Source; head.Kind != SourceHead || head.Key != reviewHeadAskwait || head.Limit != fit-1-10 {
		t.Errorf("askwait takes what is left of the read (%d): %+v", fit-1-10, head)
	}
	// askwait alone
	rp, left = r.Read(rvKeys("askwait", "ask:s1-1"), b, 0)
	if head := rp.Sprint[0].Source; len(left) != 1 || head.Kind != SourceHead || head.Limit != fit {
		t.Errorf("askwait: %+v, left %v", rp.Sprint, left)
	}
	// a line past the end of what a line holds, a key that names nothing: costless, taken
	for _, k := range []string{"ask@9+2000", "ask@9+2500", "ask@x", "ask", "ask:"} {
		if rp, left = r.Read(rvKeys(k), b, 0); len(rp.Sprint) != 0 || len(left) != 0 {
			t.Errorf("%q: %+v %v", k, rp.Sprint, left)
		}
	}
	if rp, left = r.Read(rvKeys("ask@x", "ask:s1-1"), b, 0); rvNamed(rp) != 1 || len(left) != 0 {
		t.Errorf("a key that names nothing costs nothing: %+v %v", rp.Sprint, left)
	}
	if rp, left = r.Read(nil, b, 0); len(rp.Sprint) != 0 || len(left) != 0 {
		t.Errorf("no keys: %+v %v", rp.Sprint, left)
	}
}

// Whatever the keys, the bounds and the halvings, a read fits the bounds, takes a
// prefix of the keys and at least the first, names a window of at least one id
// and no more than a line holds, and a halving never makes it larger.
func TestReviewReadsFitTheBoundsWhateverTheKeys(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(9, 21))
	for iter := 0; iter < 3000; iter++ {
		r := rvRule(t, []string{ruleAsk, ruleAccept, ruleRework}[rng.IntN(3)])
		var keys []AgendaKey
		var primaries []string
		for i, n := 0, 1+rng.IntN(40); i < n; i++ {
			var k string
			switch rng.IntN(5) {
			case 0:
				k = fmt.Sprintf("%s@%d", r.Name, 1+rng.IntN(500))
			case 1:
				k = fmt.Sprintf("%s@%d+%d", r.Name, 1+rng.IntN(500), rng.IntN(2400))
			case 2:
				k = r.Name + "@x"
				if r.Name == ruleAsk {
					k = "askwait"
				}
			default:
				k = fmt.Sprintf("%s:s1-%d", r.Name, i)
			}
			keys = append(keys, AgendaKey{Key: k, Seq: uint64(i + 1)})
		}
		b := ReadBounds{Queries: 4 + rng.IntN(1020), Records: 2000 + rng.IntN(9000), RangeIDs: 20000, Bytes: (1 + rng.IntN(8)) << 20}
		if rng.IntN(3) == 0 {
			b = L1ReadBounds()
		}
		h := rng.IntN(6)
		rp, left := r.Read(keys, b, h)
		if err := rp.Validate(); err != nil {
			t.Fatalf("%s: the read plan is refused: %v", r.Name, err)
		}
		taken := keys[:len(keys)-len(left)]
		if len(taken) == 0 || !slices.Equal(left, keys[len(taken):]) {
			t.Fatalf("%s: %d keys, %d taken", r.Name, len(keys), len(taken))
		}
		if len(rp.Sprint) > 0 && !within(rp.Queries(), rp.Cost(), b) {
			t.Fatalf("%s %v halvings %d: the read costs %+v in %d queries, over %+v", r.Name, keys, h, rp.Cost(), rp.Queries(), b)
		}
		for _, k := range taken {
			if rk, ok := parseReviewKey(k); ok && rk.kind == keyPrimary {
				primaries = append(primaries, rk.subject)
			}
		}
		var listed []string
		for _, q := range rp.Sprint {
			if q.Kind != QueryRelated {
				continue
			}
			switch src := q.Source; src.Kind {
			case SourceIDs:
				listed = append(listed, src.IDs...)
			case SourceLine:
				if src.Limit < 1 || src.Offset+src.Limit > MaxLineIDs {
					t.Fatalf("%s: a line window of %d from %d", r.Name, src.Limit, src.Offset)
				}
			case SourceHead:
				if src.Limit < 1 || src.Limit > AskwaitChunk {
					t.Fatalf("%s: askwait window of %d", r.Name, src.Limit)
				}
			}
		}
		if !slices.Equal(listed, primaries) {
			t.Fatalf("%s: the primaries read %v, the keys taken name %v", r.Name, listed, primaries)
		}
		if rp2, _ := r.Read(keys, b, h+1); rvNamed(rp2) > rvNamed(rp) {
			t.Fatalf("%s: a halving read %d ids after %d", r.Name, rvNamed(rp2), rvNamed(rp))
		}
	}
}

// The three reads project what their rules read and follow what the design says:
// ask follows the read cards and the judgments and asks for the readers (their
// counts, and no field of their control cards), accept follows the merge card and
// the control card too, rework follows the work card and asks for the fleet (its
// members' status).
func TestReviewReadsFollowTheDesign(t *testing.T) {
	t.Parallel()
	b := L1ReadBounds()
	for name, c := range map[string]struct {
		follow []string
		fixed  string
		fields []string // what the last query projects; not nil when empty: the summary
	}{
		ruleAsk:    {[]string{FollowRCards, FollowJOpen}, QueryReaders, []string{}},
		ruleAccept: {[]string{FollowRCards, FollowMerge, FollowControl, FollowJOpen}, "", nil},
		ruleRework: {[]string{FollowRCards, FollowWork}, QueryFleet, []string{"status", FieldWidth}},
	} {
		rp, _ := rvRule(t, name).Read(rvKeys(name+":p"), b, 0)
		if err := rp.Validate(); err != nil {
			t.Errorf("%s: the read plan is refused: %v", name, err)
		}
		q := rp.Sprint[0]
		if q.Kind != QueryRelated || q.Table != Work || q.Source.Kind != SourceIDs || strings.Join(q.Follow, ",") != strings.Join(c.follow, ",") || len(q.Fields) == 0 {
			t.Errorf("%s: %+v", name, rp.Sprint)
		}
		last := rp.Sprint[len(rp.Sprint)-1]
		switch {
		case c.fixed == "":
			if last.Kind != QueryRelated {
				t.Errorf("%s: its last query is %+v", name, last)
			}
		case last.Kind != c.fixed || (last.Fields == nil) != (c.fields == nil) || !slices.Equal(last.Fields, c.fields):
			t.Errorf("%s: its last query is %+v, want a %s query projecting %#v", name, last, c.fixed, c.fields)
		}
	}
}

// The twin gives a plan the fields its read named and no others, and the tables'
// rows, counts and cells only when the read asked for them: a plan that reads
// more is stopped where it reads (1.5.2).
func TestTheTwinLoadsOnlyWhatTheReadNamed(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 1, 0)
	run := rvExec(w, nil, ruleAsk, rvNow(), rvKeys("ask:s1-1"), L1ReadBounds(), 0)
	pr := run.S.Work.Card("s1-1")
	for _, f := range []string{"ci", "ci_head", "reader", "work", "bound"} {
		mustPanic(t, func() { pr.F(f) })
	}
	if pr.F("attempt") != "1" || pr.F("result") != "ok" {
		t.Errorf("the fields ask named are not there: %v", pr.Fields)
	}
	mustPanic(t, func() { run.S.Fleet.Rows() })
	mustPanic(t, func() { run.S.Work.Cell("s1", Review) })
	mustPanic(t, func() { run.S.Work.Column(Review) })
	mustPanic(t, func() { run.S.Merge.Of("s1-1") })
	if rows := run.S.Readers.Rows(); len(rows) != 3 {
		t.Errorf("the readers' rows: %v", rows)
	}
}

// R10 unsets the result and the readers of a primary it reworks (and its bound,
// which only the coordinator's rework meets: the machine leaves a bound alone),
// and its avoid when the attempt's work card names no member, on the snapshot of
// its read as on the whole one. A field the read did not name is refused when the plan asks
// for it, never taken for an absent one whose unset is dropped: the reworked
// primary would keep the readers of the attempt before it.
func TestReworkUnsetsWhatItUnsetsOnAPartialSnapshot(t *testing.T) {
	t.Parallel()
	check := func(name string, w *world, want []string) {
		t.Helper()
		req := ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Who: "coordinator"}
		whole := rvEntries(ReworkAt(w.s, req, rvNow()), Work, "s1-1")
		run := rvExec(w, nil, ruleRework, rvNow(), rvKeys("rework:s1-1"), L1ReadBounds(), 0)
		move := rvEntries(run.Plan.Plan, Work, "s1-1")
		if len(move) != 1 || len(whole) != 1 {
			t.Fatalf("%s: the primary's entries: %d on its read, %d on the whole snapshot", name, len(move), len(whole))
		}
		if !slices.Equal(move[0].Unset, want) || !slices.Equal(whole[0].Unset, want) {
			t.Errorf("%s: unset %v on the snapshot of its read and %v on the whole one, want %v", name, move[0].Unset, whole[0].Unset, want)
		}
		w.rvApply(run.Plan)
		pr := w.s.Work.Card("s1-1")
		for _, f := range want {
			if _, ok := pr.Fields[f]; ok {
				t.Errorf("%s: after the rework the primary still has %s = %q", name, f, pr.Fields[f])
			}
		}
	}

	// failed work, and the readers a return or an accept left on the primary
	w := rvReview(t, 0, 1)
	pr := w.s.Work.Card("s1-1")
	pr.Fields["readers"] = "reader-a,reader-b"
	check("failed work", w, []string{"result", "readers"})

	// a broken read, and a work card that is gone: no member to avoid, so the
	// avoid of an earlier attempt goes too
	w = rvReview(t, 1, 0)
	pr = w.s.Work.Card("s1-1")
	pr.Fields["readers"] = "reader-a,reader-b"
	pr.Fields["avoid"] = "m1"
	rvPutRead(w, "s1-1", 1, "reader-a", Broken).Fields["finding"] = "off by one"
	delete(w.s.Fleet.cards, pr.F("work"))
	w.do(Plan{})
	check("no work card", w, []string{"result", "readers", "avoid"})
}

// A plan asks whether a card has a field, to unset it, as it asks for the field's
// value: a field the read did not name is refused in a test build, and in a
// release build is recorded and unset nothing (Snapshot.Unloaded), never taken
// for one the card lacks. A field the read named and the card lacks, or has with
// no value, is told as it is.
func TestUnsetPresentIsAskedThroughTheGuard(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 1, 0)
	w.s.Work.Card("s1-1").Fields["readers"] = "reader-a,reader-b"
	w.s.Work.Card("s1-1").Fields["refused"] = "" // read, and no value
	r := rvRule(t, ruleAsk)                      // ask's read does not name readers
	rp, _ := r.Read(rvKeys("ask:s1-1"), L1ReadBounds(), 0)
	ans := rvTwin{s: w.s, fail: t.Fatalf}.answer(rp)

	s, err := LoadPartial(rp, ans)
	if err != nil {
		t.Fatal(err)
	}
	pr := s.Work.Card("s1-1")
	mustPanic(t, func() { pr.Has("readers") })
	mustPanic(t, func() { unsetPresent(pr, []string{"attempt", "readers"}) })
	if !pr.Has("attempt") || !pr.Has("refused") || pr.Has("asked") {
		t.Errorf("a field the read named: attempt %v, refused with no value %v, asked which the card lacks %v", pr.Has("attempt"), pr.Has("refused"), pr.Has("asked"))
	}

	s, err = loadPartial(rp, ans, false) // a release build
	if err != nil {
		t.Fatal(err)
	}
	pr = s.Work.Card("s1-1")
	if got := unsetPresent(pr, []string{"attempt", "readers", "refused", "asked"}); !slices.Equal(got, []string{"attempt", "refused"}) {
		t.Errorf("unset %v, want attempt and refused, which the read named and the card has", got)
	}
	if err := s.UnloadedErr(); err == nil || !strings.Contains(err.Error(), "s1-1 readers") {
		t.Errorf("the read of readers is not recorded: %v", err)
	}

	// a card built whole holds every field
	whole := &Card{ID: "x", Fields: map[string]string{"a": "", "b": "1"}}
	if !whole.Has("a") || !whole.Has("b") || whole.Has("c") || (*Card)(nil).Has("a") {
		t.Errorf("Has on a whole card: a %v, b %v, c %v", whole.Has("a"), whole.Has("b"), whole.Has("c"))
	}
	if got := unsetPresent(whole, []string{"c", "a", "b"}); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("unset %v, want the fields the card has, in the order asked", got)
	}
}

// R10 plans the next attempt's work card, whose id it derives from the attempt its
// read returns, so no read can name it beforehand: a card that exists already is
// refused by the coordinator's rework, which plans on the whole snapshot, and is
// not seen by the machine's, which creates it guarded absent (Expect.Absent) and
// leaves the refusal to the store. This is a limit of the design, open question 17:
// the check (IT26) must name such a card. The test fails, and the question goes,
// when a read loads the id.
func TestReworkSeesAStrayNextWorkCardOnlyOnAWholeSnapshot(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 0, 1)
	stray := WorkCardID("s1-1", 2)
	w.s.Fleet.Put(&Card{ID: stray, Row: "m1", Col: Ready, Rev: 1, Fields: map[string]string{"kind": "work", "primary": "s1-1", "attempt": "2", "member": "m1"}})
	w.do(Plan{})

	p := ReworkAt(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Who: "coordinator"}, rvNow())
	if len(p.Units) != 0 || len(p.Refused) != 1 || !strings.Contains(p.Refused[0].Why, "work card "+stray+" exists already") {
		t.Errorf("the coordinator's rework: units %d, refused %+v", len(p.Units), p.Refused)
	}
	rp := rvPlan(w, ruleRework, "rework:s1-1")
	creates := rvCreates(rp.Plan, Fleet)
	if len(rp.Plan.Refused) != 0 || len(creates) != 1 || creates[0].ID != stray || creates[0].Expect == nil || !creates[0].Expect.Absent {
		t.Errorf("the machine's rework: refused %+v, creates %+v, want %s created guarded absent", rp.Plan.Refused, creates, stray)
	}
}

// A read stops at the bound on queries as at the others: every line key it takes
// costs a query, and the read leaves the keys that would make it more than the
// bound allows (the one key it must take aside), whatever the rule and the
// bound, with room left in records, ids and bytes.
func TestReviewReadStopsAtTheQueriesBound(t *testing.T) {
	t.Parallel()
	const lines = 12
	for _, name := range []string{ruleAsk, ruleAccept, ruleRework} {
		r := rvRule(t, name)
		// the queries of the rule's that no key costs: the readers or the fleet
		one, _ := r.Read(rvKeys(name+"@1+1990"), L1ReadBounds(), 0)
		fixed := 0
		for _, q := range one.Sprint {
			if q.Kind != QueryRelated {
				fixed++
			}
		}
		var line, both []AgendaKey
		for i := 1; i <= lines; i++ {
			line = append(line, AgendaKey{Key: fmt.Sprintf("%s@%d+1990", name, i), Seq: uint64(i)})
		}
		both = append([]AgendaKey{{Key: name + ":s1-1", Seq: 99}}, line...)
		for q := 4; q <= 12; q++ {
			b := L1ReadBounds()
			b.Queries = q
			// lines alone: a source costs a query each
			rp, left := r.Read(line, b, 0)
			taken := lines - len(left)
			if want := max(1, q-fixed-1); taken != want || rp.Queries() > q {
				t.Errorf("%s, %d queries: %d lines taken in %d queries, want %d", name, q, taken, rp.Queries(), want)
			}
			if !slices.Equal(left, line[taken:]) {
				t.Errorf("%s, %d queries: the keys left are not those after the %d taken: %v", name, q, taken, left)
			}
			// a primary first adds the query of its ids, which the lines leave room for
			rp, left = r.Read(both, b, 0)
			taken = len(both) - len(left) - 1
			if want := q - fixed - 1; taken != want || rp.Queries() != q {
				t.Errorf("%s, %d queries: with a primary first, %d lines taken in %d queries, want %d in %d", name, q, taken, rp.Queries(), want, q)
			}
		}
		// with the queries of layer 1, every line fits
		if rp, left := r.Read(line, L1ReadBounds(), 0); len(left) != 0 || len(rp.Sprint) != lines+fixed {
			t.Errorf("%s: %d lines of ten ids at layer 1's bounds: %d queries, %d keys left", name, lines, len(rp.Sprint), len(left))
		}
	}
}

// A line of 2,000 cards is read in the reads it takes, each within layer 1's
// bounds, and each rule puts its key back with the offset its read got to (E6):
// the old key finished and the new one queued with its order kept, offsets
// strictly growing, every card of the line planned once, and nothing after the
// end of the line.
func TestReviewALineOf2000CardsIsReadInReadsThatFit(t *testing.T) {
	t.Parallel()
	const n = 2000
	for _, kind := range []string{ruleAsk, ruleAccept, ruleRework} {
		w := rvBulkWorld(t, n, kind, 8, 0)
		b := L1ReadBounds()
		key := AgendaKey{Key: kind + "@100", Seq: 100}
		keys := []AgendaKey{key}
		var offsets []int
		width := 0
		planned := 0
		for reads := 1; len(keys) > 0; reads++ {
			if reads > 12 {
				t.Fatalf("%s: the line is not finished after %d reads: %v", kind, reads-1, keys)
			}
			run := rvExec(w, nil, kind, rvNow(), keys, b, 0)
			if len(run.Left) != 0 || len(run.Read.Sprint) < 1 {
				t.Fatalf("%s: read %d left %v", kind, reads, run.Left)
			}
			src := run.Read.Sprint[0].Source
			if src.Kind != SourceLine || src.Seq != 100 || src.Limit < 1 {
				t.Fatalf("%s: the read of the line: %+v", kind, src)
			}
			if width == 0 {
				width = src.Limit
			}
			offsets = append(offsets, src.Offset)
			rp := run.Plan
			if len(rp.Done) != 1 || rp.Done[0] != keys[0] {
				t.Fatalf("%s: read %d finished %v, want the key it read, %v", kind, reads, rp.Done, keys[0])
			}
			planned += len(rp.Plan.Units)
			if src.Offset+src.Limit >= n {
				if len(rp.Requeue) != 0 {
					t.Fatalf("%s: the last read puts %v back", kind, rp.Requeue)
				}
			} else if want := (AgendaKey{Key: fmt.Sprintf("%s@100+%d", kind, src.Offset+src.Limit), Seq: 100}); len(rp.Requeue) != 1 || rp.Requeue[0] != want {
				t.Fatalf("%s: read %d puts back %v, want %v: the offset it got to, the order kept", kind, reads, rp.Requeue, want)
			}
			w.rvApply(rp)
			keys = rp.Requeue
		}
		if width >= MaxLineIDs || len(offsets) != (n+width-1)/width {
			t.Errorf("%s: %d reads of %d ids for a line of %d", kind, len(offsets), width, n)
		}
		for i, o := range offsets {
			if o != i*width {
				t.Errorf("%s: read %d starts at %d, want %d", kind, i, o, i*width)
			}
		}
		if planned != n {
			t.Errorf("%s: %d primaries planned in all, want each of the %d once", kind, planned, n)
		}
		for i := 1; i <= n; i++ {
			id := fmt.Sprintf("s1-%d", i)
			c := w.s.Work.Card(id)
			switch kind {
			case ruleAsk:
				if got := len(Split(c.F("rcards"))); got != AskReaders || c.Col != Review {
					t.Fatalf("%s: %s has %d read cards in %s", kind, id, got, c.Col)
				}
			case ruleAccept:
				if c.Col != Merging || w.s.Merge.Placed(id) == nil {
					t.Fatalf("%s: %s is %s", kind, id, c.Col)
				}
			case ruleRework:
				if c.Col == Review || c.Col == Working && c.Int("attempt") != 2 || c.Col == Ready && c.F("avoid") == "" {
					t.Fatalf("%s: %s is %s at attempt %d avoiding %q", kind, id, c.Col, c.Int("attempt"), c.F("avoid"))
				}
			}
		}
		if kind == ruleRework {
			// the members' widths hold across the reads (width.go, errata 3
			// amendment 9): the rest, if any, go back to ready for R6
			working := 0
			for i := 1; i <= n; i++ {
				if w.s.Work.Card(fmt.Sprintf("s1-%d", i)).Col == Working {
					working++
				}
			}
			if want := min(n, MaxMembers*DefaultWidth); working != want {
				t.Errorf("%s: %d primaries dealt at once, want %d: every member filled to its width and no further", kind, working, want)
			}
		}
		if kind == ruleAsk {
			for _, rd := range w.s.Readers.Rows() {
				if got := w.s.Readers.Count(rd, Asked); got != n*AskReaders/8 {
					t.Errorf("reader %s was asked %d reads, want %d: the queues stay level across the reads", rd, got, n*AskReaders/8)
				}
			}
		}
	}
}

// A key delivered twice (E7) plans the same line again: the second run of a cut
// line writes and raises nothing, and puts back the same offset.
func TestReviewACutLineRunTwiceWritesNothing(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{ruleAsk, ruleAccept, ruleRework} {
		w := rvBulkWorld(t, 2000, kind, 8, 0)
		keys := []AgendaKey{{Key: kind + "@100", Seq: 100}}
		first := rvExec(w, nil, kind, rvNow(), keys, L1ReadBounds(), 0).Plan
		if len(first.Plan.Units) == 0 || len(first.Requeue) != 1 {
			t.Fatalf("%s: first run: %d units, put back %v", kind, len(first.Plan.Units), first.Requeue)
		}
		w.rvApply(first)
		second := rvExec(w, nil, kind, rvNow(), keys, L1ReadBounds(), 0).Plan
		if len(second.Plan.Units) != 0 || len(second.Plan.Refused) != 0 || len(second.Notes) != 0 || len(second.Guards) != 0 {
			t.Errorf("%s: the second run of the cut line writes: %d units, %d notes", kind, len(second.Plan.Units), len(second.Notes))
		}
		if len(second.Done) != 1 || second.Done[0] != keys[0] || !slices.Equal(second.Requeue, first.Requeue) {
			t.Errorf("%s: the second run finishes %v and puts back %v, want %v and %v", kind, second.Done, second.Requeue, keys[0], first.Requeue)
		}
	}
}

// After a BUDGET or a LIMIT a line is read at half its window, and the offset it
// puts back is where the smaller read got to; at limits of one it reads one id.
func TestReviewALineIsReadAtHalfItsWindowAfterAHalving(t *testing.T) {
	t.Parallel()
	w := rvBulkWorld(t, 2000, ruleAsk, 8, 0)
	keys := rvKeys("ask@100")
	whole := rvExec(w, nil, ruleAsk, rvNow(), keys, L1ReadBounds(), 0)
	width := whole.Read.Sprint[0].Source.Limit
	for _, h := range []int{1, 2, 5, 30} {
		run := rvExec(w, nil, ruleAsk, rvNow(), keys, L1ReadBounds(), h)
		got := run.Read.Sprint[0].Source.Limit
		if want := Halved(width, h); got != want {
			t.Fatalf("halvings %d: a window of %d, want %d", h, got, want)
		}
		if want := (AgendaKey{Key: fmt.Sprintf("ask@100+%d", got), Seq: 1}); len(run.Plan.Requeue) != 1 || run.Plan.Requeue[0] != want || len(run.Plan.Plan.Units) != got {
			t.Errorf("halvings %d: put back %v with %d units, want %v and %d", h, run.Plan.Requeue, len(run.Plan.Plan.Units), want, got)
		}
	}
}

// askwait is put back while its chunk was full and a reader was able (2.3 R8):
// not when the chunk held every primary, and not when the pass asked none.
func TestReviewAskwaitIsPutBackWhileItsChunkWasFull(t *testing.T) {
	t.Parallel()
	const n = 600
	w := rvBulkWorld(t, n, ruleAsk, 1, 0) // one reader: no primary can be asked
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("s1-%d", i)
		note := Note{ID: "c" + id, Kind: Judgment, Type: NCannotAsk, Primaries: []string{id}, Count: 1}
		w.s.Open = append(w.s.Open, Open{Key: OpenKey(note.ID, id), Note: note})
	}
	keys := rvKeys("askwait")
	run := rvExec(w, nil, ruleAsk, rvNow(), keys, L1ReadBounds(), 0)
	width := run.Read.Sprint[0].Source.Limit
	if width >= n {
		t.Fatalf("the chunk is %d of %d askwait primaries: the read must cut it for this test", width, n)
	}
	rp := run.Plan
	if len(rp.Plan.Units) != 0 || len(rp.Notes) != 0 || len(rp.Requeue) != 0 || len(rp.Done) != 1 {
		t.Fatalf("a full chunk and no reader able: %d units, notes %+v, put back %v, done %v", len(rp.Plan.Units), rp.Notes, rp.Requeue, rp.Done)
	}
	// two readers are added: the chunk is asked, and the key is put back for the rest
	w.s.Readers.SetRows([]string{"r1", "r2", "r3"})
	rp = rvExec(w, nil, ruleAsk, rvNow(), keys, L1ReadBounds(), 0).Plan
	if len(rp.Plan.Units) != width || len(rp.Requeue) != 1 || rp.Requeue[0] != keys[0] || len(rp.Done) != 0 {
		t.Fatalf("a full chunk asked: %d units, put back %v, done %v", len(rp.Plan.Units), rp.Requeue, rp.Done)
	}
	if closing := rvNotes(rp, reviewClose, NCannotAsk); len(closing) != 1 || len(closing[0].Subjects) != width {
		t.Errorf("the judgments of the asked primaries are closed: %+v", closing)
	}
	w.rvApply(rp)
	// the rest fit the chunk: asked, and the key is finished
	rp = rvExec(w, nil, ruleAsk, rvNow(), keys, L1ReadBounds(), 0).Plan
	if len(rp.Plan.Units) != n-width || len(rp.Requeue) != 0 || len(rp.Done) != 1 {
		t.Fatalf("the rest of askwait: %d units, put back %v, done %v", len(rp.Plan.Units), rp.Requeue, rp.Done)
	}
	w.rvApply(rp)
	if rp = rvExec(w, nil, ruleAsk, rvNow(), keys, L1ReadBounds(), 0).Plan; !rvEmpty(rp) || len(rp.Done) != 1 {
		t.Errorf("askwait with nothing in it: %+v", rp)
	}
}

// The coordinator's rework answers "cannot ask": its decisions list rework --fix
// (2.2), so the rework closes the judgment and --answers naming it is accepted;
// today's Rework closes what it always did and refuses such an answer.
func TestReworkAtClosesCannotAsk(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 1, 0)
	w.s.Readers.SetRows([]string{"reader-a"})
	w.rvApply(rvPlan(w, ruleAsk, "ask:s1-1"))
	open := w.openOn("s1-1")
	if len(open) != 1 || open[0].Note.Type != NCannotAsk {
		t.Fatalf("cannot ask is not open on s1-1: %+v", open)
	}
	id := open[0].Note.ID
	req := ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "add a test for the empty case", Who: "coordinator"}
	// without --answers, the rework closes it with the step
	p := ReworkAt(w.s, req, rvNow())
	if len(p.Refused) != 0 || len(p.Units) != 1 || len(p.Units[0].Closes) != 1 || p.Units[0].Closes[0].Note.Type != NCannotAsk {
		t.Fatalf("closes %+v, refused %+v", p.Units, p.Refused)
	}
	// with --answers naming it, the step is accepted and closes it
	req.Answers = []string{id}
	p = ReworkAt(w.s, req, rvNow())
	if len(p.Refused) != 0 || len(p.Units) != 1 {
		t.Fatalf("--answers %s refused: %+v", id, p.Refused)
	}
	w.must(p)
	if len(w.openOn("s1-1")) != 0 || w.state("s1-1") == Review {
		t.Errorf("after the rework: %s, open %+v", w.state("s1-1"), w.openOn("s1-1"))
	}
	// today's Rework closes what it always did: cannot ask is not among them
	w2 := rvReview(t, 1, 0)
	w2.s.Readers.SetRows([]string{"reader-a"})
	w2.rvApply(rvPlan(w2, ruleAsk, "ask:s1-1"))
	old := Rework(w2.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "x", Who: "coordinator", Answers: []string{w2.openOn("s1-1")[0].Note.ID}})
	if len(old.Refused) != 1 || !strings.Contains(old.Refused[0].Why, "resolves no obligation") {
		t.Errorf("today's Rework: %+v", old.Refused)
	}
}

// Every key ingest makes for a review rule is served by a registered rule that
// takes it: askwait, which is a rule's word of its own, reaches ask, and a key
// put back with an offset reaches the rule it came from.
func TestEveryKeyOfTheReviewRulesReachesARegisteredRule(t *testing.T) {
	t.Parallel()
	ok := map[string]string{"result": "ok", "head": "h"}
	failed := map[string]string{"result": "failed", "head": "h"}
	lines := map[string]Line{
		"a card into review ok":        workLine("p1", "s1:working", "s1:review", ok),
		"a set into review ok":         setOf(Work, "s1:working", "s1:review", ok, "p1", "p2"),
		"a card into review failed":    workLine("p1", "s1:working", "s1:review", failed),
		"a set into review failed":     setOf(Work, "s1:working", "s1:review", failed, "p1", "p2"),
		"a read card into ok":          {Kind: LineMove, Table: Readers, Card: "p1.r1.a", Primary: "p1", From: "a:reading", To: "a:ok"},
		"read cards into ok":           setOf(Readers, "a:reading", "a:ok", nil, "p1.r1.a", "p2.r1.a"),
		"a read card into broken":      {Kind: LineMove, Table: Readers, Card: "p1.r1.a", Primary: "p1", From: "a:reading", To: "a:broken"},
		"read cards into broken":       setOf(Readers, "a:reading", "a:broken", nil, "p1.r1.a", "p2.r1.a"),
		"a primary's CI fields set":    workLine("p1", "s1:merging", "s1:merging", map[string]string{"ci": "green", "ci_head": "h"}),
		"the machine started":          NoteLine(Note{ID: "n1", Kind: Happened, Type: NMachineStarted}, "op"),
		"a card into review, no field": workLine("p1", "s1:working", "s1:review", nil),
	}
	table := map[string]Rule{}
	for _, r := range RuleTable() {
		table[r.Name] = r
	}
	seen := map[string]bool{}
	for name, line := range lines {
		for _, key := range ingestAt(t, line) {
			word := RuleOf(key)
			if word != ruleAsk && word != ruleAccept && word != ruleRework && word != ruleAskwait {
				continue
			}
			shape := word
			if rest := key[len(word):]; rest != "" {
				shape += rest[:1]
			}
			seen[shape] = true
			serving := ServingRule(key)
			r, registered := table[serving]
			if !registered {
				t.Errorf("%s: the key %q is served by %q, which no file registers", name, key, serving)
				continue
			}
			rp, left := r.Read(rvKeys(key), L1ReadBounds(), 0)
			if len(rp.Sprint) == 0 || len(left) != 0 {
				t.Errorf("%s: rule %s does not read the key %q: %+v, left %v", name, serving, key, rp.Sprint, left)
			}
		}
	}
	for _, shape := range []string{"ask:", "ask@", "accept:", "accept@", "rework:", "rework@", "askwait"} {
		if !seen[shape] {
			t.Errorf("no line the test makes queues a key of the shape %q: %v", shape, seen)
		}
	}
	if got := ServingRule("askwait"); got != ruleAsk {
		t.Errorf("askwait is served by %q, want ask", got)
	}
}

// A key put back with an offset is one the rule reads back, and is the rule's
// own: its word, its line, its offset.
func TestReviewLineKeysRoundTrip(t *testing.T) {
	t.Parallel()
	for _, rule := range []string{ruleAsk, ruleAccept, ruleRework} {
		for _, line := range []uint64{1, 48213, 1 << 40} {
			for _, offset := range []int{0, 1, 528, 1999} {
				key := lineKey(rule, line, offset)
				rk, ok := parseReviewKey(AgendaKey{Key: key})
				if !ok || rk.kind != keyLine || rk.line != line || rk.offset != offset || RuleOf(key) != rule || ServingRule(key) != rule {
					t.Errorf("%s: %+v %v", key, rk, ok)
				}
			}
		}
	}
	if got := lineKey(ruleAsk, 48213, 0); got != "ask@48213" {
		t.Errorf("a line from its first id: %q", got)
	}
	if got := lineKey(ruleAsk, 48213, 2000); got != "ask@48213+2000" {
		t.Errorf("the design's own example: %q", got)
	}
}

// The probes of the cold read that no test caught, each now pinned (the reversed
// witness of each is in the pull request).

// R9 retires every outstanding read of the attempt: one still asked, and one a
// reader has begun and not reported.
func TestAcceptRetiresAReadingReadAndAnAskedOne(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 1, 0)
	rvAllOK(w, "s1-1")
	reading := rvPutRead(w, "s1-1", 1, "reader-c", Reading)
	rp := rvPlan(w, ruleAccept, "accept:s1-1")
	if len(rp.Plan.Units) != 1 {
		t.Fatalf("units %+v refused %+v", rp.Plan.Units, rp.Plan.Refused)
	}
	if es := rvEntries(rp.Plan, Readers, reading.ID); len(es) != 1 || !es[0].Remove || es[0].Set["retired_by"] != "accept" {
		t.Errorf("the read a reader has begun is not retired: %+v", es)
	}
	w.rvApply(rp)
	w.clean("accept with a read reading")
	if w.s.Readers.Placed(reading.ID) != nil {
		t.Errorf("the read stands after the accept")
	}
	// one still asked, beside the two ok
	w = rvReview(t, 1, 0)
	rvAllOK(w, "s1-1")
	asked := rvPutRead(w, "s1-1", 1, "reader-c", Asked)
	rp = rvPlan(w, ruleAccept, "accept:s1-1")
	if es := rvEntries(rp.Plan, Readers, asked.ID); len(es) != 1 || !es[0].Remove {
		t.Errorf("the read still asked is not retired: %+v", es)
	}
}

// R9 accepts a primary that was returned: its merge card is at returned, and
// moves to queued at the primary's score, its own now (a create would be refused
// as existing); any other place of the merge card is refused by name.
func TestAcceptMovesAReturnedMergeCardToQueuedAtTheScoreOfItsPrimary(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 2, 0)
	rvAllOK(w, "s1-1", "s1-2")
	w.rvApply(rvPlan(w, ruleAccept, "accept:s1-1"))
	w.must(Return(w.s, ReturnReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "the batch went red", Who: "coordinator"}))
	if m := w.s.Merge.Placed("s1-1"); m == nil || m.Col != Returned {
		t.Fatalf("the merge card after the return: %+v", m)
	}
	// "returned to review" is open until the coordinator decides; he decides
	if rp := rvPlan(w, ruleAccept, "accept:s1-1"); len(rp.Plan.Units) != 0 || len(rp.Done) != 1 {
		t.Fatalf("a returned primary is accepted while its judgment stands: %+v", rp.Plan.Units)
	}
	w.s.Open = nil
	// the primary is re-ranked after the return, so its score is not the merge card's
	pr := w.s.Work.Card("s1-1")
	pr.Score = pr.Score + 0.5
	pr.Rev++
	w.s.Merge.Card("s1-1").Score = pr.Score - 0.25
	rp := rvPlan(w, ruleAccept, "accept:s1-1")
	if len(rp.Plan.Units) != 1 || len(rp.Plan.Refused) != 0 {
		t.Fatalf("units %d, refused %+v: a merge card at returned is moved", len(rp.Plan.Units), rp.Plan.Refused)
	}
	es := rvEntries(rp.Plan, Merge, "s1-1")
	if len(es) != 1 || es[0].Create != nil || es[0].Move == nil || es[0].Move.Col != Queued || es[0].Move.Score == nil || *es[0].Move.Score != pr.Score {
		t.Errorf("the merge card is not moved to queued at the primary's score %v: %+v", pr.Score, es)
	}
	// a merge card anywhere but absent or returned is a refusal, by name
	w.s.Merge.Card("s1-1").Col = Stuck
	if rp = rvPlan(w, ruleAccept, "accept:s1-1"); len(rp.Plan.Refused) != 1 || !strings.Contains(rp.Plan.Refused[0].Why, "merge record is s1:stuck") {
		t.Errorf("a merge card at stuck: %+v", rp.Plan.Refused)
	}
	// a merge record kept off the table says why
	w.s.Merge.Card("s1-1").Row, w.s.Merge.Card("s1-1").Col = "", ""
	w.s.Merge.Card("s1-1").Fields["outcome"] = "merged"
	w.s.Merge.Put(w.s.Merge.Card("s1-1"))
	if rp = rvPlan(w, ruleAccept, "accept:s1-1"); len(rp.Plan.Refused) != 1 || !strings.Contains(rp.Plan.Refused[0].Why, "kept, merged") {
		t.Errorf("a merge record kept off the table: %+v", rp.Plan.Refused)
	}
}

// A primary has at most 15 read cards: ask fills the last two, and refuses the
// one that would pass them.
func TestAskAcceptsExactlyFifteenReadCards(t *testing.T) {
	t.Parallel()
	old := func(n int) string {
		var ids []string
		for i := 0; i < n; i++ {
			ids = append(ids, fmt.Sprintf("old%d", i))
		}
		return strings.Join(ids, ",")
	}
	w := rvReview(t, 2, 0)
	w.s.Work.Card("s1-1").Fields["rcards"] = old(MaxRCards - AskReaders) // 13 and two more are 15
	w.s.Work.Card("s1-2").Fields["rcards"] = old(MaxRCards - AskReaders + 1)
	rp := rvPlan(w, ruleAsk, "ask@1")
	if len(rp.Plan.Refused) != 1 || rp.Plan.Refused[0].Key != "s1-2" {
		t.Fatalf("refused %+v: only the primary that would have 16 is", rp.Plan.Refused)
	}
	set := rvEntries(rp.Plan, Work, "s1-1")
	if len(set) != 1 || len(Split(set[0].Set["rcards"])) != MaxRCards || len(rvCreates(rp.Plan, Readers)) != AskReaders {
		t.Errorf("s1-1 at exactly 15: %+v", set)
	}
	w.rvApply(rp)
	if got := len(Split(w.s.Work.Card("s1-1").F("rcards"))); got != MaxRCards {
		t.Errorf("s1-1 has %d read cards", got)
	}
}

// A primary the machine refused is left as it is until ack clears it: run a
// second time, and after the refusal was applied, ask writes and raises nothing.
func TestAskAfterARefusalIsQuiet(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 2, 0)
	var many []string
	for i := 0; i < MaxRCards-1; i++ {
		many = append(many, fmt.Sprintf("old%d", i))
	}
	w.s.Work.Card("s1-1").Fields["rcards"] = strings.Join(many, ",")
	first := rvPlan(w, ruleAsk, "ask@1")
	if len(first.Plan.Refused) != 1 || len(first.Plan.Units) != 2 {
		t.Fatalf("first run: refused %+v, %d units (the refusal and s1-2's ask)", first.Plan.Refused, len(first.Plan.Units))
	}
	w.rvApply(first)
	if !strings.HasPrefix(w.s.Work.Card("s1-1").F("refused"), "ask: ") {
		t.Fatalf("s1-1 is not marked: %v", w.s.Work.Card("s1-1").Fields)
	}
	if second := rvPlan(w, ruleAsk, "ask@1"); !rvEmpty(second) || len(second.Done) != 1 {
		t.Errorf("second run after the refusal: %+v", second)
	}
	if AskDue(w.s, w.s.Work.Card("s1-1")) {
		t.Errorf("a refused primary is due")
	}
	// and it is out of every rule's conditions
	w.s.Work.Card("s1-1").Fields["result"] = "failed"
	if ReworkDue(w.s, w.s.Work.Card("s1-1")) || AcceptDue(w.s, w.s.Work.Card("s1-1")) {
		t.Errorf("a refused primary is due for another rule")
	}
}

// The machine leaves a primary it refused alone, whatever its evidence: a failed
// work card or a broken read of a refused primary is not reworked until ack.
func TestReworkAfterARefusalIsQuiet(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 0, 2)
	w.s.Work.Card("s1-1").Fields["refused"] = "accept: its merge record is s1:queued"
	rp := rvPlan(w, ruleRework, "rework@1")
	if len(rp.Plan.Units) != 1 || rp.Plan.Units[0].Key != "s1-2" {
		t.Fatalf("units %+v: s1-2 is reworked and s1-1, refused, is not", rp.Plan.Units)
	}
	w.rvApply(rp)
	w.clean("rework with a refused primary")
	if second := rvPlan(w, ruleRework, "rework@1"); !rvEmpty(second) || len(second.Done) != 1 {
		t.Errorf("second run: %+v", second)
	}
	if ReworkDue(w.s, w.s.Work.Card("s1-1")) {
		t.Errorf("a refused primary with failed work is due")
	}
}

// A judgment the coordinator acknowledged holds its condition as an open one
// does: an acknowledged "returned to review" keeps R9 from accepting, and an
// acknowledged "cannot ask" is not raised again.
func TestAnAcknowledgedHoldHoldsAsAnOpenOneDoes(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 1, 0)
	rvAllOK(w, "s1-1")
	returned := Note{ID: "n900", Kind: Judgment, Type: NReturned, Stream: "s1", Primaries: []string{"s1-1"}, Count: 1}
	w.s.Acked = []Open{{Key: OpenKey(returned.ID, "s1-1"), Note: returned}}
	if rp := rvPlan(w, ruleAccept, "accept:s1-1"); len(rp.Plan.Units) != 0 || len(rp.Done) != 1 {
		t.Errorf("accepted with \"returned to review\" acknowledged: %+v", rp.Plan.Units)
	}
	if AcceptDue(w.s, w.s.Work.Card("s1-1")) {
		t.Errorf("AcceptDue ignores the acknowledgement")
	}
	w.s.Acked = nil
	if rp := rvPlan(w, ruleAccept, "accept:s1-1"); len(rp.Plan.Units) != 1 {
		t.Errorf("not accepted once nothing holds it: %+v", rp.Plan.Refused)
	}

	// one reader, and "cannot ask" acknowledged: not raised again
	w = rvReview(t, 1, 0)
	w.s.Readers.SetRows([]string{"reader-a"})
	cannot := Note{ID: "n901", Kind: Judgment, Type: NCannotAsk, Stream: "s1", Primaries: []string{"s1-1"}, Count: 1}
	w.s.Acked = []Open{{Key: OpenKey(cannot.ID, "s1-1"), Note: cannot}}
	rp := rvPlan(w, ruleAsk, "ask:s1-1")
	if len(rp.Notes) != 0 || len(rp.Plan.Units) != 0 {
		t.Errorf("cannot ask raised again over an acknowledgement: %+v", rp.Notes)
	}
	w.s.Acked = nil
	if rp = rvPlan(w, ruleAsk, "ask:s1-1"); len(rvNotes(rp, reviewOpen, NCannotAsk)) != 1 {
		t.Errorf("cannot ask not raised with nothing held: %+v", rp.Notes)
	}
}

// The coordinator's rework of a primary it refuses (no fix given, none to take)
// leaves it in review with the judgment its state needs: a failed work with no
// report and nothing open is "stranded in review".
func TestReworkAtWritesTheJudgmentOfAPrimaryItRefuses(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 0, 1)
	w.s.Open = nil
	delete(w.s.Fleet.Card(w.s.Work.Card("s1-1").F("work")).Fields, "report")
	p := ReworkAt(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Who: "coordinator"}, rvNow())
	if len(p.Units) != 0 || len(p.Refused) != 1 || !strings.Contains(p.Refused[0].Why, "--fix") {
		t.Fatalf("units %d, refused %+v", len(p.Units), p.Refused)
	}
	if len(p.Notes) != 1 || p.Notes[0].Type != NStranded || !contains(p.Notes[0].Primaries, "s1-1") {
		t.Errorf("the judgment the refused primary needs: %+v", p.Notes)
	}
	// a rework refused for another reason writes it as well
	w = rvReview(t, 0, 1)
	w.s.Open = nil
	w.s.Fleet.Put(&Card{ID: "s1-1.w2", Row: "m1", Col: Ready, Rev: 1, Fields: map[string]string{"kind": "work", "primary": "s1-1", "member": "m1"}})
	p = ReworkAt(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "x", Who: "coordinator"}, rvNow())
	if len(p.Refused) != 1 || !strings.Contains(p.Refused[0].Why, "exists already") || len(p.Notes) != 1 || p.Notes[0].Type != NStranded {
		t.Errorf("a rework refused as existing: %+v %+v", p.Refused, p.Notes)
	}
}

// A primary's read cards are the ones its rcards lists (1.3.1: every read card
// ever made for it), placed or retired, at its attempt; a card the readers table
// holds that rcards does not list is not one of them.
func TestReviewReadsAreThoseRcardsLists(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 1, 0)
	listed := rvPutRead(w, "s1-1", 1, "reader-a", Asked)
	retired := rvPutRead(w, "s1-1", 1, "reader-b", "")
	otherAttempt := rvPutRead(w, "s1-1", 2, "reader-c", Asked)
	pr := w.s.Work.Card("s1-1")
	stray := &Card{ID: ReadCardID("s1-1", 1, "reader-c"), Row: "reader-c", Col: Asked, Score: pr.Score, Rev: 1, Fields: map[string]string{
		"kind": "read", "primary": "s1-1", "stream": "s1", "reader": "reader-c", "attempt": "1", "head": pr.F("head")}}
	w.s.Readers.Put(stray)
	var got []string
	for _, rc := range rvReads(w, "s1-1") {
		got = append(got, rc.ID)
	}
	if strings.Join(got, ",") != listed.ID+","+retired.ID {
		t.Errorf("the reads of s1-1 at attempt 1: %v, want %s (asked) and %s (retired), not %s (attempt 2) or %s (not listed)", got, listed.ID, retired.ID, otherAttempt.ID, stray.ID)
	}
	if AskDue(w.s, pr) {
		t.Errorf("a primary with a read standing is asked again")
	}
	// only a card that no rcards lists: not seen, and so not one of its reads
	pr.Fields["rcards"] = ""
	if n := len(rvReads(w, "s1-1")); n != 0 {
		t.Errorf("%d reads of a primary whose rcards lists none", n)
	}
}

// A primary with reads and no named readers keeps the readers of its reads for the
// attempt that follows, in the order its rcards lists them.
func TestReworkKeepsTheReadersOfItsReadsWhenItNamesNone(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 1, 0)
	rvPutRead(w, "s1-1", 1, "reader-c", Broken)
	rvPutRead(w, "s1-1", 1, "reader-a", OK)
	w.s.Readers.Card(ReadCardID("s1-1", 1, "reader-c")).Fields["finding"] = "the loop never ends"
	rp := rvPlan(w, ruleRework, "rework:s1-1")
	move := rvEntries(rp.Plan, Work, "s1-1")
	if len(move) != 1 || move[0].Set["asked"] != "reader-c,reader-a" || move[0].Set["fix"] != "the loop never ends" {
		t.Errorf("the primary's entry: %+v", move)
	}
}

// ReviewView answers the predicates as the one-off calls do, for every primary.
func TestReviewViewAgreesWithThePredicates(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 5, 2)
	w.rvApply(rvPlanAt(w, ruleAsk, rvNow(), "ask:s1-1", "ask:s1-2", "ask:s1-3", "ask:s1-4"))
	for _, id := range []string{"s1-1", "s1-3"} {
		for _, rd := range rvReaders(w, id) {
			rvSay(w, id, rd, "ok", "")
		}
	}
	two := rvReaders(w, "s1-2")
	rvSay(w, "s1-2", two[0], "ok", "")
	rvSay(w, "s1-2", two[1], "broken", "off by one")
	w.s.Work.Card("s1-3").Fields["ci"] = "red"
	want := map[string][3]bool{ // ask, accept, rework
		"s1-1": {false, true, false},
		"s1-2": {false, false, true},
		"s1-3": {false, false, false},
		"s1-4": {false, false, false},
		"s1-5": {true, false, false},
		"s1-6": {false, false, true},
		"s1-7": {false, false, true},
	}
	v := NewReviewView(w.s)
	for id, exp := range want {
		c := w.s.Work.Card(id)
		got := [3]bool{AskDue(w.s, c), AcceptDue(w.s, c), ReworkDue(w.s, c)}
		if got != exp {
			t.Errorf("%s: ask, accept, rework due %v, want %v", id, got, exp)
		}
		if view := [3]bool{v.AskDue(c), v.AcceptDue(c), v.ReworkDue(c)}; view != got {
			t.Errorf("%s: the view says %v, the calls %v", id, view, got)
		}
	}
	// a primary not in review is due for none
	w.s.Work.Card("s1-1").Col = Merging
	if c := w.s.Work.Card("s1-1"); AskDue(w.s, c) || AcceptDue(w.s, c) || ReworkDue(w.s, c) {
		t.Errorf("a merging primary is due")
	}
}

// The numbers the rules take from the design as their own (2.3, 1.2, 2.2).
func TestReviewRulesTakeTheDesignsNumbers(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct{ got, want time.Duration }{
		"the asked read's deadline (1.2)":      {reviewDeadlineUnbegun, 30 * time.Minute},
		"the dealt work card's deadline (1.2)": {reviewDeadlineUntaken, 15 * time.Minute},
		"the merge-idle deadline (1.2)":        {reviewDeadlineMergeIdle, 30 * time.Minute},
	} {
		if c.got != c.want {
			t.Errorf("%s: %s, want %s", name, c.got, c.want)
		}
	}
	for name, c := range map[string]struct{ got, want int }{
		"attempts (R10)":                  {MaxAttempts, 3},
		"readers asked (R8)":              {AskReaders, 2},
		"readers that must agree (R9)":    {AcceptReaders, 2},
		"read cards of a primary (1.3.1)": {MaxRCards, 15},
		"askwait's chunk (R8)":            {AskwaitChunk, 2000},
		"redeals before the bound (R2)":   {reviewMaxRedeals, 5},
		"the ids of a line (1.0)":         {MaxLineIDs, 2000},
	} {
		if c.got != c.want {
			t.Errorf("%s: %d, want %d", name, c.got, c.want)
		}
	}
}

// The coordinator's rework takes a primary in ready at the redeal bound the
// design gives (5 redeals, R2), not today's: a withdrawn card dealt again fewer
// times is not at its bound, and is refused for not being in review.
func TestReworkAtTakesTheRedealBoundOfTheDesign(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		redeals int
		bound   bool
	}{{reviewMaxRedeals - 1, false}, {reviewMaxRedeals, true}, {reviewMaxRedeals + 1, true}} {
		w := setup(t, 1)
		w.must(Deal(w.s, DealReq{Sel: Sel{Limit: 1}}))
		pr := w.s.Work.Card("s1-1")
		wc := w.s.Fleet.Card(pr.F("work"))
		wc.Col = Withdrawn
		wc.Fields["redeals"] = itoa(tt.redeals)
		w.s.Fleet.Put(wc)
		pr.Col = Ready
		delete(pr.Fields, "work")
		w.s.Work.Put(pr)
		if got := reviewAtRedealBound(w.s, pr) != nil; got != tt.bound {
			t.Errorf("redeals %d: at the bound %v, want %v", tt.redeals, got, tt.bound)
		}
		p := ReworkAt(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "x", Who: "coordinator"}, rvNow())
		if tt.bound != (len(p.Units) == 1 && len(p.Refused) == 0) {
			t.Errorf("redeals %d: units %d, refused %+v", tt.redeals, len(p.Units), p.Refused)
		}
	}
}

// rvScans is what a Go source reads of a table that a plan of a partial snapshot
// may not: a call of Column, Cell, Of or Cards (the scan of the whole table), and
// one of LoadedCards (every card the read loaded) but in the functions that list
// the primaries a read loaded.
func rvScans(t *testing.T, name string, src any) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	listsPrimaries := map[string]bool{"reviewPrimaries": true, "reviewPool": true}
	var out []string
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				sel, ok := n.Fun.(*ast.SelectorExpr)
				if !ok {
					break
				}
				switch name := sel.Sel.Name; {
				case name == "Column" || name == "Cell" || name == "Of" || name == "Cards":
					out = append(out, fmt.Sprintf("%s: %s calls %s", fset.Position(n.Pos()), fn.Name.Name, name))
				case name == "LoadedCards" && !listsPrimaries[fn.Name.Name]:
					out = append(out, fmt.Sprintf("%s: %s lists every card the read loaded", fset.Position(n.Pos()), fn.Name.Name))
				}
			}
			return true
		})
	}
	return out
}

// A rule takes the primaries its read loaded, never the cells of the review
// column and never every card of a table: a partial snapshot is not scanned
// (1.5.2). The check finds a scan where it is written, in the syntax.
func TestReviewRulesNeverScanACell(t *testing.T) {
	t.Parallel()
	bad := `package p
func f(s *S) {
	for range s.Work.Cards() {
	}
	for range s.Work.LoadedCards() {
	}
	s.Work.Cell("a", "b")
	s.Readers.Column("x")
	s.Merge.Of("p")
}
func reviewPrimaries(s *S) {
	for range s.Work.LoadedCards() {
	}
}
func reviewPool(s *S) {
	for range s.Work.Cards() {
	}
}`
	if got := rvScans(t, "bad.go", bad); len(got) != 6 {
		t.Fatalf("the check finds %d scans of a source with six: %v", len(got), got)
	}
	src, err := os.ReadFile("rules_review.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, scan := range rvScans(t, "rules_review.go", src) {
		t.Error(scan)
	}
}

// rvBulkWorld is a world with n primaries in review of one stream, the readers
// (named r1, r2, ...) and the most members a sprint has, built for the tests and
// benchmarks that need many. kind is a rule: ask (work ok, no reads), accept (ok
// from two different readers each) or rework (work failed, its work card in a
// member's failed cell). Each of the first opens primaries has one open judgment
// of a type the review rules do not read.
func rvBulkWorld(t *testing.T, n int, kind string, readers, opens int) *world {
	s := &Snapshot{Now: t0, Work: NewTable(Work), Readers: NewTable(Readers), Merge: NewTable(Merge), Fleet: NewTable(Fleet),
		Coordinator: "coordinator", Actor: "coordinator"}
	var members, names []string
	for i := 1; i <= MaxMembers; i++ {
		members = append(members, fmt.Sprintf("m%d", i))
	}
	for i := 1; i <= readers; i++ {
		names = append(names, fmt.Sprintf("r%d", i))
	}
	s.Work.SetRows([]string{"s1"})
	s.Merge.SetRows([]string{"s1"})
	s.Fleet.SetRows(members)
	s.Readers.SetRows(names)
	s.Merge.Put(&Card{ID: CtlID("s1"), Row: "s1", Col: Ctl, Rev: 1, Fields: map[string]string{"state": StreamWaiting}})
	for _, m := range members {
		s.Fleet.Put(&Card{ID: CtlID(m), Row: m, Col: Ctl, Rev: 1, Fields: map[string]string{"status": Up}})
	}
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("s1-%d", i)
		pr := &Card{ID: id, Row: "s1", Col: Review, Score: float64(i), Rev: 1, Fields: map[string]string{
			"attempt": "1", "head": id + ".w1", "result": "ok", "work": id + ".w1"}}
		switch kind {
		case ruleAccept:
			var ids []string
			for k := 0; k < AcceptReaders; k++ {
				rd := names[(i+k)%len(names)]
				rc := &Card{ID: ReadCardID(id, 1, rd), Row: rd, Col: OK, Score: pr.Score, Rev: 1, Fields: map[string]string{
					"kind": "read", "primary": id, "stream": "s1", "reader": rd, "attempt": "1", "head": pr.F("head")}}
				s.Readers.Put(rc)
				ids = append(ids, rc.ID)
			}
			pr.Fields["rcards"] = strings.Join(ids, ",")
		case ruleRework:
			pr.Fields["result"] = "failed"
			m := members[i%len(members)]
			s.Fleet.Put(&Card{ID: id + ".w1", Row: m, Col: DoneFailed, Score: pr.Score, Rev: 1, Fields: map[string]string{
				"kind": "work", "primary": id, "member": m, "report": "tests red", "attempt": "1"}})
		}
		s.Work.Put(pr)
		if i <= opens {
			note := Note{ID: "o" + id, Kind: Judgment, Type: NStalled, Primaries: []string{id}, Count: 1}
			s.Open = append(s.Open, Open{Key: OpenKey(note.ID, id), Note: note})
		}
	}
	return &world{t: t, s: s}
}

// rvBulkLoaded is the snapshot the rule plans on for the keys of a bulk world,
// read with no bound (a plan of every primary, for the timing of the planners).
func rvBulkLoaded(w *world, kind string, keys []AgendaKey) (Rule, *Snapshot) {
	r, _ := rvRuleOf(kind)
	s, _, left := rvLoad(w.s, nil, r, keys, ReadBounds{}, 0, func(f string, a ...any) { panic(fmt.Sprintf(f, a...)) })
	if len(left) != 0 {
		panic("the read left keys")
	}
	return r, s
}

// rvPlanTime is the time of one plan of the keys on the snapshot.
func rvPlanTime(r Rule, s *Snapshot, keys []AgendaKey) time.Duration {
	start := time.Now()
	r.Plan(s, keys, rvNow())
	return time.Since(start)
}

// The time a plan takes grows with the primaries it plans, not with the primaries
// times the judgments open or the readers: four times the primaries take about
// four times as long, measured alone. A cost that grows with the square (a scan
// of every open judgment for every primary) takes over ten times as long. The
// two sizes are timed in turn, so that the load of the machine falls on both, and
// the least of nine runs of each is taken; a ratio over the bound is measured
// again, up to five times, before it fails the test, since the tests of a package
// run at once.
func TestReviewPlansGrowLinearlyWithThePrimaries(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{ruleAsk, ruleAccept, ruleRework} {
		keys := rvKeys(kind + "@1")
		var rule [2]Rule
		var snap [2]*Snapshot
		for i, n := range []int{500, 2000} {
			w := rvBulkWorld(t, n, kind, 1000, n)
			rule[i], snap[i] = rvBulkLoaded(w, kind, keys)
			if got := len(rule[i].Plan(snap[i], keys, rvNow()).Plan.Units); got != n {
				t.Fatalf("%s: %d primaries planned of %d", kind, got, n)
			}
		}
		var small, large time.Duration
		var ratio float64
		for attempt := 0; attempt < 5; attempt++ {
			small, large = time.Duration(1<<62), time.Duration(1<<62)
			for run := 0; run < 9; run++ {
				small = min(small, rvPlanTime(rule[0], snap[0], keys))
				large = min(large, rvPlanTime(rule[1], snap[1], keys))
			}
			if ratio = float64(large) / float64(small); ratio <= 8 {
				break
			}
		}
		if ratio > 8 {
			t.Errorf("%s: 2,000 primaries take %s and 500 take %s, %.1f times as long for four times as many", kind, large, small, ratio)
		}
	}
}

// The limits, in Go time: the design's own limit for accept is store time,
// measured by IT25; these are the planners' share of it. Each benchmark plans
// primaries in review (2,000, the chunk) with the most readers a sprint has
// (1,000, section 3) or 8.

func rvBenchPlan(b *testing.B, kind string, n, readers, opens int, want int) {
	w := rvBulkWorld(nil, n, kind, readers, opens)
	keys := rvKeys(kind + "@1")
	r, s := rvBulkLoaded(w, kind, keys)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if rp := r.Plan(s, keys, rvNow()); len(rp.Plan.Units) != want {
			b.Fatalf("%d units", len(rp.Plan.Units))
		}
	}
}

func BenchmarkPlanAsk2000(b *testing.B)            { rvBenchPlan(b, ruleAsk, 2000, 8, 0, 2000) }
func BenchmarkPlanAsk2000Readers1000(b *testing.B) { rvBenchPlan(b, ruleAsk, 2000, 1000, 0, 2000) }
func BenchmarkPlanAcceptOne(b *testing.B)          { rvBenchPlan(b, ruleAccept, 1, 8, 0, 1) }
func BenchmarkPlanAccept2000(b *testing.B)         { rvBenchPlan(b, ruleAccept, 2000, 8, 0, 2000) }
func BenchmarkPlanAccept2000Readers1000(b *testing.B) {
	rvBenchPlan(b, ruleAccept, 2000, 1000, 2000, 2000)
}
func BenchmarkPlanRework2000(b *testing.B) { rvBenchPlan(b, ruleRework, 2000, 8, 0, 2000) }
func BenchmarkPlanRework2000Readers1000(b *testing.B) {
	rvBenchPlan(b, ruleRework, 2000, 1000, 2000, 2000)
}

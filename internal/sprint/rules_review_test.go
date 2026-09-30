package sprint

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

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
	w.rvApply(planAsk(w.s, rvKeys("ask@1"), rvNow()))
}

// rvReaders is the readers a primary was asked of at its attempt, in reader
// row order.
func rvReaders(w *world, id string) []string {
	var out []string
	for _, rd := range w.s.Readers.Rows {
		if rc := w.s.Readers.Card(ReadCardID(id, 1, rd)); rc != nil {
			out = append(out, rd)
		}
	}
	return out
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

// R8: two different readers, the shortest queues, the ones it names first.
func TestAskTwoDifferentReaders(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 3, 0)
	// reader-a is already asked of s1-3, so its queue is one and the others' none
	rvPutRead(w, "s1-3", 1, "reader-a", Asked)
	w.clean("a read of s1-3")

	rp := planAsk(w.s, rvKeys("ask@7"), rvNow())
	if len(rp.Plan.Units) != 2 || len(rp.Plan.Refused) != 0 {
		t.Fatalf("units %d, refused %v: s1-3 has a read standing and is not asked again", len(rp.Plan.Units), rp.Plan.Refused)
	}
	want := map[string][2]string{"s1-1": {"reader-b", "reader-c"}, "s1-2": {"reader-a", "reader-b"}}
	for _, u := range rp.Plan.Units {
		var got []string
		for _, e := range rvCreates(Plan{Units: []Unit{u}}, Readers) {
			got = append(got, e.Create.Row)
			if e.Create.Col != Asked || e.Create.Score != w.s.Work.Card(u.Key).Score || e.Set["reader"] != e.Create.Row || e.Set["head"] != w.s.Work.Card(u.Key).F("head") {
				t.Errorf("%s: the read card is %+v", u.Key, e)
			}
		}
		if len(got) != 2 || got[0] == got[1] || [2]string{got[0], got[1]} != want[u.Key] {
			t.Errorf("%s is asked of %v, want the two different readers with the shortest queues, %v", u.Key, got, want[u.Key])
		}
	}
	if got := rvDone(rp); len(got) != 1 || got[0] != "ask@7" || len(rp.Requeue) != 0 {
		t.Errorf("the key: done %v, requeue %v", got, rp.Requeue)
	}
	w.rvApply(rp)
	w.clean("ask")
	for _, id := range []string{"s1-1", "s1-2"} {
		if n := len(reviewReads(w.s, w.s.Work.Card(id), 1)); n != 2 {
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
	rp := planAsk(w.s, rvKeys("ask:s1-1"), now)
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
	w.s.Readers.Rows = []string{"reader-a", "reader-b"}
	rvPutRead(w, "s1-1", 1, "reader-a", "")
	rp := planAsk(w.s, rvKeys("ask:s1-1"), rvNow())
	creates := rvCreates(rp.Plan, Readers)
	if len(creates) != 0 || len(rp.Plan.Units) != 0 {
		t.Fatalf("asked of %+v with one reader able", creates)
	}
	notes := rvNotes(rp, reviewOpen, NCannotAsk)
	if len(notes) != 1 || len(notes[0].Subjects) != 1 || notes[0].Subjects[0] != "s1-1" || notes[0].Cause != reviewCauses[NCannotAsk] {
		t.Fatalf("notes %+v, want one \"cannot ask\" on s1-1", rp.Notes)
	}
	// a new reader is a second able reader
	w.s.Readers.Rows = append(w.s.Readers.Rows, "reader-d")
	rp = planAsk(w.s, rvKeys("ask:s1-1"), rvNow())
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

	rp := planAsk(w.s, rvKeys("ask@3"), rvNow())
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

	rp := planAccept(w.s, rvKeys("accept@9"), rvNow())
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
	rp := planAccept(w.s, rvKeys("accept:s1-1", "accept:s1-2"), rvNow())
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
	rp := planAccept(w.s, rvKeys("accept:s1-1", "accept:s1-2", "accept:s1-3", "accept:s1-4", "accept:s1-5"), rvNow())
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
	rp := planAccept(w.s, rvKeys("accept:s1-1", "accept:s1-2"), rvNow())
	if len(rp.Plan.Units) != 1 || rp.Plan.Units[0].Key != "s1-2" || len(rp.Done) != 2 {
		t.Fatalf("units %+v, done %v: s1-1 is returned and its key is finished", rp.Plan.Units, rp.Done)
	}
	w.s.Open = nil
	if rp = planAccept(w.s, rvKeys("accept:s1-1"), rvNow()); len(rp.Plan.Units) != 2 || rp.Plan.Units[0].Key != "s1-1" {
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
	rp := planAccept(w.s, rvKeys("accept:s1-1"), rvNow())
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
	if second := planAccept(w.s, rvKeys("accept:s1-1"), rvNow()); !rvEmpty(second) {
		t.Errorf("a refused primary is planned again: %+v", second)
	}
	// no control card for the stream
	w2 := rvReview(t, 1, 0)
	rvAllOK(w2, "s1-1")
	w2.s.Merge.Cards[CtlID("s1")].Col = ""
	w2.s.Merge.Put(w2.s.Merge.Cards[CtlID("s1")])
	rp = planAccept(w2.s, rvKeys("accept:s1-1"), rvNow())
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
	rp := planRework(w.s, rvKeys("rework:s1-1"), now)
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
	if len(rp.Guards) != 1 || rp.Guards[0] != (XGuard{Kind: guardMemberUp, Member: other}) {
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
	rvFillReady(w, other, MaxReadyPerMember)
	if c := rvCreates(planRework(w.s, rvKeys("rework:s1-1"), now).Plan, Fleet); len(c) != 1 || c[0].Create.Row != worked {
		t.Errorf("with %s full, dealt to %+v, want %s", other, c, worked)
	}
	// nobody has room: the primary goes back to ready with avoid, into again
	rvFillReady(w, worked, MaxReadyPerMember)
	rp = planRework(w.s, rvKeys("rework:s1-1"), now)
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
	move = rvEntries(planRework(w.s, rvKeys("rework:s1-1"), now).Plan, Work, "s1-1")
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
	w.rvApply(planAsk(w.s, rvKeys("ask:s1-1"), rvNow()))
	a, b := ReadCardID("s1-1", 1, "reader-a"), ReadCardID("s1-1", 1, "reader-b")
	w.must(Read(w.s, ReadReq{As: "reader-a", Verdict: "broken", Finding: "off by one", Sel: Sel{IDs: []string{a}}}))
	w.must(Read(w.s, ReadReq{As: "reader-b", Verdict: "ok", Sel: Sel{IDs: []string{b}}}))
	if !ReworkDue(w.s, w.s.Work.Card("s1-1")) {
		t.Fatalf("a broken read does not make a rework due")
	}
	rp := planRework(w.s, rvKeys("rework:s1-1"), rvNow())
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
	if len(reviewReads(w.s, w.s.Work.Card("s1-1"), 1)) != 2 || w.s.Readers.Placed(a) != nil {
		t.Errorf("the attempt's reads are not retired")
	}
}

// A primary that has failed MaxAttempts times stays in review with its bound
// set and the judgment named; only the coordinator's rework takes it further,
// and unsets the bound.
func TestReworkAtAttemptsBound(t *testing.T) {
	t.Parallel()
	w := rvReview(t, 0, 1)
	for attempt := 1; attempt < MaxAttempts; attempt++ {
		rp := planRework(w.s, rvKeys("rework:s1-1"), rvNow())
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
	rp := planRework(w.s, rvKeys("rework:s1-1"), rvNow())
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
	if ReworkDue(w.s, w.s.Work.Card("s1-1")) || AskDue(w.s, w.s.Work.Card("s1-1")) || !rvEmpty(planRework(w.s, rvKeys("rework:s1-1"), rvNow())) {
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
	wc.Fields["redeals"] = itoa(MaxRedeals)
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
	w2.rvApply(planAccept(w2.s, rvKeys("accept:s1-1"), rvNow()))
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
	rp := planAsk(w.s, rvKeys("ask@1"), rvNow())
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
// trip on; their keys are finished.
func TestReviewRulesOnAnEmptySnapshot(t *testing.T) {
	t.Parallel()
	for name, plan := range map[string]func(*Snapshot, []AgendaKey, Now) RulePlan{ruleAsk: planAsk, ruleAccept: planAccept, ruleRework: planRework} {
		for _, s := range []*Snapshot{{}, {Work: NewTable(Work)}, {Work: NewTable(Work), Readers: NewTable(Readers), Merge: NewTable(Merge), Fleet: NewTable(Fleet)}} {
			rp := plan(s, rvKeys(name+":p", name+"@4"), rvNow())
			if !rvEmpty(rp) || len(rp.Done) != 2 {
				t.Errorf("%s: %+v", name, rp)
			}
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
	first := planAsk(w.s, keys, rvNow())
	if len(first.Plan.Units) != 3 {
		t.Fatalf("first ask: %d units", len(first.Plan.Units))
	}
	w.rvApply(first)
	if second := planAsk(w.s, keys, rvNow()); !rvEmpty(second) || len(second.Done) != 1 {
		t.Errorf("second ask: %+v", second)
	}
	// R9: s1-1 is read ok twice; s1-2 has one broken read, and s1-3 nothing yet
	for _, rd := range []string{"reader-a", "reader-b", "reader-c"} {
		if rc := w.s.Readers.Card(ReadCardID("s1-1", 1, rd)); rc != nil {
			w.must(Read(w.s, ReadReq{As: rd, Verdict: "ok", Sel: Sel{IDs: []string{rc.ID}}}))
		}
	}
	for _, rc := range reviewReads(w.s, w.s.Work.Card("s1-2"), 1) {
		w.must(Read(w.s, ReadReq{As: rc.F("reader"), Verdict: "broken", Finding: "missing test", Sel: Sel{IDs: []string{rc.ID}}}))
	}
	keys = rvKeys("accept:s1-1")
	first = planAccept(w.s, keys, rvNow())
	if len(first.Plan.Units) != 1 {
		t.Fatalf("first accept: %d units", len(first.Plan.Units))
	}
	w.rvApply(first)
	w.clean("accept")
	if second := planAccept(w.s, keys, rvNow()); !rvEmpty(second) || len(second.Done) != 1 {
		t.Errorf("second accept: %+v", second)
	}
	// R10: s1-2 (broken read) and s1-4 and s1-5 (failed work)
	keys = rvKeys("rework@12")
	first = planRework(w.s, keys, rvNow())
	if len(first.Plan.Units) != 3 {
		t.Fatalf("first rework: %d units", len(first.Plan.Units))
	}
	w.rvApply(first)
	w.clean("rework")
	if second := planRework(w.s, keys, rvNow()); !rvEmpty(second) || len(second.Done) != 1 {
		t.Errorf("second rework: %+v", second)
	}
	// cannot ask, then asked: askwait
	w = rvReview(t, 2, 0)
	w.s.Readers.Rows = []string{"reader-a"}
	keys = rvKeys("askwait")
	first = planAsk(w.s, keys, rvNow())
	if len(first.Plan.Units) != 0 || len(rvNotes(first, reviewOpen, NCannotAsk)) != 1 || len(first.Requeue) != 0 {
		t.Fatalf("cannot ask: %+v", first)
	}
	w.rvApply(first)
	if second := planAsk(w.s, keys, rvNow()); !rvEmpty(second) {
		t.Errorf("second cannot ask: %+v", second)
	}
	// a reader is added: the askwait key asks both, closes the judgment and is put back once
	w.s.Readers.Rows = []string{"reader-a", "reader-b", "reader-c"}
	first = planAsk(w.s, keys, rvNow())
	closing := rvNotes(first, reviewClose, NCannotAsk)
	if len(first.Plan.Units) != 2 || len(closing) != 1 || len(closing[0].Subjects) != 2 || len(first.Requeue) != 1 || first.Requeue[0].Key != "askwait" || len(first.Done) != 0 {
		t.Fatalf("askwait after a reader is added: %+v", first)
	}
	w.rvApply(first)
	if len(w.openOn("s1-1")) != 0 && w.openOn("s1-1")[0].Note.Type == NCannotAsk {
		t.Errorf("cannot ask stays open on an asked primary")
	}
	if second := planAsk(w.s, keys, rvNow()); !rvEmpty(second) || len(second.Done) != 1 {
		t.Errorf("askwait a second time: %+v", second)
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

// A read is sized by the declared cost of what it asks: keys are cut to fit the
// records a read may hold and the rest are left; a key that names a line or
// askwait costs more than a read holds and is read alone; a halving halves what
// fits, down to one key.
func TestReviewReadSizesKeys(t *testing.T) {
	t.Parallel()
	r := reviewRules[0]
	per := QueryCost(SprintQ{Kind: qRelated, IDs: []string{"p"}, Fields: r.fields, Follow: r.follow}).Records
	fixed := QueryCost(SprintQ{Kind: qReaders}).Records
	b := ReadBounds{Queries: 1024, Records: 10_000, RangeIDs: 20_000, Bytes: 8 << 20}
	var keys []string
	for i := 0; i < 1000; i++ {
		keys = append(keys, fmt.Sprintf("ask:s1-%d", i))
	}
	ids := func(rp ReadPlan) int {
		n := 0
		for _, q := range rp.Sprint {
			if q.Kind == qRelated {
				n += len(q.IDs)
			}
		}
		return n
	}
	rp, left := r.read(rvKeys(keys...), b, 0)
	fit := (b.Records - fixed) / per
	if ids(rp) != fit || len(left) != 1000-fit {
		t.Fatalf("read %d keys and left %d, want %d and %d", ids(rp), len(left), fit, 1000-fit)
	}
	if left[0].Key != fmt.Sprintf("ask:s1-%d", fit) {
		t.Errorf("the first key left is %s: keys are taken in order", left[0].Key)
	}
	if last := rp.Sprint[len(rp.Sprint)-1]; last.Kind != qReaders {
		t.Errorf("the readers are not read: %+v", rp.Sprint)
	}
	total := 0
	for _, q := range rp.Sprint {
		total += QueryCost(q).Records
	}
	if total > b.Records {
		t.Errorf("the read costs %d records, more than %d", total, b.Records)
	}
	// each halving halves what fits, down to one key
	for h, want := 1, fit/2; want >= 1; h, want = h+1, want/2 {
		if rp, _ := r.read(rvKeys(keys...), b, h); ids(rp) < want-1 || ids(rp) > want+1 {
			t.Errorf("halvings %d: %d keys, want about %d", h, ids(rp), want)
		}
	}
	if rp, left := r.read(rvKeys(keys...), b, 40); ids(rp) != 1 || len(left) != 999 {
		t.Errorf("halvings 40: %d keys, %d left, want one", ids(rp), len(left))
	}
	// a line is read alone, from its offset, and askwait alone
	rp, left = r.read(rvKeys("ask@9+500", "ask:s1-1", "askwait"), b, 0)
	if len(left) != 2 || len(rp.Sprint) != 2 || rp.Sprint[0].Line != 9 || rp.Sprint[0].Offset != 500 || rp.Sprint[0].Limit != reviewLineIDs-500 {
		t.Errorf("a line: %+v, left %v", rp.Sprint, left)
	}
	rp, left = r.read(rvKeys("askwait", "ask:s1-1"), b, 0)
	if len(left) != 1 || rp.Sprint[0].Head != reviewHeadAskwait || rp.Sprint[0].Limit < 1 || rp.Sprint[0].Limit > AskwaitChunk {
		t.Errorf("askwait: %+v, left %v", rp.Sprint, left)
	}
	// a key that names nothing costs nothing and is taken; no key, no query
	if rp, left = r.read(rvKeys("ask@x"), b, 0); len(rp.Sprint) != 0 || len(left) != 0 {
		t.Errorf("a key that names nothing: %+v %v", rp.Sprint, left)
	}
	if rp, left = r.read(nil, b, 0); len(rp.Sprint) != 0 || len(left) != 0 {
		t.Errorf("no keys: %+v %v", rp.Sprint, left)
	}
}

// The three reads project what their rules read and follow what the design
// says: accept follows the merge card and the control card, rework the work
// card and asks for the fleet, ask for the readers.
func TestReviewReadsFollowTheDesign(t *testing.T) {
	t.Parallel()
	b := ReadBounds{Queries: 1024, Records: 10_000, RangeIDs: 20_000, Bytes: 8 << 20}
	for name, c := range map[string]struct {
		follow []string
		fixed  string
	}{ruleAsk: {[]string{"rcards", "jopen"}, qReaders}, ruleAccept: {[]string{"rcards", "merge", "control", "jopen"}, ""}, ruleRework: {[]string{"rcards", "work"}, qFleet}} {
		var r reviewRule
		for _, x := range reviewRules {
			if x.name == name {
				r = x
			}
		}
		rp, _ := r.read(rvKeys(name+":p"), b, 0)
		if len(rp.Sprint) == 0 || strings.Join(rp.Sprint[0].Follow, ",") != strings.Join(c.follow, ",") || rp.Sprint[0].Table != Work {
			t.Errorf("%s: %+v", name, rp.Sprint)
		}
		if got := rp.Sprint[len(rp.Sprint)-1].Kind; c.fixed != "" && got != c.fixed || c.fixed == "" && got != qRelated {
			t.Errorf("%s: its last query is %s", name, got)
		}
	}
}

// A rule takes the primaries its read loaded, never the cells of the review
// column: a partial snapshot is not scanned (1.5.2).
func TestReviewRulesNeverScanACell(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("rules_review.go")
	if err != nil {
		t.Fatal(err)
	}
	scan := regexp.MustCompile(`\.(Column|Cell|Of)\(`)
	for i, line := range strings.Split(string(src), "\n") {
		if code, _, _ := strings.Cut(line, "//"); scan.MatchString(code) {
			t.Errorf("rules_review.go:%d scans a cell: %s", i+1, strings.TrimSpace(line))
		}
	}
}

// rvBulk is a snapshot with n primaries in review of one stream, for the
// benchmarks: ask (work ok, no reads), accept (ok from two readers each) or
// rework (work failed, its work card in a member's failed cell).
func rvBulk(n int, kind string) *Snapshot {
	s := &Snapshot{Now: t0, Work: NewTable(Work), Readers: NewTable(Readers), Merge: NewTable(Merge), Fleet: NewTable(Fleet),
		Coordinator: "coordinator", Actor: "coordinator"}
	var members []string
	for i := 1; i <= 250; i++ { // the most members a sprint has (section 3)
		members = append(members, fmt.Sprintf("m%d", i))
	}
	readers := []string{"r1", "r2", "r3", "r4", "r5", "r6", "r7", "r8"}
	s.Work.Rows, s.Merge.Rows, s.Fleet.Rows, s.Readers.Rows = []string{"s1"}, []string{"s1"}, members, readers
	s.Merge.Put(&Card{ID: CtlID("s1"), Row: "s1", Col: Ctl, Rev: 1, Fields: map[string]string{"state": StreamWaiting}})
	for _, m := range members {
		s.Fleet.Put(&Card{ID: CtlID(m), Row: m, Col: Ctl, Rev: 1, Fields: map[string]string{"status": Up}})
	}
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("s1-%d", i)
		pr := &Card{ID: id, Row: "s1", Col: Review, Score: float64(i), Rev: 1, Fields: map[string]string{
			"attempt": "1", "head": id + ".w1", "result": "ok", "work": id + ".w1"}}
		switch kind {
		case "accept":
			var ids []string
			for k := 0; k < 2; k++ {
				rd := readers[(i+k)%len(readers)]
				rc := &Card{ID: ReadCardID(id, 1, rd), Row: rd, Col: OK, Score: pr.Score, Rev: 1, Fields: map[string]string{
					"kind": "read", "primary": id, "stream": "s1", "reader": rd, "attempt": "1", "head": pr.F("head")}}
				s.Readers.Put(rc)
				ids = append(ids, rc.ID)
			}
			pr.Fields["rcards"] = strings.Join(ids, ",")
		case "rework":
			pr.Fields["result"] = "failed"
			m := members[i%len(members)]
			s.Fleet.Put(&Card{ID: id + ".w1", Row: m, Col: DoneFailed, Score: pr.Score, Rev: 1, Fields: map[string]string{
				"kind": "work", "primary": id, "member": m, "report": "tests red", "attempt": "1"}})
		}
		s.Work.Put(pr)
	}
	return s
}

// The limits, in Go time: the design's own limit for accept is store time,
// measured by IT25; these are the planners' share of it.

func BenchmarkPlanAsk2000(b *testing.B) {
	s := rvBulk(2000, "ask")
	keys := rvKeys("ask@1")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if rp := planAsk(s, keys, rvNow()); len(rp.Plan.Units) != 2000 {
			b.Fatalf("%d units", len(rp.Plan.Units))
		}
	}
}

func BenchmarkPlanAcceptOne(b *testing.B) {
	s := rvBulk(1, "accept")
	keys := rvKeys("accept:s1-1")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if rp := planAccept(s, keys, rvNow()); len(rp.Plan.Units) != 1 {
			b.Fatalf("%d units", len(rp.Plan.Units))
		}
	}
}

func BenchmarkPlanAccept2000(b *testing.B) {
	s := rvBulk(2000, "accept")
	keys := rvKeys("accept@1")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if rp := planAccept(s, keys, rvNow()); len(rp.Plan.Units) != 2000 {
			b.Fatalf("%d units", len(rp.Plan.Units))
		}
	}
}

func BenchmarkPlanRework2000(b *testing.B) {
	s := rvBulk(2000, "rework")
	keys := rvKeys("rework@1")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if rp := planRework(s, keys, rvNow()); len(rp.Plan.Units) != 2000 {
			b.Fatalf("%d units", len(rp.Plan.Units))
		}
	}
}

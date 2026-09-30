package sprint

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"
)

// The tables of section 2.2 and 2.5 of the upper design, written out here a
// second time, row by row, in the design's own words: the tests hold the code's
// tables to this copy, so a change to a row is a change to both.

// The rows of the design's tables: 2.2 has 26, and 2.5 has 23 of which one holds
// two notices and one three, so 26 notices.
const (
	rows22 = 26
	rows25 = 26
)

// judgmentWant is one row of 2.2. ownerArg is the word the owner key is made
// of (see the header of judgments.go) and ownerKey the key the design gives
// for it, "" for a row it queues none for.
type judgmentWant struct {
	typ, subject       string
	ownerArg, ownerKey string
	decisions          []string
	tickKept, ack      bool
	raisedBy           []string
}

var table22 = []judgmentWant{
	{"sentinel reached", "sentinel", "s", "resolve:s",
		[]string{"release G --reason", "add --before G", "drop G"}, false, false, []string{"R3"}},
	{"a primary is blocked on something dropped", "waiter", "w", "held:w",
		[]string{"ack", "drop w"}, false, true, []string{"R4", "add"}},
	{"a primary is blocked on something missing", "waiter", "w", "held:w",
		[]string{"add <n>", "ack", "drop w"}, false, true, []string{"add"}},
	{"cannot ask", "primary", "p", "ask:p",
		[]string{"reader add", "rework --fix", "drop", "wait"}, true, false, []string{"R8"}},
	{"no fleet member is up", "sprint", "", "deal",
		[]string{"fleet beat", "fleet up <m>", "wait"}, true, false, []string{"R6"}},
	{"a card reached its bound", "primary", "p", "held:p",
		[]string{"rework --fix", "drop", "wait"}, true, false, []string{"R2", "R10"}},
	{"a work card is past its deadline", "work card", "untaken:c", "late:untaken:c",
		[]string{"fleet down <member>", "drop <p>", "wait"}, true, false, []string{"R11"}},
	{"a read card is past its deadline", "read card", "unbegun:c", "late:unbegun:c",
		[]string{"ask --another <p>", "drop <p>", "wait"}, true, false, []string{"R11"}},
	{"a stream has had no merge step past its deadline", "stream", "s", "late:mergeidle:s",
		[]string{"merge --stream s", "card", "wait"}, true, false, []string{"R11"}},
	{"stream stopped: conflict on a card", "stream", "s", "",
		[]string{"resume --stream s --did", "rework <card>", "drop <card>"}, false, false, []string{"merge"}},
	{"stream stopped: stream branch red", "stream", "s", "",
		[]string{"return <suspect>", "resume --did", "rework <suspect>"}, false, false, []string{"merge"}},
	{"stream stopped: needs a card of another stream first", "stream", "s", "cross",
		[]string{"rank <needed card>", "card", "return <card>", "drop <card>", "wait"}, false, false, []string{"merge"}},
	{"stream stopped: the merge queue rejected", "stream", "s", "",
		[]string{"resume --did", "return", "drop"}, false, false, []string{"merge"}},
	{"ci red on a primary", "primary", "p", "",
		[]string{"rework --fix", "accept", "drop", "card", "ack"}, false, true, []string{"ci"}},
	{"returned to review", "primary", "p", "",
		[]string{"rework --fix", "accept", "drop"}, false, false, []string{"return"}},
	{"reads exhausted", "primary", "p", "held:p",
		[]string{"ask --another", "rework --fix", "drop"}, false, false, []string{"R16"}},
	{"stranded in review", "primary", "p", "held:p",
		[]string{"ask", "rework --fix", "drop"}, false, false, []string{"R16"}},
	{"stalled", "card", "c", "held:c",
		[]string{"card", "drop", "wait"}, true, false, []string{"R16"}},
	{"the machine could not move a card", "card", "c", "held:c",
		[]string{"ack", "drop", "card", "wait"}, true, true, []string{"R3", "R6", "R8", "R9", "R10", "R11"}},
	{"an invariant is broken", "card", "c", "held:c",
		[]string{"card", "clear --confirm sprint", "drop", "wait"}, true, false, []string{"a lower-layer refusal naming the card", "the sweep"}},
	{"the machine's step was refused", "rule key", "ask:p", "ask:p",
		[]string{"log --since", "ack", "stop", "wait"}, true, true, []string{"the tick"}},
	{"the sprint is done", "sprint", "", "done",
		[]string{"clear", "add"}, false, false, []string{"R15"}},
	{"a reminder could not be delivered", "person", "u", "remind:u",
		[]string{"goal set", "goal drop", "ack"}, true, true, []string{"R14"}},
	{"the machine is falling behind", "sprint", "", "behind",
		[]string{"wait", "stop", "where"}, true, false, []string{"R18"}},
	{"the machine is STOPPED and moves are due", "sprint", "", "",
		[]string{"start", "wait --for d --reason"}, true, false, []string{"R17"}},
	{"a verb in parts stopped before its end", "op", "o", "late:cut:o",
		[]string{"the same command with --op <op>", "drop --abort --op <op>", "remove --abort --op <op>", "ack", "wait"}, true, true, []string{"R11"}},
}

func TestJudgmentsMatch22(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, want := range table22 {
		if seen[want.typ] {
			t.Fatalf("the test lists %q twice", want.typ)
		}
		seen[want.typ] = true
		got, ok := Judgments[want.typ]
		if !ok {
			t.Errorf("section 2.2 has %q and Judgments does not", want.typ)
			continue
		}
		if got.Type != want.typ {
			t.Errorf("%q: the row is keyed by %q", want.typ, got.Type)
		}
		if got.Subject != want.subject {
			t.Errorf("%q: subject %q, want %q", want.typ, got.Subject, want.subject)
		}
		if got.OwnerKey == nil {
			t.Errorf("%q: no owner key function", want.typ)
		} else if k := got.OwnerKey(want.ownerArg); k != want.ownerKey {
			t.Errorf("%q: owner key of %q is %q, want %q", want.typ, want.ownerArg, k, want.ownerKey)
		}
		var ds []string
		for _, d := range got.Decisions {
			ds = append(ds, d.String())
		}
		if !slices.Equal(ds, want.decisions) {
			t.Errorf("%q: decisions %q, want %q", want.typ, ds, want.decisions)
		}
		if got.TickKept != want.tickKept {
			t.Errorf("%q: tick kept %v, want %v", want.typ, got.TickKept, want.tickKept)
		}
		if got.Ack != want.ack {
			t.Errorf("%q: ack %v, want %v", want.typ, got.Ack, want.ack)
		}
		if !slices.Equal(got.RaisedBy, want.raisedBy) {
			t.Errorf("%q: raised by %q, want %q", want.typ, got.RaisedBy, want.raisedBy)
		}
	}
	for typ := range Judgments {
		if !seen[typ] {
			t.Errorf("Judgments has %q and section 2.2 does not", typ)
		}
	}
	if len(table22) != rows22 || len(Judgments) != rows22 {
		t.Errorf("section 2.2 has %d rows: the test has %d, Judgments %d", rows22, len(table22), len(Judgments))
	}
	// A row offers ack exactly when its ack column says yes.
	for _, want := range table22 {
		offers := false
		for _, d := range Judgments[want.typ].Decisions {
			offers = offers || d.Verb == "ack"
		}
		if offers != want.ack {
			t.Errorf("%q: the ack column says %v and its decisions offer ack %v", want.typ, want.ack, offers)
		}
	}
}

// noticeWant is one row of 2.5.
type noticeWant struct {
	typ, subject string
	raisedBy     []string
}

var table25 = []noticeWant{
	{"stream started merging", "stream", []string{"accept", "R9"}},
	{"batch landed", "stream", []string{"merge"}},
	{"stream landed", "stream", []string{"merge"}},
	{"work came back ok", "primaries", []string{"finish"}},
	{"fleet member up", "member", []string{"R1", "fleet up"}},
	{"fleet member down", "member", []string{"R2", "fleet down"}},
	{"a member's ok rate fell below OkRateFloor", "member", []string{"finish"}},
	{"a reader's broken rate rose above BrokenRateCeiling", "reader", []string{"read --broken"}},
	{"a read of p by r: its one-line summary", "primary", []string{"read --ok", "read --broken"}},
	{"stream s has landed nothing for IdleSpan", "stream", []string{"R11"}},
	{"an unknown machine is beating", "member", []string{"R1"}},
	{"cards returned to ready because no member is up", "primaries", []string{"R2"}},
	{"ci green", "primary", []string{"ci"}},
	{"sentinel landed", "sentinel", []string{"release"}},
	{"the machine started", "sprint", []string{"start"}},
	{"the machine stopped", "sprint", []string{"stop"}},
	{"the sprint was cleared, and is STOPPED", "sprint", []string{"clear"}},
	{"stream resumed: the card it needed landed", "stream", []string{"R5"}},
	{"k cards of s made ready", "stream", []string{"R3"}},
	{"k ready cards of s went back to waiting behind G", "stream", []string{"R19"}},
	{"accepted by the machine", "primaries", []string{"R9"}},
	{"reworked by the machine", "primaries", []string{"R10"}},
	{"replaced a work card not taken", "card", []string{"R11"}},
	{"replaced a late work card", "card", []string{"R11"}},
	{"replaced a late read", "card", []string{"R11"}},
	{"a judgment has waited past its due time", "note", []string{"R12"}},
}

func TestNoticesMatch25(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, want := range table25 {
		if seen[want.typ] {
			t.Fatalf("the test lists %q twice", want.typ)
		}
		seen[want.typ] = true
		got, ok := Notices[want.typ]
		if !ok {
			t.Errorf("section 2.5 has %q and Notices does not", want.typ)
			continue
		}
		if got.Type != want.typ || got.Subject != want.subject {
			t.Errorf("%q: type %q subject %q, want subject %q", want.typ, got.Type, got.Subject, want.subject)
		}
		if !slices.Equal(got.RaisedBy, want.raisedBy) {
			t.Errorf("%q: raised by %q, want %q", want.typ, got.RaisedBy, want.raisedBy)
		}
	}
	for typ := range Notices {
		if !seen[typ] {
			t.Errorf("Notices has %q and section 2.5 does not", typ)
		}
	}
	if len(table25) != rows25 || len(Notices) != rows25 {
		t.Errorf("section 2.5 has %d notices: the test has %d, Notices %d", rows25, len(table25), len(Notices))
	}
	// The three added by the reads of version 2.1 (F2-3): a stream idle past
	// IdleSpan, a member's ok rate or a reader's broken rate crossing its
	// threshold, and a read's one-line summary.
	for _, typ := range []string{
		"stream s has landed nothing for IdleSpan",
		"a member's ok rate fell below OkRateFloor",
		"a reader's broken rate rose above BrokenRateCeiling",
		"a read of p by r: its one-line summary",
	} {
		if _, ok := Notices[typ]; !ok {
			t.Errorf("the notice %q, added by F2-3, is missing", typ)
		}
	}
	// A notice needs no decision, and a judgment is no notice: the two tables
	// share no type.
	for typ := range Notices {
		if _, ok := Judgments[typ]; ok {
			t.Errorf("%q is in both tables", typ)
		}
	}
	// The notice the design could not apply (9) is not in the table.
	if _, ok := Notices["an operation was abandoned"]; ok {
		t.Errorf("a notice of the retired fence is in the table")
	}
}

// sectionThreeVerbs are the verbs of section 3, by the names its table gives.
var sectionThreeVerbs = []string{
	"init", "add", "release", "start", "stop", "run", "tick", "take", "finish", "ask", "read", "queue", "accept",
	"rework", "return", "drop", "rank", "merge", "resume", "fleet up", "fleet down", "fleet beat", "reader add",
	"ci", "ack", "wait", "inbox", "card", "log", "where", "goal set", "goal show", "goal drop", "play", "check",
	"clear", "teardown", "remove",
}

func TestEveryDecisionIsAVerb(t *testing.T) {
	t.Parallel()
	// The one decision that is no verb of section 3: "the same command with
	// --op <op>" (2.2), whose verb is the op's own, in no snapshot. It is in the
	// table behind a condition that never holds, and its guard refuses it.
	isVerb := func(verb string) bool { return slices.Contains(sectionThreeVerbs, verb) || verb == verbSameCommand }
	for typ, row := range Judgments {
		for _, d := range row.Decisions {
			if !isVerb(d.Verb) {
				t.Errorf("%q offers %q, and %q is no verb of section 3", typ, d.String(), d.Verb)
			}
			if _, ok := verbGuards[d.Verb]; !ok {
				t.Errorf("%q offers %q, and no guard holds %q to its planner", typ, d.String(), d.Verb)
			}
			if d.Verb == verbSameCommand {
				if d.Accepted == nil {
					t.Errorf("%q offers %q in every state, and no snapshot holds the op's verb", typ, d.String())
				} else if ok, _ := d.Accepted(&Snapshot{}, Open{}); ok {
					t.Errorf("%q: the condition of %q holds", typ, d.String())
				}
			}
		}
	}
	for verb := range verbGuards {
		if !isVerb(verb) {
			t.Errorf("a guard for %q, which is no verb of section 3", verb)
		}
	}
	if ok, _ := verbGuards[verbSameCommand](&Snapshot{}, Open{}, Decision{}); ok {
		t.Errorf("the guard of the op's own command accepts it")
	}
	// repair is no verb until layer 1's AL7 and the check land (3).
	if slices.Contains(sectionThreeVerbs, "repair") {
		t.Fatalf("the test's list of verbs holds repair")
	}
}

func TestTickKeptList(t *testing.T) {
	t.Parallel()
	// The conditions a wait holds (2.2, the wait column "holds").
	keeps := []string{
		"cannot ask", "no fleet member is up", "a card reached its bound", "a work card is past its deadline",
		"a read card is past its deadline", "a stream has had no merge step past its deadline", "stalled",
		"the machine could not move a card", "an invariant is broken", "the machine's step was refused",
		"a reminder could not be delivered", "the machine is falling behind",
		"the machine is STOPPED and moves are due", "a verb in parts stopped before its end",
	}
	var got []string
	for typ, row := range Judgments {
		if row.TickKept {
			got = append(got, typ)
		}
	}
	slices.Sort(got)
	slices.Sort(keeps)
	if !slices.Equal(got, keeps) {
		t.Errorf("tick kept %q, want %q", got, keeps)
	}
	// A wait on the rows marked "review" sets a review time, and on "the sprint
	// is done" nothing is ever overdue: none is a condition the tick keeps.
	for _, typ := range []string{
		"sentinel reached", "a primary is blocked on something dropped", "a primary is blocked on something missing",
		"stream stopped: conflict on a card", "stream stopped: stream branch red",
		"stream stopped: needs a card of another stream first", "stream stopped: the merge queue rejected",
		"ci red on a primary", "returned to review", "reads exhausted", "stranded in review", "the sprint is done",
	} {
		if Judgments[typ].TickKept {
			t.Errorf("%q is tick kept", typ)
		}
	}
}

// raise is a judgment of the type open on the subject in the world as J writes
// it: the verbs of the row on it, at the world's time. subject is the open
// key's subject, and mod sets what the note names besides.
func raise(w *world, typ, subject string, mod func(*Note)) Open {
	w.t.Helper()
	row, ok := Judgments[typ]
	if !ok {
		w.t.Fatalf("no row for %q", typ)
	}
	w.seq++
	n := Note{ID: fmt.Sprintf("j%d", w.seq), Kind: Judgment, Type: typ, At: w.s.Now, Count: 1}
	for _, d := range row.Decisions {
		n.Decisions = append(n.Decisions, d.Verb)
	}
	if mod != nil {
		mod(&n)
	}
	o := Open{Key: OpenKey(n.ID, subject), Note: n}
	w.s.Open = append(w.s.Open, o)
	return o
}

// ofStream names a stream-level judgment: its stream, the card it stopped on
// and the cards of its batch.
func judgedStream(stream, card string, primaries ...string) func(*Note) {
	return func(n *Note) {
		n.StreamLevel, n.Stream, n.Card, n.Primaries, n.Count = true, stream, card, primaries, len(primaries)
	}
}

// judgedSprint names a judgment of the whole sprint.
func judgedSprint(n *Note) { n.SprintLevel = true }

// toReview brings a primary of s1 to review, its work done.
func toReview(w *world, id string, failed bool) {
	w.t.Helper()
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{id}}}))
	c := w.s.Fleet.Card(w.s.Work.Card(id).F("work"))
	w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID), Failed: failed, Report: "report"}))
}

// verdicts asks a primary in review and has its readers say what is given, in
// reader order.
func verdicts(w *world, id string, says ...string) {
	w.t.Helper()
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{id}}}))
	for i, rc := range readsAt(w.s, w.s.Work.Card(id), w.s.Work.Card(id).Int("attempt")) {
		if i < len(says) && says[i] != "" {
			w.must(Read(w.s, ReadReq{As: rc.F("reader"), Verdict: says[i], Finding: "finding", Sel: Sel{IDs: []string{rc.ID}}}))
		}
	}
}

// decisionsOf is the printed decisions as command lines.
func decisionsOf(ds []Decision) []string {
	out := []string{}
	for _, d := range ds {
		out = append(out, d.String())
	}
	return out
}

// seeded is a judgment raised on a state its condition holds in, and what the
// inbox prints of it there.
type seeded struct {
	name  string
	typ   string
	build func(t *testing.T) (*world, Open)
	want  []string
}

var seeds = []seeded{
	{"sentinel reached", "sentinel reached", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"g1"}, Sentinel: true}))
		return w, raise(w, "sentinel reached", "g1", judgedStream("s2", "g1"))
	}, []string{"release G --reason", "add --before G", "drop G"}},

	{"blocked on something dropped", "a primary is blocked on something dropped", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "obsolete"}))
		w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"later"}, Needs: []string{"s1-1"}}))
		return w, raise(w, "a primary is blocked on something dropped", "later", func(n *Note) { n.Needs, n.Primaries = []string{"s1-1"}, []string{"later"} })
	}, []string{"ack", "drop w"}},

	{"blocked on something missing", "a primary is blocked on something missing", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"later"}, Needs: []string{"s1-1"}}))
		w.s.Work.Card("later").Fields["needs"] = "ghost"
		return w, raise(w, "a primary is blocked on something missing", "later", func(n *Note) { n.Needs, n.Primaries = []string{"ghost"}, []string{"later"} })
	}, []string{"add <n>", "ack", "drop w"}},

	{"cannot ask", "cannot ask", func(t *testing.T) (*world, Open) {
		w := newWorld(t, "reader-a")
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
		w.must(Add(w.s, AddReq{Stream: "s1", Count: 1}))
		toReview(w, "s1-1", false)
		return w, raise(w, "cannot ask", "s1-1", nil)
	}, []string{"reader add", "rework --fix", "drop", "wait"}},

	{"no fleet member is up", "no fleet member is up", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m1"}))
		w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m2"}))
		return w, raise(w, "no fleet member is up", SprintSubject, judgedSprint)
	}, []string{"fleet beat", "fleet up <m>", "wait"}},

	{"a card reached its bound, in ready", "a card reached its bound", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
		w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m1"}))
		w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m2"}))
		w.s.Fleet.Card("s1-1.w1").Fields["redeals"] = itoa(MaxRedeals)
		return w, raise(w, "a card reached its bound", "s1-1", nil)
	}, []string{"rework --fix", "drop", "wait"}},

	{"a card reached its bound, in review", "a card reached its bound", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", true)
		w.s.Work.Card("s1-1").Fields["bound"] = "attempts"
		return w, raise(w, "a card reached its bound", "s1-1", nil)
	}, []string{"rework --fix", "drop", "wait"}},

	{"a work card is late, taken", "a work card is past its deadline", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReviewTaken(w, "s1-1")
		w.tick(3 * time.Hour)
		return w, raise(w, "a work card is past its deadline", "s1-1.w1", func(n *Note) { n.Card, n.Primaries = "s1-1.w1", []string{"s1-1"} })
	}, []string{"fleet down <member>", "drop <p>", "wait"}},

	{"a read card is late", "a read card is past its deadline", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		verdicts(w, "s1-1")
		w.tick(time.Hour)
		rc := readsAt(w.s, w.s.Work.Card("s1-1"), 1)[0]
		return w, raise(w, "a read card is past its deadline", rc.ID, func(n *Note) { n.Card, n.Primaries = rc.ID, []string{"s1-1"} })
	}, []string{"ask --another <p>", "drop <p>", "wait"}},

	{"no merge step past its deadline", "a stream has had no merge step past its deadline", func(t *testing.T) (*world, Open) {
		w := setup(t, 2)
		accepted(w, "s1-1")
		w.tick(time.Hour)
		return w, raise(w, "a stream has had no merge step past its deadline", StreamSubject("s1"), judgedStream("s1", ""))
	}, []string{"merge --stream s", "card", "wait"}},

	{"stream stopped: conflict", "stream stopped: conflict on a card", func(t *testing.T) (*world, Open) {
		w := setup(t, 3)
		accepted(w, "s1-1", "s1-2", "s1-3")
		w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 2, Conflict: "s1-2"}))
		return w, raise(w, "stream stopped: conflict on a card", StreamSubject("s1"), judgedStream("s1", "s1-2", "s1-2"))
	}, []string{"resume --stream s --did", "drop <card>"}},

	{"stream stopped: red", "stream stopped: stream branch red", func(t *testing.T) (*world, Open) {
		w := setup(t, 3)
		accepted(w, "s1-1", "s1-2", "s1-3")
		w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 2, Red: true, Suspects: []string{"s1-1"}}))
		return w, raise(w, "stream stopped: stream branch red", StreamSubject("s1"), func(n *Note) {
			judgedStream("s1", "", "s1-1", "s1-2")(n)
			n.Suspects = []string{"s1-1"}
		})
	}, []string{"return <suspect>", "resume --did"}},

	{"stream stopped: cross", "stream stopped: needs a card of another stream first", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		w.must(Add(w.s, AddReq{Stream: "s2", Count: 1}))
		accepted(w, "s1-1")
		w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1, Cross: "s1-1=s2-1"}))
		return w, raise(w, "stream stopped: needs a card of another stream first", StreamSubject("s1"), func(n *Note) {
			judgedStream("s1", "s1-1", "s1-1")(n)
			n.Other, n.OtherStream = "s2-1", "s2"
		})
	}, []string{"rank <needed card>", "card", "return <card>", "drop <card>", "wait"}},

	{"stream stopped: rejected", "stream stopped: the merge queue rejected", func(t *testing.T) (*world, Open) {
		w := setup(t, 2)
		accepted(w, "s1-1", "s1-2")
		w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 2, Rejected: true}))
		return w, raise(w, "stream stopped: the merge queue rejected", StreamSubject("s1"), judgedStream("s1", "", "s1-1", "s1-2"))
	}, []string{"resume --did", "return", "drop"}},

	{"ci red, reads stand", "ci red on a primary", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		verdicts(w, "s1-1", "ok", "ok")
		return w, raise(w, "ci red on a primary", "s1-1", nil)
	}, []string{"rework --fix", "accept", "drop", "card", "ack"}},

	{"ci red, reads outstanding", "ci red on a primary", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		verdicts(w, "s1-1")
		return w, raise(w, "ci red on a primary", "s1-1", nil)
	}, []string{"rework --fix", "drop", "card", "ack"}},

	{"returned to review, reads stand", "returned to review", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		accepted(w, "s1-1")
		w.must(Return(w.s, ReturnReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "suspect"}))
		return w, raise(w, "returned to review", "s1-1", nil)
	}, []string{"rework --fix", "accept", "drop"}},

	{"returned to review, the head moved", "returned to review", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		accepted(w, "s1-1")
		w.must(Return(w.s, ReturnReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "suspect"}))
		w.s.Work.Card("s1-1").Fields["head"] = "moved"
		return w, raise(w, "returned to review", "s1-1", nil)
	}, []string{"rework --fix", "drop"}},

	{"reads exhausted, a reader free", "reads exhausted", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		verdicts(w, "s1-1", "ok", "broken")
		return w, raise(w, "reads exhausted", "s1-1", nil)
	}, []string{"ask --another", "rework --fix", "drop"}},

	{"reads exhausted, no reader free", "reads exhausted", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		verdicts(w, "s1-1", "ok", "broken")
		w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}, Another: true}))
		for _, rc := range readsAt(w.s, w.s.Work.Card("s1-1"), 1) {
			if rc.Col == Asked {
				w.must(Read(w.s, ReadReq{As: rc.F("reader"), Verdict: "broken", Finding: "finding", Sel: Sel{IDs: []string{rc.ID}}}))
			}
		}
		return w, raise(w, "reads exhausted", "s1-1", nil)
	}, []string{"rework --fix", "drop"}},

	{"reads exhausted, fifteen read cards", "reads exhausted", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		verdicts(w, "s1-1", "ok", "broken")
		var cards []string
		for i := 1; i <= maxReadCards; i++ {
			cards = append(cards, fmt.Sprintf("s1-1.r%d.reader-a", i))
		}
		w.s.Work.Card("s1-1").Fields["rcards"] = strings.Join(cards, ",")
		return w, raise(w, "reads exhausted", "s1-1", nil)
	}, []string{"rework --fix", "drop"}},

	{"stranded, never asked", "stranded in review", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		return w, raise(w, "stranded in review", "s1-1", nil)
	}, []string{"ask", "rework --fix", "drop"}},

	{"stranded, failed work", "stranded in review", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", true)
		return w, raise(w, "stranded in review", "s1-1", nil)
	}, []string{"rework --fix", "drop"}},

	{"stalled, ready", "stalled", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m1"}))
		w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m2"}))
		return w, raise(w, "stalled", "s1-1", nil)
	}, []string{"fleet up <m>", "card", "drop", "wait"}},

	{"stalled, working", "stalled", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReviewTaken(w, "s1-1")
		return w, raise(w, "stalled", "s1-1", nil)
	}, []string{"fleet down m1", "card", "drop", "wait"}},

	{"stalled, review with two oks", "stalled", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		verdicts(w, "s1-1", "ok", "ok")
		return w, raise(w, "stalled", "s1-1", nil)
	}, []string{"accept", "rework --fix", "card", "drop", "wait"}},

	{"stalled, review never asked", "stalled", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		return w, raise(w, "stalled", "s1-1", nil)
	}, []string{"ask", "rework --fix", "card", "drop", "wait"}},

	{"stalled, review asked", "stalled", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		verdicts(w, "s1-1")
		return w, raise(w, "stalled", "s1-1", nil)
	}, []string{"ask --another", "rework --fix", "card", "drop", "wait"}},

	{"stalled, review asked, every reader used", "stalled", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		verdicts(w, "s1-1")
		w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}, Another: true}))
		return w, raise(w, "stalled", "s1-1", nil)
	}, []string{"rework --fix", "card", "drop", "wait"}},

	{"stalled, merging", "stalled", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		accepted(w, "s1-1")
		return w, raise(w, "stalled", "s1-1", nil)
	}, []string{"return", "card", "drop", "wait"}},

	{"the machine could not move a card", "the machine could not move a card", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		w.s.Work.Card("s1-1").Fields["refused"] = "deal: no room"
		return w, raise(w, "the machine could not move a card", "s1-1", nil)
	}, []string{"ack", "drop", "card", "wait"}},

	{"an invariant is broken", "an invariant is broken", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		return w, raise(w, "an invariant is broken", "s1-1", nil)
	}, []string{"card", "clear --confirm sprint", "wait"}},

	{"the machine's step was refused", "the machine's step was refused", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		return w, raise(w, "the machine's step was refused", "ask:s1-1", nil)
	}, []string{"log --since", "ack", "wait"}},

	{"the sprint is done", "the sprint is done", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		return w, raise(w, "the sprint is done", SprintSubject, judgedSprint)
	}, []string{"clear", "add"}},

	{"a reminder could not be delivered", "a reminder could not be delivered", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		return w, raise(w, "a reminder could not be delivered", StreamSubject("person"), judgedStream("person", ""))
	}, []string{"goal set", "goal drop", "ack"}},

	{"the machine is falling behind", "the machine is falling behind", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		return w, raise(w, "the machine is falling behind", SprintSubject, judgedSprint)
	}, []string{"wait", "where"}},

	{"the machine is STOPPED and moves are due", "the machine is STOPPED and moves are due", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		return w, raise(w, "the machine is STOPPED and moves are due", SprintSubject, judgedSprint)
	}, []string{"start", "wait --for d --reason"}},

	{"a verb in parts stopped before its end", "a verb in parts stopped before its end", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		return w, raise(w, "a verb in parts stopped before its end", StreamSubject("op-1"), judgedStream("op-1", ""))
	}, []string{"wait"}},
}

// conditionSeeds are the states where a condition of a row does not hold: the
// decision it guards is not printed, and what is printed is answerable.
var conditionSeeds = []seeded{
	{"a work card is late, just redealt", "a work card is past its deadline", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
		holder := w.s.Fleet.Card("s1-1.w1").Row
		w.tick(20 * time.Minute)
		w.must(FleetStep(w.s, FleetReq{Op: "down", Member: holder}))
		return w, raise(w, "a work card is past its deadline", "s1-1.w1", func(n *Note) { n.Card, n.Primaries = "s1-1.w1", []string{"s1-1"} })
	}, []string{"drop <p>", "wait"}},

	{"a work card is late, its member is down", "a work card is past its deadline", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReviewTaken(w, "s1-1")
		w.tick(3 * time.Hour)
		w.s.MemberCtl(w.s.Fleet.Card("s1-1.w1").Row).Fields["status"] = Down
		return w, raise(w, "a work card is past its deadline", "s1-1.w1", func(n *Note) { n.Card, n.Primaries = "s1-1.w1", []string{"s1-1"} })
	}, []string{"drop <p>", "wait"}},

	{"a work card is late, withdrawn", "a work card is past its deadline", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
		w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m1"}))
		w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m2"}))
		w.tick(3 * time.Hour)
		return w, raise(w, "a work card is past its deadline", "s1-1.w1", func(n *Note) { n.Card, n.Primaries = "s1-1.w1", []string{"s1-1"} })
	}, []string{"drop <p>", "wait"}},

	{"a read card is late, every reader has read", "a read card is past its deadline", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		verdicts(w, "s1-1")
		w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}, Another: true}))
		w.tick(time.Hour)
		rc := readsAt(w.s, w.s.Work.Card("s1-1"), 1)[0]
		return w, raise(w, "a read card is past its deadline", rc.ID, func(n *Note) { n.Card, n.Primaries = rc.ID, []string{"s1-1"} })
	}, []string{"drop <p>", "wait"}},

	{"no merge step past its deadline, the stream is stopped", "a stream has had no merge step past its deadline", func(t *testing.T) (*world, Open) {
		w := setup(t, 2)
		accepted(w, "s1-1", "s1-2")
		w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1, Conflict: "s1-1"}))
		return w, raise(w, "a stream has had no merge step past its deadline", StreamSubject("s1"), judgedStream("s1", ""))
	}, []string{"card", "wait"}},

	// merge is refused for a stream that landed, whatever is queued in it (a hand
	// seed: the present build does not make a landed stream with a card queued).
	{"no merge step past its deadline, the stream landed with a card queued", "a stream has had no merge step past its deadline", func(t *testing.T) (*world, Open) {
		w := setup(t, 2)
		accepted(w, "s1-1")
		w.s.StreamCtl("s1").Fields["state"] = StreamLanded
		if w.s.Merge.Count("s1", Queued) == 0 {
			t.Fatalf("nothing is queued in stream s1")
		}
		return w, raise(w, "a stream has had no merge step past its deadline", StreamSubject("s1"), judgedStream("s1", ""))
	}, []string{"card", "wait"}},

	{"a card reached its bound, in neither", "a card reached its bound", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		return w, raise(w, "a card reached its bound", "s1-1", nil)
	}, []string{"drop", "wait"}},

	{"blocked on something missing, its need has a record", "a primary is blocked on something missing", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"later"}, Needs: []string{"s1-1"}}))
		return w, raise(w, "a primary is blocked on something missing", "later", func(n *Note) { n.Needs, n.Primaries = []string{"s1-1"}, []string{"later"} })
	}, []string{"drop w"}},

	{"blocked on something missing, its need was waived", "a primary is blocked on something missing", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"later"}, Needs: []string{"s1-1"}}))
		w.s.Work.Card("later").Fields["needs"] = "ghost"
		w.s.Work.Card("later").Fields["waived"] = "ghost"
		return w, raise(w, "a primary is blocked on something missing", "later", func(n *Note) { n.Needs, n.Primaries = []string{"ghost"}, []string{"later"} })
	}, []string{"drop w"}},

	{"sentinel reached, no score before it", "sentinel reached", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"g1"}, Sentinel: true}))
		// a landed card one representable score before the sentinel: nothing
		// is open before it, and no score lies between the two
		w.s.Work.Put(&Card{ID: "tight", Row: "s2", Col: Landed, Score: math.Nextafter(w.s.Work.Card("g1").Score, math.Inf(-1)),
			Fields: map[string]string{"kind": "primary", "stream": "s2", "attempt": "0"}})
		return w, raise(w, "sentinel reached", "g1", judgedStream("s2", "g1"))
	}, []string{"release G --reason", "drop G"}},

	{"blocked on something missing, its need cannot be named", "a primary is blocked on something missing", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"later"}, Needs: []string{"s1-1"}}))
		w.s.Work.Card("later").Fields["needs"] = "not.a.card"
		return w, raise(w, "a primary is blocked on something missing", "later", func(n *Note) { n.Needs, n.Primaries = []string{"not.a.card"}, []string{"later"} })
	}, []string{"ack", "drop w"}},

	{"reads exhausted, its work failed", "reads exhausted", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", true)
		w.s.Readers.Put(&Card{ID: ReadCardID("s1-1", 1, "reader-a"), Row: "reader-a", Col: Broken, Fields: map[string]string{
			"kind": "read", "primary": "s1-1", "reader": "reader-a", "attempt": "1", "finding": "finding"}})
		return w, raise(w, "reads exhausted", "s1-1", nil)
	}, []string{"rework --fix", "drop"}},

	{"reads exhausted, never asked", "reads exhausted", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		return w, raise(w, "reads exhausted", "s1-1", nil)
	}, []string{"rework --fix", "drop"}},

	{"stranded, already asked", "stranded in review", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		verdicts(w, "s1-1")
		return w, raise(w, "stranded in review", "s1-1", nil)
	}, []string{"rework --fix", "drop"}},

	{"no merge step past its deadline, nothing queued", "a stream has had no merge step past its deadline", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		return w, raise(w, "a stream has had no merge step past its deadline", StreamSubject("s1"), judgedStream("s1", ""))
	}, []string{"card", "wait"}},

	{"stalled, waiting", "stalled", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"later"}, Needs: []string{"s1-1"}}))
		return w, raise(w, "stalled", "later", nil)
	}, []string{"card", "drop", "wait"}},

	{"stalled, a sentinel reached", "stalled", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"g1"}, Sentinel: true}))
		return w, raise(w, "stalled", "g1", nil)
	}, []string{"release G --reason", "card", "drop", "wait"}},
}

// toReviewTaken brings a primary of s1 to working: dealt and taken.
func toReviewTaken(w *world, id string) {
	w.t.Helper()
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{id}}}))
	c := w.s.Fleet.Card(w.s.Work.Card(id).F("work"))
	w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
}

func TestAnswerableSeeded(t *testing.T) {
	t.Parallel()
	covered := map[string]bool{}
	for _, sd := range allSeeds() {
		covered[sd.typ] = true
		t.Run(sd.name, func(t *testing.T) {
			t.Parallel()
			w, o := sd.build(t)
			if got := decisionsOf(Printed(w.s, o)); !slices.Equal(got, sd.want) {
				t.Fatalf("printed %q, want %q", got, sd.want)
			}
			if v := Answerable(w.s, w.s.Open); len(v) != 0 {
				t.Fatalf("a decision printed is one its verb refuses: %v", v)
			}
		})
	}
	// Every type of 2.2 is raised on a seeded state.
	for typ := range Judgments {
		if !covered[typ] {
			t.Errorf("no seeded state raises %q", typ)
		}
	}
}

// seedNamed builds the seeded state of that name.
func seedNamed(t *testing.T, name string) (*world, Open) {
	t.Helper()
	for _, sd := range allSeeds() {
		if sd.name == name {
			return sd.build(t)
		}
	}
	t.Fatalf("no seeded state named %q", name)
	return nil, Open{}
}

// withRow is Judgments with the row of the type edited: a changed row.
func withRow(typ string, edit func(*JudgmentType)) map[string]JudgmentType {
	out := make(map[string]JudgmentType, len(Judgments))
	for k, v := range Judgments {
		out[k] = v
	}
	row := out[typ]
	row.Decisions = slices.Clone(row.Decisions)
	edit(&row)
	out[typ] = row
	return out
}

func offers(row JudgmentType, verb string) bool {
	for _, d := range row.Decisions {
		if d.Verb == verb {
			return true
		}
	}
	return false
}

// W21: the late-work judgment offers rework. It offers none (2.2), and a row
// that offered it, in every state, would be caught by Answerable: rework
// refuses a card that is not in review.
func TestLateWorkOffersNoRework(t *testing.T) {
	t.Parallel()
	const late = "a work card is past its deadline"
	if offers(Judgments[late], "rework") {
		t.Fatalf("the late-work judgment offers rework: %v", Judgments[late].Decisions)
	}
	for _, name := range []string{"a work card is late, taken", "a work card is late, just redealt", "a work card is late, withdrawn"} {
		w, o := seedNamed(t, name)
		if got := decisionsOf(Printed(w.s, o)); slices.ContainsFunc(got, func(d string) bool { return strings.HasPrefix(d, "rework") }) {
			t.Errorf("%s: printed %q", name, got)
		}
	}
	w, o := seedNamed(t, "a work card is late, taken")
	witness := withRow(late, func(r *JudgmentType) { r.Decisions = append(r.Decisions, decide("rework", "--fix")) })
	v := answerableIn(witness, w.s, []Open{o})
	if len(v) != 1 || v[0].Rule != RuleAnswerable || !strings.Contains(v[0].Detail, `offers "rework --fix"`) {
		t.Fatalf("a row that offers rework on a card that is working: %v", v)
	}
}

// Until layer 1's repair entry (AL7) and the check (IT26) land, "an invariant
// is broken" offers clear as the last resort and never repair.
func TestInvariantBrokenOffersClearNotRepair(t *testing.T) {
	t.Parallel()
	const broken = "an invariant is broken"
	if !offers(Judgments[broken], "clear") {
		t.Fatalf("the invariant judgment does not offer clear: %v", Judgments[broken].Decisions)
	}
	for typ, row := range Judgments {
		if offers(row, "repair") {
			t.Errorf("%q offers repair before AL7", typ)
		}
	}
	w, o := seedNamed(t, "an invariant is broken")
	got := decisionsOf(Printed(w.s, o))
	if !slices.Contains(got, "clear --confirm sprint") || slices.Contains(got, "repair") {
		t.Fatalf("printed %q", got)
	}
	// repair is no verb yet: a row that offered it is caught, by Answerable,
	// as a decision no verb accepts.
	witness := withRow(broken, func(r *JudgmentType) { r.Decisions = append(r.Decisions, decide("repair")) })
	if v := answerableIn(witness, w.s, []Open{o}); len(v) != 1 || !strings.Contains(v[0].Detail, `"repair" has no guard`) {
		t.Fatalf("a row that offers repair: %v", v)
	}
}

// The reversed witnesses of Answerable: each change to a row, offering a
// decision where its verb refuses it, must be found.
func TestAnswerableWitnesses(t *testing.T) {
	t.Parallel()
	unconditional := func(verb string, args ...string) func(*JudgmentType) {
		return func(r *JudgmentType) {
			r.Decisions = slices.DeleteFunc(r.Decisions, func(d Decision) bool { return d.Verb == verb })
			r.Decisions = append(r.Decisions, decide(verb, args...))
		}
	}
	for _, tt := range []struct {
		name, seed, typ string
		edit            func(*JudgmentType)
		refused         string
	}{
		{"accept of a red ci with its reads outstanding", "ci red, reads outstanding", "ci red on a primary",
			unconditional("accept"), `offers "accept": accept refuses s1-1`},
		{"ask --another with every reader used", "reads exhausted, no reader free", "reads exhausted",
			unconditional("ask", "--another"), `offers "ask --another"`},
		{"rework of the card a stream stopped on", "stream stopped: conflict", "stream stopped: conflict on a card",
			unconditional("rework", "<card>"), `offers "rework <card>": rework refuses s1-2: merging: return it first`},
		{"rework of a suspect in merging", "stream stopped: red", "stream stopped: stream branch red",
			unconditional("rework", "<suspect>"), `offers "rework <suspect>"`},
		{"add of a need that has a record", "blocked on something missing, its need has a record", "a primary is blocked on something missing",
			unconditional("add", "<n>"), `offers "add <n>": later names no need that has no record and a name a card can have`},
		{"ack of a judgment ack does not answer", "cannot ask", "cannot ask",
			func(r *JudgmentType) { r.Decisions = append(r.Decisions, decide("ack")) }, `offers "ack"`},
		{"remove, refused until AL6", "the sprint is done", "the sprint is done",
			func(r *JudgmentType) { r.Decisions = append(r.Decisions, decide("remove", "--abort", "--op", "<op>")) }, "AL6"},
		{"merge of a stream with nothing queued", "no merge step past its deadline, the stream is stopped", "a stream has had no merge step past its deadline",
			unconditional("merge", "--stream", "s"), `offers "merge --stream s"`},
		{"ask of a primary that failed", "stranded, failed work", "stranded in review",
			unconditional("ask"), `offers "ask"`},
		{"add --before with no score between", "sentinel reached, no score before it", "sentinel reached",
			unconditional("add", "--before", "G"), `offers "add --before G": add refuses probe: no score lies between`},
		{"add of a need no card can be named for", "blocked on something missing, its need cannot be named", "a primary is blocked on something missing",
			unconditional("add", "<n>"), `offers "add <n>": later names no need that has no record and a name a card can have`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w, o := seedNamed(t, tt.seed)
			if v := Answerable(w.s, []Open{o}); len(v) != 0 {
				t.Fatalf("the table itself is not answerable here: %v", v)
			}
			v := answerableIn(withRow(tt.typ, tt.edit), w.s, []Open{o})
			if len(v) != 1 || v[0].Rule != RuleAnswerable || !strings.Contains(v[0].Detail, tt.refused) {
				t.Fatalf("the changed row: %v, want one violation with %q", v, tt.refused)
			}
		})
	}
}

// A judgment the machine left open after its state moved on prints a
// decision its verb refuses: release of a sentinel that has a card before it
// again. Answerable is what names it.
func TestAnswerableFlagsAStaleJudgment(t *testing.T) {
	t.Parallel()
	w, o := seedNamed(t, "sentinel reached")
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"first"}, Before: "g1"}))
	v := Answerable(w.s, []Open{o})
	if len(v) != 1 || v[0].Rule != RuleAnswerable || !strings.Contains(v[0].Detail, `offers "release G --reason": release refuses g1`) {
		t.Fatalf("a sentinel with a card before it: %v", v)
	}
}

// A judgment left open after its state moved on prints decisions its verbs
// refuse: Answerable names each, by the verb that refuses it. move is what
// moved the state on, and gives the judgment as it is then.
func TestAnswerableFlagsStaleJudgments(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, seed string
		move       func(w *world, o Open) Open
		edit       func() map[string]JudgmentType
		refused    []string
	}{
		{"a card dropped since", "cannot ask",
			func(w *world, o Open) Open {
				w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "obsolete"}))
				return o
			}, nil,
			[]string{`offers "rework --fix": rework refuses s1-1`, `offers "drop": drop refuses s1-1`}},
		{"a stream resumed since", "stream stopped: conflict",
			func(w *world, o Open) Open {
				w.must(Resume(w.s, ResumeReq{Stream: "s1", Did: "fixed"}))
				return o
			}, nil,
			[]string{`offers "resume --stream s --did": resume refuses s1: not stopped`}},
		{"no coordinator", "the machine is STOPPED and moves are due",
			func(w *world, o Open) Open { w.s.Coordinator = ""; return o }, nil,
			[]string{`offers "wait --for d --reason": wait answers a judgment, and the sprint has no coordinator`}},
		{"a stream that names no card", "stream stopped: rejected",
			func(w *world, o Open) Open { o.Note.Primaries, o.Note.Card = nil, ""; return o },
			func() map[string]JudgmentType {
				return withRow("stream stopped: the merge queue rejected", func(r *JudgmentType) {
					r.Decisions = []Decision{decide("return"), decide("drop")}
				})
			},
			[]string{`offers "return": the judgment names no card to return`, `offers "drop": the judgment names no card to drop`}},
		{"a work card no member holds", "a work card is late, taken",
			func(w *world, o Open) Open {
				return raise(w, "a work card is past its deadline", "ghost.w1", func(n *Note) { n.Card, n.Primaries = "ghost.w1", []string{"ghost"} })
			},
			func() map[string]JudgmentType {
				return withRow("a work card is past its deadline", func(r *JudgmentType) {
					r.Decisions = []Decision{decide("fleet down", "<member>")}
				})
			},
			[]string{`offers "fleet down <member>": no member holds ghost.w1`}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w, o := seedNamed(t, tt.seed)
			o = tt.move(w, o)
			table := Judgments
			if tt.edit != nil {
				table = tt.edit()
			}
			v := answerableIn(table, w.s, []Open{o})
			if len(v) != len(tt.refused) {
				t.Fatalf("%d violations, want %d: %v", len(v), len(tt.refused), v)
			}
			for _, want := range tt.refused {
				if !slices.ContainsFunc(v, func(x Violation) bool { return x.Rule == RuleAnswerable && strings.Contains(x.Detail, want) }) {
					t.Errorf("no violation with %q in %v", want, v)
				}
			}
		})
	}
}

// Answerable holds the judgments it is given to their verbs, whatever the
// snapshot lists as open: the planners read the ones given.
func TestAnswerableReadsTheOpenGiven(t *testing.T) {
	t.Parallel()
	w, o := seedNamed(t, "the machine's step was refused")
	if !slices.Contains(decisionsOf(Printed(w.s, o)), "ack") {
		t.Fatalf("the state offers no ack, which reads the judgment from the snapshot")
	}
	w.s.Open = nil
	if v := Answerable(w.s, []Open{o}); len(v) != 0 {
		t.Fatalf("a judgment given, and not in the snapshot's open: %v", v)
	}
	if len(w.s.Open) != 0 {
		t.Fatalf("Answerable changed the snapshot's open judgments: %v", w.s.Open)
	}
}

// cardsOf: what a decision acts on.
func TestCardsOf(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		open Open
		want []string
	}{
		{"a primary", Open{Key: OpenKey("j1", "s1-1"), Note: Note{ID: "j1"}}, []string{"s1-1"}},
		{"a work card", Open{Key: OpenKey("j1", "s1-1.w2"), Note: Note{ID: "j1"}}, []string{"s1-1"}},
		{"a read card", Open{Key: OpenKey("j1", "s1-1.r1.reader-a"), Note: Note{ID: "j1"}}, []string{"s1-1"}},
		{"the sprint", Open{Key: OpenKey("j1", SprintSubject), Note: Note{ID: "j1", SprintLevel: true}}, nil},
		{"a stream: the card it stopped on", Open{Key: OpenKey("j1", StreamSubject("s1")),
			Note: Note{ID: "j1", StreamLevel: true, Card: "s1-2", Suspects: []string{"s1-3"}, Primaries: []string{"s1-2", "s1-3", "s1-4"}}}, []string{"s1-2"}},
		{"a stream: the suspects", Open{Key: OpenKey("j1", StreamSubject("s1")),
			Note: Note{ID: "j1", StreamLevel: true, Suspects: []string{"s1-3"}, Primaries: []string{"s1-2", "s1-3", "s1-4"}}}, []string{"s1-3"}},
		{"a stream: the batch", Open{Key: OpenKey("j1", StreamSubject("s1")),
			Note: Note{ID: "j1", StreamLevel: true, Primaries: []string{"s1-2", "s1-3"}}}, []string{"s1-2", "s1-3"}},
	} {
		if got := cardsOf(tt.open); !slices.Equal(got, tt.want) {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

// Printed prints for a judgment of the table only.
func TestPrintedIsForJudgmentsOfTheTable(t *testing.T) {
	t.Parallel()
	w, o := seedNamed(t, "sentinel reached")
	if len(Printed(w.s, o)) == 0 {
		t.Fatalf("nothing printed for a judgment of the table")
	}
	for _, mod := range []func(*Open){
		func(o *Open) { o.Note.Kind = Happened },
		func(o *Open) { o.Note.Kind = Acknowledged },
		func(o *Open) { o.Note.Type = "no such judgment" },
	} {
		x := o
		mod(&x)
		if got := Printed(w.s, x); len(got) != 0 {
			t.Errorf("printed %q for %+v", decisionsOf(got), x.Note)
		}
	}
	if v := Answerable(w.s, []Open{{Key: OpenKey("j9", "g1"), Note: Note{ID: "j9", Kind: Happened, Type: "batch landed"}}}); len(v) != 0 {
		t.Errorf("a notice is no judgment: %v", v)
	}
}

// Every unconditional decision of a row is printed whatever the state, and
// every conditional one only where its condition holds: the printed list is
// the row's list less the decisions whose condition is false.
func TestPrintedIsTheRowLessWhatItsConditionsRefuse(t *testing.T) {
	t.Parallel()
	for _, sd := range allSeeds() {
		if sd.typ == "stalled" {
			continue
		}
		t.Run(sd.name, func(t *testing.T) {
			t.Parallel()
			w, o := sd.build(t)
			var want []string
			for _, d := range Judgments[sd.typ].Decisions {
				if d.Accepted == nil {
					want = append(want, d.String())
					continue
				}
				if ok, why := d.Accepted(w.s, o); ok {
					want = append(want, d.String())
				} else if why == "" {
					t.Errorf("%q: a condition that does not hold says nothing", d.String())
				}
			}
			if got := decisionsOf(Printed(w.s, o)); !slices.Equal(got, want) {
				t.Fatalf("printed %q, the row less its refused conditions %q", got, want)
			}
		})
	}
}

// benchmarkSnapshot is a snapshot of n primaries in review, each with a work
// card taken long ago at an up member and read cards of three readers, and
// the streams and fleet to judge them against.
func benchmarkSnapshot(n int) *Snapshot {
	s := &Snapshot{Now: t0.Add(24 * time.Hour), Work: NewTable(Work), Readers: NewTable(Readers), Merge: NewTable(Merge), Fleet: NewTable(Fleet),
		Coordinator: "coordinator", Actor: "coordinator"}
	s.Readers.SetRows([]string{"reader-a", "reader-b", "reader-c"})
	s.Fleet.SetRows([]string{"m1", "m2"})
	s.Work.SetRows([]string{"s1"})
	s.Merge.SetRows([]string{"s1"})
	for _, m := range s.Fleet.Rows() {
		s.Fleet.Put(&Card{ID: CtlID(m), Row: m, Col: Ctl, Fields: map[string]string{"kind": "member", "status": Up}})
	}
	s.Merge.Put(&Card{ID: CtlID("s1"), Row: "s1", Col: Ctl, Fields: map[string]string{"kind": "stream", "state": StreamMerging}})
	stamped := stamp(t0)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("p%04d", i)
		s.Work.Put(&Card{ID: id, Row: "s1", Col: Review, Score: float64(i + 1), Fields: map[string]string{
			"kind": "primary", "stream": "s1", "attempt": "1", "result": "ok", "head": "h", "needs": "ghost"}})
		s.Work.Put(&Card{ID: id + "-w", Row: "s1", Col: Waiting, Score: float64(n + i + 1), Fields: map[string]string{"kind": "primary", "stream": "s1", "needs": "ghost"}})
		s.Fleet.Put(&Card{ID: WorkCardID(id, 1), Row: "m1", Col: Working, Score: float64(i + 1), Fields: map[string]string{
			"kind": "work", "primary": id, "attempt": "1", "gen": "1", "first_taken": stamped, "taken": stamped}})
		for j, r := range s.Readers.Rows() {
			if i%3 == 2 && j == 2 {
				continue
			}
			col := OK
			if i%3 == 1 && j > 0 {
				col = Asked
			}
			s.Readers.Put(&Card{ID: ReadCardID(id, 1, r), Row: r, Col: col, Score: float64(i + 1), Fields: map[string]string{
				"kind": "read", "primary": id, "reader": r, "attempt": "1", "head": "h", "asked": stamped}})
		}
	}
	s.Work.Put(&Card{ID: "g1", Row: "s1", Col: Waiting, Score: 0.5, Fields: map[string]string{"kind": "sentinel", "stream": "s1", "reached": stamped}})
	return s
}

// benchmarkOpen is the n open judgments: every type of the table that reads a
// snapshot, over the primaries of the snapshot; the stream judgments name
// cards, as merge's note does.
func benchmarkOpen(primaries, n int) []Open {
	types := []string{
		"cannot ask", "a card reached its bound", "a work card is past its deadline", "a read card is past its deadline",
		"stream stopped: conflict on a card", "ci red on a primary", "returned to review", "reads exhausted",
		"stranded in review", "stalled", "the machine could not move a card", "a primary is blocked on something missing",
		"an invariant is broken", "a stream has had no merge step past its deadline",
		"stream stopped: stream branch red", "stream stopped: needs a card of another stream first", "stream stopped: the merge queue rejected",
	}
	var out []Open
	for i := 0; i < n; i++ {
		typ := types[i%len(types)]
		id := fmt.Sprintf("p%04d", i%primaries)
		next := fmt.Sprintf("p%04d", (i+1)%primaries)
		subject := id
		note := Note{ID: fmt.Sprintf("j%d", i), Kind: Judgment, Type: typ, Stream: "s1", Count: 1}
		switch typ {
		case "a work card is past its deadline":
			subject = WorkCardID(id, 1)
		case "a read card is past its deadline":
			subject = ReadCardID(id, 1, "reader-a")
		case "a stream has had no merge step past its deadline":
			subject = StreamSubject("s1")
		case "stream stopped: conflict on a card":
			subject, note.StreamLevel, note.Card, note.Primaries = StreamSubject("s1"), true, id, []string{id}
		case "stream stopped: stream branch red":
			subject, note.StreamLevel, note.Suspects, note.Primaries = StreamSubject("s1"), true, []string{id}, []string{id, next}
		case "stream stopped: needs a card of another stream first":
			subject, note.StreamLevel, note.Card, note.Other, note.Primaries = StreamSubject("s1"), true, id, next, []string{id, next}
		case "stream stopped: the merge queue rejected":
			subject, note.StreamLevel, note.Primaries = StreamSubject("s1"), true, []string{id, next}
		}
		out = append(out, Open{Key: OpenKey(note.ID, subject), Note: note})
	}
	return out
}

// The size of the benchmark: the 1,000 open judgments of the limit (8.1,
// IT06). The primaries they name are at most 1,000, one each, and that is the
// most cards a snapshot loaded for them holds (about seven for each primary:
// the primary, its work card, its read cards and a card of the stream); the
// snapshot the inbox loads is that one, not the sprint's whole table.
const benchOpenJudgments = 1000

// BenchmarkPrinted1000 is the limit of IT06: Printed over 1,000 open
// judgments in at most 10 ms of Go time (8.1, IT06), on a snapshot the inbox
// has just loaded: each run makes a fresh one (the timer is stopped while it
// is built), so the first read of each table builds its index inside the time
// measured. The sizes are 500 primaries (two judgments each), the most the
// 1,000 judgments can name (1,000 primaries), and 5,000, which is a whole
// sprint's table with the judgments naming a fifth of it: the index grows with
// the snapshot, so the time does, at about a microsecond a primary.
func BenchmarkPrinted1000(b *testing.B) {
	for _, primaries := range []int{500, 1000, 5000} {
		b.Run(fmt.Sprintf("primaries=%d", primaries), func(b *testing.B) {
			open := benchmarkOpen(primaries, benchOpenJudgments)
			b.ReportAllocs()
			printed := 0
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				s := benchmarkSnapshot(primaries)
				s.Open = open
				b.StartTimer()
				printed = 0
				for _, o := range open {
					printed += len(Printed(s, o))
				}
			}
			if printed == 0 {
				b.Fatalf("nothing printed: the benchmark measures nothing")
			}
		})
	}
}

// benchSink keeps a benchmark's result from being optimised away.
var benchSink int

// BenchmarkAnswerable1000 measures Answerable over the same 1,000 open
// judgments, on a snapshot just loaded. The design gives it no limit (each
// decision runs the planner of its verb, over the whole snapshot): the number
// is for IT26's check and for any gate that runs it on a large state.
func BenchmarkAnswerable1000(b *testing.B) {
	for _, primaries := range []int{500, 1000, 5000} {
		b.Run(fmt.Sprintf("primaries=%d", primaries), func(b *testing.B) {
			open := benchmarkOpen(primaries, benchOpenJudgments)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				s := benchmarkSnapshot(primaries)
				b.StartTimer()
				benchSink += len(Answerable(s, open))
			}
		})
	}
}

// A snapshot that holds no table prints what needs no state, and Answerable
// says what it lacks; neither reads a table that is not there.
func TestPrintedAndAnswerableOnASnapshotWithoutTables(t *testing.T) {
	t.Parallel()
	empty := &Snapshot{Coordinator: "coordinator", Actor: "coordinator"}
	var opens []Open
	for typ, row := range Judgments {
		o := Open{Key: OpenKey("j-"+typ, "x"), Note: Note{ID: "j-" + typ, Kind: Judgment, Type: typ}}
		opens = append(opens, o)
		for _, d := range Printed(empty, o) {
			if !slices.ContainsFunc(row.Decisions, func(r Decision) bool { return r.Accepted == nil && r.String() == d.String() }) {
				t.Errorf("%q: printed %q on a snapshot with no table, and it is not an unconditional decision of its row", typ, d.String())
			}
		}
	}
	// A snapshot with the work table alone: the conditions and the place of a
	// stalled card read the other tables only where they are loaded.
	workOnly := &Snapshot{Work: NewTable(Work), Coordinator: "coordinator", Actor: "coordinator"}
	for _, col := range []string{Waiting, Ready, Working, Review, Merging} {
		workOnly.Work.Put(&Card{ID: "c-" + col, Row: "s1", Col: col, Score: 1, Fields: map[string]string{"kind": "primary", "attempt": "1", "result": "ok", "bound": "attempts"}})
		for typ := range Judgments {
			Printed(workOnly, Open{Key: OpenKey("j", "c-"+col), Note: Note{ID: "j", Kind: Judgment, Type: typ}})
		}
	}
	v := Answerable(empty, opens)
	if len(v) != 4 {
		t.Fatalf("a snapshot with no table: %v", v)
	}
	for _, x := range v {
		if x.Rule != RuleAnswerable || !strings.Contains(x.Detail, "holds no") {
			t.Errorf("violation: %v", x)
		}
	}
}

package sprint

import (
	"slices"
	"testing"
)

// The DROPPING clause of Printed and Answerable: errata 3 to version 2.1, H8,
// as amended; dropcond in tla/SprintEvents.tla. A decision whose verb DROPPING
// refuses on a card of a stream being dropped is not printed, and is printed
// again for the same card when another stream is being dropped.

// droppingCase is one decision of a seeded judgment, the stream a card it
// changes is in, and whether a drop of that stream leaves it out.
type droppingCase struct {
	name     string
	seed     string
	decision string
	stream   string
	left     bool
}

var droppingCases = []droppingCase{
	// the model's verbs (dropcond)
	{"drop", "blocked on something dropped", "drop w", "s2", true},
	{"rework", "ci red, reads stand", "rework --fix", "s1", true},
	{"release", "sentinel reached", "release G --reason", "s2", true},
	{"land, merge here", "no merge step past its deadline", "merge --stream s", "s1", true},
	{"ack of blocked on something dropped", "blocked on something dropped", "ack", "s2", true},
	{"ack of blocked on something missing", "blocked on something missing", "ack", "s2", true},
	{"ack of the machine could not move a card", "the machine could not move a card", "ack", "s1", true},
	{"add n, n's stream", "blocked on something missing", "add <n>", "s2", true},
	// the verbs of this package that change a card and are not in the model
	{"accept", "ci red, reads stand", "accept", "s1", true},
	{"return", "stalled, merging", "return", "s1", true},
	{"ask", "stranded, never asked", "ask", "s1", true},
	{"ask --another", "reads exhausted, a reader free", "ask --another", "s1", true},
	{"rank of the needed card, its stream", "stream stopped: cross", "rank <needed card>", "s2", true},
	{"add --before the sentinel", "sentinel reached", "add --before G", "s2", true},
	{"resume", "stream stopped: conflict", "resume --stream s --did", "s1", true},
	// what DROPPING does not refuse stays printed
	{"ack of ci red closes a note alone", "ci red, reads stand", "ack", "s1", false},
	{"card", "ci red, reads stand", "card", "s1", false},
	{"wait", "the machine could not move a card", "wait", "s1", false},
	{"fleet down", "stalled, working", "fleet down m1", "s1", false},
	{"a cross stop's own cards, the needed card's stream dropping", "stream stopped: cross", "return <card>", "s2", false},
}

func TestPrintedLeavesOutWhatDroppingRefuses(t *testing.T) {
	t.Parallel()
	for _, tc := range droppingCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// the same verb on a card of a stream not being dropped: printed
			w, o := seedNamed(t, tc.seed)
			w.s.Dropping = map[string]string{"s9": "op-9"}
			if got := decisionsOf(Printed(w.s, o)); !slices.Contains(got, tc.decision) {
				t.Fatalf("with only s9 being dropped, printed %q, want %q in it", got, tc.decision)
			}
			if v := Answerable(w.s, w.s.Open); len(v) != 0 {
				t.Fatalf("with only s9 being dropped: %v", v)
			}
			// the card's stream being dropped
			w.s.Dropping[tc.stream] = "op-1"
			got := decisionsOf(Printed(w.s, o))
			if slices.Contains(got, tc.decision) == tc.left {
				t.Fatalf("with %s being dropped, printed %q: %q left out is %v, want %v", tc.stream, got, tc.decision, !tc.left, tc.left)
			}
			if v := Answerable(w.s, w.s.Open); len(v) != 0 {
				t.Fatalf("with %s being dropped: %v", tc.stream, v)
			}
		})
	}
}

// add n is left out while n's stream is being dropped, and printed while
// another is: n is created in its waiter's stream (Add).
func TestPrintedAddNWhileNsStreamIsDropping(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		dropping string
		printed  bool
	}{{"s2", false}, {"s1", true}} {
		t.Run(tc.dropping, func(t *testing.T) {
			t.Parallel()
			w, o := seedNamed(t, "blocked on something missing")
			if row := w.s.Work.Card("later").Row; row != "s2" {
				t.Fatalf("the waiter is in %q, want s2", row)
			}
			w.s.Dropping = map[string]string{tc.dropping: "op-1"}
			if got := decisionsOf(Printed(w.s, o)); slices.Contains(got, "add <n>") != tc.printed {
				t.Fatalf("with %s being dropped, printed %q: add <n> printed is %v, want %v", tc.dropping, got, !tc.printed, tc.printed)
			}
		})
	}
}

// Every seeded judgment, with both of its streams being dropped: what is
// printed is what was printed less the decisions that change a card, and
// Answerable holds. The op's own decisions and the sprint's are not on a card
// of a stream.
func TestPrintedWithEveryStreamDropping(t *testing.T) {
	t.Parallel()
	for _, sd := range allSeeds() {
		t.Run(sd.name, func(t *testing.T) {
			t.Parallel()
			w, o := sd.build(t)
			before := decisionsOf(Printed(w.s, o))
			w.s.Dropping = map[string]string{"s1": "op-1", "s2": "op-2"}
			row := Judgments[o.Note.Type]
			var want []string
			for _, d := range printedIn(Judgments, withoutDropping(w.s), o) {
				changes := changesACard[d.Verb] || (d.Verb == "ack" && ackChangesACard[row.Type])
				if !changes || row.Subject == subjOp || o.Note.SprintLevel {
					want = append(want, d.String())
				}
			}
			if got := decisionsOf(Printed(w.s, o)); !slices.Equal(got, want) {
				t.Fatalf("printed %q with s1 and s2 being dropped (%q before), want %q", got, before, want)
			}
			if v := Answerable(w.s, w.s.Open); len(v) != 0 {
				t.Fatalf("with s1 and s2 being dropped: %v", v)
			}
		})
	}
}

// withoutDropping is the snapshot with no stream being dropped.
func withoutDropping(s *Snapshot) *Snapshot {
	at := *s
	at.Dropping = nil
	return &at
}

// A snapshot loaded from a read plan carries the marks its read asked for;
// one whose read did not ask for them says no stream is dropping.
func TestStreamDroppingReadsTheMarksAsked(t *testing.T) {
	t.Parallel()
	whole := &Snapshot{Dropping: map[string]string{"s1": "op-1"}}
	if !streamDropping(whole, "s1") || streamDropping(whole, "s2") || streamDropping(whole, "") {
		t.Fatalf("a whole snapshot's marks: s1 dropping %v, s2 %v", streamDropping(whole, "s1"), streamDropping(whole, "s2"))
	}
	asked := &Snapshot{Partial: &Partial{}}
	pos := asked.Partial.position()
	pos.askedDropping, pos.dropping["s1"] = true, true
	if !streamDropping(asked, "s1") || streamDropping(asked, "s2") {
		t.Fatalf("a read that asked for the marks: s1 dropping %v, s2 %v", streamDropping(asked, "s1"), streamDropping(asked, "s2"))
	}
	unasked := &Snapshot{Partial: &Partial{}}
	unasked.Partial.position().dropping["s1"] = true
	if streamDropping(unasked, "s1") {
		t.Fatalf("a read that did not ask for the marks says s1 is dropping")
	}
}

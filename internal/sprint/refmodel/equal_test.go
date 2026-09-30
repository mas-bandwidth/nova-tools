package refmodel_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
)

// deal is a move of the deal duty: a primary of the work table dealt.
func deal(card string) refmodel.Move {
	return refmodel.Move{Duty: refmodel.DutyDeal, Kind: refmodel.KindMove, Table: sprint.Work, Card: card, From: "s1:ready", To: "s1:working"}
}

func TestEqualIsTheSameMovesInAnyOrder(t *testing.T) {
	t.Parallel()
	a := []refmodel.Move{deal("p3"), deal("p1"), deal("p2")}
	b := []refmodel.Move{deal("p1"), deal("p2"), deal("p3")}
	if ok, diff := refmodel.Equal(a, b); !ok || diff != "" {
		t.Errorf("the same moves in two orders: %v %q", ok, diff)
	}
	if ok, diff := refmodel.Equal(nil, nil); !ok || diff != "" {
		t.Errorf("no moves and no moves: %v %q", ok, diff)
	}
	// equal does not sort what it was given
	if a[0].Card != "p3" {
		t.Error("Equal sorted its argument")
	}
}

func TestEqualNamesTheFirstCardThatDiffers(t *testing.T) {
	t.Parallel()
	a := []refmodel.Move{deal("p1"), deal("p2"), deal("p3")}
	moved := deal("p2")
	moved.To = "s1:review"
	b := []refmodel.Move{deal("p1"), moved, deal("p3")}
	ok, diff := refmodel.Equal(a, b)
	if ok {
		t.Fatal("moves that differ in where a card goes are equal")
	}
	for _, want := range []string{"card p2", "deal", "the first has", "the second has", `to="s1:working"`, `to="s1:review"`} {
		if !strings.Contains(diff, want) {
			t.Errorf("the difference does not say %q: %s", want, diff)
		}
	}
	if strings.Contains(diff, "p1") || strings.Contains(diff, "p3") {
		t.Errorf("the difference names a card that is the same on both sides: %s", diff)
	}
}

func TestEqualNamesACardOneSideDoesNotMove(t *testing.T) {
	t.Parallel()
	a := []refmodel.Move{deal("p1"), deal("p2")}
	b := []refmodel.Move{deal("p1")}
	if ok, diff := refmodel.Equal(a, b); ok || !strings.Contains(diff, "card p2") || !strings.Contains(diff, "the second has no such move") {
		t.Errorf("a card only the first moves: %v %q", ok, diff)
	}
	if ok, diff := refmodel.Equal(b, a); ok || !strings.Contains(diff, "card p2") || !strings.Contains(diff, "the first has no such move") {
		t.Errorf("a card only the second moves: %v %q", ok, diff)
	}
	// the first of two differences, in the order of the tick, is the one named
	late := refmodel.Move{Duty: refmodel.DutyOverdue, Kind: refmodel.KindNotice, Type: sprint.NOverdue}
	if _, diff := refmodel.Equal(append(slices.Clone(a), late), nil); !strings.Contains(diff, "card p1") {
		t.Errorf("the first difference is the first card in the tick's order: %s", diff)
	}
}

func TestEqualNamesANoteBySubject(t *testing.T) {
	t.Parallel()
	open := refmodel.Move{Duty: refmodel.DutyDeal, Kind: refmodel.KindOpen, Type: sprint.NBound, Subjects: []string{"p7", "p8"}}
	ok, diff := refmodel.Equal([]refmodel.Move{open}, nil)
	if ok || !strings.Contains(diff, "card p7") || !strings.Contains(diff, sprint.NBound) {
		t.Errorf("a judgment one side raises: %v %q", ok, diff)
	}
}

func TestEqualSeesEveryFieldOfAMove(t *testing.T) {
	t.Parallel()
	base := refmodel.Move{Duty: refmodel.DutyDeal, Kind: refmodel.KindOpen, Table: "t", Card: "c", From: "a:b", To: "c:d", Score: "1", Set: []string{"a=b"}, Unset: []string{"x"},
		Type: "ty", Stream: "s", Subjects: []string{"p"}, Decisions: []string{"wait"}, Attrs: []string{"who=machine"}, Words: "w"}
	for name, change := range map[string]func(*refmodel.Move){
		"duty":      func(m *refmodel.Move) { m.Duty = refmodel.DutyLevel },
		"kind":      func(m *refmodel.Move) { m.Kind = refmodel.KindNotice },
		"table":     func(m *refmodel.Move) { m.Table = "u" },
		"card":      func(m *refmodel.Move) { m.Card = "d" },
		"from":      func(m *refmodel.Move) { m.From = "a:c" },
		"to":        func(m *refmodel.Move) { m.To = "c:e" },
		"score":     func(m *refmodel.Move) { m.Score = "2" },
		"set":       func(m *refmodel.Move) { m.Set = []string{"a=c"} },
		"unset":     func(m *refmodel.Move) { m.Unset = []string{"y"} },
		"type":      func(m *refmodel.Move) { m.Type = "tz" },
		"stream":    func(m *refmodel.Move) { m.Stream = "t" },
		"subjects":  func(m *refmodel.Move) { m.Subjects = []string{"q"} },
		"decisions": func(m *refmodel.Move) { m.Decisions = []string{"drop"} },
		"attrs":     func(m *refmodel.Move) { m.Attrs = []string{"who=other"} },
		"words":     func(m *refmodel.Move) { m.Words = "x" },
	} {
		other := base
		change(&other)
		if ok, _ := refmodel.Equal([]refmodel.Move{base}, []refmodel.Move{other}); ok {
			t.Errorf("moves that differ in their %s are equal", name)
		}
	}
}

func TestShapeIsTheMovesWithoutTheirWords(t *testing.T) {
	t.Parallel()
	a := deal("p1")
	a.Words = "p1 ready -> working"
	b := deal("p1")
	b.Words = "dealt p1"
	if ok, _ := refmodel.Equal([]refmodel.Move{a}, []refmodel.Move{b}); ok {
		t.Fatal("the fixture: moves in other words are unequal to Equal")
	}
	if ok, diff := refmodel.Equal(refmodel.Shape([]refmodel.Move{a}), refmodel.Shape([]refmodel.Move{b})); !ok {
		t.Errorf("the same moves in other words differ in shape: %s", diff)
	}
	if a.Words == "" {
		t.Error("Shape changed the moves it was given")
	}
	for _, m := range refmodel.Shape([]refmodel.Move{a, b}) {
		if m.Words != "" || m.Card != "p1" || m.To != "s1:working" {
			t.Errorf("Shape kept the words or lost the rest: %+v", m)
		}
	}
}

// The reversed witnesses of the equality test: it fails when a duty is left
// out, when the time is not the time, and when a duty is decided on a
// snapshot that is not the one given.
func TestTheEqualityTestSeesADutyLeftOut(t *testing.T) {
	t.Parallel()
	seen := 0
	for _, s := range snapshots() {
		want := todaysDecision(s)
		for _, d := range refmodel.Duties {
			if len(want[d.Name]) == 0 {
				continue
			}
			short := slices.Clone(d.Moves(s.snap, s.now))
			dropped := short[len(short)-1]
			short = short[:len(short)-1]
			ok, diff := refmodel.Equal(want[d.Name], short)
			if ok {
				t.Fatalf("a duty short of a move (%s) is equal to today's tick", dropped)
			}
			if name := firstName(dropped); !strings.Contains(diff, name) && !strings.Contains(diff, d.Name) {
				t.Fatalf("the difference names neither the card %q nor the duty %s: %s", name, d.Name, diff)
			}
			seen++
		}
		if seen > 50 {
			return
		}
	}
	t.Error("no duty made a move to leave out")
}

// firstName is the card or subject a move is about.
func firstName(m refmodel.Move) string {
	if m.Card != "" {
		return m.Card
	}
	if len(m.Subjects) > 0 {
		return m.Subjects[0]
	}
	return m.Kind + " " + m.Type
}

func TestTheEqualityTestSeesTheWrongTime(t *testing.T) {
	t.Parallel()
	differing := 0
	for _, s := range snapshots()[:deepSamples] {
		if ok, _ := refmodel.Equal(refmodel.Decide(s.snap, s.now), refmodel.Decide(s.snap, s.now.Add(3*time.Hour))); !ok {
			differing++
		}
	}
	if differing < minSamplesWithMoves {
		t.Errorf("only %d snapshots are decided differently three hours on: the time is not read", differing)
	}
}

func TestTheEqualityTestSeesASnapshotThatIsNotTheOneGiven(t *testing.T) {
	t.Parallel()
	differing := 0
	for _, s := range snapshots()[:deepSamples] {
		other := s.snap.Clone()
		other.Beats = nil // no beats read: the presence duty does nothing
		other.Untold = nil
		other.Goals = sprint.Goals{}
		if ok, _ := refmodel.Equal(refmodel.Decide(s.snap, s.now), refmodel.Decide(other, s.now)); !ok {
			differing++
		}
	}
	if differing < minSamplesWithMoves {
		t.Errorf("only %d snapshots are decided differently without their beats, goals and strangers: they are not read", differing)
	}
}

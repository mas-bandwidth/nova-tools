package refmodel_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	ok, diff := refmodel.Equal(a, b)
	assert.True(t, ok, "the same moves in two orders: %v %q", ok, diff)
	assert.Empty(t, diff, "the same moves in two orders: %v %q", ok, diff)
	ok, diff = refmodel.Equal(nil, nil)
	assert.True(t, ok, "no moves and no moves: %v %q", ok, diff)
	assert.Empty(t, diff, "no moves and no moves: %v %q", ok, diff)
	// equal does not sort what it was given
	assert.Equal(t, "p3", a[0].Card, "Equal sorted its argument")
}

func TestEqualNamesTheFirstCardThatDiffers(t *testing.T) {
	t.Parallel()
	a := []refmodel.Move{deal("p1"), deal("p2"), deal("p3")}
	moved := deal("p2")
	moved.To = "s1:review"
	b := []refmodel.Move{deal("p1"), moved, deal("p3")}
	ok, diff := refmodel.Equal(a, b)
	require.False(t, ok, "moves that differ in where a card goes are equal")
	for _, want := range []string{"card p2", "deal", "the first has", "the second has", `to="s1:working"`, `to="s1:review"`} {
		assert.Contains(t, diff, want, "the difference does not say %q: %s", want, diff)
	}
	assert.NotContains(t, diff, "p1", "the difference names a card that is the same on both sides: %s", diff)
	assert.NotContains(t, diff, "p3", "the difference names a card that is the same on both sides: %s", diff)
}

func TestEqualNamesACardOneSideDoesNotMove(t *testing.T) {
	t.Parallel()
	a := []refmodel.Move{deal("p1"), deal("p2")}
	b := []refmodel.Move{deal("p1")}
	ok, diff := refmodel.Equal(a, b)
	assert.False(t, ok, "a card only the first moves: %v %q", ok, diff)
	assert.Contains(t, diff, "card p2", "a card only the first moves: %v %q", ok, diff)
	assert.Contains(t, diff, "the second has no such move", "a card only the first moves: %v %q", ok, diff)
	ok, diff = refmodel.Equal(b, a)
	assert.False(t, ok, "a card only the second moves: %v %q", ok, diff)
	assert.Contains(t, diff, "card p2", "a card only the second moves: %v %q", ok, diff)
	assert.Contains(t, diff, "the first has no such move", "a card only the second moves: %v %q", ok, diff)
	// the first of two differences, in the order of the tick, is the one named
	late := refmodel.Move{Duty: refmodel.DutyOverdue, Kind: refmodel.KindNotice, Type: sprint.NOverdue}
	_, diff = refmodel.Equal(append(slices.Clone(a), late), nil)
	assert.Contains(t, diff, "card p1", "the first difference is the first card in the tick's order: %s", diff)
}

func TestEqualNamesAMoveOfNoCardByItsDutyAndKind(t *testing.T) {
	t.Parallel()
	due := refmodel.Move{Duty: refmodel.DutyPresence, Kind: refmodel.KindDue, Attrs: []string{"due=1"}}
	ok, diff := refmodel.Equal([]refmodel.Move{due}, nil)
	assert.False(t, ok, "a count left due, by its duty and kind: %v %q", ok, diff)
	assert.True(t, strings.HasPrefix(diff, "the presence due move differs: the first has "), "a count left due, by its duty and kind: %v %q", ok, diff)
	assert.NotContains(t, diff, "  ", "a count left due, by its duty and kind: %v %q", ok, diff)
	assert.NotContains(t, diff, "card", "a count left due, by its duty and kind: %v %q", ok, diff)
	notice := refmodel.Move{Duty: refmodel.DutyStrangers, Kind: refmodel.KindNotice, Type: sprint.NUnknownMachine}
	_, diff = refmodel.Equal(nil, []refmodel.Move{notice})
	assert.True(t, strings.HasPrefix(diff, "the strangers notice "+sprint.NUnknownMachine+" move differs: the second has "), "a notice of no card, by its duty and kind: %q", diff)
	assert.True(t, strings.HasSuffix(diff, "the first has no such move"), "a notice of no card, by its duty and kind: %q", diff)
}

func TestEqualSeesWhichJudgmentAnUpdateRewrites(t *testing.T) {
	t.Parallel()
	update := func(id string) refmodel.Move {
		return refmodel.Move{Duty: refmodel.DutyDeadlines, Kind: refmodel.KindUpdate, Card: "s1-1.w1", Type: sprint.NWorkLate, Subjects: []string{"s1-1"}, Attrs: []string{"id=" + id}, Words: "late"}
	}
	ok, _ := refmodel.Equal([]refmodel.Move{update("n1")}, []refmodel.Move{update("n2")})
	assert.False(t, ok, "updates of two judgments are equal")
	ok, diff := refmodel.Equal([]refmodel.Move{update("n1")}, []refmodel.Move{update("n1")})
	assert.True(t, ok, "updates of one judgment differ: %s", diff)
}

func TestEqualNamesANoteBySubject(t *testing.T) {
	t.Parallel()
	open := refmodel.Move{Duty: refmodel.DutyDeal, Kind: refmodel.KindOpen, Type: sprint.NBound, Subjects: []string{"p7", "p8"}}
	ok, diff := refmodel.Equal([]refmodel.Move{open}, nil)
	assert.False(t, ok, "a judgment one side raises: %v %q", ok, diff)
	assert.Contains(t, diff, "card p7", "a judgment one side raises: %v %q", ok, diff)
	assert.Contains(t, diff, sprint.NBound, "a judgment one side raises: %v %q", ok, diff)
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
		ok, _ := refmodel.Equal([]refmodel.Move{base}, []refmodel.Move{other})
		assert.False(t, ok, "moves that differ in their %s are equal", name)
	}
}

func TestShapeIsTheMovesWithoutTheirWords(t *testing.T) {
	t.Parallel()
	a := deal("p1")
	a.Words = "p1 ready -> working"
	b := deal("p1")
	b.Words = "dealt p1"
	ok, _ := refmodel.Equal([]refmodel.Move{a}, []refmodel.Move{b})
	require.False(t, ok, "the fixture: moves in other words are unequal to Equal")
	ok, diff := refmodel.Equal(refmodel.Shape([]refmodel.Move{a}), refmodel.Shape([]refmodel.Move{b}))
	assert.True(t, ok, "the same moves in other words differ in shape: %s", diff)
	assert.NotEmpty(t, a.Words, "Shape changed the moves it was given")
	for _, m := range refmodel.Shape([]refmodel.Move{a, b}) {
		assert.Empty(t, m.Words, "Shape kept the words or lost the rest: %+v", m)
		assert.Equal(t, "p1", m.Card, "Shape kept the words or lost the rest: %+v", m)
		assert.Equal(t, "s1:working", m.To, "Shape kept the words or lost the rest: %+v", m)
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
			require.False(t, ok, "a duty short of a move (%s) is equal to today's tick", dropped)
			name := firstName(dropped)
			require.True(t, strings.Contains(diff, name) || strings.Contains(diff, d.Name), "the difference names neither the card %q nor the duty %s: %s", name, d.Name, diff)
			seen++
		}
		if seen > 50 {
			return
		}
	}
	assert.Fail(t, "no duty made a move to leave out")
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
	assert.GreaterOrEqual(t, differing, minSamplesWithMoves, "only %d snapshots are decided differently three hours on: the time is not read", differing)
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
	assert.GreaterOrEqual(t, differing, minSamplesWithMoves, "only %d snapshots are decided differently without their beats, goals and strangers: they are not read", differing)
}

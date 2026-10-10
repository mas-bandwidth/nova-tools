package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/decide"
)

// Grade is the decide lane's write of its grade decisions: it is reached by no other unit
// test (only the store's functional tier drives it through GradeStep). Each grade rides on
// its primary in card id order (FieldGrade, guarded at the card's place and revision),
// with the lane's note, and every card it cannot grade is skipped silently: the lane
// grades what it read a moment before (decide.go, Grade, Gradable).
func TestDecideCoverGradeWritesEachGradeOnItsGradablePrimaryInIDOrder(t *testing.T) {
	t.Parallel()
	w := NewTable(Work)
	w.Put(&Card{ID: "s1-2", Row: "s1", Col: Ready, Rev: 2, Fields: map[string]string{"brief": "b2", "attempt": "0"}})
	w.Put(&Card{ID: "s1-1", Row: "s1", Col: Waiting, Rev: 1, Fields: map[string]string{"brief": "b1", "attempt": "0"}})
	g1 := decide.Decided{Value: decide.GradePro, P: 0.72, Op: "s1-1@0.aaaaaaaaaaaa"}
	g2 := decide.Decided{Value: decide.GradeFlash, P: 0.9, Op: "s1-2@0.bbbbbbbbbbbb"}
	p := Grade(&Snapshot{Work: w}, GradeReq{Grades: map[string]decide.Decided{"s1-2": g2, "s1-1": g1}, Who: MachineActor})
	require.Len(t, p.Units, 2)
	for i, tc := range []struct {
		id, col string
		rev     string
		g       decide.Decided
		moved   string
	}{
		{"s1-1", Waiting, "1", g1, "s1-1 graded pro p=0.72 by nova-decide (s1-1@0.aaaaaaaaaaaa)"},
		{"s1-2", Ready, "2", g2, "s1-2 graded flash p=0.90 by nova-decide (s1-2@0.bbbbbbbbbbbb)"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()
			u := p.Units[i]
			assert.Equal(t, tc.id, u.Key)
			assert.Equal(t, "s1", u.Stream)
			assert.Equal(t, tc.moved, u.Moved)
			require.Len(t, u.Changes, 1)
			assert.Equal(t, Work, u.Changes[0].Table)
			e := u.Changes[0].Entry
			assert.Equal(t, tc.id, e.ID)
			assert.Equal(t, map[string]string{FieldGrade: tc.g.String()}, e.Set)
			require.NotNil(t, e.Expect)
			assert.Equal(t, tc.rev, e.Expect.Revision)
			require.NotNil(t, e.Expect.Place)
			assert.Equal(t, "s1", e.Expect.Place.Row)
			assert.Equal(t, tc.col, e.Expect.Place.Col)
			assert.Nil(t, e.Create)
			assert.Nil(t, e.Move)
			assert.False(t, e.Remove)
		})
	}
}

// The refusal of Grade: a card the lane can no longer grade is skipped silently, its
// record untouched — it is not placed, is a sentinel, has no brief, was dealt meanwhile,
// has left waiting and ready, or was graded by an earlier write (Gradable).
func TestDecideCoverGradeSkipsEveryCardItCannotGrade(t *testing.T) {
	t.Parallel()
	g := decide.Decided{Value: decide.GradeFlash, P: 0.8, Op: "x@0.cccccccccccc"}
	for _, tc := range []struct {
		name string
		id   string
		card *Card
	}{
		{"no card with that id", "gone", nil},
		{"a dropped record is not placed", "s1-1", &Card{ID: "s1-1", Row: "s1", Fields: map[string]string{"brief": "b", "attempt": "0"}}},
		{"a sentinel is never graded", "s1-1", &Card{ID: "s1-1", Row: "s1", Col: Waiting, Fields: map[string]string{"kind": "sentinel", "attempt": "0"}}},
		{"a card with no brief is never graded", "s1-1", &Card{ID: "s1-1", Row: "s1", Col: Waiting, Fields: map[string]string{"attempt": "0"}}},
		{"a dealt card is graded no more", "s1-1", &Card{ID: "s1-1", Row: "s1", Col: Ready, Fields: map[string]string{"brief": "b", "attempt": "1"}}},
		{"a card past ready is graded no more", "s1-1", &Card{ID: "s1-1", Row: "s1", Col: Review, Fields: map[string]string{"brief": "b", "attempt": "0"}}},
		{"a graded card is graded no more", "s1-1", &Card{ID: "s1-1", Row: "s1", Col: Ready, Fields: map[string]string{"brief": "b", "attempt": "0", FieldGrade: g.String()}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := NewTable(Work)
			if tc.card != nil {
				w.Put(tc.card)
			}
			p := Grade(&Snapshot{Work: w}, GradeReq{Grades: map[string]decide.Decided{tc.id: g}, Who: MachineActor})
			assert.Empty(t, p.Units)
			assert.Empty(t, p.Refused)
		})
	}
}

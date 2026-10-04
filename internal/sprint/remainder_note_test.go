package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A tree card that finished ok at the step before its failed step names its remainder card in
// its report, and the "work came back ok" note carries that report so the coordinator sees
// the card to add (docs/SPEC-SPRINT.md, a card is a tree of steps); any other ok note is as before.
func TestAnOkFinishNamingARemainderCarriesItOnItsNote(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Deal(w.s, DealReq{Sel: Sel{Limit: 2}}))
	w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{Limit: 10}}))
	w.must(Take(w.s, TakeReq{As: "m2", Sel: Sel{Limit: 10}}))
	rem := "pushed=abc to work/s1-1: remainder=s1-1-r3 step 3 broken: TestTwo is red"
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: gensOf(w.s, "s1-1.w1"), Report: rem}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-2.w1"}}, Gens: gensOf(w.s, "s1-2.w1"), Report: "pushed=def to work/s1-2: done"}))
	notes := w.notesOf(NWorkOK)
	require.Len(t, notes, 2)
	what := map[string]string{}
	for _, n := range notes {
		what[n.Primaries[0]] = n.What
	}
	assert.Equal(t, map[string]string{"s1-1": rem, "s1-2": ""}, what)
}

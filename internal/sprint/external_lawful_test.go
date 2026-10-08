package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// No path releases an external wait before the tick sees its operands hold (tick_external.go;
// tla/CardISA.tla, WaitDispatchesOnlyWhenOperandHolds): the lifecycle refuses a move of it to
// ready that does not write FieldExternalMet, the landing trigger passes it over when its last
// need lands, and resolve with no answers (the verb) leaves it waiting.
func TestAnExternalWaitIsReleasedOnlyThroughItsOperand(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	brief := "c: an external wait tier: pro\nREPO: mas-bandwidth/nova-tools\nDEPENDS-ON: s1-1, pr nova-tools#5303 merged\n\nThe task."
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1"}, Brief: brief}))
	b := w.s.Work.Card("b")
	require.Equal(t, Waiting, b.Col)
	require.Equal(t, "s1-1", b.F("needs"), "the operand is no need")
	require.Equal(t, "pr nova-tools#5303 merged", b.F(FieldExternal))

	land(w, "s1-1")
	assert.Equal(t, Waiting, w.state("b"), "the landing of its need does not release it")

	move := Unit{Key: "b", Stream: "s2", Changes: []Change{change(Work, moveEntry(w.s.Work.Card("b"), "s2", Ready, nil))}}
	p := Plan{Units: []Unit{move}}
	p.on(w.s)
	p = Lawful(p)
	assert.Empty(t, p.Units, "a move to ready without the tick's answer: %+v", p)
	require.Len(t, p.Refused, 1)
	assert.Equal(t, "b waits for nova-tools#5303 merged: the tick releases it when they hold", p.Refused[0].Why)

	p = Resolve(w.s, ResolveReq{Sel: Sel{IDs: []string{"b"}}})
	assert.Empty(t, p.Units, "resolve with no answers asks nothing and moves nothing: %+v", p)
	require.Len(t, p.Refused, 1)
	assert.Equal(t, "waits for nova-tools#5303 merged", p.Refused[0].Why)
}

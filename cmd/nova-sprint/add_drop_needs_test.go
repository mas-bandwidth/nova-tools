package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A need names a card that can still land (docs/SPEC-SPRINT.md section 11,
// add and drop): add refuses a need naming a dropped primary, naming the id
// and its outcome, as it refuses one naming no record at all; and drop
// refuses a primary waiting cards still need, naming the dependants and
// writing nothing, unless it cascades -- the dependants, and their
// dependants, are dropped in the same plan with the same reason.
func TestAddRefusesAnUnknownNeedAndDropListsDependants(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	ta.ok("drop s1-1 --reason obsolete")
	code, out, errs := ta.do("add --stream s2 b --needs s1-1")
	require.Equal(t, 1, code, "the add naming a dropped need is refused: out=%s errs=%s", out, errs)
	require.Contains(t, out+errs, "s1-1", "the refusal names the id: %s%s", out, errs)
	require.Contains(t, out+errs, "which was dropped", "the refusal names the outcome: %s%s", out, errs)
	require.NotContains(t, ta.ok("queue --stream s2 --col waiting"), "b work:", "the add was admitted")
	code, out, errs = ta.do("add --stream s2 b --needs ghost")
	require.Equal(t, 1, code, "the add naming no record is refused: out=%s errs=%s", out, errs)
	require.Contains(t, out+errs, "ghost", "the refusal names the id: %s%s", out, errs)
	ta.ok("add --stream s2 b --needs s1-2")
	ta.ok("add --stream s2 c --needs b")
	before := ta.applies()
	code, out, errs = ta.do("drop s1-2 --reason obsolete")
	require.Equal(t, 1, code, "the drop of a needed card is refused: out=%s errs=%s", out, errs)
	require.Equal(t, before, ta.applies(), "the refused drop wrote")
	require.Contains(t, out+errs, "is needed by b", "the refusal names the dependant: %s%s", out, errs)
	require.Contains(t, out+errs, "--cascade", "the refusal names the remedy: %s%s", out, errs)
	require.Contains(t, ta.ok("card --fields s1-2"), "place=s1:ready", "s1-2 is still ready")
	out = ta.ok("drop s1-2 --reason obsolete --cascade")
	for _, id := range []string{"s1-2", "b", "c"} {
		require.Contains(t, out, id, "the cascade names every card it took off: %s", out)
	}
	require.Contains(t, ta.ok("card --fields c"), "outcome=dropped", "c is dropped")
	ta.clean()
}

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A need names a card that can still land. add refuses one that names a
// dropped card, naming the id and its outcome, and one that names no record
// at all, naming the id; drop refuses to take a card off the table while a
// waiting card still needs it, naming the dependants, unless --cascade takes
// the dependants and their dependants in the same plan. The refusal holds of
// the plan's end state: a card whose dependant the same plan refuses is
// refused too, so no card that stays is left needing one that goes, a
// selection by stream, column or group like one by id.
func TestAddRefusesAnUnknownNeedAndDropListsDependants(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	ta.ok("drop s1-1 --reason obsolete")

	// A need on the dropped card is refused, naming it and its outcome; a
	// need on no record at all is refused, naming the id.
	code, out, errs := ta.do("add --stream s2 b --needs s1-1")
	require.NotEqual(t, 0, code, "add on a dropped need: %s%s", out, errs)
	require.NotContains(t, out, "MOVED", "add on a dropped need wrote: %s", out)
	assert.Contains(t, errs, "s1-1", "add on a dropped need: %s", errs)
	assert.Contains(t, errs, "dropped", "add on a dropped need: %s", errs)
	code, out, errs = ta.do("add --stream s2 b --needs ghost")
	require.NotEqual(t, 0, code, "add on a missing need: %s%s", out, errs)
	assert.Contains(t, errs, "ghost", "add on a missing need: %s", errs)

	// A chain: b waits on s1-2, and c waits on b.
	ta.ok("add --stream s2 b --needs s1-2")
	ta.ok("add --stream s2 c --needs b")

	// Dropping s1-2 while b waits on it is refused for that card, naming the
	// dependants, and nothing is written: s1-2 stays ready.
	before := ta.applies()
	code, out, errs = ta.do("drop s1-2 --reason obsolete")
	require.NotEqual(t, 0, code, "drop of a needed card: %s%s", out, errs)
	assert.Contains(t, errs, "s1-2", "drop of a needed card: %s", errs)
	assert.Contains(t, errs, "b", "drop of a needed card: %s", errs)
	require.Equal(t, before, ta.applies(), "a refused drop wrote")
	require.Contains(t, ta.ok("card --fields s1-2"), "place=s1:ready", "a refused drop moved s1-2")

	// --cascade drops s1-2, b and c in one plan, one moved line each, and c
	// ends off the table with outcome dropped.
	out = ta.ok("drop s1-2 --reason obsolete --cascade")
	for _, id := range []string{"s1-2", "b", "c"} {
		assert.Contains(t, out, id, "cascade did not drop %s: %s", id, out)
	}
	require.Contains(t, ta.ok("card --fields c"), "outcome=dropped", "c after the cascade")

	// The same refusal holds of a selection that is not by id: dropping
	// stream s1 names x and w, and u, outside the stream, needs w, so w is
	// refused, and x after it — w, kept a moment, is refused later in the
	// same plan and still needs x — and nothing is written; the same cards
	// by id are refused the same way.
	ta.ok("add --stream s2 v")
	ta.ok("add --stream s1 x --needs v")
	ta.ok("add --stream s1 w --needs x")
	ta.ok("add --stream s2 u --needs w")
	before = ta.applies()
	code, out, errs = ta.do("drop --stream s1 --reason obsolete")
	require.NotEqual(t, 0, code, "a stream drop of a needed chain: %s%s", out, errs)
	require.NotContains(t, out, "MOVED", "a refused stream drop wrote: %s", out)
	assert.Contains(t, errs, "REFUSED x", "a refused stream drop: %s", errs)
	assert.Contains(t, errs, "REFUSED w", "a refused stream drop: %s", errs)
	require.Equal(t, before, ta.applies(), "a refused stream drop wrote")
	require.Contains(t, ta.ok("card --fields x"), "place=s1:waiting", "x after the refused stream drop")
	require.Contains(t, ta.ok("card --fields w"), "place=s1:waiting", "w after the refused stream drop")
	code, out, errs = ta.do("drop x w --reason obsolete")
	require.NotEqual(t, 0, code, "an id drop of the same chain: %s%s", out, errs)
	require.NotContains(t, out, "MOVED", "a refused id drop wrote: %s", out)
	require.Equal(t, before, ta.applies(), "a refused id drop wrote")

	// --cascade takes the whole chain in one plan: the selection (x and w)
	// and u, w's dependant outside it; v, needed by none of them, stays.
	out = ta.ok("drop --stream s1 --reason obsolete --cascade")
	for _, id := range []string{"x", "w", "u"} {
		assert.Contains(t, out, id, "cascade did not drop %s: %s", id, out)
	}
	require.Contains(t, ta.ok("card --fields u"), "outcome=dropped", "u after the cascade")
	require.Contains(t, ta.ok("card --fields v"), "place=s2:ready", "the cascade dropped v, needed by none of them")
}

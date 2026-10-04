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
// the dependants and their dependants in the same plan.
func TestAddRefusesAnUnknownNeedAndDropListsDependants(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	ta.ok("drop s1-1 --reason obsolete")

	// A need on the dropped card is refused, naming it and its outcome; a
	// need on no record at all is refused, naming the id.
	code, out, errs := ta.do("add --stream s2 b --needs s1-1 --one")
	require.NotEqual(t, 0, code, "add on a dropped need: %s%s", out, errs)
	require.NotContains(t, out, "MOVED", "add on a dropped need wrote: %s", out)
	assert.Contains(t, errs, "s1-1", "add on a dropped need: %s", errs)
	assert.Contains(t, errs, "dropped", "add on a dropped need: %s", errs)
	code, out, errs = ta.do("add --stream s2 b --needs ghost --one")
	require.NotEqual(t, 0, code, "add on a missing need: %s%s", out, errs)
	assert.Contains(t, errs, "ghost", "add on a missing need: %s", errs)

	// A chain: b waits on s1-2, and c waits on b.
	ta.ok("add --stream s2 b --needs s1-2 --one")
	ta.ok("add --stream s2 c --needs b --one")

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
}

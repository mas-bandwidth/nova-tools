package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A need names a card that can still land. add refuses one that names a
// dropped card, naming the id and its outcome, and one that names no record
// at all, naming the id. drop of a card a waiting card needs detaches that
// need in the same step. --cascade still drops the dependants with it.
func TestAddRefusesAnUnknownNeedAndDropListsDependants(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	ta.ok("drop s1-1 --reason obsolete")

	// A need on the dropped card is refused, naming it and its outcome; a
	// need on no record at all is refused, naming the id.
	code, out, errs := ta.do("add --stream s2 b --one --needs s1-1")
	require.NotEqual(t, 0, code, "add on a dropped need: %s%s", out, errs)
	require.NotContains(t, out, "MOVED", "add on a dropped need wrote: %s", out)
	assert.Contains(t, errs, "s1-1", "add on a dropped need: %s", errs)
	assert.Contains(t, errs, "dropped", "add on a dropped need: %s", errs)
	code, out, errs = ta.do("add --stream s2 b --one --needs ghost")
	require.NotEqual(t, 0, code, "add on a missing need: %s%s", out, errs)
	assert.Contains(t, errs, "ghost", "add on a missing need: %s", errs)

	// A chain: b waits on s1-2, and c waits on b.
	ta.ok("add --stream s2 b --one --needs s1-2")
	ta.ok("add --stream s2 c --one --needs b")

	// Dropping s1-2 while b waits on it detaches that need. b stays waiting, and
	// c still needs b.
	before := ta.applies()
	out = ta.ok("drop s1-2 --reason obsolete")
	assert.Contains(t, out, "DETACHED", "drop of a needed card: %s", out)
	assert.Contains(t, out, "b", "drop of a needed card: %s", out)
	assert.Contains(t, out, "s1-2", "drop of a needed card: %s", out)
	require.Greater(t, ta.applies(), before, "the drop wrote")
	require.Contains(t, ta.ok("card --fields s1-2"), "outcome=dropped")
	require.Contains(t, ta.ok("card --fields b"), "place=s2:waiting")
	require.NotContains(t, ta.ok("card --fields b"), "NEEDS s1-2")
	require.Contains(t, ta.ok("card --fields c"), "NEEDS b")

	// --cascade drops a fresh chain, the dependants with the card.
	ta.ok("add --stream s3 d --one")
	ta.ok("add --stream s3 e --one --needs d")
	ta.ok("add --stream s3 f --one --needs e")
	out = ta.ok("drop d --reason obsolete --cascade")
	for _, id := range []string{"d", "e", "f"} {
		assert.Contains(t, out, id, "cascade did not drop %s: %s", id, out)
	}
	require.Contains(t, ta.ok("card --fields f"), "outcome=dropped", "f after the cascade")
}

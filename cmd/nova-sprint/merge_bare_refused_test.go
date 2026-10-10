package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A bare merge on a real store (2026-10-10: the seat's bare `merge --stream` recorded 127
// cards landed whose heads origin's base never held): with no fact and no --landed it is
// refused before any store is dialed, naming --landed and land, and nothing is written.
// The address here is a real one nothing listens on: a refusal that dialed it would name
// the store, not the bare merge.
func TestBareMergeIsRefusedOnARealStore(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	heads := map[string]string{"s1-1": r.head("s1-1", "main", "s1-1.txt", "s1-1\n"), "s1-2": r.head("s1-2", "main", "s1-2.txt", "s1-2\n")}
	r.queued(heads, "s1-1", "s1-2")
	before := r.applies()
	for _, line := range []string{"merge --stream s1 --redis 127.0.0.1:1", "merge --stream s1 --batch 1 --redis 127.0.0.1:1", "merge --stream s1 --batch 100 --redis redis.example.invalid:6380"} {
		code, out, errs := r.do(line)
		assert.NotEqual(t, 0, code, "%s: %s%s", line, out, errs)
		assert.Contains(t, errs, "nova-sprint merge REFUSED: a bare merge records the queue's head as landed with no push, and a real store refuses it; nothing was changed", line)
		assert.Contains(t, errs, "--landed <id>@<head>", line)
		assert.Contains(t, errs, "run: nova-sprint land --stream s1", line)
		assert.Empty(t, out, line)
	}
	assert.Equal(t, before, r.applies(), "a refused bare merge wrote")
	assert.Equal(t, map[string]string{"s1-1": "merging/queued", "s1-2": "merging/queued"}, r.places("s1-1", "s1-2"))
}

// The record by name lands a card only when its head is an ancestor of the fetched base: a
// head origin's base does not hold is refused, naming the card, and nothing is written; the
// card whose head was pushed lands, and only it.
func TestMergeLandedRefusesAHeadNotOnTheBase(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	heads := map[string]string{"s1-1": r.head("s1-1", "main", "s1-1.txt", "s1-1\n"), "s1-2": r.head("s1-2", "main", "s1-2.txt", "s1-2\n")}
	r.queued(heads, "s1-1", "s1-2")
	// s1-1 is pushed onto origin's main; s1-2 never is
	r.git(r.worker, "fetch", "-q", "origin")
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.git(r.worker, "merge", "-q", "--no-ff", "-m", "land s1-1", heads["s1-1"])
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.clone, "fetch", "-q", "origin")
	landed := func(ids ...string) string {
		line := "merge --stream s1 --repo " + r.clone + " --base-ref origin/main"
		for _, id := range ids {
			line += " --landed " + id + "@" + heads[id]
		}
		return line
	}

	before := r.applies()
	for _, ids := range [][]string{{"s1-2"}, {"s1-1", "s1-2"}} {
		code, out, errs := r.do(landed(ids...))
		assert.Equal(t, 1, code, "%v: %s%s", ids, out, errs)
		assert.Contains(t, out+errs, "s1-2", ids)
		assert.Contains(t, out+errs, "is not an ancestor of the base branch's tip", ids)
	}
	assert.Equal(t, before, r.applies(), "a refused record wrote")
	assert.Equal(t, map[string]string{"s1-1": "merging/queued", "s1-2": "merging/queued"}, r.places("s1-1", "s1-2"))

	out := r.ok(landed("s1-1"))
	require.Contains(t, out, "s1-1 merging -> landed")
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "merging/queued"}, r.places("s1-1", "s1-2"))
}

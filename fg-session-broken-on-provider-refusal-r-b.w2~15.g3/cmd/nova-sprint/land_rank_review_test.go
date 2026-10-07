package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Ranking queued cards changes priority, not the identity of work already
// built and pushed. The report must recognize its exact set in either order.
func TestLandReviewRerankingThePinnedCardsDoesNotClaimTheyWereUnreported(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	r.queued(map[string]string{
		"s1-1": r.head("s1-1", "main", "one.txt", "one\n"),
		"s1-2": r.head("s1-2", "main", "two.txt", "two\n"),
	}, "s1-1", "s1-2")
	r.a.beforePush = func(int) { r.ok("rank s1-2 --first") }
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
	assert.Equal(t, 0, code, "both exact pinned cards were pushed and reported: %s%s", out, errs)
	assert.NotContains(t, out+errs, "NOT reported")
	assert.Equal(t, []string{"land s1-2 (sprint stream s1)", "land s1-1 (sprint stream s1)", "base"}, r.mainLog(), "the built order stays fixed")
}

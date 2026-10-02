package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A stop recorded before a later stream begins building is observed before
// its push: a stopped stream moves only after resume (docs/SPEC-SPRINT.md,
// the stream lifecycle).
func TestReviewLandDoesNotPushALaterStreamStoppedDuringTheFirstBatch(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 first")
	r.ok("add --stream s2 held")
	r.queued(map[string]string{
		"first": r.head("first", "main", "first.txt", "first\n"),
		"held":  r.head("held", "main", "held.txt", "held\n"),
	}, "first", "held")
	pushes := 0
	r.a.beforePush = func(int) {
		pushes++
		if pushes == 1 {
			// This coordinator write happens before s2 starts building, rather
			// than in s2's unavoidable gap between its final read and push.
			r.ok("merge --stream s2 --batch 1 --red")
		}
	}

	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")

	assert.Equal(t, 1, code, "the stopped stream is refused before push: %s%s", out, errs)
	assert.Equal(t, 1, pushes, "only the first stream reaches a push")
	assert.Equal(t, "stopped red", r.streamState("s2"))
	assert.Equal(t, map[string]string{"first": "landed/merged", "held": "merging/queued"}, r.places("first", "held"))
	assert.Equal(t, []string{"land first (sprint stream s1)", "base"}, r.mainLog(), "the held stream's work never reaches origin")
	assert.NotContains(t, out+errs, "LAND FAILED", "a stop already read before pushing must not become an unreported delivery")
}

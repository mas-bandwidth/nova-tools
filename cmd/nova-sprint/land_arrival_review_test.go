package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A higher-priority arrival cannot change which exact cards a completed
// Git batch reports. It stays queued until a later batch actually pushes it.
func TestLandReviewAnArrivalAheadCannotReplaceThePushedBatch(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 original")
	head := r.head("original", "main", "original.txt", "original\n")
	r.queued(map[string]string{"original": head}, "original")
	r.a.beforePush = func(int) {
		r.ok("add --stream s1 newcomer --before original")
		newHead := r.head("newcomer", "main", "newcomer.txt", "newcomer\n")
		r.queued(map[string]string{"newcomer": newHead}, "newcomer")
	}
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, 0, code, "a completed unchanged batch is reportable despite the arrival: %s%s", out, errs)
	assert.Equal(t, map[string]string{"original": "landed/merged", "newcomer": "merging/queued"}, r.places("original", "newcomer"), "%s%s", out, errs)
	assert.Equal(t, []string{"land original (sprint stream s1)", "base"}, r.mainLog(), "only the selected batch reached the base")
	r.clean()
}

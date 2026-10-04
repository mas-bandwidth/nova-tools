package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A clone may fetch from origin's URL but push to remote.origin.pushurl.
// Land must hold the destination it will push to against the card repository
// before fetching, merging, pushing, or reporting.
func TestLandRereadRefusesAnotherOriginPushURLBeforeChangingEitherRepository(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	other := filepath.Join(r.dir, "other.git")
	r.git("", "init", "-q", "--bare", "-b", "main", other)
	r.git(r.worker, "push", "-q", other, "refs/remotes/origin/main:refs/heads/main")
	r.git(r.clone, "config", "remote.origin.pushurl", other)

	r.ok("add --stream s1 --count 1")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n")}, "s1-1")
	want, otherWant := r.git(r.remote, "rev-parse", "main"), r.git(other, "rev-parse", "main")

	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.NotEqual(t, 0, code, out+errs)
	assert.Equal(t, want, r.git(r.remote, "rev-parse", "main"), "the card repository must not change")
	assert.Equal(t, otherWant, r.git(other, "rev-parse", "main"), "the configured push destination must not change")
	assert.Equal(t, map[string]string{"s1-1": "merging/queued"}, r.places("s1-1"), "the refused batch must not be reported")
}

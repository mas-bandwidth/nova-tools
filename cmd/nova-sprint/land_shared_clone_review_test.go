package main

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/stretchr/testify/assert"
)

// Another landing job can cut its branch in a shared checkout after this
// job checked its batch. A successful report must still name work actually
// present in the pushed base, not the replacement checkout's HEAD.
func TestLandReviewAnotherBuildInTheCloneCannotBecomeFalseLand(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 1 --one")
	head := r.head("s1-1", "main", "work.txt", "the reviewed work\n")
	r.queued(map[string]string{"s1-1": head}, "s1-1")
	r.a.beforePush = func(int) {
		// The first operation of another build cuts from the fetched base.
		r.git(r.clone, "switch", "--no-track", "--force-create", "land/other", "refs/remotes/origin/main")
	}
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	if code == 0 {
		res, err := gitrun.Run(context.Background(), gitrun.Options{C: r.remote, Env: r.env, OwnRepo: true},
			"merge-base", "--is-ancestor", head, "refs/heads/main")
		assert.NoError(t, err, "LAND OK must put the pinned work in the base: %s%s %s; placement=%v", out, errs, res.Stderr, r.places("s1-1"))
	}
}

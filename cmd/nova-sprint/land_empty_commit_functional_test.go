//go:build functional

package main

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/testgit"
)

// TestLandReturnsAnEmptyCommitLandingToReview tests that land refuses a landing whose head
// commit's diff from the attempt's start commit is empty while the card's recorded result (its finish report) claims changes,
// and returns the card to review with the finding (docs/SPEC-SPRINT.md section 7, the lander's
// checks).
func TestLandReturnsAnEmptyCommitLandingToReview(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 1 --one")

	// One head: an empty commit on the card's branch, pushed alone. No RESULT.md is planted in
	// the lander's clone: the claim is what the card's finish records (--report), as in a real landing.
	r.git(r.worker, "switch", "-q", "--no-track", "-c", "sprint/s1-1", "refs/remotes/origin/main")
	commitEmpty(t, r.worker)
	emptyHead := r.git(r.worker, "rev-parse", "HEAD")
	r.git(r.worker, "push", "-q", "origin", "refs/heads/sprint/s1-1:refs/heads/sprint/s1-1")
	r.deal(1)
	r.ok("take --as m1 --limit 100")
	r.ok("finish --as m1 s1-1.w1@1 --head " + emptyHead + " --report 'done: 1 file changed, 10 insertions'")
	r.ok("ask")
	r.ok("read --as reader-a --ok --limit 100")
	r.ok("read --as reader-b --ok --limit 100")
	r.ok("accept --read-ok")
	// main is protected. Mark the stream so the refusal is the empty commit, not the
	// protected-base rule (docs/SPEC-SPRINT.md section 7).
	r.markProtected()

	before := r.git(r.remote, "rev-parse", "main")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, 1, code, out+errs)
	assert.Contains(t, errs, "empty commit: the result claims changes the diff does not show")

	// The push to main did not happen:
	assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"))

	// Assert the primary card is returned to review with the finding:
	pr := r.primary("s1-1")
	assert.Equal(t, sprint.Review, pr.Col, "primary must be returned to review")
	assert.Equal(t, "empty commit: the result claims changes the diff does not show", pr.F("return_reason"))

	r.clean()
}

// commitEmpty is one empty commit under the shared test identity (pkg/testgit).
func commitEmpty(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "commit", "--allow-empty", "-q", "-m", "empty commit for s1-1")
	cmd.Dir = dir
	cmd.Env = testgit.Environ(testgit.NoGlobalConfig(t)...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "empty commit: %s", out)
}

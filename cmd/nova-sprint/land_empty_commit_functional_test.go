//go:build functional

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// TestLandReturnsAnEmptyCommitLandingToReview tests that land refuses a landing whose head
// commit's diff from the attempt's start commit is empty while RESULT.md claims changes,
// and returns the card to review with the finding (docs/SPEC-SPRINT.md section 7, the lander's
// checks).
func TestLandReturnsAnEmptyCommitLandingToReview(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 1")

	// The start commit on main is created by newLandRig (README: base\n).
	// Create an empty head commit on sprint/s1-1:
	r.git(r.worker, "switch", "-q", "--no-track", "-c", "sprint/s1-1", "refs/remotes/origin/main")
	r.git(r.worker, "commit", "--allow-empty", "-q", "-m", "empty commit for s1-1")
	emptyHead := r.git(r.worker, "rev-parse", "HEAD")

	// RESULT.md that claims changes
	resultText := "head: " + emptyHead + "\nbranch: sprint/s1-1\nverdict: ok\n1 file changed, 10 insertions(+)\nreport: done\n"
	require.NoError(t, os.WriteFile(filepath.Join(r.clone, "RESULT.md"), []byte(resultText), 0o600))

	// Queue card at emptyHead with report
	r.git(r.worker, "push", "-q", "origin", "refs/heads/sprint/*:refs/heads/sprint/*")
	r.deal(1)
	r.ok("take --as m1 --limit 100")
	r.ok("finish --as m1 s1-1.w1@1 --head " + emptyHead + " --report '" + oneline.Escape(resultText) + "'")
	r.ok("ask")
	r.ok("read --as reader-a --ok --limit 100")
	r.ok("read --as reader-b --ok --limit 100")
	r.ok("accept --read-ok")

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

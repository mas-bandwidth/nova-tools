package swarm

// restage_cover_test.go reaches the two restage.go functions the unit tier's
// per-function table showed at 0.0%: restageAtTip (restage.go:29) and cmpErr
// (restage.go:113). Nothing here sleeps, reads a real clock, opens a socket or
// forks a child: a git that cannot be found makes exec's own lookup fail before
// any child is forked, the same seam stage_cover_test.go uses. What a unit test
// cannot reach without a real git child — everything past a successful fetch,
// the tip's rev-parse and the carry states, merge and commit — is the functional
// tier's (restage_functional_test.go), and is named in the card's report, never
// hidden.

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const restageCoverSha = "0123456789abcdef0123456789abcdef01234567"

// restageCoverGit is the package's git seam backed by a binary exec cannot find
// (the name stage_cover_test.go's table holds), so every call fails in exec's own
// lookup before a child is forked; each call's git subcommand is recorded for the
// test to pin.
func restageCoverGit(bin string, calls *[]string) func(context.Context, ...string) *exec.Cmd {
	return func(ctx context.Context, args ...string) *exec.Cmd {
		*calls = append(*calls, args[2])
		return exec.CommandContext(ctx, bin, args...)
	}
}

// TestRestageCoverCmpErr pins cmpErr both ways: a caller's error is returned as
// itself, unrenamed, and a nil error is made into one carrying the why, so a
// stage failure never answers a bare nil where a reason is owed.
func TestRestageCoverCmpErr(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("git fetch failed")
	for _, tc := range []struct {
		name    string
		err     error
		why     string
		wantMsg string
	}{
		{"the caller's error is returned as itself", sentinel, "no full commit sha", "git fetch failed"},
		{"a nil error becomes one saying why", nil, "no full commit sha", "no full commit sha"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := cmpErr(tc.err, tc.why)
			assert.EqualError(t, got, tc.wantMsg, "%s: cmpErr answered the wrong error", tc.name)
			if tc.err != nil {
				assert.Same(t, tc.err, got, "%s: the caller's error must be returned as itself, not wrapped or copied", tc.name)
			}
		})
	}
}

// TestRestageCoverBaseThatNeverMoves pins the main path a base that never moves
// takes: no base, or a full sha, answers no carry, no step name, no output and
// no error, and runs git not once — the checkout stays where the stage put it
// and the previous head, however real, is never carried on top.
func TestRestageCoverBaseThatNeverMoves(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		base string
	}{
		{"no base", ""},
		{"a full sha", restageCoverSha},
		{"a full sha of 64", strings.Repeat("a", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls []string
			c, what, out, err := restageAtTip(context.Background(), restageCoverGit("nova-worker-no-such-git", &calls), t.TempDir(),
				"sprint/c1.g2.e1", tc.base, Rework{Prev: restageCoverSha, From: 1}, new(time.Duration), new(time.Duration))
			require.NoError(t, err, "%s: a base that never moves stages without error", tc.name)
			assert.Nil(t, c, "%s: the carry is nil, the checkout staying where the stage put it", tc.name)
			assert.Empty(t, what, "%s: no step failed, so no step is named", tc.name)
			assert.Nil(t, out, "%s: no git output is answered", tc.name)
			assert.Empty(t, calls, "%s: git runs not once for a base that never moves", tc.name)
		})
	}
}

// TestRestageCoverFetchFailureIsTheStagesFailure pins the refusal: a base branch
// whose fetch cannot run (here exec's own lookup of git, before any child is
// forked) is the stage's failure, never a stage at a stale tip — no carry, the
// step named the fetch of the base branch, and the probes of the remote ref and
// then the tag coming before the fetch.
func TestRestageCoverFetchFailureIsTheStagesFailure(t *testing.T) {
	t.Parallel()

	var calls []string
	c, what, out, err := restageAtTip(context.Background(), restageCoverGit("nova-worker-no-such-git", &calls), t.TempDir(),
		"sprint/c1.g2.e1", "main", Rework{Prev: restageCoverSha, From: 1}, new(time.Duration), new(time.Duration))
	assert.Nil(t, c, "a fetch that fails stages no carry")
	assert.Nil(t, out, "a failed fetch answers no git output of its own")
	var lookup *exec.Error
	assert.ErrorAs(t, err, &lookup, "the failure is exec's own lookup of git, no child forked")
	assert.Equal(t, "fetch of the base branch main, whose tip a rework is staged at,", what,
		"the stage's failure names the step, whose tip a rework is staged at")
	assert.Equal(t, []string{"rev-parse", "rev-parse", "fetch"}, calls,
		"the remote ref and then the tag are probed before the branch is fetched")
}

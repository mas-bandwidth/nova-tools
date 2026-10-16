package swarm

// stage_reuse_test.go pins the reused-checkout case of StageCard: the isGitDir
// branch of stage.go. A job directory is never cleared (native.go), so a rework
// or redeal that keeps the slot's generation and epoch reuses the earlier
// attempt's checkout, which sits at that attempt's base. The carried frame head
// is then not in any branch of the checkout and the push refuses at
// cmd/nova-swarm/memberpush.go. StageCard brings the head in and switches the
// card's branch to it, while a HEAD that already descends from the head is the
// resume case and is left alone (docs/SPEC-CARD-CONTRACT.md, staging;
// tla/CardContract.tla StagedFromOrigin). Nothing here waits on a clock, opens
// a socket or reaches the network: the git seam records each argv and answers by
// it, the way stage_cover_test.go's seam answers a binary exec cannot find.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reuseGitFake is the package's git seam for the reused-checkout test: it records
// every argv in order and answers by it. merge-base answers descends; cat-file
// answers arrived; a fetch answers true and, when fetchBrings is set, makes the
// head arrive; every other call answers true. The subcommand is the argument
// after the leading `-C <dir>`, so the fake keys on args[2].
type reuseGitFake struct {
	descends    bool
	arrived     bool
	fetchBrings bool
	calls       [][]string
}

func (f *reuseGitFake) cmd(ctx context.Context, args ...string) *exec.Cmd {
	f.calls = append(f.calls, append([]string(nil), args...))
	ok := true
	switch args[2] {
	case "merge-base":
		ok = f.descends
	case "cat-file":
		ok = f.arrived
	case "fetch":
		if f.fetchBrings {
			f.arrived = true
		}
	}
	if ok {
		return exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}
	return exec.CommandContext(ctx, "sh", "-c", "exit 1")
}

// assertReuseCallsInOrder holds that want appears in calls in order: the frame's
// head is checked, then fetched, then switched to.
func assertReuseCallsInOrder(t *testing.T, calls [][]string, want ...[]string) {
	t.Helper()
	i := 0
	for _, c := range calls {
		if i < len(want) && slices.Equal(c, want[i]) {
			i++
		}
	}
	assert.Equal(t, len(want), i, "the recorded git argv did not hold, in order: %v; got %v", want, calls)
}

// TestStageCardBringsAReusedCheckoutToTheFramesCommit holds the three cases of
// the reused checkout: a HEAD that does not descend from the frame's head is
// brought to it with the same cat-file/fetch steps the fresh-clone path uses and
// the branch switched; a HEAD that already descends is the resume case, fetched
// and switched not once; and a head that never arrives is refused in one line.
func TestStageCardBringsAReusedCheckoutToTheFramesCommit(t *testing.T) {
	t.Parallel()

	const (
		repo   = "https://example.com/o/repo.git"
		sha    = "09fbedc9052145b20677501a1dbcb5f5ba9c87d4"
		branch = "sprint/c1.g1.e20"
	)
	card := []byte("c1: the card\nbase-repo: " + repo + "\nbase-sha: " + sha + "\n")
	reused := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
		return dir
	}
	stage := func(t *testing.T, target string, fake *reuseGitFake) (StageResult, error) {
		t.Helper()
		return StageCard(StageOptions{
			Card: card, TargetDir: target, JobDir: filepath.Dir(target),
			BenchName: "testhost", Timeout: 30 * time.Second, Branch: branch,
			fetchRetryDelay: time.Millisecond, git: fake.cmd,
		})
	}

	t.Run("a HEAD that does not descend is brought to the frame's head", func(t *testing.T) {
		t.Parallel()
		target := reused(t)
		fake := &reuseGitFake{descends: false, arrived: false, fetchBrings: true}
		res, err := stage(t, target, fake)
		require.NoError(t, err, "a reused checkout whose head is in origin must stage")
		assert.True(t, res.Staged)
		assert.Equal(t, branch, res.Branch, "the staged checkout is on the card's branch")
		assert.Equal(t, sha, res.BaseSha, "the staged checkout is at the frame's head")
		assertReuseCallsInOrder(t, fake.calls,
			[]string{"-C", target, "merge-base", "--is-ancestor", "--end-of-options", sha, "HEAD"},
			[]string{"-C", target, "fetch", "-q", "--refetch", "--", "origin", sha},
			[]string{"-C", target, "switch", "-q", "-C", branch, "--end-of-options", sha})
	})

	t.Run("a HEAD that descends is the resume case", func(t *testing.T) {
		t.Parallel()
		target := reused(t)
		fake := &reuseGitFake{descends: true, arrived: true}
		res, err := stage(t, target, fake)
		require.NoError(t, err, "a checkout already holding the frame's head must resume")
		assert.True(t, res.Staged)
		require.Len(t, fake.calls, 1, "only the is-ancestor check runs for a resumed checkout")
		assert.Equal(t,
			[]string{"-C", target, "merge-base", "--is-ancestor", "--end-of-options", sha, "HEAD"},
			fake.calls[0], "the resume case checks descent and fetches and switches nothing")
	})

	t.Run("a head that never arrives is refused", func(t *testing.T) {
		t.Parallel()
		target := reused(t)
		fake := &reuseGitFake{descends: false, arrived: false, fetchBrings: false}
		res, err := stage(t, target, fake)
		require.Error(t, err, "a head in neither the checkout nor origin must be refused")
		assert.False(t, res.Staged)
		assert.Contains(t, err.Error(), "staging refused: head "+sha+" is in neither",
			"the refusal was not the one line the fresh-clone path prints")
	})
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// A RESULT.md the repository carries is not the card's result (2026-10-24: rocketnet main
// held a card's RESULT.md, 3bb8e5f1, naming head 2dc17c32, and every finish on that
// repository with no job-root result took that head and was refused as a non-descendant;
// removed in rocketnet #569). The finish refuses a result found at repo/RESULT.md when that
// file is tracked and unchanged at the staged commit, and says so; a job-root result, an
// untracked repo/RESULT.md or one the child changed is the card's own and is pushed. The
// repository state is the scripted git's answers and a file under t.TempDir: no real git.
func TestAResultTrackedAndUnchangedAtTheStagedCommitIsRefusedAsStale(t *testing.T) {
	t.Parallel()
	const blob = "1111111111111111111111111111111111111111"
	for _, tc := range []struct {
		name      string
		jobRoot   bool   // the child wrote <job>/RESULT.md
		tracked   string // the staged commit's RESULT.md blob, "" for none
		hashed    string // the checkout file's blob
		wantStale bool
	}{
		{name: "tracked and unchanged", tracked: blob, hashed: blob, wantStale: true},
		{name: "tracked and changed by the child", tracked: blob, hashed: "2222222222222222222222222222222222222222"},
		{name: "not tracked at the staged commit", tracked: "", hashed: blob},
		{name: "the job root holds the card's own result", jobRoot: true, tracked: blob, hashed: blob},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newPushHarness(t)
			require.NoError(t, os.WriteFile(filepath.Join(h.checkout, "RESULT.md"), []byte("verdict: ok\nhead: 2dc17c32\n"), 0o644))
			job := filepath.Dir(h.checkout)
			if tc.jobRoot {
				require.NoError(t, os.WriteFile(swarm.ResultPath(job), []byte("verdict: ok\n"), 0o644))
			}
			h.git.trackedResult, h.git.hashed = tc.tracked, tc.hashed
			got := h.pusher().Push(h.p, member.Result{Head: h.git.head})
			pushed := false
			for _, a := range h.git.argv {
				pushed = pushed || a[0] == "push"
			}
			if !tc.wantStale {
				assert.Equal(t, member.Push{Sha: h.git.head}, got)
				assert.True(t, pushed)
				return
			}
			assert.Empty(t, got.Sha)
			assert.False(t, pushed, "a stale result's head never reaches origin")
			assert.Contains(t, got.Refused, filepath.Join(h.checkout, "RESULT.md")+", tracked and unchanged at the staged commit "+strings.Repeat("b", 40))
			assert.Contains(t, got.Refused, "a stale file from the repository, not this card's result")
			assert.Contains(t, got.Refused, swarm.ResultPath(job))
			assert.NotContains(t, got.Refused, "\n", "the refusal is one line")
		})
	}
}

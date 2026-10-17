package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
	"github.com/stretchr/testify/require"
)

// v1.2.6 fault 9: a card killed at its deadline with a commit in ./repo and no RESULT.md
// used to leave nothing, and the pool scored it `no-result`. The run now writes where the
// work is, and the verdict line it prints does not change.

func deadlineGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v:\n%s", args, out)
}

// deadlineWithRepo runs one card that clones origin into ./repo (the fake harness's
// FAKE-CARD-REPO) and then sleeps, and fires the deadline once the clone exists. When commit
// is true the fire goroutine commits the card's change first, as a card that got its work in
// before the wall would have.
func deadlineWithRepo(t *testing.T, commit bool) (job, stdout, stderr string) {
	t.Helper()
	bin := nativeHarness(t)
	root, slot := aSlot(t)

	origin := filepath.Join(t.TempDir(), "origin.git")
	seed := filepath.Join(t.TempDir(), "seed")
	require.NoError(t, os.MkdirAll(seed, 0o755))
	deadlineGitCmd(t, filepath.Dir(origin), "init", "-q", "--bare", "-b", "main", origin)
	deadlineGitCmd(t, seed, "init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(seed, "base.txt"), []byte("base\n"), 0o644))
	deadlineGitCmd(t, seed, "add", "base.txt")
	deadlineGitCmd(t, seed, "commit", "-q", "-m", "base")
	deadlineGitCmd(t, seed, "push", "-q", origin, "main")

	cardPath := filepath.Join(root, "card.md")
	require.NoError(t, os.WriteFile(cardPath, []byte("FAKE-CARD-REPO "+origin+"\nFAKE-SLEEP 300\n"), 0o644))
	jobDir := filepath.Join(slot, "jobs", "dlreport")
	changed := filepath.Join(jobDir, "repo", "base.txt")

	realDeadline := nativeDeadline
	t.Cleanup(func() { nativeDeadline = realDeadline })
	quit := make(chan struct{})
	defer close(quit)
	nativeDeadline = func(time.Duration) (<-chan time.Time, func() bool) {
		fire := make(chan time.Time)
		go func() {
			tick := time.NewTicker(5 * time.Millisecond)
			defer tick.Stop()
			for {
				if raw, err := os.ReadFile(changed); err == nil && strings.Contains(string(raw), "the card's fix") {
					if commit {
						deadlineGitCmd(t, filepath.Join(jobDir, "repo"), "checkout", "-q", "-b", "card/fix")
						deadlineGitCmd(t, filepath.Join(jobDir, "repo"), "commit", "-q", "-am", "the card's fix")
					}
					close(fire)
					return
				}
				select {
				case <-quit:
					return
				case <-tick.C:
				}
			}
		}()
		return fire, func() bool { return true }
	}

	args := []string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
		"--label", "dlreport", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "10m", "--no-wall"}
	var out, errb strings.Builder
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		_ = run(args, strings.NewReader(""), &out, &errb, time.Now())
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Minute):
		t.Fatalf("the run did not return after its deadline fired:\n%s", errb.String())
	}
	return jobDir, out.String(), errb.String()
}

func TestNativeDeadlineWithACommitWritesTheDeadlineReport(t *testing.T) {
	t.Parallel()
	windowsIsNotABench(t)
	job, stdout, stderr := deadlineWithRepo(t, true)
	path := filepath.Join(job, "RESULT.md")
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "a card killed at its deadline with a commit leaves a report:\n%s\n%s", stdout, stderr)
	require.True(t, strings.HasPrefix(string(raw), swarm.DeadlineResultPrefix+"branch=card/fix "), "%s", raw)
	require.Contains(t, string(raw), "commits=1 pushed=no")
	require.True(t, swarm.DeadlineReport(path))
	require.Contains(t, stderr, "ended at its deadline with commits")
	// The report is evidence of an absence: the verdict does not become OK because of it.
	require.Contains(t, stdout, "NATIVE INCOMPLETE ", "%s", stdout)
	require.False(t, nativeLeftAResult(job), "the machinery's report is not the card's result")
}

func TestNativeDeadlineWithNoCommitWritesNoReport(t *testing.T) {
	t.Parallel()
	windowsIsNotABench(t)
	job, stdout, stderr := deadlineWithRepo(t, false)
	_, err := os.Stat(filepath.Join(job, "RESULT.md"))
	require.True(t, os.IsNotExist(err), "no commit, no report:\n%s\n%s", stdout, stderr)
	require.Contains(t, stdout, "NATIVE INCOMPLETE ")
}

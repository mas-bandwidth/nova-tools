//go:build functional

package swarm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests exec whole programs -- the fake runner this package builds
// (testdata/fakerunner), git, sqlite3 or sh -- so they are the functional tier's,
// not unit tests (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s and
// frugal with the machine's cores). The rest of the file's tests stay in the
// unit tier.

func TestStageCardUsesMirrorAndDissociates(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	src := filepath.Join(root, "src")
	mirror := filepath.Join(root, "home", "nova-bench", "mirror", "repo.git")
	target := filepath.Join(root, "jobs", "card-1", "repo")
	jobDir := filepath.Join(root, "jobs", "card-1")

	require.NoError(t, os.MkdirAll(src, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(mirror), 0o755))

	execCmd(t, src, "git", "init", "-q")
	execCmd(t, src, "git", "config", "user.name", "test")
	execCmd(t, src, "git", "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(src, "file.txt"), []byte("hello"), 0o644))
	execCmd(t, src, "git", "add", "file.txt")
	execCmd(t, src, "git", "commit", "-q", "-m", "commit 1")
	sha1 := strings.TrimSpace(execCmd(t, src, "git", "rev-parse", "HEAD"))

	execCmd(t, root, "git", "clone", "--mirror", "-q", src, mirror)

	card := []byte("base-repo: https://example.com/mas-bandwidth/repo.git\nbase-sha: " + sha1 + "\n")
	res, err := StageCard(StageOptions{Identity: testStageIdentity,
		Card:      card,
		TargetDir: target,
		JobDir:    jobDir,
		BenchHome: filepath.Join(root, "home"),
		BenchName: "testhost",
		Timeout:   30 * time.Second,
	})
	require.NoError(t, err, "StageCard failed: %v", err)
	require.True(t, res.Staged, "expected Staged=true")
	require.Equal(t, mirror, res.Mirror, "expected mirror %s, got %s", mirror, res.Mirror)

	// Verify alternates does not exist because --dissociate was used
	alternates := filepath.Join(target, ".git", "objects", "info", "alternates")
	_, err = os.Stat(alternates)
	require.True(t, os.IsNotExist(err), "alternates file exists: staging was not dissociated: %v", err)

	head := strings.TrimSpace(execCmd(t, target, "git", "rev-parse", "HEAD"))
	require.Equal(t, sha1, head, "expected HEAD=%s, got %s", sha1, head)

	origin := strings.TrimSpace(execCmd(t, target, "git", "remote", "get-url", "origin"))
	require.Equal(t, "https://example.com/mas-bandwidth/repo.git", origin, "expected origin URL https://example.com/mas-bandwidth/repo.git, got %s", origin)
}

func TestStageCardTimesOutAndWritesResult(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	src := filepath.Join(root, "src")
	mirror := filepath.Join(root, "home", "nova-bench", "mirror", "repo.git")
	target := filepath.Join(root, "jobs", "card-1", "repo")
	jobDir := filepath.Join(root, "jobs", "card-1")
	require.NoError(t, os.MkdirAll(jobDir, 0o755))

	require.NoError(t, os.MkdirAll(src, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(mirror), 0o755))

	execCmd(t, src, "git", "init", "-q")
	execCmd(t, src, "git", "config", "user.name", "test")
	execCmd(t, src, "git", "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(src, "file.txt"), []byte("hello"), 0o644))
	execCmd(t, src, "git", "add", "file.txt")
	execCmd(t, src, "git", "commit", "-q", "-m", "commit 1")
	sha1 := strings.TrimSpace(execCmd(t, src, "git", "rev-parse", "HEAD"))
	execCmd(t, root, "git", "clone", "--mirror", "-q", src, mirror)

	card := []byte("base-repo: https://example.com/mas-bandwidth/repo.git\nbase-sha: " + sha1 + "\n")
	// 1ns timeout ensures immediate context deadline exceeded
	res, err := StageCard(StageOptions{Identity: testStageIdentity,
		Card:      card,
		TargetDir: target,
		JobDir:    jobDir,
		BenchHome: filepath.Join(root, "home"),
		BenchName: "hulk",
		Timeout:   1 * time.Nanosecond,
	})
	require.Error(t, err, "expected timeout error")
	require.ErrorIs(t, err, ErrStageTimeout, "expected ErrStageTimeout, got %v", err)
	require.True(t, res.TimedOut, "expected TimedOut=true")

	resultFile := filepath.Join(jobDir, "RESULT.md")
	raw, err := os.ReadFile(resultFile)
	require.NoError(t, err, "RESULT.md not written on timeout: %v", err)
	lines := strings.Split(string(raw), "\n")
	expectedLine1 := "RESULT: BLOCKED stage-timeout hulk 1"
	require.Equal(t, expectedLine1, lines[0], "expected line 1 %q, got %q", expectedLine1, lines[0])
}

// TestStageUsesTheBenchMirrorAndTimesOut tests that staging uses the bench mirror,
// fails on a clone that would go to GitHub without a mirror, and times out when exceeding deadline.
func TestStageUsesTheBenchMirrorAndTimesOut(t *testing.T) {
	t.Run("uses bench mirror and dissociates", TestStageCardUsesMirrorAndDissociates)
	t.Run("fails without bench mirror", TestStageCardFailsWithoutMirror)
	t.Run("times out and writes result", TestStageCardTimesOutAndWritesResult)
	t.Run("a clone that hangs past the timeout ends within it", testStageHungCloneEndsAtTheTimeout)
}

// testStageHungCloneEndsAtTheTimeout is the hulk shape from #2882: git clone hung 43-65
// minutes because its helper (git-remote-https, index-pack) kept the output pipe open. The
// fake git here backgrounds a sleep that holds stdout and waits on it, so killing git alone
// is not enough: staging must return within the timeout plus a small grace, name the timeout
// on RESULT.md, and leave no grandchild holding the card.
func testStageHungCloneEndsAtTheTimeout(t *testing.T) {
	root := t.TempDir()
	mirror := filepath.Join(root, "home", "nova-bench", "mirror", "repo.git")
	require.NoError(t, os.MkdirAll(mirror, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mirror, "HEAD"), []byte("ref: refs/heads/dev\n"), 0o644))
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	fake := "#!/bin/sh\nsleep 60 &\nwait\n"
	require.NoError(t, testbin.WriteExecutable(filepath.Join(bin, "git"), []byte(fake), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	jobDir := filepath.Join(root, "jobs", "card-1")
	require.NoError(t, os.MkdirAll(jobDir, 0o755))
	card := []byte("base-repo: https://example.com/mas-bandwidth/repo.git\nbase-sha: 09fbedc9052145b20677501a1dbcb5f5ba9c87d4\n")
	// StageCard blocks, so the event under test -- the call returning instead
	// of riding the hung 60s sleep -- is read off a done channel, not the wall
	// clock: a select against the generous NOVA_TEST_WAIT bound (default 30s)
	// is the poll-for-the-event shape, never a literal short deadline.
	type stageOutcome struct {
		res StageResult
		err error
	}
	done := make(chan stageOutcome, 1)
	go func() {
		res, err := StageCard(StageOptions{Identity: testStageIdentity,
			Card:      card,
			TargetDir: filepath.Join(jobDir, "repo"),
			JobDir:    jobDir,
			BenchHome: filepath.Join(root, "home"),
			BenchName: "hulk",
			Timeout:   1 * time.Second,
		})
		done <- stageOutcome{res: res, err: err}
	}()
	var res StageResult
	var err error
	select {
	case out := <-done:
		res, err = out.res, out.err
	case <-time.After(testWait()):
		t.Fatalf("staging did not return within %s on a clone that hung past a 1s timeout; the timeout is not hard", testWait())
	}
	require.ErrorIs(t, err, ErrStageTimeout, "a hung clone must end ErrStageTimeout with TimedOut; got err=%v res=%+v", err, res)
	require.True(t, res.TimedOut, "a hung clone must end ErrStageTimeout with TimedOut; got err=%v res=%+v", err, res)
	raw, rerr := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
	require.NoError(t, rerr, "RESULT.md not written on timeout: %v", rerr)
	first := strings.SplitN(string(raw), "\n", 2)[0]
	require.Equal(t, "RESULT: BLOCKED stage-timeout hulk 1", first, "RESULT.md line 1 = %q", first)
}

func execCmd(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s %s in %s: %v\n%s", name, strings.Join(args, " "), dir, err, string(out))
	return string(out)
}

// TestStageCardRefusesWhenOriginCannotBeRepointed holds the never-silent refusal at the
// set-url step: a checkout cloned from the mirror whose origin could not be pointed back at
// the card's repository still names the mirror, and a push from it goes to the mirror. The
// stage is refused with git's own words and the command that failed, never handed to the
// card. The step is made to fail for real: the staging seam names a remote the clone does
// not have, so git answers `No such remote`.
func TestStageCardRefusesWhenOriginCannotBeRepointed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	src := filepath.Join(root, "src")
	mirror := filepath.Join(root, "home", "nova-bench", "mirror", "repo.git")
	target := filepath.Join(root, "jobs", "card-1", "repo")
	jobDir := filepath.Join(root, "jobs", "card-1")
	for _, d := range []string{src, filepath.Dir(mirror), jobDir} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	execCmd(t, src, "git", "init", "-q")
	execCmd(t, src, "git", "config", "user.name", "test")
	execCmd(t, src, "git", "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(src, "file.txt"), []byte("hello"), 0o644))
	execCmd(t, src, "git", "add", "file.txt")
	execCmd(t, src, "git", "commit", "-q", "-m", "commit 1")
	sha1 := strings.TrimSpace(execCmd(t, src, "git", "rev-parse", "HEAD"))
	execCmd(t, root, "git", "clone", "--mirror", "-q", src, mirror)

	repointed := false
	card := []byte("base-repo: https://example.com/owner/repo.git\nbase-sha: " + sha1 + "\n")
	res, err := StageCard(StageOptions{Identity: testStageIdentity,
		Card:      card,
		TargetDir: target,
		JobDir:    jobDir,
		BenchHome: filepath.Join(root, "home"),
		BenchName: "testhost",
		Timeout:   30 * time.Second,
		git: func(ctx context.Context, args ...string) *exec.Cmd {
			for i, a := range args {
				if a == "set-url" {
					repointed = true
					args = append([]string(nil), args...)
					args[i+1] = "no-such-remote"
				}
			}
			return stageGit(ctx, args...)
		},
	})
	require.True(t, repointed, "the set-url step never ran; the test did not reach the refusal")
	require.Error(t, err, "a checkout whose origin still names the mirror was staged: %+v", res)
	assert.False(t, res.Staged)
	assert.Contains(t, err.Error(), "git remote set-url origin https://example.com/owner/repo.git failed in "+target, "the refusal names the failed command and the checkout")
	assert.Contains(t, err.Error(), "No such remote", "the refusal carries git's own words")
}

// testWait is the allowed poll bound: NOVA_TEST_WAIT when set, thirty seconds
// otherwise. It is read at the call, never written as a constant, so a loaded
// machine lengthens the wait rather than flaking a test.
func testWait() time.Duration {
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

// A frame's base and branch stage in place of the card's header lines
// (docs/SPEC-CARD-CONTRACT.md layer 2): the checkout is at the frame's sha (a
// rework's previous pushed head) on the frame's branch, whatever the card's
// prose says, and a branch git would read as an option is refused.
func TestStageCardStagesTheFramesCommitOnItsBranch(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	require.NoError(t, os.MkdirAll(src, 0o755))
	execCmd(t, src, "git", "init", "-q", "-b", "main")
	execCmd(t, src, "git", "config", "user.name", "test")
	execCmd(t, src, "git", "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(src, "f"), []byte("base"), 0o644))
	execCmd(t, src, "git", "add", "f")
	execCmd(t, src, "git", "commit", "-q", "-m", "base")
	origin := filepath.Join(root, "origin.git")
	execCmd(t, root, "git", "clone", "-q", "--bare", src, origin)
	execCmd(t, src, "git", "switch", "-q", "-c", "sprint/c1.w1")
	require.NoError(t, os.WriteFile(filepath.Join(src, "f"), []byte("attempt 1"), 0o644))
	execCmd(t, src, "git", "commit", "-q", "-am", "attempt 1")
	prev := strings.TrimSpace(execCmd(t, src, "git", "rev-parse", "HEAD"))
	execCmd(t, src, "git", "push", "-q", origin, "sprint/c1.w1")

	target := filepath.Join(root, "jobs", "c1.w2", "repo")
	card := []byte("c1: the card\nBASE: main\nThe work is branch sprint/c1.w2 from sprint/c1.w1, says the prose.\n")
	res, err := StageCard(StageOptions{Identity: testStageIdentity,
		Card: card, TargetDir: target, JobDir: filepath.Dir(target), BenchHome: filepath.Join(root, "home"), BenchName: "testhost",
		Timeout: 30 * time.Second, Base: &CardBase{Repo: origin, Sha: prev, Ref: "main", Named: origin}, Branch: "sprint/c1.w2",
	})
	require.NoError(t, err)
	assert.True(t, res.Staged)
	assert.Equal(t, prev, res.BaseSha)
	assert.Equal(t, "sprint/c1.w2", res.Branch)
	assert.Equal(t, prev, strings.TrimSpace(execCmd(t, target, "git", "rev-parse", "HEAD")))
	assert.Equal(t, "sprint/c1.w2", strings.TrimSpace(execCmd(t, target, "git", "symbolic-ref", "--short", "HEAD")))

	_, err = StageCard(StageOptions{Identity: testStageIdentity, Card: card, TargetDir: filepath.Join(root, "jobs", "x", "repo"), BenchHome: filepath.Join(root, "home"),
		Timeout: 30 * time.Second, Base: &CardBase{Repo: origin, Ref: "main", Named: origin}, Branch: "-x"})
	assert.ErrorContains(t, err, "starts with '-'")
}

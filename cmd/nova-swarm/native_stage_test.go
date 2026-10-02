//go:build functional

package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// captureStageLine redirects os.Stdout around fn and returns the first line fn prints that
// starts with "STAGE ", event-based: a goroutine scans the pipe as fn runs and signals a
// channel the instant the line lands, so the test needs no sleep and no poll. The 30s
// select arm is only a safety net far above any real staging time in these tests (a local
// clone of a few-commit repo), never the thing the test waits on.
func captureStageLine(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err, "os.Pipe")
	orig := os.Stdout
	os.Stdout = w
	lineCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "STAGE ") {
				lineCh <- line
				return
			}
		}
		close(lineCh)
	}()

	fn()

	os.Stdout = orig
	w.Close()
	defer r.Close()
	select {
	case line, ok := <-lineCh:
		require.True(t, ok, "nativeRun printed no STAGE OK/FAIL line")
		return line
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for a STAGE OK/FAIL line (30s safety bound, not the wait itself)")
		return ""
	}
}

// TestStageUsesTheBenchMirrorAndTimesOut tests the stage requirements of mas-bandwidth/nova-tools#2882:
// 1. A card pointing to a remote repo without a bench mirror fails staging without going to GitHub.
// 2. Staging clones from the bench's local mirror (--reference or clone --shared) and dissociates.
// 3. Staging past the timeout ends the card RESULT: BLOCKED stage-timeout <bench> <secs> and writes usage.tsv.
func TestStageUsesTheBenchMirrorAndTimesOut(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	benchHome := filepath.Join(root, "bench-home")
	srcDir := filepath.Join(root, "src-repo")
	mirrorDir := filepath.Join(benchHome, "nova-bench", "mirror", "sample-repo.git")

	require.NoError(t, os.MkdirAll(srcDir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(mirrorDir), 0o755))

	runGit(t, srcDir, "init", "-q")
	runGit(t, srcDir, "config", "user.name", "test")
	runGit(t, srcDir, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "README.md"), []byte("# sample\n"), 0o644))
	runGit(t, srcDir, "add", "README.md")
	runGit(t, srcDir, "commit", "-q", "-m", "initial commit")
	headSha := strings.TrimSpace(runGit(t, srcDir, "rev-parse", "HEAD"))

	runGit(t, root, "clone", "--mirror", "-q", srcDir, mirrorDir)

	t.Run("fails on clone that would go to github without mirror", func(t *testing.T) {
		cardText := []byte("base-repo: https://example.com/mas-bandwidth/missing-mirror.git\nbase-sha: 1234567890123456789012345678901234567890\n")
		var errOut bytes.Buffer
		_, code := nativeRun(nativeRunConfig{
			binary:       bin,
			model:        "fake/fake-model",
			label:        "card-no-mirror",
			card:         cardText,
			slotDir:      slot,
			root:         root,
			benchHome:    benchHome,
			benchName:    "bench-1",
			stageTimeout: 30 * time.Second,
			deadline:     30 * time.Second,
			noWall:       true,
		}, &errOut)
		require.Equal(t, 2, code, "expected refusal exit code 2 when no bench mirror exists, got %d:\n%s", code, errOut.String())
		require.Contains(t, errOut.String(), "no bench mirror", "expected error mentioning no bench mirror, got:\n%s", errOut.String())
	})

	t.Run("stages cleanly from bench mirror", func(t *testing.T) {
		cardText := []byte("base-repo: https://example.com/mas-bandwidth/sample-repo.git\nbase-sha: " + headSha + "\n")
		var errOut bytes.Buffer
		res, code := nativeRun(nativeRunConfig{
			binary:       bin,
			model:        "fake/fake-model",
			label:        "card-mirrored",
			card:         cardText,
			slotDir:      slot,
			root:         root,
			benchHome:    benchHome,
			benchName:    "bench-1",
			stageTimeout: 30 * time.Second,
			deadline:     30 * time.Second,
			noWall:       true,
		}, &errOut)
		require.Equal(t, 0, code, "expected code 0 on successful staging, got %d:\n%s", code, errOut.String())
		require.Equal(t, 0, res.rc, "expected harness rc 0, got %d", res.rc)
		repoDir := filepath.Join(slot, "jobs", "card-mirrored", "repo")
		_, err := os.Stat(repoDir)
		require.NoError(t, err, "expected repo dir %s to exist", repoDir)
		alternates := filepath.Join(repoDir, ".git", "objects", "info", "alternates")
		_, err = os.Stat(alternates)
		require.True(t, os.IsNotExist(err), "staging did not dissociate from mirror: alternates file exists at %s", alternates)
		// the checkout commits under the pool's identity (aSlot's identity.tsv), never one
		// the tool carries
		require.Equal(t, "Pool Worker", strings.TrimSpace(runGit(t, repoDir, "config", "--local", "--get", "user.name")))
		require.Equal(t, "pool@example.com", strings.TrimSpace(runGit(t, repoDir, "config", "--local", "--get", "user.email")))
	})

	t.Run("times out and writes blocked result and usage", func(t *testing.T) {
		cardText := []byte("base-repo: https://example.com/mas-bandwidth/sample-repo.git\nbase-sha: " + headSha + "\n")
		var errOut bytes.Buffer
		res, code := nativeRun(nativeRunConfig{
			binary:       bin,
			model:        "fake/fake-model",
			label:        "card-timeout",
			card:         cardText,
			slotDir:      slot,
			root:         root,
			benchHome:    benchHome,
			benchName:    "bench-1",
			stageTimeout: 1 * time.Nanosecond,
			deadline:     30 * time.Second,
			noWall:       true,
		}, &errOut)
		require.NotEqual(t, 0, code, "expected non-zero code on stage timeout, got %d", code)
		require.Equal(t, "stage-timeout", res.end, "expected res.end = stage-timeout, got %q", res.end)

		resultPath := filepath.Join(slot, "jobs", "card-timeout", "RESULT.md")
		resultBytes, err := os.ReadFile(resultPath)
		require.NoError(t, err, "RESULT.md not written on stage timeout")
		resultLines := strings.Split(string(resultBytes), "\n")
		expectedLine1 := "RESULT: BLOCKED stage-timeout bench-1 1"
		require.Equal(t, expectedLine1, resultLines[0], "expected RESULT.md line 1 %q, got %q", expectedLine1, resultLines[0])

		usagePath := filepath.Join(slot, "jobs", "card-timeout", "usage.tsv")
		usageBytes, err := os.ReadFile(usagePath)
		require.NoError(t, err, "usage.tsv not written on stage timeout")
		require.Contains(t, string(usageBytes), "card-timeout", "usage.tsv does not mention card-timeout:\n%s", string(usageBytes))
	})
}

// TestStageOKLinePrintsOnSuccessfulStage is nova-tools#3050: the launcher (#187)
// detaches 2s after it sees a STAGE OK line on stdout instead of waiting the full 135s.
// #3050's staging returned silently, so every launch waited out the timeout and printed
// STAGE UNSEEN even though staging had already finished. RED WITHOUT THE FIX: nativeRun
// prints nothing starting with "STAGE " and captureStageLine's 30s safety arm fires.
func TestStageOKLinePrintsOnSuccessfulStage(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	benchHome := filepath.Join(root, "bench-home")
	srcDir := filepath.Join(root, "src-repo")
	mirrorDir := filepath.Join(benchHome, "nova-bench", "mirror", "sample-repo.git")

	require.NoError(t, os.MkdirAll(srcDir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(mirrorDir), 0o755))
	runGit(t, srcDir, "init", "-q")
	runGit(t, srcDir, "config", "user.name", "test")
	runGit(t, srcDir, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "README.md"), []byte("# sample\n"), 0o644))
	runGit(t, srcDir, "add", "README.md")
	runGit(t, srcDir, "commit", "-q", "-m", "initial commit")
	headSha := strings.TrimSpace(runGit(t, srcDir, "rev-parse", "HEAD"))
	runGit(t, root, "clone", "--mirror", "-q", srcDir, mirrorDir)

	cardText := []byte("base-repo: https://example.com/mas-bandwidth/sample-repo.git\nbase-sha: " + headSha + "\n")
	var errOut bytes.Buffer
	var code int
	line := captureStageLine(t, func() {
		_, code = nativeRun(nativeRunConfig{
			binary:       bin,
			model:        "fake/fake-model",
			label:        "card-stage-ok",
			card:         cardText,
			slotDir:      slot,
			root:         root,
			benchHome:    benchHome,
			benchName:    "bench-1",
			stageTimeout: 30 * time.Second,
			deadline:     30 * time.Second,
			noWall:       true,
		}, &errOut)
	})
	require.Equal(t, 0, code, "expected code 0 on successful staging, got %d:\n%s", code, errOut.String())
	require.True(t, strings.HasPrefix(line, "STAGE OK "), "expected a STAGE OK line, got %q", line)
	for _, want := range []string{"bench=bench-1", "repo=https://example.com/mas-bandwidth/sample-repo.git", "base=" + headSha[:8], "secs="} {
		require.Contains(t, line, want, "STAGE OK line missing %q:\n%s", want, line)
	}
}

// TestStageFailLinePrintsOnStagingFailure covers the other half of #3050: a launcher that
// only ever watches for STAGE OK hangs the same way on a staging failure, so the failure
// path prints STAGE FAIL for exactly the same reason. RED WITHOUT THE FIX: no line printed.
func TestStageFailLinePrintsOnStagingFailure(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	benchHome := filepath.Join(root, "bench-home")

	cardText := []byte("base-repo: https://example.com/mas-bandwidth/missing-mirror.git\nbase-sha: 1234567890123456789012345678901234567890\n")
	var errOut bytes.Buffer
	var code int
	line := captureStageLine(t, func() {
		_, code = nativeRun(nativeRunConfig{
			binary:       bin,
			model:        "fake/fake-model",
			label:        "card-stage-fail",
			card:         cardText,
			slotDir:      slot,
			root:         root,
			benchHome:    benchHome,
			benchName:    "bench-1",
			stageTimeout: 30 * time.Second,
			deadline:     30 * time.Second,
			noWall:       true,
		}, &errOut)
	})
	require.Equal(t, 2, code, "expected refusal exit code 2 when no bench mirror exists, got %d:\n%s", code, errOut.String())
	require.True(t, strings.HasPrefix(line, "STAGE FAIL "), "expected a STAGE FAIL line, got %q", line)
	require.Contains(t, line, "bench=bench-1", "STAGE FAIL line missing bench/repo fields:\n%s", line)
	require.Contains(t, line, "repo=https://example.com/mas-bandwidth/missing-mirror.git", "STAGE FAIL line missing bench/repo fields:\n%s", line)
}

// TestStagePushedHeaderStagesRepoBeforeTheModel is nova-tools#3711's DONE-WHEN: a card whose
// header is `REPO: mas-bandwidth/nova-tools` / `BASE: dev` / `base-sha: <sha>` (the header
// every pushed card carries) is staged into <job>/repo at that sha from the bench mirror
// before the model starts, and STAGE OK names the repo and the 8-char sha. RED WITHOUT THE
// FIX: `STAGE OK bench=bench-1 repo= base= secs=0` and no <job>/repo, as on all 12 quack-0925b
// cards.
func TestStagePushedHeaderStagesRepoBeforeTheModel(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	benchHome := filepath.Join(root, "bench-home")
	srcDir := filepath.Join(root, "src-repo")
	mirrorDir := filepath.Join(benchHome, "nova-bench", "mirror", "nova-tools.git")
	require.NoError(t, os.MkdirAll(srcDir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(mirrorDir), 0o755))
	runGit(t, srcDir, "init", "-q")
	runGit(t, srcDir, "checkout", "-q", "-b", "dev")
	runGit(t, srcDir, "config", "user.name", "test")
	runGit(t, srcDir, "config", "user.email", "test@example.com")
	var shas []string
	for _, body := range []string{"one\n", "two\n"} {
		require.NoError(t, os.WriteFile(filepath.Join(srcDir, "README.md"), []byte(body), 0o644))
		runGit(t, srcDir, "add", "README.md")
		runGit(t, srcDir, "commit", "-q", "-m", "commit")
		shas = append(shas, strings.TrimSpace(runGit(t, srcDir, "rev-parse", "HEAD")))
	}
	runGit(t, root, "clone", "--mirror", "-q", srcDir, mirrorDir)
	base := shas[0] // the older commit: the check is the sha, not the mirror's tip

	cardText := []byte("RESULT: s00-0302-quack-bench-flash sha=" + base[:12] + "\n" +
		"KIND: fix\nTYPE: code\nREPO: mas-bandwidth/nova-tools\nBASE: dev\n" +
		"base-sha: " + base + "\nPATHS: docs/quack/s00-0302-quack-bench-flash.txt\n")
	var errOut bytes.Buffer
	var code int
	line := captureStageLine(t, func() {
		_, code = nativeRun(nativeRunConfig{
			binary:       bin,
			model:        "fake/fake-model",
			label:        "card-pushed-header",
			card:         cardText,
			slotDir:      slot,
			root:         root,
			benchHome:    benchHome,
			benchName:    "bench-1",
			stageTimeout: 30 * time.Second,
			deadline:     30 * time.Second,
			noWall:       true,
		}, &errOut)
	})
	require.Equal(t, 0, code, "expected code 0, got %d:\n%s", code, errOut.String())
	require.True(t, strings.HasPrefix(line, "STAGE OK "), "expected a STAGE OK line, got %q", line)
	for _, want := range []string{"bench=bench-1", "/mas-bandwidth/nova-tools.git", "base=" + base[:8]} {
		require.Contains(t, line, want, "STAGE OK line missing %q:\n%s", want, line)
	}
	repoDir := filepath.Join(slot, "jobs", "card-pushed-header", "repo")
	head := strings.TrimSpace(runGit(t, repoDir, "rev-parse", "HEAD"))
	require.Equal(t, base, head, "<job>/repo HEAD = %s, want base-sha %s", head, base)
	wantBranch := swarm.CardStageBranch("test-owner", cardText)
	require.NotEqual(t, "", wantBranch, "the card's branch is %q, want one derived from its label", wantBranch)
	require.NotEqual(t, wantBranch, swarm.CardStageBranch("test-owner", nil), "the card's branch is %q, want one derived from its label", wantBranch)
	branch := strings.TrimSpace(runGit(t, repoDir, "rev-parse", "--abbrev-ref", "HEAD"))
	require.Equal(t, wantBranch, branch, "<job>/repo is on %q, want the card's branch %q", branch, wantBranch)
}

// TestStageNamedRepoNotStagedIsRefused is the other half of #3711: a card that names a repo
// and ends with nothing staged prints `STAGE FAIL ... reason=no-repo-staged` and is refused
// (exit 2) before any child starts, never `STAGE OK repo= base=` into an empty job dir.
// A card with no repo line at all keeps today's behaviour (STAGE OK, nothing to stage).
func TestStageNamedRepoNotStagedIsRefused(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	benchHome := filepath.Join(root, "bench-home")

	cardText := []byte("RESULT: card-unreadable-repo sha=123456789012\nREPO: nova-tools\nBASE: dev\nbase-sha: 1234567890123456789012345678901234567890\n")
	var errOut bytes.Buffer
	var code int
	line := captureStageLine(t, func() {
		_, code = nativeRun(nativeRunConfig{
			binary:       bin,
			model:        "fake/fake-model",
			label:        "card-no-repo-staged",
			card:         cardText,
			slotDir:      slot,
			root:         root,
			benchHome:    benchHome,
			benchName:    "bench-1",
			stageTimeout: 30 * time.Second,
			deadline:     30 * time.Second,
			noWall:       true,
		}, &errOut)
	})
	require.Equal(t, 2, code, "expected refusal exit 2, got %d:\n%s", code, errOut.String())
	require.True(t, strings.HasPrefix(line, "STAGE FAIL "), "expected STAGE FAIL bench=bench-1 repo=nova-tools ... reason=no-repo-staged, got %q", line)
	require.Contains(t, line, "reason=no-repo-staged", "expected STAGE FAIL bench=bench-1 repo=nova-tools ... reason=no-repo-staged, got %q", line)
	require.Contains(t, line, "bench=bench-1", "expected STAGE FAIL bench=bench-1 repo=nova-tools ... reason=no-repo-staged, got %q", line)
	require.Contains(t, line, "repo=nova-tools", "expected STAGE FAIL bench=bench-1 repo=nova-tools ... reason=no-repo-staged, got %q", line)
	require.Contains(t, errOut.String(), "nothing was staged", "refusal does not name the cause:\n%s", errOut.String())

	// No repo line at all: nothing to stage, the card runs as before.
	plain := []byte("RESULT: card-no-repo sha=123456789012\nKIND: read\n")
	errOut.Reset()
	line = captureStageLine(t, func() {
		_, code = nativeRun(nativeRunConfig{
			binary:       bin,
			model:        "fake/fake-model",
			label:        "card-no-repo",
			card:         plain,
			slotDir:      slot,
			root:         root,
			benchHome:    benchHome,
			benchName:    "bench-1",
			stageTimeout: 30 * time.Second,
			deadline:     30 * time.Second,
			noWall:       true,
		}, &errOut)
	})
	require.Equal(t, 0, code, "card with no repo line: code=%d line=%q, want 0 and STAGE OK\n%s", code, line, errOut.String())
	require.True(t, strings.HasPrefix(line, "STAGE OK "), "card with no repo line: code=%d line=%q, want 0 and STAGE OK\n%s", code, line, errOut.String())
}

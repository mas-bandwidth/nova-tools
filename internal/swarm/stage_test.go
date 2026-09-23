package swarm

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseCardBase(t *testing.T) {
	card := []byte("RESULT test-1 sha=1234\nbase-repo: https://example.com/mas-bandwidth/nova-tools.git\nbase-sha: 09fbedc9052145b20677501a1dbcb5f5ba9c87d4\nKIND: fix\n")
	repo, sha, ok := ParseCardBase(card)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if repo != "https://example.com/mas-bandwidth/nova-tools.git" {
		t.Fatalf("unexpected repo: %s", repo)
	}
	if sha != "09fbedc9052145b20677501a1dbcb5f5ba9c87d4" {
		t.Fatalf("unexpected sha: %s", sha)
	}

	cardFallback := []byte("RESULT test-2\nBASE: dev@09fbedc9052145b20677501a1dbcb5f5ba9c87d4\nSTEP 1. " + forgeClone("mas-bandwidth/schema") + " repo\n")
	repo, sha, ok = ParseCardBase(cardFallback)
	if !ok {
		t.Fatal("expected ok=true for fallback")
	}
	if repo != defaultProbeBase+"/mas-bandwidth/schema.git" {
		t.Fatalf("unexpected repo: %s", repo)
	}
	if sha != "09fbedc9052145b20677501a1dbcb5f5ba9c87d4" {
		t.Fatalf("unexpected sha: %s", sha)
	}
}

func TestFindBenchMirror(t *testing.T) {
	home := t.TempDir()
	mirrorDir := filepath.Join(home, "nova-bench", "mirror", "nova-tools.git")
	if err := os.MkdirAll(mirrorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mirrorDir, "HEAD"), []byte("ref: refs/heads/dev\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	found := FindBenchMirror(home, "https://example.com/mas-bandwidth/nova-tools.git")
	if found != mirrorDir {
		t.Fatalf("expected %s, got %s", mirrorDir, found)
	}

	notFound := FindBenchMirror(home, "https://example.com/mas-bandwidth/nonexistent.git")
	if notFound != "" {
		t.Fatalf("expected empty string, got %s", notFound)
	}
}

func TestStageCardUsesMirrorAndDissociates(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	mirror := filepath.Join(root, "home", "nova-bench", "mirror", "repo.git")
	target := filepath.Join(root, "jobs", "card-1", "repo")
	jobDir := filepath.Join(root, "jobs", "card-1")

	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(mirror), 0o755); err != nil {
		t.Fatal(err)
	}

	execCmd(t, src, "git", "init", "-q")
	execCmd(t, src, "git", "config", "user.name", "test")
	execCmd(t, src, "git", "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(src, "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	execCmd(t, src, "git", "add", "file.txt")
	execCmd(t, src, "git", "commit", "-q", "-m", "commit 1")
	sha1 := strings.TrimSpace(execCmd(t, src, "git", "rev-parse", "HEAD"))

	execCmd(t, root, "git", "clone", "--mirror", "-q", src, mirror)

	card := []byte("base-repo: https://example.com/mas-bandwidth/repo.git\nbase-sha: " + sha1 + "\n")
	res, err := StageCard(StageOptions{
		Card:      card,
		TargetDir: target,
		JobDir:    jobDir,
		BenchHome: filepath.Join(root, "home"),
		BenchName: "testhost",
		Timeout:   30 * time.Second,
	})
	if err != nil {
		t.Fatalf("StageCard failed: %v", err)
	}
	if !res.Staged {
		t.Fatal("expected Staged=true")
	}
	if res.Mirror != mirror {
		t.Fatalf("expected mirror %s, got %s", mirror, res.Mirror)
	}

	// Verify alternates does not exist because --dissociate was used
	alternates := filepath.Join(target, ".git", "objects", "info", "alternates")
	if _, err := os.Stat(alternates); !os.IsNotExist(err) {
		t.Fatalf("alternates file exists: staging was not dissociated: %v", err)
	}

	head := strings.TrimSpace(execCmd(t, target, "git", "rev-parse", "HEAD"))
	if head != sha1 {
		t.Fatalf("expected HEAD=%s, got %s", sha1, head)
	}

	origin := strings.TrimSpace(execCmd(t, target, "git", "remote", "get-url", "origin"))
	if origin != "https://example.com/mas-bandwidth/repo.git" {
		t.Fatalf("expected origin URL https://example.com/mas-bandwidth/repo.git, got %s", origin)
	}
}

func TestStageCardFailsWithoutMirror(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "jobs", "card-1", "repo")
	jobDir := filepath.Join(root, "jobs", "card-1")

	card := []byte("base-repo: https://example.com/mas-bandwidth/missing-mirror.git\nbase-sha: 09fbedc9052145b20677501a1dbcb5f5ba9c87d4\n")
	res, err := StageCard(StageOptions{
		Card:      card,
		TargetDir: target,
		JobDir:    jobDir,
		BenchHome: filepath.Join(root, "home"),
		BenchName: "testhost",
		Timeout:   30 * time.Second,
	})
	if err == nil {
		t.Fatal("expected StageCard to fail when cloning directly without mirror")
	}
	if res.Staged {
		t.Fatal("expected Staged=false")
	}
	if !strings.Contains(err.Error(), "no bench mirror") {
		t.Fatalf("expected error mentioning no bench mirror, got: %v", err)
	}
}

func TestStageCardTimesOutAndWritesResult(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	mirror := filepath.Join(root, "home", "nova-bench", "mirror", "repo.git")
	target := filepath.Join(root, "jobs", "card-1", "repo")
	jobDir := filepath.Join(root, "jobs", "card-1")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(mirror), 0o755); err != nil {
		t.Fatal(err)
	}

	execCmd(t, src, "git", "init", "-q")
	execCmd(t, src, "git", "config", "user.name", "test")
	execCmd(t, src, "git", "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(src, "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	execCmd(t, src, "git", "add", "file.txt")
	execCmd(t, src, "git", "commit", "-q", "-m", "commit 1")
	sha1 := strings.TrimSpace(execCmd(t, src, "git", "rev-parse", "HEAD"))
	execCmd(t, root, "git", "clone", "--mirror", "-q", src, mirror)

	card := []byte("base-repo: https://example.com/mas-bandwidth/repo.git\nbase-sha: " + sha1 + "\n")
	// 1ns timeout ensures immediate context deadline exceeded
	res, err := StageCard(StageOptions{
		Card:      card,
		TargetDir: target,
		JobDir:    jobDir,
		BenchHome: filepath.Join(root, "home"),
		BenchName: "hulk",
		Timeout:   1 * time.Nanosecond,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, ErrStageTimeout) {
		t.Fatalf("expected ErrStageTimeout, got %v", err)
	}
	if !res.TimedOut {
		t.Fatal("expected TimedOut=true")
	}

	resultFile := filepath.Join(jobDir, "RESULT.md")
	raw, err := os.ReadFile(resultFile)
	if err != nil {
		t.Fatalf("RESULT.md not written on timeout: %v", err)
	}
	lines := strings.Split(string(raw), "\n")
	expectedLine1 := "RESULT: BLOCKED stage-timeout hulk 1"
	if lines[0] != expectedLine1 {
		t.Fatalf("expected line 1 %q, got %q", expectedLine1, lines[0])
	}
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
	if err := os.MkdirAll(mirror, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mirror, "HEAD"), []byte("ref: refs/heads/dev\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := "#!/bin/sh\nsleep 60 &\nwait\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	jobDir := filepath.Join(root, "jobs", "card-1")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
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
		res, err := StageCard(StageOptions{
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
	if !errors.Is(err, ErrStageTimeout) || !res.TimedOut {
		t.Fatalf("a hung clone must end ErrStageTimeout with TimedOut; got err=%v res=%+v", err, res)
	}
	raw, rerr := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
	if rerr != nil {
		t.Fatalf("RESULT.md not written on timeout: %v", rerr)
	}
	if first := strings.SplitN(string(raw), "\n", 2)[0]; first != "RESULT: BLOCKED stage-timeout hulk 1" {
		t.Fatalf("RESULT.md line 1 = %q", first)
	}
}

// testWait is the allowed poll bound: NOVA_TEST_WAIT when set, thirty
// seconds otherwise. It never drives an assertion on its own; it only caps
// how long a test waits for an event before giving up.
func testWait() time.Duration {
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

func execCmd(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s in %s: %v\n%s", name, strings.Join(args, " "), dir, err, string(out))
	}
	return string(out)
}

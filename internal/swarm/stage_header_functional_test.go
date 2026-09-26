//go:build functional

package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests exec whole programs -- the fake runner this package builds
// (testdata/fakerunner), git, sqlite3 or sh -- so they are the functional tier's,
// not unit tests (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s and
// frugal with the machine's cores). The rest of the file's tests stay in the
// unit tier.

// TestStageCardStagesThePushedHeaderFromTheMirror is the #3711 DONE-WHEN at the staging
// layer: the REPO:/BASE:/base-sha: card is cloned from the bench mirror into <job>/repo, at
// base-sha (the OLDER commit, so the check is the sha and not the mirror's tip), on a named
// branch the card can commit on.
func TestStageCardStagesThePushedHeaderFromTheMirror(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	benchHome, first, second := stageMirror(t, root)
	jobDir := filepath.Join(root, "jobs", "card-1")
	target := filepath.Join(jobDir, "repo")

	res, err := StageCard(StageOptions{
		Card:      pushedHeader(first),
		TargetDir: target,
		JobDir:    jobDir,
		BenchHome: benchHome,
		BenchName: "hulk",
		Timeout:   30 * time.Second,
	})
	if err != nil {
		t.Fatalf("StageCard: %v", err)
	}
	if !res.Staged {
		t.Fatalf("Staged=false: %+v", res)
	}
	if want := filepath.Join(benchHome, "nova-bench", "mirror", "nova-tools.git"); res.Mirror != want {
		t.Fatalf("Mirror = %q, want %q", res.Mirror, want)
	}
	if head := strings.TrimSpace(execCmd(t, target, "git", "rev-parse", "HEAD")); head != first {
		t.Fatalf("<job>/repo HEAD = %s, want base-sha %s (dev tip is %s)", head, first, second)
	}
	if res.BaseSha != first {
		t.Fatalf("res.BaseSha = %q, want %q", res.BaseSha, first)
	}
	if b := strings.TrimSpace(execCmd(t, target, "git", "rev-parse", "--abbrev-ref", "HEAD")); b != "rowan/s00-0302-quack-hulk-flash" || res.Branch != b {
		t.Fatalf("branch = %q (res %q), want rowan/s00-0302-quack-hulk-flash", b, res.Branch)
	}
	if _, err := os.Stat(filepath.Join(target, ".git", "objects", "info", "alternates")); !os.IsNotExist(err) {
		t.Fatalf("staging did not dissociate from the mirror: %v", err)
	}
	if origin := strings.TrimSpace(execCmd(t, target, "git", "remote", "get-url", "origin")); origin != defaultProbeBase+"/mas-bandwidth/nova-tools.git" {
		t.Fatalf("origin = %q", origin)
	}
}

// TestStageCardChecksOutTheBaseRefWithoutASha: BASE: dev with no base-sha stages the
// mirror's dev tip.
func TestStageCardChecksOutTheBaseRefWithoutASha(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	benchHome, _, second := stageMirror(t, root)
	jobDir := filepath.Join(root, "jobs", "card-2")
	target := filepath.Join(jobDir, "repo")
	card := []byte("RESULT: ref-card sha=000000000000\nREPO: mas-bandwidth/nova-tools\nBASE: dev\n")
	res, err := StageCard(StageOptions{Card: card, TargetDir: target, JobDir: jobDir, BenchHome: benchHome, BenchName: "hulk", Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("StageCard: %v", err)
	}
	if head := strings.TrimSpace(execCmd(t, target, "git", "rev-parse", "HEAD")); head != second || res.BaseSha != second || !res.Staged {
		t.Fatalf("HEAD = %s res=%+v, want the dev tip %s", head, res, second)
	}
}

// stageMirror builds <benchHome>/nova-bench/mirror/nova-tools.git as a bare mirror of a
// throwaway repo with two commits on dev, and returns benchHome and both shas.
func stageMirror(t *testing.T, root string) (benchHome, first, second string) {
	t.Helper()
	src := filepath.Join(root, "src")
	benchHome = filepath.Join(root, "home")
	mirror := filepath.Join(benchHome, "nova-bench", "mirror", "nova-tools.git")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(mirror), 0o755); err != nil {
		t.Fatal(err)
	}
	execCmd(t, src, "git", "init", "-q")
	execCmd(t, src, "git", "checkout", "-q", "-b", "dev")
	execCmd(t, src, "git", "config", "user.name", "test")
	execCmd(t, src, "git", "config", "user.email", "test@example.com")
	for i, body := range []string{"one\n", "two\n"} {
		if err := os.WriteFile(filepath.Join(src, "file.txt"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		execCmd(t, src, "git", "add", "file.txt")
		execCmd(t, src, "git", "commit", "-q", "-m", "commit "+body)
		sha := strings.TrimSpace(execCmd(t, src, "git", "rev-parse", "HEAD"))
		if i == 0 {
			first = sha
		} else {
			second = sha
		}
	}
	execCmd(t, root, "git", "clone", "--mirror", "-q", src, mirror)
	return benchHome, first, second
}

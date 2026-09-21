package pulse

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// setupGitRepo creates a temporary git repository with a base commit on master/main.
func setupGitRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.email", "emma@mas-bandwidth.com")
	runGit(t, repo, "config", "user.name", "Emma Antigravity")
	if err := os.WriteFile(filepath.Join(repo, "alpha.txt"), []byte("line 1\nline 2\nline 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "alpha.txt")
	runGit(t, repo, "commit", "-m", "initial alpha")
	return repo
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}

// cut-kind-recut-apply-clean: S1 mechanical rebase in `cut --kind recut`.
// When the previous card's diff applies cleanly to the tip, `applied: clean`
// is recorded in the card header, the card lands in the red lane, and the diff
// is inlined for the worker (#2498 S1).
func TestCutKindRecutMechanicalApplyClean(t *testing.T) {
	repo := setupGitRepo(t)
	dir := t.TempDir()
	out, queue := filepath.Join(dir, "pending"), filepath.Join(dir, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a feature branch with a change to alpha.txt (modifying line 2).
	runGit(t, repo, "checkout", "-b", "feat")
	if err := os.WriteFile(filepath.Join(repo, "alpha.txt"), []byte("line 1\nline 2 feat\nline 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "commit", "-am", "feat change")
	diffOut := runGit(t, repo, "diff", "HEAD~1")
	diffFile := filepath.Join(dir, "prior.diff")
	if err := os.WriteFile(diffFile, []byte(diffOut), 0o644); err != nil {
		t.Fatal(err)
	}

	// Go back to the base branch, add a non-conflicting change (new file or line 0).
	runGit(t, repo, "checkout", "master")
	if err := os.WriteFile(filepath.Join(repo, "beta.txt"), []byte("beta line 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "beta.txt")
	runGit(t, repo, "commit", "-m", "advance tip with non-conflicting beta")
	tipSHA := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))

	code, line, errs, card := cutKind(t, CutKindInput{
		Kind:     "recut",
		Repo:     "mas-bandwidth/nova-tools",
		Head:     tipSHA,
		DiffFile: diffFile,
		Dir:      repo,
		Title:    "rebase alpha feat onto tip",
		Out:      out,
		Queue:    queue,
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errs)
	}
	if !strings.Contains(line, "CUT CARD card=card-1.md") || !strings.Contains(line, "kind=recut") {
		t.Errorf("stdout = %q", line)
	}

	// Verify line 1 has sha=<sha12> binding.
	wantRE := regexp.MustCompile(`^RESULT: CARD-1 sha=([0-9a-f]{12}) recut of nova-tools at ` + regexp.QuoteMeta(tipSHA[:12]))
	line1 := strings.SplitN(card, "\n", 2)[0]
	m := wantRE.FindStringSubmatch(line1)
	if m == nil {
		t.Fatalf("line 1 = %q\nwant match %s", line1, wantRE.String())
	}
	sum := sha256.Sum256([]byte(strings.Join(strings.Split(card, "\n")[1:], "\n")))
	if m[1] != hex.EncodeToString(sum[:])[:12] {
		t.Errorf("sha=%s, want %s", m[1], hex.EncodeToString(sum[:])[:12])
	}

	// Verify header fields: KIND: recut, applied: clean.
	if !strings.Contains(card, "KIND: recut\n") {
		t.Errorf("card missing KIND: recut:\n%s", card)
	}
	if !strings.Contains(card, "applied: clean\n") {
		t.Errorf("card missing applied: clean in header:\n%s", card)
	}

	// Verify diff is inlined.
	if !strings.Contains(card, "line 2 feat") {
		t.Errorf("card does not inline the prior diff:\n%s", card)
	}

	// Verify it landed in the red priority lane.
	if matches, _ := filepath.Glob(filepath.Join(queue, "lanes", "red", "*.card")); len(matches) != 1 {
		t.Errorf("recut card is not in lanes/red: %v", matches)
	}
}

// cut-kind-recut-apply-conflict: When the previous card's diff conflicts with the tip,
// `applied: conflict` is recorded in the card header (#2498 S1).
func TestCutKindRecutMechanicalApplyConflict(t *testing.T) {
	repo := setupGitRepo(t)
	dir := t.TempDir()
	out, queue := filepath.Join(dir, "pending"), filepath.Join(dir, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}

	// Feature branch modifying line 2.
	runGit(t, repo, "checkout", "-b", "feat2")
	if err := os.WriteFile(filepath.Join(repo, "alpha.txt"), []byte("line 1\nline 2 feat2\nline 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "commit", "-am", "feat2 change")
	diffOut := runGit(t, repo, "diff", "HEAD~1")
	diffFile := filepath.Join(dir, "prior.diff")
	if err := os.WriteFile(diffFile, []byte(diffOut), 0o644); err != nil {
		t.Fatal(err)
	}

	// Base branch also modifies line 2 differently (creating conflict).
	runGit(t, repo, "checkout", "master")
	if err := os.WriteFile(filepath.Join(repo, "alpha.txt"), []byte("line 1\nline 2 conflict\nline 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "commit", "-am", "conflicting change on master")
	tipSHA := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))

	code, line, errs, card := cutKind(t, CutKindInput{
		Kind:     "recut",
		Repo:     "mas-bandwidth/nova-tools",
		Head:     tipSHA,
		DiffFile: diffFile,
		Dir:      repo,
		Title:    "conflicting rebase onto tip",
		Out:      out,
		Queue:    queue,
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errs)
	}
	if !strings.Contains(line, "CUT CARD card=card-1.md") || !strings.Contains(line, "kind=recut") {
		t.Errorf("stdout = %q", line)
	}

	if !strings.Contains(card, "KIND: recut\n") {
		t.Errorf("card missing KIND: recut:\n%s", card)
	}
	if !strings.Contains(card, "applied: conflict\n") {
		t.Errorf("card missing applied: conflict in header:\n%s", card)
	}
	if strings.Contains(card, "applied: clean\n") {
		t.Errorf("conflicting patch recorded applied: clean:\n%s", card)
	}
}

// cut-kind-recut-refusals: Missing diff/hold file, missing dir, or unreadable diff refuse cleanly.
func TestCutKindRecutRefusals(t *testing.T) {
	dir := t.TempDir()
	out, queue := filepath.Join(dir, "pending"), filepath.Join(dir, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. Neither diff-file nor hold-file.
	var bufOut, bufErr bytes.Buffer
	code := CutKind(CutKindInput{
		Kind:   "recut",
		Repo:   "mas-bandwidth/nova-tools",
		Out:    out,
		Queue:  queue,
		Stdout: &bufOut,
		Stderr: &bufErr,
	})
	if code != 2 {
		t.Errorf("no diff or hold exit = %d, want 2", code)
	}
	if !strings.Contains(bufErr.String(), "CUT REFUSED") || !strings.Contains(bufErr.String(), "--diff-file") {
		t.Errorf("stderr = %q, want CUT REFUSED naming --diff-file", bufErr.String())
	}

	// 2. Diff file does not exist.
	bufOut.Reset()
	bufErr.Reset()
	code = CutKind(CutKindInput{
		Kind:     "recut",
		Repo:     "mas-bandwidth/nova-tools",
		DiffFile: filepath.Join(dir, "nonexistent.diff"),
		Out:      out,
		Queue:    queue,
		Stdout:   &bufOut,
		Stderr:   &bufErr,
	})
	if code != 2 {
		t.Errorf("nonexistent diff exit = %d, want 2", code)
	}
	if !strings.Contains(bufErr.String(), "CUT REFUSED") || !strings.Contains(bufErr.String(), "nonexistent.diff") {
		t.Errorf("stderr = %q, want CUT REFUSED naming diff file", bufErr.String())
	}

	// 3. Dir is not a git repo.
	nonGitDir := filepath.Join(dir, "not-git")
	_ = os.MkdirAll(nonGitDir, 0o755)
	dummyDiff := filepath.Join(dir, "dummy.diff")
	_ = os.WriteFile(dummyDiff, []byte("diff content"), 0o644)

	bufOut.Reset()
	bufErr.Reset()
	code = CutKind(CutKindInput{
		Kind:     "recut",
		Repo:     "mas-bandwidth/nova-tools",
		DiffFile: dummyDiff,
		Dir:      nonGitDir,
		Out:      out,
		Queue:    queue,
		Stdout:   &bufOut,
		Stderr:   &bufErr,
	})
	if code != 2 {
		t.Errorf("non-git dir exit = %d, want 2", code)
	}
	if !strings.Contains(bufErr.String(), "CUT REFUSED") || !strings.Contains(bufErr.String(), "--dir") {
		t.Errorf("stderr = %q, want CUT REFUSED naming --dir", bufErr.String())
	}
}

// cut-kind-recut-with-hold-and-diff: When both --hold-file and --diff-file are given,
// the card carries the HOLD metadata and the mechanical rebase applied header.
func TestCutKindRecutWithHoldAndDiff(t *testing.T) {
	repo := setupGitRepo(t)
	dir := t.TempDir()
	out, queue := filepath.Join(dir, "pending"), filepath.Join(dir, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}

	runGit(t, repo, "checkout", "-b", "feat3")
	if err := os.WriteFile(filepath.Join(repo, "alpha.txt"), []byte("line 1\nline 2 feat3\nline 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "commit", "-am", "feat3 change")
	diffOut := runGit(t, repo, "diff", "HEAD~1")
	diffFile := filepath.Join(dir, "prior.diff")
	if err := os.WriteFile(diffFile, []byte(diffOut), 0o644); err != nil {
		t.Fatal(err)
	}

	holdFile := filepath.Join(dir, "hold.md")
	holdContent := "DISPOSITION who=Johnny head=d080cec1d2a5afcaef2b696840389e91e769a1d1 verdict=HOLD score=4/10\n" +
		"PATHS: alpha.txt\n" +
		"TEST: ./internal/pulse TestSample\n" +
		"BASE: dev\n" +
		"base-sha: c7104413f20c2897e6a9c4c19cc7158b0f4ea15c\n"
	if err := os.WriteFile(holdFile, []byte(holdContent), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, errs, card := cutKind(t, CutKindInput{
		Kind:     "recut",
		Repo:     "mas-bandwidth/nova-tools",
		HoldFile: holdFile,
		DiffFile: diffFile,
		Dir:      repo,
		Out:      out,
		Queue:    queue,
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errs)
	}

	if !strings.Contains(card, "KIND: recut\n") {
		t.Errorf("card missing KIND: recut:\n%s", card)
	}
	if !strings.Contains(card, "applied: clean\n") {
		t.Errorf("card missing applied: clean:\n%s", card)
	}
	if !strings.Contains(card, "BASE: dev\n") {
		t.Errorf("card missing BASE: dev:\n%s", card)
	}
	if !strings.Contains(card, "PATHS: alpha.txt\n") {
		t.Errorf("card missing PATHS: alpha.txt:\n%s", card)
	}
	if !strings.Contains(card, "HOLD: DISPOSITION who=Johnny") {
		t.Errorf("card missing HOLD evidence:\n%s", card)
	}
}

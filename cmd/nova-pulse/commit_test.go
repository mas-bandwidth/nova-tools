package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCommitVerb drives `nova-pulse commit` end to end over a real job directory, because
// the thing it replaces -- bin/harvest-priority's $commit_step -- could only ever be tried
// on the fleet, and that is how it stranded 24 DONE cells and then every bench for seven
// hours (nova-tools #2549).

func vgit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.name=Fixture", "-c", "user.email=f@example.invalid",
		"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=dev"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func vwrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commitVerbFixture lays down a swarm root holding two jobs: one DONE and one DONEish.
func commitVerbFixture(t *testing.T) (root, cards, mirror string) {
	t.Helper()
	tmp := t.TempDir()
	mirror = filepath.Join(tmp, "mirror")
	root = filepath.Join(tmp, "swarm")
	cards = filepath.Join(tmp, "cards")
	if err := os.MkdirAll(mirror, 0o755); err != nil {
		t.Fatal(err)
	}
	vgit(t, mirror, "init", "-q")
	vgit(t, mirror, "checkout", "-q", "-B", "dev")
	vwrite(t, filepath.Join(mirror, "internal/pulse/work.go"), "package x // base\n")
	vgit(t, mirror, "add", "-A")
	vgit(t, mirror, "commit", "-q", "-m", "base")

	for _, tc := range []struct{ label, line2 string }{{"fix-a", "DONE (both runs green)"}, {"fix-b", "DONEish"}} {
		job := filepath.Join(root, "slot-"+tc.label, "jobs", tc.label)
		if err := os.MkdirAll(job, 0o755); err != nil {
			t.Fatal(err)
		}
		vgit(t, tmp, "clone", "-q", mirror, filepath.Join(job, "repo"))
		vwrite(t, filepath.Join(job, "repo", "internal/pulse/work.go"), "package x // "+tc.label+"\n")
		vwrite(t, filepath.Join(job, "RESULT.md"),
			"RESULT "+tc.label+" -- the card's line 1\n"+tc.line2+"\nREPO mas-bandwidth/nova-tools\n")
		vwrite(t, filepath.Join(cards, tc.label+".md"),
			"RESULT "+tc.label+"\nBASE: dev\nPATHS: internal/pulse/work.go\n")
	}
	return root, cards, mirror
}

func TestCommitVerb(t *testing.T) {
	root, cards, mirror := commitVerbFixture(t)
	var out, errb bytes.Buffer
	code := run([]string{"commit", "--root", root, "--bench", "studio",
		"--cards", cards, "--mirror", mirror, "--max", "0"}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out.String(), errb.String())
	}
	got := out.String()
	// EVERY VERDICT IS PRINTED. The bash's driver dropped every SKIP line -- 0 SKIP
	// lines in the whole log against 12,784 REFUSED, while the pass reported skipped=96
	// every time (#2549) -- so both a commit and a skip must be on stdout by name.
	for _, want := range []string{
		"COMMIT COMMITTED bench=studio label=fix-a branch=rowan/fix-a",
		"COMMIT SKIP bench=studio label=fix-b reason=not-done",
		"COMMIT BENCH OK bench=studio jobs=2 committed=1 skipped=1 refused=0",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout does not carry %q\n---\n%s", want, got)
		}
	}
	// The DONE card's branch carries exactly the card's declared path.
	repo := filepath.Join(root, "slot-fix-a", "jobs", "fix-a", "repo")
	if b := vgit(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); b != "rowan/fix-a" {
		t.Errorf("branch = %q, want rowan/fix-a", b)
	}
	if d := vgit(t, repo, "diff", "--name-only", "dev..HEAD"); d != "internal/pulse/work.go" {
		t.Errorf("diff = %q, want internal/pulse/work.go", d)
	}
	// The DONEish card was not touched at all.
	other := filepath.Join(root, "slot-fix-b", "jobs", "fix-b", "repo")
	if b := vgit(t, other, "rev-parse", "--abbrev-ref", "HEAD"); b != "dev" {
		t.Errorf("the DONEish job's branch = %q, want dev untouched", b)
	}
}

func TestCommitVerbDryRunChangesNothing(t *testing.T) {
	root, cards, mirror := commitVerbFixture(t)
	var out, errb bytes.Buffer
	if code := run([]string{"commit", "--root", root, "--cards", cards, "--mirror", mirror, "--dry-run"},
		&out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("exit = %d, want 0: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "dry-run=yes") {
		t.Errorf("the summary does not say it was a rehearsal:\n%s", out.String())
	}
	repo := filepath.Join(root, "slot-fix-a", "jobs", "fix-a", "repo")
	if b := vgit(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); b != "dev" {
		t.Errorf("a dry run changed the branch to %q", b)
	}
}

func TestCommitVerbRefusesWithoutRoot(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"commit"}, &out, &errb, time.Now().UTC()); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "root") {
		t.Errorf("the refusal does not name --root: %q", errb.String())
	}
}

func TestCommitVerbIsInTheUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("help exit = %d", code)
	}
	if !strings.Contains(out.String(), "nova-pulse commit  --root") {
		t.Error("the commit verb has no synopsis in the usage")
	}
}

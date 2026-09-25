package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// cardOrigin is a bare origin whose dev holds one committed base.txt: the base a card
// clones and changes.
func cardOrigin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(in string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = in
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	origin := filepath.Join(dir, "origin.git")
	seed := filepath.Join(dir, "seed")
	git(dir, "init", "-q", "--bare", origin)
	git(dir, "init", "-q", seed)
	write(t, filepath.Join(seed, "base.txt"), "base\n")
	git(seed, "add", "base.txt")
	git(seed, "commit", "-q", "-m", "base")
	git(seed, "push", "-q", origin, "HEAD:refs/heads/dev")
	git(dir, "--git-dir", origin, "symbolic-ref", "HEAD", "refs/heads/dev")
	return origin
}

// TestNativeHandsCardRepoToCardOut is the quack-test defect of 2026-09-24: under the card
// wrapper, native ran the card in <slot>/jobs/<label> and left its clone there, so the
// wrapper's commit step found nothing at $NOVA_CARD_OUT/repo and every DONE card ended
// NO-COMMIT. With NOVA_CARD_OUT set, native moves the job's repo there and copies the
// card's RESULT.md beside it, and the wrapper's commit step then commits the fix.
func TestNativeHandsCardRepoToCardOut(t *testing.T) {
	windowsIsNotABench(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	const label = "quack"
	out := filepath.Join(t.TempDir(), "job", "out") // the wrapper's out, not under the slot
	t.Setenv(swarm.CardOutEnv, out)
	cardPath := filepath.Join(root, "card.md")
	write(t, cardPath, "RESULT: quack fixed\nFAKE-CARD-REPO "+cardOrigin(t)+"\n")
	args := []string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model", "--label", label,
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--idle", "0", "--no-wall",
		"--results-root", filepath.Join(out, "native")}
	var stdout, stderr bytes.Buffer
	if rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now()); rc != 0 {
		t.Fatalf("native exits 0, got %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	job := filepath.Join(slot, "jobs", label)
	if raw, err := os.ReadFile(filepath.Join(job, "card-repo-err")); err == nil {
		t.Fatalf("the fake card could not clone: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(out, "repo", ".git")); err != nil {
		t.Fatalf("the card's repo was not handed to %s/repo: %v\nstderr:\n%s", out, err, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(job, "repo")); !os.IsNotExist(err) {
		t.Fatalf("the repo is still in the job %s (stat=%v); a hand-off moves it", job, err)
	}
	published, err := os.ReadFile(filepath.Join(job, "RESULT.md"))
	if err != nil {
		t.Fatal(err)
	}
	handed, err := os.ReadFile(filepath.Join(out, "RESULT.md"))
	if err != nil || !bytes.Equal(handed, published) {
		t.Fatalf("out/RESULT.md = %q, %v; want the card's own RESULT.md %q", handed, err, published)
	}
	res, err := card.CommitOutput(filepath.Join(out, "repo"), card.WrapperBranch("quack-0924", label, 1), "RESULT: quack fixed", "space")
	if err != nil || res.Note != "COMMITTED" || len(res.SHA) != 40 {
		t.Fatalf("the wrapper's commit step on the handed repo = %+v, %v; want COMMITTED", res, err)
	}
}

// TestHandOffCardOutRefusals: no NOVA_CARD_OUT is no hand-off and leaves the job as it
// was; an out inside the job is refused; an out that already holds a repo keeps it.
func TestHandOffCardOutRefusals(t *testing.T) {
	job := t.TempDir()
	if err := os.MkdirAll(filepath.Join(job, "repo", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if h, err := swarm.HandOffCardOut(job, ""); err != nil || h != (swarm.CardHandOff{}) {
		t.Fatalf("empty out = %+v, %v; want no hand-off", h, err)
	}
	if _, err := swarm.HandOffCardOut(job, filepath.Join(job, "out")); err == nil {
		t.Fatal("an out inside the job was accepted")
	}
	out := t.TempDir()
	if err := os.MkdirAll(filepath.Join(out, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if h, err := swarm.HandOffCardOut(job, out); err != nil || h.Repo != "" {
		t.Fatalf("an out that holds a repo = %+v, %v; want it kept", h, err)
	}
	if _, err := os.Stat(filepath.Join(job, "repo", ".git")); err != nil {
		t.Fatalf("the job's repo moved over an existing out/repo: %v", err)
	}
}

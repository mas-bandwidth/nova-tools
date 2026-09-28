package swarm

// SPEC-TOOLWORK.md §3 rules 1-2 (issue #1665, work item T21): staging sets the
// identity from the pool's identity.tsv, the job clone's local git config
// ignores the bench's gitconfig, and no staged symlink leaves the job root.
// Each test below is one red row of that issue; each failed before
// internal/swarm/staging.go existed and passes with it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePoolIdentity writes a pool's identity.tsv: header owner/name/email plus
// the pool's one identity row.
func writePoolIdentity(t *testing.T, poolDir, owner, name, email string) {
	t.Helper()
	if err := os.MkdirAll(poolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "owner\tname\temail\n" + owner + "\t" + name + "\t" + email + "\n"
	if err := os.WriteFile(filepath.Join(poolDir, "identity.tsv"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLaunchRefusesAPoolWithNoIdentity is red row
// `launch-refuses-a-pool-with-no-identity`: a pool with no identity row is
// refused at launch instead of staging a job under nobody's name.
func TestLaunchRefusesAPoolWithNoIdentity(t *testing.T) {
	t.Parallel()

	pool := t.TempDir()
	job := filepath.Join(t.TempDir(), "job")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPoolIdentity(pool); err == nil {
		t.Fatal("a pool with no identity.tsv loads an identity, want a refusal")
	} else if !strings.Contains(err.Error(), "identity") {
		t.Fatalf("refusal names the pool's identity, got %q", err)
	}
	if err := StageJob(pool, job, ""); err == nil {
		t.Fatal("launch over a pool with no identity row staged a job, want a refusal")
	} else if !strings.Contains(err.Error(), "identity") {
		t.Fatalf("launch refusal names the pool's identity, got %q", err)
	}

	// A header with no row is no identity either.
	if err := os.WriteFile(filepath.Join(pool, "identity.tsv"), []byte("owner\tname\temail\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := StageJob(pool, job, ""); err == nil {
		t.Fatal("launch over a header-only identity.tsv staged a job, want a refusal")
	}
}

// TestStageRefusesASymlinkOutOfTheJob is red row
// `stage-refuses-a-symlink-out-of-the-job`: no absolute symlink and no symlink
// resolving outside the job root survives staging; the refusal names the path.
func TestStageRefusesASymlinkOutOfTheJob(t *testing.T) {
	t.Parallel()

	pool := t.TempDir()
	writePoolIdentity(t, pool, "rowan", "Rowan Friend", "rowan@example.com")

	job := t.TempDir()
	if err := os.WriteFile(filepath.Join(job, "WORK.md"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An inside link is fine: staging keeps it.
	if err := os.Symlink("WORK.md", filepath.Join(job, "ok-link")); err != nil {
		t.Fatal(err)
	}
	if err := StageJob(pool, job, ""); err != nil {
		t.Fatalf("a tree with only an inside link is refused: %v", err)
	}

	// An absolute link is refused, by path.
	abs := filepath.Join(job, "abs-link")
	if err := os.Symlink("/etc/hostname", abs); err != nil {
		t.Fatal(err)
	}
	if err := StageJob(pool, job, ""); err == nil {
		t.Fatal("an absolute symlink in the staged tree staged clean, want a refusal")
	} else if !strings.Contains(err.Error(), "abs-link") {
		t.Fatalf("symlink refusal names the path, got %q", err)
	}
	if err := os.Remove(abs); err != nil {
		t.Fatal(err)
	}

	// A relative link resolving outside the root is refused too (#1557's
	// repo/dist shape), by path.
	rel := filepath.Join(job, "up-link")
	if err := os.Symlink("../outside", rel); err != nil {
		t.Fatal(err)
	}
	if err := CheckStagedTree(job); err == nil {
		t.Fatal("a symlink resolving outside the job root staged clean, want a refusal")
	} else if !strings.Contains(err.Error(), "up-link") {
		t.Fatalf("symlink refusal names the path, got %q", err)
	}
}

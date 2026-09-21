package swarm

// SPEC-TOOLWORK.md §3 rules 1-2 (issue #1665, work item T21): staging sets the
// identity from the pool's identity.tsv, the job clone's local git config
// ignores the bench's gitconfig, and no staged symlink leaves the job root.
// Each test below is one red row of that issue; each failed before
// internal/swarm/staging.go existed and passes with it.

import (
	"os"
	"os/exec"
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

func initCloneRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "base"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

// TestLaunchRefusesAPoolWithNoIdentity is red row
// `launch-refuses-a-pool-with-no-identity`: a pool with no identity row is
// refused at launch instead of staging a job under nobody's name.
func TestLaunchRefusesAPoolWithNoIdentity(t *testing.T) {
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

// TestStagedCloneIgnoresTheBenchGitconfig is red row
// `staged-clone-ignores-the-bench-gitconfig`: the staged clone carries the
// pool's identity in its LOCAL config and answers it even where the bench's
// own config says otherwise.
func TestStagedCloneIgnoresTheBenchGitconfig(t *testing.T) {
	pool := t.TempDir()
	writePoolIdentity(t, pool, "rowan", "Rowan Friend", "rowan@example.com")
	id, err := LoadPoolIdentity(pool)
	if err != nil {
		t.Fatalf("a pool with one identity row refuses to load: %v", err)
	}
	if id.Name != "Rowan Friend" || id.Email != "rowan@example.com" {
		t.Fatalf("identity row reads back wrong: %+v", id)
	}

	job := t.TempDir()
	repo := filepath.Join(job, "repo")
	initCloneRepo(t, repo)
	if err := StageCloneIdentity(repo, id); err != nil {
		t.Fatalf("staging the clone's identity: %v", err)
	}

	// The bench's own config says somebody else; the clone still answers the pool.
	benchHome := t.TempDir()
	benchConfig := filepath.Join(benchHome, ".gitconfig")
	if err := os.WriteFile(benchConfig, []byte("[user]\n\tname = Bench Ghost\n\temail = ghost@example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(),
		"HOME="+benchHome,
		"GIT_CONFIG_GLOBAL="+benchConfig,
		"GIT_CONFIG_NOSYSTEM=0",
	)
	for key, want := range map[string]string{
		"user.name":      "Rowan Friend",
		"user.email":     "rowan@example.com",
		"commit.gpgsign": "false",
		"core.hooksPath": "/dev/null",
	} {
		cmd := exec.Command("git", "config", key)
		cmd.Dir = repo
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git config %s in the staged clone: %v", key, err)
		}
		if got := strings.TrimSpace(string(out)); got != want {
			t.Errorf("staged clone %s = %q under the bench's gitconfig, want pool identity %q", key, got, want)
		}
	}

	// The launcher exports the bench config away: the exact two assignments, and
	// exports the pool identity as author and committer.
	env = StagingGitEnv(id)
	joined := strings.Join(env, "\n")
	for _, want := range []string{
		"GIT_AUTHOR_NAME=Rowan Friend",
		"GIT_AUTHOR_EMAIL=rowan@example.com",
		"GIT_COMMITTER_NAME=Rowan Friend",
		"GIT_COMMITTER_EMAIL=rowan@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("staging env holds no %q, got %q", want, joined)
		}
	}
}

// TestWorkerClonesAfterLaunchCarriesPoolIdentity tests that when a worker
// clones or initializes a git repository *after* launch (when .git did not
// exist during staging), the commit carries the pool identity from StagingGitEnv
// with zero leakage from any hostile bench gitconfig.
func TestWorkerClonesAfterLaunchCarriesPoolIdentity(t *testing.T) {
	pool := t.TempDir()
	writePoolIdentity(t, pool, "rowan", "Rowan Friend", "rowan@example.com")
	id, err := LoadPoolIdentity(pool)
	if err != nil {
		t.Fatalf("LoadPoolIdentity: %v", err)
	}

	job := t.TempDir()
	// Stage a fresh job with NO .git directory yet (worker clones after launch).
	if err := StageJob(pool, job, filepath.Join(job, "repo")); err != nil {
		t.Fatalf("StageJob: %v", err)
	}

	// Hostile bench git config with a ghost user.
	benchHome := t.TempDir()
	benchConfig := filepath.Join(benchHome, ".gitconfig")
	if err := os.WriteFile(benchConfig, []byte("[user]\n\tname = Bench Ghost\n\temail = ghost@example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Worker process env carries hostile bench config plus the exported StagingGitEnv.
	workerEnv := append(os.Environ(),
		"HOME="+benchHome,
		"GIT_CONFIG_GLOBAL="+benchConfig,
	)
	workerEnv = append(workerEnv, StagingGitEnv(id)...)

	// Worker initializes a repository inside the job and makes a commit.
	workerRepo := filepath.Join(job, "repo")
	if err := os.MkdirAll(workerRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = workerRepo
		cmd.Env = workerEnv
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out))
	}

	runGit("init")
	if err := os.WriteFile(filepath.Join(workerRepo, "file.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "file.txt")
	runGit("commit", "-m", "worker commit after launch")

	// Verify author and committer match pool identity exactly, with zero leakage.
	got := runGit("log", "-1", "--format=%an <%ae> %cn <%ce>")
	want := "Rowan Friend <rowan@example.com> Rowan Friend <rowan@example.com>"
	if got != want {
		t.Errorf("worker commit after launch = %q, want %q", got, want)
	}
}

// TestStageRefusesASymlinkOutOfTheJob is red row
// `stage-refuses-a-symlink-out-of-the-job`: no absolute symlink and no symlink
// resolving outside the job root survives staging; the refusal names the path.
func TestStageRefusesASymlinkOutOfTheJob(t *testing.T) {
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

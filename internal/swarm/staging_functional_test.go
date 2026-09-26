//go:build functional

package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests exec whole programs -- the fake runner this package builds
// (testdata/fakerunner), git, sqlite3 or sh -- so they are the functional tier's,
// not unit tests (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s and
// frugal with the machine's cores). The rest of the file's tests stay in the
// unit tier.

// TestStagedCloneIgnoresTheBenchGitconfig is red row
// `staged-clone-ignores-the-bench-gitconfig`: the staged clone carries the
// pool's identity in its LOCAL config and answers it even where the bench's
// own config says otherwise.
func TestStagedCloneIgnoresTheBenchGitconfig(t *testing.T) {
	t.Parallel()

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
	if _, err := LoadPoolIdentity(pool); err != nil {
		t.Fatalf("LoadPoolIdentity: %v", err)
	}

	job := t.TempDir()
	// Stage a fresh job with NO .git directory yet (worker clones after launch).
	if err := StageJob(pool, job, filepath.Join(job, "repo")); err != nil {
		t.Fatalf("StageJob: %v", err)
	}

	// Hostile bench git config with a ghost user in the process environment.
	benchHome := t.TempDir()
	benchConfig := filepath.Join(benchHome, ".gitconfig")
	if err := os.WriteFile(benchConfig, []byte("[user]\n\tname = Bench Ghost\n\temail = ghost@example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", benchConfig)
	t.Setenv("HOME", benchHome)

	// The harness child environment is built through childEnv across the supervisor boundary,
	// delivering the pool's identity and git config isolation.
	w := Worker{WorkerDir: filepath.Join(pool, "worker")}
	workerEnv := childEnv(w, 1, "task-1", "", pool)

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

package testutil_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

func TestLocalRemoteEnforcesDevIntegrity(t *testing.T) {
	t.Parallel()

	remote := testutil.NewLocalRemote(t, "dev")

	// Set up local working clone
	workDir := t.TempDir()
	git := func(args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-C", workDir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	if out, err := exec.Command("git", "clone", "--quiet", remote.URL, workDir).CombinedOutput(); err != nil {
		t.Fatalf("clone failed: %v\n%s", err, out)
	}

	// Make initial commit on dev
	_ = os.WriteFile(filepath.Join(workDir, "file.txt"), []byte("v1"), 0o644)
	if out, err := git("add", "file.txt"); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := git("commit", "-m", "v1"); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	// Fast-forward push to dev should succeed
	if out, err := git("push", "origin", "dev"); err != nil {
		t.Fatalf("fast-forward push should succeed: %v\n%s", err, out)
	}

	// Make second commit
	_ = os.WriteFile(filepath.Join(workDir, "file.txt"), []byte("v2"), 0o644)
	if out, err := git("commit", "-am", "v2"); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	if out, err := git("push", "origin", "dev"); err != nil {
		t.Fatalf("fast-forward push 2 should succeed: %v\n%s", err, out)
	}

	// Try non-fast-forward push (force push or push of older commit)
	if out, err := git("reset", "--hard", "HEAD~1"); err != nil {
		t.Fatalf("git reset: %v\n%s", err, out)
	}
	_ = os.WriteFile(filepath.Join(workDir, "file.txt"), []byte("v2-divergent"), 0o644)
	if out, err := git("commit", "-am", "divergent"); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	if out, err := git("push", "--force", "origin", "dev"); err == nil {
		t.Fatalf("non-fast-forward push must be refused by update hook, but succeeded: %s", out)
	}
}

//go:build functional

package swarm

import (
	"os"
	"path/filepath"
	"testing"
)

// These tests exec whole programs -- the fake runner this package builds
// (testdata/fakerunner), git, sqlite3 or sh -- so they are the functional tier's,
// not unit tests (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s and
// frugal with the machine's cores). The rest of the file's tests stay in the
// unit tier.

// AskedEnd asks the question of a job on disk: the capture's tail, the gather's own result
// lookup, and ./repo's own commit count -- the last of these through git, which is the one
// half the table test cannot reach.
func TestAskedEndReadsTheJobTheRunLeftBehind(t *testing.T) {
	t.Parallel()

	t.Run("a question, no result, no commit", func(t *testing.T) {
		job := t.TempDir()
		writeCapture(t, job, dogfoodHulkTail)

		got, asked := AskedEnd(job, 0)
		if !asked {
			t.Fatal("a job whose capture ends with a question was not read as asking")
		}
		if want := "Step 2: May I write RESULT.md?"; got != want {
			t.Fatalf("the question read off the job is %q, want %q", got, want)
		}
	})

	t.Run("a question the card then committed past", func(t *testing.T) {
		job := t.TempDir()
		writeCapture(t, job, "Should I also update the docs?\n")
		aRepoWithACommitPastItsBase(t, filepath.Join(job, "repo"))

		if got, asked := AskedEnd(job, 0); asked {
			t.Fatalf("a card that asked and then committed the work was read as asking: %q", got)
		}
	})

	t.Run("a question beside a result the card published under repo/", func(t *testing.T) {
		job := t.TempDir()
		writeCapture(t, job, dogfoodStudioTail)
		if err := os.MkdirAll(filepath.Join(job, "repo"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(job, "repo", "RESULT.md"), []byte("RESULT: x sha=1\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		if got, asked := AskedEnd(job, 0); asked {
			t.Fatalf("a card that published under repo/ was read as asking: %q", got)
		}
	})

	t.Run("a job with no capture at all", func(t *testing.T) {
		if got, asked := AskedEnd(t.TempDir(), 0); asked {
			t.Fatalf("a job whose harness said nothing was read as asking: %q", got)
		}
	})
}

func writeCapture(t *testing.T, job, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(job, "harness-output.log"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// aRepoWithACommitPastItsBase builds what repoCommits counts, the way wall_test.go already
// builds it: a repository on a branch, a base its remote ref names, and one commit past it.
func aRepoWithACommitPastItsBase(t *testing.T, dir string) {
	t.Helper()
	git(t, dir, "init", "-q", "-b", "work")
	git(t, dir, "config", "user.email", "card@example.invalid")
	git(t, dir, "config", "user.name", "card")
	base := commit(t, dir, "base")
	git(t, dir, "update-ref", "refs/remotes/origin/main", base)
	commit(t, dir, "the-work-the-card-did-after-it-asked")
}

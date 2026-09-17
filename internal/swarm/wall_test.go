package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ISSUE #918. A harness's permission denial is a TOOL ERROR the model routes around, not the
// end of the run; and when the run does die at the harness's own fence, the death NAMES the
// path it stopped at and KEEPS the commits the card already made, so the harvester can push
// the work. Two shapes, one line each.

// TestAFencedRunThatPublishedIsDone: a fake runner emits the harness's own auto-reject line
// and then publishes RESULT.md. The rejection is a tool error the model worked around, so the
// job is DONE -- the rejection on its own is not an outcome.
func TestAFencedRunThatPublishedIsDone(t *testing.T) {
	job := t.TempDir()
	reject := "\x1b[33;1m!\x1b[0m  permission requested: external_directory (/outside/scratch/*); auto-rejecting\n"
	if err := os.WriteFile(filepath.Join(job, "harness.log"), []byte(reject), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "RESULT.md"), []byte("a card line 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if report, ok := WallDeath(job, "a"); ok {
		t.Fatalf("a run that published a result is not a wall death, got %q", report)
	}
}

// TestAWallDeathNamesItsPathAndKeepsItsCommits: the same auto-reject line, no RESULT.md, and
// one commit in ./repo past its base. The death is a WALL that names the rejected path and
// the commit it kept -- `WALL task=a path=<p> commits=1 branch=<name>` -- so the harvester
// pushes the work instead of the commits being stranded with the card.
func TestAWallDeathNamesItsPathAndKeepsItsCommits(t *testing.T) {
	job := t.TempDir()
	reject := "\x1b[33;1m!\x1b[0m  permission requested: external_directory (/outside/scratch/*); auto-rejecting\n"
	if err := os.WriteFile(filepath.Join(job, "harness.log"), []byte(reject), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(job, "repo")
	git(t, repo, "init", "-q", "-b", "work")
	git(t, repo, "config", "user.email", "card@example.invalid")
	git(t, repo, "config", "user.name", "card")
	base := commit(t, repo, "base")
	git(t, repo, "update-ref", "refs/remotes/origin/main", base)
	commit(t, repo, "one")

	report, ok := WallDeath(job, "a")
	if !ok {
		t.Fatalf("a fenced run with no result is a wall death: %s", report)
	}
	for _, want := range []string{"WALL task=a", "path=/outside/scratch/*", "commits=1", "branch=work"} {
		if !strings.Contains(report, want) {
			t.Errorf("the wall report names %q, got %q", want, report)
		}
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func commit(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name+".txt"), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", name)
	return git(t, dir, "rev-parse", "HEAD")
}

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

// ISSUE #644, THE OTHER HALF. The harness's own fence and the OS wall are both machinery,
// and a card either of them stopped did not choose to publish nothing: naming it `no-result`
// sends a reader to the model for a wall this tool built.

// TestWallRefusedReadsTheFenceAndTheSandbox: the harness's permission auto-reject line and
// the sandbox's own refusals are one class, and the path they name and the last STEP the card
// printed are what the report line carries. RED WITHOUT THE CLASSIFIER: the log was read as a
// model that published nothing.
func TestWallRefusedReadsTheFenceAndTheSandbox(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		path string
		step string
		ok   bool
	}{
		{
			name: "the harness's permission auto-reject",
			in:   "STEP 1\ncd repo\nSTEP 2\n! permission requested: external_directory (/jobs/scratch/*); auto-rejecting\nError: The user rejected permission to use this specific tool call.\n",
			path: "/jobs/scratch/*", step: "2", ok: true,
		},
		{
			name: "the sandbox's own refusal",
			in:   "STEP 2\nSANDBOX REFUSED reason=bad_write: --tmp /outside is outside every --write; the temp directory is inside the wall\n",
			path: "/outside", step: "2", ok: true,
		},
		{
			name: "an operation not permitted on a path",
			in:   "STEP 4\nfatal: unable to access '/home/rowan/.gitconfig': Operation not permitted\n",
			path: "/home/rowan/.gitconfig", step: "4", ok: true,
		},
		{name: "a quiet log", in: "STEP 1\nread the spec\nSTEP 2\nwrote the report\n", step: "2"},
		{name: "an empty log"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := WallRefused([]byte(tc.in))
			if ok != tc.ok {
				t.Fatalf("WallRefused ok=%v, want %v (got %+v)", ok, tc.ok, got)
			}
			if ok && (got.Path != tc.path || got.Step != tc.step) {
				t.Fatalf("WallRefused = %+v; want path=%q step=%q", got, tc.path, tc.step)
			}
		})
	}
}

// TestWallLineNamesThePathTheStepAndTheSurvivingWork: the one line a wall death is reported
// on, and it names the commits a harvester can still push when the clone holds any.
func TestWallLineNamesThePathTheStepAndTheSurvivingWork(t *testing.T) {
	w := WallRefusal{Path: "/jobs/scratch/*", Step: "2"}
	if got, want := WallLine("card-8311", w, "", 0), "WALL task=card-8311 path=/jobs/scratch/* step=2"; got != want {
		t.Errorf("WallLine = %q, want %q", got, want)
	}
	if got, want := WallLine("card-8311", w, "rowan/fix", 3), "WALL task=card-8311 path=/jobs/scratch/* step=2 commits=3 branch=rowan/fix"; got != want {
		t.Errorf("WallLine = %q, want %q", got, want)
	}
	if got, want := WallLine("card-8311", WallRefusal{}, "", 0), "WALL task=card-8311 path=- step=-"; got != want {
		t.Errorf("WallLine = %q, want %q", got, want)
	}
}

// TestWallCommitsCountsPastTheBase: the branch and the commits a walled card left behind, so
// a harvester can still push them. A clone with no commits past its base says zero.
func TestWallCommitsCountsPastTheBase(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=rowan", "GIT_AUTHOR_EMAIL=rowan@example.com",
			"GIT_COMMITTER_NAME=rowan", "GIT_COMMITTER_EMAIL=rowan@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "--quiet", "--initial-branch=main")
	run("commit", "--quiet", "--allow-empty", "-m", "base")
	run("update-ref", "refs/remotes/origin/main", "HEAD")
	run("checkout", "--quiet", "-b", "rowan/fix")
	run("commit", "--quiet", "--allow-empty", "-m", "one")
	run("commit", "--quiet", "--allow-empty", "-m", "two")

	branch, commits, ok := WallCommits(repo)
	if !ok || branch != "rowan/fix" || commits != 2 {
		t.Fatalf("WallCommits = %q,%d,%v; want rowan/fix,2,true", branch, commits, ok)
	}
	if _, _, ok := WallCommits(filepath.Join(repo, "no-such-dir")); ok {
		t.Errorf("WallCommits on a directory that is not a clone reports nothing")
	}
}

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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

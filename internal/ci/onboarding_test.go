package ci

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// ONBOARDING.md, asserted for EVERY directory under cmd/ — by walking it, not
// by listing the tools. The point of the walk is the binary nobody has written
// yet: a sixth command joins the standard on the day it appears, rather than on
// the day somebody remembers to add it to a table here.
//
// Two of the five points are repo-wide facts and are checked here:
//
//	(a) the bare command prints usage, and the usage ends in an `example:` block
//	(c) README.md carries a `### First run` inside that tool's `## <tool>` section
//
// The rest are per-binary and live in each command's own firstrun_test.go,
// because only that package knows its fixture: the example lines are EXECUTED
// there, the refusal sentences are asserted there, and the README transcript is
// compared against real output there.

// notYetOnTheStandard names the commands whose onboarding work is in flight on
// another branch, with the branch named, so that a skip here is a dated pointer
// rather than a permanent exemption. Each entry earns its skip only while the
// README section is genuinely still missing: the moment that branch merges, the
// condition below stops firing and the entry can be deleted.
var notYetOnTheStandard = map[string]string{
	"nova-bus": "nova-bus-first-send (a draft verb, a tolerant send with notices, all refusals at once, and a `### First run`)",
}

func TestEveryCommandMeetsTheOnboardingStandard(t *testing.T) {
	root := repoRoot(t)
	readme := readFile(t, filepath.Join(root, "README.md"))

	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		found++
		tool := e.Name()
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			_, firstRunErr := onboarding.FirstRun(readme, tool)
			if branch, inFlight := notYetOnTheStandard[tool]; inFlight && firstRunErr != nil {
				t.Skipf("%s is being brought up to this standard on branch %s; delete its entry from notYetOnTheStandard when that branch merges (%v)", tool, branch, firstRunErr)
			}

			// (c) The README section a stranger reads before anything else.
			if firstRunErr != nil {
				t.Errorf("%v\n(ONBOARDING.md point 3: every tool's README section opens with `%s`)", firstRunErr, onboarding.FirstRunHeading)
			}

			// (a) The bare command prints usage, and it ends in runnable lines.
			exit, stdout, stderr := runBare(t, root, tool)
			if exit != 2 {
				t.Errorf("a bare `%s` exits %d, want 2 (could not run — no arguments is not an invocation)", tool, exit)
			}
			banner := stderr
			if banner == "" {
				banner = stdout
			}
			examples, err := onboarding.ExampleLines(banner, tool)
			if err != nil {
				t.Fatalf("%v\n(ONBOARDING.md point 1)\n\nwhat it printed:\n%s", err, banner)
			}
			for _, ex := range examples {
				if strings.Contains(ex, "<") || strings.Contains(ex, ">") {
					t.Errorf("the example %q still carries a placeholder; the `example:` block is for lines a stranger can paste, and the usage block above it is where <dir> and <file> belong", ex)
				}
			}
		})
	}
	if found == 0 {
		t.Fatal("no command directories found under cmd/; this test was looking in the wrong place and would have passed by checking nothing")
	}
}

// runBare builds the command and runs it with no arguments. It is BUILT rather
// than called as a package, because what this test is about is what a stranger
// meets at a shell prompt.
func runBare(t *testing.T, root, tool string) (exit int, stdout, stderr string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), tool)
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, "./cmd/"+tool)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building %s: %v\n%s", tool, err, out)
	}
	cmd := exec.Command(bin)
	cmd.Dir = root
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	var exitErr *exec.ExitError
	switch err := cmd.Run(); {
	case err == nil:
	case errors.As(err, &exitErr):
		exit = exitErr.ExitCode()
	default:
		t.Fatalf("running %s: %v", tool, err)
	}
	return exit, out.String(), errb.String()
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

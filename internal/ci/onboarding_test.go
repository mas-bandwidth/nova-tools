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

// docs/ONBOARDING.md, asserted for EVERY directory under cmd/ — by walking it, not
// by listing the tools. The point of the walk is the binary nobody has written
// yet: a sixth command joins the standard on the day it appears, rather than on
// the day somebody remembers to add it to a table here.
//
// Two of the five points are repo-wide facts and are checked here:
//
//	(a) `<tool> help` prints usage ending in an `example:` block, and a bare
//	    command refuses in ONE line that names that door
//	(c) docs/TESTS.md carries a `### First run` inside that tool's `## <tool>`
//	    section -- the transcript document the tests execute, which is the file
//	    this test reads. It is NOT README.md: the comments here said README for
//	    long enough to be believed, while the code below has always opened
//	    docs/TESTS.md (TESTS.md before this move).
//
// The rest are per-binary and live in each command's own firstrun_test.go,
// because only that package knows its fixture: the example lines are EXECUTED
// there, the refusal sentences are asserted there, and the transcript is
// compared against real output there.

// notYetOnTheStandard names the commands whose onboarding work is in flight on
// another branch, with the branch named, so that a skip here is a dated pointer
// rather than a permanent exemption. Each entry earns its skip only while the
// docs/TESTS.md section is genuinely still missing: the moment that branch
// merges, the condition below stops firing and the entry can be deleted.
// It is EMPTY, and an empty list is the point: nova-bus was the last entry, and it
// sat here after the branch it named had merged -- the draft verb, the tolerant send
// and the refusals all landed, the `### First run` did not, and the one tool the
// README sends a stranger to first was the one tool excused from the standard. The
// transcript is now in docs/TESTS.md and cmd/nova-bus/firstrun_test.go executes it.
// An entry added here has to name a branch that is genuinely open, and it stops
// firing the moment that branch's section lands.
var notYetOnTheStandard = map[string]string{}

func TestEveryCommandMeetsTheOnboardingStandard(t *testing.T) {
	root := repoRoot(t)
	transcripts := readFile(t, filepath.Join(root, "docs", "TESTS.md"))

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
			_, firstRunErr := onboarding.FirstRun(transcripts, tool)
			if branch, inFlight := notYetOnTheStandard[tool]; inFlight && firstRunErr != nil {
				t.Skipf("%s is being brought up to this standard on branch %s; delete its entry from notYetOnTheStandard when that branch merges (%v)", tool, branch, firstRunErr)
			}

			// (c) The transcript section in docs/TESTS.md that a test executes.
			if firstRunErr != nil {
				t.Errorf("%v\n(docs/ONBOARDING.md point 5(c): every tool's docs/TESTS.md section opens with `%s`)", firstRunErr, onboarding.FirstRunHeading)
			}

			// (a), first half: the bare command REFUSES in one line and names the
			// door. It used to be the banner itself, which cost between 1,900 and
			// 6,500 bytes to say that no arguments is not an invocation — and cost
			// the same on every flag typo, which is the common case.
			exit, stdout, stderr := runBare(t, root, tool, nil)
			if exit != 2 {
				t.Errorf("a bare `%s` exits %d, want 2 (could not run — no arguments is not an invocation)", tool, exit)
			}
			if stdout != "" {
				t.Errorf("a bare `%s` wrote to stdout: %q; a refusal belongs on stderr", tool, stdout)
			}
			// One line, or two: point 2 says a refusal must state what the input
			// WANTS, and where that guidance is a sentence of its own it follows on
			// one indented line. Two is the ceiling, and the second line has to be
			// the hint rather than more of the banner.
			lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")
			switch {
			case len(lines) > 2:
				t.Errorf("a bare `%s` printed %d lines, want 1 (or 2 with its hint); the banner is behind `%s help`, not in front of every mistake:\n%s", tool, len(lines), tool, stderr)
			case len(lines) == 2 && !strings.HasPrefix(lines[1], "  "):
				t.Errorf("a bare `%s` printed a second line that is not an indented hint:\n%s", tool, stderr)
			}
			if want := "run: " + tool + " help"; !strings.Contains(stderr, want) {
				t.Errorf("a bare `%s` names no door; it must contain %q:\n%s", tool, want, stderr)
			}

			// (a), second half: the door opens, on stdout, at exit 0, and what is
			// behind it ends in runnable lines.
			exit, banner, helpErr := runBare(t, root, tool, []string{"help"})
			if exit != 0 {
				t.Errorf("`%s help` exits %d, want 0; stderr: %s", tool, exit, helpErr)
			}
			examples, err := onboarding.ExampleLines(banner, tool)
			if err != nil {
				t.Fatalf("%v\n(docs/ONBOARDING.md point 1)\n\nwhat it printed:\n%s", err, banner)
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

// runBare builds the command and runs it with the arguments given (none, or
// `help`). It is BUILT rather than called as a package, because what this test
// is about is what a stranger meets at a shell prompt.
func runBare(t *testing.T, root, tool string, args []string) (exit int, stdout, stderr string) {
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
	cmd := exec.Command(bin, args...)
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

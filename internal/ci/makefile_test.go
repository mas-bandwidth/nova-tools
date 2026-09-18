package ci

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// makefile_test.go is the layout check behind CARD-9019: the Makefile is the one
// entry for build, test and lint, so the commands in ci.yml that build, vet,
// format or test must be make invocations rather than their own `go ...` lines.
// The workflow may still discover its shards with `go list` and set up a
// toolchain by hand — those are setup steps, not the build/test/lint commands a
// reader compares to their own `make test` — and they are allowlisted by name
// below.
//
// Both files are read as text on purpose: the repository has no YAML library in
// go.mod (standard library only), and this test must run on every platform the
// matrix covers, including Windows, where `make` is not installed. It is the
// same approach ci_budget_test.go takes for the job names and timeouts.

// buildTestLintRe matches a build, test or lint COMMAND at the start of a shell
// statement: `go build`, `go vet`, `go test`, `gofmt`, or the nova-work
// acceptance script. `go version`, `go list` and `go env` are setup and are
// deliberately not matched. The command may follow a shell separator — `;`,
// `&&`, `||`, `|` or `$(` — so a command substitution is caught too.
var buildTestLintRe = regexp.MustCompile(`(^|[;&|($])\s*(go\s+(build|vet|test)\b|gofmt\b|\./lisp/nova-work/run-tests\.sh\b)`)

// commandAllowlist names the setup and shard-loop lines that are allowed not to
// be a make invocation. They run no test and check no formatting: they discover
// the package set or the toolchain the make target then runs against, or they
// are the fleet-probe's own diagnostic build. The fleet-probe is a
// workflow_dispatch job that proves a bench is real before the loop trusts it;
// its build and one-package run are the probe, not the CL build/test entry.
var commandAllowlist = []string{
	"go list",
	"go test -list",
	"go version",
	"go env",
	"command -v go",
	"GOMAXPROCS",
	"go build ./cmd/nova-sandbox",
	"go build ./... && go test -count=1 ./internal/oneline/",
}

// requiredTargets is the one entry CARD-9019 names: build, test (fast tier),
// test-full, lint, check (what CI runs) and clean.
var requiredTargets = []string{"build", "test", "test-full", "lint", "check", "clean", "help"}

// runCommand is one shell command line inside a step's `run:` block, with the
// ci.yml line number it came from so a red names the edit to make.
type runCommand struct {
	line int
	cmd  string
}

func TestCIBuildTestLintCommandsGoThroughMake(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	cmds := runCommands(src)
	if len(cmds) == 0 {
		t.Fatal("no run commands parsed from ci.yml; the parser is looking in the wrong place")
	}
	sawMake := false
	for _, c := range cmds {
		if c.cmd == "" || strings.HasPrefix(c.cmd, "#") {
			continue
		}
		isMake := strings.HasPrefix(c.cmd, "make ") || c.cmd == "make"
		if isMake {
			sawMake = true
		}
		if !buildTestLintRe.MatchString(c.cmd) {
			continue
		}
		if allowlistedCommand(c.cmd) || isMake {
			continue
		}
		t.Errorf("ci.yml:%d: build/test/lint command is not a make invocation: %q", c.line, c.cmd)
	}
	if !sawMake {
		t.Error("ci.yml names no make invocation; the Makefile is not the one entry for the CL tier")
	}
}

// TestMakefileIsTheOneEntry pins the Makefile's own shape: the required targets
// are declared and phony, `check` is the union of the gates CI runs, and `clean`
// removes only the two explicit directories. It reads the Makefile as text so it
// runs on the Windows legs too, where make is not installed.
func TestMakefileIsTheOneEntry(t *testing.T) {
	root := repoRoot(t)
	mk := readFile(t, filepath.Join(root, "Makefile"))

	targets := makeTargets(mk)
	for _, want := range requiredTargets {
		if !targets[want] {
			t.Errorf("Makefile declares no %q target", want)
		}
	}

	phony := makePhony(mk)
	for _, want := range requiredTargets {
		if !phony[want] {
			t.Errorf("Makefile .PHONY does not name %q", want)
		}
	}

	if !strings.Contains(mk, "rm -rf ./bin ./scratch") {
		t.Error("Makefile clean does not remove exactly ./bin and ./scratch through an explicit list")
	}

	checkDeps := makeDeps(mk, "check")
	for _, want := range []string{"build", "lint", "test"} {
		if !checkDeps[want] {
			t.Errorf("Makefile check does not run %q", want)
		}
	}

	// And, where make exists, drive it over the fixture: `make help` must answer
	// and a dry-run of `check` must expand to the gates. The dry-run is the
	// closest a unit test can come to "a CI job runs make check" without
	// recursively building the tree inside the test the tree is running. make is
	// not guaranteed on the Windows legs, so a missing make is a skip, not a red.
	if _, err := exec.LookPath("make"); err != nil {
		t.Log("make is not on PATH; the text checks above still ran")
		return
	}
	help := runMake(t, root, "help")
	for _, target := range requiredTargets {
		if !strings.Contains(help, "make "+target) {
			t.Errorf("`make help` does not list target %q:\n%s", target, help)
		}
	}
	dry := runMake(t, root, "-n", "check")
	for _, gate := range []string{
		"go build ./...",
		"go vet ./...",
		"go test -count=1 ./cmd/... ./internal/...",
		"go test -count=1 -run TestFriendSequence ./cmd/...",
		"./lisp/nova-work/run-tests.sh",
	} {
		if !strings.Contains(dry, gate) {
			t.Errorf("`make -n check` does not run %q:\n%s", gate, dry)
		}
	}
	clean := runMake(t, root, "-n", "clean")
	if !strings.Contains(clean, "rm -rf ./bin ./scratch") {
		t.Errorf("`make -n clean` is not the explicit two-directory removal:\n%s", clean)
	}
}

// runMake runs make with the repository root as its working directory and
// returns stdout and stderr combined, failing the test if make cannot start.
func runMake(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("make", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// makeTargets returns every target name declared in the Makefile, keyed by name.
func makeTargets(mk string) map[string]bool {
	out := make(map[string]bool)
	for _, line := range strings.Split(mk, "\n") {
		if m := regexp.MustCompile(`^([a-zA-Z0-9_-]+):`).FindStringSubmatch(line); m != nil {
			out[m[1]] = true
		}
	}
	return out
}

// makePhony returns the target names on the .PHONY line.
func makePhony(mk string) map[string]bool {
	out := make(map[string]bool)
	for _, line := range strings.Split(mk, "\n") {
		if !strings.HasPrefix(line, ".PHONY:") {
			continue
		}
		for _, name := range strings.Fields(strings.TrimPrefix(line, ".PHONY:")) {
			out[name] = true
		}
	}
	return out
}

// makeDeps returns the prerequisite targets named on a target's `name: deps`
// line.
func makeDeps(mk, target string) map[string]bool {
	out := make(map[string]bool)
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(target) + `:\s*(.*)$`)
	for _, line := range strings.Split(mk, "\n") {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		for _, dep := range strings.Fields(m[1]) {
			out[dep] = true
		}
	}
	return out
}

// runCommands parses ci.yml's `run:` steps into their command lines. It handles
// both an inline `run: <cmd>` and a `run: |` block scalar, and it skips shell
// comment lines so a comment that mentions `go test` is not mistaken for one.
func runCommands(src string) []runCommand {
	lines := strings.Split(src, "\n")
	var out []runCommand
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		var rest string
		switch {
		case strings.HasPrefix(trimmed, "run:"):
			rest = strings.TrimSpace(strings.TrimPrefix(trimmed, "run:"))
		case strings.HasPrefix(trimmed, "- run:"):
			rest = strings.TrimSpace(strings.TrimPrefix(trimmed, "- run:"))
		default:
			continue
		}
		if rest != "" && !strings.HasPrefix(rest, "|") && !strings.HasPrefix(rest, ">") {
			out = append(out, runCommand{line: i + 1, cmd: rest})
			continue
		}
		indent := len(lines[i]) - len(strings.TrimLeft(lines[i], " "))
		for j := i + 1; j < len(lines); j++ {
			next := lines[j]
			if strings.TrimSpace(next) == "" {
				continue
			}
			nindent := len(next) - len(strings.TrimLeft(next, " "))
			if nindent <= indent {
				break
			}
			out = append(out, runCommand{line: j + 1, cmd: strings.TrimSpace(next)})
			i = j
		}
	}
	return out
}

func allowlistedCommand(cmd string) bool {
	for _, a := range commandAllowlist {
		if strings.Contains(cmd, a) {
			return true
		}
	}
	return false
}

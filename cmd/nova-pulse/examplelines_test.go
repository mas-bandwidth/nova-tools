package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// TestHelpExampleLinesRunAsPrinted: every line of this tool's `example:`
// blocks runs, as printed, from the root of a checkout. nova-tools #1455
// measured 28 of 61 pasted example lines exiting 2 because the line names an
// input the reader has not made; an example exiting 2 is a broken example
// (ONBOARDING point 1). SCOPE, said out loud: this covers nova-pulse's own
// `example:` blocks and nothing else -- not docs/CLI.md, not docs/TESTS.md,
// not another command's banner.
//
// The banner is read AS SOURCE, the `usage` constant beside this test, so the
// text under test is the text a reader pastes. Each line runs through `sh -c`
// from a checkout-shaped temp root holding cmd/nova-pulse/testdata, with the
// built binary first on PATH and standard input closed.
//
// A line this card cannot make run is named in leftOwedExampleLines with the
// refusal that earned it. It is not asserted and it is not silently dropped:
// the test states what it does not cover instead of failing on it.
func TestHelpExampleLinesRunAsPrinted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the block is pasted through `sh -c` and the fixture's stub nova-swarm is a shell script; the fleet runs no windows leg")
	}

	lines := exampleBlockLines(usage)
	if len(lines) == 0 {
		t.Fatal("the usage banner's `example:` blocks hold no line; this test would pass by running nothing")
	}

	bin := buildPulse(t)

	root := t.TempDir()
	// The checkout shape the example reads: cmd/nova-pulse/testdata, with the pool and
	// templates the block names from the checkout root. Every path written is inside this
	// t.TempDir().
	copyTree(t, "testdata", filepath.Join(root, "cmd", "nova-pulse", "testdata"))

	ran, answered := 0, 0
	for _, line := range lines {
		if why, owed := leftOwedExampleLines[line]; owed {
			// A finding, named in RESULT.md: the line is still printed as written
			// for a stranger, and this test states what it could not cover. See
			// the map.
			t.Logf("left owed %q: %s", line, why)
			continue
		}
		exit, out := runPulseLine(t, root, bin, line)
		ran++
		if exit != 0 {
			t.Errorf("the help example %q does not run as printed: exit %d\nfirst output line: %s",
				line, exit, pulseFirstLine(out))
			continue
		}
		answered++
	}
	if ran == 0 {
		t.Fatal("every line of the `example:` blocks was excused; this test proved nothing about the banner")
	}
	if answered == 0 {
		t.Error("no line of the `example:` blocks exited 0; the blocks answer nothing a reader can watch succeed")
	}
}

// leftOwedExampleLines are the lines that could not be made to run inside this
// card's two files, by name, each with the reason. They are FINDINGS, not
// changes: the line is still printed as written, and RESULT.md's `Left owed`
// says so. An entry lands here only when the card's two paths cannot give it
// what it needs -- a live queue, a key, a bench, a network, or hours of wall
// clock -- not because this test is unwilling to run it.
var leftOwedExampleLines = map[string]string{}

// exampleBlockLines returns every command under an `example:` heading in a
// usage banner, in banner order, across every block. A line beginning with the
// tool's name under the heading is an example; a blank line closes the block, so
// the prose between two blocks is not swept up. nova-pulse has eleven blocks
// and onboarding.ExampleLines reads only the first, so this test reads them all.
func exampleBlockLines(banner string) []string {
	var out []string
	inBlock := false
	for _, line := range strings.Split(banner, "\n") {
		if line == "example:" {
			inBlock = true
			continue
		}
		if !inBlock {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			inBlock = false
			continue
		}
		if strings.HasPrefix(trimmed, "nova-pulse ") {
			out = append(out, strings.Join(strings.Fields(trimmed), " "))
		}
	}
	return out
}

// buildPulse builds this command into the test's own directory, so the binary
// the block runs is the source under test and never an installed one. The
// environment is sanitized with goenv.Clean because the parent's GOFLAGS can
// reshape a go command's output under the parser that reads it (internal/goenv,
// the `goenv` class test).
func buildPulse(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "nova-pulse")
	build := exec.Command("go", "build", "-o", bin, "./cmd/nova-pulse")
	build.Dir = root
	build.Env = goenv.Clean(os.Environ())
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building nova-pulse: %v\n%s", err, out)
	}
	return bin
}

// runPulseLine runs one example line character for character through `sh -c`,
// from a directory the caller owns, with standard input closed and two
// directories first on PATH: the built binary's, under the name the line uses,
// and the fixture's own ./bin, whose stub nova-swarm is the precondition launch
// documents.
func runPulseLine(t *testing.T, dir, bin, line string) (int, string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", line)
	cmd.Dir = dir
	cmd.Env = append(goenv.Clean(os.Environ()),
		"PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+
			filepath.Join(dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Stdin = nil // a nil Stdin is the null device: the pasted line reads no standard input
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	return exitOf(t, cmd), out.String()
}

// exitOf runs a command and reduces it to its exit code, refusing to guess when
// the command never ran at all.
func exitOf(t *testing.T, cmd *exec.Cmd) int {
	t.Helper()
	var exitErr *exec.ExitError
	switch err := cmd.Run(); {
	case err == nil:
		return 0
	case errors.As(err, &exitErr):
		return exitErr.ExitCode()
	default:
		t.Fatalf("running %s: %v", cmd.Path, err)
		return 0
	}
}

// pulseFirstLine is the one line a failure report quotes: the tool's own
// refusal, or its answer, whichever came first on either stream.
func pulseFirstLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "(no output)"
	}
	return s
}

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// TestHelpExampleLinesRunAsPrinted: every line of this tool's `example:` block runs, as printed,
// from the root of a checkout, and exits 0. nova-tools #1455 measured 28 of 61 pasted example lines
// exiting 2 because the line names an input the reader has not made. An example exiting 2 is a broken
// example (ONBOARDING point 1). SCOPE, said out loud: this covers nova-tokens's own block and nothing else.
//
// The usage banner is read AS SOURCE -- the `usage` constant beside this test -- rather than by
// running `help`, because the lines under test are the ones a reader pastes; the binary is built
// only so the lines can be RUN, with that binary on PATH and stdin closed, exactly as a stranger
// would. The block reads ./repos.tsv, ./transcripts, ./bus, ./session.jsonl and ./out, so the setup
// line the block now carries is executed first, in a checkout-shaped temp root holding the fixture.
//
// No example line here pushes, publishes, contacts a forge, acts on a machine or needs a key:
// fold, check, sum, sources, report and session read files and write one day file, so no line is
// skipped by name. A skip would be a hole, and this block has none.
func TestHelpExampleLinesRunAsPrinted(t *testing.T) {
	lines := exampleBlockLines(usage)
	if len(lines) == 0 {
		t.Fatal("the usage banner's `example:` blocks hold no line; this test would pass by running nothing")
	}

	setup := fixtureSetupLine(usage)
	if setup == "" {
		t.Fatalf("the usage banner has no fixture setup line above the block, so a stranger pasting it names\n"+
			"inputs they have not made (nova-tools #1455: an example exiting 2 is a broken example).\n"+
			"The missing line is:\n  %s", wantFixtureSetup)
	}

	bin := buildExampleBinary(t)

	root := t.TempDir()
	// The one path the setup line reads, at the path it names: the checkout shape, and nothing
	// else, because the block only reaches cmd/nova-tokens/testdata/example-bench.
	copyExampleTree(t, filepath.Join("testdata", "example-bench"),
		filepath.Join(root, "cmd", "nova-tokens", "testdata", "example-bench"))

	if exit, out := runExampleLine(t, root, filepath.Dir(bin), setup); exit != 0 {
		t.Fatalf("the fixture setup line exits %d, want 0:\n  %s\nits first output line: %s",
			exit, setup, exampleFirstLine(out))
	}

	for _, line := range lines {
		exit, out := runExampleLine(t, root, filepath.Dir(bin), line)
		if exit != 0 {
			t.Errorf("the example `%s` exits %d, want 0 -- a line a stranger pastes must run as printed:\nfirst output line: %s",
				line, exit, exampleFirstLine(out))
		}
	}
}

// wantFixtureSetup is the line the class fix added above the block, named so a test that finds it
// missing says which line a reader lost.
const wantFixtureSetup = "cp -R cmd/nova-tokens/testdata/example-bench/. . && cp ./transcripts/window.jsonl ./session.jsonl && mkdir -p ./out"

// exampleBlockLines returns every command under an `example:` heading in a usage banner, in
// banner order, across every block. A line beginning with the tool's name under the heading is
// an example; a blank line closes the block, so the prose between two blocks is not swept up.
func exampleBlockLines(usage string) []string {
	var out []string
	inBlock := false
	for _, line := range strings.Split(usage, "\n") {
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
		if strings.HasPrefix(trimmed, "nova-tokens ") {
			out = append(out, trimmed)
		}
	}
	return out
}

// fixtureSetupLine returns the fixture setup line the block reads, or "" when the banner loses
// one. It matches the shape the fix gives the line rather than its exact prose, so the printed
// line is the source of truth and this test runs what the banner carries.
func fixtureSetupLine(usage string) string {
	for _, line := range strings.Split(usage, "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "cp -R cmd/nova-tokens/testdata/example-bench") {
			return trimmed
		}
	}
	return ""
}

// buildExampleBinary builds this command into a temp dir and returns its path. It builds rather
// than calls the package because the question is what a stranger meets at a shell prompt; the
// environment is sanitized with goenv.Clean because the parent's GOFLAGS can reshape a go
// command's output under the parser that reads it (internal/goenv, the `goenv` class test).
func buildExampleBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "nova-tokens")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Env = goenv.Clean(os.Environ())
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building nova-tokens: %v\n%s", err, out)
	}
	return bin
}

// runExampleLine runs one line through `sh -c` with the built binary first on PATH, from dir,
// with stdin closed. It returns the exit code and the combined output.
func runExampleLine(t *testing.T, dir, binDir, line string) (int, string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", line)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Stdin = nil
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	var exitErr *exec.ExitError
	switch err := cmd.Run(); {
	case err == nil:
		return 0, out.String()
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), out.String()
	default:
		t.Fatalf("running %q: %v", line, err)
		return 0, ""
	}
}

// copyExampleTree copies src under dst, making directories as it goes. Every path it writes is
// inside the caller's t.TempDir() (AGENTS.md rule 10).
func copyExampleTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	})
	if err != nil {
		t.Fatalf("copying the fixture %s: %v", src, err)
	}
}

// exampleFirstLine is the first line of an output, which is where a refusal says what was wrong.
func exampleFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

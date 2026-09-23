package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// TestHelpExampleLinesRunAsPrinted: every line of this tool's `example:` block runs, as printed,
// from the root of a checkout, and exits 0. nova-tools #1455 measured 28 of 61 pasted example lines
// exiting 2 because the line names an input the reader has not made. An example exiting 2 is a broken
// example (ONBOARDING point 1). SCOPE, said out loud: this covers nova-update's own block and nothing
// else.
//
// THE BINARY, NOT AN IN-PROCESS CALL. The lines are run through `sh -c` with the built binary on PATH
// under the name the line uses, from a t.TempDir() copy of the checkout -- the way a stranger pastes
// them. An in-process update.Main call would prove the arguments parse; it would not prove the command
// a reader types exists, or that the fixture path in the banner resolves from a checkout root.
func TestHelpExampleLinesRunAsPrinted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the block is pasted through `sh -c` and the fleet runs no windows leg for this binary")
	}

	bin := buildUpdate(t)

	exit, banner, stderr := runBinary(t, bin, "help")
	if exit != 0 {
		t.Fatalf("`nova-update help` exits %d, want 0; stderr: %s", exit, stderr)
	}
	lines, err := onboarding.ExampleLines(banner, "nova-update")
	if err != nil {
		t.Fatalf("%v\n\nwhat the banner printed:\n%s", err, banner)
	}
	if len(lines) == 0 {
		t.Fatal("the `example:` block holds no nova-update command; this test would pass by running nothing")
	}

	// work is the root of a checkout as a stranger meets it: a copy of this tree, so the fixture the
	// block names (`cmd/nova-update/testdata/example.tsv`) resolves at the path it is written with,
	// and nothing the block runs can write into the checkout under test.
	work := checkoutCopy(t)

	ran := 0
	for _, line := range lines {
		if why, skip := skippedExampleLines[line]; skip {
			t.Logf("skip %q: %s", line, why)
			continue
		}
		code, out := runPastedLine(t, bin, work, line)
		ran++
		if code != 0 {
			t.Errorf("the help example %q does not run: exit %d\nfirst output line: %s", line, code, firstLine(out))
		}
	}
	if ran == 0 {
		t.Fatal("every line of the `example:` block was skipped; this bench proved nothing about the block")
	}
}

// skippedExampleLines are the lines this test does NOT run, by name, each with the reason. They are
// skipped because the line pushes, publishes, contacts a forge, acts on a machine or needs a key --
// not because the test is unwilling to run it. nova-update's block carries no such line today, so the
// list is empty and every line is run; an entry added here must carry its `// why`.
var skippedExampleLines = map[string]string{}

// buildUpdate builds this command into the test's own directory, so the binary the block runs is the
// source under test and never an installed one.
func buildUpdate(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "nova-update")
	build := exec.Command("go", "build", "-o", bin, "./cmd/nova-update")
	build.Dir = root
	build.Env = goenv.Clean(os.Environ())
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building nova-update: %v\n%s", err, out)
	}
	return bin
}

// checkoutCopy copies this checkout into a t.TempDir() and returns the new root. The `.git`
// directory is left behind: nothing the example block reads lives there, and a test that copies it
// pays for the whole history.
func checkoutCopy(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := copyTree(root, dst); err != nil {
		t.Fatalf("copying the checkout: %v", err)
	}
	return dst
}

// copyTree copies src into dst, skipping `.git`. It is the whole checkout a reader would paste from
// rather than the one file the block happens to name, so a new fixture path the block grows is
// copied too and not silently missing.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return fs.SkipDir
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			// A symlink or device is not a checkout input these examples read; skip it rather
			// than follow it out of the tree.
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, info.Mode().Perm())
	})
}

// runBinary runs this command with a directory of its own and the two streams kept apart. It is how
// the banner is read.
func runBinary(t *testing.T, bin string, args ...string) (exit int, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = t.TempDir()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	return exitOf(t, cmd), out.String(), errb.String()
}

// runPastedLine runs one example line character for character through `sh -c`, from a directory the
// caller owns, with standard input closed, and the binary on PATH under the name the line uses.
func runPastedLine(t *testing.T, bin, dir, line string) (exit int, output string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", line)
	cmd.Dir = dir
	cmd.Env = append(goenv.Clean(os.Environ()),
		"PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Stdin = nil // a nil Stdin is the null device: the pasted line reads no standard input
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	return exitOf(t, cmd), out.String()
}

// exitOf runs a command and reduces it to its exit code, refusing to guess when the command never
// ran at all.
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

// firstLine is the one line a failure report quotes: the tool's own refusal, or its answer, whichever
// came first on either stream.
func firstLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "(no output)"
	}
	return s
}

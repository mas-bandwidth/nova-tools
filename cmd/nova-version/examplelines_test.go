//go:build unix

package main

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/update"
)

// TestHelpExampleLinesRunAsPrinted: every line of this tool's `example:` block runs, as printed,
// from the root of a checkout, and exits 0. nova-tools #1455 measured 28 of 61 pasted example lines
// exiting 2 because the line names an input the reader has not made. An example exiting 2 is a broken
// example (ONBOARDING point 1). SCOPE, said out loud: this covers nova-version's own `example:` block
// and the `## nova-version` section of docs/CLI.md -- the two sources this sweep changes -- and
// nothing else. The block is read from the bytes the tool prints and from main.go's documented copy,
// so neither can drift; the document's fenced blocks are read from docs/CLI.md and run in order, so a
// line that stops running as printed goes red here naming that line.
func TestHelpExampleLinesRunAsPrinted(t *testing.T) {
	root := filepath.Join("..", "..")
	binDir := buildBinary(t, root, "nova-version")

	var banner bytes.Buffer
	update.Main("nova-version", []string{"help"}, "", &banner, &banner)
	printed, err := onboarding.ExampleLines(banner.String(), "nova-version")
	if err != nil {
		t.Fatalf("nova-version help: %v\n%s", err, banner.String())
	}
	if documented := mainDocumentedExamples(t); !reflect.DeepEqual(documented, printed) {
		t.Errorf("main.go documents the `example:` block as %q, but `nova-version help` prints %q; a documented block that drifts from the printed one is how a stranger's paste breaks", documented, printed)
	}
	work := checkout(t, root)
	for _, line := range printed {
		runExample(t, work, binDir, line)
	}

	doc, err := os.ReadFile(filepath.Join(root, "docs", "CLI.md"))
	if err != nil {
		t.Fatalf("docs/CLI.md: %v", err)
	}
	for _, heading := range []string{"First run", "Capture and compare installed binaries"} {
		block, err := onboarding.Transcript(string(doc), "nova-version", heading)
		if err != nil {
			t.Fatalf("docs/CLI.md `### %s`: %v", heading, err)
		}
		work := checkout(t, root)
		for _, line := range block {
			if strings.TrimSpace(line) == "" {
				continue
			}
			runExample(t, work, binDir, line)
		}
	}
}

// mainDocumentedExamples reads the `example:` block main.go documents in its package
// comment and returns the command lines in it, whitespace collapsed the way
// onboarding.ExampleLines collapses the printed block.
func mainDocumentedExamples(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("main.go: %v", err)
	}
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "//"))
		if strings.HasPrefix(line, "nova-version ") {
			out = append(out, strings.Join(strings.Fields(line), " "))
		}
	}
	return out
}

// buildBinary builds one command of this checkout and returns the directory holding it,
// which the caller puts on PATH so the example lines run the binary under test.
func buildBinary(t *testing.T, root, tool string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, tool)
	build := exec.Command("go", "build", "-o", bin, "./cmd/"+tool)
	build.Env = goenv.Clean(os.Environ())
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building %s: %v\n%s", tool, err, out)
	}
	return dir
}

// checkout copies the working tree into a fresh t.TempDir, skipping .git -- the examples
// write ./bin, ./before.tsv and ./after.tsv, and rule 10 says a test writes only under its
// own temp directory.
func checkout(t *testing.T, root string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == ".git" || rel == ".nova-sandbox-tmp" {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copying the checkout: %v", err)
	}
	return dst
}

// runExample runs one example line exactly as printed, through a shell, with the built
// binary on PATH ahead of anything installed, from the root of the copied checkout. It
// names the line and the line's first output on failure.
func runExample(t *testing.T, work, binDir, line string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", line)
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		first := out.String()
		if i := strings.IndexByte(first, '\n'); i >= 0 {
			first = first[:i]
		}
		t.Errorf("example line %q exited non-zero: %v; first output line: %s", line, err, first)
	}
}

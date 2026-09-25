//go:build unix

package main

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

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

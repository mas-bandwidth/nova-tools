package ci

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// TestEveryToolPrintsTheOneVersionLine is the class rule behind #1297 and
// #1264, kept where the next binary will trip over it.
//
// The hurt: `nova-version snapshot --bin ~/.local/bin` refused an entire
// install, exit 2, because nova-merge printed five tokens where the reader
// wanted four -- and nova-sandbox, in the same directory, printed a line of a
// different shape altogether (`SANDBOX VERSION tool=... version=...`). Two
// shapes meant every reader of a version line carried its own tolerant parser,
// and a tool that said one more true thing about itself broke them one at a
// time.
//
// The class fix is that there is ONE grammar -- four tokens and then
// `key=value` extras -- that internal/buildinfo both writes and reads, and this
// test runs the REAL binary of every cmd/nova-* through the REAL reader. It
// walks cmd/ rather than holding a list, so a tool added tomorrow is held to
// the grammar on the day it appears. No network: every binary is built from
// this checkout and run with one argument.
func TestEveryToolPrintsTheOneVersionLine(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	tools := novaCommands(t, root)
	if len(tools) == 0 {
		t.Fatal("no nova-* command directories found under cmd/; this test was looking in the wrong place and would have passed by checking nothing")
	}
	bin := buildAllTools(t, root)

	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
			// Both spellings, because the Conventions promise both and a tool
			// that answers only one is a tool a reader has to try twice.
			for _, verb := range []string{"version", "--version"} {
				exit, stdout, stderr := runBare(t, root, tool, filepath.Join(bin, exeName(tool)), []string{verb})
				if exit != 0 {
					t.Errorf("`%s %s` exits %d, want 0; stderr: %s", tool, verb, exit, stderr)
					continue
				}
				lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
				if len(lines) != 1 {
					t.Errorf("`%s %s` printed %d lines, want exactly 1:\n%s", tool, verb, len(lines), stdout)
					continue
				}
				f, ok := buildinfo.Parse(stdout)
				if !ok {
					t.Errorf("`%s %s` printed a line internal/buildinfo.Parse refuses; the grammar is `<tool> <identity> <goos>/<goarch> <go version>` and then any number of key=value extras:\n%s", tool, verb, stdout)
					continue
				}
				if f.Tool != tool {
					t.Errorf("`%s %s` names itself %q in field one; a reader holding two pastes reads the tool out of field one", tool, verb, f.Tool)
				}
				if f.Version == "" {
					t.Errorf("`%s %s` carries an empty identity in field two", tool, verb)
				}
				if want := runtime.GOOS + "/" + runtime.GOARCH; f.Platform != want {
					t.Errorf("`%s %s` reports platform %q, want %q", tool, verb, f.Platform, want)
				}
				if !strings.HasPrefix(f.GoVersion, "go") {
					t.Errorf("`%s %s` reports go version %q, want a go1.x", tool, verb, f.GoVersion)
				}
			}
		})
	}
}

// novaCommands lists cmd/nova-* by walking the directory, never a list.
func novaCommands(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	if err != nil {
		t.Fatalf("reading cmd/: %v", err)
	}
	var tools []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "nova-") {
			tools = append(tools, e.Name())
		}
	}
	return tools
}

// buildAllTools builds the whole set in ONE `go build ./cmd/...`, because
// twenty-one separate builds is twenty-one link steps for the same evidence and
// this package is held to the fast suite.
func buildAllTools(t *testing.T, root string) string {
	t.Helper()
	dir := t.TempDir()
	build := exec.Command("go", "build", "-o", dir+string(os.PathSeparator), "./cmd/...")
	build.Env = goenv.Clean(os.Environ())
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building ./cmd/...: %v\n%s", err, out)
	}
	return dir
}

func exeName(tool string) string {
	if runtime.GOOS == "windows" {
		return tool + ".exe"
	}
	return tool
}

// TestTheVersionGrammarIsSpelledOutOnceInTheSpec: the grammar a reader enforces
// and the grammar the spec states are one sentence, and the spec is where a
// friend adding a binary looks first.
func TestTheVersionGrammarIsSpelledOutOnceInTheSpec(t *testing.T) {
	spec := readFile(t, filepath.Join(repoRoot(t), "docs", "SPEC.md"))
	for _, want := range []string{
		"<tool> <build identity> <goos>/<goarch> <go version>",
		"key=value",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("docs/SPEC.md no longer states %q; the grammar every binary obeys is stated there once", want)
		}
	}
	for _, gone := range []string{
		"answers with its `SANDBOX VERSION` line",
	} {
		if strings.Contains(spec, gone) {
			t.Errorf("docs/SPEC.md still carves out a second version-line shape (%q); #1297 was that carve-out reaching every reader", gone)
		}
	}
}

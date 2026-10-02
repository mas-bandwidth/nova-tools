// THE SCAFFOLDING VERBS, proven the way a contributor meets them (nova-tools#2498 S5 part 2).
//
// `nova-ci new-rule <name>` lays down a new class rule's skeleton:
// - internal/ci/<name>_class_test.go
// - internal/ci/testdata/<name>/fixture.txt
// - make/rule_<name>.mk
//
// `nova-ci new-verb <tool> <verb>` lays down a new CLI verb's skeleton:
// - cmd/<tool>/<verb>.go
// - cmd/<tool>/<verb>_test.go
// - cmd/<tool>/testdata/<verb>/fixture.txt
// - make/verb_<tool>_<verb>.mk
//
// Both verbs ensure the new skeleton immediately builds, tests, and conforms to
// write confinement. new-verb refuses a tool with no func main and prints the
// exact dispatch case to add; it never edits the switch itself.
//
// Each test copies into its scratch tree only the module's packages that the
// patterns it builds depend on (go list -deps -test), never all of cmd/,
// internal/, tools/ and profiles/ (rowan hold 6 on #3616: 0.15 s -> 54 s), and
// the two tests run in parallel.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// In process, on a temp tree: --dry-run lists exactly the files the real run
// writes and writes none; a second run, a bad name, no checkout and a tool
// with no func main are each one refusal at exit 2 that names the verb's help.
func TestScaffoldVerbsDryRunAndRefuse(t *testing.T) {
	t.Parallel()
	tree := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tree, "go.mod"), []byte("module example.com/m\n"), 0o644))

	code, dry, stderr := runCI(t, []string{"new-rule", "--root", tree, "--dry-run", "demo"}, "")
	require.Equal(t, 0, code, stderr)
	entries, err := os.ReadDir(tree)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "--dry-run wrote into the tree")
	assert.Contains(t, dry, "nova-ci new-rule NOTE --dry-run wrote nothing")

	code, wrote, stderr := runCI(t, []string{"new-rule", "--root", tree, "demo"}, "")
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, strings.Count(dry, "would write "), strings.Count(wrote, "wrote "), "the dry run and the run name different files:\n%s\n%s", dry, wrote)
	assert.Equal(t, strings.ReplaceAll(strings.Split(dry, "nova-ci new-rule NOTE")[0], "would write ", "wrote "), wrote)

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"new-rule", "--root", tree, "demo"}, "already exists"},
		{[]string{"new-rule", "--root", tree, "--dry-run", "demo"}, "already exists"},
		{[]string{"new-rule", "--root", tree, "Bad Name"}, "rule name"},
		{[]string{"new-rule", "--root", t.TempDir(), "demo"}, "no go.mod"},
		{[]string{"new-verb", "--root", tree, "ghost", "probe"}, "no func main"},
		{[]string{"new-verb", "--root", tree, "ghost"}, "new-verb wants two arguments"},
	} {
		code, stdout, stderr := runCI(t, c.args, "")
		assert.Equal(t, 2, code, "%v", c.args)
		assert.Empty(t, stdout, "%v", c.args)
		assert.Contains(t, stderr, c.want, "%v", c.args)
		assert.Regexp(t, `^nova-ci new-(rule|verb) REFUSED: .*; run: nova-ci new-(rule|verb) -h\n$`, stderr, "%v", c.args)
	}
}

// scaffoldTree builds the CLI and lays out a scratch module holding go.mod,
// go.sum, the Makefile, and only the module packages that pattern depends on,
// tests included: each such package's top-level files plus its embed files.
func scaffoldTree(t *testing.T, pattern string) (root, bin, tree string) {
	t.Helper()
	for _, tool := range []string{"make", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s on PATH", tool)
		}
	}
	root, err := filepath.Abs("../..")
	require.NoError(t, err)
	bin = buildCLI(t)
	tree = t.TempDir()

	for _, f := range []string{"go.mod", "go.sum", "Makefile"} {
		src := filepath.Join(root, f)
		if _, err := os.Stat(src); err == nil {
			copyScaffoldFile(t, src, filepath.Join(tree, f))
		}
	}

	list := exec.Command("go", "list", "-deps", "-test", "-f",
		`{{.Dir}}{{range .EmbedFiles}}|{{.}}{{end}}{{range .TestEmbedFiles}}|{{.}}{{end}}`, pattern)
	list.Dir = root
	list.Env = goenv.Clean(os.Environ())
	out, err := list.Output()
	require.NoError(t, err, "go list -deps -test %s: %v", pattern, err)
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, "|")
		dir := parts[0]
		rel, err := filepath.Rel(root, dir)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") || seen[line] {
			continue // the standard library, the module cache, or seen already
		}
		seen[line] = true
		ents, err := os.ReadDir(dir)
		require.NoError(t, err)
		for _, e := range ents {
			if e.Type().IsRegular() {
				copyScaffoldFile(t, filepath.Join(dir, e.Name()), filepath.Join(tree, rel, e.Name()))
			}
		}
		for _, emb := range parts[1:] {
			copyScaffoldFile(t, filepath.Join(dir, emb), filepath.Join(tree, rel, emb))
		}
	}
	return root, bin, tree
}

func buildCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "nova-ci")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Env = goenv.Clean(os.Environ())
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "go build nova-ci: %v\n%s", err, out)
	return bin
}

func runScaffoldCmd(t *testing.T, bin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = goenv.Clean(os.Environ())
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s %s: %v\n%s", bin, strings.Join(args, " "), err, out)
	return string(out)
}

func runIn(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(goenv.Clean(os.Environ()), "GOWORK=off", "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s %s in %s: %v\n%s", name, strings.Join(args, " "), dir, err, out)
	return string(out)
}

func copyScaffoldFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	require.NoError(t, err)
	info, err := os.Stat(from)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(to), 0o755))
	require.NoError(t, os.WriteFile(to, data, info.Mode().Perm()))
}

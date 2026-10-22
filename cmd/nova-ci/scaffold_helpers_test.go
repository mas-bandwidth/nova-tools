//go:build slow || functional

// The scaffolding helpers: every caller (slow_test.go's new-rule skeleton and
// scaffold_functional_test.go) runs the go command over a scratch tree and sits behind the
// `slow` or `functional` tag, so these sit behind the same tags; untagged they are dead code
// to the unit tier and staticcheck's U1000.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/goenv"
	"github.com/stretchr/testify/require"
)

// scaffoldTree builds the CLI and lays out a scratch module holding go.mod,
// go.sum, the Makefile, and only the module packages that pattern depends on.
// testSupport includes existing tests and their dependencies when a generated
// skeleton needs a helper from the target package's tests.
func scaffoldTree(t *testing.T, pattern string, testSupport bool) (root, bin, tree string) {
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

	args := []string{"list", "-deps"}
	embeds := `{{.Dir}}{{range .EmbedFiles}}|{{.}}{{end}}`
	if testSupport {
		args = append(args, "-test")
		embeds += `{{range .TestEmbedFiles}}|{{.}}{{end}}`
	}
	args = append(args, "-f", embeds, pattern)
	list := exec.Command("go", args...)
	list.Dir = root
	list.Env = goenv.Clean(os.Environ())
	out, err := list.Output()
	require.NoError(t, err, "go %s: %v", strings.Join(args, " "), err)
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
			if e.Type().IsRegular() && (testSupport || !strings.HasSuffix(e.Name(), "_test.go")) {
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

package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryNestedModuleIsTested holds the rule that a nested Go module (a
// directory with its own go.mod beside the root) is tested and vetted by the
// same Makefile targets that test and vet the root. `go list ./...`,
// `go test ./...` and `go vet ./...` run at the root do NOT reach such a
// directory, with or without go.work (measured with go 1.26.6): the packages
// vanish from the package set in silence and no leg goes red. So the Makefile
// names every nested module in MODULES, and CL_PKGS carries their packages.
//
// The truth of which modules exist is go.work's `use` list: a nested module is
// a workspace member or it is not built or tested here. This test reads go.work
// and fails when a used module is missing from MODULES, so a module added in a
// later card cannot quietly drop out of the test tier.
func TestEveryNestedModuleIsTested(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	mk := parseMakefile(t, filepath.Join(root, "Makefile"))
	modules := strings.Fields(mk.vars["MODULES"])
	require.NotEmpty(t, modules, "Makefile MODULES is empty; a nested module (hook/, pkg/oneline/) would be tested by no make target")

	used := usedWorkModules(t, filepath.Join(root, "go.work"))
	require.NotEmpty(t, used, "go.work names no used module; the workspace is the truth this test reads")

	named := map[string]bool{}
	for _, m := range modules {
		named[moduleDir(m)] = true
	}
	for _, u := range used {
		if moduleDir(u) == "." {
			continue
		}
		assert.Truef(t, named[moduleDir(u)], "go.work uses module %q but Makefile MODULES (%v) does not name it; `go list ./...` at the root never reaches it, so make test, make vet, make test-full and make test-race would silently stop covering it", u, modules)
	}

	// CL_PKGS is the package set make test runs; it must carry MODULES, or the
	// nested packages are missing from the unit tier even though MODULES names
	// the directories.
	mkText := readFile(t, filepath.Join(root, "Makefile"))
	clLine := ""
	for _, line := range strings.Split(mkText, "\n") {
		if strings.HasPrefix(line, "CL_PKGS = ") {
			clLine = line
			break
		}
	}
	require.NotEmpty(t, clLine, "Makefile has no CL_PKGS assignment; make test would run no packages")
	assert.Contains(t, clLine, "MODULES", "CL_PKGS (%q) does not read MODULES, so the nested modules' packages never reach make test", clLine)
}

// usedWorkModules reads go.work and returns every directory its `use` list
// names, in order and unchanged: the `use (...)` block form and the single-line
// `use <dir>` form, comments and blank lines skipped.
func usedWorkModules(t *testing.T, path string) []string {
	t.Helper()
	var out []string
	inBlock := false
	for _, line := range strings.Split(readFile(t, path), "\n") {
		line, _, _ = strings.Cut(line, "//")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch {
		case inBlock:
			if line == ")" {
				inBlock = false
				continue
			}
			out = append(out, strings.Fields(line)...)
		case line == "use (":
			inBlock = true
		case strings.HasPrefix(line, "use "):
			out = append(out, strings.Fields(strings.TrimPrefix(line, "use "))...)
		}
	}
	return out
}

// moduleDir normalizes a go.work `use` entry or a Makefile MODULES entry to a
// directory relative to the root: the root itself stays ".", a relative path
// keeps its "./" prefix.
func moduleDir(m string) string {
	m = strings.TrimSpace(m)
	if m == "." || m == "./" {
		return "."
	}
	return "./" + strings.TrimPrefix(m, "./")
}

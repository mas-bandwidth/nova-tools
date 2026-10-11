package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/pkgselect"
)

// ci_selection_test.go pins the SELECTION side of the class-test contract. The
// budget test (ci_budget_test.go) says what the checks must be; this one says
// which packages must run them. pkg/pkgselect's Select chooses the packages
// a change tests: it builds the `want` set the self-hosted shards use (`ci
// test-matrix` and `nova-ci local` both call it).
//
// internal/ci holds class tests that read the workflow and source files as text
// and scan the tree rather than import what they guard. A cmd/nova-swarm edit
// (PR #1073) therefore turned an internal/ci class test red but named no
// dependent in the import graph, so no shard was selected to run it and the
// branch sat for two hours. The selection must name ./internal/ci on every
// run; these tests run the selection over a fixture and read its source.

var (
	// selectAppendRe is the statement (one tab of indentation: the body of
	// selectChange, outside any `if`) that adds ./internal/ci to the `want` set.
	// The bug it guards is that the line lives inside an `if` guarded on a
	// .github/ diff, so the regex is red until it is at the top level.
	selectAppendRe = regexp.MustCompile(`(?m)^\twant\["\./internal/ci"\] = true\s*$`)
)

var (
	// selectDocsAppendRe is the statement (one tab of indentation, outside any
	// `if`) that adds ./internal/docs to the `want` set, beside the
	// ./internal/ci line. internal/docs scans the tree instead of importing what
	// it guards, so a docs-only edit can break its class test without naming a
	// single dependent in the import graph.
	selectDocsAppendRe = regexp.MustCompile(`(?m)^\twant\["\./internal/docs"\] = true\s*$`)
)

// classTestSelection runs pkgselect.Select over a docs-only change in a fixture
// tree and returns what it selected: no Go package moved, so the answer is the
// packages selected on every run.
func classTestSelection(t *testing.T) []string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0o644))
	run := func(dir string, env []string, argv ...string) (pkgselect.Result, error) {
		switch strings.Join(argv, " ") {
		case "git diff --name-only base HEAD":
			return pkgselect.Result{Stdout: "docs/CLI.md\n.github/workflows/ci.yml\n"}, nil
		case "go list ./cmd/... ./internal/... ./pkg/... ./tools/...":
			return pkgselect.Result{Stdout: "example.com/m/cmd/a\nexample.com/m/internal/ci\nexample.com/m/internal/docs\n"}, nil
		case "go list -f {{.ImportPath}}{{range .Deps}} {{.}}{{end}} ./cmd/... ./internal/... ./pkg/... ./tools/...":
			return pkgselect.Result{Stdout: "example.com/m/cmd/a fmt\nexample.com/m/internal/ci fmt\nexample.com/m/internal/docs fmt\n"}, nil
		}
		return pkgselect.Result{}, nil // git fetch
	}
	out, err := pkgselect.Select(run, pkgselect.Options{Root: root, Base: "base"})
	require.NoError(t, err)
	return out.Packages
}

// TestSelectPackagesAlwaysAddsInternalCI pins the selection: ./internal/ci is
// added to `want` on every selection, not only when the diff touches .github/.
func TestSelectPackagesAlwaysAddsInternalCI(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, "pkg", "pkgselect", "select.go"))
	assert.Regexp(t, selectAppendRe, src, "pkgselect.Select does not add ./internal/ci to want unconditionally; internal/ci scans the tree instead of importing what it guards, so an edit elsewhere selects no shard to run its class tests")
	assert.Contains(t, classTestSelection(t), "./internal/ci", "a docs-only change must select ./internal/ci")
}

// TestSelectPackagesAlwaysAddsInternalDocs pins the selection: ./internal/docs
// is added to `want` on every selection, not only when the diff touches
// .github/.
func TestSelectPackagesAlwaysAddsInternalDocs(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, "pkg", "pkgselect", "select.go"))
	assert.Regexp(t, selectDocsAppendRe, src, "pkgselect.Select does not add ./internal/docs to want unconditionally; internal/docs scans the tree instead of importing what it guards, so a docs-only change that breaks TestAgentsPageNamesEveryClassRule (#1504) selects no shard to run it, and the red surfaces in an integration batch instead of on the PR (#1364 toolchainroots, #1409 hostseam)")
	assert.Contains(t, classTestSelection(t), "./internal/docs", "a docs-only change must select ./internal/docs")
}

// TestSelectPackagesAddsThePackagesWhoseTestsReadAChangedFile pins the
// selection against nova-tools#5111: docsd-16 changed a heading in
// docs/SPEC-SWARM.md, pkg/swarm's test asserts that heading, and the run
// tested only the touched packages and their importers, so a red test landed
// green. A changed file that is not Go selects every package whose _test.go
// files or testdata name it, though no import edge says so. The fixture is the
// shape of the miss: the test names the doc through filepath.Join, so the
// path as a string is not in its text; and the reversed witness, a doc no test
// names, selects nothing beyond the two class-test packages.
func TestSelectPackagesAddsThePackagesWhoseTestsReadAChangedFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":                  "module example.com/m\n",
		"pkg/swarm/swarm_test.go": "package swarm\n\nvar doc = filepath.Join(\"..\", \"..\", \"docs\", \"SPEC-SWARM.md\")\n",
	} {
		p := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	selectFor := func(diff string) []string {
		run := func(dir string, env []string, argv ...string) (pkgselect.Result, error) {
			switch strings.Join(argv, " ") {
			case "git diff --name-only base HEAD":
				return pkgselect.Result{Stdout: diff}, nil
			case "go list ./cmd/... ./internal/... ./pkg/... ./tools/...":
				return pkgselect.Result{Stdout: "example.com/m/internal/ci\nexample.com/m/internal/docs\nexample.com/m/pkg/swarm\n"}, nil
			case "go list -f {{.ImportPath}}{{range .Deps}} {{.}}{{end}} ./cmd/... ./internal/... ./pkg/... ./tools/...":
				return pkgselect.Result{Stdout: "example.com/m/internal/ci fmt\nexample.com/m/internal/docs fmt\nexample.com/m/pkg/swarm fmt\n"}, nil
			}
			return pkgselect.Result{}, nil // git fetch
		}
		out, err := pkgselect.Select(run, pkgselect.Options{Root: root, Base: "base"})
		require.NoError(t, err)
		return out.Packages
	}
	assert.Equal(t, []string{"./internal/ci", "./internal/docs", "./pkg/swarm"}, selectFor("docs/SPEC-SWARM.md\n"),
		"a docs-only change to SPEC-SWARM.md selected no package whose test reads it; pkgselect.Select must map a changed non-Go file to the packages whose _test.go files or testdata name it (nova-tools#5111)")
	assert.Equal(t, []string{"./internal/ci", "./internal/docs"}, selectFor("docs/OTHER.md\n"),
		"a doc no test names selected a package beyond the two class-test packages")
}

// stepBody returns the source text of one step, from its `- name:` key to the
// next step key at the same indentation, so a test can assert about the
// merge-gate selection and not the whole job.
func stepBody(src, name string) string {
	lines := strings.Split(src, "\n")
	start := -1
	indent := ""
	for i, line := range lines {
		if start < 0 {
			j := strings.Index(line, "- name: "+name)
			if j >= 0 {
				start = i
				indent = line[:j]
			}
			continue
		}
		trimmed := strings.TrimLeft(line, " ")
		if strings.HasPrefix(trimmed, "- name:") && len(line)-len(trimmed) == len(indent) {
			return strings.Join(lines[start:i], "\n")
		}
	}
	if start >= 0 {
		return strings.Join(lines[start:], "\n")
	}
	return ""
}

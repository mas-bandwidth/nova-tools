package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/pkgselect"
)

// ci_selection_test.go pins the SELECTION side of the class-test contract. The
// budget test (ci_budget_test.go) says what the checks must be; this one says
// which packages must run them. Two independent places choose the packages a
// change tests:
//
//   - internal/pkgselect's Select builds the `want` set the self-hosted shards
//     use (`ci test-matrix` and `nova-ci local` both call it); and
//   - ci.yml's test-hosted-merge job has its own inline selection and does not
//     call the script.
//
// internal/ci holds class tests that read the workflow and source files as text
// and scan the tree rather than import what they guard. A cmd/nova-swarm edit
// (PR #1073) therefore turned an internal/ci class test red but named no
// dependent in the import graph, so no shard was selected to run it and the
// branch sat for two hours. Both selection points must name ./internal/ci on
// every run; these tests run the selection over a fixture and read its source.

var (
	// selectAppendRe is the statement (one tab of indentation: the body of
	// selectChange, outside any `if`) that adds ./internal/ci to the `want` set.
	// The bug it guards is that the line lives inside an `if` guarded on a
	// .github/ diff, so the regex is red until it is at the top level.
	selectAppendRe = regexp.MustCompile(`(?m)^\twant\["\./internal/ci"\] = true\s*$`)

	// mergeAppendRe is the append to the merge gate's `$pkgs`, outside any `||`
	// fallback, so every group runs internal/ci and not only one that changed no
	// Go package. A trailing `;;` is allowed because the append sits in a `case`
	// arm since 2026-09-18: when the diff had ALREADY selected internal/ci, a
	// bare append ran it twice in every shard of every leg (ten runs of it across
	// the windows legs of run 35354900090 alone). The rule this pins is
	// "unconditionally in scope", and a guard that only prevents a DUPLICATE
	// keeps it.
	mergeAppendRe = regexp.MustCompile(`(?m)^\s*(\*\)\s*)?pkgs="\$pkgs \./internal/ci"\s*(;;)?\s*$`)

	// mergeFallback is the shape this card removes: internal/ci selected only
	// when $pkgs is empty.
	mergeFallback = `|| pkgs="./internal/ci"`
)

var (
	// selectDocsAppendRe is the statement (one tab of indentation, outside any
	// `if`) that adds ./internal/docs to the `want` set, beside the
	// ./internal/ci line. internal/docs scans the tree instead of importing what
	// it guards, so a docs-only edit can break its class test without naming a
	// single dependent in the import graph.
	selectDocsAppendRe = regexp.MustCompile(`(?m)^\twant\["\./internal/docs"\] = true\s*$`)

	// mergeDocsAppendRe is the append to the merge gate's `$pkgs`, outside any
	// `||` fallback, so every group runs internal/docs and not only one that
	// changed no Go package. A trailing `;;` is allowed for the same reason as
	// the internal/ci append: the append sits in a `case` arm, and its guard
	// only prevents a DUPLICATE. The rule this pins is "unconditionally in
	// scope".
	mergeDocsAppendRe = regexp.MustCompile(`(?m)^\s*(\*\)\s*)?pkgs="\$pkgs \./internal/docs"\s*(;;)?\s*$`)
)

// classTestSelection runs pkgselect.Select over a docs-only change in a fixture
// tree and returns what it selected: no Go package moved, so the answer is the
// packages selected on every run.
func classTestSelection(t *testing.T) []string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(dir string, env []string, argv ...string) (pkgselect.Result, error) {
		switch strings.Join(argv, " ") {
		case "git diff --name-only base HEAD":
			return pkgselect.Result{Stdout: "docs/CLI.md\n.github/workflows/ci.yml\n"}, nil
		case "go list ./cmd/... ./internal/... ./tools/...":
			return pkgselect.Result{Stdout: "example.com/m/cmd/a\nexample.com/m/internal/ci\nexample.com/m/internal/docs\n"}, nil
		case "go list -f {{.ImportPath}}{{range .Deps}} {{.}}{{end}} ./cmd/... ./internal/... ./tools/...":
			return pkgselect.Result{Stdout: "example.com/m/cmd/a fmt\nexample.com/m/internal/ci fmt\nexample.com/m/internal/docs fmt\n"}, nil
		}
		return pkgselect.Result{}, nil // git fetch
	}
	out, err := pkgselect.Select(run, pkgselect.Options{Root: root, Base: "base"})
	if err != nil {
		t.Fatal(err)
	}
	return out.Packages
}

// TestSelectPackagesAlwaysAddsInternalCI pins the selection: ./internal/ci is
// added to `want` on every selection, not only when the diff touches .github/.
func TestSelectPackagesAlwaysAddsInternalCI(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, "internal", "pkgselect", "select.go"))
	if !selectAppendRe.MatchString(src) {
		t.Errorf("pkgselect.Select does not add ./internal/ci to want unconditionally; internal/ci scans the tree instead of importing what it guards, so an edit elsewhere selects no shard to run its class tests")
	}
	if got := classTestSelection(t); !slices.Contains(got, "./internal/ci") {
		t.Errorf("a docs-only change selected %v: ./internal/ci is missing", got)
	}
}

// TestSelectPackagesAlwaysAddsInternalDocs pins the selection: ./internal/docs
// is added to `want` on every selection, not only when the diff touches
// .github/.
func TestSelectPackagesAlwaysAddsInternalDocs(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, "internal", "pkgselect", "select.go"))
	if !selectDocsAppendRe.MatchString(src) {
		t.Errorf("pkgselect.Select does not add ./internal/docs to want unconditionally; internal/docs scans the tree instead of importing what it guards, so a docs-only change that breaks TestAgentsPageNamesEveryClassRule (#1504) selects no shard to run it, and the red surfaces in an integration batch instead of on the PR (#1364 toolchainroots, #1409 hostseam)")
	}
	if got := classTestSelection(t); !slices.Contains(got, "./internal/docs") {
		t.Errorf("a docs-only change selected %v: ./internal/docs is missing", got)
	}
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

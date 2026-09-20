package ci

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ci_selection_test.go pins the SELECTION side of the class-test contract. The
// budget test (ci_budget_test.go) says what the checks must be; this one says
// which packages must run them. Two independent places choose the packages a
// change tests:
//
//   - .github/scripts/select-packages.sh builds the `want` set the self-hosted
//     shards use; and
//   - ci.yml's test-hosted-merge job has its own inline selection and does not
//     call the script.
//
// internal/ci holds class tests that read the workflow and script files as text
// and scan the tree rather than import what they guard. A cmd/nova-swarm edit
// (PR #1073) therefore turned an internal/ci class test red but named no
// dependent in the import graph, so no shard was selected to run it and the
// branch sat for two hours. Both selection points must name ./internal/ci on
// every run; these tests read the two files as text, like the rest of the
// package (go.mod carries no YAML library).

var (
	// selectAppendRe is the top-level line (no leading whitespace) that adds
	// ./internal/ci to the script's `want` set, outside any `if`. The current
	// bug is that the line lives inside an `if` guarded on a .github/ diff, so
	// the regex is red until it moves to the top level.
	selectAppendRe = regexp.MustCompile(`(?m)^want="\$want \./internal/ci"\s*$`)

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
	// selectDocsAppendRe is the top-level line (no leading whitespace) that adds
	// ./internal/docs to the script's `want` set, outside any `if`, beside the
	// ./internal/ci line. internal/docs scans the tree instead of importing what
	// it guards, so a docs-only edit can break its class test without naming a
	// single dependent in the import graph.
	selectDocsAppendRe = regexp.MustCompile(`(?m)^want="\$want \./internal/docs"\s*$`)

	// mergeDocsAppendRe is the append to the merge gate's `$pkgs`, outside any
	// `||` fallback, so every group runs internal/docs and not only one that
	// changed no Go package. A trailing `;;` is allowed for the same reason as
	// the internal/ci append: the append sits in a `case` arm, and its guard
	// only prevents a DUPLICATE. The rule this pins is "unconditionally in
	// scope".
	mergeDocsAppendRe = regexp.MustCompile(`(?m)^\s*(\*\)\s*)?pkgs="\$pkgs \./internal/docs"\s*(;;)?\s*$`)
)

// TestSelectPackagesAlwaysAddsInternalCI pins the script: ./internal/ci is
// added to `want` on every selection, not only when the diff touches .github/.
func TestSelectPackagesAlwaysAddsInternalCI(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "scripts", "select-packages.sh"))
	if !selectAppendRe.MatchString(src) {
		t.Errorf("select-packages.sh does not add ./internal/ci to want unconditionally; internal/ci scans the tree instead of importing what it guards, so a cmd/nova-swarm edit (PR #1073) selects no shard to run its class tests")
	}
}

// TestMergeGateAlwaysAppendsInternalCI pins ci.yml's independent inline
// selection: internal/ci is appended to $pkgs on every group, not only when the
// group changed no Go package.
func TestMergeGateAlwaysAppendsInternalCI(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	step := stepBody(src, "select the packages this group changes")
	if strings.TrimSpace(step) == "" {
		t.Fatal("no `select the packages this group changes` step in ci.yml; the merge gate's selection moved and this test is looking in the wrong place")
	}
	if !mergeAppendRe.MatchString(step) {
		t.Errorf("the merge gate's selection does not always append ./internal/ci to $pkgs; internal/ci scans the tree, so a cmd/nova-swarm edit (PR #1073) leaves the leg green over a red class test")
	}
	if strings.Contains(step, mergeFallback) {
		t.Errorf("the merge gate's selection still carries %q; internal/ci must be appended on every group, not chosen only when $pkgs is empty", mergeFallback)
	}
}

// TestSelectPackagesAlwaysAddsInternalDocs pins the script: ./internal/docs is
// added to `want` on every selection, not only when the diff touches .github/.
func TestSelectPackagesAlwaysAddsInternalDocs(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "scripts", "select-packages.sh"))
	if !selectDocsAppendRe.MatchString(src) {
		t.Errorf("select-packages.sh does not add ./internal/docs to want unconditionally; internal/docs scans the tree instead of importing what it guards, so a docs-only change that breaks TestAgentsPageNamesEveryClassRule (#1504) selects no shard to run it, and the red surfaces in an integration batch instead of on the PR (#1364 toolchainroots, #1409 hostseam)")
	}
}

// TestMergeGateAlwaysAppendsInternalDocs pins ci.yml's independent inline
// selection: internal/docs is appended to $pkgs on every group, not only when
// the group changed no Go package.
func TestMergeGateAlwaysAppendsInternalDocs(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	step := stepBody(src, "select the packages this group changes")
	if strings.TrimSpace(step) == "" {
		t.Fatal("no `select the packages this group changes` step in ci.yml; the merge gate's selection moved and this test is looking in the wrong place")
	}
	if !mergeDocsAppendRe.MatchString(step) {
		t.Errorf("the merge gate's selection does not always append ./internal/docs to $pkgs; internal/docs scans the tree, so a docs-only change that breaks TestAgentsPageNamesEveryClassRule (#1504) leaves the leg green over a red class test, and the red surfaces in an integration batch instead of on the PR (#1364 toolchainroots, #1409 hostseam)")
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

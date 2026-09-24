package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// THE CLASS RULE BEHIND THE DOCUMENTS (SPEC-TOOLWORK §7 rules 2-3).
//
// docs/TESTS.md is what a stranger is pointed at first, and every block in it is
// real output pasted whole. The class test this repository carried asserted that
// a tool's section EXISTS; nothing asserted that anything RUNS it. Measured by
// the 2026-09-19 triage: 8 of 22 sections were executed by no test at all, and
// nine more collected what was printed into a `printed map[string]bool` and asked
// whether each documented line was somewhere in it -- so an abridged or a
// reordered block passed. Every drift found that week was found by a person.
//
// This test asserts EXECUTION. For every `## <tool>` section of docs/TESTS.md, a
// test in cmd/<tool> must call onboarding.CompareTranscript on that tool's own
// section, and must compare no other way: not onboarding.Execute, which runs and
// compares with a norm list the caller assembles, not onboarding.Shape, which
// compares a line's field names and drops its values, and not a
// `printed map[string]bool`. One comparator means one place a reader checks to
// learn what a transcript's green is worth.
//
// The sections not yet converted are listed, by name, with the issue that owes
// each. The list is shrink-only IN BOTH DIRECTIONS, the pattern
// testdata/prmerge_allowlist.txt sets: an unlisted section that no test executes
// is red, and a listed section that a test now executes is a stale entry and red
// too, so the list cannot be widened and left there.

// transcriptAllowlistPath is the shrink-only list of sections not yet converted.
const transcriptAllowlistPath = "testdata/transcripts_allowlist.txt"

// theComparator is the one call a firstrun_test.go may make.
const theComparator = "onboarding.CompareTranscript("

// theDocument is the file this rule is about; a test that executes a section
// opens it, and one that never names it is comparing something else.
const theDocument = "TESTS.md"

// otherWays are the comparisons rule 2 replaces, by the spelling that appears in
// a test's source. Each says what it lets through, because that sentence is the
// reason the conversion is worth anybody's afternoon.
var otherWays = []struct{ spelling, lets string }{
	{"onboarding.Execute(", "runs and compares with a norm list assembled at the call site, so what is not compared is not the shared table"},
	{"onboarding.ExecuteWith(", "runs and compares with a norm list assembled at the call site, so what is not compared is not the shared table"},
	{"onboarding.Compare(", "compares one step with a norm list assembled at the call site"},
	{"onboarding.Shape(", "compares a line's event prefix and field NAMES and drops every value"},
	{"printed[", "collects what was printed into a set, so an abridged or reordered block passes"},
	{"printed :=", "collects what was printed into a set, so an abridged or reordered block passes"},
}

func TestEveryTranscriptIsExecutedLineForLine(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	md := readFile(t, filepath.Join(root, "docs", "TESTS.md"))
	allow := readAllowlist(t, transcriptAllowlistPath)
	tree := repoTree(t)

	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	if err != nil {
		t.Fatal(err)
	}

	var violations []string
	sections := 0
	executed, isSection := map[string]bool{}, map[string]bool{}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		tool := e.Name()
		if _, ok := onboarding.Section(md, tool); !ok {
			continue
		}
		sections++
		isSection[tool] = true

		calls, others := readsTheSection(tree, tool)
		if calls && len(others) == 0 {
			executed[tool] = true
		}
		if allow[tool] {
			continue
		}
		if !calls {
			violations = append(violations, fmt.Sprintf(
				"docs/TESTS.md has a `## %s` section and no test in cmd/%s compares it with %s; the section is a promise no build checks. Convert it -- run every `$` line of the `### First run` block in order and hand the steps and the results to the one comparator -- or list %s in %s with the issue that owes it",
				tool, tool, theComparator, tool, transcriptAllowlistPath))
		}
		for _, other := range others {
			violations = append(violations, fmt.Sprintf(
				"cmd/%s compares its transcript with %s, which %s; %s is the one comparison a firstrun_test.go may make (SPEC-TOOLWORK §7 rule 2)",
				tool, other.spelling, other.lets, theComparator))
		}
	}

	if sections == 0 {
		t.Fatal("no `## <tool>` sections found in docs/TESTS.md; this walk was looking in the wrong place and would have passed by checking nothing")
	}
	// The list only shrinks, and it shrinks two ways. A section a test now
	// executes line for line, with the one comparator and no other, may not stay
	// listed as owed -- and an entry naming NO section is an orphan that nothing
	// above can ever make stale, so it would sit in the list for good, reading
	// like an exception somebody still owes.
	for tool := range allow {
		switch {
		case !isSection[tool]:
			violations = append(violations, fmt.Sprintf(
				"%s lists %s, and docs/TESTS.md has no `## %s` section with a directory under cmd/; an entry naming no section is an orphan no conversion can ever remove, so nothing would shrink it. Delete it, or fix its spelling",
				transcriptAllowlistPath, tool, tool))
		case executed[tool]:
			violations = append(violations, fmt.Sprintf(
				"%s lists %s as not yet converted, and cmd/%s now calls %s and compares no other way; delete the stale entry (the list only shrinks)",
				transcriptAllowlistPath, tool, tool, theComparator))
		}
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// readsTheSection reports whether any test file in cmd/<tool> calls the one
// comparator on this tool's own section, and every other comparison those test
// files make.
//
// THREE SPELLINGS HAVE TO BE IN THE SAME FILE, and the third is the one that
// makes the proxy worth anything: the comparator's call, the tool's own name as
// a literal -- how a test names the section it opens, `onboarding.FirstRun(md,
// "nova-ci")` -- AND the document. Without the document a package that called
// the comparator on a hand-written fixture holding its own name counted as
// executing its section, which was reproduced green in the cold read of #1723.
// Asking for `TESTS.md` means the file at least opens the document this rule is
// about.
//
// It is still a proxy and this test says so rather than pretending otherwise:
// the narrowing is written out in docs/SPEC-CI.md. What it cannot see is a file
// that opens the document and compares something else it built; what closes THAT
// is the `transcript-test` kind's own control (SPEC-TOOLWORK §7 rule 4), which
// seeds the tool's real section three ways and demands red -- a control that
// runs per card, where this class test runs per tree.
func readsTheSection(tree *repoTreeIndex, tool string) (bool, []struct{ spelling, lets string }) {
	dir := "cmd/" + tool + "/"
	calls := false
	var others []struct{ spelling, lets string }
	seen := map[string]bool{}
	for _, f := range tree.Files {
		if !f.Test || !strings.HasPrefix(f.Rel, dir) || strings.Contains(strings.TrimPrefix(f.Rel, dir), "/") {
			continue
		}
		src := string(f.Src)
		if strings.Contains(src, theComparator) && strings.Contains(src, `"`+tool+`"`) && strings.Contains(src, theDocument) {
			calls = true
		}
		for _, other := range otherWays {
			if strings.Contains(src, other.spelling) && !seen[other.spelling] {
				seen[other.spelling] = true
				others = append(others, other)
			}
		}
	}
	return calls, others
}

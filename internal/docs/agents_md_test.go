package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// agents_md_test.go holds AGENTS.md — the ONE page a friend's harness reads
// before it touches this repository — against the rules it claims to carry.
//
// OpenCode and Codex read AGENTS.md natively. Claude Code 2.1.277 and later
// reads it when the folder has no CLAUDE.md; an older Claude Code loads nothing
// and has to be told `read AGENTS.md first`. Glenn, 2026-09-18: "AGENTS.md from
// now on! We should standardize on it, instead of CLAUDE.md." The ruling is
// AGENTS.md ALONE — no CLAUDE.md, no pointer file, no symlink. A pointer would
// be worse than nothing here, because Claude Code reads AGENTS.md exactly when
// no CLAUDE.md stands: a stub would hold every Claude session on a file that
// says nothing while the other harnesses read the real page.
//
// Three things are pinned, each for a way the page rots:
//
//	(a) it exists, and it names every class rule docs/SPEC-CI.md indexes, by
//	    rule name — a friend meeting a red reads the name off the refusal, and a
//	    rule the front page does not list is a rule nobody was told about;
//	(b) it stays under the line cap — a harness that reads it reads it at the
//	    start of every session, so its cost is paid on every turn, and a page
//	    that grows without a ceiling stops being read;
//	(c) no per-harness file stands anywhere in the tree.
//
// It reads text and runs nothing.

// agentsPath is the one page, relative to this package.
const agentsPath = "../../AGENTS.md"

// contributingPath is where the prose rules live (S11, nova-tools#2498).
const contributingPath = "../../docs/CONTRIBUTING.md"

// agentsLineCap is the ceiling. Every harness pays this at session start.
const agentsLineCap = 120

// harnessFiles are the per-harness files a harness would load INSTEAD of
// AGENTS.md. None of them may stand in this tree. CLAUDE.md is the only one
// today; a symlink counts, because what a harness opens is the contents.
var harnessFiles = []string{"CLAUDE.md"}

// classRuleRe reads the rule NAME out of a `### `name` — description` heading
// in SPEC-CI.md's index.
var classRuleRe = regexp.MustCompile("(?m)^### `([^`]+)` — ")

// TestContributingPageNamesEveryClassRule holds the contract: CONTRIBUTING.md
// carries the prose rules moved out of AGENTS.md (nova-tools#2498 S11) and must
// name every class rule docs/SPEC-CI.md indexes, by rule name.
func TestContributingPageNamesEveryClassRule(t *testing.T) {
	t.Parallel()

	contributingPageNamesEveryClassRule(t)
}

// contributingPageNamesEveryClassRule is the body both test names run; a test
// that calls another Test would call t.Parallel twice.
func contributingPageNamesEveryClassRule(t *testing.T) {
	t.Helper()
	page, err := os.ReadFile(contributingPath)
	if err != nil {
		t.Fatalf("%s: %v; docs/CONTRIBUTING.md carries the prose and class rules — it is not optional", contributingPath, err)
	}
	body := string(page)

	rules := classRuleNames(t)
	if len(rules) == 0 {
		t.Fatalf("%s: the %q section indexes no rule; this test is reading the wrong section", specCIPath, classTestSection)
	}

	var missing []string
	for _, name := range rules {
		if !strings.Contains(body, "`"+name+"`") {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	for _, name := range missing {
		t.Errorf("%s does not name the class rule `%s`; a friend meets that rule as a red and reads its name off the refusal, so CONTRIBUTING.md must list it — add it to the ten, or to the by-name index beside them, and keep the full entry in %s",
			contributingPath, name, specCIPath)
	}
}

// TestAgentsPageNamesEveryClassRule preserves the historical test name while
// asserting the contract in docs/CONTRIBUTING.md.
func TestAgentsPageNamesEveryClassRule(t *testing.T) {
	t.Parallel()

	contributingPageNamesEveryClassRule(t)
}

// TestAgentsPageStaysUnderTheLineCap holds the ceiling.
func TestAgentsPageStaysUnderTheLineCap(t *testing.T) {
	t.Parallel()

	page, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("%s: %v", agentsPath, err)
	}
	lines := strings.Count(strings.TrimRight(string(page), "\n"), "\n") + 1
	if lines > agentsLineCap {
		t.Errorf("%s is %d lines, over the cap of %d; a harness that reads this page reads it at the start of every session, so the cost is paid on every turn — move the detail into docs/ and leave the rule and the link here",
			agentsPath, lines, agentsLineCap)
	}
}

// TestNoPerHarnessFileStandsBesideAgents holds the ruling: AGENTS.md alone.
func TestNoPerHarnessFileStandsBesideAgents(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	for _, name := range harnessFiles {
		found, err := findHarnessFiles(root, name)
		if err != nil {
			t.Fatalf("walking for %s: %v", name, err)
		}
		for _, rel := range found {
			t.Errorf("%s stands beside AGENTS.md; Glenn, 2026-09-18: AGENTS.md alone — no %s, no pointer file, no symlink. Claude Code reads AGENTS.md exactly when no %s stands, so even a one-line pointer holds every Claude session on a file that says nothing while the other harnesses read the real page — delete it, and move anything it carried into AGENTS.md or under docs/",
				rel, name, name)
		}
	}
}

// classRuleNames returns the rule names SPEC-CI.md's index declares, in the
// order the section prints them.
func classRuleNames(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, m := range classRuleRe.FindAllStringSubmatch(classTestsSection(t), -1) {
		names = append(names, m[1])
	}
	return names
}

// findHarnessFiles returns every repo-relative path named name under root,
// skipping .git and testdata (a fixture may legitimately hold one).
func findHarnessFiles(root, name string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == "testdata" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == name {
			found = append(found, rel)
		}
		return nil
	})
	sort.Strings(found)
	return found, err
}

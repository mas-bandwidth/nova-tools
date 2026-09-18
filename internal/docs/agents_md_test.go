package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// agents_md_test.go holds AGENTS.md — the ONE page every friend's harness loads
// before it touches this repository — against the rules it claims to carry.
//
// Claude Code 2.1.277 reads AGENTS.md when a folder has no CLAUDE.md; OpenCode
// and Codex already do. Glenn, 2026-09-18: "AGENTS.md from now on! We should
// standardize on it, instead of CLAUDE.md." So there is one page, not one per
// harness, and a per-harness file in this tree is a pointer to it rather than a
// second set of rules that drifts from the first within a week.
//
// Three things are pinned, each for a way the page rots:
//
//	(a) it exists, and it names every class rule docs/SPEC-CI.md indexes, by
//	    rule name — a friend meeting a red reads the name off the refusal, and a
//	    rule the front page does not list is a rule nobody was told about;
//	(b) it stays under the line cap — the page is read at the start of every
//	    session by every harness, so its cost is paid on every turn, and a page
//	    that grows without a ceiling stops being read;
//	(c) every per-harness file carries a one-line pointer here and no rules of
//	    its own.
//
// It reads text and runs nothing.

// agentsPath is the one page, relative to this package.
const agentsPath = "../../AGENTS.md"

// agentsLineCap is the ceiling. Every harness pays this at session start.
const agentsLineCap = 120

// harnessFiles are the per-harness files a harness loads INSTEAD of AGENTS.md.
// Each one present must point here rather than carry rules. CLAUDE.md is the
// only one today: OpenCode and Codex read AGENTS.md directly.
var harnessFiles = []string{"CLAUDE.md"}

// harnessPointerLines is how short a pointer file may be and still be a
// pointer. Three non-blank lines is a heading, a sentence and a link; more than
// that is a file growing its own rules.
const harnessPointerLines = 3

// classRuleRe reads the rule NAME out of a `### `name` — description` heading
// in SPEC-CI.md's index.
var classRuleRe = regexp.MustCompile("(?m)^### `([^`]+)` — ")

// TestAgentsPageNamesEveryClassRule is the front page's contract.
func TestAgentsPageNamesEveryClassRule(t *testing.T) {
	t.Parallel()

	page, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("%s: %v; AGENTS.md is the one page every friend's harness loads before touching this repo — it is not optional", agentsPath, err)
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
		t.Errorf("%s does not name the class rule `%s`; a friend meets that rule as a red and reads its name off the refusal, so the front page must list it — add it to the ten, or to the by-name index beside them, and keep the full entry in %s",
			agentsPath, name, specCIPath)
	}
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
		t.Errorf("%s is %d lines, over the cap of %d; every harness loads this page at the start of every session, so the cost is paid on every turn — move the detail into docs/ and leave the rule and the link here",
			agentsPath, lines, agentsLineCap)
	}
}

// TestEveryHarnessFilePointsAtAgents holds the per-harness files to a pointer.
func TestEveryHarnessFilePointsAtAgents(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	for _, name := range harnessFiles {
		found, err := findHarnessFiles(root, name)
		if err != nil {
			t.Fatalf("walking for %s: %v", name, err)
		}
		for _, rel := range found {
			body, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				t.Errorf("%s: %v", rel, err)
				continue
			}
			text := string(body)
			if !strings.Contains(text, "AGENTS.md") {
				t.Errorf("%s does not point at AGENTS.md; Glenn, 2026-09-18: one page, not one per harness — replace this file's contents with a single line pointing at AGENTS.md, or delete it",
					rel)
			}
			if n := nonBlankLines(text); n > harnessPointerLines {
				t.Errorf("%s carries %d non-blank lines, more than the %d a pointer needs; a per-harness file that carries rules of its own is a second front page that drifts from the first — leave one line pointing at AGENTS.md and move the rules there",
					rel, n, harnessPointerLines)
			}
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

// nonBlankLines counts the lines that carry something other than whitespace.
func nonBlankLines(text string) int {
	n := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

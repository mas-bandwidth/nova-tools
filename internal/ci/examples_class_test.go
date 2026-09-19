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

// THE CLASS RULE BEHIND A PASTED LINE (SPEC-TOOLWORK §7 rule 7).
//
// docs/TESTS.md is not the only document a stranger pastes from. README.md,
// docs/USAGE.md, docs/CLI.md's `### First run` sections and
// docs/nova-swarm-quickstart.md all carry `$ ` lines, and every `help` banner
// ends in an `example:` block whose whole point is that the lines under it can be
// pasted. #1455 measured those banners: 28 of 61 example lines exited 2 when
// pasted -- a door that opens onto a wall, in the one place a stranger is sent
// first.
//
// So every such line is COUNTED. It is either executed by a test through
// onboarding.CompareTranscript -- the same comparator docs/TESTS.md's sections
// are held to, so that "this line runs" means the same thing everywhere -- or it
// is listed here with the reason it is not. The list only shrinks: a new
// unexecuted example is red on the pull request that adds it, and an entry whose
// line has left the document is a stale entry and red too.
//
// This rule does not RUN the lines and does not ask whether they work. It asks
// whether anything checks them. What each listed line does when pasted is the
// business of the card that takes it off the list.

// unexecutedExamplesPath is the shrink-only list of pasteable lines no test
// executes through the one comparator.
const unexecutedExamplesPath = "testdata/unexecuted_examples.txt"

// pasteableDocs are the documents §7 rule 7 counts, beside the help banners.
// README.md, docs/USAGE.md and docs/nova-swarm-quickstart.md are counted whole;
// docs/CLI.md is counted under its `### First run` headings, which is where its
// pasteable lines live and where the rule points.
var pasteableDocs = []struct {
	path      string
	firstRuns bool
}{
	{path: "README.md"},
	{path: "docs/USAGE.md"},
	{path: "docs/nova-swarm-quickstart.md"},
	{path: "docs/CLI.md", firstRuns: true},
}

func TestUnexecutedExamplesOnlyShrink(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	listed := readReasonedList(t, unexecutedExamplesPath)
	executed := docsExecutedThroughTheComparator(t)

	candidates := map[string]string{} // key -> where a reader meets it
	for _, doc := range pasteableDocs {
		for _, cmd := range pasteableLines(readFile(t, filepath.Join(root, doc.path)), doc.firstRuns) {
			candidates[doc.path+": "+cmd] = doc.path
		}
	}
	for tool, examples := range helpExamples(t, root) {
		for _, cmd := range examples {
			candidates["help "+tool+": "+cmd] = "help " + tool
		}
	}
	if len(candidates) == 0 {
		t.Fatal("no `$ ` lines and no `example:` lines found; this walk was looking in the wrong place and would have passed by counting nothing")
	}

	var violations []string
	for key, where := range candidates {
		if executed[where] || listed[key] {
			continue
		}
		violations = append(violations, fmt.Sprintf(
			"%s is pasteable and no test executes it through onboarding.CompareTranscript; execute it, or list it in %s with the reason (the list only shrinks)",
			key, unexecutedExamplesPath))
	}
	// The list only shrinks in the other direction too: an entry whose line has
	// left the document, or whose document a test now executes, is stale.
	for key := range listed {
		if _, still := candidates[key]; !still {
			violations = append(violations, fmt.Sprintf(
				"%s lists %s and no such pasteable line is there any more; delete the stale entry (the list only shrinks)",
				unexecutedExamplesPath, key))
		}
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
	t.Logf("%d pasteable lines counted, %d listed as unexecuted", len(candidates), len(listed))
}

// pasteableLines returns the `$ ` command lines inside a document's fenced
// blocks. `firstRuns` counts only the blocks under a `### First run` heading,
// which is where docs/CLI.md keeps the lines a stranger pastes; elsewhere in that
// file a `$ ` line is a fragment of a larger explanation.
//
// A fenced block is tracked rather than assumed, because a `$ ` outside one is
// prose -- `$N$` in nova-swarm's quickstart is arithmetic, not a command.
func pasteableLines(md string, firstRuns bool) []string {
	var out []string
	fenced, inSection := false, !firstRuns
	for _, line := range strings.Split(md, "\n") {
		switch {
		case strings.HasPrefix(line, "```"):
			fenced = !fenced
			continue
		case fenced:
			if cmd, ok := strings.CutPrefix(line, "$ "); ok && inSection {
				out = append(out, strings.TrimSpace(cmd))
			}
			continue
		case firstRuns && strings.HasPrefix(line, "### "):
			inSection = strings.TrimSpace(line) == onboarding.FirstRunHeading
		case firstRuns && strings.HasPrefix(line, "## "):
			inSection = false
		}
	}
	return out
}

// docsExecutedThroughTheComparator names the documents some test compares with
// onboarding.CompareTranscript. It is empty today and is not a stub: the sentence
// rule 7 makes is "executed or listed", and the day a card executes README.md's
// block through the one comparator its lines leave the list by this map rather
// than by anyone editing the list. A document named here whose lines are still
// listed is caught by the stale-entry half above.
func docsExecutedThroughTheComparator(t *testing.T) map[string]bool {
	t.Helper()
	executed := map[string]bool{}
	for _, f := range repoTree(t).Files {
		if !f.Test || !strings.Contains(string(f.Src), theComparator) {
			continue
		}
		src := string(f.Src)
		for _, doc := range pasteableDocs {
			// The document is named as a path a test opens, in the same file
			// that calls the comparator.
			if strings.Contains(src, `"`+filepath.Base(doc.path)+`"`) {
				executed[doc.path] = true
			}
		}
	}
	return executed
}

// helpExamples returns each command's `example:` lines, read off the banner the
// built binary prints. They are read from the BINARY and not from the source,
// because the banner is assembled at run time and what a stranger pastes is what
// the binary said.
func helpExamples(t *testing.T, root string) map[string][]string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	if err != nil {
		t.Fatal(err)
	}
	examples := map[string][]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		tool := e.Name()
		bin := buildTool(t, root, tool)
		exit, banner, errs := runBare(t, root, tool, bin, []string{"help"})
		if exit != 0 {
			t.Fatalf("`%s help` exits %d, want 0; stderr: %s", tool, exit, errs)
		}
		lines, err := onboarding.ExampleLines(banner, tool)
		if err != nil {
			// The banner having an `example:` block at all is
			// TestEveryCommandMeetsTheOnboardingStandard's rule, not this one.
			continue
		}
		examples[tool] = lines
	}
	return examples
}

// readReasonedList reads the shrink-only list: one entry per line as
// `<key>\t<reason>`, blank lines and `#` comments ignored.
//
// The separator is a TAB and not the ` #` the other allowlists use, for a reason
// this list alone has: its keys are lines a stranger pastes into a shell, and a
// pasted line may carry a shell comment of its own -- docs/CLI.md's
// `go build -o ~/bin/nova-sandbox ./cmd/nova-sandbox  # or name it with --sandbox <path>`
// is one. Splitting those keys on ` #` would silently truncate them, and a
// truncated key matches nothing and reads like a stale entry.
func readReasonedList(t *testing.T, path string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, reason, found := strings.Cut(line, "\t")
		if !found || strings.TrimSpace(reason) == "" {
			t.Errorf("%s:%d carries no reason; an entry is `<line>\\t<the reason no test executes it>`, and a list of bare lines is a list nobody can shrink:\n%s", path, i+1, line)
			continue
		}
		listed[strings.TrimSpace(key)] = true
	}
	return listed
}

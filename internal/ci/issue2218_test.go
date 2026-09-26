package ci

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

func TestIssue2218(t *testing.T) {
	t.Parallel()

	t.Run("platform_line_must_name_a_ci_leg", func(t *testing.T) {
		root := repoRoot(t)

		ciYAML := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
		legs := CILegsFromYAML(ciYAML)
		if len(legs) == 0 {
			t.Fatal("CILegsFromYAML found no platform legs in ci.yml; a Platform line would pass by being compared against nothing")
		}

		md := readFile(t, filepath.Join(root, "docs", "TESTS.md"))
		platforms, err := PlatformLinesFromTESTSmd(md)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range platforms {
			if !legs[p] {
				t.Errorf("docs/TESTS.md carries Platform: %s in a `## nova-*` section, which is not a GOOS that ci.yml runs a leg for (the platform a skipped transcript names must still be executed somewhere; known legs: %v)", p, mapKeysSorted(legs))
			}
		}
	})

	t.Run("unexecuted_examples_only_shrink", func(t *testing.T) {
		root := repoRoot(t)

		docExamples, err := PastedDocExamplesByDoc(root)
		if err != nil {
			t.Fatal(err)
		}
		help, err := HelpBannerExamples(root)
		if err != nil {
			t.Fatal(err)
		}
		var examples []string
		seen := make(map[string]bool)
		docsOf := make(map[string][]string)
		for _, e := range docExamples {
			if !seen[e.Line] {
				seen[e.Line] = true
				examples = append(examples, e.Line)
			}
			docsOf[e.Line] = append(docsOf[e.Line], e.Doc)
		}
		helpLines := make([]string, 0, len(help))
		for ex := range help {
			helpLines = append(helpLines, ex)
		}
		sort.Strings(helpLines)
		for _, ex := range helpLines {
			if !seen[ex] {
				seen[ex] = true
				examples = append(examples, ex)
			}
		}

		listPath := filepath.Join("testdata", "unexecuted_examples.txt")
		unexecuted := loadAllowlist(t, listPath, unexecutedListOptions)
		headList := unexecuted.Text()
		allow := make(map[string]bool)
		for _, r := range unexecuted.Rows() {
			allow[r.Key] = true
		}

		// Shrink-only: every row the change adds to the list is a new
		// unexecuted example, and fails here (SPEC-TOOLWORK §7 rule 7).
		base, err := ChangeBase(root, os.Getenv)
		switch {
		case err != nil && changeEventMustCompare(os.Getenv("GITHUB_EVENT_NAME")):
			t.Fatalf("%s is shrink-only, and a %s run must compare it with its base: %v", listPath, os.Getenv("GITHUB_EVENT_NAME"), err)
		case err != nil:
			t.Logf("%s not compared with a base (no base commit here): %v", listPath, err)
		default:
			baseList, present, err := ListAtCommit(root, base, UnexecutedListPath)
			if err != nil {
				t.Fatal(err)
			}
			if !present {
				t.Logf("%s is introduced by this change (base %.12s has none); later changes compare with it", listPath, base)
			}
			for _, r := range AddedListRows(baseList, headList) {
				if present {
					t.Errorf("%s adds %q, which base %.12s does not list; the list only shrinks -- execute the example through the comparator and name the test in compared_examples.txt instead", listPath, r, base)
				}
			}
		}

		comparedPath := filepath.Join("testdata", "compared_examples.txt")
		comparedList := loadAllowlist(t, comparedPath, comparedListOptions)
		compared := parseCompared(t, comparedPath, comparedList)
		// The measured set of compared_examples.txt is every entry that is
		// still a real example; a test is named by hand, so it never grows here.
		comparedMeasured := map[string]bool{}
		for ex := range compared {
			if seen[ex] {
				comparedMeasured[ex] = true
			}
		}
		for _, row := range allowlist.Check(t, comparedList, comparedMeasured).Stale {
			t.Errorf("%s:%d names %q, which is not a pasted example of the named docs or a help banner; delete the stale entry", comparedPath, row.Line, row.Key)
		}
		for ex, c := range compared {
			if !comparedMeasured[ex] {
				continue
			}
			if allow[ex] {
				t.Errorf("%q is in both %s and %s; a compared example leaves the unexecuted list", ex, comparedPath, listPath)
			}
			docs := docsOf[ex]
			if _, fromHelp := help[ex]; fromHelp && strings.HasPrefix(ex, "example: ") {
				docs = nil // the banner, not a doc, is where it is pasted from
			}
			if problem := ComparedEntryProblem(root, ComparedEntry{File: c.file, Test: c.test, Ex: ex}, docs); problem != "" {
				t.Errorf("%s:%d: %s", comparedPath, c.line, problem)
			}
		}

		unmatched := unlistedExamples(examples, allow, compared)
		// The measured set of unexecuted_examples.txt is every real example no
		// test executes yet: a listed one still here and not moved to
		// compared_examples.txt, and every unmatched one (which the ceiling
		// refuses to add).
		measured := map[string]bool{}
		for entry := range allow {
			if seen[entry] && compared[entry].test == "" {
				measured[entry] = true
			}
		}
		for _, u := range unmatched {
			measured[u] = true
		}
		for _, row := range allowlist.Check(t, unexecuted, measured).Stale {
			if seen[row.Key] {
				continue // moved to compared_examples.txt: the "in both" line above says so
			}
			t.Errorf("%s lists %q which is not a pasted example of the named docs or a help banner; delete the stale entry (the list only shrinks)", listPath, row.Key)
		}
		if len(unmatched) > 0 {
			t.Errorf("%d unlisted pasted example(s) in the named docs or the help banners are not covered by a test through onboarding.Compare; %s is shrink-only and does not grow to match new examples -- cover each with a comparator test and name it in %s instead of listing it", len(unmatched), listPath, comparedPath)
		}
		for _, u := range unmatched {
			t.Logf("  unlisted (needs a comparator test named in compared_examples.txt, not a list entry): %s", u)
		}
	})
}

// unlistedExamples returns the examples neither listed as unexecuted nor
// named in compared_examples.txt: each fails the class test.
func unlistedExamples(examples []string, allow map[string]bool, compared map[string]comparedExample) []string {
	var out []string
	for _, ex := range examples {
		if !allow[ex] && compared[ex].test == "" {
			out = append(out, ex)
		}
	}
	return out
}

// changeEventMustCompare says whether a GitHub Actions event is a change on
// its way into a branch -- a pull request or a merge group -- whose run must
// prove the list only shrank, rather than log that it could not.
func changeEventMustCompare(event string) bool {
	switch event {
	case "pull_request", "pull_request_target", "merge_group":
		return true
	}
	return false
}

// comparedExample is one entry of testdata/compared_examples.txt: the test
// that executes a pasted example through the comparator.
type comparedExample struct {
	file string // test file, relative to the repo root
	test string // test function name
	line int    // line in compared_examples.txt
	ex   string // the pasted example, exactly as in the doc or banner
}

// unexecutedListOptions keys a row of unexecuted_examples.txt by the whole
// example line; the list is shrink-only against its base as well.
var unexecutedListOptions = allowlist.Options{Key: allowlist.WholeRow, Ceiling: true}

// comparedListOptions keys a row of compared_examples.txt by its example: one
// `<test file>:<TestName> <$ line | example: line>` per line. An `example:`
// entry is a help banner's example line, so a test that executes it moves it
// here off the unexecuted list like any $ line. The row names a test, which no
// walk can measure, so an update only ever drops a row here.
var comparedListOptions = allowlist.Options{Key: comparedExampleKey, Ceiling: true}

func comparedExampleKey(row string) string {
	_, ex, _ := strings.Cut(row, " ")
	return strings.TrimSpace(ex)
}

func parseCompared(t *testing.T, path string, list *allowlist.List) map[string]comparedExample {
	t.Helper()
	out := make(map[string]comparedExample)
	for _, row := range list.Rows() {
		ref, ex, ok := strings.Cut(row.Text, " ")
		file, test, okRef := strings.Cut(ref, ":")
		ex = strings.TrimSpace(ex)
		if !ok || !okRef || file == "" || test == "" || !(strings.HasPrefix(ex, "$ ") || strings.HasPrefix(ex, "example: ")) {
			t.Errorf("%s:%d: want `<test file>:<TestName> $ <example>` or `<test file>:<TestName> example: <line>`, got %q", path, row.Line, row.Text)
			continue
		}
		if prev, dup := out[ex]; dup {
			t.Errorf("%s:%d: %q is already listed at line %d", path, row.Line, ex, prev.line)
			continue
		}
		out[ex] = comparedExample{file: file, test: test, line: row.Line, ex: ex}
	}
	return out
}

func mapKeysSorted(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

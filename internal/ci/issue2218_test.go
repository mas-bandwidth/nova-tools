package ci

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
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
		headList := readFile(t, listPath)
		allow := make(map[string]bool)
		for _, r := range ListRows(headList) {
			allow[r] = true
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
		compared := readCompared(t, comparedPath)
		for ex, c := range compared {
			if !seen[ex] {
				t.Errorf("%s:%d names %q, which is not a pasted example of the named docs or a help banner; delete the stale entry", comparedPath, c.line, ex)
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

		var stale []string
		for entry := range allow {
			if !seen[entry] {
				stale = append(stale, entry)
			}
		}
		sort.Strings(stale)
		unmatched := unlistedExamples(examples, allow, compared)

		for _, s := range stale {
			t.Errorf("%s lists %q which is not a pasted example of the named docs or a help banner; delete the stale entry (the list only shrinks)", listPath, s)
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

// readCompared reads testdata/compared_examples.txt: one
// `<test file>:<TestName> <$ line | example: line>` per line, blank lines and
// '#' lines skipped. An `example:` entry is a help banner's example line, so a
// test that executes it moves it here off the unexecuted list like any $ line.
func readCompared(t *testing.T, path string) map[string]comparedExample {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return parseCompared(t, path, string(raw))
}

func parseCompared(t *testing.T, path, raw string) map[string]comparedExample {
	t.Helper()
	out := make(map[string]comparedExample)
	for i, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ref, ex, ok := strings.Cut(line, " ")
		file, test, okRef := strings.Cut(ref, ":")
		ex = strings.TrimSpace(ex)
		if !ok || !okRef || file == "" || test == "" || !(strings.HasPrefix(ex, "$ ") || strings.HasPrefix(ex, "example: ")) {
			t.Errorf("%s:%d: want `<test file>:<TestName> $ <example>` or `<test file>:<TestName> example: <line>`, got %q", path, i+1, line)
			continue
		}
		if prev, dup := out[ex]; dup {
			t.Errorf("%s:%d: %q is already listed at line %d", path, i+1, ex, prev.line)
			continue
		}
		out[ex] = comparedExample{file: file, test: test, line: i + 1, ex: ex}
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

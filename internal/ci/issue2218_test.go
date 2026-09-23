package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue2218(t *testing.T) {
	t.Run("platform_line_must_name_a_ci_leg", func(t *testing.T) {
		root := repoRoot(t)

		ciYAML := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
		legs := CILegsFromYAML(ciYAML)
		if len(legs) == 0 {
			t.Fatal("CILegsFromYAML found no platform legs in ci.yml; a Platform line would pass by being compared against nothing")
		}

		md := readFile(t, filepath.Join(root, "docs", "TESTS.md"))
		platforms := PlatformLinesFromTESTSmd(md)
		for _, p := range platforms {
			if !legs[p] {
				t.Errorf("docs/TESTS.md carries Platform: %s in a `## nova-*` section, which is not a GOOS that ci.yml runs a leg for (the platform a skipped transcript names must still be executed somewhere; known legs: %v)", p, mapKeysSorted(legs))
			}
		}
	})

	t.Run("unexecuted_examples_only_shrink", func(t *testing.T) {
		root := repoRoot(t)

		examples, err := PastedDocExamples(root)
		if err != nil {
			t.Fatal(err)
		}

		listPath := filepath.Join("testdata", "unexecuted_examples.txt")
		allow := readUnexecuted(t, listPath)

		seen := make(map[string]bool, len(examples))
		for _, ex := range examples {
			seen[ex] = true
		}

		var stale []string
		for entry := range allow {
			if !seen[entry] {
				stale = append(stale, entry)
			}
		}
		var unmatched []string
		for _, ex := range examples {
			if !allow[ex] {
				unmatched = append(unmatched, ex)
			}
		}

		for _, s := range stale {
			t.Errorf("%s lists %q which is not a $  line in any of the named docs; delete the stale entry (the list only shrinks)", listPath, s)
		}
		if len(unmatched) > 0 {
			t.Errorf("%d unlisted pasted example(s) in the named docs are not covered by a test through onboarding.CompareTranscript; %s is shrink-only and does not grow to match new doc examples -- cover each with a comparator test instead of listing it", len(unmatched), listPath)
		}
		for _, u := range unmatched {
			t.Logf("  unlisted (needs a comparator test, not a list entry): %s", u)
		}
	})
}

// readUnexecuted reads the shrink-only unexecuted-examples list: one example per
// line, blank lines and lines beginning '#' skipped.
func readUnexecuted(t *testing.T, path string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	allow := make(map[string]bool)
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		allow[line] = true
	}
	return allow
}

func mapKeysSorted(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

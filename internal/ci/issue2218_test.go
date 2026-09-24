package ci

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
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
		examples := make([]string, 0, len(docExamples))
		docsOf := make(map[string][]string)
		for _, e := range docExamples {
			examples = append(examples, e.Line)
			docsOf[e.Line] = append(docsOf[e.Line], e.Doc)
		}

		listPath := filepath.Join("testdata", "unexecuted_examples.txt")
		allow := readUnexecuted(t, listPath)

		seen := make(map[string]bool, len(examples))
		for _, ex := range examples {
			seen[ex] = true
		}

		comparedPath := filepath.Join("testdata", "compared_examples.txt")
		compared := readCompared(t, comparedPath)
		for ex, c := range compared {
			if !seen[ex] {
				t.Errorf("%s:%d names %q, which is not a $  line in any of the named docs; delete the stale entry", comparedPath, c.line, ex)
			}
			if allow[ex] {
				t.Errorf("%q is in both %s and %s; a compared example leaves the unexecuted list", ex, comparedPath, listPath)
			}
			if problem := comparatorTestProblem(root, c, docsOf[ex]); problem != "" {
				t.Errorf("%s:%d: %s", comparedPath, c.line, problem)
			}
		}

		var stale []string
		for entry := range allow {
			if !seen[entry] {
				stale = append(stale, entry)
			}
		}
		var unmatched []string
		for _, ex := range examples {
			if !allow[ex] && compared[ex].test == "" {
				unmatched = append(unmatched, ex)
			}
		}

		for _, s := range stale {
			t.Errorf("%s lists %q which is not a $  line in any of the named docs; delete the stale entry (the list only shrinks)", listPath, s)
		}
		if len(unmatched) > 0 {
			t.Errorf("%d unlisted pasted example(s) in the named docs are not covered by a test through onboarding.Compare; %s is shrink-only and does not grow to match new doc examples -- cover each with a comparator test and name it in %s instead of listing it", len(unmatched), listPath, comparedPath)
		}
		for _, u := range unmatched {
			t.Logf("  unlisted (needs a comparator test named in compared_examples.txt, not a list entry): %s", u)
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

// comparedExample is one entry of testdata/compared_examples.txt: the test
// that executes a pasted example through the comparator.
type comparedExample struct {
	file string // test file, relative to the repo root
	test string // test function name
	line int    // line in compared_examples.txt
	ex   string // the pasted example, exactly as in the doc
}

// readCompared reads testdata/compared_examples.txt: one
// `<test file>:<TestName> <$ line>` per line, blank lines and '#' lines skipped.
func readCompared(t *testing.T, path string) map[string]comparedExample {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]comparedExample)
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ref, ex, ok := strings.Cut(line, " ")
		file, test, okRef := strings.Cut(ref, ":")
		ex = strings.TrimSpace(ex)
		if !ok || !okRef || file == "" || test == "" || !strings.HasPrefix(ex, "$ ") {
			t.Errorf("%s:%d: want `<test file>:<TestName> $ <example>`, got %q", path, i+1, line)
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

// comparatorTestProblem says why a compared_examples.txt entry does not name a
// real comparator test FOR ITS EXAMPLE, or "" when it does: the file is a
// _test.go file in the example's tool's package (cmd/<tool>/), it reaches the
// comparator (onboarding.Compare, or onboarding.Execute which calls it), it
// declares the named test, and that test's own body reads one of docs, the
// docs the example is pasted in.
func comparatorTestProblem(root string, c comparedExample, docs []string) string {
	if !strings.HasSuffix(c.file, "_test.go") {
		return fmt.Sprintf("%s is not a _test.go file", c.file)
	}
	fields := strings.Fields(strings.TrimPrefix(c.ex, "$ "))
	if len(fields) == 0 {
		return fmt.Sprintf("%q names no command", c.ex)
	}
	if dir := "cmd/" + fields[0] + "/"; !strings.HasPrefix(c.file, dir) {
		return fmt.Sprintf("%q runs %s, but %s is not in %s, so %s cannot be the test that executes it", c.ex, fields[0], c.file, dir, c.test)
	}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c.file)))
	if err != nil {
		return fmt.Sprintf("cannot read %s: %v", c.file, err)
	}
	src := string(raw)
	if !strings.Contains(src, "onboarding.Compare(") && !strings.Contains(src, "onboarding.Execute(") {
		return fmt.Sprintf("%s never calls onboarding.Compare or onboarding.Execute, so %s is not a comparator test", c.file, c.test)
	}
	decl := "func " + c.test + "(t *testing.T) {"
	_, body, found := strings.Cut(src, decl)
	if !found {
		return fmt.Sprintf("%s declares no func %s(t *testing.T)", c.file, c.test)
	}
	body, _, _ = strings.Cut(body, "\n}\n")
	for _, doc := range docs {
		if strings.Contains(body, strconv.Quote(path.Base(doc))) {
			return ""
		}
	}
	return fmt.Sprintf("%s's body reads none of %v, the docs %q is pasted in", c.test, docs, c.ex)
}

func mapKeysSorted(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

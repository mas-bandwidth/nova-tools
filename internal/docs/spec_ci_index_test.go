package docs

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// spec_ci_index_test.go holds docs/SPEC-CI.md's "The class tests" section
// against the tests it indexes, in both directions, so the index cannot rot.
//
// The class tests are how a lesson stops being a story: a hurt is read, a rule
// is written, and the rule is a test that reads this repository's own text and
// refuses the shape wherever it stands. Glenn's learning loop says the lessons
// live in the text read at startup and in the tools written — so an index of
// those rules is only worth something if it is TRUE, and a Markdown list nobody
// checks drifts from the tree within a week. The two halves below are what make
// it a contract rather than a note:
//
//	(a) every class test in internal/ci is named by the section, so a new rule
//	    lands with its entry or does not land;
//	(b) every `Test…` name the section prints exists in the tree, so a rename or
//	    a deletion reddens here instead of leaving a paragraph describing a test
//	    that no longer runs.
//
// It reads both sides as text and runs no test: go/parser over cmd/ and
// internal/ for the declarations, one regexp over the section for the names.

// specCIPath is the indexed document, relative to this package.
const specCIPath = "../../docs/SPEC-CI.md"

// classTestSection is the heading the index lives under. The section runs to the
// next top-level heading or to the end of the file, so entries are ### inside it.
const classTestSection = "## The class tests"

// classTestDir is the one package whose class tests the index must cover. The
// existence half reads a wider tree, because an entry may name the two halves of
// a rule where they live (cmd/nova-bus, internal/wake).
const classTestDir = "internal/ci"

// classTestPrefixes is the marker, decided from what the package already does:
// a rule that holds over the whole tree is named for its quantifier. Together
// with the *_class_test.go file suffix this is the set the index must cover.
var classTestPrefixes = []string{"TestNo", "TestEvery", "TestOnly"}

// specTestNameRe reads a `TestSomething` name out of a backtick span. The spec
// writes every test name that way, and nothing else in the section looks like it.
var specTestNameRe = regexp.MustCompile("`(Test[A-Za-z0-9_]+)`")

// TestSpecCIIndexesEveryClassTest is the index's contract.
func TestSpecCIIndexesEveryClassTest(t *testing.T) {
	t.Parallel()

	section := classTestsSection(t)
	named := map[string]bool{}
	for _, m := range specTestNameRe.FindAllStringSubmatch(section, -1) {
		named[m[1]] = true
	}
	if len(named) == 0 {
		t.Fatalf("%s: the %q section names no test; the index is the point of the section", specCIPath, classTestSection)
	}

	declared := declaredTests(t)

	// (a) Every class test is indexed.
	var missing []string
	for name, file := range declared {
		if !isClassTest(name, file) || named[name] {
			continue
		}
		missing = append(missing, name+" ("+file+")")
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("%s: no entry for the class test %s; a class test with no entry is a rule nobody can find — add an entry under %q with the rule, the hurt that produced it, its allowlist, its remedy line and its narrowings",
			specCIPath, m, classTestSection)
	}

	// (b) Every indexed test exists.
	var gone []string
	for name := range named {
		if _, ok := declared[name]; !ok {
			gone = append(gone, name)
		}
	}
	sort.Strings(gone)
	for _, name := range gone {
		t.Errorf("%s names %s, and no such test is declared under cmd/ or internal/; the index describes a test that does not run — rename the entry with the test or delete it",
			specCIPath, name)
	}
}

// classTestsSection returns the text of the index section, from its heading to
// the next top-level heading or the end of the file.
func classTestsSection(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(specCIPath)
	if err != nil {
		t.Fatalf("%s: %v", specCIPath, err)
	}
	content := string(body)
	i := strings.Index(content, "\n"+classTestSection+"\n")
	if i < 0 {
		t.Fatalf("%s carries no %q section; the index of the class tests is part of this specification", specCIPath, classTestSection)
	}
	rest := content[i+1+len(classTestSection):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// isClassTest reports whether a test function is one the index must cover: it is
// declared in a *_class_test.go file under internal/ci, or it carries one of the
// quantifier prefixes there.
func isClassTest(name, file string) bool {
	if !strings.HasPrefix(file, classTestDir+"/") {
		return false
	}
	if strings.HasSuffix(file, "_class_test.go") {
		return true
	}
	for _, p := range classTestPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// declaredTests maps every Test function under cmd/ and internal/ to the
// repo-relative, slash-separated file that declares it.
func declaredTests(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Join("..", "..")
	found := map[string]string{}
	for _, dir := range []string{"cmd", "internal"} {
		base := filepath.Join(root, dir)
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if strings.Contains(rel, "/testdata/") {
				return nil
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Errorf("cannot parse %s: %v", rel, err)
				return nil
			}
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || fn.Name == nil {
					continue
				}
				if strings.HasPrefix(fn.Name.Name, "Test") {
					found[fn.Name.Name] = rel
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}
	if len(found) == 0 {
		t.Fatal("no test functions found under cmd/ or internal/; this test is looking in the wrong place")
	}
	return found
}

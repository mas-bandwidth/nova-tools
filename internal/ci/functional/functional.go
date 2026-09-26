// Package functional is the machine behind cmd/nova-ci's `functional` verb:
// which of a change's packages carry functional tests, and which tests those
// are. A functional test is one in a _test.go file that builds only under the
// `functional` build tag (docs/TESTING.md: unit tests mock; functional tests carry
// the tag and run per stream merge, in ci.yml's functional job). The unit tier
// never builds those files, and the functional tier runs only them: `go test
// -tags functional` alone would run every unit test of the package a second
// time.
//
// It reads Go source with go/build and go/parser and nothing else: no process,
// no network, no clock.
package functional

import (
	"errors"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Tag is the build tag that makes a test file functional.
const Tag = "functional"

// Package is one package directory with functional tests, and their names.
type Package struct {
	Dir   string
	Tests []string
}

// Select returns, in the order given, the package directories that hold at
// least one test file built only under Tag, with the top-level Test functions
// those files declare (TestMain excluded: go test calls it, -run does not
// select it). A directory with no Go files is skipped, not an error: a
// deleted package can still be in a diff's list.
func Select(dirs []string) ([]Package, error) {
	var out []Package
	for _, dir := range dirs {
		plain, err := testFiles(dir, nil)
		if err != nil {
			return nil, err
		}
		if plain == nil {
			continue
		}
		tagged, err := testFiles(dir, []string{Tag})
		if err != nil {
			return nil, err
		}
		var tests []string
		for f := range tagged {
			if plain[f] {
				continue
			}
			names, err := testFuncs(filepath.Join(dir, f))
			if err != nil {
				return nil, err
			}
			tests = append(tests, names...)
		}
		if len(tests) == 0 {
			continue
		}
		sort.Strings(tests)
		out = append(out, Package{Dir: dir, Tests: tests})
	}
	return out, nil
}

// testFiles is the set of _test.go files go/build selects in dir under tags;
// nil (and no error) when dir does not exist or holds no Go package at all.
func testFiles(dir string, tags []string) (map[string]bool, error) {
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	ctx := build.Default
	ctx.BuildTags = tags
	pkg, err := ctx.ImportDir(dir, 0)
	if err != nil {
		if _, ok := err.(*build.NoGoError); ok {
			return nil, nil
		}
		return nil, err
	}
	files := map[string]bool{}
	for _, f := range append(append([]string{}, pkg.TestGoFiles...), pkg.XTestGoFiles...) {
		files[f] = true
	}
	return files, nil
}

// testFuncs lists the top-level func TestXxx declarations in one file.
func testFuncs(path string) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, decl := range f.Decls {
		name, ok := topLevelTest(decl)
		if ok {
			names = append(names, name)
		}
	}
	return names, nil
}

// RunPattern is one `go test -run` pattern naming exactly the given tests:
// ^(A|B)$, each name quoted, so a test named like a prefix of another never
// drags the longer one in.
func RunPattern(pkgs []Package) string {
	seen := map[string]bool{}
	var names []string
	for _, p := range pkgs {
		for _, t := range p.Tests {
			if !seen[t] {
				seen[t] = true
				names = append(names, regexp.QuoteMeta(t))
			}
		}
	}
	sort.Strings(names)
	return "^(" + strings.Join(names, "|") + ")$"
}

// Expand turns `dir/...` patterns into every directory under dir, the way go
// list reads them (testdata, vendor and names starting with . or _ are not
// packages); any other argument is kept as given.
func Expand(args []string) ([]string, error) {
	var out []string
	for _, arg := range args {
		root, ok := strings.CutSuffix(arg, "/...")
		if !ok {
			out = append(out, arg)
			continue
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				return nil
			}
			name := d.Name()
			if path != root && (name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			if strings.HasPrefix(root, "./") && !strings.HasPrefix(path, "./") {
				path = "./" + path
			}
			out = append(out, path)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

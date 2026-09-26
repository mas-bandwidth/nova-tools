package ci

import (
	"go/build/constraint"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// functionaltag_class_test.go is Glenn's ruling of 2026-09-26 11:20 AM ET
// (nova-tools #4328): "unit tests be < 2s (ideally <1) but also they must not
// be so aggressive that they fill a whole machine cores ... we should run
// functional tests, not on every small PR being merged or worked on, but only
// as we merge whole work streams".
//
// A test that starts a redis-server is a functional test. Every _test.go that
// calls one of the helpers that exec redis-server carries `//go:build
// functional`, so a pull request's `go test` does not build it; the merge
// group, a push to dev, `make check` and nightly-slow.yml's functional leg
// pass the tag.
//
// The check is on the direct call. A file that reaches redis through a
// package-local helper (`store(t)` wrapping testutil.Start) needs no rule of
// its own: the helper's file is tagged by this test, so an untagged caller
// does not compile, and `go vet ./...` in the lint job is red on it.

// redisHelpers are the functions that start a redis-server, by import path.
var redisHelpers = map[string][]string{
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil":  {"Start", "Program"},
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest": {"Start"},
}

// startsRedis reports whether src calls one of redisHelpers through its import.
func startsRedis(src []byte) bool {
	f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.ImportsOnly)
	if err != nil {
		return false
	}
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		funcs, ok := redisHelpers[path]
		if !ok {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		for _, fn := range funcs {
			if strings.Contains(string(src), name+"."+fn+"(") {
				return true
			}
		}
	}
	return false
}

// needsFunctional reports whether the file's build constraint keeps it out of
// every build that does not pass `-tags functional`: the expression is false
// with every other tag set.
func needsFunctional(src []byte) bool {
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			break
		}
		if !constraint.IsGoBuild(line) {
			continue
		}
		expr, err := constraint.Parse(line)
		if err != nil {
			return false
		}
		return !expr.Eval(func(tag string) bool { return tag != "functional" })
	}
	return false
}

func TestNeedsFunctionalReadsTheConstraint(t *testing.T) {
	t.Parallel()

	for src, want := range map[string]bool{
		"//go:build functional\n\npackage x\n":                 true,
		"//go:build unix && slow && functional\n\npackage x\n": true,
		"//go:build unix\n\npackage x\n":                       false,
		"//go:build slow\n\npackage x\n":                       false,
		"//go:build functional || slow\n\npackage x\n":         false,
		"package x\n\n//go:build functional\n":                 false,
		"package x\n":                                          false,
	} {
		if got := needsFunctional([]byte(src)); got != want {
			t.Errorf("needsFunctional(%q) = %v, want %v", src, got, want)
		}
	}
}

func TestStartsRedisSeesTheHelpersThroughTheirImport(t *testing.T) {
	t.Parallel()

	for src, want := range map[string]bool{
		"package x\nimport \"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil\"\nvar a = testutil.Start(nil)\n":           true,
		"package x\nimport tu \"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil\"\nvar a = tu.Program(nil)\n":            true,
		"package x\nimport \"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest\"\nvar a, b = wstest.Start(nil)\n":         true,
		"package x\nimport \"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil\"\nvar a = testutil.StartGitHubStub(nil)\n": false,
		"package x\nimport \"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil\"\nvar a = testutil.FreePort(nil)\n":        false,
		"package x\n// testutil.Start(t) in prose, no import\n":                                                                        false,
	} {
		if got := startsRedis([]byte(src)); got != want {
			t.Errorf("startsRedis(%q) = %v, want %v", src, got, want)
		}
	}
}

// THE CLASS TEST. No untagged test file starts a redis-server.
func TestRedisBackedTestsCarryTheFunctionalTag(t *testing.T) {
	t.Parallel()

	var bad []string
	seen := 0
	for _, f := range repoTree(t).Files {
		if !f.Test || f.HasDirNamed("testdata") || f.HasDirNamed("vendor") {
			continue
		}
		if !startsRedis(f.Src) {
			continue
		}
		seen++
		if !needsFunctional(f.Src) {
			bad = append(bad, f.Rel)
		}
	}
	if seen == 0 {
		t.Fatal("no _test.go calls testutil.Start, testutil.Program or wstest.Start; the walk is broken, not the tree")
	}
	sort.Strings(bad)
	for _, rel := range bad {
		t.Errorf("%s starts a redis-server but builds without `-tags functional`: put `//go:build functional` "+
			"(joined with && to any constraint it has) on its first line, or move its redis-backed tests to "+
			"<name>_functional_test.go and keep the pure ones here (nova-tools #4328)", rel)
	}
}

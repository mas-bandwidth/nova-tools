package docs

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// spec_ci_verbs_test.go holds docs/SPEC-CI.md's verb claims against
// cmd/nova-ci's dispatch. The spec's long sections once introduced the class
// tests with a help block in the nova-ci help grammar — `waits`, `testbins`,
// `templates` — presenting them as verbs of nova-ci, and nova-ci dispatches no
// such verb: they are class tests, run as `go test ./internal/ci -run <name>`.
// A spec sentence that names a verb of a real binary is a contract a reader
// can paste, so the verb list the spec gives must match the tool's own list,
// read from main.go and never copied here.

// novaCIMainPath is cmd/nova-ci's entry file, relative to this package.
const novaCIMainPath = "../../cmd/nova-ci/main.go"

// specVerbClaimRe reads a verb the spec presents as nova-ci's: the phrase
// "as the verb `waits`:" introduces a help block the tool does not carry.
var specVerbClaimRe = regexp.MustCompile("as the verb `([a-z][a-z0-9-]+)`")

// specHelpBlockRe reads the leading verb of a fenced help-grammar block: the
// block's first line is `<verb>  <description>`, the shape `nova-ci help`
// prints. An indented continuation line starts with spaces and is not read.
var specHelpBlockRe = regexp.MustCompile("(?m)^```\\n([a-z][a-z0-9-]*) {2,}\\S")

// novaCIVerbs returns the verbs nova-ci dispatches, read from the `verbs`
// constant of cmd/nova-ci/main.go so the list is the tool's own, never a copy
// that can drift from the dispatch.
func novaCIVerbs(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, novaCIMainPath, nil, 0)
	require.NoError(t, err, "%s: %v", novaCIMainPath, err)
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			val, ok := spec.(*ast.ValueSpec)
			if !ok || len(val.Names) != 1 || val.Names[0].Name != "verbs" || len(val.Values) != 1 {
				continue
			}
			lit, ok := val.Values[0].(*ast.BasicLit)
			require.True(t, ok, "%s: the verbs constant is not a string literal", novaCIMainPath)
			s, err := strconv.Unquote(lit.Value)
			require.NoError(t, err, "%s: the verbs constant: %v", novaCIMainPath, err)
			verbs := map[string]bool{}
			for _, name := range strings.Split(s, ", ") {
				verbs[name] = true
			}
			return verbs
		}
	}
	require.FailNow(t, "%s declares no verbs constant; the tool's verb list has moved", novaCIMainPath)
	return nil
}

// TestSpecCIVerbsAreTheToolsVerbs is the contract: every verb docs/SPEC-CI.md
// gives as nova-ci's — by an "as the verb" phrase or a fenced help block — is
// a verb cmd/nova-ci dispatches. A class test is a go test, not a verb: a
// section that wants one names the test and the package that runs it.
func TestSpecCIVerbsAreTheToolsVerbs(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(specCIPath)
	require.NoError(t, err, "%s: %v", specCIPath, err)
	spec := string(raw)

	claimed := map[string]bool{}
	for _, m := range specVerbClaimRe.FindAllStringSubmatch(spec, -1) {
		claimed[m[1]] = true
	}
	for _, m := range specHelpBlockRe.FindAllStringSubmatch(spec, -1) {
		claimed[m[1]] = true
	}
	require.NotEmpty(t, claimed, "%s gives no nova-ci verb at all; the spec no longer names the tool it governs", specCIPath)

	verbs := novaCIVerbs(t)
	var bad, list []string
	for name := range claimed {
		if !verbs[name] {
			bad = append(bad, name)
		}
	}
	for name := range verbs {
		list = append(list, name)
	}
	sort.Strings(bad)
	sort.Strings(list)
	for _, name := range bad {
		t.Errorf("%s presents %q as a verb of nova-ci, and cmd/nova-ci dispatches no such verb (its verbs are %s); the class tests are go tests, not verbs — drop the help grammar and name the test and the package that runs it",
			specCIPath, name, strings.Join(list, ", "))
	}
}

package ci

import (
	"go/parser"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ci_templates_test.go is the red-test contract of the templates checker in
// docs/SPEC-CI.md, "The CI class test against unquoted paths in JSON and
// template literals". Each fixture below is a real _test.go written into a
// throwaway tree and handed to CheckTemplates, so the checker is exercised on
// a tree given on the command line and never by reaching into the repository.
// The fixtures live in testdata/ so the class test that walks the repo never
// reads the offenders it is meant to find.

// fixtureTree writes one fixture under <tmp>/internal/fixture/fixture_test.go
// and returns the tree root, the way a caller hands the checker a --dir.
func fixtureTree(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", "templates", name))
	require.NoError(t, err)
	dir := filepath.Join(root, "internal", "fixture")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fixture_test.go"), src, 0o644))
	return root
}

// emptyTree is a root with no _test.go at all, so the allowlist rule can be
// tested without a real offender standing behind the entry.
func emptyTree(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// lineAt reads the line the finding named and returns it trimmed, so a test
// asserts on the offender itself rather than on a line number the fixture's
// comments can move.
func lineAt(t *testing.T, root, rel string, line int) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	require.NoError(t, err)
	lines := strings.Split(string(raw), "\n")
	require.False(t, line < 1 || line > len(lines), "%s:%d is outside the file (%d lines)", rel, line, len(lines))
	return strings.TrimSpace(lines[line-1])
}

// 1. A test carrying filepath.Join raw into a JSON literal is refused with its
// file and line, and the remedy names the quote.
func TestTemplatesRefusesRawFilepathJoin(t *testing.T) {
	t.Parallel()

	root := fixtureTree(t, "join.go.txt")
	res, err := CheckTemplates(root, "")
	require.NoError(t, err)
	require.False(t, res.Refused() != 1 || len(res.Findings) != 1, "a raw filepath.Join in a JSON literal is one refusal, got %d: %+v", res.Refused(), res.Findings)
	f := res.Findings[0]
	assert.Equal(t, "join", f.Kind, "kind = %q, want join", f.Kind)
	assert.Equal(t, TemplateRemedyJoin, f.Remedy, "remedy = %q, want %q", f.Remedy, TemplateRemedyJoin)
	got := lineAt(t, root, f.File, f.Line)
	assert.Contains(t, got, "filepath.Join", "finding names %s:%d = %q, want the filepath.Join line", f.File, f.Line, got)
}

// 2. A C:\ path placed unquoted inside a JSON literal is refused, with the
// worker description faked rather than read from disk.
func TestTemplatesRefusesUnquotedOSPathLiteral(t *testing.T) {
	t.Parallel()

	root := fixtureTree(t, "literal.go.txt")
	res, err := CheckTemplates(root, "")
	require.NoError(t, err)
	require.False(t, res.Refused() != 1 || len(res.Findings) != 1, "an unquoted C:\\ path in a JSON literal is one refusal, got %d: %+v", res.Refused(), res.Findings)
	f := res.Findings[0]
	assert.Equal(t, "literal", f.Kind, "kind = %q, want literal", f.Kind)
	assert.Equal(t, TemplateRemedyLiteral, f.Remedy, "remedy = %q, want %q", f.Remedy, TemplateRemedyLiteral)
	got := lineAt(t, root, f.File, f.Line)
	assert.Contains(t, got, `C:\Users\RUNN\keys\key`, "finding names %s:%d = %q, want the unquoted path line", f.File, f.Line, got)
}

// 3. A path unquoted inside a text/template string is refused, with the
// template rendered into a fake writer and no subprocess started.
func TestTemplatesRefusesUnquotedPathInTemplate(t *testing.T) {
	t.Parallel()

	root := fixtureTree(t, "template.go.txt")
	res, err := CheckTemplates(root, "")
	require.NoError(t, err)
	require.False(t, res.Refused() != 1 || len(res.Findings) != 1, "an unquoted path in a template literal is one refusal, got %d: %+v", res.Refused(), res.Findings)
	f := res.Findings[0]
	assert.Equal(t, "literal", f.Kind, "kind = %q, want literal", f.Kind)
	assert.Equal(t, TemplateRemedyLiteral, f.Remedy, "remedy = %q, want %q", f.Remedy, TemplateRemedyLiteral)
	got := lineAt(t, root, f.File, f.Line)
	assert.Contains(t, got, `C:\Users\RUNN\keys\key`, "finding names %s:%d = %q, want the template path line", f.File, f.Line, got)
}

// 4. A path wrapped in strconv.Quote is allowed, with the bench it guards
// faked.
func TestTemplatesAllowsStrconvQuote(t *testing.T) {
	t.Parallel()

	root := fixtureTree(t, "quoted.go.txt")
	res, err := CheckTemplates(root, "")
	require.NoError(t, err)
	require.False(t, res.Refused() != 0 || len(res.Findings) != 0, "strconv.Quote is the allowed shape, got %d refusals: %+v", res.Refused(), res.Findings)
	assert.Equal(t, 1, res.Tests, "tests = %d, want 1", res.Tests)
}

// 5. A path wrapped in oneline.Quote is allowed, with the network it reports
// on faked.
func TestTemplatesAllowsOnelineQuote(t *testing.T) {
	t.Parallel()

	root := fixtureTree(t, "oneline_quoted.go.txt")
	res, err := CheckTemplates(root, "")
	require.NoError(t, err)
	require.False(t, res.Refused() != 0 || len(res.Findings) != 0, "oneline.Quote is the allowed shape, got %d refusals: %+v", res.Refused(), res.Findings)
}

// 6. Adding an entry to the allowlist is refused; removing one is allowed. An
// entry that names no offender on the tree is a place to park a path, and the
// remedy says the file only shrinks.
func TestTemplatesAllowlistGrowsRefused(t *testing.T) {
	t.Parallel()

	root := emptyTree(t)
	allow := filepath.Join(t.TempDir(), "template-paths-allowlist.txt")

	require.NoError(t, os.WriteFile(allow, []byte(""), 0o644))
	res, err := CheckTemplates(root, allow)
	require.NoError(t, err)
	require.Zero(t, res.Refused(), "an empty allowlist over a clean tree must pass, got %d refusals", res.Refused())

	require.NoError(t, os.WriteFile(allow, []byte("internal/x/x_test.go:1 join 2026-09-17 parked here\n"), 0o644))
	res, err = CheckTemplates(root, allow)
	require.NoError(t, err)
	require.False(t, res.Refused() != 1 || len(res.Stale) != 1, "adding an allowlist entry that names no offender must be refused, got %d refusals %+v", res.Refused(), res.Stale)
	assert.Equal(t, TemplateRemedyAllow, res.Stale[0].Remedy, "allowlist remedy = %q, want %q", res.Stale[0].Remedy, TemplateRemedyAllow)

	require.NoError(t, os.WriteFile(allow, []byte(""), 0o644))
	res, err = CheckTemplates(root, allow)
	require.NoError(t, err)
	require.Zero(t, res.Refused(), "removing the entry must be allowed, got %d refusals", res.Refused())
}

// TestTemplatesOutputMatchesTheSpec pins the one-line grammar of the section:
// the OK line, the refusal line and the closing FAIL line, and the exit 2 a
// refusal costs.
func TestTemplatesOutputMatchesTheSpec(t *testing.T) {
	t.Parallel()

	root := fixtureTree(t, "join.go.txt")
	res, err := CheckTemplates(root, "")
	require.NoError(t, err)
	assert.Equal(t, "CI-TEMPLATES OK tests=1 allowlisted=0 refused=0", res.OKLine(), "clean line = %q, want %q", res.OKLine(), "CI-TEMPLATES OK tests=1 allowlisted=0 refused=0")
	assert.Equal(t, "CI-TEMPLATES FAIL tests=1 allowlisted=0 refused=1", res.FailLine(), "fail line = %q, want %q", res.FailLine(), "CI-TEMPLATES FAIL tests=1 allowlisted=0 refused=1")
	assert.Equal(t, 2, res.ExitCode(), "exit = %d, want 2", res.ExitCode())
	require.Len(t, res.Findings, 1, "want one refusal, got %d", len(res.Findings))
	line := res.Findings[0].Render()
	for _, want := range []string{
		"CI-TEMPLATES file=internal/fixture/fixture_test.go line=",
		` kind=join remedy="wrap the path in strconv.Quote"`,
	} {
		assert.Contains(t, line, want, "refusal %q lacks %q", line, want)
	}
}

// TestTemplatesVerbLineMatchesTheSpec pins the help line the class test is
// entered under, word for word, to the section that prints it.
func TestTemplatesVerbLineMatchesTheSpec(t *testing.T) {
	t.Parallel()

	spec := readFile(t, filepath.Join(repoRoot(t), "docs", "SPEC-CI.md"))
	assert.Contains(t, spec, TemplatesVerbLine, "the templates verb line is not in docs/SPEC-CI.md:\n%s", TemplatesVerbLine)
}

// TestNoUnquotedPathsInTemplateLiterals is the class test itself: every
// _test.go under internal/ and cmd/ is read, and the only unquoted paths that
// pass are the ones the allowlist already names. The count is the truth about
// the tree whether or not the lines printed.
func TestNoUnquotedPathsInTemplateLiterals(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	allow := filepath.Join(root, "internal", "ci", "testdata", "template-paths-allowlist.txt")
	res, err := CheckTemplates(root, allow)
	require.NoError(t, err)
	t.Log(res.OKLine())
	for _, f := range res.Findings {
		t.Error(f.Render())
	}
	// The stale rows come from the one helper, which under NOVA_CI_UPDATE=1
	// drops them from the file instead (nova-tools#4339).
	list := loadAllowlist(t, allow, FileLineListOptions)
	for _, row := range allowlist.Check(t, list, res.Measured).Stale {
		t.Errorf("%s:%d: %q names no offender on the tree; %s", allow, row.Line, row.Text, TemplateRemedyAllow)
	}
}

// flattenAdds splits a chain of additions into its operands, and only additions:
// another operator's operands stay together as one.
func TestFlattenAddsSplitsOnlyAdditions(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		src  string
		want int
	}{
		{"a", 1},
		{"a + b", 2},
		{"a + b + c", 3},
		{"a - b", 1},
		{"a * b", 1},
		{"a + b*c", 2},
		{"a - b + c", 2},
	} {
		t.Run(c.src, func(t *testing.T) {
			e, err := parser.ParseExpr(c.src)
			require.NoError(t, err)
			assert.Len(t, flattenAdds(e), c.want, "flattenAdds(%q) has %d operands, want %d", c.src, len(flattenAdds(e)), c.want)
		})
	}
}

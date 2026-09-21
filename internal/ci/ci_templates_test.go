package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "internal", "fixture")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture_test.go"), src, 0o644); err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	if line < 1 || line > len(lines) {
		t.Fatalf("%s:%d is outside the file (%d lines)", rel, line, len(lines))
	}
	return strings.TrimSpace(lines[line-1])
}

// 1. A test carrying filepath.Join raw into a JSON literal is refused with its
// file and line, and the remedy names the quote.
func TestTemplatesRefusesRawFilepathJoin(t *testing.T) {
	root := fixtureTree(t, "join.go.txt")
	res, err := CheckTemplates(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 1 || len(res.Findings) != 1 {
		t.Fatalf("a raw filepath.Join in a JSON literal is one refusal, got %d: %+v", res.Refused(), res.Findings)
	}
	f := res.Findings[0]
	if f.Kind != "join" {
		t.Errorf("kind = %q, want join", f.Kind)
	}
	if f.Remedy != TemplateRemedyJoin {
		t.Errorf("remedy = %q, want %q", f.Remedy, TemplateRemedyJoin)
	}
	if got := lineAt(t, root, f.File, f.Line); !strings.Contains(got, "filepath.Join") {
		t.Errorf("finding names %s:%d = %q, want the filepath.Join line", f.File, f.Line, got)
	}
}

// 2. A C:\ path placed unquoted inside a JSON literal is refused, with the
// worker description faked rather than read from disk.
func TestTemplatesRefusesUnquotedOSPathLiteral(t *testing.T) {
	root := fixtureTree(t, "literal.go.txt")
	res, err := CheckTemplates(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 1 || len(res.Findings) != 1 {
		t.Fatalf("an unquoted C:\\ path in a JSON literal is one refusal, got %d: %+v", res.Refused(), res.Findings)
	}
	f := res.Findings[0]
	if f.Kind != "literal" {
		t.Errorf("kind = %q, want literal", f.Kind)
	}
	if f.Remedy != TemplateRemedyLiteral {
		t.Errorf("remedy = %q, want %q", f.Remedy, TemplateRemedyLiteral)
	}
	if got := lineAt(t, root, f.File, f.Line); !strings.Contains(got, `C:\Users\RUNN\keys\key`) {
		t.Errorf("finding names %s:%d = %q, want the unquoted path line", f.File, f.Line, got)
	}
}

// 3. A path unquoted inside a text/template string is refused, with the
// template rendered into a fake writer and no subprocess started.
func TestTemplatesRefusesUnquotedPathInTemplate(t *testing.T) {
	root := fixtureTree(t, "template.go.txt")
	res, err := CheckTemplates(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 1 || len(res.Findings) != 1 {
		t.Fatalf("an unquoted path in a template literal is one refusal, got %d: %+v", res.Refused(), res.Findings)
	}
	f := res.Findings[0]
	if f.Kind != "literal" {
		t.Errorf("kind = %q, want literal", f.Kind)
	}
	if f.Remedy != TemplateRemedyLiteral {
		t.Errorf("remedy = %q, want %q", f.Remedy, TemplateRemedyLiteral)
	}
	if got := lineAt(t, root, f.File, f.Line); !strings.Contains(got, `C:\Users\RUNN\keys\key`) {
		t.Errorf("finding names %s:%d = %q, want the template path line", f.File, f.Line, got)
	}
}

// 4. A path wrapped in strconv.Quote is allowed, with the bench it guards
// faked.
func TestTemplatesAllowsStrconvQuote(t *testing.T) {
	root := fixtureTree(t, "quoted.go.txt")
	res, err := CheckTemplates(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 || len(res.Findings) != 0 {
		t.Fatalf("strconv.Quote is the allowed shape, got %d refusals: %+v", res.Refused(), res.Findings)
	}
	if res.Tests != 1 {
		t.Errorf("tests = %d, want 1", res.Tests)
	}
}

// 5. A path wrapped in oneline.Quote is allowed, with the network it reports
// on faked.
func TestTemplatesAllowsOnelineQuote(t *testing.T) {
	root := fixtureTree(t, "oneline_quoted.go.txt")
	res, err := CheckTemplates(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 || len(res.Findings) != 0 {
		t.Fatalf("oneline.Quote is the allowed shape, got %d refusals: %+v", res.Refused(), res.Findings)
	}
}

// 6. Adding an entry to the allowlist is refused; removing one is allowed. An
// entry that names no offender on the tree is a place to park a path, and the
// remedy says the file only shrinks.
func TestTemplatesAllowlistGrowsRefused(t *testing.T) {
	root := emptyTree(t)
	allow := filepath.Join(t.TempDir(), "template-paths-allowlist.txt")

	if err := os.WriteFile(allow, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := CheckTemplates(root, allow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 {
		t.Fatalf("an empty allowlist over a clean tree must pass, got %d refusals", res.Refused())
	}

	if err := os.WriteFile(allow, []byte("internal/x/x_test.go:1 join 2026-09-17 parked here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = CheckTemplates(root, allow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 1 || len(res.Stale) != 1 {
		t.Fatalf("adding an allowlist entry that names no offender must be refused, got %d refusals %+v", res.Refused(), res.Stale)
	}
	if res.Stale[0].Remedy != TemplateRemedyAllow {
		t.Errorf("allowlist remedy = %q, want %q", res.Stale[0].Remedy, TemplateRemedyAllow)
	}

	if err := os.WriteFile(allow, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = CheckTemplates(root, allow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 {
		t.Fatalf("removing the entry must be allowed, got %d refusals", res.Refused())
	}
}

// TestTemplatesOutputMatchesTheSpec pins the one-line grammar of the section:
// the OK line, the refusal line and the closing FAIL line, and the exit 2 a
// refusal costs.
func TestTemplatesOutputMatchesTheSpec(t *testing.T) {
	root := fixtureTree(t, "join.go.txt")
	res, err := CheckTemplates(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := res.OKLine(), "CI-TEMPLATES OK tests=1 allowlisted=0 refused=0"; got != want {
		t.Errorf("clean line = %q, want %q", got, want)
	}
	if got, want := res.FailLine(), "CI-TEMPLATES FAIL tests=1 allowlisted=0 refused=1"; got != want {
		t.Errorf("fail line = %q, want %q", got, want)
	}
	if res.ExitCode() != 2 {
		t.Errorf("exit = %d, want 2", res.ExitCode())
	}
	if len(res.Findings) != 1 {
		t.Fatalf("want one refusal, got %d", len(res.Findings))
	}
	line := res.Findings[0].Render()
	for _, want := range []string{
		"CI-TEMPLATES file=internal/fixture/fixture_test.go line=",
		` kind=join remedy="wrap the path in strconv.Quote"`,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("refusal %q lacks %q", line, want)
		}
	}
}

// TestTemplatesVerbLineMatchesTheSpec pins the help line the class test is
// entered under, word for word, to the section that prints it.
func TestTemplatesVerbLineMatchesTheSpec(t *testing.T) {
	spec := readFile(t, filepath.Join(repoRoot(t), "docs", "SPEC-CI.md"))
	if !strings.Contains(spec, TemplatesVerbLine) {
		t.Errorf("the templates verb line is not in docs/SPEC-CI.md:\n%s", TemplatesVerbLine)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	t.Log(res.OKLine())
	for _, f := range res.Findings {
		t.Error(f.Render())
	}
	for _, f := range res.Stale {
		t.Error(f.Render())
	}
}

package ci

import (
	"fmt"
	"go/parser"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ci_templates_test.go holds the one harness the four allowlisted source-scan
// rules -- templates, testbins, waits and net -- run their tests from, together
// with the templates rule's own cases. Each rule is one ciRule row: its name,
// its checker, the allowlist base name the class test reads and the testdata
// directory its fixtures live under. The shapes the four rules share -- a
// refusal, an allowed shape, the class test itself, the
// allowlist-grows-refused and the allowlist-survives-shifted-lines rules, the
// output grammar and the verb line -- are written once below; each rule's own
// cases are data on its row, and every old top-level test name stays a
// top-level test that calls the shared helper, so `go test -run` and the docs
// that name it still find it. Each fixture is a real _test.go written into a
// throwaway tree and handed to the checker, so a rule is exercised on a tree
// given on the command line and never by reaching into the repository; the
// class test that walks the repo never reads the offenders it is meant to find.

// ciFinding is one finding of any of the four rules, normalized so the shared
// cases read it the same way. host is empty for every rule but net.
type ciFinding struct {
	file, kind, remedy, host string
	line                     int
	render                   func() string
}

// ciRun is one run of any of the four checkers, normalized. refused is the
// checker's own count (offenders plus stale allowlist entries).
type ciRun struct {
	tests, allowlisted, refused int
	findings, stale             []ciFinding
	okLine, failLine            string
	exit                        int
	measured                    map[string]bool
}

// ciRefusal is one refusal case: the fixture to hand the checker, the kind and
// remedy it must name, the host (net only) and the substring of the offending
// line. An empty field pins nothing.
type ciRefusal struct {
	name     string
	fixture  string
	kind     string
	remedy   string
	host     string
	contains string
}

// ciAllow is one allowed-shape case. tests is the count the run must report,
// or -1 when the case pins no count.
type ciAllow struct {
	name    string
	fixture string
	tests   int
}

// ciRule is one of the four rules under test.
type ciRule struct {
	key            string
	prefix         string
	allowlist      string
	fixtures       string
	allowKind      string
	allowRemedy    string
	verbLine       string
	check          func(root, allow string) (ciRun, error)
	refusals       []ciRefusal
	allows         []ciAllow
	outputFixture  string
	wants          []string
	secondOffender func(rel, host string) string
}

func testbinFindings(in []TestbinFinding) []ciFinding {
	out := make([]ciFinding, 0, len(in))
	for _, f := range in {
		out = append(out, ciFinding{file: f.File, line: f.Line, kind: f.Kind, remedy: f.Remedy, render: f.Render})
	}
	return out
}

func waitFindings(in []WaitFinding) []ciFinding {
	out := make([]ciFinding, 0, len(in))
	for _, f := range in {
		out = append(out, ciFinding{file: f.File, line: f.Line, kind: f.Kind, remedy: f.Remedy, render: f.Render})
	}
	return out
}

func templateFindings(in []TemplateFinding) []ciFinding {
	out := make([]ciFinding, 0, len(in))
	for _, f := range in {
		out = append(out, ciFinding{file: f.File, line: f.Line, kind: f.Kind, remedy: f.Remedy, render: f.Render})
	}
	return out
}

func netFindings(in []NetFinding) []ciFinding {
	out := make([]ciFinding, 0, len(in))
	for _, f := range in {
		out = append(out, ciFinding{file: f.File, line: f.Line, kind: f.Kind, host: f.Host, remedy: f.Remedy, render: f.Render})
	}
	return out
}

func runTestbins(root, allow string) (ciRun, error) {
	r, err := CheckTestbins(root, allow)
	if err != nil {
		return ciRun{}, err
	}
	return ciRun{tests: r.Tests, allowlisted: r.Allowlisted, refused: r.Refused(), findings: testbinFindings(r.Findings), stale: testbinFindings(r.Stale), okLine: r.OKLine(), failLine: r.FailLine(), exit: r.ExitCode(), measured: r.Measured}, nil
}

func runWaits(root, allow string) (ciRun, error) {
	r, err := CheckWaits(root, allow)
	if err != nil {
		return ciRun{}, err
	}
	return ciRun{tests: r.Tests, allowlisted: r.Allowlisted, refused: r.Refused(), findings: waitFindings(r.Findings), stale: waitFindings(r.Stale), okLine: r.OKLine(), failLine: r.FailLine(), exit: r.ExitCode(), measured: r.Measured}, nil
}

func runTemplates(root, allow string) (ciRun, error) {
	r, err := CheckTemplates(root, allow)
	if err != nil {
		return ciRun{}, err
	}
	return ciRun{tests: r.Tests, allowlisted: r.Allowlisted, refused: r.Refused(), findings: templateFindings(r.Findings), stale: templateFindings(r.Stale), okLine: r.OKLine(), failLine: r.FailLine(), exit: r.ExitCode(), measured: r.Measured}, nil
}

func runNet(root, allow string) (ciRun, error) {
	r, err := CheckNet(root, allow)
	if err != nil {
		return ciRun{}, err
	}
	return ciRun{tests: r.Tests, allowlisted: r.Allowlisted, refused: r.Refused(), findings: netFindings(r.Findings), stale: netFindings(r.Stale), okLine: r.OKLine(), failLine: r.FailLine(), exit: r.ExitCode(), measured: r.Measured}, nil
}

// ruleFixtureTree writes one fixture under <tmp>/internal/fixture/fixture_test.go
// and returns the tree root, the way a caller hands the checker a --dir.
func ruleFixtureTree(t *testing.T, rule ciRule, name string) string {
	t.Helper()
	root := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", rule.fixtures, name))
	require.NoError(t, err)
	dir := filepath.Join(root, "internal", "fixture")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fixture_test.go"), src, 0o644))
	return root
}

// lineAt reads the line the finding named and returns it trimmed, so a case
// asserts on the offender itself rather than on a line number the fixture's
// comments can move.
func lineAt(t *testing.T, root, rel string, line int) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	require.NoError(t, err)
	lines := strings.Split(string(raw), "\n")
	require.GreaterOrEqual(t, line, 1, "%s:%d is outside the file (%d lines)", rel, line, len(lines))
	require.False(t, line > len(lines), "%s:%d is outside the file (%d lines)", rel, line, len(lines))
	return strings.TrimSpace(lines[line-1])
}

// ciNamedRefusal runs the refusal case of rule named name.
func ciNamedRefusal(t *testing.T, rule ciRule, name string) {
	t.Helper()
	for _, c := range rule.refusals {
		if c.name == name {
			root := ruleFixtureTree(t, rule, c.fixture)
			res, err := rule.check(root, "")
			require.NoError(t, err)
			require.Equal(t, 1, res.refused, "%s: refusals = %d, want 1: %+v", name, res.refused, res.findings)
			require.Len(t, res.findings, 1, "%s: findings = %+v, want 1", name, res.findings)
			f := res.findings[0]
			if c.kind != "" {
				assert.Equal(t, c.kind, f.kind, "%s: kind = %q, want %q", name, f.kind, c.kind)
			}
			if c.remedy != "" {
				assert.Equal(t, c.remedy, f.remedy, "%s: remedy = %q, want %q", name, f.remedy, c.remedy)
			}
			if c.host != "" {
				assert.Equal(t, c.host, f.host, "%s: host = %q, want %q", name, f.host, c.host)
			}
			if c.contains != "" {
				got := lineAt(t, root, f.file, f.line)
				assert.Contains(t, got, c.contains, "%s: finding names %s:%d = %q, want it to carry %q", name, f.file, f.line, got, c.contains)
			}
			return
		}
	}
	require.FailNowf(t, "no refusal case named", "%s: no refusal case named %q", rule.key, name)
}

// ciNamedAllow runs the allowed-shape case of rule named name.
func ciNamedAllow(t *testing.T, rule ciRule, name string) {
	t.Helper()
	for _, c := range rule.allows {
		if c.name == name {
			root := ruleFixtureTree(t, rule, c.fixture)
			res, err := rule.check(root, "")
			require.NoError(t, err)
			require.Zero(t, res.refused, "%s: refusals = %d, want 0: %+v", name, res.refused, res.findings)
			require.Empty(t, res.findings, "%s: findings = %+v, want none", name, res.findings)
			if c.tests >= 0 {
				assert.Equal(t, c.tests, res.tests, "%s: tests = %d, want %d", name, res.tests, c.tests)
			}
			return
		}
	}
	require.FailNowf(t, "no allow case named", "%s: no allow case named %q", rule.key, name)
}

// ciClassTest is the class test itself for one rule: every _test.go under
// internal/, pkg/ and cmd/ is read, and the only offenders that pass are the
// ones the rule's allowlist already names. The count is the truth about the CI
// path whether or not the lines printed. allow is passed by the caller so the
// allowlist guard reads its path out of a call site.
func ciClassTest(t *testing.T, rule ciRule, allow string) {
	t.Helper()
	root := repoRoot(t)
	res, err := rule.check(root, allow)
	require.NoError(t, err)
	t.Log(res.okLine)
	for _, f := range res.findings {
		assert.Fail(t, f.render())
	}
	// The stale rows come from the one helper, which under NOVA_CI_UPDATE=1
	// drops them from the file instead (nova-tools#4339).
	list := loadAllowlist(t, allow, FileLineListOptions)
	assert.Empty(t, allowlist.Check(t, list, res.measured).Stale, allow+" names no offender on the tree; "+rule.allowRemedy)
}

// ciAllowlistGrowsRefused: an empty allowlist over a clean tree passes, a row
// that names no offender is refused with the rule's shrinking remedy, and
// removing the row is allowed.
func ciAllowlistGrowsRefused(t *testing.T, rule ciRule) {
	t.Helper()
	root := t.TempDir()
	allow := filepath.Join(t.TempDir(), "allowlist.txt")

	require.NoError(t, os.WriteFile(allow, []byte(""), 0o644))
	res, err := rule.check(root, allow)
	require.NoError(t, err)
	require.Zero(t, res.refused, "an empty allowlist over a clean tree must pass, got %d refusals", res.refused)

	row := fmt.Sprintf("internal/x/x_test.go:1 %s 2026-09-17 parked here\n", rule.allowKind)
	require.NoError(t, os.WriteFile(allow, []byte(row), 0o644))
	res, err = rule.check(root, allow)
	require.NoError(t, err)
	require.Equal(t, 1, res.refused, "adding an entry that names no offender must be refused, got %d refusals %+v", res.refused, res.stale)
	require.Len(t, res.stale, 1, "adding an entry that names no offender must be refused, got %d stale %+v", len(res.stale), res.stale)
	assert.Equal(t, rule.allowRemedy, res.stale[0].remedy, "allowlist remedy = %q, want %q", res.stale[0].remedy, rule.allowRemedy)

	require.NoError(t, os.WriteFile(allow, []byte(""), 0o644))
	res, err = rule.check(root, allow)
	require.NoError(t, err)
	require.Zero(t, res.refused, "removing the entry must be allowed, got %d refusals", res.refused)
}

// ciAllowlistShiftedLines: a row allows one offender of its kind in its file
// wherever that offender now stands, a second offender of the same kind has no
// row and is refused, and the budget holds at one.
func ciAllowlistShiftedLines(t *testing.T, rule ciRule) {
	t.Helper()
	root := ruleFixtureTree(t, rule, rule.refusals[0].fixture)
	first, err := rule.check(root, "")
	require.NoError(t, err, "fixture must hold one offender: %v", err)
	require.Len(t, first.findings, 1, "fixture must hold one offender: %+v", first.findings)
	f := first.findings[0]
	allow := filepath.Join(t.TempDir(), "allowlist.txt")
	row := fmt.Sprintf("%s:%d %s 2026-09-17 written when the offender stood elsewhere\n", f.file, f.line+40, f.kind)
	require.NoError(t, os.WriteFile(allow, []byte(row), 0o644))
	res, err := rule.check(root, allow)
	require.NoError(t, err)
	require.Zero(t, res.refused, "a row must allow its offender after the lines shift: refused=%d allowlisted=%d stale=%+v", res.refused, res.allowlisted, res.stale)
	require.Equal(t, 1, res.allowlisted, "a row must allow its offender after the lines shift: refused=%d allowlisted=%d stale=%+v", res.refused, res.allowlisted, res.stale)

	// A second offender of the same kind in the same file has no row: the
	// budget is one.
	path := filepath.Join(root, filepath.FromSlash(f.file))
	src, err := os.ReadFile(path)
	require.NoError(t, err)
	more := string(src) + rule.secondOffender(f.file, f.host)
	require.NoError(t, os.WriteFile(path, []byte(more), 0o644))
	res, err = rule.check(root, allow)
	require.NoError(t, err)
	require.Len(t, res.findings, 1, "one row allows one offender; the second must be refused: findings=%d allowlisted=%d", len(res.findings), res.allowlisted)
	require.Equal(t, 1, res.allowlisted, "one row allows one offender; the second must be refused: findings=%d allowlisted=%d", len(res.findings), res.allowlisted)
}

// ciOutputMatchesTheSpec pins the one-line grammar of the section: the OK line,
// the refusal line and the closing FAIL line, the exit 2 a refusal costs, and
// the fields the refusal line carries.
func ciOutputMatchesTheSpec(t *testing.T, rule ciRule) {
	t.Helper()
	root := ruleFixtureTree(t, rule, rule.outputFixture)
	res, err := rule.check(root, "")
	require.NoError(t, err)
	assert.Equal(t, rule.prefix+" OK tests=1 allowlisted=0 refused=0", res.okLine, "clean line = %q", res.okLine)
	assert.Equal(t, rule.prefix+" FAIL tests=1 allowlisted=0 refused=1", res.failLine, "fail line = %q", res.failLine)
	assert.Equal(t, 2, res.exit, "exit = %d, want 2", res.exit)
	require.Len(t, res.findings, 1, "want one refusal, got %d", len(res.findings))
	line := res.findings[0].render()
	for _, want := range rule.wants {
		assert.Contains(t, line, want, "refusal %q lacks %q", line, want)
	}
}

// ciVerbLineMatchesTheSpec pins the help line the class test is entered under,
// word for word, to the section that prints it.
func ciVerbLineMatchesTheSpec(t *testing.T, rule ciRule) {
	t.Helper()
	spec := readFile(t, filepath.Join(repoRoot(t), "docs", "SPEC-CI.md"))
	assert.Contains(t, spec, rule.verbLine, "the %s verb line is not in docs/SPEC-CI.md:\n%s", rule.key, rule.verbLine)
}

// templatesRule is the templates row of the harness.
var templatesRule = ciRule{
	key:           "templates",
	prefix:        "CI-TEMPLATES",
	allowlist:     "template-paths-allowlist.txt",
	fixtures:      "templates",
	allowKind:     "join",
	allowRemedy:   TemplateRemedyAllow,
	verbLine:      TemplatesVerbLine,
	check:         runTemplates,
	outputFixture: "join.go.txt",
	wants: []string{
		"CI-TEMPLATES file=internal/fixture/fixture_test.go line=",
		` kind=join remedy="wrap the path in strconv.Quote"`,
	},
	refusals: []ciRefusal{
		{name: "TestTemplatesRefusesRawFilepathJoin", fixture: "join.go.txt", kind: "join", remedy: TemplateRemedyJoin, contains: "filepath.Join"},
		{name: "TestTemplatesRefusesUnquotedOSPathLiteral", fixture: "literal.go.txt", kind: "literal", remedy: TemplateRemedyLiteral, contains: `C:\Users\RUNN\keys\key`},
		{name: "TestTemplatesRefusesUnquotedPathInTemplate", fixture: "template.go.txt", kind: "literal", remedy: TemplateRemedyLiteral, contains: `C:\Users\RUNN\keys\key`},
	},
	allows: []ciAllow{
		{name: "TestTemplatesAllowsStrconvQuote", fixture: "quoted.go.txt", tests: 1},
		{name: "TestTemplatesAllowsOnelineQuote", fixture: "oneline_quoted.go.txt", tests: -1},
	},
}

// 1. A test carrying filepath.Join raw into a JSON literal is refused with its
// file and line, and the remedy names the quote.
func TestTemplatesRefusesRawFilepathJoin(t *testing.T) {
	t.Parallel()
	ciNamedRefusal(t, templatesRule, "TestTemplatesRefusesRawFilepathJoin")
}

// 2. A C:\ path placed unquoted inside a JSON literal is refused.
func TestTemplatesRefusesUnquotedOSPathLiteral(t *testing.T) {
	t.Parallel()
	ciNamedRefusal(t, templatesRule, "TestTemplatesRefusesUnquotedOSPathLiteral")
}

// 3. A path unquoted inside a text/template string is refused.
func TestTemplatesRefusesUnquotedPathInTemplate(t *testing.T) {
	t.Parallel()
	ciNamedRefusal(t, templatesRule, "TestTemplatesRefusesUnquotedPathInTemplate")
}

// 4. A path wrapped in strconv.Quote is allowed.
func TestTemplatesAllowsStrconvQuote(t *testing.T) {
	t.Parallel()
	ciNamedAllow(t, templatesRule, "TestTemplatesAllowsStrconvQuote")
}

// 5. A path wrapped in oneline.Quote is allowed.
func TestTemplatesAllowsOnelineQuote(t *testing.T) {
	t.Parallel()
	ciNamedAllow(t, templatesRule, "TestTemplatesAllowsOnelineQuote")
}

// 6. Adding an entry to the allowlist is refused; removing one is allowed.
func TestTemplatesAllowlistGrowsRefused(t *testing.T) {
	t.Parallel()
	ciAllowlistGrowsRefused(t, templatesRule)
}

// TestTemplatesOutputMatchesTheSpec pins the one-line grammar of the section.
func TestTemplatesOutputMatchesTheSpec(t *testing.T) {
	t.Parallel()
	ciOutputMatchesTheSpec(t, templatesRule)
}

// TestTemplatesVerbLineMatchesTheSpec pins the help line the class test is
// entered under, word for word, to the section that prints it.
func TestTemplatesVerbLineMatchesTheSpec(t *testing.T) {
	t.Parallel()
	ciVerbLineMatchesTheSpec(t, templatesRule)
}

// TestNoUnquotedPathsInTemplateLiterals is the class test itself: every
// _test.go under internal/ and cmd/ is read, and the only unquoted paths that
// pass are the ones the allowlist already names.
func TestNoUnquotedPathsInTemplateLiterals(t *testing.T) {
	t.Parallel()
	ciClassTest(t, templatesRule, filepath.Join(repoRoot(t), "internal", "ci", "testdata", "template-paths-allowlist.txt"))
}

// TestFlattenAddsSplitsOnlyAdditions: flattenAdds splits a chain of additions
// into its operands, and only additions: another operator's operands stay
// together as one.
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

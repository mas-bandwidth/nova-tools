package ci

import (
	"path/filepath"
	"testing"
)

// ci_testbins_test.go is the testbins rule's cases, run through the harness in
// ci_templates_test.go. Each top-level test keeps its old name and delegates to
// the one shared helper, so `go test -run` and the docs that name it still find
// it; the rule's inputs and wants are the testbinsRule row below.

// testbinsRule is the testbins row of ciRules.
var testbinsRule = ciRule{
	key:           "testbins",
	prefix:        "CI-TESTBIN",
	allowlist:     "fixed-testbins-allowlist.txt",
	fixtures:      "testbins",
	allowKind:     "copy",
	allowRemedy:   TestbinRemedyAllow,
	verbLine:      TestbinVerbLine,
	check:         runTestbins,
	outputFixture: "copy.go.txt",
	wants: []string{
		"CI-TESTBIN file=internal/fixture/fixture_test.go line=",
		` kind=copy remedy="place built binaries with testbin.Place: link, never copy"`,
	},
	refusals: []ciRefusal{
		{name: "TestTestbinsRefusesACopiedBinary", fixture: "copy.go.txt", kind: "copy", remedy: TestbinRemedyCopy, contains: `os.WriteFile("placed", raw, 0o755)`},
		{name: "TestTestbinsRefusesAMapHeldCopy", fixture: "indirect.go.txt", kind: "copy", contains: `os.WriteFile(dst, built["tool"], 0o755)`},
		{name: "TestTestbinsTaintIsPerVariableNotPerSpelling", fixture: "shadow.go.txt", contains: `os.WriteFile("placed", raw, 0o755)`},
	},
	allows: []ciAllow{
		{name: "TestTestbinsAllowsAShellScript", fixture: "script.go.txt", tests: 1},
		{name: "TestTestbinsAllowsThePlacedHelper", fixture: "helper.go.txt", tests: -1},
	},
	secondOffender: func(rel, host string) string {
		return "\nfunc TestSecondCopier(t *testing.T) {\n\traw, _ := os.ReadFile(\"built\")\n\t_ = os.WriteFile(\"again\", raw, 0o755)\n}\n"
	},
}

// 1. A test that reads a built binary with os.ReadFile and writes the bytes
// 0o755 into a fixture is refused with its file and line.
func TestTestbinsRefusesACopiedBinary(t *testing.T) {
	t.Parallel()
	ciNamedRefusal(t, testbinsRule, "TestTestbinsRefusesACopiedBinary")
}

// 2. The bytes can be handed to the WriteFile through a package-level map; the
// taint follows the bytes.
func TestTestbinsRefusesAMapHeldCopy(t *testing.T) {
	t.Parallel()
	ciNamedRefusal(t, testbinsRule, "TestTestbinsRefusesAMapHeldCopy")
}

// 3. A shell script written 0o755 is not a copied executable.
func TestTestbinsAllowsAShellScript(t *testing.T) {
	t.Parallel()
	ciNamedAllow(t, testbinsRule, "TestTestbinsAllowsAShellScript")
}

// 3b. Taint follows the variable, not its spelling.
func TestTestbinsTaintIsPerVariableNotPerSpelling(t *testing.T) {
	t.Parallel()
	ciNamedRefusal(t, testbinsRule, "TestTestbinsTaintIsPerVariableNotPerSpelling")
}

// 4. A test that places the built program through testbin.Place is allowed.
func TestTestbinsAllowsThePlacedHelper(t *testing.T) {
	t.Parallel()
	ciNamedAllow(t, testbinsRule, "TestTestbinsAllowsThePlacedHelper")
}

// 5. Adding an entry to the allowlist is refused; removing one is allowed.
func TestTestbinsAllowlistGrowsRefused(t *testing.T) {
	t.Parallel()
	ciAllowlistGrowsRefused(t, testbinsRule)
}

// TestTestbinsOutputMatchesTheSpec pins the one-line grammar of the section.
func TestTestbinsOutputMatchesTheSpec(t *testing.T) {
	t.Parallel()
	ciOutputMatchesTheSpec(t, testbinsRule)
}

// TestTestbinsVerbLineMatchesTheSpec pins the help line the class test is
// entered under, word for word, to the section that prints it.
func TestTestbinsVerbLineMatchesTheSpec(t *testing.T) {
	t.Parallel()
	ciVerbLineMatchesTheSpec(t, testbinsRule)
}

// TestNoCopiedTestBinariesOnTheCIPath is the class test itself: every _test.go
// under internal/ and cmd/ is read, and the only copied built binaries that
// pass are the ones the allowlist already names.
func TestNoCopiedTestBinariesOnTheCIPath(t *testing.T) {
	t.Parallel()
	ciClassTest(t, testbinsRule, filepath.Join(repoRoot(t), "internal", "ci", "testdata", "fixed-testbins-allowlist.txt"))
}

// TestTestbinsAllowlistSurvivesShiftedLines: a row allows one offender of its
// kind in its file wherever that offender now stands.
func TestTestbinsAllowlistSurvivesShiftedLines(t *testing.T) {
	t.Parallel()
	ciAllowlistShiftedLines(t, testbinsRule)
}

package ci

import (
	"path/filepath"
	"testing"
)

// ci_testbins_test.go is the testbins rule's cases, run through the harness in
// ci_templates_test.go: TestCIScanRules runs each case as a subtest; the
// rule's inputs and wants are the testbinsRule row below.

// testbinsRule is the testbins row of the harness.
var testbinsRule = ciRule{
	key:           "testbins",
	prefix:        "CI-TESTBIN",
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
		{name: "RefusesACopiedBinary", fixture: "copy.go.txt", kind: "copy", remedy: TestbinRemedyCopy, contains: `os.WriteFile("placed", raw, 0o755)`},
		{name: "RefusesAMapHeldCopy", fixture: "indirect.go.txt", kind: "copy", contains: `os.WriteFile(dst, built["tool"], 0o755)`},
		{name: "TaintIsPerVariableNotPerSpelling", fixture: "shadow.go.txt", contains: `os.WriteFile("placed", raw, 0o755)`},
	},
	allows: []ciAllow{
		{name: "AllowsAShellScript", fixture: "script.go.txt", tests: 1},
		{name: "AllowsThePlacedHelper", fixture: "helper.go.txt", tests: -1},
	},
	secondOffender: func(rel, host string) string {
		return "\nfunc TestSecondCopier(t *testing.T) {\n\traw, _ := os.ReadFile(\"built\")\n\t_ = os.WriteFile(\"again\", raw, 0o755)\n}\n"
	},
}

// TestNoCopiedTestBinariesOnTheCIPath is the class test itself: every _test.go
// under internal/ and cmd/ is read, and the only copied built binaries that
// pass are the ones the allowlist already names.
func TestNoCopiedTestBinariesOnTheCIPath(t *testing.T) {
	t.Parallel()
	ciClassTest(t, testbinsRule, filepath.Join(repoRoot(t), "internal", "ci", "testdata", "fixed-testbins-allowlist.txt"))
}

package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ci_testbins_test.go is the red-test contract of the testbins checker in
// docs/SPEC-CI.md, "The CI class test against copied built binaries". Each
// fixture below is a real _test.go written into a throwaway tree and handed to
// CheckTestbins, so the checker is exercised on a tree given on the command
// line and never by reaching into the repository. The fixtures live in
// testdata/ so the class test that walks the repo never reads the offenders it
// is meant to find.

// testbinFixtureTree writes one fixture under
// <tmp>/internal/fixture/fixture_test.go and returns the tree root, the way a
// caller hands the checker a --dir.
func testbinFixtureTree(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", "testbins", name))
	require.NoError(t, err)
	dir := filepath.Join(root, "internal", "fixture")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fixture_test.go"), src, 0o644))
	return root
}

// testbinEmptyTree is a root with no _test.go at all, so the allowlist rule can
// be tested without a real offender standing behind the entry.
func testbinEmptyTree(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// testbinLineAt reads the line the finding named and returns it trimmed, so a
// test asserts on the offender itself rather than on a line number the
// fixture's comments can move.
func testbinLineAt(t *testing.T, root, rel string, line int) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	require.NoError(t, err)
	lines := strings.Split(string(raw), "\n")
	require.False(t, line < 1 || line > len(lines), "%s:%d is outside the file (%d lines)", rel, line, len(lines))
	return strings.TrimSpace(lines[line-1])
}

// 1. A test that reads a built binary with os.ReadFile and writes the bytes
// 0o755 into a fixture is refused with its file and line, and the remedy names
// the helper.
func TestTestbinsRefusesACopiedBinary(t *testing.T) {
	t.Parallel()

	root := testbinFixtureTree(t, "copy.go.txt")
	res, err := CheckTestbins(root, "")
	require.NoError(t, err)
	require.False(t, res.Refused() != 1 || len(res.Findings) != 1, "a copied built binary is one refusal, got %d: %+v", res.Refused(), res.Findings)
	f := res.Findings[0]
	assert.Equal(t, "copy", f.Kind, "kind = %q, want copy", f.Kind)
	assert.Equal(t, TestbinRemedyCopy, f.Remedy, "remedy = %q, want %q", f.Remedy, TestbinRemedyCopy)
	got := testbinLineAt(t, root, f.File, f.Line)
	assert.Contains(t, got, "os.WriteFile(\"placed\", raw, 0o755)", "finding names %s:%d = %q, want the copy line", f.File, f.Line, got)
}

// 2. The bytes can be handed to the WriteFile through a package-level map, the
// way nova-wake's fakeBins is: the ReadFile is in one function and the
// executing WriteFile in another. The taint follows the bytes.
func TestTestbinsRefusesAMapHeldCopy(t *testing.T) {
	t.Parallel()

	root := testbinFixtureTree(t, "indirect.go.txt")
	res, err := CheckTestbins(root, "")
	require.NoError(t, err)
	require.False(t, res.Refused() != 1 || len(res.Findings) != 1, "a map-held built binary copied to a fixture is one refusal, got %d: %+v", res.Refused(), res.Findings)
	f := res.Findings[0]
	assert.Equal(t, "copy", f.Kind, "kind = %q, want copy", f.Kind)
	got := testbinLineAt(t, root, f.File, f.Line)
	assert.Contains(t, got, `os.WriteFile(dst, built["tool"], 0o755)`, "finding names %s:%d = %q, want the copy line", f.File, f.Line, got)
}

// 3. A shell script written 0o755 is not a copied executable: the interpreter
// is the executable and the script's bytes are never assessed, so the shape is
// allowed.
func TestTestbinsAllowsAShellScript(t *testing.T) {
	t.Parallel()

	root := testbinFixtureTree(t, "script.go.txt")
	res, err := CheckTestbins(root, "")
	require.NoError(t, err)
	require.False(t, res.Refused() != 0 || len(res.Findings) != 0, "a shell script written 0o755 is not the offender, got %d refusals: %+v", res.Refused(), res.Findings)
	assert.Equal(t, 1, res.Tests, "tests = %d, want 1", res.Tests)
}

// 3b. Taint follows the variable, not its spelling. A binary copied through
// raw in one function does not taint a different raw declared in another
// function to hold a shell script: the copy is the only finding, and the
// script written 0o755 under the same name stays exempt (Stella, #1262).
func TestTestbinsTaintIsPerVariableNotPerSpelling(t *testing.T) {
	t.Parallel()

	root := testbinFixtureTree(t, "shadow.go.txt")
	res, err := CheckTestbins(root, "")
	require.NoError(t, err)
	require.False(t, res.Refused() != 1 || len(res.Findings) != 1, "the binary copy is the only offender, got %d refusals: %+v", res.Refused(), res.Findings)
	f := res.Findings[0]
	got := testbinLineAt(t, root, f.File, f.Line)
	assert.Contains(t, got, `os.WriteFile("placed", raw, 0o755)`, "finding names %q, want the binary copy", got)
}

// 4. A test that places the built program through testbin.Place is the allowed
// shape: a link, not a copy, and no WriteFile carrying an execute bit.
func TestTestbinsAllowsThePlacedHelper(t *testing.T) {
	t.Parallel()

	root := testbinFixtureTree(t, "helper.go.txt")
	res, err := CheckTestbins(root, "")
	require.NoError(t, err)
	require.False(t, res.Refused() != 0 || len(res.Findings) != 0, "testbin.Place is the allowed shape, got %d refusals: %+v", res.Refused(), res.Findings)
}

// 5. Adding an entry to the allowlist is refused; removing one is allowed. An
// entry that names no offender on the tree is a place to park a copy, and the
// remedy says the file only shrinks.
func TestTestbinsAllowlistGrowsRefused(t *testing.T) {
	t.Parallel()

	root := testbinEmptyTree(t)
	allow := filepath.Join(t.TempDir(), "fixed-testbins-allowlist.txt")

	require.NoError(t, os.WriteFile(allow, []byte(""), 0o644))
	res, err := CheckTestbins(root, allow)
	require.NoError(t, err)
	require.Zero(t, res.Refused(), "an empty allowlist over a clean tree must pass, got %d refusals", res.Refused())

	require.NoError(t, os.WriteFile(allow, []byte("internal/x/x_test.go:1 copy 2026-09-17 parked here\n"), 0o644))
	res, err = CheckTestbins(root, allow)
	require.NoError(t, err)
	require.False(t, res.Refused() != 1 || len(res.Stale) != 1, "adding an allowlist entry that names no offender must be refused, got %d refusals %+v", res.Refused(), res.Stale)
	assert.Equal(t, TestbinRemedyAllow, res.Stale[0].Remedy, "allowlist remedy = %q, want %q", res.Stale[0].Remedy, TestbinRemedyAllow)

	require.NoError(t, os.WriteFile(allow, []byte(""), 0o644))
	res, err = CheckTestbins(root, allow)
	require.NoError(t, err)
	require.Zero(t, res.Refused(), "removing the entry must be allowed, got %d refusals", res.Refused())
}

// TestTestbinsOutputMatchesTheSpec pins the one-line grammar of the section:
// the OK line, the refusal line and the closing FAIL line, and the exit 2 a
// refusal costs.
func TestTestbinsOutputMatchesTheSpec(t *testing.T) {
	t.Parallel()

	root := testbinFixtureTree(t, "copy.go.txt")
	res, err := CheckTestbins(root, "")
	require.NoError(t, err)
	assert.Equal(t, "CI-TESTBIN OK tests=1 allowlisted=0 refused=0", res.OKLine(), "clean line = %q, want %q", res.OKLine(), "CI-TESTBIN OK tests=1 allowlisted=0 refused=0")
	assert.Equal(t, "CI-TESTBIN FAIL tests=1 allowlisted=0 refused=1", res.FailLine(), "fail line = %q, want %q", res.FailLine(), "CI-TESTBIN FAIL tests=1 allowlisted=0 refused=1")
	assert.Equal(t, 2, res.ExitCode(), "exit = %d, want 2", res.ExitCode())
	require.Len(t, res.Findings, 1, "want one refusal, got %d", len(res.Findings))
	line := res.Findings[0].Render()
	for _, want := range []string{
		"CI-TESTBIN file=internal/fixture/fixture_test.go line=",
		` kind=copy remedy="place built binaries with testbin.Place: link, never copy"`,
	} {
		assert.Contains(t, line, want, "refusal %q lacks %q", line, want)
	}
}

// TestTestbinsVerbLineMatchesTheSpec pins the help line the class test is
// entered under, word for word, to the section that prints it.
func TestTestbinsVerbLineMatchesTheSpec(t *testing.T) {
	t.Parallel()

	spec := readFile(t, filepath.Join(repoRoot(t), "docs", "SPEC-CI.md"))
	assert.Contains(t, spec, TestbinVerbLine, "the testbins verb line is not in docs/SPEC-CI.md:\n%s", TestbinVerbLine)
}

// TestNoCopiedTestBinariesOnTheCIPath is the class test itself: every _test.go
// under internal/ and cmd/ is read, and the only copied built binaries that
// pass are the ones the allowlist already names. The count is the truth about
// the CI path whether or not the lines printed.
func TestNoCopiedTestBinariesOnTheCIPath(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	allow := filepath.Join(root, "internal", "ci", "testdata", "fixed-testbins-allowlist.txt")
	res, err := CheckTestbins(root, allow)
	require.NoError(t, err)
	t.Log(res.OKLine())
	for _, f := range res.Findings {
		t.Error(f.Render())
	}
	// The stale rows come from the one helper, which under NOVA_CI_UPDATE=1
	// drops them from the file instead (nova-tools#4339).
	list := loadAllowlist(t, allow, FileLineListOptions)
	for _, row := range allowlist.Check(t, list, res.Measured).Stale {
		t.Errorf("%s:%d: %q names no offender on the tree; %s", allow, row.Line, row.Text, TestbinRemedyAllow)
	}
}

// TestTestbinsAllowlistSurvivesShiftedLines: a row allows one offender of its
// kind in its file wherever that offender now stands. On 2026-09-17 a merge
// shifted the lines of a listed file and a line-keyed list turned dev red for
// every group behind it, which is why matchTestbinAllow reads only the file and
// the kind. The budget still holds: a second copy in the file has no row and is
// refused, and a row with no offender left is still stale.
func TestTestbinsAllowlistSurvivesShiftedLines(t *testing.T) {
	t.Parallel()

	root := testbinFixtureTree(t, "copy.go.txt")
	first, err := CheckTestbins(root, "")
	require.False(t, err != nil || len(first.Findings) != 1, "fixture must hold one copied binary: %v %+v", err, first.Findings)
	f := first.Findings[0]
	allow := filepath.Join(t.TempDir(), "fixed-testbins-allowlist.txt")
	row := fmt.Sprintf("%s:%d copy 2026-09-17 written when the offender stood elsewhere\n", f.File, f.Line+40)
	require.NoError(t, os.WriteFile(allow, []byte(row), 0o644))
	res, err := CheckTestbins(root, allow)
	require.NoError(t, err)
	require.False(t, res.Refused() != 0 || res.Allowlisted != 1, "a row must allow its offender after the lines shift: refused=%d allowlisted=%d stale=%+v", res.Refused(), res.Allowlisted, res.Stale)

	// A second copy in the same file has no row: the budget is one.
	path := filepath.Join(root, filepath.FromSlash(f.File))
	src, err := os.ReadFile(path)
	require.NoError(t, err)
	more := string(src) + "\nfunc TestSecondCopier(t *testing.T) {\n\traw, _ := os.ReadFile(\"built\")\n\t_ = os.WriteFile(\"again\", raw, 0o755)\n}\n"
	require.NoError(t, os.WriteFile(path, []byte(more), 0o644))
	res, err = CheckTestbins(root, allow)
	require.NoError(t, err)
	require.False(t, len(res.Findings) != 1 || res.Allowlisted != 1, "one row allows one offender; the second must be refused: findings=%d allowlisted=%d", len(res.Findings), res.Allowlisted)
}

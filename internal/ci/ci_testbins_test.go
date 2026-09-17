package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	if line < 1 || line > len(lines) {
		t.Fatalf("%s:%d is outside the file (%d lines)", rel, line, len(lines))
	}
	return strings.TrimSpace(lines[line-1])
}

// 1. A test that reads a built binary with os.ReadFile and writes the bytes
// 0o755 into a fixture is refused with its file and line, and the remedy names
// the helper.
func TestTestbinsRefusesACopiedBinary(t *testing.T) {
	root := testbinFixtureTree(t, "copy.go.txt")
	res, err := CheckTestbins(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 1 || len(res.Findings) != 1 {
		t.Fatalf("a copied built binary is one refusal, got %d: %+v", res.Refused(), res.Findings)
	}
	f := res.Findings[0]
	if f.Kind != "copy" {
		t.Errorf("kind = %q, want copy", f.Kind)
	}
	if f.Remedy != TestbinRemedyCopy {
		t.Errorf("remedy = %q, want %q", f.Remedy, TestbinRemedyCopy)
	}
	if got := testbinLineAt(t, root, f.File, f.Line); !strings.Contains(got, "os.WriteFile(\"placed\", raw, 0o755)") {
		t.Errorf("finding names %s:%d = %q, want the copy line", f.File, f.Line, got)
	}
}

// 2. The bytes can be handed to the WriteFile through a package-level map, the
// way nova-wake's fakeBins is: the ReadFile is in one function and the
// executing WriteFile in another. The taint follows the bytes.
func TestTestbinsRefusesAMapHeldCopy(t *testing.T) {
	root := testbinFixtureTree(t, "indirect.go.txt")
	res, err := CheckTestbins(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 1 || len(res.Findings) != 1 {
		t.Fatalf("a map-held built binary copied to a fixture is one refusal, got %d: %+v", res.Refused(), res.Findings)
	}
	f := res.Findings[0]
	if f.Kind != "copy" {
		t.Errorf("kind = %q, want copy", f.Kind)
	}
	if got := testbinLineAt(t, root, f.File, f.Line); !strings.Contains(got, `os.WriteFile(dst, built["tool"], 0o755)`) {
		t.Errorf("finding names %s:%d = %q, want the copy line", f.File, f.Line, got)
	}
}

// 3. A shell script written 0o755 is not a copied executable: the interpreter
// is the executable and the script's bytes are never assessed, so the shape is
// allowed.
func TestTestbinsAllowsAShellScript(t *testing.T) {
	root := testbinFixtureTree(t, "script.go.txt")
	res, err := CheckTestbins(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 || len(res.Findings) != 0 {
		t.Fatalf("a shell script written 0o755 is not the offender, got %d refusals: %+v", res.Refused(), res.Findings)
	}
	if res.Tests != 1 {
		t.Errorf("tests = %d, want 1", res.Tests)
	}
}

// 4. A test that places the built program through testbin.Place is the allowed
// shape: a link, not a copy, and no WriteFile carrying an execute bit.
func TestTestbinsAllowsThePlacedHelper(t *testing.T) {
	root := testbinFixtureTree(t, "helper.go.txt")
	res, err := CheckTestbins(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 || len(res.Findings) != 0 {
		t.Fatalf("testbin.Place is the allowed shape, got %d refusals: %+v", res.Refused(), res.Findings)
	}
}

// 5. Adding an entry to the allowlist is refused; removing one is allowed. An
// entry that names no offender on the tree is a place to park a copy, and the
// remedy says the file only shrinks.
func TestTestbinsAllowlistGrowsRefused(t *testing.T) {
	root := testbinEmptyTree(t)
	allow := filepath.Join(t.TempDir(), "fixed-testbins-allowlist.txt")

	if err := os.WriteFile(allow, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := CheckTestbins(root, allow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 {
		t.Fatalf("an empty allowlist over a clean tree must pass, got %d refusals", res.Refused())
	}

	if err := os.WriteFile(allow, []byte("internal/x/x_test.go:1 copy 2026-09-17 parked here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = CheckTestbins(root, allow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 1 || len(res.Stale) != 1 {
		t.Fatalf("adding an allowlist entry that names no offender must be refused, got %d refusals %+v", res.Refused(), res.Stale)
	}
	if res.Stale[0].Remedy != TestbinRemedyAllow {
		t.Errorf("allowlist remedy = %q, want %q", res.Stale[0].Remedy, TestbinRemedyAllow)
	}

	if err := os.WriteFile(allow, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = CheckTestbins(root, allow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 {
		t.Fatalf("removing the entry must be allowed, got %d refusals", res.Refused())
	}
}

// TestTestbinsOutputMatchesTheSpec pins the one-line grammar of the section:
// the OK line, the refusal line and the closing FAIL line, and the exit 2 a
// refusal costs.
func TestTestbinsOutputMatchesTheSpec(t *testing.T) {
	root := testbinFixtureTree(t, "copy.go.txt")
	res, err := CheckTestbins(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := res.OKLine(), "CI-TESTBIN OK tests=1 allowlisted=0 refused=0"; got != want {
		t.Errorf("clean line = %q, want %q", got, want)
	}
	if got, want := res.FailLine(), "CI-TESTBIN FAIL tests=1 allowlisted=0 refused=1"; got != want {
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
		"CI-TESTBIN file=internal/fixture/fixture_test.go line=",
		` kind=copy remedy="place built binaries with testbin.Place: link, never copy"`,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("refusal %q lacks %q", line, want)
		}
	}
}

// TestTestbinsVerbLineMatchesTheSpec pins the help line the class test is
// entered under, word for word, to the section that prints it.
func TestTestbinsVerbLineMatchesTheSpec(t *testing.T) {
	spec := readFile(t, filepath.Join(repoRoot(t), "docs", "SPEC-CI.md"))
	if !strings.Contains(spec, TestbinVerbLine) {
		t.Errorf("the testbins verb line is not in docs/SPEC-CI.md:\n%s", TestbinVerbLine)
	}
}

// TestNoCopiedTestBinariesOnTheCIPath is the class test itself: every _test.go
// under internal/ and cmd/ is read, and the only copied built binaries that
// pass are the ones the allowlist already names. The count is the truth about
// the CI path whether or not the lines printed.
func TestNoCopiedTestBinariesOnTheCIPath(t *testing.T) {
	root := repoRoot(t)
	allow := filepath.Join(root, "internal", "ci", "testdata", "fixed-testbins-allowlist.txt")
	res, err := CheckTestbins(root, allow)
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

// TestTestbinsAllowlistSurvivesShiftedLines: a row allows one offender of its
// kind in its file wherever that offender now stands. On 2026-09-17 a merge
// shifted the lines of a listed file and a line-keyed list turned dev red for
// every group behind it, which is why matchTestbinAllow reads only the file and
// the kind. The budget still holds: a second copy in the file has no row and is
// refused, and a row with no offender left is still stale.
func TestTestbinsAllowlistSurvivesShiftedLines(t *testing.T) {
	root := testbinFixtureTree(t, "copy.go.txt")
	first, err := CheckTestbins(root, "")
	if err != nil || len(first.Findings) != 1 {
		t.Fatalf("fixture must hold one copied binary: %v %+v", err, first.Findings)
	}
	f := first.Findings[0]
	allow := filepath.Join(t.TempDir(), "fixed-testbins-allowlist.txt")
	row := fmt.Sprintf("%s:%d copy 2026-09-17 written when the offender stood elsewhere\n", f.File, f.Line+40)
	if err := os.WriteFile(allow, []byte(row), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := CheckTestbins(root, allow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 || res.Allowlisted != 1 {
		t.Fatalf("a row must allow its offender after the lines shift: refused=%d allowlisted=%d stale=%+v", res.Refused(), res.Allowlisted, res.Stale)
	}

	// A second copy in the same file has no row: the budget is one.
	path := filepath.Join(root, filepath.FromSlash(f.File))
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	more := string(src) + "\nfunc TestSecondCopier(t *testing.T) {\n\traw, _ := os.ReadFile(\"built\")\n\t_ = os.WriteFile(\"again\", raw, 0o755)\n}\n"
	if err := os.WriteFile(path, []byte(more), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = CheckTestbins(root, allow)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 || res.Allowlisted != 1 {
		t.Fatalf("one row allows one offender; the second must be refused: findings=%d allowlisted=%d", len(res.Findings), res.Allowlisted)
	}
}

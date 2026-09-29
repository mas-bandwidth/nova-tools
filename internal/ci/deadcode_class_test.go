package ci

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// deadcode_class_test.go holds the tree free of dead and unreachable code
// (nova-tools #4312).
//
// An unreachable function is compiled, vetted, read and merged on every commit
// for nobody. Two complementary tools guard against this debt:
//
//   - deadcode (golang.org/x/tools/cmd/deadcode) reads the call graph starting
//     from the main packages under cmd/ down (Rapid Type Analysis). It sees
//     exported and unexported functions that no command reaches.
//   - staticcheck U1000 (honnef.co/go/tools/cmd/staticcheck) analyzes each package
//     individually, including test files. It catches unused internal test helpers,
//     types, variables, constants and struct fields that deadcode never visits.
//
// Both tools are declared as `tool` directives in go.mod, built on demand from
// the module cache without global installs, and run with a cleaned environment
// (goenv.Clean). Each check compares findings against a shrink-only allowlist
// under testdata/, keyed by `pkg.Name` (package path relative to repo root, a dot,
// and the identifier name; never line numbers).
//
// Because dead and unused code depends on build tags and GOOS (Linux and Darwin
// have different system integrations under cmd/nova-sandbox and internal/wake),
// each platform has an explicit shrink-only allowlist under testdata/.
//
// Heavy static analysis tool execution runs under the functional tier in
// deadcode_functional_test.go (`//go:build functional`). The unit tier here
// runs fast allowlist parsing, merge-base shrink validation, and negative
// control invariants under 100ms.

const (
	deadcodeAllowlistDarwinPath = "testdata/deadcode_allowlist_darwin.txt"
	deadcodeAllowlistLinuxPath  = "testdata/deadcode_allowlist_linux.txt"
	u1000AllowlistDarwinPath    = "testdata/u1000_allowlist_darwin.txt"
	u1000AllowlistLinuxPath     = "testdata/u1000_allowlist_linux.txt"
)

// u1000LineRe reads one line of `staticcheck -checks U1000`'s output:
// `cmd/nova-bus/norace_test.go:6:7: const raceEnabled is unused (U1000)`.
var u1000LineRe = regexp.MustCompile(`^(\S+?):\d+:\d+: (func|field|type|var|const) (.+) is unused \(U1000\)$`)

// parseU1000Line parses a single line of staticcheck output, strictly excluding
// the known unbuildable fixture (testprobe) and refusing any unexpected compile diagnostics.
func parseU1000Line(line string) (string, bool, error) {
	if strings.Contains(line, "testprobe") {
		return "", false, nil
	}
	if strings.HasSuffix(line, "(compile)") || strings.Contains(line, "build constraints exclude all Go files") {
		return "", false, fmt.Errorf("unexpected compile diagnostic: %s", line)
	}
	m := u1000LineRe.FindStringSubmatch(line)
	if m == nil {
		return "", false, fmt.Errorf("staticcheck printed a line this reader does not know; fix u1000LineRe, not the tree: %q", line)
	}
	return packageKey(m[1], m[3]), true, nil
}

// packageKey is the key an allowlist carries for a finding: the package directory
// relative to repo root, a dot, and the symbol name.
func packageKey(file, name string) string {
	return path.Dir(filepath.ToSlash(file)) + "." + name
}

// deadcodeAllowlistBase is the commit allowlists are compared against: the merge
// base of HEAD and origin/dev when available and not HEAD itself, else HEAD's first parent.
func deadcodeAllowlistBase(root string) (string, error) {
	if _, err := gitOut(root, "rev-parse", "--verify", "-q", "refs/remotes/origin/dev^{commit}"); err == nil {
		mb, mbErr := gitOut(root, "merge-base", "HEAD", "refs/remotes/origin/dev")
		head, headErr := gitOut(root, "rev-parse", "HEAD")
		if mbErr == nil && headErr == nil && strings.TrimSpace(mb) != strings.TrimSpace(head) {
			return strings.TrimSpace(mb), nil
		}
	}
	return firstParent(root)
}

// compareAllowlists verifies that headList adds no row that baseList lacks,
// and never raises the ceiling.
func compareAllowlists(baseList, headList *allowlist.List, relPath, parent string) []string {
	var errs []string
	parentPrefix := parent
	if len(parentPrefix) > 9 {
		parentPrefix = parentPrefix[:9]
	}
	for _, row := range headList.Rows() {
		if !baseList.Has(row.Key) {
			errs = append(errs, fmt.Sprintf("%s adds the row %q, which its merge base %s does not have; the allowlist only shrinks: delete the dead symbol or drop the row",
				relPath, row.Key, parentPrefix))
		}
	}
	headCeil, headHasCeil := headList.Ceiling()
	baseCeil, baseHasCeil := baseList.Ceiling()
	if headHasCeil && baseHasCeil && headCeil > baseCeil {
		errs = append(errs, fmt.Sprintf("%s raises the ceiling from %d to %d (against merge base %s); ceiling only shrinks",
			relPath, baseCeil, headCeil, parentPrefix))
	}
	if headList.Len() > baseList.Len() {
		errs = append(errs, fmt.Sprintf("%s has %d rows, more than merge base %s (%d rows); allowlist only shrinks",
			relPath, headList.Len(), parentPrefix, baseList.Len()))
	}
	return errs
}

func checkAllowlistOnlyShrinksAgainstMergeBase(t *testing.T, root, allowPath string) {
	t.Helper()
	parent, err := deadcodeAllowlistBase(root)
	if err != nil {
		t.Fatal(err)
	}
	relPath := "internal/ci/" + allowPath
	if _, err := gitOut(root, "cat-file", "-e", parent+":"+relPath); err != nil {
		t.Logf("%s is not in the merge base %s: this change is the allowlist seed", relPath, parent[:9])
		return
	}
	baseText, err := gitOut(root, "show", parent+":"+relPath)
	if err != nil {
		t.Fatal(err)
	}
	baseList, err := allowlist.Parse(relPath, baseText, shrinkOnly)
	if err != nil {
		t.Fatalf("%s at %s: %v", relPath, parent[:9], err)
	}
	headList, err := allowlist.Load(filepath.Join(root, filepath.FromSlash(relPath)), shrinkOnly)
	if err != nil {
		t.Fatalf("%s at HEAD: %v", relPath, err)
	}
	errs := compareAllowlists(baseList, headList, relPath, parent)
	for _, e := range errs {
		t.Error(e)
	}
}

// TestDeadcodeAllowlistOnlyShrinksAgainstMergeBase asserts that HEAD adds no row
// to either deadcode allowlist that its merge base lacks, and that ceilings never increase.
func TestDeadcodeAllowlistOnlyShrinksAgainstMergeBase(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	loadAllowlist(t, deadcodeAllowlistDarwinPath, shrinkOnly)
	loadAllowlist(t, deadcodeAllowlistLinuxPath, shrinkOnly)
	checkAllowlistOnlyShrinksAgainstMergeBase(t, root, deadcodeAllowlistDarwinPath)
	checkAllowlistOnlyShrinksAgainstMergeBase(t, root, deadcodeAllowlistLinuxPath)
}

// TestU1000AllowlistOnlyShrinksAgainstMergeBase asserts that HEAD adds no row
// to either U1000 allowlist that its merge base lacks, and that ceilings never increase.
func TestU1000AllowlistOnlyShrinksAgainstMergeBase(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	loadAllowlist(t, u1000AllowlistDarwinPath, shrinkOnly)
	loadAllowlist(t, u1000AllowlistLinuxPath, shrinkOnly)
	checkAllowlistOnlyShrinksAgainstMergeBase(t, root, u1000AllowlistDarwinPath)
	checkAllowlistOnlyShrinksAgainstMergeBase(t, root, u1000AllowlistLinuxPath)
}

// TestU1000RefusesUnexpectedCompileDiagnostics provides negative control coverage
// asserting that non-testprobe compile failures cause immediate errors.
func TestU1000RefusesUnexpectedCompileDiagnostics(t *testing.T) {
	t.Parallel()

	// Legitimate testprobe fixture is safely ignored
	testprobeLine := "cmd/nova-pulse/testprobe/probe.go:1:1: build constraints exclude all Go files (compile)"
	_, isFinding, err := parseU1000Line(testprobeLine)
	if err != nil || isFinding {
		t.Errorf("expected testprobe to be safely ignored, got isFinding=%v, err=%v", isFinding, err)
	}

	// Unexpected compile error in a regular package is refused
	unexpectedLine := "cmd/nova-bus/bus.go:10:2: cannot find package (compile)"
	_, _, err = parseU1000Line(unexpectedLine)
	if err == nil || !strings.Contains(err.Error(), "unexpected compile diagnostic") {
		t.Errorf("expected unexpected compile diagnostic error, got %v", err)
	}

	unexpectedExclude := "internal/tokens/tokens.go: build constraints exclude all Go files"
	_, _, err = parseU1000Line(unexpectedExclude)
	if err == nil || !strings.Contains(err.Error(), "unexpected compile diagnostic") {
		t.Errorf("expected unexpected compile diagnostic error, got %v", err)
	}
}

// TestAllowlistShrinkComparisonNegativeControl provides negative control coverage
// asserting that adding an unseeded row or raising an allowlist ceiling fails comparison.
func TestAllowlistShrinkComparisonNegativeControl(t *testing.T) {
	t.Parallel()

	baseText := "# ceiling: 10\npkg.SymA reason 1\npkg.SymB reason 2\n"
	baseList, err := allowlist.Parse("test.txt", baseText, shrinkOnly)
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}

	// Identical list passes
	if errs := compareAllowlists(baseList, baseList, "test.txt", "1234567890"); len(errs) != 0 {
		t.Errorf("identical list failed: %v", errs)
	}

	// Shrunk list passes
	shrunkText := "# ceiling: 9\npkg.SymA reason 1\n"
	shrunkList, err := allowlist.Parse("test.txt", shrunkText, shrinkOnly)
	if err != nil {
		t.Fatalf("parse shrunk: %v", err)
	}
	if errs := compareAllowlists(baseList, shrunkList, "test.txt", "1234567890"); len(errs) != 0 {
		t.Errorf("shrunk list failed: %v", errs)
	}

	// Adding an unseeded row fails
	addedText := "# ceiling: 10\npkg.SymA reason 1\npkg.SymB reason 2\npkg.SymC new row\n"
	addedList, err := allowlist.Parse("test.txt", addedText, shrinkOnly)
	if err != nil {
		t.Fatalf("parse added: %v", err)
	}
	errs := compareAllowlists(baseList, addedList, "test.txt", "1234567890")
	if len(errs) == 0 {
		t.Errorf("expected failure when adding unseeded row, got 0 errors")
	}
	foundUnseeded := false
	for _, e := range errs {
		if strings.Contains(e, "adds the row") {
			foundUnseeded = true
		}
	}
	if !foundUnseeded {
		t.Errorf("expected 'adds the row' error, got %v", errs)
	}

	// Raising the ceiling fails
	raisedCeilText := "# ceiling: 12\npkg.SymA reason 1\npkg.SymB reason 2\n"
	raisedList, err := allowlist.Parse("test.txt", raisedCeilText, shrinkOnly)
	if err != nil {
		t.Fatalf("parse raised: %v", err)
	}
	errsCeil := compareAllowlists(baseList, raisedList, "test.txt", "1234567890")
	if len(errsCeil) == 0 {
		t.Errorf("expected failure when raising ceiling, got 0 errors")
	}
	foundCeil := false
	for _, e := range errsCeil {
		if strings.Contains(e, "raises the ceiling") {
			foundCeil = true
		}
	}
	if !foundCeil {
		t.Errorf("expected 'raises the ceiling' error, got %v", errsCeil)
	}
}

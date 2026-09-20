package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ci_waits_test.go is the red-test contract of the waits checker in
// docs/SPEC-CI.md, "The CI class test against fixed waits on the CI path".
// Each fixture below is a real _test.go written into a throwaway tree and
// handed to CheckWaits, so the checker is exercised on a tree given on the
// command line and never by reaching into the repository. The fixtures live in
// testdata/ so the class test that walks the repo never reads the offenders it
// is meant to find.

// waitFixtureTree writes one fixture under <tmp>/internal/fixture/fixture_test.go
// and returns the tree root, the way a caller hands the checker a --dir.
func waitFixtureTree(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", "waits", name))
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

// waitEmptyTree is a root with no _test.go at all, so the allowlist rule can be
// tested without a real offender standing behind the entry.
func waitEmptyTree(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// waitLineAt reads the line the finding named and returns it trimmed, so a test
// asserts on the offender itself rather than on a line number the fixture's
// comments can move.
func waitLineAt(t *testing.T, root, rel string, line int) string {
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

// 1. A test carrying time.Sleep(150 * time.Millisecond) is refused with its
// file and line, and the remedy names the poll.
func TestWaitsRefusesFixedSleep(t *testing.T) {
	root := waitFixtureTree(t, "sleep.go.txt")
	res, err := CheckWaits(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 1 || len(res.Findings) != 1 {
		t.Fatalf("a fixed sleep over 100ms is one refusal, got %d: %+v", res.Refused(), res.Findings)
	}
	f := res.Findings[0]
	if f.Kind != "sleep" {
		t.Errorf("kind = %q, want sleep", f.Kind)
	}
	if f.Remedy != WaitRemedySleep {
		t.Errorf("remedy = %q, want %q", f.Remedy, WaitRemedySleep)
	}
	if got := waitLineAt(t, root, f.File, f.Line); !strings.Contains(got, "time.Sleep(150 * time.Millisecond)") {
		t.Errorf("finding names %s:%d = %q, want the fixed sleep line", f.File, f.Line, got)
	}
}

// 2. A test with context.WithTimeout(ctx, 5*time.Second) used as its pass/fail
// condition is refused as a bound under ten seconds.
func TestWaitsRefusesShortBound(t *testing.T) {
	root := waitFixtureTree(t, "bound.go.txt")
	res, err := CheckWaits(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 1 || len(res.Findings) != 1 {
		t.Fatalf("a context bound under ten seconds is one refusal, got %d: %+v", res.Refused(), res.Findings)
	}
	f := res.Findings[0]
	if f.Kind != "bound" {
		t.Errorf("kind = %q, want bound", f.Kind)
	}
	if f.Remedy != WaitRemedyBound {
		t.Errorf("remedy = %q, want %q", f.Remedy, WaitRemedyBound)
	}
	if got := waitLineAt(t, root, f.File, f.Line); !strings.Contains(got, "context.WithTimeout(context.Background(), 5") {
		t.Errorf("finding names %s:%d = %q, want the context bound line", f.File, f.Line, got)
	}
}

// 3. A test asserting time.Since(start) < 5*time.Second is refused as an
// elapsed-time assertion.
func TestWaitsRefusesElapsedAssertion(t *testing.T) {
	root := waitFixtureTree(t, "elapsed.go.txt")
	res, err := CheckWaits(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 1 || len(res.Findings) != 1 {
		t.Fatalf("an elapsed-time assertion is one refusal, got %d: %+v", res.Refused(), res.Findings)
	}
	f := res.Findings[0]
	if f.Kind != "elapsed" {
		t.Errorf("kind = %q, want elapsed", f.Kind)
	}
	if f.Remedy != WaitRemedyElapsed {
		t.Errorf("remedy = %q, want %q", f.Remedy, WaitRemedyElapsed)
	}
	if got := waitLineAt(t, root, f.File, f.Line); !strings.Contains(got, "time.Since(start) < 5") {
		t.Errorf("finding names %s:%d = %q, want the elapsed assertion line", f.File, f.Line, got)
	}
}

// 4. A polling test reading NOVA_TEST_WAIT (default 30s) and waiting for the
// event through a fake network is allowed.
func TestWaitsAllowsThePoll(t *testing.T) {
	root := waitFixtureTree(t, "poll.go.txt")
	res, err := CheckWaits(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 || len(res.Findings) != 0 {
		t.Fatalf("a poll up to NOVA_TEST_WAIT is the allowed shape, got %d refusals: %+v", res.Refused(), res.Findings)
	}
	if res.Tests != 1 {
		t.Errorf("tests = %d, want 1", res.Tests)
	}
}

// 5. A test whose subprocess bench and clock are fakes passes with no wall
// clock in the file.
func TestWaitsAllowsFakeBenchAndClock(t *testing.T) {
	root := waitFixtureTree(t, "fakeclock.go.txt")
	res, err := CheckWaits(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 || len(res.Findings) != 0 {
		t.Fatalf("a fake bench and clock is the allowed shape, got %d refusals: %+v", res.Refused(), res.Findings)
	}
}

// 6. Adding an entry to the allowlist is refused; removing one is allowed. An
// entry that names no offender on the tree is a place to park a wait, and the
// remedy says the file only shrinks.
func TestWaitsAllowlistGrowsRefused(t *testing.T) {
	root := waitEmptyTree(t)
	allow := filepath.Join(t.TempDir(), "fixed-waits-allowlist.txt")

	if err := os.WriteFile(allow, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := CheckWaits(root, allow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 {
		t.Fatalf("an empty allowlist over a clean tree must pass, got %d refusals", res.Refused())
	}

	if err := os.WriteFile(allow, []byte("internal/x/x_test.go:1 sleep 2026-09-17 parked here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = CheckWaits(root, allow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 1 || len(res.Stale) != 1 {
		t.Fatalf("adding an allowlist entry that names no offender must be refused, got %d refusals %+v", res.Refused(), res.Stale)
	}
	if res.Stale[0].Remedy != WaitRemedyAllow {
		t.Errorf("allowlist remedy = %q, want %q", res.Stale[0].Remedy, WaitRemedyAllow)
	}

	if err := os.WriteFile(allow, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = CheckWaits(root, allow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 {
		t.Fatalf("removing the entry must be allowed, got %d refusals", res.Refused())
	}
}

// TestWaitsOutputMatchesTheSpec pins the one-line grammar of the section: the
// OK line, the refusal line and the closing FAIL line, and the exit 2 a
// refusal costs.
func TestWaitsOutputMatchesTheSpec(t *testing.T) {
	root := waitFixtureTree(t, "sleep.go.txt")
	res, err := CheckWaits(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := res.OKLine(), "CI-WAITS OK tests=1 allowlisted=0 refused=0"; got != want {
		t.Errorf("clean line = %q, want %q", got, want)
	}
	if got, want := res.FailLine(), "CI-WAITS FAIL tests=1 allowlisted=0 refused=1"; got != want {
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
		"CI-WAITS file=internal/fixture/fixture_test.go line=",
		` kind=sleep remedy="poll for the event up to NOVA_TEST_WAIT, not a fixed sleep"`,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("refusal %q lacks %q", line, want)
		}
	}
}

// TestWaitsVerbLineMatchesTheSpec pins the help line the class test is entered
// under, word for word, to the section that prints it.
func TestWaitsVerbLineMatchesTheSpec(t *testing.T) {
	spec := readFile(t, filepath.Join(repoRoot(t), "docs", "SPEC-CI.md"))
	if !strings.Contains(spec, WaitsVerbLine) {
		t.Errorf("the waits verb line is not in docs/SPEC-CI.md:\n%s", WaitsVerbLine)
	}
}

// TestNoFixedWaitsOnTheCIPath is the class test itself: every _test.go under
// internal/ and cmd/ is read, and the only fixed waits that pass are the ones
// the allowlist already names. The count is the truth about the CI path
// whether or not the lines printed.
func TestNoFixedWaitsOnTheCIPath(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	allow := filepath.Join(root, "internal", "ci", "testdata", "fixed-waits-allowlist.txt")
	res, err := CheckWaits(root, allow)
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

// TestWaitsAllowlistSurvivesShiftedLines: a row allows one offender of its kind in its
// file wherever that offender now stands. On 2026-09-17 a merge shifted the lines of a
// listed file and the line-keyed list turned dev red for every group behind it. The
// budget still holds: a second offender of the same kind in the file has no row and is
// refused, and a row with no offender left is still stale.
func TestWaitsAllowlistSurvivesShiftedLines(t *testing.T) {
	root := waitFixtureTree(t, "sleep.go.txt")
	first, err := CheckWaits(root, "")
	if err != nil || len(first.Findings) != 1 {
		t.Fatalf("fixture must hold one fixed sleep: %v %+v", err, first.Findings)
	}
	f := first.Findings[0]
	allow := filepath.Join(t.TempDir(), "fixed-waits-allowlist.txt")
	row := fmt.Sprintf("%s:%d sleep 2026-09-17 written when the offender stood elsewhere\n", f.File, f.Line+40)
	if err := os.WriteFile(allow, []byte(row), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := CheckWaits(root, allow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 || res.Allowlisted != 1 {
		t.Fatalf("a row must allow its offender after the lines shift: refused=%d allowlisted=%d stale=%+v", res.Refused(), res.Allowlisted, res.Stale)
	}

	// A second fixed sleep in the same file has no row: the budget is one.
	path := filepath.Join(root, filepath.FromSlash(f.File))
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	more := string(src) + "\nfunc TestSecondSleeper(t *testing.T) { time.Sleep(250 * time.Millisecond) }\n"
	if err := os.WriteFile(path, []byte(more), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = CheckWaits(root, allow)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 || res.Allowlisted != 1 {
		t.Fatalf("one row allows one offender; the second must be refused: findings=%d allowlisted=%d", len(res.Findings), res.Allowlisted)
	}
}

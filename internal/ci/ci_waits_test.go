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
	require.NoError(t, err)
	dir := filepath.Join(root, "internal", "fixture")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fixture_test.go"), src, 0o644))
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
	require.NoError(t, err)
	lines := strings.Split(string(raw), "\n")
	require.False(t, line < 1 || line > len(lines), "%s:%d is outside the file (%d lines)", rel, line, len(lines))
	return strings.TrimSpace(lines[line-1])
}

// 1. A test carrying time.Sleep(150 * time.Millisecond) is refused with its
// file and line, and the remedy names the poll.
func TestWaitsRefusesFixedSleep(t *testing.T) {
	t.Parallel()

	root := waitFixtureTree(t, "sleep.go.txt")
	res, err := CheckWaits(root, "")
	require.NoError(t, err)
	require.False(t, res.Refused() != 1 || len(res.Findings) != 1, "a fixed sleep over 100ms is one refusal, got %d: %+v", res.Refused(), res.Findings)
	f := res.Findings[0]
	assert.Equal(t, "sleep", f.Kind, "kind = %q, want sleep", f.Kind)
	assert.Equal(t, WaitRemedySleep, f.Remedy, "remedy = %q, want %q", f.Remedy, WaitRemedySleep)
	got := waitLineAt(t, root, f.File, f.Line)
	assert.Contains(t, got, "time.Sleep(150 * time.Millisecond)", "finding names %s:%d = %q, want the fixed sleep line", f.File, f.Line, got)
}

// 2. A test with context.WithTimeout(ctx, 5*time.Second) used as its pass/fail
// condition is refused as a bound under ten seconds.
func TestWaitsRefusesShortBound(t *testing.T) {
	t.Parallel()

	root := waitFixtureTree(t, "bound.go.txt")
	res, err := CheckWaits(root, "")
	require.NoError(t, err)
	require.False(t, res.Refused() != 1 || len(res.Findings) != 1, "a context bound under ten seconds is one refusal, got %d: %+v", res.Refused(), res.Findings)
	f := res.Findings[0]
	assert.Equal(t, "bound", f.Kind, "kind = %q, want bound", f.Kind)
	assert.Equal(t, WaitRemedyBound, f.Remedy, "remedy = %q, want %q", f.Remedy, WaitRemedyBound)
	got := waitLineAt(t, root, f.File, f.Line)
	assert.Contains(t, got, "context.WithTimeout(context.Background(), 5", "finding names %s:%d = %q, want the context bound line", f.File, f.Line, got)
}

// 3. A test asserting time.Since(start) < 5*time.Second is refused as an
// elapsed-time assertion.
func TestWaitsRefusesElapsedAssertion(t *testing.T) {
	t.Parallel()

	root := waitFixtureTree(t, "elapsed.go.txt")
	res, err := CheckWaits(root, "")
	require.NoError(t, err)
	require.False(t, res.Refused() != 1 || len(res.Findings) != 1, "an elapsed-time assertion is one refusal, got %d: %+v", res.Refused(), res.Findings)
	f := res.Findings[0]
	assert.Equal(t, "elapsed", f.Kind, "kind = %q, want elapsed", f.Kind)
	assert.Equal(t, WaitRemedyElapsed, f.Remedy, "remedy = %q, want %q", f.Remedy, WaitRemedyElapsed)
	got := waitLineAt(t, root, f.File, f.Line)
	assert.Contains(t, got, "time.Since(start) < 5", "finding names %s:%d = %q, want the elapsed assertion line", f.File, f.Line, got)
}

// 4. A polling test reading NOVA_TEST_WAIT (default 30s) and waiting for the
// event through a fake network is allowed.
func TestWaitsAllowsThePoll(t *testing.T) {
	t.Parallel()

	root := waitFixtureTree(t, "poll.go.txt")
	res, err := CheckWaits(root, "")
	require.NoError(t, err)
	require.False(t, res.Refused() != 0 || len(res.Findings) != 0, "a poll up to NOVA_TEST_WAIT is the allowed shape, got %d refusals: %+v", res.Refused(), res.Findings)
	assert.Equal(t, 1, res.Tests, "tests = %d, want 1", res.Tests)
}

// 5. A test whose subprocess bench and clock are fakes passes with no wall
// clock in the file.
func TestWaitsAllowsFakeBenchAndClock(t *testing.T) {
	t.Parallel()

	root := waitFixtureTree(t, "fakeclock.go.txt")
	res, err := CheckWaits(root, "")
	require.NoError(t, err)
	require.False(t, res.Refused() != 0 || len(res.Findings) != 0, "a fake bench and clock is the allowed shape, got %d refusals: %+v", res.Refused(), res.Findings)
}

// 6. Adding an entry to the allowlist is refused; removing one is allowed. An
// entry that names no offender on the tree is a place to park a wait, and the
// remedy says the file only shrinks.
func TestWaitsAllowlistGrowsRefused(t *testing.T) {
	t.Parallel()

	root := waitEmptyTree(t)
	allow := filepath.Join(t.TempDir(), "fixed-waits-allowlist.txt")

	require.NoError(t, os.WriteFile(allow, []byte(""), 0o644))
	res, err := CheckWaits(root, allow)
	require.NoError(t, err)
	require.Zero(t, res.Refused(), "an empty allowlist over a clean tree must pass, got %d refusals", res.Refused())

	require.NoError(t, os.WriteFile(allow, []byte("internal/x/x_test.go:1 sleep 2026-09-17 parked here\n"), 0o644))
	res, err = CheckWaits(root, allow)
	require.NoError(t, err)
	require.False(t, res.Refused() != 1 || len(res.Stale) != 1, "adding an allowlist entry that names no offender must be refused, got %d refusals %+v", res.Refused(), res.Stale)
	assert.Equal(t, WaitRemedyAllow, res.Stale[0].Remedy, "allowlist remedy = %q, want %q", res.Stale[0].Remedy, WaitRemedyAllow)

	require.NoError(t, os.WriteFile(allow, []byte(""), 0o644))
	res, err = CheckWaits(root, allow)
	require.NoError(t, err)
	require.Zero(t, res.Refused(), "removing the entry must be allowed, got %d refusals", res.Refused())
}

// TestWaitsOutputMatchesTheSpec pins the one-line grammar of the section: the
// OK line, the refusal line and the closing FAIL line, and the exit 2 a
// refusal costs.
func TestWaitsOutputMatchesTheSpec(t *testing.T) {
	t.Parallel()

	root := waitFixtureTree(t, "sleep.go.txt")
	res, err := CheckWaits(root, "")
	require.NoError(t, err)
	assert.Equal(t, "CI-WAITS OK tests=1 allowlisted=0 refused=0", res.OKLine(), "clean line = %q, want %q", res.OKLine(), "CI-WAITS OK tests=1 allowlisted=0 refused=0")
	assert.Equal(t, "CI-WAITS FAIL tests=1 allowlisted=0 refused=1", res.FailLine(), "fail line = %q, want %q", res.FailLine(), "CI-WAITS FAIL tests=1 allowlisted=0 refused=1")
	assert.Equal(t, 2, res.ExitCode(), "exit = %d, want 2", res.ExitCode())
	require.Len(t, res.Findings, 1, "want one refusal, got %d", len(res.Findings))
	line := res.Findings[0].Render()
	for _, want := range []string{
		"CI-WAITS file=internal/fixture/fixture_test.go line=",
		` kind=sleep remedy="poll for the event up to NOVA_TEST_WAIT, not a fixed sleep"`,
	} {
		assert.Contains(t, line, want, "refusal %q lacks %q", line, want)
	}
}

// TestWaitsVerbLineMatchesTheSpec pins the help line the class test is entered
// under, word for word, to the section that prints it.
func TestWaitsVerbLineMatchesTheSpec(t *testing.T) {
	t.Parallel()

	spec := readFile(t, filepath.Join(repoRoot(t), "docs", "SPEC-CI.md"))
	assert.Contains(t, spec, WaitsVerbLine, "the waits verb line is not in docs/SPEC-CI.md:\n%s", WaitsVerbLine)
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
	require.NoError(t, err)
	t.Log(res.OKLine())
	for _, f := range res.Findings {
		t.Error(f.Render())
	}
	// The stale rows come from the one helper, which under NOVA_CI_UPDATE=1
	// drops them from the file instead (nova-tools#4339).
	list := loadAllowlist(t, allow, FileLineListOptions)
	for _, row := range allowlist.Check(t, list, res.Measured).Stale {
		t.Errorf("%s:%d: %q names no offender on the tree; %s", allow, row.Line, row.Text, WaitRemedyAllow)
	}
}

// TestWaitsAllowlistSurvivesShiftedLines: a row allows one offender of its kind in its
// file wherever that offender now stands. On 2026-09-17 a merge shifted the lines of a
// listed file and the line-keyed list turned dev red for every group behind it. The
// budget still holds: a second offender of the same kind in the file has no row and is
// refused, and a row with no offender left is still stale.
func TestWaitsAllowlistSurvivesShiftedLines(t *testing.T) {
	t.Parallel()

	root := waitFixtureTree(t, "sleep.go.txt")
	first, err := CheckWaits(root, "")
	require.False(t, err != nil || len(first.Findings) != 1, "fixture must hold one fixed sleep: %v %+v", err, first.Findings)
	f := first.Findings[0]
	allow := filepath.Join(t.TempDir(), "fixed-waits-allowlist.txt")
	row := fmt.Sprintf("%s:%d sleep 2026-09-17 written when the offender stood elsewhere\n", f.File, f.Line+40)
	require.NoError(t, os.WriteFile(allow, []byte(row), 0o644))
	res, err := CheckWaits(root, allow)
	require.NoError(t, err)
	require.False(t, res.Refused() != 0 || res.Allowlisted != 1, "a row must allow its offender after the lines shift: refused=%d allowlisted=%d stale=%+v", res.Refused(), res.Allowlisted, res.Stale)

	// A second fixed sleep in the same file has no row: the budget is one.
	path := filepath.Join(root, filepath.FromSlash(f.File))
	src, err := os.ReadFile(path)
	require.NoError(t, err)
	more := string(src) + "\nfunc TestSecondSleeper(t *testing.T) { time.Sleep(250 * time.Millisecond) }\n"
	require.NoError(t, os.WriteFile(path, []byte(more), 0o644))
	res, err = CheckWaits(root, allow)
	require.NoError(t, err)
	require.False(t, len(res.Findings) != 1 || res.Allowlisted != 1, "one row allows one offender; the second must be refused: findings=%d allowlisted=%d", len(res.Findings), res.Allowlisted)
}

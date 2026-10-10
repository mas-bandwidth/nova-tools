//go:build functional

package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/testgit"
)

// These tests exec whole programs -- the fake runner this package builds
// (testdata/fakerunner), git, sqlite3 or sh -- so they are the functional tier's,
// not unit tests (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s and
// frugal with the machine's cores). The rest of the file's tests stay in the
// unit tier.

// baseRepo is a git repository with one commit holding internal/decide/decide.go
// and nothing else under internal/decide, which is the shape of nova-tools at
// the nx-f19 base: the package exists, entry.go does not.
func baseRepo(t *testing.T) (dir, sha string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	dir = t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = testgit.Environ("GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %v\n%s", args, err, out)
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "internal", "decide"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "internal", "decide", "decide.go"), []byte("package decide\n"), 0o644))
	git("add", ".")
	git("commit", "-q", "-m", "base")
	return dir, git("rev-parse", "HEAD")
}

func TestLintPathsResolveAtBase(t *testing.T) {
	t.Parallel()

	repo, sha := baseRepo(t)
	bc := fullEvidence(repo)

	// nx-f19: `PATHS: example.com/decide/entry.go`, a placeholder under a foreign
	// root that does not exist at base.
	fs := findingsFor(LintCardBase(baseCard(map[string]string{"base-sha": sha, "PATHS": "example.com/decide/entry.go"}), bc), "paths-at-base")
	require.Len(t, fs, 1, "a PATHS file absent at base-sha is refused by name and sha, got %v", fs)
	require.Contains(t, fs[0].Excerpt, "example.com/decide/entry.go", "a PATHS file absent at base-sha is refused by name and sha, got %v", fs)
	require.Contains(t, fs[0].Excerpt, sha[:12], "a PATHS file absent at base-sha is refused by name and sha, got %v", fs)
	require.Equal(t, 6, fs[0].Line, "the finding sits on the PATHS: line (6), got %d", fs[0].Line)

	// Passes: a file that exists, a NEW _test file beside it, a glob that matches, `none`.
	for _, paths := range []string{
		"internal/decide/decide.go",
		"internal/decide/decide.go, internal/decide/entry_test.go",
		"internal/decide/*.go",
		"internal/**",
		"none",
	} {
		fs = findingsFor(LintCardBase(baseCard(map[string]string{"base-sha": sha, "PATHS": paths}), bc), "paths-at-base")
		require.Empty(t, fs, "PATHS: %s resolves at base and passes, got %v", paths, fs)
	}

	// A glob that matches nothing is refused like a missing file.
	fs = findingsFor(LintCardBase(baseCard(map[string]string{"base-sha": sha, "PATHS": "internal/nowhere/*.go"}), bc), "paths-at-base")
	require.Len(t, fs, 1, "a glob that matches nothing at base is refused, got %v", fs)
	require.Contains(t, fs[0].Excerpt, "internal/nowhere/*.go", "a glob that matches nothing at base is refused, got %v", fs)

	// No evidence is not negative evidence: a base-sha the repository does not hold,
	// no base-sha at all, and no repository each print MISSING and refuse.
	fs = findingsFor(LintCardBase(baseCard(map[string]string{"base-sha": "39d1d6a5c918aaaaaaaaaaaaaaaaaaaaaaaaaaaa"}), bc), "paths-at-base")
	require.Len(t, fs, 1, "a base-sha the repository does not hold is MISSING, never a pass, got %v", fs)
	require.Contains(t, fs[0].Excerpt, "MISSING", "a base-sha the repository does not hold is MISSING, never a pass, got %v", fs)
	fs = findingsFor(LintCardBase(baseCard(map[string]string{"base-sha": "\x00"}), bc), "paths-at-base")
	require.Len(t, fs, 1, "a card with no base-sha is MISSING, got %v", fs)
	require.Contains(t, fs[0].Excerpt, "MISSING", "a card with no base-sha is MISSING, got %v", fs)
	noRepo := bc
	noRepo.Repo = ""
	fs = findingsFor(LintCardBase(baseCard(map[string]string{"base-sha": sha}), noRepo), "paths-at-base")
	require.Len(t, fs, 1, "no repository handed over is MISSING, got %v", fs)
	require.Contains(t, fs[0].Excerpt, "MISSING", "no repository handed over is MISSING, got %v", fs)
	// A repair card's PATHS are the PR's own files: an entry the PR adds resolves at
	// PR-HEAD, a PR-HEAD the repository does not hold is MISSING, and an entry at
	// neither is refused.
	prHead := addCommit(t, repo, "internal/swarm/effect.go")
	fs = findingsFor(LintCardBase(baseCard(map[string]string{"base-sha": sha, "PATHS": "internal/swarm/effect.go", "PR-HEAD": prHead}), bc), "paths-at-base")
	require.Empty(t, fs, "a file the PR adds resolves at PR-HEAD, got %v", fs)
	fs = findingsFor(LintCardBase(baseCard(map[string]string{"base-sha": sha, "PATHS": "internal/swarm/effect.go", "PR-HEAD": "ec4230ddde17b45706a2e93136f55e564d67f778"}), bc), "paths-at-base")
	require.Len(t, fs, 1, "a PR-HEAD the repository does not hold is MISSING, got %v", fs)
	require.Contains(t, fs[0].Excerpt, "MISSING", "a PR-HEAD the repository does not hold is MISSING, got %v", fs)
	fs = findingsFor(LintCardBase(baseCard(map[string]string{"base-sha": sha, "PATHS": "internal/swarm/nowhere.go", "PR-HEAD": prHead}), bc), "paths-at-base")
	require.Len(t, fs, 1, "an entry at neither base nor PR-HEAD is refused, got %v", fs)
	require.Contains(t, fs[0].Excerpt, "PR-HEAD", "an entry at neither base nor PR-HEAD is refused, got %v", fs)

	// The contract line's sha= stands in for a missing base-sha line when it resolves.
	fs = findingsFor(LintCardBase([]byte("RESULT c sha="+sha[:12]+" -- t\nKIND: fix\nPATHS: internal/decide/decide.go\n"), bc), "paths-at-base")
	require.Empty(t, fs, "the contract line's sha= is the base when no base-sha: line is given, got %v", fs)
}

// #3083, INSERTION 2: THE CONTROL IS THE SENTENCE. A card's DONE-WHEN names a test
// runner and a literal test that is absent at base-sha, so the test can be red there;
// an English outcome ("applied cleanly", "make preflight") is not a control and the
// card is refused before any model is spent on it.
func TestLintDoneWhenTestNameAtBase(t *testing.T) {
	t.Parallel()

	repo, _ := baseRepo(t)
	commitFileAt(t, repo, "internal/decide/decide_test.go",
		"package decide\n\nimport \"testing\"\n\nfunc TestDecideExisting(t *testing.T) {}\n")
	commitFileAt(t, repo, "tests/test_decide.py", "def test_decide_existing():\n    pass\n")
	// Same-named tests in another package and another file: a card whose command
	// targets ./internal/decide or tests/test_decide.py is not refused by them.
	commitFileAt(t, repo, "internal/other/other_test.go",
		"package other\n\nimport \"testing\"\n\nfunc TestDecideElsewhere(t *testing.T) {}\n")
	commitFileAt(t, repo, "tests/test_other.py", "def test_decide_elsewhere():\n    pass\n")
	commitFileAt(t, repo, "go.mod", "module example.com/fixture\n\ngo 1.22\n")
	sha := commitFileAt(t, repo, "src/lib.rs", "#[test]\nfn decide_existing() {}\n")
	bc := fullEvidence(repo)
	lint := func(done string, bc BaseCheck) []CardHeaderFinding {
		h := map[string]string{"base-sha": sha}
		if done != "" {
			h["DONE-WHEN"] = done
		}
		return findingsFor(LintCardBase(baseCard(h), bc), "donewhen-test-name")
	}

	// Clean: a real test runner naming a test absent at base-sha.
	for _, done := range []string{
		"`go test ./internal/decide -run TestDecideConfidenceMissing` passes",
		"`go test ./internal/decide -run=TestDecideConfidenceMissing -count=1` passes",
		"go test ./internal/decide -run '^TestDecideConfidenceMissing$' passes",
		// One new test among existing ones is still a control that can be red.
		`go test ./internal/decide -run "TestDecideExisting|TestDecideConfidenceMissing/zero" passes`,
		"`pytest tests/test_decide.py::test_decide_confidence_missing` passes",
		"`pytest tests -k test_decide_confidence_missing` passes",
		"`cargo test decide::decide_confidence_missing` passes",
		// The name exists at base, but only outside the command's own target.
		"`go test ./internal/decide -run TestDecideElsewhere` passes",
		"`go test -count=1 -run TestDecideElsewhere example.com/fixture/internal/decide` passes",
		"`pytest tests/test_decide.py::test_decide_elsewhere` passes",
		"`pytest tests/test_decide.py -k test_decide_elsewhere` passes",
		// A long option's value is no target: `--rootdir tests` must not widen the
		// lookup to tests/test_other.py (stella's hold at 8f7465f0).
		"`pytest tests/test_decide.py --rootdir tests -k test_decide_elsewhere` passes",
		"`pytest --rootdir tests --maxfail 1 tests/test_decide.py -k test_decide_elsewhere` passes",
	} {
		fs := lint(done, bc)
		require.Empty(t, fs, "DONE-WHEN %q names a test absent at base and lints clean, got %v", done, fs)
	}

	// Refused: prose outcomes, a runner with no test named, a regex that is no name.
	for _, done := range []string{
		"applied cleanly",
		"make preflight",
		"`go test ./...` passes",
		"`go test ./internal/decide -run 'TestDecide.*'` passes",
		"the lander merges it",
	} {
		fs := lint(done, bc)
		require.Len(t, fs, 1, "DONE-WHEN %q names no literal test and is refused, got %v", done, fs)
		require.Equal(t, 8, fs[0].Line, "the finding sits on the DONE-WHEN: line (8), got %d for %q", fs[0].Line, done)
	}

	// Refused: every named test already exists at base-sha, so it cannot be red there.
	for _, done := range []string{
		"`go test ./internal/decide -run TestDecideExisting` passes",
		"`pytest tests/test_decide.py::test_decide_existing` passes",
		"`cargo test decide_existing` passes",
		// The same names, looked up in the target that holds them.
		"`go test ./internal/other -run TestDecideElsewhere` passes",
		"`go test ./internal/... -run TestDecideElsewhere` passes",
		"`go test ./... -run TestDecideElsewhere` passes",
		"`go test -run TestDecideElsewhere example.com/fixture/internal/other` passes",
		"`pytest tests/test_other.py::test_decide_elsewhere` passes",
		"`pytest tests -k test_decide_elsewhere` passes",
		"`pytest tests/test_other.py --rootdir tests -k test_decide_elsewhere` passes",
		"`pytest --rootdir=. tests -k test_decide_elsewhere` passes",
	} {
		fs := lint(done, bc)
		require.Len(t, fs, 1, "DONE-WHEN %q names a test present at base and is refused by sha, got %v", done, fs)
		require.Contains(t, fs[0].Excerpt, "exists at base-sha "+sha[:12], "DONE-WHEN %q names a test present at base and is refused by sha, got %v", done, fs)
	}

	// No DONE-WHEN at all is refused.
	fs := lint("", bc)
	require.Len(t, fs, 1, "a card with no DONE-WHEN is refused, got %v", fs)
	require.Contains(t, fs[0].Excerpt, "DONE-WHEN", "a card with no DONE-WHEN is refused, got %v", fs)

	// No evidence is not negative evidence: no repo, or a base the repo lacks, is MISSING.
	none := bc
	none.Repo = ""
	fs = lint("`go test ./internal/decide -run TestDecideConfidenceMissing` passes", none)
	require.Len(t, fs, 1, "no repository handed over is MISSING, never a pass, got %v", fs)
	require.Contains(t, fs[0].Excerpt, "MISSING", "no repository handed over is MISSING, never a pass, got %v", fs)
	gone := findingsFor(LintCardBase(baseCard(map[string]string{
		"base-sha":  "1111111111111111111111111111111111111111",
		"DONE-WHEN": "`go test ./internal/decide -run TestDecideConfidenceMissing` passes",
	}), bc), "donewhen-test-name")
	require.Len(t, gone, 1, "a base-sha the repository does not hold is MISSING, got %v", gone)
	require.Contains(t, gone[0].Excerpt, "MISSING", "a base-sha the repository does not hold is MISSING, got %v", gone)

	// The token has its remedy, so `nova-worker lint --rules` lists it.
	require.NotEmpty(t, CardBaseRemedies["donewhen-test-name"], "donewhen-test-name has no remedy in CardBaseRemedies")
}

// addCommit commits one new file on top of HEAD and returns the new sha, which a
// test uses as a PR head that adds the file.
func addCommit(t *testing.T, dir, rel string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, filepath.Dir(rel)), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, rel), []byte("package x\n"), 0o644))
	var sha string
	for _, args := range [][]string{{"add", "."}, {"commit", "-q", "-m", "pr"}, {"rev-parse", "HEAD"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = testgit.Environ("GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %v\n%s", args, err, out)
		sha = strings.TrimSpace(string(out))
	}
	return sha
}

func fullEvidence(repo string) BaseCheck {
	return BaseCheck{
		Repo: repo,
		Legs: FleetLegs{"go": true, "sbcl": true},
		P95:  KindP95{"fix": 1600, "fix-red": 1600, "read": 1400},
	}
}

// commitFileAt commits one file with the given body on top of HEAD and returns the
// new sha: the base a card's DONE-WHEN is held against.
func commitFileAt(t *testing.T, dir, rel, body string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, filepath.Dir(rel)), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644))
	var sha string
	for _, args := range [][]string{{"add", "."}, {"commit", "-q", "-m", "tests"}, {"rev-parse", "HEAD"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = testgit.Environ("GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %v\n%s", args, err, out)
		sha = strings.TrimSpace(string(out))
	}
	return sha
}

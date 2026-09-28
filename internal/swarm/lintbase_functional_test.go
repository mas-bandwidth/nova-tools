//go:build functional

package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests exec whole programs -- the fake runner this package builds
// (testdata/fakerunner), git, sqlite3 or sh -- so they are the functional tier's,
// not unit tests (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s and
// frugal with the machine's cores). The rest of the file's tests stay in the
// unit tier.

func TestLintPathsResolveAtBase(t *testing.T) {
	t.Parallel()

	repo, sha := baseRepo(t)
	bc := fullEvidence(repo)

	// nx-f19: `PATHS: internal/decide/entry.go`, a file that does not exist at base.
	fs := findingsFor(LintCardBase(baseCard(map[string]string{"base-sha": sha, "PATHS": "internal/decide/entry.go"}), bc), "paths-at-base")
	if len(fs) != 1 || !strings.Contains(fs[0].Excerpt, "internal/decide/entry.go") || !strings.Contains(fs[0].Excerpt, sha[:12]) {
		t.Fatalf("a PATHS file absent at base-sha is refused by name and sha, got %v", fs)
	}
	if fs[0].Line != 6 {
		t.Fatalf("the finding sits on the PATHS: line (6), got %d", fs[0].Line)
	}

	// Passes: a file that exists, a NEW _test file beside it, a glob that matches, `none`.
	for _, paths := range []string{
		"internal/decide/decide.go",
		"internal/decide/decide.go, internal/decide/entry_test.go",
		"internal/decide/*.go",
		"internal/**",
		"none",
	} {
		if fs := findingsFor(LintCardBase(baseCard(map[string]string{"base-sha": sha, "PATHS": paths}), bc), "paths-at-base"); len(fs) != 0 {
			t.Fatalf("PATHS: %s resolves at base and passes, got %v", paths, fs)
		}
	}

	// A glob that matches nothing is refused like a missing file.
	if fs := findingsFor(LintCardBase(baseCard(map[string]string{"base-sha": sha, "PATHS": "internal/nowhere/*.go"}), bc), "paths-at-base"); len(fs) != 1 || !strings.Contains(fs[0].Excerpt, "internal/nowhere/*.go") {
		t.Fatalf("a glob that matches nothing at base is refused, got %v", fs)
	}

	// No evidence is not negative evidence: a base-sha the repository does not hold,
	// no base-sha at all, and no repository each print MISSING and refuse.
	if fs := findingsFor(LintCardBase(baseCard(map[string]string{"base-sha": "39d1d6a5c918aaaaaaaaaaaaaaaaaaaaaaaaaaaa"}), bc), "paths-at-base"); len(fs) != 1 || !strings.Contains(fs[0].Excerpt, "MISSING") {
		t.Fatalf("a base-sha the repository does not hold is MISSING, never a pass, got %v", fs)
	}
	if fs := findingsFor(LintCardBase(baseCard(map[string]string{"base-sha": "\x00"}), bc), "paths-at-base"); len(fs) != 1 || !strings.Contains(fs[0].Excerpt, "MISSING") {
		t.Fatalf("a card with no base-sha is MISSING, got %v", fs)
	}
	noRepo := bc
	noRepo.Repo = ""
	if fs := findingsFor(LintCardBase(baseCard(map[string]string{"base-sha": sha}), noRepo), "paths-at-base"); len(fs) != 1 || !strings.Contains(fs[0].Excerpt, "MISSING") {
		t.Fatalf("no repository handed over is MISSING, got %v", fs)
	}
	// A repair card's PATHS are the PR's own files: an entry the PR adds resolves at
	// PR-HEAD, a PR-HEAD the repository does not hold is MISSING, and an entry at
	// neither is refused.
	prHead := addCommit(t, repo, "internal/swarm/effect.go")
	if fs := findingsFor(LintCardBase(baseCard(map[string]string{"base-sha": sha, "PATHS": "internal/swarm/effect.go", "PR-HEAD": prHead}), bc), "paths-at-base"); len(fs) != 0 {
		t.Fatalf("a file the PR adds resolves at PR-HEAD, got %v", fs)
	}
	if fs := findingsFor(LintCardBase(baseCard(map[string]string{"base-sha": sha, "PATHS": "internal/swarm/effect.go", "PR-HEAD": "ec4230ddde17b45706a2e93136f55e564d67f778"}), bc), "paths-at-base"); len(fs) != 1 || !strings.Contains(fs[0].Excerpt, "MISSING") {
		t.Fatalf("a PR-HEAD the repository does not hold is MISSING, got %v", fs)
	}
	if fs := findingsFor(LintCardBase(baseCard(map[string]string{"base-sha": sha, "PATHS": "internal/swarm/nowhere.go", "PR-HEAD": prHead}), bc), "paths-at-base"); len(fs) != 1 || !strings.Contains(fs[0].Excerpt, "PR-HEAD") {
		t.Fatalf("an entry at neither base nor PR-HEAD is refused, got %v", fs)
	}

	// The contract line's sha= stands in for a missing base-sha line when it resolves.
	if fs := findingsFor(LintCardBase([]byte("RESULT c sha="+sha[:12]+" -- t\nKIND: fix\nPATHS: internal/decide/decide.go\n"), bc), "paths-at-base"); len(fs) != 0 {
		t.Fatalf("the contract line's sha= is the base when no base-sha: line is given, got %v", fs)
	}
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
		if fs := lint(done, bc); len(fs) != 0 {
			t.Fatalf("DONE-WHEN %q names a test absent at base and lints clean, got %v", done, fs)
		}
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
		if len(fs) != 1 {
			t.Fatalf("DONE-WHEN %q names no literal test and is refused, got %v", done, fs)
		}
		if fs[0].Line != 8 {
			t.Fatalf("the finding sits on the DONE-WHEN: line (8), got %d for %q", fs[0].Line, done)
		}
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
		if len(fs) != 1 || !strings.Contains(fs[0].Excerpt, "exists at base-sha "+sha[:12]) {
			t.Fatalf("DONE-WHEN %q names a test present at base and is refused by sha, got %v", done, fs)
		}
	}

	// No DONE-WHEN at all is refused.
	if fs := lint("", bc); len(fs) != 1 || !strings.Contains(fs[0].Excerpt, "DONE-WHEN") {
		t.Fatalf("a card with no DONE-WHEN is refused, got %v", fs)
	}

	// No evidence is not negative evidence: no repo, or a base the repo lacks, is MISSING.
	none := bc
	none.Repo = ""
	if fs := lint("`go test ./internal/decide -run TestDecideConfidenceMissing` passes", none); len(fs) != 1 || !strings.Contains(fs[0].Excerpt, "MISSING") {
		t.Fatalf("no repository handed over is MISSING, never a pass, got %v", fs)
	}
	gone := findingsFor(LintCardBase(baseCard(map[string]string{
		"base-sha":  "1111111111111111111111111111111111111111",
		"DONE-WHEN": "`go test ./internal/decide -run TestDecideConfidenceMissing` passes",
	}), bc), "donewhen-test-name")
	if len(gone) != 1 || !strings.Contains(gone[0].Excerpt, "MISSING") {
		t.Fatalf("a base-sha the repository does not hold is MISSING, got %v", gone)
	}

	// The token has its remedy, so `nova-swarm lint --rules` lists it.
	if CardBaseRemedies["donewhen-test-name"] == "" {
		t.Fatal("donewhen-test-name has no remedy in CardBaseRemedies")
	}
}

// addCommit commits one new file on top of HEAD and returns the new sha, which a
// test uses as a PR head that adds the file.
func addCommit(t *testing.T, dir, rel string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, rel), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var sha string
	for _, args := range [][]string{{"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "pr"}, {"rev-parse", "HEAD"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
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
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var sha string
	for _, args := range [][]string{{"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "tests"}, {"rev-parse", "HEAD"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		sha = strings.TrimSpace(string(out))
	}
	return sha
}

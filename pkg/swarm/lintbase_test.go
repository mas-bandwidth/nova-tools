package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"os/exec"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/testgit"
)

// initBaseRepo makes a throwaway git repository under t.TempDir() holding a Go package
// with one existing test, a Python test file, and returns its directory and base commit.
func initBaseRepo(t *testing.T) (dir, sha string) {
	t.Helper()
	dir = t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = testgit.Environ()
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	git("init", "-q", "-b", "main")
	write("go.mod", "module example.com/m\n\ngo 1.21\n")
	write("pkg/decide/entry.go", "package decide\n")
	write("pkg/decide/entry_test.go", "package decide\n\nimport \"testing\"\n\nfunc TestExisting(t *testing.T) {}\n")
	write("tests/test_x.py", "def test_one():\n    pass\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	return dir, git("rev-parse", "HEAD")
}

// #2636, THE BASE CHECKS. Four rules the 2026-09-22 sprint's failed cards taught
// (rowan-new reports/failed-cards-2026-09-22.md, classes 8, 9, 11 and the LEG
// row), each with the card shape that failed and the shape that passes.

// baseCard is a coding card in the shape the 2026-09-22 cutter wrote: the
// contract line, the header block, then the STEPs. header lines replace the
// defaults of the same key; steps are appended after the header.
func baseCard(header map[string]string, steps ...string) []byte {
	order := []string{"KIND", "DEADLINE", "LEG", "base-sha", "PATHS", "DEPENDS-ON"}
	def := map[string]string{
		"KIND":       "fix",
		"DEADLINE":   "2700",
		"LEG":        "go",
		"base-sha":   "0000000000000000000000000000000000000000",
		"PATHS":      "pkg/decide/decide.go",
		"DEPENDS-ON": "-",
	}
	for k, v := range header {
		def[k] = v
	}
	var b strings.Builder
	b.WriteString("RESULT nx-f19-decide-confidence-presence sha=d4e7c1fff962 -- a missing confidence prints as zero\n")
	for _, k := range order {
		if v, ok := def[k]; ok && v != "\x00" {
			b.WriteString(k + ": " + v + "\n")
		}
	}
	for k, v := range header {
		known := false
		for _, o := range order {
			known = known || o == k
		}
		if !known {
			b.WriteString(k + ": " + v + "\n")
		}
	}
	b.WriteString("\n## Why this card exists\n\nprose.\n\n")
	for _, s := range steps {
		b.WriteString(s + "\n")
	}
	return []byte(b.String())
}

// baseCardAt is a card whose header carries a base-sha, PATHS and the lines the other base
// checks read, so a test can look at one check at a time.
func baseCardAt(sha, paths, doneWhen string) []byte {
	return []byte("RESULT: c1 sha=000000000000 tier: pro\n" +
		"base-sha: " + sha + "\n" +
		"PATHS: " + paths + "\n" +
		"KIND: fix\n" +
		"LEG: go\n" +
		"DEADLINE: 2700\n" +
		"DONE-WHEN: " + doneWhen + "\n" +
		"\n" +
		"THE TASK.\n")
}

func findingsWith(fs []CardHeaderFinding, check string) []CardHeaderFinding {
	var out []CardHeaderFinding
	for _, f := range fs {
		if f.Check == check {
			out = append(out, f)
		}
	}
	return out
}

// A PATHS entry that resolves at base-sha passes; one that names nothing there draws
// paths-at-base, the class-9 failure.
func TestLintCardBaseResolvesPathsAtBaseSha(t *testing.T) {
	t.Parallel()
	dir, sha := initBaseRepo(t)
	bc := BaseCheck{Repo: dir}

	present := LintCardBase(baseCardAt(sha, "pkg/decide/entry.go", "go test ./pkg/decide -run TestNew"), bc)
	assert.Empty(t, findingsWith(present, "paths-at-base"), "%+v", present)

	missing := LintCardBase(baseCardAt(sha, "pkg/decide/missing.go", "go test ./pkg/decide -run TestNew"), bc)
	got := findingsWith(missing, "paths-at-base")
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Excerpt, "does not exist at base-sha")
}

// A base check with no repository handed over is MISSING, never a pass.
func TestLintCardBaseWithoutARepoIsMissing(t *testing.T) {
	t.Parallel()
	_, sha := initBaseRepo(t)
	got := findingsWith(LintCardBase(baseCardAt(sha, "pkg/decide/entry.go", "go test ./pkg/decide -run TestNew"), BaseCheck{}), "paths-at-base")
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Excerpt, "MISSING")
}

// DONE-WHEN names a literal test: one absent at base can be red there (no finding); one
// that already exists at base cannot, and draws donewhen-test-name.
func TestLintCardBaseReadsDoneWhenTestNames(t *testing.T) {
	t.Parallel()
	dir, sha := initBaseRepo(t)
	bc := BaseCheck{Repo: dir}

	absent := LintCardBase(baseCardAt(sha, "pkg/decide/entry.go", "go test ./pkg/decide -run TestNew"), bc)
	assert.Empty(t, findingsWith(absent, "donewhen-test-name"), "%+v", absent)

	present := LintCardBase(baseCardAt(sha, "pkg/decide/entry.go", "go test ./pkg/decide -run TestExisting"), bc)
	got := findingsWith(present, "donewhen-test-name")
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Excerpt, "exists at base-sha")

	pytestAbsent := LintCardBase(baseCardAt(sha, "pkg/decide/entry.go", "pytest tests/test_x.py::test_missing"), bc)
	assert.Empty(t, findingsWith(pytestAbsent, "donewhen-test-name"), "%+v", pytestAbsent)

	pytestPresent := LintCardBase(baseCardAt(sha, "pkg/decide/entry.go", "pytest tests/test_x.py::test_one"), bc)
	assert.Len(t, findingsWith(pytestPresent, "donewhen-test-name"), 1)
}

// goTestTargets is the package arguments of one `go test` command: flags and their values
// are left out, import paths are kept.
func TestGoTestTargetsReadsPackagesAndNotFlags(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"./pkg/decide", "./x/..."}, goTestTargets("go test -count=1 ./pkg/decide ./x/... -timeout 600s"))
	assert.Equal(t, []string{"github.com/org/repo/x"}, goTestTargets("go test github.com/org/repo/x"))
	assert.Empty(t, goTestTargets("go test -run TestA"))
}

// pytestTargets is the file and directory arguments of one pytest command: flags, the
// values of value-taking flags, and node ids are left out.
func TestPytestTargetsReadsFilesAndNotFlags(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"tests/test_x.py"}, pytestTargets("pytest -k test_x tests/test_x.py"))
	assert.Empty(t, pytestTargets("pytest tests/test_x.py::test_one"))
	assert.Equal(t, []string{"tests/sub"}, pytestTargets("pytest --rootdir tests tests/sub"))
}

// The leg and p95 checks: a leg the table carries passes, one it does not is refused, and
// a deadline below the kind's p95 wall is refused.
func TestLintCardBaseChecksLegAndDeadline(t *testing.T) {
	t.Parallel()
	dir, sha := initBaseRepo(t)
	card := baseCardAt(sha, "pkg/decide/entry.go", "go test ./pkg/decide -run TestNew")

	good := BaseCheck{Repo: dir, Legs: FleetLegs{"go": true}, P95: KindP95{"fix": 2000}}
	fs := LintCardBase(card, good)
	assert.Empty(t, findingsWith(fs, "leg-in-fleet"), "%+v", fs)
	assert.Empty(t, findingsWith(fs, "deadline-p95"), "%+v", fs)

	bad := BaseCheck{Repo: dir, Legs: FleetLegs{"sbcl": true}, P95: KindP95{"fix": 3000}}
	fs = LintCardBase(card, bad)
	require.Len(t, findingsWith(fs, "leg-in-fleet"), 1)
	require.Len(t, findingsWith(fs, "deadline-p95"), 1)

	// No evidence handed over is MISSING, never a pass.
	fs = LintCardBase(card, BaseCheck{Repo: dir})
	assert.Contains(t, findingsWith(fs, "leg-in-fleet")[0].Excerpt, "MISSING")
	assert.Contains(t, findingsWith(fs, "deadline-p95")[0].Excerpt, "MISSING")
}

// A STEP that runs `git push` or `gh` is refused: the wall holds no credential.
func TestLintCardBaseRefusesPushSteps(t *testing.T) {
	t.Parallel()
	dir, sha := initBaseRepo(t)
	card := []byte("RESULT: c1 sha=000000000000 tier: pro\n" +
		"base-sha: " + sha + "\n" +
		"PATHS: none\n" +
		"KIND: fix\n" +
		"\n" +
		"THE TASK.\n\n" +
		"STEP 1. Run git push origin main.\n")
	fs := LintCardBase(card, BaseCheck{Repo: dir})
	require.Len(t, findingsWith(fs, "no-push-steps"), 1)
}

// deadlineSeconds reads a number of seconds, a minutes spelling and the `finish within`
// form; anything else is no deadline.
func TestDeadlineSecondsReadsTheForms(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		in   string
		want int
		ok   bool
	}{
		{"2700", 2700, true},
		{"2700s", 2700, true},
		{"45m", 2700, true},
		{"finish within 45 minutes", 2700, true},
		{"later", 0, false},
	} {
		got, ok := deadlineSeconds(tt.in)
		assert.Equal(t, tt.ok, ok, tt.in)
		if tt.ok {
			assert.Equal(t, tt.want, got, tt.in)
		}
	}
}

// ReadFleetLegs reads the fleet's leg table: one leg per line, a header row skipped, legs
// lower cased.
func TestReadFleetLegsReadsTheTable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "legs.txt")
	require.NoError(t, os.WriteFile(p, []byte("leg\nGO\nsbcl\n# comment\n\n"), 0o644))
	got, err := ReadFleetLegs(p)
	require.NoError(t, err)
	assert.Equal(t, FleetLegs{"go": true, "sbcl": true}, got)

	_, err = ReadFleetLegs(filepath.Join(dir, "absent.txt"))
	require.Error(t, err)
}

// ReadKindP95 reads `<kind> <seconds>` rows; a header row is skipped, a later bad row is
// an error.
func TestReadKindP95ReadsTheTable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "p95.txt")
	require.NoError(t, os.WriteFile(p, []byte("kind seconds\nfix 2000\nread 1500s\n"), 0o644))
	got, err := ReadKindP95(p)
	require.NoError(t, err)
	assert.Equal(t, KindP95{"fix": 2000, "read": 1500}, got)

	bad := filepath.Join(dir, "bad.txt")
	require.NoError(t, os.WriteFile(bad, []byte("fix 2000\nread notanumber\n"), 0o644))
	_, err = ReadKindP95(bad)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a number")
}

// contractSha is the `sha=<hex>` on the contract line, or "".
func TestContractShaReadsTheContractLine(t *testing.T) {
	t.Parallel()
	assert.Equal(t, testBaseSha40, contractSha("RESULT: c1 sha="+testBaseSha40+" tier: pro"))
	assert.Equal(t, "", contractSha("RESULT: c1 tier: pro"))
}

// oneLineCap caps a value at n bytes for an excerpt.
func TestOneLineCapCapsTheValue(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "short", oneLineCap("short", 10))
	assert.Equal(t, "abcdefghij...", oneLineCap("abcdefghijklmnop", 10))
}

// doneWhenTests reads every literal test a DONE-WHEN names: a go -run, a pytest node id, a
// pytest -k, and a cargo test name; a go test with no -run names none.
func TestDoneWhenTestsReadsTheRunners(t *testing.T) {
	t.Parallel()
	tests, why := doneWhenTests("go test ./pkg -run TestA")
	require.Empty(t, why)
	require.Len(t, tests, 1)
	assert.Equal(t, "go", tests[0].runner)
	assert.Equal(t, "TestA", tests[0].name)

	tests, why = doneWhenTests("pytest tests/test_x.py::test_one")
	require.Empty(t, why)
	require.Len(t, tests, 1)
	assert.Equal(t, "pytest", tests[0].runner)
	assert.Equal(t, "test_one", tests[0].name)

	tests, why = doneWhenTests("pytest -k test_one tests/")
	require.Empty(t, why)
	require.Len(t, tests, 1)
	assert.Equal(t, "test_one", tests[0].name)

	tests, why = doneWhenTests("cargo test my_test")
	require.Empty(t, why)
	require.Len(t, tests, 1)
	assert.Equal(t, "cargo", tests[0].runner)
	assert.Equal(t, "my_test", tests[0].name)

	_, why = doneWhenTests("go test ./pkg")
	assert.Contains(t, why, "no `-run")

	// The prose after a backticked command is no runner-form case: the node id is
	// the one test named, so a node id present at base is refused.
	tests, why = doneWhenTests("`pytest tests/test_x.py::test_one` passes")
	require.Empty(t, why)
	require.Len(t, tests, 1, "got %v", tests)
	assert.Equal(t, "test_one", tests[0].name)

	// An option is no runner-form target: `--rootdir=. tests` names no case "tests".
	tests, why = doneWhenTests("`pytest --rootdir=. tests -k test_one` passes")
	require.Empty(t, why)
	require.Len(t, tests, 1, "got %v", tests)
	assert.Equal(t, "test_one", tests[0].name)
}

// pytestTakesValue says whether a pytest option word is followed by its value.
func TestPytestTakesValueReadsTheOption(t *testing.T) {
	t.Parallel()
	assert.True(t, pytestTakesValue("-k"))
	assert.True(t, pytestTakesValue("--rootdir"))
	assert.False(t, pytestTakesValue("--rootdir=tests"))
	assert.False(t, pytestTakesValue("-ktest_x"))
	assert.False(t, pytestTakesValue("tests/test_x.py"))
}

// newTestFile is a literal path whose base name is a test file; a glob is never one.
func TestNewTestFileReadsTheBaseName(t *testing.T) {
	t.Parallel()
	assert.True(t, newTestFile("pkg/x_test.go"))
	assert.True(t, newTestFile("tests/test_x.py"))
	assert.False(t, newTestFile("pkg/x.go"))
	assert.False(t, newTestFile("pkg/*_test.go"))
}

// pushStepLines is every line inside a STEP that runs `git push` or `gh`; a RULES paragraph
// is not read as a command.
func TestPushStepLinesReadsTheSteps(t *testing.T) {
	t.Parallel()
	raw := []byte("STEP 1. Run git push origin main.\n" +
		"STEP 2. Do the thing.\n" +
		"gh pr merge\n" +
		"RULES.\n" +
		"no gh, no push\n")
	got := pushStepLines(raw)
	require.Len(t, got, 2)
	assert.Equal(t, 1, got[0].line)
	assert.Equal(t, 3, got[1].line)
}

func findingsFor(fs []CardHeaderFinding, check string) []CardHeaderFinding {
	var out []CardHeaderFinding
	for _, f := range fs {
		if f.Check == check {
			out = append(out, f)
		}
	}
	return out
}

func TestLintRefusesPushSteps(t *testing.T) {
	t.Parallel()

	bc := BaseCheck{Legs: FleetLegs{"go": true}, P95: KindP95{"fix": 1}}
	// The holdfix-2479 shape: STEP 6 pushes with a lease, STEP 7 posts with gh, and
	// STEP 2 asks gh for the PR state in prose backticks.
	holdfix := baseCard(nil,
		"STEP 1. Pin the base.",
		"",
		"```",
		"cd repo && git rev-parse HEAD",
		"```",
		"",
		"STEP 2. If the PR is closed (`gh api repos/mas-bandwidth/nova-tools/pulls/2479 --jq .state`), stop.",
		"",
		"STEP 6. Push to the PR's branch, leased on the head you started from:",
		"",
		"```",
		"cd repo && git push --force-with-lease=refs/heads/x:ec4230dd origin HEAD:refs/heads/x && git rev-parse HEAD",
		"```",
		"",
		"STEP 7. Post the typed REPAIR line:",
		"",
		"```",
		"gh api repos/mas-bandwidth/nova-tools/issues/2479/comments -f body='REPAIR who=swarm'",
		"```",
		"",
		"STEP 8. Write RESULT.md.",
		"",
		"RULES. Network is limited to the one leased push and read-only `gh api` GETs; no `gh`, no push.",
	)
	fs := findingsFor(LintCardBase(holdfix, bc), "no-push-steps")
	require.Len(t, fs, 3, "the holdfix card's STEP 2 gh, STEP 6 push and STEP 7 gh are three refusals, got %d: %v", len(fs), fs)
	var sawPush, sawGh bool
	for _, f := range fs {
		sawPush = sawPush || strings.Contains(f.Excerpt, "git push")
		sawGh = sawGh || strings.Contains(f.Excerpt, "gh api")
		require.NotContains(t, f.Excerpt, "RULES", "the RULES paragraph is not a STEP: %v", f)
	}
	require.True(t, sawPush, "the refusals quote the push and the gh line, got %v", fs)
	require.True(t, sawGh, "the refusals quote the push and the gh line, got %v", fs)

	// `git -c ... push` is a push, and a markdown `## STEP` heading is a STEP.
	flagged := baseCard(nil, "## STEP 1. Enter.", "", "cd repo", "", "## STEP 2. Ship.", "", "git -c user.name=Rowan push origin HEAD")
	fs = findingsFor(LintCardBase(flagged, bc), "no-push-steps")
	require.Len(t, fs, 1, "`git -c k=v push` under a `## STEP` heading is refused, got %v", fs)
	require.NotZero(t, fs[0].Line, "`git -c k=v push` under a `## STEP` heading is refused, got %v", fs)

	// Passes: a card that ends at a local commit and says `no gh, no push` only in
	// RULES and in the header, and whose STEPs mention pushing in words, not commands.
	clean := baseCard(map[string]string{"COMMIT RULE": "commit locally; the harvest pushes, never git push"},
		"STEP 1. Enter the repo: cd repo.",
		"STEP 2. Commit locally; the harvest pushes and opens the PR (a pushed branch is refused).",
		"STEP 3. Write RESULT.md.",
		"",
		"RULES. No network beyond the clone; no `gh`, no push, no PR.",
	)
	fs = findingsFor(LintCardBase(clean, bc), "no-push-steps")
	require.Empty(t, fs, "a card whose STEPs run no push and no gh passes, got %v", fs)
}

func TestLintLegInFleetTable(t *testing.T) {
	t.Parallel()

	bc := BaseCheck{Legs: FleetLegs{"go": true, "sbcl": true}, P95: KindP95{"fix": 1}}

	// nx-r1633: `LEG: lisp`, which no bench carries (the fleet's leg is sbcl).
	fs := findingsFor(LintCardBase(baseCard(map[string]string{"LEG": "lisp"}), bc), "leg-in-fleet")
	require.Len(t, fs, 1, "LEG: lisp is refused, naming the leg and the table, got %v", fs)
	require.Contains(t, fs[0].Excerpt, `"lisp"`, "LEG: lisp is refused, naming the leg and the table, got %v", fs)
	require.Contains(t, fs[0].Excerpt, "sbcl", "LEG: lisp is refused, naming the leg and the table, got %v", fs)
	fs = findingsFor(LintCardBase(baseCard(map[string]string{"LEG": "sbcl"}), bc), "leg-in-fleet")
	require.Empty(t, fs, "LEG: sbcl is in the table and passes, got %v", fs)
	fs = findingsFor(LintCardBase(baseCard(map[string]string{"LEG": "Go"}), bc), "leg-in-fleet")
	require.Empty(t, fs, "a leg is compared without case, got %v", fs)
	// LEGS: is a list, and every entry is looked up.
	fs = findingsFor(LintCardBase(baseCard(map[string]string{"LEG": "\x00", "LEGS": "go, lisp"}), bc), "leg-in-fleet")
	require.Len(t, fs, 1, "LEGS: go, lisp refuses lisp alone, got %v", fs)
	require.Contains(t, fs[0].Excerpt, `"lisp"`, "LEGS: go, lisp refuses lisp alone, got %v", fs)
	require.NotContains(t, fs[0].Excerpt, `"go"`, "LEGS: go, lisp refuses lisp alone, got %v", fs)
	fs = findingsFor(LintCardBase(baseCard(map[string]string{"LEG": "\x00"}), bc), "leg-in-fleet")
	require.Len(t, fs, 1, "a coding card with no LEG: is refused, got %v", fs)
	noTable := bc
	noTable.Legs = nil
	fs = findingsFor(LintCardBase(baseCard(nil), noTable), "leg-in-fleet")
	require.Len(t, fs, 1, "no fleet table handed over is MISSING, never a pass, got %v", fs)
	require.Contains(t, fs[0].Excerpt, "MISSING", "no fleet table handed over is MISSING, never a pass, got %v", fs)

	// The table file: one leg per line, or a TSV whose first column is the leg; a
	// header naming `leg` and `#` comments are skipped.
	p := filepath.Join(t.TempDir(), "legs.tsv")
	require.NoError(t, os.WriteFile(p, []byte("# fleet legs\nleg\tbenches\ngo\thulk,vision\nsbcl\thulk\n\nrust\n"), 0o644))
	legs, err := ReadFleetLegs(p)
	require.NoError(t, err)
	require.Len(t, legs, 3, "ReadFleetLegs read %v, want go, sbcl, rust", legs)
	require.True(t, legs["go"], "ReadFleetLegs read %v, want go, sbcl, rust", legs)
	require.True(t, legs["sbcl"], "ReadFleetLegs read %v, want go, sbcl, rust", legs)
	require.True(t, legs["rust"], "ReadFleetLegs read %v, want go, sbcl, rust", legs)
	require.False(t, legs["leg"], "ReadFleetLegs read %v, want go, sbcl, rust", legs)
}

func TestLintDeadlineAtKindP95(t *testing.T) {
	t.Parallel()

	bc := BaseCheck{Legs: FleetLegs{"go": true}, P95: KindP95{"fix-red": 1600, "read": 1526}}

	// card-read3-nova-tools-2708: DEADLINE 1500 on a kind whose DONE cards ran to 1526 s.
	fs := findingsFor(LintCardBase(baseCard(map[string]string{"KIND": "read", "DEADLINE": "1500"}), bc), "deadline-p95")
	require.Len(t, fs, 1, "DEADLINE below the kind p95 is refused on the DEADLINE: line with both numbers, got %v", fs)
	require.Contains(t, fs[0].Excerpt, "1500", "DEADLINE below the kind p95 is refused on the DEADLINE: line with both numbers, got %v", fs)
	require.Contains(t, fs[0].Excerpt, "1526", "DEADLINE below the kind p95 is refused on the DEADLINE: line with both numbers, got %v", fs)
	require.Equal(t, 3, fs[0].Line, "DEADLINE below the kind p95 is refused on the DEADLINE: line with both numbers, got %v", fs)
	// At the p95 passes (at or above), and so does above it.
	for _, d := range []string{"1526", "2700", "2700s", "finish within 30 minutes"} {
		fs = findingsFor(LintCardBase(baseCard(map[string]string{"KIND": "read", "DEADLINE": d}), bc), "deadline-p95")
		require.Empty(t, fs, "DEADLINE: %s is at or above 1526 s and passes, got %v", d, fs)
	}
	fs = findingsFor(LintCardBase(baseCard(map[string]string{"KIND": "read", "DEADLINE": "finish within 20 minutes"}), bc), "deadline-p95")
	require.Len(t, fs, 1, "finish within 20 minutes is 1200 s, below 1526, got %v", fs)
	// A kind the table does not hold is MISSING, unless the table has a `*` row.
	fs = findingsFor(LintCardBase(baseCard(map[string]string{"KIND": "mutation-kill", "DEADLINE": "9999"}), bc), "deadline-p95")
	require.Len(t, fs, 1, "a kind with no measured p95 is MISSING, never a pass, got %v", fs)
	require.Contains(t, fs[0].Excerpt, "MISSING", "a kind with no measured p95 is MISSING, never a pass, got %v", fs)
	star := BaseCheck{Legs: bc.Legs, P95: KindP95{"*": 1800}}
	fs = findingsFor(LintCardBase(baseCard(map[string]string{"KIND": "mutation-kill", "DEADLINE": "1200"}), star), "deadline-p95")
	require.Len(t, fs, 1, "the `*` row answers for a kind the table does not name, got %v", fs)
	require.Contains(t, fs[0].Excerpt, "1800", "the `*` row answers for a kind the table does not name, got %v", fs)
	for name, h := range map[string]map[string]string{
		"no DEADLINE":  {"KIND": "read", "DEADLINE": "\x00"},
		"not a number": {"KIND": "read", "DEADLINE": "soon"},
		"no KIND":      {"KIND": "\x00"},
	} {
		fs = findingsFor(LintCardBase(baseCard(h), bc), "deadline-p95")
		require.Len(t, fs, 1, "%s is refused, got %v", name, fs)
	}
	none := bc
	none.P95 = nil
	fs = findingsFor(LintCardBase(baseCard(nil), none), "deadline-p95")
	require.Len(t, fs, 1, "no p95 table handed over is MISSING, got %v", fs)
	require.Contains(t, fs[0].Excerpt, "MISSING", "no p95 table handed over is MISSING, got %v", fs)

	// The table file: `<kind> <seconds>` per line, TSV or spaces, `#` comments and a
	// header row skipped, an `s` suffix allowed.
	p := filepath.Join(t.TempDir(), "p95.tsv")
	require.NoError(t, os.WriteFile(p, []byte("# p95 of DONE walls by kind\nkind\tp95_s\nread\t1526\nfix-red 1600s\n*\t1800\n"), 0o644))
	p95, err := ReadKindP95(p)
	require.NoError(t, err)
	require.Len(t, p95, 3, "ReadKindP95 read %v", p95)
	require.Equal(t, 1526, p95["read"], "ReadKindP95 read %v", p95)
	require.Equal(t, 1600, p95["fix-red"], "ReadKindP95 read %v", p95)
	require.Equal(t, 1800, p95["*"], "ReadKindP95 read %v", p95)
	bad := filepath.Join(t.TempDir(), "bad.tsv")
	require.NoError(t, os.WriteFile(bad, []byte("read\t1526\nfix\tlater\n"), 0o644))
	_, err = ReadKindP95(bad)
	require.Error(t, err, "a row after the first whose seconds are not a number is an error, not a skipped row")
}

// TestTestDefinedAtRefusesATreeThatIsNotAFullSha holds the shape git grep is given: no
// separator keeps a tree-ish an operand on git 2.43, so the function takes only the 40 hex
// digits rev-parse prints, and never runs git for anything else.
func TestTestDefinedAtRefusesATreeThatIsNotAFullSha(t *testing.T) {
	t.Parallel()

	for _, tree := range []string{"--bogus", "-e", "", "HEAD", "abc123"} {
		_, err := testDefinedAt(t.TempDir(), tree, doneTest{runner: "go", name: "TestX"})
		require.Error(t, err, tree)
		assert.Contains(t, err.Error(), "not a full commit sha", tree)
	}
}

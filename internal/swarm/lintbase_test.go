package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		"PATHS":      "internal/decide/decide.go",
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
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %v\n%s", args, err, out)
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "internal", "decide"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "internal", "decide", "decide.go"), []byte("package decide\n"), 0o644))
	git("add", ".")
	git("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "base")
	return dir, git("rev-parse", "HEAD")
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

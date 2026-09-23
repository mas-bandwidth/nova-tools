package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	if err := os.MkdirAll(filepath.Join(dir, "internal", "decide"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "decide", "decide.go"), []byte("package decide\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "base")
	return dir, git("rev-parse", "HEAD")
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

func findingsFor(fs []CardHeaderFinding, check string) []CardHeaderFinding {
	var out []CardHeaderFinding
	for _, f := range fs {
		if f.Check == check {
			out = append(out, f)
		}
	}
	return out
}

func fullEvidence(repo string) BaseCheck {
	return BaseCheck{
		Repo: repo,
		Legs: FleetLegs{"go": true, "sbcl": true},
		P95:  KindP95{"fix": 1600, "fix-red": 1600, "read": 1400},
	}
}

func TestLintPathsResolveAtBase(t *testing.T) {
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
	prHead := addCommit(t, repo, "internal/harvest/effect.go")
	if fs := findingsFor(LintCardBase(baseCard(map[string]string{"base-sha": sha, "PATHS": "internal/harvest/effect.go", "PR-HEAD": prHead}), bc), "paths-at-base"); len(fs) != 0 {
		t.Fatalf("a file the PR adds resolves at PR-HEAD, got %v", fs)
	}
	if fs := findingsFor(LintCardBase(baseCard(map[string]string{"base-sha": sha, "PATHS": "internal/harvest/effect.go", "PR-HEAD": "ec4230ddde17b45706a2e93136f55e564d67f778"}), bc), "paths-at-base"); len(fs) != 1 || !strings.Contains(fs[0].Excerpt, "MISSING") {
		t.Fatalf("a PR-HEAD the repository does not hold is MISSING, got %v", fs)
	}
	if fs := findingsFor(LintCardBase(baseCard(map[string]string{"base-sha": sha, "PATHS": "internal/harvest/nowhere.go", "PR-HEAD": prHead}), bc), "paths-at-base"); len(fs) != 1 || !strings.Contains(fs[0].Excerpt, "PR-HEAD") {
		t.Fatalf("an entry at neither base nor PR-HEAD is refused, got %v", fs)
	}

	// The contract line's sha= stands in for a missing base-sha line when it resolves.
	if fs := findingsFor(LintCardBase([]byte("RESULT c sha="+sha[:12]+" -- t\nKIND: fix\nPATHS: internal/decide/decide.go\n"), bc), "paths-at-base"); len(fs) != 0 {
		t.Fatalf("the contract line's sha= is the base when no base-sha: line is given, got %v", fs)
	}
}

func TestLintRefusesPushSteps(t *testing.T) {
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
	if len(fs) != 3 {
		t.Fatalf("the holdfix card's STEP 2 gh, STEP 6 push and STEP 7 gh are three refusals, got %d: %v", len(fs), fs)
	}
	var sawPush, sawGh bool
	for _, f := range fs {
		sawPush = sawPush || strings.Contains(f.Excerpt, "git push")
		sawGh = sawGh || strings.Contains(f.Excerpt, "gh api")
		if strings.Contains(f.Excerpt, "RULES") {
			t.Fatalf("the RULES paragraph is not a STEP: %v", f)
		}
	}
	if !sawPush || !sawGh {
		t.Fatalf("the refusals quote the push and the gh line, got %v", fs)
	}

	// `git -c ... push` is a push, and a markdown `## STEP` heading is a STEP.
	flagged := baseCard(nil, "## STEP 1. Enter.", "", "cd repo", "", "## STEP 2. Ship.", "", "git -c user.name=Rowan push origin HEAD")
	if fs := findingsFor(LintCardBase(flagged, bc), "no-push-steps"); len(fs) != 1 || fs[0].Line == 0 {
		t.Fatalf("`git -c k=v push` under a `## STEP` heading is refused, got %v", fs)
	}

	// Passes: a card that ends at a local commit and says `no gh, no push` only in
	// RULES and in the header, and whose STEPs mention pushing in words, not commands.
	clean := baseCard(map[string]string{"COMMIT RULE": "commit locally; the harvest pushes, never git push"},
		"STEP 1. Enter the repo: cd repo.",
		"STEP 2. Commit locally; the harvest pushes and opens the PR (a pushed branch is refused).",
		"STEP 3. Write RESULT.md.",
		"",
		"RULES. No network beyond the clone; no `gh`, no push, no PR.",
	)
	if fs := findingsFor(LintCardBase(clean, bc), "no-push-steps"); len(fs) != 0 {
		t.Fatalf("a card whose STEPs run no push and no gh passes, got %v", fs)
	}
}

func TestLintLegInFleetTable(t *testing.T) {
	bc := BaseCheck{Legs: FleetLegs{"go": true, "sbcl": true}, P95: KindP95{"fix": 1}}

	// nx-r1633: `LEG: lisp`, which no bench carries (the fleet's leg is sbcl).
	fs := findingsFor(LintCardBase(baseCard(map[string]string{"LEG": "lisp"}), bc), "leg-in-fleet")
	if len(fs) != 1 || !strings.Contains(fs[0].Excerpt, `"lisp"`) || !strings.Contains(fs[0].Excerpt, "sbcl") {
		t.Fatalf("LEG: lisp is refused, naming the leg and the table, got %v", fs)
	}
	if fs := findingsFor(LintCardBase(baseCard(map[string]string{"LEG": "sbcl"}), bc), "leg-in-fleet"); len(fs) != 0 {
		t.Fatalf("LEG: sbcl is in the table and passes, got %v", fs)
	}
	if fs := findingsFor(LintCardBase(baseCard(map[string]string{"LEG": "Go"}), bc), "leg-in-fleet"); len(fs) != 0 {
		t.Fatalf("a leg is compared without case, got %v", fs)
	}
	// LEGS: is a list, and every entry is looked up.
	if fs := findingsFor(LintCardBase(baseCard(map[string]string{"LEG": "\x00", "LEGS": "go, lisp"}), bc), "leg-in-fleet"); len(fs) != 1 || !strings.Contains(fs[0].Excerpt, `"lisp"`) || strings.Contains(fs[0].Excerpt, `"go"`) {
		t.Fatalf("LEGS: go, lisp refuses lisp alone, got %v", fs)
	}
	if fs := findingsFor(LintCardBase(baseCard(map[string]string{"LEG": "\x00"}), bc), "leg-in-fleet"); len(fs) != 1 {
		t.Fatalf("a coding card with no LEG: is refused, got %v", fs)
	}
	noTable := bc
	noTable.Legs = nil
	if fs := findingsFor(LintCardBase(baseCard(nil), noTable), "leg-in-fleet"); len(fs) != 1 || !strings.Contains(fs[0].Excerpt, "MISSING") {
		t.Fatalf("no fleet table handed over is MISSING, never a pass, got %v", fs)
	}

	// The table file: one leg per line, or a TSV whose first column is the leg; a
	// header naming `leg` and `#` comments are skipped.
	p := filepath.Join(t.TempDir(), "legs.tsv")
	if err := os.WriteFile(p, []byte("# fleet legs\nleg\tbenches\ngo\thulk,vision\nsbcl\thulk\n\nrust\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	legs, err := ReadFleetLegs(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(legs) != 3 || !legs["go"] || !legs["sbcl"] || !legs["rust"] || legs["leg"] {
		t.Fatalf("ReadFleetLegs read %v, want go, sbcl, rust", legs)
	}
}

func TestLintDeadlineAtKindP95(t *testing.T) {
	bc := BaseCheck{Legs: FleetLegs{"go": true}, P95: KindP95{"fix-red": 1600, "read": 1526}}

	// card-read3-nova-tools-2708: DEADLINE 1500 on a kind whose DONE cards ran to 1526 s.
	fs := findingsFor(LintCardBase(baseCard(map[string]string{"KIND": "read", "DEADLINE": "1500"}), bc), "deadline-p95")
	if len(fs) != 1 || !strings.Contains(fs[0].Excerpt, "1500") || !strings.Contains(fs[0].Excerpt, "1526") || fs[0].Line != 3 {
		t.Fatalf("DEADLINE below the kind p95 is refused on the DEADLINE: line with both numbers, got %v", fs)
	}
	// At the p95 passes (at or above), and so does above it.
	for _, d := range []string{"1526", "2700", "2700s", "finish within 30 minutes"} {
		if fs := findingsFor(LintCardBase(baseCard(map[string]string{"KIND": "read", "DEADLINE": d}), bc), "deadline-p95"); len(fs) != 0 {
			t.Fatalf("DEADLINE: %s is at or above 1526 s and passes, got %v", d, fs)
		}
	}
	if fs := findingsFor(LintCardBase(baseCard(map[string]string{"KIND": "read", "DEADLINE": "finish within 20 minutes"}), bc), "deadline-p95"); len(fs) != 1 {
		t.Fatalf("finish within 20 minutes is 1200 s, below 1526, got %v", fs)
	}
	// A kind the table does not hold is MISSING, unless the table has a `*` row.
	if fs := findingsFor(LintCardBase(baseCard(map[string]string{"KIND": "mutation-kill", "DEADLINE": "9999"}), bc), "deadline-p95"); len(fs) != 1 || !strings.Contains(fs[0].Excerpt, "MISSING") {
		t.Fatalf("a kind with no measured p95 is MISSING, never a pass, got %v", fs)
	}
	star := BaseCheck{Legs: bc.Legs, P95: KindP95{"*": 1800}}
	if fs := findingsFor(LintCardBase(baseCard(map[string]string{"KIND": "mutation-kill", "DEADLINE": "1200"}), star), "deadline-p95"); len(fs) != 1 || !strings.Contains(fs[0].Excerpt, "1800") {
		t.Fatalf("the `*` row answers for a kind the table does not name, got %v", fs)
	}
	for name, h := range map[string]map[string]string{
		"no DEADLINE":  {"KIND": "read", "DEADLINE": "\x00"},
		"not a number": {"KIND": "read", "DEADLINE": "soon"},
		"no KIND":      {"KIND": "\x00"},
	} {
		if fs := findingsFor(LintCardBase(baseCard(h), bc), "deadline-p95"); len(fs) != 1 {
			t.Fatalf("%s is refused, got %v", name, fs)
		}
	}
	none := bc
	none.P95 = nil
	if fs := findingsFor(LintCardBase(baseCard(nil), none), "deadline-p95"); len(fs) != 1 || !strings.Contains(fs[0].Excerpt, "MISSING") {
		t.Fatalf("no p95 table handed over is MISSING, got %v", fs)
	}

	// The table file: `<kind> <seconds>` per line, TSV or spaces, `#` comments and a
	// header row skipped, an `s` suffix allowed.
	p := filepath.Join(t.TempDir(), "p95.tsv")
	if err := os.WriteFile(p, []byte("# p95 of DONE walls by kind\nkind\tp95_s\nread\t1526\nfix-red 1600s\n*\t1800\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p95, err := ReadKindP95(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(p95) != 3 || p95["read"] != 1526 || p95["fix-red"] != 1600 || p95["*"] != 1800 {
		t.Fatalf("ReadKindP95 read %v", p95)
	}
	bad := filepath.Join(t.TempDir(), "bad.tsv")
	if err := os.WriteFile(bad, []byte("read\t1526\nfix\tlater\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadKindP95(bad); err == nil {
		t.Fatal("a row after the first whose seconds are not a number is an error, not a skipped row")
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

// #3083, INSERTION 2: THE CONTROL IS THE SENTENCE. A card's DONE-WHEN names a test
// runner and a literal test that is absent at base-sha, so the test can be red there;
// an English outcome ("applied cleanly", "make preflight") is not a control and the
// card is refused before any model is spent on it.
func TestLintDoneWhenTestNameAtBase(t *testing.T) {
	repo, _ := baseRepo(t)
	commitFileAt(t, repo, "internal/decide/decide_test.go",
		"package decide\n\nimport \"testing\"\n\nfunc TestDecideExisting(t *testing.T) {}\n")
	commitFileAt(t, repo, "tests/test_decide.py", "def test_decide_existing():\n    pass\n")
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

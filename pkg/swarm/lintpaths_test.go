package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/testgit"
)

// briefTwin is a repository in the shape of a card's base: one package with its test,
// testdata and two class-test ledgers that count it, a tool, its documentation, a model,
// and the branches a brief may name. It returns the directory and the sprint/s1 tip.
func briefTwin(t *testing.T) (dir, sha string) {
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
	for name, text := range map[string]string{
		"internal/x/x.go":                              "package x\n",
		"internal/x/x_test.go":                         "package x\n\nimport \"testing\"\n\nfunc TestOld(t *testing.T) {}\n",
		"internal/x/testdata/in.txt":                   "in\n",
		"internal/ci/testdata/errcheck/internal/x.txt": "internal/x 1\n",
		"internal/ci/testdata/dead_code_allowlist.txt": "# ledger\ninternal/x 2\n",
		"internal/ci/deadcode_test.go":                 "package ci\n\nimport \"testing\"\n\nfunc TestDeadCode(t *testing.T) {}\n",
		"cmd/nova-sprint/verbs.go":                     "package main\n",
		"docs/CLI.md":                                  "# CLI\n",
		"docs/SPEC-SPRINT.md":                          "# SPEC\n",
		"tla/Model.tla":                                "---- MODULE Model ----\n====\n",
		"README.md":                                    "readme\n",
	} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(text), 0o644))
	}
	git("init", "-q", "-b", "sprint/s1")
	git("add", ".")
	git("commit", "-q", "-m", "base")
	git("branch", "tmp/try")
	git("branch", "feature/x")
	return dir, git("rev-parse", "HEAD")
}

// twinBrief is a card brief that passes every brief check against briefTwin, with the
// header lines of over replacing its own (a value "\x00" drops the line) and task its
// THE TASK paragraph when not "".
func twinBrief(repo string, over map[string]string, task string) []byte {
	order := []string{"REPO", "BASE", "KIND", "START", "PATHS", "NEW", "SHARED", "TEST", "WHO"}
	h := map[string]string{
		"REPO":   repo,
		"BASE":   "sprint/s1",
		"START":  "internal/x/x.go (Fix), internal/x (read)",
		"PATHS":  "internal/x/*.go,internal/x/*_test.go,internal/x/testdata/**,internal/ci/testdata/errcheck/internal/x.txt,internal/ci/testdata/dead_code_allowlist.txt",
		"SHARED": "internal/ci/testdata/errcheck/internal/x.txt,internal/ci/testdata/dead_code_allowlist.txt",
		"TEST":   "./internal/x TestNew",
	}
	line1 := "RESULT: c sha=0123456789ab tier: pro"
	for k, v := range over {
		if k == "1" {
			line1 = v
			continue
		}
		h[k] = v
	}
	if task == "" {
		task = "Fix internal/x/x.go so it says what README.md says."
	}
	var b strings.Builder
	b.WriteString(line1 + "\n")
	for _, k := range order {
		if v, ok := h[k]; ok && v != "\x00" {
			b.WriteString(k + ": " + v + "\n")
		}
	}
	b.WriteString("\nTHE TASK. " + task + "\n\nSTEP 1. Do it.\n\n" + ChildRulesParagraph())
	return []byte(b.String())
}

// THE BRIEF CHECKS ADD RUNS (the owner, 2026-10-05: "can we add a check for this to card
// lint?"). One brief per token that fails it and one that passes, against a twin of the
// card's base: each failing brief draws its token, the remedy table answers for it, and a
// PATHS, NEW or SHARED defect comes back with the corrected line, which, applied, passes.
// The base checks (paths-at-base, donewhen-test-name) run on the brief too, and a check
// handed no base refuses naming what is missing.
func TestAddLintRefusesABriefWhosePathsMissTheFilesItNames(t *testing.T) {
	t.Parallel()
	repo, sha := briefTwin(t)
	bb := BriefBase{Repo: repo, Sha: sha, Listed: []string{"sprint/s1"}, Friends: map[string][]string{"amy": {"pro"}, "bo": {"flash"}}}

	fs, fix := LintBrief(twinBrief(repo, nil, ""), bb)
	require.Empty(t, fs, "the twin brief passes every check")
	require.Empty(t, fix, "a passing brief has nothing to correct")

	for _, tc := range []struct {
		token, name string
		over        map[string]string
		task        string
		bb, passBB  *BriefBase
		fix         string // a corrected line the refusal carries, "" for none
		pass        map[string]string
		passTask    string
	}{
		{token: "paths-at-base", name: "a PATHS file absent at the base",
			over: map[string]string{"PATHS": "internal/x/*.go,internal/x/*_test.go,internal/x/testdata/**,internal/ci/testdata/errcheck/internal/x.txt,internal/ci/testdata/dead_code_allowlist.txt,internal/x/entry.go"},
			fix:  "NEW: internal/x/entry.go",
			pass: map[string]string{"PATHS": "internal/x/*.go,internal/x/*_test.go,internal/x/testdata/**,internal/ci/testdata/errcheck/internal/x.txt,internal/ci/testdata/dead_code_allowlist.txt,internal/x/entry.go", "NEW": "internal/x/entry.go"}},
		{token: "donewhen-test-name", name: "a TEST that exists at the base",
			over: map[string]string{"TEST": "./internal/x TestOld"},
			pass: map[string]string{"TEST": "./internal/x TestNew"}},
		{token: "donewhen-test-name", name: "a TEST line that does not read",
			over: map[string]string{"TEST": "./internal/... TestNew"},
			pass: map[string]string{"TEST": "none the change is a comment"}},
		{token: "paths-cover-named", name: "START names a file PATHS leaves out",
			over: map[string]string{"START": "internal/x/x.go (Fix), docs/CLI.md (the verb's row)"},
			fix:  "PATHS: internal/x/*.go,internal/x/*_test.go,internal/x/testdata/**,internal/ci/testdata/errcheck/internal/x.txt,internal/ci/testdata/dead_code_allowlist.txt,docs/CLI.md",
			pass: map[string]string{"START": "internal/x/x.go (Fix), docs/CLI.md (read)"}},
		{token: "paths-cover-named", name: "THE TASK names a file PATHS leaves out",
			task:     "Fix internal/x/x.go and say so in docs/SPEC-SPRINT.md.",
			fix:      "PATHS: internal/x/*.go,internal/x/*_test.go,internal/x/testdata/**,internal/ci/testdata/errcheck/internal/x.txt,internal/ci/testdata/dead_code_allowlist.txt,docs/SPEC-SPRINT.md",
			passTask: "Fix internal/x/x.go as docs/SPEC-SPRINT.md (read) says."},
		{token: "paths-cover-test", name: "PATHS covers none of the TEST package's tests",
			over: map[string]string{"PATHS": "internal/x/x.go,internal/ci/testdata/errcheck/internal/x.txt,internal/ci/testdata/dead_code_allowlist.txt"},
			fix:  "PATHS: internal/x/x.go,internal/ci/testdata/errcheck/internal/x.txt,internal/ci/testdata/dead_code_allowlist.txt,internal/x/*_test.go",
			pass: map[string]string{"PATHS": "internal/x/x.go,internal/x/x_test.go,internal/ci/testdata/errcheck/internal/x.txt,internal/ci/testdata/dead_code_allowlist.txt"}},
		{token: "paths-cover-testdata", name: "dir/*.go leaves the package's testdata out",
			over: map[string]string{"PATHS": "internal/x/*.go,internal/ci/testdata/errcheck/internal/x.txt,internal/ci/testdata/dead_code_allowlist.txt"},
			fix:  "PATHS: internal/x/*.go,internal/ci/testdata/errcheck/internal/x.txt,internal/ci/testdata/dead_code_allowlist.txt,internal/x/testdata/**",
			pass: map[string]string{"PATHS": "internal/x/*.go,internal/ci/testdata/errcheck/internal/x.txt,internal/ci/testdata/dead_code_allowlist.txt,internal/x/testdata/**"}},
		{token: "paths-cover-ledgers", name: "a ledger counting the package is off SHARED",
			over: map[string]string{"SHARED": "internal/ci/testdata/dead_code_allowlist.txt"},
			fix:  "SHARED: internal/ci/testdata/dead_code_allowlist.txt,internal/ci/testdata/errcheck/internal/x.txt",
			pass: map[string]string{"SHARED": "internal/ci/testdata/dead_code_allowlist.txt,internal/ci/testdata/errcheck/internal/x.txt"}},
		{token: "paths-cover-ledgers", name: "a ledger counting the package is off PATHS",
			over: map[string]string{"PATHS": "internal/x/*.go,internal/x/*_test.go,internal/x/testdata/**,internal/ci/testdata/dead_code_allowlist.txt"},
			fix:  "PATHS: internal/x/*.go,internal/x/*_test.go,internal/x/testdata/**,internal/ci/testdata/dead_code_allowlist.txt,internal/ci/testdata/errcheck/internal/x.txt"},
		{token: "paths-cover-docs", name: "a new flag with no CLI.md or SPEC",
			over: map[string]string{"START": "cmd/nova-sprint/verbs.go (the flag)", "PATHS": "cmd/nova-sprint/*.go", "SHARED": "\x00", "TEST": "./cmd/nova-sprint TestNew"},
			task: "Give add a --dry-run flag.",
			fix:  "SHARED: docs/CLI.md,docs/SPEC-SPRINT.md",
			pass: map[string]string{"START": "cmd/nova-sprint/verbs.go (the flag)", "PATHS": "cmd/nova-sprint/*.go,docs/CLI.md,docs/SPEC-SPRINT.md", "SHARED": "docs/CLI.md,docs/SPEC-SPRINT.md", "TEST": "./cmd/nova-sprint TestNew"}, passTask: "Give add a --dry-run flag."},
		{token: "base-is-live", name: "a temporary base",
			over: map[string]string{"BASE": "tmp/try"}},
		{token: "base-is-live", name: "a base no sprint uses",
			over: map[string]string{"BASE": "feature/x"},
			pass: map[string]string{"BASE": "feature/x"}, passBB: &BriefBase{Repo: repo, Sha: sha, Listed: []string{"sprint/s1", "feature/x"}, Friends: bb.Friends}},
		{token: "base-is-live", name: "a deleted base",
			bb: &BriefBase{Repo: repo, Sha: sha, Gone: true, Friends: bb.Friends}},
		{token: "tier-set", name: "no tier on line 1",
			over: map[string]string{"1": "RESULT: c sha=0123456789ab"},
			pass: map[string]string{"1": "RESULT: c sha=0123456789ab tier: flash"}},
		{token: "tier-set", name: "tier -",
			over: map[string]string{"1": "RESULT: c sha=0123456789ab tier: -"}},
		{token: "tla-is-frontier", name: "a model at tier pro",
			over: map[string]string{"PATHS": "internal/x/*.go,internal/x/*_test.go,internal/x/testdata/**,internal/ci/testdata/errcheck/internal/x.txt,internal/ci/testdata/dead_code_allowlist.txt,tla/Model.tla"},
			fix:  "RESULT: c sha=0123456789ab tier: frontier",
			pass: map[string]string{"1": "RESULT: c sha=0123456789ab tier: frontier", "PATHS": "internal/x/*.go,internal/x/*_test.go,internal/x/testdata/**,internal/ci/testdata/errcheck/internal/x.txt,internal/ci/testdata/dead_code_allowlist.txt,tla/Model.tla"}},
		{token: "who-serves-tier", name: "a friend who does not serve the tier",
			over: map[string]string{"WHO": "friend bo"},
			pass: map[string]string{"WHO": "friend amy"}},
		{token: "who-serves-tier", name: "any friend, and none serves the tier",
			over: map[string]string{"1": "RESULT: c sha=0123456789ab tier: heavy", "WHO": "friend"},
			pass: map[string]string{"1": "RESULT: c sha=0123456789ab tier: flash", "WHO": "friend"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			use := bb
			if tc.bb != nil {
				use = *tc.bb
			}
			fs, fix := LintBrief(twinBrief(repo, tc.over, tc.task), use)
			got := findingsFor(fs, tc.token)
			require.NotEmpty(t, got, "%s draws %s; got %v", tc.name, tc.token, fs)
			assert.NotEmpty(t, BriefRemedy(tc.token), "%s has a remedy", tc.token)
			if tc.fix != "" {
				require.Contains(t, fix, tc.fix, "the refusal carries the corrected line")
			}
			if tc.pass == nil && tc.passTask == "" {
				return
			}
			pb := bb
			if tc.passBB != nil {
				pb = *tc.passBB
			}
			fs, fix = LintBrief(twinBrief(repo, tc.pass, tc.passTask), pb)
			assert.Empty(t, fs, "the corrected brief passes")
			assert.Empty(t, fix, "the corrected brief has nothing left to correct")
		})
	}

	t.Run("the corrected lines, applied, pass", func(t *testing.T) {
		bad := twinBrief(repo, map[string]string{"PATHS": "internal/x/x.go", "SHARED": "\x00"}, "")
		fs, fix := LintBrief(bad, bb)
		require.NotEmpty(t, fs)
		require.NotEmpty(t, fix)
		fixed := string(bad)
		for _, l := range fix {
			key, _, _ := strings.Cut(l, ":")
			if !strings.Contains(fixed, "\n"+key+": ") {
				fixed = strings.Replace(fixed, "\nTEST: ", "\n"+l+"\nTEST: ", 1)
				continue
			}
			lines := strings.Split(fixed, "\n")
			for i, x := range lines {
				if strings.HasPrefix(x, key+": ") {
					lines[i] = l
				}
			}
			fixed = strings.Join(lines, "\n")
		}
		fs, fix = LintBrief([]byte(fixed), bb)
		assert.Empty(t, fs, "one step: the corrected lines answer every PATHS and SHARED finding\n%s", fixed)
		assert.Empty(t, fix)
	})

	t.Run("no evidence is not negative evidence", func(t *testing.T) {
		fs, _ := LintBrief(twinBrief(repo, nil, ""), BriefBase{Missing: "the clone of " + repo + " could not be fetched", Friends: bb.Friends})
		for _, token := range []string{"paths-at-base", "donewhen-test-name", "paths-cover-testdata", "paths-cover-ledgers", "base-is-live"} {
			got := findingsFor(fs, token)
			if assert.NotEmpty(t, got, "%s refuses with no base", token) {
				assert.Contains(t, got[0].Excerpt, "MISSING: the clone of", token)
			}
		}
		fs, _ = LintBrief(twinBrief(repo, map[string]string{"WHO": "friend amy"}, ""), BriefBase{Repo: repo, Sha: sha, Listed: bb.Listed})
		if got := findingsFor(fs, "who-serves-tier"); assert.NotEmpty(t, got) {
			assert.Contains(t, got[0].Excerpt, "MISSING")
		}
	})

	t.Run("a brief with no PATHS, or no base, is held to what it carries", func(t *testing.T) {
		fs, fix := LintBrief([]byte("RESULT: c\nREPO: "+repo+"\n\nTHE TASK. Anything.\n"), bb)
		assert.Empty(t, fs, "no PATHS: no card brief")
		assert.Empty(t, fix)
		fs, _ = LintBrief(twinBrief(repo, map[string]string{"BASE": "\x00"}, ""), BriefBase{Friends: bb.Friends})
		assert.Empty(t, fs, "no BASE: the tree checks do not run, and the rest pass")
	})
}

// A LEDGER CARD KEEPS ITS CLASS TEST (the seat, 2026-10-07): a card nova-card generate cuts
// from a ledger names the ledger's class test, which is green at the base by construction,
// and its proof is the ledger shrinking. donewhen-test-name lets that existing test pass only
// when both hold: the brief says KIND: ledger, and its TEST is under internal/ci/. Without
// the marker, or for a test outside internal/ci/, an existing test is still refused.
func TestALedgerCardsExistingClassTestPassesDonewhenOnlyWithItsMarker(t *testing.T) {
	t.Parallel()
	repo, sha := briefTwin(t)
	bb := BriefBase{Repo: repo, Sha: sha, Listed: []string{"sprint/s1"}}
	ledger := map[string]string{
		"START": "internal/x/x.go (Fix), internal/ci (read)",
		"PATHS": "internal/x/*.go,internal/x/*_test.go,internal/x/testdata/**,internal/ci/*.go,internal/ci/*_test.go,internal/ci/testdata/errcheck/internal/x.txt,internal/ci/testdata/dead_code_allowlist.txt",
		"TEST":  "internal/ci TestDeadCode",
	}
	with := func(over map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range ledger {
			out[k] = v
		}
		for k, v := range over {
			out[k] = v
		}
		return out
	}
	for _, tc := range []struct {
		name   string
		over   map[string]string
		refuse bool
	}{
		{name: "KIND: ledger and an existing class test under internal/ci", over: map[string]string{"KIND": LedgerKind}},
		{name: "the marker in another case, the package written ./", over: map[string]string{"KIND": "Ledger", "TEST": "./internal/ci TestDeadCode"}},
		{name: "no KIND: line", refuse: true},
		{name: "another kind", over: map[string]string{"KIND": "fix-red"}, refuse: true},
		{name: "KIND: ledger and an existing test outside internal/ci", over: map[string]string{"KIND": LedgerKind, "TEST": "./internal/x TestOld"}, refuse: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs, _ := LintBrief(twinBrief(repo, with(tc.over), ""), bb)
			got := findingsFor(fs, "donewhen-test-name")
			if !tc.refuse {
				assert.Empty(t, got, "a ledger card's class test exists at the base and passes")
				return
			}
			require.Len(t, got, 1, "%v", fs)
			assert.Contains(t, got[0].Excerpt, "exists in", "an existing test with no ledger marker is still refused")
			assert.Contains(t, got[0].Excerpt, "so it cannot be red there")
		})
	}
}

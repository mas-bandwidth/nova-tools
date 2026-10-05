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

// twinBase is a throwaway repository in the shape of nova-tools at a sprint base: a
// tool with its CLI reference and SPEC, a package with testdata, a class-test ledger
// keyed on the tool's functions, a TLA+ model and one test that exists already. It
// returns the evidence the brief checks read: the repository and the tip's sha.
func twinBase(t *testing.T) BriefBase {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	for name, text := range map[string]string{
		"go.mod":                                                "module example.com/twin\n",
		"cmd/nova-sprint/verbs.go":                              "package main\n",
		"cmd/nova-sprint/verbs_test.go":                         "package main\n",
		"internal/swarm/lintbase.go":                            "package swarm\n",
		"internal/swarm/old_test.go":                            "package swarm\n\nfunc TestOld(t *testing.T) {}\n",
		"internal/swarm/testdata/a.txt":                         "fixture\n",
		"internal/cardhdr/cardhdr.go":                           "package cardhdr\n",
		"internal/ci/testdata/remedy/cmd/nova-sprint.txt":       "# ledger\ncmd/nova-sprint/verbs.go:app.cmdAdd:exit-2 1 why\n",
		"internal/ci/testdata/staticcheck/internal/cardhdr.txt": "# counts\ninternal/cardhdr:ST1005 1 measured\n",
		"docs/CLI.md":                                           "# cli\n",
		"docs/SPEC-SPRINT.md":                                   "# spec\n",
		"tla/Land.tla":                                          "---- MODULE Land ----\n====\n",
	} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(text), 0o644))
	}
	git("init", "-q")
	git("add", ".")
	git("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "base")
	return BriefBase{Repo: dir, Sha: git("rev-parse", "HEAD")}
}

// twinBrief is a coding brief that passes every brief check against twinBase; each key of
// header replaces the line of that key ("" drops it), and task replaces THE TASK.
func twinBrief(header map[string]string, task string) []byte {
	keys := []string{"REPO", "BASE", "START", "PATHS", "SHARED", "TEST", "WHO"}
	def := map[string]string{
		"REPO":   "mas-bandwidth/nova-tools",
		"BASE":   "sprint/mechanical",
		"START":  "cmd/nova-sprint/verbs.go (lintBrief), internal/swarm/lintbase.go (read)",
		"PATHS":  "cmd/nova-sprint/verbs.go,cmd/nova-sprint/addlint*_test.go,internal/ci/testdata/remedy/cmd/nova-sprint.txt,docs/CLI.md,docs/SPEC-SPRINT.md",
		"SHARED": "internal/ci/testdata/remedy/cmd/nova-sprint.txt,docs/CLI.md,docs/SPEC-SPRINT.md",
		"TEST":   "./cmd/nova-sprint TestAddLintNew",
	}
	line1 := "card.w1: add lints the brief, tier: heavy"
	for k, v := range header {
		if k == "line1" {
			line1 = v
			continue
		}
		def[k] = v
	}
	var b strings.Builder
	b.WriteString(line1 + "\n")
	for _, k := range keys {
		if v := def[k]; v != "" {
			b.WriteString(k + ": " + v + "\n")
		}
	}
	if task == "" {
		task = "THE TASK. Make add run the checks; docs/SPEC-SPRINT.md names every token."
	}
	b.WriteString("\n" + task + "\n\nSTEP 1. Write the red test.\n")
	return []byte(b.String())
}

func briefFindingsFor(fs []BriefFinding, check string) []BriefFinding {
	var out []BriefFinding
	for _, f := range fs {
		if f.Check == check {
			out = append(out, f)
		}
	}
	return out
}

// TestAddLintRefusesABriefWhosePathsMissTheFilesItNames holds each brief check of
// nova-sprint add (docs/SPEC-SPRINT.md, card lint) to the defects of epoch 15's sprint
// log: per token a brief that fails it and one that passes, on a twin repository at its
// BASE tip; a PATHS or SHARED refusal carries the corrected line; no evidence refuses.
func TestAddLintRefusesABriefWhosePathsMissTheFilesItNames(t *testing.T) {
	t.Parallel()
	bb := twinBase(t)
	friends := map[string][]string{"amy": {"flash", "pro"}, "bo": {"frontier"}, "cy": nil}
	lint := func(raw []byte) []BriefFinding {
		b := bb
		b.Friends = friends
		return LintBrief(raw, b)
	}

	require.True(t, BriefChecked(twinBrief(nil, "")), "a brief naming REPO: and PATHS: is a coding brief")
	require.False(t, BriefChecked(twinBrief(map[string]string{"PATHS": "none"}, "")), "PATHS: none names no scope to hold")
	require.False(t, BriefChecked(twinBrief(map[string]string{"REPO": ""}, "")), "a brief naming no repository has no base to read")
	require.Empty(t, lint(twinBrief(nil, "")), "the passing brief passes every check")

	for _, c := range []struct {
		name, check string
		fail        []byte
		want        []string // in the excerpt
		fix         string   // the corrected line, "" for none
		pass        []byte
	}{
		{"a file START names to change", CheckPathsCoverNamed,
			twinBrief(map[string]string{"START": "cmd/nova-sprint/verbs.go, internal/cardhdr (the reader)"}, ""),
			[]string{"internal/cardhdr"}, "PATHS: cmd/nova-sprint/verbs.go,cmd/nova-sprint/addlint*_test.go,internal/ci/testdata/remedy/cmd/nova-sprint.txt,docs/CLI.md,docs/SPEC-SPRINT.md,internal/cardhdr",
			twinBrief(map[string]string{"START": "cmd/nova-sprint/verbs.go, internal/cardhdr (read: the reader)"}, "")},
		{"a file THE TASK names", CheckPathsCoverNamed,
			twinBrief(nil, "THE TASK. Wire it through internal/swarm/lintbase.go and docs/CLI.md."),
			[]string{"internal/swarm/lintbase.go"}, "PATHS: cmd/nova-sprint/verbs.go,cmd/nova-sprint/addlint*_test.go,internal/ci/testdata/remedy/cmd/nova-sprint.txt,docs/CLI.md,docs/SPEC-SPRINT.md,internal/swarm/lintbase.go",
			twinBrief(nil, "THE TASK. Wire it through docs/CLI.md; a path not in the tree (dir/*.go, tla/) is prose.")},
		{"the TEST package's tests", CheckPathsCoverTest,
			twinBrief(map[string]string{"TEST": "./internal/swarm TestNew"}, ""),
			[]string{"./internal/swarm"}, "PATHS: cmd/nova-sprint/verbs.go,cmd/nova-sprint/addlint*_test.go,internal/ci/testdata/remedy/cmd/nova-sprint.txt,docs/CLI.md,docs/SPEC-SPRINT.md,internal/swarm/*_test.go",
			twinBrief(map[string]string{"TEST": "./internal/swarm TestNew", "PATHS": "cmd/nova-sprint/verbs.go,internal/swarm/new_test.go,internal/ci/testdata/remedy/cmd/nova-sprint.txt,docs/CLI.md,docs/SPEC-SPRINT.md"}, "")},
		{"a package's testdata", CheckPathsCoverTestdata,
			twinBrief(map[string]string{"PATHS": "internal/swarm/*.go"}, ""),
			[]string{"internal/swarm/testdata/**"}, "PATHS: internal/swarm/*.go,internal/swarm/testdata/**",
			twinBrief(map[string]string{"START": "internal/swarm/lintbase.go", "PATHS": "internal/swarm/*.go,internal/swarm/testdata/**", "SHARED": "", "TEST": "./internal/swarm TestNew"}, "THE TASK. Change the package.")},
		{"a ledger keyed on the package's functions", CheckPathsCoverLedgers,
			twinBrief(map[string]string{"PATHS": "cmd/nova-sprint/verbs.go,cmd/nova-sprint/addlint*_test.go,docs/CLI.md,docs/SPEC-SPRINT.md", "SHARED": "docs/CLI.md,docs/SPEC-SPRINT.md"}, ""),
			[]string{"internal/ci/testdata/remedy/cmd/nova-sprint.txt"}, "PATHS: cmd/nova-sprint/verbs.go,cmd/nova-sprint/addlint*_test.go,docs/CLI.md,docs/SPEC-SPRINT.md,internal/ci/testdata/remedy/cmd/nova-sprint.txt | SHARED: docs/CLI.md,docs/SPEC-SPRINT.md,internal/ci/testdata/remedy/cmd/nova-sprint.txt",
			// a ledger of counts (staticcheck) keys on no function: the cardhdr card needs none
			twinBrief(map[string]string{"START": "internal/cardhdr/cardhdr.go", "PATHS": "internal/cardhdr/*.go", "SHARED": "", "TEST": "./internal/cardhdr TestNew"}, "THE TASK. Change the reader.")},
		{"the tool's CLI reference and SPEC, on SHARED", CheckPathsCoverDocs,
			twinBrief(map[string]string{"SHARED": "internal/ci/testdata/remedy/cmd/nova-sprint.txt,docs/CLI.md"}, ""),
			[]string{"docs/SPEC-SPRINT.md"}, "SHARED: internal/ci/testdata/remedy/cmd/nova-sprint.txt,docs/CLI.md,docs/SPEC-SPRINT.md",
			twinBrief(nil, "")},
		{"a card's attempt branch as BASE", CheckBaseIsLive,
			twinBrief(map[string]string{"BASE": "sprint/other-card.w2.g3.e15"}, ""),
			[]string{"attempt branch"}, "", twinBrief(map[string]string{"BASE": "sprint/mechanical-2026-10-02"}, "")},
		{"a personal branch as BASE", CheckBaseIsLive,
			twinBrief(map[string]string{"BASE": "glenn/try-this"}, ""),
			[]string{"personal or temporary"}, "", twinBrief(map[string]string{"BASE": "main"}, "")},
		{"no tier", CheckTierSet,
			twinBrief(map[string]string{"line1": "card.w1: add lints the brief"}, ""),
			[]string{"no tier"}, "", twinBrief(map[string]string{"line1": "card.w1: add lints the brief, tier: flash"}, "")},
		{"a TLA+ model on heavy", CheckTLAIsFrontier,
			twinBrief(map[string]string{"PATHS": "tla/Land.tla,cmd/nova-sprint/verbs.go,cmd/nova-sprint/addlint*_test.go,internal/ci/testdata/remedy/cmd/nova-sprint.txt,docs/CLI.md,docs/SPEC-SPRINT.md"}, ""),
			[]string{"tla/Land.tla", "heavy"}, "",
			twinBrief(map[string]string{"line1": "card.w1: model the land, tier: frontier", "PATHS": "tla/Land.tla,cmd/nova-sprint/verbs.go,cmd/nova-sprint/addlint*_test.go,internal/ci/testdata/remedy/cmd/nova-sprint.txt,docs/CLI.md,docs/SPEC-SPRINT.md"}, "")},
		{"a named friend who cannot serve the tier", CheckWhoServesTier,
			twinBrief(map[string]string{"WHO": "friend amy"}, ""),
			[]string{"amy", "heavy"}, "", twinBrief(map[string]string{"WHO": "friend bo", "line1": "card.w1: x, tier: frontier"}, "")},
		{"no friend serves the tier", CheckWhoServesTier,
			twinBrief(map[string]string{"WHO": "friend"}, ""),
			[]string{"no friend"}, "", twinBrief(map[string]string{"WHO": "friend", "line1": "card.w1: x, tier: pro"}, "")},
		{"a PATHS file absent at BASE", "paths-at-base",
			twinBrief(map[string]string{"PATHS": "cmd/nova-sprint/verbs.go,cmd/nova-sprint/addlint*_test.go,internal/ci/testdata/remedy/cmd/nova-sprint.txt,docs/CLI.md,docs/SPEC-SPRINT.md,internal/decide/entry.go"}, ""),
			[]string{"internal/decide/entry.go", bb.Sha[:12]}, "",
			// a glob of new files in a directory the base holds is a card's new file
			twinBrief(map[string]string{"PATHS": "cmd/nova-sprint/verbs.go,cmd/nova-sprint/addlint*.go,cmd/nova-sprint/addlint*_test.go,internal/ci/testdata/remedy/cmd/nova-sprint.txt,docs/CLI.md,docs/SPEC-SPRINT.md"}, "")},
		{"a TEST that exists at BASE", "donewhen-test-name",
			twinBrief(map[string]string{"TEST": "./internal/swarm TestOld", "PATHS": "cmd/nova-sprint/verbs.go,internal/swarm/*_test.go,internal/ci/testdata/remedy/cmd/nova-sprint.txt,docs/CLI.md,docs/SPEC-SPRINT.md"}, ""),
			[]string{"TestOld", "cannot be red"}, "",
			twinBrief(map[string]string{"TEST": "none the change is a doc"}, "")},
	} {
		fs := briefFindingsFor(lint(c.fail), c.check)
		if assert.Len(t, fs, 1, "%s is refused under %s, got %v", c.name, c.check, lint(c.fail)) {
			for _, w := range c.want {
				assert.Contains(t, fs[0].Excerpt, w, c.name)
			}
			assert.Equal(t, c.fix, fs[0].Fix, "%s: the corrected line", c.name)
			assert.NotEmpty(t, CardBaseRemedies[c.check], "%s has its remedy beside the base checks'", c.check)
		}
		assert.Empty(t, lint(c.pass), "%s: the brief that fixes it passes every check", c.name)
	}

	// NO EVIDENCE IS NOT NEGATIVE EVIDENCE: no clone, no BASE line, a base origin does not
	// hold, and an unread friends table each refuse, naming what is missing.
	for name, c := range map[string]struct {
		raw []byte
		bb  BriefBase
	}{
		"no clone":  {twinBrief(nil, ""), BriefBase{Missing: "the lander keeps no clone"}},
		"no BASE":   {twinBrief(map[string]string{"BASE": ""}, ""), bb},
		"base gone": {twinBrief(nil, ""), BriefBase{Repo: bb.Repo, Gone: "origin has no branch sprint/mechanical"}},
	} {
		fs := briefFindingsFor(LintBrief(c.raw, c.bb), "paths-at-base")
		if assert.Len(t, fs, 1, name) {
			assert.Contains(t, fs[0].Excerpt, "MISSING", name)
			for _, check := range TreeChecks {
				assert.Contains(t, fs[0].Excerpt, check, "%s: the refusal names every check it could not run", name)
			}
		}
	}
	gone := briefFindingsFor(LintBrief(twinBrief(nil, ""), BriefBase{Repo: bb.Repo, Gone: "origin has no branch sprint/mechanical"}), CheckBaseIsLive)
	require.Len(t, gone, 1)
	assert.Contains(t, gone[0].Excerpt, "deleted")
	unread := briefFindingsFor(LintBrief(twinBrief(map[string]string{"WHO": "friend amy"}, ""), BriefBase{Repo: bb.Repo, Sha: bb.Sha, FriendsMissing: "the store is down"}), CheckWhoServesTier)
	require.Len(t, unread, 1)
	assert.Contains(t, unread[0].Excerpt, "MISSING")
}

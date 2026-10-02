package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What a cold reader of this tool, an AI with only the help, acts on.

// Every verb that parses its own flags lists each one with what it wants, and quotes its
// own exit codes, never the wrapped command's paragraph (ledger D2, X7, X9).
func TestEachVerbsHelpListsItsFlagsAndItsOwnExitCodes(t *testing.T) {
	t.Parallel()
	for verb, doc := range verbDocs {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			help := novaSandbox.Do(t, append(strings.Fields(verb), "-h")...).Exit(0).Stdout
			assert.Contains(t, help, "\n"+exitsLabel+"\n"+verb+": "+doc.exits+"\n")
			assert.NotContains(t, help, "passed through", "a verb that wraps nothing quotes the wrapped command's codes")
			for _, f := range doc.flags {
				assert.Contains(t, help, "\n  --"+f[0]+" ", "the flag is listed with what it wants")
			}
		})
	}
}

// What a verb's help lists is what its parser takes: a listed flag is never refused as
// unknown, so the help cannot promise a flag the verb does not have.
func TestEveryListedFlagIsOneTheVerbParses(t *testing.T) {
	t.Parallel()
	parsers := map[string]func(args []string) []string{
		"policy":       func(a []string) []string { return texts(parseVerb("policy", a).bad) },
		"probe":        func(a []string) []string { return texts(parseVerb("probe", a).bad) },
		"worktree":     func(a []string) []string { return parseWorktree(a).unknown },
		"egress plan":  func(a []string) []string { return texts(parseEgress(a).bad) },
		"egress apply": func(a []string) []string { return texts(parseEgress(a).bad) },
		"egress check": func(a []string) []string { return texts(parseEgress(a).bad) },
		"egress drop":  func(a []string) []string { return texts(parseEgress(a).bad) },
	}
	for verb, parse := range parsers {
		for _, f := range verbDocs[verb].flags {
			args := []string{"--" + f[0]}
			if strings.Contains(f[1], "`") {
				args = append(args, "none")
			}
			for _, bad := range parse(args) {
				assert.NotContains(t, bad, "unknown flag", "%s lists --%s and refuses it", verb, f[0])
			}
		}
	}
}

// check, policy and probe answer --json with one object on stdout, a refusal included,
// and the bare wrap refuses it as another verb's flag (ledger X1).
func TestTheInspectionVerbsAnswerJSON(t *testing.T) {
	t.Parallel()
	j := newJob(t)
	type result struct {
		Result struct {
			Verb, Status, Remedy string
			Exit                 int
			Why                  []string
		}
		Facts   map[string]any
		Items   []struct{ Kind string }
		Notes   []string
		Payload string
	}
	for _, c := range []struct {
		name, verb, status string
		args               []string
		exit               int
	}{
		{"check", "check", "ok", []string{"check", "--json"}, 0},
		{"check refused", "check", "refused", []string{"check", "--json", "--bogus"}, 2},
		{"policy", "policy", "ok", []string{"policy", "--json", "--write", j.write, "--secret", j.secret}, 0},
		{"policy refused", "policy", "refused", []string{"policy", "--json", "--write", filepath.Join(j.base, "absent")}, 2},
		{"probe refused", "probe", "refused", []string{"probe", "--json"}, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			res := j.run(t, c.args...)
			assert.Equal(t, c.exit, res.Code)
			assert.Empty(t, res.Stderr, "--json says everything on stdout")
			r := testkit.JSON[result](t, res.Stdout)
			assert.Equal(t, c.verb, r.Result.Verb)
			assert.Equal(t, c.status, r.Result.Status)
			assert.Equal(t, c.exit, r.Result.Exit)
			if c.status != "ok" {
				assert.NotEmpty(t, r.Result.Why)
				assert.Equal(t, "nova-sandbox "+c.verb+" -h", r.Result.Remedy)
			}
			if c.name == "policy" {
				assert.EqualValues(t, len(r.Payload), r.Facts["bytes"], "the payload is the policy the bytes count")
				assert.Len(t, r.Notes, 1, "--secret is probe's, and policy says it ignored it")
			}
		})
	}
	j.run(t, "--write", j.write, "--json", "--", "/bin/sh", "-c", "true").Exit(125).Err("--json is not a flag of the bare form")
}

// A probe that would build its wall without a flag it was given refuses that flag: a
// probe of a different wall answers the wrong question.
func TestProbeRefusesAFlagItsWallWouldNotCarry(t *testing.T) {
	t.Parallel()
	j := newJob(t)
	j.run(t, "probe", "--write", j.write, "--cwd", j.write, "--net-allow", "127.0.0.1:1").Exit(2).
		Err("--cwd is not a flag of probe", "--net-allow is not a flag of probe")
	j.run(t, "policy", "--write", j.write, "--max", "3").Exit(0).Err("POLICY NOTE --max is probe's flag and is ignored here")
}

// The bare command is a refusal in the tool's own grammar (ledger X4).
func TestTheBareCommandRefusesInTheToolsGrammar(t *testing.T) {
	t.Parallel()
	r := novaSandbox.Do(t).Exit(2).Err("; run: nova-sandbox help\n")
	assert.Empty(t, r.Stdout, r)
	assert.True(t, strings.HasPrefix(r.Stderr, "SANDBOX REFUSED reason=no_command: no arguments;"), r)
}

// SANDBOX OK says the wall is up and the command is starting; the last stderr line says
// how it ended, with the command's own status, so a 125 the command returned is told from
// the tool's refusal, which prints no DONE (ledger D6). stdout is the command's alone.
func TestTheBareWrapEndsWithTheCommandsOwnStatus(t *testing.T) {
	t.Parallel()
	needDarwin(t)
	j := newJob(t)
	for _, status := range []string{"0", "7", "125"} {
		r := j.wrapped(t, "echo out; exit "+status)
		assert.Equal(t, status, strconv.Itoa(r.Code))
		assert.Equal(t, "out\n", r.Stdout, "stdout carries the command's output and nothing of the tool's")
		lines := strings.Split(strings.TrimSuffix(r.Stderr, "\n"), "\n")
		assert.Equal(t, "SANDBOX DONE exit="+status+" cmd=sh", lines[len(lines)-1])
	}
	// A refusal is the tool's, and no command ran to be done.
	j.run(t, "--write", filepath.Join(j.base, "absent"), "--", "/bin/sh", "-c", "true").Exit(125).NotErr("SANDBOX DONE")
}

// A HOME outside every --write is refused (rule 9: never defaulted), and the refusal names
// the command that answers it, the same invocation with a data home made inside the first
// --write (ledger D3).
func TestAHomeOutsideRefusalNamesTheCommandThatAnswersIt(t *testing.T) {
	t.Parallel()
	j := newJob(t)
	w := filepath.Join(j.base, "a b")
	require.NoError(t, os.MkdirAll(w, 0o755))
	outside := []string{"HOME=" + j.outside, "PATH=/usr/bin:/bin"}
	home := "'" + filepath.Join(w, "home") + "'"
	for _, argv := range [][]string{
		{"--write", w, "--", "/bin/sh", "-c", "true"},
		{"probe", "--write", w},
		{"policy", "--write", w},
	} {
		r := j.runEnv(t, outside, argv...)
		words := make([]string, len(argv))
		for i, a := range argv {
			words[i] = shellWord(a)
		}
		want := "; run: mkdir -p " + home + " && HOME=" + home + " nova-sandbox " + strings.Join(words, " ") + "\n"
		assert.Contains(t, r.Stderr, "home_outside")
		assert.True(t, strings.HasSuffix(r.Stderr, want), "%s\nwant the line to end %q", r.Stderr, want)
	}
	// the command it names runs: policy with that HOME is accepted
	require.NoError(t, os.MkdirAll(filepath.Join(w, "home"), 0o755))
	j.runEnv(t, []string{"HOME=" + filepath.Join(w, "home"), "PATH=/usr/bin:/bin"}, "policy", "--write", w).Exit(0)
	assert.Equal(t, `'it'\''s'`, shellWord("it's"))
	assert.Equal(t, "/a/b", shellWord("/a/b"))
}

// egress plan names every problem its flags have before it resolves a name: a plan with no
// --run and no selector is refused at once, every problem in the one run, and no
// resolution starts (the resolve step's line never prints), so no resolver is waited on.
func TestEgressPlanNamesItsFlagProblemsBeforeResolving(t *testing.T) {
	t.Parallel()
	novaSandbox.Do(t, "egress", "plan", "--policy", filepath.Join("..", "..", "infra", "image", "egress.txt"),
		"--resolver", "10.9.0.53", "--out", filepath.Join(t.TempDir(), "plan.nft")).Exit(2).
		Err("EGRESS REFUSED reason=no_name", "EGRESS REFUSED reason=no_selector", "EGRESS REFUSED reason=bad_model_host").
		NotErr("EGRESS STEP name=resolve", "resolve_failed")
}

// run's refusal of a missing -- names its remedy once, on the remedy line, not again
// inside the refusal.
func TestRunNamesItsRemedyOnce(t *testing.T) {
	t.Parallel()
	r := withEnv(runVerb, []string{"PATH=/usr/bin:/bin"}).Do(t, "--name", "x", "--size", "1g").Exit(125).Err("reason=no_command: no --")
	assert.Equal(t, 1, strings.Count(r.Stderr, remedyFor(runtime.GOOS)), r)
}

// worktree names every problem in one run, an unknown flag among them, and never ignores
// a flag it does not have.
func TestWorktreeNamesEveryProblemInOneRun(t *testing.T) {
	t.Parallel()
	r := novaSandbox.Do(t, "worktree", "--repoo", "x").Exit(2).Err("WORKTREE REFUSED reason=bad_flag: unknown flag --repoo",
		"WORKTREE REFUSED reason=bad_pr", "WORKTREE REFUSED reason=bad_repo", "WORKTREE REFUSED reason=bad_scratch")
	assert.True(t, strings.HasSuffix(r.Stderr, worktreeRemedy+"\n"), r)
}

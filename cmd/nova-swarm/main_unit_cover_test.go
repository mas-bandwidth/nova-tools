package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// The v1.4 unit-tier coverage table left eight helpers of cmd/nova-swarm/main.go at
// 0.0% and cmdVerify at 16.4%: the native verdict and its line suffixes, the two
// pure reads nativeVerdictWhy leans on, the process exit it folds the child's rc
// into, and verify's whole body past its flag refusals. These tests reach each one
// through its own seam. Every case is pure or reads a file a test wrote under its
// own t.TempDir(): no clock, no socket, no subprocess, no store.
//
// cmdNative and main launch a harness and belong to the functional tier; they are
// deliberately not reached here.

// TestSwarmMainCoverNativeVerdictWhy pins the word and why= of the NATIVE line:
// a lost provider body is unknown-acceptance before anything on disk is read; a
// silent harness is harness-silent; a harness that answered with no result in the
// job is no-result; a job with a result takes its rc; rc 0 is OK.
func TestSwarmMainCoverNativeVerdictWhy(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		res       nativeRunResult
		result    string
		verdict   string
		why       string
		noJobFile bool
	}{
		"lost is unknown-acceptance": {
			res: nativeRunResult{lost: true}, verdict: "INCOMPLETE", why: "unknown-acceptance", noJobFile: true},
		"empty harness is silent": {
			res: nativeRunResult{}, verdict: "INCOMPLETE", why: "harness-silent", noJobFile: true},
		"named silent harness": {
			res: nativeRunResult{harness: "silent"}, verdict: "INCOMPLETE", why: "harness-silent", noJobFile: true},
		"harness ok, no result": {
			res: nativeRunResult{harness: "ok"}, verdict: "INCOMPLETE", why: "no-result", noJobFile: true},
		"result present, rc 3": {
			res: nativeRunResult{harness: "ok", rc: 3}, result: "result: reviewed\nverdict: not-done\n", verdict: "INCOMPLETE", why: "rc"},
		"result present, rc 0 is OK": {
			res: nativeRunResult{harness: "ok", rc: 0}, result: "result: reviewed\nverdict: ok\n", verdict: "OK", why: ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			job := t.TempDir()
			tc.res.job = job
			if !tc.noJobFile {
				write(t, filepath.Join(job, "RESULT.md"), tc.result)
			}
			verdict, why := nativeVerdictWhy(tc.res)
			assert.Equal(t, tc.verdict, verdict, "the verdict word")
			assert.Equal(t, tc.why, why, "the why= that follows it")
		})
	}
}

// TestSwarmMainCoverResultsRootOf pins the directory native publishes into: a
// named --results-root wins, a blank flag with a blank root is no directory at
// all, and a blank flag under a root is <root>/results.
func TestSwarmMainCoverResultsRootOf(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		flag, root, want string
	}{
		"the flag wins":              {flag: "/named/root", root: "/other", want: "/named/root"},
		"a blank flag and root":      {flag: "", root: "", want: ""},
		"a whitespace flag is blank": {flag: "  ", root: "/root", want: filepath.Join("/root", "results")},
		"blank flag under a root":    {flag: "", root: "/root", want: filepath.Join("/root", "results")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, resultsRootOf(tc.flag, tc.root), "the published results root")
		})
	}
}

// TestSwarmMainCoverNativeProcessExit pins the process exit after a launch that
// started: rc 0 passes, a kill or ssh's 255 is a retry-by-reconciliation 1, and
// every other child code is carried through.
func TestSwarmMainCoverNativeProcessExit(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		child, want int
	}{
		"a clean child":   {child: 0, want: 0},
		"a failing child": {child: 3, want: 3},
		"ssh's 255":       {child: 255, want: 1},
		"a killed child":  {child: -1, want: 1},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, nativeProcessExit(tc.child), "the process exit for rc=%d", tc.child)
		})
	}
}

// TestSwarmMainCoverNativeLeftAResult pins the one artefact a card exists to
// produce: a blank or absent job is false; RESULT.md at the job root or under
// repo/ is true; a directory named RESULT.md is not a result; the asked report
// the machinery wrote is an absence; and a shaped finish record the gh shim wrote
// at cardcontract.FinishName is the framed card's own result.
func TestSwarmMainCoverNativeLeftAResult(t *testing.T) {
	t.Parallel()

	t.Run("a blank job is false", func(t *testing.T) {
		t.Parallel()
		assert.False(t, nativeLeftAResult(""), "no job directory holds no result")
	})

	t.Run("an empty directory is false", func(t *testing.T) {
		t.Parallel()
		assert.False(t, nativeLeftAResult(t.TempDir()), "an empty job holds no result")
	})

	t.Run("a RESULT.md at the root is true", func(t *testing.T) {
		t.Parallel()
		job := t.TempDir()
		write(t, filepath.Join(job, "RESULT.md"), "result: reviewed\nverdict: ok\n")
		assert.True(t, nativeLeftAResult(job), "the job root's RESULT.md is the result")
	})

	t.Run("a RESULT.md under repo is true", func(t *testing.T) {
		t.Parallel()
		job := t.TempDir()
		write(t, filepath.Join(job, "repo", "RESULT.md"), "result: reviewed\nverdict: ok\n")
		assert.True(t, nativeLeftAResult(job), "the clone's RESULT.md is the result")
	})

	t.Run("a directory named RESULT.md is false", func(t *testing.T) {
		t.Parallel()
		job := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(job, "RESULT.md"), 0o755))
		assert.False(t, nativeLeftAResult(job), "a directory is not a published report")
	})

	t.Run("the asked report is false", func(t *testing.T) {
		t.Parallel()
		job := t.TempDir()
		write(t, filepath.Join(job, "RESULT.md"), strings.Join([]string{
			swarm.AskedResultPrefix + "would you like me to proceed?",
			"asked: c1 ended its last turn with a question",
			swarm.AskedWrittenBy,
			"",
		}, "\n"))
		assert.False(t, nativeLeftAResult(job), "a report the machinery wrote is evidence of an absence, not a result")
	})

	t.Run("a shaped finish record is true", func(t *testing.T) {
		t.Parallel()
		job := t.TempDir()
		finish := strings.Join([]string{
			"head: deadbee",
			"branch: sprint/w",
			"verdict: ok",
			"gate: -",
			"output: -",
			"report: the framed card published a pr",
			"",
		}, "\n")
		require.True(t, typedrec.ParseCardResult([]byte(finish)).Shaped,
			"the finish fixture is in the card contract's shape")
		write(t, filepath.Join(job, cardcontract.FinishName), finish)
		assert.True(t, nativeLeftAResult(job), "the gh shim's finish record is the framed card's result")
	})
}

// TestSwarmMainCoverFenceSuffix pins the field a fenced card carries: the empty
// string when nothing was rejected, and ` fence=rejected path=<field>` otherwise,
// with the path as one safe token through oneline.Field.
func TestSwarmMainCoverFenceSuffix(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		path, want string
	}{
		"nothing rejected":      {"", ""},
		"a plain path":          {"/work/file.go", " fence=rejected path=/work/file.go"},
		"a blank in the path":   {"/work/my file.go", ` fence=rejected path=/work/my\x20file.go`},
		"a newline in the path": {"/work/a\nb.go", ` fence=rejected path=/work/a\x0ab.go`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, fenceSuffix(tc.path), "the fence field")
		})
	}
}

// TestSwarmMainCoverStoppedSuffix pins the budget rule's own key: empty for every
// card the machinery did not stop, ` stopped=<field>` for one it did.
func TestSwarmMainCoverStoppedSuffix(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		stopped, want string
	}{
		"not stopped":            {"", ""},
		"tokens":                 {"tokens", " stopped=tokens"},
		"unverifiable":           {"unverifiable", " stopped=unverifiable"},
		"a newline is one token": {"max\nturns", ` stopped=max\x0aturns`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, stoppedSuffix(tc.stopped), "the stopped field")
		})
	}
}

// TestSwarmMainCoverTermSuffix pins the one clean outside ending: ` reason=terminated`
// for a card a TERM stopped, and nothing for every other card.
func TestSwarmMainCoverTermSuffix(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		terminated bool
		want       string
	}{
		"not terminated": {terminated: false, want: ""},
		"terminated":     {terminated: true, want: " reason=terminated"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, termSuffix(tc.terminated), "the terminated field")
		})
	}
}

// TestSwarmMainCoverUsageSuffix pins the usage status the NATIVE OK line carries:
// empty when a store answered, and ` usage=none reason=<r> path=<looked>` with the
// looked path one safe token through oneline.Field otherwise.
func TestSwarmMainCoverUsageSuffix(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		reason, path, want string
	}{
		"a store answered":      {"", "/store", ""},
		"no rows":               {"no-rows", "/store", " usage=none reason=no-rows path=/store"},
		"a blank in the path":   {"no-store", "/my store", ` usage=none reason=no-store path=/my\x20store`},
		"a newline in the path": {"query-failed", "/a\nb", ` usage=none reason=query-failed path=/a\x0ab`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, usageSuffix(tc.reason, tc.path), "the usage field")
		})
	}
}

// verifyArgs is the required shape every cmdVerify case starts from: a readable
// result whose line 1 equals the contract, and the label the line and receipt carry.
func verifyArgs(result, contract, label string, extra ...string) []string {
	args := []string{"--result", result, "--contract", contract, "--label", label}
	return append(args, extra...)
}

// TestSwarmMainCoverVerifyMaxZero pins the ceiling's floor: a --max below one is a
// refusal at exit 2, naming the flag, before any file is read.
func TestSwarmMainCoverVerifyMaxZero(t *testing.T) {
	t.Parallel()
	result := filepath.Join(t.TempDir(), "RESULT.md")
	write(t, result, "result: reviewed\nverdict: ok\n")
	var stdout, stderr bytes.Buffer
	code := cmdVerify(verifyArgs(result, "result: reviewed", "c1", "--max", "0"), &stdout, &stderr)
	assert.Equal(t, 2, code, "a --max below one cannot run")
	assert.Contains(t, stderr.String(), "--max is at least 1", "the refusal names the flag and its floor")
	assert.Empty(t, stdout.String(), "a refusal prints nothing on stdout")
}

// TestSwarmMainCoverVerifyUnreadableFile pins the three optional inputs' error
// paths: an unreadable --card, --run-record or --usage is exit 2 and the message
// names the flag.
func TestSwarmMainCoverVerifyUnreadableFile(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		flag, want string
	}{
		"an unreadable card":       {flag: "--card", want: "--card wants a readable file"},
		"an unreadable run-record": {flag: "--run-record", want: "--run-record wants a readable exit.json"},
		"an unreadable usage":      {flag: "--usage", want: "--usage wants a readable usage file"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			result := filepath.Join(dir, "RESULT.md")
			write(t, result, "result: reviewed\nverdict: ok\n")
			var stdout, stderr bytes.Buffer
			code := cmdVerify(verifyArgs(result, "result: reviewed", "c1", tc.flag, filepath.Join(dir, "absent")), &stdout, &stderr)
			assert.Equal(t, 2, code, "an unreadable input cannot run")
			assert.Contains(t, stderr.String(), tc.want, "the refusal names the flag and what it wants")
		})
	}
}

// TestSwarmMainCoverVerifyMissingResult pins the result read: a missing --result
// file is exit 2, and the message names the flag.
func TestSwarmMainCoverVerifyMissingResult(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := cmdVerify(verifyArgs(filepath.Join(dir, "absent.md"), "result: reviewed", "c1"), &stdout, &stderr)
	assert.Equal(t, 2, code, "a missing result cannot run")
	assert.Contains(t, stderr.String(), "--result wants a readable RESULT.md", "the refusal names the flag and what it wants")
}

// TestSwarmMainCoverVerifyRefusesMismatchedLineOne pins the contract check: a
// report whose line 1 differs from --contract is a RESULT REFUSED line at exit 1.
func TestSwarmMainCoverVerifyRefusesMismatchedLineOne(t *testing.T) {
	t.Parallel()
	result := filepath.Join(t.TempDir(), "RESULT.md")
	write(t, result, "result: something else\nverdict: ok\n")
	var stdout, stderr bytes.Buffer
	code := cmdVerify(verifyArgs(result, "result: reviewed", "c1"), &stdout, &stderr)
	assert.Equal(t, 1, code, "a mismatch is a verdict that said no")
	assert.Contains(t, stdout.String(), "RESULT REFUSED", "the outcome line refuses the report")
	assert.Empty(t, stderr.String(), "a read report is not an error")
}

// TestSwarmMainCoverVerifyPassingResult pins the passing path: a RESULT.md whose
// line 1 equals --contract with a run record and a two-line usage TSV is exit 0, a
// RESULT OK line, and a receipt holding the exit code, the token totals, the
// dollars and the wall time from the usage row's started and ended.
func TestSwarmMainCoverVerifyPassingResult(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	result := filepath.Join(dir, "RESULT.md")
	write(t, result, "result: reviewed\nverdict: ok\n")
	runRecord := filepath.Join(dir, "exit.json")
	write(t, runRecord, `{"rc":0}`)
	usage := filepath.Join(dir, "usage.tsv")
	write(t, usage, strings.Join([]string{
		"started\tended\ttokens_in\ttokens_out\tusd",
		"2026-10-04T10:00:00Z\t2026-10-04T10:00:05Z\t10\t2\t0.10",
		"",
	}, "\n"))

	var stdout, stderr bytes.Buffer
	code := cmdVerify(verifyArgs(result, "result: reviewed", "c1", "--run-record", runRecord, "--usage", usage), &stdout, &stderr)
	require.Equal(t, 0, code, "a report that meets its contract runs: %s", stderr.String())
	assert.Contains(t, stdout.String(), "RESULT OK", "the outcome line accepts the report")
	assert.Empty(t, stderr.String(), "a passing run prints no error")

	receipt, err := os.ReadFile(result + ".receipt")
	require.NoError(t, err, "the receipt is written beside the result")
	got := string(receipt)
	for _, want := range []string{"exit_code=0", "tokens_in=10", "tokens_out=2", "usd=0.10", "wall_seconds=5"} {
		assert.Contains(t, got, want, "the receipt carries %s", want)
	}
}

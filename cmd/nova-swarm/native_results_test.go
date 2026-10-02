package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestControlCardResultsSurviveSweep is issue #2632. A control card's RESULT.md,
// usage.tsv and report are published under
// <results-root>/<label>/<runID>/<attempt>/, which is not the job directory.
// --sweep-now leaves that directory intact and the job directory gone. nova-pulse
// status reads the spend from the results root, not from the job that was deleted.
func TestControlCardResultsSurviveSweep(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	t.Run("sweep-now", func(t *testing.T) {
		controlCardSurvivesSweep(t, true)
	})
}

func controlCardSurvivesSweep(t *testing.T, sweepNow bool) {
	t.Helper()
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "control"
	resultsRoot := filepath.Join(t.TempDir(), "results")
	cardPath := filepath.Join(root, "card.md")
	require.NoError(t, os.WriteFile(cardPath, []byte("FAKE-SAY control-report-line\nRESULT: control\n"), 0o644))
	args := []string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model", "--label", label,
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--idle", "0", "--no-wall",
		"--results-root", resultsRoot}
	if sweepNow {
		args = append(args, "--sweep-now")
	}
	var stdout, stderr bytes.Buffer
	rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	require.Equal(t, 0, rc, "the control card exits 0, got %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	require.Contains(t, stdout.String(), "NATIVE OK ", "the control card publishes a result:\n%s\n%s", stdout.String(), stderr.String())

	job := filepath.Join(slot, "jobs", label)

	_, err := os.Stat(job)
	require.True(t, os.IsNotExist(err), "the job directory must be gone after the sweep, stat=%v", err)
	attempt := oneRunAttempt(t, resultsRoot, label)
	rel, err := filepath.Rel(job, attempt)
	require.False(t, err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), "results %s sit inside the job directory %s", attempt, job)
	for _, name := range []string{"RESULT.md", "usage.tsv", "report"} {
		_, err := os.Stat(filepath.Join(attempt, name))
		require.NoError(t, err, "%s was not published under %s\nstderr:\n%s", name, attempt, stderr.String())
	}
	report, err := os.ReadFile(filepath.Join(attempt, "report"))
	require.NoError(t, err)
	require.Contains(t, string(report), "control-report-line", "the report does not carry what the harness said:\n%s", report)
	usage, err := os.ReadFile(filepath.Join(attempt, "usage.tsv"))
	require.NoError(t, err)
	require.Contains(t, string(usage), label, "usage.tsv does not name the card:\n%s", usage)

	// The nova-pulse status check that followed here left with internal/pulse (deleted 2026-09-25, #3969).
}

// TestTwoInvocationsPreserveResults is the preservation control for a repeated
// label. Attempt numbers restart at 1 every invocation, so the run directory
// has to be the identity. Both sweeps run, and each run's report, RESULT.md
// and usage.tsv stay intact and unmixed.
func TestTwoInvocationsPreserveResults(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "control"
	resultsRoot := filepath.Join(t.TempDir(), "results")
	store := nativeStore(t)
	for _, card := range []struct{ say, findings string }{
		{"run-one-report", "1"},
		{"run-two-report", "2"},
	} {
		cardPath := filepath.Join(root, "card-"+card.findings+".md")
		body := fmt.Sprintf("FAKE-SAY %s\nFAKE-FINDINGS %s\n", card.say, card.findings)
		require.NoError(t, os.WriteFile(cardPath, []byte(body), 0o644))
		var stdout, stderr bytes.Buffer
		rc := run([]string{"native", "--tokens", "unmetered", "--slots-store", store, "--owner", "fake-1",
			"--harness", bin, "--model", "fake/fake-model", "--label", label,
			"--card", cardPath, "--slot", slot, "--root", root,
			"--deadline", "30s", "--idle", "0", "--no-wall",
			"--results-root", resultsRoot, "--sweep-now"},
			strings.NewReader(""), &stdout, &stderr, time.Now())
		require.Equal(t, 0, rc, "invocation findings=%s exits 0, got %d\n%s\n%s", card.findings, rc, stdout.String(), stderr.String())
	}
	job := filepath.Join(slot, "jobs", label)
	_, err := os.Stat(job)
	require.True(t, os.IsNotExist(err), "the job directory must be gone after both sweeps, stat=%v", err)
	runs := runDirs(t, resultsRoot, label)
	require.Len(t, runs, 2, "two invocations need two run directories, got %v", runs)
	require.NotEqual(t, runs[0], runs[1], "the run ids collided: %v", runs)
	seenSay := map[string]bool{}
	seenFindings := map[string]bool{}
	for _, id := range runs {
		attempt := filepath.Join(resultsRoot, label, id, "1")
		report, err := os.ReadFile(filepath.Join(attempt, "report"))
		require.NoError(t, err, "run %s report", id)
		result, err := os.ReadFile(filepath.Join(attempt, "RESULT.md"))
		require.NoError(t, err, "run %s RESULT.md", id)
		n := usageDataRows(t, filepath.Join(attempt, "usage.tsv"))
		require.Equal(t, 1, n, "run %s usage has %d data rows, want 1 (a second invocation must not append here)", id, n)
		switch {
		case strings.Contains(string(report), "run-one-report"):
			seenSay["one"] = true
			require.NotContains(t, string(report), "run-two-report", "run %s report mixes both invocations:\n%s", id, report)
		case strings.Contains(string(report), "run-two-report"):
			seenSay["two"] = true
			require.NotContains(t, string(report), "run-one-report", "run %s report mixes both invocations:\n%s", id, report)
		default:
			t.Fatalf("run %s report is neither invocation:\n%s", id, report)
		}
		switch {
		case strings.Contains(string(result), "findings: 1\n"):
			seenFindings["1"] = true
			require.NotContains(t, string(result), "findings: 2\n", "run %s RESULT mixes both invocations:\n%s", id, result)
		case strings.Contains(string(result), "findings: 2\n"):
			seenFindings["2"] = true
			require.NotContains(t, string(result), "findings: 1\n", "run %s RESULT mixes both invocations:\n%s", id, result)
		default:
			t.Fatalf("run %s RESULT lost its findings line:\n%s", id, result)
		}
	}
	require.True(t, seenSay["one"], "both reports and both results must survive, says=%v findings=%v", seenSay, seenFindings)
	require.True(t, seenSay["two"], "both reports and both results must survive, says=%v findings=%v", seenSay, seenFindings)
	require.True(t, seenFindings["1"], "both reports and both results must survive, says=%v findings=%v", seenSay, seenFindings)
	require.True(t, seenFindings["2"], "both reports and both results must survive, says=%v findings=%v", seenSay, seenFindings)
}

// TestPublicationFailureKeepsTheJob is the capture-failure control. The harness
// unlinks harness-output.log, so the required report cannot be published.
// --sweep-now must leave the job directory in place.
func TestPublicationFailureKeepsTheJob(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "control"
	resultsRoot := filepath.Join(t.TempDir(), "results")
	cardPath := filepath.Join(root, "card.md")
	require.NoError(t, os.WriteFile(cardPath, []byte("FAKE-DROP-CAPTURE\nFAKE-SAY kept-in-the-job\n"), 0o644))
	var stdout, stderr bytes.Buffer
	rc := run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model", "--label", label,
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--idle", "0", "--no-wall",
		"--results-root", resultsRoot, "--sweep-now"},
		strings.NewReader(""), &stdout, &stderr, time.Now())
	require.Equal(t, 0, rc, "the card still exits 0, got %d\n%s\n%s", rc, stdout.String(), stderr.String())
	require.Contains(t, stderr.String(), "the report could not be published", "a missing capture must be named:\n%s", stderr.String())
	require.Contains(t, stderr.String(), "left", "--sweep-now must say it left the job in place:\n%s", stderr.String())
	require.Contains(t, stderr.String(), "in place", "--sweep-now must say it left the job in place:\n%s", stderr.String())
	job := filepath.Join(slot, "jobs", label)
	_, err := os.Stat(job)
	require.NoError(t, err, "the job directory must be kept when the report cannot be published")
	_, err = os.Stat(filepath.Join(job, "RESULT.md"))
	require.NoError(t, err, "the job still holds RESULT.md")
	_, err = os.Stat(filepath.Join(job, "harness-output.log"))
	require.True(t, os.IsNotExist(err), "the capture was dropped, stat=%v", err)
	_ = filepath.WalkDir(resultsRoot, func(path string, d os.DirEntry, err error) error {
		assert.False(t, err == nil && d.Name() == "report", "a failed publish still wrote %s", path)
		return nil
	})
}

func oneRunAttempt(t *testing.T, resultsRoot, label string) string {
	t.Helper()
	runs := runDirs(t, resultsRoot, label)
	require.Len(t, runs, 1, "one run directory under %s, got %v", label, runs)
	require.NotEqual(t, "1", runs[0], "run id %q is the restarting attempt number, not an invocation identity", runs[0])
	require.Contains(t, runs[0], "-", "run id %q is the restarting attempt number, not an invocation identity", runs[0])
	return filepath.Join(resultsRoot, label, runs[0], "1")
}

func runDirs(t *testing.T, resultsRoot, label string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(resultsRoot, label))
	require.NoError(t, err, "reading %s", filepath.Join(resultsRoot, label))
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	return dirs
}

func usageDataRows(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	n := 0
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if line != "" && !strings.HasPrefix(line, "job\t") {
			n++
		}
	}
	return n
}

// TestReaderVerdictRecognizedFromHarnessLogWhenResultMDIsAbsent is the break-it probe
// for defect 1 (card neg-03, route reader-space, attempt 1): an OpenCode reader writes
// `verdict: ok` and `report: pushed=620175e8...` directly to stdout/stderr in
// harness-output.log without writing a physical RESULT.md file.
// nativeLeftAResult must recognize the result from harness-output.log so the run earns
// NATIVE OK rather than NATIVE INCOMPLETE why=no-result (which caused ran=false and
// verdict=""), and member.Result must extract the verdict and report.
//
// Break-it probe:
// - Removing the harness log check in nativeLeftAResult causes assertion 1 and assertion 2 to fail.
// - Removing report: parsing in readResult causes assertion 3 to fail.
// - Removing the fallback to harness log in nativeChild.Result causes assertions 4-7 to fail.
func TestReaderVerdictRecognizedFromHarnessLogWhenResultMDIsAbsent(t *testing.T) {
	t.Parallel()

	jobDir := t.TempDir()
	logContent := "SANDBOX OK backend=sandbox-exec abi=- read=3 read-noexec=0 write=2 net=nopromise cwd=" + jobDir + " ancestors=17 cmd=opencode\n" +
		"verdict: ok\n" +
		"report: pushed=620175e81234567890abcdef1234567890abcdef\n" +
		"SANDBOX DONE exit=0 cmd=opencode\n"

	harnessLog := filepath.Join(jobDir, "harness-output.log")
	require.NoError(t, os.WriteFile(harnessLog, []byte(logContent), 0o644))

	// 1. nativeLeftAResult must return true for the job directory holding harness-output.log
	assert.True(t, nativeLeftAResult(jobDir), "nativeLeftAResult must return true when harness-output.log contains verdict: ok without RESULT.md")

	// 2. nativeVerdictWhy returns OK with no why= failure reason
	runRes := nativeRunResult{job: jobDir, harness: "ok", rc: 0}
	verdictWord, why := nativeVerdictWhy(runRes)
	assert.Equal(t, "OK", verdictWord, "verdict word on NATIVE line must be OK")
	assert.Empty(t, why, "why must be empty for successful reader run")

	// 3. readResult on the harness log extracts verdict and report
	head, verdict, report := readResult(harnessLog)
	assert.Empty(t, head, "head should be empty when not specified")
	assert.Equal(t, "ok", verdict, "verdict must be ok")
	assert.Equal(t, "pushed=620175e81234567890abcdef1234567890abcdef", report, "report must be extracted")

	// 4. nativeChild.Result() extracts verdict and report even when RESULT.md is absent
	resultsDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(resultsDir, "report"), []byte(logContent), 0o644))
	child := &nativeChild{
		job:     jobDir,
		results: resultsDir,
		logPath: filepath.Join(jobDir, "native.log"),
	}
	require.NoError(t, os.WriteFile(child.logPath, []byte("NATIVE OK label=neg-03 job="+jobDir+" rc=0 harness=ok\n"), 0o644))

	res := child.Result()
	assert.True(t, res.Ran, "child must be marked as ran")
	assert.True(t, res.OK, "child must be marked as OK")
	assert.Equal(t, "ok", res.Verdict, "verdict must be parsed as ok")
	assert.Equal(t, "pushed=620175e81234567890abcdef1234567890abcdef", res.Report, "report must be extracted from harness output")
}



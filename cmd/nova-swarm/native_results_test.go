//go:build slow || functional

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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
	require.True(t, err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)), "results %s sit inside the job directory %s", attempt, job)
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

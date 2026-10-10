package main

// THE PUBLISH, THE USAGE ROW AND THE WALL'S UNIT TIER (cover-cmd-nova-swarm-publish).
//
// `go tool cover -func` held publishNativeResults (native.go:2425), writeNativeUsage
// (native.go:2214), wall (native.go:964) and sandboxHostRules (native.go:1788) at 0.0%.
// Each of the four is a pure function of its arguments plus the files a job directory
// holds, so each is driven here with no sleep, no real time, no network, no subprocess
// and no live store: a headless harness reads its usage from the capture bytes handed in,
// an opencode harness with an empty data home answers `no-store` before it ever looks up
// sqlite3, and the wall's refusal paths never need a wall that starts. What a test here
// does NOT do is the functional tier's: the wall-on-PATH lookup (a real nova-sandbox must
// answer `check`), a real wall's own `check`, and initRunState / start / watch / collect /
// report (a launched harness under the real clock) are left to the functional suite.

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// publishCoverWallPrep builds the frozen *nativePrepared the wall reads: a label, a slot
// under t.TempDir(), the launch argv the wall wraps when it is told to, and a phase
// recorder so every case can assert the wall announced itself through cfg.onPhase.
func publishCoverWallPrep(t *testing.T) (*nativePrepared, *[]string) {
	t.Helper()
	slot := t.TempDir()
	phases := &[]string{}
	p := &nativePrepared{
		cfg: nativeRunConfig{
			label:   "pubcover",
			slotDir: slot,
			onPhase: func(phase string) { *phases = append(*phases, phase) },
		},
		launch:   []string{filepath.Join("bin", "harness"+exeSuffix()), "run", "prompt"},
		jobDir:   filepath.Join(slot, "jobs", "pubcover"),
		dataHome: filepath.Join(slot, "home"),
		tmpDir:   filepath.Join(slot, "tmp"),
		shimDir:  filepath.Join(slot, "shim"),
	}
	return p, phases
}

// publishCoverUsageRow reads the header and the one data row AppendCardUsage wrote, mapped
// by column name so an assertion never depends on a fixed index.
func publishCoverUsageRow(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "reading %s", path)
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	require.Len(t, lines, 2, "%s holds a header and one row", path)
	header := strings.Split(lines[0], "\t")
	values := strings.Split(lines[1], "\t")
	require.Len(t, values, len(header), "%s: a row of %d columns", path, len(header))
	row := map[string]string{}
	for i, name := range header {
		row[name] = values[i]
	}
	return row
}

// An empty resultsRoot or runID means this invocation is not publishing outside the job:
// nativeResultsAttemptDir answers "", and the copy writes nothing.
func TestSwarmNativePublishCoverResultsNoAttemptDir(t *testing.T) {
	t.Parallel()
	jobDir := t.TempDir()
	cases := []struct {
		name string
		cfg  nativeRunConfig
	}{
		{"no resultsRoot", nativeRunConfig{label: "pubcover", runID: "run-1"}},
		{"no runID", nativeRunConfig{label: "pubcover", resultsRoot: t.TempDir()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var errOut bytes.Buffer
			got := publishNativeResults(tc.cfg, jobDir, 1, &errOut)
			assert.Equal(t, "", got)
			assert.Empty(t, errOut.String())
		})
	}
}

// A results root that sits inside the job directory is a directory the sweep would delete
// with the job: the publish refuses it with a NOTE naming the layout and returns "".
func TestSwarmNativePublishCoverResultsInsideJob(t *testing.T) {
	t.Parallel()
	jobDir := t.TempDir()
	cfg := nativeRunConfig{label: "pubcover", resultsRoot: filepath.Join(jobDir, "results"), runID: "run-1"}
	var errOut bytes.Buffer
	got := publishNativeResults(cfg, jobDir, 1, &errOut)
	assert.Equal(t, "", got)
	assert.Contains(t, errOut.String(), "NATIVE NOTE")
	assert.Contains(t, errOut.String(), "inside the job directory")
}

// A missing harness-output.log is not an absent optional file: copying nothing and still
// claiming success is how --sweep-now would delete the only copy, so the publish refuses.
func TestSwarmNativePublishCoverResultsNoCapture(t *testing.T) {
	t.Parallel()
	jobDir := filepath.Join(t.TempDir(), "job")
	require.NoError(t, os.MkdirAll(jobDir, 0o755))
	cfg := nativeRunConfig{label: "pubcover", resultsRoot: t.TempDir(), runID: "run-1"}
	var errOut bytes.Buffer
	got := publishNativeResults(cfg, jobDir, 1, &errOut)
	assert.Equal(t, "", got)
	assert.Contains(t, errOut.String(), "the report could not be published")
}

// The capture and RESULT.md can both be there and the attempt directory still hold no
// usage.tsv: the publish copies nothing more and refuses with the NOTE that names it.
func TestSwarmNativePublishCoverResultsNoUsage(t *testing.T) {
	t.Parallel()
	jobDir := filepath.Join(t.TempDir(), "job")
	require.NoError(t, os.MkdirAll(jobDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(jobDir, "harness-output.log"), []byte("the child said this\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte("# RESULT\n"), 0o644))
	cfg := nativeRunConfig{label: "pubcover", resultsRoot: t.TempDir(), runID: "run-1"}
	var errOut bytes.Buffer
	got := publishNativeResults(cfg, jobDir, 1, &errOut)
	assert.Equal(t, "", got)
	assert.Contains(t, errOut.String(), "usage.tsv was not published under")
}

// The landing case: the attempt directory was already given its usage.tsv, so the publish
// copies the report and RESULT.md byte for byte and returns the attempt directory.
func TestSwarmNativePublishCoverResultsLands(t *testing.T) {
	t.Parallel()
	jobDir := filepath.Join(t.TempDir(), "job")
	require.NoError(t, os.MkdirAll(jobDir, 0o755))
	report := []byte("the child said this\nand then this\n")
	result := []byte("# RESULT\n\nthe card's own result\n")
	require.NoError(t, os.WriteFile(filepath.Join(jobDir, "harness-output.log"), report, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(jobDir, "RESULT.md"), result, 0o644))
	cfg := nativeRunConfig{label: "pubcover", resultsRoot: t.TempDir(), runID: "run-1"}
	dir := nativeResultsAttemptDir(cfg, 1)
	require.NotEqual(t, "", dir)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "usage.tsv"), []byte("job\tattempt\nx\t1\n"), 0o644))
	var errOut bytes.Buffer
	got := publishNativeResults(cfg, jobDir, 1, &errOut)
	assert.Equal(t, dir, got)
	assert.Empty(t, errOut.String())
	gotReport, err := os.ReadFile(filepath.Join(dir, "report"))
	require.NoError(t, err)
	assert.Equal(t, report, gotReport)
	gotResult, err := os.ReadFile(filepath.Join(dir, "RESULT.md"))
	require.NoError(t, err)
	assert.Equal(t, result, gotResult)
}

// A headless child's usage comes from its capture, not a store: a capture holding no
// result answers reason `no-usage`, the path is the capture under the job directory, and a
// launch the deadline killed (rc -1) writes its rc column as a dash.
func TestSwarmNativePublishCoverUsageHeadless(t *testing.T) {
	t.Parallel()
	slot := t.TempDir()
	label := "pubcover"
	jobDir := filepath.Join(slot, "jobs", label)
	require.NoError(t, os.MkdirAll(jobDir, 0o755))
	cfg := nativeRunConfig{
		binary:      "claude",
		label:       label,
		slotDir:     slot,
		resultsRoot: t.TempDir(),
		runID:       "run-1",
	}
	start := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	var errOut bytes.Buffer
	_, reason, path, row := writeNativeUsage(cfg, t.TempDir(), "subscription-claude", "claude-opus",
		start, start.Add(time.Minute), time.Time{}, -1, 2, "done", []byte("no result in this capture\n"), &errOut)
	assert.Equal(t, "no-usage", reason)
	assert.Equal(t, filepath.Join(jobDir, "harness-output.log"), path)
	assert.Equal(t, "-", row["rc"])
	assert.Equal(t, "2", row["attempt"])
	assert.Equal(t, "done", row["end"])
	assert.Equal(t, "claude-opus", row["model"])
	for _, p := range []string{
		filepath.Join(jobDir, "usage.tsv"),
		filepath.Join(slot, "usage.tsv"),
		filepath.Join(cfg.resultsRoot, label, cfg.runID, "2", "usage.tsv"),
	} {
		r := publishCoverUsageRow(t, p)
		assert.Equal(t, "2", r["attempt"], p)
		assert.Equal(t, "done", r["end"], p)
		assert.Equal(t, "claude-opus", r["model"], p)
	}
}

// An opencode child whose data home holds no store answers `no-store` before sqlite3 is
// ever looked up, the path it names is the database under the data home, and usd stays a
// dash -- an absence, never a zero.
func TestSwarmNativePublishCoverUsageOpenCode(t *testing.T) {
	t.Parallel()
	slot := t.TempDir()
	label := "pubcover"
	jobDir := filepath.Join(slot, "jobs", label)
	require.NoError(t, os.MkdirAll(jobDir, 0o755))
	dataHome := filepath.Join(t.TempDir(), "home")
	cfg := nativeRunConfig{
		binary:      "opencode",
		label:       label,
		slotDir:     slot,
		resultsRoot: t.TempDir(),
		runID:       "run-1",
	}
	start := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	var errOut bytes.Buffer
	_, reason, path, row := writeNativeUsage(cfg, dataHome, "fake", "fake-model",
		start, start.Add(time.Minute), time.Time{}, 0, 1, "failed", []byte("ignored by a store read\n"), &errOut)
	assert.Equal(t, "no-store", reason)
	assert.Equal(t, filepath.Join(dataHome, filepath.FromSlash(swarm.OpenCodeDB)), path)
	assert.Equal(t, "-", row["usd"])
	assert.Equal(t, "1", row["attempt"])
	assert.Equal(t, "failed", row["end"])
	assert.Equal(t, "fake-model", row["model"])
	for _, p := range []string{
		filepath.Join(jobDir, "usage.tsv"),
		filepath.Join(slot, "usage.tsv"),
		filepath.Join(cfg.resultsRoot, label, cfg.runID, "1", "usage.tsv"),
	} {
		r := publishCoverUsageRow(t, p)
		assert.Equal(t, "1", r["attempt"], p)
		assert.Equal(t, "failed", r["end"], p)
		assert.Equal(t, "fake-model", r["model"], p)
	}
}

// --no-wall owns every read and write the child makes, so a card that also names a repo
// is refused: no-wall cannot express the repo's host rule, exit 2.
func TestSwarmNativePublishCoverWallNoWallWithRepo(t *testing.T) {
	t.Parallel()
	p, phases := publishCoverWallPrep(t)
	p.cfg.noWall = true
	p.cfg.repos = []string{"mas-bandwidth/nova-tools"}
	var errOut bytes.Buffer
	w, _, rc := wall(p, &errOut)
	assert.Nil(t, w)
	assert.Equal(t, 2, rc)
	assert.Contains(t, errOut.String(), "NATIVE REFUSED")
	assert.Contains(t, errOut.String(), "wall cannot express repo rule")
	assert.Equal(t, []string{"wall"}, *phases)
}

// A --sandbox naming a file that is not there cannot answer the repo rule's `check`, so a
// card naming a repo is refused exactly as the no-wall case is (the exec fails to start).
func TestSwarmNativePublishCoverWallMissingSandboxWithRepo(t *testing.T) {
	t.Parallel()
	p, phases := publishCoverWallPrep(t)
	p.cfg.sandbox = filepath.Join(t.TempDir(), "no-such-wall")
	p.cfg.repos = []string{"mas-bandwidth/nova-tools"}
	var errOut bytes.Buffer
	w, _, rc := wall(p, &errOut)
	assert.Nil(t, w)
	assert.Equal(t, 2, rc)
	assert.Contains(t, errOut.String(), "NATIVE REFUSED")
	assert.Contains(t, errOut.String(), "wall cannot express repo rule")
	assert.Equal(t, []string{"wall"}, *phases)
}

// A --sandbox naming a file and a card naming no repo wraps the launch in the wall: the
// wall is the runPath, the argv is nativeSandboxArgv for the same inputs, and the priority
// answer is nativeNicesChild's for a walled child -- and where it says native nices the
// child, the nice shim lands in the shim directory.
func TestSwarmNativePublishCoverWallSandboxNoRepo(t *testing.T) {
	t.Parallel()
	p, phases := publishCoverWallPrep(t)
	wallFile := filepath.Join(t.TempDir(), "wall")
	require.NoError(t, os.WriteFile(wallFile, []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, os.MkdirAll(p.shimDir, 0o755))
	p.cfg.sandbox = wallFile
	var errOut bytes.Buffer
	w, _, rc := wall(p, &errOut)
	require.NotNil(t, w)
	assert.Equal(t, 0, rc)
	assert.Equal(t, wallFile, w.runPath)
	assert.Equal(t, wallFile, w.wall)
	assert.Equal(t, nativeSandboxArgv(p.launch, p.cfg, p.dataHome, p.jobDir, p.tmpDir), w.runArgv)
	assert.Equal(t, nativeNicesChild(runtime.GOOS, true), w.niced)
	if w.niced {
		_, err := os.Stat(filepath.Join(p.shimDir, "nice"))
		assert.NoError(t, err, "a wall that nices the child leaves the nice shim in %s", p.shimDir)
	}
	assert.Equal(t, []string{"wall"}, *phases)
}

// --no-wall with no repo wraps nothing: the runPath is the launch's own binary, the wall
// name is empty, and the launch argv is handed through unchanged.
func TestSwarmNativePublishCoverWallNoWallNoRepo(t *testing.T) {
	t.Parallel()
	p, phases := publishCoverWallPrep(t)
	p.cfg.noWall = true
	var errOut bytes.Buffer
	w, _, rc := wall(p, &errOut)
	require.NotNil(t, w)
	assert.Equal(t, 0, rc)
	assert.Equal(t, p.launch[0], w.runPath)
	assert.Equal(t, p.launch[1:], w.runArgv)
	assert.Equal(t, "", w.wall)
	assert.False(t, w.niced)
	assert.Equal(t, []string{"wall"}, *phases)
}

// sandboxHostRules of a wall that is not there answers false: its `check` never starts, so
// a card naming a repo is refused rather than run unwalled.
func TestSwarmNativePublishCoverHostRulesMissing(t *testing.T) {
	t.Parallel()
	assert.False(t, sandboxHostRules(filepath.Join(t.TempDir(), "no-such-wall")))
}

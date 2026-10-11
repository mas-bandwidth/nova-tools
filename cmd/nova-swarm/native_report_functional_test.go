//go:build functional

package main

// WHAT `nova-swarm native`'S REPORT SAYS AFTER A CHILD RAN (docs/SPEC-SWARM.md;
// docs/SPEC-CARD-CONTRACT.md). Each test drives nativeRun (native.go:1590) with a
// nativeRunConfig built as native_test.go builds one -- an aSlot, the shared fake harness
// buildShared builds, and the config's own gateRun seam so no real gate runs -- and asserts
// on what a caller sees: the returned result's end and reason and the files the run leaves
// in the job directory. No test uses t.Parallel: some NOTE lines go to os.Stdout directly.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// nativeReportRun runs one fake-harness card through nativeRun and returns the result, the
// exit code, errOut and the job directory. gateRun is a no-op so report never runs a real
// gate.
func nativeReportRun(t *testing.T, label, card string) (nativeRunResult, int, string, string) {
	t.Helper()
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte(card), slotDir: slot, root: root,
		deadline: 30 * time.Second, noWall: true,
		gateRun: func(context.Context, string, []decide.Failure) (map[string]bool, error) {
			return nil, nil
		},
	}, &errOut)
	return res, code, errOut.String(), filepath.Join(slot, "jobs", label)
}

// A harness that exits non-zero is recorded as end=failed (native.go:1452-1463).
func TestReportMarksANonZeroHarnessFailed(t *testing.T) {
	res, code, errOut, _ := nativeReportRun(t, "failed", "FAKE-SAY it broke\nFAKE-RC 1\n")
	require.Equal(t, 0, code, "a failed child is not a refusal, got exit %d:\n%s", code, errOut)
	assert.Equal(t, swarm.EndFailed, res.end, "a non-zero harness is end=%q, want failed:\n%s", res.end, errOut)
	assert.Equal(t, 1, res.rc, "the child's exit code is %d, want 1", res.rc)
}

// A harness that exits 0 and writes no RESULT.md is recorded reason=harness-silent
// (native.go:1578-1582).
func TestReportMarksASilentHarness(t *testing.T) {
	res, code, errOut, jobDir := nativeReportRun(t, "silent", "FAKE-NORESULT\n")
	require.Equal(t, 0, code, "a silent child is not a refusal, got exit %d:\n%s", code, errOut)
	assert.Equal(t, 0, res.rc, "the silent child exited %d, want 0", res.rc)
	assert.Equal(t, "harness-silent", res.reason, "a silent harness is reason=%q, want harness-silent:\n%s", res.reason, errOut)
	_, err := os.Stat(filepath.Join(jobDir, "RESULT.md"))
	assert.True(t, os.IsNotExist(err), "a silent harness published a result: %v", err)
}

// A card whose last turn asked a question and published nothing gets a report naming the
// question, RESULT: ASKED (native.go:1506-1515; pkg/swarm/nativeasked.go).
func TestReportWritesAnAskedResultForAQuestion(t *testing.T) {
	res, code, errOut, jobDir := nativeReportRun(t, "asked", "FAKE-SAY Would you like me to proceed?\nFAKE-NORESULT\n")
	require.Equal(t, 0, code, "a card that asked is not a refusal, got exit %d:\n%s", code, errOut)
	assert.Equal(t, 0, res.rc, "the asking child exited %d, want 0", res.rc)
	raw, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
	require.NoError(t, err, "the asked result was not written:\n%s", errOut)
	assert.True(t, strings.Contains(string(raw), "RESULT: ASKED"), "the report does not name the asked verdict:\n%s", raw)
	assert.Contains(t, string(raw), "Would you like me to proceed?", "the report does not carry the question:\n%s", raw)
}

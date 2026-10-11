//go:build functional

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// (B) REPORT'S OUTCOMES, in the functional tier: a run of the fake harness buildShared
// (swarm_test.go:30) builds, driven through nativeRun and ending in report. Each test
// pins one outcome a caller sees on the returned result and in the job directory.

// aFunctionalRun builds the frozen configuration native_test.go uses -- aSlot's root and
// slot, the shared fake harness standing in for the binary, a 30s deadline -- and returns
// the job directory report writes into.
func aFunctionalRun(t *testing.T, label, card string) (nativeRunConfig, string) {
	t.Helper()
	require.NoError(t, buildShared(), "building the binaries these tests run")
	root, slot := aSlot(t)
	cfg := nativeRunConfig{
		binary: builtHarness, model: "fake/fake-model", label: label,
		card: []byte(card), slotDir: slot, root: root,
		deadline: 30 * time.Second, noWall: true,
	}
	return cfg, filepath.Join(slot, "jobs", label)
}

// TestANativeRunWhoseHarnessExitsNonZeroEndsFailed pins report at native.go:1466: a
// child that exits non-zero records the failed end.
func TestANativeRunWhoseHarnessExitsNonZeroEndsFailed(t *testing.T) {
	t.Parallel()

	cfg, _ := aFunctionalRun(t, "rc-nonzero", "FAKE-RC 1\n")
	var errOut bytes.Buffer
	res, code := nativeRun(cfg, &errOut)
	require.Equal(t, 0, code, "the run ran the child, got %d:\n%s", code, errOut.String())
	assert.Equal(t, swarm.EndFailed, res.end, "a non-zero child ends failed, got %q", res.end)
}

// TestANativeRunWhoseHarnessIsSilentSaysHarnessSilent pins report at native.go:1580: a
// child that exits 0 and publishes no RESULT.md is harness-silent.
func TestANativeRunWhoseHarnessIsSilentSaysHarnessSilent(t *testing.T) {
	t.Parallel()

	cfg, jobDir := aFunctionalRun(t, "silent", "FAKE-NORESULT\n")
	var errOut bytes.Buffer
	res, code := nativeRun(cfg, &errOut)
	require.Equal(t, 0, code, "the run ran the child, got %d:\n%s", code, errOut.String())
	assert.Equal(t, "harness-silent", res.reason, "a clean child that wrote no report is harness-silent")
	_, err := os.Stat(filepath.Join(jobDir, "RESULT.md"))
	assert.True(t, os.IsNotExist(err), "a silent child leaves no report of its own")
}

// TestANativeRunThatAsksAQuestionWritesTheAskedResult pins report at native.go:1523
// (swarm.WriteAskedResult): a child that exited 0, published no report and left a question
// as its last word has a RESULT: ASKED report written for it.
func TestANativeRunThatAsksAQuestionWritesTheAskedResult(t *testing.T) {
	t.Parallel()

	cfg, jobDir := aFunctionalRun(t, "asked", "FAKE-SAY Would you like me to proceed?\nFAKE-NORESULT\n")
	var errOut bytes.Buffer
	res, code := nativeRun(cfg, &errOut)
	require.Equal(t, 0, code, "the run ran the child, got %d:\n%s", code, errOut.String())
	assert.Equal(t, swarm.EndDone, res.end, "a child that asked and exited clean is done, got %q", res.end)
	raw, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
	require.NoError(t, err, "the asked report was written")
	assert.Contains(t, string(raw), swarm.AskedResultPrefix, "the asked report opens with RESULT: ASKED:\n%s", raw)
}

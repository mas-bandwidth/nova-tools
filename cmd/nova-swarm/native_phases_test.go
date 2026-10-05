package main

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNativeRunPhasesAreTheOldOrder verifies that nativeRun decomposes into
// the named phase functions (prepare, wall, start, watch, collect, report)
// and executes them in that exact linear order.
func TestNativeRunPhasesAreTheOldOrder(t *testing.T) {
	t.Parallel()

	wantPhases := []string{"prepare", "wall", "start", "watch", "collect", "report"}
	assert.Equal(t, wantPhases, nativeRunPhases, "nativeRunPhases must declare the exact old order of phases")

	bin := nativeHarness(t)
	_, slot := aSlot(t)
	root := filepath.Dir(slot)

	var recorded []string
	cfg := nativeRunConfig{
		binary:   bin,
		model:    "fake/fake-model",
		label:    "phase-order-test",
		card:     []byte("test card\n"),
		slotDir:  slot,
		root:     root,
		deadline: 30 * time.Second,
		noWall:   true,
		onPhase: func(phase string) {
			recorded = append(recorded, phase)
		},
	}
	var errOut bytes.Buffer
	res, code := nativeRun(cfg, &errOut)
	require.Equal(t, 0, code, "nativeRun failed: %s", errOut.String())
	require.Equal(t, 0, res.rc, "child did not succeed")
	assert.Equal(t, wantPhases, recorded, "phases did not execute in the expected order")
}

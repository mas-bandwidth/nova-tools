//go:build unix

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNativeDoesNotRetryOrReplaceReceiptForUnprovenPreviousGroup(t *testing.T) {
	t.Parallel()
	root, slot := aSlot(t)
	marker := filepath.Join(root, "release")
	childReady := filepath.Join(root, "child-ready")
	harness := filepath.Join(root, "failed-start")
	script := "#!/bin/sh\n" +
		"while [ ! -f '" + marker + "' ]; do sleep 0.01; done\n" +
		"(trap '' TERM; echo ready > '" + childReady + "'; while :; do sleep 1; done) >/dev/null 2>&1 &\n" +
		"while [ ! -f '" + childReady + "' ]; do sleep 0.01; done\n" +
		"echo ProviderModelNotFoundError\nexit 1\n"
	require.NoError(t, os.WriteFile(harness, []byte(script), 0o755))
	starts := 0
	var firstReceipt []byte
	var errOut bytes.Buffer
	res, _ := nativeRun(nativeRunConfig{
		binary: harness, model: "fake/fake-model", label: "failed-start",
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
		startSleep: func(time.Duration) { t.Error("unproven group reached retry sleep") },
		onPhase: func(phase string) {
			if phase == "start" {
				starts++
			}
			if phase != "watch" {
				return
			}
			var err error
			firstReceipt, err = os.ReadFile(filepath.Join(slot, nativeGroupReceiptName))
			require.NoError(t, err)
			b, err := os.ReadFile(filepath.Join(slot, nativeAnchorReceiptName))
			require.NoError(t, err)
			fields := strings.Fields(string(b))
			require.Len(t, fields, 5)
			anchorPID, err := strconv.Atoi(fields[0])
			require.NoError(t, err)
			require.NoError(t, syscall.Kill(anchorPID, syscall.SIGKILL))
			require.Eventually(t, func() bool { return swarm.StartStamp(anchorPID) == "-" }, time.Second, 10*time.Millisecond)
			require.NoError(t, os.Remove(filepath.Join(slot, nativeAnchorReceiptName)))
			require.NoError(t, os.WriteFile(marker, []byte("go\n"), 0o600))
		},
	}, &errOut)
	group := groupNumber(slot)
	defer func() {
		if group > 0 && groupRunnable(group) {
			_ = syscall.Kill(-group, syscall.SIGKILL) // ignored: test-owned orphan cleanup
		}
	}()
	require.Positive(t, group)
	assert.Equal(t, 1, starts, "a failed start with live descendants must not launch again")
	assert.Equal(t, strconv.Itoa(group)+":alive", res.survivors)
	assert.True(t, groupRunnable(group), "lost ownership must not signal the descendant")
	assert.NotContains(t, errOut.String(), "NATIVE RETRY")
	after, err := os.ReadFile(filepath.Join(slot, nativeGroupReceiptName))
	require.NoError(t, err)
	assert.Equal(t, firstReceipt, after, "retry must not replace the only group receipt")
}

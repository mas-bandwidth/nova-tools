//go:build linux && functional

package friend

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/procgroup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This process runs the production wall. A normal work write proves the wall
// admitted the child, while its direct sibling FIFO command must be refused.
func TestNativeWallCannotCommandSiblingCleanupChannel(t *testing.T) {
	t.Parallel()
	if os.Getenv("NOVA_TEST_NATIVE_CONTROL_WALL") == "1" {
		work, state, control := os.Getenv("NOVA_TEST_WORK"), os.Getenv("NOVA_TEST_STATE"), os.Getenv("NOVA_TEST_CONTROL")
		code := RunWall([]string{"--dir", work, "--deny", os.Getenv("NOVA_TEST_DENY"), "--control-dir", control, "--control-state", state,
			"--", "/bin/sh", "-c", `printf allowed > "$1/allowed"; if cat "$2" >/dev/null 2>&1; then echo receipt-readable; else echo receipt-denied; fi; if printf 'KILL\n' > "$3"; then echo attack-succeeded; else echo attack-denied; fi`,
			"sh", work, os.Getenv("NOVA_TEST_ANCHOR"), os.Getenv("NOVA_TEST_FIFO")}, os.Environ(), strings.NewReader(""), os.Stdout, os.Stderr)
		os.Exit(code)
	}
	base := t.TempDir()
	work, state := filepath.Join(base, "work"), filepath.Join(base, "state")
	control, deny := filepath.Join(state, "native-control"), filepath.Join(base, "coordinator")
	require.NoError(t, os.MkdirAll(work, 0700))
	require.NoError(t, os.MkdirAll(deny, 0700))
	d := &Daemon{Dir: work, StateDir: state, NativeControlDir: control}
	l := &loop{d: d, tag: "sibling"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = l.controlContext(ctx, "message", 1)
	ready, done := make(chan struct{}), make(chan struct{})
	var runErr error
	ctx = WithOutputSeen(ctx, func() {
		select {
		case <-ready:
		default:
			close(ready)
		}
	})
	go func() {
		_, _, err := RealExec(ctx, work, "/bin/sh", []string{"-c", "trap '' TERM; sleep 60 & echo ready; wait"}, "")
		runErr = err
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Error("owned sibling cleanup did not finish")
		}
	})
	select {
	case <-ready:
	case <-done:
		t.Fatalf("sibling never started: %v", runErr)
	case <-time.After(10 * time.Second):
		t.Fatal("sibling did not reach native execution")
	}
	anchor := processGroupAnchor(ctx)
	raw, err := os.ReadFile(anchor)
	require.NoError(t, err)
	fields := strings.Fields(string(raw))
	require.Len(t, fields, 5)
	fifo := filepath.Join(filepath.Dir(anchor), fields[2])
	job := filepath.Base(filepath.Dir(anchor))
	r, err := readRunReceipt(d.nativeRunDir(), job)
	require.NoError(t, err)
	require.True(t, procgroup.AnchorPinned(r.PID, anchor))
	cmd := exec.Command(os.Args[0], "-test.run=^TestNativeWallCannotCommandSiblingCleanupChannel$", "-test.count=1")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + work, "NOVA_TEST_NATIVE_CONTROL_WALL=1", "NOVA_TEST_WORK=" + work,
		"NOVA_TEST_STATE=" + state, "NOVA_TEST_CONTROL=" + control, "NOVA_TEST_DENY=" + deny, "NOVA_TEST_ANCHOR=" + anchor, "NOVA_TEST_FIFO=" + fifo}
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	require.NoError(t, cmd.Run(), "actual native wall child: stdout=%s stderr=%s", out.String(), stderr.String())
	assert.FileExists(t, filepath.Join(work, "allowed"), "the negative probe must really execute inside the wall")
	assert.Contains(t, out.String(), "receipt-denied")
	assert.Contains(t, out.String(), "attack-denied")
	assert.NotContains(t, out.String(), "attack-succeeded")
	select {
	case <-done:
		t.Fatalf("sibling was cancelled by native child: %v", runErr)
	default:
	}
	require.True(t, procgroup.AnchorPinned(r.PID, anchor), "native child cannot remove the daemon's live group pin")
	cancel()
	select {
	case <-done:
		require.ErrorContains(t, runErr, "verified process group")
	case <-time.After(15 * time.Second):
		t.Fatal("daemon cannot clean its private sibling channel")
	}
	assert.Eventually(t, func() bool { return !procgroup.GroupRunnable(r.PID) }, 30*time.Second, 20*time.Millisecond)
}

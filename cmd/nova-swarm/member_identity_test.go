package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// identityLane owns every process used by the adoption probes. The kernel mark
// is placed at exec, never patched into a running test process's environment.
func identityLane(t *testing.T, mark string) *exec.Cmd {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("kernel lane identity is available on Linux and macOS")
	}
	cmd := exec.Command("sleep", "600")
	cmd.Env = append(environWithoutName(os.Environ(), swarm.LaneEnv), swarm.LaneEnv+"="+mark)
	ownLaneSession(cmd)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }) // ignored: cleanup of only this fixture-owned child
	require.NotEqual(t, "-", swarm.LaneStartStamp(cmd.Process.Pid))
	return cmd
}

// An old PID reused by a stranger, a changed birth, and a different lane mark all
// refuse adoption before a stop can signal anything or overwrite the launch.
func TestAStaleLanePIDNeverAuthorizesAdoptionOrStop(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"legacy", "wrong-birth", "wrong-mark"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			r, slots, marker := markerRunner(t)
			p := member.Packet{Card: "c1", Kind: "work", Gen: 2, Attempt: 1, Epoch: 7}
			name := launchName(p)
			mark := filepath.Join(slots, name)
			actualMark := mark
			if kind == "wrong-mark" {
				actualMark += "-other"
			}
			stranger := identityLane(t, actualMark)
			pid := stranger.Process.Pid
			record := strconv.Itoa(pid)
			switch kind {
			case "wrong-birth":
				record += " old-birth"
			case "wrong-mark":
				record += " " + swarm.LaneStartStamp(pid)
			}
			path := filepath.Join(slots, name+".pid")
			write(t, path, record+"\n")
			child, err := r.Start(p)
			assert.Nil(t, child)
			require.ErrorContains(t, err, "no matching lane identity")
			assert.Zero(t, r.endLanes(), "an unverified PID never enters the stop set")
			assert.NoError(t, stranger.Process.Signal(syscall.Signal(0)), "the unrelated fixture process remains alive")
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, record+"\n", string(after))
			assert.NoFileExists(t, marker)
			assert.NoDirExists(t, mark)
			c := &nativeChild{proc: stranger.Process, adopted: true, started: "old-birth", mark: actualMark, done: make(chan struct{})}
			assert.Zero(t, c.Stop(), "a formerly adopted PID whose birth changed is never signalled")
			assert.NoError(t, stranger.Process.Signal(syscall.Signal(0)))
		})
	}
}

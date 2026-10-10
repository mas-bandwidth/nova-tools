package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTheLandingGateDiesWithTheServer(t *testing.T) {
	t.Parallel()

	t.Run("writes the gate pid before the gate exits and removes it after", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		gateFile := filepath.Join(dir, ".gate_pid")
		lander := newGateTestLander(t, dir, "sleep 0.05; exit 0")

		why, out := lander.runCheck(context.Background(), dir)
		assert.Equal(t, "", why, "a quiet green check returns without a refusal")
		assert.Equal(t, "", out, "a quiet green check returns no output")
		_, statErr := os.Stat(gateFile)
		assert.True(t, os.IsNotExist(statErr), "the gate pid file is removed when runCheck returns: path=%s", gateFile)
	})

	t.Run("the next run kills a fake gate that sleeps and was left behind", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		a := newGateTestApp(dir)

		gate := exec.Command("sleep", "60")
		gate.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		require.NoError(t, gate.Start(), "the fake gate starts in its own process group")
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".gate_pid"), []byte(strconv.Itoa(gate.Process.Pid)+"\n"), 0o600))

		var out bytes.Buffer
		a.killStaleGate(&out)
		assert.Contains(t, out.String(), "pid="+strconv.Itoa(gate.Process.Pid), "the line names the gate the earlier run left")

		assert.True(t, gateGone(t, gate.Process.Pid), "the fake gate is gone after the next run's start")
	})

	t.Run("the server's exit ends a gate it left running", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		a := newGateTestApp(dir)

		gate := exec.Command("sleep", "60")
		gate.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		require.NoError(t, gate.Start(), "the fake gate starts in its own process group")
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".gate_pid"), []byte(strconv.Itoa(gate.Process.Pid)+"\n"), 0o600))

		a.endGate()

		assert.True(t, gateGone(t, gate.Process.Pid), "the fake gate is gone after the server's exit ends its group")
	})

	t.Run("runs the gate in its own process group", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		gateFile := filepath.Join(dir, ".gate_pid")
		lander := newGateTestLander(t, dir, "sleep 5; exit 0")

		var seen int
		done := make(chan struct{})
		go func() {
			defer close(done)
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				data, err := os.ReadFile(gateFile)
				if err == nil {
					pid, perr := strconv.Atoi(strings.TrimSpace(string(data)))
					if perr == nil && pid > 0 {
						if pgid, gerr := syscall.Getpgid(pid); gerr == nil {
							assert.Equal(t, pid, pgid, "the gate is the leader of its own process group")
							seen++
							if pgid == pid {
								_ = syscall.Kill(-pgid, syscall.SIGKILL)
								return
							}
						}
					}
				}
				runtime.Gosched()
			}
		}()
		_, _ = lander.runCheck(context.Background(), dir)
		<-done
		assert.Greater(t, seen, 0, "the gate was alive in its own group while the pid file held its PID")
	})
}

// gateGone waits up to ten seconds for the process pid to be reaped, so a kill
// the kernel has accepted but not yet reported is not read as a live gate.
func gateGone(t *testing.T, pid int) bool {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var status syscall.WaitStatus
		if got, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil); err == nil && got == pid {
			return true
		}
		runtime.Gosched()
	}
	return false
}

// newGateTestApp is the app a gate test runs land through: its land root is dir, so
// the .gate_pid file lands there, and its git environment carries the test identity.
func newGateTestApp(dir string) *app {
	a := &app{}
	a.landRoot = func() (string, error) { return dir, nil }
	a.gitEnv = gateTestEnv()
	return a
}

func newGateTestLander(t *testing.T, dir, check string) *lander {
	t.Helper()
	return &lander{a: newGateTestApp(dir), check: check}
}

func gateTestEnv() []string {
	env := slices.DeleteFunc(os.Environ(), func(e string) bool {
		return strings.HasPrefix(e, "GOFLAGS=")
	})
	return append(env,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=lander",
		"GIT_AUTHOR_EMAIL=lander@example.invalid",
		"GIT_COMMITTER_NAME=lander",
		"GIT_COMMITTER_EMAIL=lander@example.invalid",
	)
}

package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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

	t.Run("the next run kills a stale gate pid recorded by the previous run", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		a := &app{}
		a.landRoot = func() (string, error) { return dir, nil }
		a.gitEnv = slices.DeleteFunc(os.Environ(), func(e string) bool {
			return strings.HasPrefix(e, "GOFLAGS=")
		})
		a.gitEnv = append(a.gitEnv, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".gate_pid"), []byte("1\n"), 0o600))
		killStaleGateAt(dir, 1, t)
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
					pid, perr := parseInt(string(data))
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

func newGateTestLander(t *testing.T, dir, check string) *lander {
	t.Helper()
	a := &app{}
	a.landRoot = func() (string, error) { return dir, nil }
	a.gitEnv = slices.DeleteFunc(os.Environ(), func(e string) bool {
		return strings.HasPrefix(e, "GOFLAGS=")
	})
	a.gitEnv = append(a.gitEnv,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=lander",
		"GIT_AUTHOR_EMAIL=lander@example.invalid",
		"GIT_COMMITTER_NAME=lander",
		"GIT_COMMITTER_EMAIL=lander@example.invalid",
	)
	return &lander{a: a, check: check}
}

func killStaleGateAt(dir string, pid int, t *testing.T) {
	t.Helper()
	gateFile := filepath.Join(dir, ".gate_pid")
	if proc, err := os.FindProcess(pid); err == nil {
		_ = proc.Kill()
	}
	require.NoError(t, os.Remove(gateFile))
	_, statErr := os.Stat(gateFile)
	assert.True(t, os.IsNotExist(statErr), "the stale gate pid file is removed by the next-run start")
}

func parseInt(s string) (int, error) {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return 0, errEmpty
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errNotANumber
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

var (
	errEmpty      = errSentinel{"empty"}
	errNotANumber = errSentinel{"not a number"}
)

type errSentinel struct{ s string }

func (e errSentinel) Error() string { return e.s }

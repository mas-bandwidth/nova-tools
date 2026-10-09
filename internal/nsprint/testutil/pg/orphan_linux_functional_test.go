//go:build linux && functional

package pg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A killed test binary runs no cleanup. Its private Postgres must still die,
// and neither it nor the watcher may inherit an injected coordinator FD.
func TestPostgresDiesWithItsTestBinary(t *testing.T) {
	t.Parallel()
	const mark = "NOVA_PG_ABORT_HELPER"
	if os.Getenv(mark) == "1" {
		dir := os.Getenv("NOVA_PG_ABORT_DIR")
		s, err := StartServer(dir)
		require.NoError(t, err)
		fields, err := processFields(s.cmd.Process.Pid)
		require.NoError(t, err)
		require.Equal(t, strconv.Itoa(s.watch.group), fields[2])
		record := fmt.Sprintf("%d %d %s %s", s.cmd.Process.Pid, s.watch.group, s.Port, fields[19])
		require.NoError(t, os.WriteFile(filepath.Join(dir, "ready"), []byte(record), 0600))
		select {}
	}
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "coordinator.lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	require.NoError(t, err)
	defer lock.Close()
	exe, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := subproc.Long(ctx, exe, "-test.run=^TestPostgresDiesWithItsTestBinary$")
	cmd.Env = append(os.Environ(), mark+"=1", "NOVA_PG_ABORT_DIR="+dir)
	cmd.ExtraFiles = []*os.File{lock}
	require.NoError(t, cmd.Start())
	var pid, watcher int
	var port, birth string
	helperWait := make(chan error, 1)
	go func() { helperWait <- cmd.Wait() }()
	helperEnded := false
	t.Cleanup(func() {
		if !helperEnded {
			if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				assert.NoError(t, err, "owned helper cleanup")
			}
			select {
			case <-helperWait:
			case <-time.After(15 * time.Second):
				assert.Fail(t, "owned helper did not join during cleanup")
			}
		}
		if watcher > 0 {
			// Observation only: a reaped watcher's numeric group cannot authorize a signal.
			assert.Eventually(t, func() bool { return !groupRunning(watcher) },
				15*time.Second, 10*time.Millisecond, "owned postgres group remains runnable during cleanup")
		}
	})
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(filepath.Join(dir, "ready"))
		if err == nil {
			if n, scanErr := fmt.Sscanf(string(body), "%d %d %s %s", &pid, &watcher, &port, &birth); scanErr == nil && n == 4 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.Positive(t, pid, "nested fixture never became ready")
	postmaster, err := os.FindProcess(pid)
	require.NoError(t, err)
	t.Cleanup(func() {
		// ignored: releasing the probe cannot signal any process.
		_ = postmaster.Release()
	})
	fields, err := processFields(pid)
	require.NoError(t, err)
	require.Equal(t, birth, fields[19], "postmaster identity changed before ownership binding")
	require.Equal(t, strconv.Itoa(watcher), fields[2], "postmaster left its owned group")
	t.Cleanup(func() {
		// A stopped postmaster cannot act on pg_ctl even after a failed assertion.
		// ignored: only this pinned disposable process is killed, including on failure.
		if err := postmaster.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			assert.NoError(t, err, "pinned postmaster cleanup")
		}
		// Even a failed assertion must not leave this exact disposable server.
		bin, err := Binaries()
		if err == nil {
			stop, release := subproc.CommandFor(context.Background(), pgToolBudget, filepath.Join(bin, "pg_ctl"), "-D", filepath.Join(dir, "data"), "-m", "immediate", "-w", "stop")
			out, stopErr := stop.CombinedOutput()
			release()
			// A redundant stop may refuse an already-gone server; group quiet is
			// the proof that makes that refusal harmless.
			if stopErr != nil && groupRunning(watcher) {
				assert.NoError(t, stopErr, "pg_ctl cleanup left a runnable owned group: %s", out)
			}
		}
	})
	for _, process := range []int{pid, watcher} {
		entries, err := os.ReadDir(filepath.Join("/proc", strconv.Itoa(process), "fd"))
		require.NoError(t, err)
		for _, entry := range entries {
			target, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(process), "fd", entry.Name()))
			if err == nil {
				require.NotEqual(t, lockPath, target, "process %d inherited coordinator FD", process)
			}
		}
	}
	// A stopped postmaster cannot act on TERM. Parent death must still make
	// the watcher escalate while its live process pins the private group.
	fields, err = processFields(pid)
	require.NoError(t, err)
	require.Equal(t, birth, fields[19], "postmaster identity changed before stop")
	require.Equal(t, strconv.Itoa(watcher), fields[2], "postmaster left its owned group")
	require.NoError(t, postmaster.Signal(syscall.SIGSTOP))
	require.Eventually(t, func() bool {
		body, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
		if err != nil {
			return false
		}
		end := strings.LastIndexByte(string(body), ')')
		return end >= 0 && strings.HasPrefix(strings.TrimSpace(string(body[end+1:])), "T ")
	}, 15*time.Second, 10*time.Millisecond, "owned postmaster never stopped")
	require.NoError(t, cmd.Process.Kill())
	select {
	case <-helperWait:
		helperEnded = true
	case <-time.After(15 * time.Second):
		require.FailNow(t, "killed helper did not join")
	}
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !running(pid) && !running(watcher) && !groupRunning(watcher) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.False(t, running(pid), "Postgres %d outlived killed test binary on %s", pid, port)
	require.False(t, running(watcher), "watcher %d outlived killed test binary", watcher)
	require.False(t, groupRunning(watcher), "a Postgres worker outlived process group %d", watcher)
}

func running(pid int) bool {
	body, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	fields := strings.Fields(string(body))
	return len(fields) > 2 && fields[2] != "Z"
}

func groupRunning(group int) bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return true
	}
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		body, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		fields := strings.Fields(string(body))
		if len(fields) < 5 || fields[2] == "Z" {
			continue
		}
		pgrp, err := strconv.Atoi(fields[4])
		if err == nil && pgrp == group {
			return true
		}
	}
	return false
}

// processFields reads the owned process identity and group from Linux stat.
func processFields(pid int) ([]string, error) {
	body, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return nil, err
	}
	end := strings.LastIndexByte(string(body), ')')
	if end < 0 {
		return nil, fmt.Errorf("process %d has malformed stat", pid)
	}
	fields := strings.Fields(string(body[end+1:]))
	if len(fields) < 20 {
		return nil, fmt.Errorf("process %d has incomplete stat", pid)
	}
	return fields, nil
}

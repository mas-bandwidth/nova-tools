//go:build !windows

package main

// landproc_test.go pins the landing step's process side (docs/SPEC-SPRINT.md
// section 7, the tree gate): every child the landing step starts runs in a
// process group of its own, the server's exit ends that group, and a run's
// start ends a gate an earlier run left under the land root.

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// gateWait is the generous poll bound the waits rule names: the fake gate is a
// subprocess and its start is the event polled for, never a fixed sleep.
const gateWait = 30 * time.Second

// fakeGate writes the fake gate's shell script: run as `test`, it records its
// own pid (the group leader's) and its sleeping child's, then waits to be
// killed. goRun takes its program from the run's first word, so an absolute
// path stands in for `go`.
func fakeGate(t *testing.T, root string) (gate, pidfile string) {
	t.Helper()
	dir := filepath.Join(root, "fake")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	gate, pidfile = filepath.Join(dir, "gate.sh"), filepath.Join(dir, "pids")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = test ]; then\n" +
		"  echo $$ > " + pidfile + "\n" +
		"  sleep 300 &\n" +
		"  echo $! >> " + pidfile + "\n" +
		"  wait\n" +
		"fi\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(gate, []byte(script), 0o755))
	return gate, pidfile
}

// waitFakeGate waits for the fake gate to record its group leader and its
// sleeping child and returns both.
func waitFakeGate(t *testing.T, pidfile string) (pgid, child int) {
	t.Helper()
	var fields []string
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(pidfile)
		if err != nil {
			return false
		}
		fields = strings.Fields(string(b))
		return len(fields) >= 2
	}, gateWait, 20*time.Millisecond, "the fake gate did not start")
	var err error
	pgid, err = strconv.Atoi(fields[0])
	require.NoError(t, err, "the gate's pid")
	child, err = strconv.Atoi(fields[1])
	require.NoError(t, err, "the gate's child pid")
	return pgid, child
}

// pidAlive reports whether a pid names a live process. A pid that is a zombie
// until its group is reaped reads alive; the polls above wait it out.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// The landing step's gate runs in a process group of its own, and the server's
// exit ends that group before the process goes, so no test binary outlives its
// server (the orphan of 2026-10-06: run --land exited 4 on a tick past its
// deadline and left ci.test at 310% CPU under launchd, parent pid 1), and an
// earlier run's gate is ended when the landing step starts again.
func TestTheLandingGateDiesWithTheServer(t *testing.T) {
	t.Parallel()

	t.Run("the run loop's exit ends the gate's group", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		gate, pidfile := fakeGate(t, root)

		l := newDeadlineLoop(t)
		l.ta.a.landRoot = func() (string, error) { return root, nil }
		land := &lander{a: l.ta.a, root: root}

		done := make(chan struct{})
		go func() {
			defer close(done)
			// ignored: the gate is killed at the exit, and the kill is the error
			// the test asserts on
			_, _ = land.goRun(context.Background(), root, []string{gate, "test"})
		}()
		pgid, child := waitFakeGate(t, pidfile)
		require.True(t, landGroupAlive(pgid), "the gate's group runs before the exit")
		record := filepath.Join(landGatesDir(root), strconv.Itoa(pgid))
		require.Eventually(t, func() bool { _, err := os.Stat(record); return err == nil }, gateWait, 20*time.Millisecond, "the gate is recorded under the land root")

		// the server is wedged: three given-up ticks in a row exit 4, and the
		// landing step's groups go before the process does
		s := newAfterScript("deadline", "further", "stop")
		l.ta.a.after = s.after
		l.ta.a.tickFn = func(ctx context.Context, _ *store.Store) (store.TickResult, error) {
			l.tick()
			return s.deaf(ctx)
		}
		var out, errb bytes.Buffer
		assert.False(t, l.ta.a.runLoop(context.Background(), l.st, 20, 10, &out, &errb))
		assert.Equal(t, []int{exitTickDeadline}, l.exits)

		require.Eventually(t, func() bool {
			select {
			case <-done:
				return true
			default:
				return false
			}
		}, gateWait, 20*time.Millisecond, "the landing run did not end after the loop's exit ended its group")
		assert.Eventually(t, func() bool { return !landGroupAlive(pgid) }, gateWait, 20*time.Millisecond, "the gate's group is ended with the server")
		assert.Eventually(t, func() bool { return !pidAlive(child) }, gateWait, 20*time.Millisecond, "the gate's sleeping child is ended with the group")
	})

	t.Run("a gate an earlier run left is ended at start", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		gates := landGatesDir(root)
		require.NoError(t, os.MkdirAll(gates, 0o755))

		// an earlier server's gate: a sleeping process in a group of its own,
		// with the record the start reads. It is started with the group alone:
		// a bare exec.Command carries no context to cancel.
		cmd := exec.Command("sleep", "300")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		require.NoError(t, cmd.Start())
		pgid := cmd.Process.Pid
		defer killLandGroup(pgid)
		require.NoError(t, os.WriteFile(filepath.Join(gates, strconv.Itoa(pgid)), []byte(strconv.Itoa(pgid)+"\n"), 0o644))
		require.True(t, landGroupAlive(pgid), "the earlier run's gate runs")

		a := newApp(func(string) string { return "" })
		a.landRoot = func() (string, error) { return root, nil }
		var out bytes.Buffer
		a.endStaleLandGates(&out)

		// ignored: the process was killed on purpose, and the assertion is its
		// liveness, not how it ended
		_ = cmd.Wait()
		assert.False(t, landGroupAlive(pgid), "the earlier run's gate is ended")
		assert.NoFileExists(t, filepath.Join(gates, strconv.Itoa(pgid)), "its record is removed")
		assert.Contains(t, out.String(), "gate process group", out.String())
		assert.Contains(t, out.String(), strconv.Itoa(pgid), out.String())
	})
}

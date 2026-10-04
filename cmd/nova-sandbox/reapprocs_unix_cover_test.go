//go:build darwin || linux

// The unit tests for reapprocs_unix.go's two direct answers to the process
// table. They call signalProcess and processAlive themselves rather than the
// reap verb's seams, so the unix bodies are the code under test. processesUnder
// is not reached here: every path through it starts lsof, and a unit test runs
// no child. Its parsing and its lsof-exit answer are therefore carried by the
// seam in reapverb_test.go instead, and this file says so rather than pretending
// to cover it.
package main

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// notAProcess is the pid table both functions refuse before they reach the
// process table: PID 0 and PID 1, which the tool never signals or asks about,
// and a negative pid, which names a process group and not a process.
var notAProcess = map[string]int{"zero": 0, "one": 1, "negative": -1}

func TestReapprocsUnixCoverSignalProcess(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name string
		pid  int
		want string
	}{
		{name: "this process", pid: syscall.Getpid()},
	}
	for name, pid := range notAProcess {
		rows = append(rows, struct {
			name string
			pid  int
			want string
		}{name: name, pid: pid, want: "is not a process this tool signals"})
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			// Signal 0 delivers nothing, so the live row proves the call is
			// forwarded to the kernel without this test sending a real signal.
			err := signalProcess(row.pid, syscall.Signal(0))
			if row.want == "" {
				assert.NoErrorf(t, err, "pid %d: signal 0 is a delivery the kernel accepts", row.pid)
				return
			}
			require.Errorf(t, err, "pid %d: want a refusal", row.pid)
			assert.ErrorContainsf(t, err, row.want, "pid %d refusal", row.pid)
		})
	}
}

func TestReapprocsUnixCoverProcessAlive(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name string
		pid  int
		want bool
	}{
		{name: "this process", pid: syscall.Getpid(), want: true},
	}
	for name, pid := range notAProcess {
		rows = append(rows, struct {
			name string
			pid  int
			want bool
		}{name: name, pid: pid, want: false})
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			assert.Equalf(t, row.want, processAlive(row.pid), "processAlive(%d)", row.pid)
		})
	}
}

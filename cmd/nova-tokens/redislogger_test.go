package main

import (
	"bytes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// asToolEnv makes the test binary run as nova-tokens (TestMain).
const asToolEnv = "NOVA_TOKENS_AS_TOOL"

// #3463: `report --redis` at an address nothing listens on exited 1 with the right typed
// line, but go-redis's own logger wrote four untyped, local-time "connection pool: failed
// to dial after 5 attempts" lines to the process's stderr ahead of it. run's stderr is an
// injected writer and the library writes to os.Stderr, so an in-process test cannot see the
// leak: this runs the tool as its own process and holds the WHOLE stderr to the one line.
func TestReportRedisDialFailureStderrIsTheOneFailedLine(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.False(t, err != nil, err)
	addr := ln.Addr().String()
	ln.Close() // nothing listens there now: every dial is refused

	self, err := os.Executable()
	require.False(t, err != nil, err)
	cmd := exec.Command(self, "report", "--redis", addr, "--month", "2026-09")
	cmd.Env = append(os.Environ(), asToolEnv+"=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	exit := 0
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if err != nil {
		require.FailNow(t, "%v", err)
	}
	assert.False(t, exit != 1, "exit %d, want 1\nstdout:\n%s\nstderr:\n%s", exit, stdout.String(), stderr.String())
	assert.False(t, stdout.Len() != 0, "stdout is not empty on a dial failure:\n%s", stdout.String())
	got := stderr.String()
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	assert.False(t, len(lines) != 1 || !strings.HasSuffix(got, "\n") ||
		!strings.HasPrefix(lines[0], "REPORT FAILED store=redis err=") ||
		!strings.Contains(lines[0], addr), "stderr of a dial failure must be exactly one `REPORT FAILED store=redis err=... %s ...` line, got %d line(s):\n%s", addr, len(lines), got)
}

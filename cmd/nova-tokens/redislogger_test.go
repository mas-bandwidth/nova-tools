package main

import (
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close()) // nothing listens there now: every dial is refused

	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(self, "report", "--redis", addr, "--month", "2026-09")
	cmd.Env = append(os.Environ(), asToolEnv+"=1")
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	var exit *exec.ExitError
	require.ErrorAs(t, cmd.Run(), &exit)
	assert.Equal(t, 1, exit.ExitCode(), "stderr:\n%s", stderr.String())
	assert.Empty(t, stdout.String(), "stdout on a dial failure")
	assert.Regexp(t, `^REPORT FAILED store=redis err=[^\n]*`+regexp.QuoteMeta(addr)+`[^\n]*\n$`, stderr.String(), "stderr of a dial failure is exactly one REPORT FAILED line naming the address")
}

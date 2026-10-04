//go:build unix

package testredis

import (
	"errors"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Unit cover for the unix sentry's helpers in sentry_unix.go. join and enlist's
// refusals are reached through the package's own seams (the spec's exe), with no
// store, no clock and no child process: a spec whose binary is missing fails at
// the exe seam or at Start, and no process is ever created. enlist's standing
// path forks the sentry binary, and killGroup kills the caller's whole process
// group, this test binary included; neither is reachable in a unit test, and
// both run under the functional tier, where the processes are the test's own.

func TestSentryUnixCoverJoinPutsAServerInTheSentrysGroup(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		group int
		carry *syscall.SysProcAttr // the group attributes the server already carries, when any
		want  *syscall.SysProcAttr
	}{
		{
			name:  "a server joins the sentry's group between fork and exec",
			group: 4242,
			want:  &syscall.SysProcAttr{Setpgid: true, Pgid: 4242},
		},
		{
			name:  "a server that already carries a group is re-joined: the sentry's group replaces it whole",
			group: 4242,
			carry: &syscall.SysProcAttr{Setpgid: true, Pgid: 1},
			want:  &syscall.SysProcAttr{Setpgid: true, Pgid: 4242},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd := &exec.Cmd{SysProcAttr: tc.carry}
			join(cmd, tc.group)
			assert.Equal(t, tc.want, cmd.SysProcAttr, "the server is set into the sentry's group, and nothing of the old one survives")
		})
	}
}

func TestSentryUnixCoverEnlistRefusesASentryItCannotStart(t *testing.T) {
	t.Parallel()
	gone := filepath.Join(t.TempDir(), "gone.test")
	notFound := errors.New("no /proc")
	cases := []struct {
		name      string
		exe       func() (string, error)
		wantText  string
		wantCause error
	}{
		{
			name:      "the test binary is not found: there is no copy to stand sentry",
			exe:       func() (string, error) { return "", notFound },
			wantText:  "which was not found",
			wantCause: notFound,
		},
		{
			name:      "the binary is gone: the sentry did not start, and nothing was forked",
			exe:       func() (string, error) { return gone, nil },
			wantText:  "did not start",
			wantCause: syscall.ENOENT,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			at, err := enlist(sentrySpec{exe: tc.exe, wait: time.Minute})
			require.Error(t, err, "a sentry that cannot start is a refusal, not a server")
			require.Nil(t, at, "a refused enlist leaves no standing sentry")
			assert.ErrorContains(t, err, tc.wantText, "the refusal names what went wrong")
			assert.ErrorIs(t, err, tc.wantCause, "the cause the exe seam or Start reported survives the wrap")
			assert.Contains(t, err.Error(), "the sentry", "the refusal says it is the sentry that failed")
		})
	}
}

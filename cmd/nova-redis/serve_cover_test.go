package main

// serve_cover_test.go is the per-function cover for launchRedis in serve.go,
// the one function the unit tier's per-function coverage table held at 0.0%
// (go tool cover -func). launchRedis IS the subprocess seam: its success arm
// starts a child and returns nil, and the unit tier may not start one, so that
// arm is left to the functional tier's TestRestartOnTheSameDirKeepsTheStore,
// which runs launchRedis against a throwaway redis-server. What the unit tier
// can reach is the refusal arm and the cancellation guard without a child:
// exec resolves the program before it forks, so a name no PATH carries and a
// path that is a directory both fail before any process starts, and the guard
// that answers nil for a child that stopped cleanly never fires on them
// because a child that never started has no successful ProcessState to read.

import (
	"bytes"
	"context"
	"io/fs"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
)

// coverNoRedisProgram is a program name no machine carries on PATH, so
// launchRedis's program resolution fails and no child is ever forked.
const coverNoRedisProgram = "nova-redis-cover-no-such-program"

// TestServeCoverLaunchRedisRefusesAProgramThatCannotStart pins launchRedis's
// refusal arm and its cancellation guard in one table: a program the PATH does
// not carry, and a path that is a directory, each come back as the error its
// resolution produced; and a cancelled context does not turn either into the
// nil launchRedis answers for a child that stopped cleanly, because the guard
// requires a successful ProcessState a child that never started does not have.
// Nothing reaches the child's stdout or stderr, and no child is started.
func TestServeCoverLaunchRedisRefusesAProgramThatCannotStart(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cases := []struct {
		name    string
		program string
		cancel  bool
		wantIs  error
	}{
		{name: "a name no PATH carries", program: coverNoRedisProgram, wantIs: exec.ErrNotFound},
		{name: "a path that is a directory", program: dir, wantIs: fs.ErrPermission},
		{name: "a name no PATH carries under a cancelled context", program: coverNoRedisProgram, cancel: true, wantIs: exec.ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			if tc.cancel {
				var stop context.CancelFunc
				ctx, stop = context.WithCancel(ctx)
				stop()
			}
			var stdout, stderr bytes.Buffer
			err := launchRedis(ctx, launchSpec{
				Program: tc.program,
				Args:    []string{"-"},
				Env:     []string{"NOVA_REDIS_COVER=1"},
				Config:  []byte("port 0\n"),
				Dir:     dir,
			}, &stdout, &stderr)

			if assert.Error(t, err, "launchRedis(%q) answered no error for a program that cannot start", tc.program) {
				assert.ErrorIs(t, err, tc.wantIs, "launchRedis(%q) returned %v, want the resolution error %v", tc.program, err, tc.wantIs)
			}
			assert.Empty(t, stdout.String(), "launchRedis(%q) wrote to the child's stdout: %q", tc.program, stdout.String())
			assert.Empty(t, stderr.String(), "launchRedis(%q) wrote to the child's stderr: %q", tc.program, stderr.String())
		})
	}
}

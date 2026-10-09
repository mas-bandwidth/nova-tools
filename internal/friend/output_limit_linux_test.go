//go:build linux

package friend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/procgroup"
	"github.com/stretchr/testify/require"
)

func TestRealExecOutputLimitCancelsAndPreservesRefusal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, script string
		overflow     bool
	}{
		{"exact", "printf 12345678", false},
		{"stdout", "yes output", true},
		{"resistant-stderr-child", "trap '' PIPE TERM; (trap '' PIPE TERM; while :; do printf 'output-output-output\\n'; done) >&2 & wait", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			job := "output~1"
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", job), 0o755))
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			ctx = context.WithValue(ctx, execOutputLimitKey{}, 8)
			ctx = context.WithValue(ctx, execSeparateOutputKey{}, true)
			anchorPath := runReceiptPath(dir, job) + ".anchor"
			ctx = WithProcessGroupAnchor(ctx, anchorPath)
			pid := 0
			defer func() {
				if pid > 0 && procgroup.GroupRunnable(pid) {
					cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					require.True(t, procgroup.ReapVerified(cleanupCtx, pid, "", anchorPath, 100*time.Millisecond))
				}
			}()
			ctx = WithProcessStarted(ctx, func(started int) error {
				pid = started
				return writeRunReceipt(dir, job, runReceipt{RunID: "output-run", PID: pid, Identity: ProcessIdentity(pid)})
			})
			before := time.Now()
			out, exit, err := realExec(ctx, 100*time.Millisecond, dir, "/bin/sh", []string{"-c", tc.script}, "")
			t.Logf("native output case %s returned in %s", tc.name, time.Since(before))
			require.LessOrEqual(t, len(out), 8)
			require.False(t, procgroup.GroupRunnable(pid), "output cancellation must reap the verified native group")
			if tc.overflow {
				require.ErrorIs(t, err, errNativeOutputLimit, "ExitError/context normalization cannot erase the output refusal")
				require.NoError(t, ctx.Err(), "overflow must stop the resistant child before the caller's generous deadline")
			} else {
				require.NoError(t, err)
				require.Zero(t, exit)
				require.Equal(t, "12345678", out, "exactly the byte limit is a complete response")
			}
		})
	}
}

func TestSetupPreservesSafeOutputLimitRefusal(t *testing.T) {
	t.Parallel()
	fs := NewMemFS()
	require.NoError(t, fs.MkdirAll("/private", 0700))
	r := SetupRunner{FS: fs, Identity: func(int) string { return "birth" }, GroupRunnable: func(int) bool { return false }, Nonce: func() string { return "run" }, Lock: func(string) (func() error, error) { return func() error { return nil }, nil }}
	r.Exec = func(ctx context.Context, _, _ string, _ []string, _ string) (string, int, error) {
		require.NoError(t, processStarted(ctx)(42))
		return "sensitive native output", 7, errors.Join(errNativeOutputLimit, errors.New("sensitive native error"))
	}
	validated, later := false, false
	plan := SetupPlan{Steps: []SetupStep{{Name: "bounded", Command: &SetupCommand{Program: "/native", Dir: "/private", Env: []string{"HOME=/private", "PATH=/bin"}, Timeout: time.Minute, AllowedExits: []int{7}}, Validate: func(SetupOutput) ([]SetupFact, error) { validated = true; return nil, nil }}, {Apply: func() ([]SetupFact, error) { later = true; return nil, nil }}}}
	_, err := r.Apply(context.Background(), plan, filepath.Join(t.TempDir(), "state"))
	require.ErrorIs(t, err, errNativeOutputLimit)
	require.NotContains(t, err.Error(), "sensitive")
	require.False(t, validated, "an allowed exit cannot accept a truncated response")
	require.False(t, later)
}

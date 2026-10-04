package friendwatch

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"

	"github.com/stretchr/testify/require"
)

func TestBeatFailureCancelsOwnedWait(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "beat")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\nprintf 'store refused' >&2\nexit 7\n"), 0700))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := Run(ctx, Options{Sprint: p, Server: "actual:1", Friend: "reader", Argv: []string{"/bin/sleep", "30"}, Every: time.Millisecond, Timeout: time.Second, Parent: os.Getppid()})
	require.ErrorContains(t, err, "store refused")
	require.NoError(t, ctx.Err(), "owned wait must be cancelled before the outer deadline")
}

func TestClosedHarnessPipeStopsWait(t *testing.T) {
	t.Parallel()
	r, w := io.Pipe()
	require.NoError(t, w.Close())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := Run(ctx, Options{Sprint: "/usr/bin/true", Server: "actual:1", Friend: "reader", Argv: []string{"/bin/sleep", "30"}, Every: 10 * time.Millisecond, Timeout: time.Second, Parent: os.Getppid(), StdinLifetime: true, Stdin: r})
	require.ErrorContains(t, err, "pipe closed")
	require.NoError(t, ctx.Err())
}

// activeOutput observes the actual owned child's startup without fixed sleeps.
type activeOutput struct {
	once   sync.Once
	active chan struct{}
}

func (w *activeOutput) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.active) })
	return len(p), nil
}

func TestActiveWaitEndsWithInvocation(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"context", "pipe"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			r, w := io.Pipe()
			defer w.Close() // ignored: cleanup closes a test-owned pipe; the assertion observes cancellation separately.
			output := &activeOutput{active: make(chan struct{})}
			done := make(chan error, 1)
			go func() {
				done <- Run(ctx, Options{Sprint: "/usr/bin/true", Server: "actual:1", Friend: "reader", Argv: []string{"/bin/sh", "-c", "printf active; exec sleep 30"}, Every: 10 * time.Millisecond, Timeout: time.Second, Parent: os.Getppid(), StdinLifetime: true, Stdin: r, Stdout: output})
			}()
			select {
			case <-output.active:
			case err := <-done:
				t.Fatalf("owned child ended before becoming active: %v", err)
			}
			if mode == "context" {
				cancel()
			} else {
				require.NoError(t, w.Close())
			}
			err := <-done
			if mode == "context" {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorContains(t, err, "pipe closed")
			}
		})
	}
}

func TestCompletedChildClosesOwnedLifetimeMonitor(t *testing.T) {
	t.Parallel()
	r, w := io.Pipe()
	defer w.Close() // ignored: the test-owned writer is closed after the reader assertion.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, Run(ctx, Options{Sprint: "/usr/bin/true", Server: "actual:1", Friend: "reader", Argv: []string{"/usr/bin/true"}, Every: time.Second, Timeout: time.Second, Parent: os.Getppid(), StdinLifetime: true, Stdin: r}))
	_, err := w.Write([]byte("after completion"))
	require.ErrorIs(t, err, io.ErrClosedPipe, "normal completion must release its owned monitor")
}

func TestUnrelatedParentCannotClaimPresence(t *testing.T) {
	t.Parallel()
	err := Run(context.Background(), Options{Sprint: "/usr/bin/true", Server: "actual:1", Friend: "reader", Argv: []string{"/bin/sleep", "30"}, Every: time.Second, Timeout: time.Second, Parent: os.Getppid() + 100000})
	require.ErrorContains(t, err, "actual invoking parent")
}

func TestExplicitServerRemovesPoisonedLocalStoreEnvironment(t *testing.T) {
	t.Parallel()
	got := beatEnvironment([]string{"NOVA_SPRINT_REDIS=mem:wrong", "NOVA_REDIS_ADDR=wrong:1", "NOVA_SPRINT_REDIS_USER=wrong", "NOVA_SPRINT_REDIS_PASSWORD_ENV=WRONG", "NOVA_SPRINT_SERVER=wrong:2", "PATH=/bin", "OTHER=kept"}, "actual:3")
	require.Equal(t, []string{"PATH=/bin", "OTHER=kept", "NOVA_SPRINT_SERVER=actual:3"}, got)
}

func TestActualInvokingParentExitStopsActiveWait(t *testing.T) {
	t.Parallel()
	cmd, release := subproc.CommandFor(t.Context(), 30*time.Second, os.Args[0], "-test.run=^TestWatchParentProcess$")
	defer release()
	cmd.Env = append(os.Environ(), "NOVA_WATCH_TEST_ROLE=parent")
	body, err := cmd.CombinedOutput()
	require.NoError(t, err, string(body))
	require.Contains(t, string(body), "invoking harness parent exited")
}

// TestWatchParentProcess provides real parent exit, without unrelated process kills.
func TestWatchParentProcess(t *testing.T) {
	t.Parallel()
	switch os.Getenv("NOVA_WATCH_TEST_ROLE") {
	case "parent":
		r, w, err := os.Pipe()
		require.NoError(t, err)
		cmd := subproc.Long(t.Context(), os.Args[0], "-test.run=^TestWatchParentProcess$")
		cmd.Env = append(os.Environ(), "NOVA_WATCH_TEST_ROLE=watch")
		cmd.ExtraFiles = []*os.File{w}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		require.NoError(t, cmd.Start())
		require.NoError(t, w.Close())
		var b [1]byte
		_, err = r.Read(b[:])
		require.NoError(t, err)
		os.Exit(0)
	case "watch":
		ready := os.NewFile(3, "ready")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := Run(ctx, Options{Sprint: "/usr/bin/true", Server: "actual:1", Friend: "reader", Argv: []string{"/bin/sh", "-c", "printf active; exec sleep 30"}, Every: 10 * time.Millisecond, Timeout: time.Second, Parent: os.Getppid(), Stdout: ready})
		fmt.Fprintln(os.Stdout, err)
		if err == nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
}

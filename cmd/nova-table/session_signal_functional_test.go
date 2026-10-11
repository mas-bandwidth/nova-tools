//go:build functional && !windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"github.com/redis/go-redis/v9"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"

	"github.com/stretchr/testify/require"
)

// Re-exec only this test binary; the helper receives an explicit disposable
// store and ordinary input. It exercises the process's actual signal handlers.
func TestShellSignalHelper(t *testing.T) {
	t.Parallel()
	for i, arg := range os.Args {
		if arg == "--shell-signal-helper" {
			os.Exit(run([]string{"shell", "--redis", os.Args[i+1]}, os.Stdout, os.Stderr))
		}
	}
}

func TestShellWatchDistinguishesTerminateAndInterrupt(t *testing.T) {
	t.Parallel()
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			t.Parallel()
			addr := firstRunStore(t)
			admin := redis.NewClient(&redis.Options{Addr: addr})
			defer admin.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			for _, args := range [][]string{{"create", "jobs", "--columns", "ready"}, {"row", "add", "jobs", "existing"}} {
				{
					code, out, errs := runTable(at(addr, args...)...)
					require.EqualValues(t, 0, code, "setup %d %s %s", code, out, errs)
				}
			}
			exe, err := os.Executable()
			require.NoError(t, err, "%v", err)
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestShellSignalHelper$", "--", "--shell-signal-helper", addr)
			cmd.Dir = t.TempDir()
			for _, env := range os.Environ() {
				if !strings.HasPrefix(env, "NOVA_") {
					cmd.Env = append(cmd.Env, env)
				}
			}
			cmd.Stdin = strings.NewReader("watch jobs\nrow add jobs after-signal\nquit\n")
			stdout, err := cmd.StdoutPipe()
			require.NoError(t, err, "%v", err)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			require.NoError(t, cmd.Start())
			// A rendered frame proves watch installed its handlers and reached Redis.
			reader := bufio.NewReader(stdout)
			{
				_, err := reader.ReadString('\n')
				require.NoError(t, err, "first frame: %v", err)
			}
			require.NoError(t, cmd.Process.Signal(sig))
			err = cmd.Wait()
			require.NoError(t, ctx.Err(), "session did not finish after %v: %v", sig, ctx.Err())
			if sig == syscall.SIGTERM {
				status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
				require.True(t, ok, "SIGTERM: error=%v status=%v stderr=%s", err, cmd.ProcessState, &stderr)
				require.Equal(t, sig, status.Signal(), "SIGTERM: error=%v status=%v stderr=%s", err, cmd.ProcessState, &stderr)
			} else {
				require.NoError(t, err, "SIGINT: %v stderr=%s", err, &stderr)
			}
			tab, err := ntable.Read(ctx, admin, "jobs")
			require.NoError(t, err, "%v", err)
			want := 1
			if sig == syscall.SIGINT {
				want = 2
			}
			require.Equal(t, want, len(tab.Rows), "%v left %d rows, want %d: %s", sig, len(tab.Rows), want, fmt.Sprint(tab.Rows))
		})
	}
}

func TestShellDevNullIsNotATerminal(t *testing.T) {
	t.Parallel()
	in, err := os.Open(os.DevNull)
	require.NoError(t, err, "%v", err)
	defer in.Close()
	var out, errs bytes.Buffer
	code := (&application{in: in}).run([]string{"shell", "--redis", "127.0.0.1:1"}, &out, &errs)
	require.EqualValues(t, 0, code, "devnull: %d %s", code, &errs)
	require.EqualValues(t, 0, errs.Len(), "devnull: %d %s", code, &errs)
}

//go:build functional && !windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
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
				if code, out, errs := runTable(at(addr, args...)...); code != 0 {
					t.Fatalf("setup %d %s %s", code, out, errs)
				}
			}
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestShellSignalHelper$", "--", "--shell-signal-helper", addr)
			cmd.Dir = t.TempDir()
			for _, env := range os.Environ() {
				if !strings.HasPrefix(env, "NOVA_") {
					cmd.Env = append(cmd.Env, env)
				}
			}
			cmd.Stdin = strings.NewReader("watch jobs\nrow add jobs after-signal\nquit\n")
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			// A rendered frame proves watch installed its handlers and reached Redis.
			reader := bufio.NewReader(stdout)
			if _, err := reader.ReadString('\n'); err != nil {
				t.Fatalf("first frame: %v", err)
			}
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			if ctx.Err() != nil {
				t.Fatalf("session did not finish after %v: %v", sig, ctx.Err())
			}
			if sig == syscall.SIGTERM {
				status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
				if !ok || status.Signal() != sig {
					t.Fatalf("SIGTERM: error=%v status=%v stderr=%s", err, cmd.ProcessState, &stderr)
				}
			} else if err != nil {
				t.Fatalf("SIGINT: %v stderr=%s", err, &stderr)
			}
			tab, err := ntable.Read(ctx, admin, "jobs")
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if sig == syscall.SIGINT {
				want = 2
			}
			if len(tab.Rows) != want {
				t.Fatalf("%v left %d rows, want %d: %s", sig, len(tab.Rows), want, fmt.Sprint(tab.Rows))
			}
		})
	}
}

func TestShellDevNullIsNotATerminal(t *testing.T) {
	t.Parallel()
	in, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	var out, errs bytes.Buffer
	code := (&application{in: in}).run([]string{"shell", "--redis", "127.0.0.1:1"}, &out, &errs)
	if code != 0 || errs.Len() != 0 {
		t.Fatalf("devnull: %d %s", code, &errs)
	}
}

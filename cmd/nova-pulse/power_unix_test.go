//go:build unix

package main

// power-runner-timeout-kills-the-group: the ssh child is a process group leader, and
// cancelling the context (as a timeout does) kills the whole group, not just the process we
// started. The fake ssh spawns a child that ignores TERM; without the group kill the child
// outlives the verb. The wait reads NOVA_TEST_WAIT so no fixed bound lives on the CI path.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// powerTestWait is the poll bound the waits class test allows: the event, or thirty
// seconds, overridable by NOVA_TEST_WAIT.
func powerTestWait() time.Duration {
	wait := 30 * time.Second
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			wait = d
		}
	}
	return wait
}

// powerPollStep is how often a state the kernel owns -- a pid that is gone -- is looked at
// again. It is a poll INTERVAL, not a bound: every loop below ends on powerTestWait().
const powerPollStep = 20 * time.Millisecond // wall-ok: poll interval between reads, not an assertion

// readPidFromFIFO blocks in the kernel until the fake's child announces its pid, and answers
// on the returned channel.
//
// THE HANDSHAKE, not a guess at how long a fork takes. Opening a FIFO for reading blocks
// until a writer opens it, and that write is the child saying "I exist", so a loaded bench
// makes this slower and never makes it wrong. What was here before was 100 reads of a pid
// file 20 ms apart -- two seconds of wall clock spent waiting for a shell to fork on a
// machine with the whole fleet on it -- and it failed as `the fake ssh never wrote its child
// pid` on a busy Studio while the rest of the test was sound.
func readPidFromFIFO(t *testing.T, path string) <-chan int {
	t.Helper()
	out := make(chan int, 1)
	go func() {
		defer close(out)
		f, err := os.OpenFile(path, os.O_RDONLY, 0)
		if err != nil {
			return
		}
		defer f.Close()
		raw, err := io.ReadAll(f)
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil {
			return
		}
		out <- pid
	}()
	return out
}

func TestPowerRunnerTimeoutKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "child.pid.fifo")
	if err := syscall.Mkfifo(ready, 0o600); err != nil {
		t.Fatal(err)
	}
	ssh := filepath.Join(dir, "ssh")
	// The trap is installed BEFORE the pid is announced, so the pid the test reads is
	// already a process that ignores TERM. The group kill is SIGKILL, which no trap
	// catches, and that is what the assertion at the end is about.
	body := "#!/bin/sh\n" +
		"sh -c 'trap \"\" TERM INT; echo $$ > \"$NOVA_PG_READY\"; sleep 30' &\n" +
		"trap \"\" TERM INT\n" +
		"sleep 30\n"
	if err := os.WriteFile(ssh, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOVA_PG_READY", ready)

	// The reader opens first so the fake's write never waits on nobody; the open blocks
	// until the fake reaches its echo, whenever that is.
	pids := readPidFromFIFO(t, ready)

	r := powerSSHRunner{Program: ssh}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := r.Run(ctx, "ignored", "true")
		done <- err
	}()

	var pid int
	select {
	case p, ok := <-pids:
		if !ok || p == 0 {
			cancel()
			t.Fatal("the fake ssh never wrote its child pid")
		}
		pid = p
	case <-time.After(powerTestWait()):
		cancel()
		t.Fatal("the fake ssh never wrote its child pid")
	}

	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Run returned nil after the group kill")
		}
	case <-time.After(powerTestWait()):
		t.Fatal("Run did not return after the group kill")
	}

	// The kill is asynchronous in the kernel, so the only honest read is "gone by the
	// generous bound", never "gone in three seconds".
	deadline := time.Now().Add(powerTestWait())
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(powerPollStep)
	}
	t.Fatalf("child pid %d survived the timeout; the process group was not killed", pid)
}

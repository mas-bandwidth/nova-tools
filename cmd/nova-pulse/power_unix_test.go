//go:build unix

package main

// power-runner-timeout-kills-the-group: the ssh child is a process group leader, and
// cancelling the context (as a timeout does) kills the whole group, not just the process we
// started. The fake ssh spawns a child that ignores TERM; without the group kill the child
// outlives the verb. The wait reads NOVA_TEST_WAIT so no fixed bound lives on the CI path.

import (
	"context"
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

func TestPowerRunnerTimeoutKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	childPid := filepath.Join(dir, "child.pid")
	ssh := filepath.Join(dir, "ssh")
	body := "#!/bin/sh\n" +
		"sh -c 'echo $$ > \"$NOVA_PG_CHILD\"; trap \"\" TERM INT; sleep 30' &\n" +
		"trap \"\" TERM INT\n" +
		"sleep 30\n"
	if err := os.WriteFile(ssh, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOVA_PG_CHILD", childPid)

	r := powerSSHRunner{Program: ssh}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := r.Run(ctx, "ignored", "true")
		done <- err
	}()

	pid := 0
	for i := 0; i < 100 && pid == 0; i++ {
		if raw, err := os.ReadFile(childPid); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
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

	for i := 0; i < 150; i++ {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("child pid %d survived the timeout; the process group was not killed", pid)
}

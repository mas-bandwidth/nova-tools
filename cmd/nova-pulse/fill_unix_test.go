//go:build unix

package main

// sshCapacity used CommandContext to kill the ssh child, but bytes.Buffer and
// the tail writer make os/exec copy pipes. Descendants that inherit those
// descriptors keep Cmd.Wait blocked after the parent is gone when WaitDelay is
// zero (#2009 HOLD on 5914cd01). This is the command-edge regression: a 1s
// probe must return even while two 60s children retain stdout/stderr.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSSHCapacityReturnsWhenDescendantKeepsOutputOpen(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "desc.pids")
	ssh := filepath.Join(dir, "ssh")
	body := "#!/bin/sh\n" +
		"sleep 60 &\n" +
		"echo $! >> \"$NOVA_DESC_PIDS\"\n" +
		"sleep 60 &\n" +
		"echo $! >> \"$NOVA_DESC_PIDS\"\n" +
		"sleep 60\n"
	if err := os.WriteFile(ssh, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOVA_DESC_PIDS", pidFile)

	done := make(chan error, 1)
	go func() {
		_, err := sshCapacity{ssh: ssh, timeout: time.Second}.Capacity("bench-x")
		done <- err
	}()

	var pids []int
	deadline := time.Now().Add(powerTestWait())
	for time.Now().Before(deadline) {
		pids = readDescendantPids(pidFile)
		if len(pids) >= 2 {
			break
		}
		time.Sleep(powerPollStep) // wall-ok: poll interval between pid-file reads, not an assertion
	}
	if len(pids) < 2 {
		t.Fatal("the fake ssh never recorded two descendant pids")
	}
	t.Cleanup(func() {
		for _, pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("capacity returned nil while descendants held the pipes; want a timeout")
		}
	case <-time.After(powerTestWait()):
		t.Fatal("capacity did not return at its context deadline while a descendant retained stdout/stderr")
	}
}

func readDescendantPids(path string) []int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var pids []int
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil || pid <= 0 {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}

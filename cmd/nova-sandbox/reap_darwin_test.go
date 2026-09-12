//go:build darwin

// The reaper case of test 12, in its own file because it is POSIX: Setpgid and
// syscall.Kill do not exist on windows, and a test file with no build tag broke the
// windows job at COMPILE time -- a green suite that never ran is the failure "test on
// multiple platforms" exists to catch, and so is a red one about the wrong thing.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The wall's child stays in the CALLER's process group, and the caller owns pgid and
// reaping. A swarm supervisor puts each job in its own group and reaps that group when
// the job's deadline passes (SPEC-SWARM rule 11); if the tool put its child in a group of
// its own, a command that forked a background child left that child outside the group the
// supervisor kills — the reaper reported survivors=0 while a process was still running,
// which is the silent failure the rule exists to prevent.
//
// The test models exactly that: the tool is started in a group of the TEST's making, the
// wrapped command forks a background sleep and exits, and the test kills the group it
// made. Red with Setpgid on the tool's child; green without it.
func TestAForkedChildIsReapedWithTheCallersGroup(t *testing.T) {
	needDarwin(t)
	j := newJob(t)
	bin := toolBinary(t)
	pidFile := filepath.Join(j.write, "bg.pid")
	cmd := exec.Command(bin, "--read", j.read, "--write", j.write, "--",
		"/bin/sh", "-c", "sleep 300 & echo $! > '"+pidFile+"'; exit 0")
	cmd.Env = j.env()
	// The caller's own group, the way a supervisor starts a job.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Run(); err != nil {
		t.Fatalf("the wrapped command did not run: %v", err)
	}
	pgid := cmd.Process.Pid
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the wrapped command wrote no background pid: %v", err)
	}
	bg, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || bg <= 0 {
		t.Fatalf("background pid %q: %v", raw, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(bg, syscall.SIGKILL) })
	if err := syscall.Kill(bg, 0); err != nil {
		t.Fatalf("control: the background child was already gone before the reap: %v", err)
	}
	// The reap: the caller kills the group IT made, which is the only group it knows.
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	for i := 0; i < 50; i++ {
		if err := syscall.Kill(bg, 0); err != nil {
			return // reaped
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid %d survived the caller's group kill (pgid %d): the tool put its child in a group of its own, outside the one the caller reaps", bg, pgid)
}

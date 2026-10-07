package main

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestOwnGroupSetsProcessGroup(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err := cmd.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	if cmd.Process.Pid <= 0 {
		t.Fatal("process not started")
	}

	// Check the process group
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("could not get process group: %v", err)
	}

	if pgid != cmd.Process.Pid {
		t.Fatalf("process group %d != pid %d", pgid, cmd.Process.Pid)
	}
}

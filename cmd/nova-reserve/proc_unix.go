//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package main

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// watchdog starts nova-reserve again as "--watch <ppid> <pgid>", in a group of
// its own, to kill the command's group when this process dies without a word
// (SIGKILL). The returned func ends it once the command has been waited for.
func watchdog(pgid int) (func(), error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	w := exec.Command(self, "--watch", strconv.Itoa(os.Getpid()), strconv.Itoa(pgid))
	w.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := w.Start(); err != nil {
		return nil, err
	}
	return func() {
		// ignored: a watchdog that has already ended needs no kill
		_ = w.Process.Kill()
		// ignored: only reaping; its status says nothing
		_ = w.Wait()
	}, nil
}

// A watchdog is this binary re-run with --watch; no verb starts with a dash.
func init() {
	if len(os.Args) != 4 || os.Args[1] != "--watch" {
		return
	}
	ppid, e1 := strconv.Atoi(os.Args[2])
	pgid, e2 := strconv.Atoi(os.Args[3])
	if e1 != nil || e2 != nil || pgid <= 1 {
		os.Stderr.WriteString("nova-reserve: --watch is internal: <ppid> <pgid>\n")
		os.Exit(2)
	}
	g := guard{
		parentGone: func() bool { return os.Getppid() != ppid || !running(ppid) },
		groupGone:  func() bool { return syscall.Kill(-pgid, 0) == syscall.ESRCH },
		// ignored: a group that has already ended needs no kill
		killGroup: func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) },
		sleep:     time.Sleep,
	}
	g.watch()
	os.Exit(0)
}

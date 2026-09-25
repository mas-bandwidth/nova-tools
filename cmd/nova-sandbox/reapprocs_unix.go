//go:build darwin || linux

// The reap verb's three questions about the process table, answered on unix. Each is one
// bounded call to a tool macOS ships, or one signal — and each is behind a var in
// reapverb.go, so the verb's logic is tested with none of them.
package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// procQueryBound is how long lsof or ps may take. A reap that hangs on a wedged
// filesystem is worse than one that says it could not look: the caller is told and the
// volume is left alone rather than deleted out from under something.
const procQueryBound = 30 * time.Second

// processesUnder is every process holding the volume open. lsof is given the MOUNT POINT,
// which it resolves to the filesystem: that reports a process whose only claim is its
// working directory, which is exactly the survivor the soak measured — a `sleep 60`
// reparented to PID 1 with its cwd on the volume, holding it against every unmount.
//
// lsof exits 1 when it found nothing, which is an answer and not a failure. An exit with
// output that does not parse is the failure, and it is reported.
func processesUnder(mount string) ([]int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), procQueryBound)
	defer cancel()
	// -t is pids and nothing else; -w drops the warnings a mount in flux produces.
	cmd := exec.CommandContext(ctx, "/usr/sbin/lsof", "-t", "-w", "--", mount)
	if found, err := exec.LookPath("lsof"); err == nil {
		cmd = exec.CommandContext(ctx, found, "-t", "-w", "--", mount)
	}
	out, err := cmd.Output()
	if err != nil && ctx.Err() != nil {
		return nil, fmt.Errorf("lsof on %s did not answer inside %s", mount, procQueryBound)
	}
	var pids []int
	self := syscall.Getpid()
	for _, line := range strings.Split(string(out), "\n") {
		n, cerr := strconv.Atoi(strings.TrimSpace(line))
		if cerr != nil || n <= 1 || n == self {
			continue
		}
		pids = append(pids, n)
	}
	// Nothing found and a non-zero status is lsof's way of saying nothing found.
	if len(pids) == 0 && err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, nil
		}
		return nil, err
	}
	return pids, nil
}

// signalProcess sends one signal to one process.
func signalProcess(pid int, sig syscall.Signal) error {
	if pid <= 1 {
		return fmt.Errorf("%d is not a process this tool signals", pid)
	}
	return syscall.Kill(pid, sig)
}

// processAlive asks the kernel whether the pid is still there. Signal 0 delivers nothing
// and answers exactly that question; EPERM is someone else's process, which is still a
// running one.
func processAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// processStart is when the process under this pid started, as the machine spells it. The
// value is never parsed and never compared to a clock — it is compared, as a string, with
// the one the marker was written with, and any difference at all means the number has
// been handed to someone else.
func processStart(pid int) (string, error) {
	if pid <= 1 {
		return "", fmt.Errorf("%d is not a process this tool asks about", pid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), procQueryBound)
	defer cancel()
	bin := "/bin/ps"
	if found, err := exec.LookPath("ps"); err == nil {
		bin = found
	}
	out, err := exec.CommandContext(ctx, bin, "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", err
	}
	start := strings.TrimSpace(string(out))
	if start == "" {
		return "", fmt.Errorf("ps names no start time for pid %d", pid)
	}
	return start, nil
}

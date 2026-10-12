package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// adoptLane binds the durable PID record to the kernel process and the launch mark
// before a restarted member takes responsibility for it (SPEC-SWARM, member).
// A live legacy or mismatched record is refused, never replaced or signalled.
func adoptLane(path, mark string, pid int) (*os.Process, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	fields := strings.Fields(string(raw))
	proc, err := os.FindProcess(pid)
	if err == nil && len(fields) == 2 && fields[0] == strconv.Itoa(pid) && swarm.LaneIdentityMatches(pid, fields[1], mark) {
		return proc, fields[1], nil
	}
	if proc != nil {
		_ = proc.Release() // ignored: releases only this inspection handle
	}
	return nil, "", fmt.Errorf("live pid %d in %s has no matching lane identity; no process was adopted or signalled; inspect the record and its process, then remove the stale record only after the launch is accounted for", pid, path)
}

// adoptedAlive keeps observation and signalling on the same launch after a restart;
// an exited process or a PID reused for another birth is no longer this child.
func (c *nativeChild) adoptedAlive() bool {
	return c.proc != nil && swarm.LaneIdentityMatches(c.proc.Pid, c.started, c.mark)
}

// signal holds the restart identity check for both stop paths. An owned os.Process
// retains the operating system's process handle where one is available.
func (c *nativeChild) signal(sig os.Signal) error {
	if c.proc == nil || (c.adopted && !c.adoptedAlive()) {
		return os.ErrProcessDone
	}
	return c.proc.Signal(sig)
}

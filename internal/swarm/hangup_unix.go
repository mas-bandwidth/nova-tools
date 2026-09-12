//go:build unix

package swarm

import (
	"os/signal"
	"syscall"
)

// ignoreHangup is rule 18's "a supervisor's death cannot precede its acknowledgement",
// defended against the one signal the KERNEL sends without anybody asking.
//
// A supervisor is its runner's child, in a process group of its own. When the runner dies
// that group is orphaned, and POSIX says: if a newly orphaned process group holds a stopped
// process, the kernel sends the whole group SIGHUP and then SIGCONT (linux
// kill_orphaned_pgrp, darwin orphanpg). SIGHUP's default action ends the process where it
// stands -- before identify, before aborted.json, before exit.json -- and a job that was
// mid-transaction simply vanishes: an empty supervisor.log, no durable evidence of any
// kind, and a slot the next dispatcher can only quarantine.
//
// Measured 2026-09-12: on ubuntu 6.8 and on macOS 26.6, a supervisor stopped before its
// runner's death is killed by that hangup every time. It is what turned
// `TestTheLaunchIsATransaction/reverse-schedule` red on ubuntu CI while every Mac was
// green -- the two deaths race, and the loaded runner kept landing on the order that
// hangs up.
//
// So the supervisor ignores it. Nothing in SPEC-SWARM ends a job by hangup: a job ends at
// its deadline, at its budget, at the runner's group kill, or by `stop`. The SIGCONT that
// follows still arrives and still resumes it, which is exactly what is wanted -- the
// transaction finishes and writes the evidence it owes.
func ignoreHangup() { signal.Ignore(syscall.SIGHUP) }

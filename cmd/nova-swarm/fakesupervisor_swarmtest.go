//go:build swarmtest

package main

import (
	"os"
	"time"
)

// A SUPERVISOR THAT NEVER IDENTIFIES, for the one test that needs one
// (TestTheLaunchIsATransaction/fake-supervisor-never-identifies, #2984).
//
// That test used to stage it with NOVA_SWARM_PAUSEPOINT=before-identify, and a SIGSTOP a
// process sends ITSELF is not a stop on linux: kill(getpid(), SIGSTOP) is process-directed,
// the kernel hands it to the thread-group leader, and the thread that sent it runs on until
// the leader is scheduled and stops the group. A Go program's goroutine is not on the leader
// by then, so the supervisor could run straight through Identify and the dispatcher printed
// `RUN START` where the test wanted `RUN LAUNCH-FAILED` (space, #2141 at e0a99e65).
// Measured on hulk (linux 6.8) with a probe that stops itself from a non-leader thread and
// then writes a file: the write landed in 67 of 200 runs idle and 200 of 200 on a core
// shared with two spinners.
//
// So the case is not staged with a signal at all. This supervisor is the real binary's
// `supervise` verb, and in the swarmtest build, with NOVA_SWARM_FAKE_SUPERVISOR set to
// never-identifies, it does nothing: no slot write, no exit, no signal to race. It is alive
// and silent until the dispatcher's launch timeout kills its group, which is exactly the
// supervisor the launch timeout exists for. No scheduler decides the outcome; only the
// dispatcher's own clock decides WHEN it is reached.
//
// THE SILENCE IS BOUNDED, like every other injected wait (killpoint_unix.go): a fake whose
// dispatcher died before killing it must not outlive the test run as a leaked `supervise`
// (#1598). Two minutes is 120 launch timeouts of the one test that sets it, and it ends in
// an exit that writes nothing, so even a dispatcher starved for the whole of it reads
// "exited without writing an identity" -- another LAUNCH-FAILED, never a START.
//
// The release build never reads the variable (fakesupervisor_release.go), and
// TestTheReleaseBuildIgnoresTheInjectionVariables proves it.
const fakeSupervisorSilence = 2 * time.Minute

func injectedSupervisor() {
	if os.Getenv("NOVA_SWARM_FAKE_SUPERVISOR") != "never-identifies" {
		return
	}
	time.Sleep(fakeSupervisorSilence)
	os.Exit(3)
}

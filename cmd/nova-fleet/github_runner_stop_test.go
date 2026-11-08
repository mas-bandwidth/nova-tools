package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// github_runner_stop_test.go pins how a Linux runner's unit stops (roles/github-runner, cmd/nova-fleet/README.md
// "github-runners"). ExecStart is the release's run.sh, which starts run-helper.sh, which starts bin/Runner.Listener.
// Until 2026-11-08 the unit was KillMode=process: a stop sent SIGTERM to run.sh alone, which did not pass it on, and
// systemd left run-helper.sh and the listener running in the unit's cgroup. The restart's new listener then looped on
// "A session for this runner already exists" while the old one kept its session and took jobs with the old
// environment, so a unit change (a PATH) never reached a job (amd-1, amd-2, hetzner1, hulk: 9 units).
//
// The stop now has two halves. Graceful: RUNNER_MANUALLY_TRAP_SIG=1 is run.sh's own switch to run run-helper.sh in its
// own process group and turn the SIGTERM into SIGINT to that group, and run.sh then waits for the listener to shut
// down (Runner.Listener on SIGINT: "stop Runner.Listener and Runner.Worker"). Guaranteed: KillMode=mixed sends that
// SIGTERM to run.sh only, and SIGKILLs whatever is left in the cgroup once run.sh has exited, or at TimeoutStopSec.
// bin/runsvc.sh (svc.sh's ExecStart) is not used: it replaces PATH with .path, config.sh's snapshot of the shell that
// registered the runner, so the unit's PATH (github_runner_env, github_runner_path_test.go) would never reach a job.
// The render through ansible's templar is github_runner_stop_functional_test.go.

const runnerStopService = "roles/github-runner/templates/runner.service.j2"

func TestRunnerServiceStopEndsTheWholeRunner(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(runnerStopService)
	require.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, "\nExecStart={{ github_runner_dir }}/run.sh\n")
	assert.Contains(t, s, "\nEnvironment=RUNNER_MANUALLY_TRAP_SIG=1\n", "run.sh passes the stop on to the listener only with this")
	assert.Contains(t, s, "\nKillMode=mixed\n", "what is left in the cgroup after run.sh exits is killed")
	assert.NotContains(t, s, "KillMode=process", "a stop leaves the old listener running")
	assert.NotContains(t, s, "KillMode=none")
	assert.Contains(t, s, "\nKillSignal=SIGTERM\n")
	assert.Contains(t, s, "\nTimeoutStopSec=5min\n", "the bound on a graceful stop")
	assert.NotContains(t, s, "SendSIGKILL=no", "the guaranteed half is the SIGKILL")
}

// macOS has no such flaw: launchd makes run.sh a process group leader, run-helper.sh and the listener stay in that
// group, and when the job stops launchd kills what is left of the group. Two settings would break that: launchd's
// AbandonProcessGroup, and RUNNER_MANUALLY_TRAP_SIG, whose set -m moves run-helper.sh into a group of its own.
func TestRunnerDaemonStopKeepsTheProcessGroup(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("roles/github-runner/templates/runner.plist.j2")
	require.NoError(t, err)
	assert.NotContains(t, string(b), "AbandonProcessGroup")
	assert.NotContains(t, string(b), "RUNNER_MANUALLY_TRAP_SIG")
}

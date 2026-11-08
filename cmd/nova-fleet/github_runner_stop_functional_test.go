//go:build functional

package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// github_runner_stop_functional_test.go renders the Linux unit through ansible-core's templar (renderRunnerUnits in
// github_runner_path_functional_test.go) and reads how it stops; github_runner_stop_test.go says why.
func TestRunnerServiceRendersAWholeRunnerStop(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine; TestRunnerServiceStopEndsTheWholeRunner still runs")
	}
	for name, extra := range map[string][]string{
		"defaults": nil,
		"env":      {"-e", `{"github_runner_env": {"PATH": "/mine:/bin", "FOO": "bar", "GOFLAGS": "-mod=mod"}}`},
	} {
		service, _, _ := renderRunnerUnits(t, extra...)
		assert.Contains(t, service, "\nExecStart=/runners/o-r-1/run.sh\n", name)
		assert.Equal(t, 1, strings.Count(service, "Environment=RUNNER_MANUALLY_TRAP_SIG=1\n"), name)
		assert.Equal(t, 1, strings.Count(service, "\nKillMode="), name)
		assert.Contains(t, service, "\nKillMode=mixed\n", name)
		assert.Contains(t, service, "\nKillSignal=SIGTERM\n", name)
		assert.Contains(t, service, "\nTimeoutStopSec=5min\n", name)
		if extra != nil {
			// github_runner_env keeps every line it had: PATH once, then each other key
			assert.Contains(t, service, "\nEnvironment=PATH=/mine:/bin\n", name)
			assert.Contains(t, service, "\nEnvironment=FOO=bar\n", name)
			assert.Contains(t, service, "\nEnvironment=GOFLAGS=-mod=mod\n", name)
			assert.Equal(t, 1, strings.Count(service, "Environment=PATH="), name)
			assert.Equal(t, 4, strings.Count(service, "\nEnvironment="), name)
		}
	}
}

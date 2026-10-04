package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const goodUnit = `[Service]
Environment=PATH=/home/u/sdk/go/bin:/home/u/go/bin:/home/u/.local/bin:/usr/bin
KillMode=control-group
TimeoutStopSec=30s
`

// runnerBench is a conforming bench with one self-hosted runner directory, its
// unit file, ps, and a listener table the test sets.
type runnerBench struct {
	*bench
	dir string // the runner directory, with its trailing slash as the witness prints it
}

func withRunner(t *testing.T, idx string, unitBody string, ps string) *runnerBench {
	t.Helper()
	b := conformingBench(t)
	dir := b.mkdir("runner-nova-tools-" + idx)
	b.stub("ps")
	b.h.first(func(s runSpec) bool { return filepath.Base(s.name) == "ps" }, out(ps))
	if unitBody != "" {
		b.write(filepath.Join(".config", "systemd", "user", "nova-runner-"+idx+".service"), unitBody, false)
	}
	return &runnerBench{bench: b, dir: dir + "/"}
}

func (r *runnerBench) listener(pid string) string {
	return "  " + pid + " " + r.dir + "bin/Runner.Listener run --startuptype service\n"
}

func TestARunnerWithOneListenerUnderItsUnitConforms(t *testing.T) {
	t.Parallel()
	r := withRunner(t, "1", goodUnit, "")
	// The runner's own scripts carry the directory too; only the listener counts.
	r.h.first(func(s runSpec) bool { return filepath.Base(s.name) == "ps" },
		out("    1 /sbin/init\n  900 /bin/bash "+r.dir+"run.sh\n  901 /bin/bash "+r.dir+"bin/run-helper.sh\n"+r.listener("1234")))
	r.h.cgroups = map[string]string{"1234": "0::/user.slice/user-1000.slice/nova-runner-1.service\n"}
	code, output := r.standard()
	require.Equal(t, 0, code, output)
	ps := r.h.calls("ps")
	require.Len(t, ps, 1)
	assert.Equal(t, []string{"-eo", "pid=,args="}, ps[0].args, "ps was run as one snapshot of `-eo pid=,args=`")
}

func TestListenerCountMustBeExactlyOne(t *testing.T) {
	t.Parallel()
	for name, n := range map[string]string{"none": "0", "two": "2"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := withRunner(t, "3", goodUnit, "")
			table := ""
			if n == "2" {
				table = r.listener("1234") + r.listener("1235")
				r.h.cgroups = map[string]string{"1234": "nova-runner-3.service", "1235": "nova-runner-3.service"}
			}
			r.h.first(func(s runSpec) bool { return filepath.Base(s.name) == "ps" }, out(table))
			r.drift(t, "nova-runner-3.service listeners="+n+" want=1 in "+r.dir)
		})
	}
}

func TestAListenerNotUnderItsUnitIsAStray(t *testing.T) {
	t.Parallel()
	r := withRunner(t, "2", goodUnit, "")
	r.h.first(func(s runSpec) bool { return filepath.Base(s.name) == "ps" }, out(r.listener("4242")))
	r.drift(t, "nova-runner-2.service pid=4242 not under nova-runner-2.service")
	assert.Empty(t, r.h.killed, "a run without --apply killed")
}

func TestAListenerIsUnderItsUnitByCgroupOrByMainPID(t *testing.T) {
	t.Parallel()
	systemctl := func(user bool, pid string) func(s runSpec) bool {
		return func(s runSpec) bool {
			if filepath.Base(s.name) != "systemctl" {
				return false
			}
			isUser := len(s.args) > 0 && s.args[0] == "--user"
			return isUser == user
		}
	}
	cases := []struct {
		name  string
		setup func(r *runnerBench)
		stray bool
	}{
		{"the cgroup names the unit", func(r *runnerBench) {
			r.h.cgroups = map[string]string{"7": "1:name=systemd:/system.slice/nova-runner-1.service"}
		}, false},
		{"the user manager's MainPID", func(r *runnerBench) {
			r.stub("systemctl")
			r.h.first(systemctl(true, "7"), out("MainPID=7\n"))
			r.h.first(systemctl(false, "7"), out("MainPID=0\n"))
		}, false},
		{"the system manager's MainPID", func(r *runnerBench) {
			r.stub("systemctl")
			r.h.first(systemctl(true, "7"), out("MainPID=0\n"))
			r.h.first(systemctl(false, "7"), out("MainPID=7\n"))
		}, false},
		{"a MainPID that is another process", func(r *runnerBench) {
			r.stub("systemctl")
			r.h.first(systemctl(true, "7"), out("MainPID=8\n"))
			r.h.first(systemctl(false, "7"), out("MainPID=9\n"))
		}, true},
		{"a MainPID of zero is no process", func(r *runnerBench) {
			r.stub("systemctl")
			r.h.first(systemctl(true, "7"), out("MainPID=0\n"))
			r.h.first(systemctl(false, "7"), out("MainPID=0\n"))
		}, true},
		{"no cgroup and no systemctl", func(r *runnerBench) {}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := withRunner(t, "1", goodUnit, "")
			r.h.first(func(s runSpec) bool { return filepath.Base(s.name) == "ps" }, out(r.listener("7")))
			tc.setup(r)
			if !tc.stray {
				code, output := r.standard()
				require.Equal(t, 0, code, output)
				return
			}
			r.drift(t, "nova-runner-1.service pid=7 not under nova-runner-1.service")
		})
	}
}

// --apply kills the stray listeners and nothing else.
func TestApplyKillsTheStrayListenersAndNothingMore(t *testing.T) {
	t.Parallel()
	r := withRunner(t, "1", goodUnit, "")
	r.h.first(func(s runSpec) bool { return filepath.Base(s.name) == "ps" }, out(r.listener("4242")+"  500 /bin/sleep 100\n"))
	code, output := r.standard("--apply")
	assert.Equal(t, 1, code, "a witness that repairs still reports what it found")
	assert.Equal(t, []int{4242}, r.h.killed, "want exactly [4242]")
	assert.Contains(t, output, "NOTE stray runner listeners killed: 4242\n")
	assert.Contains(t, output, "DRIFT nova-runner-1.service pid=4242 not under nova-runner-1.service\n")
	// The NOTE comes after the runner findings and before the toolchain checks.
	i, j := strings.Index(output, "DRIFT nova-runner-1.service pid"), strings.Index(output, "NOTE stray")
	assert.LessOrEqual(t, i, j, "the NOTE came before the finding it reports")
}

func TestApplyOnACleanBenchKillsNothingAndSaysNothing(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	code, output := b.standard("--apply")
	require.Equal(t, 0, code, output)
	assert.NotContains(t, output, "NOTE")
	assert.Empty(t, b.h.killed)
	// And on an empty bench: drifts everywhere, still no kill.
	e := emptyBench(t)
	code, output = e.standard("--apply")
	require.Equal(t, 1, code, output)
	assert.NotContains(t, output, "NOTE")
	assert.Empty(t, e.h.killed)
}

func TestApplyNeverSignalsProcessOneOrAnythingNonNumeric(t *testing.T) {
	t.Parallel()
	r := withRunner(t, "1", goodUnit, "")
	r.h.first(func(s runSpec) bool { return filepath.Base(s.name) == "ps" },
		out(r.listener("1")+r.listener("0")+r.listener("12x")+r.listener("77")))
	r.standard("--apply")
	assert.Equal(t, []int{77}, r.h.killed, "want only [77]")
}

func TestOnlyStraysOfTheRunnerAreKilled(t *testing.T) {
	t.Parallel()
	// Two runners: one's listener is under its unit, the other's is a stray.
	r := withRunner(t, "1", goodUnit, "")
	r.mkdir("runner-nova-tools-2")
	r.write(".config/systemd/user/nova-runner-2.service", goodUnit, false)
	r.h.first(func(s runSpec) bool { return filepath.Base(s.name) == "ps" },
		out(r.listener("11")+"  22 "+filepath.Join(r.home, "runner-nova-tools-2")+"/bin/Runner.Listener run\n"))
	r.h.cgroups = map[string]string{"11": "nova-runner-1.service", "22": "nova-runner-1.service"}
	r.standard("--apply")
	// Pid 22 sits under runner 1's unit, not runner 2's, so it is runner 2's stray.
	assert.Equal(t, []int{22}, r.h.killed, "want [22]")
}

func TestPsNotAvailableIsDriftAndSkipsTheRunnersUnitFile(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	dir := b.mkdir("runner-nova-tools-5") + "/"
	output := b.drift(t, "nova-runner-5.service ps not available to count listeners in "+dir)
	assert.Len(t, drifts(output), 1, "the unit file is not read without a listener count")
}

func TestRunnerUnitFileStanzas(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		unit  string
		drift string
	}{
		"missing": {"", "nova-runner-1.service missing unit file nova-runner-1.service"},
		"no PATH": {"KillMode=control-group\nTimeoutStopSec=30s\n",
			"nova-runner-1.service unit file PATH lacks go/bin and .local/bin in"},
		"PATH without go/bin": {"Environment=PATH=/x/.local/bin\nKillMode=control-group\nTimeoutStopSec=30s\n",
			"nova-runner-1.service unit file PATH lacks go/bin and .local/bin in"},
		"PATH without .local/bin": {"Environment=PATH=/x/go/bin\nKillMode=control-group\nTimeoutStopSec=30s\n",
			"nova-runner-1.service unit file PATH lacks go/bin and .local/bin in"},
		"no KillMode": {"Environment=PATH=/x/go/bin:/x/.local/bin\nTimeoutStopSec=30s\n",
			"nova-runner-1.service unit file lacks KillMode=control-group in"},
		"no TimeoutStopSec": {"Environment=PATH=/x/go/bin:/x/.local/bin\nKillMode=control-group\n",
			"nova-runner-1.service unit file lacks TimeoutStopSec=30s in"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := withRunner(t, "1", tc.unit, "")
			r.h.first(func(s runSpec) bool { return filepath.Base(s.name) == "ps" }, out(r.listener("7")))
			r.h.cgroups = map[string]string{"7": "nova-runner-1.service"}
			assert.Len(t, drifts(r.drift(t, tc.drift)), 1)
		})
	}
}

func TestRunnerChecksAreLinuxOnly(t *testing.T) {
	t.Parallel()
	r := withRunner(t, "1", "", "")
	r.h.osName = "darwin"
	code, output := r.standard()
	require.Equal(t, 0, code, output)
	assert.Empty(t, r.h.calls("ps"), "ps ran on darwin")
}

func TestOnlyRunnerDirectoriesAreRunners(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	// A file with a runner's name is not a runner directory.
	require.NoError(t, os.WriteFile(filepath.Join(b.home, "runner-nova-tools-9"), []byte("x"), 0o644))
	b.mkdir("runner-other-1")
	code, output := b.standard()
	require.Equal(t, 0, code, output)
}

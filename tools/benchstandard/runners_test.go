package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, output)
	}
	ps := r.h.calls("ps")
	if len(ps) != 1 || !reflect.DeepEqual(ps[0].args, []string{"-eo", "pid=,args="}) {
		t.Errorf("ps was run as %v, want one snapshot of `-eo pid=,args=`", ps)
	}
}

func TestListenerCountMustBeExactlyOne(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ ps, n string }{
		"none": {"", "0"},
		"two":  {"", "2"},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := withRunner(t, "3", goodUnit, "")
			table := ""
			if tc.n == "2" {
				table = r.listener("1234") + r.listener("1235")
				r.h.cgroups = map[string]string{"1234": "nova-runner-3.service", "1235": "nova-runner-3.service"}
			}
			r.h.first(func(s runSpec) bool { return filepath.Base(s.name) == "ps" }, out(table))
			code, output := r.standard()
			wantOnlyDrift(t, code, output, "nova-runner-3.service listeners="+tc.n+" want=1 in "+r.dir)
		})
	}
}

func TestAListenerNotUnderItsUnitIsAStray(t *testing.T) {
	t.Parallel()
	r := withRunner(t, "2", goodUnit, "")
	r.h.first(func(s runSpec) bool { return filepath.Base(s.name) == "ps" }, out(r.listener("4242")))
	code, output := r.standard()
	wantOnlyDrift(t, code, output, "nova-runner-2.service pid=4242 not under nova-runner-2.service")
	if len(r.h.killed) != 0 {
		t.Errorf("a run without --apply killed %v", r.h.killed)
	}
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
		name   string
		setup  func(r *runnerBench)
		stray  bool
		detail string
	}{
		{"the cgroup names the unit", func(r *runnerBench) {
			r.h.cgroups = map[string]string{"7": "1:name=systemd:/system.slice/nova-runner-1.service"}
		}, false, ""},
		{"the user manager's MainPID", func(r *runnerBench) {
			r.stub("systemctl")
			r.h.first(systemctl(true, "7"), out("MainPID=7\n"))
			r.h.first(systemctl(false, "7"), out("MainPID=0\n"))
		}, false, ""},
		{"the system manager's MainPID", func(r *runnerBench) {
			r.stub("systemctl")
			r.h.first(systemctl(true, "7"), out("MainPID=0\n"))
			r.h.first(systemctl(false, "7"), out("MainPID=7\n"))
		}, false, ""},
		{"a MainPID that is another process", func(r *runnerBench) {
			r.stub("systemctl")
			r.h.first(systemctl(true, "7"), out("MainPID=8\n"))
			r.h.first(systemctl(false, "7"), out("MainPID=9\n"))
		}, true, ""},
		{"a MainPID of zero is no process", func(r *runnerBench) {
			r.stub("systemctl")
			r.h.first(systemctl(true, "7"), out("MainPID=0\n"))
			r.h.first(systemctl(false, "7"), out("MainPID=0\n"))
		}, true, ""},
		{"no cgroup and no systemctl", func(r *runnerBench) {}, true, ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := withRunner(t, "1", goodUnit, "")
			r.h.first(func(s runSpec) bool { return filepath.Base(s.name) == "ps" }, out(r.listener("7")))
			tc.setup(r)
			code, output := r.standard()
			if !tc.stray {
				if code != 0 {
					t.Errorf("exit %d:\n%s", code, output)
				}
				return
			}
			wantOnlyDrift(t, code, output, "nova-runner-1.service pid=7 not under nova-runner-1.service")
		})
	}
}

// --apply kills the stray listeners and nothing else.
func TestApplyKillsTheStrayListenersAndNothingMore(t *testing.T) {
	t.Parallel()
	r := withRunner(t, "1", goodUnit, "")
	r.h.first(func(s runSpec) bool { return filepath.Base(s.name) == "ps" }, out(r.listener("4242")+"  500 /bin/sleep 100\n"))
	code, output := r.standard("--apply")
	if code != 1 {
		t.Errorf("exit %d, want 1: a witness that repairs still reports what it found", code)
	}
	if !reflect.DeepEqual(r.h.killed, []int{4242}) {
		t.Errorf("killed %v, want exactly [4242]", r.h.killed)
	}
	if !strings.Contains(output, "NOTE stray runner listeners killed: 4242\n") {
		t.Errorf("no NOTE naming the kill:\n%s", output)
	}
	if !strings.Contains(output, "DRIFT nova-runner-1.service pid=4242 not under nova-runner-1.service\n") {
		t.Errorf("the stray is not reported as drift too:\n%s", output)
	}
	// The NOTE comes after the runner findings and before the toolchain checks.
	if i, j := strings.Index(output, "DRIFT nova-runner-1.service pid"), strings.Index(output, "NOTE stray"); i > j {
		t.Errorf("the NOTE came before the finding it reports:\n%s", output)
	}
}

func TestApplyOnACleanBenchKillsNothingAndSaysNothing(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	code, output := b.standard("--apply")
	if code != 0 || strings.Contains(output, "NOTE") {
		t.Errorf("exit %d:\n%s", code, output)
	}
	if len(b.h.killed) != 0 {
		t.Errorf("killed %v", b.h.killed)
	}
	// And on an empty bench: drifts everywhere, still no kill.
	e := emptyBench(t)
	code, output = e.standard("--apply")
	if code != 1 || strings.Contains(output, "NOTE") || len(e.h.killed) != 0 {
		t.Errorf("exit %d killed %v:\n%s", code, e.h.killed, output)
	}
}

func TestApplyNeverSignalsProcessOneOrAnythingNonNumeric(t *testing.T) {
	t.Parallel()
	r := withRunner(t, "1", goodUnit, "")
	r.h.first(func(s runSpec) bool { return filepath.Base(s.name) == "ps" },
		out(r.listener("1")+r.listener("0")+r.listener("12x")+r.listener("77")))
	r.standard("--apply")
	if !reflect.DeepEqual(r.h.killed, []int{77}) {
		t.Errorf("killed %v, want only [77]", r.h.killed)
	}
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
	if !reflect.DeepEqual(r.h.killed, []int{22}) {
		t.Errorf("killed %v, want [22]", r.h.killed)
	}
}

func TestPsNotAvailableIsDriftAndSkipsTheRunnersUnitFile(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	dir := b.mkdir("runner-nova-tools-5") + "/"
	code, output := b.standard()
	wantOnlyDrift(t, code, output, "nova-runner-5.service ps not available to count listeners in "+dir)
	if n := len(drifts(output)); n != 1 {
		t.Errorf("%d findings, want 1 (the unit file is not read without a listener count):\n%s", n, output)
	}
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
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := withRunner(t, "1", tc.unit, "")
			r.h.first(func(s runSpec) bool { return filepath.Base(s.name) == "ps" }, out(r.listener("7")))
			r.h.cgroups = map[string]string{"7": "nova-runner-1.service"}
			code, output := r.standard()
			wantOnlyDrift(t, code, output, tc.drift)
			if n := len(drifts(output)); n != 1 {
				t.Errorf("%d findings, want 1:\n%s", n, output)
			}
		})
	}
}

func TestRunnerChecksAreLinuxOnly(t *testing.T) {
	t.Parallel()
	r := withRunner(t, "1", "", "")
	r.h.osName = "darwin"
	if code, output := r.standard(); code != 0 {
		t.Errorf("exit %d:\n%s", code, output)
	}
	if n := len(r.h.calls("ps")); n != 0 {
		t.Errorf("ps ran on darwin")
	}
}

func TestOnlyRunnerDirectoriesAreRunners(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	// A file with a runner's name is not a runner directory.
	if err := os.WriteFile(filepath.Join(b.home, "runner-nova-tools-9"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	b.mkdir("runner-other-1")
	if code, output := b.standard(); code != 0 {
		t.Errorf("exit %d:\n%s", code, output)
	}
}

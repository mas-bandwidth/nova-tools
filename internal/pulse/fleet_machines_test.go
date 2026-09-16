package pulse

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeShell is the machine a test has instead of a machine: every command recorded, every
// answer canned, nothing run. It answers by the first substring that matches, so a test
// says what `go version` prints without knowing the script around it.
type fakeShell struct {
	answers []shellAnswer
	runs    []shellRun
}

type shellAnswer struct {
	match string
	out   string
	err   error
}

type shellRun struct {
	host    string
	command string
	stdin   string
}

func (f *fakeShell) Run(host, command, stdin string) (string, error) {
	f.runs = append(f.runs, shellRun{host: host, command: command, stdin: stdin})
	for _, a := range f.answers {
		if strings.Contains(command, a.match) {
			return a.out, a.err
		}
	}
	return "", nil
}

// commands is everything the verb said out loud to the machine, which is where a secret
// must never appear.
func (f *fakeShell) commands() string {
	var b strings.Builder
	for _, r := range f.runs {
		b.WriteString(r.host + " " + r.command + "\n")
	}
	return b.String()
}

// fakeTokens mints the same fake registration token every time and counts the mintings: a
// token is single-use, so one runner is one minting.
type fakeTokens struct {
	token string
	asked int
	err   error
}

func (f *fakeTokens) Registration(repo string) (string, error) {
	f.asked++
	return f.token, f.err
}

// TestFleetAddWritesTheRowsAndNeverLogsTheToken is the whole of `fleet add` in one test:
// the toolchain and the tarball asked for once, a token per runner, the configure fed that
// token ON STDIN, a service started per runner, and the two rows written -- the machine in
// fleet.tsv and each runner in rule E2's runner-services.tsv, so the reaper can restart
// what this verb stood up.
//
// The mutation that matters most: passing the token as an argument of the configure script
// instead of over stdin. It would still configure the runner and it would still print the
// same FLEET line, and the token would be in the ssh command line, in `ps` on the far side,
// and in any log of what this tool ran. So the test greps everything printed AND every
// command for the token, and demands it in the stdin.
func TestFleetAddWritesTheRowsAndNeverLogsTheToken(t *testing.T) {
	queue := t.TempDir()
	const token = "AXXXXXFAKEREGISTRATIONTOKEN123456"
	tokens := &fakeTokens{token: token}
	shell := &fakeShell{answers: []shellAnswer{
		{match: "go version", out: "go version go1.26.5 darwin/arm64"},
		{match: "actions-runner", out: "/Users/rowan/dl/actions-runner-osx-arm64-2.328.0.tar.gz"},
		{match: "config.sh", out: "configured"},
	}}
	var out, errs bytes.Buffer

	exit := FleetAdd(FleetAddInput{
		Name: "studio", Host: "studio.local", Repo: "mas-bandwidth/nova-tools", Queue: queue,
		Runners: 2, Labels: []string{"self-hosted", "macos", "nova"}, ServiceKind: "svc.sh",
		Cores: 24, RAMGB: 192, OS: "darwin-arm64", CardSlots: 6, Network: "tailscale",
		Shell: shell, Tokens: tokens, Stdout: &out, Stderr: &errs,
	})
	if exit != 0 {
		t.Fatalf("fleet add exit %d: %s%s", exit, out.String(), errs.String())
	}

	// One line, the counts on it.
	line := strings.TrimSpace(out.String())
	if n := len(strings.Split(line, "\n")); n != 1 {
		t.Errorf("fleet add printed %d lines, want one FLEET line:\n%s", n, line)
	}
	for _, want := range []string{"FLEET add", "name=studio", "runners=2", "configured=2", "started=2", "kind=svc", "labels=self-hosted,macos,nova"} {
		if !strings.Contains(line, want) {
			t.Errorf("FLEET line = %q, want %s", line, want)
		}
	}

	// The token: on stdin, nowhere else. Not in what was printed, not in a command.
	printed := out.String() + errs.String()
	if strings.Contains(printed, token) {
		t.Errorf("the registration token was printed:\n%s", printed)
	}
	if strings.Contains(shell.commands(), token) {
		t.Errorf("the registration token was put on a command line:\n%s", shell.commands())
	}
	fed := 0
	for _, r := range shell.runs {
		if strings.Contains(r.stdin, token) {
			fed++
			if !strings.Contains(r.command, "config.sh") {
				t.Errorf("the token was fed to a command that is not the configure: %q", headLine(r.command))
			}
		}
	}
	if fed != 2 {
		t.Errorf("the token reached stdin %d times, want once per runner (2)", fed)
	}
	if tokens.asked != 2 {
		t.Errorf("minted %d tokens, want one per runner (2): a registration token is single-use", tokens.asked)
	}

	// The toolchain where the workflow looks for it, and the tarball once for two runners.
	if !strings.Contains(shell.commands(), "$HOME/go/bin/go") {
		t.Errorf("no toolchain was ensured at ~/go/bin/go, which is where the workflow looks:\n%s", shell.commands())
	}
	if got := strings.Count(shell.commands(), "releases/download"); got != 1 {
		t.Errorf("the runner tarball was fetched %d times for 2 runners, want once per machine", got)
	}

	// The machine's row: eleven fields, this machine's facts.
	machines, err := ReadFleet(queue)
	if err != nil {
		t.Fatal(err)
	}
	studio, ok := machines["studio"]
	if !ok {
		t.Fatalf("no studio row in %s: %v", FleetFile, machines)
	}
	if studio.Host != "studio.local" || studio.ServiceKind != "svc" || studio.Cores != 24 || studio.CardSlots != 6 {
		t.Errorf("studio row = %+v, want the host, kind svc, 24 cores and 6 card slots", studio)
	}
	if strings.Join(studio.Labels, ",") != "self-hosted,macos,nova" {
		t.Errorf("studio labels = %v", studio.Labels)
	}

	// And each runner's row in rule E2's map, so the reaper can restart what add started.
	services, err := ReadRunnerServices(queue)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		name := fmt.Sprintf("studio-nova-%d", i)
		svc, ok := services[name]
		if !ok {
			t.Fatalf("%s has no row in %s: %v", name, RunnerServicesFile, services)
		}
		if svc.Kind != "svc" || svc.Target != fmt.Sprintf("~/runner-nova-tools-%d", i) || svc.Host != "studio.local" {
			t.Errorf("%s = %+v, want the svc.sh directory on studio.local", name, svc)
		}
	}
}

// TestFleetAddIsRunTwiceOnOneMachine: `add` half succeeded on 2026-09-16 and was run again.
// One row per machine and per runner, not two.
func TestFleetAddIsRunTwiceOnOneMachine(t *testing.T) {
	queue := t.TempDir()
	for i := 0; i < 2; i++ {
		var out, errs bytes.Buffer
		if exit := FleetAdd(FleetAddInput{
			Name: "space", Host: "space", Repo: "o/n", Queue: queue, Runners: 1,
			ServiceKind: "systemd", Shell: &fakeShell{}, Tokens: &fakeTokens{token: "AFAKETOKENFORSPACE00000"},
			Stdout: &out, Stderr: &errs,
		}); exit != 0 {
			t.Fatalf("pass %d: exit %d: %s%s", i, exit, out.String(), errs.String())
		}
	}
	machines, err := ReadFleet(queue)
	if err != nil {
		t.Fatal(err)
	}
	if len(machines) != 1 {
		t.Errorf("%s holds %d machines after adding one twice: %v", FleetFile, len(machines), machines)
	}
	services, err := ReadRunnerServices(queue)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Errorf("%s holds %d runners after adding one twice: %v", RunnerServicesFile, len(services), services)
	}
	if got := services["space-nova-1"]; got.Kind != "systemd" || !strings.HasPrefix(got.Target, "actions.runner.") {
		t.Errorf("space-nova-1 = %+v, want the systemd unit", got)
	}
}

// TestFleetAddRefusesRatherThanGuess: a machine half stood up is worse than one not
// started, so every missing thing is a refusal before anything leaves this process.
func TestFleetAddRefusesRatherThanGuess(t *testing.T) {
	base := FleetAddInput{Name: "studio", Host: "studio.local", Repo: "o/n", Queue: t.TempDir(), Runners: 2,
		Shell: &fakeShell{}, Tokens: &fakeTokens{token: "AFAKE0000000000000000000"}}
	cases := map[string]func(*FleetAddInput){
		"no name":           func(in *FleetAddInput) { in.Name = "" },
		"no host":           func(in *FleetAddInput) { in.Host = "" },
		"no repo":           func(in *FleetAddInput) { in.Repo = "" },
		"no queue":          func(in *FleetAddInput) { in.Queue = "" },
		"no runners":        func(in *FleetAddInput) { in.Runners = 0 },
		"a kind nobody has": func(in *FleetAddInput) { in.ServiceKind = "upstart" },
	}
	for name, break_ := range cases {
		in := base
		var out, errs bytes.Buffer
		in.Stdout, in.Stderr = &out, &errs
		break_(&in)
		if exit := FleetAdd(in); exit != 2 {
			t.Errorf("%s: exit %d, want 2 and a refusal; printed %q", name, exit, out.String())
		}
		if !strings.Contains(errs.String(), "FLEET REFUSED") {
			t.Errorf("%s: stderr = %q, want a FLEET REFUSED line", name, errs.String())
		}
	}
}

// TestFleetRestartChoosesTheMechanismPerServiceKind is the rest of 2026-09-16 by hand:
// `systemctl restart` on Space, `svc.sh stop; svc.sh start` on the Studio, and on a machine
// with neither, the supervised run.sh loop killed and started again. One row, one
// mechanism, and the mechanism is the machine's, never a guess.
func TestFleetRestartChoosesTheMechanismPerServiceKind(t *testing.T) {
	queue := t.TempDir()
	body := "space-nova-1\tspace\tsystemd\tactions.runner.mas-bandwidth-nova-tools.space-nova-1\n" +
		"studio-nova-2\t-\tsvc\t/Users/glenn/runner-nova-tools-2\n" +
		"mini-nova-1\tmini\trunsh\t/Users/rowan/runner-nova-tools-1\n"
	if err := os.WriteFile(filepath.Join(queue, RunnerServicesFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		runner, host, kind string
		wants              []string
		nots               []string
	}{
		{"space-nova-1", "space", "systemd",
			[]string{"systemctl restart", "actions.runner.mas-bandwidth-nova-tools.space-nova-1"},
			[]string{"svc.sh", "run.sh"}},
		{"studio-nova-2", "-", "svc",
			[]string{"/svc.sh stop", "/svc.sh start", "/Users/glenn/runner-nova-tools-2"},
			[]string{"systemctl", "pkill"}},
		{"mini-nova-1", "mini", "runsh",
			[]string{"pkill -f", "run.sh", "nova-runsh.pid", "while true"},
			[]string{"systemctl", "svc.sh"}},
	} {
		shell := &fakeShell{}
		var out, errs bytes.Buffer
		exit := FleetRestart(FleetRestartInput{Runner: tc.runner, Queue: queue, Shell: shell, Stdout: &out, Stderr: &errs})
		if exit != 0 {
			t.Fatalf("%s: exit %d: %s%s", tc.runner, exit, out.String(), errs.String())
		}
		if len(shell.runs) != 1 {
			t.Fatalf("%s: %d commands, want exactly one restart", tc.runner, len(shell.runs))
		}
		run := shell.runs[0]
		if run.host != tc.host {
			t.Errorf("%s: restarted on host %q, want %q", tc.runner, run.host, tc.host)
		}
		for _, want := range tc.wants {
			if !strings.Contains(run.command, want) {
				t.Errorf("%s: command = %q, want it to contain %q", tc.runner, run.command, want)
			}
		}
		for _, not := range tc.nots {
			if strings.Contains(run.command, not) {
				t.Errorf("%s: command = %q, must not contain %q (that is another machine's mechanism)", tc.runner, run.command, not)
			}
		}
		if got := strings.TrimSpace(out.String()); !strings.Contains(got, "kind="+tc.kind) || !strings.Contains(got, "ok=true") {
			t.Errorf("%s: FLEET line = %q, want kind=%s and ok=true", tc.runner, got, tc.kind)
		}
	}
}

// TestFleetRestartRefusesARunnerWithNoRow: restarting the wrong service is worse than the
// jam, so a runner nobody wrote down is a refusal naming the file to write it in.
func TestFleetRestartRefusesARunnerWithNoRow(t *testing.T) {
	shell := &fakeShell{}
	var out, errs bytes.Buffer
	exit := FleetRestart(FleetRestartInput{Runner: "space-nova-9", Queue: t.TempDir(), Shell: shell, Stdout: &out, Stderr: &errs})
	if exit != 2 || len(shell.runs) != 0 {
		t.Fatalf("exit %d after running %d commands, want 2 and nothing run", exit, len(shell.runs))
	}
	if !strings.Contains(errs.String(), RunnerServicesFile) {
		t.Errorf("stderr = %q, want a refusal naming %s", errs.String(), RunnerServicesFile)
	}
}

// TestFleetProbeReportsTheThreeFacts: the toolchain, the key files by NAME, and a
// card-shaped job under the cap a real card gets. The mutation that matters: reporting a
// key's contents, or its path, instead of its name -- so the test demands the name and
// refuses the directory it sits in.
func TestFleetProbeReportsTheThreeFacts(t *testing.T) {
	queue := t.TempDir()
	if err := upsertMachine(queue, Machine{Name: "mini", Host: "mini", ServiceKind: "runsh"}); err != nil {
		t.Fatal(err)
	}
	shell := &fakeShell{answers: []shellAnswer{
		{match: "go/bin/go\" version", out: "go version go1.26.5 darwin/arm64\n"},
		{match: "echo present", out: "present ~/.config/gh/hosts.yml\nmissing ~/.ssh/id_ed25519\n"},
		{match: "git clone", out: "ok\n"},
	}}
	ticks := []time.Time{
		time.Date(2026, 9, 16, 19, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 16, 19, 1, 33, 0, time.UTC),
	}
	tick := 0
	var out, errs bytes.Buffer
	exit := FleetProbe(FleetProbeInput{
		Name: "mini", Queue: queue, Keys: []string{"~/.config/gh/hosts.yml", "~/.ssh/id_ed25519"},
		Shell: shell, Stdout: &out, Stderr: &errs,
		Now: func() time.Time {
			at := ticks[min(tick, len(ticks)-1)]
			tick++
			return at
		},
	})
	line := strings.TrimSpace(out.String())
	if n := len(strings.Split(line, "\n")); n != 1 {
		t.Errorf("probe printed %d lines, want one PROBE line:\n%s", n, line)
	}
	for _, want := range []string{"PROBE name=mini", "go=go1.26.5", "keys=1/2", "missing=id_ed25519", "job=pass", "secs=93", "cap=360s"} {
		if !strings.Contains(line, want) {
			t.Errorf("PROBE line = %q, want %s", line, want)
		}
	}
	if strings.Contains(line, ".config/gh") {
		t.Errorf("PROBE line = %q, want key NAMES and no paths", line)
	}
	// A missing key is not a passing machine: adopt reads the exit code, not the prose.
	if exit == 0 {
		t.Errorf("exit 0 with a key missing; adopt would switch to a machine that cannot clone")
	}
	// The job ran under the cap a card gets, in a clone, and cleaned up after itself.
	job := ""
	for _, r := range shell.runs {
		if strings.Contains(r.command, "git clone") {
			job = r.command
		}
	}
	for _, want := range []string{"timeout 360", "go test ./internal/ci", "mktemp -d", "rm -rf"} {
		if !strings.Contains(job, want) {
			t.Errorf("the card-shaped job = %q, want it to contain %q", job, want)
		}
	}
}

// TestFleetProbeRefusesAMachineWithNoRow: a machine nobody added is a refusal naming the
// verb that adds it, not a probe of a guessed host.
func TestFleetProbeRefusesAMachineWithNoRow(t *testing.T) {
	shell := &fakeShell{}
	var out, errs bytes.Buffer
	if exit := FleetProbe(FleetProbeInput{Name: "ghost", Queue: t.TempDir(), Shell: shell, Stdout: &out, Stderr: &errs}); exit != 2 {
		t.Fatalf("exit %d, want 2", exit)
	}
	if len(shell.runs) != 0 {
		t.Errorf("probed a machine with no row: %v", shell.runs)
	}
	if !strings.Contains(errs.String(), FleetFile) {
		t.Errorf("stderr = %q, want a refusal naming %s", errs.String(), FleetFile)
	}
}

// TestFleetAddDryRunTouchesNothing: the plan is readable before it is trusted, and reading
// it starts no runner and mints no token.
func TestFleetAddDryRunTouchesNothing(t *testing.T) {
	queue := t.TempDir()
	shell, tokens := &fakeShell{}, &fakeTokens{token: "AFAKE0000000000000000000"}
	var out, errs bytes.Buffer
	if exit := FleetAdd(FleetAddInput{
		Name: "space", Host: "space", Repo: "o/n", Queue: queue, Runners: 8, DryRun: true,
		Shell: shell, Tokens: tokens, Stdout: &out, Stderr: &errs,
	}); exit != 0 {
		t.Fatalf("exit %d: %s%s", exit, out.String(), errs.String())
	}
	if len(shell.runs) != 0 || tokens.asked != 0 {
		t.Errorf("--dry-run ran %d commands and minted %d tokens", len(shell.runs), tokens.asked)
	}
	if _, err := os.Stat(filepath.Join(queue, FleetFile)); err == nil {
		t.Errorf("--dry-run wrote %s", FleetFile)
	}
	if got := strings.TrimSpace(out.String()); !strings.Contains(got, "runners=8") || !strings.Contains(got, "dry-run=true") {
		t.Errorf("FLEET line = %q, want runners=8 and dry-run=true", got)
	}
}

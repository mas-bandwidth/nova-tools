package main

// The fleet verb and its sub-verbs (SPEC-PULSE ## Fleet, issue #880 items 14, 16 and 17).
// The work is internal/pulse/fleet.go and internal/pulse/fleetadd.go; ssh comes from --ssh so a
// test puts a fake on PATH and no test reaches a machine.
//
// Issue #880 item 13: the benches live in one tab-separated file kept in git, and
// `fleet survey` runs tools/bench-standard.sh on every bench over ssh and folds the
// answers into one FLEET <name> line per bench. The coordinator surveyed the fleet by
// hand in 97 ssh turns; this is the machinery that retires that.
//
// The ssh child is `ssh <target> bash -s` with the standard script on its stdin, so a
// test fakes ssh on PATH and no test reaches the network. The benches run in parallel
// under --timeout, then print in file order so the one-line-per-bench reading is stable.
//
// `fleet add <bench>` admits a bench to the loop only on a fully green fleet-probe record
// (rule R): it reads the fleet-probe read-back, refuses unless every runner name is green
// (naming the runner that is not and the run id), and on green writes PULSE_ROOTS and the
// runner labels. No other verb writes those two.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// fleetBench is one line of the benches file: name, ssh target, home, and the optional
// mac that keeps the file's four-column shape (accepted and unused by survey).
type fleetBench struct {
	Name   string
	Target string
	Home   string
}

// fleetSurveyResult is one bench's fold: the lines to print and the exit it votes for
// (0 ok, 2 drift, 3 unreachable).
type fleetSurveyResult struct {
	lines  []string
	status int
}

// fleetSurveyRunner is the survey's one remote step: run the standard script on one bench
// and return its combined output. The real one is `ssh <target> bash -s`, bounded by the
// context. A test wires in a Go fake that answers from a table, so no unit test starts a
// program or waits on the host's scheduler.
type fleetSurveyRunner interface {
	Run(ctx context.Context, target, script string) (string, error)
}

// fleetSSHRunner is the real runner: `ssh <target> bash -s` with the script on stdin.
type fleetSSHRunner struct {
	Program string
}

func (r fleetSSHRunner) Run(ctx context.Context, target, script string) (string, error) {
	program := r.Program
	if program == "" {
		program = "ssh"
	}
	testguard.RefuseHosts(program, target, "bash -s")
	cmd := exec.CommandContext(ctx, program, target, "bash -s")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// The hooks a test replaces. Nothing here is a global side effect: a test wires its fake
// in and restores it. fleetNow is the survey's clock, so a test can assert the deadline
// each bench is given without waiting for one.
var (
	fleetNewSurveyRunner = func(program string) fleetSurveyRunner { return fleetSSHRunner{Program: program} }
	fleetNow             = func() time.Time { return time.Now() }
)

func cmdFleet(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, " fleet", "a sub-verb is required (registry, add, survey, suspend, wake, reboot, secrets, standard, mirror, join, sleep)")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "registry":
		return cmdFleetRegistry(rest, stdout, stderr)
	case "add":
		return cmdFleetAdd(rest, stdout, stderr)
	case "survey":
		return cmdFleetSurvey(rest, stdout, stderr)
	case "suspend":
		return cmdFleetSuspend(rest, stdout, stderr)
	case "wake":
		return cmdFleetWake(rest, stdout, stderr)
	case "reboot":
		return cmdFleetReboot(rest, stdout, stderr)
	case "secrets":
		return cmdFleetSecrets(rest, stdout, stderr)
	case "standard":
		return cmdFleetStandard(rest, stdout, stderr)
	case "mirror":
		return cmdFleetMirror(rest, stdout, stderr)
	case "join":
		return cmdFleetJoin(rest, stdout, stderr)
	case "sleep":
		return cmdFleetSleep(rest, stdout, stderr)
	}
	fmt.Fprintf(stderr, "nova-pulse fleet: unknown sub-verb %q (the sub-verbs are registry, add, survey, suspend, wake, reboot, secrets, standard, mirror, join, sleep; run: nova-pulse help)\n", sub)
	return 2
}

func cmdFleetAdd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		refuse(stderr, " fleet add", "a bench label is required; refusing to guess")
		return 2
	}
	bench := args[0]
	f := newFlags("fleet add")
	queue := f.fs.String("queue", "", "")
	roots := f.fs.String("roots", "", "")
	probe := f.fs.String("probe", "", "")
	if !f.parse(args[1:], stderr) {
		return 2
	}
	f.want(*queue, "queue", "the queue directory PULSE_ROOTS and the runner labels hang under")
	f.want(*roots, "roots", "the swarm roots PULSE_ROOTS will hold, comma separated")
	if f.refused(stderr) {
		return 2
	}
	return pulse.FleetAdd(pulse.FleetAddInput{
		Bench:  bench,
		Queue:  *queue,
		Roots:  *roots,
		Probe:  *probe,
		Stdout: stdout,
		Stderr: stderr,
	})
}

func cmdFleetSuspend(args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet suspend")
	benches := f.fs.String("benches", "", "")
	machines := f.fs.String("machines", "", "")
	bench := f.fs.String("bench", "", "")
	ssh := f.fs.String("ssh", "ssh", "")
	ifIdle := f.fs.Bool("if-idle", false, "")
	force := f.fs.Bool("force", false, "")
	timeout := f.fs.Int("timeout", 120, "")
	max := f.fs.Int("max", bounded.Default, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*benches, "benches", "the fleet file: name, ssh target, home, mac one per line")
	f.want(*bench, "bench", "the bench or benches to suspend, comma separated")
	if *timeout < 1 {
		f.add(fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
	}
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.FleetSuspend(pulse.FleetSuspendInput{
		Benches: *benches, Machines: *machines, Names: fleetNames(*bench), SSH: *ssh,
		Force: *force, IfIdle: *ifIdle,
		Timeout: time.Duration(*timeout) * time.Second, Max: *max,
		Stdout: stdout, Stderr: stderr,
	})
}

func cmdFleetWake(args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet wake")
	benches := f.fs.String("benches", "", "")
	machines := f.fs.String("machines", "", "")
	bench := f.fs.String("bench", "", "")
	ssh := f.fs.String("ssh", "ssh", "")
	wait := f.fs.String("wait", "3m", "")
	timeout := f.fs.Int("timeout", 120, "")
	max := f.fs.Int("max", bounded.Default, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*benches, "benches", "the fleet file: name, ssh target, home, mac one per line")
	f.want(*bench, "bench", "the bench or benches to wake, comma separated")
	whole, err := time.ParseDuration(*wait)
	if err != nil || whole <= 0 {
		f.add(fmt.Sprintf("--wait wants a positive duration such as 3m or 90s, got %q", *wait))
	}
	if *timeout < 1 {
		f.add(fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
	}
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.FleetWake(pulse.FleetWakeInput{
		Benches: *benches, Machines: *machines, Names: fleetNames(*bench), SSH: *ssh,
		Wait: whole, Timeout: time.Duration(*timeout) * time.Second, Max: *max,
		Now: func() time.Time { return time.Now().UTC() }, Sleep: time.Sleep,
		Stdout: stdout, Stderr: stderr,
	})
}

func cmdFleetReboot(args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet reboot")
	benches := f.fs.String("benches", "", "")
	machines := f.fs.String("machines", "", "")
	bench := f.fs.String("bench", "", "")
	ssh := f.fs.String("ssh", "ssh", "")
	wait := f.fs.String("wait", "5m", "")
	timeout := f.fs.Int("timeout", 120, "")
	max := f.fs.Int("max", 20, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*benches, "benches", "the fleet file: name, ssh target, home, mac one per line")
	f.want(*bench, "bench", "the bench or benches to reboot, comma separated")
	whole, err := time.ParseDuration(*wait)
	if err != nil || whole <= 0 {
		f.add(fmt.Sprintf("--wait wants a positive duration such as 5m or 90s, got %q", *wait))
	}
	if *timeout < 1 {
		f.add(fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
	}
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.FleetReboot(pulse.FleetRebootInput{
		Benches: *benches, Machines: *machines, Names: fleetNames(*bench), SSH: *ssh,
		Wait: whole, Timeout: time.Duration(*timeout) * time.Second, Max: *max,
		Now: func() time.Time { return time.Now().UTC() }, Sleep: time.Sleep,
		Stdout: stdout, Stderr: stderr,
	})
}

func cmdFleetSecrets(args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet secrets")
	benches := f.fs.String("benches", "", "")
	ssh := f.fs.String("ssh", "ssh", "")
	timeout := f.fs.Int("timeout", 120, "")
	max := f.fs.Int("max", bounded.Default, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*benches, "benches", "the fleet file: name, ssh-target, home, mac, tab separated")
	if *timeout < 1 {
		f.add(fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
	}
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.FleetSecrets(pulse.FleetSecretsInput{
		Benches: *benches,
		SSH:     *ssh,
		Timeout: time.Duration(*timeout) * time.Second,
		Max:     *max,
		Stdout:  stdout,
		Stderr:  stderr,
	})
}

// fleetNames splits the comma-separated --bench list, dropping empties.
func fleetNames(s string) []string {
	out := []string{}
	for _, part := range strings.Split(s, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func cmdFleetSurvey(args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet survey")
	benches := f.fs.String("benches", "", "")
	machines := f.fs.String("machines", "", "")
	ssh := f.fs.String("ssh", "ssh", "")
	timeout := f.fs.Int("timeout", 120, "")
	max := f.fs.Int("max", bounded.Default, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*benches, "benches", "the fleet file: name, ssh target, home, tab separated")
	if *timeout < 1 {
		f.add(fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
	}
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
	}
	if f.refused(stderr) {
		return 2
	}

	list, err := readFleetBenches(*benches)
	if err != nil {
		fmt.Fprintf(stderr, "FLEET REFUSED: --benches %s: %s\n", oneline.Field(*benches), oneline.Err(err))
		return 2
	}
	script, err := readBenchStandard()
	if err != nil {
		fmt.Fprintf(stderr, "FLEET REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	// The lock (Glenn 2026-09-18): a survey is an ssh and a script on the machine, which
	// is load, so the benches file's names are held against the machines registry before a
	// single child starts. A refused machine prints its line and is not surveyed; the rest
	// of the fleet is, so one wrong line in the benches file does not hide the fleet.
	list, refusals, code := surveyBenches(list, *machines, stdout, stderr)
	if code == 2 && len(list) == 0 {
		return 2
	}

	bound := time.Duration(*timeout) * time.Second
	runner := fleetNewSurveyRunner(*ssh)
	results := make([]fleetSurveyResult, len(list))
	var wg sync.WaitGroup
	for i, bench := range list {
		wg.Add(1)
		go func(i int, bench fleetBench) {
			defer wg.Done()
			results[i] = surveyOneBench(runner, script, bench, bound)
		}(i, bench)
	}
	wg.Wait()

	shown := refusals
	for _, r := range results {
		switch r.status {
		case 3:
			code = 3
		case 2:
			if code != 3 {
				code = 2
			}
		}
		for _, line := range r.lines {
			if *max > 0 && shown >= *max {
				return code
			}
			fmt.Fprintln(stdout, line)
			shown++
		}
	}
	return code
}

// surveyOneBench runs the standard on one bench over ssh and folds the script's DRIFT
// lines and its last line into FLEET lines. An ssh failure that is not a drift script's
// own exit 1 is an unreachable bench.
func surveyOneBench(runner fleetSurveyRunner, script string, bench fleetBench, timeout time.Duration) fleetSurveyResult {
	ctx, cancel := context.WithDeadline(context.Background(), fleetNow().Add(timeout))
	defer cancel()
	text, err := runner.Run(ctx, bench.Target, script)

	drifting := isDriftOutput(text)
	answered := strings.Contains(text, "STANDARD OK")
	if !drifting && !answered {
		reason := oneline.Err(err)
		if msg := firstNonemptyLine(text); msg != "" {
			reason = oneline.Escape(msg)
		}
		return fleetSurveyResult{lines: []string{"FLEET " + bench.Name + " UNREACHABLE " + reason}, status: 3}
	}
	status := 0
	if drifting {
		status = 2
	}
	return fleetSurveyResult{lines: foldBenchStandard(bench.Name, text), status: status}
}

// foldBenchStandard prints every DRIFT line of the script and its last line, each
// prefixed FLEET <name>.
func foldBenchStandard(name, out string) []string {
	var drifts []string
	last := ""
	for _, raw := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		last = line
		if line == "DRIFT" || strings.HasPrefix(line, "DRIFT ") {
			drifts = append(drifts, "FLEET "+name+" "+line)
		}
	}
	if last != "" {
		drifts = append(drifts, "FLEET "+name+" "+last)
	}
	return drifts
}

func isDriftOutput(out string) bool {
	if strings.Contains(out, "STANDARD DRIFT") {
		return true
	}
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimRight(raw, "\r")
		if line == "DRIFT" || strings.HasPrefix(line, "DRIFT ") {
			return true
		}
	}
	return false
}

func firstNonemptyLine(out string) string {
	for _, raw := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if line := strings.TrimSpace(raw); line != "" {
			return line
		}
	}
	return ""
}

// readFleetBenches reads the tab-separated fleet file. The card's three columns are name,
// ssh target and home; the spec's fourth (mac) is accepted and ignored here.
func readFleetBenches(path string) ([]fleetBench, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var list []fleetBench
	for n, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			return nil, fmt.Errorf("line %d wants at least 3 tab-separated fields name, ssh target, home, got %d", n+1, len(fields))
		}
		bench := fleetBench{Name: strings.TrimSpace(fields[0]), Target: strings.TrimSpace(fields[1]), Home: strings.TrimSpace(fields[2])}
		if bench.Name == "" || bench.Target == "" {
			return nil, fmt.Errorf("line %d wants a name and an ssh target", n+1)
		}
		list = append(list, bench)
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("no benches; refusing to guess")
	}
	return list, nil
}

// readBenchStandard finds tools/bench-standard.sh above the working directory, so the
// verb runs from anywhere inside the clone.
func readBenchStandard() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return readBenchStandardFrom(dir)
}

// readBenchStandardFrom is the walk itself, taking its starting directory, so a test
// can drive it without moving the process.
//
// IT SAYS WHERE IT LOOKED. The fourth release dogfood (2026-09-18) ran `fleet survey`
// from a home directory and read `tools/bench-standard.sh not found above the working
// directory` as "the script is missing", when what it means is that this verb wants a
// nova-tools CHECKOUT as its working directory. A refusal naming the first directory it
// tried and the last is one a person can act on without reading the source.
func readBenchStandardFrom(dir string) (string, error) {
	started, last := dir, dir
	for i := 0; i < 16; i++ {
		candidate := filepath.Join(dir, "tools", "bench-standard.sh")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			raw, err := os.ReadFile(candidate)
			if err != nil {
				return "", err
			}
			return string(raw), nil
		}
		last = dir
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("tools/bench-standard.sh not found: looked in every directory from %s up to %s; "+
		"fleet survey reads the script out of a nova-tools checkout, so run it with a checkout as the working directory", started, last)
}

// surveyBenches holds every bench in the benches file against the machines registry. It
// answers the benches that may be surveyed, how many refusal lines it printed, and the exit
// so far. An unnamed registry is the documented narrowing: no guard, every bench surveyed,
// exactly as the verb behaved before the registry existed.
func surveyBenches(list []fleetBench, machines string, stdout, stderr io.Writer) ([]fleetBench, int, int) {
	if strings.TrimSpace(machines) == "" {
		return list, 0, 0
	}
	reg, err := fleet.ReadRegistry(machines)
	if err != nil {
		fmt.Fprintf(stderr, "FLEET REFUSED: %s\n", oneline.Err(err))
		return nil, 0, 2
	}
	kept := make([]fleetBench, 0, len(list))
	printed, code := 0, 0
	for _, b := range list {
		var refusal *fleet.Refusal
		if err := reg.RequireBench(b.Name); errors.As(err, &refusal) {
			fmt.Fprintln(stdout, refusal.Line("FLEET"))
			printed++
			code = 2
			continue
		}
		kept = append(kept, b)
	}
	return kept, printed, code
}

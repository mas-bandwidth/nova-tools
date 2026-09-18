package main

// The fleet verb's four sub-verbs and their flags. The work is internal/pulse/fleet.go and
// internal/pulse/phantoms.go; the real Shell is one bounded ssh, the real Tokens is one
// bounded `gh api`, and the real tables are bounded `gh api` calls -- each wired here so
// every test of the work drives a fake and nothing in a test reaches a machine.

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdFleetAdd(name string, args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet add")
	host := f.fs.String("host", "", "")
	repo := f.fs.String("repo", "mas-bandwidth/nova-tools", "")
	queue := f.fs.String("queue", "", "")
	runners := f.fs.Int("runners", 0, "")
	labels := f.fs.String("labels", "", "")
	goVersion := f.fs.String("go", pulse.DefaultGoVersion, "")
	runnerVersion := f.fs.String("runner-version", pulse.DefaultRunnerVersion, "")
	user := f.fs.String("user", "", "")
	cores := f.fs.Int("cores", 0, "")
	ram := f.fs.Int("ram-gb", 0, "")
	osName := f.fs.String("os", "", "")
	dirPattern := f.fs.String("runner-dir", pulse.DefaultRunnerDirPattern, "")
	kind := f.fs.String("service-kind", "", "")
	slots := f.fs.Int("card-slots", 0, "")
	network := f.fs.String("network", "", "")
	dryRun := f.fs.Bool("dry-run", false, "")
	timeout := f.fs.Int("timeout", 600, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(name, "<name>", "the machine's name in the fleet, given as the first argument")
	f.want(*host, "host", "the ssh destination, or - for this machine")
	f.want(*queue, "queue", "the queue directory holding fleet.tsv and runner-services.tsv")
	if *runners < 1 {
		f.add(fmt.Sprintf("--runners is required and is at least 1, got %d; it is how many runners this machine takes", *runners))
	}
	if *timeout < 1 {
		f.add(fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
	}
	if f.refused(stderr) {
		return 2
	}
	bound := time.Duration(*timeout) * time.Second
	return pulse.FleetAdd(pulse.FleetAddInput{
		Name: name, Host: *host, Repo: *repo, Queue: *queue, Runners: *runners,
		Labels: splitList(*labels), Go: *goVersion, RunnerVersion: *runnerVersion,
		User: *user, Cores: *cores, RAMGB: *ram, OS: *osName, RunnerDir: *dirPattern,
		ServiceKind: *kind, CardSlots: *slots, Network: *network, DryRun: *dryRun,
		Shell: pulse.SSHShell{Timeout: bound}, Tokens: pulse.GHTokens{Timeout: bound},
		Now:    func() time.Time { return time.Now().UTC() },
		Stdout: stdout, Stderr: stderr,
	})
}

func cmdFleetRestart(runner string, args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet restart")
	queue := f.fs.String("queue", "", "")
	dryRun := f.fs.Bool("dry-run", false, "")
	timeout := f.fs.Int("timeout", 120, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(runner, "<runner>", "the runner's name, given as the first argument, as runner-services.tsv spells it")
	f.want(*queue, "queue", "the queue directory holding runner-services.tsv")
	if *timeout < 1 {
		f.add(fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.FleetRestart(pulse.FleetRestartInput{
		Runner: runner, Queue: *queue, DryRun: *dryRun,
		Shell:  pulse.SSHShell{Timeout: time.Duration(*timeout) * time.Second},
		Stdout: stdout, Stderr: stderr,
	})
}

func cmdFleetProbe(name string, args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet probe")
	queue := f.fs.String("queue", "", "")
	repo := f.fs.String("repo", "mas-bandwidth/nova-tools", "")
	job := f.fs.String("job", "go test ./internal/ci", "")
	keys := f.fs.String("keys", "", "")
	capSeconds := f.fs.Int("cap", int(pulse.DefaultProbeCap/time.Second), "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(name, "<name>", "the machine's name, given as the first argument, as fleet.tsv spells it")
	f.want(*queue, "queue", "the queue directory holding fleet.tsv")
	if *capSeconds < 1 {
		f.add(fmt.Sprintf("--cap wants a whole number of seconds, got %d; it is the cap a real card gets", *capSeconds))
	}
	if f.refused(stderr) {
		return 2
	}
	// The probe's own bound is the job's cap with room for the clone around it.
	bound := time.Duration(*capSeconds)*time.Second + 2*time.Minute
	return pulse.FleetProbe(pulse.FleetProbeInput{
		Name: name, Queue: *queue, Repo: *repo, Job: *job, Keys: splitList(*keys),
		Cap:    time.Duration(*capSeconds) * time.Second,
		Shell:  pulse.SSHShell{Timeout: bound},
		Now:    func() time.Time { return time.Now().UTC() },
		Stdout: stdout, Stderr: stderr,
	})
}

func cmdFleetPhantoms(args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet phantoms")
	repo := f.fs.String("repo", "", "")
	force := f.fs.Bool("force-cancel", false, "")
	scan := f.fs.Int("scan", pulse.DefaultPhantomScan, "")
	dryRun := f.fs.Bool("dry-run", false, "")
	timeout := f.fs.Int("timeout", 120, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*repo, "repo", "the repository whose runners and runs to read, owner/name")
	if *scan < 1 {
		f.add(fmt.Sprintf("--scan is at least 1, got %d; it is how many recent runs --force-cancel looks through", *scan))
	}
	if *timeout < 1 {
		f.add(fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
	}
	if f.refused(stderr) {
		return 2
	}
	bound := time.Duration(*timeout) * time.Second
	return pulse.Phantoms(pulse.PhantomsInput{
		Repo: *repo, Runners: pulse.GHRunners{Timeout: bound}, Runs: pulse.GHRuns{Timeout: bound},
		ForceCancel: *force, Scan: *scan, DryRun: *dryRun, Stdout: stdout, Stderr: stderr,
	})
}

// splitList takes a comma-separated flag to its items, empties dropped.
func splitList(s string) []string {
	out := []string{}
	for _, part := range strings.Split(s, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

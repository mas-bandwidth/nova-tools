package main

// The four fleet sub-verbs that retire the last four bench scripts (#1142, "everything
// sketched becomes a tool"):
//
//	scripts/bench-standard.sh -> nova-pulse fleet standard
//	scripts/bench-mirror.sh   -> nova-pulse fleet mirror
//	scripts/ts-join-one.sh    -> nova-pulse fleet join
//	scripts/fleet-sleep.sh    -> nova-pulse fleet sleep
//
// Flag parsing only: the work is internal/pulse/fleetstandard.go, fleetmirror.go,
// fleetjoin.go and fleetsleep.go. Every path is a flag with no default -- a bench's home,
// its mirror, its tailscale -- because the scripts' hard-coded paths are what made them
// one-bench tools, and the auth key is never a flag at all: `fleet join` reads it from the
// environment variable --authkey-env names, which nova-secrets exec fills.

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// splitItems reads `--items a,b,c` as the list it looks like. An empty list is every item
// the machine has a remedy for, which is what `--apply` alone means.
func splitItems(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func cmdFleetStandard(args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet standard")
	benches := f.fs.String("benches", "", "")
	machines := f.fs.String("machines", "", "")
	bench := f.fs.String("bench", "", "")
	ssh := f.fs.String("ssh", "ssh", "")
	want := f.fs.String("want", "", "")
	goWant := f.fs.String("go", "", "")
	osName := f.fs.String("os", "", "")
	minFree := f.fs.Int("min-free", 25, "")
	timeout := f.fs.Int("timeout", 120, "")
	max := f.fs.Int("max", 0, "")
	// --apply is the other half of the standard: the checks, as REMEDIES. It is the one
	// mutating fleet verb, so it names its machine through the machines REGISTRY -- the file
	// that decides where cards go -- and never through the benches file, it never takes
	// --all, and every change it makes is one line.
	apply := f.fs.Bool("apply", false, "")
	machine := f.fs.String("machine", "", "")
	items := f.fs.String("items", "", "")
	home := f.fs.String("home", "", "")
	gitName := f.fs.String("git-name", "", "")
	gitEmail := f.fs.String("git-email", "", "")
	dryRun := f.fs.Bool("dry-run", false, "")
	if !f.parse(args, stderr) {
		return 2
	}
	if *apply {
		f.want(*machines, "machines", "the machines registry: name, ssh, os/arch, roles, seat, cores, notes")
		f.want(*machine, "machine", "the one machine to repair; --apply CHANGES a machine, so it is never the whole fleet")
		if strings.TrimSpace(*bench) != "" || strings.TrimSpace(*benches) != "" {
			f.add("--apply names its machine with --machine against --machines; --bench and --benches belong to the reading half of this verb")
		}
		if *timeout < 1 {
			f.add(fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
		}
		if f.refused(stderr) {
			return 2
		}
		return pulse.FleetStandardApply(pulse.ApplyInput{
			Machines: *machines, Name: *machine, Items: splitItems(*items), SSH: *ssh,
			Home: *home, GitName: *gitName, GitEmail: *gitEmail, DryRun: *dryRun,
			Timeout: time.Duration(*timeout) * time.Second,
			Stdout:  stdout, Stderr: stderr,
		}).Code
	}
	for _, only := range []struct{ name, value string }{
		{"items", *items}, {"home", *home}, {"git-name", *gitName}, {"git-email", *gitEmail},
	} {
		if strings.TrimSpace(only.value) != "" {
			f.add(fmt.Sprintf("--%s belongs to --apply; without --apply this verb only reads", only.name))
		}
	}
	if *dryRun {
		f.add("--dry-run belongs to --apply; without --apply this verb only reads, and there is nothing to withhold")
	}
	f.want(*benches, "benches", "the fleet file: name, ssh target, home, mac one per line")
	f.want(*bench, "bench", "the one bench to hold against the standard")
	switch *osName {
	case "", "linux", "darwin":
	default:
		f.add(fmt.Sprintf("--os is linux or darwin, got %q; leave it out and the bench is asked with uname -s", *osName))
	}
	if *minFree < 0 {
		f.add(fmt.Sprintf("--min-free wants whole gigabytes, got %d", *minFree))
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
	return pulse.FleetStandard(pulse.FleetStandardInput{
		Benches: *benches, Machines: *machines, Name: *bench, SSH: *ssh, OS: *osName,
		Go: *goWant, Want: *want, MinFreeGB: *minFree,
		Timeout: time.Duration(*timeout) * time.Second, Max: *max,
		Stdout: stdout, Stderr: stderr,
	})
}

func cmdFleetMirror(args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet mirror")
	benches := f.fs.String("benches", "", "")
	machines := f.fs.String("machines", "", "")
	bench := f.fs.String("bench", "", "")
	ssh := f.fs.String("ssh", "ssh", "")
	repo := f.fs.String("repo", "", "")
	path := f.fs.String("path", "", "")
	timeout := f.fs.Int("timeout", 600, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*benches, "benches", "the fleet file: name, ssh target, home, mac one per line")
	f.want(*bench, "bench", "the one bench whose mirror this is")
	f.want(*repo, "repo", "the https remote to mirror, such as https://github.com/mas-bandwidth/nova-tools.git")
	f.want(*path, "path", "the absolute path of the bare mirror on the bench")
	if *timeout < 1 {
		f.add(fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.FleetMirror(pulse.FleetMirrorInput{
		Benches: *benches, Machines: *machines, Name: *bench, SSH: *ssh, Repo: *repo, Path: *path,
		Timeout: time.Duration(*timeout) * time.Second,
		Stdout:  stdout, Stderr: stderr,
	})
}

func cmdFleetJoin(args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet join")
	benches := f.fs.String("benches", "", "")
	machines := f.fs.String("machines", "", "")
	bench := f.fs.String("bench", "", "")
	ssh := f.fs.String("ssh", "ssh", "")
	tailscale := f.fs.String("tailscale", "", "")
	authkeyEnv := f.fs.String("authkey-env", "", "")
	timeout := f.fs.Int("timeout", 120, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*benches, "benches", "the fleet file: name, ssh target, home, mac one per line")
	f.want(*bench, "bench", "the one bench to join to the tailnet; it is also the tailnet hostname")
	f.want(*tailscale, "tailscale", "the absolute path of tailscale on the bench")
	f.want(*authkeyEnv, "authkey-env", "the NAME of the environment variable holding the auth key; the key itself is never a flag value")
	if *timeout < 1 {
		f.add(fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.FleetJoin(pulse.FleetJoinInput{
		Benches: *benches, Machines: *machines, Name: *bench, SSH: *ssh,
		Tailscale: *tailscale, AuthKeyEnv: *authkeyEnv,
		Timeout: time.Duration(*timeout) * time.Second,
		Stdout:  stdout, Stderr: stderr,
	})
}

func cmdFleetSleep(args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet sleep")
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
	f.want(*bench, "bench", "the one bench to put to sleep")
	if *timeout < 1 {
		f.add(fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
	}
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.FleetSleep(pulse.FleetSleepInput{
		Benches: *benches, Machines: *machines, Name: *bench, SSH: *ssh,
		Force: *force, IfIdle: *ifIdle,
		Timeout: time.Duration(*timeout) * time.Second, Max: *max,
		Stdout: stdout, Stderr: stderr,
	})
}

// cmdFleetRegistry lists the machines registry: the reading verb that answers "what is this
// machine, and may work go on it?" without touching a machine.
func cmdFleetRegistry(args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet registry")
	machines := f.fs.String("machines", "", "")
	role := f.fs.String("role", "", "")
	max := f.fs.Int("max", 0, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*machines, "machines", "the machines registry: name, ssh, os/arch, roles, seat, cores, notes, tab separated")
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.FleetRegistry(pulse.FleetRegistryInput{
		Machines: *machines, Role: *role, Max: *max,
		Stdout: stdout, Stderr: stderr,
	})
}

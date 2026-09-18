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
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

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
	if !f.parse(args, stderr) {
		return 2
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

// registryColumns is the machines file's shape, as every one of these verbs' refusals
// spells it. One string, so the reader and the three verbs cannot drift apart.
const registryColumns = "the machines registry: name, ssh, os/arch, roles, seat, cores, notes, provider, mac, tab separated"

// cmdFleetRegistry lists the machines registry -- the reading verb that answers "what is
// this machine, and may work go on it?" without touching a machine -- and dispatches the
// two verbs that write it.
func cmdFleetRegistry(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "add":
			return cmdFleetRegistryAdd(args[1:], stdout, stderr)
		case "set":
			return cmdFleetRegistrySet(args[1:], stdout, stderr)
		}
	}
	f := newFlags("fleet registry")
	machines := f.fs.String("machines", "", "")
	role := f.fs.String("role", "", "")
	max := f.fs.Int("max", 0, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*machines, "machines", registryColumns)
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

// cmdFleetRegistryAdd adds one machine. Every column is a flag with no default, because a
// guessed column in a control file is how a card reaches a CI runner host; the two columns
// that may honestly be empty are given as `-`.
func cmdFleetRegistryAdd(args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet registry add")
	machines := f.fs.String("machines", "", "")
	name := f.fs.String("name", "", "")
	ssh := f.fs.String("ssh", "", "")
	osArch := f.fs.String("os", "", "")
	roles := f.fs.String("roles", "", "")
	seat := f.fs.String("seat", "", "")
	cores := f.fs.Int("cores", 0, "")
	notes := f.fs.String("notes", "", "")
	provider := f.fs.String("provider", "self", "")
	mac := f.fs.String("mac", "-", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*machines, "machines", registryColumns)
	f.want(*name, "name", "the machine's name, which is what every verb's --bench takes")
	f.want(*ssh, "ssh", "the ssh target: an alias in ~/.ssh/config, or user@host")
	f.want(*osArch, "os", "the machine's os/arch, such as linux/x64 or darwin/arm64")
	f.want(*roles, "roles", "the roles set, comma separated, from bench, runner, coordination, services")
	f.want(*seat, "seat", "the machine's nova-secrets seat, or `-` when it carries none")
	f.want(*notes, "notes", "what a reader needs to know about the machine, or `-`; a bench+runner machine says `allow-shared=<YYYY-MM-DD> <why>` here")
	if *cores < 1 {
		f.add(fmt.Sprintf("--cores wants whole cores above zero, got %d; ask the machine with nproc or sysctl -n hw.ncpu", *cores))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.FleetRegistryAdd(pulse.FleetRegistryAddInput{
		Machines: *machines,
		Draft: fleet.Draft{
			Name: *name, SSH: *ssh, OSArch: *osArch, Roles: *roles, Seat: *seat,
			Cores: *cores, Notes: *notes, Provider: *provider, MAC: *mac,
		},
		Stdout: stdout, Stderr: stderr,
	})
}

// cmdFleetRegistrySet changes columns of one machine. A flag the command line did not give
// is not a change: the row keeps what the file already says, so `set --notes` writes a note
// and not a rebuilt machine.
func cmdFleetRegistrySet(args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet registry set")
	machines := f.fs.String("machines", "", "")
	name := f.fs.String("name", "", "")
	ssh := f.fs.String("ssh", "", "")
	osArch := f.fs.String("os", "", "")
	roles := f.fs.String("roles", "", "")
	seat := f.fs.String("seat", "", "")
	cores := f.fs.Int("cores", 0, "")
	notes := f.fs.String("notes", "", "")
	provider := f.fs.String("provider", "", "")
	mac := f.fs.String("mac", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*machines, "machines", registryColumns)
	f.want(*name, "name", "the machine to change; run nova-pulse fleet registry --machines <file> to list them")

	var change fleet.Change
	given := map[string]bool{}
	f.fs.Visit(func(fl *flag.Flag) { given[fl.Name] = true })
	if given["ssh"] {
		change.SSH = ssh
	}
	if given["os"] {
		change.OSArch = osArch
	}
	if given["roles"] {
		change.Roles = roles
	}
	if given["seat"] {
		change.Seat = seat
	}
	if given["cores"] {
		change.Cores = cores
	}
	if given["notes"] {
		change.Notes = notes
	}
	if given["provider"] {
		change.Provider = provider
	}
	if given["mac"] {
		change.MAC = mac
	}
	if !change.Any() {
		f.add("name a column to change: one of --ssh, --os, --roles, --seat, --cores, --notes, --provider, --mac")
	}
	if given["cores"] && *cores < 1 {
		f.add(fmt.Sprintf("--cores wants whole cores above zero, got %d; ask the machine with nproc or sysctl -n hw.ncpu", *cores))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.FleetRegistrySet(pulse.FleetRegistrySetInput{
		Machines: *machines, Name: *name, Change: change,
		Stdout: stdout, Stderr: stderr,
	})
}

package main

// `nova-pulse fleet certify` (Glenn, 2026-09-18: "certify fleet machines").
//
// Flag parsing and the two production seams; the work is internal/fleet/certify.go. The
// seams are the same two the rest of the fleet verbs use: ssh is `ssh <target> bash -s` with
// the script on stdin, named by --ssh so a test puts a fake on PATH, and the forge is the
// same `gh api .../actions/runners` the reaper reads, so no test here reaches either.
//
// Why the verb exists: `fleet survey` asks a machine what it HAS, and hulk passed every
// survey it was ever given on the morning its first Go card died inside the swarm wall --
// `$HOME/sdk/go1.26.5` was not a readable root, so the only go reachable was /usr/bin/go
// 1.22 and go.mod refused it by name. Certification makes the machine DO the work instead,
// under the wall, and writes down that it did.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// The hooks a test replaces, in the shape fleet.go already uses: nothing global, wired in
// and restored by the test that wants it.
var (
	fleetNewCertifyRemote = func(program string) fleet.Remote { return fleetSSHRunner{Program: program} }
	fleetNewCertifyForge  = func(timeout time.Duration) fleet.Forge { return ghRunnerList{Timeout: timeout} }
)

// ghRunnerList is the production forge: the reaper's own runner read, narrowed to the one
// question certification asks.
type ghRunnerList struct{ Timeout time.Duration }

func (g ghRunnerList) Runners(repo string) ([]fleet.RunnerStatus, error) {
	runners, err := pulse.GHRunners{Timeout: g.Timeout}.Runners(repo)
	if err != nil {
		return nil, err
	}
	out := make([]fleet.RunnerStatus, 0, len(runners))
	for _, r := range runners {
		out = append(out, fleet.RunnerStatus{Name: r.Name, Status: r.Status})
	}
	return out, nil
}

func cmdFleetCertify(args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet certify")
	machines := f.fs.String("machines", "", "")
	machine := f.fs.String("machine", "", "")
	all := f.fs.Bool("all", false, "")
	workloads := f.fs.String("workloads", "", "")
	certs := f.fs.String("certs", "", "")
	standard := f.fs.String("standard", "", "")
	build := f.fs.String("build", "", "")
	repo := f.fs.String("repo", "mas-bandwidth/nova-tools", "")
	bin := f.fs.String("bin", "", "")
	ssh := f.fs.String("ssh", "ssh", "")
	timeout := f.fs.String("timeout", "10m", "")
	maxAge := f.fs.String("max-age", "24h", "")
	ifStale := f.fs.Bool("if-stale", false, "")
	status := f.fs.Bool("status", false, "")
	logPath := f.fs.String("log", "", "")
	dryRun := f.fs.Bool("dry-run", false, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*machines, "machines", "the machines registry: name, ssh, os/arch, roles, seat, cores, notes, tab separated")
	if !*dryRun {
		f.want(*certs, "certs", "the certificates file appended to: machine, build, standard-hash, class, verdict, evidence, at")
	}
	if *machine == "" && !*all && !*status {
		f.add("neither --machine <name> nor --all; certification puts real load on a machine, so it is never the whole fleet by accident")
	}
	if *machine != "" && *all {
		f.add("--machine and --all together; pass one")
	}
	bound, err := time.ParseDuration(*timeout)
	if err != nil || bound <= 0 {
		f.add(fmt.Sprintf("--timeout wants a positive duration such as 10m or 90s, got %q", *timeout))
	}
	age, err := time.ParseDuration(*maxAge)
	if err != nil || age <= 0 {
		f.add(fmt.Sprintf("--max-age wants a positive duration such as 24h or 90m, got %q", *maxAge))
	}
	if *status && (*machine != "" || *all || *ifStale) {
		f.add("--status reads the record and reaches no machine; pass it alone")
	}
	if f.refused(stderr) {
		return 2
	}

	loads, err := certifyWorkloads(*workloads)
	if err != nil {
		fmt.Fprintf(stderr, "CERTIFY REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	standardPath, err := certifyStandardPath(*standard)
	if err != nil {
		fmt.Fprintf(stderr, "CERTIFY REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	hash, err := fleet.StandardHash(standardPath, loads)
	if err != nil {
		fmt.Fprintf(stderr, "CERTIFY REFUSED: %s\n", oneline.Err(err))
		return 2
	}

	if *status {
		return fleet.Status(fleet.StatusInput{
			Machines: *machines, Certs: *certs, Workloads: loads, Hash: hash, MaxAge: age,
			Now:    func() time.Time { return fleetNow().UTC() },
			Stdout: stdout, Stderr: stderr,
		})
	}

	// --log is the structured event stream beside the lines, appended to. The loop's timer
	// runs this with nobody watching, so the lines go to a file the dashboard reads.
	var events io.Writer
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintf(stderr, "CERTIFY REFUSED: cannot open --log %s: %s\n", oneline.Field(*logPath), oneline.Err(err))
			return 2
		}
		defer f.Close()
		events = f
	}

	return fleet.Certify(fleet.CertifyInput{
		Machines: *machines, Only: *machine, All: *all, Workloads: loads,
		Certs: *certs, Hash: hash, Build: *build, Bin: *bin, Repo: *repo,
		Timeout: bound, DryRun: *dryRun, IfStale: *ifStale, MaxAge: age, Log: events,
		Remote: fleetNewCertifyRemote(*ssh), Forge: fleetNewCertifyForge(bound),
		Now:    func() time.Time { return fleetNow().UTC() },
		Stdout: stdout, Stderr: stderr,
	})
}

// certifyWorkloads takes the override directory when one is named and the embedded standard
// set otherwise. The embedded set is what ships, so a machine certified from a laptop and a
// machine certified from the loop were held to the same work.
func certifyWorkloads(dir string) ([]fleet.Workload, error) {
	if dir == "" {
		return fleet.StandardWorkloads()
	}
	return fleet.ReadWorkloads(dir)
}

// certifyStandardPath answers the provisioning standard file the hash is taken over.
// --standard names it; left out, it is tools/bench-standard.sh above the working directory,
// the same file `fleet survey` runs.
func certifyStandardPath(named string) (string, error) {
	if named != "" {
		if _, err := os.Stat(named); err != nil {
			return "", fmt.Errorf("cannot read --standard %s: %w", named, err)
		}
		return named, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for i := 0; i < 16; i++ {
		candidate := filepath.Join(dir, "tools", "bench-standard.sh")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("tools/bench-standard.sh not found above the working directory; name it with --standard")
}

// certifyBuildReader is the fill's build seam in production: one ssh per bench per tick,
// asking the machine what nova-merge it is running. It is a seam because the fill must be
// drivable by a test with no machine at all.
type certifyBuildReader struct {
	SSH     string
	Timeout time.Duration
}

func (r certifyBuildReader) Build(machine string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.Timeout)
	defer cancel()
	out, err := fleetSSHRunner{Program: r.SSH}.Run(ctx, machine, fleet.BuildScript)
	if err != nil {
		return "", err
	}
	return fleet.BuildVersion(out), nil
}

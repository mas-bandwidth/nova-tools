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
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/friends"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
	"github.com/mas-bandwidth/nova-tools/tools"
)

// The hooks a test replaces, in the shape fleet.go already uses: nothing global, wired in
// and restored by the test that wants it.
var (
	fleetNewCertifyRemote = func(program string) fleet.Remote { return fleetSSHRunner{Program: program} }
	fleetNewCertifyForge  = func(timeout time.Duration) fleet.Forge { return ghRunnerList{Timeout: timeout} }
	fleetNewCertifyFixer  = func(f certifyFixer) fleet.Fixer { return f }
	fleetNewCertifyBus    = func(b certifyBusPoster) fleet.BusPoster { return b }
	// The local transport, behind a hook like every other one. It is a seam FOR SAFETY as
	// much as for testing: a test that ran the real workloads would run them on whichever CI
	// machine it landed on, and the machines in this fleet are named in the test registries.
	fleetNewCertifyLocal = func() fleet.Remote { return localRunner{} }
	// The machine this process is on. A test replaces it; nothing else reads the hostname,
	// so "is this me" is decided in one place.
	fleetLocalHost = func() string {
		name, err := os.Hostname()
		if err != nil {
			return ""
		}
		return name
	}
)

// localRunner runs a script HERE, with `bash -s` and no ssh, for the machine that is this
// machine. hulk certifying hulk went through `ssh hulk` on the first real run of this verb
// and died on its own host key -- a machine has no business proving itself over a transport
// it does not need, and the host key, the agent and BatchMode are all of them ways for that
// to fail for reasons that say nothing about the bench.
type localRunner struct{}

func (localRunner) Run(ctx context.Context, target, script string) (string, error) {
	cmd := exec.CommandContext(ctx, "bash", "-s")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

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
	attempts := f.fs.String("attempts", "", "")
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
	// The repair round. It is ON by default, because the whole point of mechanizing
	// certification is that the fleet does not wait for a person to type the same four
	// repairs on four machines again; `--no-fix` waives it out loud.
	fix := f.fs.Bool("fix", true, "")
	noFix := f.fs.Bool("no-fix", false, "")
	maxFixRounds := f.fs.Int("max-fix-rounds", fleet.DefaultFixRounds, "")
	gitName := f.fs.String("git-name", "", "")
	gitEmail := f.fs.String("git-email", "", "")
	// Where an escalation goes. With no --bus the escalation is still a line and still an
	// event; the note is what needs a bus, and a run without one says so rather than
	// dropping it.
	busDir := f.fs.String("bus", "", "")
	busAs := f.fs.String("as", "", "")
	busTo := f.fs.String("to", "", "")
	busRemote := f.fs.String("bus-remote", "origin", "")
	busBranch := f.fs.String("bus-branch", "main", "")
	lane := f.fs.String("lane", fleet.DefaultLane, "")
	if !f.parse(args, stderr) {
		return 2
	}
	// --status touches no machine and reads one file, so it wants one file. It used to want
	// the registry AND a provisioning standard above the working directory, which is how a
	// reading verb becomes something nobody can run from anywhere.
	if !*status {
		f.want(*machines, "machines", "the machines registry: name, ssh, os/arch, roles, seat, cores, notes, tab separated")
	}
	if *status {
		f.want(*certs, "certs", "the certificates file to read back")
	}
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
	if *maxFixRounds < 0 {
		f.add(fmt.Sprintf("--max-fix-rounds is 0 or more, got %d; 0 is the same waiver as --no-fix", *maxFixRounds))
	}
	if strings.TrimSpace(*busDir) != "" && strings.TrimSpace(*busAs) == "" {
		f.add("--bus without --as; a note needs a sender, and the bus refuses one with no lane of its own")
	}
	if strings.TrimSpace(*busDir) != "" && strings.TrimSpace(*busTo) == "" {
		f.add("--bus without --to; an escalation nobody is addressed by is an escalation nobody reads")
	}
	if f.refused(stderr) {
		return 2
	}

	loads, err := certifyWorkloads(*workloads)
	if err != nil {
		fmt.Fprintf(stderr, "CERTIFY REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	// The provisioning standard, and the hash over it. --standard names a file; without one
	// the clone's own tools/bench-standard.sh is used, and outside a clone the copy embedded
	// in this binary -- which is the SAME BYTES, so the hash is the same either way.
	//
	// The default exists because the first real fleet-wide run refused: the launchd job runs
	// from wherever launchd starts it, and `tools/bench-standard.sh not found above the
	// working directory` is not a thing a timer can fix at four in the morning.
	standardRaw, standardFrom, err := certifyStandard(*standard)
	if err != nil {
		fmt.Fprintf(stderr, "CERTIFY REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	hash, err := fleet.StandardHashFrom(standardRaw, loads)
	if err != nil {
		fmt.Fprintf(stderr, "CERTIFY REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	fmt.Fprintf(stderr, "CERTIFY NOTE standard=%s hash=%s\n", oneline.Field(standardFrom), oneline.Field(hash))

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

	// The repair is on unless it was waived, and it is wired even when it is off: a run that
	// turns the fix on must never fall back to a guessed path for the apply.
	wantFix := *fix && !*noFix
	fixer := fleetNewCertifyFixer(certifyFixer{
		Machines: *machines, SSH: *ssh, GitName: *gitName, GitEmail: *gitEmail,
		Timeout: bound, LocalHost: fleetLocalHost(), Stdout: stdout, Stderr: stderr,
	})
	var poster fleet.BusPoster
	if strings.TrimSpace(*busDir) != "" {
		poster = fleetNewCertifyBus(certifyBusPoster{
			Bus: *busDir, As: *busAs, To: *busTo, Remote: *busRemote, Branch: *busBranch, Timeout: bound,
		})
	}

	return fleet.Certify(fleet.CertifyInput{
		Machines: *machines, Only: *machine, All: *all, Workloads: loads,
		Certs: *certs, Attempts: *attempts, Hash: hash, Build: *build, Bin: *bin, Repo: *repo,
		Timeout: bound, DryRun: *dryRun, IfStale: *ifStale, MaxAge: age, Log: events,
		Fix: wantFix, MaxFixRounds: *maxFixRounds, Fixer: fixer, Bus: poster, Lane: *lane,
		Remote: fleetNewCertifyRemote(*ssh), Forge: fleetNewCertifyForge(bound),
		Local: fleetNewCertifyLocal(), LocalHost: fleetLocalHost(),
		Now:    func() time.Time { return fleetNow().UTC() },
		Stdout: stdout, Stderr: stderr,
	})
}

// certifyFixer is the apply seam in production: `fleet standard --apply` for one machine,
// run in-process rather than as a subprocess, so there is ONE apply and not a second copy of
// it behind a certify. Its STANDARD APPLY lines go to the same streams as the CERTIFY lines,
// because what a repair changed on a machine belongs in the same transcript as the failure
// that asked for it.
type certifyFixer struct {
	Machines  string
	SSH       string
	GitName   string
	GitEmail  string
	Timeout   time.Duration
	LocalHost string
	Stdout    io.Writer
	Stderr    io.Writer
}

func (f certifyFixer) Apply(machine string, items []string) ([]string, error) {
	out := pulse.FleetStandardApply(pulse.ApplyInput{
		Machines: f.Machines, Name: machine, Items: items, SSH: f.SSH,
		GitName: f.GitName, GitEmail: f.GitEmail, Timeout: f.Timeout,
		Local: fleetNewCertifyLocal(), LocalHost: f.LocalHost,
		Stdout: f.Stdout, Stderr: f.Stderr,
	})
	if out.Code != 0 {
		return out.Changed(), fmt.Errorf("the apply on %s exited %d; the STANDARD APPLY lines above name the item", machine, out.Code)
	}
	return out.Changed(), nil
}

// certifyBusPoster is the escalation's note in production: ONE `nova-bus send`, through the
// bus's own send path -- its locking, its index, its push -- and never a second shape of
// note on one bus. The lane is the first line of the body, the way a card names its lane.
type certifyBusPoster struct {
	Bus     string
	As      string
	To      string
	Remote  string
	Branch  string
	Timeout time.Duration
}

func (b certifyBusPoster) Post(lane, subject, body string) error {
	note := bus.Skeleton{From: b.As, To: b.To, Subject: subject}.RenderWith("LANE: " + lane + "\n\n" + body)
	_, err := friends.BusSender{
		Bin: "nova-bus", Bus: b.Bus, As: b.As, Remote: b.Remote, Branch: b.Branch, Timeout: b.Timeout,
	}.Send(note)
	return err
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

// certifyStandard answers the provisioning standard's BYTES and where they came from.
//
// Three roads, in order: --standard names a file (a named file that cannot be read is a
// refusal -- somebody meant that file); the clone's own tools/bench-standard.sh above the
// working directory; and, outside a clone, the copy embedded in this binary. The embedded
// copy is the same file, compiled in, so the hash does not move between them.
func certifyStandard(named string) ([]byte, string, error) {
	if strings.TrimSpace(named) != "" {
		raw, err := os.ReadFile(named)
		if err != nil {
			return nil, "", fmt.Errorf("cannot read --standard %s: %w", named, err)
		}
		return raw, named, nil
	}
	dir, err := os.Getwd()
	if err == nil {
		for i := 0; i < 16; i++ {
			candidate := filepath.Join(dir, "tools", "bench-standard.sh")
			if raw, err := os.ReadFile(candidate); err == nil {
				return raw, candidate, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if len(tools.BenchStandard) == 0 {
		return nil, "", fmt.Errorf("this binary carries no provisioning standard and none is above the working directory; name one with --standard")
	}
	return tools.BenchStandard, "embedded", nil
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

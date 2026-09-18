package main

// The loop verb: bin/pulse-loop.sh as ONE program (internal/pulse/loop.go). One tick is run,
// fill and manager in the script's order under one queue lock, then the launch-dead probe,
// then one LOOP TICK line.

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdLoop(args []string, stdout, stderr io.Writer) int {
	f := newFlags("loop")
	queue := f.fs.String("queue", "", "")
	machines := f.fs.String("machines", "", "")
	lanes := f.fs.String("lanes", "", "")
	roots := f.fs.String("roots", "", "")
	repo := f.fs.String("repo", "", "")
	branch := f.fs.String("branch", "", "")
	policy := f.fs.String("policy", "", "")
	bus := f.fs.String("bus", "", "")
	as := f.fs.String("as", "", "")
	ready := f.fs.String("ready", "", "")
	launched := f.fs.String("launched", "", "")
	session := f.fs.String("session", "", "")
	ghConfig := f.fs.String("gh-config", "", "")
	once := f.fs.Bool("once", false, "")
	dryRun := f.fs.Bool("dry-run", false, "")
	deadline := f.fs.String("deadline", "", "")
	interval := f.fs.String("interval", "", "")
	grace := f.fs.String("launch-grace", "", "")
	capacity := f.fs.Int("capacity", -1, "")
	launcher := f.fs.String("launcher", "", "")
	ssh := f.fs.String("ssh", "", "")
	cardDeadline := f.fs.Int("card-deadline", defaultCardDeadline, "")
	timeout := f.fs.Int("timeout", 120, "")
	max := f.fs.Int("max", bounded.Default, "")
	var benches benchFlag
	f.fs.Var(&benches, "bench", "")

	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*queue, "queue", "the queue directory holding pending, launched, done, failed and the state files")
	f.want(*machines, "machines", "the machines registry: which hosts are benches and which serve the merge group's shards")
	f.want(*lanes, "lanes", "the lanes table: one <name>\\t<path prefixes> line per serial area")
	f.want(*roots, "roots", "the swarm roots this loop runs on, comma separated")
	dur := func(name, raw string, zero time.Duration) time.Duration {
		if strings.TrimSpace(raw) == "" {
			return zero
		}
		d, err := time.ParseDuration(raw)
		if err != nil || d < 0 {
			f.add(fmt.Sprintf("--%s wants a duration like 10s or 6h, got %q", name, raw))
			return zero
		}
		return d
	}
	end := dur("deadline", *deadline, 0)
	every := dur("interval", *interval, 0)
	wait := dur("launch-grace", *grace, 0)
	if !*once && strings.TrimSpace(*deadline) == "" {
		f.add("--deadline is required and wants a duration like 6h; --once runs exactly one tick and needs none")
	}
	if *capacity < -1 {
		f.add(fmt.Sprintf("--capacity is 0 or more, got %d; leave it out to read each bench's capacity over ssh", *capacity))
	}
	// --dry-run is REFUSED rather than half-kept: the run step's seams have no read-only
	// mode, so a dry run would still harvest, merge, reap, cut and launch. The flag exists so
	// the refusal can name the two verbs that do have one -- a flag that is simply absent
	// sends a person looking for a spelling (Stella's cold read of #1430, defect 1).
	if *dryRun {
		f.add("--dry-run is not honoured by the run step and is refused rather than half-kept: its seams (gate, harvest, sweep, reap, refill, launch) have no read-only mode, so the tick would still harvest, merge, reap, cut and launch. Use `nova-pulse fill --dry-run` and `nova-pulse manager --dry-run`, which change nothing, or run the loop for real (nova-tools #1441)")
	}
	if f.refused(stderr) {
		return 2
	}
	if err := applyGhConfig(*ghConfig); err != nil {
		fmt.Fprintf(stderr, "nova-pulse loop: --gh-config %s: %s\n", *ghConfig, err)
		return 2
	}
	if len(benches) == 0 {
		benches = fillBenches
	}
	var reader pulse.Capacity = sshCapacity{ssh: *ssh}
	if *capacity >= 0 {
		reader = fixedCapacity(*capacity)
	}
	return pulse.Loop(pulse.LoopInput{
		Queue: *queue, Machines: *machines, Lanes: *lanes, Roots: *roots,
		Repo: *repo, Branch: *branch, Policy: *policy, Bus: *bus, As: *as,
		Ready: *ready, Launched: *launched, Benches: []string(benches), Session: *session,
		Once: *once, DryRun: *dryRun, Deadline: end, Interval: every, Grace: wait,
		Max:      *max,
		Stdout:   stdout,
		Stderr:   stderr,
		Now:      func() time.Time { return time.Now().UTC() },
		Capacity: reader,
		Launcher: flashLauncher{bin: *launcher, deadline: *cardDeadline, grace: defaultLaunchGrace},
		Forge:    pulse.GHSource{Timeout: time.Duration(*timeout) * time.Second},
	})
}

// applyGhConfig exports GH_CONFIG_DIR for every gh, git and nova-merge child this process
// starts, which is what bin/pulse-loop.sh's line 4 did. gh answers as whoever that says, so
// a loop started from a service manager with a bare environment pushed and merged as
// nobody-in-particular; without the flag the caller's own GH_CONFIG_DIR goes through
// untouched (the manager dogfood, gap 3).
func applyGhConfig(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	return os.Setenv("GH_CONFIG_DIR", dir)
}

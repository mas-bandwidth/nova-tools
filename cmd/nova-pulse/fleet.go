package main

// The fleet verb and its power sub-verbs (SPEC-PULSE ## Fleet, issue #880 item 17). The
// work is internal/pulse/fleet.go; ssh comes from --ssh so a test puts a fake on PATH and
// no test reaches a machine.

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdFleet(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, " fleet", "a sub-verb is required (suspend, wake)")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "suspend":
		return cmdFleetSuspend(rest, stdout, stderr)
	case "wake":
		return cmdFleetWake(rest, stdout, stderr)
	}
	fmt.Fprintf(stderr, "nova-pulse fleet: unknown sub-verb %q (the sub-verbs are suspend, wake; run: nova-pulse help)\n", sub)
	return 2
}

func cmdFleetSuspend(args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet suspend")
	benches := f.fs.String("benches", "", "")
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
		Benches: *benches, Names: fleetNames(*bench), SSH: *ssh,
		Force: *force, IfIdle: *ifIdle,
		Timeout: time.Duration(*timeout) * time.Second, Max: *max,
		Stdout: stdout, Stderr: stderr,
	})
}

func cmdFleetWake(args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet wake")
	benches := f.fs.String("benches", "", "")
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
		Benches: *benches, Names: fleetNames(*bench), SSH: *ssh,
		Wait: whole, Timeout: time.Duration(*timeout) * time.Second, Max: *max,
		Now: func() time.Time { return time.Now().UTC() }, Sleep: time.Sleep,
		Stdout: stdout, Stderr: stderr,
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

package main

// `nova-pulse fleet net` -- the tailnet of THIS node, specified in docs/SPEC-FLEET-NET.md.
//
// Two sub-verbs are written: `status` (read-only, behind the Tailnet seam, the one certify
// needs first) and `init` (pure file generation: the policy, the GitOps workflow and
// TAILNET.md out of the machines registry). The rest are specified and refuse by name with
// the rule that specifies them, because a stub that pretends to work is worse than one that
// says what it is.

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdFleetNet(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, " fleet net", "a sub-verb is required ("+strings.Join(pulse.NetVerbs, ", ")+")")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "status":
		return cmdFleetNetStatus(rest, stdout, stderr)
	case "init":
		return cmdFleetNetInit(rest, stdout, stderr)
	case "acl", "ssh", "join", "expiry", "names", "share", "serve":
		return pulse.FleetNetNotImplemented(sub, stderr)
	}
	fmt.Fprintf(stderr, "nova-pulse fleet net: unknown sub-verb %q (the sub-verbs are %s; the spec is docs/SPEC-FLEET-NET.md)\n",
		sub, strings.Join(pulse.NetVerbs, ", "))
	return 2
}

func cmdFleetNetStatus(args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet net status")
	machines := f.fs.String("machines", "", "")
	tailscale := f.fs.String("tailscale", "", "")
	max := f.fs.Int("max", 0, "")
	timeout := f.fs.Int("timeout", 30, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*machines, "machines", "the machines registry: name, ssh, os/arch, roles, seat, cores, [provider,] notes, tab separated")
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
	}
	if *timeout < 1 {
		f.add(fmt.Sprintf("--timeout is a whole number of seconds above zero, got %d", *timeout))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.FleetNetStatus(pulse.FleetNetStatusInput{
		Machines:  *machines,
		Tailscale: *tailscale,
		Max:       *max,
		Timeout:   time.Duration(*timeout) * time.Second,
		Stdout:    stdout,
		Stderr:    stderr,
	})
}

func cmdFleetNetInit(args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet net init")
	machines := f.fs.String("machines", "", "")
	node := f.fs.String("node", "", "")
	tailnet := f.fs.String("tailnet", "", "")
	owner := f.fs.String("owner", "", "")
	out := f.fs.String("out", "", "")
	actionSHA := f.fs.String("action-sha", "", "")
	actionVer := f.fs.String("action-version", "", "")
	force := f.fs.Bool("force", false, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*machines, "machines", "the machines registry the policy is generated from")
	f.want(*node, "node", "the node's name, as the bus names it")
	f.want(*tailnet, "tailnet", "the tailnet as Tailscale spells it (example.com, org.github)")
	f.want(*owner, "owner", "the login that owns this node's devices; the one source that reaches the coordination machine")
	f.want(*out, "out", "the directory the three files are written under; `.` in the node's own repository")
	if strings.TrimSpace(*actionSHA) != "" && strings.TrimSpace(*actionVer) == "" {
		f.add("--action-sha without --action-version: a pinned action carries the tag it is, as a comment, or nobody can tell what was pinned")
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.FleetNetInit(pulse.FleetNetInitInput{
		Machines:  *machines,
		Node:      *node,
		Tailnet:   *tailnet,
		Owner:     *owner,
		Out:       *out,
		ActionSHA: strings.TrimSpace(*actionSHA),
		ActionVer: strings.TrimSpace(*actionVer),
		Force:     *force,
		Stdout:    stdout,
		Stderr:    stderr,
	})
}

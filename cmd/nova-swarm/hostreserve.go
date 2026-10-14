package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// THE SERVER HOST RESERVE (docs/SPEC-SWARM.md, the server host reserve).
//
// A member that runs on the same host as the sprint server must not take the
// server down: on 2026-10-13 a fleet card ran `go test` at 600% CPU beside the
// server, the bus, Redis and Postgres, the host load reached 74, and every seat
// verb slowed. A card's children already run at nice 19 and the load still
// reached 74, so this file adds the lever that holds on the server host: the
// card's whole process group runs at the background QoS (hostreserve_darwin.go),
// and GOMAXPROCS bounds each process the card starts. The policy is a pure
// function of the host, the OS and the core count, and the zero value is today's
// behaviour byte for byte.

// reservePolicy is what a member does for a card on the host that runs the sprint
// server. The zero value changes nothing: no QoS change and no GOMAXPROCS bound.
type reservePolicy struct {
	Background bool
	GoMaxProcs int
}

// hostReserve is the policy one host runs under. On the server host on darwin the
// card runs at the background QoS and each process is bounded to max(2, cores/8)
// threads; every other host keeps the zero policy, and so does Linux, where the
// same reserved CPU needs cgroups rather than a nice or a QoS value.
func hostReserve(serverHost bool, goos string, cores int) reservePolicy {
	if !serverHost || goos != "darwin" {
		return reservePolicy{}
	}
	n := cores / 8
	if n < 2 {
		n = 2
	}
	return reservePolicy{Background: true, GoMaxProcs: n}
}

// reserveEnv adds GOMAXPROCS to one card child's environment when the policy
// bounds it and the environment does not already name it. A card (or a harness)
// that sets GOMAXPROCS keeps its value: the reserve never overrides it.
func reserveEnv(env []string, p reservePolicy) []string {
	if p.GoMaxProcs <= 0 {
		return env
	}
	for _, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); name == "GOMAXPROCS" {
			return env
		}
	}
	out := make([]string, 0, len(env)+1)
	out = append(out, env...)
	return append(out, "GOMAXPROCS="+strconv.Itoa(p.GoMaxProcs))
}

// applyBackground runs the platform's background-QoS hook once for the leader pid
// when the policy puts the card's process group behind the server. It is called
// right after the child starts, from the same place that lowers the child's
// priority (nativeRunner.Start); children inherit the QoS. The hook is nil off
// the server host, and a policy that is not Background calls nothing.
func applyBackground(p reservePolicy, pid int, hook func(int) error) error {
	if !p.Background || hook == nil {
		return nil
	}
	return hook(pid)
}

// serverHostOf decides whether this member runs on the host that runs the sprint
// server: the --server-host flag, else a coordinator read the caller holds, compared
// with this machine's name. No such read exists in this path today — the member opens
// no store, and its one config read, the queue verb, carries the width and the machine's
// state word and no coordinator field — so production passes nil and --server-host alone
// decides (the loop row's argv names it on the server's machine). A read that is
// unavailable, or a machine name that cannot be read, means false: a member never
// guesses it is the server.
func serverHostOf(flag bool, coordinator func() (string, bool), hostname func() (string, error)) bool {
	if flag {
		return true
	}
	if coordinator == nil || hostname == nil {
		return false
	}
	host, ok := coordinator()
	if !ok || host == "" {
		return false
	}
	me, err := hostname()
	if err != nil {
		return false
	}
	return host == me
}

// reserveLine is the one MEMBER RESERVE line a member prints at its start, so a
// reader sees the policy in force (docs/SPEC-SWARM.md, the server host reserve).
func reserveLine(serverHost bool, p reservePolicy, cores int) string {
	return fmt.Sprintf("MEMBER RESERVE server-host=%t background=%t gomaxprocs=%d cores=%d",
		serverHost, p.Background, p.GoMaxProcs, cores)
}

// printReserve writes the MEMBER RESERVE line once.
func printReserve(w io.Writer, serverHost bool, p reservePolicy, cores int) {
	fmt.Fprintln(w, oneline.Escape(reserveLine(serverHost, p, cores)))
}

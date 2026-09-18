package pulse

// THE LOCAL SEAM: a bench that IS this machine is reached without ssh.
//
// `fill` read every bench's capacity over ssh, including the bench it was running on.
// Filling hulk FROM hulk therefore asked hulk to ssh to itself, and on 2026-09-18 that
// answered `glenn@hulk: Permission denied` -- the fleet's benches hold each other's keys
// and no machine holds its own, by design, because a machine that can ssh to itself is a
// machine a card can loop on. Every card the tick would have launched there was lost to a
// transport error that had nothing to do with capacity.
//
// `certify` already had the rule: a row whose ssh target resolves to the host you are on
// runs its work locally. This is that rule in pulse, with the registry as the one source
// of what a bench's ssh target IS -- never a guess from the name, because `--bench space`
// is an ~/.ssh/config alias and the hostname is something else entirely.
//
// The detection is pure: it takes the host name rather than reading it, so the tests name
// a host and no test asks the machine it runs on what it is called.

import (
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
)

// LocalNames is what a machine may be called and still be this one. A bare `localhost` and
// the loopback addresses are in it because an ~/.ssh/config alias may resolve to them.
var localNames = map[string]bool{
	"localhost": true,
	"127.0.0.1": true,
	"::1":       true,
}

// sshHost is the HOST part of an ssh target: `user@host:port` and `ssh://user@host/path`
// both answer `host`, lower case and without its domain. A registry row's ssh column is
// whatever a person types after `ssh`, so it is narrowed here and nowhere else.
func sshHost(target string) string {
	t := strings.TrimSpace(target)
	if t == "" || t == "-" {
		return ""
	}
	if _, rest, ok := strings.Cut(t, "://"); ok {
		t = rest
	}
	if _, rest, ok := strings.Cut(t, "@"); ok {
		t = rest
	}
	if i := strings.IndexAny(t, "/:"); i >= 0 {
		t = t[:i]
	}
	if i := strings.IndexByte(t, '.'); i > 0 {
		t = t[:i]
	}
	return strings.ToLower(t)
}

// IsLocalMachine says whether the registry resolves `bench` to the host named `host`. The
// bench's own NAME counts as well as its ssh target: hulk is reached as `hulk` and is
// called `hulk`, and a registry that has not been given an ssh column for it is still not
// a reason to open a connection to yourself.
func IsLocalMachine(reg *fleet.Registry, bench, host string) bool {
	h := sshHost(host)
	if h == "" {
		return false
	}
	if sshHost(bench) == h {
		return true
	}
	if reg == nil {
		return false
	}
	m, ok := reg.Lookup(bench)
	if !ok {
		return false
	}
	target := sshHost(m.SSH)
	return target != "" && (target == h || localNames[target])
}

// LocalBenches is the set of names in the registry that resolve to the host named `host`.
// It is read ONCE per run and handed to the seams, so the decision is taken in one place
// and every transport below it is told rather than asked.
func LocalBenches(machines, host string) map[string]bool {
	out := map[string]bool{}
	if strings.TrimSpace(host) == "" {
		return out
	}
	reg, err := fleet.ReadRegistry(machines)
	if err != nil {
		// A registry that cannot be read is refused by the verb itself, loudly and by
		// name. Here it means only that nothing is known to be local, which costs an
		// ssh and never a wrong launch.
		return out
	}
	for _, m := range reg.Machines() {
		if IsLocalMachine(reg, m.Name, host) {
			out[m.Name] = true
		}
	}
	return out
}

// ThisHost is the short name of the machine this process runs on, which is what a registry
// row's ssh target is compared against.
func ThisHost() string { return shortHost() }

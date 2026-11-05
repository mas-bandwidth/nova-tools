// Command nova-sandbox runs one command inside a wall the kernel enforces: the
// directories you name are readable or writable and everything else on disk is denied
// (sandbox-exec on macOS, Landlock on Linux).
package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
)

const usage = `nova-sandbox: run one command inside a kernel-enforced wall.

  nova-sandbox [--read <dir>]... [--write <dir>]... [--net-deny [--net-allow <host:port>]...] [--gpu] -- <cmd> <args...>
  nova-sandbox check
  nova-sandbox probe

  --read <dir>          readable and executable, recursively. Repeatable.
  --write <dir>         readable, writable and executable, recursively. Repeatable.
  --net-deny            TCP connect and bind are closed, or the wall refuses to start.
                        Without it nothing is promised about the network.
  --net-allow <h:port>  under --net-deny, reopen TCP connect to one loopback port. Repeatable.
  --gpu                 macOS: open the Metal device clients and compiler services.

Beyond the system roots, only what you name is reachable. Every path must be absolute,
exist and be a directory; none is created. The command must lie under a system root,
a --read or a --write. The environment and working directory pass through unchanged:
point TMPDIR and HOME inside a --write, and start from a directory the wall grants.

check   says which backend this machine has and whether it works.
probe   proves the wall: a write inside is allowed, a write and a read outside are
        refused, and a connect under --net-deny is refused.

Exit: the command's own status; 125 the wall refused to start (SANDBOX REFUSED lines on
stderr say why), 127 the command is on no PATH entry; check and probe: 0 yes, 1 no.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Environ()))
}

func run(args []string, stdout, stderr io.Writer, env []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "help", "-h", "--help":
			fmt.Fprint(stdout, usage)
			return 0
		case "check":
			return check(stdout)
		case "probe":
			return probe(stdout, env)
		case "dial": // probe's in-wall client: exit 0 if a TCP connect to args[1] succeeds
			return dial(args[1:])
		}
	}
	s, bad := parseArgs(args)
	if len(bad) == 0 {
		var w *wall
		if w, bad = build(s, runtime.GOOS); len(bad) == 0 {
			if r := execWall(w, env); r != nil {
				bad = []refusal{*r}
			}
		}
	}
	return report(stderr, bad)
}

// report prints every refusal and returns the exit status: 127 when the only problem is
// a command on no PATH entry, 125 otherwise.
func report(stderr io.Writer, bad []refusal) int {
	code := 127
	for _, r := range bad {
		fmt.Fprintf(stderr, "SANDBOX REFUSED reason=%s: %s\n", r.reason, strings.ReplaceAll(r.text, "\n", " "))
		if r.reason != "not_found" {
			code = 125
		}
	}
	return code
}

// parseArgs reads the flags before --, by hand because list flags repeat and the split at
// -- must be exact: everything after it is the command, verbatim.
func parseArgs(args []string) (s spec, bad []refusal) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			s.argv = args[i+1:]
			if len(s.argv) == 0 {
				bad = append(bad, refuse("no_command", "nothing after --"))
			}
			return s, bad
		}
		switch a {
		case "--net-deny":
			s.netDeny = true
			continue
		case "--gpu":
			s.gpu = true
			continue
		}
		dst := map[string]*[]string{"--read": &s.reads, "--write": &s.writes, "--net-allow": &s.netAllow}[a]
		switch {
		case dst == nil:
			bad = append(bad, refuse("bad_flag", "unknown flag %s; the flags are --read --write --net-deny --net-allow --gpu; run: nova-sandbox help", a))
		case i+1 >= len(args):
			bad = append(bad, refuse("bad_flag", "%s wants a value", a))
		default:
			i++
			*dst = append(*dst, args[i])
		}
	}
	return s, append(bad, refuse("no_command", "no --; the command comes after it: nova-sandbox --write <dir> -- <cmd> <args...>"))
}

// check says which backend is here and whether it works. Exit 0 if it does.
func check(stdout io.Writer) int {
	abi, netOK, note, err := wallCheck()
	if err != nil {
		fmt.Fprintf(stdout, "CHECK FAILED backend=%s abi=%s reason=%s\n", backendName, abi, strings.ReplaceAll(err.Error(), "\n", " "))
		return 1
	}
	net := "enforceable"
	if !netOK {
		net = "unavailable"
	}
	fmt.Fprintf(stdout, "CHECK OK backend=%s abi=%s net=%s note=%s\n", backendName, abi, net, note)
	return 0
}

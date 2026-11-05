// Command nova-sandbox runs one command inside a wall the kernel enforces: the
// directories you name are readable or writable and everything else on disk is denied
// (sandbox-exec on macOS, Landlock on Linux).
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

const usage = `nova-sandbox: run one command inside a kernel-enforced wall.

  nova-sandbox --read <dir>... --write <dir>... [--net-deny] [--cwd <dir>] -- <cmd> <args...>
  nova-sandbox check
  nova-sandbox probe

  --read <dir>          readable, recursively, and executable. Repeatable.
  --read-noexec <dir>   readable and not executable: a cache or a data tree. Repeatable.
  --write <dir>         readable and writable. Repeatable; at least one is required.
                        The first is the default working and temp directory.
  --cwd <dir>           the command's working directory; must be inside a --write.
  --tmp <dir>           TMPDIR for the command; must be inside a --write. Default:
                        <first --write>/.nova-sandbox-tmp, the one directory this tool makes.
  --net-deny            the network is closed, or the wall refuses to start. Without it
                        nothing is promised about the network.
  --net-allow <h:port>  under --net-deny, open one loopback port back up. Repeatable.
  --gpu <none|metal>    macOS only: open the named Metal clients and services.

Beyond the system roots the command needs to run, only what you name is reachable.
Every path must be absolute and exist; none is created. HOME must be set to a
directory inside a --write, because tools derive paths from it and the real HOME is
denied. Agent socket variables (SSH_AUTH_SOCK and the like) are removed from the
command's environment.

check   says which backend this machine has and whether it works.
probe   proves the wall: a write outside is refused, a write inside is allowed, a read
        outside is refused, and a connect under --net-deny is refused.

Exit: the command's own status; 125 the wall refused to start (SANDBOX REFUSED lines on
stderr say why), 127 the command is on no PATH entry; check and probe: 0 yes, 1 no.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Environ()))
}

func run(args []string, stdout, stderr io.Writer, env []string) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, "SANDBOX REFUSED reason=no_command: no arguments; run: nova-sandbox help\n")
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "check":
		return check(stdout)
	case "probe":
		return probe(stdout, env)
	case "dial": // probe's helper: exit 0 if a TCP connect to args[1] succeeds
		return dial(args[1:])
	}
	in, bad := parseArgs(args)
	if len(bad) == 0 {
		in.home, in.pathEnv, in.homes = homeOf(env), os.Getenv("PATH"), callerHomes()
		var p *policy
		if p, bad = build(in); len(bad) == 0 {
			if r := execWall(p, childEnv(env, p.tmp)); r != nil {
				bad = []refusal{*r}
			}
		}
	}
	allNotFound := true
	for _, r := range bad {
		fmt.Fprintf(stderr, "SANDBOX REFUSED reason=%s: %s\n", r.reason, strings.ReplaceAll(r.text, "\n", " "))
		allNotFound = allNotFound && r.reason == "not_found"
	}
	if allNotFound {
		return 127
	}
	return 125
}

// parseArgs reads the flags before --, by hand because list flags repeat and the split at
// -- must be exact: everything after it is the command, verbatim.
func parseArgs(args []string) (in input, bad []refusal) {
	var dashes bool
	for i := 0; i < len(args) && !dashes; i++ {
		a := args[i]
		if a == "--" {
			in.argv, dashes = args[i+1:], true
			break
		}
		if a == "--net-deny" {
			in.netDeny = true
			continue
		}
		dst := map[string]*[]string{"--read": &in.reads, "--read-noexec": &in.readsNoExec, "--write": &in.writes, "--net-allow": &in.netAllow}[a]
		one := map[string]*string{"--cwd": &in.cwd, "--tmp": &in.tmp, "--gpu": &in.gpu}[a]
		switch {
		case dst == nil && one == nil:
			bad = append(bad, refuse("bad_flag", "unknown flag %s; the flags are --read --read-noexec --write --cwd --tmp --net-deny --net-allow --gpu; run: nova-sandbox help", a))
		case i+1 >= len(args):
			bad = append(bad, refuse("no_command", "%s wants a value", a))
		case dst != nil:
			i++
			*dst = append(*dst, args[i])
		default:
			i++
			*one = args[i]
		}
	}
	if !dashes {
		bad = append(bad, refuse("no_command", "no --; the command comes after it: nova-sandbox --write <dir> -- <command> <args...>"))
	}
	return in, bad
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

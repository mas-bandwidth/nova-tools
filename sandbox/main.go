// Command nova-sandbox runs one command inside a wall the kernel enforces: the
// directories you name are readable or writable and everything else on disk is denied
// (sandbox-exec on macOS, Landlock and a seccomp filter on Linux).
package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"
)

const runFlags = `  --read <dir>          readable and executable, recursively. Repeatable.
  --write <dir>         readable, writable and executable, recursively. Repeatable.
  --net-deny            the network is closed, or the wall refuses to start. Linux: no
                        socket but unix and netlink-route (no TCP, UDP or raw); DNS works
                        only from /etc/hosts. macOS: no outbound IP and no DNS socket.
                        Without it nothing is promised about the network.
  --net-allow <h:port>  under --net-deny, reopen connect to one loopback port. Linux: TCP
                        connect only, on every address with that port; UDP stays closed,
                        and listen, accept and TCP Fast Open are refused on every socket.
                        macOS: TCP and UDP to localhost on that port. Repeatable.
  --gpu                 macOS: open the Metal and IOSurface IOKit clients and the
                        MTLCompiler, CoreMedia videoencoder and videodecoder services.
  --unix                Linux: leave AF_UNIX sockets open, which reopens the same-uid
                        escape (any local socket reachable by path). Without it /dev/log,
                        NSS sockets, agents and local daemons fail.
  --keep-fds            do not close inherited descriptors above stderr before exec.
                        Without it every fd > 2 is closed, so an inherited open socket
                        or file cannot be used inside the wall.
`

const usage = `nova-sandbox: run one command inside a kernel-enforced wall.

  nova-sandbox [--read <dir>]... [--write <dir>]... [--net-deny [--net-allow <host:port>]...] [--gpu] [--unix] [--keep-fds] -- <cmd> <args...>
  nova-sandbox show [the same flags] [-- <cmd> <args...>]
  nova-sandbox check
  nova-sandbox probe
  nova-sandbox help [<verb>]

` + runFlags + `
Beyond the system roots, only what you name is reachable. Every path must be absolute,
exist and be a directory; none is created. The command must lie under a system root,
a --read or a --write; it runs with argv[0] exactly as you give it. The environment and
working directory pass through unchanged: point TMPDIR and HOME inside a --write, and
start from a directory the wall grants.

show    prints the wall a flag set would apply here, without running anything.
check   says which backend this machine has and whether it works.
probe   proves the wall on this machine by running it.
Run nova-sandbox <verb> -h for a verb's own usage.

Exit: the command's own status; 2 the invocation is malformed (one line on stderr);
125 the wall refused to start on this machine (SANDBOX REFUSED lines on stderr say
why); 126 the command is not executable; 127 it is on no PATH entry.
`

const showUsage = `nova-sandbox show: print the wall a flag set would apply here, without running anything.

  nova-sandbox show [--read <dir>]... [--write <dir>]... [--net-deny [--net-allow <host:port>]...] [--gpu] [--unix] [--keep-fds] [-- <cmd> <args...>]

The flags are the run flags (nova-sandbox -h), checked the same way. The command is
optional; given, its resolved path and argv are shown too.

Output: one block on stdout, one rule per line in the order applied: paths, net,
seccomp rules by name, scope, descriptors; on macOS the generated profile, verbatim.

Exit: 0 the wall would start here; 1 it would refuse here (SANDBOX REFUSED lines on
stderr say why, stdout is empty); 2 the invocation is malformed; 126 and 127 the
command is not executable or on no PATH entry, as for a run.
`

const checkUsage = `nova-sandbox check: say which backend this machine has and whether it works.

  nova-sandbox check

One line on stdout: CHECK OK backend=<b> abi=<n> net=enforceable|unavailable note=<text>,
or CHECK FAILED backend=<b> abi=<n> reason=<text>. It applies nothing to a command.
net=unavailable means --net-deny will be refused here.

Exit: 0 CHECK OK; 1 CHECK FAILED; 2 the invocation is malformed.
`

const probeUsage = `nova-sandbox probe: prove the wall on this machine by running it.

  nova-sandbox probe

It walls children of this binary in a scratch directory and prints one line per step,
PROBE STEP name=<step> expect=allow|deny got=<what happened>, then PROBE OK or
PROBE FAILED. The steps are listed in docs/SPEC-SANDBOX.md.

Exit: 0 PROBE OK; 1 PROBE FAILED; 2 the invocation is malformed.
`

var verbUsage = map[string]string{"show": showUsage, "check": checkUsage, "probe": probeUsage}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Environ()))
}

func isHelp(a string) bool { return a == "-h" || a == "--help" }

// malformed prints the one CLI-STYLE refusal line for a malformed invocation of verb
// ("" is the run form) and returns 2.
func malformed(stderr io.Writer, verb string, what ...string) int {
	tool := "nova-sandbox"
	if verb != "" {
		tool += " " + verb
	}
	fmt.Fprintf(stderr, "%s: %s; run: %s -h\n", tool, strings.ReplaceAll(strings.Join(what, ". "), "\n", " "), tool)
	return 2
}

func run(args []string, stdout, stderr io.Writer, env []string) int {
	if len(args) == 0 {
		return malformed(stderr, "", "no command: nova-sandbox [flags] -- <cmd> <args...>")
	}
	switch v := args[0]; v {
	case "help":
		switch {
		case len(args) == 1:
			fmt.Fprint(stdout, usage)
		case len(args) == 2 && verbUsage[args[1]] != "":
			fmt.Fprint(stdout, verbUsage[args[1]])
		default:
			return malformed(stderr, "help", fmt.Sprintf("no help for %q; the verbs are show check probe", strings.Join(args[1:], " ")))
		}
		return 0
	case "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "check", "probe":
		if len(args) > 1 {
			if len(args) == 2 && isHelp(args[1]) {
				fmt.Fprint(stdout, verbUsage[v])
				return 0
			}
			return malformed(stderr, v, fmt.Sprintf("%s takes no flags or arguments and got %s", v, args[1]))
		}
		if v == "check" {
			return check(stdout)
		}
		return probe(stdout, env)
	case "show":
		if slices.ContainsFunc(beforeDashes(args[1:]), isHelp) {
			fmt.Fprint(stdout, showUsage)
			return 0
		}
		return show(args[1:], stdout, stderr)
	case clientVerb: // hidden: the probe's act inside the wall
		return probeClient(args[1:])
	}
	if !strings.HasPrefix(args[0], "-") {
		return malformed(stderr, "", fmt.Sprintf("unknown verb %s; the verbs are show check probe help (see nova-sandbox help), or flags then -- <cmd>", args[0]))
	}
	if slices.ContainsFunc(beforeDashes(args), isHelp) {
		fmt.Fprint(stdout, usage)
		return 0
	}
	w, bad := parseAndBuild(args, true)
	if len(bad) == 0 {
		bad = execWall(w, env)
	}
	return report(stderr, "", bad)
}

// beforeDashes is args up to the first --: a -h after it belongs to the command.
func beforeDashes(args []string) []string {
	if i := slices.Index(args, "--"); i >= 0 {
		return args[:i]
	}
	return args
}

// usageReasons are the refusals that mean the invocation itself is malformed.
var usageReasons = []string{"bad_flag", "no_command", "bad_read", "bad_write", "bad_net", "bad_gpu"}

// report prints the refusals and returns the exit status. A malformed invocation is one
// CLI-STYLE line naming every problem at once, exit 2. Otherwise each refusal is a
// SANDBOX REFUSED line: 125 when the wall refused to start, and when the only problem
// is the command itself, 126 not executable (shell convention) or 127 on no PATH entry.
func report(stderr io.Writer, verb string, bad []refusal) int {
	if slices.ContainsFunc(bad, func(r refusal) bool { return slices.Contains(usageReasons, r.reason) }) {
		return malformed(stderr, verb, texts(bad)...)
	}
	wallRefused, notExec := false, false
	for _, r := range bad {
		fmt.Fprintf(stderr, "SANDBOX REFUSED reason=%s: %s\n", r.reason, strings.ReplaceAll(r.text, "\n", " "))
		switch r.reason {
		case "not_found":
		case "not_executable":
			notExec = true
		default:
			wallRefused = true
		}
	}
	switch {
	case wallRefused:
		return 125
	case notExec:
		return 126
	default:
		return 127
	}
}

// parseAndBuild parses and checks the flags, and reports every problem in both at
// once: a bad path is named beside a missing --.
func parseAndBuild(args []string, needCommand bool) (*wall, []refusal) {
	s, bad := parseArgs(args, needCommand)
	w, more := build(s, runtime.GOOS)
	return w, append(bad, more...)
}

// texts is each refusal as "reason: text", for the one malformed-invocation line.
func texts(bad []refusal) []string {
	var what []string
	for _, r := range bad {
		what = append(what, r.reason+": "+r.text)
	}
	return what
}

// parseArgs reads the flags before --, by hand because list flags repeat and the split at
// -- must be exact: everything after it is the command, verbatim. needCommand is false
// for show, where the command is optional.
func parseArgs(args []string, needCommand bool) (s spec, bad []refusal) {
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
		case "--unix":
			s.unix = true
			continue
		case "--keep-fds":
			s.keepFDs = true
			continue
		}
		dst := map[string]*[]string{"--read": &s.reads, "--write": &s.writes, "--net-allow": &s.netAllow}[a]
		switch {
		case dst == nil:
			bad = append(bad, refuse("bad_flag", "unknown flag %s; the flags are --read --write --net-deny --net-allow --gpu --unix --keep-fds", a))
		case i+1 >= len(args):
			bad = append(bad, refuse("bad_flag", "%s wants a value", a))
		default:
			i++
			*dst = append(*dst, args[i])
		}
	}
	if needCommand {
		bad = append(bad, refuse("no_command", "no --; the command comes after it: nova-sandbox --write <dir> -- <cmd> <args...>"))
	}
	return s, bad
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

// show prints the wall the flags would apply here and runs nothing.
func show(args []string, stdout, stderr io.Writer) int {
	w, bad := parseAndBuild(args, false)
	if len(bad) > 0 { // the same codes as a run: 2 malformed, 126/127 the command alone
		return report(stderr, "show", bad)
	}
	text, bad := describeWall(w)
	if len(bad) > 0 {
		report(stderr, "show", bad)
		return 1
	}
	fmt.Fprint(stdout, text)
	return 0
}

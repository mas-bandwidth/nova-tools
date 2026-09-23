// nova-sprint table reads a consistent Redis snapshot or keeps the last
// published table across a unit restart when the next render is pending.
//
// A launchd kickstart -k SIGTERMs the unit's process group. The refresh
// the unit starts is put in its own session (POSIX setsid) so that signal
// does not kill it, and a start whose next render is not ready reprints
// the previous table instead of opening the published file empty.
//
// Exit 0 ran, 2 could not run.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprinttable"
)

var version string

const usage = `nova-sprint: the sprint table, kept across a unit restart (see docs/CLI.md)

usage:
  nova-sprint version
  nova-sprint help
  nova-sprint table --once (--fixture <file> [--out <file>] | --refresh pending --out <file>)
  nova-sprint table --redis <addr> [--sprint <name>] [--once | --loop] [--out <file>]
  nova-sprint table --check --redis <addr>
  nova-sprint refresh -- <command> [arg...]

table --fixture prints that file byte for byte, and with --out publishes it.
table --refresh pending does not open --out: the previous table stays, and
the command prints it again. An empty render is not published.
The Redis form reads one consistent FCALL_RO snapshot per render; --loop
renders once per second. Load the function library first with
nova-sprint fn load --redis <addr> (fn check exits 1 while it is missing or stale).
Control sprints are hidden unless named with --sprint.
--check reads an existing throwaway fixture store and compares exact output.

refresh runs the command after -- in its own session (POSIX setsid) and
returns without waiting, so a unit restart does not kill it. The loop
plist sets AbandonProcessGroup (fleet/templates/nova-loop.plist.j2), which
stops launchd from signalling the unit's process group; setsid is the
refresh leaving that group itself.

exit codes: 0 ran, 2 could not run.

example:
  nova-sprint table --once --fixture table.txt
  nova-sprint table --once --fixture table.txt --out sprint-table.txt
  nova-sprint table --once --refresh pending --out sprint-table.txt
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, "", "no verb; table renders, refresh detaches")
	}
	switch args[0] {
	case "help", "-h", "--help":
		if len(args) > 1 {
			return refuse(stderr, "help", "help takes no arguments")
		}
		fmt.Fprint(stdout, usageWithRegisteredVerbs())
		return 0
	case "version", "--version":
		if len(args) > 1 {
			return refuse(stderr, "version", "version takes no arguments")
		}
		fmt.Fprintln(stdout, buildinfo.Line("nova-sprint", version))
		return 0
	case "table":
		return cmdTable(args[1:], stdout, stderr)
	case "refresh":
		return cmdRefresh(args[1:], stdout, stderr)
	default:
		if code, ok := runRegistered(args[0], args[1:], stdout, stderr); ok {
			return code
		}
		return refuse(stderr, "", fmt.Sprintf("unknown verb %s; table renders, refresh detaches", args[0]))
	}
}

func refuse(stderr io.Writer, verb, what string) int {
	where := ""
	if verb != "" {
		where = " " + verb
	}
	fmt.Fprintf(stderr, "nova-sprint%s: %s; run: nova-sprint help\n", where, oneline.Escape(what))
	return 2
}

func cmdRefresh(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "--" {
		return refuse(stderr, "refresh", "needs -- and the command to run in its own session, for example: nova-sprint refresh -- /usr/bin/true")
	}
	argv := args[1:]
	if len(argv) == 0 {
		return refuse(stderr, "refresh", "needs a command after --; that command is what survives the unit restart")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	if err := sprinttable.ApplyOwnSession(cmd); err != nil {
		return refuse(stderr, "refresh", err.Error())
	}
	if err := cmd.Start(); err != nil {
		return refuse(stderr, "refresh", "cannot start "+argv[0]+": "+err.Error())
	}
	go func() { _ = cmd.Wait() }()
	fmt.Fprintf(stdout, "REFRESH SESSION pid=%d\n", cmd.Process.Pid)
	return 0
}

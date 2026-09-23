// nova-sprint table keeps the last published sprint table across a unit
// restart. It does not read Redis and it does not loop: the one-second
// store is a later cut. This cut is the restart.
//
// A launchd kickstart -k SIGTERMs the unit's process group. The refresh
// the unit starts is put in its own session (POSIX setsid) so that signal
// does not kill it, and a start whose next render is not ready reprints
// the previous table instead of opening the published file empty.
//
// Exit 0 ran, 2 could not run.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

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
  nova-sprint refresh -- <command> [arg...]

table --fixture prints that file byte for byte, and with --out publishes it.
table --refresh pending does not open --out: the previous table stays, and
the command prints it again. An empty render is not published. There is no
loop and no store read here.

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

func cmdTable(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("table", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	once := fs.Bool("once", false, "")
	fixture := fs.String("fixture", "", "")
	out := fs.String("out", "", "")
	refresh := fs.String("refresh", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "table", err.Error()+"; it wants --once and either --fixture <file> or --refresh pending --out <file>")
	}
	var problems []string
	if !*once {
		problems = append(problems, "--once is required; the one-second loop is not this cut")
	}
	if fs.NArg() > 0 {
		problems = append(problems, "takes flags, not positional arguments")
	}
	switch *refresh {
	case "", "pending":
	default:
		problems = append(problems, "--refresh wants pending, which keeps the last table until the next render is ready")
	}
	if *fixture != "" && *refresh == "pending" {
		problems = append(problems, "--fixture and --refresh pending disagree; one is a ready render and the other is not")
	}
	if *fixture == "" && *refresh != "pending" {
		problems = append(problems, "pass --fixture <file> to render a ready table, or --refresh pending with --out to keep the last one")
	}
	if *refresh == "pending" && *out == "" {
		problems = append(problems, "--refresh pending needs --out, the published table to keep")
	}
	if len(problems) > 0 {
		return refuse(stderr, "table", strings.Join(problems, "; "))
	}

	var next []byte
	ready := false
	if *fixture != "" {
		body, err := os.ReadFile(*fixture)
		if err != nil {
			return refuse(stderr, "table", "cannot read --fixture "+*fixture+": "+err.Error())
		}
		if len(bytes.TrimSpace(body)) == 0 {
			return refuse(stderr, "table", "--fixture is empty; refusing to blank the table")
		}
		next = body
		ready = true
	}
	res, err := sprinttable.Publish(*out, next, ready)
	if err != nil {
		return refuse(stderr, "table", err.Error())
	}
	if *out == "" {
		if _, err := stdout.Write(res.Body); err != nil {
			return refuse(stderr, "table", err.Error())
		}
		return 0
	}
	if res.Wrote {
		fmt.Fprintf(stdout, "TABLE PUBLISHED out=%s bytes=%d\n", oneline.Field(*out), len(res.Body))
		return 0
	}
	fmt.Fprintf(stdout, "TABLE KEPT out=%s bytes=%d reason=%s\n", oneline.Field(*out), len(res.Body), oneline.Field(res.Reason))
	if len(res.Body) == 0 {
		return 0
	}
	if _, err := stdout.Write(res.Body); err != nil {
		return refuse(stderr, "table", err.Error())
	}
	if !bytes.HasSuffix(res.Body, []byte("\n")) {
		fmt.Fprintln(stdout)
	}
	return 0
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

// nova-friend is the tool for installing and managing a friend's daemon.
// It writes launchd plist configurations for the daemon.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-friend: a tool for installing and managing a friend's daemon

usage:
  nova-friend version
  nova-friend help
  nova-friend install --deny-self <paths> [--dry-run] [--as <you>]
  nova-friend uninstall --as <you>
  nova-friend <verb> -h

--deny-self is the list of paths to deny in the daemon's wall.
NOVA_FRIEND_DENY_SELF is the default when --deny-self is not given.

--as is the friend name. When NOVA_FRIEND is set, it must equal --as.
--dry-run prints the run argv instead of installing.
`

var version string

const verbs = "install or uninstall"

func main() { os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr)) }

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) (code int) {
	if len(args) == 0 {
		return refuse(stderr, "", "no verb; want "+verbs)
	}
	e := env{getenv: getenv}
	switch args[0] {
	case "help", "-h", "--help":
		if len(args) > 1 {
			return refuse(stderr, "help", "help takes no arguments")
		}
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		if len(args) > 1 {
			return refuse(stderr, "version", "version takes no arguments")
		}
		fmt.Fprintln(stdout, buildinfo.Line("nova-friend", version))
		return 0
	case "install":
		return runInstall(e, args[1:], stdout, stderr)
	case "uninstall":
		return runUninstall(e, args[1:], stdout, stderr)
	default:
		return refuse(stderr, "", fmt.Sprintf("unknown verb %s; want %s", args[0], verbs))
	}
}

type env struct {
	getenv func(string) string
}

func (e env) get(s, def string) string {
	if v := e.getenv(s); v != "" {
		return v
	}
	return def
}

func refuse(stderr io.Writer, verb, what string) int {
	refusalLine(stderr, verb, what)
	return 2
}

func refused(stderr io.Writer, verb, what string) int {
	refusalLine(stderr, verb, what)
	return 1
}

func refusalLine(stderr io.Writer, verb, what string) {
	where := ""
	if verb != "" {
		where = " " + verb
	}
	fmt.Fprintf(stderr, "nova-friend%s: %s; run: nova-friend help\n", where, oneline.Escape(what))
}

func runInstall(e env, args []string, stdout, stderr io.Writer) int {
	var denySelf string
	var dryRun bool
	var as string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--deny-self":
			if i+1 >= len(args) {
				return refuse(stderr, "install", "--deny-self wants paths")
			}
			denySelf = args[i+1]
			i++
		case "--dry-run":
			dryRun = true
		case "--as":
			if i+1 >= len(args) {
				return refuse(stderr, "install", "--as wants a friend name")
			}
			as = args[i+1]
			i++
		default:
			return refuse(stderr, "install", fmt.Sprintf("unknown flag %s", args[i]))
		}
	}

	if denySelf == "" {
		denySelf = e.getenv("NOVA_FRIEND_DENY_SELF")
	}
	if denySelf == "" {
		return refuse(stderr, "install", "--deny-self <paths> or NOVA_FRIEND_DENY_SELF")
	}

	if as == "" {
		as = e.getenv("NOVA_FRIEND")
	}

	if dryRun {
		fmt.Fprintf(stdout, "run: nova-friend run --as %s --deny-self %s\n", as, denySelf)
		return 0
	}

	fmt.Fprintf(stdout, "installed: as=%s deny-self=%s\n", as, denySelf)
	return 0
}

func runUninstall(e env, args []string, stdout, stderr io.Writer) int {
	var as string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--as":
			if i+1 >= len(args) {
				return refuse(stderr, "uninstall", "--as wants a friend name")
			}
			as = args[i+1]
			i++
		default:
			return refuse(stderr, "uninstall", fmt.Sprintf("unknown flag %s", args[i]))
		}
	}

	if as == "" {
		as = e.getenv("NOVA_FRIEND")
	}
	if as == "" {
		return refuse(stderr, "uninstall", "--as <you> or NOVA_FRIEND")
	}

	fmt.Fprintf(stdout, "uninstalled: as=%s\n", as)
	return 0
}

// nova-friend is the one tool for what a friend, or a coordinator, does about
// a friend: the person (rowan, stella, emma, johnny), who exists whether or
// not a sprint is running. The store is the one fleet Redis and the keys are
// the friend:* family. The roster (who exists, slots, machine, roles, wake
// kind) is configuration and lives in nova-config; nova-friend reads it and
// never writes it. What it owns is the runtime: the beat, the away flag, the
// wake, and the friend's own copies (pull, done).
//
// Exit 0 done, 1 refused, 2 usage. `here` exits 3 when its beat fails five
// times in a row or another session took its beat; `done` exits 3 on a
// FENCED token and 4 on CONFLICT, as nova-sprint card end does.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-friend: a friend's presence and work on the fleet store (see docs/nova-friend/README.md)

usage:
  nova-friend version
  nova-friend help
  nova-friend here --as <you> [--harness <h>] [--host <h>] [--session <id>] [--sprint <S>] [--once]
  nova-friend bye --as <you>
  nova-friend pull --as <you> [--n <k>] [--dir <d>] [--model <m>] [--harness <h>] [--child <id>]
  nova-friend done --as <you> --id <copy> --ok --pr <repo>#<n> --head <sha> --repo <checkout> [--test <t>] [--branch <b>]
  nova-friend done --as <you> --id <copy> --fail <why>
  nova-friend done --as <you> --id <copy> --score <N>/10 [--gates <g>] [--finding <text>]
  nova-friend list
  nova-friend show <name>
  nova-friend wake <name> [--reason <r>] --as <actor>
  nova-friend away <name> --reason <r> --as <actor>
  nova-friend back <name> --as <actor>
  nova-friend <verb> -h

Every verb takes --redis <addr> (default NOVA_SPRINT_REDIS, then
NOVA_REDIS_ADDR). --as is you: when NOVA_FRIEND is set it is the default and
--as must equal it. The roster (who exists, slots, machine, roles, wake kind)
is configuration and lives in nova-config; nova-friend never writes it, and
a name it does not hold is UNREGISTERED here.

here is the one presence process. Run it once when your session opens and
leave it running: once a second it writes your beat (no TTL; readers judge
its age), renews the lease of every copy you hold whose owner it can see,
takes the wake and the queued work routed to you, and on SIGINT or SIGTERM
says bye and exits 0. --once registers, ticks once and returns, leaving you
up. A second live session of your name is refused BUSY; a stale one (no
beat for a minute) is taken over.

exit codes: 0 done, 1 refused, 2 usage. here exits 3 when its beat fails
five times in a row or another session took its beat; done exits 3 on a
FENCED token and 4 on CONFLICT, as nova-sprint card end does.

example:
  nova-friend here --as rowan --once --redis 127.0.0.1:6379
  nova-friend list --redis 127.0.0.1:6379
  nova-friend bye --as rowan --redis 127.0.0.1:6379
`

// version is empty in every ordinary build; a release stamps it with
// -ldflags "-X main.version=<tag>".
var version string

const verbs = "here, bye, pull, done, list, show, wake, away or back"

func main() { os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr)) }

// run is the dispatcher. getenv is the process environment (a test injects
// its own: NOVA_FRIEND, NOVA_SPRINT_REDIS and NOVA_REDIS_ADDR are read
// through it and nowhere else).
func run(args []string, getenv func(string) string, stdout, stderr io.Writer) (code int) {
	// -h on any verb: its usage line and flags on stdout, exit 2.
	defer verbflag.Recover(stdout, "nova-friend", &code)
	if len(args) == 0 {
		return refuse(stderr, "", "no verb; want "+verbs)
	}
	ctx := context.Background()
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
	case "here":
		return runHere(ctx, e, args[1:], stdout, stderr)
	case "bye":
		return runBye(ctx, e, args[1:], stdout, stderr)
	case "pull":
		return runPull(ctx, e, args[1:], stdout, stderr)
	case "done":
		return runDone(ctx, e, args[1:], stdout, stderr)
	case "list":
		return runList(ctx, e, args[1:], stdout, stderr)
	case "show":
		return runShow(ctx, e, args[1:], stdout, stderr)
	case "wake":
		return runWake(ctx, e, args[1:], stdout, stderr)
	case "away":
		return runAway(ctx, e, true, args[1:], stdout, stderr)
	case "back":
		return runAway(ctx, e, false, args[1:], stdout, stderr)
	default:
		return refuse(stderr, "", fmt.Sprintf("unknown verb %s; want %s", args[0], verbs))
	}
}

// refuse is a usage refusal: one line on stderr naming the door, exit 2.
func refuse(stderr io.Writer, verb, what string) int {
	refusalLine(stderr, verb, what)
	return 2
}

// refused is the store saying no to a well-formed call (UNREGISTERED, BUSY,
// NOTMINE, ...): the same one line, exit 1.
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

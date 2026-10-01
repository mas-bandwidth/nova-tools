package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
)

// The coordinator's verbs go to the sprint's server too (the owner, 2026-10-01: "Let's
// go and finalize the one client/server path for writing and then this whole system
// collapses into a tiny core"). With NOVA_SPRINT_SERVER set (the server's loopback
// address, which `run --listen` prints), a verb that writes the sprint is not run here:
// its arguments are sent to the server, which runs it beside the store on its one line
// of control, and what it printed is printed here with its exit code. One process
// writes the sprint. The reads (queue, inbox, card, log, check, where, routes) and the
// verbs the server runs for nobody (notServed) run here, as before; so does any verb
// given its own --redis.

// ServerEnv names the sprint's server for the coordinator's verbs: host:port.
const ServerEnv = "NOVA_SPRINT_SERVER"

// fileFlags are the flags of the coordinator's verbs whose value is a file or a
// directory: the server runs in another directory, so a path sent to it is absolute.
var fileFlags = []string{"--rules", "--brief-file", "--brief-dir", "--file"}

// forwarded sends the verb to the sprint's server when there is one and the verb is
// one that writes the sprint; sent is false when the verb runs here.
func (a *app) forwarded(args []string, stdout, stderr io.Writer) (code int, sent bool) {
	addr := a.getenv(ServerEnv)
	if addr == "" || a.serveAddr != "" {
		return 0, false // no server named, or this process is the server
	}
	name, words, why := localVerb(args)
	if why != "" {
		return 0, false // not served (run, land, ...), not a verb, or its own --redis: runs here
	}
	switch verbClasses[name] {
	case classCoordinator, classReport, classWorker:
	default:
		return 0, false // a read, or the machine's own
	}
	for _, w := range args[words:] {
		if verbHelpWord(w) {
			return 0, false // help is printed here
		}
	}
	send := a.forward
	if send == nil {
		send = func(ctx context.Context, addr string, verbs ...[]string) ([]sprintwire.Result, error) {
			return sprintwire.Client{Addr: addr}.Do(ctx, verbs...)
		}
	}
	argv := slices.Clone(args)
	if actor := a.getenv("NOVA_SPRINT_ACTOR"); actor != "" && verbClasses[name] != classWorker {
		// who acts is this caller's, said before its own words so a --actor it gave wins
		argv = slices.Concat(argv[:words], []string{"--actor", actor}, argv[words:])
	}
	argv = absolutePaths(argv)
	res, err := send(context.Background(), addr, argv)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s; nothing is known of what ran: read the sprint (nova-sprint where, log) before running it again\n", prog, name, oneline.Escape(err.Error()))
		return 2, true
	}
	_, _ = io.WriteString(stdout, res[0].Stdout) // ignored: the caller's own streams
	_, _ = io.WriteString(stderr, res[0].Stderr)
	return res[0].Code, true
}

// verbHelpWord says the word asks for a verb's help.
func verbHelpWord(w string) bool {
	return w == "-h" || w == "--help" || w == "-help" || w == "help"
}

// absolutePaths is the arguments with each file flag's value made absolute from this
// directory (`--flag value` and `--flag=value`); one that cannot be is left as it is,
// for the verb to refuse.
func absolutePaths(argv []string) []string {
	abs := func(p string) string {
		if a, err := filepath.Abs(p); err == nil && p != "" {
			return a
		}
		return p
	}
	for i := 0; i < len(argv); i++ {
		flag, value, has := strings.Cut(argv[i], "=")
		if !slices.Contains(fileFlags, flag) {
			continue
		}
		if has {
			argv[i] = flag + "=" + abs(value)
		} else if i+1 < len(argv) {
			i++
			argv[i] = abs(argv[i])
		}
	}
	return argv
}

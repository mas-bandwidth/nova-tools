package main

import (
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The coordinator's verbs reach the sprint through its server (forward.go), and
// NOVA_SPRINT_SERVER says where that is. A cold coordinator names neither a store
// nor a server: this is the default that makes `nova-sprint <verb>` the whole
// command line for them, with no wrapper and no Redis password, the server the
// one writer (docs/SPEC-SPRINT.md, "The coordinator's verbs go to the server
// too"; docs/CLI.md, "nova-sprint").

// LocalServer is the local sprint server's loopback address: the server a verb
// reaches when nothing names a store and nothing names a server. `run --listen`
// prints its own address; this is the one the fleet's clients already assume
// (docs/CLI.md, "nova-friend").
const LocalServer = "127.0.0.1:6390"

// useLocalServerDefault is main's one call (main.go): when this process names no
// server (NOVA_SPRINT_SERVER), no store (NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR) and no
// seat login records a store either, it answers NOVA_SPRINT_SERVER with the local
// sprint server from here on and says true. Every verb the server runs then goes
// through it as a client, which asks for no store and no secret; the verbs not
// served run here as they did, and one that needs the store still refuses for want
// of an address. A login that cannot be read names a store all the same: the verb
// runs here, where its refusal can name the file and its remedy rather than a
// server no one named (docs/SPEC-SPRINT.md, "The coordinator's verbs go to the
// server too" and "The seat's store login").
func (a *app) useLocalServerDefault() bool {
	if a.serveAddr != "" || a.getenv(ServerEnv) != "" || firstEnv(a.getenv, "NOVA_SPRINT_REDIS", "NOVA_REDIS_ADDR") != "" {
		return false
	}
	if _, ok, err := a.recordedLogin(); ok || err != nil {
		return false
	}
	base := a.getenv
	a.getenv = func(k string) string {
		if k == ServerEnv {
			return LocalServer
		}
		return base(k)
	}
	return true
}

// clientForwarded is the step run takes before the store (main.go), forwarded with
// the default's two own acts: a served verb going through the local server says
// which it used, in one NOTE line naming the local server and how to name a store
// or another server instead; and the seat's store login stays on this machine,
// where seat login and seat logout record and remove the login of the machine they
// are typed on, never on the wire (docs/SPEC-SPRINT.md, "The seat's store login").
// Without the default it is exactly forwarded, as before: a named server, a named
// store or a store login changes nothing here.
func (a *app) clientForwarded(args []string, stdout, stderr io.Writer) (code int, sent bool) {
	if a.serverDefaulted {
		v := readVerb(args)
		sub := ""
		if v.words > 0 && len(args) > v.words {
			sub = args[v.words]
		}
		switch {
		case v.name == "seat" && (sub == "login" || sub == "logout"):
			return 0, false
		case v.words != 0 && a.serveAddr == "" && !v.help && v.err == nil && !v.given("redis") && v.unserved() == "":
			fmt.Fprintf(stderr, "NOTE %s %s: no NOVA_SPRINT_REDIS and no %s named, so through the local sprint server at %s; name a store with --redis <addr>, or a server with %s=<addr>\n",
				prog, v.name, ServerEnv, oneline.Field(LocalServer), ServerEnv)
		}
	}
	return a.forwarded(args, stdout, stderr)
}

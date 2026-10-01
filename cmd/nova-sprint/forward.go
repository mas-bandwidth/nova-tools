package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
)

// The coordinator's verbs go to the sprint's server too (the owner, 2026-10-01: "Let's
// go and finalize the one client/server path for writing and then this whole system
// collapses into a tiny core"). With NOVA_SPRINT_SERVER set (the server's loopback
// address, which `run --listen` prints), a verb that writes the sprint is not run here:
// its arguments are sent to the server, which runs it beside the store on its one line
// of control, and what it printed is printed here with its exit code. One process
// writes the sprint. The reads (queue, inbox, card, log, check, where, routes), the
// verbs the server runs for nobody (notServed), a verb that waits for the sprint to
// move (waits), a verb given its own --redis, and a verb's help run here, as before.
// What a verb's arguments say (its help, its flags, which word is a flag's value) is
// read by the verb's own flags (readVerb), never by a scan of the words.

// ServerEnv names the sprint's server for the coordinator's verbs: host:port.
const ServerEnv = "NOVA_SPRINT_SERVER"

// fileFlags are the flags of the coordinator's verbs whose value is a file or a
// directory: the server runs in another directory, so a path sent to it is absolute.
var fileFlags = []string{"rules", "brief-file", "brief-dir", "file"}

// waits are the flags that make a read wait for the sprint to move (where --watch,
// inbox --wait). The server moves the sprint on the one line of control a verb it runs
// holds, so such a verb is never run by the server: it runs where it is typed.
var waits = map[string]string{"where": "watch", "inbox": "wait"}

// verbArgs is an argument list as its verb's own flags read it.
type verbArgs struct {
	name  string        // the verb; "" when the list names none
	words int           // how many words the verb is
	fs    *flag.FlagSet // the verb's flags, set as the list sets them; nil for no verb
	help  bool          // the list asks for the verb's help
	err   error         // the flags do not parse: the verb refuses them where it runs
}

// readVerb is the argument list read by its verb's flags, as the verb parses them.
func readVerb(argv []string) (v verbArgs) {
	for _, vb := range verbs {
		w := strings.Fields(vb.name)
		if len(argv) >= len(w) && slices.Equal(argv[:len(w)], w) && len(w) > v.words {
			v.name, v.words = vb.name, len(w)
		}
	}
	if v.words == 0 {
		return v
	}
	if v.fs = verbFlags(v.name); v.fs == nil {
		v.err = fmt.Errorf("the flags of %s could not be read", v.name)
		return v
	}
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(verbflag.Help); !ok {
				panic(r)
			}
			v.help = true
		}
	}()
	_, v.err = parse(v.fs, argv[v.words:])
	return v
}

// verbFlags is the verb's flag set, got as its -h is (helpCommand): the verb run with
// --help stops at its flags, before it reads or writes anything. nil when it did not.
func verbFlags(name string) (fs *flag.FlagSet) {
	for _, v := range verbs {
		if v.name != name {
			continue
		}
		defer func() {
			r := recover()
			if h, ok := r.(verbflag.Help); ok {
				fs = h.FS
			} else if r != nil {
				panic(r)
			}
		}()
		v.run(newApp(func(string) string { return "" }), []string{"--help"}, io.Discard, io.Discard)
		return nil
	}
	return nil
}

// on says the list sets the verb's boolean flag true.
func (v verbArgs) on(name string) bool {
	if v.fs == nil {
		return false
	}
	f := v.fs.Lookup(name)
	return f != nil && f.Value.String() == "true"
}

// given says the list gives the flag.
func (v verbArgs) given(name string) (yes bool) {
	if v.fs != nil {
		v.fs.Visit(func(f *flag.Flag) { yes = yes || f.Name == name })
	}
	return yes
}

// unserved is why the server does not run the verb sent from this machine, "" when it
// does: any verb of the command but the ones no one is served (notServed), one that
// waits for the sprint to move (waits), and one naming a store (the server's is the
// store).
func (v verbArgs) unserved() string {
	switch {
	case v.words == 0:
		return "not a verb of nova-sprint; run: nova-sprint help"
	case slices.Contains(notServed, v.name):
		return v.name + " is not run by the server; run it by itself"
	case v.given("redis"):
		return "--redis is not given to the server: its store is the sprint's"
	case v.on(waits[v.name]):
		return v.name + " --" + waits[v.name] + " waits for the sprint to move, and the server moves it: it is a read, run where it is typed"
	}
	return ""
}

// writes says the verb writes the sprint: a coordinator's, a report's or a worker's
// verb, or inbox --read, which moves the coordinator's cursor.
func (v verbArgs) writes() bool {
	switch verbClasses[v.name] {
	case classCoordinator, classReport, classWorker:
		return true
	}
	return v.name == "inbox" && v.on("read")
}

// forwarded sends the verb to the sprint's server when there is one and the verb is
// one that writes the sprint; sent is false when the verb runs here.
func (a *app) forwarded(args []string, stdout, stderr io.Writer) (code int, sent bool) {
	addr := a.getenv(ServerEnv)
	if addr == "" || a.serveAddr != "" {
		return 0, false // no server named, or this process is the server
	}
	v := readVerb(args)
	if v.unserved() != "" || v.help || v.err != nil || !v.writes() {
		return 0, false // not served, a read, a wait, its help, or flags it refuses: runs here
	}
	send := a.forward
	if send == nil {
		send = func(ctx context.Context, addr string, verbs ...[]string) ([]sprintwire.Result, error) {
			return sprintwire.Client{Addr: addr}.Do(ctx, verbs...)
		}
	}
	// who acts is this caller's alone, said even when it is no one (the server never
	// acts as its own environment names), before the caller's own words so a --actor it
	// gave wins
	argv := slices.Concat(args[:v.words], []string{"--actor", a.getenv("NOVA_SPRINT_ACTOR")}, args[v.words:])
	argv = absolutePaths(argv)
	res, err := send(context.Background(), addr, argv)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s; nothing is known of what ran: read the sprint (nova-sprint where, log) before running it again\n", prog, v.name, oneline.Escape(err.Error()))
		return 2, true
	}
	_, _ = io.WriteString(stdout, res[0].Stdout) // ignored: the caller's own streams
	_, _ = io.WriteString(stderr, res[0].Stderr)
	return res[0].Code, true
}

// absolutePaths is the arguments with each file flag's value made absolute from this
// directory (`--flag value` and `--flag=value`). Which word is a flag and which is a
// flag's value is as the verb's flags parse them: a value that looks like a flag is a
// value, a boolean flag takes no word, and nothing after -- is a flag. Arguments whose
// flags do not parse are left as they are, for the verb to refuse; so is a value that
// cannot be made absolute.
func absolutePaths(argv []string) []string {
	v := readVerb(argv)
	if v.fs == nil || v.err != nil || v.help {
		return argv
	}
	abs := func(p string) string {
		if a, err := filepath.Abs(p); err == nil && p != "" {
			return a
		}
		return p
	}
	for i := v.words; i < len(argv); i++ {
		w := argv[i]
		if w == "--" {
			break
		}
		if len(w) < 2 || w[0] != '-' {
			continue // a word the verb takes as it is
		}
		name, value, has := strings.Cut(strings.TrimPrefix(w[1:], "-"), "=")
		f := v.fs.Lookup(name)
		if f == nil {
			continue
		}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			continue
		}
		file := slices.Contains(fileFlags, name)
		switch {
		case has && file:
			argv[i] = w[:len(w)-len(value)] + abs(value)
		case !has && i+1 < len(argv):
			i++ // the flag's value, whatever it looks like
			if file {
				argv[i] = abs(argv[i])
			}
		}
	}
	return argv
}

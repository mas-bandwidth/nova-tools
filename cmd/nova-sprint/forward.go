package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
)

// The coordinator's verbs go to the sprint's server too: a single client/server
// path for writing collapses the system into a tiny core, and the reads close
// the same gap. With
// NOVA_SPRINT_SERVER set (the server's loopback address, which `run --listen` prints),
// a verb the server runs is not run here: its arguments are sent to the server, which
// runs it beside the store on its one line of control, and what it printed is printed
// here with its exit code, so the coordinator's side needs no store and no store
// credentials. One process reads and writes the sprint. The verbs the server runs for
// nobody (notServed), a verb given its own --redis, and a verb's help run here, as
// before. A read that waits for the sprint to move (waits) is never run by the server:
// here it sends the server its plain read, again and again (where --watch: one a
// frame; inbox --wait: the log's tick-end notes, polled). What a verb's arguments say
// (its help, its flags, which word is a flag's value) is read by the verb's own flags
// (readVerb), never by a scan of the words.

// ServerEnv names the sprint's server for the coordinator's verbs: host:port.
const ServerEnv = "NOVA_SPRINT_SERVER"

// fileFlags are the flags of the coordinator's verbs whose value is a file or a
// directory: the server runs in another directory, so a path sent to it is absolute.
var fileFlags = []string{"rules", "brief-file", "brief-dir", "file", "decide-record"}

// waits are the flags that make a verb wait for the sprint to move (where --watch,
// inbox --wait, lane take --wait: its asks are each sent as a plain take). The server moves the sprint on the one line of control a verb it runs
// holds, so such a verb is never run by the server: it runs where it is typed.
var waits = map[string]string{"where": "watch", "inbox": "wait", "lane take": "wait"}

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
func verbFlags(name string) *flag.FlagSet {
	return flagsOfVerb(verbs, name)
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
	case waits[v.name] != "" && v.given(waits[v.name]) && !slices.Contains([]string{"false", "0s"}, v.fs.Lookup(waits[v.name]).Value.String()):
		return v.name + " --" + waits[v.name] + " waits for the sprint to move, and the server moves it: it is a read, run where it is typed"
	}
	return ""
}

// server is the sprint's server this process sends the verb's reads and writes to
// (NOVA_SPRINT_SERVER), "" when the verb runs on a store here: no server named, this
// process is the server, or the verb was given its own --redis (fs, as parsed).
func (a *app) server(fs *flag.FlagSet) string {
	if a.serveAddr != "" || (verbArgs{fs: fs}).given("redis") {
		return ""
	}
	return a.getenv(ServerEnv)
}

// forwarded sends the verb to the sprint's server when there is one and the server runs
// the verb; sent is false when the verb runs here.
func (a *app) forwarded(args []string, stdout, stderr io.Writer) (code int, sent bool) {
	v := readVerb(args)
	addr := a.server(v.fs)
	if addr == "" || v.unserved() != "" || v.help || v.err != nil {
		return 0, false // no server, not served, a wait, its help, or flags it refuses: runs here
	}
	rest := args[v.words:]
	var brief []string
	if v.name == "add" { // add's checks and its brief decisions run here first (briefdecide.go)
		extra, lines, code := a.gateForward(v, args, stdout, stderr)
		if code != 0 {
			return code, true
		}
		rest, brief = append(append([]string(nil), rest...), extra...), lines
	}
	res, err := a.ask(context.Background(), addr, args[:v.words], rest)
	if err != nil {
		return a.unanswered(v.name, addr, err, stderr), true
	}
	a.answer(withBrief(res, brief), stdout, stderr)
	return res.Code, true
}

// ask sends the server one verb (its words, then what follows them) as this caller:
// who acts is this caller's alone, said even when it is no one (the server never acts
// as its own environment names), before the caller's own words so a --actor it gave
// wins; each file it names is absolute.
func (a *app) ask(ctx context.Context, addr string, verb, rest []string) (sprintwire.Result, error) {
	send := a.forward
	if send == nil {
		send = func(ctx context.Context, addr string, verbs ...[]string) ([]sprintwire.Result, error) {
			return sprintwire.Client{
				Addr:     addr,
				Build:    buildinfo.Version(version),
				VerbHash: verbTableHash(),
			}.Do(ctx, verbs...)
		}
	}
	argv := absolutePaths(slices.Concat(verb, []string{"--actor", a.getenv("NOVA_SPRINT_ACTOR")}, rest))
	res, err := send(ctx, addr, argv)
	if err != nil {
		return sprintwire.Result{}, err
	}
	return res[0], nil
}

// answer prints what the server's run of a verb printed, as it printed it.
func (a *app) answer(res sprintwire.Result, stdout, stderr io.Writer) {
	_, _ = io.WriteString(stdout, res.Stdout) // ignored: the caller's own streams
	_, _ = io.WriteString(stderr, res.Stderr)
}

// unanswered says the server did not answer the verb, with what to do, and is its exit
// code: the verb is not run here behind the server's back.
func (a *app) unanswered(verb, addr string, err error, stderr io.Writer) int {
	fmt.Fprintf(stderr, "%s %s: %s (NOVA_SPRINT_SERVER=%s); nothing is known of what ran: once it answers, read the sprint (nova-sprint where, log) before running it again; the server is the run loop: run: nova-sprint run --listen <host:port>\n", prog, verb, oneline.Escape(err.Error()), oneline.Field(addr))
	return 2
}

// without is the words with the named flags' words taken out, as fs parses them.
func without(fs *flag.FlagSet, words []string, names ...string) []string {
	drop := make([]bool, len(words))
	// ignored: the words the verb has parsed, parsed the same again
	_, _ = parseEach(fs, words, func(name string, at, n int) {
		for i := at; i < at+n && slices.Contains(names, name); i++ {
			drop[i] = true
		}
	})
	var out []string
	for i, w := range words {
		if !drop[i] {
			out = append(out, w)
		}
	}
	return out
}

// absolutePaths is the arguments with each file flag's value made absolute from this
// directory (`--flag value` and `--flag=value`). Which word is a flag and which is a
// flag's value is the verb's own parse's to say (parseEach): a value that looks like a
// flag is a value, a boolean flag takes no word, and nothing after -- is a flag.
// Arguments whose flags do not parse are left as they are, for the verb to refuse; so
// is a value that cannot be made absolute.
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
	words := argv[v.words:]
	// ignored: the words parsed above (readVerb), and parse the same again
	_, _ = parseEach(verbFlags(v.name), words, func(name string, at, n int) {
		w := words[at]
		_, value, inline := strings.Cut(w, "=")
		switch {
		case !slices.Contains(fileFlags, name):
		case inline:
			words[at] = w[:len(w)-len(value)] + abs(value)
		case n == 2:
			words[at+1] = abs(words[at+1])
		}
	})
	return argv
}

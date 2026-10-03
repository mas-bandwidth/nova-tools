package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
)

// The sprint's server (the owner, 2026-10-01: "single threaded server,
// pipelined batches like redis." / "I think we should not use redis as the
// transport, but have a client/server" / "so we have our own redis-like thing
// that the distributed things talk to."). The run loop is the one writer of the
// sprint; with --listen it also takes the workers' verbs. A worker (a member, a
// reader) sends a batch: its verbs, each the argument list it would give
// nova-sprint, in order. serve runs each through the verb's own code, in this
// process, beside the store, one at a time and never during a tick, and answers
// with each verb's exit code and what it printed. A worker's machine reads and
// writes nothing of the store itself, so no verb of it can lose to another
// writer, and a verb costs one exchange from anywhere.
//
// The server is a state machine with one step, serve: a batch in, its results
// out, the sprint moved. The listener is a shell around it (ServeHTTP), and a
// test steps it with batches and ticks in one process, with no connection.
//
// The fleet pass that showed the need (2026-10-01 17:00 ET): from 108 ms away
// a worker's write lost the fence for about 50 s and gave up, and a finished
// card took a median 391 s to be reported, against 12 to 21 s beside the store.

// workerVerb is the worker a verb of a batch acts as and how many words its
// verb is, or why the server does not run it. A worker sends its own verbs only:
// take, finish, read or queue, then --as and its name; or fleet beat, its name,
// --load and a number, and nothing more; or friend beat and its name alone. The name is one worker, never a list.
// No later word, wherever it stands, is a flag named as, redis or actor: the
// server gives the store and the actor (serve puts them before the worker's
// words, where nothing the worker sent can take them as a value or end the
// flags ahead of them). The check is on the words as sent, not on how the verb
// would parse them: a word that only looks like one of those flags (a report's
// text) is refused too, which is the safe side. The epoch a take, a finish and
// a read must name is checked where the verb has parsed it (runStep, a.serving):
// what counts is the epoch the verb runs at, not a word that looks like one. A beat's load is the worker's own measure: the server cannot measure
// another machine, and would record its own. A queue's --packets and --have (the packets
// the worker wants) are held to their shapes here, before the verb runs: a count, and card
// ids (packetsWanted).
func workerVerb(argv []string) (as string, words int, why string) {
	if len(argv) >= 2 && argv[0] == "fleet" && argv[1] == "beat" {
		rest := argv[2:]
		if len(rest) != 3 || rest[1] != "--load" || !sprint.ValidID(rest[0]) {
			return "", 0, "a beat sent to the server is `fleet beat <member> --load <percent>` and nothing more: the server cannot measure the worker's machine"
		}
		if _, err := strconv.ParseFloat(rest[2], 64); err != nil {
			return "", 0, "a beat's --load is a number, found " + rest[2]
		}
		return rest[0], 2, ""
	}
	if len(argv) >= 2 && argv[0] == "friend" && argv[1] == "beat" {
		if len(argv) != 3 || !sprint.ValidID(argv[2]) {
			return "", 0, "a friend's beat sent to the server is `friend beat <friend>` and nothing more"
		}
		return argv[2], 2, ""
	}
	if len(argv) == 0 || !slices.Contains([]string{"take", "finish", "read", "queue"}, argv[0]) {
		return "", 0, "the server runs the workers' verbs only: take, finish, read, queue, fleet beat, friend beat"
	}
	verb, rest := argv[0], argv[1:]
	if len(rest) < 2 || rest[0] != "--as" {
		return "", 0, "it does not begin `" + verb + " --as <worker>`: a worker's verb names its worker first"
	}
	as, rest = rest[1], rest[2:]
	if !sprint.ValidID(as) {
		return "", 0, "a worker's verb names one worker (letters, digits, _ and -), found " + as
	}
	for _, w := range rest {
		if !strings.HasPrefix(w, "-") {
			continue
		}
		switch name, _, _ := strings.Cut(strings.TrimLeft(w, "-"), "="); name {
		case "as", "redis", "actor":
			return "", 0, "--" + name + " is not a worker's to give the server"
		}
	}
	if verb == "queue" {
		packets, okP := flagWord(rest, "packets")
		have, okH := flagWord(rest, "have")
		if !okP || !okH {
			return "", 0, "a queue's --packets and --have are each given at most once, each with its value"
		}
		if _, err := packetsWanted(packets, have); err != nil {
			return "", 0, err.Error()
		}
	}
	return as, 1, ""
}

// flagWord is the value the words give the flag name (--name v, --name=v, one dash or
// two), "" when they give none; ok is false when they give it more than once or with no
// value.
func flagWord(words []string, name string) (value string, ok bool) {
	seen := false
	for i := 0; i < len(words); i++ {
		w := words[i]
		if !strings.HasPrefix(w, "-") {
			continue
		}
		n, v, eq := strings.Cut(strings.TrimLeft(w, "-"), "=")
		if n != name {
			continue
		}
		if seen {
			return "", false
		}
		seen = true
		if !eq {
			if i+1 >= len(words) {
				return "", false
			}
			i++
			v = words[i]
		}
		value = v
	}
	return value, true
}

// notServed are the verbs the server runs for nobody: itself (run, tick), the ones that
// work for seconds or minutes outside the store (land's git, the driver), fleet sync and
// friend sync, which read the config store with their caller's own credentials, friend
// clean, which works on the directories of the machine it runs on, and dashboard, which
// serves a page until it is interrupted and reads through the server.
var notServed = []string{"run", "tick", "land", "play", "fleet sync", "friend sync", "friend clean", "dashboard", "answer"}

// serveFrom is the server's one step: the batch's verbs run in order, each through
// the verb's own code with its worker as the actor, and each answered. The
// server's own words (the store, the actor) go between the verb and what the
// worker sent. A verb the server does not run (workerVerb) is answered as a
// usage refusal, exit 2, and the batch goes on: every verb has its own answer.
// Local batches run any verb the server runs (verbArgs.unserved), while fleet
// batches run a worker's verbs only.
// One batch, and one tick, at a time (a.serial): the lock is taken here, after
// the request is read whole, and released before any answer is written, so a
// slow worker never holds the tick.
func (a *app) serveFrom(req sprintwire.Request, local bool) sprintwire.Response {
	a.serial.Lock()
	defer a.serial.Unlock()
	defer func() { a.serving = false }()
	out := sprintwire.Response{Results: make([]sprintwire.Result, len(req.Verbs))}
	for i, argv := range req.Verbs {
		var args []string
		as, words, why := workerVerb(argv)
		switch {
		case why == "":
			// a worker's write names the epoch its worker holds, whoever sent it (runStep)
			a.serving = true
			args = slices.Concat(argv[:words], []string{"--redis", a.serveAddr, "--actor", as}, argv[words:])
		case local:
			v := readVerb(argv)
			if why = v.unserved(); why == "" {
				// a worker's verb is held to the epoch its worker holds whatever its words
				// (runStep); who acts is the caller's --actor, and no one when it gave none,
				// never whoever the server's own environment names
				a.serving = verbClasses[v.name] == classWorker
				args = slices.Concat(argv[:v.words], []string{"--redis", a.serveAddr, "--actor", ""}, argv[v.words:])
			}
		}
		if why != "" {
			verb := ""
			if len(argv) > 0 {
				verb = argv[0]
			}
			out.Results[i] = sprintwire.Result{Code: 2, Stderr: fmt.Sprintf("%s server: %s: %s; nothing was changed\n", prog, oneline.Escape(verb), oneline.Escape(why))}
			continue
		}
		var stdout, stderr bytes.Buffer
		code := a.run(args, &stdout, &stderr)
		out.Results[i] = sprintwire.Result{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
	}
	return out
}

// ServeHTTP is the listener's shell around serve: one endpoint, a batch in the
// body, its results in the answer.
func (a *app) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.serveHTTP(w, r, false) }

// localHandler is the shell for the loopback listener: the coordinator's verbs.
type localHandler struct{ a *app }

func (h localHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.a.serveHTTP(w, r, true) }

func (a *app) serveHTTP(w http.ResponseWriter, r *http.Request, local bool) {
	if r.URL.Path != sprintwire.Path || r.Method != http.MethodPost {
		http.Error(w, "the sprint server takes POST "+sprintwire.Path, http.StatusNotFound)
		return
	}
	var req sprintwire.Request
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, sprintwire.MaxRequest))
	if err == nil {
		err = json.Unmarshal(body, &req)
	}
	if err != nil {
		http.Error(w, "the request is not a batch of verbs: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Verbs) > sprintwire.MaxVerbs {
		http.Error(w, fmt.Sprintf("a batch is at most %d verbs, found %d", sprintwire.MaxVerbs, len(req.Verbs)), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// THE ANSWER IS COMPRESSED FOR A CLIENT THAT TAKES IT. A worker's queue carries each of
	// its cards' briefs, every pass: 46 to 93 KB an answer on a sprint of 1000 cards, and a
	// worker 300 ms away took 1.3 to 2.4 s to read one (the fleet pass of 2026-10-01
	// 20:18 ET). The briefs are text and alike, and go to a twentieth. Go's client asks
	// for gzip and reads it by itself.
	var out io.Writer = w
	if takesGzip(r.Header.Get("Accept-Encoding")) {
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		// ignored: a worker that has gone reads no answer
		defer func() { _ = gz.Close() }()
		out = gz
	}
	// ignored: a worker that has gone reads no answer; what ran is in the sprint's log
	_ = json.NewEncoder(out).Encode(a.serveFrom(req, local))
}

// takesGzip says a request's Accept-Encoding names gzip and does not refuse it (q=0).
func takesGzip(accept string) bool {
	for _, part := range strings.Split(accept, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.TrimSpace(name) != "gzip" {
			continue
		}
		q := strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(params), " ", ""), "q=")
		if f, err := strconv.ParseFloat(q, 64); params != "" && err == nil && f == 0 {
			return false
		}
		return true
	}
	return false
}

// listenRefused is why host is not an address the server binds, "" when it is.
// The decision is listenable's, so the server and the dashboard refuse the same
// addresses: a name, a public address, a link-local address and an unspecified
// address. Loopback, a private address and the tailnet stay. docs/SPEC-SPRINT.md
// section 14, The server. The caller returns this before net.Listen.
func listenRefused(host string) string {
	const refused = "--listen wants one address of this machine (its address on the fleet's private network, or 127.0.0.1): the server checks no credential, so it does not listen on every network"
	if host == "" || listenable(net.ParseIP(host)) != "" {
		return refused
	}
	return ""
}

// listen starts the server on the address for the store the run loop ticks,
// and returns once it is listening. The address is the coordinator's machine's
// on the fleet's private network, which is what keeps others out: the server
// checks no credential (the owner, 2026-10-01: "I am OK with relying on tailnet
// as secure"), so an address every network can reach is refused
// (docs/SPEC-SPRINT.md section 14, The server). The refusal is returned before
// a socket is opened. The coordinator's verbs stay on loopback; a private or
// tailnet address is the workers' listener beside that loopback listener.
func (a *app) listen(addr, store string, stdout io.Writer) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("--listen wants host:port, found %s", addr)
	}
	if why := listenRefused(host); why != "" {
		return errors.New(why)
	}
	// the fleet's listener takes the workers' verbs; the loopback one, on the same port,
	// takes the coordinator's (any verb the server runs). An address that is loopback
	// itself is the one listener, the coordinator's
	_, port, _ := net.SplitHostPort(addr)
	loop := net.JoinHostPort("127.0.0.1", port)
	lns := map[string]http.Handler{loop: localHandler{a}}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		lns[addr] = a
	}
	a.serveAddr = store
	for at, h := range lns {
		ln, err := net.Listen("tcp", at)
		if err != nil {
			return fmt.Errorf("--listen %s: %w", at, err)
		}
		srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: time.Minute, WriteTimeout: 5 * time.Minute}
		go func() {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				fmt.Fprintf(stdout, "SERVER STOPPED %s: %s; its verbs are not taken until run is started again\n", at, oneline.Escape(err.Error()))
			}
		}()
	}
	if _, fleet := lns[addr]; fleet {
		fmt.Fprintf(stdout, "SERVER listening on %s: the workers' verbs (take, finish, read, queue, fleet beat) run here, one at a time, beside the store\n", addr)
	}
	fmt.Fprintf(stdout, "SERVER listening on %s: the coordinator's verbs, from this machine (NOVA_SPRINT_SERVER=%s)\n", loop, loop)
	return nil
}

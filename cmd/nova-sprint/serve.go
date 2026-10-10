package main

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
)

// The sprint's server is single threaded and takes pipelined batches like
// redis, but it is its own client/server, not redis as the transport: a
// redis-like thing the distributed things talk to.
// The run loop is the one writer of the
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
// The need is distance: a worker far from the store loses a write's fence for
// long enough to give up, and a finished card takes minutes to be reported,
// against seconds beside the store.

// workerVerb is the worker a verb of a batch acts as and how many words its
// verb is, or why the server does not run it. A worker sends its own verbs only:
// take, finish, read or queue, then --as and its name; or fleet beat, its name,
// --load and a number, and nothing more; or friend beat, its name, and its report's flags each with its value (friendBeatReport); or friend cards, its name, and --json at most. The name is one worker, never a list.
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
		if len(argv) < 3 || !sprint.ValidID(argv[2]) {
			return "", 0, "a friend's beat sent to the server is `friend beat <friend>` and its report's flags (" + friendBeatServed + ") and nothing more"
		}
		if why := friendBeatReport(argv[3:]); why != "" {
			return "", 0, why
		}
		return argv[2], 2, ""
	}
	if len(argv) >= 2 && argv[0] == "friend" && argv[1] == "cards" {
		// a friend's daemon reads the cards held on her row, each with its brief, to write
		// them into her inbox (friendcards_verb.go): her name, --json at most, nothing more
		if len(argv) < 3 || len(argv) > 4 || !sprint.ValidID(argv[2]) || (len(argv) == 4 && argv[3] != "--json") {
			return "", 0, "a friend's cards sent to the server are `friend cards <friend> [--json]` and nothing more"
		}
		return argv[2], 2, ""
	}
	if len(argv) >= 2 && argv[0] == "lane" && (argv[1] == "take" || argv[1] == "give") {
		// a lane's take or give (lane.go; docs/SPEC-SPRINT.md section 18): its kind, the
		// machine and the worker, and nothing more; the server never waits (--wait asks again
		// from the worker's side)
		rest := argv[2:]
		if len(rest) != 5 || !slices.Contains(sprint.LaneKinds, rest[0]) || rest[1] != "--machine" || !sprint.ValidID(rest[2]) || rest[3] != "--as" || !sprint.ValidLaneWho(rest[4]) {
			return "", 0, "a lane's verb sent to the server is `lane " + argv[1] + " <kind> --machine <m> --as <worker>` and nothing more, the kind one of " + strings.Join(sprint.LaneKinds, ", ")
		}
		return rest[4], 2, ""
	}
	if len(argv) == 0 || !slices.Contains([]string{"take", "finish", "progress", "read", "queue"}, argv[0]) {
		return "", 0, "the server runs the workers' verbs only: take, finish, progress, read, queue, fleet beat, friend beat, friend cards, lane take, lane give"
	}
	verb, rest := argv[0], argv[1:]
	if len(rest) < 2 || rest[0] != "--as" {
		return "", 0, "it does not begin `" + verb + " --as <worker>`: a worker's verb names its worker first"
	}
	as, rest = rest[1], rest[2:]
	// a friend's row is a worker (friend.<name>: her session's take, progress and finish)
	if name, ok := sprint.FriendOfRow(as); !sprint.ValidID(as) && (!ok || !sprint.ValidID(name)) {
		return "", 0, "a worker's verb names one worker (letters, digits, _ and -, or friend.<name>), found " + as
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
// clean and friend reconcile, which work on the directories of the machine they run on, and dashboard, which
// serves a page until it is interrupted and reads through the server, and seat install
// and seat uninstall, which install the push loop as a service of the machine they are
// typed on.
var notServed = []string{"run", "tick", "land", "play", "fleet sync", "friend sync", "friend reconcile", "friend clean", "dashboard", "answer", "seat install", "seat uninstall", "selftest land", "server switch"}

// serveCtx is the server's one step: the batch's verbs run in order, each through
// the verb's own code with its worker as the actor, and each answered. The
// server's own words (the store, the actor) go between the verb and what the
// worker sent. A verb the server does not run (workerVerb) is answered as a
// usage refusal, exit 2, and the batch goes on: every verb has its own answer.
// Local batches run any verb the server runs (verbArgs.unserved), while fleet
// batches run a worker's verbs only. It is for a caller that can go away (ctx, the request's). A friend's
// beat runs on the beat lane and a read on the read lane, neither on the line
// (servelanes.go); every other verb runs on the line (a.serial), one batch's verbs and
// one tick at a time: the line is taken at the batch's first such verb, after the
// request is read whole, held to the batch's end, and released before any answer is
// written, so a slow worker never holds the tick. A batch waits for the line only while
// its caller waits for the answer: a caller gone (ctx done) before the line is taken has
// its verbs from there on not run, each answered exit 2 saying so, and nothing changed.
func (a *app) serveCtx(ctx context.Context, req sprintwire.Request, local bool) sprintwire.Response {
	out := sprintwire.Response{Results: make([]sprintwire.Result, len(req.Verbs))}
	lanes := a.lanesFor(ctx)
	begun := a.now()
	var took time.Time
	held, beats, reads, onLine, gone := false, 0, 0, 0, 0
	var first []string
	defer func() {
		var wait, hold time.Duration
		if held {
			a.serving = false
			hold = a.now().Sub(took)
			wait = took.Sub(begun)
			a.serial.Unlock()
		}
		a.tally(beats, reads, onLine, gone, wait, hold, first)
	}()
	for i, argv := range req.Verbs {
		var args []string
		serving, lane := false, ""
		as, words, why := workerVerb(argv)
		switch {
		case why == "":
			// a worker's write names the epoch its worker holds, whoever sent it (runStep)
			serving = true
			if lanes != nil && isFriendBeat(argv) {
				lane = "beat"
			}
			args = slices.Concat(argv[:words], []string{"--redis", a.serveAddr, "--actor", as}, argv[words:])
		case local:
			v := readVerb(argv)
			if why = v.unserved(); why == "" {
				// where --json is answered from the last tick's document, at once: no line,
				// no lane, no store (whereSnapshots)
				if res, ok := a.snapshotWhere(v); ok {
					reads++
					out.Results[i] = res
					continue
				}
				// a worker's verb is held to the epoch its worker holds whatever its words
				// (runStep); who acts is the caller's --actor, and no one when it gave none,
				// never whoever the server's own environment names
				serving = verbClasses[v.name] == classWorker
				if lanes != nil && v.err == nil && !v.help && onReadLane(v) {
					lane = "read"
				}
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
		switch lane {
		case "beat":
			beats++
			out.Results[i] = lanes.friendBeat(ctx, a, argv, args[words:])
			continue
		case "read":
			reads++
			out.Results[i] = lanes.readVerbRun(ctx, args)
			continue
		}
		if !held {
			if err := a.serial.LockCtx(ctx); err != nil {
				for j := i; j < len(req.Verbs); j++ {
					out.Results[j] = goneResult(req.Verbs[j])
					gone++
				}
				return out
			}
			held, took, first = true, a.now(), argv
		}
		onLine++
		a.serving = serving
		var stdout, stderr bytes.Buffer
		code := a.run(args, &stdout, &stderr)
		out.Results[i] = sprintwire.Result{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
		if code == 0 && len(argv) > 0 && argv[0] == "queue" && as != "" {
			if st, err := a.store(common{redis: a.serveAddr, actor: as}); err == nil && st != nil {
				// ignored: a best-effort lease renewal on reader beat; next beat will renew
				_, _ = st.Run(ctx, store.Step{
					Verb: "lease",
					Load: []string{sprint.Readers},
					Plan: func(s *sprint.Snapshot) sprint.Plan {
						return sprint.RenewReaderLeases(s, as)
					},
				})
			}
		}
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
	if strings.HasPrefix(r.URL.Path, viewPath) {
		a.serveView(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, friendCardsPath) {
		a.serveFriendCards(w, r)
		return
	}
	if r.URL.Path == sprintPath {
		a.serveSprint(w, r)
		return
	}
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
	// worker on a slow link takes seconds to read one.
	// The briefs are text and alike, and go to a twentieth. Go's client asks
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
	_ = json.NewEncoder(out).Encode(a.serveCtx(r.Context(), req, local))
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
// address. Loopback, a private address and the tailnet stay outside local-only
// mode; in local-only mode nothing but loopback binds, and the refusal names the
// mode, an empty host included (sprint.CheckAddr, docs/SPEC-SPRINT.md,
// sprint-local-only-mode-r-bcb.w5). The caller returns this before net.Listen.
func listenRefused(host string) string {
	const refused = "--listen wants one address of this machine (its address on the fleet's private network, or 127.0.0.1): the server checks no credential, so it does not listen on every network"
	if host == "" {
		if sprint.LocalOnlyMode() {
			return "local-only mode allows only loopback; a --listen address with no host names every network: " + refused
		}
		return refused
	}
	if why := listenable(net.ParseIP(host)); why != "" {
		if sprint.LocalOnlyMode() {
			return why
		}
		return refused
	}
	return ""
}

// listen starts the server on the address for the store the run loop ticks,
// and returns once it is listening. The address is the coordinator's machine's
// on the fleet's private network, which is what keeps others out: the server
// checks no credential, relying on the fleet's private network as the whole
// of its access control, so an address every network can reach is refused
// (docs/SPEC-SPRINT.md section 14, The server). The refusal is returned before
// a socket is opened. The coordinator's verbs stay on loopback; a private or
// tailnet address is the workers' listener beside that loopback listener.
func (a *app) listen(addr, redis string, stdout io.Writer) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("--listen wants host:port, found %s", addr)
	}
	if why := listenRefused(host); why != "" {
		return errors.New(why)
	}
	// the address rule the card states, applied to the listener too: loopback or the
	// tailnet and nothing else, so a private address outside the tailnet is refused
	// here (sprint.CheckAddr, docs/SPEC-SPRINT.md, sprint-local-only-mode-r-bcb.w5)
	if why := sprint.CheckAddr(addr, sprint.LocalOnlyMode()); why != "" {
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
	a.serveAddr, a.serveLog, a.serveStarted = redis, stdout, a.now()
	// every tick's end publishes the where --json document the server answers from
	a.publishWhere()
	// the lanes are made before the first batch, while the line is free: a batch never
	// waits for the line to make them (servelanes.go)
	a.lanesFor(context.Background())
	if st, err := a.store(common{redis: redis}); err == nil && st != nil {
		// ignored: a best-effort read cleanup on server listen; tick takes care of any subsequent lapses
		_ = a.serverStart(context.Background(), st)
	}
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

// friendBeatFlags are the flags of a friend's beat the server runs, each with the shape of
// its value: what her machinery reports of her work (friend beat), and her daemon's word
// that she is down until a time and why (--until, --reason: her harness at its limit).
var friendBeatFlags = map[string]func(string) bool{
	"--running": runningIDs,
	"--working": wholeAtLeast(0),
	"--queue":   wholeAtLeast(0),
	"--width":   wholeAtLeast(1),
	"--load": func(v string) bool {
		f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
		return err == nil && f >= 0
	},
	"--active": rfc3339,
	"--pong":   oneLineText, // any word: the server's proof step says a beat with no proof
	"--check":  sprint.ValidID,
	"--run":    sprint.ValidID,
	"--until":  rfc3339,
	"--reason": oneLineText,
}

// oneLineText is the shape of a short text on one line: not empty, no control character.
func oneLineText(v string) bool {
	if v == "" || len(v) > 1024 {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// rfc3339 is the shape of a time.
func rfc3339(v string) bool {
	_, err := time.Parse(time.RFC3339, v)
	return err == nil
}

// wholeAtLeast is the shape of a count of at least min.
func wholeAtLeast(min int) func(string) bool {
	return func(v string) bool {
		n, err := strconv.Atoi(v)
		return err == nil && n >= min
	}
}

// friendBeatServed names friendBeatFlags for a refusal.
var friendBeatServed = strings.Join(slices.Sorted(maps.Keys(friendBeatFlags)), ", ")

// friendBeatReport is why the words after a friend's beat's name are not its report's
// flags, each once with a value of its shape; "" when they are.
func friendBeatReport(words []string) string {
	seen := map[string]bool{}
	for i := 0; i < len(words); i += 2 {
		ok, known := friendBeatFlags[words[i]]
		switch {
		case !known || seen[words[i]]:
			return "a friend's beat sent to the server takes its report's flags (" + friendBeatServed + "), each once with its value, and nothing more; found " + oneline.Escape(words[i])
		case i+1 == len(words) || !ok(words[i+1]):
			return "a friend's beat's " + words[i] + " wants its value"
		}
		seen[words[i]] = true
	}
	return ""
}

// runningIDs says the value is a list of card ids or job names, comma separated: letters,
// digits, and . _ - ~ only.
func runningIDs(v string) bool {
	if v == "" || len(v) > 4096 {
		return false
	}
	for _, r := range v {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("._-~,", r) {
			return false
		}
	}
	return true
}

// viewPath is where the server serves the role views (view.go): GET /api/view/coordinator
// and GET /api/view/worker?as=<name>, each with since=<cursor>, and the coordinator's with
// all=1. They are reads, served on both listeners as the workers' queue is: the fleet's
// private network is the whole of the access control (listen).
const viewPath = "/api/view/"

// serveView runs view <role> --json for a GET, on the line of control as any verb the server
// runs (a.serial: never during a tick), and answers its JSON, gzipped for a client that takes
// it. A role, a name or a cursor of the wrong shape is a 400 and nothing is run; a name that
// is no worker of the sprint is a 404; a store that did not answer is a 503; each with the
// verb's line.
func (a *app) serveView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "the views are read with GET "+viewPath+"coordinator or "+viewPath+"worker?as=<name>", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	role := strings.TrimPrefix(r.URL.Path, viewPath)
	argv := []string{"view", role, "--redis", a.serveAddr, "--actor", "", "--json"}
	switch role {
	case "coordinator":
		if on, err := strconv.ParseBool(cmp.Or(q.Get("all"), "false")); err != nil {
			http.Error(w, "all is 1 or 0: "+oneline.Err(err), http.StatusBadRequest)
			return
		} else if on {
			argv = append(argv, "--all")
		}
	case "cards":
		if q.Get("since") != "" {
			http.Error(w, "the cards view takes no since", http.StatusBadRequest)
			return
		}
		for _, k := range []string{"col", "stream", "holder", "by"} {
			if v := q.Get(k); v != "" {
				if !sprint.ValidID(v) {
					http.Error(w, k+" is a name (letters, digits, _ and -)", http.StatusBadRequest)
					return
				}
				argv = append(argv, "--"+k, v)
			}
		}
	case "worker":
		as := q.Get("as")
		if !sprint.ValidID(as) {
			http.Error(w, "as=<name> names one fleet member or friend (letters, digits, _ and -)", http.StatusBadRequest)
			return
		}
		argv = append(argv, "--as", as)
	default:
		http.Error(w, "the views are "+viewPath+"coordinator, "+viewPath+"cards and "+viewPath+"worker?as=<name>", http.StatusNotFound)
		return
	}
	if since := q.Get("since"); since != "" {
		if _, err := parseCursor(since); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		argv = append(argv, "--since", since)
	}
	var stdout, stderr bytes.Buffer
	a.serial.Lock()
	code := a.run(argv, &stdout, &stderr)
	a.serial.Unlock()
	switch code {
	case 0:
	case 1:
		http.Error(w, strings.TrimSpace(stderr.String()), http.StatusNotFound)
		return
	default:
		http.Error(w, strings.TrimSpace(stderr.String()), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	var out io.Writer = w
	if takesGzip(r.Header.Get("Accept-Encoding")) {
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		defer func() { _ = gz.Close() }() // ignored: a reader that has gone reads no answer
		out = gz
	}
	_, _ = out.Write(stdout.Bytes()) // ignored: a reader that has gone reads no answer
}

// THE TICK'S SNAPSHOT. The owner, 2026-10-07 6:55 PM ET: "It's a requirement that it
// updates at 1s." Measured that evening, `where --json` through the server took 4.6 to
// 8.5 s wall (89 to 112 store trips, 192 to 560 rows) while the tick had the same display
// ready in 29 ms, and the dashboard, which runs the verb back to back, showed a frame every
// 5 to 8 s. So the server keeps the where --json document of its last tick in memory,
// rebuilt at the end of every tick from the rows the tick already read (its twin: the same
// four tables every step of this process reads through, the one twin the store shares),
// never read from the store a second time (`--cards --rows --archived`, the dashboard's,
// the fullest). Every where --json sent to it is answered from that document at once, with
// no line, no lane and no store trip: a narrower one (no --cards, no --rows, no --archived) is the
// same document with those parts left out, as a read of the store leaves them out. The
// answer carries "snapshot": {"at": <when the tick ended>, "age_ms": <its age>}. A document
// older than two ticks (two of the last tick's periods, at least store.TickEvery each), none
// yet, or a server whose binary was replaced under it (a switch under way: it stops at its
// next tick) is refused, `snapshot stale: <age>; run where --json --fresh`, never served
// silently. where --json --fresh, --stale, --at-epoch, and where without --json read the
// store as before. GET /api/sprint answers the full document under the same rule, so a page
// that needs only the document never spawns a process. docs/SPEC-SPRINT.md section 14, The
// server, "The tick's snapshot".

// sprintPath is where the server answers its last tick's where --json document (GET).
const sprintPath = "/api/sprint"

// whereSnaps holds each serving app's snapshots (publishWhere), by its *app.
var whereSnaps sync.Map

// whereSnapshots is the where --json document of the server's last tick, and the ticks
// that make it: one build at a time, a tick that ends during a build making it build once
// more after.
type whereSnapshots struct {
	mu sync.Mutex
	// stamp is the server's binary when it began to listen: another stamp is a switch under
	// way (run stops at its next tick, and its supervisor starts the new binary)
	stamp string
	// last is when the last tick ended, tick its count, and period the time between the
	// last two ticks' ends, at least store.TickEvery (zero before the second)
	last   time.Time
	tick   int
	period time.Duration
	// building is a build in flight, again a tick ended during it, idle closed when it ends
	building, again bool
	idle            chan struct{}
	doc             *whereSnapshot
	// failed is why the last build failed, "" when it did not
	failed string
	log    io.Writer
	// logEpoch, logAfter and logLines are the epoch's log as the builds have read it, to
	// the line logAfter: each build reads the lines after it alone (the log is only ever
	// appended to; a clear begins another epoch, read from its first line)
	logEpoch uint64
	logAfter string
	logLines []sprint.Line
}

// whereSnapshot is one tick's document: the view read with --cards --rows --archived, the
// oldest merging card's age as each read gives it (oldest: none, --cards, --rows), the
// landings series where --json carries (landedSeries, where_landed_series.go), and each
// narrower document as marshalled once.
type whereSnapshot struct {
	at     time.Time
	tick   int
	view   whereView
	oldest [3]*int
	series sprint.LandedSeries
	docs   map[snapKey][]byte
}

// snapKey is which parts of the document a where --json asks for.
type snapKey struct{ cards, rows, archived bool }

// snapshots is the server's snapshots, nil when it keeps none (no listen).
func (a *app) snapshots() *whereSnapshots {
	if s, ok := whereSnaps.Load(a); ok {
		return s.(*whereSnapshots)
	}
	return nil
}

// publishWhere makes the server keep its last tick's where --json document: each tick's end
// (a.ticked, told by the run loop once the tick has given back the line) starts a build of it
// beside the line. Once only.
func (a *app) publishWhere() *whereSnapshots {
	s := &whereSnapshots{stamp: a.binaryStamp(), log: a.serveLog}
	if had, loaded := whereSnaps.LoadOrStore(a, s); loaded {
		return had.(*whereSnapshots)
	}
	told := a.ticked
	a.ticked = func(n int, began time.Time, why string) {
		s.ticked(a, n)
		if told != nil {
			told(n, began, why)
		}
	}
	return s
}

// ticked is a tick's end: the build starts, or builds once more after the one in flight.
func (s *whereSnapshots) ticked(a *app, n int) {
	now := a.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.last.IsZero() {
		s.period = max(now.Sub(s.last), store.TickEvery)
	}
	s.last, s.tick = now, n
	if s.building {
		s.again = true
		return
	}
	s.building, s.idle = true, make(chan struct{})
	go s.build(a)
}

// build reads the document after the last tick that ended, until no tick has ended since
// its read began; a read that failed leaves the last document, which goes stale, and says
// why once on the server's log.
func (s *whereSnapshots) build(a *app) {
	for {
		s.mu.Lock()
		at, n := s.last, s.tick
		s.mu.Unlock()
		doc, err := a.readWhereSnapshot(context.Background(), s, at, n)
		s.mu.Lock()
		if err == nil {
			s.doc, s.failed = doc, ""
		} else if why := oneline.Err(err); why != s.failed {
			s.failed = why
			if s.log != nil {
				fmt.Fprintf(s.log, "%s SNAPSHOT the where --json document of tick %d was not read: %s; where --json through the server is refused once the last document is two ticks old; run: nova-sprint where --json --fresh\n", a.now().Format("15:04:05"), n, why)
			}
		}
		if !s.again {
			s.building = false
			close(s.idle)
			s.mu.Unlock()
			return
		}
		s.again = false
		s.mu.Unlock()
	}
}

// settled is closed once no build is in flight.
func (s *whereSnapshots) settled() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.building {
		done := make(chan struct{})
		close(done)
		return done
	}
	return s.idle
}

// readWhereSnapshot builds the where --json document of the tick that ended at at, as
// `where --json --cards --rows --archived` reads it, but from the rows the tick already read:
// the store it opens is the server's own (a.store), which shares the process's one twin
// (main.go, readTwins), so the four tables come from the twin, not read again from the
// store. The cards (--cards) and the rows (--rows) are built from that snapshot; the rest
// (the tables' shapes, the where record, the stream clocks, the friends, the routes) is
// read beside them, each in one exchange, as where --json reads it, so the document is byte
// for byte the fresh read of the same rows. On a twin file, which has no lanes, the read
// takes the line, as every verb does there. The landings series is the epoch's log's, read
// from the line the last build read to (s's log, which only s's one build at a time
// touches).
func (a *app) readWhereSnapshot(ctx context.Context, s *whereSnapshots, at time.Time, tick int) (*whereSnapshot, error) {
	_, c := a.verbSetup("where")
	c.redis, c.actor, c.json = a.serveAddr, "", true
	if a.lanesFor(ctx) == nil {
		if err := a.serial.LockCtx(ctx); err != nil {
			return nil, err
		}
		defer a.serial.Unlock()
	}
	st, err := a.storeAtCtx(ctx, *c, -1)
	if err != nil {
		return nil, err
	}
	// the four tables the tick read, from the twin, with no second read of their cards; a
	// twin held by another step (the next tick, a verb on the line) reads the store instead
	// (store.fencedStep), as the fallback.
	var snap *sprint.Snapshot
	if _, err := st.Run(ctx, store.Step{Verb: "where snapshot", Load: store.All,
		Plan: func(s *sprint.Snapshot) sprint.Plan { snap = s; return sprint.Plan{} }}); err != nil {
		return nil, err
	}
	v, _, err := a.whereOf(ctx, st, defaultStale, false, true)
	if err != nil {
		return nil, err
	}
	doc := &whereSnapshot{at: at, tick: tick, docs: map[snapKey][]byte{}}
	doc.oldest[0] = v.MergeRow.OldestMergingMin
	d := dealtOf(snap)
	v.Cards, v.Judgments = dealtView(d, st.Names.Prefix, v.Epoch)
	v.Merging = mergingView(d.Merging)
	v.MergeRow.OldestMergingMin = oldestMerging(v.At, d.Merging)
	doc.oldest[1] = v.MergeRow.OldestMergingMin
	if v.Holds, err = st.Holds(ctx); err != nil {
		return nil, err
	}
	if v.Lanes, err = st.LaneRows(ctx); err != nil {
		return nil, err
	}
	v.Rows = rowsView(snap, nil)
	v.MergeRow.OldestMergingMin = oldestMerging(v.At, snap.Work.Column(string(sprint.Merging)))
	doc.oldest[2] = v.MergeRow.OldestMergingMin
	doc.view = v
	if s.logEpoch != v.Epoch || s.logLines == nil {
		s.logEpoch, s.logAfter, s.logLines = v.Epoch, "", []sprint.Line{}
	}
	for {
		lines, ids, err := st.B.LogSince(ctx, s.logAfter, snapshotLogPage)
		if err != nil {
			return nil, err
		}
		s.logLines = append(s.logLines, lines...)
		if len(ids) > 0 {
			s.logAfter = ids[len(ids)-1]
		}
		if len(ids) < snapshotLogPage {
			break
		}
	}
	// the series at the document's time, as where --json's frame is given it
	doc.series = sprint.LandedSeriesOf(s.logLines, v.At)
	return doc, nil
}

// dealtOf is the dealt cards the where --json --cards view reads, from the snapshot the
// tick read (no store read): the fleet's ready and working cards, the work table's cards
// and the open judgments naming one of their primaries, as store.Dealt reads them
// (store/reads.go). The work cards are the table's placed rows, the read a cell read of
// the work shape's rows makes.
func dealtOf(snap *sprint.Snapshot) store.Dealt {
	var d store.Dealt
	if snap.Work != nil {
		d.Work = sprint.NewTable(sprint.Work)
		d.Work.SetProps(snap.Work.Props())
		d.Merging = placedCards(snap.Work)
	}
	if snap.Fleet != nil {
		d.Cards = snap.Fleet.Column(sprint.Ready, sprint.Working)
	}
	if len(d.Cards) == 0 {
		return d
	}
	primaries := map[string]bool{}
	for _, c := range d.Cards {
		primaries[c.F(sprint.PrimaryField)] = true
	}
	for _, o := range snap.Open {
		if primaries[o.Subject()] || slices.ContainsFunc(o.Note.Primaries, func(p string) bool { return primaries[p] }) {
			d.Open = append(d.Open, o)
		}
	}
	return d
}

// placedCards is a table's cards that have a place: the rows a read of the table brings
// back, never the kept records a step's extras named.
func placedCards(t *sprint.Table) []*sprint.Card {
	var out []*sprint.Card
	for _, c := range t.Cards() {
		if c.Placed() {
			out = append(out, c)
		}
	}
	return out
}

// snapshotLogPage is how many lines of the log a build reads at a time.
const snapshotLogPage = 5000

// snapshotFlags are the flags a where --json answered from the snapshot may give: the
// parts it asks for, and the flags a JSON view does not read. Any other (--fresh, --stale,
// --at-epoch, --epoch, ...) reads the store as before.
var snapshotFlags = map[string]bool{"json": true, "cards": true, "rows": true, "archived": true, "all": true, "release": true, "actor": true, "max": true, "fresh": true}

// snapshotWhere answers a where --json sent to the server from its last tick's document; ok
// false when it keeps none or the verb asks what the document does not hold (--fresh, a
// flag not in snapshotFlags), and the verb then runs as before.
func (a *app) snapshotWhere(v verbArgs) (res sprintwire.Result, ok bool) {
	s := a.snapshots()
	if s == nil || v.name != "where" || v.err != nil || v.help || !v.on("json") || v.on("fresh") {
		return res, false
	}
	ok = true
	v.fs.Visit(func(f *flag.Flag) { ok = ok && snapshotFlags[f.Name] })
	if !ok {
		return res, false
	}
	doc, why := s.answer(a.now(), a.binaryStamp(), snapKey{cards: v.on("cards"), rows: v.on("rows"), archived: v.on("archived")})
	if why != "" {
		return sprintwire.Result{Code: 1, Stderr: prog + " where: " + why + "\n"}, true
	}
	return sprintwire.Result{Stdout: string(doc)}, true
}

// answer is the document with the parts k asks for and its snapshot field, at now, by the
// binary stamp now has; or why it is refused: none yet, older than two ticks, or the binary
// replaced (a switch under way).
func (s *whereSnapshots) answer(now time.Time, stamp string, k snapKey) ([]byte, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	const fresh = "; run where --json --fresh"
	failed := ""
	if s.failed != "" {
		failed = " (its last read failed: " + s.failed + ")"
	}
	if s.doc == nil {
		return nil, "snapshot stale: none yet, no tick has ended since the server began" + failed + fresh
	}
	age := now.Sub(s.doc.at)
	if s.stamp != "" && stamp != s.stamp {
		return nil, "snapshot stale: " + age.Round(time.Millisecond).String() + ", the server's binary was replaced (a switch under way)" + fresh
	}
	if age > 2*max(s.period, store.TickEvery) {
		return nil, "snapshot stale: " + age.Round(time.Millisecond).String() + failed + fresh
	}
	b := s.doc.part(k)
	meta, _ := json.Marshal(snapshotMeta{At: s.doc.at, AgeMS: age.Milliseconds()}) // two fields that always marshal
	out := make([]byte, 0, len(b)+len(meta)+16)
	out = append(out, b[:len(b)-1]...)
	out = append(append(append(out, `,"snapshot":`...), meta...), "}\n"...)
	return out, ""
}

// part is the document as a read with k's parts gives it, marshalled once: without
// --archived the archived streams' rows of the work and merge tables and their primaries'
// rows are left out, without --cards the dealt cards, the judgments on them, the merge
// queue, the holds and the lanes, without --rows the rows; the oldest merging card's age is
// the one that read gives (whereRead).
func (d *whereSnapshot) part(k snapKey) []byte {
	if b, ok := d.docs[k]; ok {
		return b
	}
	v := d.view
	if !k.archived && v.Archived != nil {
		v.Tables = maps.Clone(v.Tables)
		for _, t := range []string{sprint.Work, sprint.Merge} {
			rows := maps.Clone(v.Tables[t])
			for _, stream := range v.Archived.Streams {
				delete(rows, stream)
			}
			if v.Tables[t] != nil {
				v.Tables[t] = rows
			}
		}
		v.Rows = slices.DeleteFunc(slices.Clone(v.Rows), func(r primaryRow) bool { return v.Archived.has(r.Stream) })
	}
	if !k.cards {
		v.Cards, v.Judgments, v.Merging, v.Holds, v.Lanes = nil, nil, nil, nil, nil
	}
	if !k.rows {
		v.Rows = nil
	}
	v.MergeRow.OldestMergingMin = d.oldest[0]
	if k.cards {
		v.MergeRow.OldestMergingMin = d.oldest[1]
	}
	if k.rows {
		v.MergeRow.OldestMergingMin = d.oldest[2]
	}
	// as where --json prints it: the view marshalled (whereLoop), then given its landings
	// series (whereJSONWriter.enrich)
	b, _ := json.Marshal(v) // a view always marshals, as where --json's does
	var raw map[string]any
	if json.Unmarshal(b, &raw) == nil {
		raw["landedSeries"] = d.series
		if e, err := json.Marshal(raw); err == nil {
			b = e
		}
	}
	d.docs[k] = b
	return b
}

// serveSprint answers GET /api/sprint: the last tick's full where --json document (--cards
// --rows --archived) with its snapshot field, gzipped for a client that takes it; a refused
// snapshot is a 503 with the refusal's line.
func (a *app) serveSprint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "the sprint's document is read with GET "+sprintPath, http.StatusMethodNotAllowed)
		return
	}
	s := a.snapshots()
	if s == nil {
		http.Error(w, "snapshot stale: this server keeps none (it was not started by run --listen); run where --json --fresh", http.StatusServiceUnavailable)
		return
	}
	doc, why := s.answer(a.now(), a.binaryStamp(), snapKey{cards: true, rows: true, archived: true})
	if why != "" {
		http.Error(w, why, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	var out io.Writer = w
	if takesGzip(r.Header.Get("Accept-Encoding")) {
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		defer func() { _ = gz.Close() }() // ignored: a reader that has gone reads no answer
		out = gz
	}
	_, _ = out.Write(doc) // ignored: a reader that has gone reads no answer
}

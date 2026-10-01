package main

import (
	"bytes"
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
// --load and a number, and nothing more. The name is one worker, never a list.
// No later word, wherever it stands, is a flag named as, redis or actor: the
// server gives the store and the actor (serve puts them before the worker's
// words, where nothing the worker sent can take them as a value or end the
// flags ahead of them). A take, a finish and a read name the epoch their worker
// holds, so none runs in a sprint its worker has not read. The check is on the
// words as sent, not on how the verb would parse them: a word that only looks
// like one of those flags (a report's text) is refused too, which is the safe
// side. A beat's load is the worker's own measure: the server cannot measure
// another machine, and would record its own.
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
	if len(argv) == 0 || !slices.Contains([]string{"take", "finish", "read", "queue"}, argv[0]) {
		return "", 0, "the server runs the workers' verbs only: take, finish, read, queue, fleet beat"
	}
	verb, rest := argv[0], argv[1:]
	if len(rest) < 2 || rest[0] != "--as" {
		return "", 0, "it does not begin `" + verb + " --as <worker>`: a worker's verb names its worker first"
	}
	as, rest = rest[1], rest[2:]
	if !sprint.ValidID(as) {
		return "", 0, "a worker's verb names one worker (letters, digits, _ and -), found " + as
	}
	epoch := false
	for i, w := range rest {
		if !strings.HasPrefix(w, "-") {
			continue
		}
		name, value, has := strings.Cut(strings.TrimLeft(w, "-"), "=")
		switch name {
		case "as", "redis", "actor":
			return "", 0, "--" + name + " is not a worker's to give the server"
		case "epoch":
			if !has && i+1 < len(rest) {
				value = rest[i+1]
			}
			if _, err := strconv.ParseUint(value, 10, 64); err == nil {
				epoch = true
			}
		}
	}
	if verb != "queue" && !epoch {
		return "", 0, "a " + verb + " sent to the server names the epoch its worker holds, --epoch <n> (queue --as " + as + " prints it)"
	}
	return as, 1, ""
}

// serve is the server's one step: the batch's verbs run in order, each through
// the verb's own code with its worker as the actor, and each answered. The
// server's own words (the store, the actor) go between the verb and what the
// worker sent. A verb the server does not run (workerVerb) is answered as a
// usage refusal, exit 2, and the batch goes on: every verb has its own answer.
// One batch, and one tick, at a time (a.serial): the lock is taken here, after
// the request is read whole, and released before any answer is written, so a
// slow worker never holds the tick.
func (a *app) serve(req sprintwire.Request) sprintwire.Response {
	a.serial.Lock()
	defer a.serial.Unlock()
	out := sprintwire.Response{Results: make([]sprintwire.Result, len(req.Verbs))}
	for i, argv := range req.Verbs {
		as, words, why := workerVerb(argv)
		if why != "" {
			verb := ""
			if len(argv) > 0 {
				verb = argv[0]
			}
			out.Results[i] = sprintwire.Result{Code: 2, Stderr: fmt.Sprintf("%s server: %s: %s; nothing was changed\n", prog, oneline.Escape(verb), oneline.Escape(why))}
			continue
		}
		args := slices.Concat(argv[:words], []string{"--redis", a.serveAddr, "--actor", as}, argv[words:])
		var stdout, stderr bytes.Buffer
		code := a.run(args, &stdout, &stderr)
		out.Results[i] = sprintwire.Result{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
	}
	return out
}

// ServeHTTP is the listener's shell around serve: one endpoint, a batch in the
// body, its results in the answer.
func (a *app) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	// ignored: a worker that has gone reads no answer; what ran is in the sprint's log
	_ = json.NewEncoder(w).Encode(a.serve(req))
}

// listen starts the server on the address for the store the run loop ticks,
// and returns once it is listening. The address is the coordinator's machine's
// on the fleet's private network, which is what keeps others out: the server
// checks no credential (the owner, 2026-10-01: "I am OK with relying on tailnet
// as secure"), so an address every network can reach is refused.
func (a *app) listen(addr, store string, stdout io.Writer) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("--listen wants host:port, found %s", addr)
	}
	if ip := net.ParseIP(host); host == "" || ip != nil && ip.IsUnspecified() {
		return errors.New("--listen wants one address of this machine (its address on the fleet's private network, or 127.0.0.1): the server checks no credential, so it does not listen on every network")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("--listen %s: %w", addr, err)
	}
	a.serveAddr = store
	srv := &http.Server{Handler: a, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: time.Minute, WriteTimeout: 5 * time.Minute}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(stdout, "SERVER STOPPED %s: %s; the workers' verbs are not taken until run is started again\n", addr, oneline.Escape(err.Error()))
		}
	}()
	fmt.Fprintf(stdout, "SERVER listening on %s: the workers' verbs (take, finish, read, queue, fleet beat) run here, one at a time, beside the store\n", ln.Addr())
	return nil
}

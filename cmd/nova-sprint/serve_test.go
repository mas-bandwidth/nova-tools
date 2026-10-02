package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"

	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
)

// The sprint's server is tested as the state machine it is (the owner,
// 2026-10-01: "design client and server as a state machine, and so you can
// create unit tests by mocking data and batches coming in to the server in one
// process." / "Never test with real sockets"): a rig holds one server over a
// twin store and steps it, a batch or a tick at a time, in the test's own
// process. No test here opens a socket.

// serverRig is one sprint server over a twin file.
type serverRig struct {
	t      *testing.T
	a      *app
	served [][]string // every verb sent to the server, in order
}

// newServerRig is a server whose sprint the coordinator's lines made.
func newServerRig(t *testing.T, lines ...string) *serverRig {
	t.Helper()
	file := filepath.Join(t.TempDir(), "sprint.twin")
	env := map[string]string{"NOVA_SPRINT_REDIS": "mem:" + file, "NOVA_SPRINT_ACTOR": "boss"}
	a := newApp(func(k string) string { return env[k] })
	t.Cleanup(a.close)
	a.serveAddr = "mem:" + file
	r := &serverRig{t: t, a: a}
	for _, l := range lines {
		r.boss(l)
	}
	return r
}

// boss runs a coordinator's line (or a tick) on the server's own line of
// control, as the run loop runs a tick: never during a batch.
func (r *serverRig) boss(line string) string {
	r.t.Helper()
	r.a.serial.Lock()
	defer r.a.serial.Unlock()
	var out, errb bytes.Buffer
	code := r.a.run(split(strings.TrimPrefix(line, prog+" ")), &out, &errb)
	require.Equal(r.t, 0, code, "%s\n%s%s", line, out.String(), errb.String())
	return out.String()
}

// send is a worker's delivery of a batch with no connection: the server's step.
func (r *serverRig) send(_ context.Context, verbs ...[]string) ([]sprintwire.Result, error) {
	r.served = append(r.served, verbs...)
	return r.a.serveFrom(sprintwire.Request{Verbs: verbs}, false).Results, nil
}

// one sends one verb and returns its answer.
func (r *serverRig) one(argv ...string) sprintwire.Result {
	r.t.Helper()
	res, err := r.send(context.Background(), argv)
	require.NoError(r.t, err)
	require.Len(r.t, res, 1)
	return res[0]
}

// queue is the worker's cards by column, as its queue lists them.
func (r *serverRig) queue(as string) map[string][]string {
	r.t.Helper()
	res := r.one("queue", "--as", as, "--json")
	require.Equal(r.t, 0, res.Code, res.Stderr)
	var q struct {
		Cards []struct{ ID, Col string }
	}
	require.NoError(r.t, json.Unmarshal([]byte(res.Stdout), &q), res.Stdout)
	out := map[string][]string{}
	for _, c := range q.Cards {
		out[c.Col] = append(out[c.Col], c.ID)
	}
	return out
}

// taken is the cards a take's answer handed its worker, each as <card>@<gen>.
func taken(t *testing.T, res sprintwire.Result) []string {
	t.Helper()
	require.Equal(t, 0, res.Code, res.Stderr)
	var out struct {
		Packets []struct {
			Card string
			Gen  int
		}
	}
	require.NoError(t, json.Unmarshal([]byte(res.Stdout), &out), res.Stdout)
	var ids []string
	for _, p := range out.Packets {
		ids = append(ids, p.Card+"@"+strconv.Itoa(p.Gen))
	}
	return ids
}

// twoLanes is the coordinator's lines that make a sprint of six cards dealt to
// one member of width 2: each test's own.
func twoLanes() []string {
	return []string{
		"nova-sprint init --readers reader-a,reader-b --members m1:2",
		"nova-sprint add --stream s1 --count 6",
		"nova-sprint start",
		"nova-sprint tick",
		"nova-sprint tick",
	}
}

// A worker's verbs sent to the server do what the verbs do: the queue lists the
// worker's cards, a take moves them to working and hands their packets, a
// finish takes the card off the worker. The worker's machine opened no store.
func TestTheServerRunsAWorkersVerbs(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	before := r.queue("m1")
	require.Len(t, before["ready"], 4, "dealt twice its width ahead: %v", before)

	cards := taken(t, r.one("take", "--as", "m1", "--limit", "2", "--epoch", "0", "--json"))
	require.Len(t, cards, 2)
	after := r.queue("m1")
	assert.Len(t, after["working"], 2, "%v", after)
	assert.Len(t, after["ready"], 2, "%v", after)

	fin := r.one("finish", "--as", "m1", cards[0], "--epoch", "0", "--report", "done", "--head", "0123456")
	require.Equal(t, 0, fin.Code, fin.Stderr)
	assert.Contains(t, fin.Stdout, "working -> done ok")
	assert.Len(t, r.queue("m1")["working"], 1, "the finished card is off the worker's queue")

	beat := r.one("fleet", "beat", "m1", "--load", "12.5")
	assert.Equal(t, 0, beat.Code, beat.Stderr)
}

// The server runs the workers' verbs only, each naming one worker first, and
// no word of a verb gives it a store, an actor or a second name, wherever the
// word stands: each refusal is answered exit 2, says nothing was changed, and
// changes nothing.
func TestTheServerRunsWorkersVerbsOnly(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	log := func() string { return r.boss("nova-sprint log") }
	before := log()
	for name, argv := range map[string][]string{
		"a coordinator's verb":                      {"add", "--stream", "s1", "--count", "1"},
		"clear":                                     {"clear", "--confirm", "sprint"},
		"another fleet verb":                        {"fleet", "up", "m1", "--width", "64"},
		"no verb at all":                            {},
		"no worker named":                           {"take", "--limit", "1", "--epoch", "0"},
		"the worker not named first":                {"take", "--limit", "1", "--as", "m1", "--epoch", "0"},
		"the worker given as --as=":                 {"take", "--as=m1", "--limit", "1", "--epoch", "0"},
		"the worker given as -as":                   {"take", "-as", "m1", "--limit", "1", "--epoch", "0"},
		"a list of workers":                         {"take", "--as", "m1,m2", "--limit", "1", "--epoch", "0"},
		"a flag where the name should be":           {"take", "--as", "--limit", "1", "--epoch", "0"},
		"an empty name":                             {"take", "--as", "", "--limit", "1", "--epoch", "0"},
		"a name with a space":                       {"take", "--as", "m1 m2", "--limit", "1", "--epoch", "0"},
		"a store":                                   {"take", "--as", "m1", "--redis", "mem:/tmp/other.twin", "--epoch", "0"},
		"a store, one dash":                         {"take", "--as", "m1", "-redis", "mem:/tmp/other.twin", "--epoch", "0"},
		"a store, with =":                           {"take", "--as", "m1", "--redis=mem:/tmp/other.twin", "--epoch", "0"},
		"a store, one dash, with =":                 {"take", "--as", "m1", "-redis=mem:/tmp/other.twin", "--epoch", "0"},
		"an actor":                                  {"finish", "--as", "m1", "s1-1.w1@1", "--actor", "boss", "--epoch", "0"},
		"an actor, one dash, with =":                {"finish", "--as", "m1", "s1-1.w1@1", "-actor=boss", "--epoch", "0"},
		"a second name":                             {"finish", "--as", "m1", "s1-1.w1@1", "--as", "m2", "--epoch", "0"},
		"a second name, one dash, with =":           {"finish", "--as", "m1", "s1-1.w1@1", "-as=m2", "--epoch", "0"},
		"a second name hidden as a flag's value":    {"finish", "--as", "m1", "s1-1.w1@1", "--report", "--as", "m2", "--epoch", "0"},
		"a store after the -- terminator":           {"read", "--as", "reader-a", "--begin", "--epoch", "0", "--", "--redis", "mem:/tmp/x"},
		"a take with no epoch":                      {"take", "--as", "m1", "--limit", "1"},
		"a take whose epoch a later word undid":     {"take", "--as", "m1", "--limit", "1", "--epoch", "0", "--epoch", "-1"},
		"a take whose epoch another flag swallowed": {"take", "--as", "m1", "--limit", "1", "--op", "--epoch=0"},
		"a finish with no epoch":                    {"finish", "--as", "m1", "s1-1.w1@1"},
		"a read with no epoch":                      {"read", "--as", "reader-a", "--begin"},
		"a beat with no load":                       {"fleet", "beat", "m1"},
		"a beat whose load has no value":            {"fleet", "beat", "m1", "--load"},
		"a beat whose load is not a number":         {"fleet", "beat", "m1", "--load", "high"},
		"a beat whose load another flag swallowed":  {"fleet", "beat", "m1", "--op", "--load", "5"},
		"a beat as another actor":                   {"fleet", "beat", "m1", "--load", "5", "--actor", "boss"},
		"a beat of no member":                       {"fleet", "beat", "--load", "5"},
		"a beat of a list of members":               {"fleet", "beat", "m1,m2", "--load", "5"},
	} {
		res := r.one(argv...)
		assert.Equal(t, 2, res.Code, "%s: %v", name, argv)
		assert.Contains(t, res.Stderr, "nothing was changed", "%s: %v", name, argv)
		assert.Empty(t, res.Stdout, "%s: %v", name, argv)
	}
	assert.Equal(t, before, log(), "no refused verb wrote a line of the log")
	assert.Len(t, r.queue("m1")["ready"], 4, "nor moved a card")
}

// The server's own words, the store and the actor, stand before what the worker
// sent, so nothing a worker sends can take them as a flag's value or end the
// flags ahead of them: a verb ending in the flag terminator, or in a flag that
// wants a value, still runs on the server's store as its worker, or is refused
// by the verb's own parse; it never runs as the coordinator or elsewhere.
func TestNothingAWorkerSendsDisplacesTheServersWords(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	res := r.one("take", "--as", "m1", "--limit", "1", "--epoch", "0", "--json", "--")
	cards := taken(t, res)
	require.Len(t, cards, 1, "a trailing terminator changes nothing: %s%s", res.Stdout, res.Stderr)

	// an epoch that is not a number: the verb's own parse refuses it, exit 2
	res = r.one("take", "--as", "m1", "--limit", "1", "--epoch", "now")
	assert.Equal(t, 2, res.Code, "%s%s", res.Stdout, res.Stderr)
	assert.Len(t, r.queue("m1")["working"], 1, "and nothing more was taken")

	// a report flag left with no value: the verb's own parse refuses it, exit 2
	res = r.one("finish", "--as", "m1", cards[0], "--epoch", "0", "--report")
	assert.Equal(t, 2, res.Code, "%s%s", res.Stdout, res.Stderr)
	assert.Len(t, r.queue("m1")["working"], 1, "and the card is still working")

	res = r.one("finish", "--as", "m1", cards[0], "--epoch", "0", "--report", "done", "--head", "0123456", "--")
	require.Equal(t, 0, res.Code, res.Stderr)
	log := r.boss("nova-sprint log")
	assert.Contains(t, log, "m1", "the moves are the worker's")
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, "took") || strings.Contains(line, "finished") {
			assert.NotContains(t, line, "boss", "no worker's verb ran as the coordinator: %s", line)
		}
	}
}

// A batch of several verbs is answered verb by verb, in its order: a verb the
// server refuses, or one the sprint refuses, has its own answer and the verbs
// after it still run.
func TestABatchIsAnsweredVerbByVerbInOrder(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	res, err := r.send(context.Background(),
		[]string{"take", "--as", "m1", "--limit", "1", "--epoch", "0", "--json"},
		[]string{"clear", "--confirm", "sprint"},
		[]string{"finish", "--as", "m1", "no-such-card@1", "--epoch", "0"},
		[]string{"take", "--as", "m1", "--limit", "1", "--epoch", "0", "--json"},
	)
	require.NoError(t, err)
	require.Len(t, res, 4)
	first, last := taken(t, res[0]), taken(t, res[3])
	require.Len(t, first, 1)
	require.Len(t, last, 1)
	assert.NotEqual(t, first[0], last[0], "the second take ran after the first: another card")
	assert.Equal(t, 2, res[1].Code, "the server does not run clear")
	assert.Equal(t, 1, res[2].Code, "the sprint refused the finish of a card that is not there: %s%s", res[2].Stdout, res[2].Stderr)
	assert.Len(t, r.queue("m1")["working"], 2)
}

// Batches from many workers at once, with ticks between them, run one at a
// time: every take gets its own card, none is refused as busy, and none sees
// the sprint part way through another's step. Eight workers ask at once for one
// card each of the eight lanes' worth dealt to one member.
func TestBatchesAndTicksRunOneAtATime(t *testing.T) {
	t.Parallel()
	r := newServerRig(t,
		"nova-sprint init --readers reader-a,reader-b --members m1:8",
		"nova-sprint add --stream s1 --count 16",
		"nova-sprint start",
		"nova-sprint tick",
		"nova-sprint tick",
	)
	var wg sync.WaitGroup
	results := make([]sprintwire.Result, 8)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = r.a.serveFrom(sprintwire.Request{Verbs: [][]string{{"take", "--as", "m1", "--limit", "1", "--epoch", "0", "--json"}}}, false).Results[0]
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 4 {
			r.boss("nova-sprint tick")
		}
	}()
	wg.Wait()
	seen := map[string]bool{}
	for _, res := range results {
		cards := taken(t, res)
		require.Len(t, cards, 1, "%s%s", res.Stdout, res.Stderr)
		assert.NotContains(t, res.Stderr, "busy")
		assert.False(t, seen[cards[0]], "%s was taken twice", cards[0])
		seen[cards[0]] = true
	}
	assert.Len(t, r.queue("m1")["working"], 8)
}

// pushed answers every push with the commit: the member pushes several ended
// children at once, so it keeps nothing.
type pushed string

func (p pushed) Push(member.Packet, member.Result) member.Push { return member.Push{Sha: string(p)} }

// A member whose sprint is the server runs its cards as it does against the
// store: it takes to its width, reports each ended child, and fills the freed
// lane, every verb of it sent to the server and none run on its own machine.
func TestAMemberWorksThroughTheServer(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	rn := &twinRunner{children: map[string]*twinChild{}}
	var log bytes.Buffer
	m := member.New(member.Config{As: "m1"}, &sprintwire.Worker{Send: r.send}, rn, pushed("0123456789abcdef0123456789abcdef01234567"), &log)
	pass := func() {
		t.Helper()
		_, err := m.Tick(time.Unix(0, 0))
		require.NoError(t, err, log.String())
	}
	pass()
	require.Len(t, rn.packets, 2, "at its width of 2: %s", log.String())
	for round := range 2 {
		for _, p := range rn.packets {
			c := rn.children[p.Card]
			c.mu.Lock()
			c.done, c.res = true, member.Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "0123456", Report: "did it"}
			c.mu.Unlock()
		}
		pass()
		r.boss("nova-sprint tick")
		assert.Len(t, rn.packets, 2*(round+2), "the freed lanes are filled again: %s", log.String())
		assert.LessOrEqual(t, m.Running(), 2, "never over its width")
	}
	// a member with no load sampled yet does not beat a load it did not measure
	require.ErrorContains(t, m.Beat(), "no sample yet")
	verbs := map[string]int{}
	for _, v := range r.served {
		verbs[v[0]]++
	}
	assert.Equal(t, 4, verbs["finish"], "%v\n%s", verbs, log.String())
	assert.Positive(t, verbs["take"])
	assert.Positive(t, verbs["queue"])
	assert.Zero(t, verbs["fleet"], "the beat with no load was not sent")
	seen := map[string]bool{}
	for _, p := range rn.packets {
		assert.False(t, seen[p.Card], "%s started twice", p.Card)
		seen[p.Card] = true
	}
}

// A child's words that begin like a flag do not stop its report: the worker
// joins them to their flag, and the server takes them as that flag's value.
func TestAReportThatBeginsLikeAFlagIsStillTaken(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	w := &sprintwire.Worker{Send: r.send}
	code, out := w.Run("take", "--as", "m1", "--limit", "1", "--epoch", "0", "--json")
	require.Equal(t, 0, code, string(out))
	card := taken(t, sprintwire.Result{Stdout: string(out)})[0]
	code, out = w.Run("finish", "--as", "m1", card, "--epoch", "0", "--report", "--actor=boss was the flag at fault", "--head", "0123456")
	require.Equal(t, 0, code, string(out))
	id, _, _ := strings.Cut(card, "@")
	primary, _, _ := strings.Cut(id, ".")
	assert.Contains(t, r.boss("nova-sprint card "+primary), "--actor=boss was the flag at fault", "the report is the child's words, whole")
}

// An answer lost on the way back is not a verb run twice: the worker sends the
// verb again with the operation id it gave it, and the server returns what the
// first run did. A take whose answer was lost hands the same cards, and takes
// no more.
func TestAVerbWhoseAnswerWasLostIsNotRunTwice(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	lost := 0
	w := &sprintwire.Worker{Send: func(ctx context.Context, verbs ...[]string) ([]sprintwire.Result, error) {
		res, err := r.send(ctx, verbs...)
		if lost == 0 {
			lost++
			return nil, errors.New("the answer was lost")
		}
		return res, err
	}}
	code, out := w.Run("take", "--as", "m1", "--limit", "2", "--epoch", "0", "--json")
	require.Equal(t, 0, code, string(out))
	require.Len(t, r.served, 2, "sent again after the lost answer")
	assert.Equal(t, r.served[0], r.served[1], "the same verb with the same operation id")
	assert.Contains(t, r.served[0], "--op")
	assert.Len(t, taken(t, sprintwire.Result{Stdout: string(out)}), 2, "the answer names the cards the first run took")
	q := r.queue("m1")
	assert.Len(t, q["working"], 2, "two cards taken, not four: %v", q)
	assert.Len(t, q["ready"], 2, "%v", q)
}

// A server that does not answer is, to the member, a store that did not answer:
// exit 2 after its tries, the words saying which server.
func TestAServerThatDoesNotAnswerIsExitTwo(t *testing.T) {
	t.Parallel()
	tries := 0
	w := &sprintwire.Worker{Send: func(context.Context, ...[]string) ([]sprintwire.Result, error) {
		tries++
		return nil, errors.New("the sprint server at studio:6390 did not answer: connection refused")
	}}
	code, out := w.Run("finish", "--as", "m1", "s1-1.w1@1", "--epoch", "0")
	assert.Equal(t, 2, code)
	assert.Equal(t, sprintwire.Tries, tries)
	assert.Contains(t, string(out), "did not answer")
}

// handlerTransport is a client's transport with no connection: the request is
// handed to the handler in this process.
type handlerTransport struct{ h http.Handler }

func (tr handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	tr.h.ServeHTTP(rec, req)
	return rec.Result(), nil
}

// The listener's shell carries a batch to the server's step and its results
// back, on its one endpoint; anything else is refused and runs nothing.
func TestTheShellCarriesABatchAndItsResults(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	c := sprintwire.Client{Addr: "sprint.test:6390", HTTP: &http.Client{Transport: handlerTransport{r.a}}}
	res, err := c.Do(context.Background(),
		[]string{"take", "--as", "m1", "--limit", "1", "--epoch", "0", "--json"},
		[]string{"queue", "--as", "m1", "--json"})
	require.NoError(t, err)
	require.Len(t, res, 2)
	assert.Len(t, taken(t, res[0]), 1)
	assert.Equal(t, 0, res[1].Code)

	for name, req := range map[string]*http.Request{
		"another path":   httptest.NewRequest(http.MethodPost, "/other", strings.NewReader(`{"verbs":[]}`)),
		"another method": httptest.NewRequest(http.MethodGet, sprintwire.Path, nil),
		"not a batch":    httptest.NewRequest(http.MethodPost, sprintwire.Path, strings.NewReader(`take --as m1`)),
		"too many verbs": httptest.NewRequest(http.MethodPost, sprintwire.Path, strings.NewReader(`{"verbs":[`+strings.Repeat(`["queue","--as","m1"],`, sprintwire.MaxVerbs)+`["queue","--as","m1"]]}`)),
		"too large":      httptest.NewRequest(http.MethodPost, sprintwire.Path, strings.NewReader(`{"verbs":[["`+strings.Repeat("x", sprintwire.MaxRequest)+`"]]}`)),
	} {
		rec := httptest.NewRecorder()
		r.a.ServeHTTP(rec, req)
		assert.NotEqual(t, http.StatusOK, rec.Code, name)
	}
	assert.Len(t, r.queue("m1")["working"], 1, "the refused requests ran nothing")
}

// The answer is compressed for a client that asks for it, and plain for one that does
// not: a worker's queue carries every brief of its cards, every pass, and the same answer
// read back from gzip is the plain one, at a fraction of the bytes.
func TestTheShellCompressesItsAnswerForAClientThatTakesGzip(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	body := `{"verbs":[["queue","--as","m1","--json"]]}`
	plain := httptest.NewRecorder()
	r.a.ServeHTTP(plain, httptest.NewRequest(http.MethodPost, sprintwire.Path, strings.NewReader(body)))
	require.Equal(t, http.StatusOK, plain.Code)
	assert.Empty(t, plain.Header().Get("Content-Encoding"), "a client that names no encoding is answered plain")

	req := httptest.NewRequest(http.MethodPost, sprintwire.Path, strings.NewReader(body))
	req.Header.Set("Accept-Encoding", "gzip")
	packed := httptest.NewRecorder()
	r.a.ServeHTTP(packed, req)
	require.Equal(t, http.StatusOK, packed.Code)
	require.Equal(t, "gzip", packed.Header().Get("Content-Encoding"))
	zr, err := gzip.NewReader(packed.Body)
	require.NoError(t, err)
	unpacked, err := io.ReadAll(zr)
	require.NoError(t, err)
	assert.JSONEq(t, plain.Body.String(), string(unpacked), "the same answer, compressed")
}

// The server checks no credential, so it listens on one address of its machine
// and never on every network.

func TestTheServerDoesNotListenOnEveryNetwork(t *testing.T) {
	t.Parallel()
	a := newApp(func(string) string { return "" })
	t.Cleanup(a.close)
	var out bytes.Buffer
	every4, every6 := net.JoinHostPort(net.IPv4zero.String(), "6390"), net.JoinHostPort(net.IPv6unspecified.String(), "6390")
	for _, addr := range []string{":6390", every4, every6, "6390", ""} {
		err := a.listen(addr, "mem:x", &out)
		assert.Error(t, err, addr)
	}
	assert.Empty(t, out.String(), "nothing was started")
}

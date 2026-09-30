package sprintfn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The tests of IT16's parts run on the composed twin (sprintfn.Twin over
// tset.Mem and the log stub), with the six real parts on the twin's own
// registry, X that guards nothing, and PartsBefore as the before hook, at a
// clock the test moves. No store is involved: the Lua half of each part is
// checked by sprint_parts.lua's own tests, and waits on G0 to be loaded.

// stepClock is the twin's time, which a test moves.
type stepClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *stepClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *stepClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func (c *stepClock) ms() int64 { return c.now().UnixMilli() }

// partsTwin is a twin with the six parts registered on its own registry, at
// epoch 0.
func partsTwin(t *testing.T) (*Twin, *tset.Mem, *LogStub, *stepClock) {
	t.Helper()
	return partsTwinAt(t, "0")
}

// partsTwinAt is the same at an epoch: the four tables defined, the epoch
// active before the twin reads it.
func partsTwinAt(t *testing.T, epoch tset.Decimal) (*Twin, *tset.Mem, *LogStub, *stepClock) {
	t.Helper()
	m := newTestMem(t)
	if epoch != "0" {
		if err := m.SetActiveEpoch(testPrefix, epoch); err != nil {
			t.Fatal(err)
		}
	}
	log := NewLogStub()
	tw := NewTwin(m, log, testNames)
	if tw.broken != nil {
		t.Fatal(tw.broken)
	}
	tw.parts = NewPartRegistry()
	tw.phases = passX()
	tw.phases.Before = PartsBefore
	if err := RegisterTickParts(tw.parts); err != nil {
		t.Fatal(err)
	}
	clk := &stepClock{t: testTime}
	tw.SetClock(clk.now)
	return tw, m, log, clk
}

// sk is a sprint key with no epoch, ek one of epoch 0, ekAt one of an epoch.
func sk(name string) string { return testPrefix + "sprint:" + name }
func ek(name string) string { return ekAt(name, "0") }
func ekAt(name string, epoch tset.Decimal) string {
	return testPrefix + "sprint:" + name + "@" + string(epoch)
}

// seed applies commands straight to the twin's sprint keys, as a fixture.
func seed(tw *Twin, cmds ...Cmd) { tw.keys.apply(cmds) }

// state is a State as the twin gives a part, at the clock's time.
func (tw *Twin) partState(clk *stepClock, epoch tset.Decimal) *State {
	return &State{Prefix: testPrefix, Epoch: epoch, NowMS: tset.Decimal(strconv.FormatInt(clk.ms(), 10)),
		Names: testNames, Keys: &Keys{ks: tw.keys}}
}

// refusedStep sends a request and returns the refusal, failing when it was
// applied. A request refused before dispatch (the client's static check) comes
// back as the pipeline's error.
func refusedStep(t *testing.T, c Client, req *Request) *Refusal {
	t.Helper()
	res, err := Step(context.Background(), c, req)
	var ref *Refusal
	if err != nil {
		if !errors.As(err, &ref) {
			t.Fatalf("step error %v, want a refusal", err)
		}
		return ref
	}
	if res.Err != nil {
		t.Fatalf("step error %v, want a refusal", res.Err)
	}
	if res.Refusal == nil {
		t.Fatalf("step applied: %+v", res.Step)
	}
	return res.Refusal
}

// partReply decodes the reply of one part of an applied step.
func partReply(t *testing.T, r *StepReply, name string) map[string]any {
	t.Helper()
	raw, ok := r.Parts[name]
	if !ok {
		t.Fatalf("no reply from part %s: %v", name, r.Parts)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("part %s reply %s: %v", name, raw, err)
	}
	return out
}

func leaseReq(owner, name string, hold int64, hb map[string]string) *Request {
	return &Request{Epoch: "0", Meta: Meta{Verb: "tick", Tick: true},
		Lease: &LeasePart{Owner: owner, Name: name, HoldMS: hold, Heartbeat: hb}}
}

func genReq(gen uint64) *Request { return &Request{Epoch: "0", Meta: Meta{Tick: true, Gen: gen}} }

func ingestReq(gen uint64, from, to string, keys ...sprint.AgendaKey) *Request {
	r := genReq(gen)
	r.Ingest = &IngestPart{From: tset.Decimal(from), To: tset.Decimal(to), Keys: keys}
	return r
}

func popReq(gen uint64, limit int) *Request {
	r := genReq(gen)
	r.Pop = &PopPart{Limit: limit}
	return r
}

// changedKeys are the sprint keys whose value differs between two dumps.
func changedKeys(before, after map[string]KeyValue) []string {
	var out []string
	for k, v := range after {
		if b, ok := before[k]; !ok || !reflect.DeepEqual(b, v) {
			out = append(out, k)
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// TestPartsAreRegisteredByDefault: the write path's own registry holds the six
// parts of 1.0 from this package's init, each a part that can be called, and a
// twin assembled with a registry of its own takes them with RegisterTickParts.
func TestPartsAreRegisteredByDefault(t *testing.T) {
	t.Parallel()
	for _, name := range PartOrder {
		if p, ok := defaultParts.Lookup(name); !ok || partIsNil(p) {
			t.Errorf("the write path's registry has no part %s", name)
		}
	}
	r := NewPartRegistry()
	if err := RegisterTickParts(r); err != nil {
		t.Fatal(err)
	}
	if err := RegisterTickParts(r); !errors.Is(err, ErrPartTwice) {
		t.Fatalf("a second registration: %v, want ErrPartTwice", err)
	}
}

// TestLeaseTokenPerProcess (1.1, C15): two loops that share one actor name are
// told apart by their tokens: the first takes the lease (generation 1), the
// second is told who holds it and is not the tick; the holder renews at the
// same generation; when it lapses the other takes it at generation 2, and the
// old holder, renewing, finds itself idle. A step that carries the old
// generation is refused STALEGEN, and generation 0 is never current.
func TestLeaseTokenPerProcess(t *testing.T) {
	t.Parallel()
	tw, _, _, clk := partsTwin(t)
	a := func() *StepReply { return mustStep(t, tw, leaseReq("token-a", "run", 5000, nil)) }
	b := func() *StepReply { return mustStep(t, tw, leaseReq("token-b", "run", 5000, nil)) }

	got := partReply(t, a(), PartLease)
	if got["held"] != true || got["took"] != true || got["gen"] != "1" || got["owner"] != "token-a" {
		t.Fatalf("first loop: %v", got)
	}
	got = partReply(t, b(), PartLease)
	if got["held"] != false || got["owner"] != "token-a" || got["name"] != "run" || got["gen"] != "1" {
		t.Fatalf("second loop with the same actor name: %v; want it told token-a holds generation 1", got)
	}
	clk.advance(2 * time.Second)
	got = partReply(t, a(), PartLease)
	if got["held"] != true || got["took"] != false || got["gen"] != "1" {
		t.Fatalf("a renewal moved the generation: %v", got)
	}
	if want := strconv.FormatInt(clk.ms()+5000, 10); got["until_ms"] != want {
		t.Fatalf("renewal until %v, want %s", got["until_ms"], want)
	}
	clk.advance(3 * time.Second) // 1 s before the lease lapses
	if got = partReply(t, b(), PartLease); got["held"] != false {
		t.Fatalf("the second loop took a lease that had not lapsed: %v", got)
	}
	clk.advance(5 * time.Second) // lapsed at +4 s
	got = partReply(t, b(), PartLease)
	if got["held"] != true || got["took"] != true || got["gen"] != "2" || got["owner"] != "token-b" {
		t.Fatalf("second loop after the lapse: %v", got)
	}
	got = partReply(t, a(), PartLease)
	if got["held"] != false || got["owner"] != "token-b" || got["gen"] != "2" {
		t.Fatalf("the old holder renewing: %v; want it idle under generation 2", got)
	}
	if h := tw.SprintKeys()[sk("lease")].Hash; h["owner"] != "token-b" || h["gen"] != "2" {
		t.Fatalf("lease hash %v", h)
	}

	// The generation is the write's authority (E4, T1).
	img := tw.SprintKeys()
	if ref := refusedStep(t, tw, popReq(1, 10)); ref.Code != CodeStaleGen {
		t.Fatalf("a pop at the old generation: %v, want STALEGEN", ref)
	}
	if ref := refusedStep(t, tw, popReq(0, 10)); ref.Code != CodeStaleGen {
		t.Fatalf("a pop at generation 0: %v, want STALEGEN", ref)
	}
	if len(changedKeys(img, tw.SprintKeys())) != 0 {
		t.Fatal("a refused pop changed the keys")
	}
	mustStep(t, tw, popReq(2, 10))
}

// TestLeaseHeldWritesOnlyIdleFields (1.1, 1.4.2 RT1, A3): a loop whose lease
// is held by another sends the first round trip of a tick (lease, pop, and a
// sprint part with a quarantine, here with an ingest as well); the step's only
// write is idle_loop and idle_at on the heartbeat, its reply says who holds the
// lease, and the due entries, the agenda, the cursor and the quarantine are as
// they were.
func TestLeaseHeldWritesOnlyIdleFields(t *testing.T) {
	t.Parallel()
	tw, m, log, clk := partsTwin(t)
	mustStep(t, tw, leaseReq("token-a", "run-a", 60000, map[string]string{"ticks": "9"}))
	seed(tw, Command("ZADD", ek("due"), kindZSet, "1", "seen:m1"),
		Command("ZADD", ek("agenda"), kindZSet, "4", "deal"))
	clk.advance(time.Second)
	before, img := tw.SprintKeys(), image(t, tw, m, log)

	req := leaseReq("token-b", "run-b", 5000, map[string]string{"ticks": "77", "error": "boom"})
	req.Pop = &PopPart{Limit: 10}
	req.Ingest = &IngestPart{From: "0", To: "3", Keys: []sprint.AgendaKey{{Key: "deal", Seq: 3}}}
	req.Sprint = &SprintPart{Coordinator: "someone",
		Quarantine: []Quarantined{{ID: "p1", Stream: "s1", Code: "DRIFT", Rule: "deal", Cells: []string{"s1:ready"}}}}
	req.Body.Quarantine = req.Sprint.Quarantine // X acts on the same card
	reply := mustStep(t, tw, req)

	if got := partReply(t, reply, PartLease); got["held"] != false || got["owner"] != "token-a" || got["name"] != "run-a" || got["gen"] != "1" {
		t.Fatalf("lease reply %v; want token-a's", got)
	}
	for _, name := range []string{PartPop, PartIngest, PartSprint} {
		if got := partReply(t, reply, name); got["skipped"] != true {
			t.Errorf("part %s ran for a loop that does not hold the lease: %v", name, got)
		}
	}
	after := tw.SprintKeys()
	if got := changedKeys(before, after); !sameStrings(got, []string{sk("heartbeat")}) {
		t.Fatalf("changed keys %v, want the heartbeat alone", got)
	}
	hb := after[sk("heartbeat")].Hash
	if hb["idle_loop"] != "run-b" || hb["idle_at"] != strconv.FormatInt(clk.ms(), 10) {
		t.Fatalf("idle fields %v", hb)
	}
	if hb["ticks"] != "9" || hb["error"] != "" || hb["owner"] != "token-a" {
		t.Fatalf("the idle loop touched the holder's fields: %v", hb)
	}
	// Whole image: only the heartbeat's idle fields moved; the tables and the
	// log did not.
	snapBefore, snapAfter := imageParts(t, img), imageParts(t, image(t, tw, m, log))
	if !reflect.DeepEqual(snapBefore["Tables"], snapAfter["Tables"]) || !reflect.DeepEqual(snapBefore["Log"], snapAfter["Log"]) {
		t.Fatal("the idle step changed the tables or the log")
	}
}

// imageParts splits an image's JSON into its three parts.
func imageParts(t *testing.T, img []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(img, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestHeartbeatByField (A3): two loops for one minute, one holding the lease
// and sending its tick's fields each second, the other idle. At every read the
// heartbeat holds the holder's owner and generation, its last fields, and the
// idle loop's name and time: neither loop's write dropped the other's facts.
// owner and gen are the lease part's own whatever the loop sends, looked_at is
// the call's time when the loop runs STOPPED, and the idle loop's fields and
// fields the design does not name are refused.
func TestHeartbeatByField(t *testing.T) {
	t.Parallel()
	tw, _, _, clk := partsTwin(t)
	for sec := 1; sec <= 60; sec++ {
		clk.advance(time.Second)
		mustStep(t, tw, leaseReq("token-a", "run-a", 5000, map[string]string{"ticks": strconv.Itoa(sec), "tick_at": strconv.FormatInt(clk.ms(), 10)}))
		if sec%2 == 0 {
			mustStep(t, tw, leaseReq("token-b", "run-b", 5000, nil))
		}
		hb := tw.SprintKeys()[sk("heartbeat")].Hash
		if hb["owner"] != "token-a" || hb["gen"] != "1" || hb["ticks"] != strconv.Itoa(sec) {
			t.Fatalf("second %d: the holder's facts in the heartbeat are %v", sec, hb)
		}
		if sec >= 2 && (hb["idle_loop"] != "run-b" || hb["idle_at"] == "") {
			t.Fatalf("second %d: the idle loop's facts in the heartbeat are %v", sec, hb)
		}
	}
	// A loop that sends owner and gen, as 1.4.1's list has it, writes the lease's.
	mustStep(t, tw, leaseReq("token-a", "run-a", 5000, map[string]string{"owner": "someone-else", "gen": "99", "looked_at": "5"}))
	if hb := tw.SprintKeys()[sk("heartbeat")].Hash; hb["owner"] != "token-a" || hb["gen"] != "1" || hb["looked_at"] != "5" {
		t.Fatalf("heartbeat %v", hb)
	}
	// A STOPPED loop writes looked_at itself (1.4.5).
	stopped := leaseReq("token-a", "run-a", 5000, map[string]string{"looked_at": "5"})
	stopped.Lease.Stopped = true
	mustStep(t, tw, stopped)
	if hb := tw.SprintKeys()[sk("heartbeat")].Hash; hb["looked_at"] != strconv.FormatInt(clk.ms(), 10) {
		t.Fatalf("looked_at %q", hb["looked_at"])
	}
	for _, field := range []string{"idle_loop", "idle_at", "nonsense"} {
		if ref := refusedStep(t, tw, leaseReq("token-a", "run-a", 5000, map[string]string{field: "x"})); ref.Code != CodeRequest {
			t.Errorf("heartbeat field %s from a caller: %v, want REQUEST", field, ref)
		}
	}
	if !sameStrings(HeartbeatFields(), []string{"agenda", "backlog", "due_now", "error", "failures", "gen", "heldq", "looked_at", "owner",
		"rules", "swept", "tick_at", "ticks"}) {
		t.Fatalf("HeartbeatFields %v", HeartbeatFields())
	}
}

// ingestCmds is the command list the ingest part builds for a request, as the
// twin would commit it, without applying it.
func ingestCmds(t *testing.T, tw *Twin, clk *stepClock, req *Request) []Cmd {
	t.Helper()
	st := tw.partState(clk, req.Epoch)
	plan, ref := ingestPart{}.Pre(st, req, nil)
	if ref != nil {
		t.Fatalf("ingest pre: %v", ref)
	}
	cmds, ref := ingestPart{}.Cmds(st, plan, LogPlan{})
	if ref != nil {
		t.Fatal(ref)
	}
	return cmds
}

// sprintKeysOf is the agenda, held queue and tick hash of a twin, the keys an
// ingest writes.
func sprintKeysOf(tw *Twin, names ...string) map[string]KeyValue {
	all := tw.SprintKeys()
	out := map[string]KeyValue{}
	for _, n := range names {
		if v, ok := all[ek(n)]; ok {
			out[n] = v
		}
	}
	return out
}

// TestIngestKeysBeforeCursor (A1, 1.1, E7): the ingest's command list adds
// every key before it moves the cursor; a fault injected after the ZADDs (the
// function erred and kept what it had written) leaves the keys in and the
// cursor back, the next ingest adds nothing new and moves the cursor, and the
// keys end where a fault-free run leaves them. The reversed witness, a list
// that moved the cursor first, loses the keys: the retry is refused INGESTAT
// and they are never added.
func TestIngestKeysBeforeCursor(t *testing.T) {
	t.Parallel()
	keys := []sprint.AgendaKey{{Key: "resolve:s1", Seq: 2}, {Key: "deal", Seq: 3}, {Key: "held:p1", Seq: 4}, {Key: "ask:p1", Seq: 5}}
	setup := func() (*Twin, *stepClock) {
		tw, _, _, clk := partsTwin(t)
		mustStep(t, tw, leaseReq("token-a", "run", 60000, nil))
		return tw, clk
	}

	clean, _ := setup()
	reply := mustStep(t, clean, ingestReq(1, "0", "5", keys...))
	if got := partReply(t, reply, PartIngest); got["cur"] != "5" || got["added"] != float64(4) {
		t.Fatalf("ingest reply %v", got)
	}
	want := sprintKeysOf(clean, "agenda", "heldq", "tick")
	if want["agenda"].ZSet["resolve:s1"] != 2 || want["agenda"].ZSet["ask:p1"] != 5 || want["heldq"].ZSet["held:p1"] != 4 ||
		want["tick"].Hash["cur"] != "5" {
		t.Fatalf("a clean ingest left %v", want)
	}
	if _, inAgenda := want["agenda"].ZSet["held:p1"]; inAgenda {
		t.Fatal("a held key was added to the agenda")
	}

	tw, clk := setup()
	req := ingestReq(1, "0", "5", keys...)
	cmds := ingestCmds(t, tw, clk, req)
	last := cmds[len(cmds)-1]
	if last.Argv[0] != "HSET" || last.Argv[1] != ek("tick") || last.Argv[2] != "cur" || last.Argv[3] != "5" {
		t.Fatalf("the last command is %v, want the cursor's HSET", last.Argv)
	}
	for i, c := range cmds[:len(cmds)-1] {
		if c.Argv[0] != "ZADD" {
			t.Fatalf("command %d is %v before the cursor, want ZADDs only", i, c.Argv)
		}
	}
	tw.keys.apply(cmds[:len(cmds)-1]) // the fault: every key written, the cursor not
	if got := sprintKeysOf(tw, "tick"); got["tick"].Hash["cur"] != "" {
		t.Fatalf("the fault left the cursor at %v", got)
	}
	retry := mustStep(t, tw, req)
	if got := partReply(t, retry, PartIngest); got["cur"] != "5" || got["added"] != float64(0) {
		t.Fatalf("the retry after the fault: %v; want the cursor moved and no key added twice", got)
	}
	if got := sprintKeysOf(tw, "agenda", "heldq", "tick"); !reflect.DeepEqual(got, want) {
		t.Fatalf("after the fault and the retry %v\nwant what a clean run leaves %v", got, want)
	}

	// The reversed witness: cursor first, then the fault before the keys.
	rw, rclk := setup()
	rreq := ingestReq(1, "0", "5", keys...)
	rcmds := ingestCmds(t, rw, rclk, rreq)
	reversed := append([]Cmd{rcmds[len(rcmds)-1]}, rcmds[:len(rcmds)-1]...)
	rw.keys.apply(reversed[:1])
	if ref := refusedStep(t, rw, rreq); ref.Code != CodeIngestAt {
		t.Fatalf("the retry after a cursor-first fault: %v, want INGESTAT", ref)
	}
	if got := sprintKeysOf(rw, "agenda", "heldq"); len(got) != 0 {
		t.Fatalf("the witness left keys %v; the order of A1 would not have mattered", got)
	}
}

// TestIngestCursorRefused (1.1): an ingest whose From is not the cursor is
// refused INGESTAT and writes nothing, the lease generation is checked first,
// and a page that does not move the cursor forward, or whose keys no line of
// the page queued, is REQUEST.
func TestIngestCursorRefused(t *testing.T) {
	t.Parallel()
	tw, m, log, _ := partsTwin(t)
	mustStep(t, tw, leaseReq("token-a", "run", 60000, nil))
	mustStep(t, tw, ingestReq(1, "0", "5", sprint.AgendaKey{Key: "deal", Seq: 5}))
	img, keys := image(t, tw, m, log), tw.SprintKeys()

	cases := []struct {
		name string
		req  *Request
		code string
	}{
		{"another loop ingested (from behind the cursor)", ingestReq(1, "0", "3", sprint.AgendaKey{Key: "ask:p1", Seq: 3}), CodeIngestAt},
		{"from ahead of the cursor", ingestReq(1, "9", "12", sprint.AgendaKey{Key: "ask:p1", Seq: 10}), CodeIngestAt},
		{"a stale generation wins over a wrong cursor", ingestReq(7, "0", "3"), CodeStaleGen},
		{"to behind from", ingestReq(1, "5", "4"), CodeRequest},
		{"keys with no line", ingestReq(1, "5", "5", sprint.AgendaKey{Key: "deal", Seq: 5}), CodeRequest},
		{"a key older than the page", ingestReq(1, "5", "8", sprint.AgendaKey{Key: "deal", Seq: 5}), CodeRequest},
		{"a key newer than the page", ingestReq(1, "5", "8", sprint.AgendaKey{Key: "deal", Seq: 9}), CodeRequest},
		{"a key with a control character", ingestReq(1, "5", "8", sprint.AgendaKey{Key: "de\tal", Seq: 6}), CodeRequest},
		{"a cursor past the exact seqs", ingestReq(1, "5", "9007199254740992"), CodeRequest},
	}
	for _, c := range cases {
		if ref := refusedStep(t, tw, c.req); ref.Code != c.code {
			t.Errorf("%s: %v, want %s", c.name, ref, c.code)
		}
	}
	if string(image(t, tw, m, log)) != string(img) || len(changedKeys(keys, tw.SprintKeys())) != 0 {
		t.Fatal("a refused ingest changed the twin")
	}
	mustStep(t, tw, ingestReq(1, "5", "8", sprint.AgendaKey{Key: "ask:p1", Seq: 7}))
	if got := sprintKeysOf(tw, "tick")["tick"].Hash["cur"]; got != "8" {
		t.Fatalf("cursor %q after a good ingest", got)
	}
}

// TestIngestReplyAndRefusalCarryCur (1.1, E6): the reply of an applied ingest
// carries the new cur, and INGESTAT carries the cursor as the store holds it,
// as an exact decimal in its detail, which a loop reads to drop the lines it
// need not ingest again.
func TestIngestReplyAndRefusalCarryCur(t *testing.T) {
	t.Parallel()
	tw, _, _, _ := partsTwin(t)
	mustStep(t, tw, leaseReq("token-a", "run", 60000, nil))
	if got := partReply(t, mustStep(t, tw, ingestReq(1, "0", "9007199254740991", sprint.AgendaKey{Key: "deal", Seq: 9007199254740991})), PartIngest); got["cur"] != "9007199254740991" {
		t.Fatalf("reply %v", got)
	}
	ref := refusedStep(t, tw, ingestReq(1, "0", "3", sprint.AgendaKey{Key: "deal", Seq: 3}))
	if ref.Code != CodeIngestAt || ref.Detail.Cur != "9007199254740991" {
		t.Fatalf("refusal %+v, want INGESTAT with cur 9007199254740991", ref)
	}
	b, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Code   string `json:"code"`
		Detail struct {
			Cur string `json:"cur"`
		} `json:"detail"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(b, &wire); err != nil || wire.Detail.Cur != "9007199254740991" {
		t.Fatalf("wire %s: %v", b, err)
	}
	if wire.Message != "the cursor is at 9007199254740991 and this ingest starts at 0: another loop ingested; nothing was changed" {
		t.Fatalf("message %q", wire.Message)
	}
}

// TestPartIngestHeldQueueCap (1.1, Order): held keys go to the held queue, and
// an ingest that takes it past its cap drops the oldest held keys, at most
// HeldDropMax a call; the agenda's keys are never dropped. A queue at the cap
// with one key of the page already in it adds nothing and drops nothing.
func TestPartIngestHeldQueueCap(t *testing.T) {
	t.Parallel()
	tw, _, _, _ := partsTwin(t)
	mustStep(t, tw, leaseReq("token-a", "run", 60000, nil))
	var fill flatPairs
	for i := 1; i <= HeldQueueCap; i++ {
		fill = append(fill, strconv.Itoa(i), "held:c"+strconv.Itoa(i))
	}
	seed(tw, zaddCommands(ek("heldq"), fill)...)
	seed(tw, Command("ZADD", ek("agenda"), kindZSet, "1", "deal"))

	// A key already in the queue adds nothing, so nothing is dropped.
	reply := mustStep(t, tw, ingestReq(1, "0", "200000", sprint.AgendaKey{Key: "held:c7", Seq: 150000}))
	if got := partReply(t, reply, PartIngest); got["added"] != float64(0) || got["dropped"] != float64(0) {
		t.Fatalf("a key already queued: %v", got)
	}
	// Three new held keys and one agenda key: the three oldest held keys go.
	reply = mustStep(t, tw, ingestReq(1, "200000", "300000",
		sprint.AgendaKey{Key: "held:n1", Seq: 200001}, sprint.AgendaKey{Key: "held@200002", Seq: 200002},
		sprint.AgendaKey{Key: "held:n3", Seq: 200003}, sprint.AgendaKey{Key: "ask:p1", Seq: 200004}))
	if got := partReply(t, reply, PartIngest); got["added"] != float64(4) || got["dropped"] != float64(3) {
		t.Fatalf("over the cap: %v", got)
	}
	q := tw.SprintKeys()[ek("heldq")].ZSet
	if len(q) != HeldQueueCap {
		t.Fatalf("the held queue holds %d keys, want the cap %d", len(q), HeldQueueCap)
	}
	for _, gone := range []string{"held:c1", "held:c2", "held:c3"} {
		if _, ok := q[gone]; ok {
			t.Errorf("the oldest key %s was not dropped", gone)
		}
	}
	for _, kept := range []string{"held:c4", "held:n1", "held@200002", "held:n3"} {
		if _, ok := q[kept]; !ok {
			t.Errorf("key %s was dropped", kept)
		}
	}
	if a := tw.SprintKeys()[ek("agenda")].ZSet; a["deal"] != 1 || a["ask:p1"] != 200004 {
		t.Fatalf("agenda %v", a)
	}

	// Far over the cap: at most HeldDropMax go in one call.
	var many []sprint.AgendaKey
	for i := 0; i < HeldDropMax+500; i++ {
		many = append(many, sprint.AgendaKey{Key: "held:m" + strconv.Itoa(i), Seq: uint64(300001 + i)})
	}
	reply = mustStep(t, tw, ingestReq(1, "300000", "400000", many...))
	if got := partReply(t, reply, PartIngest); got["added"] != float64(len(many)) || got["dropped"] != float64(HeldDropMax) {
		t.Fatalf("far over the cap: %v", got)
	}
}

// fleetSeed gives the fleet table the rows and control cards a beat reads:
// m1 up, m2 down, m3 held; m4 has no fleet row.
func fleetSeed(t *testing.T, tw *Twin) { fleetSeedAt(t, tw, "0") }

// fleetSeedAt is fleetSeed at an epoch, where a control card's stored id
// carries the epoch (errata E1; sprint.StoredID).
func fleetSeedAt(t *testing.T, tw *Twin, epoch tset.Decimal) {
	t.Helper()
	ctl := func(m, status string) tset.Entry {
		id, _ := storedControlID(epoch, m)
		return tset.Entry{Kind: "create", Table: sprint.Fleet, To: m + ":ctl", IDs: []string{id}, Scores: []string{"0"},
			Set: map[string]string{"kind": "member", "status": status}, About: []string{id}}
	}
	req := &Request{Epoch: epoch, Meta: Meta{Verb: "fleet up"}, Body: Body{Entries: []tset.Entry{
		{Kind: "rows", Table: sprint.Fleet, Add: []string{"m1", "m2", "m3"}},
		ctl("m1", "up"), ctl("m2", "down"), ctl("m3", "held")}}}
	mustStep(t, tw, req)
}

func beatReq(members ...string) *Request {
	b := &BeatPart{}
	for _, m := range members {
		b.Members = append(b.Members, BeatMember{Member: m, Load: "0.25 0.5"})
	}
	return &Request{Epoch: "0", Meta: Meta{Verb: "fleet beat"}, Beat: b}
}

// TestBeatEntersSeenOnlyWhenDown (1.4.4): a beat writes each member's record
// and moves beat:<m> to R + 15 s; it enters seen:<m> at R only for a member
// whose control card says down, or who has no fleet row (a stranger, noted once
// in {p}strangers). An up member and a held member get none. seen is added
// only when absent, so the earliest R stays. A beat writes no line and changes
// no table record, and a twin whose before hook did not ask for the control
// cards refuses CONFIG rather than read every member as a stranger.
func TestBeatEntersSeenOnlyWhenDown(t *testing.T) {
	t.Parallel()
	tw, m, log, clk := partsTwin(t)
	fleetSeed(t, tw)
	tablesBefore, linesBefore := imageParts(t, image(t, tw, m, log))["Tables"], len(log.Lines(testPrefix, "0"))
	r := clk.ms() // no clock record: R is the store's time

	reply := mustStep(t, tw, beatReq("m1", "m2", "m3", "m4"))
	if reply.Reply.Lines != 0 || reply.Reply.Changed != 0 {
		t.Fatalf("a beat wrote lines or changed records: %+v", reply.Reply)
	}
	due := tw.SprintKeys()[ek("due")].ZSet
	fresh := float64(r + BeatFreshMS)
	for _, member := range []string{"m1", "m2", "m3", "m4"} {
		if due["beat:"+member] != fresh {
			t.Errorf("beat:%s is due at %v, want R + 15 s = %v", member, due["beat:"+member], fresh)
		}
	}
	if len(due) != 6 || due["seen:m2"] != float64(r) || due["seen:m4"] != float64(r) {
		t.Fatalf("due set %v: want seen:m2 and seen:m4 at R, and none for m1 (up) or m3 (held)", due)
	}
	got := partReply(t, reply, PartBeat)
	if !reflect.DeepEqual(got["seen"], []any{"m2", "m4"}) || !reflect.DeepEqual(got["strangers"], []any{"m4"}) ||
		got["fresh_until"] != strconv.FormatInt(r+BeatFreshMS, 10) {
		t.Fatalf("beat reply %v", got)
	}
	if s := tw.SprintKeys()[sk("strangers")].Hash; !reflect.DeepEqual(s, map[string]string{"m4": strconv.FormatInt(r, 10)}) {
		t.Fatalf("strangers %v", s)
	}
	if rec := tw.SprintKeys()[sk("beat:m1")].Hash; rec["at_ms"] != strconv.FormatInt(r, 10) || rec["load"] != "0.25 0.5" {
		t.Fatalf("m1's beat record %v", rec)
	}

	// A second beat 3 s later: beat:<m> moves, seen:<m> keeps its first R, and
	// the stranger is noted once.
	clk.advance(3 * time.Second)
	reply = mustStep(t, tw, beatReq("m1", "m2", "m4"))
	due = tw.SprintKeys()[ek("due")].ZSet
	if due["beat:m2"] != float64(clk.ms()+BeatFreshMS) || due["seen:m2"] != float64(r) || due["seen:m4"] != float64(r) {
		t.Fatalf("after the second beat %v", due)
	}
	got = partReply(t, reply, PartBeat)
	if !reflect.DeepEqual(got["seen"], []any{}) || !reflect.DeepEqual(got["strangers"], []any{}) {
		t.Fatalf("a second beat entered %v again", got)
	}
	if s := tw.SprintKeys()[sk("strangers")].Hash["m4"]; s != strconv.FormatInt(r, 10) {
		t.Fatalf("the stranger's first notice moved to %s", s)
	}
	if string(mustJSON(t, imageParts(t, image(t, tw, m, log))["Tables"])) != string(mustJSON(t, tablesBefore)) ||
		len(log.Lines(testPrefix, "0")) != linesBefore {
		t.Fatal("beats changed the tables or the log")
	}

	// R is running time: STOPPED, it stands still, so seen and beat follow it.
	stop := &Request{Epoch: "0", Meta: Meta{Verb: "stop"}, Clock: &ClockPart{Verb: ClockStop}}
	mustStep(t, tw, stop)
	rStill := clk.ms() - 0
	clk.advance(10 * time.Second)
	mustStep(t, tw, beatReq("m1"))
	if due = tw.SprintKeys()[ek("due")].ZSet; due["beat:m1"] != float64(rStill+BeatFreshMS) {
		t.Fatalf("beat:m1 at %v while STOPPED, want R + 15 s = %d", due["beat:m1"], rStill+BeatFreshMS)
	}

	// Without the before hook nothing was asked for: CONFIG, nothing written.
	bare, _, _ := newTestTwin(t, passX())
	if err := RegisterTickParts(bare.parts); err != nil {
		t.Fatal(err)
	}
	before := bare.SprintKeys()
	if ref := refusedStep(t, bare, beatReq("m1", "m4")); ref.Code != CodeConfig {
		t.Fatalf("a beat with no control cards asked for: %v, want CONFIG", ref)
	}
	if len(changedKeys(before, bare.SprintKeys())) != 0 {
		t.Fatal("the refused beat wrote")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestPartsStoreLimits: an ingest of 5,000 keys takes at most 5 ms of store
// time and a beat at most 0.1 ms, SLOWLOG in the container (8.1's IT16 limit).
// It needs the store.
func TestPartsStoreLimits(t *testing.T) {
	t.Parallel()
	t.Skip("G0: needs the store (Layer 1 revision 4 pinned, Layer 2 accepted again) and the container; the Lua half is not loaded before G0")
}

// TestPartsTwinEqualsLuaOnTheStore: the parts' replies, commands and images on
// the store equal the twin's over 10,000 random steps (the parts' share of
// IT12's TestTwinEqualsLua10000). Until G0, TestPartsLuaMatchesTwin runs the
// Lua parts under gopher-lua against a fake keyspace.
func TestPartsTwinEqualsLuaOnTheStore(t *testing.T) {
	t.Parallel()
	t.Skip("G0: needs the store; TestPartsLuaMatchesTwin is the part-level check that runs without it")
}

// TestPartsPerEpochKeys (0, row 1; errata E1): at a later epoch every per-epoch
// key is named with its epoch, "@2" here, the sprint's keys with no epoch are
// the same, and a beat reads its control cards at their stored ids, which carry
// the epoch. A step that advances writes the counter at the successor epoch, where
// the new epoch's keys are made.
func TestPartsPerEpochKeys(t *testing.T) {
	t.Parallel()
	tw, _, _, clk := partsTwinAt(t, "2")
	fleetSeedAt(t, tw, "2")
	at := func(req *Request) *Request { req.Epoch = "2"; return req }
	mustStep(t, tw, at(leaseReq("token-a", "run", 600000, nil)))
	clk.advance(time.Second)
	dueAt := clk.ms()
	seed(tw, Command("ZADD", ekAt("due", "2"), kindZSet, strconv.FormatInt(dueAt-5, 10), "seen:m2", strconv.FormatInt(dueAt-4, 10), "behind"),
		Command("ZADD", ekAt("agenda", "2"), kindZSet, "4", "deal"))
	pop := at(popReq(1, 10))
	ingest := at(ingestReq(1, "0", "4", sprint.AgendaKey{Key: "ask:p1", Seq: 3}, sprint.AgendaKey{Key: "held:p1", Seq: 4}))
	mustStep(t, tw, pop)
	mustStep(t, tw, ingest)
	reply := mustStep(t, tw, at(beatReq("m1", "m2", "m9")))
	if got := partReply(t, reply, PartBeat); !reflect.DeepEqual(got["seen"], []any{"m2", "m9"}) {
		t.Fatalf("beat at epoch 2 entered %v in seen: m1 is up at its stored id ctl-m1~2", got["seen"])
	}
	mustStep(t, tw, at(sprintReq(&SprintPart{Counter: &CounterChange{Read: map[string]string{"score": ""}, Set: map[string]string{"score": "9"}},
		Dropping: map[string]string{"s1": "op"}, Park: []ParkedKey{{Key: "deal", Code: "LIMIT"}}, Coordinator: "boss"},
		Quarantined{ID: "p1~2", Code: "DRIFT"})))
	got := tw.SprintKeys()
	for _, name := range []string{"agenda", "heldq", "tick", "due", "cut", "next", "dropping", "parked", "quarantine"} {
		if _, stray := got[ek(name)]; stray {
			t.Errorf("a key of epoch 0 was written: %s", ek(name))
		}
	}
	for _, key := range []string{ekAt("agenda", "2"), ekAt("heldq", "2"), ekAt("tick", "2"), ekAt("due", "2"), ekAt("next", "2"),
		ekAt("dropping", "2"), ekAt("parked", "2"), ekAt("quarantine", "2"), sk("lease"), sk("heartbeat"), sk("coordinator"), sk("strangers"), sk("beat:m1")} {
		if _, ok := got[key]; !ok {
			t.Errorf("no key %s", key)
		}
	}
	if got[ekAt("tick", "2")].Hash["cur"] != "4" || got[ekAt("next", "2")].Hash["score"] != "9" {
		t.Fatalf("tick %v, next %v", got[ekAt("tick", "2")], got[ekAt("next", "2")])
	}

	// A step that advances: the counter is written where the new epoch's keys are.
	adv := &Request{Epoch: "2", Meta: Meta{Verb: "clear"},
		Body:   Body{Entries: []tset.Entry{{Kind: "advance", AdvanceFrom: "2"}}, Op: &Op{ID: "clear-1", Intent: "clear"}},
		Sprint: &SprintPart{Counter: &CounterChange{Read: map[string]string{"score": ""}, Set: map[string]string{"score": "1"}}}}
	mustStep(t, tw, adv)
	got = tw.SprintKeys()
	if got[ekAt("next", "3")].Hash["score"] != "1" || got[ekAt("next", "2")].Hash["score"] != "9" {
		t.Fatalf("after the advance: next@3 %v, next@2 %v", got[ekAt("next", "3")], got[ekAt("next", "2")])
	}
}

// TestIngestNamesParkedKeys: the ingest reply names the page's keys that are
// parked, in the page's order, and queues none of them, so a loop that did not
// park them leaves them out of its plans with no read of its own (1.3.5); a
// page with none names an empty list.
func TestIngestNamesParkedKeys(t *testing.T) {
	t.Parallel()
	tw, _, _, _ := partsTwin(t)
	mustStep(t, tw, leaseReq("token-a", "run", 60000, nil))
	park := genReq(1)
	park.Sprint = &SprintPart{Park: []ParkedKey{{Key: "deal", Rule: "deal", Code: "NOCOL"}, {Key: "resolve:s2", Rule: "resolve", Code: "NOCOL"}}}
	mustStep(t, tw, park)
	reply := mustStep(t, tw, ingestReq(1, "0", "4", sprint.AgendaKey{Key: "resolve:s2", Seq: 2}, sprint.AgendaKey{Key: "ask:p1", Seq: 3}, sprint.AgendaKey{Key: "deal", Seq: 4}))
	got := partReply(t, reply, PartIngest)
	if got["parked"] != float64(2) || fmt.Sprint(got["parked_keys"]) != "[resolve:s2 deal]" || got["added"] != float64(1) {
		t.Fatalf("ingest reply %v", got)
	}
	reply = mustStep(t, tw, ingestReq(1, "4", "5", sprint.AgendaKey{Key: "ask:p2", Seq: 5}))
	if got := partReply(t, reply, PartIngest); fmt.Sprint(got["parked_keys"]) != "[]" {
		t.Fatalf("ingest reply %v", got)
	}
}

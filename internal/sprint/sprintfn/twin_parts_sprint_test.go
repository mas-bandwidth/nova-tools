package sprintfn

import (
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// sprintReq is a request that carries only a sprint part.
func sprintReq(sp *SprintPart, qs ...Quarantined) *Request {
	r := &Request{Epoch: "0", Meta: Meta{Verb: "sprint"}, Sprint: sp}
	r.Body.Quarantine = qs
	return r
}

// sprintCmds is the command list the sprint part builds for a request, as the
// twin would commit it, without applying it.
func sprintCmds(t *testing.T, tw *Twin, clk *stepClock, p sprintPart, req *Request) []Cmd {
	t.Helper()
	st := tw.partState(clk, req.Epoch)
	plan, ref := p.Pre(st, req, nil)
	if ref != nil {
		t.Fatalf("sprint pre: %v", ref)
	}
	cmds, ref := p.Cmds(st, plan, LogPlan{})
	if ref != nil {
		t.Fatal(ref)
	}
	return cmds
}

// tickEndTwin is a twin whose sprint part takes the tick end from tick, as
// IT12's SprintPart will once it carries the field; the test sets *tick before
// each step.
func tickEndTwin(t *testing.T, tick **TickEnd) (*Twin, *stepClock) {
	t.Helper()
	tw, _, _, clk := partsTwin(t)
	tw.parts = NewPartRegistry()
	for _, name := range PartOrder {
		p, _ := defaultParts.Lookup(name)
		if name == PartSprint {
			p = sprintPart{tickEnd: func(*Request) *TickEnd { return *tick }}
		}
		if err := tw.parts.Register(name, p); err != nil {
			t.Fatal(err)
		}
	}
	return tw, clk
}

// TestTickEndArmsBehind (1.2, R18): the tick end, on the last step of a tick,
// arms behind at R + 5 min and records the backlog as behind_n when the
// backlog is not zero and behind is not armed; an armed backlog that is not
// zero moves nothing (not moved while it shrinks); zero disarms both. R is
// running time: while STOPPED it stands still, so a backlog seen then is armed
// at the R of the stop and not at wall time.
func TestTickEndArmsBehind(t *testing.T) {
	t.Parallel()
	var tick *TickEnd
	tw, clk := tickEndTwin(t, &tick)
	send := func(backlog string) map[string]any {
		tick = &TickEnd{Backlog: tset.Decimal(backlog)}
		return partReply(t, mustStep(t, tw, sprintReq(&SprintPart{})), PartSprint)
	}

	r0 := clk.ms()
	if got := send("0"); got["tickend"] != "quiet" {
		t.Fatalf("zero and not armed: %v", got)
	}
	if len(tw.SprintKeys()) != 0 {
		t.Fatalf("a quiet tick end wrote %v", tw.SprintKeys())
	}
	if got := send("7"); got["tickend"] != "armed" {
		t.Fatalf("a backlog: %v", got)
	}
	if due := tw.SprintKeys()[ek("due")].ZSet; len(due) != 1 || due["behind"] != float64(r0+BehindSpanMS) {
		t.Fatalf("due %v, want behind at R + 5 min = %d", due, r0+BehindSpanMS)
	}
	if h := tw.SprintKeys()[ek("tick")].Hash; h["behind_n"] != "7" {
		t.Fatalf("tick hash %v", h)
	}

	clk.advance(time.Minute)
	if got := send("3"); got["tickend"] != "held" {
		t.Fatalf("a shrinking backlog: %v", got)
	}
	if due := tw.SprintKeys()[ek("due")].ZSet["behind"]; due != float64(r0+BehindSpanMS) {
		t.Fatalf("behind moved to %v while the backlog shrank", due)
	}
	if h := tw.SprintKeys()[ek("tick")].Hash; h["behind_n"] != "7" {
		t.Fatalf("behind_n changed to %v while armed", h["behind_n"])
	}

	if got := send("0"); got["tickend"] != "disarmed" {
		t.Fatalf("a zero backlog: %v", got)
	}
	if k := tw.SprintKeys(); len(k[ek("due")].ZSet) != 0 || k[ek("tick")].Hash["behind_n"] != "" {
		t.Fatalf("disarming left %v", k)
	}

	// When behind fired, the pop removed it; the tick end arms it again with
	// the new backlog.
	if got := send("4"); got["tickend"] != "armed" {
		t.Fatalf("re-arming: %v", got)
	}
	if h := tw.SprintKeys()[ek("tick")].Hash; h["behind_n"] != "4" {
		t.Fatalf("behind_n %v after re-arming", h)
	}

	// STOPPED: R stands still at the stop, wall time goes on.
	send("0")
	mustStep(t, tw, &Request{Epoch: "0", Meta: Meta{Verb: "stop"}, Clock: &ClockPart{Verb: ClockStop}})
	rStill := clk.ms()
	clk.advance(10 * time.Minute)
	if got := send("9"); got["tickend"] != "armed" {
		t.Fatalf("while STOPPED: %v", got)
	}
	if due := tw.SprintKeys()[ek("due")].ZSet["behind"]; due != float64(rStill+BehindSpanMS) {
		t.Fatalf("behind at %v while STOPPED, want the still R + 5 min = %d", due, rStill+BehindSpanMS)
	}

	// The value is an exact decimal; a wrong one is the caller's fault.
	for _, bad := range []string{"", "-1", "1.5", "007", "18446744073709551616"} {
		tick = &TickEnd{Backlog: tset.Decimal(bad)}
		if ref := refusedStep(t, tw, sprintReq(&SprintPart{})); ref.Code != CodeRequest {
			t.Errorf("backlog %q: %v, want REQUEST", bad, ref)
		}
	}
	// The real registry has no tick end to give yet (IT12's SprintPart has no
	// field): a sprint part with nothing else to do writes nothing.
	plain, _, _, _ := partsTwin(t)
	if got := partReply(t, mustStep(t, plain, sprintReq(&SprintPart{})), PartSprint); got["tickend"] != nil {
		t.Fatalf("the registered sprint part invented a tick end: %v", got)
	}
}

// TestParkMovesKeyOutOfAgenda (1.3.5, A1): the error step parks a key in
// {p}parked@e and takes it out of the agenda, and the park is written before
// the ZREM, so a fault between them leaves the key in both places and the
// retry finishes the move; the reversed witness, a ZREM first, leaves it in
// neither. A key parked by another note is XGUARD; unparking removes the park
// and puts nothing back in the agenda (its acknowledged line queues it again).
func TestParkMovesKeyOutOfAgenda(t *testing.T) {
	t.Parallel()
	setup := func() (*Twin, *stepClock) {
		tw, _, _, clk := partsTwin(t)
		seed(tw, Command("ZADD", ek("agenda"), kindZSet, "4", "deal", "5", "ask:p1"))
		return tw, clk
	}
	part := sprintPart{}
	req := func() *Request { return sprintReq(&SprintPart{Park: map[string]string{"deal": "n7"}}) }

	tw, _ := setup()
	reply := mustStep(t, tw, req())
	if got := partReply(t, reply, PartSprint); !reflect.DeepEqual(got["parked"], []any{"deal"}) {
		t.Fatalf("reply %v", got)
	}
	k := tw.SprintKeys()
	if k[ek("parked")].Hash["deal"] != "n7" {
		t.Fatalf("parked %v", k[ek("parked")])
	}
	if a := k[ek("agenda")].ZSet; len(a) != 1 || a["ask:p1"] != 5 {
		t.Fatalf("agenda %v: the parked key is still in it, or another key left", a)
	}

	// The order: the park's HSET, then the agenda's ZREM.
	fw, clk := setup()
	cmds := sprintCmds(t, fw, clk, part, req())
	if len(cmds) != 2 || cmds[0].Argv[0] != "HSET" || cmds[0].Argv[1] != ek("parked") ||
		cmds[1].Argv[0] != "ZREM" || cmds[1].Argv[1] != ek("agenda") {
		t.Fatalf("commands %v, want the park's HSET and then the agenda's ZREM", cmds)
	}
	fw.keys.apply(cmds[:1]) // the fault: parked, not yet out of the agenda
	if a := fw.SprintKeys()[ek("agenda")].ZSet; a["deal"] != 4 {
		t.Fatal("the fault removed the key")
	}
	mustStep(t, fw, req()) // the retry: the same park, the ZREM
	if !reflect.DeepEqual(fw.SprintKeys()[ek("agenda")], tw.SprintKeys()[ek("agenda")]) ||
		!reflect.DeepEqual(fw.SprintKeys()[ek("parked")], tw.SprintKeys()[ek("parked")]) {
		t.Fatal("after the fault and the retry the keys differ from a clean run")
	}

	// The reversed witness loses the key: out of the agenda, never parked.
	rw, rclk := setup()
	rcmds := sprintCmds(t, rw, rclk, part, req())
	rw.keys.apply(rcmds[1:2]) // the ZREM first, then the fault
	k = rw.SprintKeys()
	if _, inAgenda := k[ek("agenda")].ZSet["deal"]; inAgenda || len(k[ek("parked")].Hash) != 0 {
		t.Fatal("the witness did not lose the key")
	}

	// Another note holds it.
	if ref := refusedStep(t, tw, sprintReq(&SprintPart{Park: map[string]string{"deal": "n9"}})); ref.Code != CodeXGuard {
		t.Fatalf("parked by another note: %v, want XGUARD", ref)
	}
	// The same note again is the same park, and writes nothing new.
	seed(tw, Command("ZADD", ek("agenda"), kindZSet, "4", "deal"))
	mustStep(t, tw, req())
	if a := tw.SprintKeys()[ek("agenda")].ZSet; len(a) != 1 {
		t.Fatalf("a repeated park left %v in the agenda", a)
	}
	// Unparking removes the park and adds nothing to the agenda.
	reply = mustStep(t, tw, sprintReq(&SprintPart{Park: map[string]string{"deal": ""}}))
	if got := partReply(t, reply, PartSprint); !reflect.DeepEqual(got["unparked"], []any{"deal"}) {
		t.Fatalf("reply %v", got)
	}
	k = tw.SprintKeys()
	if len(k[ek("parked")].Hash) != 0 {
		t.Fatalf("parked %v after the unpark", k[ek("parked")])
	}
	if _, inAgenda := k[ek("agenda")].ZSet["deal"]; inAgenda {
		t.Fatal("the unpark put the key back in the agenda")
	}
	for _, bad := range []map[string]string{{"": "n1"}, {"deal": "n\t1"}, {"de\nal": "n1"}} {
		if ref := refusedStep(t, tw, sprintReq(&SprintPart{Park: bad})); ref.Code != CodeRequest {
			t.Errorf("park %v: %v, want REQUEST", bad, ref)
		}
	}
}

// TestQuarantinePartWritesNoEntry (1.3.5): a card a lower layer refused is
// quarantined by the sprint part alone: its record lands in {p}quarantine@e
// with the code, the rule, the stream and the refusal's cells, and no table
// entry, no log line and no record changes, so Layer 1 cannot refuse the
// quarantine for the reason it refused the card. A card already quarantined
// keeps its first record; a card named twice in a step keeps the first.
func TestQuarantinePartWritesNoEntry(t *testing.T) {
	t.Parallel()
	tw, m, log, _ := partsTwin(t)
	mustStep(t, tw, seedRequest())
	tablesBefore, linesBefore := imageParts(t, image(t, tw, m, log))["Tables"], log.Lines(testPrefix, "0")
	revBefore, _ := m.Snapshot(testPrefix)

	q := Quarantined{ID: "p1", Stream: "s1", Code: "DRIFT", Rule: "deal", Cells: []string{"s1:ready", "s1:waiting"}}
	reply := mustStep(t, tw, sprintReq(&SprintPart{}, q, Quarantined{ID: "p2", Code: "MISSING", Rule: "resolve"}))
	if reply.Reply.Changed != 0 || reply.Reply.Lines != 0 || reply.Reply.FirstSeq != "0" || len(reply.Reply.ChangedPerEntry) != 0 {
		t.Fatalf("the quarantine changed a table or wrote a line: %+v", reply.Reply)
	}
	if got := partReply(t, reply, PartSprint); !reflect.DeepEqual(got["quarantined"], []any{"p1", "p2"}) {
		t.Fatalf("reply %v", got)
	}
	h := tw.SprintKeys()[ek("quarantine")].Hash
	if h["p1"] != "DRIFT\tdeal\ts1\t\ts1:ready\ts1:waiting" || h["p2"] != "MISSING\tresolve\t\t" {
		t.Fatalf("quarantine %q", h)
	}
	after, _ := m.Snapshot(testPrefix)
	if !reflect.DeepEqual(revBefore, after) || string(mustJSON(t, imageParts(t, image(t, tw, m, log))["Tables"])) != string(mustJSON(t, tablesBefore)) ||
		len(log.Lines(testPrefix, "0")) != len(linesBefore) {
		t.Fatal("the tables or the log changed")
	}

	// The first record stands, whatever a later step says; a card named twice
	// in one step keeps the first.
	again := Quarantined{ID: "p1", Stream: "s1", Code: "MEMBEREPOCH", Rule: "ask"}
	reply = mustStep(t, tw, sprintReq(&SprintPart{}, again, Quarantined{ID: "p3", Code: "DRIFT"}, Quarantined{ID: "p3", Code: "MISSING"}))
	if got := partReply(t, reply, PartSprint); !reflect.DeepEqual(got["quarantined"], []any{"p3"}) {
		t.Fatalf("reply %v", got)
	}
	if h = tw.SprintKeys()[ek("quarantine")].Hash; h["p1"] != "DRIFT\tdeal\ts1\t\ts1:ready\ts1:waiting" || h["p3"] != "DRIFT\t\t\t" {
		t.Fatalf("quarantine %q", h)
	}
	for _, bad := range []Quarantined{{ID: "", Code: "DRIFT"}, {ID: "p1", Code: ""}, {ID: "p1", Code: "DRIFT", Cells: []string{"a\tb"}},
		{ID: "p1", Code: "DRI\nFT"}} {
		if ref := refusedStep(t, tw, sprintReq(&SprintPart{}, bad)); ref.Code != CodeRequest {
			t.Errorf("quarantine %+v: %v, want REQUEST", bad, ref)
		}
	}
}

// TestPartSprintCounterGuard (1.3.1, U2): the counter is written only when it
// still holds what the plan read (COUNTER otherwise), every field written was
// guarded, and no counter goes down.
func TestPartSprintCounterGuard(t *testing.T) {
	t.Parallel()
	tw, _, _, _ := partsTwin(t)
	first := &CounterChange{Read: map[string]string{"score": "", "streams": "", "id:s1": ""},
		Set: map[string]string{"score": "101", "streams": "1", "id:s1": "4"}}
	mustStep(t, tw, sprintReq(&SprintPart{Counter: first}))
	if h := tw.SprintKeys()[ek("next")].Hash; !reflect.DeepEqual(h, map[string]string{"score": "101", "streams": "1", "id:s1": "4"}) {
		t.Fatalf("counter %v", h)
	}
	// Moved since the read.
	stale := &CounterChange{Read: map[string]string{"score": "100"}, Set: map[string]string{"score": "150"}}
	if ref := refusedStep(t, tw, sprintReq(&SprintPart{Counter: stale})); ref.Code != CodeCounter {
		t.Fatalf("a stale counter: %v, want COUNTER", ref)
	}
	if ref := refusedStep(t, tw, sprintReq(&SprintPart{Counter: &CounterChange{Read: map[string]string{"gate:s1": "3"}, Set: map[string]string{"gate:s1": "4"}}})); ref.Code != CodeCounter {
		t.Fatalf("a field read as present that is absent: %v, want COUNTER", ref)
	}
	good := &CounterChange{Read: map[string]string{"score": "101"}, Set: map[string]string{"score": "101"}}
	mustStep(t, tw, sprintReq(&SprintPart{Counter: good}))
	mustStep(t, tw, sprintReq(&SprintPart{Counter: &CounterChange{Read: map[string]string{"score": "101", "gate:s1": ""}, Set: map[string]string{"score": "201", "gate:s1": "1"}}}))
	if h := tw.SprintKeys()[ek("next")].Hash; h["score"] != "201" || h["gate:s1"] != "1" || h["streams"] != "1" {
		t.Fatalf("counter %v", h)
	}
	bad := []*CounterChange{
		{Read: map[string]string{"score": "201"}, Set: map[string]string{"score": "200"}}, // a counter goes down
		{Read: map[string]string{"score": "201"}, Set: map[string]string{"streams": "2"}}, // a field written that was not guarded
		{Read: map[string]string{"score": "201"}},                                         // nothing written
		{Read: map[string]string{"score": "201"}, Set: map[string]string{"score": "2.5"}},
		{Read: map[string]string{"score": "201"}, Set: map[string]string{"score": "-3"}},
		{Read: map[string]string{"nonsense": "1"}, Set: map[string]string{"nonsense": "2"}},
		{Read: map[string]string{"id:": ""}, Set: map[string]string{"id:": "1"}},
		{Read: map[string]string{"score": "x"}, Set: map[string]string{"score": "300"}},
	}
	for i, c := range bad {
		if ref := refusedStep(t, tw, sprintReq(&SprintPart{Counter: c})); ref.Code != CodeRequest {
			t.Errorf("counter change %d: %v, want REQUEST", i, ref)
		}
	}
}

// TestPartSprintDroppingMarks (1.5.4, V6): a stream is marked with the op that
// freezes it, refused DROPPING when another op holds it; the same op marks it
// again harmlessly; an empty op unmarks it.
func TestPartSprintDroppingMarks(t *testing.T) {
	t.Parallel()
	tw, _, _, _ := partsTwin(t)
	mustStep(t, tw, sprintReq(&SprintPart{Dropping: map[string]string{"s1": "op-1", "s2": "op-1"}}))
	if h := tw.SprintKeys()[ek("dropping")].Hash; !reflect.DeepEqual(h, map[string]string{"s1": "op-1", "s2": "op-1"}) {
		t.Fatalf("marks %v", h)
	}
	ref := refusedStep(t, tw, sprintReq(&SprintPart{Dropping: map[string]string{"s3": "op-2", "s2": "op-2"}}))
	if ref.Code != CodeDropping || !reflect.DeepEqual(ref.Detail.IDs, []string{"s2"}) {
		t.Fatalf("a stream frozen by another op: %v (ids %v), want DROPPING naming s2", ref, ref.Detail.IDs)
	}
	if _, marked := tw.SprintKeys()[ek("dropping")].Hash["s3"]; marked {
		t.Fatal("the refused step marked s3")
	}
	mustStep(t, tw, sprintReq(&SprintPart{Dropping: map[string]string{"s1": "op-1"}}))
	reply := mustStep(t, tw, sprintReq(&SprintPart{Dropping: map[string]string{"s1": "", "s9": ""}}))
	if got := partReply(t, reply, PartSprint); !reflect.DeepEqual(got["unmarked"], []any{"s1"}) {
		t.Fatalf("reply %v; want only the mark that existed unmarked", got)
	}
	if h := tw.SprintKeys()[ek("dropping")].Hash; !reflect.DeepEqual(h, map[string]string{"s2": "op-1"}) {
		t.Fatalf("marks %v", h)
	}
	for _, bad := range []map[string]string{{"bad stream": "op"}, {"s1": "o\np"}, {"a.b": "op"}} {
		if ref := refusedStep(t, tw, sprintReq(&SprintPart{Dropping: bad})); ref.Code != CodeRequest {
			t.Errorf("marks %v: %v, want REQUEST", bad, ref)
		}
	}
}

// TestPartSprintCoordinatorAndUnwritten: the coordinator is one string key
// (1.3.1, F2-13). Goals and the sweep's position, which the part does not
// write, are refused REQUEST and never ignored.
func TestPartSprintCoordinatorAndUnwritten(t *testing.T) {
	t.Parallel()
	tw, _, _, _ := partsTwin(t)
	mustStep(t, tw, sprintReq(&SprintPart{Coordinator: "boss"}))
	if v := tw.SprintKeys()[sk("coordinator")]; v.Kind != kindString || v.String != "boss" {
		t.Fatalf("coordinator %+v", v)
	}
	mustStep(t, tw, sprintReq(&SprintPart{Coordinator: "other"}))
	if v := tw.SprintKeys()[sk("coordinator")]; v.String != "other" {
		t.Fatalf("coordinator %+v", v)
	}
	before := tw.SprintKeys()
	for _, sp := range []*SprintPart{{Goals: map[string]string{"p": "g"}}, {Sweep: "s3"}, {Coordinator: "a\tb"}} {
		if ref := refusedStep(t, tw, sprintReq(sp)); ref.Code != CodeRequest {
			t.Errorf("%+v: %v, want REQUEST", sp, ref)
		}
	}
	if len(changedKeys(before, tw.SprintKeys())) != 0 {
		t.Fatal("a refused sprint part wrote")
	}
}

// popCmds is the command list the pop part builds for a request.
func popCmds(t *testing.T, tw *Twin, clk *stepClock, req *Request) []Cmd {
	t.Helper()
	st := tw.partState(clk, req.Epoch)
	plan, ref := popPart{}.Pre(st, req, nil)
	if ref != nil {
		t.Fatalf("pop pre: %v", ref)
	}
	cmds, ref := popPart{}.Cmds(st, plan, LogPlan{})
	if ref != nil {
		t.Fatal(ref)
	}
	return cmds
}

// dueFixture seeds a due set and a cut set and a cursor at 12.
func dueFixture(tw *Twin, now int64) {
	seed(tw,
		Command("ZADD", ek("due"), kindZSet,
			strconv.FormatInt(now-300, 10), "untaken:p1.w1", strconv.FormatInt(now-200, 10), "unbegun:p2.r1.rd",
			strconv.FormatInt(now-100, 10), "beat:m1", strconv.FormatInt(now-50, 10), "seen:m2",
			strconv.FormatInt(now-40, 10), "overdue:n5", strconv.FormatInt(now-30, 10), "idle:s1",
			strconv.FormatInt(now-20, 10), "behind", strconv.FormatInt(now+60000, 10), "remind:ann"),
		Command("ZADD", ek("cut"), kindZSet, strconv.FormatInt(now-10, 10), "cut:op-7", strconv.FormatInt(now+60000, 10), "cut:op-8"),
		Command("HSET", ek("tick"), kindHash, "cur", "12"))
}

// TestPartPopKeysBeforeDueEntries (A1, 1.2, D3): the pop's command list adds
// every key to the agenda, scored by cur, before it removes a due or cut entry;
// the keys are named as 1.2 names them; a fault after the ZADDs leaves the
// entries, so the next pop adds the same keys and loses none; the reversed
// witness, the ZREMs first, loses them.
func TestPartPopKeysBeforeDueEntries(t *testing.T) {
	t.Parallel()
	setup := func() (*Twin, *stepClock) {
		tw, _, _, clk := partsTwin(t)
		mustStep(t, tw, leaseReq("token-a", "run", 600000, nil))
		dueFixture(tw, clk.ms())
		return tw, clk
	}

	tw, _ := setup()
	reply := mustStep(t, tw, popReq(1, 100))
	got := partReply(t, reply, PartPop)
	if got["popped"] != float64(8) || got["due"] != float64(7) || got["cut"] != float64(1) || got["skipped"] != false {
		t.Fatalf("pop reply %v", got)
	}
	agenda := tw.SprintKeys()[ek("agenda")].ZSet
	want := map[string]float64{"late:untaken:p1.w1": 12, "late:unbegun:p2.r1.rd": 12, "down:m1": 12, "seen:m2": 12,
		"overdue:n5": 12, "late:idle:s1": 12, "behind": 12, "late:cut:op-7": 12}
	if !reflect.DeepEqual(agenda, want) {
		t.Fatalf("agenda %v\nwant %v", agenda, want)
	}
	if due := tw.SprintKeys()[ek("due")].ZSet; len(due) != 1 || due["remind:ann"] == 0 {
		t.Fatalf("due %v: only the entry not yet due stays", due)
	}
	if cut := tw.SprintKeys()[ek("cut")].ZSet; len(cut) != 1 || cut["cut:op-8"] == 0 {
		t.Fatalf("cut %v", cut)
	}

	fw, fclk := setup()
	req := popReq(1, 100)
	cmds := popCmds(t, fw, fclk, req)
	seenRemove := false
	for i, c := range cmds {
		switch c.Argv[0] {
		case "ZREM":
			seenRemove = true
		case "ZADD":
			if seenRemove {
				t.Fatalf("command %d is a ZADD after a ZREM: %v", i, cmds)
			}
		default:
			t.Fatalf("command %d is %v", i, c.Argv)
		}
	}
	if !seenRemove || cmds[0].Argv[0] != "ZADD" || cmds[0].Argv[1] != ek("agenda") {
		t.Fatalf("commands %v, want the agenda's ZADD first", cmds)
	}
	var adds int
	for _, c := range cmds {
		if c.Argv[0] == "ZADD" {
			adds++
		}
	}
	fw.keys.apply(cmds[:adds]) // the fault: every key queued, no entry removed
	if due := fw.SprintKeys()[ek("due")].ZSet; len(due) != 8 {
		t.Fatalf("the fault removed entries: %v", due)
	}
	mustStep(t, fw, req) // the next pop adds the same keys again (none is new) and removes the entries
	if !reflect.DeepEqual(fw.SprintKeys()[ek("agenda")].ZSet, want) || len(fw.SprintKeys()[ek("due")].ZSet) != 1 {
		t.Fatalf("after the fault and the next pop: agenda %v", fw.SprintKeys()[ek("agenda")].ZSet)
	}

	rw, rclk := setup()
	rcmds := popCmds(t, rw, rclk, popReq(1, 100))
	rw.keys.apply(rcmds[adds:]) // the ZREMs first, then the fault before the ZADDs
	if len(rw.SprintKeys()[ek("agenda")].ZSet) != 0 || len(rw.SprintKeys()[ek("due")].ZSet) != 1 {
		t.Fatal("the witness did not lose the keys")
	}
}

// TestPartPopLimitNXAndStopped (1.2): the pop takes at most its limit in all,
// the cut entries first and then the due entries lowest first; a key already in
// the agenda keeps its earlier order; while STOPPED R stands still, so it pops
// what was due at the stop, and a cut entry, which counts wall time, still
// comes due.
func TestPartPopLimitNXAndStopped(t *testing.T) {
	t.Parallel()
	tw, _, _, clk := partsTwin(t)
	mustStep(t, tw, leaseReq("token-a", "run", 6000000, nil))
	now := clk.ms()
	dueFixture(tw, now)
	seed(tw, Command("ZADD", ek("agenda"), kindZSet, "3", "down:m1"))

	reply := mustStep(t, tw, popReq(1, 3))
	if got := partReply(t, reply, PartPop); got["popped"] != float64(3) || got["cut"] != float64(1) || got["due"] != float64(2) {
		t.Fatalf("a limit of 3: %v, want the one cut entry and the two lowest due", got)
	}
	agenda := tw.SprintKeys()[ek("agenda")].ZSet
	if !reflect.DeepEqual(agenda, map[string]float64{"late:cut:op-7": 12, "late:untaken:p1.w1": 12, "late:unbegun:p2.r1.rd": 12, "down:m1": 3}) {
		t.Fatalf("agenda %v", agenda)
	}
	// beat:m1 is next; its key is in the agenda at order 3, and stays there.
	mustStep(t, tw, popReq(1, 1))
	if a := tw.SprintKeys()[ek("agenda")].ZSet["down:m1"]; a != 3 {
		t.Fatalf("down:m1 moved to order %v; ZADD NX keeps the earliest", a)
	}
	if _, left := tw.SprintKeys()[ek("due")].ZSet["beat:m1"]; left {
		t.Fatal("the entry whose key was already queued was not removed")
	}

	// STOPPED: the clock stands still at the stop.
	mustStep(t, tw, &Request{Epoch: "0", Meta: Meta{Verb: "stop"}, Clock: &ClockPart{Verb: ClockStop}})
	clk.advance(2 * time.Hour)
	seed(tw, Command("ZADD", ek("due"), kindZSet, strconv.FormatInt(clk.ms(), 10), "hold:n9"),
		Command("ZADD", ek("cut"), kindZSet, strconv.FormatInt(clk.ms()-1, 10), "cut:op-9"))
	reply = mustStep(t, tw, popReq(1, 100))
	got := partReply(t, reply, PartPop)
	if got["cut"] != float64(2) || got["due"] != float64(4) {
		t.Fatalf("while STOPPED: %v; want both cut entries (wall time) and the four due at the stop", got)
	}
	due := tw.SprintKeys()[ek("due")].ZSet
	if _, ok := due["hold:n9"]; !ok {
		t.Fatal("an entry due only in wall time after the stop was popped while STOPPED")
	}
	if _, ok := due["remind:ann"]; !ok {
		t.Fatal("an entry due after the stop in running time was popped while STOPPED")
	}
	for _, k := range []string{"late:cut:op-8", "late:cut:op-9"} {
		if _, ok := tw.SprintKeys()[ek("agenda")].ZSet[k]; !ok {
			t.Fatalf("the cut entry's key %s was not queued", k)
		}
	}
	if got["r"] != strconv.FormatInt(now, 10) {
		t.Fatalf("R %v while STOPPED, want it still at the stop %d", got["r"], now)
	}
	if ref := refusedStep(t, tw, popReq(1, 0)); ref.Code != CodeRequest {
		t.Fatalf("a limit of 0: %v", ref)
	}
}

// TestPartPopKeyNames (1.2): the key a due entry queues.
func TestPartPopKeyNames(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		member string
		cut    bool
		key    string
	}{
		{"untaken:p1.w1", false, "late:untaken:p1.w1"}, {"unfinished:p1.w1", false, "late:unfinished:p1.w1"},
		{"unbegun:p1.r1.x", false, "late:unbegun:p1.r1.x"}, {"unreported:p1.r1.x", false, "late:unreported:p1.r1.x"},
		{"mergeidle:s1", false, "late:mergeidle:s1"}, {"idle:s1", false, "late:idle:s1"},
		{"overdue:n7", false, "overdue:n7"}, {"hold:n7", false, "hold:n7"}, {"remind:ann", false, "remind:ann"},
		{"behind", false, "behind"}, {"seen:m1", false, "seen:m1"}, {"beat:m1", false, "down:m1"},
		{"cut:op-1", true, "late:cut:op-1"}, {"surprise:x", false, "surprise:x"},
	} {
		if got := popKey(c.member, c.cut); got != c.key {
			t.Errorf("popKey(%q, %v) = %q, want %q", c.member, c.cut, got, c.key)
		}
	}
}

// TestPartClockStopStartKeepRunningTimeStill (A2, 1.2): stop, wait, stop,
// start: the second stop is refused MACHINESTATE and cannot move
// stopped_since_ms, and R, read by a pop at each point, has not moved across
// the whole span; start while RUNNING is refused; init writes a STOPPED clock
// and is refused over an existing one; clear runs only while STOPPED and
// leaves the clock.
func TestPartClockStopStartKeepRunningTimeStill(t *testing.T) {
	t.Parallel()
	tw, _, _, clk := partsTwin(t)
	mustStep(t, tw, leaseReq("token-a", "run", 6000000, nil))
	clock := func(verb string) *Request {
		return &Request{Epoch: "0", Meta: Meta{Verb: verb}, Clock: &ClockPart{Verb: verb}}
	}
	rNow := func() string { return partReply(t, mustStep(t, tw, popReq(1, 1)), PartPop)["r"].(string) }

	if ref := refusedStep(t, tw, clock(ClockStart)); ref.Code != CodeMachineState {
		t.Fatalf("start while RUNNING: %v", ref)
	}
	if ref := refusedStep(t, tw, clock(ClockClear)); ref.Code != CodeMachineState {
		t.Fatalf("clear while RUNNING: %v", ref)
	}
	clk.advance(5 * time.Second)
	t1 := clk.ms()
	got := partReply(t, mustStep(t, tw, clock(ClockStop)), PartClock)
	if got["running"] != false || got["r"] != strconv.FormatInt(t1, 10) || got["stopped_since_ms"] != strconv.FormatInt(t1, 10) {
		t.Fatalf("stop: %v", got)
	}
	rStill := rNow()
	clk.advance(30 * time.Second)
	before := tw.SprintKeys()
	if ref := refusedStep(t, tw, clock(ClockStop)); ref.Code != CodeMachineState || ref.Message != "the machine is already stopped, since "+strconv.FormatInt(t1, 10)+"; nothing was changed" {
		t.Fatalf("a second stop: %v", ref)
	}
	if len(changedKeys(before, tw.SprintKeys())) != 0 {
		t.Fatal("a refused stop changed the clock")
	}
	if rNow() != rStill {
		t.Fatal("R moved while STOPPED")
	}
	clk.advance(20 * time.Second)
	mustStep(t, tw, clock(ClockClear)) // STOPPED: allowed, changes nothing
	if len(changedKeys(before, tw.SprintKeys())) != 0 {
		t.Fatal("clear changed the clock")
	}
	// R17's bookkeeping of the span: start ends the span, so due_since_ms goes,
	// and the hold and the raised mark, which are kept for the next reader, stay.
	seed(tw, Command("HSET", sk("clock"), kindHash, "due_since_ms", "111", "stophold_ms", "222", "stopraised_ms", "333"))
	got = partReply(t, mustStep(t, tw, clock(ClockStart)), PartClock)
	if h := tw.SprintKeys()[sk("clock")].Hash; h["due_since_ms"] != "" || h["stophold_ms"] != "222" || h["stopraised_ms"] != "333" {
		t.Fatalf("clock after start: %v", h)
	}
	if got["running"] != true || got["r"] != rStill {
		t.Fatalf("start: %v; R must be where it stood (%s)", got, rStill)
	}
	if rNow() != rStill {
		t.Fatalf("R moved across stop, stop, start: %s then %s", rStill, rNow())
	}
	clk.advance(7 * time.Second)
	if want := strconv.FormatInt(mustAtoi(t, rStill)+7000, 10); rNow() != want {
		t.Fatalf("R after 7 s of running: %s, want %s", rNow(), want)
	}
	if ref := refusedStep(t, tw, clock(ClockStart)); ref.Code != CodeMachineState || ref.Message != "the machine is already running; nothing was changed" {
		t.Fatalf("a second start: %v", ref)
	}
	if ref := refusedStep(t, tw, clock("explode")); ref.Code != CodeRequest {
		t.Fatalf("an unknown verb: %v", ref)
	}
	if ref := refusedStep(t, tw, clock(ClockInit)); ref.Code != CodeMachineState {
		t.Fatalf("init over a clock: %v", ref)
	}

	fresh, _, _, fclk := partsTwin(t)
	got = partReply(t, mustStep(t, fresh, clock(ClockInit)), PartClock)
	if got["running"] != false || got["stopped_since_ms"] != strconv.FormatInt(fclk.ms(), 10) {
		t.Fatalf("init: %v; want STOPPED since the call", got)
	}
	if h := fresh.SprintKeys()[sk("clock")].Hash; h["stopped_since_ms"] != strconv.FormatInt(fclk.ms(), 10) || h["stopped_ms"] != "0" {
		t.Fatalf("clock hash %v", h)
	}
}

func mustAtoi(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

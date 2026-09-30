package sprintfn

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// X's tests run on the composed twin with the real X (twin_x_helpers_test.go).
// A refusal of X.pre is checked three ways: its code, its phase, and the whole
// image (the Mem's state, the sprint's keys and the log) equal to what it was
// before the step, so a refused step is shown to have written nothing.

// xNow is the call's time on every test twin, in ms.
var xNow = testTime.UnixMilli()

func xMS(n int64) string { return strconv.FormatInt(n, 10) }

// wantRefusal checks a refusal is code from X.pre and that the step left the
// twin's whole image as it was.
func (h *xh) wantRefusal(req *Request, code string) *Refusal {
	h.t.Helper()
	before := h.img()
	ref := h.refused(req)
	if ref.Code != code || ref.Phase != PhaseXPre {
		h.t.Fatalf("refused %s from %s (%s); want %s from %s", ref.Code, ref.Phase, ref.Message, code, PhaseXPre)
	}
	if !strings.HasSuffix(ref.Message, "nothing was changed") {
		h.t.Fatalf("the refusal's message %q does not say nothing was changed", ref.Message)
	}
	if h.img() != before {
		h.t.Fatalf("a %s refusal of X.pre changed the twin", code)
	}
	return ref
}

// applies sends a request and fails, naming what, unless it applied.
func (h *xh) applies(what string, req *Request) *StepReply {
	h.t.Helper()
	res, err := Step(context.Background(), h.tw, req)
	if err != nil || res.Refusal != nil || res.Err != nil || res.Step == nil {
		h.t.Fatalf("%s: result %+v, err %v; want it applied", what, res, err)
	}
	return res.Step
}

func xWithActor(req *Request, actor string) *Request {
	req.Meta.Actor = actor
	return req
}

// lease is the command that holds the lease at generation gen.
func xLease(gen string) Cmd {
	return Command("HSET", xp+"lease", kindHash, "owner", "tok", "name", "loop-a", "gen", gen, "until_ms", xMS(xNow+60_000))
}

func xClockCmd(fields ...string) Cmd { return Command("HSET", xp+"clock", kindHash, fields...) }

// stopped is the clock of a machine STOPPED for ago ms, after past ms stopped.
func xStopped(ago, past int64) Cmd {
	return xClockCmd("stopped_ms", xMS(past), "stopped_since_ms", xMS(xNow-ago))
}

// running is the clock of a machine RUNNING after past ms stopped.
func xRunning(past int64) Cmd { return xClockCmd("stopped_ms", xMS(past), "stopped_since_ms", "") }

func xMoveWaitingReady(id string) tset.Entry {
	return xMove(sprint.Work, "s1:waiting", "s1:ready", id, nil)
}

// TestXStaleGen: a tick step whose generation is not the lease's is refused
// STALEGEN, naming the generation and the holder, with nothing written; the
// current generation applies; a verb carries no generation; a step with a lease
// part is the lease's own and is not held to one; with no lease held, every
// generation is stale (T1).
func TestXStaleGen(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture()
	h.write(xLease("3"))

	ref := h.wantRefusal(xTick(2, xMoveWaitingReady("p1")), CodeStaleGen)
	if !strings.Contains(ref.Message, "generation 3") || !strings.Contains(ref.Message, "loop-a") {
		t.Fatalf("message %q does not name the generation and the holder", ref.Message)
	}
	h.wantRefusal(xTick(0, xMoveWaitingReady("p1")), CodeStaleGen)
	h.wantRefusal(xTick(4, xMoveWaitingReady("p1")), CodeStaleGen)
	h.applies("the current generation", xTick(3, xMoveWaitingReady("p1")))
	h.applies("a verb, which carries no generation", xVerb("rank", xMoveWaitingReady("p2")))
	h.applies("a step with a lease part", &Request{Epoch: "0", Meta: Meta{Tick: true, Gen: 1},
		Lease: &LeasePart{Owner: "tok2", Name: "loop-b", HoldMS: 5000}})
	if got := h.keys()[xp+"lease"].Hash["gen"]; got != "4" {
		t.Fatalf("the lease part's step left generation %s, want 4", got)
	}

	none := newXHarness(t)
	none.fixture()
	none.wantRefusal(xTick(1, xMoveWaitingReady("p1")), CodeStaleGen)
}

// TestXStoppedRefusesMovesAllowsNotes: while the machine is STOPPED a tick step
// that changes a table member (an entry, or an intent, which becomes one) is
// refused STOPPED; lease, pop, ingest and beat, steps of notes and sprint keys
// only, and verbs apply; after start the same move applies (T2).
func TestXStoppedRefusesMovesAllowsNotes(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture()
	h.write(xLease("1"), xStopped(5_000, 0))

	h.wantRefusal(xTick(1, xMoveWaitingReady("p1")), CodeStopped)
	intent := xTick(1)
	intent.Body.Intents = []Intent{{Kind: "needmet", Card: "p2", Need: "n0", Waiters: []string{"p2"}}}
	h.wantRefusal(intent, CodeStopped)

	notes := xTick(1)
	notes.Body.Notes = []NoteReq{{Op: "open", Type: "stopped", Cause: "moves due", Subjects: []string{"p1"}}}
	h.applies("a step of notes only", notes)
	sp := xTick(1)
	sp.Sprint = &SprintPart{Coordinator: "c1"}
	h.applies("a step of sprint keys only", sp)
	pop := xTick(1)
	pop.Pop = &PopPart{Limit: 10}
	h.applies("a pop", pop)
	ing := xTick(1)
	ing.Ingest = &IngestPart{From: "0", To: "0"}
	h.applies("an ingest", ing)
	beat := xTick(1)
	beat.Beat = &BeatPart{Members: []BeatMember{{Member: "m1"}}}
	h.applies("a beat", beat)
	h.applies("a lease renewal", &Request{Epoch: "0", Meta: Meta{Tick: true, Gen: 1}, Lease: &LeasePart{Owner: "tok", Name: "loop-a", HoldMS: 5000, Stopped: true}})
	h.applies("a verb's move", xVerb("rank", xMoveWaitingReady("p1")))

	h.applies("start", &Request{Epoch: "0", Meta: Meta{Verb: "start"}, Clock: &ClockPart{Verb: ClockStart}})
	h.write(xLease("2"))
	h.applies("the same tick move once RUNNING", xTick(2, xMoveWaitingReady("p2")))
}

// TestXDropping: a step touching a card of a stream being dropped is refused
// DROPPING, naming the stream and the cards, with nothing written, whether the
// card is in the stream's row (work, merge) or names the stream in a field
// (fleet, readers) and whether the step is a verb's or the tick's; the op's own
// parts apply, another op's and a stranger's do not; unfreezing lets the stream
// move again (1.3.5, 1.5.4).
func TestXDropping(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture()
	h.write(xLease("1"))
	h.applies("a work card of s1 in a member's cell, naming its stream",
		xVerb("rank", xCreate(sprint.Fleet, "m1:ready", "p1.w1", "1", map[string]string{"stream": "s1", "member": "m1"})))
	h.applies("the mark", &Request{Epoch: "0", Meta: Meta{Verb: "drop"}, Sprint: &SprintPart{Dropping: map[string]string{"s1": "op7"}}})

	for name, req := range map[string]*Request{
		"a verb's move of a card in the stream":         xVerb("rank", xMoveWaitingReady("p1")),
		"the tick's move of a card in the stream":       xTick(1, xMoveWaitingReady("p1")),
		"a create in the stream's row":                  xVerb("add", xCreate(sprint.Work, "s1:waiting", "p9", "9", nil)),
		"a move of a card naming the stream in a field": xVerb("take", xMove(sprint.Fleet, "m1:ready", "m1:working", "p1.w1", nil)),
		"a create naming the stream in a field":         xVerb("add", xCreate(sprint.Fleet, "m1:ready", "p2.w1", "2", map[string]string{"stream": "s1"})),
		"a create of the stream's merge card":           xVerb("add", xCreate(sprint.Merge, "s1:queued", "p2", "2", nil)),
	} {
		ref := h.wantRefusal(req, CodeDropping)
		if len(ref.Detail.Rows) != 1 || ref.Detail.Rows[0] != "s1" || len(ref.Detail.IDs) == 0 {
			t.Fatalf("%s: detail %+v does not name the stream and its cards", name, ref.Detail)
		}
	}
	other := xVerb("add", xCreate(sprint.Work, "s2:waiting", "q1", "5", nil))
	h.applies("a card of another stream", other)

	mine := xVerb("drop", xMove(sprint.Work, "s1:waiting", "s1:ready", "p2", nil))
	mine.Body.Op = &Op{ID: "op7/p2", Intent: "drop s1"}
	h.applies("the op's own part", mine)
	for _, id := range []string{"op70/p1", "op7/x", "op7/p", "op7", "op/p1", "op7/p1/p2"} {
		req := xVerb("drop", xMove(sprint.Work, "s1:waiting", "s1:ready", "p1", nil))
		req.Body.Op = &Op{ID: id, Intent: "drop s1"}
		h.wantRefusal(req, CodeDropping)
	}
	abort := xVerb("drop", xMove(sprint.Work, "s1:waiting", "s1:ready", "p1", nil))
	abort.Body.Op = &Op{ID: "op7/abort", Intent: "abort"}
	abort.Sprint = &SprintPart{Dropping: map[string]string{"s1": ""}}
	h.applies("the op's abort, which unfreezes", abort)
	h.applies("a move once unfrozen", xVerb("rank", xMove(sprint.Work, "s1:ready", "s1:working", "p1", nil)))
}

// TestXNotCoord: a coordinator's verb from any actor but {p}coordinator is
// refused NOTCOORD with nothing written, and so is one when no coordinator is
// set; the verbs that are not the coordinator's are not checked (1.5.3, 2.2).
func TestXNotCoord(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture()
	h.wantRefusal(xWithActor(xVerb("release", xMoveWaitingReady("p1")), "anyone"), CodeNotCoord)
	h.applies("a verb that is not the coordinator's, with no coordinator set", xWithActor(xVerb("rank", xMoveWaitingReady("p1")), "anyone"))

	h.applies("init --coordinator", &Request{Epoch: "0", Meta: Meta{Verb: "init"}, Sprint: &SprintPart{Coordinator: "c1"}})
	for _, v := range []string{"release", "ack", "wait", "accept", "rework"} {
		ref := h.wantRefusal(xWithActor(xVerb(v, xMoveWaitingReady("p2")), "intruder"), CodeNotCoord)
		if !strings.Contains(ref.Message, v) {
			t.Fatalf("message %q does not name the verb %s", ref.Message, v)
		}
		h.wantRefusal(xWithActor(xVerb(v, xMoveWaitingReady("p2")), ""), CodeNotCoord)
	}
	h.applies("release by the coordinator", xWithActor(xVerb("release", xMoveWaitingReady("p2")), "c1"))
	h.applies("a worker's verb by anyone", xWithActor(xVerb("take", xMove(sprint.Work, "s1:ready", "s1:working", "p3", nil)), "intruder"))
}

// xCounterStep is a verb's step that carries a counter change and one entry.
func xCounterStep(read, set map[string]string, entries ...tset.Entry) *Request {
	r := xVerb("add", entries...)
	r.Sprint = &SprintPart{Counter: &CounterChange{Read: read, Set: set}}
	return r
}

// TestXCounter: a step planned on a counter that has moved is refused COUNTER
// with nothing written; one that read it as it is applies and the counter takes
// its new value; a field that was absent when read and is set now is a move (U2).
func TestXCounter(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture()
	create := func(id, score string) tset.Entry { return xCreate(sprint.Work, "s1:waiting", id, score, nil) }

	h.wantRefusal(xCounterStep(map[string]string{"score": "999"}, map[string]string{"score": "1005"}, create("a1", "1000")), CodeCounter)
	h.wantRefusal(xCounterStep(map[string]string{"score": "1001"}, map[string]string{"score": "1005"}, create("a1", "1000")), CodeCounter)
	h.wantRefusal(xCounterStep(map[string]string{"id:s1": "7"}, map[string]string{"id:s1": "8"}, create("a1", "5")), CodeCounter)
	h.applies("a counter as read", xCounterStep(map[string]string{"score": "1000", "id:s1": ""}, map[string]string{"score": "1005", "id:s1": "1"}, create("a1", "1000")))
	if got := h.keys()[xp+"next@0"].Hash; got["score"] != "1005" || got["id:s1"] != "1" {
		t.Fatalf("the counter after the step is %v", got)
	}
	h.wantRefusal(xCounterStep(map[string]string{"score": "1000"}, map[string]string{"score": "1010"}, create("a2", "1001")), CodeCounter)
}

// TestXStreamsCounter: R15's step guards the stream set's counter: a stream
// added or removed since the read (the counter's streams field moved) refuses the
// step COUNTER, a race, with nothing written; with the counter as read it applies
// (1.3.1, R15).
func TestXStreamsCounter(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture() // streams is "1"
	done := func(streams string) *Request {
		r := xVerb("done", tset.Entry{Kind: "rcount", Table: sprint.Work, Cells: []string{"s2:waiting", "s2:ready", "s2:working", "s2:review", "s2:merging"},
			ScoreMin: "-inf", ScoreMax: "+inf", AtMost: func() *uint64 { n := uint64(0); return &n }()})
		r.Sprint = &SprintPart{Counter: &CounterChange{Read: map[string]string{"streams": streams}}}
		return r
	}
	h.wantRefusal(done("2"), CodeCounter)
	h.wantRefusal(done("0"), CodeCounter)
	h.applies("the streams counter as read", done("1"))
	h.applies("a stream added", xCounterStep(map[string]string{"streams": "1"}, map[string]string{"streams": "2"},
		tset.Entry{Kind: "rows", Table: sprint.Work, Add: []string{"s3"}}, xCreate(sprint.Work, "s3:waiting", "q1", "5", nil)))
	h.wantRefusal(done("1"), CodeCounter)
	h.applies("the counter as it is now", done("2"))
}

// TestXRankRaisesCounter: a score placed at or above the score counter, and not
// raised past by the same step, is refused COUNTER (W17: a rank above the counter
// without raising it would break UniqueScores); raised past in the same step it
// applies and the counter ends above the score; a score below it needs nothing;
// only the work table's scores are held to the counter (1.3.1, U2).
func TestXRankRaisesCounter(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture() // the counter is 1000
	rank := func(id, score string) tset.Entry {
		return tset.Entry{Kind: "move", Table: sprint.Work, From: "s1:waiting", IDs: []string{id}, Scores: []string{score}, About: []string{id}}
	}

	h.wantRefusal(xVerb("rank", rank("p1", "1500")), CodeCounter)
	h.wantRefusal(xVerb("rank", rank("p1", "1000")), CodeCounter)
	ref := h.wantRefusal(xVerb("rank", rank("p1", "1500"), rank("p2", "3")), CodeCounter)
	if len(ref.Detail.IDs) != 1 || ref.Detail.IDs[0] != "p1" {
		t.Fatalf("detail %+v does not name the card over the counter", ref.Detail)
	}
	h.applies("a score below the counter, not an integer", xVerb("rank", rank("p1", "2.5")))

	raise := xVerb("rank", rank("p1", "1500"))
	raise.Sprint = &SprintPart{Counter: &CounterChange{Read: map[string]string{"score": "1000"}, Set: map[string]string{"score": "1501"}}}
	h.applies("a score raised past in the same step", raise)
	if got := h.keys()[xp+"next@0"].Hash["score"]; got != "1501" {
		t.Fatalf("the counter is %s after the rank, want 1501", got)
	}
	equal := xVerb("rank", rank("p2", "1600"))
	equal.Sprint = &SprintPart{Counter: &CounterChange{Read: map[string]string{"score": "1501"}, Set: map[string]string{"score": "1600"}}}
	h.wantRefusal(equal, CodeCounter) // the counter must end above the score, not at it

	h.applies("a score of another table", xVerb("rank", xCreate(sprint.Fleet, "m1:ready", "p1.w1", "99999", nil)))

	bare := newXHarness(t)
	bare.do(xVerb("seed", tset.Entry{Kind: "rows", Table: sprint.Work, Add: []string{"s1"}}))
	ref = bare.wantRefusal(xVerb("add", xCreate(sprint.Work, "s1:waiting", "p1", "1", nil)), CodeCounter)
	if !strings.Contains(ref.Message, "no score counter") {
		t.Fatalf("message %q", ref.Message)
	}
}

// TestXMemberUpGuard: a deal to a member that has been marked down since the
// plan read it is refused XGUARD with nothing written; a member that is up, and
// the guard on it, apply (2.3: X's memberup).
func TestXMemberUpGuard(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture()
	deal := func(m string) *Request {
		r := xVerb("deal", xCreate(sprint.Fleet, m+":ready", "p3.w1", "3", map[string]string{"stream": "s1", "member": m}))
		r.Body.Guards = []XGuard{{Kind: XGuardMemberUp, Member: m}}
		return r
	}
	h.wantRefusal(deal("m2"), CodeXGuard)
	h.wantRefusal(deal("m9"), CodeXGuard)
	flip := func(status string) {
		h.t.Helper()
		h.applies("a member's status", xVerb("rank", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ctl", IDs: []string{"ctl-m1"},
			Set: map[string]string{"status": status}, About: []string{"ctl-m1"}}))
	}
	flip("down") // marked down since the read
	h.wantRefusal(deal("m1"), CodeXGuard)
	flip("held")
	h.wantRefusal(deal("m1"), CodeXGuard)
	flip("up")
	h.applies("a deal to a member that is up", deal("m1"))
}

// TestXBeatStaleGuardOnDueSet: R2's guard reads the due set: a beat since the
// read has moved beat:<m> to R + 15 s, above R, and the step is refused XGUARD
// with nothing written; with no beat entry, or one at or below R, it applies;
// while STOPPED R does not move, so the entry is compared with the frozen R (2.3,
// R2; 1.4.4).
func TestXBeatStaleGuardOnDueSet(t *testing.T) {
	t.Parallel()
	down := func() *Request {
		r := xTick(1, tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ctl", IDs: []string{"ctl-m1"},
			Set: map[string]string{"status": "down"}, About: []string{"ctl-m1"}})
		r.Body.Guards = []XGuard{{Kind: XGuardBeatStale, Member: "m1"}}
		return r
	}
	beat := func(score int64) Cmd { return Command("ZADD", xp+"due@0", kindZSet, xMS(score), "beat:m1") }

	h := newXHarness(t)
	h.fixture()
	h.write(xLease("1"), xRunning(0))
	h.write(beat(xNow + 15_000))
	ref := h.wantRefusal(down(), CodeXGuard)
	if !strings.Contains(ref.Message, "m1") {
		t.Fatalf("message %q does not name the member", ref.Message)
	}
	h.write(beat(xNow + 1))
	h.wantRefusal(down(), CodeXGuard)
	h.write(beat(xNow))
	h.applies("a beat entry at R", down())

	absent := newXHarness(t)
	absent.fixture()
	absent.write(xLease("1"), xRunning(0))
	absent.applies("no beat entry", down())

	frozen := newXHarness(t)
	frozen.fixture()
	frozen.write(xLease("1"), xStopped(1_000, 200)) // R = xNow - 1000 - 200, and it does not move
	guardOnly := xTick(1)
	guardOnly.Body.Guards = []XGuard{{Kind: XGuardBeatStale, Member: "m1"}}
	frozen.write(beat(xNow - 1_000)) // above the frozen R, below the wall clock
	frozen.wantRefusal(guardOnly, CodeXGuard)
	frozen.write(beat(xNow - 1_200))
	frozen.applies("a beat entry at the frozen R", guardOnly)
}

// xQuarantineStep is the tick's step that quarantines cards (1.3.5): no entry
// names them, the sprint part writes the marks, X takes the ids out of the
// indexes and J opens the judgment.
func xQuarantineStep(gen uint64, ids map[string]string) *Request {
	r := xTick(gen)
	var subjects []string
	for id, stream := range ids {
		r.Body.Quarantine = append(r.Body.Quarantine, Quarantined{ID: id, Stream: stream, Code: "DRIFT", Rule: "resolve", Cells: []string{stream + ":waiting"}})
		subjects = append(subjects, id)
	}
	r.Body.Notes = []NoteReq{{Op: "open", Type: "invariant", Cause: "drift", Subjects: subjects}}
	r.Sprint = &SprintPart{Quarantine: append([]Quarantined(nil), r.Body.Quarantine...)}
	return r
}

// TestXQuarantineWithoutEntry: a card whose record and cell disagree is refused
// DRIFT by Layer 1 in every step that names it; the quarantine step, which has no
// entry on it, applies: each id leaves elig, fresh, again and askwait, a sentinel
// stays in sent, the mark is written and the judgment opens (J's half is IT15's:
// its stand-in opens it here) (1.3.5).
func TestXQuarantineWithoutEntry(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture()
	h.do(xVerb("seed", xCreate(sprint.Work, "s1:ready", "p4", "5", map[string]string{"kind": "primary", "attempt": "1"})))
	h.write(xLease("1"), Command("ZADD", xp+"askwait@0", kindZSet, "5", "p1"))
	for _, want := range []struct{ key, member string }{{"elig:s1@0", "p1"}, {"fresh:s1@0", "p3"}, {"again:s1@0", "p4"}, {"sent:s1@0", "g1"}, {"askwait@0", "p1"}} {
		if _, ok := h.zset(want.key)[want.member]; !ok {
			t.Fatalf("before the quarantine %s lacks %s: %v", want.key, want.member, h.zset(want.key))
		}
	}
	for _, c := range []struct{ table, row, col, id string }{{sprint.Work, "s1", "waiting", "p1"}, {sprint.Work, "s1", "ready", "p3"},
		{sprint.Work, "s1", "ready", "p4"}, {sprint.Work, "s1", "waiting", "g1"}} {
		if err := h.mem.CorruptCell(testPrefix, c.table, "0", c.row, c.col, c.id, "77"); err != nil {
			t.Fatal(err)
		}
	}
	before := h.img()
	ref := h.refused(xTick(1, xMove(sprint.Work, "s1:waiting", "s1:ready", "p1", nil)))
	if ref.Code != "DRIFT" || h.img() != before {
		t.Fatalf("a step naming the corrupted card: %s from %s, image changed %v; want DRIFT with nothing written", ref.Code, ref.Phase, h.img() != before)
	}

	h.applies("the quarantine step", xQuarantineStep(1, map[string]string{"p1": "s1", "p3": "s1", "p4": "s1", "g1": "s1"}))
	for _, gone := range []struct{ key, member string }{{"elig:s1@0", "p1"}, {"fresh:s1@0", "p3"}, {"again:s1@0", "p4"}, {"askwait@0", "p1"}} {
		if _, ok := h.zset(gone.key)[gone.member]; ok {
			t.Fatalf("%s still holds %s after the quarantine: %v", gone.key, gone.member, h.zset(gone.key))
		}
	}
	if _, ok := h.zset("sent:s1@0")["g1"]; !ok {
		t.Fatalf("the quarantined sentinel left sent:s1: %v", h.zset("sent:s1@0"))
	}
	if _, ok := h.zset("elig:s1@0")["p2"]; !ok {
		t.Fatalf("a card that was not quarantined left elig:s1: %v", h.zset("elig:s1@0"))
	}
	q := h.keys()[xp+"quarantine@0"].Hash
	if len(q) != 4 || q["p1"] != "DRIFT" {
		t.Fatalf("the quarantine key holds %v", q)
	}
	if got := h.keys()[xp+"jopen:p1@0"].Hash["invariant|drift"]; !strings.HasPrefix(got, "n") {
		t.Fatalf("no judgment opened on p1: %v", h.keys()[xp+"jopen:p1@0"])
	}
}

// TestXQuarantinedGetsNoMembership: a quarantined card that changes is given no
// index membership but sent (1.3.2; I1): a quarantined ready card never enters
// fresh again, one that leaves waiting is not added to elig, its due entries are
// not derived; a quarantined sentinel keeps its sent membership, re-scored, and
// leaves it only by leaving waiting; a card that is not quarantined is derived as
// ever.
func TestXQuarantinedGetsNoMembership(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture()
	h.write(xLease("1"))
	h.applies("the quarantine", xQuarantineStep(1, map[string]string{"p3": "s1", "g1": "s1"}))

	h.applies("p3 goes to working", xVerb("take", xMove(sprint.Work, "s1:ready", "s1:working", "p3", map[string]string{"attempt": "1"})))
	h.applies("p3 back to ready", xVerb("rank", xMove(sprint.Work, "s1:working", "s1:ready", "p3", map[string]string{"attempt": "0"})))
	h.applies("p3 to waiting", xVerb("rank", xMove(sprint.Work, "s1:ready", "s1:waiting", "p3", map[string]string{"open": "0"})))
	for _, key := range []string{"elig:s1@0", "fresh:s1@0", "again:s1@0"} {
		if _, ok := h.zset(key)["p3"]; ok {
			t.Fatalf("the quarantined p3 is in %s: %v", key, h.zset(key))
		}
	}

	h.applies("g1 re-scored", xVerb("rank", tset.Entry{Kind: "move", Table: sprint.Work, From: "s1:waiting", IDs: []string{"g1"}, Scores: []string{"4.5"}, About: []string{"g1"}}))
	if got, ok := h.zset("sent:s1@0")["g1"]; !ok || got != 4.5 {
		t.Fatalf("the quarantined sentinel is in sent:s1 at %v (%v); want it there at 4.5", got, ok)
	}
	h.applies("p1 moves as ever", xVerb("rank", xMoveWaitingReady("p1")))
	if _, ok := h.zset("fresh:s1@0")["p1"]; !ok {
		t.Fatalf("a card that is not quarantined did not enter fresh:s1: %v", h.zset("fresh:s1@0"))
	}
	h.applies("g1 lands", xVerb("merge", xMove(sprint.Work, "s1:waiting", "s1:landed", "g1", nil)))
	if _, ok := h.zset("sent:s1@0")["g1"]; ok {
		t.Fatalf("a quarantined sentinel that landed is still in sent:s1")
	}
}

// TestXGuardKinds: each kind of XGuard holds the step to the key it names, as the
// plan read it, and refuses XGUARD with nothing written when the key has moved: a
// due entry's score (an absent entry too, and a cut entry, which is read in the
// wall-time set), a hold, a clock field, the coordinator, a stranger already
// noticed (2.3: R1, R11, R13, R14, R17; 3: init --coordinator). A guard of a kind
// the design does not have, or without what its kind names, is REQUEST.
func TestXGuardKinds(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture()
	h.write(xLease("1"), xRunning(300),
		Command("ZADD", xp+"due@0", kindZSet, "5000", "remind:alice"),
		Command("ZADD", xp+"cut@0", kindZSet, "7000", "cut:op1"),
		Command("HSET", xp+"jopen:p1@0", kindHash, "stalled|slow", "h42", "blocked|n3", "n9"),
		Command("SET", xp+"coordinator", kindString, "c1"),
		Command("HSET", xp+"strangers", kindHash, "m8", "1000", "m9", "noticed"))
	guard := func(g ...XGuard) *Request {
		r := xTick(1)
		r.Body.Guards = g
		return r
	}
	cases := []struct {
		name  string
		ok    []XGuard
		moved []XGuard
	}{
		{"a due entry", []XGuard{{Kind: XGuardDue, Key: "remind:alice", Score: 5000}},
			[]XGuard{{Kind: XGuardDue, Key: "remind:alice", Score: 4999}, {Kind: XGuardDue, Key: "remind:bob", Score: 5000}}},
		{"an absent due entry", []XGuard{{Kind: XGuardDue, Key: "remind:bob", Score: XGuardAbsent}},
			[]XGuard{{Kind: XGuardDue, Key: "remind:alice", Score: XGuardAbsent}}},
		{"a cut entry, in wall time", []XGuard{{Kind: XGuardDue, Key: "cut:op1", Score: 7000}},
			[]XGuard{{Kind: XGuardDue, Key: "cut:op1", Score: 5000}, {Kind: XGuardDue, Key: "cut:op2", Score: 7000}}},
		{"a hold", []XGuard{{Kind: XGuardHold, Member: "p1", Key: "stalled|slow=h42"}},
			[]XGuard{{Kind: XGuardHold, Member: "p1", Key: "stalled|slow=h43"}, {Kind: XGuardHold, Member: "p1", Key: "blocked|n3=hn9"},
				{Kind: XGuardHold, Member: "p2", Key: "stalled|slow=h42"}}},
		{"a clock field", []XGuard{{Kind: XGuardClock, Key: "stopped_ms", Score: 300}, {Kind: XGuardClock, Key: "stopped_since_ms", Score: XGuardAbsent}},
			[]XGuard{{Kind: XGuardClock, Key: "stopped_ms", Score: 0}, {Kind: XGuardClock, Key: "stopped_since_ms", Score: 100}, {Kind: XGuardClock, Key: "due_since_ms", Score: 1}}},
		{"the coordinator", []XGuard{{Kind: XGuardCoordinator, Member: "c1"}},
			[]XGuard{{Kind: XGuardCoordinator, Member: "c2"}, {Kind: XGuardCoordinator, Member: ""}}},
		{"a stranger", []XGuard{{Kind: XGuardStranger, Member: "m8"}, {Kind: XGuardStranger, Member: "m7"}},
			[]XGuard{{Kind: XGuardStranger, Member: "m9"}}},
	}
	for _, c := range cases {
		h.applies(c.name+" as read", guard(c.ok...))
		for _, g := range c.moved {
			ref := h.wantRefusal(guard(g), CodeXGuard)
			if !strings.Contains(ref.Message, "XGUARD") {
				t.Fatalf("%s: message %q", c.name, ref.Message)
			}
		}
	}
	for _, bad := range []XGuard{{Kind: "nope", Member: "m1"}, {Kind: XGuardMemberUp}, {Kind: XGuardBeatStale}, {Kind: XGuardStranger},
		{Kind: XGuardDue, Score: 1}, {Kind: XGuardDue, Key: "a:b", Score: -5}, {Kind: XGuardHold, Member: "p1", Key: "noequals"}, {Kind: XGuardHold, Key: "a=b"},
		{Kind: XGuardClock, Key: "not_a_field", Score: 1}, {Kind: XGuardClock, Key: "stopped_ms", Score: -2}} {
		h.wantRefusal(guard(bad), CodeRequest)
	}
}

// TestXSentAndCounterGuardsAgree: a sent guard holds the step to no sentinel of
// its stream placed at or below its max, as S.zguard(sent:<s>, rcount, -inf,
// max, atmost 0) (2.3, R3 and R6), an open bound excluding the max; a counter
// guard holds it to a field of {p}next@e as read, 0 for a field absent (2.3,
// R15; errata 3 H14). Each refuses XGUARD with nothing written when the key has
// moved, a sent key of another type WRONGTYPE, a counter that is no whole
// number CONFIG, and a malformed guard REQUEST; the Lua half agrees on each (the
// harness runs it beside the twin), its bound grammar included.
func TestXSentAndCounterGuardsAgree(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture()
	h.write(xLease("1"), xRunning(300),
		Command("ZADD", xp+"sent:s1@0", kindZSet, "20.5", "g1", "40", "g2"),
		Command("HSET", xp+"sent:bad@0", kindHash, "x", "1"))
	guard := func(g ...XGuard) *Request {
		r := xTick(1)
		r.Body.Guards = g
		return r
	}
	h.write(Command("HDEL", xp+"next@0", kindHash, "streams"))
	h.applies("a counter field absent reads 0", guard(XGuard{Kind: XGuardCounter, Key: "streams", Score: 0}))
	h.wantRefusal(guard(XGuard{Kind: XGuardCounter, Key: "streams", Score: 1}), CodeXGuard)
	h.write(Command("HSET", xp+"next@0", kindHash, "streams", "3"))
	for _, ok := range [][]XGuard{
		{{Kind: XGuardSent, Key: "sent:s1 20.4"}, {Kind: XGuardSent, Key: "sent:s1 (20.5"}, {Kind: XGuardSent, Key: "sent:s9 1e+21"}},
		{{Kind: XGuardSent, Key: "sent:s1 -3"}, {Kind: XGuardSent, Key: "sent:s1 2.04e1"}},
		{{Kind: XGuardCounter, Key: "streams", Score: 3}, {Kind: XGuardCounter, Key: "score", Score: 1000}},
	} {
		h.applies(fmt.Sprintf("%+v", ok), guard(ok...))
	}
	for _, moved := range []XGuard{
		{Kind: XGuardSent, Key: "sent:s1 20.5"}, {Kind: XGuardSent, Key: "sent:s1 (20.6"}, {Kind: XGuardSent, Key: "sent:s1 1e2"},
		{Kind: XGuardCounter, Key: "streams", Score: 2}, {Kind: XGuardCounter, Key: "streams", Score: 0}, {Kind: XGuardCounter, Key: "score", Score: 1},
	} {
		if ref := h.wantRefusal(guard(moved), CodeXGuard); !strings.Contains(ref.Message, "XGUARD") {
			t.Fatalf("%+v: message %q", moved, ref.Message)
		}
	}
	h.wantRefusal(guard(XGuard{Kind: XGuardSent, Key: "sent:bad 1"}), CodeWrongType)
	for _, bad := range []XGuard{{Kind: XGuardSent}, {Kind: XGuardSent, Key: "sent:s1"}, {Kind: XGuardSent, Key: "s1 1"},
		{Kind: XGuardSent, Key: "sent: 1"}, {Kind: XGuardSent, Key: "sent:s1 +inf"}, {Kind: XGuardSent, Key: "sent:s1 1e100"},
		{Kind: XGuardSent, Key: "sent:s1 0x10"}, {Kind: XGuardSent, Key: "sent:s1 1."}, {Kind: XGuardSent, Key: "sent:s1  1"},
		{Kind: XGuardSent, Key: "sent:s 1 1"}, {Kind: XGuardSent, Key: "sent:s@0 1"},
		{Kind: XGuardCounter, Key: "id", Score: 1}, {Kind: XGuardCounter, Key: "streams", Score: -1}, {Kind: XGuardCounter}} {
		h.wantRefusal(guard(bad), CodeRequest)
	}
	h.write(Command("HSET", xp+"next@0", kindHash, "streams", "03"))
	h.wantRefusal(guard(XGuard{Kind: XGuardCounter, Key: "streams", Score: 3}), CodeConfig)

	// due as the time rules read it (entryAsRead: 2.3 R11's cut clock, R14,
	// R18): the entry at its score as read, or absent after the pop; one armed
	// again since, or popped since, refuses
	h.write(Command("ZADD", xp+"due@0", kindZSet, "5000", "remind:alice"), Command("ZADD", xp+"cut@0", kindZSet, "7000", "cut:op1"))
	h.applies("as read, or absent", guard(XGuard{Kind: XGuardDue, Key: "remind:alice", Score: 5000},
		XGuard{Kind: XGuardDue, Key: "remind:bob", Score: XGuardAbsent}, XGuard{Kind: XGuardDue, Key: "cut:op1", Score: 7000}))
	for _, moved := range []XGuard{{Kind: XGuardDue, Key: "remind:alice", Score: XGuardAbsent}, {Kind: XGuardDue, Key: "remind:alice", Score: 4999},
		{Kind: XGuardDue, Key: "cut:op1", Score: 9000}, {Kind: XGuardDue, Key: "remind:bob", Score: 1}} {
		h.wantRefusal(guard(moved), CodeXGuard)
	}
	for _, bad := range []XGuard{{Kind: XGuardDue, Score: 1}, {Kind: XGuardDue, Key: "a", Score: -2}, {Kind: "dueatmost", Key: "a", Score: 1}} {
		h.wantRefusal(guard(bad), CodeRequest)
	}
	if h.mirror.pre == 0 {
		t.Fatal("the Lua half was not compared")
	}
}

// TestXMachineState: a stop on a STOPPED machine, a start on a RUNNING one and a
// clear on one that is not STOPPED are refused MACHINESTATE with nothing written,
// and so a second stop can never move stopped_since_ms (A2); each verb applies in
// the state it is for, and the sequence stop, start, stop, start goes through.
func TestXMachineState(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.write(xRunning(0))
	clock := func(v string) *Request {
		return &Request{Epoch: "0", Meta: Meta{Verb: v}, Clock: &ClockPart{Verb: v}}
	}
	h.wantRefusal(clock(ClockStart), CodeMachineState)
	h.wantRefusal(clock(ClockClear), CodeMachineState)
	h.applies("stop", clock(ClockStop))
	since := h.keys()[xp+"clock"].Hash["stopped_since_ms"]
	if since != xMS(xNow) {
		t.Fatalf("stopped_since_ms is %q after the stop, want %d", since, xNow)
	}
	ref := h.wantRefusal(clock(ClockStop), CodeMachineState)
	if !strings.Contains(ref.Message, "already stopped") {
		t.Fatalf("message %q", ref.Message)
	}
	if got := h.keys()[xp+"clock"].Hash["stopped_since_ms"]; got != since {
		t.Fatalf("a second stop moved stopped_since_ms from %s to %s", since, got)
	}
	h.applies("a clear while STOPPED", clock(ClockClear))
	h.applies("start", clock(ClockStart))
	ref = h.wantRefusal(clock(ClockStart), CodeMachineState)
	if !strings.Contains(ref.Message, "already running") {
		t.Fatalf("message %q", ref.Message)
	}
	h.applies("stop again", clock(ClockStop))
	h.applies("start again", clock(ClockStart))
	h.applies("init needs no state", clock(ClockInit))
}

// TestXAgendaEdits: X adds the requeued keys and then removes the done keys, in
// that order (A1: owed work before the trigger is forgotten); a requeued key that
// is queued keeps its order and is not written; one that is not (a continuation
// with an offset) is added at its line's seq; a held rule's keys are in the held
// queue; a done key the sprint part parks is left to that part; a requeued key
// with no queue entry and no line has no order and is REQUEST (1.1, 1.3.6).
func TestXAgendaEdits(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture()
	h.write(xLease("1"),
		Command("ZADD", xp+"agenda@0", kindZSet, "10", "ask@48213", "11", "deal", "12", "resolve:s1", "13", "needs:n1"),
		Command("ZADD", xp+"heldq@0", kindZSet, "20", "held@50", "21", "held:p3"))
	req := xTick(1)
	req.Body.Done = []string{"ask@48213", "resolve:s1", "held@50", "needs:n1", "needs:n1", "absent-key"}
	req.Body.Requeue = []string{"ask@48213+2000", "deal", "held@50+2000"}
	req.Sprint = &SprintPart{Park: []ParkedKey{{Key: "needs:n1", Code: "LIMIT"}}}
	h.applies("the edits", req)

	agenda, held := h.zset("agenda@0"), h.zset("heldq@0")
	if len(agenda) != 3 || agenda["ask@48213+2000"] != 48213 || agenda["deal"] != 11 || agenda["needs:n1"] != 13 {
		t.Fatalf("the agenda after the step is %v; want ask@48213+2000 at 48213, deal kept at 11, needs:n1 left to the part at 13", agenda)
	}
	if len(held) != 2 || held["held@50+2000"] != 50 || held["held:p3"] != 21 {
		t.Fatalf("the held queue after the step is %v; want held@50+2000 at 50 and held:p3 kept", held)
	}
	// A1 across the list: no ZREM of a done key before the ZADD of a requeued one.
	lastAdd, firstRem := -1, len(h.last)
	for i, c := range h.last {
		if c.Argv[1] != xp+"agenda@0" && c.Argv[1] != xp+"heldq@0" {
			continue
		}
		if c.Argv[0] == "ZADD" {
			lastAdd = i
		} else if c.Argv[0] == "ZREM" && i < firstRem {
			firstRem = i
		}
	}
	if lastAdd < 0 || firstRem == len(h.last) || lastAdd > firstRem {
		t.Fatalf("the agenda's commands are out of A1's order (last ZADD at %d, first ZREM at %d): %v", lastAdd, firstRem, h.last)
	}

	nameless := xTick(1)
	nameless.Body.Requeue = []string{"ask:p9+10"}
	h.wantRefusal(nameless, CodeRequest)
}

// TestXDueDerivation: the card kinds of the due set are derived like indexes
// (1.2): a work card dealt and not taken has untaken:<card>, taken has
// unfinished, a read card asked has unbegun and begun unreported, a stream's
// control card mergeidle:<stream>; each leaves when its state ends or its field is
// unset, and is re-scored when the field changes; a card in no timed state has
// none.
func TestXDueDerivation(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture()
	due := func() map[string]float64 { return h.zset("due@0") }
	h.do(xVerb("deal", xCreate(sprint.Fleet, "m1:ready", "p3.w1", "3", map[string]string{"due_untaken": "9000"})))
	if got := due(); len(got) != 1 || got["untaken:p3.w1"] != 9000 {
		t.Fatalf("due after the deal is %v", got)
	}
	h.do(xVerb("take", xMove(sprint.Fleet, "m1:ready", "m1:working", "p3.w1", map[string]string{"due_unfinished": "17000"})))
	if got := due(); len(got) != 1 || got["unfinished:p3.w1"] != 17000 {
		t.Fatalf("due after the take is %v (a card in the working cell holds no untaken entry while due_untaken stays set)", got)
	}
	h.do(xVerb("read", xCreate(sprint.Readers, "r1:asked", "p3.r1.a", "3", map[string]string{"due_unbegun": "4000"})))
	h.do(xVerb("read", xMove(sprint.Readers, "r1:asked", "r1:reading", "p3.r1.a", map[string]string{"due_unreported": "8000"})))
	h.do(xVerb("add", xCreate(sprint.Merge, "s1:ctl", "ctl-s1", "0", map[string]string{"due_mergeidle": "600"})))
	want := map[string]float64{"unfinished:p3.w1": 17000, "unreported:p3.r1.a": 8000, "mergeidle:s1": 600}
	if got := due(); len(got) != len(want) || got["unfinished:p3.w1"] != 17000 || got["unreported:p3.r1.a"] != 8000 || got["mergeidle:s1"] != 600 {
		t.Fatalf("due is %v, want %v", got, want)
	}
	h.do(xVerb("read", xMove(sprint.Readers, "r1:reading", "r1:ok", "p3.r1.a", nil)))
	h.do(xVerb("finish", xMove(sprint.Fleet, "m1:working", "m1:ok", "p3.w1", map[string]string{"result": "ok"})))
	h.do(xVerb("merge", tset.Entry{Kind: "move", Table: sprint.Merge, From: "s1:ctl", IDs: []string{"ctl-s1"}, Unset: []string{"due_mergeidle"}, About: []string{"ctl-s1"}}))
	if got := due(); len(got) != 0 {
		t.Fatalf("due is %v after every timed state ended, want none", got)
	}
}

// TestXRefusesMalformedIndexedFields: a field the definitions read that holds
// anything but a whole number is refused before anything is written: one of the
// before-state is DRIFT naming the card (a record the machine never wrote), one an
// entry sets is REQUEST naming it. X.plan has no refusal channel, so X.pre finds
// these.
func TestXRefusesMalformedIndexedFields(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture()
	if err := h.mem.SeedMember(testPrefix, sprint.Work, "0", "bad1", tset.MemRecord{Epoch: "0", Revision: "1", Row: "s1", Column: "waiting", Score: "9",
		Fields: map[string]string{"kind": "primary", "open": "x"}}); err != nil {
		t.Fatal(err)
	}
	ref := h.refused(xVerb("rank", xMove(sprint.Work, "s1:waiting", "s1:ready", "bad1", nil)))
	if ref.Code != "DRIFT" || len(ref.Detail.IDs) != 1 || ref.Detail.IDs[0] != "bad1" {
		t.Fatalf("a card whose open is x: %s %+v", ref.Code, ref.Detail)
	}
	ref = h.wantRefusal(xVerb("add", xCreate(sprint.Work, "s1:waiting", "p7", "7", map[string]string{"open": "one"})), CodeRequest)
	if len(ref.Detail.IDs) != 1 || ref.Detail.IDs[0] != "p7" {
		t.Fatalf("a create with open one: %+v", ref.Detail)
	}
	h.wantRefusal(xVerb("add", xCreate(sprint.Fleet, "m1:ready", "p1.w1", "1", map[string]string{"due_untaken": "soon"})), CodeRequest)
}

// TestXWrongTypeIsRefused: a sprint key X reads that holds another type is
// WRONGTYPE before any value of it is used, with nothing written (1.0).
func TestXWrongTypeIsRefused(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		seed []Cmd
		req  func() *Request
	}{
		"the lease":       {[]Cmd{Command("RPUSH", xp+"lease", kindList, "x")}, func() *Request { return xTick(1, xMoveWaitingReady("p1")) }},
		"the clock":       {[]Cmd{xLease("1"), Command("RPUSH", xp+"clock", kindList, "x")}, func() *Request { return xTick(1, xMoveWaitingReady("p1")) }},
		"the marks":       {[]Cmd{Command("SET", xp+"dropping@0", kindString, "x")}, func() *Request { return xVerb("rank", xMoveWaitingReady("p1")) }},
		"the coordinator": {[]Cmd{Command("RPUSH", xp+"coordinator", kindList, "x")}, func() *Request { return xVerb("release", xMoveWaitingReady("p1")) }},
		"the counter": {[]Cmd{Command("RPUSH", xp+"next@0", kindList, "x")}, func() *Request {
			return xCounterStep(map[string]string{"score": "1000"}, nil)
		}},
		"the due set": {[]Cmd{xLease("1"), Command("SET", xp+"due@0", kindString, "x")}, func() *Request {
			r := xTick(1)
			r.Body.Guards = []XGuard{{Kind: XGuardBeatStale, Member: "m1"}}
			return r
		}},
		"the quarantine": {[]Cmd{Command("SET", xp+"quarantine@0", kindString, "x")}, func() *Request { return xVerb("rank", xMoveWaitingReady("p1")) }},
		"the agenda": {[]Cmd{xLease("1"), Command("SET", xp+"agenda@0", kindString, "x")}, func() *Request {
			r := xTick(1)
			r.Body.Requeue = []string{"ask@5+1"}
			return r
		}},
		"a hold": {[]Cmd{xLease("1"), Command("SET", xp+"jopen:p1@0", kindString, "x")}, func() *Request {
			r := xTick(1)
			r.Body.Guards = []XGuard{{Kind: XGuardHold, Member: "p1", Key: "stalled|slow=h1"}}
			return r
		}},
		"the strangers": {[]Cmd{xLease("1"), Command("SET", xp+"strangers", kindString, "x")}, func() *Request {
			r := xTick(1)
			r.Body.Guards = []XGuard{{Kind: XGuardStranger, Member: "m1"}}
			return r
		}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newXHarness(t)
			h.write(c.seed...)
			h.wantRefusal(c.req(), CodeWrongType)
		})
	}
}

// TestXCostIsExact: the coster counts X's share of a body from the read by
// running X on the plan the entries make: the commands and their argv bytes are
// exactly what XCmds writes for the same step, and the probes are the reads X.pre
// made plus one type read in prepare for each other key X writes (S.prepare's
// key_type: one cell probe for every key a step writes that no read has typed),
// which the Lua half's commands and reads give independently; a body X would refuse
// is refused. The steps include the removal of a card that leaves waiting with its
// needs, a change of a quarantined sentinel and a quarantined card that is removed,
// so a coster that leaves any removal out is wrong. The step builder keeps its
// headroom for what a derived entry adds (1.3.6).
func TestXCostIsExact(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture()
	h.do(xVerb("seed",
		xCreate(sprint.Work, "s1:waiting", "p6", "6", map[string]string{"kind": "primary", "open": "0", "needs": "n1,n2"}),
		xCreate(sprint.Work, "s1:waiting", "g2", "7", map[string]string{"kind": "sentinel"}),
		xCreate(sprint.Fleet, "m1:ready", "q1", "8", map[string]string{"stream": "s1", "due_untaken": "9100"})))
	h.write(xLease("1"), xRunning(0), Command("ZADD", xp+"agenda@0", kindZSet, "5", "deal", "6", "held:p1"),
		Command("ZADD", xp+"wait:n1@0", kindZSet, "0", "p6"), Command("ZADD", xp+"wait:n2@0", kindZSet, "0", "p6"))
	h.write(Command("SET", xp+"coordinator", kindString, "c1"))
	h.applies("the quarantine of a sentinel and a timed card", xQuarantineStep(1, map[string]string{"g2": "s1", "q1": "s1"}))
	steps := []*Request{
		xTick(1, xMoveWaitingReady("p1"), xMove(sprint.Work, "s1:ready", "s1:working", "p3", map[string]string{"attempt": "1"})),
		func() *Request {
			r := xTick(1, xCreate(sprint.Fleet, "m1:ready", "p3.w1", "3", map[string]string{"due_untaken": "9000", "stream": "s1"}))
			r.Body.Guards = []XGuard{{Kind: XGuardMemberUp, Member: "m1"}, {Kind: XGuardBeatStale, Member: "m1"}}
			r.Body.Done = []string{"deal", "held:p1"}
			r.Body.Requeue = []string{"ask@9+5"}
			return r
		}(),
		func() *Request {
			r := xVerb("release", xMove(sprint.Work, "s1:waiting", "s1:landed", "g1", nil))
			r.Meta.Actor = "c1"
			return r
		}(),
		// a card that leaves waiting with needs, by a move and by a remove: its wait:<n> removals
		xVerb("rank", xMove(sprint.Work, "s1:waiting", "s1:ready", "p6", nil)),
		xVerb("drop", xRemove(sprint.Work, "s1:waiting", "p2")),
		// a quarantined sentinel re-scored, and a quarantined timed card removed: what a quarantined card is owed
		xVerb("rank", tset.Entry{Kind: "move", Table: sprint.Work, From: "s1:waiting", IDs: []string{"g2"}, Scores: []string{"7.5"}, About: []string{"g2"}}),
		xVerb("drop", xRemove(sprint.Fleet, "m1:ready", "q1")),
		xQuarantineStep(1, map[string]string{"p3": "s1"}),
		xTick(1),
	}
	// the commands the steps that remove, or that change a quarantined card, must
	// have among them: the coster counts them, and a coster that left a removal out
	// would not match what X wrote
	wantCmds := map[int][]string{
		3: {"ZREM " + xp + "elig:s1@0 p6", "ZREM " + xp + "wait:n1@0 p6", "ZREM " + xp + "wait:n2@0 p6", "ZADD " + xp + "fresh:s1@0"},
		4: {"ZREM " + xp + "elig:s1@0 p2"},
		5: {"ZADD " + xp + "sent:s1@0 7.5 g2"},
		6: {"ZREM " + xp + "due@0 untaken:q1"},
	}
	for i, req := range steps {
		st := &State{Prefix: testPrefix, Epoch: "0", NowMS: tset.Decimal(xMS(xNow)), Names: testNames, Keys: &Keys{ks: h.tw.keys}}
		obs, ref := h.tw.before(context.Background(), st, req)
		if ref != nil {
			t.Fatalf("step %d: before: %v", i, ref)
		}
		cost, ref := CostX(st, req, obs)
		if ref != nil {
			t.Fatalf("step %d: CostX: %v", i, ref)
		}
		h.last = nil
		h.applies("a step the coster counted", req)
		for _, want := range wantCmds[i] {
			found := false
			for _, c := range h.last {
				found = found || strings.Contains(strings.Join(c.Argv, " "), want)
			}
			if !found {
				t.Fatalf("step %d: X wrote %v, which has no command with %q", i, h.last, want)
			}
		}
		if len(h.last) != cost.Commands {
			t.Fatalf("step %d: the coster counted %d commands, X wrote %d: %v", i, cost.Commands, len(h.last), h.last)
		}
		bytes := 0
		for _, c := range h.last {
			for _, a := range c.Argv {
				bytes += len(a)
			}
		}
		if bytes != cost.ArgvBytes {
			t.Fatalf("step %d: the coster counted %d argv bytes, X wrote %d", i, cost.ArgvBytes, bytes)
		}
		carry, _ := xPre(st, req, obs)
		if carry == nil {
			t.Fatalf("step %d: X.pre refused what the step applied with", i)
		}
		// X.pre's reads, and the type reads of prepare for the keys it writes and did not read,
		// counted from the commands the Lua half wrote and the reads it made.
		if want := h.mirror.probes + h.mirror.typeProbes; cost.Probes != want {
			t.Fatalf("step %d: the coster counted %d probes; the Lua half made %d reads and prepare would type-read %d keys more (%d)",
				i, cost.Probes, h.mirror.probes, h.mirror.typeProbes, want)
		}
		if cost.Probes < carry.probes {
			t.Fatalf("step %d: the coster counted %d probes, X.pre alone made %d", i, cost.Probes, carry.probes)
		}
	}
	st := &State{Prefix: testPrefix, Epoch: "0", NowMS: tset.Decimal(xMS(xNow)), Names: testNames, Keys: &Keys{ks: h.tw.keys}}
	// A body X refuses is refused by the coster, with X's refusal.
	bad := xTick(9, xMoveWaitingReady("p1"))
	obs, _ := h.tw.before(context.Background(), st, bad)
	if _, ref := CostX(st, bad, obs); ref == nil || ref.Code != CodeStaleGen {
		t.Fatalf("CostX of a stale step: %v", ref)
	}
}

// TestXStashIsBoundedAndTakenOnce: what X.pre hands X.plan is taken exactly once
// and only for the call it was made for, and steps refused between the two phases
// cannot grow the stash past its bound: the oldest are forgotten, the newest kept.
// (It runs on a stash of its own: filling the twins' would forget the carries of
// the steps other tests have in flight.)
func TestXStashIsBoundedAndTakenOnce(t *testing.T) {
	t.Parallel()
	const bound = 4
	stash := newXStash(bound)
	first := &State{}
	stash.put(first, &xCarry{probes: 1})
	for i := 0; i < bound; i++ {
		stash.put(&State{}, &xCarry{probes: 2})
	}
	if got := stash.take(first); got != nil {
		t.Fatalf("the oldest carry survived %d newer ones", bound)
	}
	last := &State{}
	stash.put(last, &xCarry{probes: 3})
	if got := stash.take(last); got == nil || got.probes != 3 {
		t.Fatalf("the newest carry was not kept: %+v", got)
	}
	if got := stash.take(last); got != nil {
		t.Fatal("a carry was taken twice")
	}
	if got := stash.take(&State{}); got != nil {
		t.Fatal("a carry was taken for a call it was not made for")
	}
	if stash.order.Len() != bound-1 || len(stash.byKey) != bound-1 {
		t.Fatalf("the stash holds %d carries (%d by call); want %d", stash.order.Len(), len(stash.byKey), bound-1)
	}
}

// TestXCmdsWithoutXPreIsRefusedByPrepare: X.plan for a call X.pre did not pass has
// nothing to derive from, and returns the one command prepare refuses (REQUEST),
// so nothing is written, rather than an empty list that would leave the indexes
// disagreeing with their definitions.
func TestXCmdsWithoutXPreIsRefusedByPrepare(t *testing.T) {
	t.Parallel()
	cmds := XCmds(&State{Prefix: testPrefix}, TablePlan{}, LogPlan{})
	if len(cmds) != 1 {
		t.Fatalf("XCmds for a call X.pre did not pass returned %v", cmds)
	}
	if _, ref := newKeyspace().check(testPrefix, cmds); ref == nil || ref.Code != CodeRequest {
		t.Fatalf("prepare on %v: %v", cmds, ref)
	}
}

// TestXIsInTheDefaultPhases: the package's init registers X in the phases every
// twin starts with, and chains the asks of xBefore with any phase registered before
// it, so the files of IT14 and IT15 may ask for their own ids too.
func TestXIsInTheDefaultPhases(t *testing.T) {
	t.Parallel()
	if defaultPhases.XPre == nil || defaultPhases.XCmds == nil || defaultPhases.Before == nil {
		t.Fatalf("the default phases lack X: %+v", defaultPhases)
	}
	asks := defaultPhases.Before(&State{}, xVerb("rank", xMoveWaitingReady("p1")))
	var saw bool
	for _, a := range asks {
		saw = saw || (a.Table == sprint.Work && len(a.IDs) == 1 && a.IDs[0] == "p1")
	}
	if !saw {
		t.Fatalf("the default asks %v do not name the card the step moves", asks)
	}
}

// TestXStoreTime2000: the derivation adds at most 3 ms of store time to a step of
// 2,000 changes (IT13's limit). It needs the store's SLOWLOG.
func TestXStoreTime2000(t *testing.T) {
	t.Parallel()
	t.Skip("G0: needs the store: SLOWLOG in the container, with Layer 1 revision 4 pinned and Layer 2 accepted again; Lua half is not loaded before G0")
}

// TestXStepChunkMeasured: without AL8 the StepChunk at which a step fits 25 ms is
// measured here (IT13's limit). It needs the store's SLOWLOG.
func TestXStepChunkMeasured(t *testing.T) {
	t.Parallel()
	t.Skip("G0: needs the store: a step of 2,000 changes timed in the container, at chunks from 2,000 down")
}

// TestXRefusalAfterPlanLeavesNothing: a refusal of X's own commands in prepare (a
// sprint key of another type where X writes an index, the commands past the shared
// bound of argv bytes) leaves the whole image equal, the Mem's state, the sprint's
// keys and the log, as the store's plan-then-commit leaves it: X's commands are
// checked by prepare after Layer 1 has planned and before it writes (Mem.Plan, then
// Mem.Commit, S15), the agenda's key is still there, the twin still stands for the
// store, and the same step with nothing refusing applies (errata E6, E7.1).
func TestXRefusalAfterPlanLeavesNothing(t *testing.T) {
	t.Parallel()
	refusedAt := func(t *testing.T, h *xh, req *Request, code string) *Refusal {
		t.Helper()
		before := h.img()
		res, err := Step(context.Background(), h.tw, req)
		if err != nil || res.Err != nil || res.Refusal == nil || res.Refusal.Code != code || res.Refusal.Phase != PhasePrepare {
			t.Fatalf("result %+v, err %v; want %s from %s", res, err, code, PhasePrepare)
		}
		if h.img() != before {
			t.Fatalf("a %s refusal of prepare changed the twin", code)
		}
		if _, ok := h.zset("agenda@0")["deal"]; !ok {
			t.Fatal("the agenda's deal key was removed by a refused step")
		}
		return res.Refusal
	}

	t.Run("a sprint key of another type where X writes an index", func(t *testing.T) {
		t.Parallel()
		h := newXHarness(t)
		h.fixture()
		h.write(xLease("1"), Command("ZADD", xp+"agenda@0", kindZSet, "5", "deal"), Command("SET", xp+"again:s1@0", kindString, "x"))
		// p3 is dealt before: it leaves fresh and enters again, which is a string
		again := xVerb("take", xMove(sprint.Work, "s1:ready", "s1:ready", "p3", map[string]string{"attempt": "1"}))
		again.Body.Entries[0].To = ""
		again.Body.Done = []string{"deal"}
		refusedAt(t, h, again, CodeWrongType)
		if len(h.last) == 0 {
			t.Fatal("X wrote no commands for the step: the refusal is not from X's own commands")
		}
		h.applies("a move that touches no key of another type", func() *Request {
			r := xVerb("rank", xMoveWaitingReady("p1"))
			r.Body.Done = []string{"deal"}
			return r
		}())
		if _, ok := h.zset("fresh:s1@0")["p1"]; !ok {
			t.Fatalf("the step after the refusal did not apply: %v", h.zset("fresh:s1@0"))
		}
	})

	t.Run("commands past the shared bound of argv bytes", func(t *testing.T) {
		t.Parallel()
		h := newXHarness(t)
		h.fixture()
		h.write(xLease("1"), Command("ZADD", xp+"agenda@0", kindZSet, "5", "deal"))
		// A step quarantines at most QuarantineMax cards (IT16), and an id and a
		// stream are at most tset.MaxIdentifierBytes (L1 6), so X's quarantine
		// commands peak at 2,000 ids of 256 bytes over 2,000 streams of 256 bytes:
		// three ZREMs of a stream's elig, fresh and again, each naming its stream
		// and the id, and the ZREM of askwait, about 3.7 MB. That alone cannot pass
		// 8 MiB, and no X command class can by itself: the request is at most
		// 4 MiB. So the step fills the parts' share (partArgvShare, the most the
		// parts of one step may write together) with the stand-in sprint part's
		// commands, and X's own commands carry the total past the shared bound:
		// the quarantine's ZREMs, and the agenda's ZREMs of finished keys (a class
		// with no count cap) in the rest of the request.
		q := xTick(1)
		for i := 0; i < QuarantineMax; i++ {
			q.Body.Quarantine = append(q.Body.Quarantine, Quarantined{
				ID:     fmt.Sprintf("%0*d", tset.MaxIdentifierBytes, i),
				Stream: fmt.Sprintf("s%0*d", tset.MaxIdentifierBytes-1, i),
				Code:   "DRIFT", Rule: "resolve"})
		}
		q.Sprint = &SprintPart{Quarantine: q.Body.Quarantine}
		for i := 0; i < 2000; i++ {
			q.Body.Done = append(q.Body.Done, fmt.Sprintf("resolve@%d+%0*d", i+1, 400, i))
		}
		var filler []Cmd
		fill := 0
		for i := 0; fill < partArgvShare-(1<<16); i++ {
			c := Command("HSET", xp+"filler@0", kindHash, fmt.Sprintf("f%03d", i), strings.Repeat("v", 60000))
			for _, a := range c.Argv {
				fill += len(a)
			}
			filler = append(filler, c)
		}
		h.extra = filler
		ref := refusedAt(t, h, q, CodeLimit)
		if len(h.last) == 0 {
			t.Fatal("X wrote no commands for the step: the refusal is not from X's own commands")
		}
		xArgv := 0
		for _, c := range h.last {
			for _, a := range c.Argv {
				xArgv += len(a)
			}
		}
		if ref.Detail.Budget != "argv_bytes" || ref.Detail.Actual == nil || *ref.Detail.Actual <= tset.MaxPlannedArgvBytes {
			t.Fatalf("refusal %+v: want the shared bound of argv bytes passed", ref.Detail)
		}
		if rest := *ref.Detail.Actual - int64(xArgv); rest > tset.MaxPlannedArgvBytes {
			t.Fatalf("the step's commands other than X's are %d bytes, over the bound alone: X's %d are not what passes it", rest, xArgv)
		}
		h.extra = nil
		h.applies("a step that fits", xVerb("rank", xMoveWaitingReady("p1")))
	})
}

// TestXTableVersions (errata 3 H17): X moves the version of a table in
// {p}tver@e by one on each step whose plan changes a card of it, and on no
// other; the version guard holds a table to its version as read (0 for none),
// so a card of it written since the read refuses XGUARD. The Lua half gives the
// same reads, commands and refusals.
func TestXTableVersions(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture() // one step creates the work and fleet cards
	h.write(xLease("1"), xRunning(300))
	version := func(table string) string { return h.keys()[xp+"tver@0"].Hash[table] }
	if version(sprint.Work) != "1" || version(sprint.Fleet) != "1" || version(sprint.Merge) != "" {
		t.Fatalf("the versions after the fixture: %v", h.keys()[xp+"tver@0"].Hash)
	}
	guard := func(g ...XGuard) *Request {
		r := xTick(1)
		r.Body.Guards = g
		return r
	}
	h.applies("as read, and 0 for none", guard(XGuard{Kind: XGuardVersion, Key: sprint.Work, Score: 1},
		XGuard{Kind: XGuardVersion, Key: sprint.Merge, Score: 0}))
	if len(h.last) != 0 {
		t.Fatalf("a step that changes no card wrote %v", h.last)
	}
	h.applies("a move", xVerb("rank", xMoveWaitingReady("p1")))
	if version(sprint.Work) != "2" || version(sprint.Fleet) != "1" {
		t.Fatalf("the versions after a move of a work card: %v", h.keys()[xp+"tver@0"].Hash)
	}
	if v := h.last[len(h.last)-1]; strings.Join(v.Argv, " ") != "HSET "+xp+"tver@0 work 2" {
		t.Fatalf("X's last command: %v", v.Argv)
	}
	h.wantRefusal(guard(XGuard{Kind: XGuardVersion, Key: sprint.Work, Score: 1}), CodeXGuard)
	h.wantRefusal(guard(XGuard{Kind: XGuardVersion, Key: sprint.Merge, Score: 1}), CodeXGuard)
	h.applies("a change that changes nothing", xVerb("rank", tset.Entry{Kind: "move", Table: sprint.Work, From: "s1:waiting",
		IDs: []string{"p2"}, Set: map[string]string{"open": "0"}, About: []string{"p2"}}))
	if version(sprint.Work) != "2" {
		t.Fatalf("a step that changed no card moved the version to %s", version(sprint.Work))
	}
	h.applies("a guard on a card changes none", xVerb("look", tset.Entry{Kind: "guard", Table: sprint.Work, From: "s1:waiting", IDs: []string{"p2"}}))
	if version(sprint.Work) != "2" {
		t.Fatalf("a guard entry moved the version to %s", version(sprint.Work))
	}
	for _, bad := range []XGuard{{Kind: XGuardVersion}, {Kind: XGuardVersion, Key: "a b"}, {Kind: XGuardVersion, Key: "work@0"},
		{Kind: XGuardVersion, Key: sprint.Work, Score: -1}} {
		h.wantRefusal(guard(bad), CodeRequest)
	}
	h.write(Command("HSET", xp+"tver@0", kindHash, sprint.Work, "02"))
	h.wantRefusal(guard(XGuard{Kind: XGuardVersion, Key: sprint.Work, Score: 2}), CodeConfig)
	h.wantRefusal(xVerb("rank", xMoveWaitingReady("p2")), CodeConfig)
	if h.mirror.pre == 0 || h.mirror.cmds == 0 {
		t.Fatal("the Lua half was not compared")
	}
}

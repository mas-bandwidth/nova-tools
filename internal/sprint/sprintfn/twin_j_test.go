package sprintfn

import (
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// jRig is a twin with J wired in as the twin composes it (defaultPhases: J for
// any step that carries entries or note requests, asking the due fields of the
// cards a step moves through Phases.JBefore) and the four tables defined, at a
// clock the test moves. XCmds writes what seed holds once, so a test can put the
// sprint's own keys (the clock, a judgment's state) where a step reads them.
type jRig struct {
	t    *testing.T
	tw   *Twin
	m    *tset.Mem
	log  *MemLog
	now  time.Time
	seed []Cmd
}

func newJRig(t *testing.T) *jRig {
	t.Helper()
	r := &jRig{t: t, now: testTime}
	ph := passX()
	ph.XCmds = func(*State, TablePlan, LogPlan) []Cmd {
		c := r.seed
		r.seed = nil
		return c
	}
	ph.JBefore, ph.JOnEntries, ph.JDecide, ph.JCmds = JBefore, true, JDecide, JCmds
	r.tw, r.m, r.log = newTestTwin(t, ph)
	r.tw.SetClock(func() time.Time { return r.now })
	return r
}

// wall is the twin's clock in milliseconds, and so R while nothing is STOPPED.
func (r *jRig) wall() int64 { return r.now.UnixMilli() }

// step sends a step of note requests and nothing else: an op-less tick step
// (errata 2, item 9).
func (r *jRig) step(notes ...NoteReq) *StepReply {
	r.t.Helper()
	return mustStep(r.t, r.tw, &Request{Epoch: "0", Meta: Meta{Rule: "j", Tick: true}, Body: Body{Notes: notes}})
}

func (r *jRig) send(req *Request) *StepReply {
	r.t.Helper()
	return mustStep(r.t, r.tw, req)
}

// refused sends a step of note requests that J or a later phase must refuse.
func (r *jRig) refused(notes ...NoteReq) *Refusal {
	r.t.Helper()
	res, err := Step(r.t.Context(), r.tw, &Request{Epoch: "0", Meta: Meta{Rule: "j", Tick: true}, Body: Body{Notes: notes}})
	if err != nil || res.Refusal == nil {
		r.t.Fatalf("the step was not refused: result %+v, err %v", res, err)
	}
	return res.Refusal
}

func (r *jRig) put(cmds ...Cmd) {
	r.t.Helper()
	r.seed = cmds
	r.step()
}

func (r *jRig) img() string { return string(image(r.t, r.tw, r.m, r.log)) }

func (r *jRig) keys() map[string]KeyValue { return r.tw.SprintKeys() }

// line is a line of the log as the stub stores it, with its meta decoded.
type jLine struct {
	Seq   string
	Kind  string
	About []string
	Meta  map[string]any
}

func (r *jRig) line(seq int) jLine {
	r.t.Helper()
	lines := r.log.Lines(testPrefix, "0")
	if seq < 1 || seq > len(lines) {
		r.t.Fatalf("no line %d: the log has %d", seq, len(lines))
	}
	var l jLine
	if err := json.Unmarshal(lines[seq-1], &l); err != nil {
		r.t.Fatal(err)
	}
	return l
}

// jk is a per epoch sprint key at epoch 0, with the test deployment's prefix.
func jk(name string) string { return testPrefix + "sprint:" + name + "@0" }

// jReq is a note request with no text, decisions or time.
func jReq(op, typ, cause string, subjects ...string) NoteReq {
	return NoteReq{Op: op, Type: typ, Cause: cause, Subjects: subjects}
}

// jHold is a wait on a condition the tick keeps, until running time until.
func jHold(typ, cause string, until int64, subjects ...string) NoteReq {
	r := jReq(JOpHold, typ, cause, subjects...)
	r.Until = until
	return r
}

// jStr is a decimal.
func jStr(n int64) string { return strconv.FormatInt(n, 10) }

func (r *jRig) hash(key string) map[string]string {
	return r.keys()[key].Hash
}

func (r *jRig) zset(key string) map[string]float64 {
	return r.keys()[key].ZSet
}

func (r *jRig) list(key string) []string {
	return r.keys()[key].List
}

func (r *jRig) wantHash(key string, want map[string]string) {
	r.t.Helper()
	got := r.hash(key)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		r.t.Fatalf("%s is %v, want %v", key, got, want)
	}
}

func (r *jRig) wantZSet(key string, want map[string]float64) {
	r.t.Helper()
	got := r.zset(key)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		r.t.Fatalf("%s is %v, want %v", key, got, want)
	}
}

// Types of 2.2 and 2.5 the tests use. "no fleet member is up" is one the tick
// keeps whose words Layer 3's ingest also has (its owner key is deal).
const (
	jtCannotAsk = "cannot ask"
	jtNoMember  = "no fleet member is up"
	jtBlocked   = "a primary is blocked on something dropped"
	jtDone      = "the sprint is done"
	jtStopped   = "the machine is STOPPED and moves are due"
)

// TestJOnePerCauseRace (8.1 IT15): two steps planned from one read, each asking
// to open one judgment, open it once. The second finds the field and writes
// nothing, not even a line; a step naming one subject that has the judgment and
// one that has not opens it on the one that has not.
func TestJOnePerCauseRace(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	first := r.step(jReq(JOpOpen, jtCannotAsk, "c", "p1"))
	if first.Reply.Lines != 1 || first.Reply.FirstSeq != "1" {
		t.Fatalf("the first step wrote %d lines from %s", first.Reply.Lines, first.Reply.FirstSeq)
	}
	r.wantHash(jk("jopen:p1"), map[string]string{"cannot ask|c": "n1"})
	before := r.img()

	second := r.step(jReq(JOpOpen, jtCannotAsk, "c", "p1"))
	if second.Reply.Lines != 0 || r.img() != before {
		t.Fatalf("the second step wrote %d lines or changed the twin: one judgment per cause", second.Reply.Lines)
	}

	both := r.step(jReq(JOpOpen, jtCannotAsk, "c", "p1", "p2"))
	if both.Reply.Lines != 1 {
		t.Fatalf("a step of a subject that has it and one that has not wrote %d lines, want 1", both.Reply.Lines)
	}
	if l := r.line(2); !reflect.DeepEqual(l.About, []string{"p2"}) {
		t.Fatalf("the new note names %v, want only p2", l.About)
	}
	r.wantHash(jk("jopen:p1"), map[string]string{"cannot ask|c": "n1"})
	r.wantHash(jk("jopen:p2"), map[string]string{"cannot ask|c": "n2"})
	r.wantHash(jk("jn"), map[string]string{"n1": "1", "n2": "1"})
}

// TestJWaitHoldsUntilTime (8.1 IT15): a wait on a condition the tick keeps
// closes the judgment with a hold line, writes h<note> in place of the note id
// and enters hold:<note> at its time, and leaves the subject in askwait; while
// it stands the owner rule raises nothing; when it runs out (R13's unhold) the
// field goes, and the owner rule raises again.
func TestJWaitHoldsUntilTime(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	r.step(jReq(JOpOpen, jtCannotAsk, "c", "p1"))
	until := r.wall() + 60_000
	held := r.step(jHold(jtCannotAsk, "c", until, "p1"))
	if held.Reply.Lines != 1 {
		t.Fatalf("the wait wrote %d lines, want its hold line", held.Reply.Lines)
	}
	r.wantHash(jk("jopen:p1"), map[string]string{"cannot ask|c": "hn1"})
	r.wantZSet(jk("due"), map[string]float64{"hold:n1": float64(until)})
	r.wantZSet(jk("jnotes"), nil)
	r.wantHash(jk("jn"), nil)
	r.wantZSet(jk("askwait"), map[string]float64{"p1": float64(r.wall())})
	if l := r.line(2); l.Meta["op"] != "hold" || l.Meta["note"] != "n1" || l.Meta["until"] != jStr(until) || l.Meta["kind"] != nil {
		t.Fatalf("the hold line's meta is %v", l.Meta)
	}

	// Under the hold the owner rule raises nothing of that cause and subject.
	img := r.img()
	if again := r.step(jReq(JOpOpen, jtCannotAsk, "c", "p1")); again.Reply.Lines != 0 || r.img() != img {
		t.Fatalf("an open under a hold wrote %d lines or changed the twin", again.Reply.Lines)
	}
	// Another cause on the same subject is not held.
	if other := r.step(jReq(JOpOpen, jtCannotAsk, "d", "p1")); other.Reply.Lines != 1 {
		t.Fatalf("an open of another cause wrote %d lines, want 1", other.Reply.Lines)
	}

	// The hold runs out: R13 unholds, and the owner rule raises the judgment again.
	r.now = r.now.Add(61 * time.Second)
	un := r.step(jReq(JOpUnhold, jtCannotAsk, "c", "p1"))
	if un.Reply.Lines != 1 {
		t.Fatalf("the unhold wrote %d lines, want its unheld line", un.Reply.Lines)
	}
	if _, there := r.hash(jk("jopen:p1"))["cannot ask|c"]; there {
		t.Fatal("the unhold left the field in jopen")
	}
	if _, there := r.zset(jk("askwait"))["p1"]; !there {
		t.Fatal("the cause d of p1 is open, and askwait names p1 while any is")
	}
	if l := r.line(4); l.Meta["op"] != "unhold" || l.Meta["note"] != "n1" || l.Meta["kind"] != sprint.Decided {
		t.Fatalf("the unheld line's meta is %v", l.Meta)
	}
	again := r.step(jReq(JOpOpen, jtCannotAsk, "c", "p1"))
	if again.Reply.Lines != 1 || r.hash(jk("jopen:p1"))["cannot ask|c"] != "n5" {
		t.Fatalf("after the hold the owner rule raises the judgment again: lines %d, field %q",
			again.Reply.Lines, r.hash(jk("jopen:p1"))["cannot ask|c"])
	}
}

// TestJHoldEndsWhenConditionClears (8.1 IT15): while a wait stands, the owner
// rule finds the condition cleared and closes: the hold ends with it. The field
// goes, the held count goes, and hold:<note> leaves the due set, so no entry is
// left to fire; R13's unhold of what is gone writes nothing, and the owner rule
// raises again at once.
func TestJHoldEndsWhenConditionClears(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	r.step(jReq(JOpOpen, jtNoMember, "c", "sprint"))
	until := r.wall() + 60_000
	r.step(jHold(jtNoMember, "c", until, "sprint"))
	r.wantHash(jk("jopen:sprint"), map[string]string{"no fleet member is up|c": "hn1"})
	r.wantHash(jk("jh"), map[string]string{"n1": "1"})
	r.wantZSet(jk("due"), map[string]float64{"hold:n1": float64(until)})

	closed := r.step(jReq(JOpClose, jtNoMember, "c", "sprint"))
	if closed.Reply.Lines != 1 {
		t.Fatalf("the close wrote %d lines, want 1", closed.Reply.Lines)
	}
	r.wantHash(jk("jopen:sprint"), nil)
	r.wantHash(jk("jn"), nil)
	r.wantHash(jk("jh"), nil)
	r.wantZSet(jk("due"), nil)
	if l := r.line(3); l.Meta["op"] != "close" || l.Meta["note"] != "n1" || l.Meta["kind"] != sprint.Decided {
		t.Fatalf("the close line's meta is %v", l.Meta)
	}

	// The hold entry is gone, so nothing fires; an unhold of nothing writes nothing.
	img := r.img()
	if un := r.step(jReq(JOpUnhold, jtNoMember, "c", "sprint")); un.Reply.Lines != 0 || r.img() != img {
		t.Fatalf("an unhold of nothing wrote %d lines or changed the twin", un.Reply.Lines)
	}
	// And the wait has ended: the owner rule raises again at once.
	if again := r.step(jReq(JOpOpen, jtNoMember, "c", "sprint")); again.Reply.Lines != 1 {
		t.Fatalf("an open after the close wrote %d lines, want 1", again.Reply.Lines)
	}
}

// TestJHoldNamingSeveralSubjectsKeepsItsEntry: a wait holds every subject of a
// note, and J reads each subject's field alone, so the held count (jh) is what
// says the last one has gone: a close or an unhold of one subject lowers it and
// leaves hold:<note> to fire for the rest; the last to go takes the entry with
// it. Closing twice writes nothing the second time.
func TestJHoldNamingSeveralSubjectsKeepsItsEntry(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	r.step(jReq(JOpOpen, jtCannotAsk, "c", "p1", "p2", "p3"))
	until := r.wall() + 60_000
	r.step(jHold(jtCannotAsk, "c", until, "p1", "p2", "p3"))
	r.wantHash(jk("jh"), map[string]string{"n1": "3"})
	r.wantHash(jk("jn"), nil)
	r.wantZSet(jk("due"), map[string]float64{"hold:n1": float64(until)})

	r.step(jReq(JOpClose, jtCannotAsk, "c", "p1"))
	r.wantHash(jk("jh"), map[string]string{"n1": "2"})
	r.wantZSet(jk("due"), map[string]float64{"hold:n1": float64(until)})
	if _, there := r.zset(jk("askwait"))["p1"]; there {
		t.Fatal("p1 closed its only cause and is still in askwait")
	}
	img := r.img()
	if again := r.step(jReq(JOpClose, jtCannotAsk, "c", "p1")); again.Reply.Lines != 0 || r.img() != img {
		t.Fatalf("a second close of p1 wrote %d lines or changed the twin", again.Reply.Lines)
	}

	r.step(jReq(JOpUnhold, jtCannotAsk, "c", "p2"))
	r.wantHash(jk("jh"), map[string]string{"n1": "1"})
	r.wantZSet(jk("due"), map[string]float64{"hold:n1": float64(until)})

	r.step(jReq(JOpClose, jtCannotAsk, "c", "p3"))
	r.wantHash(jk("jh"), nil)
	r.wantZSet(jk("due"), nil)
	for _, p := range []string{"p1", "p2", "p3"} {
		r.wantHash(jk("jopen:"+p), nil)
	}
	r.wantZSet(jk("askwait"), nil)
}

// TestJStoppedHoldEndsWithTheJudgment (errata 3, H5): a wait on the STOPPED
// judgment sets stophold_ms, and a close or an unhold of it clears it, so a stale
// hold of an earlier span cannot mute R17 in the next; the unhold line is the
// wait's expiry line (a decided line of the type), and the judgment is raised
// again at once.
func TestJStoppedHoldEndsWithTheJudgment(t *testing.T) {
	t.Parallel()
	clock := testPrefix + "sprint:clock"
	for _, op := range []string{JOpClose, JOpUnhold} {
		r := newJRig(t)
		r.step(jReq(JOpOpen, jtStopped, "c", "sprint"))
		wall := r.wall() + 3_600_000
		r.step(jHold(jtStopped, "c", wall, "sprint"))
		r.wantHash(clock, map[string]string{"stophold_ms": jStr(wall)})
		r.wantHash(jk("jh"), map[string]string{"n1": "1"})

		if reply := r.step(jReq(op, jtStopped, "c", "sprint")); reply.Reply.Lines != 1 {
			t.Fatalf("%s: wrote %d lines, want 1", op, reply.Reply.Lines)
		}
		r.wantHash(clock, nil)
		r.wantHash(jk("jh"), nil)
		r.wantHash(jk("jopen:sprint"), nil)
		if ev := r.eventOf(3); !ev.Closes || ev.NoteType != jtStopped {
			t.Fatalf("%s: the line reads as %+v, want a close of the STOPPED judgment", op, ev)
		}
		if again := r.step(jReq(JOpOpen, jtStopped, "c", "sprint")); again.Reply.Lines != 1 {
			t.Fatalf("%s: an open after wrote %d lines, want 1", op, again.Reply.Lines)
		}
		img := r.img()
		if none := r.step(jReq(op, jtStopped, "x", "sprint")); none.Reply.Lines != 0 || r.img() != img {
			t.Fatalf("%s: a run on a cause that is not open wrote %d lines", op, none.Reply.Lines)
		}
	}
}

// eventOf parses a line of the log as Layer 3 does.
func (r *jRig) eventOf(seq int) sprint.Event {
	r.t.Helper()
	lines := r.log.Lines(testPrefix, "0")
	ev, err := sprint.ParseEvent(strconv.Itoa(seq)+"-0", lines[seq-1])
	if err != nil {
		r.t.Fatalf("line %d does not parse as an event: %v", seq, err)
	}
	return ev
}

func jIngestKeys(evs ...sprint.Event) []string {
	var out []string
	for _, k := range sprint.Ingest(evs).Keys {
		out = append(out, k.Key)
	}
	sort.Strings(out)
	return out
}

// TestJHoldLineQueuesNothing (8.1 IT15): the hold line of a wait is a plain
// note that Layer 3 reads as neither opening nor closing a judgment, and
// ingest queues no key for it (2.1); the line that closes a judgment, and the
// unheld line of R13, queue the owner key of the type; the line that opens one
// queues none.
func TestJHoldLineQueuesNothing(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	r.step(jReq(JOpOpen, jtNoMember, "c", "sprint")) // line 1
	r.step(jHold(jtNoMember, "c", r.wall()+60_000, "sprint"))
	r.step(jReq(JOpUnhold, jtNoMember, "c", "sprint")) // line 3
	r.step(jReq(JOpOpen, jtNoMember, "c", "sprint"))   // line 4
	r.step(jReq(JOpClose, jtNoMember, "c", "sprint"))  // line 5

	opens, holds := r.eventOf(1), r.eventOf(2)
	if !opens.Opens || opens.Closes || opens.Kind != sprint.Judgment {
		t.Fatalf("the open line reads as %+v", opens)
	}
	if got := jIngestKeys(opens); len(got) != 0 {
		t.Fatalf("the open line queues %v", got)
	}
	if holds.Opens || holds.Closes || holds.NoteType != jtNoMember {
		t.Fatalf("the hold line reads as %+v: a plain note of the type", holds)
	}
	if got := jIngestKeys(holds); len(got) != 0 {
		t.Fatalf("the hold line queues %v, want nothing (2.1)", got)
	}
	for _, seq := range []int{3, 5} {
		ev := r.eventOf(seq)
		if !ev.Closes {
			t.Fatalf("line %d reads as %+v: it closes the judgment", seq, ev)
		}
		if got := jIngestKeys(ev); !reflect.DeepEqual(got, []string{"deal"}) {
			t.Fatalf("line %d queues %v, want the owner key deal", seq, got)
		}
	}
}

// fleetSeed creates the fleet member's row m1 and the work card w1 in
// m1:ready with a due_untaken field, and the reader r1's read card rc in
// readers r1:reading with due_unreported, for the lateness tests.
func jFleetSeed() *Request {
	return &Request{Epoch: "0", Meta: Meta{Verb: "add", Actor: "coordinator"}, Body: Body{Entries: []tset.Entry{
		{Kind: "rows", Table: sprint.Fleet, Add: []string{"m1", "m2"}},
		{Kind: "rows", Table: sprint.Readers, Add: []string{"r1"}},
		{Kind: "create", Table: sprint.Fleet, To: "m1:ready", IDs: []string{"w1", "w2"}, Scores: []string{"1", "2"},
			Set: map[string]string{"due_untaken": "5000"}, About: []string{"p1", "p2"}},
		{Kind: "create", Table: sprint.Readers, To: "r1:reading", IDs: []string{"rc"}, Scores: []string{"1"},
			Set: map[string]string{"due_unreported": "9000"}, About: []string{"p1"}},
	}}}
}

// TestJClosesLatenessWhenStateEnds (8.1 IT15): a step that ends a timed state
// closes the lateness judgment of that kind open on the card, or ends its hold
// and its hold entry, with the reason: a take closes "not taken" (an open one,
// and a held one), and a report closes "a read card is past its deadline"; a move
// that keeps the state closes nothing. The steps carry entries and no note
// request, as a verb's step or a rule's does, and the composed twin calls J for
// them (errata 3: the close runs on the composed write path).
func TestJClosesLatenessWhenStateEnds(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	r.send(jFleetSeed()) // lines 1 to 4
	step := func(verb string, e tset.Entry) *StepReply {
		r.t.Helper()
		return r.send(&Request{Epoch: "0", Meta: Meta{Verb: verb, Actor: "m1"}, Body: Body{Entries: []tset.Entry{e}}})
	}
	// R11 raised "not taken" on w1 and w2 (note n5) and "not reported" on rc (n6).
	r.step(jReq(JOpOpen, jTypeWorkLate, "untaken", "w1", "w2"), jReq(JOpOpen, jTypeReadLate, "unreported", "rc"))
	until := r.wall() + 60_000
	r.step(jHold(jTypeWorkLate, "untaken", until, "w2")) // the coordinator waits on w2's
	r.wantHash(jk("jopen:w1"), map[string]string{jTypeWorkLate + "|untaken": "n5"})
	r.wantHash(jk("jopen:w2"), map[string]string{jTypeWorkLate + "|untaken": "hn5"})
	r.wantHash(jk("jn"), map[string]string{"n5": "1", "n6": "1"})
	r.wantHash(jk("jh"), map[string]string{"n5": "1"})
	r.wantZSet(jk("due"), map[string]float64{"hold:n5": float64(until), "overdue:n5": float64(r.wall()) + 600_000, "overdue:n6": float64(r.wall()) + 600_000})

	// A level: w1 moves to another member's ready cell, and stays untaken.
	if reply := step("level", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ready", To: "m2:ready", IDs: []string{"w1"}, About: []string{"p1"}}); reply.Reply.Lines != 1 {
		t.Fatalf("a level wrote %d lines, want its one", reply.Reply.Lines)
	}
	r.wantHash(jk("jopen:w1"), map[string]string{jTypeWorkLate + "|untaken": "n5"})

	// w1 is taken: the open judgment closes, and its note, left with no open
	// subject, leaves jnotes and loses its overdue entry. w2's is held, and stays.
	take := step("take", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m2:ready", To: "m2:working", IDs: []string{"w1"},
		Set: map[string]string{"due_unfinished": "7000"}, Unset: []string{"due_untaken"}, About: []string{"p1"}})
	if take.Reply.Lines != 2 {
		t.Fatalf("a take wrote %d lines, want its own and the close J added", take.Reply.Lines)
	}
	r.wantHash(jk("jopen:w1"), nil)
	r.wantHash(jk("jopen:w2"), map[string]string{jTypeWorkLate + "|untaken": "hn5"})
	r.wantHash(jk("jn"), map[string]string{"n6": "1"})
	if _, there := r.zset(jk("jnotes"))["n5"]; there {
		t.Fatal("n5 is in jnotes with no open subject")
	}
	if _, there := r.zset(jk("due"))["overdue:n5"]; there {
		t.Fatal("n5 kept its overdue entry with no open subject")
	}
	// The step's lines are in order: the move's own, then the close J added.
	closeLine := r.line(len(r.log.Lines(testPrefix, "0")))
	if closeLine.Meta["op"] != "close" || closeLine.Meta["text"] != "taken" || closeLine.Meta["type"] != jTypeWorkLate ||
		closeLine.Meta["cause"] != "untaken" || closeLine.Meta["note"] != "n5" || !reflect.DeepEqual(closeLine.About, []string{"w1"}) ||
		closeLine.Meta["kind"] != sprint.Decided {
		t.Fatalf("the take's close line is %+v", closeLine)
	}

	// w2 is taken while its judgment is held: the hold ends with the state, and
	// its entry goes with it.
	step("take", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ready", To: "m1:working", IDs: []string{"w2"},
		Unset: []string{"due_untaken"}, About: []string{"p2"}})
	r.wantHash(jk("jopen:w2"), nil)
	r.wantHash(jk("jh"), nil)
	if _, there := r.zset(jk("due"))["hold:n5"]; there {
		t.Fatal("the take left hold:n5 in the due set with no subject held")
	}

	// rc is reported: "a read card is past its deadline" closes, with "reported".
	step("read", tset.Entry{Kind: "move", Table: sprint.Readers, From: "r1:reading", To: "r1:ok", IDs: []string{"rc"},
		Unset: []string{"due_unreported"}, About: []string{"p1"}})
	r.wantHash(jk("jopen:rc"), nil)
	r.wantHash(jk("jn"), nil)
	r.wantZSet(jk("jnotes"), nil)
	found := false
	for seq := 1; seq <= len(r.log.Lines(testPrefix, "0")); seq++ {
		if m := r.line(seq).Meta; m["op"] == "close" && m["type"] == jTypeReadLate {
			found = m["text"] == "reported" && m["cause"] == "unreported" && m["note"] == "n6"
		}
	}
	if !found {
		t.Fatal("no close line of the read card's judgment with the reason reported")
	}
	if got := len(r.zset(jk("due"))); got != 0 {
		t.Fatalf("the due set holds %v after every judgment closed", r.zset(jk("due")))
	}
}

// TestJLatenessCloseIsIdempotent (E7): the same ending run again on the state
// the first run left closes nothing, and a step that ends no timed state makes
// no note and no line.
func TestJLatenessCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	r.send(jFleetSeed())
	r.send(seedRequest())
	r.step(jReq(JOpOpen, jTypeWorkLate, "untaken", "w1"))
	take := tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ready", To: "m1:working", IDs: []string{"w1"},
		Unset: []string{"due_untaken"}, About: []string{"p1"}}
	if reply := r.send(&Request{Epoch: "0", Meta: Meta{Verb: "take", Actor: "m1"}, Body: Body{Entries: []tset.Entry{take}}}); reply.Reply.Lines != 2 {
		t.Fatalf("the take wrote %d lines, want 2", reply.Reply.Lines)
	}
	// The second run of J, on the keys the first left and the before-state it read.
	ks := r.tw.keys
	st := &State{Prefix: testPrefix, Epoch: "0", NowMS: tset.Decimal(jStr(r.wall())), Names: testNames, Keys: &Keys{ks: ks}, Entries: []tset.Entry{take}}
	obs := jObs(jCard(sprint.Fleet, "w1", "m1", "ready", map[string]string{"due_untaken": "5000"}))
	if notes, jp, ref := JDecide(st, nil, obs); ref != nil || len(notes) != 0 || len(jp.Notes) != 0 {
		t.Fatalf("the second run of the ending made %d notes: %v", len(notes), ref)
	}
	// A step of entries that end nothing makes no line beyond its own.
	other := tset.Entry{Kind: "move", Table: sprint.Work, From: "s1:waiting", To: "s1:ready", IDs: []string{"p1"}, About: []string{"p1"}}
	if reply := r.send(&Request{Epoch: "0", Meta: Meta{Rule: "resolve", Tick: true}, Body: Body{Entries: []tset.Entry{other}}}); reply.Reply.Lines != 1 {
		t.Fatalf("a move that ends no timed state wrote %d lines, want its own", reply.Reply.Lines)
	}
}

// TestJNoteIdsFromSeqs (8.1 IT15): a note's id is n and the seq of its line, from
// LogPlan.NoteSeqs, and a request that makes no line has none: of three
// requests whose middle one is already open, the two that open have the next two
// seqs and their ids, and the field of each names its own line.
func TestJNoteIdsFromSeqs(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	r.step(jReq(JOpOpen, jtCannotAsk, "c", "p2")) // n1
	reply := r.step(jReq(JOpOpen, jtCannotAsk, "c", "p1"), jReq(JOpOpen, jtCannotAsk, "c", "p2"), jReq(JOpOpen, jtNoMember, "c", "sprint"))
	if reply.Reply.Lines != 2 || reply.Reply.FirstSeq != "2" || reply.Reply.LastSeq != "3" {
		t.Fatalf("lines %d seqs %s..%s, want 2 lines 2..3: the skipped request has no line",
			reply.Reply.Lines, reply.Reply.FirstSeq, reply.Reply.LastSeq)
	}
	r.wantHash(jk("jopen:p1"), map[string]string{"cannot ask|c": "n2"})
	r.wantHash(jk("jopen:p2"), map[string]string{"cannot ask|c": "n1"})
	r.wantHash(jk("jopen:sprint"), map[string]string{"no fleet member is up|c": "n3"})
	r.wantHash(jk("jn"), map[string]string{"n1": "1", "n2": "1", "n3": "1"})
	if got := r.list(jk("notes")); !reflect.DeepEqual(got, []string{"1", "2", "3"}) {
		t.Fatalf("the notes list is %v, want the seqs of the three lines", got)
	}
	for seq, about := range map[int]string{2: "p1", 3: "sprint"} {
		if l := r.line(seq); !reflect.DeepEqual(l.About, []string{about}) {
			t.Fatalf("line %d names %v, want %s", seq, l.About, about)
		}
	}
}

// TestJNotesCountPerNote (8.1 IT15): notes are counted for each note line and
// never for each subject: one note naming 2,000 subjects is one note and 2,000
// about ids; the step's bound of 100 notes, and of 4,000 about ids, refuse
// LIMIT whole; and JCost reports the notes of a step as its lines.
func TestJNotesCountPerNote(t *testing.T) {
	t.Parallel()
	many := func(prefix string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = prefix + strconv.Itoa(i)
		}
		return out
	}
	r := newJRig(t)
	big := jReq(JOpOpen, jtCannotAsk, "c", many("p", 2000)...)
	reply := r.step(big)
	if reply.Reply.Lines != 1 {
		t.Fatalf("a note naming 2,000 subjects wrote %d lines, want 1", reply.Reply.Lines)
	}
	if n := len(r.hash(jk("jn"))); n != 1 || r.hash(jk("jn"))["n1"] != "2000" {
		t.Fatalf("jn is %v, want one note of 2,000", r.hash(jk("jn")))
	}

	st := &State{Prefix: testPrefix, Epoch: "0", NowMS: tset.Decimal(jStr(r.wall())), Names: testNames, Keys: &Keys{ks: newKeyspace()}}
	cost, ref := JCost(st, []NoteReq{big, jReq(JOpOpen, jtNoMember, "c", "sprint")}, nil, nil)
	if ref != nil || cost.Notes != 2 {
		t.Fatalf("JCost of 2 requests: %+v, %v; want 2 notes however many subjects", cost, ref)
	}
	// 1 RPUSH, 3 records each of two notes less the done type's, 2,001 jopen HSETs:
	// the count is per command, and a command for each subject is how jopen is kept.
	if cost.Commands < 2001 || cost.ArgvBytes == 0 || cost.Probes < 2001 {
		t.Fatalf("JCost of 2,001 subjects: %+v", cost)
	}

	// 100 notes are the bound of a step; a 101st is LIMIT, and nothing was written.
	var notices []NoteReq
	for i := 0; i <= jNotesMax; i++ {
		notices = append(notices, jReq(JOpKnow, "ci green", strconv.Itoa(i), "p"+strconv.Itoa(i)))
	}
	img := r.img()
	if ref := r.refused(notices...); ref.Code != CodeLimit || ref.Detail.Budget != "notes" || ref.Phase != PhaseJ {
		t.Fatalf("101 notes: %+v, want LIMIT on notes from J", ref)
	}
	if r.img() != img {
		t.Fatal("a refused step changed the twin")
	}
	if reply := r.step(notices[:jNotesMax]...); reply.Reply.Lines != jNotesMax {
		t.Fatalf("100 notices wrote %d lines", reply.Reply.Lines)
	}
	// 3 notes of 1,400 subjects are 4,200 about ids: over the step's 4,000.
	a, b, c := many("a", 1400), many("b", 1400), many("c", 1400)
	if ref := r.refused(jReq(JOpKnow, "ci green", "", a...), jReq(JOpKnow, "ci green", "x", b...), jReq(JOpKnow, "ci green", "y", c...)); ref.Code != CodeLimit || ref.Detail.Budget != "about" {
		t.Fatalf("4,200 about ids: %+v, want LIMIT on about", ref)
	}
	if ref := r.refused(jReq(JOpKnow, "ci green", "", many("z", 2001)...)); ref.Code != CodeLimit || ref.Detail.Budget != "about" {
		t.Fatalf("a note of 2,001 subjects: %+v, want LIMIT on about", ref)
	}
}

// TestJOverdueOnOpen (8.1 IT15): opening writes overdue:<note> at R plus ten
// minutes, jnotes at R and jn at the count, where R is running time: the wall
// time less STOPPED time, and less the present STOPPED span. "The sprint is done"
// is never overdue. askwait takes the subject of "cannot ask" at R, and of no
// other type.
func TestJOverdueOnOpen(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	wall := r.wall()
	r.step(jReq(JOpOpen, jtCannotAsk, "c", "p1"), jReq(JOpOpen, jtBlocked, "n0", "w1", "w2"))
	R := float64(wall)
	r.wantZSet(jk("due"), map[string]float64{"overdue:n1": R + 600_000, "overdue:n2": R + 600_000})
	r.wantZSet(jk("jnotes"), map[string]float64{"n1": R, "n2": R})
	r.wantHash(jk("jn"), map[string]string{"n1": "1", "n2": "2"})
	r.wantZSet(jk("askwait"), map[string]float64{"p1": R})

	// The sprint is done: a note that is never overdue.
	r.step(jReq(JOpOpen, jtDone, "d", "sprint"))
	if _, there := r.zset(jk("due"))["overdue:n3"]; there {
		t.Fatal("\"the sprint is done\" has an overdue entry; it is never overdue (2.2)")
	}
	if _, there := r.zset(jk("jnotes"))["n3"]; !there {
		t.Fatal("\"the sprint is done\" is not in jnotes")
	}

	// A machine STOPPED for 100 s before now, and one STOPPED since 5 s ago.
	r.now = r.now.Add(time.Minute)
	r.put(Command("HSET", testPrefix+"sprint:clock", kindHash, "stopped_ms", "100000", "stopped_since_ms", ""))
	r.step(jReq(JOpOpen, jtBlocked, "n1", "w3"))
	r1 := float64(r.wall() - 100_000)
	r.wantZSet(jk("jnotes"), map[string]float64{"n1": R, "n2": R, "n3": R, "n4": r1})
	if got := r.zset(jk("due"))["overdue:n4"]; got != r1+600_000 {
		t.Fatalf("overdue:n4 at %v, want R + 10 min = %v with 100 s STOPPED before now", got, r1+600_000)
	}
	r.now = r.now.Add(time.Minute)
	r.put(Command("HSET", testPrefix+"sprint:clock", kindHash, "stopped_since_ms", jStr(r.wall()-5_000)))
	r.step(jReq(JOpOpen, jtBlocked, "n2", "w4"))
	r2 := float64(r.wall() - 5_000 - 100_000)
	if got := r.zset(jk("jnotes"))["n5"]; got != r2 {
		t.Fatalf("jnotes n5 at %v, want R = %v while STOPPED since 5 s ago", got, r2)
	}
}

// TestJCloseCountsSubjectsPerNote: a close takes each closed subject off its
// note's count, and the note leaves jnotes and loses its overdue entry only when
// the count reaches zero, in the step that takes the last: two requests of one
// step that close the two last subjects are counted together (one write of the
// count, not two that overwrite each other); a close of a subject that has no
// judgment writes nothing.
func TestJCloseCountsSubjectsPerNote(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	r.step(jReq(JOpOpen, jtBlocked, "n0", "w1", "w2", "w3"))
	r.wantHash(jk("jn"), map[string]string{"n1": "3"})

	r.step(jReq(JOpClose, jtBlocked, "n0", "w1"))
	r.wantHash(jk("jn"), map[string]string{"n1": "2"})
	r.wantHash(jk("jopen:w1"), nil)
	if _, there := r.zset(jk("jnotes"))["n1"]; !there {
		t.Fatal("n1 left jnotes with two open subjects")
	}
	if _, there := r.zset(jk("due"))["overdue:n1"]; !there {
		t.Fatal("n1 lost its overdue entry with two open subjects")
	}

	img := r.img()
	if none := r.step(jReq(JOpClose, jtBlocked, "n0", "w1")); none.Reply.Lines != 0 || r.img() != img {
		t.Fatalf("a close of a subject with no judgment wrote %d lines or changed the twin", none.Reply.Lines)
	}

	// Two requests of one type and cause are one note naming both subjects.
	two := r.step(jReq(JOpClose, jtBlocked, "n0", "w2"), jReq(JOpClose, jtBlocked, "n0", "w3"))
	if two.Reply.Lines != 1 {
		t.Fatalf("two closes of one note wrote %d lines, want the one note naming both", two.Reply.Lines)
	}
	if l := r.line(3); !reflect.DeepEqual(l.About, []string{"w2", "w3"}) {
		t.Fatalf("the close names %v, want w2 and w3", l.About)
	}
	r.wantHash(jk("jn"), nil)
	r.wantZSet(jk("jnotes"), nil)
	r.wantZSet(jk("due"), nil)
	for _, s := range []string{"w1", "w2", "w3"} {
		r.wantHash(jk("jopen:"+s), nil)
	}
}

// TestJUpdateWritesOnlyTheLine: an update of an open judgment whose text changed
// is a line of kind judgment with the verb "updated" (which Layer 3 reads as
// neither opening nor closing one) and changes no key but the notes list and the
// note's digest; an update of a held or an absent one writes nothing.
func TestJUpdateWritesOnlyTheLine(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	r.step(jReq(JOpOpen, jtCannotAsk, "c", "p1", "p2"))
	r.step(jHold(jtCannotAsk, "c", r.wall()+1000, "p2")) // p2 is held
	keysBefore := r.keys()
	up := jReq(JOpUpdate, jtCannotAsk, "c", "p1", "p2", "p3")
	up.Text = "now there is one reader"
	reply := r.step(up)
	if reply.Reply.Lines != 1 {
		t.Fatalf("the update wrote %d lines, want 1 (p2 is held and p3 has none)", reply.Reply.Lines)
	}
	l := r.line(3)
	if l.Meta["op"] != "update" || l.Meta["verb"] != "updated" || l.Meta["kind"] != sprint.Judgment || l.Meta["note"] != "n1" ||
		l.Meta["text"] != up.Text || !reflect.DeepEqual(l.About, []string{"p1"}) {
		t.Fatalf("the update line is %+v", l)
	}
	if ev := r.eventOf(3); ev.Opens || ev.Closes {
		t.Fatalf("the update line reads as %+v: it neither opens nor closes", ev)
	}
	after := r.keys()
	wantDigest := jDigest(up.Text, nil)
	if got := after[jk("jtext")].Hash["n1"]; got != wantDigest {
		t.Fatalf("jtext holds %q, want the digest of the new text %q", got, wantDigest)
	}
	keysBefore[jk("notes")] = after[jk("notes")] // the keys an update writes
	keysBefore[jk("jtext")] = after[jk("jtext")]
	if !reflect.DeepEqual(keysBefore, after) {
		t.Fatal("an update changed a key besides the notes list and the digest")
	}
	if got := r.list(jk("notes")); !reflect.DeepEqual(got, []string{"1", "2", "3"}) {
		t.Fatalf("the notes list is %v", got)
	}
}

// TestJUpdateIsIdempotent (E7, errata 3): an update whose text and decisions are
// the note's own writes nothing, a second run of a changed one included; one
// that changes either writes a line and the new digest; emptying the text is a
// change, and an update of an empty text against a note with none is not.
func TestJUpdateIsIdempotent(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	open := jReq(JOpOpen, jtCannotAsk, "c", "p1", "p2")
	open.Text, open.Decisions = "no two readers are free", []string{"wait", "drop"}
	r.step(open)
	img := r.img()
	same := open
	same.Op = JOpUpdate
	if reply := r.step(same); reply.Reply.Lines != 0 || r.img() != img {
		t.Fatalf("an update of the note's own text and decisions wrote %d lines or changed the twin", reply.Reply.Lines)
	}
	changed := same
	changed.Text = "one reader is free"
	if reply := r.step(changed); reply.Reply.Lines != 1 {
		t.Fatalf("an update that changed the text wrote %d lines, want 1", reply.Reply.Lines)
	}
	img = r.img()
	if reply := r.step(changed); reply.Reply.Lines != 0 || r.img() != img {
		t.Fatalf("the second run of the same update wrote %d lines or changed the twin", reply.Reply.Lines)
	}
	decisions := changed
	decisions.Decisions = []string{"wait", "drop", "reader add"}
	if reply := r.step(decisions); reply.Reply.Lines != 1 {
		t.Fatalf("an update that changed the decisions wrote %d lines, want 1", reply.Reply.Lines)
	}
	if got := r.hash(jk("jtext"))["n1"]; got != jDigest(decisions.Text, decisions.Decisions) {
		t.Fatalf("jtext holds %q after the decisions changed", got)
	}
	empty := jReq(JOpUpdate, jtCannotAsk, "c", "p1", "p2")
	if reply := r.step(empty); reply.Reply.Lines != 1 {
		t.Fatalf("an update to no text wrote %d lines, want 1", reply.Reply.Lines)
	}
	img = r.img()
	if reply := r.step(empty); reply.Reply.Lines != 0 || r.img() != img {
		t.Fatalf("the second update to no text wrote %d lines or changed the twin", reply.Reply.Lines)
	}

	// A note opened with no text has no digest; an update with none is no change.
	r.step(jReq(JOpOpen, jtBlocked, "n0", "w1"))
	if _, there := r.hash(jk("jtext"))["n5"]; there {
		t.Fatal("a note with no text and no decisions has a digest")
	}
	img = r.img()
	if reply := r.step(jReq(JOpUpdate, jtBlocked, "n0", "w1")); reply.Reply.Lines != 0 || r.img() != img {
		t.Fatalf("an update of no text against a note with none wrote %d lines", reply.Reply.Lines)
	}
}

// TestJDigestForgottenWithTheNote: the digest of a note goes when its last open
// subject does, in the step that closes or holds it.
func TestJDigestForgottenWithTheNote(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	open := jReq(JOpOpen, jtCannotAsk, "c", "p1", "p2")
	open.Text = "no reader"
	r.step(open)
	r.wantHash(jk("jtext"), map[string]string{"n1": jDigest("no reader", nil)})
	r.step(jReq(JOpClose, jtCannotAsk, "c", "p1"))
	if _, there := r.hash(jk("jtext"))["n1"]; !there {
		t.Fatal("the digest went with one of two subjects")
	}
	r.step(jHold(jtCannotAsk, "c", r.wall()+1000, "p2"))
	r.wantHash(jk("jtext"), nil)
}

// TestJKnowAndRequestWriteOnlyTheLine: a notice is a line of kind happened, a
// request a plain line, and neither touches a judgment key; their subjects are
// listed once.
func TestJKnowAndRequestWriteOnlyTheLine(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	know := jReq(JOpKnow, "ci green", "", "p1", "p2", "p1")
	know.Text = "ci is green at the head"
	reply := r.step(know, jReq(JOpRequest, "unfrozen", "", "s1", "s2"))
	if reply.Reply.Lines != 2 {
		t.Fatalf("a notice and a request wrote %d lines, want 2", reply.Reply.Lines)
	}
	if l := r.line(1); l.Meta["kind"] != sprint.Happened || l.Meta["op"] != "know" || !reflect.DeepEqual(l.About, []string{"p1", "p2"}) ||
		l.Meta["cause"] != nil || l.Meta["text"] != know.Text {
		t.Fatalf("the notice line is %+v", l)
	}
	if l := r.line(2); l.Meta["kind"] != nil || l.Meta["op"] != "request" || l.Meta["type"] != "unfrozen" {
		t.Fatalf("the request line is %+v", l)
	}
	keys := r.keys()
	if len(keys) != 1 || !reflect.DeepEqual(keys[jk("notes")].List, []string{"1", "2"}) {
		t.Fatalf("a notice and a request wrote keys %v, want only the notes list", keys)
	}
}

// TestJReviewWaitMovesOverdue (1.3.4): a wait on a judgment the tick does not
// keep moves its overdue entry to the review time and leaves it open; a wait on
// one that is never overdue writes only its line.
func TestJReviewWaitMovesOverdue(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	r.step(jReq(JOpOpen, jtBlocked, "n0", "w1"), jReq(JOpOpen, jtDone, "d", "sprint"))
	review := r.wall() + 3_600_000
	reply := r.step(jHold(jtBlocked, "n0", review, "w1"), jHold(jtDone, "d", review, "sprint"))
	if reply.Reply.Lines != 2 {
		t.Fatalf("two waits wrote %d lines", reply.Reply.Lines)
	}
	r.wantZSet(jk("due"), map[string]float64{"overdue:n1": float64(review)})
	r.wantHash(jk("jopen:w1"), map[string]string{jtBlocked + "|n0": "n1"})
	r.wantHash(jk("jopen:sprint"), map[string]string{jtDone + "|d": "n2"})
	if l := r.line(3); l.Meta["op"] != "review" || l.Meta["until"] != jStr(review) || l.Meta["kind"] != nil {
		t.Fatalf("the review line is %+v", l)
	}
	if got := len(r.hash(jk("jn"))); got != 2 {
		t.Fatalf("jn has %d notes; both stay open", got)
	}
}

// TestJReviewWaitFindsTheReviewTimeAlready (E7): a wait on a judgment the tick
// does not keep moves the overdue entry of its note to the review time, once:
// run again at the same time it finds the entry there and writes nothing, not
// even its line, and run at another time it moves the entry again. A wait naming
// two subjects of one note reads the entry once, and the read is a probe that
// JCost counts.
func TestJReviewWaitFindsTheReviewTimeAlready(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	r.step(jReq(JOpOpen, jtBlocked, "n0", "w1", "w2"))
	review := r.wall() + 3_600_000
	wait := func(at int64, subjects ...string) *StepReply {
		r.t.Helper()
		return r.step(jHold(jtBlocked, "n0", at, subjects...))
	}
	if reply := wait(review, "w1", "w2"); reply.Reply.Lines != 1 {
		t.Fatalf("the first wait wrote %d lines, want its one", reply.Reply.Lines)
	}
	r.wantZSet(jk("due"), map[string]float64{"overdue:n1": float64(review)})
	img := r.img()
	if reply := wait(review, "w1", "w2"); reply.Reply.Lines != 0 || r.img() != img {
		t.Fatalf("the second wait wrote %d lines or changed the twin", reply.Reply.Lines)
	}
	// One of the note's subjects alone names the same note, and finds the same time.
	if reply := wait(review, "w2"); reply.Reply.Lines != 0 || r.img() != img {
		t.Fatalf("a wait on one subject of the note wrote %d lines or changed the twin", reply.Reply.Lines)
	}
	// Another time is another wait: the entry moves.
	later := review + 60_000
	if reply := wait(later, "w1", "w2"); reply.Reply.Lines != 1 {
		t.Fatalf("a wait at another time wrote %d lines, want its one", reply.Reply.Lines)
	}
	r.wantZSet(jk("due"), map[string]float64{"overdue:n1": float64(later)})

	// What the read costs: one probe for the entry of a note, however many of its
	// subjects the wait names, and a probe in JCost.
	st := jUnitState(r.wall(),
		Command("HSET", jk("jopen:w1"), kindHash, jField(jtBlocked, "n0"), "n1"),
		Command("HSET", jk("jopen:w2"), kindHash, jField(jtBlocked, "n0"), "n1"),
		Command("HSET", jk("jn"), kindHash, "n1", "2"))
	one, ref := JCost(st, []NoteReq{jHold(jtBlocked, "n0", later, "w1")}, nil, nil)
	if ref != nil {
		t.Fatal(ref)
	}
	both, ref := JCost(st, []NoteReq{jHold(jtBlocked, "n0", later, "w1", "w2")}, nil, nil)
	if ref != nil {
		t.Fatal(ref)
	}
	if one.Probes != 2 || both.Probes != 3 { // the field of each subject, and the one entry
		t.Fatalf("a wait on one subject costs %d probes, on two %d; want 2 and 3", one.Probes, both.Probes)
	}
}

// TestJWaitOnANeverOverdueTypeWritesOnlyItsLine: the one wait that cannot find
// its own earlier run is the wait on "the sprint is done", which is never overdue
// and so has no entry to move: it changes no state, and writes its line, and the
// list of note lines that holds it, each time it is run. It is a verb's event,
// which the verb layer deduplicates by op and intent, and it is outside E7; the
// head of twin_j.go says so, and this holds it to that.
func TestJWaitOnANeverOverdueTypeWritesOnlyItsLine(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	r.step(jReq(JOpOpen, jtDone, "d", "sprint"))
	at := r.wall() + 3_600_000
	before := r.keys()
	for run := 1; run <= 2; run++ {
		if reply := r.step(jHold(jtDone, "d", at, "sprint")); reply.Reply.Lines != 1 {
			t.Fatalf("run %d wrote %d lines, want its one", run, reply.Reply.Lines)
		}
	}
	after := r.keys()
	notes := jk("notes")
	if got := after[notes].List; len(got) != 3 {
		t.Fatalf("the notes list is %v, want the open and the two waits", got)
	}
	for key, want := range before {
		if key != notes && !reflect.DeepEqual(after[key], want) {
			t.Errorf("a wait on the done judgment changed %s: %+v, was %+v", key, after[key], want)
		}
	}
	if len(after) != len(before) {
		t.Errorf("a wait on the done judgment made %d keys, had %d", len(after), len(before))
	}
}

// TestJStoppedWaitSetsStophold (1.3.4): a wait on the STOPPED judgment holds it
// as any tick-kept one is held, but enters no hold entry: it sets stophold_ms
// to its wall time, since R does not move while STOPPED.
func TestJStoppedWaitSetsStophold(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	r.step(jReq(JOpOpen, jtStopped, "c", "sprint"))
	wall := r.wall() + 120_000
	r.step(jHold(jtStopped, "c", wall, "sprint"))
	r.wantHash(jk("jopen:sprint"), map[string]string{jtStopped + "|c": "hn1"})
	r.wantHash(testPrefix+"sprint:clock", map[string]string{"stophold_ms": jStr(wall)})
	if _, there := r.zset(jk("due"))["hold:n1"]; there {
		t.Fatal("a wait on the STOPPED judgment entered a hold entry in running time")
	}
}

// TestJAskwaitFollowsCannotAsk: askwait names a primary while "cannot ask" is
// open or held on it, of any cause: a close or an unhold of one cause leaves it
// while another stands, a hold keeps it, and the last to go takes it out; an
// open in the step that closes another keeps it.
func TestJAskwaitFollowsCannotAsk(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	R := float64(r.wall())
	r.step(jReq(JOpOpen, jtCannotAsk, "c", "p1", "p2"), jReq(JOpOpen, jtCannotAsk, "d", "p1"))
	r.wantZSet(jk("askwait"), map[string]float64{"p1": R, "p2": R})
	r.step(jReq(JOpClose, jtCannotAsk, "c", "p1", "p2"))
	r.wantZSet(jk("askwait"), map[string]float64{"p1": R}) // p2 had only c; p1 has d
	r.step(jHold(jtCannotAsk, "d", r.wall()+1000, "p1"))
	r.wantZSet(jk("askwait"), map[string]float64{"p1": R}) // held is still named
	// Raise c again and close d in one step: p1 keeps its place.
	r.step(jReq(JOpOpen, jtCannotAsk, "c", "p1"), jReq(JOpUnhold, jtCannotAsk, "d", "p1"))
	r.wantZSet(jk("askwait"), map[string]float64{"p1": R})
	r.step(jReq(JOpClose, jtCannotAsk, "c", "p1"))
	r.wantZSet(jk("askwait"), nil)
}

// TestJSecondRunWritesNothing (E7, ReplayNoop): the requests of a step run a
// second time, with nothing changed between, write nothing: open, close, hold,
// unhold and the wait on a judgment the tick does not keep (a review wait, which
// finds the overdue entry already at its review time) each find their state
// already as the first run left it.
func TestJSecondRunWritesNothing(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	until := r.wall() + 60_000
	for _, tc := range []struct {
		name string
		step []NoteReq
		prep []NoteReq
	}{
		{"open", []NoteReq{jReq(JOpOpen, jtCannotAsk, "c", "p1", "p2")}, nil},
		{"update", []NoteReq{{Op: JOpUpdate, Type: jtCannotAsk, Cause: "c", Subjects: []string{"p1"}, Text: "changed"}}, nil},
		{"hold", []NoteReq{jHold(jtCannotAsk, "c", until, "p1")}, nil},
		{"unhold", []NoteReq{jReq(JOpUnhold, jtCannotAsk, "c", "p1")}, nil},
		{"close", []NoteReq{jReq(JOpClose, jtCannotAsk, "c", "p2")}, nil},
		{"review wait", []NoteReq{jHold(jtBlocked, "n0", until, "w1")}, []NoteReq{jReq(JOpOpen, jtBlocked, "n0", "w1")}},
	} {
		if tc.prep != nil {
			r.step(tc.prep...)
		}
		first := r.step(tc.step...)
		if first.Reply.Lines != 1 {
			t.Fatalf("%s: the first run wrote %d lines, want 1", tc.name, first.Reply.Lines)
		}
		img := r.img()
		if second := r.step(tc.step...); second.Reply.Lines != 0 || r.img() != img {
			t.Fatalf("%s: the second run wrote %d lines or changed the twin", tc.name, second.Reply.Lines)
		}
	}
}

// TestJRefusals: a request J refuses is refused whole, from phase J and with
// the code of its fault, and leaves the twin as it was: the caller's faults are
// REQUEST (an op, subject, word, type or a repeated field), the step's bounds
// are LIMIT, and a key of the sprint's that disagrees with itself is DRIFT, of
// another type WRONGTYPE, or a clock that is no clock CONFIG.
func TestJRefusals(t *testing.T) {
	t.Parallel()
	jopen := func(s string) string { return jk("jopen:" + s) }
	cases := []struct {
		name string
		seed []Cmd
		reqs []NoteReq
		code string
	}{
		{"an op J does not take", nil, []NoteReq{jReq("frobnicate", jtCannotAsk, "c", "p1")}, CodeRequest},
		{"no subject", nil, []NoteReq{jReq(JOpOpen, jtCannotAsk, "c")}, CodeRequest},
		{"an empty subject", nil, []NoteReq{jReq(JOpOpen, jtCannotAsk, "c", "")}, CodeRequest},
		{"a subject over 256 bytes", nil, []NoteReq{jReq(JOpOpen, jtCannotAsk, "c", strings.Repeat("s", 257))}, CodeRequest},
		{"a judgment type 2.2 does not have", nil, []NoteReq{jReq(JOpOpen, "made up", "c", "p1")}, CodeRequest},
		{"a notice asked as a judgment", nil, []NoteReq{jReq(JOpOpen, "ci green", "c", "p1")}, CodeRequest},
		{"a judgment asked as a notice", nil, []NoteReq{jReq(JOpKnow, jtCannotAsk, "", "p1")}, CodeRequest},
		{"a type with a bar", nil, []NoteReq{jReq(JOpOpen, "a|b", "c", "p1")}, CodeRequest},
		{"an empty cause on a judgment", nil, []NoteReq{jReq(JOpOpen, jtCannotAsk, "", "p1")}, CodeRequest},
		{"a hold with no time", nil, []NoteReq{jReq(JOpHold, jtCannotAsk, "c", "p1")}, CodeRequest},
		{"text that is not UTF-8", nil, []NoteReq{{Op: JOpOpen, Type: jtCannotAsk, Cause: "c", Subjects: []string{"p1"}, Text: "\xff"}}, CodeRequest},
		{"one field named by two requests", nil, []NoteReq{jReq(JOpOpen, jtCannotAsk, "c", "p1", "p2"), jReq(JOpClose, jtCannotAsk, "c", "p2")}, CodeRequest},
		{"2,001 subjects", nil, []NoteReq{jReq(JOpKnow, "ci green", "", jSeqNames("p", 2001)...)}, CodeLimit},
		{"a value in jopen that is no note", []Cmd{Command("HSET", jopen("p1"), kindHash, jtCannotAsk+"|c", "zzz")},
			[]NoteReq{jReq(JOpOpen, jtCannotAsk, "c", "p1")}, jCodeDrift},
		{"a note open in jopen with no count in jn", []Cmd{Command("HSET", jopen("p1"), kindHash, jtBlocked+"|c", "n1")},
			[]NoteReq{jReq(JOpClose, jtBlocked, "c", "p1")}, jCodeDrift},
		{"a count in jn below what a close takes", []Cmd{Command("HSET", jopen("p1"), kindHash, jtBlocked+"|c", "n1"),
			Command("HSET", jopen("p2"), kindHash, jtBlocked+"|c", "n1"), Command("HSET", jk("jn"), kindHash, "n1", "1")},
			[]NoteReq{jReq(JOpClose, jtBlocked, "c", "p1", "p2")}, jCodeDrift},
		{"a jopen key of another type", []Cmd{Command("ZADD", jopen("p1"), kindZSet, "1", "m")},
			[]NoteReq{jReq(JOpOpen, jtCannotAsk, "c", "p1")}, CodeWrongType},
		{"a jn key of another type", []Cmd{Command("HSET", jopen("p1"), kindHash, jtBlocked+"|c", "n1"), Command("ZADD", jk("jn"), kindZSet, "1", "m")},
			[]NoteReq{jReq(JOpClose, jtBlocked, "c", "p1")}, CodeWrongType},
		{"a clock that is no clock", []Cmd{Command("HSET", testPrefix+"sprint:clock", kindHash, "stopped_ms", "soon")},
			[]NoteReq{jReq(JOpOpen, jtCannotAsk, "c", "p1")}, CodeConfig},
		{"a held field with no held count", []Cmd{Command("HSET", jopen("p1"), kindHash, jtCannotAsk+"|c", "hn1")},
			[]NoteReq{jReq(JOpClose, jtCannotAsk, "c", "p1")}, jCodeDrift},
		{"a held count below what a close takes", []Cmd{Command("HSET", jopen("p1"), kindHash, jtCannotAsk+"|c", "hn1"),
			Command("HSET", jopen("p2"), kindHash, jtCannotAsk+"|c", "hn1"), Command("HSET", jk("jh"), kindHash, "n1", "1")},
			[]NoteReq{jReq(JOpClose, jtCannotAsk, "c", "p1", "p2")}, jCodeDrift},
		{"a held count that is no count", []Cmd{Command("HSET", jopen("p1"), kindHash, jtCannotAsk+"|c", "n1"),
			Command("HSET", jk("jn"), kindHash, "n1", "1"), Command("HSET", jk("jh"), kindHash, "n1", "zero")},
			[]NoteReq{jHold(jtCannotAsk, "c", 1_790_000_000_000, "p1")}, jCodeDrift},
		{"a jh key of another type", []Cmd{Command("HSET", jopen("p1"), kindHash, jtCannotAsk+"|c", "hn1"), Command("ZADD", jk("jh"), kindZSet, "1", "m")},
			[]NoteReq{jReq(JOpUnhold, jtCannotAsk, "c", "p1")}, CodeWrongType},
		{"a jtext key of another type", []Cmd{Command("HSET", jopen("p1"), kindHash, jtCannotAsk+"|c", "n1"),
			Command("HSET", jk("jn"), kindHash, "n1", "1"), Command("ZADD", jk("jtext"), kindZSet, "1", "m")},
			[]NoteReq{{Op: JOpUpdate, Type: jtCannotAsk, Cause: "c", Subjects: []string{"p1"}, Text: "x"}}, CodeWrongType},
		{"a quarantine key of another type", []Cmd{Command("ZADD", jk("quarantine"), kindZSet, "1", "m")},
			[]NoteReq{jReq(JOpOpen, jtCannotAsk, "c", "p1")}, CodeWrongType},
		{"a due key of another type under a review wait", []Cmd{Command("HSET", jopen("p1"), kindHash, jtBlocked+"|c", "n1"),
			Command("HSET", jk("jn"), kindHash, "n1", "1"), Command("HSET", jk("due"), kindHash, "overdue:n1", "1")},
			[]NoteReq{jHold(jtBlocked, "c", 1_790_000_000_000, "p1")}, CodeWrongType},
	}
	for _, tc := range cases {
		r := newJRig(t)
		if tc.seed != nil {
			r.put(tc.seed...)
		}
		img := r.img()
		ref := r.refused(tc.reqs...)
		if ref.Code != tc.code || ref.Phase != PhaseJ {
			t.Errorf("%s: refused %s from %q, want %s from J (%s)", tc.name, ref.Code, ref.Phase, tc.code, ref.Message)
		}
		if r.img() != img {
			t.Errorf("%s: a refused step changed the twin", tc.name)
		}
	}
}

func jSeqNames(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = prefix + strconv.Itoa(i)
	}
	return out
}

// TestJTypesAreInTheTables: every type J treats by name is a row of IT06's
// table of 2.2, the ones it holds or waits on are kept by the tick (a wait
// holds them), each due kind of 1.2 has a lateness judgment that is a row, and
// the notices the tests use are rows of 2.5. A table that moves breaks here.
func TestJTypesAreInTheTables(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{jTypeCannotAsk, jTypeStopped, jTypeDone, jTypeWorkLate, jTypeReadLate, jTypeStreamLate, jtNoMember, jtBlocked} {
		if _, ok := sprint.Judgments[typ]; !ok {
			t.Errorf("%q is no row of 2.2", typ)
		}
	}
	for _, typ := range []string{jTypeCannotAsk, jTypeStopped, jtNoMember, jTypeWorkLate, jTypeReadLate, jTypeStreamLate} {
		if kept, _ := jJudgment(typ); !kept {
			t.Errorf("%q is not kept by the tick: a wait on it would not hold", typ)
		}
	}
	for _, typ := range []string{jtBlocked, jTypeDone} {
		if kept, _ := jJudgment(typ); kept {
			t.Errorf("%q is kept by the tick: a wait on it would hold, not review", typ)
		}
	}
	for _, k := range sprint.DueKinds {
		typ, cause, ok := LatenessJudgment(k.Kind)
		if k.Kind == "idle" {
			continue
		}
		if !ok || cause != k.Kind || sprint.Judgments[typ].Type != typ {
			t.Errorf("due kind %q: lateness judgment %q, %q, %v; want a row of 2.2 caused by the kind", k.Kind, typ, cause, ok)
		}
		why := jEndReasons[k.Kind]
		if why.other == "" || why.cleared == "" {
			t.Errorf("due kind %q has no reason a close carries for a move elsewhere or a cleared field: %+v", k.Kind, why)
		}
	}
	for _, typ := range []string{"ci green", "work came back ok"} {
		if !jNotice(typ) {
			t.Errorf("%q is no row of 2.5", typ)
		}
	}
}

// TestJLinesParseAsEvents: each op's line reads as an event under Layer 3's own
// parser, with the kind the design gives it: an open opens, a close and an unhold
// close (and queue the owner key), an update, a hold, a review wait and a request
// are plain, and a notice happened.
func TestJLinesParseAsEvents(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	r.step(jReq(JOpOpen, jtNoMember, "c", "sprint"), jReq(JOpOpen, jtBlocked, "n0", "w1"))                      // 1, 2
	r.step(NoteReq{Op: JOpUpdate, Type: jtNoMember, Cause: "c", Subjects: []string{"sprint"}, Text: "changed"}) // 3
	r.step(jHold(jtBlocked, "n0", r.wall()+1000, "w1"))                                                         // 4: review
	r.step(jHold(jtNoMember, "c", r.wall()+1000, "sprint"))                                                     // 5: hold
	r.step(jReq(JOpUnhold, jtNoMember, "c", "sprint"))                                                          // 6
	r.step(jReq(JOpKnow, "ci green", "", "p1"))                                                                 // 7
	r.step(jReq(JOpRequest, "unfrozen", "", "s1"))                                                              // 8
	r.step(jReq(JOpClose, jtBlocked, "n0", "w1"))                                                               // 9
	want := []struct {
		seq           int
		kind          string
		opens, closes bool
	}{
		{1, sprint.Judgment, true, false}, {3, sprint.Judgment, false, false}, {4, "note", false, false},
		{5, "note", false, false}, {6, sprint.Decided, false, true}, {7, sprint.Happened, false, false},
		{8, "note", false, false}, {9, sprint.Decided, false, true},
	}
	for _, w := range want {
		ev := r.eventOf(w.seq)
		if ev.Kind != w.kind || ev.Opens != w.opens || ev.Closes != w.closes {
			t.Errorf("line %d reads as kind %q opens %v closes %v, want %q %v %v", w.seq, ev.Kind, ev.Opens, ev.Closes, w.kind, w.opens, w.closes)
		}
	}
}

// TestJOpLessStepCarriesNoOp (errata 2, item 9): notes J makes in an op-less
// step write lines and no receipt, and in a step with an op they ride with its
// receipt; neither needs the op the Mem asks of a caller's notes.
func TestJOpLessStepCarriesNoOp(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	r.step(jReq(JOpOpen, jtCannotAsk, "c", "p1"))
	snap, err := r.m.Snapshot(testPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(snap.Receipts["0"]); n != 0 {
		t.Fatalf("an op-less step left %d receipts", n)
	}
	if got := len(r.log.Lines(testPrefix, "0")); got != 1 {
		t.Fatalf("the log has %d lines, want J's one", got)
	}
	reply := r.send(&Request{Epoch: "0", Meta: Meta{Verb: "drop"}, Body: Body{Op: &Op{ID: "op-1", Intent: "drop p1"},
		Notes: []NoteReq{jReq(JOpClose, jtCannotAsk, "c", "p1")}}})
	if reply.Reply.Lines != 1 || reply.Reply.Status != "ok" {
		t.Fatalf("a step with an op and a note: %+v", reply.Reply)
	}
	if snap, err = r.m.Snapshot(testPrefix); err != nil || snap.Receipts["0"]["op-1"].Status != "ok" {
		t.Fatalf("the op's receipt: %v, %v", snap.Receipts["0"], err)
	}
}

func jUnitState(now int64, seed ...Cmd) *State {
	ks := newKeyspace()
	ks.apply(seed)
	return &State{Prefix: testPrefix, Epoch: "0", NowMS: tset.Decimal(jStr(now)), Names: testNames, Keys: &Keys{ks: ks}}
}

func jObs(recs ...tset.MemberRecord) *Before {
	b := &Before{Records: map[string]map[string]tset.MemberRecord{}}
	for _, rec := range recs {
		table := strings.SplitN(rec.ID, "/", 2)[0]
		if b.Records[table] == nil {
			b.Records[table] = map[string]tset.MemberRecord{}
		}
		b.Records[table][strings.SplitN(rec.ID, "/", 2)[1]] = rec
	}
	return b
}

// jCard is a record in a table at a cell with fields; its ID is "table/id", which
// jObs splits.
func jCard(table, id, row, col string, fields map[string]string) tset.MemberRecord {
	rec := tset.MemberRecord{ID: table + "/" + id, Exists: true, Place: &tset.CellPlace{Row: row, Col: col}, Fields: map[string]tset.FieldValue{}}
	for k, v := range fields {
		rec.Fields[k] = tset.FieldValue{Present: true, Value: v}
	}
	return rec
}

// TestJEnds: the ways a timed state ends, and the ways it does not: a remove; a
// move to another column, or for a kind keyed by row to another row; a move that
// unsets the due field or sets it empty, shared or for that card; not a move
// within the column of a kind keyed by card (a level), not a change of other
// fields, and not a card that is not in the state or has no due field. The
// close names the subject R11 raises the judgment on: the card's id, from a
// stored id that carries an epoch suffix too, and for the stream's judgment
// sprint.StreamSubject, not the bare row.
func TestJEnds(t *testing.T) {
	t.Parallel()
	due := map[string]string{"due_untaken": "5000"}
	untaken := jCard(sprint.Fleet, "w1", "m1", "ready", due)
	stream := jCard(sprint.Merge, "s1ctl", "s1", "ctl", map[string]string{"due_mergeidle": "9000"})
	cases := []struct {
		name    string
		entry   tset.Entry
		rec     tset.MemberRecord
		subject string
		reason  string // "" when the state does not end
	}{
		{"a take", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ready", To: "m1:working", IDs: []string{"w1"}}, untaken, "w1", "taken"},
		{"a remove", tset.Entry{Kind: "remove", Table: sprint.Fleet, From: "m1:ready", IDs: []string{"w1"}}, untaken, "w1", "removed"},
		{"a level", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ready", To: "m2:ready", IDs: []string{"w1"}}, untaken, "", ""},
		{"a stay with other fields", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ready", IDs: []string{"w1"}, Set: map[string]string{"x": "y"}}, untaken, "", ""},
		{"the due field unset", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ready", IDs: []string{"w1"}, Unset: []string{"due_untaken"}}, untaken, "w1", "cleared"},
		{"the due field set empty", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ready", IDs: []string{"w1"}, Set: map[string]string{"due_untaken": ""}}, untaken, "w1", "cleared"},
		{"the due field set empty for the card", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ready", IDs: []string{"w1"}, Each: []map[string]string{{"due_untaken": ""}}}, untaken, "w1", "cleared"},
		{"the due field moved", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ready", IDs: []string{"w1"}, Set: map[string]string{"due_untaken": "8000"}}, untaken, "", ""},
		{"a card with no due field", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ready", To: "m1:working", IDs: []string{"w1"}},
			jCard(sprint.Fleet, "w1", "m1", "ready", nil), "", ""},
		{"a card in another column", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:working", To: "m1:ok", IDs: []string{"w1"}},
			jCard(sprint.Fleet, "w1", "m1", "working", due), "", ""},
		{"a stream keyed by row changing row", tset.Entry{Kind: "move", Table: sprint.Merge, From: "s1:ctl", To: "s2:ctl", IDs: []string{"s1ctl"}}, stream, sprint.StreamSubject("s1"), "stream no longer merging"},
		{"a stream's due field unset", tset.Entry{Kind: "move", Table: sprint.Merge, From: "s1:ctl", IDs: []string{"s1ctl"}, Unset: []string{"due_mergeidle"}}, stream, sprint.StreamSubject("s1"), "stream no longer merging"},
		{"a stream's control card removed", tset.Entry{Kind: "remove", Table: sprint.Merge, From: "s1:ctl", IDs: []string{"s1ctl"}}, stream, sprint.StreamSubject("s1"), "removed"},
		{"a take of a card stored at epoch one", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ready", To: "m1:working", IDs: []string{"w1~1"}}, untaken, sprint.CardID("w1~1"), "taken"},
		{"an untaken card replaced", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ready", To: "m1:withdrawn", IDs: []string{"w1"}}, untaken, "w1", "replaced"},
		{"an untaken card moved where no reason names", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ready", To: "m1:ok", IDs: []string{"w1"}}, untaken, "w1", "state ended"},
		{"an unfinished card finishing ok", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:working", To: "m1:ok", IDs: []string{"w1"}}, jCard(sprint.Fleet, "w1", "m1", "working", map[string]string{"due_unfinished": "7000"}), "w1", "finished"},
		{"an unfinished card finishing failed", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:working", To: "m1:failed", IDs: []string{"w1"}}, jCard(sprint.Fleet, "w1", "m1", "working", map[string]string{"due_unfinished": "7000"}), "w1", "finished"},
		{"an unfinished card replaced", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:working", To: "m1:withdrawn", IDs: []string{"w1"}}, jCard(sprint.Fleet, "w1", "m1", "working", map[string]string{"due_unfinished": "7000"}), "w1", "replaced"},
		{"an unfinished card back to ready", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:working", To: "m1:ready", IDs: []string{"w1"}}, jCard(sprint.Fleet, "w1", "m1", "working", map[string]string{"due_unfinished": "7000"}), "w1", "state ended"},
		{"a read card begun", tset.Entry{Kind: "move", Table: sprint.Readers, From: "r1:asked", To: "r1:reading", IDs: []string{"rc"}}, jCard(sprint.Readers, "rc", "r1", "asked", map[string]string{"due_unbegun": "4000"}), "rc", "begun"},
		{"a read card reported broken", tset.Entry{Kind: "move", Table: sprint.Readers, From: "r1:reading", To: "r1:broken", IDs: []string{"rc"}}, jCard(sprint.Readers, "rc", "r1", "reading", map[string]string{"due_unreported": "4000"}), "rc", "reported"},
		{"a read card retired", tset.Entry{Kind: "move", Table: sprint.Readers, From: "r1:asked", To: "r1:retired", IDs: []string{"rc"}}, jCard(sprint.Readers, "rc", "r1", "asked", map[string]string{"due_unbegun": "4000"}), "rc", "state ended"},
	}
	for _, tc := range cases {
		table, id := tc.entry.Table, tc.entry.IDs[0]
		rec := tc.rec
		rec.ID = table + "/" + id
		got := jEndings([]tset.Entry{tc.entry}, jObs(rec), map[[3]string]bool{})
		if tc.reason == "" {
			if len(got) != 0 {
				t.Errorf("%s: closes %+v, want nothing", tc.name, got)
			}
			continue
		}
		if len(got) != 1 || got[0].Op != JOpClose || got[0].Text != tc.reason || !reflect.DeepEqual(got[0].Subjects, []string{tc.subject}) {
			t.Errorf("%s: closes %+v, want one close of %s for %q", tc.name, got, tc.subject, tc.reason)
		}
	}

	// A request already naming the (type, cause, subject) is not closed twice.
	named := map[[3]string]bool{{jTypeWorkLate, "untaken", "w1"}: true}
	if got := jEndings([]tset.Entry{cases[0].entry}, jObs(jCard(sprint.Fleet, "w1", "m1", "ready", due)), named); len(got) != 0 {
		t.Errorf("a close was added beside the caller's own: %+v", got)
	}
}

// jMergeLateRaise is the raise of "a stream has had no merge step past its
// deadline" on a stream, made from the sprint package's own names and none of
// J's: the type it is raised under, the cause R11 gives it (the due kind keyed
// by row), and the subjects of a stream-level judgment (Note.Subjects). R11 is
// on sprint/foundation (IT10) and not on this stack's base; it raises
// NoteReq{Op: open, Type: NMergeLate, Cause: kind, Subjects: {StreamSubject(s)}},
// which is what the tick it replaces raises the same judgment as. When the stack
// sits on a base that has R11, this is the place to take the request from its
// plan instead.
func jMergeLateRaise(t *testing.T, stream string) NoteReq {
	t.Helper()
	kind := ""
	for _, k := range sprint.DueKinds {
		if k.OfRow {
			kind = k.Kind
		}
	}
	if kind == "" {
		t.Fatal("no due kind is keyed by row")
	}
	n := sprint.Note{Kind: sprint.Judgment, Type: sprint.NMergeLate, Stream: stream, StreamLevel: true}
	return NoteReq{Op: JOpOpen, Type: n.Type, Cause: kind, Subjects: n.Subjects()}
}

// TestJClosesTheStreamJudgmentOnTheSubjectItIsRaisedOn (1.3.4, 2.2): a raise of
// the stream's lateness judgment and J's close of it meet. The judgment is raised
// on the stream's subject ("stream:s1", sprint.StreamSubject), and the steps that
// end the stream's timed state (its control card leaves the row, loses its due
// field, or is removed) close it on the twin composed as it runs: its jopen
// field, its count, its note's place in jnotes and its overdue entry go, with the
// reason, and the line is about the stream's subject. A close on the bare row
// would leave the judgment open after the state that caused it.
func TestJClosesTheStreamJudgmentOnTheSubjectItIsRaisedOn(t *testing.T) {
	t.Parallel()
	ends := []struct {
		name   string
		entry  tset.Entry
		reason string
	}{
		{"the control card leaves for another row", tset.Entry{Kind: "move", Table: sprint.Merge, From: "s1:ctl", To: "s2:ctl", IDs: []string{"s1ctl"}, About: []string{"s1"}}, "stream no longer merging"},
		{"the due field is unset", tset.Entry{Kind: "move", Table: sprint.Merge, From: "s1:ctl", To: "s1:ctl", IDs: []string{"s1ctl"}, Unset: []string{"due_mergeidle"}, About: []string{"s1"}}, "stream no longer merging"},
		{"the control card is removed", tset.Entry{Kind: "remove", Table: sprint.Merge, From: "s1:ctl", IDs: []string{"s1ctl"}, About: []string{"s1"}}, "removed"},
	}
	for _, tc := range ends {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newJRig(t)
			r.send(&Request{Epoch: "0", Meta: Meta{Verb: "add", Actor: "coordinator"}, Body: Body{Entries: []tset.Entry{
				{Kind: "rows", Table: sprint.Merge, Add: []string{"s1", "s2"}},
				{Kind: "create", Table: sprint.Merge, To: "s1:ctl", IDs: []string{"s1ctl"}, Scores: []string{"1"},
					Set: map[string]string{"due_mergeidle": "9000"}, About: []string{"s1"}},
			}}})
			raise := jMergeLateRaise(t, "s1")
			if want := []string{sprint.StreamSubject("s1")}; !reflect.DeepEqual(raise.Subjects, want) {
				t.Fatalf("the raise is on %v, want the stream's subject %v", raise.Subjects, want)
			}
			subject := raise.Subjects[0]
			field := raise.Type + "|" + raise.Cause
			r.step(raise)
			note := r.hash(jk("jopen:" + subject))[field]
			if note == "" {
				t.Fatalf("the raise left no judgment on %s: %v", subject, r.keys())
			}
			r.wantHash(jk("jn"), map[string]string{note: "1"})

			reply := r.send(&Request{Epoch: "0", Meta: Meta{Rule: "j", Tick: true}, Body: Body{Entries: []tset.Entry{tc.entry}}})
			if reply.Reply.Lines != 2 {
				t.Fatalf("the ending wrote %d lines, want its own and the close J added", reply.Reply.Lines)
			}
			r.wantHash(jk("jopen:"+subject), nil)
			r.wantHash(jk("jn"), nil)
			r.wantZSet(jk("jnotes"), nil)
			r.wantZSet(jk("due"), nil)
			closeLine := r.line(len(r.log.Lines(testPrefix, "0")))
			if closeLine.Meta["op"] != "close" || closeLine.Meta["type"] != raise.Type || closeLine.Meta["cause"] != raise.Cause ||
				closeLine.Meta["note"] != note || closeLine.Meta["text"] != tc.reason || !reflect.DeepEqual(closeLine.About, []string{subject}) {
				t.Fatalf("the close line is %+v, want the close of %s on %s for %q", closeLine, note, subject, tc.reason)
			}
		})
	}
}

// TestJDecideReadsEntriesFromTheState: JDecide closes the lateness judgment of a
// timed state the step ends from the entries in State, from no note request of
// the step's own, and closes nothing when the State has none; JDecideEntries
// takes the entries apart from it; and JBefore asks the due fields of the cards a
// step moves.
func TestJDecideReadsEntriesFromTheState(t *testing.T) {
	t.Parallel()
	judged := Command("HSET", jk("jopen:w1"), kindHash, jField(jTypeWorkLate, "untaken"), "n5")
	counted := Command("HSET", jk("jn"), kindHash, "n5", "1")
	st := jUnitState(1_790_000_000_000, judged, counted)
	take := tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ready", To: "m1:working", IDs: []string{"w1"}}
	obs := jObs(jCard(sprint.Fleet, "w1", "m1", "ready", map[string]string{"due_untaken": "5000"}))

	if notes, jp, ref := JDecide(st, nil, obs); ref != nil || len(notes) != 0 || len(jp.Notes) != 0 {
		t.Fatalf("JDecide of no requests and no entries: %v %v %v", notes, jp, ref)
	}
	withEntries := *st
	withEntries.Entries = []tset.Entry{take}
	notes, jp, ref := JDecide(&withEntries, nil, obs)
	if ref != nil || len(notes) != 1 || len(jp.Notes) != 1 || jp.Notes[0].Existing != "n5" || jp.Notes[0].Req.Op != JOpClose {
		t.Fatalf("JDecide of a State with the take closed %v %+v, %v; want one close of n5", notes, jp, ref)
	}
	notes, jp, ref = JDecideEntries(st, nil, obs, []tset.Entry{take})
	if ref != nil || len(notes) != 1 || len(jp.Notes) != 1 || jp.Notes[0].Existing != "n5" {
		t.Fatalf("JDecideEntries closed %v %+v, %v; want one close of n5", notes, jp, ref)
	}
	// No judgment open on the card: a take ends a state nothing raised a judgment on.
	bare := jUnitState(1_790_000_000_000)
	bare.Entries = []tset.Entry{take}
	if notes, _, ref := JDecide(bare, nil, obs); ref != nil || len(notes) != 0 {
		t.Fatalf("a take with no judgment open made notes %v, %v", notes, ref)
	}
	asks := JBefore(st, &Request{Body: Body{Entries: []tset.Entry{take,
		{Kind: "move", Table: sprint.Work, From: "s1:ready", To: "s1:working", IDs: []string{"p1"}},
		{Kind: "remove", Table: sprint.Readers, From: "r1:reading", IDs: []string{"rc"}},
		{Kind: "create", Table: sprint.Fleet, To: "m1:ready", IDs: []string{"w9"}}}}})
	want := []BeforeAsk{{Table: sprint.Fleet, IDs: []string{"w1"}, Fields: []string{"due_untaken", "due_unfinished"}},
		{Table: sprint.Readers, IDs: []string{"rc"}, Fields: []string{"due_unbegun", "due_unreported"}}}
	if !reflect.DeepEqual(asks, want) {
		t.Fatalf("JBefore asked %+v, want %+v", asks, want)
	}
}

// TestJCmdsOrder (A1): what records owed work comes before the state it is owed
// for, and what forgets a trigger after: an open writes its list, overdue entry,
// index and count before the fields of jopen; a hold enters its hold entry
// before the field becomes a hold; a close and an unhold remove the field before
// they change a count, leave jnotes or leave askwait.
func TestJCmdsOrder(t *testing.T) {
	t.Parallel()
	index := func(cmds []Cmd, name, key string) int {
		for i, c := range cmds {
			if c.Argv[0] == name && c.Argv[1] == key {
				return i
			}
		}
		return -1
	}
	seqs := LogPlan{NoteSeqs: []tset.Decimal{"7"}}

	st := jUnitState(1_790_000_000_000)
	_, jp, ref := JDecide(st, []NoteReq{jReq(JOpOpen, jtCannotAsk, "c", "p1")}, nil)
	if ref != nil {
		t.Fatal(ref)
	}
	cmds := JCmds(st, jp, seqs)
	states := index(cmds, "HSET", jk("jopen:p1"))
	for _, rec := range [][2]string{{"RPUSH", jk("notes")}, {"ZADD", jk("due")}, {"ZADD", jk("jnotes")}, {"HSET", jk("jn")}, {"ZADD", jk("askwait")}} {
		if i := index(cmds, rec[0], rec[1]); i < 0 || i > states {
			t.Errorf("an open: %s %s is at %d, and jopen's HSET at %d; owed work is recorded first", rec[0], rec[1], i, states)
		}
	}

	held := jUnitState(1_790_000_000_000,
		Command("HSET", jk("jopen:p1"), kindHash, jField(jtCannotAsk, "c"), "n3"), Command("HSET", jk("jn"), kindHash, "n3", "1"))
	hold := jHold(jtCannotAsk, "c", 1_790_000_060_000, "p1")
	_, jp, _ = JDecide(held, []NoteReq{hold}, nil)
	cmds = JCmds(held, jp, seqs)
	if i, j := index(cmds, "ZADD", jk("due")), index(cmds, "HSET", jk("jopen:p1")); i < 0 || j < 0 || i > j {
		t.Errorf("a hold: the hold entry is at %d and the field at %d; the entry comes first", i, j)
	}
	if j, k := index(cmds, "HSET", jk("jopen:p1")), index(cmds, "HDEL", jk("jn")); j < 0 || k < 0 || k < j {
		t.Errorf("a hold: the field is at %d and the count's removal at %d; the field comes first", j, k)
	}

	_, jp, _ = JDecide(held, []NoteReq{jReq(JOpClose, jtCannotAsk, "c", "p1")}, nil)
	cmds = JCmds(held, jp, seqs)
	field := index(cmds, "HDEL", jk("jopen:p1"))
	for _, forget := range [][2]string{{"HDEL", jk("jn")}, {"ZREM", jk("jnotes")}, {"ZREM", jk("askwait")}, {"ZREM", jk("due")}} {
		if i := index(cmds, forget[0], forget[1]); field < 0 || i < field {
			t.Errorf("a close: %s %s is at %d and the field's removal at %d; the field goes first", forget[0], forget[1], i, field)
		}
	}
}

// TestJCmdsSkipsANoteWithNoSeq: a plan whose note has no seq in the log plan
// writes nothing for it; the twin's alignment never produces one.
func TestJCmdsSkipsANoteWithNoSeq(t *testing.T) {
	t.Parallel()
	st := jUnitState(1_790_000_000_000)
	_, jp, _ := JDecide(st, []NoteReq{jReq(JOpOpen, jtCannotAsk, "c", "p1")}, nil)
	if cmds := JCmds(st, jp, LogPlan{}); len(cmds) != 0 {
		t.Fatalf("a note with no seq wrote %d commands", len(cmds))
	}
	if cmds := JCmds(nil, jp, LogPlan{NoteSeqs: []tset.Decimal{"1"}}); cmds != nil {
		t.Fatal("JCmds of no state wrote commands")
	}
}

// TestJCmdsCountsOnlyTheNotesWithASeq: what a step takes off a count, out of a
// hold or out of askwait is worked out over the notes that have a line, as the
// Lua half does (the golden vectors missing_seq_*): a note whose seq is missing
// leaves its count, its held count, its hold entry and its askwait alone, and a
// note of the same step that has one writes its own.
func TestJCmdsCountsOnlyTheNotesWithASeq(t *testing.T) {
	t.Parallel()
	st := jUnitState(1_790_000_000_000,
		Command("HSET", jk("jopen:p1"), kindHash, jField(jtBlocked, "n0"), "n3"),
		Command("HSET", jk("jopen:p2"), kindHash, jField(jtBlocked, "n0"), "n3"),
		Command("HSET", jk("jn"), kindHash, "n3", "2"),
		Command("HSET", jk("jopen:p6"), kindHash, jField(jtCannotAsk, "c"), "hn4"),
		Command("HSET", jk("jh"), kindHash, "n4", "1"))
	_, jp, ref := JDecide(st, []NoteReq{jReq(JOpClose, jtBlocked, "n0", "p1"), jReq(JOpClose, jtCannotAsk, "c", "p6"), jReq(JOpOpen, jtCannotAsk, "d", "p5")}, nil)
	if ref != nil || len(jp.Notes) != 3 {
		t.Fatalf("decide: %d notes, %v", len(jp.Notes), ref)
	}
	cmds := JCmds(st, jp, LogPlan{NoteSeqs: []tset.Decimal{"", "", "9"}})
	for _, c := range cmds {
		switch {
		case c.Argv[1] == jk("jn") && c.Argv[2] == "n3", c.Argv[1] == jk("jh"), c.Argv[1] == jk("askwait") && c.Argv[0] == "ZREM",
			c.Argv[1] == jk("jopen:p1"), c.Argv[1] == jk("jopen:p6"):
			t.Errorf("a note with no seq left a command: %v", c.Argv)
		}
	}
	var opens bool
	for _, c := range cmds {
		if c.Argv[0] == "HSET" && c.Argv[1] == jk("jopen:p5") {
			opens = true
		}
	}
	if !opens {
		t.Fatalf("the note that has a seq wrote nothing: %v", cmds)
	}
}

// TestJPhasesAreTheTwinsDefaults: J is what a twin's J phase is by default, as
// IT12 has each item fill defaultPhases from its init.
func TestJPhasesAreTheTwinsDefaults(t *testing.T) {
	t.Parallel()
	if defaultPhases.JDecide == nil || defaultPhases.JCmds == nil || defaultPhases.JBefore == nil || !defaultPhases.JOnEntries {
		t.Fatal("the default phases have no J, no asks for J's before-state, or do not run J on a step's entries")
	}
}

// TestJNoteOfTwoThousandSubjectsStoreTime (8.1 IT15, Limit): a note naming 2,000
// subjects takes at most 10 ms of store time, SLOWLOG in the container. It needs
// the store.
func TestJNoteOfTwoThousandSubjectsStoreTime(t *testing.T) {
	t.Parallel()
	t.Skip("G0: needs the store (Layer 1 revision 4 pinned, Layer 2 accepted again) and its container's SLOWLOG")
}

// TestJLuaEqualsTwinOnAStore: the Lua half run by a store makes what the twin
// makes, step for step and image for image. Tonight the two are held to the same
// golden vectors (TestJVectorsGo here, TestSprintJLuaVectors under gopher-lua in
// internal/nsprint/fn); the store half needs the store.
func TestJLuaEqualsTwinOnAStore(t *testing.T) {
	t.Parallel()
	t.Skip("G0: needs the store (Layer 1 revision 4 pinned, Layer 2 accepted again) to load sprint_j.lua")
}

// TestJDefaultPhasesRunOnTheTwin: a twin made with NewTwin, given only X (which
// another item fills), runs a note request through J as its init registered it:
// the judgment opens, with its line and its keys.
func TestJDefaultPhasesRunOnTheTwin(t *testing.T) {
	t.Parallel()
	m := newTestMem(t)
	log := NewMemLog()
	tw := NewTwin(m, log, testNames)
	tw.parts = NewPartRegistry()
	tw.SetClock(func() time.Time { return testTime })
	pass := passX()
	tw.phases.XPre, tw.phases.XCmds = pass.XPre, pass.XCmds
	reply := mustStep(t, tw, &Request{Epoch: "0", Meta: Meta{Rule: "j", Tick: true},
		Body: Body{Notes: []NoteReq{jReq(JOpOpen, jtCannotAsk, "c", "p1")}}})
	if reply.Reply.Lines != 1 {
		t.Fatalf("the step wrote %d lines, want J's one", reply.Reply.Lines)
	}
	if got := tw.SprintKeys()[jk("jopen:p1")].Hash[jtCannotAsk+"|c"]; got != "n1" {
		t.Fatalf("jopen holds %q, want n1", got)
	}

	// And a step of entries with no note request ends the timed state it ends,
	// with no phase set by the test but the defaults: the lateness judgment open
	// on the card closes in the step that takes it.
	mustStep(t, tw, jFleetSeed())
	mustStep(t, tw, &Request{Epoch: "0", Meta: Meta{Rule: "j", Tick: true},
		Body: Body{Notes: []NoteReq{jReq(JOpOpen, jTypeWorkLate, "untaken", "w1")}}})
	if got := tw.SprintKeys()[jk("jopen:w1")].Hash[jTypeWorkLate+"|untaken"]; got == "" {
		t.Fatal("the lateness judgment did not open")
	}
	take := mustStep(t, tw, &Request{Epoch: "0", Meta: Meta{Verb: "take", Actor: "m1"}, Body: Body{Entries: []tset.Entry{
		{Kind: "move", Table: sprint.Fleet, From: "m1:ready", To: "m1:working", IDs: []string{"w1"}, Unset: []string{"due_untaken"}, About: []string{"p1"}}}}})
	if take.Reply.Lines != 2 {
		t.Fatalf("a take wrote %d lines, want its own and the close J added", take.Reply.Lines)
	}
	if _, there := tw.SprintKeys()[jk("jopen:w1")]; there {
		t.Fatal("the take left the lateness judgment open on the default phases")
	}
}

// TestJWritesAtTheWriteEpoch (L1 1.2, errata 3): a step that advances the epoch
// reads and writes J's keys at the next one, as the Lua half's ctx.write_epoch
// does, and names its notes with that epoch's suffix; one that does not advance
// writes at its own. A judgment open at the old epoch does not make an open at
// the new one a repeat.
func TestJWritesAtTheWriteEpoch(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	r.step(jReq(JOpOpen, jtBlocked, "n0", "w1")) // open at epoch 0: n1
	reply := r.send(&Request{Epoch: "0", Meta: Meta{Verb: "clear", Actor: "coordinator"},
		Body: Body{Op: &Op{ID: "clear-1", Intent: "clear"}, Entries: []tset.Entry{{Kind: "advance", AdvanceFrom: "0"}},
			Notes: []NoteReq{jReq(JOpOpen, jtBlocked, "n0", "w1")}}})
	if reply.Reply.Lines != 2 {
		t.Fatalf("the clear wrote %d lines, want the advance and the note", reply.Reply.Lines)
	}
	keys := r.keys()
	at1 := func(name string) string { return testPrefix + "sprint:" + name + "@1" }
	list := keys[at1("notes")].List
	if len(list) != 1 {
		t.Fatalf("notes@1 is %v, want the one note", list)
	}
	id := "n" + list[0] + "~1"
	if got := keys[at1("jopen:w1")].Hash[jtBlocked+"|n0"]; got != id {
		t.Fatalf("jopen@1 holds %q, want %q: the note's id has its epoch", got, id)
	}
	if got := keys[at1("jn")].Hash[id]; got != "1" {
		t.Fatalf("jn@1 is %v", keys[at1("jn")].Hash)
	}
	if got := keys[jk("jopen:w1")].Hash[jtBlocked+"|n0"]; got != "n1" {
		t.Fatalf("the judgment at epoch 0 changed: %q", got)
	}

	// A step that does not advance writes at its own epoch.
	if reply := mustStep(t, r.tw, &Request{Epoch: "1", Meta: Meta{Rule: "j", Tick: true}, Body: Body{Notes: []NoteReq{jReq(JOpOpen, jtBlocked, "n0", "w2")}}}); reply.Reply.Lines != 1 {
		t.Fatalf("a step at epoch 1 wrote %d lines", reply.Reply.Lines)
	}
	if _, there := r.keys()[at1("jopen:w2")].Hash[jtBlocked+"|n0"]; !there {
		t.Fatal("a step at epoch 1 did not write at epoch 1")
	}

	// An epoch with no successor is refused from J, as Layer 1 would refuse it.
	st := jUnitState(1_790_000_000_000)
	st.Epoch = "18446744073709551615"
	if _, _, ref := JDecideEntries(st, []NoteReq{jReq(JOpOpen, jtBlocked, "n0", "w1")}, nil, []tset.Entry{{Kind: "advance", AdvanceFrom: "18446744073709551615"}}); ref == nil || ref.Code != "OVERFLOW" || ref.Phase != PhaseJ {
		t.Fatalf("an advance past the last epoch: %+v", ref)
	}
}

// TestJOpensOfOneCauseMakeOneNote (1.3.4, errata 3): "a note names every subject
// of its step with the same type and cause": a step whose caller sent a request
// for each waiter makes one note naming them all, where 150 notes would pass a
// step's 100; requests of another cause, another text or another time are other
// notes; more than 2,000 subjects are cut into notes of 2,000; closes and holds
// of the subjects of one note are one line as well, and a second run writes
// nothing.
func TestJOpensOfOneCauseMakeOneNote(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	var reqs []NoteReq
	var all []string
	for i := 0; i < 150; i++ {
		w := "w" + strconv.Itoa(i)
		all = append(all, w)
		reqs = append(reqs, NoteReq{Op: JOpOpen, Type: jtBlocked, Cause: "n9", Subjects: []string{w}, Text: "n9 was dropped"})
	}
	reply := r.step(reqs...)
	if reply.Reply.Lines != 1 {
		t.Fatalf("150 requests of one type and cause wrote %d lines, want one note", reply.Reply.Lines)
	}
	if l := r.line(1); !reflect.DeepEqual(l.About, all) {
		t.Fatalf("the note names %d subjects, want the 150 in order", len(l.About))
	}
	r.wantHash(jk("jn"), map[string]string{"n1": "150"})
	img := r.img()
	if again := r.step(reqs...); again.Reply.Lines != 0 || r.img() != img {
		t.Fatalf("the second run wrote %d lines or changed the twin", again.Reply.Lines)
	}

	// Another cause, another text, or another time is another note.
	other := r.step(
		NoteReq{Op: JOpOpen, Type: jtCannotAsk, Cause: "a", Subjects: []string{"p1"}, Text: "t"},
		NoteReq{Op: JOpOpen, Type: jtCannotAsk, Cause: "a", Subjects: []string{"p2"}, Text: "u"},
		NoteReq{Op: JOpOpen, Type: jtCannotAsk, Cause: "b", Subjects: []string{"p3"}, Text: "t"},
		NoteReq{Op: JOpOpen, Type: jtCannotAsk, Cause: "a", Subjects: []string{"p4"}, Text: "t"})
	if other.Reply.Lines != 3 {
		t.Fatalf("three causes and texts wrote %d lines, want 3", other.Reply.Lines)
	}
	if l := r.line(2); !reflect.DeepEqual(l.About, []string{"p1", "p4"}) {
		t.Fatalf("the first of them names %v, want p1 and p4", l.About)
	}

	// Closes of the subjects of one note are one line, counted together.
	var closes []NoteReq
	for _, w := range all[:100] {
		closes = append(closes, NoteReq{Op: JOpClose, Type: jtBlocked, Cause: "n9", Subjects: []string{w}})
	}
	if cl := r.step(closes...); cl.Reply.Lines != 1 {
		t.Fatalf("100 closes of one note wrote %d lines, want 1", cl.Reply.Lines)
	}
	r.wantHash(jk("jn"), map[string]string{"n1": "50", "n2": "2", "n3": "1", "n4": "1"})

	// Holds of two of its subjects, at one time, are one hold line and one count.
	until := r.wall() + 60_000
	if hl := r.step(jHold(jtCannotAsk, "a", until, "p1"), jHold(jtCannotAsk, "a", until, "p4")); hl.Reply.Lines != 1 {
		t.Fatalf("two holds of one note wrote %d lines, want 1", hl.Reply.Lines)
	}
	r.wantHash(jk("jh"), map[string]string{"n2": "2"})
}

// TestJMoreThan2000SubjectsAreCutIntoNotes: requests that name 2,500 subjects of
// one type and cause make a note of 2,000 and a note of 500, within the step's
// 4,000 about ids; 4,001 subjects are LIMIT on about.
func TestJMoreThan2000SubjectsAreCutIntoNotes(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	var reqs []NoteReq
	for _, p := range jSeqNames("w", 2500) {
		reqs = append(reqs, NoteReq{Op: JOpOpen, Type: jtBlocked, Cause: "n0", Subjects: []string{p}})
	}
	if reply := r.step(reqs...); reply.Reply.Lines != 2 {
		t.Fatalf("2,500 subjects wrote %d lines, want 2 notes", reply.Reply.Lines)
	}
	r.wantHash(jk("jn"), map[string]string{"n1": "2000", "n2": "500"})
	var over []NoteReq
	for _, p := range jSeqNames("x", 4001) {
		over = append(over, NoteReq{Op: JOpOpen, Type: jtBlocked, Cause: "n1", Subjects: []string{p}})
	}
	if ref := r.refused(over...); ref.Code != CodeLimit || ref.Detail.Budget != "about" || ref.Phase != PhaseJ {
		t.Fatalf("4,001 subjects: %+v, want LIMIT on about from J", ref)
	}
}

// TestJNeverWritesAQuarantinedIdIntoAskwait (I1, errata 3): a primary in
// {p}quarantine@e is in no index but sent and wait:n, so "cannot ask" opens on it
// and writes it nowhere else: the judgment names it and jopen holds it, and
// askwait does not. A step run again writes nothing.
func TestJNeverWritesAQuarantinedIdIntoAskwait(t *testing.T) {
	t.Parallel()
	r := newJRig(t)
	r.put(Command("HSET", jk("quarantine"), kindHash, "p1", "DRIFT", "p3", "MISSING"))
	R := float64(r.wall())
	reply := r.step(jReq(JOpOpen, jtCannotAsk, "c", "p1", "p2", "p3"))
	if reply.Reply.Lines != 1 {
		t.Fatalf("the open wrote %d lines, want 1: the judgment itself opens", reply.Reply.Lines)
	}
	r.wantZSet(jk("askwait"), map[string]float64{"p2": R})
	for _, p := range []string{"p1", "p2", "p3"} {
		r.wantHash(jk("jopen:"+p), map[string]string{"cannot ask|c": "n1"})
	}
	if l := r.line(1); !reflect.DeepEqual(l.About, []string{"p1", "p2", "p3"}) {
		t.Fatalf("the note names %v", l.About)
	}
	img := r.img()
	if again := r.step(jReq(JOpOpen, jtCannotAsk, "c", "p1", "p2", "p3")); again.Reply.Lines != 0 || r.img() != img {
		t.Fatalf("the second run wrote %d lines or changed the twin", again.Reply.Lines)
	}

	// Closing them takes p2 out, and asks nothing of a quarantined id.
	r.step(jReq(JOpClose, jtCannotAsk, "c", "p1", "p2", "p3"))
	r.wantZSet(jk("askwait"), nil)

	// Another type of judgment does not look at the quarantine.
	r.step(jReq(JOpOpen, jtBlocked, "n0", "p1"))
	r.wantHash(jk("jopen:p1"), map[string]string{jtBlocked + "|n0": "n3"})
}

// TestJQuarantineIsReadInPieces (errata 3, cost): the primaries a "cannot ask"
// opens on are checked against the quarantine in reads of at most 1,000, each
// primary once however many notes name it: the reads are linear in the
// primaries, never one for each request or for each note.
func TestJQuarantineIsReadInPieces(t *testing.T) {
	t.Parallel()
	for _, n := range []int{1, 999, 1000, 1001, 2000} {
		st := jUnitState(1_790_000_000_000)
		subjects := jSeqNames("p", n)
		one, ref := JCost(st, []NoteReq{jReq(JOpOpen, jtCannotAsk, "c", subjects...)}, nil, nil)
		if ref != nil {
			t.Fatal(ref)
		}
		pieces := (n + jPiece - 1) / jPiece
		// n reads of the field, the pieces of the quarantine, and the clock.
		if want := n + pieces + 1; one.Probes != want {
			t.Errorf("%d subjects: %d probes, want %d (%d pieces)", n, one.Probes, want, pieces)
		}
		// The same primaries named again by a second cause are not read again.
		two, ref := JCost(st, []NoteReq{jReq(JOpOpen, jtCannotAsk, "c", subjects...), jReq(JOpOpen, jtCannotAsk, "d", subjects...)}, nil, nil)
		if ref != nil {
			t.Fatal(ref)
		}
		if want := 2*n + pieces + 1; two.Probes != want {
			t.Errorf("%d subjects in two causes: %d probes, want %d", n, two.Probes, want)
		}
	}
}

// TestJCostGrowsWithTheInput (errata 3, cost): what J reads and writes grows with
// the subjects a step names and not with how its requests are cut: n requests of
// one subject, one request of n, cost the same commands, bytes and reads, and
// each is linear in n (a read for each subject, one for the clock, one for
// each piece of the quarantine; a command for each subject and one for each
// piece of askwait).
func TestJCostGrowsWithTheInput(t *testing.T) {
	t.Parallel()
	var prev JCounts
	for _, n := range []int{100, 200, 400, 800, 1600} {
		subjects := jSeqNames("p", n)
		var each []NoteReq
		for _, s := range subjects {
			each = append(each, NoteReq{Op: JOpOpen, Type: jtCannotAsk, Cause: "c", Subjects: []string{s}, Text: "t"})
		}
		st := jUnitState(1_790_000_000_000)
		one, ref := JCost(st, []NoteReq{{Op: JOpOpen, Type: jtCannotAsk, Cause: "c", Subjects: subjects, Text: "t"}}, nil, nil)
		if ref != nil {
			t.Fatal(ref)
		}
		split, ref := JCost(st, each, nil, nil)
		if ref != nil {
			t.Fatal(ref)
		}
		if one != split {
			t.Fatalf("%d subjects: one request costs %+v, %d requests cost %+v", n, one, n, split)
		}
		pieces := (n + jPiece - 1) / jPiece
		// RPUSH, overdue, jnotes, jn, jtext, the askwait pieces, a jopen field for each.
		if want := 5 + pieces + n; one.Commands != want {
			t.Errorf("%d subjects: %d commands, want %d", n, one.Commands, want)
		}
		if want := n + pieces + 1; one.Probes != want { // the field of each, the quarantine's pieces, the clock
			t.Errorf("%d subjects: %d probes, want %d", n, one.Probes, want)
		}
		if prev.Commands != 0 && n > 100 {
			// Doubling the subjects doubles the commands, less the constant and the pieces.
			if d := one.Commands - prev.Commands; d < n/2 || d > n/2+2 {
				t.Errorf("%d subjects: %d commands over the last size's %d: not linear", n, one.Commands, prev.Commands)
			}
		}
		prev = one
	}
}

// jElements is how many collection elements a command carries (L1 1.4): a pair
// for ZADD and HSET, a member or field for ZREM, HDEL and RPUSH.
func jElements(c Cmd) int {
	switch c.Argv[0] {
	case "ZADD", "HSET":
		return (len(c.Argv) - 2) / 2
	}
	return len(c.Argv) - 2
}

// TestJPiecesAreAtMostAThousand: a note naming more than 1,000 primaries writes
// askwait in pieces of at most 1,000 pairs, and a close that takes more than
// 1,000 of them out removes them in pieces of at most 1,000 members, so that no
// command passes Layer 1's bound of 1,000 elements; and the pieces are full, so
// that 2,000 is two commands and 1,001 is two.
func TestJPiecesAreAtMostAThousand(t *testing.T) {
	t.Parallel()
	askwait := jk("askwait")
	for _, n := range []int{1000, 1001, 2000} {
		subjects := jSeqNames("p", n)
		// An open: askwait is written in pieces.
		st := jUnitState(1_790_000_000_000)
		_, jp, ref := JDecide(st, []NoteReq{jReq(JOpOpen, jtCannotAsk, "c", subjects...)}, nil)
		if ref != nil {
			t.Fatal(ref)
		}
		var adds []int
		for _, c := range JCmds(st, jp, LogPlan{NoteSeqs: []tset.Decimal{"1"}}) {
			if e := jElements(c); e > jPiece {
				t.Fatalf("an open of %d: %s %s has %d elements, over %d", n, c.Argv[0], c.Argv[1], e, jPiece)
			}
			if c.Argv[0] == "ZADD" && c.Argv[1] == askwait {
				adds = append(adds, jElements(c))
			}
		}
		if want := jPieces(n); !reflect.DeepEqual(adds, want) {
			t.Errorf("an open of %d wrote askwait in pieces %v, want %v", n, adds, want)
		}

		// A close of the same: askwait loses them in pieces.
		var seed []Cmd
		for _, s := range subjects {
			seed = append(seed, Command("HSET", jk("jopen:"+s), kindHash, jtCannotAsk+"|c", "n1"))
		}
		seed = append(seed, Command("HSET", jk("jn"), kindHash, "n1", strconv.Itoa(n)))
		closing := jUnitState(1_790_000_000_000, seed...)
		_, jp, ref = JDecide(closing, []NoteReq{jReq(JOpClose, jtCannotAsk, "c", subjects...)}, nil)
		if ref != nil {
			t.Fatal(ref)
		}
		var drops []int
		for _, c := range JCmds(closing, jp, LogPlan{NoteSeqs: []tset.Decimal{"2"}}) {
			if e := jElements(c); e > jPiece {
				t.Fatalf("a close of %d: %s %s has %d elements, over %d", n, c.Argv[0], c.Argv[1], e, jPiece)
			}
			if c.Argv[0] == "ZREM" && c.Argv[1] == askwait {
				drops = append(drops, jElements(c))
			}
		}
		if want := jPieces(n); !reflect.DeepEqual(drops, want) {
			t.Errorf("a close of %d dropped askwait in pieces %v, want %v", n, drops, want)
		}
	}
}

// jPieces is n elements in full pieces of jPiece and the rest.
func jPieces(n int) []int {
	var out []int
	for ; n > jPiece; n -= jPiece {
		out = append(out, jPiece)
	}
	return append(out, n)
}

// TestJCostIsExact: JCost counts what J plans under the longest seq the log can
// give, and what the pre stage reads, exactly. Three cases are written out from
// the design's keys, command by command: the commands and their bytes are the
// ones the twin's JCmds makes (a seq shorter than the ceiling, or a command
// left out, is a different count), and the reads are the ones the Lua half makes
// (a typed read of a field, a count, a digest, an HLEN and an HKEYS of a
// subject, a piece of the quarantine, the clock), which the vectors hold it to.
func TestJCostIsExact(t *testing.T) {
	t.Parallel()
	const seq = "9007199254740991"
	id := "n" + seq
	const now = 1_790_000_000_000
	R, due := strconv.FormatInt(now, 10), strconv.FormatInt(now+600_000, 10)
	cases := []struct {
		name   string
		seed   []Cmd
		reqs   []NoteReq
		cmds   [][]string
		probes int
		notes  int
	}{
		{"an open of two primaries", nil,
			[]NoteReq{{Op: JOpOpen, Type: jtCannotAsk, Cause: "c", Subjects: []string{"p1", "p2"}, Text: "x"}},
			[][]string{
				{"RPUSH", jk("notes"), seq},
				{"ZADD", jk("due"), due, "overdue:" + id},
				{"ZADD", jk("jnotes"), R, id},
				{"HSET", jk("jn"), id, "2"},
				{"HSET", jk("jtext"), id, jDigest("x", nil)},
				{"ZADD", jk("askwait"), R, "p1", R, "p2"},
				{"HSET", jk("jopen:p1"), jtCannotAsk + "|c", id},
				{"HSET", jk("jopen:p2"), jtCannotAsk + "|c", id},
			}, 4, 1}, // two fields, the quarantine, the clock
		{"a close of a held primary", []Cmd{Command("HSET", jk("jopen:p1"), kindHash, jtCannotAsk+"|c", "hn3"), Command("HSET", jk("jh"), kindHash, "n3", "1")},
			[]NoteReq{jReq(JOpClose, jtCannotAsk, "c", "p1")},
			[][]string{
				{"RPUSH", jk("notes"), seq},
				{"HDEL", jk("jopen:p1"), jtCannotAsk + "|c"},
				{"HDEL", jk("jh"), "n3"},
				{"ZREM", jk("due"), "hold:n3"},
				{"ZREM", jk("askwait"), "p1"},
			}, 4, 1}, // the field, the held count, an HLEN and an HKEYS
		{"an update of the text", []Cmd{Command("HSET", jk("jopen:p1"), kindHash, jtCannotAsk+"|c", "n3"), Command("HSET", jk("jn"), kindHash, "n3", "1")},
			[]NoteReq{{Op: JOpUpdate, Type: jtCannotAsk, Cause: "c", Subjects: []string{"p1"}, Text: "new"}},
			[][]string{
				{"RPUSH", jk("notes"), seq},
				{"HSET", jk("jtext"), "n3", jDigest("new", nil)},
			}, 2, 1}, // the field and the digest
	}
	for _, tc := range cases {
		st := jUnitState(now, tc.seed...)
		wantBytes := 0
		for _, c := range tc.cmds {
			for _, a := range c {
				wantBytes += len(a)
			}
		}
		got, ref := JCost(st, tc.reqs, nil, nil)
		if ref != nil {
			t.Fatalf("%s: %v", tc.name, ref)
		}
		want := JCounts{Commands: len(tc.cmds), ArgvBytes: wantBytes, Probes: tc.probes, Notes: tc.notes}
		if got != want {
			t.Errorf("%s: JCost is %+v, want %+v", tc.name, got, want)
		}
		_, jp, _ := JDecide(st, tc.reqs, nil)
		var made [][]string
		for _, c := range JCmds(st, jp, LogPlan{NoteSeqs: []tset.Decimal{seq}}) {
			made = append(made, c.Argv)
		}
		if !reflect.DeepEqual(made, tc.cmds) {
			t.Errorf("%s: JCmds made %v, want %v", tc.name, made, tc.cmds)
		}
	}
}

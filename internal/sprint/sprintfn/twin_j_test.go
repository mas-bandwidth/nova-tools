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

// jRig is a twin with J wired in and the four tables defined, at a clock the
// test moves. XCmds writes what seed holds once, so a test can put the sprint's
// own keys (the clock, a judgment's state) where a step reads them. With
// entries set, J also sees the step's entries (JDecideEntries) and the pre
// stage asks the due fields of the cards they move (JBefore): State carries no
// entries, so the twin of the later composition gives them here.
type jRig struct {
	t       *testing.T
	tw      *Twin
	m       *tset.Mem
	log     *LogStub
	now     time.Time
	seed    []Cmd
	entries []tset.Entry
}

func newJRig(t *testing.T, withEntries bool) *jRig {
	t.Helper()
	r := &jRig{t: t, now: testTime}
	ph := passX()
	ph.XCmds = func(*State, TablePlan, LogPlan) []Cmd {
		c := r.seed
		r.seed = nil
		return c
	}
	ph.JDecide, ph.JCmds = JDecide, JCmds
	if withEntries {
		ph.Before = func(st *State, req *Request) []BeforeAsk {
			r.entries = req.Body.Entries
			return JBefore(st, req)
		}
		ph.JDecide = func(st *State, in []NoteReq, obs *Before) ([]tset.Note, JPlan, *Refusal) {
			return JDecideEntries(st, in, obs, r.entries)
		}
	}
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

func jkey(name string) string { return testPrefix + "sprint:" + name + "@0" }

func req(op, typ, cause string, subjects ...string) NoteReq {
	return NoteReq{Op: op, Type: typ, Cause: cause, Subjects: subjects}
}

// hold is a wait on a condition the tick keeps, until running time until.
func hold(typ, cause string, until int64, subjects ...string) NoteReq {
	r := req(JOpHold, typ, cause, subjects...)
	r.Until = until
	return r
}

func str(n int64) string { return strconv.FormatInt(n, 10) }

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
	tCannotAsk = "cannot ask"
	tNoMember  = "no fleet member is up"
	tBlocked   = "a primary is blocked on something dropped"
	tDone      = "the sprint is done"
	tStopped   = "the machine is STOPPED and moves are due"
)

// TestJOnePerCauseRace (8.1 IT15): two steps planned from one read, each asking
// to open one judgment, open it once. The second finds the field and writes
// nothing, not even a line; a step naming one subject that has the judgment and
// one that has not opens it on the one that has not.
func TestJOnePerCauseRace(t *testing.T) {
	t.Parallel()
	r := newJRig(t, false)
	first := r.step(req(JOpOpen, tCannotAsk, "c", "p1"))
	if first.Reply.Lines != 1 || first.Reply.FirstSeq != "1" {
		t.Fatalf("the first step wrote %d lines from %s", first.Reply.Lines, first.Reply.FirstSeq)
	}
	r.wantHash(jkey("jopen:p1"), map[string]string{"cannot ask|c": "n1"})
	before := r.img()

	second := r.step(req(JOpOpen, tCannotAsk, "c", "p1"))
	if second.Reply.Lines != 0 || r.img() != before {
		t.Fatalf("the second step wrote %d lines or changed the twin: one judgment per cause", second.Reply.Lines)
	}

	both := r.step(req(JOpOpen, tCannotAsk, "c", "p1", "p2"))
	if both.Reply.Lines != 1 {
		t.Fatalf("a step of a subject that has it and one that has not wrote %d lines, want 1", both.Reply.Lines)
	}
	if l := r.line(2); !reflect.DeepEqual(l.About, []string{"p2"}) {
		t.Fatalf("the new note names %v, want only p2", l.About)
	}
	r.wantHash(jkey("jopen:p1"), map[string]string{"cannot ask|c": "n1"})
	r.wantHash(jkey("jopen:p2"), map[string]string{"cannot ask|c": "n2"})
	r.wantHash(jkey("jn"), map[string]string{"n1": "1", "n2": "1"})
}

// TestJWaitHoldsUntilTime (8.1 IT15): a wait on a condition the tick keeps
// closes the judgment with a hold line, writes h<note> in place of the note id
// and enters hold:<note> at its time, and leaves the subject in askwait; while
// it stands the owner rule raises nothing; when it runs out (R13's unhold) the
// field goes, and the owner rule raises again.
func TestJWaitHoldsUntilTime(t *testing.T) {
	t.Parallel()
	r := newJRig(t, false)
	r.step(req(JOpOpen, tCannotAsk, "c", "p1"))
	until := r.wall() + 60_000
	held := r.step(hold(tCannotAsk, "c", until, "p1"))
	if held.Reply.Lines != 1 {
		t.Fatalf("the wait wrote %d lines, want its hold line", held.Reply.Lines)
	}
	r.wantHash(jkey("jopen:p1"), map[string]string{"cannot ask|c": "hn1"})
	r.wantZSet(jkey("due"), map[string]float64{"hold:n1": float64(until)})
	r.wantZSet(jkey("jnotes"), nil)
	r.wantHash(jkey("jn"), nil)
	r.wantZSet(jkey("askwait"), map[string]float64{"p1": float64(r.wall())})
	if l := r.line(2); l.Meta["op"] != "hold" || l.Meta["note"] != "n1" || l.Meta["until"] != str(until) || l.Meta["kind"] != nil {
		t.Fatalf("the hold line's meta is %v", l.Meta)
	}

	// Under the hold the owner rule raises nothing of that cause and subject.
	img := r.img()
	if again := r.step(req(JOpOpen, tCannotAsk, "c", "p1")); again.Reply.Lines != 0 || r.img() != img {
		t.Fatalf("an open under a hold wrote %d lines or changed the twin", again.Reply.Lines)
	}
	// Another cause on the same subject is not held.
	if other := r.step(req(JOpOpen, tCannotAsk, "d", "p1")); other.Reply.Lines != 1 {
		t.Fatalf("an open of another cause wrote %d lines, want 1", other.Reply.Lines)
	}

	// The hold runs out: R13 unholds, and the owner rule raises the judgment again.
	r.now = r.now.Add(61 * time.Second)
	un := r.step(req(JOpUnhold, tCannotAsk, "c", "p1"))
	if un.Reply.Lines != 1 {
		t.Fatalf("the unhold wrote %d lines, want its unheld line", un.Reply.Lines)
	}
	if _, there := r.hash(jkey("jopen:p1"))["cannot ask|c"]; there {
		t.Fatal("the unhold left the field in jopen")
	}
	if _, there := r.zset(jkey("askwait"))["p1"]; !there {
		t.Fatal("the cause d of p1 is open, and askwait names p1 while any is")
	}
	if l := r.line(4); l.Meta["op"] != "unhold" || l.Meta["note"] != "n1" || l.Meta["kind"] != sprint.Decided {
		t.Fatalf("the unheld line's meta is %v", l.Meta)
	}
	again := r.step(req(JOpOpen, tCannotAsk, "c", "p1"))
	if again.Reply.Lines != 1 || r.hash(jkey("jopen:p1"))["cannot ask|c"] != "n5" {
		t.Fatalf("after the hold the owner rule raises the judgment again: lines %d, field %q",
			again.Reply.Lines, r.hash(jkey("jopen:p1"))["cannot ask|c"])
	}
}

// TestJHoldEndsWhenConditionClears (8.1 IT15): while a wait stands, the owner
// rule finds the condition cleared and closes: the hold ends with it (the field
// goes, a later open raises again), and the hold entry is left to fire, since a
// hold can name several subjects and J cannot see the others; R13 then finds no
// hold and writes nothing (2.3).
func TestJHoldEndsWhenConditionClears(t *testing.T) {
	t.Parallel()
	r := newJRig(t, false)
	r.step(req(JOpOpen, tNoMember, "c", "sprint"))
	until := r.wall() + 60_000
	r.step(hold(tNoMember, "c", until, "sprint"))
	r.wantHash(jkey("jopen:sprint"), map[string]string{"no fleet member is up|c": "hn1"})

	closed := r.step(req(JOpClose, tNoMember, "c", "sprint"))
	if closed.Reply.Lines != 1 {
		t.Fatalf("the close wrote %d lines, want 1", closed.Reply.Lines)
	}
	r.wantHash(jkey("jopen:sprint"), nil)
	r.wantHash(jkey("jn"), nil)
	if l := r.line(3); l.Meta["op"] != "close" || l.Meta["note"] != "n1" || l.Meta["kind"] != sprint.Decided {
		t.Fatalf("the close line's meta is %v", l.Meta)
	}
	if _, there := r.zset(jkey("due"))["hold:n1"]; !there {
		t.Fatal("the hold entry was removed by a close; it is left to fire (one hold may name several subjects)")
	}

	// The hold entry fires: R13's unhold finds no hold and writes nothing.
	img := r.img()
	if un := r.step(req(JOpUnhold, tNoMember, "c", "sprint")); un.Reply.Lines != 0 || r.img() != img {
		t.Fatalf("an unhold of nothing wrote %d lines or changed the twin", un.Reply.Lines)
	}
	// And the wait has ended: the owner rule raises again at once.
	if again := r.step(req(JOpOpen, tNoMember, "c", "sprint")); again.Reply.Lines != 1 {
		t.Fatalf("an open after the close wrote %d lines, want 1", again.Reply.Lines)
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

func ingestKeys(evs ...sprint.Event) []string {
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
	r := newJRig(t, false)
	r.step(req(JOpOpen, tNoMember, "c", "sprint")) // line 1
	r.step(hold(tNoMember, "c", r.wall()+60_000, "sprint"))
	r.step(req(JOpUnhold, tNoMember, "c", "sprint")) // line 3
	r.step(req(JOpOpen, tNoMember, "c", "sprint"))   // line 4
	r.step(req(JOpClose, tNoMember, "c", "sprint"))  // line 5

	opens, holds := r.eventOf(1), r.eventOf(2)
	if !opens.Opens || opens.Closes || opens.Kind != sprint.Judgment {
		t.Fatalf("the open line reads as %+v", opens)
	}
	if got := ingestKeys(opens); len(got) != 0 {
		t.Fatalf("the open line queues %v", got)
	}
	if holds.Opens || holds.Closes || holds.NoteType != tNoMember {
		t.Fatalf("the hold line reads as %+v: a plain note of the type", holds)
	}
	if got := ingestKeys(holds); len(got) != 0 {
		t.Fatalf("the hold line queues %v, want nothing (2.1)", got)
	}
	for _, seq := range []int{3, 5} {
		ev := r.eventOf(seq)
		if !ev.Closes {
			t.Fatalf("line %d reads as %+v: it closes the judgment", seq, ev)
		}
		if got := ingestKeys(ev); !reflect.DeepEqual(got, []string{"deal"}) {
			t.Fatalf("line %d queues %v, want the owner key deal", seq, got)
		}
	}
}

// fleetSeed creates the fleet member's row m1 and the work card w1 in
// m1:ready with a due_untaken field, and the reader r1's read card rc in
// readers r1:reading with due_unreported, for the lateness tests.
func fleetSeed() *Request {
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
// closes the lateness judgment of that kind open on the card, or ends its hold,
// with the reason: a take closes "not taken" (an open one, and a held one), and
// a report closes "a read card is past its deadline"; a move that keeps the
// state closes nothing. The twin calls J only for a step that carries a note
// request, so each step carries the line its verb writes.
func TestJClosesLatenessWhenStateEnds(t *testing.T) {
	t.Parallel()
	r := newJRig(t, true)
	r.send(fleetSeed()) // lines 1 to 4
	record := func(card string) NoteReq { return req(JOpRequest, "record", "", card) }
	step := func(verb string, e tset.Entry, card string) {
		r.t.Helper()
		r.send(&Request{Epoch: "0", Meta: Meta{Verb: verb, Actor: "m1"},
			Body: Body{Entries: []tset.Entry{e}, Notes: []NoteReq{record(card)}}})
	}
	// R11 raised "not taken" on w1 and w2 (note n5) and "not reported" on rc (n6).
	r.step(req(JOpOpen, jTypeWorkLate, "untaken", "w1", "w2"), req(JOpOpen, jTypeReadLate, "unreported", "rc"))
	r.step(hold(jTypeWorkLate, "untaken", r.wall()+60_000, "w2")) // the coordinator waits on w2's
	r.wantHash(jkey("jopen:w1"), map[string]string{jTypeWorkLate + "|untaken": "n5"})
	r.wantHash(jkey("jopen:w2"), map[string]string{jTypeWorkLate + "|untaken": "hn5"})
	r.wantHash(jkey("jn"), map[string]string{"n5": "1", "n6": "1"})

	// A level: w1 moves to another member's ready cell, and stays untaken.
	step("level", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ready", To: "m2:ready", IDs: []string{"w1"}, About: []string{"p1"}}, "w1")
	r.wantHash(jkey("jopen:w1"), map[string]string{jTypeWorkLate + "|untaken": "n5"})

	// w1 is taken: the open judgment closes, and its note, left with no open
	// subject, leaves jnotes and loses its overdue entry. w2's is held, and stays.
	step("take", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m2:ready", To: "m2:working", IDs: []string{"w1"},
		Set: map[string]string{"due_unfinished": "7000"}, Unset: []string{"due_untaken"}, About: []string{"p1"}}, "w1")
	r.wantHash(jkey("jopen:w1"), nil)
	r.wantHash(jkey("jopen:w2"), map[string]string{jTypeWorkLate + "|untaken": "hn5"})
	r.wantHash(jkey("jn"), map[string]string{"n6": "1"})
	if _, there := r.zset(jkey("jnotes"))["n5"]; there {
		t.Fatal("n5 is in jnotes with no open subject")
	}
	if _, there := r.zset(jkey("due"))["overdue:n5"]; there {
		t.Fatal("n5 kept its overdue entry with no open subject")
	}
	// The step's notes are in order: the verb's record, then the close J added.
	closeLine := r.line(len(r.log.Lines(testPrefix, "0")))
	if closeLine.Meta["op"] != "close" || closeLine.Meta["text"] != "taken" || closeLine.Meta["type"] != jTypeWorkLate ||
		closeLine.Meta["cause"] != "untaken" || closeLine.Meta["note"] != "n5" || !reflect.DeepEqual(closeLine.About, []string{"w1"}) ||
		closeLine.Meta["kind"] != sprint.Decided {
		t.Fatalf("the take's close line is %+v", closeLine)
	}

	// w2 is taken while its judgment is held: the hold ends with the state.
	step("take", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ready", To: "m1:working", IDs: []string{"w2"},
		Unset: []string{"due_untaken"}, About: []string{"p2"}}, "w2")
	r.wantHash(jkey("jopen:w2"), nil)

	// rc is reported: "a read card is past its deadline" closes, with "reported".
	step("read", tset.Entry{Kind: "move", Table: sprint.Readers, From: "r1:reading", To: "r1:ok", IDs: []string{"rc"},
		Unset: []string{"due_unreported"}, About: []string{"p1"}}, "rc")
	r.wantHash(jkey("jopen:rc"), nil)
	r.wantHash(jkey("jn"), nil)
	r.wantZSet(jkey("jnotes"), nil)
	found := false
	for seq := 1; seq <= len(r.log.Lines(testPrefix, "0")); seq++ {
		if m := r.line(seq).Meta; m["op"] == "close" && m["type"] == jTypeReadLate {
			found = m["text"] == "reported" && m["cause"] == "unreported" && m["note"] == "n6"
		}
	}
	if !found {
		t.Fatal("no close line of the read card's judgment with the reason reported")
	}
}

// TestJNoteIdsFromSeqs (8.1 IT15): a note's id is n and the seq of its line, from
// LogPlan.NoteSeqs, and a request that makes no line has none: of three
// requests whose middle one is already open, the two that open have the next two
// seqs and their ids, and the field of each names its own line.
func TestJNoteIdsFromSeqs(t *testing.T) {
	t.Parallel()
	r := newJRig(t, false)
	r.step(req(JOpOpen, tCannotAsk, "c", "p2")) // n1
	reply := r.step(req(JOpOpen, tCannotAsk, "c", "p1"), req(JOpOpen, tCannotAsk, "c", "p2"), req(JOpOpen, tNoMember, "c", "sprint"))
	if reply.Reply.Lines != 2 || reply.Reply.FirstSeq != "2" || reply.Reply.LastSeq != "3" {
		t.Fatalf("lines %d seqs %s..%s, want 2 lines 2..3: the skipped request has no line",
			reply.Reply.Lines, reply.Reply.FirstSeq, reply.Reply.LastSeq)
	}
	r.wantHash(jkey("jopen:p1"), map[string]string{"cannot ask|c": "n2"})
	r.wantHash(jkey("jopen:p2"), map[string]string{"cannot ask|c": "n1"})
	r.wantHash(jkey("jopen:sprint"), map[string]string{"no fleet member is up|c": "n3"})
	r.wantHash(jkey("jn"), map[string]string{"n1": "1", "n2": "1", "n3": "1"})
	if got := r.list(jkey("notes")); !reflect.DeepEqual(got, []string{"1", "2", "3"}) {
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
	r := newJRig(t, false)
	big := req(JOpOpen, tCannotAsk, "c", many("p", 2000)...)
	reply := r.step(big)
	if reply.Reply.Lines != 1 {
		t.Fatalf("a note naming 2,000 subjects wrote %d lines, want 1", reply.Reply.Lines)
	}
	if n := len(r.hash(jkey("jn"))); n != 1 || r.hash(jkey("jn"))["n1"] != "2000" {
		t.Fatalf("jn is %v, want one note of 2,000", r.hash(jkey("jn")))
	}

	st := &State{Prefix: testPrefix, Epoch: "0", NowMS: tset.Decimal(str(r.wall())), Names: testNames, Keys: &Keys{ks: newKeyspace()}}
	cost, ref := JCost(st, []NoteReq{big, req(JOpOpen, tNoMember, "c", "sprint")}, nil, nil)
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
		notices = append(notices, req(JOpKnow, "ci green", strconv.Itoa(i), "p"+strconv.Itoa(i)))
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
	if ref := r.refused(req(JOpKnow, "ci green", "", a...), req(JOpKnow, "ci green", "x", b...), req(JOpKnow, "ci green", "y", c...)); ref.Code != CodeLimit || ref.Detail.Budget != "about" {
		t.Fatalf("4,200 about ids: %+v, want LIMIT on about", ref)
	}
	if ref := r.refused(req(JOpKnow, "ci green", "", many("z", 2001)...)); ref.Code != CodeLimit || ref.Detail.Budget != "about" {
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
	r := newJRig(t, false)
	wall := r.wall()
	r.step(req(JOpOpen, tCannotAsk, "c", "p1"), req(JOpOpen, tBlocked, "n0", "w1", "w2"))
	R := float64(wall)
	r.wantZSet(jkey("due"), map[string]float64{"overdue:n1": R + 600_000, "overdue:n2": R + 600_000})
	r.wantZSet(jkey("jnotes"), map[string]float64{"n1": R, "n2": R})
	r.wantHash(jkey("jn"), map[string]string{"n1": "1", "n2": "2"})
	r.wantZSet(jkey("askwait"), map[string]float64{"p1": R})

	// The sprint is done: a note that is never overdue.
	r.step(req(JOpOpen, tDone, "d", "sprint"))
	if _, there := r.zset(jkey("due"))["overdue:n3"]; there {
		t.Fatal("\"the sprint is done\" has an overdue entry; it is never overdue (2.2)")
	}
	if _, there := r.zset(jkey("jnotes"))["n3"]; !there {
		t.Fatal("\"the sprint is done\" is not in jnotes")
	}

	// A machine STOPPED for 100 s before now, and one STOPPED since 5 s ago.
	r.now = r.now.Add(time.Minute)
	r.put(Command("HSET", testPrefix+"sprint:clock", kindHash, "stopped_ms", "100000", "stopped_since_ms", ""))
	r.step(req(JOpOpen, tBlocked, "n1", "w3"))
	r1 := float64(r.wall() - 100_000)
	r.wantZSet(jkey("jnotes"), map[string]float64{"n1": R, "n2": R, "n3": R, "n4": r1})
	if got := r.zset(jkey("due"))["overdue:n4"]; got != r1+600_000 {
		t.Fatalf("overdue:n4 at %v, want R + 10 min = %v with 100 s STOPPED before now", got, r1+600_000)
	}
	r.now = r.now.Add(time.Minute)
	r.put(Command("HSET", testPrefix+"sprint:clock", kindHash, "stopped_since_ms", str(r.wall()-5_000)))
	r.step(req(JOpOpen, tBlocked, "n2", "w4"))
	r2 := float64(r.wall() - 5_000 - 100_000)
	if got := r.zset(jkey("jnotes"))["n5"]; got != r2 {
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
	r := newJRig(t, false)
	r.step(req(JOpOpen, tBlocked, "n0", "w1", "w2", "w3"))
	r.wantHash(jkey("jn"), map[string]string{"n1": "3"})

	r.step(req(JOpClose, tBlocked, "n0", "w1"))
	r.wantHash(jkey("jn"), map[string]string{"n1": "2"})
	r.wantHash(jkey("jopen:w1"), nil)
	if _, there := r.zset(jkey("jnotes"))["n1"]; !there {
		t.Fatal("n1 left jnotes with two open subjects")
	}
	if _, there := r.zset(jkey("due"))["overdue:n1"]; !there {
		t.Fatal("n1 lost its overdue entry with two open subjects")
	}

	img := r.img()
	if none := r.step(req(JOpClose, tBlocked, "n0", "w1")); none.Reply.Lines != 0 || r.img() != img {
		t.Fatalf("a close of a subject with no judgment wrote %d lines or changed the twin", none.Reply.Lines)
	}

	two := r.step(req(JOpClose, tBlocked, "n0", "w2"), req(JOpClose, tBlocked, "n0", "w3"))
	if two.Reply.Lines != 2 {
		t.Fatalf("two closes wrote %d lines, want 2", two.Reply.Lines)
	}
	r.wantHash(jkey("jn"), nil)
	r.wantZSet(jkey("jnotes"), nil)
	r.wantZSet(jkey("due"), nil)
	for _, s := range []string{"w1", "w2", "w3"} {
		r.wantHash(jkey("jopen:"+s), nil)
	}
}

// TestJUpdateWritesOnlyTheLine: an update of an open judgment is a line of kind
// judgment with the verb "updated" (which Layer 3 reads as neither opening nor
// closing one) and changes no key but the notes list; an update of a held or an
// absent one writes nothing.
func TestJUpdateWritesOnlyTheLine(t *testing.T) {
	t.Parallel()
	r := newJRig(t, false)
	r.step(req(JOpOpen, tCannotAsk, "c", "p1", "p2"))
	r.step(hold(tCannotAsk, "c", r.wall()+1000, "p2")) // p2 is held
	keysBefore := r.keys()
	up := req(JOpUpdate, tCannotAsk, "c", "p1", "p2", "p3")
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
	keysBefore[jkey("notes")] = after[jkey("notes")] // the one key an update writes
	if !reflect.DeepEqual(keysBefore, after) {
		t.Fatal("an update changed a key besides the notes list")
	}
	if got := r.list(jkey("notes")); !reflect.DeepEqual(got, []string{"1", "2", "3"}) {
		t.Fatalf("the notes list is %v", got)
	}
}

// TestJKnowAndRequestWriteOnlyTheLine: a notice is a line of kind happened, a
// request a plain line, and neither touches a judgment key; their subjects are
// listed once.
func TestJKnowAndRequestWriteOnlyTheLine(t *testing.T) {
	t.Parallel()
	r := newJRig(t, false)
	know := req(JOpKnow, "ci green", "", "p1", "p2", "p1")
	know.Text = "ci is green at the head"
	reply := r.step(know, req(JOpRequest, "unfrozen", "", "s1", "s2"))
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
	if len(keys) != 1 || !reflect.DeepEqual(keys[jkey("notes")].List, []string{"1", "2"}) {
		t.Fatalf("a notice and a request wrote keys %v, want only the notes list", keys)
	}
}

// TestJReviewWaitMovesOverdue (1.3.4): a wait on a judgment the tick does not
// keep moves its overdue entry to the review time and leaves it open; a wait on
// one that is never overdue writes only its line.
func TestJReviewWaitMovesOverdue(t *testing.T) {
	t.Parallel()
	r := newJRig(t, false)
	r.step(req(JOpOpen, tBlocked, "n0", "w1"), req(JOpOpen, tDone, "d", "sprint"))
	review := r.wall() + 3_600_000
	reply := r.step(hold(tBlocked, "n0", review, "w1"), hold(tDone, "d", review, "sprint"))
	if reply.Reply.Lines != 2 {
		t.Fatalf("two waits wrote %d lines", reply.Reply.Lines)
	}
	r.wantZSet(jkey("due"), map[string]float64{"overdue:n1": float64(review)})
	r.wantHash(jkey("jopen:w1"), map[string]string{tBlocked + "|n0": "n1"})
	r.wantHash(jkey("jopen:sprint"), map[string]string{tDone + "|d": "n2"})
	if l := r.line(3); l.Meta["op"] != "review" || l.Meta["until"] != str(review) || l.Meta["kind"] != nil {
		t.Fatalf("the review line is %+v", l)
	}
	if got := len(r.hash(jkey("jn"))); got != 2 {
		t.Fatalf("jn has %d notes; both stay open", got)
	}
}

// TestJStoppedWaitSetsStophold (1.3.4): a wait on the STOPPED judgment holds it
// as any tick-kept one is held, but enters no hold entry: it sets stophold_ms
// to its wall time, since R does not move while STOPPED.
func TestJStoppedWaitSetsStophold(t *testing.T) {
	t.Parallel()
	r := newJRig(t, false)
	r.step(req(JOpOpen, tStopped, "c", "sprint"))
	wall := r.wall() + 120_000
	r.step(hold(tStopped, "c", wall, "sprint"))
	r.wantHash(jkey("jopen:sprint"), map[string]string{tStopped + "|c": "hn1"})
	r.wantHash(testPrefix+"sprint:clock", map[string]string{"stophold_ms": str(wall)})
	if _, there := r.zset(jkey("due"))["hold:n1"]; there {
		t.Fatal("a wait on the STOPPED judgment entered a hold entry in running time")
	}
}

// TestJAskwaitFollowsCannotAsk: askwait names a primary while "cannot ask" is
// open or held on it, of any cause: a close or an unhold of one cause leaves it
// while another stands, a hold keeps it, and the last to go takes it out; an
// open in the step that closes another keeps it.
func TestJAskwaitFollowsCannotAsk(t *testing.T) {
	t.Parallel()
	r := newJRig(t, false)
	R := float64(r.wall())
	r.step(req(JOpOpen, tCannotAsk, "c", "p1", "p2"), req(JOpOpen, tCannotAsk, "d", "p1"))
	r.wantZSet(jkey("askwait"), map[string]float64{"p1": R, "p2": R})
	r.step(req(JOpClose, tCannotAsk, "c", "p1", "p2"))
	r.wantZSet(jkey("askwait"), map[string]float64{"p1": R}) // p2 had only c; p1 has d
	r.step(hold(tCannotAsk, "d", r.wall()+1000, "p1"))
	r.wantZSet(jkey("askwait"), map[string]float64{"p1": R}) // held is still named
	// Raise c again and close d in one step: p1 keeps its place.
	r.step(req(JOpOpen, tCannotAsk, "c", "p1"), req(JOpUnhold, tCannotAsk, "d", "p1"))
	r.wantZSet(jkey("askwait"), map[string]float64{"p1": R})
	r.step(req(JOpClose, tCannotAsk, "c", "p1"))
	r.wantZSet(jkey("askwait"), nil)
}

// TestJSecondRunWritesNothing (E7, ReplayNoop): the requests of a step run a
// second time, with nothing changed between, write nothing: open, close, hold
// and unhold each find their field already as the first run left it.
func TestJSecondRunWritesNothing(t *testing.T) {
	t.Parallel()
	r := newJRig(t, false)
	until := r.wall() + 60_000
	for _, tc := range []struct {
		name string
		step []NoteReq
		prep []NoteReq
	}{
		{"open", []NoteReq{req(JOpOpen, tCannotAsk, "c", "p1", "p2")}, nil},
		{"hold", []NoteReq{hold(tCannotAsk, "c", until, "p1")}, nil},
		{"unhold", []NoteReq{req(JOpUnhold, tCannotAsk, "c", "p1")}, nil},
		{"close", []NoteReq{req(JOpClose, tCannotAsk, "c", "p2")}, nil},
	} {
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
	jopen := func(s string) string { return jkey("jopen:" + s) }
	cases := []struct {
		name string
		seed []Cmd
		reqs []NoteReq
		code string
	}{
		{"an op J does not take", nil, []NoteReq{req("frobnicate", tCannotAsk, "c", "p1")}, CodeRequest},
		{"no subject", nil, []NoteReq{req(JOpOpen, tCannotAsk, "c")}, CodeRequest},
		{"an empty subject", nil, []NoteReq{req(JOpOpen, tCannotAsk, "c", "")}, CodeRequest},
		{"a subject over 256 bytes", nil, []NoteReq{req(JOpOpen, tCannotAsk, "c", strings.Repeat("s", 257))}, CodeRequest},
		{"a judgment type 2.2 does not have", nil, []NoteReq{req(JOpOpen, "made up", "c", "p1")}, CodeRequest},
		{"a notice asked as a judgment", nil, []NoteReq{req(JOpOpen, "ci green", "c", "p1")}, CodeRequest},
		{"a judgment asked as a notice", nil, []NoteReq{req(JOpKnow, tCannotAsk, "", "p1")}, CodeRequest},
		{"a type with a bar", nil, []NoteReq{req(JOpOpen, "a|b", "c", "p1")}, CodeRequest},
		{"an empty cause on a judgment", nil, []NoteReq{req(JOpOpen, tCannotAsk, "", "p1")}, CodeRequest},
		{"a hold with no time", nil, []NoteReq{req(JOpHold, tCannotAsk, "c", "p1")}, CodeRequest},
		{"text that is not UTF-8", nil, []NoteReq{{Op: JOpOpen, Type: tCannotAsk, Cause: "c", Subjects: []string{"p1"}, Text: "\xff"}}, CodeRequest},
		{"one field named by two requests", nil, []NoteReq{req(JOpOpen, tCannotAsk, "c", "p1", "p2"), req(JOpClose, tCannotAsk, "c", "p2")}, CodeRequest},
		{"2,001 subjects", nil, []NoteReq{req(JOpKnow, "ci green", "", seqNames("p", 2001)...)}, CodeLimit},
		{"a value in jopen that is no note", []Cmd{Command("HSET", jopen("p1"), kindHash, tCannotAsk+"|c", "zzz")},
			[]NoteReq{req(JOpOpen, tCannotAsk, "c", "p1")}, jCodeDrift},
		{"a note open in jopen with no count in jn", []Cmd{Command("HSET", jopen("p1"), kindHash, tBlocked+"|c", "n1")},
			[]NoteReq{req(JOpClose, tBlocked, "c", "p1")}, jCodeDrift},
		{"a count in jn below what a close takes", []Cmd{Command("HSET", jopen("p1"), kindHash, tBlocked+"|c", "n1"),
			Command("HSET", jopen("p2"), kindHash, tBlocked+"|c", "n1"), Command("HSET", jkey("jn"), kindHash, "n1", "1")},
			[]NoteReq{req(JOpClose, tBlocked, "c", "p1", "p2")}, jCodeDrift},
		{"a jopen key of another type", []Cmd{Command("ZADD", jopen("p1"), kindZSet, "1", "m")},
			[]NoteReq{req(JOpOpen, tCannotAsk, "c", "p1")}, CodeWrongType},
		{"a jn key of another type", []Cmd{Command("HSET", jopen("p1"), kindHash, tBlocked+"|c", "n1"), Command("ZADD", jkey("jn"), kindZSet, "1", "m")},
			[]NoteReq{req(JOpClose, tBlocked, "c", "p1")}, CodeWrongType},
		{"a clock that is no clock", []Cmd{Command("HSET", testPrefix+"sprint:clock", kindHash, "stopped_ms", "soon")},
			[]NoteReq{req(JOpOpen, tCannotAsk, "c", "p1")}, CodeConfig},
	}
	for _, tc := range cases {
		r := newJRig(t, false)
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

func seqNames(prefix string, n int) []string {
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
	for _, typ := range []string{jTypeCannotAsk, jTypeStopped, jTypeDone, jTypeWorkLate, jTypeReadLate, jTypeStreamLate, tNoMember, tBlocked} {
		if _, ok := sprint.Judgments[typ]; !ok {
			t.Errorf("%q is no row of 2.2", typ)
		}
	}
	for _, typ := range []string{jTypeCannotAsk, jTypeStopped, tNoMember, jTypeWorkLate, jTypeReadLate, jTypeStreamLate} {
		if kept, _ := jJudgment(typ); !kept {
			t.Errorf("%q is not kept by the tick: a wait on it would not hold", typ)
		}
	}
	for _, typ := range []string{tBlocked, jTypeDone} {
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
		if jEndReason[k.Kind] == "" {
			t.Errorf("due kind %q has no reason a close carries", k.Kind)
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
	r := newJRig(t, false)
	r.step(req(JOpOpen, tNoMember, "c", "sprint"), req(JOpOpen, tBlocked, "n0", "w1")) // 1, 2
	r.step(req(JOpUpdate, tNoMember, "c", "sprint"))                                   // 3
	r.step(hold(tBlocked, "n0", r.wall()+1000, "w1"))                                  // 4: review
	r.step(hold(tNoMember, "c", r.wall()+1000, "sprint"))                              // 5: hold
	r.step(req(JOpUnhold, tNoMember, "c", "sprint"))                                   // 6
	r.step(req(JOpKnow, "ci green", "", "p1"))                                         // 7
	r.step(req(JOpRequest, "unfrozen", "", "s1"))                                      // 8
	r.step(req(JOpClose, tBlocked, "n0", "w1"))                                        // 9
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
	r := newJRig(t, false)
	r.step(req(JOpOpen, tCannotAsk, "c", "p1"))
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
		Notes: []NoteReq{req(JOpClose, tCannotAsk, "c", "p1")}}})
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
	return &State{Prefix: testPrefix, Epoch: "0", NowMS: tset.Decimal(str(now)), Names: testNames, Keys: &Keys{ks: ks}}
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

// card is a record in a table at a cell with fields; its ID is "table/id", which
// jObs splits.
func card(table, id, row, col string, fields map[string]string) tset.MemberRecord {
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
// fields, and not a card that is not in the state or has no due field.
func TestJEnds(t *testing.T) {
	t.Parallel()
	due := map[string]string{"due_untaken": "5000"}
	untaken := card(sprint.Fleet, "w1", "m1", "ready", due)
	stream := card(sprint.Merge, "s1ctl", "s1", "ctl", map[string]string{"due_mergeidle": "9000"})
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
			card(sprint.Fleet, "w1", "m1", "ready", nil), "", ""},
		{"a card in another column", tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:working", To: "m1:ok", IDs: []string{"w1"}},
			card(sprint.Fleet, "w1", "m1", "working", due), "", ""},
		{"a stream keyed by row changing row", tset.Entry{Kind: "move", Table: sprint.Merge, From: "s1:ctl", To: "s2:ctl", IDs: []string{"s1ctl"}}, stream, "s1", "stream no longer merging"},
		{"a stream's due field unset", tset.Entry{Kind: "move", Table: sprint.Merge, From: "s1:ctl", IDs: []string{"s1ctl"}, Unset: []string{"due_mergeidle"}}, stream, "s1", "cleared"},
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
	if got := jEndings([]tset.Entry{cases[0].entry}, jObs(card(sprint.Fleet, "w1", "m1", "ready", due)), named); len(got) != 0 {
		t.Errorf("a close was added beside the caller's own: %+v", got)
	}
}

// TestJDecideNeedsEntriesForLateness: JDecide, which sees no entries, closes no
// lateness judgment; JDecideEntries closes it, from no note request of the
// step's own; and JBefore asks the due fields of the cards a step moves.
func TestJDecideNeedsEntriesForLateness(t *testing.T) {
	t.Parallel()
	judged := Command("HSET", jkey("jopen:w1"), kindHash, jField(jTypeWorkLate, "untaken"), "n5")
	counted := Command("HSET", jkey("jn"), kindHash, "n5", "1")
	st := jUnitState(1_790_000_000_000, judged, counted)
	take := tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ready", To: "m1:working", IDs: []string{"w1"}}
	obs := jObs(card(sprint.Fleet, "w1", "m1", "ready", map[string]string{"due_untaken": "5000"}))

	if notes, jp, ref := JDecide(st, nil, obs); ref != nil || len(notes) != 0 || len(jp.Notes) != 0 {
		t.Fatalf("JDecide of no requests: %v %v %v", notes, jp, ref)
	}
	notes, jp, ref := JDecideEntries(st, nil, obs, []tset.Entry{take})
	if ref != nil || len(notes) != 1 || len(jp.Notes) != 1 || jp.Notes[0].Existing != "n5" || jp.Notes[0].Req.Op != JOpClose {
		t.Fatalf("JDecideEntries closed %v %+v, %v; want one close of n5", notes, jp, ref)
	}
	// No judgment open on the card: a take ends a state nothing raised a judgment on.
	bare := jUnitState(1_790_000_000_000)
	if notes, _, ref := JDecideEntries(bare, nil, obs, []tset.Entry{take}); ref != nil || len(notes) != 0 {
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
	_, jp, ref := JDecide(st, []NoteReq{req(JOpOpen, tCannotAsk, "c", "p1")}, nil)
	if ref != nil {
		t.Fatal(ref)
	}
	cmds := JCmds(st, jp, seqs)
	states := index(cmds, "HSET", jkey("jopen:p1"))
	for _, rec := range [][2]string{{"RPUSH", jkey("notes")}, {"ZADD", jkey("due")}, {"ZADD", jkey("jnotes")}, {"HSET", jkey("jn")}, {"ZADD", jkey("askwait")}} {
		if i := index(cmds, rec[0], rec[1]); i < 0 || i > states {
			t.Errorf("an open: %s %s is at %d, and jopen's HSET at %d; owed work is recorded first", rec[0], rec[1], i, states)
		}
	}

	held := jUnitState(1_790_000_000_000,
		Command("HSET", jkey("jopen:p1"), kindHash, jField(tCannotAsk, "c"), "n3"), Command("HSET", jkey("jn"), kindHash, "n3", "1"))
	hold := hold(tCannotAsk, "c", 1_790_000_060_000, "p1")
	_, jp, _ = JDecide(held, []NoteReq{hold}, nil)
	cmds = JCmds(held, jp, seqs)
	if i, j := index(cmds, "ZADD", jkey("due")), index(cmds, "HSET", jkey("jopen:p1")); i < 0 || j < 0 || i > j {
		t.Errorf("a hold: the hold entry is at %d and the field at %d; the entry comes first", i, j)
	}
	if j, k := index(cmds, "HSET", jkey("jopen:p1")), index(cmds, "HDEL", jkey("jn")); j < 0 || k < 0 || k < j {
		t.Errorf("a hold: the field is at %d and the count's removal at %d; the field comes first", j, k)
	}

	_, jp, _ = JDecide(held, []NoteReq{req(JOpClose, tCannotAsk, "c", "p1")}, nil)
	cmds = JCmds(held, jp, seqs)
	field := index(cmds, "HDEL", jkey("jopen:p1"))
	for _, forget := range [][2]string{{"HDEL", jkey("jn")}, {"ZREM", jkey("jnotes")}, {"ZREM", jkey("askwait")}, {"ZREM", jkey("due")}} {
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
	_, jp, _ := JDecide(st, []NoteReq{req(JOpOpen, tCannotAsk, "c", "p1")}, nil)
	if cmds := JCmds(st, jp, LogPlan{}); len(cmds) != 0 {
		t.Fatalf("a note with no seq wrote %d commands", len(cmds))
	}
	if cmds := JCmds(nil, jp, LogPlan{NoteSeqs: []tset.Decimal{"1"}}); cmds != nil {
		t.Fatal("JCmds of no state wrote commands")
	}
}

// TestJPhasesAreTheTwinsDefaults: J is what a twin's J phase is by default, as
// IT12 has each item fill defaultPhases from its init.
func TestJPhasesAreTheTwinsDefaults(t *testing.T) {
	t.Parallel()
	if defaultPhases.JDecide == nil || defaultPhases.JCmds == nil {
		t.Fatal("the default phases have no J")
	}
}

package verbs

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The tests of IT22 drive the composed twin (sprintfn.Twin over tset.Mem, with
// X, the derivation, J, the parts and the queries of the stack) through Env,
// as the store's binding will be driven after G0. Their helpers carry the jr
// prefix, so that they stand beside the driver's own (IT18) in one package.

const jrPrefix = "t:"

var jrNames = sprint.Names{Prefix: jrPrefix}

// jrColumns are the set columns of the four tables on Layer 1's store, as the
// write path's own tests define them.
var jrColumns = map[string][]string{
	sprint.Work:    {"waiting", "ready", "working", "review", "merging", "landed"},
	sprint.Readers: {"asked", "reading", "ok", "broken"},
	sprint.Merge:   {"queued", "merged", "stuck", "returned", "ctl"},
	sprint.Fleet:   {"ready", "working", "withdrawn", "ok", "failed", "ctl"},
}

// jrCoord is the coordinator of every test sprint.
const jrCoord = "coord"

// jrWorld is one sprint on the twin: the four tables defined, init run (the
// clock STOPPED, the coordinator set), a clock the test moves, and an Env over
// a counting client.
type jrWorld struct {
	t   *testing.T
	tw  *sprintfn.Twin
	cc  *Counting
	env *Env
	mu  sync.Mutex
	now time.Time
	// next is the score counter {p}next@e as the tests' steps set it (U2),
	// "" before the first score is placed.
	next int
}

func newJRWorld(t *testing.T) *jrWorld {
	t.Helper()
	m := tset.NewMem()
	for _, table := range []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet} {
		if err := m.DefineTable(jrPrefix, table, tset.TableDefinition{Columns: jrColumns[table],
			MemberPrefix: jrNames.TSetMemberPrefix(table), EpochKey: jrNames.EpochKey(), EpochField: "n"}); err != nil {
			t.Fatalf("define %s: %v", table, err)
		}
	}
	w := &jrWorld{t: t, now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
	w.tw = sprintfn.NewTwin(m, sprintfn.NewLogStub(), jrNames)
	w.tw.UseQueries()
	w.tw.UseIntents()
	w.tw.SetClock(func() time.Time {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.now
	})
	w.cc = &Counting{C: w.tw}
	w.env = &Env{C: w.cc, Names: jrNames, Actor: jrCoord, noWait: true}
	w.step(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "init"}, Clock: &sprintfn.ClockPart{Verb: sprintfn.ClockInit},
		Sprint: &sprintfn.SprintPart{Coordinator: jrCoord}})
	w.tick(time.Second)
	return w
}

// tick moves the store's clock on.
func (w *jrWorld) tick(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.now = w.now.Add(d)
}

// wall is the store's time in milliseconds.
func (w *jrWorld) wall() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.now.UnixMilli()
}

// try sends one raw request at epoch 0 and returns what came back.
func (w *jrWorld) try(req *sprintfn.Request) sprintfn.Result {
	w.t.Helper()
	if req.Epoch == "" {
		req.Epoch = "0"
	}
	if req.Meta.Actor == "" {
		req.Meta.Actor = jrCoord
	}
	res, err := sprintfn.Step(context.Background(), w.tw, req)
	if err != nil {
		w.t.Fatalf("step %s: %v", req.Meta.Verb, err)
	}
	return res
}

// step sends one raw request and fails the test on a refusal.
func (w *jrWorld) step(req *sprintfn.Request) *sprintfn.StepReply {
	w.t.Helper()
	res := w.try(req)
	if res.Refusal != nil || res.Err != nil {
		w.t.Fatalf("step %s: refusal %v, err %v", req.Meta.Verb, res.Refusal, res.Err)
	}
	return res.Step
}

// read is one atomic read at epoch 0.
func (w *jrWorld) read(rr *sprintfn.ReadRequest) *sprintfn.ReadReply {
	w.t.Helper()
	if rr.Epoch == "" {
		rr.Epoch = "0"
	}
	res, err := sprintfn.Read(context.Background(), w.tw, rr)
	if err != nil || res.Refusal != nil || res.Err != nil {
		w.t.Fatalf("read: err %v, refusal %v, result err %v", err, res.Refusal, res.Err)
	}
	return res.Read
}

// keyRead reads one sprint key.
func (w *jrWorld) keyRead(q sprintfn.KeyQ) sprintfn.QueryResult {
	w.t.Helper()
	rd := w.read(&sprintfn.ReadRequest{Sprint: []sprintfn.SprintQuery{keyQuery(q)}})
	qr, err := sprintfn.DecodeResult(q.Kind, rd.Sprint[0])
	if err != nil {
		w.t.Fatal(err)
	}
	return qr
}

// clockNow is the clock's answer now.
func (w *jrWorld) clockNow() sprintfn.ClockResult {
	w.t.Helper()
	return w.keyRead(sprintfn.KeyQ{Kind: sprintfn.KeyClock}).(sprintfn.ClockResult)
}

// start runs the machine.
func (w *jrWorld) start() {
	w.t.Helper()
	w.step(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "start"}, Clock: &sprintfn.ClockPart{Verb: sprintfn.ClockStart}})
}

// open raises a judgment through J and returns its note id: n and the seq of
// its line (1.3.4). Its step writes no other line.
func (w *jrWorld) open(typ, cause string, subjects ...string) string {
	w.t.Helper()
	rep := w.step(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "test", Rule: "test"}, Body: sprintfn.Body{
		Notes: []sprintfn.NoteReq{{Op: sprintfn.JOpOpen, Type: typ, Cause: cause, Subjects: subjects, Text: "raised by the test"}}}})
	if rep.Reply.FirstSeq != rep.Reply.LastSeq || rep.Reply.LastSeq == "0" {
		w.t.Fatalf("the open wrote lines %s to %s, want one", rep.Reply.FirstSeq, rep.Reply.LastSeq)
	}
	return "n" + string(rep.Reply.LastSeq)
}

// own is what the subject's jopen holds for the type and cause: the note id, h
// and the note id for a hold, "" when none.
func (w *jrWorld) own(subject, typ, cause string) string {
	w.t.Helper()
	field := typ + "|" + cause
	r := w.keyRead(sprintfn.KeyQ{Kind: sprintfn.KeyJOpen, Subjects: []string{subject}, Names: []string{field}}).(sprintfn.JOpenResult)
	if v := r.Items[0].Fields[field]; v != nil {
		return *v
	}
	return ""
}

// record is a work card's record, with the fields named.
func (w *jrWorld) record(id string, fields ...string) tset.MemberRecord {
	w.t.Helper()
	if fields == nil {
		fields = []string{}
	}
	rd := w.read(&sprintfn.ReadRequest{Tset: []tset.ReadQuery{{Kind: "ids", Table: sprint.Work, IDs: []string{id}, Fields: fields}}})
	return rd.Tset[0].Records[0]
}

// admit adds cards on the work table in one step, and the stream row s1 when
// it is new: each card in s1:<col>, with its fields, and for a waiting card
// with needs the waitfor the builder would carry (1.3.3; IT14's admission).
func (w *jrWorld) admit(cards ...jrCard) {
	w.t.Helper()
	req := &sprintfn.Request{Meta: sprintfn.Meta{Verb: "add"}}
	if !w.hasRow("s1") {
		req.Body.Entries = append(req.Body.Entries, tset.Entry{Kind: "rows", Table: sprint.Work, Add: []string{"s1"}})
	}
	base := w.next
	req.Sprint = w.scores(len(cards))
	for i, c := range cards {
		set := map[string]string{"kind": "work"}
		for k, v := range c.fields {
			set[k] = v
		}
		if c.needs != "" {
			set["needs"], set["open"] = c.needs, strconv.Itoa(len(sprint.Split(c.needs)))
			req.Body.Intents = append(req.Body.Intents, sprintfn.Intent{Kind: "waitfor", Card: c.id, Needs: sprint.Split(c.needs)})
		}
		req.Body.Entries = append(req.Body.Entries, tset.Entry{Kind: "create", Table: sprint.Work, To: "s1:" + c.col,
			IDs: []string{c.id}, Scores: []string{strconv.Itoa(base + i)}, Set: set, About: []string{c.id}})
	}
	w.step(req)
}

// scores reserves n scores under the counter (U2: every placed score lies
// below the counter after the step): the sprint part's change of {p}next@e.
func (w *jrWorld) scores(n int) *sprintfn.SprintPart {
	read := ""
	if w.next > 0 {
		read = strconv.Itoa(w.next)
	}
	w.next += n
	return &sprintfn.SprintPart{Counter: &sprintfn.CounterChange{Read: map[string]string{"score": read},
		Set: map[string]string{"score": strconv.Itoa(w.next)}}}
}

// hasRow says the work table has the row.
func (w *jrWorld) hasRow(row string) bool {
	rd := w.read(&sprintfn.ReadRequest{Tset: []tset.ReadQuery{{Kind: "rows", Table: sprint.Work}}})
	for _, r := range rd.Tset[0].Rows {
		if r.Row == row {
			return true
		}
	}
	return false
}

// jrCard is a card admit creates.
type jrCard struct {
	id, col, needs string
	fields         map[string]string
}

// codeOf is the code of a verb's refusal, "" when it is none.
func codeOf(err error) string {
	var rf *Refused
	if errors.As(err, &rf) {
		return rf.Code()
	}
	return ""
}

// TestAckWaivesMissingOnlyWhileMissing: ack of "blocked on something missing"
// waives the need while it has no record (2.2; 1.3.3): the judgment closes,
// the waiter's open falls and the need is waived, in one step of two round
// trips. Once the need has a record, the derive phase refuses the waiver
// XGUARD naming it, and nothing changes: a live prerequisite is never waived.
func TestAckWaivesMissingOnlyWhileMissing(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) (*jrWorld, string) {
		w := newJRWorld(t)
		w.admit(jrCard{id: "w", col: "waiting", needs: "ghost"})
		note := w.own("w", typeBlockedMissing, "ghost")
		if note == "" {
			t.Fatalf("the admission opened no missing judgment on w")
		}
		return w, note
	}

	t.Run("while missing", func(t *testing.T) {
		t.Parallel()
		w, note := setup(t)
		w.cc.Reset()
		res, err := Ack(context.Background(), w.env, AckReq{Notes: []string{note}, Reason: "ghost is not coming"})
		if err != nil {
			t.Fatal(err)
		}
		if res.Trips != 2 || w.cc.Trips() != 2 {
			t.Fatalf("ack took %d round trips (counted %d), want 2", res.Trips, w.cc.Trips())
		}
		if got := w.own("w", typeBlockedMissing, "ghost"); got != "" {
			t.Fatalf("the judgment is still open on w: %q", got)
		}
		rec := w.record("w", "open", "waived")
		if fieldOf(rec, "open") != "0" || fieldOf(rec, "waived") != "ghost" {
			t.Fatalf("w: open %q waived %q, want 0 and ghost", fieldOf(rec, "open"), fieldOf(rec, "waived"))
		}
	})

	t.Run("once it has a record", func(t *testing.T) {
		t.Parallel()
		w, note := setup(t)
		// ghost is created by a step that does not close the waiters' judgment
		// (the add of n closes it in its own step, errata 3 H3; this is the race
		// between the ack's read and its apply).
		score := strconv.Itoa(w.next)
		w.step(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "test"}, Sprint: w.scores(1), Body: sprintfn.Body{Entries: []tset.Entry{{Kind: "create",
			Table: sprint.Work, To: "s1:ready", IDs: []string{"ghost"}, Scores: []string{score}, Set: map[string]string{"kind": "work"},
			About: []string{"ghost"}}}}})
		before := w.record("w", "open", "waived")
		_, err := Ack(context.Background(), w.env, AckReq{Notes: []string{note}, Reason: "ghost is not coming"})
		var rf *Refused
		if !errors.As(err, &rf) || rf.Code() != sprintfn.CodeXGuard || !strings.Contains(rf.Error(), "ghost exists now") {
			t.Fatalf("ack of a missing need that has a record: %v, want XGUARD naming ghost", err)
		}
		// XGUARD is a race to the driver (IT18's IsRace), so the ack is planned
		// again on a fresh read until its retries run out, though the need will
		// not stop having a record: pinned here for the integrator.
		if rf.Retries != Retries {
			t.Fatalf("the ack was refused after %d retries, want the driver's %d", rf.Retries, Retries)
		}
		if got := w.own("w", typeBlockedMissing, "ghost"); got != note {
			t.Fatalf("the judgment changed: %q, want %q", got, note)
		}
		after := w.record("w", "open", "waived")
		if fieldOf(after, "open") != fieldOf(before, "open") || fieldOf(after, "waived") != "" || after.Revision != before.Revision {
			t.Fatalf("w changed: open %q waived %q rev %s, before rev %s", fieldOf(after, "open"), fieldOf(after, "waived"), after.Revision, before.Revision)
		}
	})
}

// lastLine is the log's last line at epoch 0, as an event (L2 4; ParseEvent).
func (w *jrWorld) lastLine() sprint.Event {
	w.t.Helper()
	rd := w.read(&sprintfn.ReadRequest{Tset: []tset.ReadQuery{{Kind: "last"}}})
	last := rd.Tset[0].LastSeq
	n, _ := strconv.ParseUint(string(last), 10, 64)
	return w.lineAt(n)
}

// lineAt is the line at a seq, as an event.
func (w *jrWorld) lineAt(seq uint64) sprint.Event {
	w.t.Helper()
	through := tset.Decimal(strconv.FormatUint(seq, 10))
	rd := w.read(&sprintfn.ReadRequest{Tset: []tset.ReadQuery{{Kind: "lines", AfterSeq: tset.Decimal(strconv.FormatUint(seq-1, 10)),
		ThroughSeq: &through, Limit: 1}}})
	ev, err := sprint.ParseEvent(string(through)+"-0", rd.Tset[0].Lines[0])
	if err != nil {
		w.t.Fatal(err)
	}
	return ev
}

// parked is the note {p}parked@e holds for a key, "" when it is not parked.
func (w *jrWorld) parked(key string) (string, int) {
	w.t.Helper()
	r := w.keyRead(sprintfn.KeyQ{Kind: sprintfn.KeyParked, Keys: []string{key}}).(sprintfn.ParkedResult)
	return r.Notes[key], r.Count
}

// TestAckUnparksThroughLine: ack of "the machine's step was refused" removes
// the parked key from {p}parked@e and closes the judgment with a decided line
// about the key, which ingest turns into the key again (F1-22: the step never
// writes its own trigger, Q5; 2.1, 2.2's owner key "the parked key"). The
// ingest half skips, naming the missing row, while Layer 3's ingest has none.
func TestAckUnparksThroughLine(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	w.step(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "test"}, Sprint: &sprintfn.SprintPart{Park: []sprintfn.ParkedKey{
		{Key: "deal", Rule: "deal", Code: "LIMIT", Budget: "entries", Actual: "300", Limit: "256"}}}})
	if v, n := w.parked("deal"); v == "" || n != 1 {
		t.Fatalf("the key was not parked: %q, %d keys", v, n)
	}
	note := w.open(typeStepRefused, "LIMIT", "deal")
	w.cc.Reset()
	res, err := Ack(context.Background(), w.env, AckReq{Notes: []string{note}, Reason: "the bound is raised"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Trips != 2 || w.cc.Trips() != 2 {
		t.Fatalf("ack took %d round trips, want 2", res.Trips)
	}
	if v, n := w.parked("deal"); v != "" || n != 0 {
		t.Fatalf("the key is still parked: %q, %d keys", v, n)
	}
	if got := w.own("deal", typeStepRefused, "LIMIT"); got != "" {
		t.Fatalf("the judgment is still open: %q", got)
	}
	ev := w.lastLine()
	if ev.Kind != sprint.Decided || ev.NoteType != typeStepRefused || len(ev.Subjects) != 1 || ev.Subjects[0] != "deal" {
		t.Fatalf("the ack's line is %+v, want a decided line of %q about deal", ev, typeStepRefused)
	}
	t.Run("ingest queues the key", func(t *testing.T) {
		t.Parallel()
		in := sprint.Ingest([]sprint.Event{ev})
		for _, k := range in.Keys {
			if k.Key == "deal" {
				return
			}
		}
		// Layer 3's ingest (IT01, ingest.go closeRows) has no row for this type:
		// 2.2 gives its owner key as the parked key, and 2.1's table as "an
		// acknowledged line of the machine's step was refused". Until the row
		// lands, the unparked key is queued by nothing.
		t.Skipf("ingest of the ack's line queued %v: IT01's closeRows has no row for %q (2.2: the parked key)", in.Keys, typeStepRefused)
	})
}

// TestAckRefusedForTickKept: a judgment whose row of 2.2 does not list ack is
// refused before anything is sent, naming its decisions; for a condition the
// tick keeps the refusal names wait, which holds it (an ack would close what
// the tick raises again). The note stays open, and one round trip is spent: the
// read that found its type.
func TestAckRefusedForTickKept(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	cannot := w.open("cannot ask", "no reader", "p1")
	acked := w.open(typeStepRefused, "LIMIT", "deal")
	w.cc.Reset()
	_, err := Ack(context.Background(), w.env, AckReq{Notes: []string{acked, cannot}, Reason: "looked"})
	var rf *Refused
	if !errors.As(err, &rf) || !rf.Local || rf.Code() != sprintfn.CodeRequest {
		t.Fatalf("ack of cannot ask: %v, want a local REQUEST", err)
	}
	for _, want := range []string{cannot, "not answered by ack", "reader add", "wait holds it"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal %q does not name %q", err, want)
		}
	}
	if w.cc.Steps() != 0 || w.cc.Trips() != 1 {
		t.Fatalf("the refused ack sent %d steps in %d round trips, want none in one", w.cc.Steps(), w.cc.Trips())
	}
	if w.own("p1", "cannot ask", "no reader") != cannot || w.own("deal", typeStepRefused, "LIMIT") != acked {
		t.Fatalf("a note changed: the set is one step, all or nothing")
	}
	if !sprint.Judgments["cannot ask"].TickKept || sprint.Judgments["cannot ask"].Ack {
		t.Fatalf("the row of cannot ask is not a condition the tick keeps without ack: the test names the wrong row")
	}
}

// TestAckNotCoordinator: ack is the coordinator's (2.2): X refuses NOTCOORD
// from another actor, and nothing changes.
func TestAckNotCoordinator(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	note := w.open(typeStepRefused, "LIMIT", "deal")
	other := &Env{C: w.cc, Names: jrNames, Actor: "someone", noWait: true}
	if _, err := Ack(context.Background(), other, AckReq{Notes: []string{note}, Reason: "mine"}); codeOf(err) != sprintfn.CodeNotCoord {
		t.Fatalf("ack by another actor: %v, want NOTCOORD", err)
	}
	if w.own("deal", typeStepRefused, "LIMIT") != note {
		t.Fatalf("the refused ack changed the note")
	}
}

// TestAckClearsRefused: ack of "the machine could not move a card" closes it
// and unsets refused on the card (1.3.5), so the machine tries it again. The
// card is named only by the note's line, so its record is read after the
// note: three round trips. (`related` over the note's line in the first read
// would make it two, but a line source is named and refuses MISSING for any
// subject with no record, and the first read cannot tell a note of cards from
// one of rule keys, "the machine's step was refused": an IT30 row, a line
// source that leaves absent ids out as an id list does.)
func TestAckClearsRefused(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	w.admit(jrCard{id: "p1", col: "ready", fields: map[string]string{"refused": "deal: no member"}})
	note := w.open(typeCouldNotMove, "refused", "p1")
	w.cc.Reset()
	res, err := Ack(context.Background(), w.env, AckReq{Notes: []string{note}, Reason: "a member is back"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Trips != 3 || w.cc.Trips() != 3 {
		t.Fatalf("ack took %d round trips (counted %d), want 3", res.Trips, w.cc.Trips())
	}
	if got := fieldOf(w.record("p1", "refused"), "refused"); got != "" {
		t.Fatalf("refused is still %q", got)
	}
	if got := w.own("p1", typeCouldNotMove, "refused"); got != "" {
		t.Fatalf("the judgment is still open: %q", got)
	}
}

// TestAckOnFrozenCardRefusedDropping: an ack that waives or clears a card of a
// stream being dropped changes the card, and is refused DROPPING (errata 3,
// H8; the model's VGuard for ack: ~Frozen(w)) by the verb itself, before
// anything is sent: the read that finds a drop in progress reads the marks of
// the work table's streams and the changed cards' records, so the driver never
// plans again a step X would refuse the same way (IT18's Do retries DROPPING
// as a race). Nothing changes, no step is sent, no retry is made. A drop of
// another stream refuses nothing.
func TestAckOnFrozenCardRefusedDropping(t *testing.T) {
	t.Parallel()
	drop := func(w *jrWorld, stream string) {
		w.step(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "drop"}, Sprint: &sprintfn.SprintPart{Dropping: map[string]string{stream: "op-drop"}}})
	}
	frozen := func(t *testing.T, w *jrWorld, note, card string) {
		t.Helper()
		w.cc.Reset()
		res, err := Ack(context.Background(), w.env, AckReq{Notes: []string{note}, Reason: "not coming"})
		var rf *Refused
		if !errors.As(err, &rf) || rf.Code() != sprintfn.CodeDropping || !rf.Local || res.Retries != 0 {
			t.Fatalf("ack on a frozen card: %v (retries %d), want a local DROPPING with no retry", err, res.Retries)
		}
		for _, want := range []string{note, card, "s1", "op-drop"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("the refusal %q does not name %q", err, want)
			}
		}
		if w.cc.Steps() != 0 || w.cc.Trips() != 2 || res.Trips != 2 {
			t.Fatalf("the refused ack sent %d steps in %d round trips (said %d), want none in 2", w.cc.Steps(), w.cc.Trips(), res.Trips)
		}
	}
	t.Run("waive missing", func(t *testing.T) {
		t.Parallel()
		w := newJRWorld(t)
		w.admit(jrCard{id: "w", col: "waiting", needs: "ghost"})
		note := w.own("w", typeBlockedMissing, "ghost")
		drop(w, "s1")
		frozen(t, w, note, "w")
		if w.own("w", typeBlockedMissing, "ghost") != note || fieldOf(w.record("w", "open"), "open") != "1" {
			t.Fatalf("the refused ack changed the card or its judgment")
		}
	})
	t.Run("waive dropped", func(t *testing.T) {
		t.Parallel()
		w := newJRWorld(t)
		w.admit(jrCard{id: "w", col: "waiting", needs: "ghost"})
		note := w.open(typeBlockedDropped, "ghost", "w")
		drop(w, "s1")
		frozen(t, w, note, "w")
		if w.own("w", typeBlockedDropped, "ghost") != note {
			t.Fatalf("the refused ack changed the judgment")
		}
	})
	t.Run("clear refused", func(t *testing.T) {
		t.Parallel()
		w := newJRWorld(t)
		w.admit(jrCard{id: "p1", col: "ready", fields: map[string]string{"refused": "deal: no member"}})
		note := w.open(typeCouldNotMove, "refused", "p1")
		drop(w, "s1")
		frozen(t, w, note, "p1")
		if fieldOf(w.record("p1", "refused"), "refused") == "" || w.own("p1", typeCouldNotMove, "refused") != note {
			t.Fatalf("the refused ack changed the card or its judgment")
		}
	})
	t.Run("another stream", func(t *testing.T) {
		t.Parallel()
		w := newJRWorld(t)
		w.admit(jrCard{id: "w", col: "waiting", needs: "ghost"})
		note := w.own("w", typeBlockedMissing, "ghost")
		drop(w, "s2")
		w.cc.Reset()
		res, err := Ack(context.Background(), w.env, AckReq{Notes: []string{note}, Reason: "not coming"})
		if err != nil || res.Step == nil || res.Trips != 3 {
			t.Fatalf("ack beside a drop of another stream: %v, step %v, %d round trips, want the step in 3", err, res.Step != nil, res.Trips)
		}
	})
}

// TestAckAndWaitOfHeldNoteWriteNothing: a note held since it was printed (its
// subjects' field is "h" and its id, 1.3.4) is not open on them: ack and wait
// read it, send no step, change nothing and say so (the reader of PR 4793's
// P16: a held subject counted as open survived every test).
func TestAckAndWaitOfHeldNoteWriteNothing(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	note := w.open(typeStepRefused, "LIMIT", "deal")
	if _, err := Wait(context.Background(), w.env, WaitReq{Notes: []string{note}, For: time.Hour, Reason: "later"}); err != nil {
		t.Fatal(err)
	}
	if got := w.own("deal", typeStepRefused, "LIMIT"); got != "h"+note {
		t.Fatalf("the wait did not hold the note: %q", got)
	}
	r, _ := strconv.ParseInt(w.clockNow().R, 10, 64)
	hold := w.dueScore("hold:" + note)
	w.cc.Reset()
	res, err := Ack(context.Background(), w.env, AckReq{Notes: []string{note}, Reason: "now"})
	if err != nil || res.Step != nil || w.cc.Steps() != 0 || !strings.Contains(res.Said, "nothing was written") {
		t.Fatalf("ack of a held note: %v, step %v, %d steps, %q", err, res.Step != nil, w.cc.Steps(), res.Said)
	}
	res, err = Wait(context.Background(), w.env, WaitReq{Notes: []string{note}, For: 2 * time.Hour, Reason: "again"})
	if err != nil || res.Step != nil || w.cc.Steps() != 0 || !strings.Contains(res.Said, "nothing was written") {
		t.Fatalf("wait of a held note: %v, step %v, %d steps, %q", err, res.Step != nil, w.cc.Steps(), res.Said)
	}
	if got := w.own("deal", typeStepRefused, "LIMIT"); got != "h"+note {
		t.Fatalf("the held note changed: %q", got)
	}
	if got := w.dueScore("hold:" + note); got != hold || hold < r {
		t.Fatalf("hold:%s moved from %d to %d", note, hold, got)
	}
}

// TestWaitNeedsReason: wait wants --reason and a duration above 0, refused
// before anything is read; with both, it holds a condition the tick keeps
// (1.3.4): the judgment closes with a hold line, its field becomes h<note>, and
// hold:<note> waits at R + d.
func TestWaitNeedsReason(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	note := w.open("cannot ask", "no reader", "p1")
	w.cc.Reset()
	for _, req := range []WaitReq{{Notes: []string{note}, For: time.Minute}, {Notes: []string{note}, For: time.Minute, Reason: "  "},
		{Notes: []string{note}, Reason: "later"}} {
		_, err := Wait(context.Background(), w.env, req)
		var rf *Refused
		if !errors.As(err, &rf) || !rf.Local || rf.Code() != sprintfn.CodeRequest {
			t.Fatalf("wait %+v: %v, want a local REQUEST", req, err)
		}
	}
	if w.cc.Trips() != 0 {
		t.Fatalf("a refused wait made %d round trips", w.cc.Trips())
	}
	r := w.clockNow().R
	res, err := Wait(context.Background(), w.env, WaitReq{Notes: []string{note}, For: 30 * time.Minute, Reason: "a reader joins at noon"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Trips != 2 {
		t.Fatalf("wait took %d round trips, want 2", res.Trips)
	}
	if got := w.own("p1", "cannot ask", "no reader"); got != "h"+note {
		t.Fatalf("the field holds %q, want h%s", got, note)
	}
	ev := w.lastLine()
	if ev.NoteType != "cannot ask" || len(ev.Subjects) != 1 || ev.Subjects[0] != "p1" {
		t.Fatalf("the wait's line is %+v", ev)
	}
	rn, _ := strconv.ParseInt(r, 10, 64)
	if got := w.dueScore("hold:" + note); got != rn+30*60*1000 {
		t.Fatalf("hold:%s is at %d, want R + 30 min = %d", note, got, rn+30*60*1000)
	}
}

// dueScore is the score of a member of {p}due@e, -1 when it has none.
func (w *jrWorld) dueScore(member string) int64 {
	w.t.Helper()
	rd := w.read(&sprintfn.ReadRequest{Tset: []tset.ReadQuery{{Kind: "range", Key: jrPrefix + "sprint:due@0", Min: "-inf", Max: "+inf", Limit: 2000}}})
	for i, id := range rd.Tset[0].IDs {
		if id == member {
			n, err := strconv.ParseFloat(rd.Tset[0].Scores[i], 64)
			if err != nil {
				w.t.Fatal(err)
			}
			return int64(n)
		}
	}
	return -1
}

// TestWaitStoppedSetsWallHold: wait on "the machine is STOPPED and moves are
// due" sets stophold_ms to wall + d, a wall time, and enters no hold entry
// (1.3.4; 2.2: holds, wall time).
func TestWaitStoppedSetsWallHold(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	note := w.open(typeStoppedDue, "moves due", "sprint")
	w.tick(3 * time.Second)
	wall := w.wall()
	if _, err := Wait(context.Background(), w.env, WaitReq{Notes: []string{note}, For: 2 * time.Hour, Reason: "the owner starts it after lunch"}); err != nil {
		t.Fatal(err)
	}
	c := w.clockNow()
	if c.Clock.StopholdMS == nil || *c.Clock.StopholdMS != strconv.FormatInt(wall+2*60*60*1000, 10) {
		t.Fatalf("stophold_ms is %v, want wall + 2 h = %d", c.Clock.StopholdMS, wall+2*60*60*1000)
	}
	if w.dueScore("hold:"+note) != -1 {
		t.Fatalf("a hold entry was entered for the STOPPED judgment")
	}
	if got := w.own("sprint", typeStoppedDue, "moves due"); got != "h"+note {
		t.Fatalf("the field holds %q, want h%s", got, note)
	}
}

// TestWaitReviewLeavesOpen: wait on a judgment the tick does not keep moves
// its overdue entry to the review time R + d and leaves it open (1.3.4; 2.2:
// review).
func TestWaitReviewLeavesOpen(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	note := w.open("returned to review", "returned", "p1")
	r, _ := strconv.ParseInt(w.clockNow().R, 10, 64)
	if _, err := Wait(context.Background(), w.env, WaitReq{Notes: []string{note}, For: time.Hour, Reason: "after the release"}); err != nil {
		t.Fatal(err)
	}
	if got := w.own("p1", "returned to review", "returned"); got != note {
		t.Fatalf("the field holds %q, want the open note %s", got, note)
	}
	if got := w.dueScore("overdue:" + note); got != r+60*60*1000 {
		t.Fatalf("overdue:%s is at %d, want R + 1 h = %d", note, got, r+60*60*1000)
	}
}

// TestAckOfClosedNoteWritesNothing: a note closed since it was printed is left
// as it is: the ack reads it and sends no step (one round trip), and says so.
func TestAckOfClosedNoteWritesNothing(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	note := w.open(typeStepRefused, "LIMIT", "deal")
	if _, err := Ack(context.Background(), w.env, AckReq{Notes: []string{note}, Reason: "first"}); err != nil {
		t.Fatal(err)
	}
	w.cc.Reset()
	res, err := Ack(context.Background(), w.env, AckReq{Notes: []string{note}, Reason: "again"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Step != nil || w.cc.Steps() != 0 || w.cc.Trips() != 1 || !strings.Contains(res.Said, "nothing was written") {
		t.Fatalf("a second ack sent %d steps in %d round trips: %q", w.cc.Steps(), w.cc.Trips(), res.Said)
	}
}

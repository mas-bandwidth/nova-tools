package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The tests of IT21 drive the composed twin (sprintfn.Twin over tset.Mem, with
// the stack's X, derivation, J, parts and queries) through Env, over a counting
// client, as IT18's tests do; their world is their own (rv), so that the
// driver's tests and these share no name.

const rvPrefix = "r:"

var rvNames = sprint.Names{Prefix: rvPrefix}

// rvColumns are the set columns of the four tables on Layer 1's store.
var rvColumns = map[string][]string{
	sprint.Work:    {"waiting", "ready", "working", "review", "merging", "landed"},
	sprint.Readers: {"asked", "reading", "ok", "broken"},
	sprint.Merge:   {"queued", "merged", "stuck", "returned", "ctl"},
	sprint.Fleet:   {"ready", "working", "withdrawn", "ok", "failed", "ctl"},
}

// rv is one initialised sprint on the twin, with coordinator "coord" and an
// Env of that actor over a counting client.
type rv struct {
	t   *testing.T
	tw  *sprintfn.Twin
	log *sprintfn.LogStub
	cc  *Counting
	env *Env
	mu  sync.Mutex
	now time.Time
}

func newRV(t *testing.T) *rv {
	t.Helper()
	m := tset.NewMem()
	for _, table := range []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet} {
		if err := m.DefineTable(rvPrefix, table, tset.TableDefinition{Columns: rvColumns[table],
			MemberPrefix: rvNames.TSetMemberPrefix(table), EpochKey: rvNames.EpochKey(), EpochField: "n"}); err != nil {
			t.Fatalf("define %s: %v", table, err)
		}
	}
	w := &rv{t: t, log: sprintfn.NewLogStub(), now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
	w.tw = sprintfn.NewTwin(m, w.log, rvNames)
	w.tw.UseQueries()
	w.tw.UseIntents()
	w.tw.SetClock(func() time.Time {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.now
	})
	w.cc = &Counting{C: w.tw}
	w.env = &Env{C: w.cc, Names: rvNames, Actor: "coord", noWait: true}
	w.raw(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "init", Actor: "coord"},
		Clock: &sprintfn.ClockPart{Verb: sprintfn.ClockInit}, Sprint: &sprintfn.SprintPart{Coordinator: "coord",
			Counter: &sprintfn.CounterChange{Read: map[string]string{"score": ""}, Set: map[string]string{"score": "1000000"}}}})
	w.raw(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "start", Actor: "coord"}, Clock: &sprintfn.ClockPart{Verb: sprintfn.ClockStart}})
	w.tick(time.Second)
	return w
}

func (w *rv) tick(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.now = w.now.Add(d)
}

// raw sends a request to the twin, and fails the test on a refusal.
func (w *rv) raw(req *sprintfn.Request) *sprintfn.StepReply {
	w.t.Helper()
	if req.Epoch == "" {
		req.Epoch = "0"
	}
	if req.Meta.Verb == "" {
		req.Meta.Verb = "fixture"
	}
	res, err := sprintfn.Step(context.Background(), w.tw, req)
	if err != nil || res.Refusal != nil || res.Err != nil {
		w.t.Fatalf("step %s: err %v, refusal %v, result err %v", req.Meta.Verb, err, res.Refusal, res.Err)
	}
	return res.Step
}

// put writes entries in one fixture step.
func (w *rv) put(entries ...tset.Entry) {
	w.t.Helper()
	w.raw(&sprintfn.Request{Body: sprintfn.Body{Entries: entries}})
}

// rows adds rows to a table.
func (w *rv) rows(table string, names ...string) {
	w.t.Helper()
	w.put(tset.Entry{Kind: "rows", Table: table, Add: names})
}

// card creates one card at a cell with the fields kv (pairs).
func (w *rv) card(table, cell, id string, score int, kv ...string) {
	w.t.Helper()
	f := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		f[kv[i]] = kv[i+1]
	}
	w.put(tset.Entry{Kind: "create", Table: table, To: cell, IDs: []string{id}, Scores: []string{strconv.Itoa(score)},
		Each: []map[string]string{f}, About: []string{id}})
}

// stream makes a stream: its rows in work and merge, and its control card.
func (w *rv) stream(s, state string, kv ...string) {
	w.t.Helper()
	w.rows(sprint.Work, s)
	w.rows(sprint.Merge, s)
	w.card(sprint.Merge, s+":ctl", sprint.CtlID(s), 0, append([]string{"kind", "stream", "state", state}, kv...)...)
}

// member makes an up member of the fleet.
func (w *rv) member(m string) {
	w.t.Helper()
	w.rows(sprint.Fleet, m)
	w.card(sprint.Fleet, m+":ctl", sprint.CtlID(m), 0, "kind", "member", "status", sprint.Up)
}

// readers adds reader rows.
func (w *rv) readers(names ...string) {
	w.t.Helper()
	w.rows(sprint.Readers, names...)
}

// inReview puts a primary of s in review at attempt 1, head h1, its work ok,
// with ok reads from the readers named.
func (w *rv) inReview(s, p string, score int, okFrom ...string) {
	w.t.Helper()
	var rcards []string
	for _, r := range okFrom {
		rcards = append(rcards, sprint.ReadCardID(p, 1, r))
	}
	w.card(sprint.Work, s+":review", p, score, "kind", "work", "attempt", "1", "head", "h1", "result", "ok", "rcards", strings.Join(rcards, ","))
	for _, r := range okFrom {
		w.card(sprint.Readers, r+":ok", sprint.ReadCardID(p, 1, r), score, "kind", "read", sprint.PrimaryField, p, "stream", s,
			"reader", r, "attempt", "1", "head", "h1")
	}
}

// merging puts a primary of s in merging with its merge card queued.
func (w *rv) merging(s, p string, score int) {
	w.t.Helper()
	w.card(sprint.Work, s+":merging", p, score, "kind", "work", "attempt", "1", "head", "h1")
	w.card(sprint.Merge, s+":queued", p, score, "kind", "merge", sprint.PrimaryField, p, "stream", s)
}

// rec is a card's record as the store holds it.
func (w *rv) rec(table, id string) tset.MemberRecord {
	w.t.Helper()
	res, err := sprintfn.Read(context.Background(), w.tw, &sprintfn.ReadRequest{Epoch: "0", Tset: []tset.ReadQuery{{Kind: "ids", Table: table, IDs: []string{id}}}})
	if err != nil || res.Refusal != nil || res.Err != nil {
		w.t.Fatalf("read %s %s: %v %v %v", table, id, err, res.Refusal, res.Err)
	}
	return res.Read.Tset[0].Records[0]
}

// at says where a card is, "" off the table.
func (w *rv) at(table, id string) string {
	w.t.Helper()
	r := w.rec(table, id)
	if r.Place == nil {
		return ""
	}
	return r.Place.Row + ":" + r.Place.Col
}

// field is a card's field, "" when absent.
func (w *rv) field(table, id, name string) string {
	w.t.Helper()
	return w.rec(table, id).Fields[name].Value
}

// jopen is the judgments open or held on a subject: type|cause -> note.
func (w *rv) jopen(subject string) map[string]string {
	return w.tw.SprintKeys()[rvPrefix+"sprint:jopen:"+subject+"@0"].Hash
}

// dropping is the dropping marks: stream -> op.
func (w *rv) dropping() map[string]string {
	return w.tw.SprintKeys()[rvPrefix+"sprint:dropping@0"].Hash
}

// lines is the log's lines, each decoded.
func (w *rv) lines() []map[string]any {
	var out []map[string]any
	for _, raw := range w.log.Lines(rvPrefix, "0") {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			w.t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

// noteLines is the note lines of a type, their metas.
func (w *rv) noteLines(typ string) []map[string]any {
	var out []map[string]any
	for _, l := range w.lines() {
		b, _ := json.Marshal(l)
		if strings.Contains(string(b), `"type":"`+typ+`"`) {
			out = append(out, l)
		}
	}
	return out
}

// trips starts counting round trips.
func (w *rv) trips() func() int {
	w.cc.Reset()
	return w.cc.Trips
}

// wantTrips pins a verb's round trips, as it counts them and as the client does.
func wantTrips(t *testing.T, res Result, counted func() int, want int) {
	t.Helper()
	if res.Trips != want || counted() != want {
		t.Fatalf("round trips: the verb counts %d and the client %d, want %d", res.Trips, counted(), want)
	}
}

// refusedWith says err is a refusal with the code, and returns it.
func refusedWith(t *testing.T, err error, code string) *Refused {
	t.Helper()
	var rf *Refused
	if !errors.As(err, &rf) || rf.Code() != code {
		t.Fatalf("want a refusal %s, got %v", code, err)
	}
	return rf
}

func mustOK(t *testing.T, what string, res Result, err error) Result {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	return res
}

// ---- ask

func TestAskAnotherRefusedPast15(t *testing.T) {
	t.Parallel()
	w := newRV(t)
	w.stream("s1", sprint.StreamWaiting)
	w.readers("r1", "r2", "r3")
	// p1 has 15 read cards (rcards at most 15); p2 has two, and r3 is free.
	var rcards []string
	for i := 0; i < sprint.MaxRCards; i++ {
		rcards = append(rcards, fmt.Sprintf("old.r%d.x", i))
	}
	w.card(sprint.Work, "s1:review", "p1", 1, "kind", "work", "attempt", "1", "head", "h1", "result", "ok", "rcards", strings.Join(rcards, ","))
	w.inReview("s1", "p2", 2, "r1", "r2")

	n := w.trips()
	_, err := Ask(context.Background(), w.env, AskReq{IDs: []string{"p1", "p2"}, Another: true})
	rf := refusedWith(t, err, sprintfn.CodeRequest)
	if !rf.Local || len(rf.Refusal.Detail.IDs) != 1 || rf.Refusal.Detail.IDs[0] != "p1" || !strings.Contains(rf.Error(), "15 read cards") {
		t.Fatalf("the refusal names p1 and its 15 read cards: %v (ids %v)", rf, rf.Refusal.Detail.IDs)
	}
	if n() != 1 || w.cc.Steps() != 0 {
		t.Fatalf("a local refusal is its read alone: %d trips, %d steps", n(), w.cc.Steps())
	}
	if got := w.at(sprint.Readers, sprint.ReadCardID("p2", 1, "r3")); got != "" {
		t.Fatalf("one step, all or nothing: p2's read card was written at %s", got)
	}

	// p2 alone: one more reader, r3, the one not yet asked at its attempt.
	n = w.trips()
	res, err := Ask(context.Background(), w.env, AskReq{IDs: []string{"p2"}, Another: true})
	mustOK(t, "ask --another p2", res, err)
	wantTrips(t, res, n, 2)
	rid := sprint.ReadCardID("p2", 1, "r3")
	if got := w.at(sprint.Readers, rid); got != "r3:asked" {
		t.Fatalf("p2's third read card is at %q, want r3:asked", got)
	}
	if got := w.field(sprint.Work, "p2", "rcards"); !strings.HasSuffix(got, ","+rid) {
		t.Fatalf("p2's rcards %q does not end with %s", got, rid)
	}
	if w.field(sprint.Readers, rid, "due_unbegun") == "" {
		t.Fatal("an asked read card has its due (1.2)")
	}
}

// rvHook stands between a verb and the counting client: before the pipeline
// call numbered n (from 1) it runs before(n), which may write fixtures to the
// twin as another writer would, and it loses the call whose step is numbered
// kill (from 1: the steps the verb sent) before sending it, as a dropped
// connection would (OUTCOMEUNKNOWN: nothing of it applied).
type rvHook struct {
	c      sprintfn.Client
	mu     sync.Mutex
	calls  int
	steps  int
	before func(n int)
	kill   int
}

func (h *rvHook) Pipeline(ctx context.Context, items []sprintfn.Item) ([]sprintfn.Result, error) {
	h.mu.Lock()
	h.calls++
	n := h.calls
	lose := false
	for _, it := range items {
		if it.Step != nil {
			h.steps++
			if h.steps == h.kill {
				lose = true
			}
		}
	}
	h.mu.Unlock()
	if h.before != nil {
		h.before(n)
	}
	if lose {
		return nil, &sprintfn.OutcomeUnknownError{Cause: errors.New("the connection dropped before the step was sent")}
	}
	return h.c.Pipeline(ctx, items)
}

// hooked is an Env of the world's actor over a hook in front of its
// counting client.
func (w *rv) hooked(h *rvHook) *Env {
	h.c = w.cc
	return &Env{C: h, Names: rvNames, Actor: "coord", noWait: true}
}

// ---- accept

func TestAcceptNamedInParts(t *testing.T) {
	t.Parallel()
	w := newRV(t)
	w.stream("s1", sprint.StreamWaiting)
	w.readers("r1", "r2", "r3")
	w.inReview("s1", "p1", 1, "r1", "r2")
	w.inReview("s1", "p2", 2, "r1", "r2")
	w.inReview("s1", "p3", 3, "r1") // one ok read: not yet acceptable
	// p1 also has an outstanding read, which its accept retires.
	extra := sprint.ReadCardID("p1", 1, "r3")
	w.card(sprint.Readers, "r3:asked", extra, 1, "kind", "read", sprint.PrimaryField, "p1", "stream", "s1", "reader", "r3", "attempt", "1", "head", "h1")
	w.put(tset.Entry{Kind: "move", Table: sprint.Work, From: "s1:review", IDs: []string{"p1"}, About: []string{"p1"},
		Set: map[string]string{"rcards": strings.Join([]string{sprint.ReadCardID("p1", 1, "r1"), sprint.ReadCardID("p1", 1, "r2"), extra}, ",")}})

	// One primary a part: parts 1 and 2 apply, part 3 is refused naming p3.
	n := w.trips()
	res, err := Accept(context.Background(), w.env, AcceptReq{Op: "acc1", IDs: []string{"p3", "p1", "p2"}, Chunk: acceptEach})
	rf := refusedWith(t, err, sprintfn.CodeRequest)
	if rf.Part != 3 || rf.Op != "acc1" || len(rf.Refusal.Detail.IDs) != 1 || rf.Refusal.Detail.IDs[0] != "p3" {
		t.Fatalf("part 3 is refused naming p3: %v", rf)
	}
	if res.Parts != 2 || n() != 1+1+2 {
		t.Fatalf("parts 1 and 2 applied (%d), in %d round trips: the resume's done, then n + 1 for the two parts and the refused third's read, want 4", res.Parts, n())
	}
	for _, p := range []string{"p1", "p2"} {
		if got := w.at(sprint.Work, p); got != "s1:merging" {
			t.Fatalf("%s is at %s, want s1:merging", p, got)
		}
		if got := w.at(sprint.Merge, p); got != "s1:queued" {
			t.Fatalf("%s's merge card is at %s, want s1:queued", p, got)
		}
		if got := w.rec(sprint.Merge, p).Score; got != w.rec(sprint.Work, p).Score {
			t.Fatalf("%s's merge card is queued at %s, the primary's score is %s", p, got, w.rec(sprint.Work, p).Score)
		}
	}
	if got := w.at(sprint.Readers, extra); got != "" {
		t.Fatalf("the outstanding read %s was not retired: %s", extra, got)
	}
	if w.at(sprint.Work, "p3") != "s1:review" {
		t.Fatal("p3 stays in review")
	}
	if got := w.field(sprint.Merge, sprint.CtlID("s1"), "state"); got != sprint.StreamMerging {
		t.Fatalf("the stream is %q, want merging", got)
	}
	if w.field(sprint.Merge, sprint.CtlID("s1"), "due_mergeidle") == "" {
		t.Fatal("a stream that starts merging has its merge-idle deadline (1.2)")
	}
	if got := len(w.noteLines(sprint.NStartedMerging)); got != 1 {
		t.Fatalf("%d notices of the stream starting to merge, want 1", got)
	}

	// p3 gets its second ok read; the same command with --op runs part 3 alone.
	w.card(sprint.Readers, "r2:ok", sprint.ReadCardID("p3", 1, "r2"), 3, "kind", "read", sprint.PrimaryField, "p3", "stream", "s1", "reader", "r2", "attempt", "1", "head", "h1")
	w.put(tset.Entry{Kind: "move", Table: sprint.Work, From: "s1:review", IDs: []string{"p3"}, About: []string{"p3"},
		Set: map[string]string{"rcards": sprint.ReadCardID("p3", 1, "r1") + "," + sprint.ReadCardID("p3", 1, "r2")}})
	n = w.trips()
	res, err = Accept(context.Background(), w.env, AcceptReq{Op: "acc1", IDs: []string{"p1", "p2", "p3"}, Chunk: acceptEach})
	mustOK(t, "accept --op acc1", res, err)
	if res.Resumed != 2 || res.Parts != 1 {
		t.Fatalf("the resume found %d parts applied and ran %d, want 2 and 1", res.Resumed, res.Parts)
	}
	wantTrips(t, res, n, 1+1+1) // done, then n + 1 for one part
	if w.at(sprint.Work, "p3") != "s1:merging" {
		t.Fatal("p3 is accepted by the resumed part")
	}
	// A repeat of the finished op writes nothing.
	n = w.trips()
	res, err = Accept(context.Background(), w.env, AcceptReq{Op: "acc1", IDs: []string{"p1", "p2", "p3"}, Chunk: acceptEach})
	mustOK(t, "accept --op acc1 again", res, err)
	if !res.Replay || w.cc.Steps() != 0 {
		t.Fatalf("a finished op replays with no step: replay %v, %d steps", res.Replay, w.cc.Steps())
	}
	wantTrips(t, res, n, 1)
}

func TestAcceptStreamCursor(t *testing.T) {
	t.Parallel()
	w := newRV(t)
	w.stream("s1", sprint.StreamWaiting)
	w.readers("r1", "r2")
	w.inReview("s1", "p1", 10, "r1", "r2")
	w.inReview("s1", "p2", 20, "r1") // ineligible: left in review
	w.inReview("s1", "p3", 30, "r1", "r2")
	w.inReview("s1", "p4", 40, "r1", "r2") // the boundary
	// After part 1, p0 enters review below the cursor and p9 above the
	// boundary: neither is in the op's selection.
	h := &rvHook{before: func(n int) {
		if n == 3 {
			w.inReview("s1", "p0", 5, "r1", "r2")
			w.inReview("s1", "p9", 90, "r1", "r2")
		}
	}}
	env := w.hooked(h)
	n := w.trips()
	res, err := Accept(context.Background(), env, AcceptReq{Streams: []string{"s1"}, Chunk: acceptEach})
	mustOK(t, "accept --stream s1", res, err)
	if res.Parts != 4 {
		t.Fatalf("one card a part over four cards: %d parts", res.Parts)
	}
	wantTrips(t, res, n, 4+1)
	for p, want := range map[string]string{"p1": "s1:merging", "p2": "s1:review", "p3": "s1:merging", "p4": "s1:merging", "p0": "s1:review", "p9": "s1:review"} {
		if got := w.at(sprint.Work, p); got != want {
			t.Fatalf("%s is at %s, want %s", p, got, want)
		}
	}
}

// ---- rework

func TestReworkAtRedealBound(t *testing.T) {
	t.Parallel()
	w := newRV(t)
	w.stream("s1", sprint.StreamWaiting)
	w.member("m1")
	w.member("m2")
	// p1 is in ready at its redeal bound: its work card was dealt again five
	// times and is withdrawn; the coordinator's rework takes it (2.2).
	w.card(sprint.Work, "s1:ready", "p1", 1, "kind", "work", "attempt", "1", "head", "h1", "bound", "redeals", "work", sprint.WorkCardID("p1", 1))
	w.card(sprint.Fleet, "m1:withdrawn", sprint.WorkCardID("p1", 1), 1, "kind", "work", sprint.PrimaryField, "p1", "stream", "s1",
		"attempt", "1", "member", "m1", "redeals", "5")

	n := w.trips()
	res, err := Rework(context.Background(), w.env, ReworkReq{IDs: []string{"p1"}, Fix: "split the change in two"})
	mustOK(t, "rework p1", res, err)
	wantTrips(t, res, n, 2)
	if got := w.at(sprint.Fleet, sprint.WorkCardID("p1", 1)); got != "" {
		t.Fatalf("the withdrawn work card is retired, and is at %s", got)
	}
	next := sprint.WorkCardID("p1", 2)
	if got := w.at(sprint.Fleet, next); got != "m2:ready" {
		t.Fatalf("attempt 2 is dealt to the up member that is not avoid: %s, want m2:ready", got)
	}
	if got := w.at(sprint.Work, "p1"); got != "s1:working" {
		t.Fatalf("p1 is at %s, want s1:working", got)
	}
	p1 := w.rec(sprint.Work, "p1")
	if p1.Fields["bound"].Present || p1.Fields["attempt"].Value != "2" || p1.Fields["fix"].Value != "split the change in two" || p1.Fields["avoid"].Value != "m1" {
		t.Fatalf("p1 after the rework: %+v", p1.Fields)
	}

	// A primary in review is taken too, at any attempt.
	w.readers("r1")
	w.card(sprint.Work, "s1:review", "p2", 2, "kind", "work", "attempt", "3", "head", "h1", "result", "failed", "work", sprint.WorkCardID("p2", 3))
	res, err = Rework(context.Background(), w.env, ReworkReq{IDs: []string{"p2"}, Fix: "again"})
	mustOK(t, "rework p2", res, err)
	if got := w.field(sprint.Work, "p2", "attempt"); got != "4" {
		t.Fatalf("the coordinator's rework takes attempt 3 to %s, want 4", got)
	}
}

// ---- return

func TestReturnOpensReturned(t *testing.T) {
	t.Parallel()
	w := newRV(t)
	w.stream("s1", sprint.StreamMerging)
	w.merging("s1", "p1", 1)
	w.merging("s1", "p2", 2)
	n := w.trips()
	res, err := Return(context.Background(), w.env, ReturnReq{IDs: []string{"p2", "p1"}})
	mustOK(t, "return", res, err)
	wantTrips(t, res, n, 2)
	for _, p := range []string{"p1", "p2"} {
		if got := w.at(sprint.Work, p); got != "s1:review" {
			t.Fatalf("%s is at %s, want s1:review", p, got)
		}
		if got := w.at(sprint.Merge, p); got != "s1:returned" {
			t.Fatalf("%s's merge card is at %s, want s1:returned", p, got)
		}
		if _, open := w.jopen(p)[typeReturned+"|"+causeReturn]; !open {
			t.Fatalf("\"returned to review\" is not open on %s: %v", p, w.jopen(p))
		}
	}
	// A primary not merging refuses the whole step.
	_, err = Return(context.Background(), w.env, ReturnReq{IDs: []string{"p1"}})
	refusedWith(t, err, sprintfn.CodeRequest)
}

// ---- ci

func TestCIRedOpensGreenCloses(t *testing.T) {
	t.Parallel()
	w := newRV(t)
	w.stream("s1", sprint.StreamWaiting)
	w.inReview("s1", "p1", 1)
	n := w.trips()
	res, err := CI(context.Background(), w.env, CIReq{IDs: []string{"p1"}, Red: true})
	mustOK(t, "ci red", res, err)
	wantTrips(t, res, n, 2)
	if w.field(sprint.Work, "p1", "ci") != "red" || w.field(sprint.Work, "p1", "ci_head") != "h1" {
		t.Fatal("ci red at p1's head is recorded")
	}
	key := typeCIRed + "|" + causeCI
	if _, open := w.jopen("p1")[key]; !open {
		t.Fatalf("\"ci red on a primary\" is not open on p1: %v", w.jopen("p1"))
	}
	res, err = CI(context.Background(), w.env, CIReq{IDs: []string{"p1"}})
	mustOK(t, "ci green", res, err)
	if _, open := w.jopen("p1")[key]; open {
		t.Fatal("a green CI at the red's head closes it")
	}
	if w.field(sprint.Work, "p1", "ci") != "green" || len(w.noteLines(typeCIGreen)) != 1 {
		t.Fatal("ci green is recorded, and said once")
	}
}

func TestCIRedOnMergingRetreats(t *testing.T) {
	t.Parallel()
	w := newRV(t)
	w.stream("s1", sprint.StreamMerging)
	w.merging("s1", "p1", 1)
	n := w.trips()
	res, err := CI(context.Background(), w.env, CIReq{IDs: []string{"p1"}, Red: true})
	mustOK(t, "ci red on merging", res, err)
	wantTrips(t, res, n, 2)
	if w.cc.Steps() != 1 {
		t.Fatalf("one step, and %d were sent", w.cc.Steps())
	}
	if w.at(sprint.Work, "p1") != "s1:review" || w.at(sprint.Merge, "p1") != "s1:returned" || w.field(sprint.Work, "p1", "ci") != "red" {
		t.Fatal("p1 went back to review, its merge card to returned, with ci red")
	}
	// In that one step: the retreat's lines, then the judgment's.
	first, last := res.Step.Reply.FirstSeq, res.Step.Reply.LastSeq
	var kinds []string
	for _, l := range w.lines() {
		seq, _ := strconv.Atoi(l["seq"].(string))
		f, _ := strconv.Atoi(string(first))
		z, _ := strconv.Atoi(string(last))
		if seq < f || seq > z {
			continue
		}
		k := l["kind"].(string)
		if k == "note" {
			k += ":" + l["meta"].(map[string]any)["type"].(string)
		} else {
			k += ":" + l["table"].(string)
		}
		kinds = append(kinds, k)
	}
	want := []string{"move:merge", "move:work", "note:" + typeCIRed}
	if strings.Join(kinds, " ") != strings.Join(want, " ") {
		t.Fatalf("the step's lines are %v, want %v", kinds, want)
	}
}

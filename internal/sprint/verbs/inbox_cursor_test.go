package verbs

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/machine"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The coordinator's stored inbox cursor (inbox_cursor.go) on the twin: inbox
// --read writes it, inbox and inbox --wait start from it, and the loop the
// command gives the coordinator, wait, read, wait, loses nothing between calls
// and repeats nothing.

// storedOf is the stored cursor, read as inbox reads it, and whether one is
// stored.
func storedOf(t *testing.T, w *jrWorld) (uint64, bool) {
	t.Helper()
	res, err := w.env.Do(context.Background(), Planned{Verb: "inbox", Read: func(epoch tset.Decimal) *sprintfn.ReadRequest {
		return &sprintfn.ReadRequest{Epoch: epoch, Tset: []tset.ReadQuery{cursorQuery()}}
	}})
	if err != nil {
		t.Fatal(err)
	}
	c, has, err := cursorAnswer("inbox", res.Read.Tset[0])
	if err != nil {
		t.Fatal(err)
	}
	return c, has
}

// knowNote writes a HAPPENED notice through J, one line.
func (w *jrWorld) knowNote(typ string, subjects ...string) {
	w.t.Helper()
	w.step(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "test", Rule: "test"}, Body: sprintfn.Body{
		Notes: []sprintfn.NoteReq{{Op: sprintfn.JOpKnow, Type: typ, Subjects: subjects, Text: "told by the test"}}}})
}

// coordWait is inbox --wait as the command runs it with no --after: from the
// stored cursor.
func coordWait(t *testing.T, w *jrWorld, wt *jrWaiter, timeout time.Duration) InboxView {
	t.Helper()
	var v InboxView
	if _, err := Inbox(context.Background(), w.env, InboxReq{Stored: true, Wait: wt.wait(timeout), Out: &v}); err != nil {
		t.Fatal(err)
	}
	return v
}

// coordRead is inbox --read as the command runs it.
func coordRead(t *testing.T, w *jrWorld) (InboxView, Result) {
	t.Helper()
	var v InboxView
	res, err := Inbox(context.Background(), w.env, InboxReq{Read: true, Out: &v})
	if err != nil {
		t.Fatal(err)
	}
	return v, res
}

// TestInboxReadWritesTheStoredCursor: inbox --read writes the last line shown
// as the coordinator's cursor, and inbox with no --after lists from it: what
// was shown is not shown again, a line written after it is. A read that shows
// no line writes nothing, and the cursor never goes down.
func TestInboxReadWritesTheStoredCursor(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	if c, has := storedOf(t, w); has || c != 0 {
		t.Fatalf("a new sprint stores cursor %d (stored %v), want none", c, has)
	}
	w.open(typeStepRefused, "LIMIT", "k1")
	w.knowNote(sprint.NMemberUp, "m1")
	last := w.lastSeq()

	v, res := coordRead(t, w)
	if v.Cursor != "0" || v.Last != strconv.FormatUint(last, 10) || noticeCount(v, sprint.NMemberUp) != 1 {
		t.Fatalf("the first read: %+v, want cursor 0, last %d and the notice", v, last)
	}
	if !strings.Contains(res.Said, "INBOX CURSOR "+v.Last+" (was 0)") || res.Step == nil {
		t.Fatalf("the read said %q with step %v, want the cursor written in a step of its own", res.Said, res.Step)
	}
	if c, has := storedOf(t, w); !has || c != last {
		t.Fatalf("the stored cursor is %d (stored %v), want %d", c, has, last)
	}

	// From the stored cursor: the notice shown is not shown again.
	var again InboxView
	if _, err := Inbox(context.Background(), w.env, InboxReq{Stored: true, Out: &again}); err != nil {
		t.Fatal(err)
	}
	if again.Cursor != v.Last || noticeCount(again, sprint.NMemberUp) != 0 {
		t.Fatalf("an inbox from the stored cursor: %+v, want cursor %s and no notice again", again, v.Last)
	}
	// A read that shows no line writes nothing.
	steps := w.cc.Steps()
	v2, res2 := coordRead(t, w)
	if v2.Last != v.Last || res2.Step != nil || w.cc.Steps() != steps || !strings.Contains(res2.Said, "INBOX CURSOR "+v.Last+"\n") {
		t.Fatalf("a read with nothing new: %+v, step %v, said %q; want the cursor unchanged and no step", v2, res2.Step, res2.Said)
	}
	// A line after it is shown, once.
	w.knowNote(sprint.NMemberUp, "m2")
	v3, _ := coordRead(t, w)
	if noticeCount(v3, sprint.NMemberUp) != 1 || v3.Cursor != v.Last {
		t.Fatalf("the read after a new line: %+v, want its one notice from cursor %s", v3, v.Last)
	}
	// The cursor never goes down.
	res4, err := moveCursor(context.Background(), w.env, 0, 1)
	if err != nil || res4.Step != nil {
		t.Fatalf("moving the cursor to 1: %v, step %v, want no step", err, res4.Step)
	}
	if c, _ := storedOf(t, w); c != w.lastSeq() {
		t.Fatalf("the stored cursor went to %d, want %d", c, w.lastSeq())
	}
}

// TestInboxReadIsTheCoordinatorsAlone: another actor's read shows the inbox
// and writes nothing (NOTCOORD), and a caller's own --after neither reads nor
// writes the stored cursor.
func TestInboxReadIsTheCoordinatorsAlone(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	w.knowNote(sprint.NMemberUp, "m1")
	other := &Env{C: w.cc, Names: jrNames, Actor: "someone", noWait: true}
	_, err := Inbox(context.Background(), other, InboxReq{Read: true})
	var rf *Refused
	if !errors.As(err, &rf) || rf.Code() != "NOTCOORD" {
		t.Fatalf("another actor's inbox --read: %v, want NOTCOORD", err)
	}
	if c, has := storedOf(t, w); has || c != 0 {
		t.Fatalf("a refused read stored cursor %d (%v)", c, has)
	}
	var v InboxView
	if _, err := Inbox(context.Background(), other, InboxReq{Stored: true, Out: &v}); err != nil || noticeCount(v, sprint.NMemberUp) != 1 {
		t.Fatalf("another actor's inbox: %v, %+v, want the read to work", err, v)
	}
	if _, err := Inbox(context.Background(), w.env, InboxReq{After: w.lastSeq(), Out: &v}); err != nil || noticeCount(v, sprint.NMemberUp) != 0 {
		t.Fatalf("an inbox after the last line: %v, %+v", err, v)
	}
	if c, has := storedOf(t, w); has || c != 0 {
		t.Fatalf("an inbox with its own cursor stored %d (%v)", c, has)
	}
}

// TestInboxWaitStartsFromTheStoredCursor: the wait with no --after starts from
// the stored cursor and consumes nothing: until inbox --read moves the cursor,
// the same tick-end wakes it again, and after the read it does not.
func TestInboxWaitStartsFromTheStoredCursor(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	wt := newJRWaiter()
	w.knowNote(sprint.NMemberUp, "m1")
	w.knowNote(sprint.NMemberUp, "m2")
	l := w.lastSeq()
	seq := func(n uint64) string { return strconv.FormatUint(n, 10) }
	wt.appendTickEnd(t, 0, seq(l), 1) // the notes' stream is the test's own: a tick-end at the log's last line
	v := coordWait(t, w, wt, time.Hour)
	if !v.Woke || v.Cursor != "0" || v.Last != seq(l) || len(wt.blocked) != 0 {
		t.Fatalf("the first wait: %+v, want the tick-end n%d at once from cursor 0", v, l)
	}
	if !strings.Contains(w.saidOfWait(t, wt), "inbox --read reads it from the cursor 0") {
		t.Fatal("the wait does not say where the batch is read from")
	}
	if c, has := storedOf(t, w); has || c != 0 {
		t.Fatalf("a wait stored cursor %d (%v): it consumes nothing", c, has)
	}
	if v = coordWait(t, w, wt, time.Hour); !v.Woke || v.Cursor != "0" {
		t.Fatalf("with the cursor not moved the same tick-end wakes the wait again: %+v", v)
	}
	// The stored cursor moves to the tick-end (inbox --read): a wait from it
	// finds only the tick-end after.
	w.knowNote(sprint.NMemberUp, "m3")
	wt.appendTickEnd(t, 0, seq(l+1), 1)
	if _, err := moveCursor(context.Background(), w.env, 0, l); err != nil {
		t.Fatal(err)
	}
	if v = coordWait(t, w, wt, time.Hour); !v.Woke || v.Cursor != seq(l) || v.Last != seq(l+1) {
		t.Fatalf("a wait from the stored cursor %d: %+v, want the tick-end n%d", l, v, l+1)
	}
	// --after is the caller's own, over the stored one.
	var own InboxView
	if _, err := Inbox(context.Background(), w.env, InboxReq{After: 0, Wait: wt.wait(time.Hour), Out: &own}); err != nil || own.Cursor != "0" || own.Last != seq(l) {
		t.Fatalf("a wait with --after 0: %v, %+v, want cursor 0 and the tick-end n%d", err, own, l)
	}
}

// saidOfWait is what a wait from the stored cursor says.
func (w *jrWorld) saidOfWait(t *testing.T, wt *jrWaiter) string {
	t.Helper()
	res, err := Inbox(context.Background(), w.env, InboxReq{Stored: true, Wait: wt.wait(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	return res.Said
}

// TestCoordinatorLoopThreeTicks: the coordinator's loop from the command
// alone, inbox --wait, inbox --read (act on all), inbox --wait, over three
// ticks that each raise judgments and a notice, and a quiet tick between them.
// Every notice is shown exactly once across the reads, whichever of the
// calls was blocked when its tick ended, and the stored cursor after each read
// is the last line the tick wrote.
func TestCoordinatorLoopThreeTicks(t *testing.T) {
	t.Parallel()
	n := 3
	r := newIdleWakeRig(t, raiseRule("deal", &n))
	seen := map[string]int{} // notice id -> times shown
	shown := func(v InboxView) {
		for _, g := range v.Groups {
			if g.Kind == sprint.Happened && g.Type == sprint.NMemberUp {
				for _, id := range g.Notes {
					seen[id]++
				}
			}
		}
	}
	read := func(judgments int) {
		t.Helper()
		v, res := coordRead(t, r.w)
		shown(v)
		if got := len(judgmentNotes(v, typeStepRefused)); got < judgments {
			t.Fatalf("the read shows %d judgments, want at least the tick's %d", got, judgments)
		}
		if c, _ := storedOf(t, r.w); strconv.FormatUint(c, 10) != v.Last {
			t.Fatalf("after the read (%s) the stored cursor is %d, want the last line %s", res.Said, c, v.Last)
		}
		if v.Last != strconv.FormatUint(r.w.lastSeq(), 10) {
			t.Fatalf("the read shows to line %s, the log is at %d", v.Last, r.w.lastSeq())
		}
	}

	// Tick 1: the tick ends before the wait: it returns at once.
	r.tickAll() // the lease, and the cursor learned
	rep := r.tickAll()
	v := coordWait(t, r.w, r.wt, time.Hour)
	if rep.Wake != 3 || !v.Woke || v.Judgments != 3 {
		t.Fatalf("tick 1: %+v, wait %+v", rep, v)
	}
	read(3)

	// Tick 2: the wait is blocked when the tick's lines are written.
	n = 2
	done := r.wt.startStored(r.w.env, time.Hour)
	<-r.wt.blocked
	rep = r.tickBlocked()
	got := <-done
	if got.err != nil || rep.Wake != 2 || !got.view.Woke || got.view.Judgments != 2 {
		t.Fatalf("tick 2: %+v, wait %+v, %v", rep, got.view, got.err)
	}
	if c, _ := storedOf(t, r.w); strconv.FormatUint(c, 10) != got.view.Cursor {
		t.Fatalf("the wait woke from cursor %s, the stored cursor is %d", got.view.Cursor, c)
	}
	read(2)

	// A quiet tick wakes nothing: the wait times out, and consumes nothing.
	n = 0
	before, _ := storedOf(t, r.w)
	done = r.wt.startStored(r.w.env, 5*time.Minute)
	<-r.wt.blocked
	if rep = r.tickBlocked(); rep.Wake != 0 {
		t.Fatalf("a quiet tick: %+v", rep)
	}
	r.wt.clock.Advance(5 * time.Minute)
	if got = <-done; got.err != nil || got.view.Woke {
		t.Fatalf("the quiet tick woke the wait: %+v, %v", got.view, got.err)
	}
	if c, _ := storedOf(t, r.w); c != before {
		t.Fatalf("a wait that timed out moved the cursor from %d to %d", before, c)
	}

	// Tick 3: after the quiet one.
	n = 1
	done = r.wt.startStored(r.w.env, time.Hour)
	<-r.wt.blocked
	rep = r.tickBlocked()
	if got = <-done; got.err != nil || rep.Wake != 1 || !got.view.Woke || got.view.Judgments != 1 {
		t.Fatalf("tick 3: %+v, wait %+v, %v", rep, got.view, got.err)
	}
	read(1)

	// Nothing repeated, nothing lost: each of the three ticks' notices was shown
	// once (one notice a tick with judgments, and the one of the quiet tick's
	// none).
	if len(seen) != 3 {
		t.Fatalf("the loop showed %d notices, want the 3 the ticks wrote: %v", len(seen), seen)
	}
	for id, k := range seen {
		if k != 1 {
			t.Fatalf("notice %s was shown %d times", id, k)
		}
	}
	// The next wait is blocked: nothing to read, no wake.
	done = r.wt.startStored(r.w.env, 5*time.Minute)
	<-r.wt.blocked
	r.wt.clock.Advance(5 * time.Minute)
	if got = <-done; got.err != nil || got.view.Woke {
		t.Fatalf("a wait after the last read woke: %+v, %v", got.view, got.err)
	}
}

// newIdleWakeRig is a started sprint whose machine runs the rules given and
// whose log is mirrored to the notes stream, with no coordinator running: the
// test is the coordinator.
func newIdleWakeRig(t *testing.T, rules ...sprint.Rule) *wakeRig {
	t.Helper()
	r := newWakeRigWith(t, false, rules...)
	return r
}

// tickAll runs one tick and mirrors every line it wrote to the stream at once
// (no one is blocked).
func (r *wakeRig) tickAll() machine.Report {
	r.t.Helper()
	r.w.tick(time.Second)
	rep, err := machine.Tick(context.Background(), r.w.tw, r.loop)
	if err != nil {
		r.t.Fatalf("tick: %v", err)
	}
	for r.mirror() {
	}
	return rep
}

// tickBlocked runs one tick and mirrors its lines to the stream one at a time,
// the wait blocked in the stream reading each before the next; the wait's last
// read returns on the tick-end, so it is not waited for again.
func (r *wakeRig) tickBlocked() machine.Report {
	r.t.Helper()
	r.w.tick(time.Second)
	rep, err := machine.Tick(context.Background(), r.w.tw, r.loop)
	if err != nil {
		r.t.Fatalf("tick: %v", err)
	}
	for r.mirror() {
		if r.mirrored < len(r.w.log.Stored(jrPrefix, "0")) {
			<-r.wt.blocked
		}
	}
	return rep
}

// startStored runs inbox --wait from the stored cursor in its own goroutine.
func (w *jrWaiter) startStored(env *Env, timeout time.Duration) <-chan jrWaitResult {
	done := make(chan jrWaitResult, 1)
	go func() {
		var v InboxView
		res, err := Inbox(context.Background(), env, InboxReq{Stored: true, Wait: w.wait(timeout), Out: &v})
		done <- jrWaitResult{res: res, view: v, err: err}
	}()
	return done
}

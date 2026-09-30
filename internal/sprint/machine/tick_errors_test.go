package machine

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
)

// TestTickErrorStepParksOnceABatch: a plan cut into two requests, both refused
// with a bug code, parks its key once: the error step names the key once and
// its judgment once, applies, and the key is parked and its judgment open; the
// tick does not fail, and the key is not planned again (1.3.5; SprintEvents.tla
// ParkOnBug).
func TestTickErrorStepParksOnceABatch(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	w.verb(create("s1:waiting", waiting(), "q1", "q2"))
	ic := &intercept{c: w.tw, answer: refuseRule("resolve", "NOCOL")}
	k := &counting{c: ic}
	l, err := NewLoop(Config{Names: testNames, Owner: "token-a", Name: "a", Rules: []sprint.Rule{releaseRule(64)}, Build: testBuild(builderOpts{maxUnits: 1})})
	if err != nil {
		t.Fatal(err)
	}
	w.tick(l, k)
	rep := w.tick(l, k)
	if len(rep.Dealt) != 2 || rep.Refused["NOCOL"] != 2 || strings.Join(rep.Parked, ",") != "resolve:s1" || len(l.owed.park) != 1 || len(l.owed.notes) != 1 {
		t.Fatalf("the busy tick: dealt %v, refused %v, parked %v, owed %d keys and %d notes", rep.Dealt, rep.Refused, rep.Parked, len(l.owed.park), len(l.owed.notes))
	}
	for i := 0; i < 5; i++ {
		w.clk.add(TickEvery)
		if rep = w.tick(l, k); rep.Read["resolve"] != 0 || len(rep.Dealt) != 0 {
			t.Fatalf("tick %d planned the parked key: %+v", i, rep)
		}
	}
	if got := strings.Join(codes(ic.errRes), ","); got != "applied" {
		t.Fatalf("the error steps: %s; want one, applied", got)
	}
	if _, parked := w.hash("parked@0")["resolve:s1"]; !parked || !l.owed.empty() || l.failures != 0 {
		t.Fatalf("parked %v, owed %+v, failures %d", w.hash("parked@0"), l.owed, l.failures)
	}
	if len(w.hash("jopen:resolve:s1@0")) != 1 {
		t.Fatalf("the judgment on the key: %v", w.hash("jopen:resolve:s1@0"))
	}
}

// TestTickErrorStepCutAtLimits: what is owed is cut at the store's limits,
// 1,000 parked keys and 2,000 quarantined cards a request, one request each
// RT1, the rest owed; the keys owed are not planned meanwhile (1.3.5).
func TestTickErrorStepCutAtLimits(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	k := &counting{c: w.tw}
	l := w.loop("a", []sprint.Rule{dealRule(64)}, Budget{})
	w.tick(l, k)
	var keys []sprint.AgendaKey
	for i := 0; i < 2500; i++ {
		keys = append(keys, sprint.AgendaKey{Key: fmt.Sprintf("deal:k%d", i), Seq: uint64(i + 1)})
	}
	rep := Report{}
	l.onBug(Batch{Rule: "deal", Keys: keys[:1500]}, "NOCOL", "", "a test's bug", &rep)
	l.onBug(Batch{Rule: "deal", Keys: keys}, "NOCOL", "", "a test's bug", &rep) // the first 1,500 again: owed once
	for i := 0; i < 2500; i++ {
		l.owed.addQuarantine(sprint.Quarantined{ID: fmt.Sprintf("c%d", i), Stream: "s1", Code: "DRIFT", Rule: "deal"})
	}
	if len(l.owed.park) != 2500 || len(rep.Parked) != 2500 || len(l.owed.notes) != 2 { // the later note names every key, in notes of 2,000
		t.Fatalf("owed %d keys (reported %d) and %d notes", len(l.owed.park), len(rep.Parked), len(l.owed.notes))
	}
	var parks, quarantines []int
	for i := 0; i < 3; i++ {
		w.clk.add(TickEvery)
		rep := w.tick(l, k)
		for _, it := range k.sent[len(k.sent)-rep.RoundTrips] {
			if isErrorStep(it) {
				parks = append(parks, len(it.Step.Sprint.Park))
				quarantines = append(quarantines, len(it.Step.Sprint.Quarantine))
			}
		}
	}
	if fmt.Sprint(parks, quarantines) != "[1000 1000 500] [2000 500 0]" {
		t.Fatalf("the error steps parked %v and quarantined %v", parks, quarantines)
	}
	if n := len(w.hash("parked@0")); n != 2500 || !l.owed.empty() || len(l.parked) != 2500 {
		t.Fatalf("%d keys parked in the store, %d known; owed %d", n, len(l.parked), len(l.owed.park))
	}
}

// TestTickErrorStepRefusedFailsTick: an error step refused with any code but
// STALEGEN fails the tick: the failures count, the heartbeat's error names
// the refusal, "the tick keeps failing" shows, and what is owed stays owed,
// its keys out of every plan; once the step applies the count starts again
// (1.3.5, 1.4.1). STALEGEN is a race: owed again, no failure.
func TestTickErrorStepRefusedFailsTick(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	code := "REQUEST"
	ic := &intercept{c: w.tw, answer: func(it sprintfn.Item) *sprintfn.Result {
		if isErrorStep(it) && code != "" {
			return &sprintfn.Result{Refusal: &sprintfn.Refusal{Code: code}}
		}
		return nil
	}}
	k := &counting{c: ic}
	l := w.loop("a", []sprint.Rule{dealRule(64)}, Budget{})
	w.tick(l, k)
	l.onBug(Batch{Rule: "deal", Keys: []sprint.AgendaKey{{Key: "deal", Seq: 1}}}, "NOCOL", "", "a test's bug", &Report{})
	for i := 1; i <= FailingAfter+1; i++ {
		w.verb(create("s1:ready", fresh(), fmt.Sprintf("p%d", i)))
		w.clk.add(TickEvery)
		rep, err := Tick(context.Background(), k, l)
		if err == nil || !strings.Contains(err.Error(), "the error step was refused") || rep.Read["deal"] != 0 {
			t.Fatalf("tick %d: %v, read %v", i, err, rep.Read)
		}
	}
	res, _, err := w.tw.KeyQuery(sprintfn.KeyQ{Kind: sprintfn.KeyHeartbeat})
	if err != nil {
		t.Fatal(err)
	}
	hb := ParseHeartbeat(res.(sprintfn.HeartbeatResult).Fields)
	groups := InboxGroups(hb, Clock{}, hb.TickAt)
	if hb.Failures != FailingAfter || len(groups) != 1 || groups[0].ID != GroupFailing || !strings.Contains(groups[0].What, "REQUEST") {
		t.Fatalf("the heartbeat: %d failures, %q; groups %+v", hb.Failures, hb.Error, groups)
	}
	code = sprintfn.CodeStaleGen
	w.clk.add(TickEvery)
	if rep, err := Tick(context.Background(), k, l); err != nil || rep.Refused[sprintfn.CodeStaleGen] != 1 || rep.Read["deal"] != 0 {
		t.Fatalf("an error step refused STALEGEN: %v, %+v", err, rep)
	}
	if len(l.owed.park) != 1 {
		t.Fatalf("owed %+v", l.owed)
	}
	code = ""
	w.clk.add(TickEvery)
	if rep := w.tick(l, k); l.failures != 0 || !l.owed.empty() || rep.Read["deal"] != 0 {
		t.Fatalf("the tick after: failures %d, owed %+v, %+v", l.failures, l.owed, rep)
	}
	if _, parked := w.hash("parked@0")["deal"]; !parked {
		t.Fatalf("parked %v", w.hash("parked@0"))
	}
}

// TestTickNewLoopLeavesParkedKey: a loop that did not park a key (a new lease
// holder) does not run a refused step again when a new line queues the key:
// the ingest reply names the page's parked keys, and the loop leaves them out
// of its plans with no round trip of its own (1.3.5).
func TestTickNewLoopLeavesParkedKey(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	ic := &intercept{c: w.tw, answer: refuseRule("deal", "NOCOL")}
	k := &counting{c: ic}
	a := w.loop("a", []sprint.Rule{dealRule(64)}, Budget{})
	w.tick(a, k)
	w.verb(create("s1:ready", fresh(), "p1"))
	if rep := w.tick(a, k); rep.Refused["NOCOL"] != 1 {
		t.Fatalf("setup: the deal was not refused: %+v", rep)
	}
	w.clk.add(TickEvery)
	w.tick(a, k) // the error step parks deal
	if _, ok := w.hash("parked@0")["deal"]; !ok {
		t.Fatalf("setup: deal not parked: %v", w.hash("parked@0"))
	}
	b := w.loop("b", []sprint.Rule{dealRule(64)}, Budget{})
	w.clk.add(LeaseHold + TickEvery)
	w.tick(b, k) // b takes the lease and learns the cursor
	w.verb(create("s1:ready", fresh(), "p2"))
	w.clk.add(TickEvery)
	rep := w.tick(b, k)
	if rep.Lines == 0 || len(rep.Dealt) != 0 || rep.Refused["NOCOL"] != 0 || rep.RoundTrips > 2 {
		t.Fatalf("the new loop ran the refused step again: %+v", rep)
	}
}

// TestTickStoppedCutClock: while STOPPED, a look sends the cut clock's step of
// notes and sprint keys key by key: alone, and beside a late key of another
// kind in the same rule's batch, which does not hold it back (1.4.5, D4;
// SprintEvents.tla PlanOrLook).
func TestTickStoppedCutClock(t *testing.T) {
	t.Parallel()
	for _, mixed := range []bool{false, true} {
		w := newWorld(t)
		w.rows("s1")
		w.step(&sprintfn.Request{Epoch: "0", Meta: sprintfn.Meta{Verb: "init", Actor: "coordinator"}, Clock: &sprintfn.ClockPart{Verb: sprintfn.ClockInit}})
		late := testRule("late", headRead(sprint.IndexFresh, 4, []string{"kind"}, nil),
			func(s *sprint.Snapshot, keys []sprint.AgendaKey, now sprint.Now) sprint.RulePlan {
				var rp sprint.RulePlan
				for _, key := range keys {
					if op, ok := strings.CutPrefix(key.Key, "late:cut:"); ok {
						rp.Notes = append(rp.Notes, sprint.NoteReq{Op: "open", Type: "a verb in parts stopped before its end", Cause: "cut",
							Subjects: []string{op}, Text: "cut"})
						rp.Done = append(rp.Done, key)
					} else {
						rp.Plan.Units = append(rp.Plan.Units, moveUnit(sprint.Work, "p1", "s1", "ready", "working", nil))
					}
				}
				return rp
			})
		ic := &intercept{c: w.tw, agenda: []sprint.AgendaKey{{Key: "late:cut:op1", Seq: 1}}}
		if mixed {
			ic.agenda = append(ic.agenda, sprint.AgendaKey{Key: "late:c1", Seq: 2})
		}
		k := &counting{c: ic}
		l := w.loop("a", []sprint.Rule{late}, Budget{})
		rep := w.tick(l, k)
		if !rep.Looked || strings.Join(rep.Dealt, ",") != "late" || rep.Applied != 1 || rep.MovesDue != mixed {
			t.Fatalf("mixed %v: looked %v, dealt %v, applied %d, moves due %v", mixed, rep.Looked, rep.Dealt, rep.Applied, rep.MovesDue)
		}
		body := k.sent[len(k.sent)-1][0].Step.Body
		if len(body.Entries) != 0 || strings.Join(body.Done, ",") != "late:cut:op1" || len(body.Notes) != 1 {
			t.Fatalf("mixed %v: the step sent %+v", mixed, body)
		}
	}
}

// owe puts n notes of text bytes each into the loop's owed error step.
func owe(l *Loop, n, text int) {
	for i := 0; i < n; i++ {
		l.owed.addNotes(sprint.NoteReq{Op: "open", Type: TypeStepRefused, Cause: fmt.Sprintf("C%d", i),
			Subjects: []string{fmt.Sprintf("deal:k%d", i)}, Text: strings.Repeat("y", text)})
	}
}

// TestErrorRequestNotesOverBytes: notes whose bytes alone pass the step's
// bytes are cut to the notes that fit, and the rest ride later ticks; the
// request comes back (the halving keeps its count, and a chunk of
// one that is over the bytes is not retried for ever), and every owed note
// leaves in a chunk that fits (1.3.5).
func TestErrorRequestNotesOverBytes(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	b := DefaultBudget()
	b.StepBytes = 4000
	l := w.loop("a", []sprint.Rule{dealRule(64)}, b)
	owe(l, 10, 1000)
	done := make(chan struct{})
	go func() {
		defer close(done)
		sent := 0
		for !l.owed.empty() {
			req, c := l.errorRequest()
			if n, ref := sprintfn.EncodedSize(l.cfg.Names.Prefix, req); ref != nil || n > b.StepBytes {
				t.Errorf("chunk %+v is %d bytes, over %d (%v)", c, n, b.StepBytes, ref)
				return
			}
			if c.notes < 1 || c.notes > 3 || len(req.Body.Notes) != c.notes {
				t.Errorf("chunk %+v carries %d notes", c, len(req.Body.Notes))
				return
			}
			sent += c.notes
			l.owed.settle(c)
		}
		if sent != 10 {
			t.Errorf("%d notes went, want 10", sent)
		}
	}()
	<-done // a loop that does not end is the package's test timeout
}

// TestErrorRequestOneNoteOverBytes: one note whose text alone passes the
// step's bytes is cut in its text, its head kept and the cut said, and goes;
// a text of any length is brought under the limit (1.3.5).
func TestErrorRequestOneNoteOverBytes(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	b := DefaultBudget()
	b.StepBytes = 4000
	l := w.loop("a", []sprint.Rule{dealRule(64)}, b)
	head := "the deliverer said: "
	l.owed.addNotes(sprint.NoteReq{Op: "open", Type: TypeStepRefused, Cause: "C", Subjects: []string{"deal:k1"},
		Text: head + strings.Repeat("é", 32<<10)}) // a two-byte rune: the cut never splits one
	type ret struct {
		req *sprintfn.Request
		c   errChunk
	}
	done := make(chan ret, 1)
	go func() { req, c := l.errorRequest(); done <- ret{req, c} }()
	{
		r := <-done // a loop that does not end is the package's test timeout
		n, ref := sprintfn.EncodedSize(l.cfg.Names.Prefix, r.req)
		text := r.req.Body.Notes[0].Text
		if ref != nil || n > b.StepBytes || r.c.notes != 1 || !strings.HasPrefix(text, head) || !strings.HasSuffix(text, "... (cut)") || !utf8.ValidString(text) {
			t.Fatalf("%d bytes (%v), chunk %+v, text of %d bytes %.40q", n, ref, r.c, len(text), text)
		}
		l.owed.settle(r.c)
		if !l.owed.empty() {
			t.Fatal("the note stays owed")
		}
	}
}

// errorStepSizes is the notes and about ids of each error step the loop sends
// over the ticks.
func errorStepSizes(t *testing.T, w *world, l *Loop, k *counting, ticks int) []string {
	t.Helper()
	var got []string
	for i := 0; i < ticks; i++ {
		w.clk.add(TickEvery)
		rep := w.tick(l, k)
		for _, it := range k.sent[len(k.sent)-rep.RoundTrips] {
			if isErrorStep(it) {
				a := 0
				for _, nn := range it.Step.Body.Notes {
					a += len(nn.Subjects)
				}
				got = append(got, fmt.Sprintf("%d/%d", len(it.Step.Body.Notes), a))
			}
		}
		if rep.Err != nil {
			t.Fatalf("tick %d: %v", i, rep.Err)
		}
	}
	return got
}

// TestErrorStepNotesAt100: the owed notes are cut at Layer 1's 100 notes a
// step: 101 go as 100 then 1, each applied (1.3.5; L1 6).
func TestErrorStepNotesAt100(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	k := &counting{c: w.tw}
	l := w.loop("a", []sprint.Rule{dealRule(64)}, Budget{})
	w.tick(l, k)
	for i := 0; i < 101; i++ {
		l.owed.addNotes(sprint.NoteReq{Op: "open", Type: TypeStepRefused, Cause: fmt.Sprintf("C%d", i), Subjects: []string{fmt.Sprintf("deal:k%d", i)}, Text: "x"})
	}
	if got := strings.Join(errorStepSizes(t, w, l, k, 2), ","); got != "100/100,1/1" || !l.owed.empty() {
		t.Fatalf("error steps %s, owed %d", got, len(l.owed.notes))
	}
}

// TestErrorStepAboutsAt4000: the abouts are cut at 4,000 ids a step: notes of
// 2000, 2000 and 1 subjects go as two notes, then one (1.3.5; L1 6).
func TestErrorStepAboutsAt4000(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	k := &counting{c: w.tw}
	l := w.loop("a", []sprint.Rule{dealRule(64)}, Budget{})
	w.tick(l, k)
	n := 0
	for i, size := range []int{2000, 2000, 1} {
		var subj []string
		for j := 0; j < size; j++ {
			n++
			subj = append(subj, fmt.Sprintf("deal:k%d", n))
		}
		l.owed.addNotes(sprint.NoteReq{Op: "open", Type: TypeStepRefused, Cause: fmt.Sprintf("C%d", i), Subjects: subj, Text: "x"})
	}
	if got := strings.Join(errorStepSizes(t, w, l, k, 2), ","); got != "2/4000,1/1" || !l.owed.empty() {
		t.Fatalf("error steps %s, owed %d", got, len(l.owed.notes))
	}
}

package verbs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
)

// dropWorld is a stream s1 with an open card in every open cell, each with
// its live cards, and a stream s2 with one in review.
func dropWorld(t *testing.T) *rv {
	t.Helper()
	w := newRV(t)
	w.stream("s1", sprint.StreamMerging)
	w.stream("s2", sprint.StreamWaiting)
	w.member("m1")
	w.readers("r1", "r2")
	w.card(sprint.Work, "s1:waiting", "a1", 1, "kind", "work", "attempt", "0", "open", "1", "needs", "x")
	w.card(sprint.Work, "s1:ready", "a2", 2, "kind", "work", "attempt", "0")
	w.card(sprint.Work, "s1:ready", "a3", 3, "kind", "work", "attempt", "0")
	w.card(sprint.Work, "s1:working", "a4", 4, "kind", "work", "attempt", "1")
	w.card(sprint.Fleet, "m1:working", sprint.WorkCardID("a4", 1), 4, "kind", "work", sprint.PrimaryField, "a4", "stream", "s1", "attempt", "1", "member", "m1")
	w.inReview("s1", "a5", 5, "r1", "r2")
	w.merging("s1", "a6", 6)
	w.inReview("s2", "b1", 1)
	return w
}

// dropped says every card of s1's open cells, and their live cards, are gone.
func (w *rv) droppedAll() {
	w.t.Helper()
	for _, p := range []string{"a1", "a2", "a3", "a4", "a5", "a6"} {
		if got := w.at(sprint.Work, p); got != "" {
			w.t.Fatalf("%s is at %s, want dropped", p, got)
		}
	}
	for table, id := range map[string]string{sprint.Fleet: sprint.WorkCardID("a4", 1), sprint.Readers: sprint.ReadCardID("a5", 1, "r1"), sprint.Merge: "a6"} {
		if got := w.at(table, id); got != "" {
			w.t.Fatalf("the live card %s is at %s, want removed with its primary", id, got)
		}
	}
}

// stopAfter is a drop of s1 in parts, one primary a part, that loses its
// connection before step kill: the parts before it stay applied.
func stopAfter(t *testing.T, w *rv, op, col string, kill int) error {
	t.Helper()
	env := w.hooked(&rvHook{kill: kill})
	_, err := Drop(context.Background(), env, DropReq{Op: op, Streams: []string{"s1"}, Col: col, Reason: "out of scope", Chunk: dropEach})
	var u *Unknown
	if !errors.As(err, &u) || u.Part != kill {
		t.Fatalf("want part %d's outcome unknown, got %v", kill, err)
	}
	return err
}

func TestDropStreamFreezes(t *testing.T) {
	t.Parallel()
	w := dropWorld(t)
	stopAfter(t, w, "d1", "", 2)
	if got := w.dropping()["s1"]; got != "d1" {
		t.Fatalf("part 1 freezes s1 for its op: the mark is %q", got)
	}
	if w.at(sprint.Work, "a1") != "" || w.at(sprint.Work, "a2") != "s1:ready" {
		t.Fatal("part 1 drained the head of waiting, and nothing else")
	}
	// Every other step touching s1 is refused DROPPING; s2 is free.
	_, err := CI(context.Background(), w.env, CIReq{IDs: []string{"a5"}, Red: true})
	rf := refusedWith(t, err, sprintfn.CodeDropping)
	if rf.Retries != Retries {
		t.Fatalf("DROPPING is a race, planned again %d times: %d", Retries, rf.Retries)
	}
	_, err = Return(context.Background(), w.env, ReturnReq{IDs: []string{"a6"}})
	refusedWith(t, err, sprintfn.CodeDropping)
	if _, err := CI(context.Background(), w.env, CIReq{IDs: []string{"b1"}, Red: true}); err != nil {
		t.Fatalf("s2 is not frozen: %v", err)
	}
	freezeFirst(t)
	// The op's own parts go on.
	res, err := Drop(context.Background(), w.env, DropReq{Op: "d1", Streams: []string{"s1"}, Reason: "out of scope", Chunk: dropEach})
	mustOK(t, "drop --op d1", res, err)
	w.droppedAll()
	if len(w.dropping()) != 0 {
		t.Fatalf("the last part unfreezes: %v", w.dropping())
	}
}

// freezeFirst is errata 3's H11 (in TestDropStreamFreezes): part 1 reads before its
// freeze exists, so a card that enters a cell before the one part 1 drains
// refuses its step (RANGECOUNT), and the part planned again drains it first.
func freezeFirst(t *testing.T) {
	w := newRV(t)
	w.stream("s1", sprint.StreamWaiting)
	w.card(sprint.Work, "s1:ready", "c2", 2, "kind", "work", "attempt", "0")
	h := &rvHook{before: func(n int) {
		if n == 3 { // part 1's step, after its read
			w.card(sprint.Work, "s1:waiting", "c1", 1, "kind", "work", "attempt", "0", "open", "0")
		}
	}}
	res, err := Drop(context.Background(), w.hooked(h), DropReq{Op: "f1", Streams: []string{"s1"}, Chunk: dropEach})
	mustOK(t, "drop with a card behind the head", res, err)
	if res.Retries != 1 || res.Parts != 2 || w.at(sprint.Work, "c1") != "" || w.at(sprint.Work, "c2") != "" {
		t.Fatalf("part 1 is refused once and planned again, then both cards drop: %d retries, %d parts, c1 %q, c2 %q",
			res.Retries, res.Parts, w.at(sprint.Work, "c1"), w.at(sprint.Work, "c2"))
	}
}

func TestDropColFreezes(t *testing.T) {
	t.Parallel()
	w := dropWorld(t)
	stopAfter(t, w, "d2", sprint.Ready, 2)
	if w.at(sprint.Work, "a2") != "" || w.at(sprint.Work, "a3") != "s1:ready" || w.at(sprint.Work, "a1") != "s1:waiting" {
		t.Fatal("part 1 of --col ready drained the head of ready alone")
	}
	// The whole stream is frozen, not only the cell.
	_, err := Drop(context.Background(), w.env, DropReq{IDs: []string{"a1"}})
	refusedWith(t, err, sprintfn.CodeDropping)
	_, err = CI(context.Background(), w.env, CIReq{IDs: []string{"a5"}})
	refusedWith(t, err, sprintfn.CodeDropping)
	res, err := Drop(context.Background(), w.env, DropReq{Op: "d2", Streams: []string{"s1"}, Col: sprint.Ready, Reason: "out of scope", Chunk: dropEach})
	mustOK(t, "drop --col ready --op d2", res, err)
	if w.at(sprint.Work, "a3") != "" || w.at(sprint.Work, "a1") != "s1:waiting" || w.at(sprint.Work, "a5") != "s1:review" {
		t.Fatal("--col drops the cell and leaves the others")
	}
	if len(w.dropping()) != 0 {
		t.Fatal("the last part unfreezes")
	}
}

func TestDropLastPartWritesRequestLine(t *testing.T) {
	t.Parallel()
	w := dropWorld(t)
	n := w.trips()
	res, err := Drop(context.Background(), w.env, DropReq{Op: "d3", Streams: []string{"s1", "s2"}, Reason: "out of scope", Chunk: 3 * dropEach})
	mustOK(t, "drop --stream s1,s2", res, err)
	w.droppedAll()
	if w.at(sprint.Work, "b1") != "" {
		t.Fatal("s2's card is dropped too")
	}
	// Three primaries a part, one cell a part: s1's waiting, ready (a2 and a3
	// together), working, review and merging, then s2's review.
	if res.Parts != 6 {
		t.Fatalf("%d parts, want 6", res.Parts)
	}
	wantTrips(t, res, n, 1+res.Parts+1) // the done of the op the verb made, then n + 1
	lines := w.noteLines(typeUnfrozen)
	if len(lines) != 1 {
		t.Fatalf("%d request lines, want 1", len(lines))
	}
	meta := lines[0]["meta"].(map[string]any)
	about := lines[0]["about"].([]any)
	if meta["op"] != sprintfn.JOpRequest || len(about) != 2 || about[0] != sprint.StreamSubject("s1") || about[1] != sprint.StreamSubject("s2") {
		t.Fatalf("the request line names the unfrozen streams: %v", lines[0])
	}
	if seq := lines[0]["seq"].(string); seq != string(res.Step.Reply.LastSeq) {
		t.Fatalf("the request line is the last part's (seq %s, the part's last %s)", seq, res.Step.Reply.LastSeq)
	}
	if len(w.dropping()) != 0 {
		t.Fatal("the last part deletes the marks")
	}
}

func TestDropAbortUnfreezes(t *testing.T) {
	t.Parallel()
	w := dropWorld(t)
	stopAfter(t, w, "d4", "", 3)
	n := w.trips()
	res, err := DropAbort(context.Background(), w.env, DropAbortReq{Op: "d4", Streams: []string{"s1"}})
	mustOK(t, "drop --abort --op d4", res, err)
	wantTrips(t, res, n, 2)
	if len(w.dropping()) != 0 {
		t.Fatalf("the abort deletes the op's marks: %v", w.dropping())
	}
	if !strings.Contains(res.Said, "ready 1") || !strings.Contains(res.Said, "review 1") || !strings.Contains(res.Said, "merging 1") {
		t.Fatalf("the abort names what was left: %q", res.Said)
	}
	if len(w.noteLines(typeUnfrozen)) != 1 {
		t.Fatal("the abort writes the request line")
	}
	// Its receipt is <op>/abort: a repeat writes nothing.
	res, err = DropAbort(context.Background(), w.env, DropAbortReq{Op: "d4", Streams: []string{"s1"}})
	mustOK(t, "drop --abort again", res, err)
	if !res.Replay || !strings.Contains(res.Recorded, "left:") {
		t.Fatalf("a repeat of the abort is its recorded result: %+v", res)
	}
	// The op is over: a resume is refused, and s1 moves again.
	_, err = Drop(context.Background(), w.env, DropReq{Op: "d4", Streams: []string{"s1"}, Reason: "out of scope", Chunk: dropEach})
	refusedWith(t, err, "ABORTED")
	if _, err := CI(context.Background(), w.env, CIReq{IDs: []string{"a5"}, Red: true}); err != nil {
		t.Fatalf("s1 is unfrozen: %v", err)
	}
	// An op that froze nothing has nothing to abort.
	_, err = DropAbort(context.Background(), w.env, DropAbortReq{Op: "d9", Streams: []string{"s1"}})
	refusedWith(t, err, sprintfn.CodeRequest)
}

func TestDropResumeByOp(t *testing.T) {
	t.Parallel()
	w := dropWorld(t)
	stopAfter(t, w, "d5", "", 3) // parts 1 and 2 applied: a1, a2
	n := w.trips()
	res, err := Drop(context.Background(), w.env, DropReq{Op: "d5", Streams: []string{"s1"}, Reason: "out of scope", Chunk: dropEach})
	mustOK(t, "drop --op d5", res, err)
	if res.Resumed != 2 || res.Parts != 4 {
		t.Fatalf("the resume found %d parts applied and ran %d: want 2 and 4 (a3, a4, a5, a6)", res.Resumed, res.Parts)
	}
	wantTrips(t, res, n, 1+res.Parts+1)
	w.droppedAll()
	if w.at(sprint.Work, "b1") != "s2:review" {
		t.Fatal("s2 is not the op's")
	}
	// A repeat of the finished op writes nothing.
	res, err = Drop(context.Background(), w.env, DropReq{Op: "d5", Streams: []string{"s1"}, Reason: "out of scope", Chunk: dropEach})
	mustOK(t, "drop --op d5 again", res, err)
	if !res.Replay {
		t.Fatal("a finished drop replays")
	}
	// The same op with other arguments is refused.
	_, err = Drop(context.Background(), w.env, DropReq{Op: "d5", Streams: []string{"s1"}, Reason: "other", Chunk: dropEach})
	refusedWith(t, err, "OPCONFLICT")

	// Named primaries, with their live cards: n + 1 round trips.
	w.inReview("s2", "b2", 2, "r1")
	n = w.trips()
	res, err = Drop(context.Background(), w.env, DropReq{IDs: []string{"b2", "b1"}, Reason: "superseded"})
	mustOK(t, "drop b1 b2", res, err)
	wantTrips(t, res, n, 2)
	if w.at(sprint.Work, "b1") != "" || w.at(sprint.Work, "b2") != "" || w.at(sprint.Readers, sprint.ReadCardID("b2", 1, "r1")) != "" {
		t.Fatal("the named primaries and their read cards are dropped")
	}
	if w.field(sprint.Work, "b2", "drop_reason") != "superseded" {
		t.Fatal("a dropped primary keeps its reason")
	}
}

// A part that ends a stream decides it on the state at apply (the model's
// PartApply, final on T1): a card that enters the drained cell, or a later
// one, between the part's read and its step refuses it RANGECOUNT, and the
// part planned again drains it.
func TestDropStreamDoneGuarded(t *testing.T) {
	t.Parallel()
	for _, cell := range []string{"s1:ready", "s1:review"} {
		w := newRV(t)
		w.stream("s1", sprint.StreamWaiting)
		w.stream("s2", sprint.StreamWaiting)
		w.card(sprint.Work, "s1:ready", "c2", 2, "kind", "work", "attempt", "0")
		w.card(sprint.Work, "s2:ready", "d1", 1, "kind", "work", "attempt", "0")
		h := &rvHook{before: func(n int) {
			if n == 3 { // part 1's step, after its read
				w.card(sprint.Work, cell, "c7", 7, "kind", "work", "attempt", "0")
			}
		}}
		res, err := Drop(context.Background(), w.hooked(h), DropReq{Op: "g1", Streams: []string{"s1", "s2"}, Chunk: dropEach})
		mustOK(t, "drop --stream s1,s2", res, err)
		if res.Retries != 1 || w.at(sprint.Work, "c7") != "" || w.at(sprint.Work, "c2") != "" || w.at(sprint.Work, "d1") != "" {
			t.Fatalf("%s: part 1 is refused once and planned again, and c7 drops: %d retries, c7 at %q", cell, res.Retries, w.at(sprint.Work, "c7"))
		}
		if len(w.dropping()) != 0 {
			t.Fatalf("%s: the last part unfreezes: %v", cell, w.dropping())
		}
	}
}

// `drop --abort --op <op>` runs as printed: it reads the sprint's streams and
// the marks of each, and unfreezes exactly the op's (the model's AbortApply).
func TestDropAbortByOpAlone(t *testing.T) {
	t.Parallel()
	w := dropWorld(t)
	w.card(sprint.Work, "s2:ready", "b7", 7, "kind", "work", "attempt", "0")
	stopAfter(t, w, "dA", "", 2) // s1 frozen by dA
	if _, err := Drop(context.Background(), w.hooked(&rvHook{kill: 2}), DropReq{Op: "dB", Streams: []string{"s2"}, Chunk: dropEach}); err == nil {
		t.Fatal("want dB cut after its part 1")
	}
	n := w.trips()
	res, err := DropAbort(context.Background(), w.env, DropAbortReq{Op: "dA"})
	mustOK(t, "drop --abort --op dA", res, err)
	wantTrips(t, res, n, 3)
	if m := w.dropping(); m["s1"] != "" || m["s2"] != "dB" {
		t.Fatalf("the abort unfreezes dA's streams and no other: %v", m)
	}
	if lines := w.noteLines(typeUnfrozen); len(lines) != 1 || len(lines[0]["about"].([]any)) != 1 {
		t.Fatalf("the request line names s1 alone: %v", lines)
	}
	// An op that froze nothing has nothing to abort.
	_, err = DropAbort(context.Background(), w.env, DropAbortReq{Op: "d9"})
	refusedWith(t, err, sprintfn.CodeRequest)
}

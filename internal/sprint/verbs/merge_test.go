package verbs

import (
	"context"
	"strconv"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
)

// r is the store's running time now, as the clock's read gives it.
func (w *rv) r() int64 {
	w.t.Helper()
	res, err := sprintfn.Read(context.Background(), w.tw, clockRead("0"))
	if err != nil || res.Refusal != nil || res.Err != nil {
		w.t.Fatalf("clock: %v %v %v", err, res.Refusal, res.Err)
	}
	c, _, err := clockOf(res.Read, 0)
	if err != nil {
		w.t.Fatal(err)
	}
	r, err := strconv.ParseInt(c.R, 10, 64)
	if err != nil {
		w.t.Fatal(err)
	}
	return r
}

func TestMergeFactsStops(t *testing.T) {
	t.Parallel()
	w := newRV(t)
	w.stream("s1", sprint.StreamMerging, "due_mergeidle", "1")
	w.merging("s1", "p1", 1)
	w.merging("s1", "p2", 2)
	w.merging("s1", "p3", 3)

	// A card outside the batch is refused before anything is written.
	_, err := Merge(context.Background(), w.env, MergeReq{Stream: "s1", Batch: 2, Conflict: "p3"})
	refusedWith(t, err, sprintfn.CodeRequest)

	n := w.trips()
	res, err := Merge(context.Background(), w.env, MergeReq{Stream: "s1", Batch: 2, Conflict: "p2", Note: "a conflict in the parser"})
	mustOK(t, "merge --conflict p2", res, err)
	wantTrips(t, res, n, 2)
	ctl := w.rec(sprint.Merge, sprint.CtlID("s1")).Fields
	if ctl["state"].Value != sprint.StreamStopped || ctl["cause"].Value != "conflict" || ctl["card"].Value != "p2" || ctl["due_mergeidle"].Present {
		t.Fatalf("the stream stops on the conflict, its merge-idle deadline unset: %+v", ctl)
	}
	if w.at(sprint.Merge, "p2") != "s1:stuck" || w.at(sprint.Merge, "p1") != "s1:queued" || w.field(sprint.Work, "p2", "stuck") != "1" {
		t.Fatal("the conflicting card is stuck; the rest of the batch stays queued")
	}
	if _, open := w.jopen(sprint.StreamSubject("s1"))[sprint.NConflict+"|conflict"]; !open {
		t.Fatalf("\"stream stopped: conflict on a card\" is not open on the stream: %v", w.jopen(sprint.StreamSubject("s1")))
	}
	// A stopped stream takes no merge step until resume.
	_, err = Merge(context.Background(), w.env, MergeReq{Stream: "s1", Batch: 1})
	refusedWith(t, err, sprintfn.CodeRequest)

	// The other facts stop it too, each with its judgment.
	w2 := newRV(t)
	w2.stream("s2", sprint.StreamMerging)
	w2.stream("s3", sprint.StreamWaiting)
	w2.merging("s2", "q1", 1)
	w2.card(sprint.Work, "s3:review", "o1", 1, "kind", "work", "attempt", "1")
	res, err = Merge(context.Background(), w2.env, MergeReq{Stream: "s2", Cross: "q1=o1"})
	mustOK(t, "merge --cross", res, err)
	if w2.field(sprint.Merge, "q1", "need_card") != "o1" || w2.field(sprint.Merge, sprint.CtlID("s2"), "other") != "o1" {
		t.Fatal("a cross stop names the card it needs")
	}
	if _, open := w2.jopen(sprint.StreamSubject("s2"))[sprint.NCross+"|cross"]; !open {
		t.Fatal("the cross stop's judgment is open")
	}
}

func TestMergeLandingSetsIdle(t *testing.T) {
	t.Parallel()
	w := newRV(t)
	w.stream("s1", sprint.StreamMerging, "due_mergeidle", "1")
	w.merging("s1", "p1", 1)
	w.merging("s1", "p2", 2)
	w.card(sprint.Work, "s1:review", "p3", 3, "kind", "work", "attempt", "1") // open: the stream is not done
	w.stream("s2", sprint.StreamMerging)
	w.merging("s2", "q1", 1)

	r := w.r()
	n := w.trips()
	res, err := Merge(context.Background(), w.env, MergeReq{Stream: "s1", Batch: 2})
	mustOK(t, "merge s1", res, err)
	wantTrips(t, res, n, 2)
	for _, p := range []string{"p1", "p2"} {
		if w.at(sprint.Work, p) != "s1:landed" || w.at(sprint.Merge, p) != "s1:merged" {
			t.Fatalf("%s landed and its merge card merged", p)
		}
	}
	ctl := w.rec(sprint.Merge, sprint.CtlID("s1")).Fields
	if want := strconv.FormatInt(r+IdleSpan.Milliseconds(), 10); ctl["due_idle"].Value != want {
		t.Fatalf("due_idle is %q, want R + IdleSpan = %s", ctl["due_idle"].Value, want)
	}
	if ctl["state"].Value != sprint.StreamWaiting || ctl["due_mergeidle"].Present {
		t.Fatalf("nothing is queued after the batch: the stream waits, with no merge-idle deadline: %+v", ctl)
	}
	if len(w.noteLines(sprint.NBatchLanded)) != 1 || len(w.noteLines(sprint.NStreamLanded)) != 0 {
		t.Fatal("KNOW batch landed, and the stream has not landed")
	}

	// The last open card of s2 lands: the stream has landed.
	res, err = Merge(context.Background(), w.env, MergeReq{Stream: "s2"})
	mustOK(t, "merge s2", res, err)
	if w.field(sprint.Merge, sprint.CtlID("s2"), "state") != sprint.StreamLanded || len(w.noteLines(sprint.NStreamLanded)) != 1 {
		t.Fatal("s2 landed, and said so")
	}
}

func TestResumeSetOfStreams(t *testing.T) {
	t.Parallel()
	w := newRV(t)
	for _, s := range []string{"s1", "s2"} {
		w.stream(s, sprint.StreamMerging)
		w.merging(s, s+"p1", 1)
		w.merging(s, s+"p2", 2)
		if _, err := Merge(context.Background(), w.env, MergeReq{Stream: s, Batch: 1, Conflict: s + "p1"}); err != nil {
			t.Fatal(err)
		}
	}
	w.stream("s3", sprint.StreamWaiting)
	// A set with a stream that is not stopped is refused whole.
	_, err := Resume(context.Background(), w.env, ResumeReq{Streams: []string{"s1", "s3"}, Did: "fixed"})
	refusedWith(t, err, sprintfn.CodeRequest)
	if w.field(sprint.Merge, sprint.CtlID("s1"), "state") != sprint.StreamStopped {
		t.Fatal("nothing of a refused resume is written")
	}

	n := w.trips()
	res, err := Resume(context.Background(), w.env, ResumeReq{Streams: []string{"s2", "s1"}, Did: "resolved the conflicts"})
	mustOK(t, "resume", res, err)
	wantTrips(t, res, n, 2)
	if w.cc.Steps() != 1 {
		t.Fatalf("the set in one step, and %d were sent", w.cc.Steps())
	}
	for _, s := range []string{"s1", "s2"} {
		ctl := w.rec(sprint.Merge, sprint.CtlID(s)).Fields
		if ctl["state"].Value != sprint.StreamMerging || ctl["cause"].Present || ctl["due_mergeidle"].Value == "" || ctl["did"].Value == "" {
			t.Fatalf("%s resumed merging: %+v", s, ctl)
		}
		if w.at(sprint.Merge, s+"p1") != s+":queued" {
			t.Fatalf("%s's stuck card is queued again", s)
		}
		if len(w.jopen(sprint.StreamSubject(s))) != 0 {
			t.Fatalf("%s's stop judgment is closed: %v", s, w.jopen(sprint.StreamSubject(s)))
		}
	}
}

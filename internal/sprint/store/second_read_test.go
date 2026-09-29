package store

// A second cold reader's sequences against the in-memory store, with the
// section 9 check after every step.

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

func (p *probe) noteOf(subject, typ string) string {
	for _, o := range p.openOn(subject) {
		if o.Note.Type == typ {
			return o.Note.ID
		}
	}
	return ""
}

func (p *probe) ctl(stream string) *sprint.Card { return p.snap().StreamCtl(stream) }

// R6b. two broken reads, the coordinator acks both judgments in ONE ack:
// reads are exhausted and no judgment is open.
func TestAckingBothBrokenReadsInOneCallExhaustsTheReads(t *testing.T) {
	t.Parallel()
	p := newProbe(t)
	p.setup(1)
	p.toReview("h", "s1-1")
	p.do("ask", AskStep(sprint.AskReq{Sel: ids("s1-1")}))
	rs := p.snap().Readers.Of("s1-1")
	p.read(rs[0].F("reader"), rs[0].ID, "broken")
	p.read(rs[1].F("reader"), rs[1].ID, "broken")
	var nids []string
	for _, o := range p.openOn("s1-1") {
		nids = append(nids, o.Note.ID)
	}
	if len(nids) != 2 {
		t.Fatalf("open %v", nids)
	}
	p.do("ack both", AckStep(sprint.AckReq{Notes: nids, Reason: "looked"}))
	o := p.openOn("s1-1")
	if len(o) != 1 || o[0].Note.Type != sprint.NReadsExhausted {
		t.Errorf("two broken judgments acked in one call leave s1-1 in review, reads exhausted, with open judgments %v", o)
	}
}

// ok + broken, ci red; ack the broken (ci red still open); ci green
// closes the last judgment: reads exhausted, no judgment open.
func TestCIGreenClosingTheLastJudgmentExhaustsTheReads(t *testing.T) {
	t.Parallel()
	p := newProbe(t)
	p.setup(1)
	p.toReview("h", "s1-1")
	p.do("ask", AskStep(sprint.AskReq{Sel: ids("s1-1")}))
	rs := p.snap().Readers.Of("s1-1")
	p.read(rs[0].F("reader"), rs[0].ID, "ok")
	p.do("ci red", CIStep(sprint.CIReq{Sel: ids("s1-1"), Red: true, Run: "r1"}))
	p.read(rs[1].F("reader"), rs[1].ID, "broken")
	p.do("ack broken", AckStep(sprint.AckReq{Notes: []string{p.noteOf("s1-1", sprint.NReadBroken)}, Reason: "x"}))
	p.do("ci green", CIStep(sprint.CIReq{Sel: ids("s1-1"), Run: "r2"}))
	o := p.openOn("s1-1")
	if len(o) != 1 || o[0].Note.Type != sprint.NReadsExhausted {
		t.Errorf("ci green closed the last judgment of s1-1 (review, one ok, one broken, nothing outstanding) and open is %v", o)
	}
}

// the stream's state and since through a stream's life.
func TestTheStreamStateThroughAStreamsLife(t *testing.T) {
	t.Parallel()
	p := newProbe(t)
	p.setup(3)
	st := func(when, want string) {
		t.Helper()
		c := p.ctl("s1")
		t.Logf("%-34s state=%s since=%s", when, c.F("state"), c.F("since"))
		if c.F("state") != want {
			t.Errorf("after %s the stream is %s, want %s", when, c.F("state"), want)
		}
		p.tick(time.Minute)
	}
	st("add", sprint.StreamWaiting)
	p.through("s1-1")
	st("accept s1-1", sprint.StreamMerging)
	p.do("return s1-1", ReturnStep(sprint.ReturnReq{Sel: ids("s1-1")}))
	st("return s1-1", sprint.StreamWaiting)
	p.do("rework s1-1", ReworkStep(sprint.ReworkReq{Sel: ids("s1-1"), Fix: "f"}))
	st("rework s1-1", sprint.StreamWaiting)
	p.through("s1-2")
	st("accept s1-2", sprint.StreamMerging)
	p.do("merge s1-2", MergeStep(sprint.MergeReq{Stream: "s1"}))
	st("merge s1-2 (lands)", sprint.StreamWaiting)
	// s1-1's fixed work
	c := p.snap().Fleet.Card("s1-1.w2")
	p.do("take", TakeStep(sprint.TakeReq{As: c.Row, Sel: ids(c.ID), Gens: map[string]int{c.ID: 1}}))
	p.do("finish", FinishStep(sprint.FinishReq{As: c.Row, Sel: ids(c.ID), Gens: map[string]int{c.ID: 1}, Head: "h2"}))
	for _, rc := range p.snap().Readers.Of("s1-1") {
		if rc.Col == sprint.Asked {
			p.read(rc.F("reader"), rc.ID, "ok")
		}
	}
	p.do("accept s1-1", AcceptStep(sprint.AcceptReq{Sel: ids("s1-1")}))
	st("accept s1-1 again", sprint.StreamMerging)
	p.do("merge conflict", MergeStep(sprint.MergeReq{Stream: "s1", Conflict: "s1-1"}))
	st("conflict", sprint.StreamStopped)
	p.do("return from stuck", ReturnStep(sprint.ReturnReq{Sel: ids("s1-1")}))
	st("return from stuck (stopped stays)", sprint.StreamStopped)
	p.do("resume", ResumeStep(sprint.ResumeReq{Stream: "s1", Did: "x"}))
	st("resume, nothing queued", sprint.StreamWaiting)
	p.do("drop s1-1", DropStep(sprint.DropReq{Sel: ids("s1-1"), Reason: "x"}))
	st("drop s1-1 (s1-3 still ready)", sprint.StreamWaiting)
	p.do("drop s1-3", DropStep(sprint.DropReq{Sel: ids("s1-3"), Reason: "x"}))
	st("drop the last open primary", sprint.StreamLanded)
	p.must(AddStep(sprint.AddReq{Stream: "s1", Count: 1}))
	st("add to a landed stream", sprint.StreamWaiting)
}

// R1d. return and drop answering the stream-level judgments they are listed
// as decisions for.
func TestReturnAndDropAnswerTheStreamJudgmentsThatListThem(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"red", "rejected", "conflict", "cross"} {
		for _, verb := range []string{"return", "drop"} {
			kind, verb := kind, verb
			t.Run(kind+"-"+verb, func(t *testing.T) {
				p := newProbe(t)
				p.setup(2)
				p.must(AddStep(sprint.AddReq{Stream: "s2", Count: 1}))
				p.through("s1-1", "s1-2")
				p.toReview("h", "s2-1")
				req := sprint.MergeReq{Stream: "s1"}
				switch kind {
				case "red":
					req.Red = true
				case "rejected":
					req.Rejected = true
				case "conflict":
					req.Conflict = "s1-1"
				case "cross":
					req.Cross = "s1-1=s2-1"
				}
				p.do("merge "+kind, MergeStep(req))
				o := p.openOn("stream:s1")
				if len(o) != 1 {
					t.Fatalf("open on stream: %v", o)
				}
				nid := o[0].Note.ID
				t.Logf("decisions of %q: %v", o[0].Note.Type, o[0].Note.Decisions)
				var res Result
				if verb == "return" {
					res = p.do("return --answers", ReturnStep(sprint.ReturnReq{Sel: ids("s1-1"), Answers: []string{nid}}))
				} else {
					res = p.do("drop --answers", DropStep(sprint.DropReq{Sel: ids("s1-1"), Reason: "x", Answers: []string{nid}}))
				}
				listed := false
				for _, d := range o[0].Note.Decisions {
					listed = listed || strings.HasPrefix(d, verb)
				}
				refusedAnswer := false
				for _, r := range res.Refused {
					refusedAnswer = refusedAnswer || r.Key == nid
				}
				t.Logf("%s %s: listed=%v moved=%v refused=%v", verb, kind, listed, res.Moved, res.Refused)
				if listed && refusedAnswer {
					t.Errorf("%s is a listed decision of %q and --answers naming it is refused", verb, o[0].Note.Type)
				}
				if a := p.do("ack stopped", AckStep(sprint.AckReq{Notes: []string{nid}, Reason: "x"})); len(a.Refused) != 1 {
					t.Errorf("ack of a stopped stream's judgment: %+v", a)
				}
				rr := p.do("resume", ResumeStep(sprint.ResumeReq{Stream: "s1", Did: "fixed"}))
				t.Logf("resume: %+v state=%s", rr, p.ctl("s1").F("state"))
			})
		}
	}
}

// reads exhausted: ask --another names it with --answers.
func TestAskAnotherAnswersReadsExhausted(t *testing.T) {
	t.Parallel()
	p := newProbe(t)
	p.setup(1)
	p.toReview("h", "s1-1")
	p.do("ask", AskStep(sprint.AskReq{Sel: ids("s1-1")}))
	rs := p.snap().Readers.Of("s1-1")
	p.read(rs[0].F("reader"), rs[0].ID, "ok")
	p.read(rs[1].F("reader"), rs[1].ID, "broken")
	p.do("ack broken", AckStep(sprint.AckReq{Notes: []string{p.noteOf("s1-1", sprint.NReadBroken)}, Reason: "x"}))
	re := p.noteOf("s1-1", sprint.NReadsExhausted)
	if re == "" {
		t.Fatalf("no reads exhausted")
	}
	r := p.do("ask another --answers RE", AskStep(sprint.AskReq{Sel: ids("s1-1"), Another: true, Answers: []string{re}}))
	if len(r.Moved) != 1 || len(r.Refused) != 0 || p.noteOf("s1-1", sprint.NReadsExhausted) != "" {
		t.Errorf("ask --another --answers <reads exhausted>: moved=%v refused=%v; open after: %v", r.Moved, r.Refused, p.openOn("s1-1"))
	}
}

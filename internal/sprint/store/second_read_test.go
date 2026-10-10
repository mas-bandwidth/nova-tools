package store

// A second cold reader's sequences against the in-memory store, with the
// section 9 check after every step.

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
func TestAckOfBrokenReadsIsRefused(t *testing.T) {
	t.Parallel()
	p := newProbe(t)
	p.setup(1)
	p.toReview("h", "s1-1")
	rs := p.pairAsked("s1-1")
	p.read(rs[0].F("reader"), rs[0].ID, "broken")
	p.read(rs[1].F("reader"), rs[1].ID, "broken")
	var nids []string
	for _, o := range p.openOn("s1-1") {
		nids = append(nids, o.Note.ID)
	}
	// open per card and cause: the second broken read writes no second
	require.Len(t, nids, 1, "open %v", nids)
	r := p.do("ack", AckStep(sprint.AckReq{Notes: nids, Reason: "looked"}))
	if len(r.Moved) != 0 || len(r.Refused) != 1 || !strings.Contains(r.Refused[0].Why, "nova-sprint rework s1-1") {
		assert.Fail(t, fmt.Sprintf("ack of the broken reads: %+v", r))
	}
	o := p.openOn("s1-1")
	assert.Len(t, o, 1, "after the refused ack: %v", o)
}

// ok + broken, ci red; ack the broken (ci red still open); ci green
// closes the last judgment: reads exhausted, no judgment open.
func TestCIGreenLeavesTheBrokenReadOpen(t *testing.T) {
	t.Parallel()
	p := newProbe(t)
	p.setup(1)
	p.toReview("h", "s1-1")
	rs := p.pairAsked("s1-1")
	p.read(rs[0].F("reader"), rs[0].ID, "ok")
	p.do("ci red", CIStep(sprint.CIReq{Sel: ids("s1-1"), Red: true, Run: "r1"}))
	p.read(rs[1].F("reader"), rs[1].ID, "broken")
	r := p.do("ack broken", AckStep(sprint.AckReq{Notes: []string{p.noteOf("s1-1", sprint.NReadBroken)}, Reason: "x"}))
	require.Len(t, r.Refused, 1, "ack of a broken read: %+v", r)
	p.do("ci green", CIStep(sprint.CIReq{Sel: ids("s1-1"), Run: "r2"}))
	o := p.openOn("s1-1")
	if len(o) != 1 || o[0].Note.Type != sprint.NReadBroken {
		assert.Fail(t, fmt.Sprintf("ci green closes ci red and leaves the broken read open; open is %v", o))
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
		assert.Equal(t, want, c.F("state"), "after %s the stream is %s, want %s", when, c.F("state"), want)
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
	p.do("ask again", AskStep(sprint.AskReq{Sel: ids("s1-1")})) // the machine's ask: the finish asks no reader
	p.readAllOK("s1-1")
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
				require.Len(t, o, 1, "open on stream: %v", o)
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
					assert.Fail(t, fmt.Sprintf("%s is a listed decision of %q and --answers naming it is refused", verb, o[0].Note.Type))
				}
				a := p.do("ack stopped", AckStep(sprint.AckReq{Notes: []string{nid}, Reason: "x"}))
				assert.Len(t, a.Refused, 1, "ack of a stopped stream's judgment: %+v", a)
				rr := p.do("resume", ResumeStep(sprint.ResumeReq{Stream: "s1", Did: "fixed"}))
				t.Logf("resume: %+v state=%s", rr, p.ctl("s1").F("state"))
			})
		}
	}
}

// reads exhausted: ask --another names it with --answers.
func TestAskAnotherAnswersABrokenRead(t *testing.T) {
	t.Parallel()
	p := newProbe(t)
	p.setup(1)
	p.toReview("h", "s1-1")
	rs := p.pairAsked("s1-1")
	p.read(rs[0].F("reader"), rs[0].ID, "ok")
	p.read(rs[1].F("reader"), rs[1].ID, "broken")
	br := p.noteOf("s1-1", sprint.NReadBroken)
	r := p.do("ask another --answers broken", AskStep(sprint.AskReq{Sel: ids("s1-1"), Another: true, Answers: []string{br}}))
	if len(r.Moved) != 1 || len(r.Refused) != 0 || p.noteOf("s1-1", sprint.NReadBroken) != "" {
		assert.Fail(t, fmt.Sprintf("ask --another --answers <broken read>: moved=%v refused=%v; open after: %v", r.Moved, r.Refused, p.openOn("s1-1")))
	}
}

// A repair that skipped accept's work entry leaves the merge card queued and
// the primary in review (rule 4 broken, and said so). rework, or return, takes
// the orphan off in the same step, rule 4 holds again, and accept works after.
func TestReworkOrReturnTakesAnOrphanMergeCardOff(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"rework", "return"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			p := newProbe(t)
			p.setup(1)
			p.toReview("h", "s1-1")
			p.do("ask", AskStep(sprint.AskReq{Sel: ids("s1-1")}))
			p.readAllOK("s1-1")
			p.m.Fail = func(pt string) error {
				if pt == "apply t-work before" {
					return errors.New("cut")
				}
				return nil
			}
			_, err := p.st.Run(p.ctx, AcceptStep(sprint.AcceptReq{Sel: ids("s1-1")}))
			require.Error(t, err, "not cut")
			p.m.Fail = nil
			s := p.snap()
			pr := s.Work.Card("s1-1")
			_, err = p.m.Apply(p.ctx, ntable.BatchManifest{Schema: 1, Table: "t-work", Epoch: "0", ExpectedTableRevision: strconv.FormatUint(s.Work.Revision, 10),
				OperationID: "outside", Members: []ntable.BatchMemberEntry{{ID: "s1-1", Expect: &ntable.MemberExpect{Place: &ntable.PlaceExpect{Row: pr.Row, Col: pr.Col}}, Set: map[string]string{"outside": "1"}}}})
			require.NoError(t, err)
			_, err = p.st.Repair(p.ctx)
			require.NoError(t, err)
			rep, _, _ := p.st.Check(p.ctx, 1)
			require.NotEmpty(t, rep.Violations, "the orphan is not reported")
			var res Result
			if verb == "rework" {
				res = p.do(verb, ReworkStep(sprint.ReworkReq{Sel: ids("s1-1"), Fix: "g"}))
			} else {
				res = p.do(verb, ReturnStep(sprint.ReturnReq{Sel: ids("s1-1")}))
			}
			require.Len(t, res.Moved, 1, "%s: %+v, merge card %s", verb, res, p.snap().Merge.Card("s1-1").Col)
			require.Equal(t, string(sprint.Returned), p.snap().Merge.Card("s1-1").Col, "%s: %+v, merge card %s", verb, res, p.snap().Merge.Card("s1-1").Col)
			if verb == "rework" {
				c := p.snap().Fleet.Card("s1-1.w2")
				p.do("take", TakeStep(sprint.TakeReq{As: c.Row, Sel: ids(c.ID), Gens: map[string]int{c.ID: 1}}))
				p.do("finish", FinishStep(sprint.FinishReq{As: c.Row, Sel: ids(c.ID), Gens: map[string]int{c.ID: 1}, Head: "h2"}))
				p.do("ask again", AskStep(sprint.AskReq{Sel: ids("s1-1")})) // the machine's ask: the finish asks no reader
				p.readAllOK("s1-1")
			}
			if res := p.do("accept", AcceptStep(sprint.AcceptReq{Sel: ids("s1-1")})); len(res.Moved) != 1 || p.state("s1-1") != sprint.Merging {
				require.Fail(t, fmt.Sprintf("accept after %s: %+v", verb, res))
			}
		})
	}
}

// The 8 KiB bound covers every text field a card or a control card carries:
// a return reason, a ci note and a resume's did over it refuse the step.
func TestEveryTextFieldIsBounded(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", MaxCardTextBytes+1)
	p := newProbe(t)
	p.setup(2)
	p.through("s1-1", "s1-2")
	over := func(name string, step Step) {
		t.Helper()
		res, err := p.st.Run(p.ctx, step)
		refused := len(res.Refused) > 0 && strings.Contains(res.Refused[0].Why, "over the bound")
		if err != nil || len(res.Moved) != 0 && !refused || !refused {
			assert.Fail(t, fmt.Sprintf("%s over 8 KiB: %+v %v", name, res, err))
		}
	}
	over("return reason", ReturnStep(sprint.ReturnReq{Sel: ids("s1-1"), Reason: long}))
	over("ci note", CIStep(sprint.CIReq{Sel: ids("s1-1"), Red: true, Note: long}))
	p.do("red", MergeStep(sprint.MergeReq{Stream: "s1", Red: true}))
	over("did", ResumeStep(sprint.ResumeReq{Stream: "s1", Did: long}))
	require.Equal(t, string(sprint.StreamStopped), p.ctl("s1").F("state"), "a refused step moved something")
	require.Equal(t, sprint.Merging, p.state("s1-1"), "a refused step moved something")
}

// the lifecycle at run time: an illegal move through Lawful is refused;
// the engine holds a plan that did not go through Lawful to it anyway, creates included.
func TestTheEngineHoldsEveryPlanToTheLifecycle(t *testing.T) {
	t.Parallel()
	p := newProbe(t)
	p.setup(1)
	p.toReview("h", "s1-1")
	illegal := func(s *sprint.Snapshot) sprint.Plan {
		c := s.Work.Card("s1-1")
		e := ntable.BatchMemberEntry{ID: c.ID, Expect: &ntable.MemberExpect{Revision: strconv.FormatUint(c.Rev, 10), Place: &ntable.PlaceExpect{Row: c.Row, Col: c.Col}},
			Move: &ntable.MemberMoveOp{Row: c.Row, Col: sprint.Landed}}
		return sprint.Plan{Units: []sprint.Unit{{Key: c.ID, Stream: c.Row, Changes: []sprint.Change{{Table: sprint.Work, Entry: e}}, Moved: "review -> landed"}}}
	}
	r := p.do("through Lawful", Step{Verb: "mutant", Load: All, Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Lawful(illegal(s)) }})
	if len(r.Moved) != 0 || p.state("s1-1") != sprint.Review {
		assert.Fail(t, fmt.Sprintf("Lawful let review -> landed through: %+v", r))
	}
	r = p.do("not through Lawful", Step{Verb: "mutant", Load: All, Plan: illegal})
	if len(r.Moved) != 0 || len(r.Refused) != 1 || p.state("s1-1") != sprint.Review {
		assert.Fail(t, fmt.Sprintf("the engine applied a plan that skips Lawful: moved=%v refused=%v state=%s", r.Moved, r.Refused, p.state("s1-1")))
	}
	create := func(s *sprint.Snapshot) sprint.Plan {
		e := ntable.BatchMemberEntry{ID: "zz", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "s1", Col: sprint.Merging, Score: 9}}
		return sprint.Plan{Units: []sprint.Unit{{Key: "zz", Stream: "s1", Changes: []sprint.Change{{Table: sprint.Work, Entry: e}}}}}
	}
	if r := p.do("admitted merging", Step{Verb: "mutant", Load: All, Plan: create}); len(r.Moved) != 0 || p.snap().Work.Card("zz") != nil {
		assert.Fail(t, fmt.Sprintf("a primary admitted merging: %+v", r))
	}
}

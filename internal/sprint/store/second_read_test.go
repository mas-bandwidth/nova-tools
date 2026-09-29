package store

// A second cold reader's sequences against the in-memory store, with the
// section 9 check after every step.

import (
	"testing"

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

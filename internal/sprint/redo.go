package sprint

import (
	"fmt"
	"slices"
	"strings"
)

// RedoFixText is the fix every redo applies to the conflicted attempt.
const RedoFixText = "redo the same change on the current tip"

// RedoReq is the request for redo.
type RedoReq struct {
	Sel     Sel
	Answers []string
	Who     string
}

// InConflict says whether a primary card is in a merge conflict:
// it is in Merging, its merge card is in Stuck, and its stream is stopped for conflict on this card.
func InConflict(s *Snapshot, c *Card) bool {
	if c == nil || !c.Placed() || c.Col != Merging {
		return false
	}
	m := s.Merge.Placed(c.ID)
	if m == nil || m.Col != Stuck {
		return false
	}
	ctl := s.StreamCtl(c.Row)
	if ctl == nil || ctl.F("state") != StreamStopped || ctl.F("cause") != "conflict" || ctl.F("card") != c.ID {
		return false
	}
	return true
}

// Redo atomically executes return, rework with fix "redo the same change on the current tip",
// and resumes the stopped stream in one step with one history line.
// Refused when the card is not in a conflict.
func Redo(s *Snapshot, r RedoReq) Plan {
	s, _ = s.withRests()
	var p Plan

	if r.Sel.Stream != "" && len(r.Sel.IDs) == 0 {
		ctl := s.StreamCtl(r.Sel.Stream)
		if ctl == nil {
			p.refuse(r.Sel.Stream, "no such stream")
			return p
		}
		if ctl.F("state") != StreamStopped || ctl.F("cause") != "conflict" {
			p.refuse(r.Sel.Stream, "not in a conflict")
			return p
		}
	}

	chosen := pick(&p, r.Sel, s.Work.Column(Merging), rowOf, func(c *Card) string {
		if r.Sel.Stream != "" && c.Row != r.Sel.Stream {
			return "not in stream " + r.Sel.Stream
		}
		if !InConflict(s, c) {
			return "not in a conflict"
		}
		return ""
	}, s.primaryCard)

	if len(chosen) == 0 {
		return p
	}

	up := s.UpMembers()
	q, room := memberLoads(s, up), memberWidths(s, up)
	rr, ri := dealRound(s), routeIndexesOf(s)
	moves := roundMoves{}
	streams := map[string]bool{}
	chosenMap := map[string]bool{}
	for _, c := range chosen {
		chosenMap[c.ID] = true
	}

	for _, c := range chosen {
		streams[c.Row] = true
		fix := RedoFixText
		broken := 0
		var retire []Change
		if bound := AtRedealBound(s, c); bound != nil {
			retire = append(retire, change(Fleet, removeEntry(bound, map[string]string{"retired": stamp(s.Now), "retired_by": "rework"})))
		}
		for _, rc := range s.Readers.Of(c.ID) {
			if rc.Col == Broken {
				broken++
			}
			retire = append(retire, change(Readers, removeEntry(rc, map[string]string{"retired": stamp(s.Now), "retired_by": "rework"})))
		}
		set := map[string]string{
			"fix":                fix,
			"returns":            itoa(c.Int("returns") + 1),
			"reworks":            itoa(c.Int("reworks") + 1),
			"return_reason":      "conflict",
			FieldReturnedAttempt: itoa(c.Int("attempt")),
			"broken_reads":       itoa(c.Int("broken_reads") + broken),
		}
		unset := []string{"readers"}
		if len(okReaders(s, c)) > 0 && c.F("result") != "failed" {
			set[FieldPassedHead] = c.F("head")
		} else {
			unset = append(unset, FieldPassedHead)
		}
		given := map[string]string{"why": "redo the same change on the current tip"}
		if f := c.F("finding"); f != "" {
			given["finding"] = f
			set["finding"] = f
		}
		m := ""
		_, friend := FriendCard(c)
		if len(up) > 0 && !friend {
			m = rr.next(up, q, room, reworkAvoid(s, c))
		}
		var u Unit
		if m != "" {
			var why string
			u, why = deal(s, c, fix, m, q, ri, set, given, unset...)
			if why != "" {
				p.refuse(c.ID, why)
				continue
			}
			rr.moved(m)
			moves[c.ID] = m
			u.Changes = append(retire, u.Changes...)
			u.Moved = strings.Replace(u.Moved, " merging -> working", " merging -> working (rework)", 1)
		} else {
			later := "no fleet member is up: start delegates it"
			switch {
			case friend:
				later = "a friend's card: the tick deals it to a friend up with room"
			case len(up) > 0:
				later = "no fleet member has room: the tick deals it when one has"
			}
			u = Unit{
				Key: c.ID, Stream: c.Row,
				Changes: append(retire, change(Work, moveEntry(c, c.Row, Ready, set, append(unset, "result")...))),
				Moved:   c.ID + " merging -> ready (rework; " + later + ")",
			}
		}
		mc := s.Merge.Placed(c.ID)
		if mc != nil {
			u.Changes = append(u.Changes, change(Merge, moveEntry(mc, c.Row, Returned, nil, "need_card", "need_stream")))
			u.Moved += "; off merge " + mc.Col
		}
		u.Closes = closesFor(s.Open, append(ReworkResolves, ReturnResolves...), c.ID)
		p.Units = append(p.Units, u)
	}

	sortedStreams := make([]string, 0, len(streams))
	for st := range streams {
		sortedStreams = append(sortedStreams, st)
	}
	slices.Sort(sortedStreams)

	for _, stream := range sortedStreams {
		ctl := s.StreamCtl(stream)
		if ctl == nil {
			continue
		}
		var otherStuck []*Card
		for _, sc := range s.Merge.Cell(stream, Stuck) {
			if !chosenMap[sc.ID] {
				otherStuck = append(otherStuck, sc)
			}
		}
		state := StreamWaiting
		switch {
		case len(otherStuck)+s.Merge.Count(stream, Queued) > 0:
			state = StreamMerging
		case streamDone(s, stream, 0):
			state = StreamLanded
		}
		set := map[string]string{"state": state, "since": stamp(s.Now)}
		u := Unit{
			Key:     ctl.ID,
			Stream:  stream,
			Changes: []Change{change(Merge, setEntry(ctl, set, "cause", "card", "other"))},
			Moved:   fmt.Sprintf("stream %s stopped -> %s", stream, state),
		}
		if len(otherStuck) > 0 {
			u.Moved += fmt.Sprintf("; %d stuck -> queued", len(otherStuck))
		}
		if state == StreamLanded {
			n := happened(NStreamLanded, stream, s.Now)
			n.Who = r.Who
			u.Notes = append(u.Notes, n)
		}
		for _, o := range s.Open {
			if o.Subject() == StreamSubject(stream) {
				u.Closes = append(u.Closes, o)
			}
		}
		for _, sc := range otherStuck {
			u.Changes = append(u.Changes, change(Merge, moveEntry(sc, stream, Queued, nil, "need_card", "need_stream")))
		}
		p.Units = append(p.Units, u)
	}

	answered(&p, s, r.Answers, r.Who)
	p = Lawful(p)
	roundWrites(&p, rr, moves)
	ri.write(&p)
	return p
}

package sprint

import (
	"fmt"
	"slices"
	"strings"
)

// LandRefusedFix is the fix of a card whose head the landing refused (landRefused): why, in
// the lander's words, and what the next attempt does about it.
func LandRefusedFix(why string) string {
	return cutText("the landing refused this head: "+why+"; rebase on the base tip, resolve, make the tree gate pass", MaxCardTextBytes)
}

// LandRefusedFinding begins the finding a landing refusal leaves on its card, the way of it
// after (RefusalWay): two refusals the same way are the same finding, and the brief's bound
// (AtBriefBound) stops the card at the second.
const LandRefusedFinding = "the landing refused its head: "

// landRefused is the merge step's answer to a conflict fact on the card's own head: the
// landing refused the head of pr, a card of the batch queued (m), one of the ways of
// RefusalWay (way). It never stops the stream: the card leaves merge (returned), and the
// stream stays merging while it has cards to land, so the lander lands them in the same pass.
//
// Files outside its PATHS (RefusedPaths) go back to review marked as the conflict rule marks
// them (FieldRuleRedo, FieldRuleRefused, FieldRuleRefusal), under the returned judgment: the
// widen rule twins it wider from its finished head, or the conflict rule's redo reworks it
// (widen.go, rules.go). Any other way is reworked at once: its next attempt waits ready with
// the refusal as its fix, its finished head the base staging carries onto the tip (BaseOf),
// its read cards retired, the way kept as its finding (LandRefusedFinding), and the seat told
// once (NLandRefused, nothing to answer). A card at its brief's bound (AtBriefBound: the same
// refusal twice, or the attempt cap) is not reworked: it goes back to review with the bound's
// judgment (NBriefWrong). state, ctlSet and notes are the merge step's for the stream.
func landRefused(s *Snapshot, r MergeReq, way, state string, ctl *Card, ctlSet map[string]string, notes []Note, pr, m *Card) Unit {
	id, attempt, now := pr.ID, pr.F("attempt"), stamp(s.Now)
	refusal := "the landing refused attempt " + attempt + "'s head: " + orDash(r.Note)
	u := Unit{Key: id, Stream: r.Stream}
	// the stream after the card leaves: merging while it has cards to land, else waiting
	if s.Merge.Count(r.Stream, Queued)+s.Merge.Count(r.Stream, Stuck) == 1 {
		if state == StreamWaiting {
			delete(ctlSet, "state")
			delete(ctlSet, "since")
			notes = nil
		} else {
			ctlSet["state"], ctlSet["since"] = StreamWaiting, now
		}
	}
	// a refusal of the card's own: the base passed its gate, and its count starts again
	u.Changes = append(u.Changes, change(Merge, setEntry(ctl, ctlSet, baseGateCount...)))
	u.Notes = notes
	u.Changes = append(u.Changes, change(Merge, moveEntry(m, r.Stream, Returned, nil, "need_card", "need_stream")))
	set := map[string]string{"returns": itoa(pr.Int("returns") + 1), "return_reason": "conflict", FieldReturnedAttempt: attempt}
	review := func(j Note) Unit {
		u.Changes = append(u.Changes, change(Work, moveEntry(pr, r.Stream, Review, set)))
		u.Notes = append(u.Notes, j)
		u.Closes = closesFor(s.Open, ReturnResolves, id)
		if ra, ok := reviewJudgment(s, inReview(pr, set), reviewStep{closing: noteIDs(u.Closes), writes: u.Notes, who: r.Who}); ok {
			u.Notes = append(u.Notes, ra)
		}
		return u
	}
	if way == RefusedPaths {
		set[FieldRuleRedo], set[FieldRuleRefused], set[FieldRuleRefusal] = attempt, way, cutText(orDash(r.Note), MaxCardTextBytes)
		j := judgment(NReturned, r.Stream, s.Now, pr.Int("returns"), id)
		j.Who, j.Attempt, j.What = r.Who, pr.Int("attempt"), cutText(refusal, MaxCardTextBytes)
		if !acceptable(s, pr) {
			j.Decisions = removeDecision(j.Decisions, "accept")
		}
		u.Moved = fmt.Sprintf("%s merging -> review (the landing refused its head: files outside its PATHS; off merge %s)", id, m.Col)
		return review(j)
	}
	finding := LandRefusedFinding + way
	if bb, ok := AtBriefBound(pr, finding, s.AttemptsCap(pr.Row)); ok {
		j := judgment(NBriefWrong, r.Stream, s.Now, 0, id) // its decisions alone: it is the repeat
		j.Who, j.Card, j.Attempt, j.What = r.Who, id, pr.Int("attempt"), cutText(bb.String()+"; "+refusal, MaxCardTextBytes)
		u.Moved = fmt.Sprintf("%s merging -> review (the landing refused its head at its brief's bound; off merge %s)", id, m.Col)
		return review(j)
	}
	broken := 0
	if s.Readers != nil {
		for _, rc := range s.Readers.Of(id) {
			if rc.Col == Broken {
				broken++
			}
			u.Changes = append(u.Changes, change(Readers, removeEntry(rc, map[string]string{"retired": now, "retired_by": "rework"})))
		}
	}
	reworkPriority(s, pr, set)
	set["fix"], set["why"], set["finding"] = LandRefusedFix(orDash(r.Note)), refusal, finding
	set["reworks"], set["broken_reads"] = itoa(pr.Int("reworks")+1), itoa(pr.Int("broken_reads")+broken)
	set[FieldFindingAttempt] = attempt
	if lines := findingsOf(pr, pr.Int("attempt"), finding+": "+orDash(r.Note)); len(lines) > 0 {
		set[FieldFindings] = findingsLine(lines)
	}
	// it is no passed head (FieldPassedHead): the landing refused it, so an attempt that finds
	// nothing new at it has not answered the fix
	u.Changes = append(u.Changes, change(Work, moveEntry(pr, r.Stream, Ready, set, "readers", "result", FieldFindingReader, FieldPassedHead)))
	n := happened(NLandRefused, r.Stream, s.Now, id)
	n.Who, n.To, n.Card, n.Attempt, n.What = r.Who, s.Coordinator, id, pr.Int("attempt"), cutText(orDash(r.Note), MaxCardTextBytes)
	u.Notes = append(u.Notes, n)
	u.Closes = closesFor(s.Open, append(ReworkResolves, ReturnResolves...), id)
	u.Moved = fmt.Sprintf("%s merging -> ready (the landing refused its head: reworked at the tip; off merge %s)", id, m.Col)
	return u
}

// RedoFixText is the fix every redo applies to the conflicted attempt.
const RedoFixText = "redo the same change on the current tip"

// RedoReq is the request for redo.
type RedoReq struct {
	Sel     Sel
	Answers []string
	Who     string
	// Reason is why a dropped primary comes back, the redo's restore: it is
	// refused without one, and it is written on the card's MOVED line (the
	// drop's own reason is cleared with its marks). The conflict redo reads
	// no reason (docs/SPEC-SPRINT.md section 11, redo).
	Reason string
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

// droppedPrimaries is every primary kept off the table with the outcome
// dropped, the records redo restores (docs/SPEC-SPRINT.md section 11, redo):
// a dropped card's record keeps its stream, brief, needs, tier, priority,
// score and attempt history, and redo puts it back in waiting.
func droppedPrimaries(s *Snapshot) []*Card {
	var out []*Card
	for _, c := range s.Work.Cards() {
		if c.F("outcome") == "dropped" && !IsSentinel(c) {
			out = append(out, c)
		}
	}
	return out
}

// streamHasDropped says the stream holds a dropped primary to restore.
func streamHasDropped(s *Snapshot, stream string) bool {
	for _, c := range droppedPrimaries(s) {
		if c.F("stream") == stream {
			return true
		}
	}
	return false
}

// replacedByTwin is the twin a dropped card was replaced by ("" for none): the
// reason a Replace or the twin verb leaves ("replaced by <twin>" or "twinned as
// <twin>"), which redo refuses to restore (docs/SPEC-SPRINT.md section 2, "A
// card replaced by its twin").
func replacedByTwin(c *Card) string {
	reason := c.F("reason")
	for _, p := range []string{"replaced by ", "twinned as "} {
		if strings.HasPrefix(reason, p) {
			return strings.TrimPrefix(reason, p)
		}
	}
	return ""
}

// redoStreamOf is a card's stream for a redo's selection: the row a placed
// card is on, the stream field a dropped record keeps.
func redoStreamOf(c *Card) string {
	if !c.Placed() {
		return c.F("stream")
	}
	return c.Row
}

// redoRestore is the unit that brings one dropped primary back to waiting at
// its old score: its record is placed again on its stream's waiting cell
// (Plan.Places, the table layer's cell add, when it is off the table) and its
// drop marks are cleared; its brief, needs, tier, priority, score and attempt
// history are kept as the drop left them (docs/SPEC-SPRINT.md section 11,
// redo).
func redoRestore(p *Plan, c *Card, reason string) Unit {
	stream := c.F("stream")
	u := Unit{Key: c.ID, Stream: stream,
		Moved: fmt.Sprintf("%s off the table -> waiting (restored: %s)", c.ID, reason)}
	if c.Placed() {
		// the record was placed again already (the rebuild after its place):
		// clear its drop marks on the placed record
		u.Changes = append(u.Changes, change(Work, setEntry(c, nil, "outcome", "reason", "dropped_from", "dropped_at")))
		return u
	}
	back := *c
	back.Row, back.Col, back.Rev = stream, Waiting, c.Rev+1
	p.Places = append(p.Places, PlaceAgain{Table: Work, Row: stream, Col: Waiting, ID: c.ID, Score: c.Score,
		Said: "a dropped primary comes back: " + c.ID + " is placed again in " + stream})
	u.Changes = append(u.Changes, change(Work, setEntry(&back, nil, "outcome", "reason", "dropped_from", "dropped_at")))
	return u
}

// Redo atomically executes return, rework with fix "redo the same change on the current tip",
// and resumes the stopped stream in one step with one history line.
// Refused when the card is not in a conflict. A dropped primary, named or of
// the stream named, is restored instead: it comes back to waiting at its old
// score with its brief, needs, tier, priority and attempt history kept, one
// MOVED line, refused when --reason is not given or it was replaced by a twin.
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
			// a stream not in a conflict may still hold dropped primaries to
			// restore; with none, the redo is refused as it always was
			if !streamHasDropped(s, r.Sel.Stream) {
				p.refuse(r.Sel.Stream, "not in a conflict")
				return p
			}
		}
		// a dropped primary replaced by a twin is refused naming the twin,
		// never silently skipped by a stream selection
		// (docs/SPEC-SPRINT.md section 2, "A card replaced by its twin")
		for _, c := range droppedPrimaries(s) {
			if c.F("stream") != r.Sel.Stream {
				continue
			}
			if twin := replacedByTwin(c); twin != "" {
				p.refuse(c.ID, "dropped replaced by "+twin+": redo the twin, not the dropped card")
			}
		}
	}

	chosen := pick(&p, r.Sel, append(s.Work.Column(Merging), droppedPrimaries(s)...), redoStreamOf, func(c *Card) string {
		if r.Sel.Stream != "" && redoStreamOf(c) != r.Sel.Stream {
			return "not in stream " + r.Sel.Stream
		}
		if c.F("outcome") == "dropped" {
			if r.Reason == "" {
				return "wants --reason <text> to say why it comes back"
			}
			if twin := replacedByTwin(c); twin != "" {
				return "dropped replaced by " + twin + ": redo the twin, not the dropped card"
			}
			return ""
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
		if c.F("outcome") == "dropped" {
			p.Units = append(p.Units, redoRestore(&p, c, r.Reason))
			continue
		}
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
		c = reworkPriority(s, c, set)
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
		// no route serves its tier and a friend up does: the friends' deal's, never a machine's
		_, _, toFriend, byFriend := s.routeOf(c, nil, nil)
		if len(up) > 0 && !friend && !byFriend {
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
			case byFriend:
				later = toFriend
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

package sprint

import (
	"fmt"
	"slices"
	"strings"
)

// RedoFix is the fix a redo gives the next attempt when the coordinator names
// none: the change was read and passed, and only the tip it merges onto moved.
const RedoFix = "redo the same change on the current tip"

// RedoReq is the coordinator answering a stream stopped on a conflict by
// redoing the card it stopped on (docs/SPEC-SPRINT.md section 7, redo): the
// cards named, or with Stream alone the card that stream stopped on.
type RedoReq struct {
	IDs     []string
	Stream  string
	Fix     string
	Answers []string
	Who     string
}

// RedoResolves is the judgments a redo discharges on its primary: a return's
// and a rework's (ReturnResolves, ReworkResolves). The stream's conflict
// judgment is discharged as resume discharges it: every judgment open on the
// stream.
var RedoResolves = func() []string {
	out := slices.Clone(ReworkResolves)
	for _, t := range ReturnResolves {
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}()

// Redo is return, rework with RedoFix (or the fix given) and resume, the three
// verbs a conflict took, as one step and one unit per card, so the card's
// history has one line (docs/SPEC-SPRINT.md section 7, redo; section 3, the
// lifecycle's merging -> working and merging -> ready rows). Each card must be
// in a conflict: merging, its merge card stuck, its stream stopped with cause
// conflict on it; anything else is refused, naming where it is, and the step
// writes nothing (store.RedoStep is named: all or none). For each card, in one
// unit: its merge card stuck -> returned, as return moves it (a later accept
// moves it back); the primary marked returned at its attempt and its read cards
// retired; the next attempt dealt as rework deals it (the next member round the
// fleet with room, the attempt's own member avoided; ready for the tick's deal
// when none has room or the card is a friend's), the primary merging -> working
// or merging -> ready; and the stream resumed as resume resumes a conflict
// stop: its other stuck cards back to queued, the stream merging when anything
// is queued, else waiting, and every judgment open on it answered.
func Redo(s *Snapshot, r RedoReq) Plan {
	s, _ = s.withRests() // the resting routes, read once (rule 3, route_rest.go)
	var p Plan
	ids := r.IDs
	if len(ids) == 0 {
		if r.Stream == "" {
			p.refuse("-", "wants <card>... or --stream <s>: the card a stream stopped on for a conflict")
			return p
		}
		ctl := s.StreamCtl(r.Stream)
		if ctl == nil {
			p.refuse(r.Stream, "no such stream")
			return p
		}
		if why := conflictWhy(r.Stream, ctl); why != "" {
			p.refuse(r.Stream, why)
			return p
		}
		ids = []string{ctl.F("card")}
	}
	var chosen []*Card
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			p.refuse(id, "named twice")
			continue
		}
		seen[id] = true
		c := s.primaryCard(id)
		if c == nil {
			p.refuse(id, noSuchCard)
			continue
		}
		if why := redoWhy(s, c, r.Stream); why != "" {
			p.refuse(id, why)
			continue
		}
		chosen = append(chosen, c)
	}
	if len(p.Refused) > 0 {
		return p // all or none, as a named step is
	}
	fix := r.Fix
	if fix == "" {
		fix = RedoFix
	}
	up := s.UpMembers()
	q, room := memberLoads(s, up), memberWidths(s, up)
	rr, ri := dealRound(s), routeIndexesOf(s)
	moves := roundMoves{}
	for _, c := range chosen {
		u, m, why := redoUnit(s, c, fix, up, q, room, rr, ri)
		if why != "" {
			p.refuse(c.ID, why)
			continue
		}
		if m != "" {
			rr.moved(m)
			moves[c.ID] = m
		}
		p.Units = append(p.Units, u)
	}
	if len(p.Refused) > 0 {
		return Plan{Refused: p.Refused}
	}
	answered(&p, s, r.Answers, r.Who)
	p = Lawful(p)
	roundWrites(&p, rr, moves)
	ri.write(&p) // a redo's attempt is a card dealt: its tier's route index moves (route.go)
	return p
}

// conflictWhy is why the stream's control card says it is not stopped on a
// conflict on a card; "" when it is.
func conflictWhy(stream string, ctl *Card) string {
	switch {
	case ctl.F("state") != StreamStopped:
		return "stream " + stream + " is not stopped on a conflict (it is " + orDash(ctl.F("state")) + "): redo answers a stream stopped on a conflict on a card"
	case ctl.F("cause") != "conflict":
		return "stream " + stream + " is not stopped on a conflict (it stopped for " + orDash(ctl.F("cause")) + "; nova-sprint inbox names its decisions)"
	case ctl.F("card") == "":
		return "stream " + stream + " stopped on a conflict naming no card"
	}
	return ""
}

// redoWhy is why the primary c is not in a conflict redo may answer; "" when
// it is: merging, its merge card stuck, its stream stopped on a conflict on
// it, and of the stream named when one is.
func redoWhy(s *Snapshot, c *Card, stream string) string {
	if stream != "" && c.Row != stream {
		return "not in a conflict of stream " + stream + " (it is of stream " + orDash(c.Row) + ")"
	}
	if !c.Placed() {
		return "not in a conflict: it is off the table (" + orDash(c.F("outcome")) + ")"
	}
	if c.Col != Merging {
		return "not in a conflict: it is " + c.Col + "; redo answers a stream stopped on a conflict on the card"
	}
	m := s.Merge.Placed(c.ID)
	if m == nil || m.Col != Stuck {
		where := "no merge card"
		if m != nil {
			where = "its merge card is " + m.Col
		}
		return "not in a conflict: " + where + "; redo answers a stream stopped on a conflict on the card"
	}
	ctl := s.StreamCtl(c.Row)
	if ctl == nil {
		return "not in a conflict: its stream " + c.Row + " has no control card"
	}
	if why := conflictWhy(c.Row, ctl); why != "" {
		return "not in a conflict: " + why
	}
	if ctl.F("card") != c.ID {
		return "not in a conflict: stream " + c.Row + " stopped on a conflict on " + ctl.F("card") + ", not on it"
	}
	return ""
}

// redoUnit is the one unit of a redo of the primary c (Redo): the member its
// next attempt is dealt to ("" when it waits ready), or why it is refused.
func redoUnit(s *Snapshot, c *Card, fix string, up []string, q, room map[string]int, rr *round, ri routeIndexes) (Unit, string, string) {
	mc, ctl := s.Merge.Placed(c.ID), s.StreamCtl(c.Row)
	attempt := c.Int("attempt")
	var retire []Change
	broken := 0
	for _, rc := range s.Readers.Of(c.ID) {
		if rc.Col == Broken {
			broken++
		}
		retire = append(retire, change(Readers, removeEntry(rc, map[string]string{"retired": stamp(s.Now), "retired_by": "redo"})))
	}
	// what the next attempt is told: why it exists, and git's words on the conflict
	why := fmt.Sprintf("attempt %d passed its reads and met a conflict merging onto stream %s's base", attempt, c.Row)
	for _, o := range s.Open {
		if o.Subject() == StreamSubject(c.Row) && o.Note.Type == NConflict && o.Note.What != "" {
			why += ": " + o.Note.What
			break
		}
	}
	given := map[string]string{"finding": cutText(brokenFindings(s, c), MaxCardTextBytes), "why": cutText(why, MaxCardTextBytes)}
	// the return's marks (Return) and the rework's (Rework), on the primary
	set := map[string]string{"returns": itoa(c.Int("returns") + 1), FieldReturnedAttempt: itoa(attempt), "return_reason": "conflict",
		"fix": fix, "finding": given["finding"], "why": given["why"],
		"reworks": itoa(c.Int("reworks") + 1), "broken_reads": itoa(c.Int("broken_reads") + broken)}
	unset := []string{"readers"}
	if len(okReaders(s, c)) > 0 && c.F("result") != "failed" {
		set[FieldPassedHead] = c.F("head")
	} else {
		unset = append(unset, FieldPassedHead)
	}
	m := ""
	if _, friend := FriendCard(c); len(up) > 0 && !friend {
		m = rr.next(up, q, room, reworkAvoid(s, c))
	}
	var u Unit
	if m != "" {
		var refused string
		if u, refused = deal(s, c, fix, m, q, ri, set, given, unset...); refused != "" {
			return Unit{}, "", refused
		}
		u.Moved = strings.Replace(u.Moved, fmt.Sprintf("%s work %s -> working", c.ID, Merging), c.ID+" merging -> working (redo: off merge stuck, the same change on the current tip;", 1)
		u.Moved = strings.Replace(u.Moved, " (fleet ready)", ")", 1)
	} else {
		later := "no fleet member is up: start delegates it"
		if _, friend := FriendCard(c); friend {
			later = "a friend's card: the tick deals it to a friend up with room"
		} else if len(up) > 0 {
			later = "no fleet member has room: the tick deals it when one has"
		}
		u = Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, moveEntry(c, c.Row, Ready, set, append(unset, "result")...))},
			Moved: c.ID + " merging -> ready (redo: off merge stuck, the same change on the current tip; " + later + ")"}
	}
	u.Changes = append(append([]Change{change(Merge, moveEntry(mc, c.Row, Returned, nil, "need_card", "need_stream"))}, retire...), u.Changes...)
	u.Moved += fmt.Sprintf("; %d read cards retired", len(retire))

	// the stream resumed as resume resumes a conflict stop (Resume), the card
	// redone off its stuck cell
	var stuck []*Card
	for _, sc := range s.Merge.Cell(c.Row, Stuck) {
		if sc.ID != c.ID {
			stuck = append(stuck, sc)
		}
	}
	state := StreamWaiting
	if len(stuck)+s.Merge.Count(c.Row, Queued) > 0 {
		state = StreamMerging
	}
	u.Changes = append(u.Changes, change(Merge, setEntry(ctl, map[string]string{"state": state, "since": stamp(s.Now), "did": cutText("redo "+c.ID+": "+fix, MaxCardTextBytes)}, "cause", "card", "other")))
	for _, sc := range stuck {
		u.Changes = append(u.Changes, change(Merge, moveEntry(sc, c.Row, Queued, nil, "need_card", "need_stream")))
	}
	u.Moved += fmt.Sprintf("; stream %s stopped -> %s; %d stuck -> queued", c.Row, state, len(stuck))
	u.Closes = closesFor(s.Open, RedoResolves, c.ID)
	for _, o := range s.Open {
		if o.Subject() == StreamSubject(c.Row) && !slices.ContainsFunc(u.Closes, func(x Open) bool { return x.Key == o.Key }) {
			u.Closes = append(u.Closes, o)
		}
	}
	return u, m, ""
}

package sprint

import (
	"fmt"
	"strings"
)

// MergeReq is one mechanical merge step for a stream, given its facts by the
// caller. The step never decides: what merged, what conflicted and the CI
// result are facts.
type MergeReq struct {
	Stream   string
	Batch    int
	Conflict string // a card of the batch that did not merge
	Cross    string // "<card>=<other>": a card that needs a card of another stream first
	Red      bool   // the stream branch went red on the batch
	Rejected bool   // the merge queue rejected the batch
	Note     string
	Who      string
}

// streamDone says every primary of the stream on the table has landed, given
// the ones about to land, and at least one has.
func streamDone(s *Snapshot, stream string, landing int) bool {
	landed := s.Work.Count(stream, Landed) + landing
	open := 0
	for _, st := range []State{Waiting, Ready, Working, Review, Merging} {
		open += s.Work.Count(stream, st)
	}
	return landed > 0 && open == landing
}

// crossRefusal is why a cross fact "<card>=<other>" is refused, "" when it
// holds: the other card is a primary placed on the table, in another stream
// than the card's, and not landed.
func crossRefusal(s *Snapshot, stream, card, other string) string {
	oc := s.Work.Placed(other)
	switch {
	case other == "":
		return "the cross fact names no other card; it wants <card>=<other>"
	case other == card:
		return "the cross fact names the card itself (" + card + "); the other card is in another stream"
	case oc == nil:
		return "the other card " + other + " is not on the table; the other card is placed, in another stream, not landed"
	case oc.Row == stream:
		return "the other card " + other + " is in the same stream " + stream + "; the other card is in another stream"
	case oc.Col == Landed:
		return "the other card " + other + " (stream " + oc.Row + ") has landed already; nothing to wait for"
	}
	return ""
}

// MergeStep merges the head of the stream's queue, in work order, as one
// batch; or, given a fact that stops the stream, stops it and tells the
// coordinator why. A stopped stream moves only after resume.
func MergeStep(s *Snapshot, r MergeReq) Plan { return Lawful(mergeStep(s, r)) }

func mergeStep(s *Snapshot, r MergeReq) Plan {
	var p Plan
	ctl := s.StreamCtl(r.Stream)
	if ctl == nil {
		p.refuse(r.Stream, "no such stream")
		return p
	}
	state := ctl.F("state")
	switch state {
	case StreamStopped:
		p.refuse(r.Stream, "stopped ("+ctl.F("cause")+"); run: nova-sprint resume --stream "+r.Stream)
		return p
	case StreamLanded:
		p.refuse(r.Stream, "landed")
		return p
	}
	// A stuck card is a barrier: the step never passes an earlier stuck card.
	queued := s.Merge.Cell(r.Stream, Queued)
	if stuck := s.Merge.Cell(r.Stream, Stuck); len(stuck) > 0 {
		var before []*Card
		for _, c := range queued {
			if c.Score < stuck[0].Score || (c.Score == stuck[0].Score && c.ID < stuck[0].ID) {
				before = append(before, c)
			}
		}
		queued = before
	}
	now := stamp(s.Now)
	if len(queued) == 0 {
		switch {
		case streamDone(s, r.Stream, 0):
			n := happened(NStreamLanded, r.Stream, s.Now)
			n.Who = r.Who
			p.Units = append(p.Units, Unit{Key: ctl.ID, Stream: r.Stream, Changes: []Change{change(Merge, setEntry(ctl, map[string]string{"state": StreamLanded, "since": now}))},
				Notes: []Note{n}, Moved: "stream " + r.Stream + " " + state + " -> landed"})
		case state == StreamMerging:
			p.Units = append(p.Units, Unit{Key: ctl.ID, Stream: r.Stream, Changes: []Change{change(Merge, setEntry(ctl, map[string]string{"state": StreamWaiting, "since": now}))},
				Moved: "stream " + r.Stream + " merging -> waiting (nothing queued)"})
		}
		return p
	}
	n := r.Batch
	if n <= 0 || n > len(queued) {
		n = len(queued)
	}
	batch := queued[:n]
	var ids []string
	for _, c := range batch {
		ids = append(ids, c.ID)
	}
	ctlSet := map[string]string{}
	var notes []Note
	if state == StreamWaiting {
		ctlSet["state"], ctlSet["since"] = StreamMerging, now
		m := happened(NStartedMerging, r.Stream, s.Now)
		m.Who = r.Who
		notes = append(notes, m)
	}
	stop := func(cause, typ string, primaries []string, before int, unset ...string) Unit {
		ctlSet["state"], ctlSet["since"], ctlSet["cause"] = StreamStopped, now, cause
		j := judgment(typ, r.Stream, s.Now, before, primaries...)
		j.StreamLevel, j.Who, j.What = true, r.Who, r.Note
		return Unit{Key: ctl.ID, Stream: r.Stream, Changes: []Change{change(Merge, setEntry(ctl, ctlSet, unset...))}, Notes: append(notes, j)}
	}
	switch {
	case r.Conflict != "":
		m := s.Merge.Placed(r.Conflict)
		if m == nil || m.Row != r.Stream || m.Col != Queued {
			p.refuse(r.Conflict, "not queued in stream "+r.Stream)
			return p
		}
		pr := s.Work.Placed(r.Conflict)
		ctlSet["card"] = r.Conflict
		u := stop("conflict", NConflict, []string{r.Conflict}, pr.Int("stuck"), "other")
		u.Changes = append(u.Changes, change(Merge, moveEntry(m, r.Stream, Stuck, nil)))
		if pr != nil {
			u.Changes = append(u.Changes, change(Work, setEntry(pr, map[string]string{"stuck": itoa(pr.Int("stuck") + 1)})))
		}
		u.Moved = fmt.Sprintf("stream %s stopped: %s queued -> stuck (conflict)", r.Stream, r.Conflict)
		p.Units = append(p.Units, u)
	case r.Cross != "":
		card, other, ok := strings.Cut(r.Cross, "=")
		m := s.Merge.Placed(card)
		if !ok || m == nil || m.Row != r.Stream || m.Col != Queued {
			p.refuse(card, "the cross fact wants <card>=<other> with the card queued in stream "+r.Stream)
			return p
		}
		if why := crossRefusal(s, r.Stream, card, other); why != "" {
			p.refuse(card, why)
			return p
		}
		otherStream := s.Work.Placed(other).Row
		ctlSet["card"], ctlSet["other"] = card, other
		u := stop("cross", NCross, []string{card, other}, 0)
		u.Notes[len(u.Notes)-1].What = fmt.Sprintf("%s (stream %s) needs %s (stream %s) landed first", card, r.Stream, other, orDash(otherStream))
		u.Changes = append(u.Changes, change(Merge, moveEntry(m, r.Stream, Stuck, map[string]string{"need_card": other, "need_stream": otherStream})))
		u.Moved = fmt.Sprintf("stream %s stopped: %s queued -> stuck, needs %s (stream %s) landed first", r.Stream, card, other, orDash(otherStream))
		p.Units = append(p.Units, u)
	case r.Red:
		ctlSet["ci"] = "red"
		u := stop("red", NRed, ids, 0, "card", "other")
		u.Notes[len(u.Notes)-1].What = "suspects: the batch of " + itoa(len(ids))
		for _, c := range batch {
			if pr := s.Work.Placed(c.ID); pr != nil {
				u.Changes = append(u.Changes, change(Work, setEntry(pr, map[string]string{"ci": "red", "ci_at": now})))
			}
		}
		u.Moved = fmt.Sprintf("stream %s stopped: branch red on a batch of %d", r.Stream, len(ids))
		p.Units = append(p.Units, u)
	case r.Rejected:
		u := stop("rejected", NRejected, ids, 0, "other", "card")
		u.Moved = fmt.Sprintf("stream %s stopped: the merge queue rejected a batch of %d", r.Stream, len(ids))
		p.Units = append(p.Units, u)
	default:
		// A card queued in merge but not merging in work is refused; the
		// stream's control change and its notes ride on the first card that
		// lands, and the batch note lists only the cards that landed.
		var landing []*Card
		var landed []string
		for _, c := range batch {
			if pr := s.Work.Placed(c.ID); pr == nil || pr.Col != Merging {
				p.refuse(c.ID, "queued in merge but not merging in work ("+placeWord(orEmpty(pr, c.ID))+"); run: nova-sprint check")
				continue
			}
			landing = append(landing, c)
			landed = append(landed, c.ID)
		}
		if len(landing) == 0 {
			return p
		}
		ctlSet["ci"], ctlSet["moved"] = "green", now
		if streamDone(s, r.Stream, len(landing)) {
			ctlSet["state"], ctlSet["since"] = StreamLanded, now
		}
		for i, c := range landing {
			u := Unit{Key: c.ID, Stream: r.Stream}
			if i == 0 {
				u.Changes = append(u.Changes, change(Merge, setEntry(ctl, ctlSet)))
				u.Notes = notes
			}
			u.Changes = append(u.Changes, change(Merge, moveEntry(c, r.Stream, Merged, map[string]string{"merged": now})))
			u.Changes = append(u.Changes, change(Work, moveEntry(s.Work.Placed(c.ID), r.Stream, Landed, map[string]string{"ci": "green", "landed": now})))
			u.Moved = c.ID + " merging -> landed"
			p.Units = append(p.Units, u)
		}
		last := &p.Units[len(p.Units)-1]
		b := happened(NBatchLanded, r.Stream, s.Now, landed...)
		b.Who, b.What = r.Who, "ci green"
		last.Notes = append(last.Notes, b)
		if ctlSet["state"] == StreamLanded {
			l := happened(NStreamLanded, r.Stream, s.Now)
			l.Who = r.Who
			last.Notes = append(last.Notes, l)
		}
	}
	return p
}

// ResumeReq moves a stopped stream again.
type ResumeReq struct {
	Stream  string
	Did     string // what the coordinator did
	Answers []string
	Who     string
}

// Resume moves a stopped stream's stuck cards whose cause is resolved back to
// queued at their unchanged scores and the stream to merging (waiting when
// nothing is queued). It is refused while a cause is unresolved, naming it: a
// stuck card that needs a card of another stream waits until that card has
// landed (ranking it is not landing it). The other causes (a conflict, a red
// branch, a rejected batch) are resolved by the coordinator, who says what
// was done; after a red branch saying it is required. It answers every
// judgment open on the stream.
func Resume(s *Snapshot, r ResumeReq) Plan {
	var p Plan
	ctl := s.StreamCtl(r.Stream)
	if ctl == nil {
		p.refuse(r.Stream, "no such stream")
		return p
	}
	if ctl.F("state") != StreamStopped {
		p.refuse(r.Stream, "not stopped (it is "+orDash(ctl.F("state"))+")")
		return p
	}
	if ctl.F("cause") == "red" && strings.TrimSpace(r.Did) == "" {
		p.refuse(r.Stream, "stopped for a red branch; say what was done: nova-sprint resume --stream "+r.Stream+" --did <text>")
		return p
	}
	stuck := s.Merge.Cell(r.Stream, Stuck)
	for _, c := range stuck {
		if need := c.F("need_card"); need != "" && s.StateOf(need) != Landed {
			p.refuse(r.Stream, fmt.Sprintf("unresolved: %s needs %s (stream %s) landed first, and it is %s", c.ID, need, orDash(c.F("need_stream")), orDash(s.StateOf(need))))
			return p
		}
	}
	state := StreamWaiting
	if len(stuck)+s.Merge.Count(r.Stream, Queued) > 0 {
		state = StreamMerging
	}
	set := map[string]string{"state": state, "since": stamp(s.Now)}
	if r.Did != "" {
		set["did"] = r.Did
	}
	u := Unit{Key: ctl.ID, Stream: r.Stream, Changes: []Change{change(Merge, setEntry(ctl, set, "cause", "card", "other"))},
		Moved: fmt.Sprintf("stream %s stopped -> %s; %d stuck -> queued", r.Stream, state, len(stuck))}
	for _, o := range s.Open {
		if o.Subject() == StreamSubject(r.Stream) {
			u.Closes = append(u.Closes, o)
		}
	}
	for _, c := range stuck {
		u.Changes = append(u.Changes, change(Merge, moveEntry(c, r.Stream, Queued, nil, "need_card", "need_stream")))
	}
	p.Units = append(p.Units, u)
	answered(&p, s.Open, r.Answers)
	return p
}

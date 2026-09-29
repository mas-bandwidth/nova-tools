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

// MergeStep merges the head of the stream's queue, in work order, as one
// batch; or, given a fact that stops the stream, stops it and tells the
// coordinator why. A stopped stream moves only after resume.
func MergeStep(s *Snapshot, r MergeReq) Plan {
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
	queued := s.Merge.Cell(r.Stream, Queued)
	now := stamp(s.Now)
	if len(queued) == 0 {
		switch {
		case streamDone(s, r.Stream, 0):
			n := happened(NStreamLanded, r.Stream, s.Now)
			n.Who = r.Who
			p.Units = append(p.Units, Unit{Key: ctl.ID, Changes: []Change{change(Merge, setEntry(ctl, map[string]string{"state": StreamLanded, "since": now}))},
				Notes: []Note{n}, Moved: "stream " + r.Stream + " " + state + " -> landed"})
		case state == StreamMerging:
			p.Units = append(p.Units, Unit{Key: ctl.ID, Changes: []Change{change(Merge, setEntry(ctl, map[string]string{"state": StreamWaiting, "since": now}))},
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
		return Unit{Key: ctl.ID, Changes: []Change{change(Merge, setEntry(ctl, ctlSet, unset...))}, Notes: append(notes, j)}
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
		if m := s.Merge.Placed(card); !ok || m == nil || m.Row != r.Stream || m.Col != Queued {
			p.refuse(card, "the cross fact wants <card>=<other> with the card queued in stream "+r.Stream)
			return p
		}
		ctlSet["card"], ctlSet["other"] = card, other
		u := stop("cross", NCross, []string{card, other}, 0)
		u.Notes[len(u.Notes)-1].What = card + " needs " + other + " first"
		u.Moved = fmt.Sprintf("stream %s stopped: %s needs %s first", r.Stream, card, other)
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
		ctlSet["ci"], ctlSet["moved"] = "green", now
		if streamDone(s, r.Stream, len(batch)) {
			ctlSet["state"], ctlSet["since"] = StreamLanded, now
		}
		for i, c := range batch {
			u := Unit{Key: c.ID}
			if i == 0 {
				u.Changes = append(u.Changes, change(Merge, setEntry(ctl, ctlSet)))
				u.Notes = notes
			}
			u.Changes = append(u.Changes, change(Merge, moveEntry(c, r.Stream, Merged, map[string]string{"merged": now})))
			if pr := s.Work.Placed(c.ID); pr != nil && pr.Col == Merging {
				u.Changes = append(u.Changes, change(Work, moveEntry(pr, r.Stream, Landed, map[string]string{"ci": "green", "landed": now})))
			} else {
				p.refuse(c.ID, "queued in merge but not merging in work ("+placeWord(orEmpty(pr, c.ID))+"); run: nova-sprint check")
				continue
			}
			u.Moved = c.ID + " merging -> landed"
			p.Units = append(p.Units, u)
		}
		if len(p.Units) > 0 {
			last := &p.Units[len(p.Units)-1]
			b := happened(NBatchLanded, r.Stream, s.Now, ids...)
			b.Who, b.What = r.Who, "ci green"
			last.Notes = append(last.Notes, b)
			if ctlSet["state"] == StreamLanded {
				l := happened(NStreamLanded, r.Stream, s.Now)
				l.Who = r.Who
				last.Notes = append(last.Notes, l)
			}
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

// Resume moves a stopped stream's stuck cards back to queued, in their score
// order, and the stream to merging (waiting when nothing is queued). It
// answers every judgment open on the stream.
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
	stuck := s.Merge.Cell(r.Stream, Stuck)
	state := StreamWaiting
	if len(stuck)+s.Merge.Count(r.Stream, Queued) > 0 {
		state = StreamMerging
	}
	set := map[string]string{"state": state, "since": stamp(s.Now)}
	if r.Did != "" {
		set["did"] = r.Did
	}
	u := Unit{Key: ctl.ID, Changes: []Change{change(Merge, setEntry(ctl, set, "cause", "card", "other"))},
		Closes: closesFor(s.Open, StreamSubject(r.Stream)),
		Moved:  fmt.Sprintf("stream %s stopped -> %s; %d stuck -> queued", r.Stream, state, len(stuck))}
	for _, c := range stuck {
		u.Changes = append(u.Changes, change(Merge, moveEntry(c, r.Stream, Queued, nil)))
	}
	p.Units = append(p.Units, u)
	p.Closes = answering(s.Open, r.Answers)
	return p
}

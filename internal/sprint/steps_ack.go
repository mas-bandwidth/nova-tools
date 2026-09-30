package sprint

import (
	"fmt"
	"strings"
	"time"
)

// AckReq is the coordinator saying it looked at judgments and nothing is to
// be done.
type AckReq struct {
	Notes  []string
	Reason string
	Who    string
}

// Ack closes the named judgments, every subject of each, and records the
// reason as their answer. It answers only a judgment whose own decisions list
// ack (information to be seen); any other is refused, with its decisions as
// commands. It is refused for the judgment of a stream that is stopped: that
// judgment stays open until the stream resumes. It is refused when it would
// leave a primary held by nobody. A primary in
// review whose last open judgment it closes, and that nothing then moves
// (reads exhausted, or stranded in review), is a judgment of its own.
func Ack(s *Snapshot, r AckReq) Plan {
	var p Plan
	p.on(s)
	if why := notCoordinator(s, r.Who, "ack"); why != "" {
		for _, id := range r.Notes {
			p.refuse(id, why)
		}
		return p
	}
	closing := map[string]bool{} // every note id this call closes
	named := map[string]bool{}
	for _, id := range r.Notes {
		if named[id] {
			p.refuse(id, "named twice")
			continue
		}
		named[id] = true
		var entries []Open
		for _, o := range s.Open {
			if o.Note.ID == id {
				entries = append(entries, o)
			}
		}
		if len(entries) == 0 {
			p.refuse(id, noJudgment(s, id))
			continue
		}
		n := entries[0].Note
		if !contains(n.Decisions, "ack") {
			p.refuse(id, notAckable(n, entries))
			continue
		}
		if n.StreamLevel && s.StreamCtl(n.Stream).F("state") == StreamStopped {
			p.refuse(id, "stream "+n.Stream+" is stopped: its judgment stays open until it resumes; run: nova-sprint resume --stream "+n.Stream+" --did <what was done>")
			continue
		}
		closing[id] = true
		u := Unit{Key: id, Stream: n.Stream, Closes: entries, Moved: fmt.Sprintf("%s (%s) acknowledged: %s", id, n.Type, r.Reason)}
		u.Notes = append(u.Notes, decided(entries[0], "ack: "+r.Reason, r.Who, s.Now))
		if TickKept(n.Type) {
			// The tick's condition may still hold: the acknowledgement is
			// kept on it, so the tick does not write it again until it has
			// cleared and come back.
			u.Notes = append(u.Notes, acknowledged(n, entries, r.Who, s.Now))
		}
		p.Units = append(p.Units, u)
	}
	// An ack cannot silence a card: one that would leave a primary held by
	// nobody (no outside actor, no move the tick would make, no other open
	// judgment) is refused, with the decisions that do move it as commands.
	acked := map[string][]string{}
	for _, u := range p.Units {
		for _, o := range u.Closes {
			acked[o.Subject()] = append(acked[o.Subject()], o.Note.Type)
		}
	}
	silenced := map[string]string{} // note id -> the primary it would leave held by nobody
	for _, u := range p.Units {
		for _, o := range u.Closes {
			if pr := s.Work.Placed(o.Subject()); pr != nil && silenced[o.Note.ID] == "" && !heldAfterAck(s, pr, closing, acked[pr.ID]) {
				silenced[o.Note.ID] = pr.ID
			}
		}
	}
	if len(silenced) > 0 {
		kept := p.Units[:0]
		for _, u := range p.Units {
			if pr := silenced[u.Key]; pr != "" {
				p.refuse(u.Key, silenceRefusal(s, u, pr))
				delete(closing, u.Key)
				continue
			}
			kept = append(kept, u)
		}
		p.Units = kept
		acked = map[string][]string{}
		for _, u := range p.Units {
			for _, o := range u.Closes {
				acked[o.Subject()] = append(acked[o.Subject()], o.Note.Type)
			}
		}
	}
	waivers := map[string][]Note{}
	for _, u := range p.Units {
		for _, o := range u.Closes {
			if o.Note.Type == NBlocked || o.Note.Type == NMissingNeed {
				waivers[o.Subject()] = append(waivers[o.Subject()], o.Note)
			}
		}
	}
	// Several judgments can name the same primary. Apply their acknowledged
	// needs together, once, while retaining each judgment's own answer.
	waived := map[string]bool{}
	for i := range p.Units {
		u := &p.Units[i]
		for _, o := range u.Closes {
			id := o.Subject()
			if waived[id] || len(waivers[id]) == 0 {
				continue
			}
			waived[id] = true
			if c, notes := waive(s, id, r.Who, waivers[id]); c.Entry.ID != "" {
				u.Changes = append(u.Changes, c)
				u.Notes = append(u.Notes, notes...)
				u.Moved += "; " + id + " waives " + c.Entry.Set["waived"]
				if c.Entry.Move != nil {
					u.Moved += " and is ready"
				}
			}
		}
	}

	written := map[string]bool{}
	for i := range p.Units {
		for _, o := range p.Units[i].Closes {
			pr := s.Work.Placed(o.Subject())
			if pr == nil || written[pr.ID] {
				continue
			}
			written[pr.ID] = true
			if j, ok := reviewJudgment(s, pr, reviewStep{closing: closing, acked: acked[pr.ID], who: r.Who}); ok {
				p.Units[i].Notes = append(p.Units[i].Notes, j)
			}
		}
	}
	return p
}

// waive is the change that records, on a waiting primary, that the
// coordinator acknowledged its dropped or missing needs: the union named by
// these judgments (every need of its kind for a legacy judgment naming none),
// by whom and when, and count as satisfied. A need dropped after the judgment
// was written has its own. A primary with nothing else to wait for moves to
// ready in the same change; a sentinel is reached instead.
func waive(s *Snapshot, id, who string, judgments []Note) (Change, []Note) {
	c := s.Work.Placed(id)
	if c == nil || c.Col != Waiting {
		return Change{}, nil
	}
	var gone []string
	waits := WaitsFor(s, c, nil)
	for _, n := range judgments {
		needs := droppedNeeds(s, waits)
		if n.Type == NMissingNeed {
			needs = missingNeeds(s, waits)
		}
		for _, need := range needs {
			if (len(n.Needs) == 0 || contains(n.Needs, need)) && !contains(gone, need) {
				gone = append(gone, need)
			}
		}
	}
	if len(gone) == 0 {
		return Change{}, nil
	}
	set := map[string]string{"waived": strings.Join(append(Split(c.F("waived")), gone...), ","), "waived_by": who, "waived_at": stamp(s.Now)}
	fields := map[string]string{}
	for k, v := range c.Fields {
		fields[k] = v
	}
	fields["waived"] = set["waived"]
	after := &Card{ID: c.ID, Row: c.Row, Col: c.Col, Score: c.Score, Fields: fields}
	switch {
	case len(WaitsFor(s, after, nil)) > 0:
	case IsSentinel(c):
		set["reached"] = stamp(s.Now)
		return change(Work, setEntry(c, set)), []Note{reachedNote(s, c, nil, 0, who)}
	default:
		return change(Work, moveEntry(c, c.Row, Ready, set)), nil
	}
	return change(Work, setEntry(c, set)), nil
}

// blockedNote is the judgment that a waiting primary needs primaries dropped
// off the table (gone): it names them, and acknowledging it waives those.
func blockedNote(s *Snapshot, stream, id, who string, gone []string) Note {
	n := judgment(NBlocked, stream, s.Now, 0, id)
	n.What, n.Who, n.Needs = id+" needs "+Preview(gone, ",")+", dropped", who, gone
	return n
}

// unblocked is the needs (gone) of a waiting primary that no open judgment
// of this type names: a need dropped after the judgment was written is
// its own judgment. A blocked judgment that names none names every one.
func unblocked(open []Open, id string, gone []string, typ string) []string {
	named := map[string]bool{}
	for _, o := range open {
		if o.Note.Type != typ || o.Subject() != id {
			continue
		}
		if len(o.Note.Needs) == 0 {
			return nil
		}
		for _, n := range o.Note.Needs {
			named[n] = true
		}
	}
	var out []string
	for _, g := range gone {
		if !named[g] {
			out = append(out, g)
		}
	}
	return out
}

// strandedNote is the judgment of a stranded primary: failed work is not
// read, so asking is not a decision for it.
func strandedNote(s *Snapshot, pr *Card, typ, why, who string) Note {
	j := judgment(typ, pr.Row, s.Now, 0, pr.ID)
	j.Who, j.Attempt, j.What = who, pr.Int("attempt"), why
	if pr.F("result") == "failed" {
		j.Decisions = removeDecision(j.Decisions, "ask")
	}
	return j
}

// acknowledged is the record of an acknowledged tick judgment on the subjects
// it closes: the judgment's type, stream and what, so the tick's condition
// finds it.
func acknowledged(n Note, entries []Open, who string, now time.Time) Note {
	a := Note{Kind: Acknowledged, Type: n.Type, Stream: n.Stream, What: n.What, StreamLevel: n.StreamLevel, Who: who, At: now, Card: n.Card}
	if !n.StreamLevel {
		for _, o := range entries {
			a.Primaries = append(a.Primaries, o.Subject())
		}
		a.Count = len(a.Primaries)
	}
	return a
}

// heldAfterAck says a primary is still held after an ack closes the
// judgments in closing: the no-stall rule (Unheld, check's rule 12, the one
// definition of held) judged on the state after the ack, the judgments it
// closes gone and the one the primary would need next open.
func heldAfterAck(s *Snapshot, pr *Card, closing map[string]bool, acked []string) bool {
	after := *s
	after.Open = nil
	for _, o := range s.Open {
		if !closing[o.Note.ID] {
			after.Open = append(after.Open, o)
		}
	}
	if j, ok := reviewJudgment(s, pr, reviewStep{closing: closing, acked: acked}); ok {
		j.ID = "after-ack"
		for _, sub := range j.Subjects() {
			after.Open = append(after.Open, Open{Key: OpenKey(j.ID, sub), Note: j})
		}
	}
	for _, f := range Unheld(HeldState{Snap: &after, Running: true}, s.Now) {
		if f.Subject == pr.ID {
			return false
		}
	}
	return true
}

// silenceRefusal is why an ack is refused: the primary it would leave held by
// nobody, and the judgment's other decisions as the commands that make them.
func silenceRefusal(s *Snapshot, u Unit, pr string) string {
	n := u.Closes[0].Note
	var members []string
	for _, o := range u.Closes {
		members = append(members, o.Subject())
	}
	g := Group{ID: n.ID, Kind: Judgment, Type: n.Type, Stream: n.Stream, Size: len(members), Notes: []string{n.ID},
		Members: members, Decisions: removeDecision(n.Decisions, "ack")}
	var lines []string
	for _, c := range commands(g, n, "") {
		lines = append(lines, c.Decision+": "+strings.Join(c.Lines, " && "))
	}
	why := fmt.Sprintf("%s is the last judgment on %s, which would then be held by nobody (no read outstanding, nothing the tick would do, no other judgment open on it); decide instead", n.ID, pr)
	if len(lines) == 0 {
		return why + ": rework, return or drop it"
	}
	return why + ": " + strings.Join(lines, "; ")
}

// TickKept says the tick keeps the judgments of this type: it writes one while
// its condition holds and closes it when the condition clears.
func TickKept(typ string) bool {
	_, ok := TickDecisions[typ]
	return ok || typ == NRemindFailed
}

// notAckable is why ack is refused for a judgment whose decisions do not list
// it: the decisions that answer it, as commands.
func notAckable(n Note, entries []Open) string {
	var members []string
	for _, o := range entries {
		members = append(members, o.Subject())
	}
	g := Group{ID: n.ID, Kind: Judgment, Type: n.Type, Stream: n.Stream, Size: len(members), Notes: []string{n.ID}, Members: members, Decisions: n.Decisions}
	var lines []string
	for _, c := range commands(g, n, "") {
		lines = append(lines, c.Decision+": "+strings.Join(c.Lines, " && "))
	}
	why := fmt.Sprintf("ack does not answer %s (%s): its decisions are", n.ID, n.Type)
	if TickKept(n.Type) {
		why = fmt.Sprintf("ack does not answer %s (%s), a condition the tick keeps; wait sets when it is shown again: its decisions are", n.ID, n.Type)
	}
	return why + " " + strings.Join(lines, "; ")
}

// WaitReq holds a condition the tick keeps until a time: the judgment is
// closed and a hold kept on its condition; when the time has passed in
// running time and the condition still holds, the tick raises it again.
type WaitReq struct {
	Note  string
	Until time.Time
	Who   string
}

// Wait is the step of WaitReq; it is refused for a judgment the tick does not
// keep (its review time is set instead) and for one not open.
func Wait(s *Snapshot, r WaitReq) Plan {
	var p Plan
	if why := notCoordinator(s, r.Who, "wait"); why != "" {
		p.refuse(r.Note, why)
		return p
	}
	var entries []Open
	for _, o := range s.Open {
		if o.Note.ID == r.Note {
			entries = append(entries, o)
		}
	}
	if len(entries) == 0 {
		p.refuse(r.Note, noJudgment(s, r.Note))
		return p
	}
	n := entries[0].Note
	if !TickKept(n.Type) {
		p.refuse(r.Note, n.Type+" is not a condition the tick keeps: its review time is set instead")
		return p
	}
	hold := acknowledged(n, entries, r.Who, s.Now)
	hold.Review = r.Until
	u := Unit{Key: r.Note, Stream: n.Stream, Closes: entries, Notes: []Note{decided(entries[0], "wait until "+r.Until.UTC().Format(time.RFC3339), r.Who, s.Now), hold},
		Moved: fmt.Sprintf("%s (%s) held until %s of running time", r.Note, n.Type, r.Until.UTC().Format(time.RFC3339))}
	p.Units = append(p.Units, u)
	return p
}

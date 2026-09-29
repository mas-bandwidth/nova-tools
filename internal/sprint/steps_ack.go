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
// reason as their answer. It is refused for the judgment of a stream that is
// stopped: that judgment stays open until the stream resumes. A primary in
// review whose last open judgment it closes, and that nothing then moves
// (reads exhausted, or stranded in review), is a judgment of its own.
func Ack(s *Snapshot, r AckReq) Plan {
	var p Plan
	p.on(s)
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
		if n.StreamLevel && s.StreamCtl(n.Stream).F("state") == StreamStopped {
			p.refuse(id, "stream "+n.Stream+" is stopped: its judgment stays open until it resumes; run: nova-sprint resume --stream "+n.Stream+" --did <what was done>")
			continue
		}
		closing[id] = true
		u := Unit{Key: id, Stream: n.Stream, Closes: entries, Moved: fmt.Sprintf("%s (%s) acknowledged: %s", id, n.Type, r.Reason)}
		u.Notes = append(u.Notes, decided(entries[0], "ack: "+r.Reason, r.Who, s.Now))
		if _, ticks := TickDecisions[n.Type]; ticks {
			// The tick's condition may still hold: the acknowledgement is
			// kept on it, so the tick does not write it again until it has
			// cleared and come back.
			u.Notes = append(u.Notes, acknowledged(n, entries, r.Who, s.Now))
		}
		if n.Type == NBlocked || n.Type == NMissingNeed {
			for _, o := range entries {
				if c, notes := waive(s, o.Subject(), r.Who, n.Needs, n.Type == NMissingNeed); c.Entry.ID != "" {
					u.Changes = append(u.Changes, c)
					u.Notes = append(u.Notes, notes...)
					u.Moved += "; " + o.Subject() + " waives " + c.Entry.Set["waived"]
					if c.Entry.Move != nil {
						u.Moved += " and is ready"
					}
				}
			}
		}
		p.Units = append(p.Units, u)
	}
	// A primary in review whose judgments this call closes gets the judgment
	// it needs after them, once, unless the call acknowledged exactly that.
	acked := map[string][]string{}
	for _, u := range p.Units {
		for _, o := range u.Closes {
			acked[o.Subject()] = append(acked[o.Subject()], o.Note.Type)
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
// coordinator acknowledged its dropped or missing needs: the ones the judgment
// names (only; every need of that kind for a judgment that names none) are waived,
// by whom and when, and count as satisfied. A need dropped after the judgment
// was written has its own. A primary with nothing else to wait for moves to
// ready in the same change; a sentinel is reached instead.
func waive(s *Snapshot, id, who string, only []string, missing bool) (Change, []Note) {
	c := s.Work.Placed(id)
	if c == nil || c.Col != Waiting {
		return Change{}, nil
	}
	var gone []string
	needs := droppedNeeds(s, WaitsFor(s, c, nil))
	if missing {
		needs = missingNeeds(s, WaitsFor(s, c, nil))
	}
	for _, n := range needs {
		if len(only) == 0 || contains(only, n) {
			gone = append(gone, n)
		}
	}
	if len(gone) == 0 {
		return Change{}, nil
	}
	set := map[string]string{"waived": strings.Join(append(Split(c.F("waived")), gone...), ","), "waived_by": who, "waived_at": stamp(s.Now)}
	after := &Card{ID: c.ID, Fields: map[string]string{"needs": c.F("needs"), "waived": set["waived"]}}
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
	n.What, n.Who, n.Needs = id+" needs "+strings.Join(gone, ",")+", dropped", who, gone
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
	a := Note{Kind: Acknowledged, Type: n.Type, Stream: n.Stream, What: n.What, StreamLevel: n.StreamLevel, Who: who, At: now}
	if !n.StreamLevel {
		for _, o := range entries {
			a.Primaries = append(a.Primaries, o.Subject())
		}
		a.Count = len(a.Primaries)
	}
	return a
}

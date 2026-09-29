package sprint

import (
	"fmt"
	"strings"
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
// stopped: that judgment stays open until the stream resumes. A primary whose
// last open judgment it closes, and whose reads are exhausted, is a judgment
// of its own.
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
			p.refuse(id, "no open judgment "+id+"; run: nova-sprint inbox")
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
		if n.Type == NBlocked {
			for _, o := range entries {
				if c := waive(s, o.Subject(), r.Who); c.Entry.ID != "" {
					u.Changes = append(u.Changes, c)
					u.Moved += "; " + o.Subject() + " waives " + c.Entry.Set["waived"]
					if c.Entry.Move != nil {
						u.Moved += " and is ready"
					}
				}
			}
		}
		p.Units = append(p.Units, u)
	}
	// A primary whose last open judgment this call closes, with its reads
	// exhausted, is a judgment once, unless the call acknowledged exactly that.
	written := map[string]bool{}
	for i := range p.Units {
		for _, o := range p.Units[i].Closes {
			pr := s.Work.Placed(o.Subject())
			if pr == nil || written[pr.ID] || o.Note.Type == NReadsExhausted {
				continue
			}
			written[pr.ID] = true
			if j, ok := exhaustedAfter(s, pr, closing, r.Who); ok {
				p.Units[i].Notes = append(p.Units[i].Notes, j)
			}
		}
	}
	return p
}

// waive is the change that records, on a waiting primary, that the
// coordinator acknowledged its dropped needs: they are waived, by whom and
// when, and count as satisfied. A primary with nothing else to wait for moves
// to ready in the same change.
func waive(s *Snapshot, id, who string) Change {
	c := s.Work.Placed(id)
	if c == nil || c.Col != Waiting {
		return Change{}
	}
	gone := droppedNeeds(s, WaitsFor(s, c, nil))
	if len(gone) == 0 {
		return Change{}
	}
	set := map[string]string{"waived": strings.Join(append(Split(c.F("waived")), gone...), ","), "waived_by": who, "waived_at": stamp(s.Now)}
	after := &Card{ID: c.ID, Fields: map[string]string{"needs": c.F("needs"), "waived": set["waived"]}}
	if len(WaitsFor(s, after, nil)) == 0 {
		return change(Work, moveEntry(c, c.Row, Ready, set))
	}
	return change(Work, setEntry(c, set))
}

// exhaustedAfter is the reads exhausted judgment of a primary in review whose
// reads are exhausted and whose every open judgment is among closing (note ids
// a step closes), if the condition holds.
func exhaustedAfter(s *Snapshot, pr *Card, closing map[string]bool, who string) (Note, bool) {
	if !exhausted(s, pr, nil) {
		return Note{}, false
	}
	for _, x := range closesFor(s.Open, nil, pr.ID) {
		if !closing[x.Note.ID] {
			return Note{}, false
		}
	}
	j := judgment(NReadsExhausted, pr.Row, s.Now, 0, pr.ID)
	j.Who, j.Attempt = who, pr.Int("attempt")
	return j, true
}

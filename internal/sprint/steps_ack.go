package sprint

import "fmt"

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

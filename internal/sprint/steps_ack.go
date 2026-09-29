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
	for _, id := range r.Notes {
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
		u := Unit{Key: id, Stream: n.Stream, Closes: entries, Moved: fmt.Sprintf("%s (%s) acknowledged: %s", id, n.Type, r.Reason)}
		u.Notes = append(u.Notes, decided(entries[0], "ack: "+r.Reason, r.Who, s.Now))
		for _, o := range entries {
			pr := s.Work.Placed(o.Subject())
			if pr == nil || !exhausted(s, pr, nil) {
				continue
			}
			left := 0
			for _, x := range closesFor(s.Open, nil, pr.ID) {
				if x.Note.ID != id {
					left++
				}
			}
			if left == 0 {
				j := judgment(NReadsExhausted, pr.Row, s.Now, 0, pr.ID)
				j.Who, j.Attempt = r.Who, pr.Int("attempt")
				u.Notes = append(u.Notes, j)
			}
		}
		p.Units = append(p.Units, u)
	}
	return p
}

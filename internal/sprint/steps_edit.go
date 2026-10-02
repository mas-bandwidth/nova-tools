package sprint

import "fmt"

// The coordinator's edits of a STOPPED sprint's primaries that have not
// started (the owner, 2026-10-01: "What other things should you be able to do
// to mutate a stopped sprint" / "I don't want you manually hopping in and
// working around it and doing manual stuff."): brief replaces a primary's
// brief, move takes primaries to another stream. Each is a pure plan of a
// snapshot, refused on a RUNNING machine and for a card that has started.

// stoppedOnly is the refusal of an edit on a RUNNING machine.
func stoppedOnly(what string) string {
	return "the machine is RUNNING, and " + what + " only on a STOPPED sprint; nothing was changed; run: nova-sprint stop"
}

// unstarted is why a primary may not be edited: "" when it is a primary
// placed waiting or ready that no work card was ever dealt for (its attempt
// is 0). A card dealt, working, in review, merging or landed keeps what it
// has (keeps names it).
func unstarted(s *Snapshot, id, keeps string) string {
	c := s.Work.Placed(id)
	switch {
	case c == nil:
		return "no primary " + id + " on the work table; nothing was changed"
	case IsSentinel(c):
		return id + " is a sentinel, not a primary; nothing was changed"
	case c.Col != Waiting && c.Col != Ready:
		return fmt.Sprintf("%s is %s: a card dealt, working, in review, merging or landed keeps %s; nothing was changed", id, c.Col, keeps)
	case c.Int("attempt") > 0:
		return fmt.Sprintf("%s is %s with attempt %d dealt: a card dealt keeps %s; nothing was changed", id, c.Col, c.Int("attempt"), keeps)
	}
	return ""
}

// BriefReq replaces the brief of a primary that has not started.
type BriefReq struct {
	ID, Brief, Who string
}

// Brief replaces a primary's brief (nova-sprint brief): on a STOPPED machine
// only, for a primary waiting or ready with no work card dealt. The card keeps
// its id, stream, score and needs; the command line holds the brief to the
// card lint, and the store to its bound, as add's.
func Brief(s *Snapshot, r BriefReq) Plan {
	var p Plan
	p.on(s)
	why := unstarted(s, r.ID, "its brief")
	if s.Running {
		why = stoppedOnly("a brief is replaced")
	}
	if why != "" {
		p.refuse(r.ID, why)
		return p
	}
	c := s.Work.Placed(r.ID)
	p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, setEntry(c, map[string]string{"brief": r.Brief}))},
		Moved: fmt.Sprintf("%s brief replaced (%d bytes) stream=%s %s", c.ID, len(r.Brief), c.Row, c.Col)})
	return p
}

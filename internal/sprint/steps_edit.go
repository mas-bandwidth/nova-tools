package sprint

import (
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

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
	// a card dealt is refused with what changes it instead, before the machine's
	// state: stopping the machine would not let its brief be replaced
	why := unstarted(s, r.ID, "its brief")
	c := s.Work.Placed(r.ID)
	if why != "" && c != nil && !IsSentinel(c) {
		why += "; " + briefStarted(c)
	}
	if why == "" && s.Running {
		why = stoppedOnly("a brief is replaced")
	}
	if why != "" {
		p.refuse(r.ID, why)
		return p
	}
	p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, setEntry(c, map[string]string{"brief": r.Brief}))},
		Moved: fmt.Sprintf("%s brief replaced (%d bytes) stream=%s %s", c.ID, len(r.Brief), c.Row, c.Col)})
	return p
}

// briefStarted is the remedy of a brief refused for a card dealt: what changes
// its work instead, by the lifecycle's moves (lifecycle.go, Moves). A rework's
// --fix is the next attempt's change, carried beside the brief, and rework
// takes a card in review only; drop takes any open card off the table, and the
// new brief is then a new card.
func briefStarted(c *Card) string {
	readd := "nova-sprint add --stream " + c.Row + " <new id> --brief-file <path>"
	drop := "nova-sprint drop " + c.ID + " --reason '<why>', then " + readd
	rework := "nova-sprint rework " + c.ID + " --fix '<what changes>'"
	switch c.Col {
	case Review:
		return "run: " + rework + " (the next attempt's fix), or " + drop
	case Merging:
		return "run: nova-sprint return " + c.ID + " --reason '<why>', then " + rework + " (the next attempt's fix), or " + drop
	case Landed:
		return "run: " + readd + " for the change"
	case Working:
		return "run: " + drop + ", or once it finishes (review), " + rework + " (the next attempt's fix)"
	}
	return "run: " + drop
}

// reworkWhy is why a primary takes no rework (rework is the next attempt of one
// in review), with the command that does what was wanted by its state: a card
// still working is reworked once it finishes, or dropped now; one never dealt
// has its brief replaced; one dealt and not finished is dropped and added again;
// one landed is a new card. "" for a primary in review.
func reworkWhy(c *Card) string {
	why := inState(c, Review)
	if why == "" || !c.Placed() {
		return why
	}
	drop := "nova-sprint drop " + c.ID + " --reason '<why>'"
	switch {
	case c.Col == Working:
		return why + "; its attempt is still running: once it finishes (review), run: nova-sprint rework " + c.ID + " --fix '<what changes>', or now: " + drop
	case (c.Col == Waiting || c.Col == Ready) && c.Int("attempt") == 0:
		return why + "; no attempt has run, so there is nothing to rework: change its task instead: nova-sprint brief " + c.ID + " --brief-file <path>"
	case c.Col == Landed:
		return why + "; it landed: the change is a new card: nova-sprint add --stream " + c.Row + " <new id> --brief-file <path>"
	}
	return why + "; run: nova-sprint card " + c.ID + " (what holds it), or " + drop
}

// MoveReq moves primaries that have not started to another stream, placed as
// add places cards: Before, After or Score, else at the end of the line.
type MoveReq struct {
	IDs           []string
	Stream        string
	Score         *float64
	Before, After string
	Who           string
}

// MoveCards takes primaries to another stream (nova-sprint move): on a STOPPED
// machine only, each a primary waiting or ready with no work card dealt and
// not of the stream already; all or none. The destination is placed exactly as
// add places cards (Add, on the snapshot without the moved cards): the stream
// made as add makes one when it is new, the cards in line by --before,
// --after or --score, else at the end, waiting or ready by their needs and the
// stream's sentinels, a reached sentinel behind them no longer reached, a
// cycle of needs refused; a ready card the destination would put behind a
// sentinel is refused by the lifecycle (Lawful: ready -> waiting is only the
// effect of inserting a sentinel). A moved card is the same card moved, not a new one:
// its id, brief, needs and admission stay, and a need naming it still holds.
func MoveCards(s *Snapshot, r MoveReq) Plan {
	var p Plan
	p.on(s)
	view := *s
	view.Work = s.Work.Frozen()
	moving := map[string]*Card{}
	var cards []CardAdd
	for _, id := range r.IDs {
		why := unstarted(s, id, "its stream")
		c := s.Work.Placed(id)
		switch {
		case s.Running:
			why = stoppedOnly("a card is moved")
		case why != "":
		case moving[id] != nil:
			why = "named twice"
		case c.Row == r.Stream:
			why = id + " is in stream " + r.Stream + " already; its place in line changes with nova-sprint rank; nothing was changed"
		}
		if why != "" {
			p.refuse(id, why)
			continue
		}
		moving[id] = c
		view.Work.Drop(id)
		cards = append(cards, CardAdd{ID: id, Brief: c.F("brief"), Needs: without(Split(c.F("needs")), Split(c.F("waived"))), File: id})
	}
	if len(cards) == 0 {
		return p
	}
	q := Add(&view, AddReq{Stream: r.Stream, Cards: cards, Score: r.Score, Before: r.Before, After: r.After, Who: r.Who})
	p.Rows, p.Notes, p.inserting = q.Rows, q.Notes, q.inserting
	p.Refused = append(p.Refused, q.Refused...)
	for _, u := range q.Units {
		var notes []Note
		for _, n := range u.Notes {
			// a need dropped was the card's before it moved: its judgment is open already
			if n.Type != NBlocked || moving[u.Key] == nil {
				notes = append(notes, n)
			}
		}
		u.Notes = notes
		for i, ch := range u.Changes {
			c := moving[ch.Entry.ID]
			if ch.Table != Work || ch.Entry.Create == nil || c == nil {
				continue
			}
			to := ch.Entry.Create
			score := to.Score
			u.Changes[i] = change(Work, ntable.BatchMemberEntry{ID: c.ID, Expect: at(c),
				Move: &ntable.MemberMoveOp{Row: to.Row, Col: to.Col, Score: &score}, Set: map[string]string{"stream": to.Row}})
			u.Moved = "moved from stream " + c.Row + " " + c.Col + ": " + u.Moved
		}
		p.Units = append(p.Units, u)
	}
	return p
}

package sprint

import (
	"fmt"
	"slices"
)

// A friend's card taken back (docs/SPEC-SPRINT.md section 1, a friend's card taken
// back): the coordinator takes a dealt card a friend has not started off her row, and
// its primary goes back to ready for the tick's deal (FriendDeal) to deal again, as its
// next attempt. A friend holds her cards whatever her status (no take-back by presence);
// this is the coordinator's own move, card by card, for a friend who has stalled.

// NFriendTaken is the happened note of a take: one on the primary's story per card taken.
const NFriendTaken = "a friend's card was taken back"

// FriendTakeReq is the coordinator taking cards back from a friend's row. IDs are work
// cards (or their primaries) dealt to Friend, ready or working on her row; Gens, when it
// names a card, is the generation the caller read it at. Pushed and Unread are what the
// caller read of origin before the step, by work card: the tip of its branch when
// origin holds one (her work, never taken), and why the tip could not be read.
type FriendTakeReq struct {
	Friend string
	IDs    []string
	Gens   map[string]int    `json:",omitempty"`
	Pushed map[string]string `json:",omitempty"`
	Unread map[string]string `json:",omitempty"`
	Reason string            `json:",omitempty"`
	Who    string
}

// FriendTake takes each named card off the friend's row (docs/SPEC-SPRINT.md section 1,
// a friend's card taken back): its work card is retired (its record kept, retired_by
// friend take) and its primary moves working -> ready, where the tick's deal deals it
// again as its next attempt (FriendDeal), with one happened note on the primary per
// take. A working card taken frees her lane, and her oldest ready card not taken moves
// into working, as her finish takes her next (friendNext). A card is refused by name when
// it is not ready or working on her row, when its generation is not the one named, when
// origin holds a push on its branch (it is her work: her report finishes it), or when
// that tip could not be read; one refusal refuses the step (it names its cards).
func FriendTake(s *Snapshot, r FriendTakeReq) Plan {
	var p Plan
	row := FriendRow(r.Friend)
	var taken []*Card
	for _, id := range r.IDs {
		c := s.Fleet.Placed(id)
		if c == nil || c.F("kind") != "work" {
			if pr := s.Work.Placed(id); pr != nil && pr.F("work") != "" {
				c = s.Fleet.Placed(pr.F("work"))
			}
		}
		switch {
		case c == nil || c.Row != row || (c.Col != Ready && c.Col != Working):
			p.refuse(id, fmt.Sprintf("%s is no card dealt to friend %s (ready or working on %s); run: nova-sprint card %s", id, r.Friend, row, id))
			continue
		case r.Gens[c.ID] != 0 && r.Gens[c.ID] != c.Int("gen"):
			p.refuse(id, fmt.Sprintf("%s is at generation %d, not %d: it moved since it was read; run: nova-sprint friend take %s %s", c.ID, c.Int("gen"), r.Gens[c.ID], r.Friend, c.ID))
			continue
		case r.Pushed[c.ID] != "":
			p.refuse(id, fmt.Sprintf("%s has a push on its branch at %s: it is %s's work, and her report finishes it; run: nova-sprint card %s", c.ID, r.Pushed[c.ID], r.Friend, c.F("primary")))
			continue
		case r.Unread[c.ID] != "":
			p.refuse(id, fmt.Sprintf("%s: origin's tip of its branch cannot be read (%s), so a push cannot be ruled out; run: nova-sprint friend take %s %s again", c.ID, r.Unread[c.ID], r.Friend, c.ID))
			continue
		}
		pr := s.Work.Placed(c.F("primary"))
		if pr == nil || pr.Col != Working || pr.F("work") != c.ID {
			p.refuse(id, fmt.Sprintf("%s is not its primary's live work card: nothing to take back; run: nova-sprint card %s", c.ID, c.F("primary")))
			continue
		}
		if !slices.Contains(taken, c) {
			taken = append(taken, c)
		}
	}
	// her ready cards not taken, oldest first: one moves into working for each working card taken
	var next []*Card
	for _, c := range s.Fleet.Cell(row, Ready) {
		if !slices.Contains(taken, c) {
			next = append(next, c)
		}
	}
	SortCards(next)
	why := ""
	if r.Reason != "" {
		why = ": " + r.Reason
	}
	for _, c := range taken {
		pr := s.Work.Placed(c.F("primary"))
		u := Unit{Key: pr.ID, Stream: pr.Row, Changes: []Change{
			change(Fleet, removeEntry(c, map[string]string{"retired": stamp(s.Now), "retired_by": "friend take"})),
			change(Work, moveEntry(pr, pr.Row, Ready, nil, "work")),
		}, Moved: fmt.Sprintf("%s taken back from %s (%s, unstarted)%s; %s working -> ready, for the tick to deal again", c.ID, row, c.Col, why, pr.ID)}
		n := happened(NFriendTaken, pr.Row, s.Now, pr.ID)
		n.Who, n.Card, n.Attempt = r.Who, c.ID, c.Int("attempt")
		n.What = fmt.Sprintf("%s taken back from friend %s, unstarted (no push on its branch)%s; the tick deals %s again as its next attempt", c.ID, r.Friend, why, pr.ID)
		u.Notes = append(u.Notes, n)
		if c.Col == Working && len(next) > 0 {
			nc := next[0]
			next = next[1:]
			u.Changes = append(u.Changes, change(Fleet, moveEntry(nc, row, Working, takenStamps(nc, s.Now), "untaken_since")))
			u.Moved += fmt.Sprintf("; %s ready -> working (her next, taken now)", nc.ID)
		}
		p.Units = append(p.Units, u)
	}
	return Lawful(p)
}

package sprint

import (
	"fmt"
	"slices"
)

// A friend's card taken back (docs/SPEC-SPRINT.md section 1, a friend's card taken back;
// the owner, 2026-10-04, on cards dealt to a friend who would not start them, which could
// only be dropped and added again: "sounds bad, we should fix this"). The coordinator takes
// back a card dealt to a friend that she has not started (friend take), and the hold of a
// friend (friend down) takes back every one of hers she has not started, as a held machine's
// cards are withdrawn: the work card is withdrawn on her row (never a failure: no redeal of
// its bound is spent, FieldTakeEnded is not set), its primary goes back to ready, and the
// friends' deal (friendDeal) places the same card again at its next generation, on its own
// branch and job. A card she has started stays with her and finishes: one she pushed to, one
// her beat names running (the caller reads both: Started), and one she finished (in review
// or later, so no longer ready or working on her row). With cards named, each one she may
// give up is taken and each one she keeps is refused, one line each (section 1, the card
// friend-take-partial.w1); AllOrNothing takes none when any is refused, as the take did
// before.

const (
	// FieldTakenBack is why a friend's work card was taken back, on the withdrawn card until
	// the deal places it again: "taken back by the coordinator: <reason>", or the hold's words.
	FieldTakenBack = "taken_back"
	// FieldTakenFrom is the friend's row the coordinator took the card back from (friend
	// take, not the hold): the deal that places it again never gives it back to her.
	FieldTakenFrom = "taken_from"
	// NTakenBack is the happened note of a friend's card taken back to ready.
	NTakenBack = "a friend's card taken back to ready"
)

// FriendTakeReq is friend take (IDs or All) or the hold's withdrawal (Hold, All): the
// friend, the cards named (each a primary or its work card), every card of hers she has
// not started (All), why, and Started, the work cards she has started as the caller read
// them (a push on the card's branch, her beat naming it running), each with its why.
// AllOrNothing takes none of the cards named when any one is refused.
type FriendTakeReq struct {
	Friend       string
	IDs          []string
	All          bool
	Hold         bool
	AllOrNothing bool
	Reason       string
	Started      map[string]string
	Who          string
}

// takenBackWhy is the words a taken card carries (FieldTakenBack).
func (r FriendTakeReq) takenBackWhy() string {
	if r.Hold {
		return "taken back by the hold of friend " + r.Friend + " (friend down)"
	}
	if r.Reason == "" {
		return "taken back by the coordinator"
	}
	return "taken back by the coordinator: " + r.Reason
}

// FriendTake takes back the friend's cards she has not started: each one withdrawn on her
// row, its primary ready for the friends' deal. A card named that is not dealt to her, or
// that she has started, is refused, one refusal each, and the rest are taken; with
// AllOrNothing one refusal takes none, each card that would have been taken refused too;
// with All a started card stays (the caller, which read Started, says so). A working card taken frees her lane: her oldest ready card not taken
// is taken into working in the same unit, as her finish takes it (friendNext).
func FriendTake(s *Snapshot, r FriendTakeReq) Plan {
	var p Plan
	row := FriendRow(r.Friend)
	mine := append(append([]*Card(nil), s.Fleet.Cell(row, Ready)...), s.Fleet.Cell(row, Working)...)
	SortCards(mine)
	var take []*Card
	if r.All {
		for _, c := range mine {
			// a hold takes started cards too: a card sitting on a held friend blocks every card
			// that needs it (the owner, 2026-10-04: "Held friends cards need to be
			// redistributed automatically"); the coordinator's own take keeps a started one
			if r.Started[c.ID] == "" || r.Hold {
				take = append(take, c)
			}
		}
	}
	for _, id := range r.IDs {
		c := s.Fleet.Placed(id)
		if c == nil || c.F("kind") != "work" {
			if pr := s.Work.Placed(id); pr != nil {
				c = s.Fleet.Placed(WorkCardID(pr.ID, pr.Int("attempt")))
			}
		}
		switch {
		case c == nil:
			p.refuse(id, "no card "+id+" is dealt to friend "+r.Friend)
		case c.Row != row:
			p.refuse(id, fmt.Sprintf("%s is not dealt to friend %s: it is at %s", c.ID, r.Friend, placeOf(c)))
		case c.Col != Ready && c.Col != Working:
			p.refuse(id, fmt.Sprintf("%s has started: friend %s finished it (%s), and its primary %s is %s", c.ID, r.Friend, c.Col, c.F("primary"), orDash(s.StateOf(c.F("primary")))))
		case r.Started[c.ID] != "":
			p.refuse(id, fmt.Sprintf("%s has started: %s; it stays with friend %s and finishes", c.ID, r.Started[c.ID], r.Friend))
		case !slices.Contains(take, c):
			take = append(take, c)
		}
	}
	if r.AllOrNothing && len(p.Refused) > 0 {
		n := len(p.Refused)
		for _, c := range take {
			p.refuse(c.ID, fmt.Sprintf("not taken: --all-or-nothing, and %d of the cards named %s refused", n, map[bool]string{true: "was", false: "were"}[n == 1]))
		}
		return p
	}
	// her ready cards not taken, oldest first: each working card taken frees a lane one fills
	var next []*Card
	for _, c := range mine {
		if c.Col == Ready && !slices.Contains(take, c) {
			next = append(next, c)
		}
	}
	why := r.takenBackWhy()
	for _, c := range take {
		// the take, not the hold, keeps her from it; a card never taken from her has its whole
		// dealt bound again from now (untaken_since), its first take unset
		set, unset := map[string]string{FieldTakenBack: why, "untaken_since": stamp(s.Now)}, []string{"first_taken"}
		if r.Hold {
			unset = append(unset, FieldTakenFrom)
		} else {
			set[FieldTakenFrom] = row
		}
		u := withdrawUnit(s, c, set, unset, NTakenBack, r.Who, why+" from friend "+r.Friend)
		if c.Col == Working && len(next) > 0 {
			n := next[0]
			next = next[1:]
			set, unset := friendTaken(s, n, r.Friend)
			u.Changes = append(u.Changes, change(Fleet, moveEntry(n, row, Working, set, unset...)))
			u.Moved += fmt.Sprintf("; %s ready -> working (her next, taken now)", n.ID)
		}
		p.Units = append(p.Units, u)
	}
	return p
}

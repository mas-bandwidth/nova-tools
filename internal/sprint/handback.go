package sprint

import (
	"fmt"
	"slices"
	"strings"
)

// A dealt card handed back (docs/SPEC-SPRINT.md, a dealt card handed back).
// handback withdraws a friend's work card whose lane has not started. The
// primary returns to ready at the same attempt, take_ended is not set, and
// taken_from is her row. friendsLeft (internal/sprint/friend_deal.go) then
// skips her on the next pass. That skip lasts until friend give: a one-pass
// expiry is friendDealPass, which this card does not change.

const (
	// FieldHandedBack is the reason a hand-back logged on the withdrawn work card.
	FieldHandedBack = "handed_back"
	// NHandedBack is the happened note of a dealt card handed back to the pool.
	NHandedBack = "a dealt card handed back to the pool"
)

// HandBackReq is handback: the friend (friend.<name> or her name), the cards
// named or every unstarted card on her row (All), the reason, and Lane, the
// work cards whose lane the caller has already seen started, each with the
// lane's name. A card she has started in the store (her start, a progress
// stamp, a report) is started even when Lane does not name it.
type HandBackReq struct {
	From   string
	IDs    []string
	All    bool
	Reason string
	Lane   map[string]string
	Who    string
}

// HandBackFriend is the friend a --from word names: friend.<name> or a bare name.
func HandBackFriend(from string) (string, bool) {
	from = strings.TrimSpace(from)
	if n, ok := FriendOfRow(from); ok && ValidID(n) {
		return n, true
	}
	if ValidID(from) {
		return from, true
	}
	return "", false
}

// handbackLane is the lane that has started c, or "" when it has not.
func handbackLane(c *Card, lane map[string]string) string {
	if c == nil {
		return ""
	}
	if name := lane[c.ID]; name != "" {
		return name
	}
	switch {
	case strings.TrimSpace(c.F("report")) != "":
		return "its report"
	case c.F(FieldProgress) != "":
		return "its progress"
	case startedNow(c):
		return "her start"
	default:
		return ""
	}
}

// HandBack hands the friend's unstarted work cards back to the pool. A card
// named that is not on her row, or whose lane has started, is refused, and
// the rest are handed back. With All, a started card stays and is said,
// naming the lane. The attempt is unchanged and no failure is recorded.
func HandBack(s *Snapshot, r HandBackReq) Plan {
	var p Plan
	name, ok := HandBackFriend(r.From)
	reason := strings.TrimSpace(r.Reason)
	switch {
	case !ok:
		p.refuse(r.From, "a friend is friend.<name> or a name of letters, digits, _ and -")
		return p
	case reason == "":
		p.refuse("handback", "a hand-back names its reason")
		return p
	case len(reason) > MaxCardTextBytes:
		p.refuse("handback", fmt.Sprintf("the reason is %d bytes, and a card field holds at most %d", len(reason), MaxCardTextBytes))
		return p
	case r.All == (len(r.IDs) > 0):
		p.refuse("handback", "name the cards, or --all-unstarted, and not both")
		return p
	}
	row := FriendRow(name)
	mine := append(append([]*Card(nil), s.Fleet.Cell(row, Ready)...), s.Fleet.Cell(row, Working)...)
	SortCards(mine)
	var take []*Card
	if r.All {
		for _, c := range mine {
			if lane := handbackLane(c, r.Lane); lane != "" {
				p.Said = append(p.Said, fmt.Sprintf("friend %s keeps %s: lane %s", name, c.ID, lane))
				continue
			}
			take = append(take, c)
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
			p.refuse(id, "no card "+id+" is dealt to friend "+name)
		case c.F("kind") != "work":
			p.refuse(id, c.ID+" is not a work card")
		case c.Row != row:
			p.refuse(id, fmt.Sprintf("%s is not dealt to friend %s: it is at %s", c.ID, name, placeOf(c)))
		case c.Col != Ready && c.Col != Working:
			p.refuse(id, fmt.Sprintf("%s has started: it is at %s, not ready or working on friend %s's row", c.ID, placeOf(c), name))
		case handbackLane(c, r.Lane) != "":
			p.refuse(id, fmt.Sprintf("%s has started: lane %s; it stays with friend %s", c.ID, handbackLane(c, r.Lane), name))
		case !slices.Contains(take, c):
			take = append(take, c)
		}
	}
	why := "handed back: " + reason
	for _, c := range take {
		set := map[string]string{
			FieldHandedBack: reason,
			FieldTakenFrom:  row,
			"untaken_since": stamp(s.Now),
		}
		what := fmt.Sprintf("%s from friend %s", why, name)
		p.Units = append(p.Units, withdrawUnit(s, c, set, []string{"first_taken"}, NHandedBack, r.Who, what))
	}
	return p
}

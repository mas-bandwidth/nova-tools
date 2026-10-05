package sprint

import (
	"sort"
	"strings"
)

// A judgment retires with its card (docs/SPEC-SPRINT.md, "A judgment retires with its
// card"). A judgment is about cards on the table: once every card it is open on has
// landed, been dropped or been replaced, no decision listed on it can move anything, and
// the coordinator could not even ack a tick-kept one. The tick end's check part closes it
// on the tick that sees the card gone, answered by the machine with the card's event, so
// the inbox never shows it. The verbs that move a card close what they answer (drop closes
// every judgment on the card); this is the floor under them, for a judgment a step left
// open or a tick wrote on the read before the card moved.

// NRetired is the answer the machine records on a judgment it retires: "retired with its
// card: <card> <event>; ...".
const NRetired = "retired with its card"

// OutlivesLanding is the judgment types about a card's landed work itself: they stay the
// coordinator's when the card lands (a low score's repair card, a red CI run at the landed
// head). A card off the table retires them too.
var OutlivesLanding = []string{NScoredLow, NCIRed}

// cardEvent is how the card left the table, as the retirement says it, and false while
// the card is on it: landed, dropped (its reason), replaced by its twin, or off the table
// (its outcome). A subject that is no card (a member, a reader) is never gone.
func cardEvent(s *Snapshot, id, typ string) (string, bool) {
	c := s.Work.Card(id)
	switch {
	case c == nil:
		return "", false
	case c.Placed() && c.Col == Landed:
		if contains(OutlivesLanding, typ) {
			return "", false
		}
		return id + " landed", true
	case c.Placed():
		return "", false
	case c.F("outcome") == "dropped" && strings.HasPrefix(c.F("reason"), "replaced by "):
		return id + " " + c.F("reason"), true
	case c.F("outcome") == "dropped":
		return id + " dropped (" + orDash(c.F("reason")) + ")", true
	}
	return id + " off the table (" + orDash(c.F("outcome")) + ")", true
}

// RetireJudgments closes every open judgment whose every open subject is a card that has
// left the table, each with one decided note by the machine naming the cards' events. A
// stream's or the sprint's judgment is never retired here. Running it twice changes
// nothing the second time.
func RetireJudgments(s *Snapshot, r TickReq) Plan {
	var p Plan
	type judg struct {
		note   Note
		opens  []Open
		events []string
		stays  bool
	}
	var order []string
	byID := map[string]*judg{}
	for _, o := range s.Open {
		n := o.Note
		if n.Kind != Judgment || n.StreamLevel || n.SprintLevel {
			continue
		}
		j := byID[n.ID]
		if j == nil {
			j = &judg{note: n}
			byID[n.ID] = j
			order = append(order, n.ID)
		}
		j.opens = append(j.opens, o)
		event, gone := cardEvent(s, o.Subject(), n.Type)
		if !gone {
			j.stays = true
			continue
		}
		j.events = append(j.events, event)
	}
	for _, id := range order {
		j := byID[id]
		if j.stays {
			continue
		}
		sort.Strings(j.events)
		var subjects []string
		for _, o := range j.opens {
			subjects = append(subjects, o.Subject())
		}
		sort.Strings(subjects)
		p.Closes = append(p.Closes, j.opens...)
		p.Notes = append(p.Notes, decided(Open{Note: j.note}, NRetired+": "+strings.Join(j.events, "; "), r.who(), s.Now, subjects...))
	}
	return p
}

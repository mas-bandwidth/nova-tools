package sprint

import (
	"strings"
)

// A judgment retires with its card (docs/SPEC-SPRINT.md section 8, "A judgment
// retires with its card"): an open judgment whose every card has left the table
// (landed, dropped, or dropped replaced by its twin) is closed by the tick that
// sees it gone, answered by the machine with the card's event, so the inbox never
// shows a judgment about a card no longer on the table. A judgment on a primary
// retires on that primary; a tick-kept judgment of a stream-level condition that
// names cards (a tier no route serves, a starving fleet's sentinel) retires when
// every card it names has left, and the tick raises it again on the cards still
// waiting. A stream's own judgment (a stop, a red base) is its stream's, never a
// card's, and stays.

// RetiredPrefix opens the decided note of a judgment retired with its card.
const RetiredPrefix = "retired with its card: "

// retiresWithCards are the stream-level judgments the tick keeps on the cards
// they name: each retires once every card it names has left the table.
var retiresWithCards = []string{NNoRoute, NStarving}

// aboutTheLanding are the judgments about a landed card by design: its landing
// is their cause, not their end. NInvariant is the check's own, closed when its
// rule holds again.
var aboutTheLanding = []string{NScoredLow, NInvariant}

// offTable is the event a card left the table by: "<id> landed", "<id> dropped
// (<reason>)", "<id> replaced by <twin>"; "" while it is on the table, or when the
// work table never had it (a subject that is not a card).
func offTable(s *Snapshot, id string) string {
	c := s.Work.Card(id)
	switch {
	case c == nil:
		return ""
	case c.Placed() && c.Col == Landed:
		return id + " landed"
	case c.Placed():
		return ""
	}
	reason := c.F("reason")
	if strings.HasPrefix(reason, "replaced by ") {
		return id + " " + reason
	}
	if c.F("outcome") == "dropped" {
		if reason == "" {
			return id + " dropped"
		}
		return id + " dropped (" + reason + ")"
	}
	return id + " off the table (" + orDash(c.F("outcome")) + ")"
}

// RetireJudgments is the closes and the machine's answers of every open judgment
// whose cards have all left the table, skipping those the plan closes already: one
// decided note a judgment, naming every card's event.
func RetireJudgments(s *Snapshot, p *Plan, who string) {
	if s.Work == nil {
		return
	}
	closing := map[string]bool{}
	for _, o := range p.Closes {
		closing[o.Key] = true
	}
	var order []string
	retired := map[string]*Note{}
	for _, o := range s.Open {
		n := o.Note
		if n.Kind != Judgment || closing[o.Key] || n.SprintLevel || contains(aboutTheLanding, n.Type) {
			continue
		}
		cards := []string{o.Subject()}
		if n.StreamLevel {
			if !contains(retiresWithCards, n.Type) || len(n.Primaries) == 0 {
				continue
			}
			cards = n.Primaries
		}
		var events []string
		for _, id := range cards {
			e := offTable(s, id)
			if e == "" {
				events = nil
				break
			}
			events = append(events, e)
		}
		if len(events) == 0 {
			continue
		}
		closing[o.Key] = true
		p.Closes = append(p.Closes, o)
		d := retired[n.ID]
		if d == nil {
			nd := decided(o, RetiredPrefix, who, s.Now)
			d = &nd
			retired[n.ID] = d
			order = append(order, n.ID)
		} else {
			d.What += "; "
		}
		d.What += strings.Join(events, "; ")
		if !n.StreamLevel {
			d.Primaries = append(d.Primaries, o.Subject())
			d.Count = len(d.Primaries)
		}
	}
	for _, id := range order {
		p.Notes = append(p.Notes, *retired[id])
	}
}

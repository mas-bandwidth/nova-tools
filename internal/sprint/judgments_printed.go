package sprint

import (
	"fmt"
	"strings"
)

// Every command a judgment prints runs as printed (the owner, 2026-10-05: "as the unreliable
// parts move into crystallized tools and process, the coordinator need not be a frontier
// model"; that day the bound judgment printed brief and brief refused eleven cards, a card in
// review keeps its brief; a tick-kept judgment printed ack and ack refused it, a condition the
// tick keeps; and a rework printed for one card of a group was refused for want of --one). A
// coordinator of any model follows the printed command, and a printed command the verb
// refuses is the machine lying to it. Pinned by TestEveryPrintedDecisionCommandRuns
// (cmd/nova-sprint), which runs every decision command of every judgment kind against a
// store the judgment is open in.

// printedDecisions is a judgment's decisions as its commands print them: a condition the
// tick keeps (TickKept) is never offered ack, since the tick raises it again while it holds
// and the remedy is what ends it; its ack is dropped and wait kept, so its commands are wait
// and the remedy. Every other judgment's decisions are its own.
func printedDecisions(typ string, ds []string) []string {
	if !TickKept(typ) || !contains(ds, "ack") {
		return ds
	}
	out := removeDecision(ds, "ack")
	for _, d := range out {
		if d == "wait" || strings.HasPrefix(d, "wait ") {
			return out
		}
	}
	return append(out, "wait")
}

// briefAtBound is brief run on a card at its brief's bound (brief_bound.go): the judgment
// raised for it prints brief, and a card dealt keeps its brief, so the new brief is the
// card's twin. In one step the card is replaced by its twin with the new brief (Recut: add
// --replaces of the old card, every card that needed it needing the twin, the old card
// dropped "replaced by <twin>", no blocked judgment raised), and each judgment open on it
// that offered brief is answered "brief: replaced by its twin <twin>". ok is false for a card
// at no bound: a card no judgment offering brief is open on, whose attempts since its brief
// last changed are under the attempt cap.
func briefAtBound(s *Snapshot, c *Card, b BriefCard, who string) (Plan, bool) {
	var offered []Open
	for _, o := range closesFor(s.Open, nil, c.ID) {
		if contains(o.Note.Decisions, "brief") {
			offered = append(offered, o)
		}
	}
	if len(offered) == 0 {
		if _, at := AtBriefBound(c, "", s.AttemptsCap(c.Row)); !at {
			return Plan{}, false
		}
	}
	twin := TwinID(s, c)
	p := Recut(s, RecutReq{ID: c.ID, New: twin, Brief: b.Brief, Rules: b.Rules, Needs: b.Needs, Who: who})
	if len(p.Refused) > 0 || len(p.Units) == 0 {
		return p, true
	}
	at := 0
	for i, u := range p.Units {
		if u.Key == c.ID {
			at = i
		}
	}
	for _, o := range offered {
		p.Units[at].Notes = append(p.Units[at].Notes, decided(o, "brief: replaced by its twin "+twin, who, s.Now))
	}
	p.Units[at].Moved += fmt.Sprintf("; %s was at its brief's bound: the brief is its twin %s", c.ID, twin)
	return p, true
}

package sprint

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// A primary's needs edited in place (the owner, 2026-10-07: "If it's just dependencies,
// please check if the dependencies are still correct"; 2026-10-06: "stop doing this twin
// shit. it's waste"): `needs <card> --drop <id>... --add <id>... --reason <text>` takes needs
// off a card and puts needs on it, the card keeping its id, stream, score, brief and log,
// one log line naming the needs before and after with the actor and the reason. Before it,
// a card whose need was dropped off the table (deferred, replaced, or done elsewhere) waited
// for ever unless its brief's DEPENDS-ON line was rewritten (brief) or the need waived (ack
// on the blocked judgment); on 2026-10-07 57 waiting cards named a need no listing printed.

// NNeedsSet is the happened note that records a primary's needs edited in place, on the
// card's timeline (log --card), with the actor and the reason.
const NNeedsSet = "needs set"

// NeedsReq is the verb needs' edit request: the card, the needs to take off it and to put on
// it, and the reason.
type NeedsReq struct {
	ID     string
	Drop   []string `json:",omitempty"`
	Add    []string `json:",omitempty"`
	Reason string
	Who    string
}

// EditNeeds edits a primary's needs in place, one step (docs/SPEC-SPRINT.md section 11,
// needs). The card keeps its id, stream, score, brief and log; the step writes its needs,
// `needs_set` ("<before> -> <after> <stamp> by <who>", the last edit) and one happened note
// on its timeline with the actor and the reason. When the edit leaves a waiting card with
// nothing unlanded to wait for (its named needs and its place in line, WaitsFor), the same
// step moves it waiting -> ready, as the step that lands its last need does (resolveAfter);
// a held card stays, for release. The blocked or missing judgments on the card that named
// only needs it no longer names are answered. The coordinator's alone. Refused whole, nothing
// written, for no --drop and no --add, no reason, a card that is no primary on the table, a
// sentinel (sentinel set), a landed card (landed is final), a need to drop the card does not
// have, a need to add it has already, the card itself, a need named twice, one that is no
// card on the table (every one named at once), a ready card given a need not landed (the
// deal would run it first: ready -> waiting is only a sentinel's effect, lifecycle.go), and
// a cycle the added needs would close (section 2's cycle rule).
func EditNeeds(s *Snapshot, r NeedsReq) Plan {
	var p Plan
	p.on(s)
	if why := notCoordinator(s, r.Who, "needs"); why != "" {
		p.refuse(r.ID, strings.Replace(why, "answers a judgment, which is", "is", 1))
		return p
	}
	if len(r.Drop)+len(r.Add) == 0 {
		p.refuse(r.ID, "needs wants --drop <id>... or --add <id>...: the needs to take off the card and to put on it; nothing was changed")
		return p
	}
	if strings.TrimSpace(r.Reason) == "" {
		p.refuse(r.ID, "needs wants --reason <text>: why the needs change, recorded on the card's timeline; nothing was changed")
		return p
	}
	c := s.Work.Placed(r.ID)
	switch {
	case c == nil:
		what := "no card, on the table or off it"
		if rc := s.Work.Card(r.ID); rc != nil {
			what = placeWord(rc)
		}
		p.refuse(r.ID, r.ID+" is no card on the table ("+what+"); nothing was changed; run: nova-sprint card "+r.ID)
		return p
	case IsSentinel(c):
		p.refuse(r.ID, r.ID+" is a sentinel: its needs change with nova-sprint sentinel set "+r.ID+" --needs <a,b>; nothing was changed")
		return p
	case c.Col == Landed:
		p.refuse(r.ID, r.ID+" landed: landed is final, and what needed it went on; nothing was changed")
		return p
	}
	old := Split(c.F("needs"))
	var why, gone []string
	seen := map[string]bool{}
	for _, n := range r.Drop {
		switch {
		case seen[n]:
			why = append(why, n+" is named twice")
		case !contains(old, n):
			why = append(why, n+" is no need of "+c.ID+" (its needs: "+orDash(strings.Join(old, ","))+")")
		}
		seen[n] = true
	}
	for _, n := range r.Add {
		nc := s.Work.Card(n)
		switch {
		case seen[n]:
			why = append(why, n+" is named twice")
		case n == c.ID:
			why = append(why, "a card does not need itself")
		case contains(old, n):
			why = append(why, c.ID+" needs "+n+" already")
		case nc == nil:
			gone = append(gone, n+" (no card)")
		case !nc.Placed():
			gone = append(gone, n+" (off the table, "+orDash(nc.F("outcome"))+")")
		case c.Col == Ready && nc.Col != Landed:
			why = append(why, fmt.Sprintf("%s is ready and %s is %s, not landed: a ready card would be dealt before its need (ready -> waiting is only a sentinel's effect)", c.ID, n, nc.Col))
		}
		seen[n] = true
	}
	if len(gone) > 0 {
		why = append(why, "not a card on the table: "+strings.Join(gone, ", "))
	}
	if len(why) > 0 {
		p.refuse(r.ID, strings.Join(why, "; ")+"; nothing was changed")
		return p
	}
	var now []string
	for _, n := range old {
		if !contains(r.Drop, n) {
			now = append(now, n)
		}
	}
	now = append(now, r.Add...)
	if len(r.Add) > 0 {
		waived := Split(c.F("waived"))
		var live []string
		for _, n := range now {
			if !contains(waived, n) {
				live = append(live, n)
			}
		}
		g := newNeedGraph(s, map[string][]string{c.ID: live}, nil, nil)
		if cycle := g.closes([]string{c.ID}); cycle != nil {
			p.refuse(r.ID, "the needs would make a cycle: "+g.tellLoop(cycle)+"; nothing was changed")
			return p
		}
	}
	before, after := orDash(strings.Join(old, ",")), orDash(strings.Join(now, ","))
	set := map[string]string{FieldNeedsSet: fmt.Sprintf("%s -> %s %s by %s", before, after, stamp(s.Now), orDash(r.Who))}
	var unset []string
	if len(now) > 0 {
		set["needs"] = strings.Join(now, ",")
	} else {
		unset = append(unset, "needs")
	}
	n := happened(NNeedsSet, c.Row, s.Now, c.ID)
	n.Who, n.What = r.Who, fmt.Sprintf("%s needs %s -> %s: %s", c.ID, before, after, r.Reason)
	u := Unit{Key: c.ID, Stream: c.Row, Notes: []Note{n}, Moved: n.What}
	// the blocked or missing judgments on it that named only needs it no longer names
	for _, o := range s.Open {
		if o.Subject() != c.ID || (o.Note.Type != NBlocked && o.Note.Type != NMissingNeed) {
			continue
		}
		if len(o.Note.Needs) > 0 && slices.ContainsFunc(o.Note.Needs, func(x string) bool { return contains(now, x) }) {
			continue // it still names a need the card keeps
		}
		u.Closes = append(u.Closes, o)
		u.Notes = append(u.Notes, decided(o, n.What, r.Who, s.Now, c.ID))
	}
	edited := withField(c, "needs", strings.Join(now, ","))
	if c.Col == Waiting && !IsHeld(c) && len(WaitsFor(s, edited, nil)) == 0 {
		// the same move the step that lands its last need makes (resolveAfter)
		maps.Copy(set, readyStamp(c, s.Now))
		u.Changes = []Change{change(Work, moveEntry(c, c.Row, Ready, set, unset...))}
		u.Moved += "; " + c.ID + " waiting -> ready (its needs landed)"
	} else {
		u.Changes = []Change{change(Work, setEntry(c, set, unset...))}
	}
	p.Units = append(p.Units, u)
	return Lawful(p)
}

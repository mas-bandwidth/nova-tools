package sprint

import (
	"fmt"
	"slices"
	"strings"
)

// A card replaced by its twin (docs/SPEC-SPRINT.md section 2; Rowan, 2026-10-04, at 1:30 PM:
// the fleet ran 4 of 68 slots while 311 cards sat behind 21 judgments "a primary is blocked
// on something dropped", each raised because a card was re-cut as a twin, its old id dropped
// and a new id added, and every card that needed the old id stalled for a person's ack). A
// twin takes over its old card's edges: every waiting card that needs the old id, and did
// not waive it, needs the new id instead, in the same place of its needs. Two verbs make it:
// add --replaces (Replace), the drop and the add as one step, which raises no blocked
// judgment; and relink (Relink), the repair of the edges a drop and an add already left,
// which answers the blocked judgments of that pair. The model is tla/SprintRules.tla
// (TwinsInherit, NoDanglingNeed).

// FieldRelinked is a card's record of its needs re-pointed to a twin: "<old,...> -> <new>
// <stamp> by <who>", the last relink.
const FieldRelinked = "relinked"

// RelinkReq re-points every waiting card's need of an old id to its twin.
type RelinkReq struct {
	Old    []string
	New    string
	Reason string `json:",omitempty"`
	Who    string
}

// twinDependents is the waiting cards whose needs name one of olds they did not waive, in
// work order.
func twinDependents(s *Snapshot, olds []string) []*Card {
	var out []*Card
	for _, c := range s.Work.Column(Waiting) {
		if len(dependentOlds(c, olds)) > 0 {
			out = append(out, c)
		}
	}
	return out
}

// dependentOlds is the olds the card needs and did not waive, in the order of its needs.
func dependentOlds(c *Card, olds []string) []string {
	waived := Split(c.F("waived"))
	var out []string
	for _, n := range Split(c.F("needs")) {
		if contains(olds, n) && !contains(waived, n) && !contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// relinkedNeeds is needs with every one of olds replaced by nw in its place, each id once.
func relinkedNeeds(needs, olds []string, nw string) []string {
	var out []string
	for _, n := range needs {
		if contains(olds, n) {
			n = nw
		}
		if !contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// relinked is the twin's step on the dependents of olds: one unit per dependent, its needs
// re-pointed to nw and its blocked or missing judgments that named only olds answered, and
// the weights the new edges move. adding is the twin when this step admits it (Replace),
// nil when it is on the table already. why is a refusal of the whole step: a cycle the new
// edges would close.
func relinked(s *Snapshot, olds []string, nw string, adding *Card, who, reason string) (units []Unit, deps []*Card, why string) {
	deps = twinDependents(s, olds)
	named := map[string][]string{}
	var roots []string
	var adds []*Card
	if adding != nil {
		named[adding.ID] = Split(adding.F("needs"))
		adds = append(adds, adding)
		roots = append(roots, adding.ID)
	}
	after := map[string]*Card{}
	for _, d := range deps {
		var live []string
		waived := Split(d.F("waived"))
		needs := relinkedNeeds(Split(d.F("needs")), dependentOlds(d, olds), nw)
		for _, n := range needs {
			if !contains(waived, n) {
				live = append(live, n)
			}
		}
		named[d.ID] = live
		roots = append(roots, d.ID)
		after[d.ID] = withField(d, "needs", strings.Join(needs, ","))
	}
	g := newNeedGraph(s, named, adds, nil)
	if cycle := g.closes(roots); cycle != nil {
		return nil, nil, "the needs would make a cycle: " + g.tellLoop(cycle) + "; nothing is written"
	}
	said := "replaced by " + nw
	if reason != "" {
		said += ": " + reason
	}
	for _, d := range deps {
		gone := dependentOlds(d, olds)
		set := map[string]string{"needs": after[d.ID].F("needs"),
			FieldRelinked: fmt.Sprintf("%s -> %s %s by %s", strings.Join(gone, ","), nw, stamp(s.Now), orDash(who))}
		u := Unit{Key: d.ID, Stream: d.Row, Changes: []Change{change(Work, setEntry(d, set))},
			Moved: fmt.Sprintf("%s needs %s -> %s", d.ID, strings.Join(gone, ","), nw)}
		for _, o := range s.Open {
			if o.Subject() != d.ID || (o.Note.Type != NBlocked && o.Note.Type != NMissingNeed) {
				continue
			}
			answered := len(o.Note.Needs) > 0 && !slices.ContainsFunc(o.Note.Needs, func(n string) bool { return !contains(gone, n) })
			if len(o.Note.Needs) == 0 {
				// a judgment that names none names every need of its kind: answered when the
				// dependent has none left once relinked
				left := droppedNeeds(s, NamedWaits(s, after[d.ID], nil))
				if o.Note.Type == NMissingNeed {
					left = missingNeeds(s, NamedWaits(s, after[d.ID], nil))
				}
				answered = len(left) == 0
			}
			if answered {
				u.Closes = append(u.Closes, o)
				u.Notes = append(u.Notes, decided(o, said+": "+d.ID+" needs "+nw+" in place of "+strings.Join(gone, ","), who, s.Now, d.ID))
			}
		}
		units = append(units, u)
	}
	return units, deps, ""
}

// twinWeights is the units that write each open primary's weight once the edges are
// re-pointed (weighUnits' rule over the state after the step): dropping the cards the step
// takes off, the dependents with their new needs, and the twin when the step admits it
// (its weight is written on its create, setCreate).
func twinWeights(s *Snapshot, deps []*Card, olds []string, nw string, adding *Card, dropping map[string]bool) (units []Unit, twin int) {
	var cards []*Card
	moved := map[string]*Card{}
	for _, d := range deps {
		moved[d.ID] = withField(d, "needs", strings.Join(relinkedNeeds(Split(d.F("needs")), dependentOlds(d, olds), nw), ","))
	}
	for _, c := range openPrimaries(s) {
		if dropping[c.ID] {
			continue
		}
		if m := moved[c.ID]; m != nil {
			c = m
		}
		cards = append(cards, c)
	}
	all := cards
	if adding != nil {
		all = append(slices.Clone(cards), adding)
	}
	w := weightsOver(all)
	for _, c := range cards {
		n := w[c.ID]
		if c.Int(FieldBehind) == n && (n != 0 || c.F(FieldBehind) == "") {
			continue
		}
		set, unset := map[string]string{FieldBehind: itoa(n)}, []string(nil)
		if n == 0 {
			set, unset = nil, []string{FieldBehind}
		}
		units = append(units, Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, setEntry(s.Work.Card(c.ID), set, unset...))},
			Moved: fmt.Sprintf("%s behind=%d", c.ID, n)})
	}
	if adding != nil {
		twin = w[adding.ID]
	}
	return units, twin
}

// Relink re-points every waiting card's need of an old id to the twin New, and answers the
// blocked (or missing) judgments that named only those olds, "replaced by <new>": the repair
// of a drop and an add made apart. The coordinator's alone. Refused whole, writing nothing,
// for a twin not on the table or a sentinel, an old id that is the twin or landed, an old
// id nothing waits on, or a cycle the new edges would close.
func Relink(s *Snapshot, r RelinkReq) Plan {
	var p Plan
	refuseAll := func(why string) Plan {
		var q Plan
		q.refuse(strings.Join(append(slices.Clone(r.Old), r.New), ","), why)
		return q
	}
	if why := notCoordinator(s, r.Who, "relink"); why != "" {
		return refuseAll(strings.Replace(why, "answers a judgment, which is", "is", 1))
	}
	if len(r.Old) == 0 || r.New == "" {
		return refuseAll("relink wants <old-id>[,<old-id>...] <new-id>")
	}
	nw := s.Work.Placed(r.New)
	switch {
	case nw == nil:
		return refuseAll(r.New + " is not on the table (" + placeWord(orEmpty(s.Work.Card(r.New), r.New)) + "): a twin is a card on the table; add it first")
	case IsSentinel(nw):
		return refuseAll(r.New + " is a sentinel, which is no card's twin")
	}
	var why []string
	seen := map[string]bool{}
	for _, old := range r.Old {
		switch c := s.Work.Card(old); {
		case seen[old]:
			why = append(why, old+" is named twice")
		case old == r.New:
			why = append(why, "relink "+old+" "+r.New+" names a card as its own twin: a card relinked to itself")
		case c.Placed() && c.Col == Landed:
			why = append(why, old+" landed: what needed it went on")
		case len(twinDependents(s, []string{old})) == 0:
			why = append(why, "nothing waits on "+old+" (no waiting card needs it, or each waived it)")
		}
		seen[old] = true
	}
	if len(why) > 0 {
		return refuseAll(strings.Join(why, "; ") + "; nothing was changed")
	}
	units, deps, cyc := relinked(s, r.Old, r.New, nil, r.Who, r.Reason)
	if cyc != "" {
		return refuseAll(cyc)
	}
	weights, _ := twinWeights(s, deps, r.Old, r.New, nil, nil)
	units, bad := mergeCardChanges(append(units, weights...))
	if bad != "" {
		return refuseAll(bad)
	}
	p.Units = units
	return p
}

// Replace is add --replaces: the one card r admits is the twin of every card r.Replaces
// names. In one step the twin is admitted (Add), every waiting card that needs an old id
// needs the twin instead, the blocked judgments that named only the old ids are answered
// "replaced by <new>", each old card still on the table is dropped with the reason
// "replaced by <new>" (Drop, on the state with its edges re-pointed, so no blocked judgment
// is raised), and the weights are written once for the state after. Refused whole, writing
// nothing, for an add of more or less than one card or of a sentinel, an old id that is the
// new one, is no card, landed or is a sentinel, a cycle, or any refusal of the add or the
// drop.
func Replace(s *Snapshot, r AddReq) Plan {
	ids := AddIDs(s, r)
	refuseAll := func(why string) Plan {
		var q Plan
		keys := ids
		if len(keys) == 0 {
			keys = []string{strings.Join(r.Replaces, ",")}
		}
		for _, id := range keys {
			q.refuse(id, why)
		}
		return q
	}
	if len(ids) != 1 {
		return refuseAll(fmt.Sprintf("add --replaces admits one card, the twin; found %d", len(ids)))
	}
	nw := ids[0]
	if r.Sentinel || (len(r.Cards) == 1 && r.Cards[0].Sentinel) || r.Every > 0 {
		return refuseAll("a sentinel replaces no card: add --replaces admits a primary")
	}
	var why []string
	var open []string
	seen := map[string]bool{}
	for _, old := range r.Replaces {
		c := s.Work.Card(old)
		switch {
		case seen[old]:
			why = append(why, old+" is named twice")
		case old == nw:
			why = append(why, nw+" replaces itself")
		case c == nil:
			why = append(why, "no card "+old+" on the table or off it")
		case IsSentinel(c):
			why = append(why, old+" is a sentinel, which no twin replaces")
		case c.Placed() && c.Col == Landed:
			why = append(why, old+" landed: landed is final")
		case c.Placed():
			open = append(open, old)
		}
		seen[old] = true
	}
	if len(why) > 0 {
		return refuseAll(strings.Join(why, "; ") + "; nothing was changed")
	}
	base := r
	base.Replaces = nil
	ap := Add(s, base)
	if len(ap.Refused) > 0 {
		return Plan{Refused: ap.Refused}
	}
	var adding *Card
	for _, u := range ap.Units {
		for _, ch := range u.Changes {
			if e := ch.Entry; ch.Table == Work && e.ID == nw && e.Create != nil {
				adding = &Card{ID: nw, Row: e.Create.Row, Col: e.Create.Col, Score: e.Create.Score, Fields: e.Set}
			}
		}
	}
	if adding == nil {
		return refuseAll("the add placed no card " + nw + "; nothing was changed")
	}
	units, deps, cyc := relinked(s, r.Replaces, nw, adding, r.Who, "")
	if cyc != "" {
		return refuseAll(cyc)
	}
	dropping := map[string]bool{}
	var dp Plan
	if len(open) > 0 {
		// the drop plans on the state with the edges re-pointed: no dependent needs an old
		// id there, so it raises no blocked judgment
		after := *s
		after.Work = s.Work.Frozen()
		for _, d := range deps {
			after.Work.Put(withField(d, "needs", strings.Join(relinkedNeeds(Split(d.F("needs")), dependentOlds(d, r.Replaces), nw), ",")))
		}
		dp = Drop(&after, DropReq{Sel: Sel{Only: open}, Reason: "replaced by " + nw, Who: r.Who})
		if len(dp.Refused) > 0 {
			return Plan{Refused: dp.Refused}
		}
		for _, id := range open {
			dropping[id] = true
		}
	}
	weights, twin := twinWeights(s, deps, r.Replaces, nw, adding, dropping)
	var all []Unit
	for _, u := range ap.Units {
		if weightOnly(u) {
			continue
		}
		for i, ch := range u.Changes {
			if ch.Table == Work && ch.Entry.ID == nw && ch.Entry.Create != nil {
				set := map[string]string{}
				for k, v := range ch.Entry.Set {
					set[k] = v
				}
				delete(set, FieldBehind)
				if twin > 0 {
					set[FieldBehind] = itoa(twin)
				}
				u.Changes[i].Entry.Set = set
				u.Moved += "; replaces " + strings.Join(r.Replaces, ",")
			}
		}
		all = append(all, u)
	}
	all = append(all, units...)
	for _, u := range dp.Units {
		if !weightOnly(u) {
			all = append(all, u)
		}
	}
	all, bad := mergeCardChanges(append(all, weights...))
	if bad != "" {
		return refuseAll(bad)
	}
	p := Plan{Rows: append(ap.Rows, dp.Rows...), Units: all, Notes: append(ap.Notes, dp.Notes...), Closes: append(ap.Closes, dp.Closes...),
		Props: append(ap.Props, dp.Props...), inserting: ap.inserting}
	p.on(s)
	return p
}

// weightOnly says the unit writes a primary's weight and nothing else (weighUnits).
func weightOnly(u Unit) bool {
	if len(u.Changes) != 1 || len(u.Notes) > 0 || len(u.Closes) > 0 || len(u.Bumps) > 0 {
		return false
	}
	e := u.Changes[0].Entry
	if e.Create != nil || e.Move != nil || e.Remove {
		return false
	}
	_, onlySet := e.Set[FieldBehind]
	return (len(e.Set) == 1 && onlySet && len(e.Unset) == 0) || (len(e.Set) == 0 && slices.Equal(e.Unset, []string{FieldBehind}))
}

// mergeCardChanges keeps one change per card and table across the units, as the store
// applies one entry per card in a step: a later change that only sets or unsets fields is
// folded into the first change of the same card, guarded on the same revision, and a unit
// left with nothing is passed over. Two changes that both move, create or remove a card are
// a refusal (bad), never applied.
func mergeCardChanges(units []Unit) (out []Unit, bad string) {
	type at struct{ u, c int }
	first := map[string]at{}
	for _, u := range units {
		var keep []Change
		for _, ch := range u.Changes {
			k := ch.Table + "\x00" + ch.Entry.ID
			f, ok := first[k]
			if !ok {
				first[k] = at{len(out), len(keep)}
				keep = append(keep, ch)
				continue
			}
			var into *Change
			if f.u == len(out) {
				into = &keep[f.c]
			} else {
				into = &out[f.u].Changes[f.c]
			}
			e := ch.Entry
			if e.Create != nil || e.Move != nil || e.Remove {
				return nil, "two changes of " + ch.Entry.ID + " in one step (" + ch.Table + "); nothing was changed"
			}
			set := map[string]string{}
			for k, v := range into.Entry.Set {
				set[k] = v
			}
			for k, v := range e.Set {
				set[k] = v
			}
			unset := slices.Clone(into.Entry.Unset)
			for _, n := range e.Unset {
				delete(set, n)
				if !slices.Contains(unset, n) {
					unset = append(unset, n)
				}
			}
			for k := range e.Set {
				unset = slices.DeleteFunc(unset, func(n string) bool { return n == k })
			}
			into.Entry.Set, into.Entry.Unset = nonEmpty(set), unset
			if f.u != len(out) && u.Moved != "" && !strings.Contains(out[f.u].Moved, u.Moved) {
				out[f.u].Moved += "; " + u.Moved
			}
		}
		u.Changes = keep
		if len(keep) == 0 && len(u.Notes) == 0 && len(u.Closes) == 0 && len(u.Bumps) == 0 {
			continue
		}
		out = append(out, u)
	}
	return out, ""
}

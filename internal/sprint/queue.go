package sprint

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// The work table's queue (the owner's tick, 2026-09-30, errata 3 amendment
// 12): "nothing advances the work stream table EXCEPT on the next tick", and
// "the previous tick does queue up all the changes for the work stream table,
// to process start of next tick". While the machine runs, a step other than
// the work pump writes the work table's changes it plans into the queue, in
// the same commit as the rest of the step, and applies the others now; the
// next tick's pump drains the whole queue in one update (Drain), then
// advances the cards. The queue is the work table's inbox, and its length is
// its dirty bit: "the dirty bit is just the number of entries in each table's
// queue". The model is tla/DirtyTick.tla.

// DrainVerb is the verb of the pump's drain step.
const DrainVerb = "tick drain"

// QueuedChange is one work-table change a step queued for the pump: the entry as
// the step planned it (its id the card's, not the stored one), or a property
// write of the work table; with the step's verb, actor and words, which the
// pump's line of the change carries.
type QueuedChange struct {
	Entry *ntable.BatchMemberEntry `json:"entry,omitempty"`
	Prop  *PropWrite               `json:"prop,omitempty"`
	Verb  string                   `json:"verb"`
	Actor string                   `json:"actor,omitempty"`
	Moved string                   `json:"moved,omitempty"`
}

// QueueOf splits a plan into what the step applies now and what it queues:
// every change and property of the work table goes to the queue, in the
// plan's order, and a guard-only entry of the work table is dropped (the
// fence's generation already holds the whole read, and only the pump writes
// the work table while the queue is used). A unit left with no change keeps
// its notes and closes.
func QueueOf(p Plan, verb, actor string) (Plan, []QueuedChange) {
	var q []QueuedChange
	units := make([]Unit, len(p.Units))
	for i, u := range p.Units {
		var keep []Change
		for _, c := range u.Changes {
			if c.Table != Work {
				keep = append(keep, c)
				continue
			}
			if !queuedChanges(c.Entry) {
				continue
			}
			e := c.Entry
			q = append(q, QueuedChange{Entry: &e, Verb: verb, Actor: actor, Moved: u.Moved})
		}
		u.Changes = keep
		units[i] = u
	}
	p.Units = units
	var props []PropWrite
	for _, pw := range p.Props {
		if pw.Table != Work {
			props = append(props, pw)
			continue
		}
		w := pw
		q = append(q, QueuedChange{Prop: &w, Verb: verb, Actor: actor})
	}
	p.Props = props
	return p, q
}

func queuedChanges(e ntable.BatchMemberEntry) bool {
	return e.Create != nil || e.Move != nil || e.Remove || len(e.Set) > 0 || len(e.Unset) > 0
}

// Drain is the pump's first update: the whole queue applied to the work table
// in one plan. The changes of one card are one entry, composed in the order
// they were queued (a later field wins, a later move takes the card on from
// where the one before left it); a property takes its last value. Each
// queued change was planned on the work table with the changes queued before
// it (the store plans every step but the pump's on WithQueue), so each
// expects the card where the changes before it leave it; one that does not
// is refused, named, and consumed with the rest: nothing of the queue is
// applied twice or kept for a later pump.
func Drain(s *Snapshot, q []QueuedChange, who string) Plan {
	p := Plan{pre: s, drained: true}
	type acc struct {
		e        ntable.BatchMemberEntry
		n        int // changes composed into e
		exists   bool
		placed   bool
		row, col string
		moved    []string
		why      string
		later    bool // a change the table cannot take in the same entry: it and the rest wait for the next drain
	}
	by := map[string]*acc{}
	var order []string
	props := map[string]PropWrite{}
	var propOrder []string
	for _, x := range q {
		switch x.Verb {
		case "release":
			p.releasing = true
		case "add":
			p.inserting = true
		}
		if x.Prop != nil {
			if _, ok := props[x.Prop.Name]; !ok {
				propOrder = append(propOrder, x.Prop.Name)
			}
			props[x.Prop.Name] = *x.Prop
			continue
		}
		if x.Entry == nil {
			continue
		}
		e := *x.Entry
		a := by[e.ID]
		if a == nil {
			a = &acc{}
			if c := s.Work.Card(e.ID); c != nil {
				a.exists, a.placed, a.row, a.col = true, c.Placed(), c.Row, c.Col
			}
			by[e.ID] = a
			order = append(order, e.ID)
		}
		words := strings.TrimSpace(x.Moved)
		if words == "" {
			words = x.Verb
		}
		words += " (" + x.Verb
		if x.Actor != "" {
			words += " by " + x.Actor
		}
		a.moved = append(a.moved, words+")")
		if a.why != "" {
			continue
		}
		if a.later || a.n > 0 && a.e.Create != nil && e.Remove {
			// a card created and taken off the table in one drain: the table
			// takes no entry that does both, so the removal waits for the
			// next drain, in its place in the queue
			a.later = true
			p.Requeue = append(p.Requeue, x)
			a.moved = a.moved[:len(a.moved)-1]
			continue
		}
		switch {
		case e.Expect != nil && e.Expect.Absent:
			if a.exists {
				a.why = "it creates the card and the card is there already"
			}
		case !a.placed:
			a.why = "the card is not on the table"
		case e.Expect != nil && e.Expect.Place != nil && (e.Expect.Place.Row != a.row || e.Expect.Place.Col != a.col):
			a.why = "it expects the card at " + e.Expect.Place.Row + ":" + e.Expect.Place.Col + " and the queue leaves it at " + a.row + ":" + a.col
		}
		if a.why != "" {
			continue
		}
		if a.n == 0 {
			a.e = e
		} else if a.e, a.why = composeQueued(a.e, e); a.why != "" {
			continue
		}
		a.n++
		switch {
		case e.Create != nil:
			a.exists, a.placed, a.row, a.col = true, true, e.Create.Row, e.Create.Col
		case e.Remove:
			a.placed = false
		case e.Move != nil:
			a.row, a.col = e.Move.Row, e.Move.Col
		}
	}
	for _, id := range order {
		a := by[id]
		if a.why != "" {
			p.refuse(id, "a queued change of "+id+" is not applied: "+a.why+" ("+strings.Join(a.moved, "; ")+")")
			continue
		}
		stream := ""
		if c := s.Work.Card(id); c.Placed() {
			stream = c.Row
			a.e.Expect = at(c)
		} else if a.e.Create != nil {
			stream = a.e.Create.Row
		}
		p.Units = append(p.Units, Unit{Key: id, Stream: stream, Changes: []Change{change(Work, a.e)},
			Moved: strings.Join(a.moved, "; ")})
	}
	for _, name := range propOrder {
		pw := props[name]
		was, had := s.Work.Prop(name)
		pw.Was, pw.WasAbsent = was, !had
		p.Props = append(p.Props, pw)
	}
	if len(p.Refused) > 0 {
		var ids []string
		for _, r := range p.Refused {
			ids = append(ids, r.Key)
		}
		sort.Strings(ids)
		n := judgment(NInvariant, "", s.Now, 0, ids...)
		n.Who, n.What = who, p.Refused[0].Why
		if len(p.Refused) > 1 {
			n.What += fmt.Sprintf("; and %d more", len(p.Refused)-1)
		}
		n.Decisions = append([]string(nil), TickDecisions[NInvariant]...)
		p.Notes = append(p.Notes, n)
	}
	return p
}

// composeQueued is two queued changes of one card as one: b after a. Its
// fields win, its move takes the card on from where a left it, and a card
// taken off the table or created twice cannot take a later change.
func composeQueued(a, b ntable.BatchMemberEntry) (ntable.BatchMemberEntry, string) {
	switch {
	case a.Remove:
		return a, "it was taken off the table by an earlier queued change"
	case b.Create != nil:
		return a, "it is created twice"
	}
	out := a
	if b.Move != nil {
		mv := *b.Move
		if mv.Score == nil {
			if out.Create != nil {
				sc := out.Create.Score
				mv.Score = &sc
			} else if out.Move != nil {
				mv.Score = out.Move.Score
			}
		}
		if out.Create != nil {
			cr := *out.Create
			cr.Row, cr.Col = mv.Row, mv.Col
			if mv.Score != nil {
				cr.Score = *mv.Score
			}
			out.Create = &cr
		} else {
			out.Move = &mv
		}
	}
	if b.Remove {
		out.Remove, out.Move = true, nil
	}
	if len(b.Set) > 0 || len(b.Unset) > 0 {
		set := map[string]string{}
		maps.Copy(set, a.Set)
		unset := append([]string(nil), a.Unset...)
		for _, k := range b.Unset {
			delete(set, k)
			if !contains(unset, k) {
				unset = append(unset, k)
			}
		}
		for k, v := range b.Set {
			set[k] = v
			if i := slices.Index(unset, k); i >= 0 {
				unset = slices.Delete(unset, i, i+1)
			}
		}
		out.Set, out.Unset = nonEmpty(set), unset
		if len(out.Unset) == 0 {
			out.Unset = nil
		}
	}
	return out, ""
}

// WithQueue is the snapshot as the pump will leave its work table once it has
// drained q: every change applied in memory, in the queue's order, the cards
// and the properties. The snapshot's other tables are shared, not copied. A
// change the drain would refuse is left out, as the drain leaves it out.
func WithQueue(s *Snapshot, q []QueuedChange) *Snapshot {
	if len(q) == 0 || s.Work == nil {
		return s
	}
	p := Drain(s, q, "")
	w := NewTable(s.Work.Name)
	w.Epoch, w.Revision, w.Texts, w.rows = s.Work.Epoch, s.Work.Revision+1, s.Work.Texts, s.Work.rows
	w.props = map[string]string{}
	maps.Copy(w.props, s.Work.props)
	for id, c := range s.Work.cards {
		cp := *c
		cp.Fields = map[string]string{}
		maps.Copy(cp.Fields, c.Fields)
		w.cards[id] = &cp
	}
	for _, u := range p.Units {
		for _, ch := range u.Changes {
			e := ch.Entry
			c := w.cards[e.ID]
			switch {
			case e.Create != nil:
				c = &Card{ID: e.ID, Row: e.Create.Row, Col: e.Create.Col, Score: e.Create.Score, Fields: map[string]string{}}
				w.cards[e.ID] = c
			case c == nil:
				continue
			}
			c.Rev++
			if e.Move != nil {
				c.Row, c.Col = e.Move.Row, e.Move.Col
				if e.Move.Score != nil {
					c.Score = *e.Move.Score
				}
			}
			if e.Remove {
				c.Col = ""
			}
			maps.Copy(c.Fields, e.Set)
			for _, k := range e.Unset {
				delete(c.Fields, k)
			}
		}
	}
	for _, pw := range p.Props {
		w.props[pw.Name] = pw.Value
	}
	n := *s
	n.Work = w
	n.Queue, n.QueueLen = nil, 0
	if len(p.Requeue) > 0 {
		return WithQueue(&n, p.Requeue)
	}
	return &n
}

// QueuedCards is the work cards the queue's changes name.
func QueuedCards(q []QueuedChange) map[string]bool {
	out := map[string]bool{}
	for _, x := range q {
		if x.Entry != nil {
			out[x.Entry.ID] = true
		}
	}
	return out
}

// LeaveQueued is a pump part's plan less every unit that changes a work card
// a queued change names: the change was queued after the pump's drain, and
// "nothing advances the work stream table EXCEPT on the next tick", so the
// card waits for the next tick's pump, where the change finds it where it
// expects it. Each unit left is said, never silent.
func LeaveQueued(p Plan, held map[string]bool) Plan {
	var keep []Unit
	for _, u := range p.Units {
		var named string
		for _, c := range u.Changes {
			if c.Table == Work && held[c.Entry.ID] {
				named = c.Entry.ID
				break
			}
		}
		if named == "" {
			keep = append(keep, u)
			continue
		}
		p.refuse(u.Key, named+" has a change queued after this tick's drain: it waits for the next tick's pump")
	}
	if len(keep) < len(p.Units) {
		p.Units = keep
		rewriteRounds(&p) // a unit dropped placed nothing: no index moves for it
	}
	return p
}

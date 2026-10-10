package sprint

import (
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// One selector, one step (docs/SPEC-SPRINT.md, "One selector, one step"; the owner,
// 2026-10-04: "BATCH EVERYTHING"). brief, recut, rework, return, release, rank and drop
// take one selector grammar, --stream, --who, --state and --ids-file, combinable (every
// one given must hold of a card), and change every card it selects in one store step: the
// selection is made on the state the step reads (Selected), so a step planned again on a
// fresh read selects again, and the step applies all or none.

// Selector is the cards a batch verb acts on: every placed primary of the work table that
// each field given holds of, landed cards never.
type Selector struct {
	Stream string `json:",omitempty"` // its stream
	// Who is the worker its WHO line names (FieldWho, a hard pin's only. prefix ignored):
	// friend.<name> (or the name alone) for one friend, friend for any friend's card, none
	// for a card that names no friend.
	Who string `json:",omitempty"`
	// State is ready, waiting (not held), held (IsHeld) or merging.
	State string `json:",omitempty"`
	// IDs is the cards an --ids-file names; one that is no placed card is refused.
	IDs []string `json:",omitempty"`
	// Sentinels takes a stream's sentinels with its primaries (release).
	Sentinels bool `json:",omitempty"`
}

// SelectorStates is the states a Selector's State names.
var SelectorStates = []string{string(Ready), string(Waiting), "held", string(Merging)}

// WhoNone is the Selector's Who for a card that names no friend.
const WhoNone = "none"

// Given says the selector names anything: a batch verb with none is refused.
func (q Selector) Given() bool {
	return q.Stream != "" || q.Who != "" || q.State != "" || len(q.IDs) > 0
}

// Check is why the selector does not read, "" when it does.
func (q Selector) Check() string {
	var why []string
	if q.State != "" && !slices.Contains(SelectorStates, q.State) {
		why = append(why, "--state wants "+strings.Join(SelectorStates, ", ")+", found "+q.State)
	}
	if w := q.who(); w != "" && w != WhoNone && w != WhoFriend {
		if _, ok := FriendOfRow(w); !ok || !ValidID(strings.TrimPrefix(w, friendRowPrefix)) {
			why = append(why, "--who wants friend.<name>, friend or none, found "+q.Who)
		}
	}
	for _, id := range q.IDs {
		if !ValidID(id) {
			why = append(why, "--ids-file names "+id+", which is no card id")
		}
	}
	return strings.Join(why, "; ")
}

// who is Who as FieldWho writes it: a bare name is that friend's row.
func (q Selector) who() string {
	switch w := q.Who; {
	case w == "" || w == WhoNone || w == WhoFriend || strings.HasPrefix(w, friendRowPrefix):
		return w
	default:
		return FriendRow(w)
	}
}

// Words is the selector as the command line gives it, for the step's lines.
func (q Selector) Words() string {
	var w []string
	if q.Stream != "" {
		w = append(w, "--stream "+q.Stream)
	}
	if q.Who != "" {
		w = append(w, "--who "+q.Who)
	}
	if q.State != "" {
		w = append(w, "--state "+q.State)
	}
	if len(q.IDs) > 0 {
		w = append(w, "--ids-file ("+itoa(len(q.IDs))+" ids)")
	}
	return strings.Join(w, " ")
}

// Selects says the selector holds of c.
func (q Selector) Selects(c *Card) bool {
	switch {
	case !c.Placed() || c.Col == Landed:
		return false
	case IsSentinel(c) && !q.Sentinels:
		return false
	case q.Stream != "" && c.Row != q.Stream:
		return false
	case len(q.IDs) > 0 && !slices.Contains(q.IDs, c.ID):
		return false
	}
	if q.Who != "" {
		w := strings.TrimPrefix(c.F(FieldWho), "only.")
		if want := q.who(); want == WhoNone && w != "" || want != WhoNone && w != want {
			return false
		}
	}
	switch q.State {
	case "":
	case "held":
		return IsHeld(c)
	case string(Waiting):
		return c.Col == Waiting && !IsHeld(c)
	default:
		return c.Col == q.State
	}
	return true
}

// Selected is the ids of the cards q selects on s, in work order (stream, then score, then
// id), and a refusal for each id of q.IDs that is no placed card or that the rest of the
// selector does not hold of.
func Selected(s *Snapshot, q Selector) (ids []string, refused []Refusal) {
	var cards []*Card
	for _, c := range s.Work.Cards() {
		if q.Selects(c) {
			cards = append(cards, c)
		}
	}
	sort.SliceStable(cards, func(i, j int) bool {
		a, b := cards[i], cards[j]
		if a.Row != b.Row {
			return a.Row < b.Row
		}
		if a.Score != b.Score {
			return a.Score < b.Score
		}
		return a.ID < b.ID
	})
	for _, c := range cards {
		ids = append(ids, c.ID)
	}
	seen := map[string]bool{}
	for _, id := range q.IDs {
		switch c := s.Work.Card(id); {
		case seen[id]:
			refused = append(refused, Refusal{id, "named twice in --ids-file"})
		case !c.Placed():
			refused = append(refused, Refusal{id, noSuchCard})
		case !slices.Contains(ids, id):
			refused = append(refused, Refusal{id, "the rest of the selector (" + q.Words() + ") does not hold of it"})
		}
		seen[id] = true
	}
	return ids, refused
}

// NoneSelected is why a selector that selects no card changes nothing.
func NoneSelected(q Selector) string {
	return "the selector names no card (" + q.Words() + "); nothing was changed"
}

// BriefEdit is a brief transform (nova-sprint brief and recut by selector): instead of a
// whole new brief, each selected card's own brief changed by rule.
type BriefEdit struct {
	SetBase string `json:",omitempty"` // the BASE: line names this branch
	DropWho bool   `json:",omitempty"` // the WHO: line is taken out: no friend is named
}

// Given says the transform changes anything.
func (e BriefEdit) Given() bool { return e.SetBase != "" || e.DropWho }

// Apply is brief with the transform made on its header block (the key: value lines under
// line 1, as cardhdr.ReadWho reads them): every WHO line taken out with DropWho, and with
// SetBase every BASE line's value replaced, or one BASE line put under line 1 when the
// header has none.
func (e BriefEdit) Apply(brief string) string {
	lines := strings.Split(brief, "\n")
	out := []string{lines[0]}
	header, based := true, false
	for _, l := range lines[1:] {
		k, _, ok := cardhdr.KeyValue(l)
		if header && !ok {
			header = false
		}
		if header && e.DropWho && strings.EqualFold(k, "who") {
			continue
		}
		if header && e.SetBase != "" && k == "BASE" {
			l, based = "BASE: "+e.SetBase, true
		}
		out = append(out, l)
	}
	if e.SetBase != "" && !based {
		out = slices.Insert(out, 1, "BASE: "+e.SetBase)
	}
	return strings.Join(out, "\n")
}

// Batch is one step of the plans steps makes, in order, each planned on the state the
// plans before it leave (applyPlan): a card re-cut after another sees that one's twin and
// re-pointed needs, so the twins of a chain need each other and a dependant of several
// follows every one. Their changes are folded into one entry per card (foldUnits), guarded
// on what the step read; the plan is all or none: a refusal of any plan refuses the whole.
// pre is the state the step read, against which the lifecycle judges the whole (Lawful).
func Batch(pre *Snapshot, steps []func(*Snapshot) Plan) Plan {
	cur := *pre
	cur.Work, cur.Readers, cur.Merge, cur.Fleet = pre.Work.Frozen(), pre.Readers.Frozen(), pre.Merge.Frozen(), pre.Fleet.Frozen()
	var p Plan
	var refused []Refusal
	props := map[string]int{}
	rows := map[RowAdd]bool{}
	closes := map[string]bool{}
	for _, step := range steps {
		q := step(&cur)
		if len(q.Refused) > 0 {
			refused = append(refused, q.Refused...)
			continue
		}
		applyPlan(&cur, q)
		for _, r := range q.Rows {
			if !rows[r] {
				rows[r] = true
				p.Rows = append(p.Rows, r)
			}
		}
		for _, pw := range q.Props {
			k := pw.Table + "\x00" + pw.Name
			if i, ok := props[k]; ok {
				p.Props[i].Value = pw.Value // guarded on the value the step read, the last one written
				continue
			}
			props[k] = len(p.Props)
			p.Props = append(p.Props, pw)
		}
		for _, o := range q.Closes {
			if !closes[o.Key] {
				closes[o.Key] = true
				p.Closes = append(p.Closes, o)
			}
		}
		p.Units = append(p.Units, q.Units...)
		p.Notes = append(p.Notes, q.Notes...)
		p.Updates = append(p.Updates, q.Updates...)
		p.Said = append(p.Said, q.Said...)
		p.Places = append(p.Places, q.Places...)
		p.inserting = p.inserting || q.inserting
		p.releasing = p.releasing || q.releasing
	}
	if len(refused) > 0 {
		return Plan{Refused: refused}
	}
	units, bad := foldUnits(p.Units)
	if bad != "" {
		return Plan{Refused: []Refusal{{"batch", bad}}}
	}
	p.Units = units
	p.on(pre)
	return p
}

// applyPlan is s as q leaves it: each change of each table the snapshot holds applied to a
// new card (a card is replaced whole, never changed in place, as the store's twin does),
// its revision kept, so a later plan's guard is the one the step read; each bump added;
// each property written.
func applyPlan(s *Snapshot, q Plan) {
	table := func(name string) *Table {
		switch name {
		case Work:
			return s.Work
		case Readers:
			return s.Readers
		case Merge:
			return s.Merge
		case Fleet:
			return s.Fleet
		}
		return nil
	}
	for _, u := range q.Units {
		for _, ch := range u.Changes {
			if t := table(ch.Table); t != nil {
				applyEntry(t, ch.Entry)
			}
		}
		for _, b := range u.Bumps {
			if t := table(b.Table); t != nil {
				if old := t.Card(b.ID); old != nil {
					t.Put(withField(old, b.Field, itoa(old.Int(b.Field)+b.Delta)))
				}
			}
		}
	}
	for _, pw := range q.Props {
		if t := table(pw.Table); t != nil {
			t.SetProp(pw.Name, pw.Value)
		}
	}
}

func applyEntry(t *Table, e ntable.BatchMemberEntry) {
	c := &Card{ID: e.ID, Fields: map[string]string{}}
	if old := t.Card(e.ID); old != nil {
		*c = *old
		c.Fields = maps.Clone(old.Fields)
		if c.Fields == nil {
			c.Fields = map[string]string{}
		}
	}
	switch {
	case e.Create != nil:
		c.Row, c.Col, c.Score = e.Create.Row, e.Create.Col, e.Create.Score
	case e.Move != nil:
		c.Row, c.Col = e.Move.Row, e.Move.Col
		if e.Move.Score != nil {
			c.Score = *e.Move.Score
		}
	}
	if e.Remove {
		c.Col = "" // kept, off the table
	}
	maps.Copy(c.Fields, e.Set)
	for _, k := range e.Unset {
		delete(c.Fields, k)
	}
	if e.Create == nil && e.Move == nil && !e.Remove && len(e.Set) == 0 && len(e.Unset) == 0 {
		return // a guard
	}
	t.Put(c)
}

// foldUnits keeps one change per card and table across the units, as the store applies one
// entry per card in a step (mergeCardChanges, which a batch widens): a later change of a
// card is folded into its first, guarded on the first's expectation (the state the step
// read): its fields set and unset over the first's, and its move, or its taking the card
// off the table, made the entry's. A unit left with nothing to write is passed over, its
// line kept on the unit it folded into. A card created twice, or changed after it left the
// table, is a refusal (bad), never applied.
func foldUnits(units []Unit) (out []Unit, bad string) {
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
			if why := foldEntry(&into.Entry, ch.Entry); why != "" {
				return nil, why + " (" + ch.Table + "); nothing was changed"
			}
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

// foldEntry folds the later entry e of a card into its first, into.
func foldEntry(into *ntable.BatchMemberEntry, e ntable.BatchMemberEntry) string {
	guard := e.Create == nil && e.Move == nil && !e.Remove && len(e.Set) == 0 && len(e.Unset) == 0
	switch {
	case guard:
		return ""
	case into.Remove:
		return e.ID + " is changed after it left the table in one step"
	case e.Create != nil:
		return "two changes of " + e.ID + " in one step create it"
	case e.Remove && into.Create != nil:
		return e.ID + " is created and taken off the table in one step"
	case e.Remove:
		into.Remove, into.Move = true, nil
	case e.Move != nil && into.Create != nil:
		cr := *into.Create
		cr.Row, cr.Col = e.Move.Row, e.Move.Col
		if e.Move.Score != nil {
			cr.Score = *e.Move.Score
		}
		into.Create = &cr
	case e.Move != nil:
		mv := *e.Move
		if mv.Score == nil && into.Move != nil {
			mv.Score = into.Move.Score
		}
		into.Move = &mv
	}
	set := maps.Clone(into.Set)
	if set == nil {
		set = map[string]string{}
	}
	maps.Copy(set, e.Set)
	unset := slices.Clone(into.Unset)
	for _, n := range e.Unset {
		delete(set, n)
		if !slices.Contains(unset, n) {
			unset = append(unset, n)
		}
	}
	for k := range e.Set {
		unset = slices.DeleteFunc(unset, func(n string) bool { return n == k })
	}
	into.Set, into.Unset = nonEmpty(set), unset
	return ""
}

// RecutSelReq is recut by selector: every selected card re-cut as its twin (Recut), its
// tier Tier and its brief its own with Edit made, in one step (Batch).
type RecutSelReq struct {
	Sel  Selector
	Tier string    `json:",omitempty"`
	Edit BriefEdit `json:",omitempty"`
	Who  string
}

// RecutSel is recut by selector (docs/SPEC-SPRINT.md, "One selector, one step"): the
// selection made on s (Selected), and each card's Recut planned on the state the recuts
// before it leave, all or none. A card the transform leaves as it was, with no tier
// named, is refused: its twin would change nothing.
func RecutSel(s *Snapshot, r RecutSelReq) Plan {
	ids, refused := Selected(s, r.Sel)
	if len(refused) > 0 {
		return Plan{Refused: refused}
	}
	if len(ids) == 0 {
		return Plan{Refused: []Refusal{{"selector", NoneSelected(r.Sel)}}}
	}
	steps := make([]func(*Snapshot) Plan, 0, len(ids))
	for _, id := range ids {
		steps = append(steps, func(cur *Snapshot) Plan {
			c := cur.Work.Placed(id)
			req := RecutReq{ID: id, Tier: r.Tier, Who: r.Who}
			if c != nil && r.Edit.Given() {
				brief := r.Edit.Apply(c.F("brief"))
				if brief == c.F("brief") && r.Tier == "" {
					var p Plan
					p.refuse(id, "the transform leaves the brief of "+id+" as it is, and no tier is named: its twin would change nothing")
					return p
				}
				req.Brief, req.Rules = brief, c.F(FieldRules)
			}
			return Recut(cur, req)
		})
	}
	return selected(Batch(s, steps), "recut", ids, r.Sel)
}

// BriefSelReq is brief by selector: every selected card's own brief with Edit made, and
// with Tier re-tiered, in one step.
type BriefSelReq struct {
	Sel  Selector
	Tier string    `json:",omitempty"`
	Edit BriefEdit `json:",omitempty"`
	Who  string
}

// BriefSel is brief by selector (docs/SPEC-SPRINT.md, "One selector, one step"): each
// selected card's brief replaced by its own with the transform made (Brief), and re-tiered
// (Brief with Tier), all or none in one step. A card the transform leaves as it was is
// passed over with a NOTE when a tier is named, and refused when none is.
func BriefSel(s *Snapshot, r BriefSelReq) Plan {
	ids, refused := Selected(s, r.Sel)
	if len(refused) > 0 {
		return Plan{Refused: refused}
	}
	if len(ids) == 0 {
		return Plan{Refused: []Refusal{{"selector", NoneSelected(r.Sel)}}}
	}
	var steps []func(*Snapshot) Plan
	for _, id := range ids {
		if r.Edit.Given() {
			steps = append(steps, func(cur *Snapshot) Plan {
				c := cur.Work.Placed(id)
				if c == nil {
					return Brief(cur, BriefReq{ID: id, Who: r.Who})
				}
				brief := r.Edit.Apply(c.F("brief"))
				if brief == c.F("brief") {
					var p Plan
					if r.Tier == "" {
						p.refuse(id, "the transform leaves the brief of "+id+" as it is")
					}
					return p
				}
				return Brief(cur, BriefReq{Cards: []BriefCard{{ID: id, Brief: brief, Rules: c.F(FieldRules)}}, Who: r.Who})
			})
		}
		if r.Tier != "" {
			steps = append(steps, func(cur *Snapshot) Plan { return Brief(cur, BriefReq{ID: id, Tier: r.Tier, Who: r.Who}) })
		}
	}
	return selected(Batch(s, steps), "brief", ids, r.Sel)
}

// selected is p with the selection said: one line per card, and the total.
func selected(p Plan, verb string, ids []string, q Selector) Plan {
	if len(p.Refused) > 0 {
		return p
	}
	for _, id := range ids {
		p.Said = append(p.Said, verb+" "+id+": selected")
	}
	p.Said = append(p.Said, verb+" selected="+itoa(len(ids))+" by "+q.Words())
	return p
}

// SelectIDs is a step over named ids made a step over a selection: the selection made on
// s (Selected) and handed to plan as the ids it names, with the lines selected says; a
// selection of no card, or an --ids-file id it does not hold of, is refused.
func SelectIDs(s *Snapshot, verb string, q Selector, plan func(ids []string) Plan) Plan {
	ids, refused := Selected(s, q)
	if len(refused) > 0 {
		return Plan{Refused: refused}
	}
	if len(ids) == 0 {
		return Plan{Refused: []Refusal{{"selector", NoneSelected(q)}}}
	}
	return selected(plan(ids), verb, ids, q)
}

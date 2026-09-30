package machine

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/stepbuild"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// StepBuilder is the loop's production Builder: a rule's plan cut into bodies
// by the step builder of 1.3.6, which landed as internal/sprint/stepbuild
// (IT04; errata 2, item 11). stepbuild cuts entries at every modelled bound of
// Layer 1's section 6; this adapter keeps each unit whole on top of it (1.3.6:
// "a primary's move, its work card's create and its read cards' retirement go
// in one request or the plan is cut before that unit"):
//
//   - each unit's changes become Layer 1 entries (create, move, a stay that
//     sets fields, remove, guard), grouped into one entry by kind, table,
//     cells and fields (T4: batch always), the unit's key the about id;
//   - the units are packed in order into groups within the bounds'
//     candidates, and a group is a body only when stepbuild.Build places it
//     in one step and its request, with the tick's meta, is inside the
//     bounds' bytes (sprintfn.EncodedSize, at the widest epoch); a group that
//     is not is halved, and a unit that is not alone refuses the plan (a
//     LIMIT, 1.3.5);
//   - the plan's notes, guards, intents and requeued keys ride the first
//     body, its count guards as one Layer 1 count entry, and its Done a plan
//     of one body only (a cut plan leaves its keys queued, 1.3.6); the rows it
//     adds go first.
//
// What the adapter does not carry refuses the plan, naming it, so the loop
// parks the keys and names the gap in "the machine's step was refused": a
// unit's bumps, a field guard, a guard of absence on anything but a create,
// the legacy notes and closes of Plan and Unit, and the time rules' writes to
// the sprint's own keys (TimeWrites), which have no wire in the sprint part
// yet. The Coster of 8.0 (X's and J's commands and probes) is not modelled
// here, as stepbuild models no planned command count (its package comment);
// the store refuses what it does not fit, and the loop halves (1.3.5).
func StepBuilder(prefix string) Builder {
	return func(rp sprint.RulePlan, m sprintfn.Meta, b stepbuild.Bounds) ([]sprintfn.Body, error) {
		if err := carried(rp); err != nil {
			return nil, err
		}
		units := make([]builtUnit, 0, len(rp.Plan.Units))
		for i, u := range rp.Plan.Units {
			es, err := stepEntries(u)
			if err != nil {
				return nil, fmt.Errorf("unit %d (%s): %w", i, u.Key, err)
			}
			units = append(units, builtUnit{entries: es, candidates: candidatesOf(es)})
		}
		var rows []stepbuild.Entry
		for _, r := range rp.Plan.Rows {
			rows = append(rows, stepbuild.Entry{Kind: stepbuild.KindRows, Table: r.Table, Add: []string{r.Row}})
		}
		sb := &stepBodies{prefix: prefix, meta: m, bounds: b, rp: rp, rows: rows}
		var group []builtUnit
		n := 0
		for _, u := range units {
			if len(group) != 0 && n+u.candidates > b.Candidates {
				if err := sb.fit(group); err != nil {
					return nil, err
				}
				group, n = nil, 0
			}
			group, n = append(group, u), n+u.candidates
		}
		if len(group) != 0 || len(rows) != 0 {
			if err := sb.fit(group); err != nil {
				return nil, err
			}
		}
		if len(sb.bodies) == 0 && len(rp.Notes)+len(rp.Guards)+len(rp.Intents)+len(rp.Requeue)+len(rp.Done) != 0 {
			body := sb.extras(sprintfn.Body{Entries: []tset.Entry{}}, true)
			if err := sb.check(body); err != nil {
				return nil, err
			}
			sb.bodies = append(sb.bodies, body)
		}
		if len(sb.bodies) == 1 {
			for _, k := range rp.Done {
				sb.bodies[0].Done = append(sb.bodies[0].Done, k.Key)
			}
		}
		return sb.bodies, nil
	}
}

// builtUnit is one unit's entries and the members they change.
type builtUnit struct {
	entries    []stepbuild.Entry
	candidates int
}

// stepBodies is one plan's build.
type stepBodies struct {
	prefix string
	meta   sprintfn.Meta
	bounds stepbuild.Bounds
	rp     sprint.RulePlan
	rows   []stepbuild.Entry
	bodies []sprintfn.Body
}

// guardCount is the kind of XGuard that is Layer 1's count entry (the fleet
// rules' guardCount).
const guardCount = "count"

// widestEpoch sizes a body at the longest epoch a request can carry, so the
// size holds at the loop's real epoch.
const widestEpoch = "18446744073709551615"

// fit makes a group of units bodies: one body when it fits one step, and
// otherwise its halves in order.
func (sb *stepBodies) fit(group []builtUnit) error {
	first := len(sb.bodies) == 0
	var es []stepbuild.Entry
	if first {
		es = append(es, sb.rows...)
	}
	for _, u := range group {
		es = appendGrouped(es, u.entries)
	}
	steps, err := stepbuild.Build(stepbuild.Config{Epoch: widestEpoch, Bounds: sb.bounds}, es)
	if err == nil && len(steps) > 1 {
		err = fmt.Errorf("its entries take %d steps", len(steps))
	}
	if err == nil {
		body := sprintfn.Body{Entries: []tset.Entry{}}
		if len(steps) == 1 {
			body.Entries = wireEntries(steps[0].Entries)
		}
		body = sb.extras(body, first)
		if err = sb.check(body); err == nil {
			sb.bodies = append(sb.bodies, body)
			return nil
		}
	}
	if len(group) <= 1 {
		return fmt.Errorf("the plan cannot be cut with its units whole: %w", err)
	}
	if err := sb.fit(group[:len(group)/2]); err != nil {
		return err
	}
	return sb.fit(group[len(group)/2:])
}

// extras puts the plan's notes, guards, intents and requeued keys on its first
// body. The count guards become one Layer 1 count entry over their cells (1.3.6:
// "a count guard names many cells in one entry"; rules_fleet.go, guardCount:
// the cell of a fleet member's ready queue and the most it held); every other
// guard goes to X as it is.
func (sb *stepBodies) extras(body sprintfn.Body, first bool) sprintfn.Body {
	if !first {
		return body
	}
	var guards []sprint.XGuard
	var count *tset.Entry
	for _, g := range sb.rp.Guards {
		if g.Kind != guardCount {
			guards = append(guards, g)
			continue
		}
		if count == nil {
			count = &tset.Entry{Kind: "count", Table: sprint.Fleet}
		}
		count.Cells = append(count.Cells, g.Key)
		count.CountMax = append(count.CountMax, uint64(max(g.Score, 0)))
	}
	if count != nil {
		body.Entries = append([]tset.Entry{*count}, body.Entries...)
	}
	body.Notes, body.Guards, body.Intents = sb.rp.Notes, guards, sb.rp.Intents
	for _, k := range sb.rp.Requeue {
		body.Requeue = append(body.Requeue, k.Key)
	}
	return body
}

// check holds a body's request, with the tick's meta, to the bounds' bytes.
func (sb *stepBodies) check(body sprintfn.Body) error {
	n, ref := sprintfn.EncodedSize(sb.prefix, &sprintfn.Request{Epoch: widestEpoch, Body: body, Meta: sb.meta})
	if ref != nil {
		return ref
	}
	if n > sb.bounds.RequestBytes {
		return fmt.Errorf("a request of %d bytes, over %d", n, sb.bounds.RequestBytes)
	}
	return nil
}

// carried refuses a plan with what the adapter does not carry.
func carried(rp sprint.RulePlan) error {
	p := rp.Plan
	switch {
	case len(p.Notes)+len(p.Closes)+len(p.Updates) != 0:
		return errors.New("the plan's legacy notes, closes or updates (a rule's notes are RulePlan.Notes)")
	case !reflect.DeepEqual(rp.Sprint, sprint.TimeWrites{}):
		return errors.New("the time rules' writes to the sprint's own keys have no wire in the sprint part yet")
	case len(p.Props) != 0:
		return errors.New("the plan's table properties (tset/1 prop entries) have no wire in the sprint part yet")
	}
	for _, u := range p.Units {
		if len(u.Bumps)+len(u.Notes)+len(u.Closes) != 0 {
			return fmt.Errorf("unit %s: bumps, notes or closes of a unit", u.Key)
		}
	}
	return nil
}

// stepEntries is a unit's changes as stepbuild entries, and its row deletes.
func stepEntries(u sprint.Unit) ([]stepbuild.Entry, error) {
	var out []stepbuild.Entry
	for _, c := range u.Changes {
		e := c.Entry
		about := u.Key
		if about == "" {
			about = e.ID
		}
		x := e.Expect
		if x != nil && len(x.Fields) != 0 {
			return nil, fmt.Errorf("%s: a field guard", e.ID)
		}
		se := stepbuild.Entry{Table: c.Table, IDs: []string{e.ID}, About: []string{about}, Set: e.Set, Unset: e.Unset}
		switch {
		case e.Create != nil:
			if x == nil || !x.Absent || x.Place != nil || x.Revision != "" {
				return nil, fmt.Errorf("%s: a create is guarded on absence alone", e.ID)
			}
			se.Kind, se.To = stepbuild.KindCreate, e.Create.Row+":"+e.Create.Col
			se.Scores = []string{strconv.FormatFloat(e.Create.Score, 'f', -1, 64)}
		case x == nil || x.Place == nil || x.Absent:
			return nil, fmt.Errorf("%s: a change of a card with no place to guard it at", e.ID)
		default:
			se.From = x.Place.Row + ":" + x.Place.Col
			if x.Revision != "" {
				se.Revs = []string{x.Revision}
			}
			switch {
			case e.Remove:
				se.Kind = stepbuild.KindRemove
			case e.Move != nil:
				se.Kind, se.To = stepbuild.KindMove, e.Move.Row+":"+e.Move.Col
				if e.Move.Score != nil {
					se.Scores = []string{strconv.FormatFloat(*e.Move.Score, 'f', -1, 64)}
				}
			case len(e.Set)+len(e.Unset) != 0:
				se.Kind = stepbuild.KindMove // a stay: from alone, the fields set
			default:
				se.Kind, se.About, se.Set, se.Unset = stepbuild.KindGuard, nil, nil, nil
			}
		}
		out = append(out, se)
	}
	for _, d := range u.RowDels {
		out = append(out, stepbuild.Entry{Kind: stepbuild.KindRows, Table: d.Table, Del: []string{d.Row}})
	}
	return out, nil
}

// candidatesOf is the members a unit's entries change (create, move, remove).
func candidatesOf(es []stepbuild.Entry) int {
	n := 0
	for _, e := range es {
		if e.Kind == stepbuild.KindCreate || e.Kind == stepbuild.KindMove || e.Kind == stepbuild.KindRemove {
			n += len(e.IDs)
		}
	}
	return n
}

// appendGrouped adds entries, each into an entry of the same kind, table,
// cells, fields and aligned arrays when there is one (T4).
func appendGrouped(into, es []stepbuild.Entry) []stepbuild.Entry {
	for _, e := range es {
		merged := false
		if e.Kind != stepbuild.KindRows {
			for i := range into {
				if groupable(into[i], e) {
					into[i].IDs = append(into[i].IDs, e.IDs...)
					into[i].Scores = append(into[i].Scores, e.Scores...)
					into[i].Revs = append(into[i].Revs, e.Revs...)
					into[i].About = append(into[i].About, e.About...)
					merged = true
					break
				}
			}
		}
		if !merged {
			e.IDs = append([]string(nil), e.IDs...)
			e.Scores = append([]string(nil), e.Scores...)
			e.Revs = append([]string(nil), e.Revs...)
			e.About = append([]string(nil), e.About...)
			into = append(into, e)
		}
	}
	return into
}

// groupable says two entries are one entry with more members: the same kind,
// table, cells and fields, and the same aligned arrays present.
func groupable(a, b stepbuild.Entry) bool {
	return a.Kind == b.Kind && a.Table == b.Table && a.From == b.From && a.To == b.To &&
		(len(a.Scores) == 0) == (len(b.Scores) == 0) && (len(a.Revs) == 0) == (len(b.Revs) == 0) &&
		(len(a.About) == 0) == (len(b.About) == 0) && len(a.IDs) < stepbuild.LimitEntryIDs &&
		reflect.DeepEqual(a.Set, b.Set) && reflect.DeepEqual(a.Unset, b.Unset)
}

// wireEntries are a step's entries as Layer 1's.
func wireEntries(ps []stepbuild.Placed) []tset.Entry {
	out := make([]tset.Entry, 0, len(ps))
	for _, p := range ps {
		e := tset.Entry{Kind: string(p.Kind), Table: p.Table, From: p.From, To: p.To, IDs: p.IDs, Scores: p.Scores,
			Set: p.Set, Each: p.Each, Unset: p.Unset, BeforeFields: p.BeforeFields, About: p.About, Add: p.Add, Del: p.Del}
		for _, r := range p.Revs {
			e.Revs = append(e.Revs, tset.Decimal(r))
		}
		if p.Meta != nil {
			e.Meta, _ = json.Marshal(p.Meta) // a map of strings encodes
		}
		out = append(out, e)
	}
	return out
}

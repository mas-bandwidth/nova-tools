package machine

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

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
//     body, its guards as convertGuards makes them (Layer 1 entries first),
//     and its Done a plan of one body only (a cut plan leaves its keys
//     queued, 1.3.6); the rows it adds go first. Its writes to the sprint's
//     own keys (TimeWrites) ride the first request as the sprint part
//     (TimePart, put on it by the loop's cut()).
//
// What the adapter does not carry refuses the plan, naming it, so the loop
// parks the keys and names the gap in "the machine's step was refused": a
// unit's bumps, a field guard, a guard of absence on anything but a create,
// the legacy notes and closes of Plan and Unit, and a guard no store check
// holds (convertGuards). The Coster of 8.0 (X's and J's commands and probes)
// is not modelled here, as stepbuild models no planned command count (its
// package comment); the store refuses what it does not fit, and the loop
// halves (1.3.5).
func StepBuilder(prefix string) Builder {
	return func(rp sprint.RulePlan, m sprintfn.Meta, b stepbuild.Bounds) ([]sprintfn.Body, error) {
		if err := carried(rp); err != nil {
			return nil, err
		}
		l1, guards, err := convertGuards(rp)
		if err != nil {
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
		sb := &stepBodies{prefix: prefix, meta: m, bounds: b, rp: rp, rows: rows, l1: l1, guards: guards}
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
		if len(sb.bodies) == 0 && (len(rp.Notes)+len(rp.Guards)+len(rp.Intents)+len(rp.Requeue)+len(rp.Done) != 0 || !rp.Sprint.Empty()) {
			body := sb.extras(sprintfn.Body{Entries: []tset.Entry{}}, true)
			if err := sb.check(body); err != nil {
				return nil, err
			}
			sb.bodies = append(sb.bodies, body)
		}
		if len(sb.bodies) == 1 {
			parked := map[string]bool{}
			for _, k := range rp.Sprint.Park {
				parked[k.Key] = true
			}
			for _, k := range rp.Done {
				if !parked[k.Key] { // the sprint part moves a parked key out of the agenda, the park first (A1)
					sb.bodies[0].Done = append(sb.bodies[0].Done, k.Key)
				}
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
	// l1 are the plan's guards that are Layer 1 entries, and guards those X
	// checks, as convertGuards made them; both ride the first body.
	l1     []tset.Entry
	guards []sprint.XGuard
}

// The kinds of XGuard the rules plan that are not X's own (8.0 has no field
// for them; each rule file says why it rides in RulePlan.Guards). The builder
// makes each a Layer 1 entry or a kind X has (convertGuards).
const (
	guardCount    = "count"    // rules_fleet.go guardCount: a ready cell held at most Score cards
	guardRCount   = "rcount"   // rules_fleet.go guardRCount: "sent:<s> <max>", R6's zguard on sent:s
	guardSetGuard = "setguard" // rules_position.go posSetGuard: a SetGuard as JSON (R3, R15)
	guardCounter  = "counter"  // rules_position.go posCounter (R15), rules_time.go guardCounter (R17)
	guardCtl      = "ctl"      // rules_time.go guardCtl: a member's control card's revision (R17)
	guardBeat     = "beat"     // rules_time.go guardBeat: beat:<m>'s score in the due set (R17)
	guardRevs     = "revs"     // rules_time.go guardRevs: the fold of a table's cards' revisions (R17)
	guardDue      = "due"      // rules_time.go noEntryAbove: no due (or cut) entry above Score (R11, R14, R18)
	guardClock    = "clock"    // rules_time.go guardClock: a clock field as read, 0 for an empty one (R17)
)

// convertGuards makes a plan's guards what the store checks (1.3.5; L1 3):
//
//   - count (R6's receivers' ready cells): one Layer 1 count entry over the
//     cells (1.3.6: "a count guard names many cells in one entry");
//   - a SetGuard of kind rcount (R3's reach and unreach, R15's done): a Layer
//     1 rcount entry, as it is;
//   - a SetGuard of kind zguard with a count bound (R3's release, the add's
//     ready guard) and R6's rcount on sent:s ("sent:<s> <max>", made
//     S.zguard(sent:s, rcount, -inf, max, atmost 0)): X's set guard, S.zguard
//     over the index's key {p}sprint:<index>@e (sprintfn.XGuardSet, IT19's; the
//     one kind X has for a zguard);
//   - counter (R15's COUNTER on next.streams, R17's score and streams): X's
//     counter guard on the field of {p}next@e (sprintfn.XGuardCounter). A
//     counter is a sprint key, not a card, so no Layer 1 entry can guard it;
//     the sprint part's CounterChange guards only the fields it writes;
//   - due (the time rules' noEntryAbove: R11's cut clock, R14, R18): X's
//     dueatmost guard, the entry absent or at or below the time; X's own due
//     kind holds an entry to its score as read, and the pop takes the entry,
//     so it would refuse every such step (the erratum on 2.3's two readings
//     is owed);
//   - clock (R17): X's clock guard, a field R17 read as 0 guarded as
//     XGuardAbsent: sprint.Clock reads an empty field as 0, and the clock part
//     writes every field but stopped_ms empty for 0 (X reads "" as absent);
//   - ctl (R17): a Layer 1 guard entry on the member's control card at its
//     cell and revision; beat (R17): X's due guard on beat:<m>, absent when
//     read as 0;
//   - every other kind goes to X as it is.
//
// revs (R17's fold of the revisions of a table's cards) is refused: the fold
// names no card, so neither Layer 1 nor X can check it (errata 3 H17 chose one
// guard a table; its carrier is owed).
func convertGuards(rp sprint.RulePlan) ([]tset.Entry, []sprint.XGuard, error) {
	var guards []sprint.XGuard
	var entries []tset.Entry
	var count *tset.Entry
	sets, err := sprint.SetGuardsOf(rp)
	if err != nil {
		return nil, nil, err
	}
	for _, g := range sets {
		switch {
		case g.Kind == sprint.GuardRCount:
			e := tset.Entry{Kind: "rcount", Table: g.Table, Cells: append([]string(nil), g.Cells...), ScoreMin: g.Min, ScoreMax: g.Max}
			if g.AtLeast != nil {
				e.AtLeast = ptrUint(*g.AtLeast)
			}
			if g.AtMost != nil {
				e.AtMost = ptrUint(*g.AtMost)
			}
			entries = append(entries, e)
		case g.Kind == sprint.GuardZGuard && (g.AtLeast != nil || g.AtMost != nil):
			guards = append(guards, g.XGuard())
		default:
			return nil, nil, fmt.Errorf("a set guard of kind %s on %q that neither Layer 1 nor X checks", g.Kind, g.Key+strings.Join(g.Cells, ","))
		}
	}
	for _, g := range rp.Guards {
		switch g.Kind {
		case guardSetGuard:
		case guardCount:
			if count == nil {
				count = &tset.Entry{Kind: "count", Table: sprint.Fleet}
			}
			count.Cells = append(count.Cells, g.Key)
			count.CountMax = append(count.CountMax, uint64(max(g.Score, 0)))
		case guardRCount:
			sent, most, ok := strings.Cut(g.Key, " ")
			if !ok || !strings.HasPrefix(sent, "sent:") {
				return nil, nil, fmt.Errorf("R6's rcount %q is not sent:<s> <max>", g.Key)
			}
			guards = append(guards, sprint.SetGuard{Kind: sprint.GuardZGuard, Key: sent, Min: "-inf", Max: most, AtMost: ptrInt(0)}.XGuard())
		case guardCounter:
			field := "streams"
			if g.Key == "next" {
				field = "score"
			}
			guards = append(guards, sprint.XGuard{Kind: sprintfn.XGuardCounter, Key: field, Score: g.Score})
		case guardCtl:
			entries = append(entries, tset.Entry{Kind: "guard", Table: sprint.Fleet, From: g.Member + ":ctl",
				IDs: []string{sprint.CtlID(g.Member)}, Revs: []tset.Decimal{tset.Decimal(strconv.FormatInt(g.Score, 10))}})
		case guardDue:
			guards = append(guards, sprint.XGuard{Kind: sprintfn.XGuardDueAtMost, Key: g.Key, Score: g.Score})
		case guardClock:
			if g.Score == 0 && g.Key != "stopped_ms" {
				g.Score = sprintfn.XGuardAbsent
			}
			guards = append(guards, g)
		case guardBeat:
			score := g.Score
			if score == 0 {
				score = sprintfn.XGuardAbsent
			}
			guards = append(guards, sprint.XGuard{Kind: sprintfn.XGuardDue, Key: g.Key, Score: score})
		case guardRevs:
			return nil, nil, fmt.Errorf("the fold of the revisions of the %s cards read (R17's stopinputs) has no guard in Layer 1 or X", g.Key)
		default:
			guards = append(guards, g)
		}
	}
	if count != nil {
		entries = append([]tset.Entry{*count}, entries...)
	}
	return entries, guards, nil
}

func ptrUint(n int) *uint64 {
	u := uint64(max(n, 0))
	return &u
}

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
// body, the guards as convertGuards made them: its Layer 1 entries first.
func (sb *stepBodies) extras(body sprintfn.Body, first bool) sprintfn.Body {
	if !first {
		return body
	}
	if len(sb.l1) != 0 {
		body.Entries = append(append([]tset.Entry(nil), sb.l1...), body.Entries...)
	}
	body.Notes, body.Guards, body.Intents = sb.rp.Notes, sb.guards, sb.rp.Intents
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
	if len(p.Notes)+len(p.Closes)+len(p.Updates) != 0 {
		return errors.New("the plan's legacy notes, closes or updates (a rule's notes are RulePlan.Notes)")
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

// TimePart is a plan's writes to the sprint's own keys (RulePlan.Sprint,
// rules_time.go TimeWrites) as the sprint part carries them (IT16's
// SprintPart.Time and Park), nil when it has none; the loop's cut() puts it on
// the first request of the rule's step. The due entries and R14's claims go as
// they are (the claim's generation is the step's, Meta.Gen); R17's clock
// fields as set or cleared; a parked key with its rule and code, which the
// part records in {p}parked@e and moves out of the agenda (1.3.5, A1).
func TimePart(w sprint.TimeWrites) *sprintfn.SprintPart {
	if w.Empty() {
		return nil
	}
	dec := func(n int64) tset.Decimal { return tset.Decimal(strconv.FormatInt(n, 10)) }
	sp := &sprintfn.SprintPart{}
	if len(w.Due)+len(w.Goal) != 0 || w.Clock != nil {
		tm := &sprintfn.SprintTime{}
		for _, d := range w.Due {
			tm.Due = append(tm.Due, sprintfn.DueAt{Key: d.Key, At: dec(d.At)})
		}
		for _, g := range w.Goal {
			tm.Goals = append(tm.Goals, sprintfn.GoalClaim{Person: g.Person, R: dec(g.R)})
		}
		if c := w.Clock; c != nil {
			field := func(set *int64, clear bool) *tset.Decimal {
				switch {
				case set != nil:
					d := dec(*set)
					return &d
				case clear:
					d := tset.Decimal("")
					return &d
				}
				return nil
			}
			tm.Clock = &sprintfn.ClockWrite{DueSince: field(c.DueSince, c.ClearDueSince), StopRaised: field(c.StopRaised, c.ClearStopRaised)}
		}
		sp.Time = tm
	}
	for _, k := range w.Park {
		sp.Park = append(sp.Park, sprintfn.ParkedKey{Key: k.Key, Rule: k.Rule, Code: k.Code})
	}
	return sp
}

func ptrInt(n int) *int { return &n }

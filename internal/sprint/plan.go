package sprint

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// Change is one batch manifest entry for one table.
type Change struct {
	Table string // logical name
	Entry ntable.BatchMemberEntry
}

// Bump adds Delta to a counter field of a card that several units of one step
// share: the store binding sums the bumps of the units it applies together
// into one entry.
type Bump struct {
	Table, ID, Field string
	Delta            int
}

// Unit is one card's part of a step: the changes that must apply together,
// the notifications the move causes, and the open judgments it answers.
type Unit struct {
	Key     string // the primary or card the unit is about
	Stream  string // the stream whose progress it is
	Changes []Change
	Bumps   []Bump
	Notes   []Note
	Closes  []Open // open judgments this unit answers: recorded as decided
	Moved   string // what moved, one line
	// RowDels are rows the unit's part deletes once its changes are applied:
	// only the last part of a remove has them (Remove).
	RowDels []RowDel
}

// Refusal is a card the step did not move, and why.
type Refusal struct {
	Key string `json:"id"`
	Why string `json:"why"`
}

// RowAdd is a row the step needs before its manifests: rows are declared by
// the table layer's row verb, not by a batch.
type RowAdd struct{ Table, Row string }

// Plan is what a step does: its units, in order, and the cards it refused.
type Plan struct {
	Rows    []RowAdd
	Units   []Unit
	Refused []Refusal
	// Notes the step writes with no unit (a fleet member going down with no
	// cards to deal); they are written with the first unit, or alone.
	Notes []Note
	// Closes are open judgments the step answers as a whole (--answers).
	Closes []Open
	// Updates are open judgments the step rewrites in place with the latest
	// facts (a lateness while its attempt lives): same id, new text, each a
	// line of the log.
	Updates []Note
	// Props are the table properties the step writes, each with the guard on
	// the value its plan read.
	Props []PropWrite
	// Requeue is the queued changes a drain leaves for the next one (Drain):
	// its commit puts them back at the queue's tail.
	Requeue []QueuedChange
	// pre is the pre-state the plan was built on, set only by the steps of
	// this package that may admit or move a primary into ready (on): the
	// lifecycle judges a primary's needs against it, and a plan without one
	// moves no primary into ready.
	pre *Snapshot
	// releasing says release built the plan: the one step that may land a
	// sentinel. inserting says add inserts a sentinel in front of ready
	// primaries: the one step that may move a primary ready -> waiting.
	releasing, inserting bool
	// removing says remove built the plan: the one step that takes a landed
	// primary off the table.
	removing bool
	// drained says Drain built the plan: each of its changes composes changes
	// the lifecycle already held when the steps that queued them were
	// planned, so a composed move may pass over states (review to landed, by
	// an accept and a merge queued in one tick) and is not judged again.
	drained bool
}

// PropWrite is a table property a step writes (L1 contract amendment, table
// properties): the table (logical name), the property and its new value,
// guarded on the value the plan read (Was, or WasAbsent when it read none),
// applied in the same atomic batch as the step's changes of that table.
type PropWrite struct {
	Table, Name, Value string
	Was                string
	WasAbsent          bool
}

// on records the pre-state the plan was built on (see Plan.pre).
func (p *Plan) on(s *Snapshot) { p.pre = s }

// Tables is the logical tables the plan writes, in ApplyOrder.
func (p Plan) Tables() []string {
	seen := map[string]bool{}
	for _, u := range p.Units {
		for _, c := range u.Changes {
			seen[c.Table] = true
		}
		for _, b := range u.Bumps {
			seen[b.Table] = true
		}
	}
	for _, pw := range p.Props {
		seen[pw.Table] = true
	}
	var out []string
	for _, t := range ApplyOrder {
		if seen[t] {
			out = append(out, t)
		}
	}
	return out
}

func (p *Plan) refuse(key, why string) { p.Refused = append(p.Refused, Refusal{key, why}) }

// Entry builders.

func u64(n uint64) string { return strconv.FormatUint(n, 10) }

// at guards a card at its observed place and member revision.
func at(c *Card) *ntable.MemberExpect {
	return &ntable.MemberExpect{Revision: u64(c.Rev), Place: &ntable.PlaceExpect{Row: c.Row, Col: c.Col}}
}

// moveEntry moves a card to row/col, keeping its score, with fields set.
func moveEntry(c *Card, row, col string, set map[string]string, unset ...string) ntable.BatchMemberEntry {
	e := ntable.BatchMemberEntry{ID: c.ID, Expect: at(c)}
	if row != c.Row || col != c.Col {
		e.Move = &ntable.MemberMoveOp{Row: row, Col: col}
	}
	e.Set = nonEmpty(set)
	e.Unset = unsetPresent(c, unset)
	return e
}

// setEntry changes fields of a card in place.
func setEntry(c *Card, set map[string]string, unset ...string) ntable.BatchMemberEntry {
	return ntable.BatchMemberEntry{ID: c.ID, Expect: at(c), Set: nonEmpty(set), Unset: unsetPresent(c, unset)}
}

// guardEntry checks a card at its place and revision and changes nothing.
func guardEntry(c *Card) ntable.BatchMemberEntry {
	return ntable.BatchMemberEntry{ID: c.ID, Expect: at(c)}
}

// createEntry places a new card.
func createEntry(id, row, col string, score float64, set map[string]string) ntable.BatchMemberEntry {
	return ntable.BatchMemberEntry{ID: id, Expect: &ntable.MemberExpect{Absent: true},
		Create: &ntable.MemberCreateOp{Row: row, Col: col, Score: score}, Set: nonEmpty(set)}
}

// removeEntry takes a card off the table, keeping its record, with fields set.
func removeEntry(c *Card, set map[string]string) ntable.BatchMemberEntry {
	return ntable.BatchMemberEntry{ID: c.ID, Expect: at(c), Remove: true, Set: nonEmpty(set)}
}

// scoreEntry gives a card a new score where it is.
func scoreEntry(c *Card, score float64) ntable.BatchMemberEntry {
	s := score
	return ntable.BatchMemberEntry{ID: c.ID, Expect: at(c), Move: &ntable.MemberMoveOp{Row: c.Row, Col: c.Col, Score: &s}}
}

func nonEmpty(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}

// unsetPresent keeps the unset names the card has: unsetting an absent field
// is a no-op the manifest need not carry. The card is asked through Has, as
// every read of a field is, so that on a card loaded from a read plan a field
// the plan did not load is refused and never taken for an absent one (which
// would drop the unset): a rule that unsets a field projects it.
func unsetPresent(c *Card, names []string) []string {
	var out []string
	for _, n := range names {
		if c.Has(n) {
			out = append(out, n)
		}
	}
	return out
}

func change(table string, e ntable.BatchMemberEntry) Change { return Change{Table: table, Entry: e} }

// closesFor is the open judgments of the types (every type when none is
// named) on the subject: the per-card, per-cause obligations a verb resolves
// by acting on the card. A stream-level judgment is never closed here: it
// stays open while its stream is stopped.
func closesFor(open []Open, types []string, subject string) []Open {
	var out []Open
	for _, o := range open {
		if o.Subject() != subject || o.Note.StreamLevel {
			continue
		}
		if len(types) == 0 || contains(types, o.Note.Type) {
			out = append(out, o)
		}
	}
	return out
}

// answered checks --answers: every named notification must be one the step
// resolves some obligation of (it closes, or records a decided answer to it);
// one it does not is refused by its id. Naming a notification closes nothing
// by itself.
func answered(p *Plan, s *Snapshot, ids []string, who string) {
	// Judgments are the coordinator's: an answer by anyone else refuses the
	// whole step.
	if len(ids) > 0 {
		if why := notCoordinator(s, who, "--answers"); why != "" {
			*p = Plan{Refused: []Refusal{{Key: strings.Join(ids, ","), Why: why}}}
			return
		}
	}
	// An answer of another epoch refuses the whole step: nothing moves, and
	// the refusal names the id's epoch and when the sprint was cleared.
	var other []Refusal
	for _, id := range ids {
		if e := IDEpoch(id); e != s.Epoch {
			other = append(other, Refusal{Key: id, Why: otherEpochAnswer(s, id, e)})
		}
	}
	if len(other) > 0 {
		*p = Plan{Refused: other}
		return
	}
	var bad []Refusal
	for _, id := range ids {
		known, resolved := false, false
		for _, o := range s.Open {
			known = known || o.Note.ID == id
		}
		for _, u := range p.Units {
			for _, c := range u.Closes {
				resolved = resolved || c.Note.ID == id
			}
		}
		for _, c := range p.Closes {
			resolved = resolved || c.Note.ID == id
		}
		for _, u := range p.Units {
			for _, n := range u.Notes {
				resolved = resolved || n.Kind == Decided && n.Answers == id
			}
		}
		for _, n := range p.Notes {
			resolved = resolved || n.Kind == Decided && n.Answers == id
		}
		switch {
		case !known:
			bad = append(bad, Refusal{Key: id, Why: noJudgment(s, id) + "; the whole step is refused and nothing was changed"})
		case !resolved:
			bad = append(bad, Refusal{Key: id, Why: "this step resolves no obligation of " + id + "; the whole step is refused and nothing was changed"})
		}
	}
	// A verb refused has written nothing: one refused answer refuses the
	// whole step.
	if len(bad) > 0 {
		*p = Plan{Refused: append(p.Refused, bad...)}
	}
}

// decided is the answer to a judgment that stays open (a stopped stream's,
// until it resumes): recorded, not closed.
func decided(o Open, what, who string, now time.Time, primaries ...string) Note {
	return Note{Kind: Decided, Type: o.Note.Type, Stream: o.Note.Stream, Answers: o.Note.ID, What: what, Who: who, At: now,
		Primaries: primaries, Count: len(primaries)}
}

// setStream is the stream's control card changed by a step, carried by the
// step's first unit of that stream: the card is changed once per step, so a
// second change of it in the plan is merged into the first.
func setStream(p *Plan, s *Snapshot, stream string, set map[string]string, notes ...Note) {
	ctl := s.StreamCtl(stream)
	if ctl == nil || len(set) == 0 {
		return
	}
	first := -1
	for i := range p.Units {
		if p.Units[i].Stream != stream {
			continue
		}
		if first < 0 {
			first = i
		}
		for j, c := range p.Units[i].Changes {
			if c.Table == Merge && c.Entry.ID == ctl.ID {
				merged := map[string]string{}
				for k, v := range c.Entry.Set {
					merged[k] = v
				}
				for k, v := range set {
					merged[k] = v
				}
				p.Units[i].Changes[j].Entry.Set = merged
				p.Units[first].Notes = append(p.Units[first].Notes, notes...)
				return
			}
		}
	}
	if first >= 0 {
		p.Units[first].Changes = append(p.Units[first].Changes, change(Merge, setEntry(ctl, set)))
		p.Units[first].Notes = append(p.Units[first].Notes, notes...)
	}
}

// Lists says a judgment's decisions list the verb (a decision starts with it).
func Lists(n Note, verb string) bool {
	if verb == "return" && (n.Type == NRed || n.Type == NRejected) {
		return true // "take the suspect off" is a return
	}
	for _, d := range n.Decisions {
		if d == verb || strings.HasPrefix(d, verb+" ") {
			return true
		}
	}
	return false
}

// answerListed records the unit's answer to every stream-level judgment
// named by --answers, open on the stream, whose decisions list the verb: the
// judgment stays open while its stream is stopped, and the answer is kept.
func answerListed(u *Unit, open []Open, answers []string, verb, stream, what, who string, now time.Time, primary string) {
	for _, o := range open {
		if o.Note.StreamLevel && o.Note.Stream == stream && contains(answers, o.Note.ID) && Lists(o.Note, verb) && !answeredIn(u.Notes, o.Note.ID) {
			u.Notes = append(u.Notes, decided(o, what, who, now, primary))
		}
	}
}

// otherEpochAnswer is the refusal of a step that answers a judgment of
// another epoch.
func otherEpochAnswer(s *Snapshot, id string, e uint64) string {
	if e > s.Epoch {
		return OtherEpoch(id, e, s.Epoch)
	}
	when := "an earlier clear"
	if !s.Cleared.IsZero() {
		when = s.Cleared.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("--answers %s names a judgment of epoch %d; the sprint was cleared at %s and its epoch is now %d; the whole step is refused and nothing was changed; run: nova-sprint inbox", id, e, when, s.Epoch)
}

// Applied is what the store applies of a step's plan: the plan held to the
// lifecycle (Lawful) and to one judgment per cause (OnePerCause), on the
// state the step read.
func Applied(s *Snapshot, p Plan) Plan { return OnePerCause(s, Lawful(p)) }

// OnePerCause keeps a judgment open once per card and cause: a note of a
// plan that would open a judgment of a type already open on a subject (and
// not closed by the plan) leaves that subject out; a note left with no
// subject is not written. A stream's and the sprint's judgments are as they
// are written.
func OnePerCause(s *Snapshot, p Plan) Plan {
	closing := map[string]bool{}
	for _, u := range p.Units {
		for _, o := range u.Closes {
			closing[o.Key] = true
		}
	}
	for _, o := range p.Closes {
		closing[o.Key] = true
	}
	// A cause is the type, and for a judgment that names needs, the needs
	// it names: a need dropped later is its own judgment; for one that names
	// its card (a late read or work card), the card: each is its own.
	cause := func(n Note, sub string) string {
		return n.Type + "|" + strings.Join(n.Needs, ",") + "|" + n.Card + "|" + sub
	}
	open := map[string]bool{}
	for _, o := range s.Open {
		if o.Note.Kind == Judgment && !closing[o.Key] {
			open[cause(o.Note, o.Subject())] = true
		}
	}
	keep := func(notes []Note) []Note {
		var out []Note
		for _, n := range notes {
			if n.Kind != Judgment || n.StreamLevel || n.SprintLevel || len(n.Primaries) == 0 {
				out = append(out, n)
				continue
			}
			var subs []string
			for _, sub := range n.Primaries {
				if !open[cause(n, sub)] {
					subs = append(subs, sub)
				}
			}
			if len(subs) == 0 {
				continue
			}
			n.Primaries, n.Count = subs, len(subs)
			out = append(out, n)
		}
		return out
	}
	for i := range p.Units {
		p.Units[i].Notes = keep(p.Units[i].Notes)
	}
	p.Notes = keep(p.Notes)
	return p
}

// notCoordinator is why who may not answer a judgment: judgments are the
// sprint's coordinator's (init --coordinator), as release is. "" is may.
func notCoordinator(s *Snapshot, who, verb string) string {
	if who == "" {
		who = s.Actor
	}
	return NotCoordinator(s.Coordinator, who, verb)
}

// NotCoordinator is why who may not answer a judgment of a sprint whose
// coordinator is coordinator; "" is may.
func NotCoordinator(coordinator, who, verb string) string {
	s := struct{ Coordinator string }{coordinator}
	switch {
	case s.Coordinator == "":
		return verb + " answers a judgment, and the sprint has no coordinator; judgments are the coordinator's (init --coordinator)"
	case who != s.Coordinator:
		return verb + " answers a judgment, which is the coordinator's alone: " + s.Coordinator + ", not " + orDash(who) + "; nothing was changed"
	}
	return ""
}

// PlanRows is the rows of each table a plan changes: the row a card is moved
// from and the row it goes to, the row a card is created in, and the row of a
// card whose fields it sets, unsets or removes; a guard that changes nothing
// names no row. The tick's log names them by table (errata 3 amendment 10:
// every row of every table moves every tick).
func PlanRows(p Plan) map[string][]string {
	out := map[string][]string{}
	seen := map[string]bool{}
	add := func(table, row string) {
		if row == "" || seen[table+"\x00"+row] {
			return
		}
		seen[table+"\x00"+row] = true
		out[table] = append(out[table], row)
	}
	for _, r := range p.Rows {
		add(r.Table, r.Row)
	}
	for _, u := range p.Units {
		for _, c := range u.Changes {
			e := c.Entry
			writes := e.Move != nil || e.Create != nil || e.Remove || len(e.Set) > 0 || len(e.Unset) > 0
			if !writes {
				continue
			}
			if e.Expect != nil && e.Expect.Place != nil {
				add(c.Table, e.Expect.Place.Row)
			}
			if e.Move != nil {
				add(c.Table, e.Move.Row)
			}
			if e.Create != nil {
				add(c.Table, e.Create.Row)
			}
		}
	}
	for t := range out {
		sort.Strings(out[t])
	}
	return out
}

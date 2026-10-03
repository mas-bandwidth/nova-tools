package sprint

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// The steps that move primaries through the work table and the fleet: add,
// resolve, start, take, finish, and the fleet's own moves. Each is a pure
// function of an observed snapshot and a request; it returns the plan (the
// manifests' entries, per card, and the notifications the moves cause) or a
// refusal per card. Nothing here reads a store or a clock.

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// CardAdd is one card of the many-brief add form: its id, brief and needs.
// File names the brief file the card came from, for a refusal that names it.
type CardAdd struct {
	ID    string
	Brief string
	// Rules is the held rules file the member injects into this card at stage time
	// (FieldRules), "" when the brief carries its own.
	Rules string
	Needs []string
	File  string
	// Sentinel marks this card a sentinel (a stop), not a primary: the
	// many-brief form's --sentinel <id>, admitted after the brief cards.
	Sentinel bool
}

// AddReq admits primaries into a stream.
type AddReq struct {
	Stream string
	IDs    []string
	Count  int // generate this many ids, <stream>-<n>
	Needs  []string
	Brief  string
	Rules  string // the held rules file of every card the add admits with Brief (FieldRules)
	// Cards, when set, is the many-brief form: one card per entry, in order,
	// each with its own brief and needs (a need names a primary already on
	// the table or one of this add). IDs, Count, Brief and Needs are then
	// empty.
	Cards []CardAdd
	Score *float64 // the first primary's score; the rest follow it
	// Sentinel admits one sentinel (IDs names it): a stop in the stream that
	// the coordinator releases (docs/SPEC-SPRINT.md, sentinel cards).
	Sentinel bool
	// Before or After places the cards in line, in front of or after this
	// card of the stream: their scores lie between its and its neighbour's.
	Before, After string
	// Every, with Count, puts a sentinel <stream>-gate-<n> after every Every
	// cards, none after the last unless Last: a stream in stops, one step.
	Every int
	Last  bool
	// Held admits every card held (IsHeld): waiting, a sentinel never
	// reached, nothing dealt, until the coordinator's release.
	Held bool
	Only []string
	Who  string
}

// gatePrefix is the prefix of the sentinels add --sentinel-every names.
func gatePrefix(stream string) string { return stream + "-gate-" }

// IsGate says the id is a sentinel this add names by --sentinel-every.
func (r AddReq) IsGate(id string) bool {
	return r.Every > 0 && strings.HasPrefix(id, gatePrefix(r.Stream))
}

// AddIDs is the ids an add admits: the named ones, or Count generated ones
// numbered after the highest the stream has.
func AddIDs(s *Snapshot, r AddReq) []string {
	if r.Only != nil {
		return r.Only
	}
	if len(r.Cards) > 0 {
		out := make([]string, len(r.Cards))
		for i, c := range r.Cards {
			out[i] = c.ID
		}
		return out
	}
	if len(r.IDs) > 0 || r.Count <= 0 {
		return r.IDs
	}
	high, gates := 0, 0
	prefix, gp := r.Stream+"-", gatePrefix(r.Stream)
	for _, c := range s.Work.Cards() {
		id := c.ID
		if n, err := strconv.Atoi(strings.TrimPrefix(id, prefix)); err == nil && strings.HasPrefix(id, prefix) && n > high {
			high = n
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(id, gp)); err == nil && strings.HasPrefix(id, gp) && n > gates {
			gates = n
		}
	}
	out := make([]string, 0, r.Count+r.Count/max(r.Every, 1))
	for i := 1; i <= r.Count; i++ {
		out = append(out, fmt.Sprintf("%s-%d", r.Stream, high+i))
		if r.Every > 0 && i%r.Every == 0 && (i < r.Count || r.Last) {
			gates++
			out = append(out, fmt.Sprintf("%s%d", gp, gates))
		}
	}
	return out
}

// Add admits primaries: waiting if they need something not landed, else
// ready. A need names a primary on the table (placed or kept) or one of this
// add. A stream's sentinels stop it by position: a card placed after an
// unlanded sentinel of its stream waits on the latest such sentinel, and a
// card placed in front of one is a need of it. A sentinel waits for every
// primary of its stream that sorts before it and has not landed; inserted in
// front of cards already in line, those waiting wait on it too, those ready go
// back to waiting, and those in flight are past the stop: it waits for them as
// well. It is one step: a cycle of needs refuses the whole add.
func Add(s *Snapshot, r AddReq) Plan {
	var p Plan
	p.on(s)
	ids := AddIDs(s, r)
	refuseAll := func(why string) Plan {
		var q Plan
		for _, id := range ids {
			q.refuse(id, why)
		}
		return q
	}
	if !ValidID(r.Stream) {
		return refuseAll(fmt.Sprintf("stream %q wants letters, digits, _ and -", r.Stream))
	}
	if RemovedStream(s, r.Stream) {
		return refuseAll(fmt.Sprintf("stream %s was removed in this epoch (stream remove), and the table layer never places its control card again; nothing was changed; add under another stream, or run: nova-sprint clear --confirm sprint, then add", r.Stream))
	}
	if r.Sentinel && len(ids) != 1 {
		return refuseAll("a sentinel is admitted one at a time: add --stream <s> --sentinel <id>")
	}
	if r.Every > 0 && (r.Count <= 0 || r.Sentinel || r.Before != "" || r.After != "" || len(r.IDs) > 0) {
		return refuseAll("--sentinel-every goes with --count, at the end of the stream")
	}
	scores, why := addScores(s, r, len(ids))
	if why != "" {
		return refuseAll(why)
	}
	for _, t := range []string{Work, Merge} {
		if !s.T(t).HasRow(r.Stream) {
			p.Rows = append(p.Rows, RowAdd{t, r.Stream})
		}
	}
	ctl := s.Merge.Card(CtlID(r.Stream))
	var head []Change
	switch {
	case ctl == nil:
		head = append(head, change(Merge, createEntry(CtlID(r.Stream), r.Stream, Ctl, 0,
			map[string]string{"kind": "stream", "state": StreamWaiting, "since": stamp(s.Now)})))
	case ctl.F("state") == StreamLanded:
		head = append(head, change(Merge, setEntry(ctl, map[string]string{"state": StreamWaiting, "since": stamp(s.Now)})))
	}
	adding := map[string]bool{}
	for _, id := range ids {
		if ValidID(id) && !strings.HasPrefix(id, "ctl-") {
			adding[id] = true
		}
	}
	// needsOf and briefOf are the needs and brief of the i'th card admitted:
	// its own in the many-brief form, else the add's one for every card.
	needsOf := func(i int) []string {
		if len(r.Cards) > 0 {
			return r.Cards[i].Needs
		}
		return r.Needs
	}
	briefOf := func(i int) string {
		if len(r.Cards) > 0 {
			return r.Cards[i].Brief
		}
		return r.Brief
	}
	rulesOf := func(i int) string {
		if len(r.Cards) > 0 {
			return r.Cards[i].Rules
		}
		return r.Rules
	}
	// isSent says the i'th card admitted is a sentinel: the one --sentinel form,
	// or a card of the many-brief form marked one (its --sentinel <id>).
	isSent := func(i int) bool {
		if r.Sentinel {
			return true
		}
		return len(r.Cards) > 0 && r.Cards[i].Sentinel
	}
	// The cards admitted, each with its score, needs and brief.
	type admit struct {
		id     string
		score  float64
		needs  []string
		brief  string
		rules  string // FieldRules
		behind string // the sentinel it waits behind by position
		gate   bool   // a stop of --sentinel-every
		sent   bool   // a stop: --sentinel or a many-brief card marked one
	}
	var in []admit
	lastGate := ""
	seen := map[string]bool{}
	for i, id := range ids {
		needs := append([]string(nil), needsOf(i)...)
		// missing is the needs that name no primary they may name: one on the
		// table or one of this add (a cycle is refused below).
		var missing []string
		for _, n := range needs {
			if s.Work.Card(n) == nil && !adding[n] {
				missing = append(missing, n)
			}
		}
		switch {
		case seen[id]:
			p.refuse(id, "named twice")
			continue
		case !ValidID(id) || strings.HasPrefix(id, "ctl-"):
			p.refuse(id, "an id wants letters, digits, _ and -, and does not start with ctl-")
			continue
		case s.Work.Card(id) != nil:
			p.refuse(id, "exists already ("+placeWord(s.Work.Card(id))+")")
			continue
		case len(missing) > 0:
			if len(r.Cards) > 0 {
				p.refuse(id, fmt.Sprintf("%s: needs %s, which is no primary on the table or in this add", r.Cards[i].File, strings.Join(missing, ",")))
			} else {
				p.refuse(id, "needs "+strings.Join(missing, ",")+", which is no primary on the table or in this add")
			}
			continue
		}
		seen[id] = true
		a := admit{id: id, score: scores[i], needs: needs, brief: briefOf(i), rules: rulesOf(i), gate: r.IsGate(id), sent: isSent(i)}
		if st := sentinelBefore(s, r.Stream, a.score); st != nil && !a.sent {
			a.behind = st.ID // it waits behind the stop by its place; nothing is written of it
		}
		if lastGate != "" && !a.gate {
			a.behind = lastGate // behind a stop of this add
		}
		if a.gate {
			a.needs = nil // a stop names nothing: it waits by its place
			lastGate = id
		}
		in = append(in, a)
	}
	// What the admitted change in the cards already in line.
	mods := map[string]*mod{}
	var past, pulled []string
	if r.Sentinel && len(in) == 1 {
		st := &in[0]
		// The sentinel waits for what is before it and what is in flight
		// past it by its place in line; nothing of either is written.
		for _, c := range streamLine(s, r.Stream) {
			switch {
			case c.Col == Landed:
			case c.Score < st.score:
				if !IsSentinel(c) && c.Col != Waiting && (c.Col != Ready || withdrawnCard(s, c)) {
					past = append(past, c.ID)
				}
			case c.Col == Ready && !withdrawnCard(s, c) && !IsSentinel(c):
				m := modOf(mods, c)
				m.to = Waiting
				pulled = append(pulled, c.ID)
			case IsSentinel(c) && c.F("reached") != "":
				// a later stop now waits for this one too: no longer reached
				modOf(mods, c).unreach = st.id
			case c.Col == Waiting:
			default: // in flight: past the stop, and the sentinel waits for it
				past = append(past, c.ID)
			}
		}
	} else if len(in) > 0 {
		// A card placed in front of an unlanded sentinel is a need of it.
		if st := sentinelAfter(s, r.Stream, in[0].score); st != nil && st.F("reached") != "" {
			modOf(mods, st).unreach = in[0].id // it waits for them by their place
		}
	}
	// The cycle check walks what the cards would wait for with the add in
	// place: the needs they name, and the needs of their places in line (a
	// card behind a sentinel needs it; a sentinel needs every card of its
	// stream before it). Only a cycle through a card the add places or
	// changes is one it closes (errata 3 amendment 7; design section 3, add).
	edges := map[string][]string{}
	var placed []*Card
	var roots []string
	for _, a := range in {
		edges[a.id] = a.needs
		col := Ready
		if a.behind != "" || a.gate || a.sent {
			col = Waiting
		}
		for _, n := range a.needs {
			if s.StateOf(n) != Landed {
				col = Waiting
			}
		}
		fields := map[string]string{"kind": "primary"}
		if a.gate || a.sent {
			fields["kind"] = "sentinel"
		}
		placed = append(placed, &Card{ID: a.id, Row: r.Stream, Col: col, Score: a.score, Fields: fields})
		roots = append(roots, a.id)
	}
	var changed []string
	for id := range mods {
		edges[id] = Split(s.Work.Card(id).F("needs"))
		changed = append(changed, id)
	}
	sort.Strings(changed)
	waits := map[string]bool{}
	for _, id := range pulled {
		waits[id] = true
	}
	g := newNeedGraph(s, edges, placed, waits)
	if cycle := g.closes(append(roots, changed...)); cycle != nil {
		return refuseAll("the needs would make a cycle: " + g.tellLoop(cycle) + "; nothing is written")
	}
	if len(pulled) > 0 {
		p.inserting = true
	}
	for _, a := range in {
		col := Ready
		for _, n := range a.needs {
			if s.StateOf(n) != Landed {
				col = Waiting
			}
		}
		if a.behind != "" {
			col = Waiting
		}
		kind := "primary"
		if a.sent || a.gate {
			kind, col = "sentinel", Waiting
		}
		if r.Held {
			col = Waiting
		}
		fields := map[string]string{"kind": kind, "stream": r.Stream, "attempt": "0", "admitted": stamp(s.Now)}
		if r.Held {
			fields[FieldHeld] = stamp(s.Now)
		}
		if a.brief != "" && !a.gate {
			fields["brief"] = a.brief
			if a.rules != "" {
				fields[FieldRules] = a.rules
			}
		}
		if len(a.needs) > 0 {
			fields["needs"] = strings.Join(a.needs, ",")
		}
		u := Unit{Key: a.id, Stream: r.Stream, Moved: fmt.Sprintf("%s -> %s stream=%s score=%s", a.id, col, r.Stream, fmtScore(a.score))}
		if a.gate {
			u.Moved = "sentinel " + u.Moved
		}
		if r.Held {
			u.Moved += "; held until release"
		}
		if r.Sentinel {
			u.Moved = "sentinel " + u.Moved
			var open []string
			for _, n := range a.needs {
				if s.StateOf(n) != Landed {
					open = append(open, n)
				}
			}
			for _, c := range streamLine(s, r.Stream) {
				if c.Col != Landed && c.Score < a.score {
					open = append(open, c.ID)
				}
			}
			// reached only after something: a stop with nothing before it is simply next
			if len(open) == 0 && !r.Held && (len(a.needs) > 0 || anyBefore(s, r.Stream, a.id, a.score) || workInFlight(s, nil) == "") {
				fields["reached"] = stamp(s.Now)
				u.Notes = append(u.Notes, reachedNote(s, &Card{ID: a.id, Row: r.Stream}, nil, len(pulled), r.Who))
				u.Moved += "; reached"
			}
			if len(pulled) > 0 {
				u.Moved += "; " + strings.Join(pulled, ",") + " ready -> waiting behind it"
			}
			if len(past) > 0 {
				u.Moved += "; already past the stop: " + strings.Join(past, ",")
			}
		} else if a.sent {
			// the many-brief form's sentinel: a stop after the cards, marked
			// reached by the tick once what sorts before it has landed.
			u.Moved = "sentinel " + u.Moved
		}
		if a.behind != "" {
			u.Moved += "; waits behind sentinel " + a.behind
		}
		u.Changes = append(head, change(Work, createEntry(a.id, r.Stream, col, a.score, fields)))
		if gone := droppedNeeds(s, a.needs); len(gone) > 0 {
			u.Notes = append(u.Notes, blockedNote(s, r.Stream, a.id, r.Who, gone))
		}
		head = nil
		p.Units = append(p.Units, u)
	}
	if len(p.Units) > 0 {
		last := &p.Units[len(p.Units)-1]
		for _, c := range streamLine(s, r.Stream) {
			if m := mods[c.ID]; m != nil {
				last.Changes = append(last.Changes, m.change(c))
				if m.unreach != "" {
					for _, o := range closesFor(s.Open, []string{NSentinelReached}, c.ID) {
						last.Closes = append(last.Closes, o)
						last.Notes = append(last.Notes, decided(o, "no longer reached: "+m.unreach+" was placed before it", r.Who, s.Now, c.ID))
					}
					last.Moved += "; sentinel " + c.ID + " is no longer reached"
				} else if !r.Sentinel {
					last.Moved += "; sentinel " + c.ID + " waits for it too"
				}
			}
		}
	}
	if head != nil && len(p.Units) == 0 && len(p.Refused) == 0 {
		p.Units = append(p.Units, Unit{Key: CtlID(r.Stream), Changes: head, Moved: "stream " + r.Stream + " open"})
	}
	// More work: the sprint is not done. An add that only opens a stream
	// admits no card, and leaves it done.
	if admits(p) {
		for _, o := range s.Open {
			if o.Note.Type == NSprintDone {
				p.Units[0].Closes = append(p.Units[0].Closes, o)
			}
		}
	}
	return p
}

// AddEach is one add of several streams, one step: each stream's add on the
// same pre-state, their plans as one.
func AddEach(s *Snapshot, rs []AddReq) Plan {
	var p Plan
	p.on(s)
	closed := map[string]bool{}
	for _, r := range rs {
		q := Add(s, r)
		p.Rows = append(p.Rows, q.Rows...)
		p.Refused = append(p.Refused, q.Refused...)
		p.Notes = append(p.Notes, q.Notes...)
		p.inserting = p.inserting || q.inserting
		for _, u := range q.Units {
			var keep []Open
			for _, o := range u.Closes {
				if !closed[o.Key] {
					closed[o.Key] = true
					keep = append(keep, o)
				}
			}
			u.Closes = keep
			p.Units = append(p.Units, u)
		}
	}
	return p
}

// admits says the plan places a card on the work table.
func admits(p Plan) bool {
	for _, u := range p.Units {
		for _, c := range u.Changes {
			if c.Table == Work && c.Entry.Create != nil {
				return true
			}
		}
	}
	return false
}

// WaitsFor is what a primary still waits for: its needs that have not landed
// (before the step, or in it: landing) and that the coordinator did not waive.
func WaitsFor(s *Snapshot, c *Card, landing map[string]bool) []string {
	out := NamedWaits(s, c, landing)
	for _, n := range PositionWaits(s, c, landing) {
		if !contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// NamedWaits is what a primary waits for among the needs it names (stored on
// it): the part of WaitsFor that is not its place in line.
func NamedWaits(s *Snapshot, c *Card, landing map[string]bool) []string {
	var out []string
	waived := Split(c.F("waived"))
	for _, n := range Split(c.F("needs")) {
		if s.StateOf(n) != Landed && !landing[n] && !contains(waived, n) {
			out = append(out, n)
		}
	}
	return out
}

// NeedState is one need of a primary as it stands: the need's state (its
// column, or off the table with its outcome), and whether it was waived.
type NeedState struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Waived   bool   `json:"waived,omitempty"`
	WaivedBy string `json:"waived_by,omitempty"` // who waived the primary's needs last, and when
	WaivedAt string `json:"waived_at,omitempty"`
}

// NeedsOf is each need of the primary with its state, and the primaries on
// the table that need it (with the needs read as records into s).
func NeedsOf(s *Snapshot, id string) (needs []NeedState, neededBy []string) {
	c := s.Work.Card(id)
	named := Split(c.F("needs"))
	for _, n := range PositionWaits(s, c, nil) {
		if !contains(named, n) {
			named = append(named, n)
		}
	}
	for _, n := range named {
		st := "not on the table"
		if nc := s.Work.Card(n); nc.Placed() {
			st = nc.Col
		} else if nc != nil {
			st = "off the table (" + orDash(nc.F("outcome")) + ")"
		}
		ns := NeedState{ID: n, State: st, Waived: contains(Split(c.F("waived")), n)}
		if ns.Waived {
			ns.WaivedBy, ns.WaivedAt = c.F("waived_by"), c.F("waived_at")
		}
		needs = append(needs, ns)
	}
	for _, o := range s.Work.Column(States...) {
		if contains(Split(o.F("needs")), id) {
			neededBy = append(neededBy, o.ID)
		}
	}
	if c.Placed() && IsSentinel(c) {
		for _, b := range Behind(s, c) {
			if !contains(neededBy, b.ID) {
				neededBy = append(neededBy, b.ID)
			}
		}
	}
	return needs, neededBy
}

// droppedNeeds is the needs that name a primary dropped off the table.
func droppedNeeds(s *Snapshot, needs []string) []string {
	var out []string
	for _, n := range needs {
		if c := s.Work.Card(n); c != nil && !c.Placed() && c.F("outcome") == "dropped" {
			out = append(out, n)
		}
	}
	return out
}

// missingNeeds names dependencies with no record at all. Resolve reads the
// unplaced dependencies too, so kept dropped records remain a distinct case.
func missingNeeds(s *Snapshot, needs []string) []string {
	var out []string
	for _, n := range needs {
		if s.Work.Card(n) == nil {
			out = append(out, n)
		}
	}
	return out
}

func placeWord(c *Card) string {
	if c.Placed() {
		return c.Row + ":" + c.Col
	}
	return "kept, " + orDash(c.F("outcome"))
}

func fmtScore(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

// ResolveReq moves waiting primaries whose needs have landed.
type ResolveReq struct {
	Sel
	Who string
}

// ResolveExtras is the needs a resolve must read as records: the ones not on
// the table, which may have been dropped.
func ResolveExtras(s *Snapshot) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range s.Work.Column(Waiting) {
		for _, n := range Split(c.F("needs")) {
			if s.Work.Placed(n) == nil && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

// Resolve moves waiting -> ready where every need has landed. A need that was
// dropped or missing is a judgment for the coordinator, once.
func Resolve(s *Snapshot, r ResolveReq) Plan { return Lawful(resolvePlan(s, r)) }

func resolvePlan(s *Snapshot, r ResolveReq) Plan {
	var p Plan
	p.on(s)
	chosen := pick(&p, r.Sel, s.Work.Column(Waiting), rowOf, func(c *Card) string { return inState(c, Waiting) }, s.primaryCard)
	// each card's open judgments, found by an index built once, not by a
	// walk of every open judgment for each card (the owner's rule: never a
	// row at a time); each list keeps the judgments' order
	bySubject := map[string][]Open{}
	for _, o := range s.Open {
		bySubject[o.Subject()] = append(bySubject[o.Subject()], o)
	}
	for _, c := range chosen {
		// A missing prerequisite that now exists is no longer a missing-need
		// judgment; it still has to land before the primary can move.
		for _, o := range bySubject[c.ID] {
			if o.Note.Type == NMissingNeed && o.Subject() == c.ID && len(o.Note.Needs) > 0 && len(missingNeeds(s, o.Note.Needs)) == 0 {
				p.Closes = append(p.Closes, o)
			}
		}
		var waits, dropped, missing []string
		for _, n := range WaitsFor(s, c, nil) {
			if s.Work.Card(n) == nil {
				missing = append(missing, n)
			} else if len(droppedNeeds(s, []string{n})) > 0 {
				dropped = append(dropped, n)
			} else {
				waits = append(waits, n)
			}
		}
		if len(missing) > 0 {
			if left := unblocked(bySubject[c.ID], c.ID, missing, NMissingNeed); len(left) > 0 {
				n := judgment(NMissingNeed, c.Row, s.Now, 0, c.ID)
				n.What, n.Who, n.Needs = c.ID+" needs "+Preview(left, ",")+", not on the table", r.Who, left
				p.Notes = append(p.Notes, n)
			}
			if len(r.IDs) > 0 {
				p.refuse(c.ID, "needs "+strings.Join(missing, ",")+", not on the table")
			}
		}
		if len(dropped) > 0 {
			if left := unblocked(bySubject[c.ID], c.ID, dropped, NBlocked); len(left) > 0 {
				p.Notes = append(p.Notes, blockedNote(s, c.Row, c.ID, r.Who, left))
			}
			if len(r.IDs) > 0 {
				p.refuse(c.ID, "needs "+strings.Join(dropped, ",")+", which was dropped")
			}
			continue
		}
		if len(missing) > 0 {
			continue
		}
		if len(waits) > 0 {
			if len(r.IDs) > 0 {
				p.refuse(c.ID, "waits for "+strings.Join(waits, ","))
			}
			continue
		}
		if IsHeld(c) { // held, never reached or ready: the coordinator releases it
			if len(r.IDs) > 0 {
				p.refuse(c.ID, "held (add --held): the coordinator releases it: nova-sprint release "+c.ID+" --reason <text>")
			}
			continue
		}
		if IsSentinel(c) { // reached, never ready: the coordinator releases it
			if c.F("reached") == "" && Reachable(s, c, nil) {
				p.Units = append(p.Units, reachUnit(s, c, nil, r.Who))
			} else if len(r.IDs) > 0 {
				p.refuse(c.ID, "a reached sentinel: the coordinator releases it: nova-sprint release "+c.ID+" --reason <text>")
			}
			continue
		}
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, moveEntry(c, c.Row, Ready, nil))},
			Moved: c.ID + " waiting -> ready"})
	}
	return p
}

// resolveAfter is resolve as a trigger of a step that lands primaries
// (landing, by id): every waiting primary whose needs have all landed, with
// this step's, moves to ready in the same step; a sentinel is marked reached
// instead, and waits for the coordinator's release.
func resolveAfter(s *Snapshot, landing map[string]bool, who string) []Unit {
	var out []Unit
	for _, c := range s.Work.Column(Waiting) {
		if landing[c.ID] || IsHeld(c) || len(WaitsFor(s, c, landing)) > 0 {
			continue
		}
		if IsSentinel(c) {
			if c.F("reached") == "" && Reachable(s, c, landing) {
				out = append(out, reachUnit(s, c, landing, who))
			}
			continue
		}
		out = append(out, Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, moveEntry(c, c.Row, Ready, nil))},
			Moved: c.ID + " waiting -> ready (its needs landed)"})
	}
	return out
}

// DealReq cuts and deals work cards for ready primaries.
type DealReq struct {
	Sel
	Who string
}

// Deal moves ready -> working: for each primary, in work order, its work
// card is dealt to the next member round the fleet (round.go, errata 3
// amendment 5): the first from the rolling index, wrapping, that is up and
// holds fewer work cards, ready and working, than its width (width.go, errata
// 3 amendment 9; tla/DirtyTick.tla, Room and WidthRespected); a card no up
// member has room for is refused, never dealt past a width; the index (the
// fleet table's deal_index, a counter) moves past the member dealt to, written
// with the deal.
// Every card of the selection is dealt in the one plan, one card at a time
// round the fleet. A card withdrawn because no member was up is the
// same card dealt again at a new generation, its attempt unchanged; otherwise
// the next attempt's card is cut.
func Deal(s *Snapshot, r DealReq) Plan {
	rr, ri := dealRound(s), routeIndexesOf(s)
	p, moves := dealPlan(s, r, rr, ri)
	p = Lawful(p)
	roundWrites(&p, rr, moves)
	// each tier's route index moves by the cards dealt on it (route.go)
	ri.write(&p)
	// the streams take turns from the work table's stream index (round.go,
	// errata 3 amendment 10): it moves past the stream of the last card dealt
	streamIndexWrite(&p, streamRound(s, PropStreamIndex), s.Work.Placed)
	return p
}

// noRoomWhy is the refusal of a card every up member is at its room for (DealAhead times its
// width, width.go): the tick deals it when one has room.
const noRoomWhy = "every up fleet member is at its room (DealAhead times its width): the tick deals it when one has room"

func dealPlan(s *Snapshot, r DealReq, rr *round, ri routeIndexes) (Plan, roundMoves) {
	var p Plan
	moves := roundMoves{}
	ready := func(c *Card) string { return inState(c, Ready) }
	chosen := pick(&p, r.Sel, eligibleTurns(s.Work.Column(Ready), ready, streamRound(s, PropStreamIndex)), rowOf, ready, s.primaryCard)
	up := s.UpMembers()
	if len(up) == 0 {
		for _, c := range chosen {
			p.refuse(c.ID, "no fleet member is up: a member is up while its machine beats; start nova-sprint fleet beat <member> on a machine, or release a hold with nova-sprint fleet up <member>")
		}
		return p, moves
	}
	q, widths := memberLoads(s, up), memberWidths(s, up)
	next := func() string { return rr.next(up, q, widths, "") }
	for _, c := range chosen {
		if wc := s.Fleet.Placed(WorkCardID(c.ID, c.Int("attempt"))); wc != nil && wc.Col == Withdrawn {
			if redealBound(wc) {
				p.refuse(c.ID, fmt.Sprintf("%s was redealt %d times, its bound: rework it with a fix, or drop it", wc.ID, wc.Int("redeals")))
				continue
			}
			if _, why := s.noRoute(c); why != "" {
				p.refuse(c.ID, why)
				continue
			}
			// a member that refused it at staging is not dealt it again (StagingRefusers)
			m := next()
			if refused := StagingRefusers(wc); len(refused) > 0 {
				others := without(up, refused)
				if len(others) == 0 {
					p.refuse(c.ID, fmt.Sprintf("%s was refused at staging by every member up (%s): rework it with a fix, or drop it", wc.ID, strings.Join(refused, ", ")))
					continue
				}
				m = rr.next(others, q, widths, "")
			}
			if m == "" {
				p.refuse(c.ID, noRoomWhy)
				continue
			}
			u, why := redeal(s, c, wc, m, q, ri)
			if why != "" {
				p.refuse(c.ID, why)
				continue
			}
			rr.moved(m)
			moves[c.ID] = m
			p.Units = append(p.Units, u)
			continue
		}
		if _, why := s.noRoute(c); why != "" {
			p.refuse(c.ID, why)
			continue
		}
		m := next()
		if m == "" {
			p.refuse(c.ID, noRoomWhy)
			continue
		}
		u, why := deal(s, c, c.F("fix"), m, q, ri, nil, map[string]string{"finding": c.F("finding"), "why": c.F("why")})
		if why != "" {
			p.refuse(c.ID, why)
			continue
		}
		rr.moved(m)
		moves[c.ID] = m
		p.Units = append(p.Units, u)
	}
	return p, moves
}

// deal cuts the primary's next attempt's work card, carrying the fix and the
// primary's score, into the ready queue of the up member m (the next round the
// fleet, a deal's or a rework's), at generation 1, on the route at its tier's
// index (ri, moved past it: route.go), and moves
// the primary to working with set; given is more fields of the work card.
func deal(s *Snapshot, c *Card, fix, m string, q map[string]int, ri routeIndexes, set, given map[string]string, unset ...string) (Unit, string) {
	attempt := c.Int("attempt") + 1
	card := WorkCardID(c.ID, attempt)
	if s.Fleet.Card(card) != nil {
		return Unit{}, "work card " + card + " exists already"
	}
	route, _, why := s.routeOf(c, nil, ri)
	if why != "" {
		return Unit{}, why
	}
	q[m]++
	fields := map[string]string{"kind": "work", "primary": c.ID, "stream": c.Row, "attempt": itoa(attempt), "gen": "1", "member": m,
		"dealt": stamp(s.Now), "first_dealt": stamp(s.Now), "untaken_since": stamp(s.Now)}
	if fix != "" {
		fields["fix"] = fix
	}
	for k, v := range given { // what a rework adds on the attempt's work card: its finding and why
		if v != "" {
			fields[k] = v
		}
	}
	if set == nil {
		set = map[string]string{}
	}
	work, primary := splitRoute(route)
	for k, v := range work {
		if v != "" {
			fields[k] = v
		}
	}
	maps.Copy(set, primary)
	set["attempt"], set["work"] = itoa(attempt), card
	return Unit{Key: c.ID, Stream: c.Row, Changes: []Change{
		change(Fleet, createEntry(card, m, Ready, c.Score, fields)),
		change(Work, moveEntry(c, c.Row, Working, set, append(unset, "result")...)),
	}, Moved: fmt.Sprintf("%s work %s -> working card=%s member=%s (fleet ready)", c.ID, c.Col, card, m)}, ""
}

// redeal deals a withdrawn work card again, into the ready queue of the up
// member m (the deal's next round the fleet, no member avoided: the avoid of a
// rework is for a new attempt), at a new generation bound to that member, and
// moves its primary to working on it. The attempt, the fix and the score are
// the card's own, unchanged. The redeal counts only when a take of the card
// ended (FieldTakeEnded): a card withdrawn while ready keeps its count
// (tla/DirtyTick.tla DealOne). Its route is the next at its tier's index that the
// card was not dealt on (ri, moved past it and the entries skipped: route.go).
func redeal(s *Snapshot, c, wc *Card, m string, q map[string]int, ri routeIndexes) (Unit, string) {
	route, _, why := s.routeOf(c, wc, ri)
	if why != "" {
		return Unit{}, why
	}
	q[m]++
	set := nextGen(wc, m, s.Now)
	if wc.F(FieldTakeEnded) != "" {
		set["redeals"] = itoa(wc.Int("redeals") + 1)
	}
	unset := []string{"withdrawn", FieldTakeEnded, FieldProviderError}
	work, primary := splitRoute(route)
	for k, v := range work {
		if v == "" {
			unset = append(unset, k)
			continue
		}
		set[k] = v
	}
	primary["work"] = wc.ID
	return Unit{Key: c.ID, Stream: c.Row, Changes: []Change{
		change(Fleet, moveEntry(wc, m, Ready, set, unset...)),
		change(Work, moveEntry(c, c.Row, Working, primary, "result")),
	}, Moved: fmt.Sprintf("%s work %s -> working card=%s member=%s gen=%d (fleet ready, dealt again)", c.ID, c.Col, wc.ID, m, wc.Int("gen")+1)}, ""
}

// TakeReq is a worker taking its work cards. Gens names the generation the
// worker holds for a named card; a take by id names one for every card, and
// a card whose live generation differs has been dealt again, so the take is
// refused as stale. A take by selection takes the member's oldest ready cards
// and reports each one's generation.
type TakeReq struct {
	Sel
	As   string
	Gens map[string]int
	Who  string
}

// named says the selection names its cards by id.
func named(sel Sel) bool { return len(sel.IDs) > 0 || sel.Only != nil }

// liveGen is the refusal of a card whose generation the request does not
// name, or names and is not the card's live one.
func liveGen(verb string, c *Card, gens map[string]int) string {
	g, ok := gens[c.ID]
	switch {
	case !ok:
		return fmt.Sprintf("names no generation; the live one is %d: %s %s@%d", c.Int("gen"), verb, c.ID, c.Int("gen"))
	case g != c.Int("gen"):
		return fmt.Sprintf("stale: generation %d is not the live one (%d): the card was dealt again to %s", g, c.Int("gen"), orDash(c.Row))
	}
	return ""
}

// Take moves the member's work cards fleet ready -> working. As may name
// several members, comma separated: each takes from its own ready queue, up
// to the limit, in the one plan (the world's workers move in one batch a
// tick, every member's row at once: errata 3 amendment 10); a take by id
// names one member.
func Take(s *Snapshot, r TakeReq) Plan {
	members := Split(r.As)
	if len(members) <= 1 {
		return takeOne(s, r)
	}
	var p Plan
	if named(r.Sel) {
		for _, id := range r.Sel.IDs {
			p.refuse(id, "a take by id names one member: --as <member>")
		}
		return p
	}
	for _, m := range members {
		q := r
		q.As = m
		if q.Who == r.As {
			q.Who = m
		}
		one := takeOne(s, q)
		p.Units = append(p.Units, one.Units...)
		p.Refused = append(p.Refused, one.Refused...)
	}
	return p
}

func takeOne(s *Snapshot, r TakeReq) Plan {
	var p Plan
	sel := r.Sel
	if !named(sel) && sel.Limit == 0 {
		sel.Limit = 1
	}
	if !s.Fleet.HasRow(r.As) {
		for _, id := range sel.IDs {
			p.refuse(id, "no fleet member "+r.As)
		}
		return p
	}
	if st := s.MemberCtl(r.As).F("status"); st != Up {
		for _, id := range sel.IDs {
			p.refuse(id, "member "+r.As+" is "+orDash(st))
		}
		return p
	}
	byID := named(sel)
	// THE WIDTH IS HARD: a member's working cards never pass its width, held here, at the
	// sprint's one writer, whatever the member asks (the owner, 2026-10-01: "this
	// \"squishiness\" of having > width in the working set has me concerned."). A take by
	// count is cut to the room; a take by id past it is refused.
	room := max(s.Width(r.As)-len(s.Fleet.Cell(r.As, Working)), 0)
	if !byID {
		if room == 0 {
			return p
		}
		// a limit under zero asks for every ready card (pick): that too is the room
		if sel.Limit < 0 || sel.Limit > room {
			sel.Limit = room
		}
	}
	// the member's ready cards in stream turns (takeTurns), as the deal dealt
	// them: a member holding DealAhead times its width takes its width of them
	// from every stream alike, never one stream's lowest scores first (errata 3
	// amendment 10)
	chosen := pick(&p, sel, takeTurns(s.Fleet.Cell(r.As, Ready), slices.Index(s.Fleet.Rows(), r.As)), fieldStream, func(c *Card) string {
		if byID {
			if why := liveGen("take", c, r.Gens); why != "" {
				return why
			}
		}
		if !c.Placed() || c.Row != r.As || c.Col != Ready {
			return "not in " + r.As + " ready (it is " + placeWord(c) + ")"
		}
		if byID {
			if room == 0 {
				return fmt.Sprintf("member %s is at its width (%d working of %d): a card is taken when one is reported", r.As, len(s.Fleet.Cell(r.As, Working)), s.Width(r.As))
			}
			room--
		}
		return ""
	}, s.Fleet.Card)
	for _, c := range chosen {
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, c.Row, Working, takenStamps(c, s.Now), "untaken_since"))},
			Moved: fmt.Sprintf("%s fleet ready -> working member=%s gen=%s", c.ID, r.As, c.F("gen"))})
	}
	return p
}

// FinishReq is a worker finishing work cards, ok or failed. A finish always
// names, in Gens, the generation the worker holds for every card: one naming
// none is refused, and one whose generation is not the live one is refused as
// stale and changes nothing.
type FinishReq struct {
	Sel
	As     string
	Gens   map[string]int // the generation held, per named card
	Failed bool
	Head   string
	Report string
	// Branch and Base are the branch the work is on and the one it started
	// from, as the worker reports them.
	Branch, Base string
	// Usage is what the run spent, as the member read it from its child (its
	// budget word and wall, the tokens by class, the harness's cost: cardcost.Usage):
	// kept on the work card, the attempt's record, timed and priced (cost.go).
	Usage string
	Who   string
}

// Finish moves work cards working -> done and their primaries working ->
// review. Fixed work that comes back ok is asked by the machine's ask, as
// any work is.
// A finish always names the generation it holds for every card it finishes:
// a card without one is refused, naming the live generation, and a finish
// by selection without --as is refused outright. As may name several
// members, comma separated: every card named is on one of them, each
// finished as its own member's, in the one plan (errata 3 amendment 10).
func Finish(s *Snapshot, r FinishReq) Plan { return Lawful(finishPlan(s, r)) }

func finishPlan(s *Snapshot, r FinishReq) Plan {
	var p Plan
	if !named(r.Sel) && r.As == "" {
		p.refuse("finish", "a finish by selection names its member: --as <member>; better, name each card: finish <card>@<gen>")
		return p
	}
	members := Split(r.As)
	var all []*Card
	if len(members) > 0 {
		for _, m := range members {
			all = append(all, s.Fleet.Cell(m, Working)...)
		}
	} else {
		all = s.Fleet.Column(Working)
	}
	byID := named(r.Sel)
	picked := pick(&p, r.Sel, all, fieldStream, func(c *Card) string {
		if byID {
			if why := liveGen("finish", c, r.Gens); why != "" {
				return why
			}
		}
		if !c.Placed() || c.Col != Working {
			return "not working (it is " + placeWord(c) + ")"
		}
		if len(members) > 0 && !contains(members, c.Row) {
			return "dealt to " + c.Row + ", not " + r.As
		}
		if pr := s.Work.Placed(c.F("primary")); pr == nil || pr.Col != Working || pr.F("work") != c.ID {
			return "its primary " + c.F("primary") + " is not working on it"
		}
		return ""
	}, s.Fleet.Card)
	var chosen []*Card
	for _, c := range picked {
		if why := liveGen("finish", c, r.Gens); why != "" {
			p.refuse(c.ID, why)
			continue
		}
		chosen = append(chosen, c)
	}
	for _, c := range chosen {
		// the member that finished it: the one --as names, each card's own
		// when it names several
		who := r.As
		switch {
		case len(members) > 1:
			who = c.Row
		case who == "":
			who = r.Who
		}
		pr := s.Work.Placed(c.F("primary"))
		if r.Failed && IsProviderFailure(r.Report) {
			p.Units = append(p.Units, takeEnded(s, c, pr, r, cardhdr.EndProvider))
			continue
		}
		if r.Failed && IsNoResult(r.Report) {
			p.Units = append(p.Units, takeEnded(s, c, pr, r, cardhdr.EndNoResult))
			continue
		}
		if r.Failed && IsStagingRefusal(r.Report) {
			p.Units = append(p.Units, stagingRefused(s, c, pr, r))
			continue
		}
		head := r.Head
		if head == "" {
			head = c.ID
		}
		result, okWord, into := "ok", "yes", DoneOK
		if r.Failed {
			result, okWord, into = "failed", "no", DoneFailed
		}
		// A rework whose child found nothing to do, or committed nothing, at the head an
		// earlier attempt pushed and a reader passed is no failed work: the card was right.
		// It goes back to review at that head, where the machine's ask asks two readers
		// (docs/SPEC-SPRINT.md section 6; Rework sets FieldPassedHead).
		passed := r.Failed && r.Head == "" && IsNothingNew(r.Report) && pr.F(FieldPassedHead) != ""
		if passed {
			head, result, okWord, into = pr.F(FieldPassedHead), "ok", "yes", DoneOK
		}
		cardSet := map[string]string{"ok": okWord, "head": head, "finished": stamp(s.Now)}
		if r.Report != "" {
			cardSet["report"] = r.Report
		}
		if r.Branch != "" {
			cardSet["branch"] = r.Branch
		}
		if r.Base != "" {
			cardSet["base"] = r.Base
		}
		// what the take cost, timed and priced (cost.go): kept on the work card when the
		// member reported it, and recorded on the primary, the producer, in this step
		dealt, taken := takeStamps(c)
		rec := costRecord(s, r.Usage, c.F(FieldRoute), c.F(FieldModel), false, dealt, taken)
		if r.Usage != "" {
			cardSet[FieldUsage] = rec
		}
		set := map[string]string{"head": head, "result": result}
		if r.Failed && !passed {
			set["failed"] = itoa(pr.Int("failed") + 1)
		}
		addConsumer(pr, set, workConsumer(s, c, 0, result, rec))
		u := Unit{Key: c.ID, Stream: pr.Row, Changes: []Change{change(Fleet, moveEntry(c, c.Row, into, cardSet))},
			Moved: fmt.Sprintf("%s working -> done %s; %s working -> review", c.ID, result, pr.ID)}
		attempt := pr.Int("attempt")
		// ONE PATH ASKS: the finish asks no reader. The machine's ask does, in the tick the
		// finish wakes, the earlier pair first (Ask, the primary's asked field), each read
		// with the route it draws. A read the finish created itself carried no route (a
		// finish loads none) and no reader could start it (fleet pass 7, 2026-10-01: two
		// such reads held a card twelve minutes)
		asked := map[string]string{}
		if passed {
			n := happened(NWorkOK, pr.Row, s.Now, pr.ID)
			n.Who, n.Attempt = who, attempt
			n.What = "nothing new at " + head + ", which a reader passed: back in review at it; " + r.Report
			u.Notes = append(u.Notes, n)
		} else if !r.Failed {
			n := happened(NWorkOK, pr.Row, s.Now, pr.ID)
			n.Who, n.Attempt = who, attempt
			u.Notes = append(u.Notes, n)
		} else {
			n := judgment(NWorkFailed, pr.Row, s.Now, pr.Int("failed"), pr.ID)
			n.Who, n.Attempt, n.What = who, attempt, r.Report
			if c.F(FieldRoute) != "" {
				// which route failed it: a bad route is seen in the inbox
				n.What = "route=" + c.F(FieldRoute) + " model=" + c.F(FieldModel) + ": " + r.Report
			}
			u.Notes = append(u.Notes, n)
		}
		u.Changes = append(u.Changes, change(Work, moveEntry(pr, pr.Row, Review, set)))
		if j, ok := reviewJudgment(s, inReview(pr, set), reviewStep{moved: asked, writes: u.Notes, who: who}); ok {
			u.Notes = append(u.Notes, j)
		}
		p.Units = append(p.Units, u)
	}
	return p
}

// FieldPassedHead is the head of the attempt a rework sent back when a reader had passed
// it (an ok read at it): what a next attempt that finds nothing new returns to review at.
const FieldPassedHead = "passed_head"

// IsNothingNew says a failed finish's report is the member's word for no new work: its
// child found nothing to do (cardhdr.EndNothing) or committed nothing (cardhdr.EndNoCommit).
func IsNothingNew(report string) bool {
	return strings.HasPrefix(report, cardhdr.EndNothing+":") || strings.HasPrefix(report, cardhdr.EndNoCommit+":")
}

// IsProviderFailure says a failed finish's report names the provider as the cause: it
// begins with the finish kind `provider failure` (cardhdr.EndProvider), the member's
// word when its child's run ended on the provider's error (docs/SPEC-CARD-CONTRACT.md,
// section 4).
func IsProviderFailure(report string) bool { return strings.HasPrefix(report, cardhdr.EndProvider) }

// IsNoResult says a failed finish's report names no result as the cause: it begins with
// the finish kind `no result` (cardhdr.EndNoResult), the member's word when its child
// ended by itself having written no result at all.
func IsNoResult(report string) bool { return strings.HasPrefix(report, cardhdr.EndNoResult+":") }

// takeEnded is the unit of a take that ended with no work to judge: the provider failed
// it (tla/CardContract.tla, ProviderFailure), or its child left no result at all (kind
// is which: cardhdr.EndProvider or cardhdr.EndNoResult). The take ended, so the work
// card is withdrawn with FieldTakeEnded and its primary goes back to ready, exactly as a
// member that went down leaves a working card (downPlan); the tick's deal places it
// again, counting the take against the redeal bound, on a route the card has not been
// drawn when another remains (routeOf). No failed-work judgment is written and the
// primary's failed count does not move: such a take is never the card's. At the redeal
// bound the card stays withdrawn and the bound's judgment names it. The card keeps a
// record of the take that ended (ProviderTake: the route, the member, the line).
func takeEnded(s *Snapshot, c, pr *Card, r FinishReq, kind string) Unit {
	set := nextGen(c, "", s.Now)
	set["withdrawn"], set[FieldTakeEnded] = stamp(s.Now), stamp(s.Now)
	line := cutText(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(r.Report, kind), ":")), MaxProviderErrorBytes)
	why := "the provider failed the take"
	if kind == cardhdr.EndNoResult {
		// the record's line says which kind it was: the routes' count of a route's ended
		// takes holds both, and a reader of the card tells them apart
		line, why = cutText(kind+": "+line, MaxProviderErrorBytes), "the child left no result"
	}
	set[FieldProviderError] = line
	// what the take cost, timed and priced before its stamps go (cost.go): it still cost
	// tokens and time. The take's record is its one place: the card's usage field is
	// left alone, so it only ever holds the card's own ended take (finishPlan), and a
	// redealt card never shows, or counts, this take's usage again
	dealt, taken := takeStamps(c)
	rec := costRecord(s, r.Usage, c.F(FieldRoute), c.F(FieldModel), false, dealt, taken)
	usage := ""
	if r.Usage != "" {
		usage = rec
	}
	// the ended take's own record, kept through the redeals: its route, member, usage and line
	take := c.Int("redeals") + 1
	set[FieldProviderTake+itoa(take)] = ProviderTake{Route: c.F(FieldRoute), Model: c.F(FieldModel), Member: c.Row,
		Finished: stamp(s.Now), Usage: usage, Error: line}.String()
	// and the producer's record of it (cost.go): it still cost tokens and time
	prSet := map[string]string{}
	addConsumer(pr, prSet, workConsumer(s, c, take, kind, rec))
	return Unit{Key: c.ID, Stream: pr.Row, Changes: []Change{
		change(Fleet, moveEntry(c, c.Row, Withdrawn, set, "taken", "dealt")),
		change(Work, moveEntry(pr, pr.Row, Ready, prSet, "work")),
	}, Moved: fmt.Sprintf("%s working -> withdrawn gen=%d, %s; %s working -> ready", c.ID, c.Int("gen")+1, why, pr.ID)}
}

// IsStagingRefusal says a failed finish's report names its member's staging as the cause:
// it begins with the finish kind `staging refused` (cardhdr.EndStaging), the member's word
// when its machine refused the launch before any child ran.
func IsStagingRefusal(report string) bool { return strings.HasPrefix(report, cardhdr.EndStaging) }

// stagingRefused is the unit of a launch its member refused at staging (tla/CardContract.tla,
// StageRefused and Restage): no child ran, so the work card is withdrawn WITHOUT
// FieldTakeEnded and its primary goes back to ready, and the deal places it again on a
// member that has not refused it (StagingRefusers), spending none of the redeal bound: the
// member's failure, never the card's. No failed-work judgment is written; the inbox is told
// what happened, the member and the reason; the card keeps the refusal's record
// (FieldStagingTake at the generation refused). A member refuses a card once: a
// refusal by a member already among its refusers writes no second record and no
// second note (tla/CardContract.tla, NeverOnARefuser).
func stagingRefused(s *Snapshot, c, pr *Card, r FinishReq) Unit {
	set := nextGen(c, "", s.Now)
	set["withdrawn"] = stamp(s.Now)
	line := cutText(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(r.Report, cardhdr.EndStaging), ":")), MaxProviderErrorBytes)
	var notes []Note
	if !contains(StagingRefusers(c), c.Row) {
		set[FieldStagingTake+itoa(c.Int("gen"))] = ProviderTake{Route: c.F(FieldRoute), Model: c.F(FieldModel), Member: c.Row,
			Finished: stamp(s.Now), Usage: r.Usage, Error: line}.String()
		n := happened(NStagingRefused, pr.Row, s.Now, pr.ID)
		n.Who, n.Attempt = c.Row, c.Int("attempt")
		n.What = cardhdr.EndStaging + " on " + c.Row + ": " + line
		notes = append(notes, n)
	}
	// no child ran: the producer records the launch only when it cost something
	prSet := map[string]string{}
	if r.Usage != "" {
		dealt, taken := takeStamps(c)
		addConsumer(pr, prSet, workConsumer(s, c, 0, "staging refused", costRecord(s, r.Usage, c.F(FieldRoute), c.F(FieldModel), false, dealt, taken)))
	}
	return Unit{Key: c.ID, Stream: pr.Row, Changes: []Change{
		change(Fleet, moveEntry(c, c.Row, Withdrawn, set, "taken", "dealt")),
		change(Work, moveEntry(pr, pr.Row, Ready, prSet, "work")),
	}, Notes: notes, Moved: fmt.Sprintf("%s working -> withdrawn gen=%d, %s refused it at staging; %s working -> ready", c.ID, c.Int("gen")+1, c.Row, pr.ID)}
}

// FleetReq is a fleet move: a member up or down, or the ready queues
// levelled (the tick's moves, as a member's derived status changes), or the
// coordinator's hold on a member (hold) and its release (release), or the
// whole fleet made to match the inventory (sync).
type FleetReq struct {
	Op     string // up, down, level, hold, release, sync
	Member string
	Who    string
	// Fresh says the member is alive (Beat.Alive: fewer than MissedBeatsDown
	// beat windows missed): release brings it up at once.
	Fresh bool
	// Live, when set, is the other members up for this move: down deals to
	// them and up levels with them. nil is every member whose status is up.
	Live []string
	// Why is said in the happened notification of a change of status.
	Why string
	// Width, above zero, is the member's width set by up or release (the
	// machine's child cap, width.go); zero leaves the width as it is.
	Width int `json:",omitempty"`
	// Sync, with Op sync, is every machine the inventory says is a member
	// and its width (fleet_sync.go); Member is empty.
	Sync []SyncMember `json:",omitempty"`
	// HeldBy, with hold, marks the hold as made by that mechanism (the sync's,
	// fleet_sync.go) and not the coordinator's: the control card's held_by.
	HeldBy string `json:",omitempty"`
}

// Fleet brings a member up (and levels the ready queues), takes one down
// (dealing its unfinished work cards to up members, or withdrawing them when
// none is up), or levels the ready queues. hold marks a member held and takes
// it down; release clears the hold, adding a member it does not know, and
// brings it up when its beat is fresh.
func FleetStep(s *Snapshot, r FleetReq) Plan {
	// every card the step places on a member (a down member's cards dealt
	// again, the levelling) goes round the fleet from the deal's rolling index
	// and moves it (round.go, errata 3 amendment 5), written with the step
	extra := append([]string{r.Member}, r.Live...)
	for _, m := range r.Sync {
		extra = append(extra, m.Name)
	}
	rr := dealRoundWith(s, extra...)
	moves := roundMoves{}
	p := Lawful(fleetStepPlan(s, r, rr, moves))
	roundWrites(&p, rr, moves)
	return p
}

// liveFor is the members up for a move of member: r.Live, else the members
// whose status is up, without member.
func liveFor(s *Snapshot, r FleetReq) []string {
	all := r.Live
	if all == nil {
		all = s.UpMembers()
	}
	var out []string
	for _, m := range all {
		if m != r.Member {
			out = append(out, m)
		}
	}
	return out
}

// headOf puts the control card's change, its notification and its line in
// front of the plan's first unit.
func headOf(p *Plan, member string, head []Change, n *Note, line string) {
	if len(head) == 0 && n == nil {
		return
	}
	if len(p.Units) == 0 {
		p.Units = append(p.Units, Unit{Key: CtlID(member)})
	}
	p.Units[0].Changes = append(head, p.Units[0].Changes...)
	if n != nil {
		p.Units[0].Notes = append(p.Units[0].Notes, *n)
	}
	if !strings.HasSuffix(p.Units[0].Moved, "; "+line) {
		p.Units[0].Moved = strings.TrimPrefix(p.Units[0].Moved+"; "+line, "; ")
	}
}

// statusNote is the happened notification of a member's change of status.
func statusNote(s *Snapshot, r FleetReq, typ, word string) *Note {
	n := happened(typ, "", s.Now)
	n.What, n.Who = r.Member+" "+word, r.Who
	if r.Why != "" {
		n.What += ": " + r.Why
	}
	return &n
}

func fleetStepPlan(s *Snapshot, r FleetReq, rr *round, moves roundMoves) Plan {
	var p Plan
	switch r.Op {
	case "up", "release":
		if !ValidID(r.Member) {
			p.refuse(r.Member, "a member name wants letters, digits, _ and -")
			return p
		}
		if r.Width < 0 || r.Width > MaxWidth {
			p.refuse(r.Member, fmt.Sprintf("a width wants a whole number from 1 to %d", MaxWidth))
			return p
		}
		if !s.Fleet.HasRow(r.Member) {
			p.Rows = append(p.Rows, RowAdd{Fleet, r.Member})
		}
		ctl := s.Fleet.Card(CtlID(r.Member))
		comeUp := r.Op == "up" || r.Fresh
		var head []Change
		var n *Note
		line := r.Member + " up"
		switch {
		case ctl == nil:
			status := Down
			if comeUp {
				status = Up
				n = statusNote(s, r, NMemberUp, "up")
			} else {
				line = r.Member + " added, down until it beats"
			}
			fields := map[string]string{"kind": "member", "status": status, "since": stamp(s.Now)}
			if r.Width > 0 {
				fields[FieldWidth] = itoa(r.Width)
			}
			head = append(head, change(Fleet, createEntry(CtlID(r.Member), r.Member, Ctl, 0, fields)))
		default:
			set := map[string]string{}
			var unset []string
			if comeUp && ctl.F("status") != Up {
				set["status"], set["since"] = Up, stamp(s.Now)
				n = statusNote(s, r, NMemberUp, "up")
			}
			if r.Width > 0 && ctl.F(FieldWidth) != itoa(r.Width) {
				set[FieldWidth] = itoa(r.Width)
			}
			if r.Op == "release" && ctl.F("held") != "" {
				unset = append(unset, "held", FieldHeldBy)
				if !comeUp {
					line = r.Member + " released, down until it beats"
				}
			}
			if len(set) > 0 || len(unset) > 0 {
				head = append(head, change(Fleet, setEntry(ctl, set, unset...)))
			}
		}
		if r.Width > 0 && (ctl == nil || ctl.F(FieldWidth) != itoa(r.Width)) {
			line += " width=" + itoa(r.Width)
		}
		if comeUp {
			level(s, &p, orderLike(s.Fleet.Rows(), append(liveFor(s, r), r.Member), r.Member), rr, moves, nil)
			if len(moves) > 0 {
				to, from := map[string]int{}, map[string]int{}
				for id, m := range moves {
					to[m]++
					from[s.Fleet.Card(id).Row]++
				}
				line += fmt.Sprintf("; moved=%d to %s from %s", len(moves), countsByMember(to), countsByMember(from))
			}
		}
		headOf(&p, r.Member, head, n, line)
	case "down", "hold":
		// the room of each receiver is its width (width.go, errata 3 amendment
		// 9): its work cards held, ready and working, under it
		up := liveFor(s, r)
		return downPlan(s, r, up, rr, moves, memberLoads(s, up), memberWidths(s, up))
	case "level":
		up := s.UpMembers()
		held := memberLoads(s, up)
		sweep(s, &p, r, up, rr, moves, held)
		level(s, &p, up, rr, moves, held)
	case "sync":
		return fleetSyncPlan(s, r, rr, moves)
	default:
		p.refuse(r.Op, "fleet wants up, down, level, hold, release or sync")
	}
	return p
}

// downPlan is one member going down (or held): its control card, and its
// unfinished work cards, ready and working, dealt round the members of up
// below their width at a new generation, or withdrawn when none has room or a
// working card is at its redeal bound. A working card's take ended without a
// finish: its redeal counts (redeals + 1), and withdrawn it carries
// FieldTakeEnded for the deal that places it again to count; a ready card was
// never taken and keeps its count (tla/DirtyTick.tla ApplyF "lapse" and
// RedealsAreEndedTakes). q and widths are the
// receivers' loads and widths, counted on as cards are dealt: a tick that
// takes several members down in one plan (presence) shares them, so every
// down member's cards go round the fleet together (the owner's rule: every
// row of every table moves every tick, errata 3 amendment 10).
func downPlan(s *Snapshot, r FleetReq, up []string, rr *round, moves roundMoves, q, widths map[string]int) Plan {
	var p Plan
	ctl := s.MemberCtl(r.Member)
	if ctl == nil {
		p.refuse(r.Member, "no fleet member "+r.Member)
		return p
	}
	set := map[string]string{}
	var n *Note
	line := r.Member + " down"
	if ctl.F("status") != Down {
		set["status"], set["since"] = Down, stamp(s.Now)
		n = statusNote(s, r, NMemberDown, "down")
	}
	var unset []string
	switch {
	case r.Op == "hold" && ctl.F("held") == "":
		set["held"] = stamp(s.Now)
		line = r.Member + " held down"
		// who made the hold: the sync marks its own, so that it alone releases
		// it (fleet_sync.go); a coordinator's hold carries no mark, and a
		// mark left by an earlier hold is cleared
		if r.HeldBy != "" {
			set[FieldHeldBy] = r.HeldBy
		} else {
			unset = append(unset, FieldHeldBy)
		}
	case r.Op == "hold" && r.HeldBy == "":
		// the coordinator holds a member that is already held (the sync's hold
		// included): the hold is now the coordinator's, and the sync's mark
		// goes, so the sync never releases it
		unset = append(unset, FieldHeldBy)
	}
	var head []Change
	if len(set) > 0 || len(unset) > 0 {
		head = append(head, change(Fleet, setEntry(ctl, set, unset...)))
	}
	cards := append(append([]*Card{}, s.Fleet.Cell(r.Member, Ready)...), s.Fleet.Cell(r.Member, Working)...)
	SortCards(cards)
	for _, c := range cards {
		taken := c.Col == Working
		if len(up) > 0 && !(taken && c.Int("redeals") >= MaxRedeals) {
			// the next member round the fleet below its width (round.go), the
			// index moved past it; with none below its width the card is
			// withdrawn, and the next deal places it where there is room: a
			// member at its width takes no more (errata 3 amendment 9)
			if m := rr.next(without(up, StagingRefusers(c)), q, widths, ""); m != "" {
				rr.moved(m)
				moves[c.ID] = m
				q[m]++
				set := nextGen(c, m, s.Now)
				if taken {
					set["redeals"] = itoa(c.Int("redeals") + 1)
				}
				p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, m, Ready, set, "taken"))},
					Moved: fmt.Sprintf("%s %s:%s -> %s:ready gen=%d; %s down", c.ID, c.Row, c.Col, m, c.Int("gen")+1, r.Member)})
				continue
			}
		}
		set := nextGen(c, "", s.Now)
		set["withdrawn"] = stamp(s.Now)
		if taken {
			set[FieldTakeEnded] = stamp(s.Now)
		}
		u := Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, c.Row, Withdrawn, set, "taken", "dealt"))},
			Moved: fmt.Sprintf("%s withdrawn gen=%d", c.ID, c.Int("gen")+1)}
		if pr := s.Work.Placed(c.F("primary")); pr != nil && pr.Col == Working && pr.F("work") == c.ID {
			u.Changes = append(u.Changes, change(Work, moveEntry(pr, pr.Row, Ready, nil, "work")))
			u.Moved += "; " + pr.ID + " working -> ready"
			w := happened(NWithdrawn, pr.Row, s.Now, pr.ID)
			w.Who = r.Who
			u.Notes = append(u.Notes, w)
		}
		p.Units = append(p.Units, u)
	}
	// where the member's cards went, and which stayed (nova-tools#5096 item 21)
	to, stayed := map[string]int{}, []string{}
	for _, c := range cards {
		if m, ok := moves[c.ID]; ok {
			to[m]++
		} else {
			stayed = append(stayed, c.F("primary"))
		}
	}
	line += fmt.Sprintf("; moved=%d", len(cards)-len(stayed))
	if len(to) > 0 {
		line += " to " + countsByMember(to)
	}
	line += fmt.Sprintf("; stayed=%d", len(stayed))
	if len(stayed) > 0 {
		line += " withdrawn: " + Preview(stayed, ",")
	}
	headOf(&p, r.Member, head, n, line)
	return p
}

// countsByMember is a count per member, in name order: "m2(3),m3(1)".
func countsByMember(n map[string]int) string {
	var out []string
	for _, m := range slices.Sorted(maps.Keys(n)) {
		out = append(out, fmt.Sprintf("%s(%d)", m, n[m]))
	}
	return strings.Join(out, ",")
}

// sweep is the rebalance's safety (the owner, 2026-10-01: "and it's a safety, if
// ever there are cards on a held or down machine, rebalance moves them away."):
// every work card, ready or working, on a member whose status is not up (down,
// or held) goes as a member going down sends it (downPlan): to the next up
// member round the fleet below DealAhead times its width, at a new generation,
// a working card's redeal counted; withdrawn, its primary ready again, when none
// has room or no member is up. held counts what each up member holds, the
// cards placed here included, for the level after it.
func sweep(s *Snapshot, p *Plan, r FleetReq, up []string, rr *round, moves roundMoves, held map[string]int) {
	widths := memberWidths(s, up)
	for _, m := range s.Fleet.Rows() {
		ctl := s.MemberCtl(m)
		if ctl == nil || ctl.F("status") == Up || s.Fleet.Count(m, Ready)+s.Fleet.Count(m, Working) == 0 {
			continue
		}
		why := "down"
		if ctl.F("held") != "" {
			why = "held"
		}
		q := downPlan(s, FleetReq{Op: "down", Member: m, Who: r.Who, Why: "the rebalance: cards on a " + why + " member"}, up, rr, moves, held, widths)
		p.Units = append(p.Units, q.Units...)
		p.Refused = append(p.Refused, q.Refused...)
	}
}

// level evens the up members' backlogs, once at the start of every tick (the
// owner, 2026-10-01: "both for readers and fleet, there needs to be a
// rebalance step done at the start of each tick. it's simple. just once before
// tick, rebalance each table."). A member's backlog is the work cards it holds,
// ready and working, less its width: below zero it has free lanes its ready
// cards do not fill, above zero it holds ready cards it cannot start. While the
// largest backlog of a member with a ready card and the smallest of the members
// below DealAhead times their width differ by more than one, the newest ready
// card (the last in work order) of the largest that has a target moves to the
// next member round the fleet below DealAhead times its width, at least two
// below, at or below the mean backlog and no refuser of the card
// (round.levelTo, errata 3 amendment 5: the levelling moves the deal's index
// too). So no up member has free lanes and an empty ready column while another
// holds ready cards it cannot start, a member that comes up takes its share at
// once, and none is levelled past DealAhead times its width (width.go).
//
// The call ends, by two guards, each enough alone (tla/Level.tla; the wedge of
// 2026-10-02, nova-tools#5122, was a call that did not): every move lowers the
// sum of squared backlogs by at least two (levelTo's gap), and a card it moved
// is never one it moves again, so it makes at most as many moves as the fleet
// had ready cards. A newest card whose only target is refused or one below
// does not stop an older card that has one.
func level(s *Snapshot, p *Plan, up []string, rr *round, moves roundMoves, held map[string]int) {
	levelWith(s, p, up, rr, moves, held, (*round).levelTo)
}

// levelTarget is where level sends a card: round.levelTo. A test hands level
// the rule of the wedge (b7776ca3) to show that the ready count alone ends the
// loop (tla/Level.tla, MCLevelOldRuleNoRequeue).
type levelTarget func(r *round, up []string, n, held, widths map[string]int, from string, avoid []string) string

// levelWith is level with its target.
func levelWith(s *Snapshot, p *Plan, up []string, rr *round, moves roundMoves, held map[string]int, target levelTarget) {
	if len(up) < 2 {
		return
	}
	queues := map[string][]*Card{}
	if held == nil {
		held = memberLoads(s, up)
	}
	widths := memberWidths(s, up)
	for _, m := range up {
		queues[m] = append([]*Card{}, s.Fleet.Cell(m, Ready)...)
	}
	for {
		long, short := "", ""
		n := map[string]int{}
		for _, m := range up {
			n[m] = held[m] - s.Width(m)
		}
		for _, m := range up {
			if len(queues[m]) > 0 && (long == "" || n[m] > n[long]) {
				long = m
			}
			if held[m] < widths[m] && (short == "" || n[m] < n[short]) {
				short = m
			}
		}
		if long == "" || short == "" || n[long]-n[short] <= 1 {
			return
		}
		// the newest card of the longest queue that has a target, never a member that
		// refused it at staging (StagingRefusers; tla/CardContract.tla, Level)
		q := queues[long]
		i, to := len(q)-1, ""
		for ; i >= 0 && to == ""; i-- {
			to = target(rr, up, n, held, widths, long, StagingRefusers(q[i]))
		}
		if to == "" {
			return
		}
		i++
		held[long]--
		held[to]++
		c := q[i]
		// the card leaves the queues and is not queued on its receiver: no card
		// moves twice in one call (each unit is guarded on the place the card
		// was read at), so the call makes at most as many moves as the fleet
		// had ready cards, whatever the target (tla/Level.tla, MovesBounded)
		queues[long] = append(q[:i:i], q[i+1:]...)
		moves[c.ID] = to
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, to, Ready, nextGen(c, to, s.Now)))},
			Moved: fmt.Sprintf("%s %s:ready -> %s:ready gen=%d", c.ID, long, to, c.Int("gen")+1)})
	}
}

// nextGen is the fields of a work card dealt again: a new generation, bound
// to the member it is dealt to, with dealt stamped now ("" when it is
// withdrawn: no member, and dealt is unset by the caller), and untaken_since
// stamped when this is the first deal since its last take.
func nextGen(c *Card, member string, now time.Time) map[string]string {
	set := map[string]string{"gen": itoa(c.Int("gen") + 1)}
	if member != "" {
		set["member"], set["dealt"] = member, stamp(now)
		if c.F("untaken_since") == "" {
			set["untaken_since"] = stamp(now)
		}
	}
	return set
}

// without is xs less every one of out, in xs's order.
func without(xs, out []string) []string {
	var keep []string
	for _, x := range xs {
		if !contains(out, x) {
			keep = append(keep, x)
		}
	}
	return keep
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// orderLike orders names by the rows' order, a name with no row last.
func orderLike(rows, names []string, extra string) []string {
	var out []string
	for _, r := range rows {
		if contains(names, r) {
			out = append(out, r)
		}
	}
	if !contains(out, extra) && contains(names, extra) {
		out = append(out, extra)
	}
	return out
}

// takenStamps is the fields of a take: taken now, and first_taken once per
// attempt, kept through every redeal and withdrawal.
func takenStamps(c *Card, now time.Time) map[string]string {
	set := map[string]string{"taken": stamp(now)}
	if c.F("first_taken") == "" {
		set["first_taken"] = stamp(now)
	}
	return set
}

// takeTurns is a member's work cards in stream turns: one card of each stream
// (its stream field) in turn, the streams in name order from the one at
// offset (the member's place in the fleet, so the members together start at
// every stream alike), within a stream by work order (SortCards), a stream
// with none left skipped.
func takeTurns(cards []*Card, offset int) []*Card {
	by := map[string][]*Card{}
	var streams []string
	for _, c := range cards {
		st := c.F("stream")
		if _, ok := by[st]; !ok {
			streams = append(streams, st)
		}
		by[st] = append(by[st], c)
	}
	sort.Strings(streams)
	if n := len(streams); n > 0 && offset > 0 {
		streams = append(append([]string(nil), streams[offset%n:]...), streams[:offset%n]...)
	}
	for _, st := range streams {
		SortCards(by[st])
	}
	out := make([]*Card, 0, len(cards))
	for turn := 0; len(out) < len(cards); turn++ {
		for _, st := range streams {
			if turn < len(by[st]) {
				out = append(out, by[st][turn])
			}
		}
	}
	return out
}

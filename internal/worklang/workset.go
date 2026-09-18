package worklang

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The work set is the OTHER top form of this language, and the one a coordinator
// actually writes (work/pitstop-2026-09-17.lisp):
//
//	(work-set "id" :title "..." :units ((unit "u1" :owner "Stella" :lane "work"
//	                                           :needs ("u0") :deadline "2026-09-19T12:00Z"
//	                                           :title "...") ...))
//
// `(:plan ...)` is the expander's form -- nodes with kinds, expanded into cards.
// `(work-set ...)` is the coordinator's: units with owners, lanes, needs and
// deadlines, pulled by a friend or a bench. ParsePlan reads only the first, so
// `plan check` was blind to every real work set on the bench. ParseWorkSet reads
// the second, through the SAME bounded reader: the three bounds still hold, a
// dispatch macro is still a refusal at the byte that owes it, and nothing here is
// ever evaluated.
//
// A key this reader does not know is KEPT, not refused: the work set is a person's
// document with comments, order and keys that outgrow any one reader of it, and a
// unit with a :pr, a :budget or a :derive is still a unit with an owner and a
// deadline. What is refused is only what cannot be read at all.
//
// The split between a refusal and a finding is the whole point of this file. A
// refusal (exit 2) is a file this reader could not read. A finding (exit 1) is a
// file it read whose CONTENT is wrong -- a duplicate id, an absent need, a cycle,
// an owner no registry names, a lane no lanes file names, a deadline that is not an
// instant. A checker that stopped at the first finding would cost a round trip per
// defect, so every finding of every rule is collected in one pass and printed.

// Unit is one `(unit "id" ...)` of a work set: the keys this reader knows, the byte
// it starts at, and the raw text of the keys it validates rather than parses.
type Unit struct {
	ID    string
	Title string
	Owner string
	Lane  string
	// Status is the :status value as written -- "closed", :review -- which is one
	// of the three ways a unit says it is done.
	Status string
	Needs  []string
	// Deadline is the :deadline text EXACTLY as written. It is parsed by Check
	// rather than here, because an unreadable deadline is a finding about one unit
	// and never a refusal of the whole set.
	Deadline string
	// Done is the :done key, read as true when it is written true, t, yes or a
	// non-empty string that folds to one of those.
	Done bool
	// Offset is the unit form's first byte, so a finding can name where it lives.
	Offset int
	// Keys is every key the form carried, known or not, in the order written. It is
	// what lets a later slice read what this one ignores without a second reader.
	Keys []string
}

// WorkSet is one work-set form: its id, its title, and its units in written order.
type WorkSet struct {
	File  string
	ID    string
	Title string
	Units []Unit
}

// ParseWorkSet reads one `(work-set ...)` form under the reader's three bounds.
// Everything it refuses is exit 2 through *Refusal: a file it cannot read, a top
// form that is not a work set, a `:units` that is not a list. Everything about the
// CONTENT of a unit is a finding from Check, never a refusal here.
func ParseWorkSet(file string, data []byte, limits Limits) (*WorkSet, error) {
	root, err := Read(file, data, limits)
	if err != nil {
		return nil, err
	}
	if root.Kind != List || len(root.List) == 0 || !isSymbolNamed(root.List[0], "work-set") {
		return nil, refuse(file, `not a work set: the top form must be (work-set "id" ... :units (...))`)
	}
	ws := &WorkSet{File: file}
	body := root.List[1:]
	if len(body) > 0 && body[0].Kind == String {
		ws.ID = body[0].Value
		body = body[1:]
	}
	var units *Form
	for i := 0; i < len(body); i++ {
		if body[i].Kind != Keyword {
			continue
		}
		switch body[i].Value {
		case "units":
			if i+1 >= len(body) || body[i+1].Kind != List {
				return nil, refuse(file, ":units must be a list of (unit ...) forms; refusing to guess")
			}
			list := body[i+1]
			units = &list
		case "title":
			if i+1 < len(body) {
				ws.Title = body[i+1].Text()
			}
		}
	}
	if units == nil {
		return nil, refuse(file, "the work set carries no :units; refusing to guess which forms are its units")
	}
	for _, form := range units.List {
		ws.Units = append(ws.Units, readUnit(form))
	}
	return ws, nil
}

// readUnit reads one member of `:units`. A member that is not a `(unit "id" ...)`
// form yields a unit with an empty id at that member's byte, which Check reports as
// a finding: the member is IN the set and saying nothing about it would hide it.
func readUnit(form Form) Unit {
	u := Unit{Offset: form.Offset}
	if form.Kind != List || len(form.List) == 0 || !isSymbolNamed(form.List[0], "unit") {
		return u
	}
	if len(form.List) > 1 && form.List[1].Kind == String {
		u.ID = form.List[1].Value
	}
	rest := form.List[2:]
	for i := 0; i < len(rest); i++ {
		key := rest[i]
		if key.Kind != Keyword {
			continue
		}
		u.Keys = append(u.Keys, key.Value)
		if i+1 >= len(rest) {
			break
		}
		val := rest[i+1]
		i++
		switch key.Value {
		case "title":
			u.Title = val.Text()
		case "owner":
			u.Owner = atomText(val)
		case "lane":
			u.Lane = atomText(val)
		case "status":
			u.Status = atomText(val)
		case "needs":
			u.Needs = idList(val)
		case "deadline":
			u.Deadline = val.Text()
		case "done":
			u.Done = truthy(val)
		}
	}
	return u
}

// isSymbolNamed reports whether f is the bare symbol (or keyword) named name. A
// coordinator writes `work-set` and `unit` bare; the same head spelled `:unit`
// reads the same, because both are data either way.
func isSymbolNamed(f Form, name string) bool {
	return (f.Kind == Symbol || f.Kind == Keyword) && f.Value == name
}

// atomText is the text of a one-atom value: a string's contents, or a symbol's or
// keyword's own name (`:status :review` says review). An integer or a list says
// nothing rather than a guess at its text.
func atomText(f Form) string {
	switch f.Kind {
	case String, Symbol, Keyword:
		return f.Value
	}
	return ""
}

// idList is a `:needs` value: a list of id strings, or one bare id. Anything else
// contributes nothing, and Check reports a unit whose needs do not resolve.
func idList(f Form) []string {
	switch f.Kind {
	case String, Symbol:
		if f.Value == "" {
			return nil
		}
		return []string{f.Value}
	case List:
		var out []string
		for _, el := range f.List {
			if (el.Kind == String || el.Kind == Symbol) && el.Value != "" {
				out = append(out, el.Value)
			}
		}
		return out
	}
	return nil
}

// truthy reads the handful of spellings a person writes for yes. Anything else is
// false: a `:done` nobody can read is not a unit this tool will call finished.
func truthy(f Form) bool {
	switch strings.ToLower(atomText(f)) {
	case "true", "t", "yes", "done", "closed":
		return true
	}
	return f.Kind == Integer && f.Int != 0
}

// doneWords are the :status spellings that say a unit has finished. They are a
// fixed list rather than a guess: a :status this list does not hold leaves the unit
// open, and --done can still name it.
var doneWords = map[string]bool{"closed": true, "done": true, "landed": true, "merged": true}

// Options is what Check needs from outside the language. Every one of them is
// optional, and an absent one turns its rule OFF rather than inventing a default:
// there is no default minds file, no default lanes file and no discovery.
type Options struct {
	// Minds is the set of known owners, folded to lower case. Nil means no --minds
	// was given and no owner is checked.
	Minds map[string]bool
	// MindsFile is what Minds was read from, named in the finding so the remedy
	// says which file to add the row to.
	MindsFile string
	// Lanes is the set of known lane names. Nil means no --lanes was given and no
	// lane is checked.
	Lanes     map[string]bool
	LanesFile string
	// Done names units that are done regardless of what the file says, for a
	// caller that holds the settled facts the document does not.
	Done map[string]bool
}

// Finding is one thing wrong with the CONTENT of a work set: the rule that caught
// it, the unit that owes it, and the fields the line prints. Rule is the word the
// SET line carries, so a caller greps one word rather than a sentence.
type Finding struct {
	Rule string
	Unit string
	// Key and Value are the rule's own field -- need=<id>, owner=<name>,
	// cycle=<a->b->a> -- kept apart so the printing site escapes the value on
	// its own and a unit id with a space can never break the line.
	Key    string
	Value  string
	Remedy string
	Offset int
}

// Detail is the rule's field as one `name=value` token, for a caller that wants the
// line's middle rather than its two halves.
func (f Finding) Detail() string {
	if f.Key == "" {
		return ""
	}
	return f.Key + "=" + f.Value
}

// Counts is the one summary line's arithmetic: units = ready + blocked + done.
// owned is the units that name an owner, the ones the machinery routes to a friend
// rather than cutting as a card.
type Counts struct {
	Units   int
	Ready   int
	Blocked int
	Done    int
	Owned   int
}

// Check validates one work set whole and returns EVERY finding, in the order the
// rules run and the units are written. It never stops at the first: a checker that
// did would cost the caller one round trip per defect.
//
// The rules:
//
//  1. every unit has an id, and no two units share one
//  2. every :needs names a unit of this set
//  3. the :needs edges are acyclic
//  4. every :owner is a mind --minds names (off without --minds)
//  5. every :lane is a lane --lanes names (off without --lanes)
//  6. every :deadline is an instant, in either RFC3339 spelling
//
// A unit is done when it says so (`:done`, or a `:status` of closed, done, landed
// or merged) or when Options.Done names it. It is ready when it is not done and
// every need is done; blocked when it is not done and some need is not.
func (w *WorkSet) Check(opts Options) ([]Finding, Counts) {
	var out []Finding
	add := func(f Finding) { out = append(out, f) }

	// rule 1: identity, before anything that names a unit by id.
	first := map[string]int{}
	present := map[string]bool{}
	for _, u := range w.Units {
		if u.ID == "" {
			add(Finding{Rule: "NO-ID", Offset: u.Offset,
				Remedy: `every member of :units is (unit "id" ...); give it an id`})
			continue
		}
		if at, dup := first[u.ID]; dup {
			add(Finding{Rule: "DUPLICATE", Unit: u.ID, Offset: u.Offset,
				Key: "first", Value: strconv.Itoa(at),
				Remedy: "two units share one id; rename one"})
			continue
		}
		first[u.ID] = u.Offset
		present[u.ID] = true
	}

	// rule 2: every need resolves. An absent need is reported ONCE per unit and
	// need, and the edge is dropped so rule 3 walks a closed graph.
	needs := map[string][]string{}
	var order []string
	seen := map[string]bool{}
	for _, u := range w.Units {
		if u.ID == "" || seen[u.ID] {
			continue
		}
		seen[u.ID] = true
		order = append(order, u.ID)
		for _, need := range u.Needs {
			if !present[need] {
				add(Finding{Rule: "NEEDS", Unit: u.ID, Offset: u.Offset,
					Key: "need", Value: need,
					Remedy: "no unit of this set has that id; add the unit or fix the spelling"})
				continue
			}
			if need == u.ID {
				add(Finding{Rule: "CYCLE", Unit: u.ID, Offset: u.Offset,
					Key: "cycle", Value: u.ID + "->" + u.ID,
					Remedy: "a unit cannot need itself"})
				continue
			}
			needs[u.ID] = append(needs[u.ID], need)
		}
	}

	// rule 3: no cycle. Every cycle is named, not only the first, and each is
	// printed once whichever of its members the walk entered it by.
	for _, cycle := range FindCycles(order, needs) {
		add(Finding{Rule: "CYCLE", Unit: cycle[0],
			Key: "cycle", Value: strings.Join(cycle, "->"),
			Remedy: "these units deadlock; drop one :needs edge"})
	}

	// rules 4, 5 and 6, per unit in written order.
	for _, u := range w.Units {
		if u.ID == "" {
			continue
		}
		if opts.Minds != nil && u.Owner != "" && !opts.Minds[strings.ToLower(u.Owner)] {
			add(Finding{Rule: "OWNER", Unit: u.ID, Offset: u.Offset,
				Key: "owner", Value: u.Owner,
				Remedy: "no mind of " + opts.MindsFile + " is named that; add the row or fix the spelling"})
		}
		if opts.Lanes != nil && u.Lane != "" && !opts.Lanes[u.Lane] {
			add(Finding{Rule: "LANE", Unit: u.ID, Offset: u.Offset,
				Key: "lane", Value: u.Lane,
				Remedy: "no lane of " + opts.LanesFile + " is named that; add the lane or drop :lane"})
		}
		if u.Deadline != "" {
			if _, err := ParseStamp(u.Deadline); err != nil {
				add(Finding{Rule: "DEADLINE", Unit: u.ID, Offset: u.Offset,
					Key: "deadline", Value: u.Deadline,
					Remedy: "write it as 2026-09-19T12:00:00Z or 2026-09-19T12:00Z"})
			}
		}
	}

	counts := Counts{Units: len(w.Units)}
	done := w.DoneSet(opts.Done)
	for _, u := range w.Units {
		if u.Owner != "" {
			counts.Owned++
		}
		switch {
		case u.ID == "":
			counts.Blocked++
		case done[u.ID]:
			counts.Done++
		case w.unmet(u, done) == "":
			counts.Ready++
		default:
			counts.Blocked++
		}
	}
	return out, counts
}

// DoneSet is every unit this run counts as finished: what the document says and
// what the caller's --done adds. It is the only input to readiness, so the ready
// set is mechanical -- a function of the language and the settled facts, never of
// anyone's judgment.
func (w *WorkSet) DoneSet(extra map[string]bool) map[string]bool {
	done := map[string]bool{}
	for _, u := range w.Units {
		if u.ID == "" {
			continue
		}
		if u.Done || doneWords[strings.ToLower(u.Status)] || extra[u.ID] {
			done[u.ID] = true
		}
	}
	for id := range extra {
		done[id] = true
	}
	return done
}

// unmet is the FIRST need of u that is not done, or "" when every one of them is.
// A need no unit of the set defines counts as unmet: rule 2 has already reported
// it, and a unit whose need does not exist is never ready.
func (w *WorkSet) unmet(u Unit, done map[string]bool) string {
	for _, need := range u.Needs {
		if !done[need] {
			return need
		}
	}
	return ""
}

// Ready is the mechanical ready set: the units that are not done and whose every
// need is done, in written order. It is what a coordinator asks the language for --
// "what can be pulled right now" -- and it is derived, never maintained by hand.
func (w *WorkSet) Ready(extra map[string]bool) []Unit {
	done := w.DoneSet(extra)
	var out []Unit
	for _, u := range w.Units {
		if u.ID == "" || done[u.ID] {
			continue
		}
		if w.unmet(u, done) == "" {
			out = append(out, u)
		}
	}
	return out
}

// Blocked is the complement of Ready among the open units: each one with the first
// need that is holding it. It is the same walk, so the two can never disagree.
func (w *WorkSet) Blocked(extra map[string]bool) map[string]string {
	done := w.DoneSet(extra)
	out := map[string]string{}
	for _, u := range w.Units {
		if u.ID == "" || done[u.ID] {
			continue
		}
		if need := w.unmet(u, done); need != "" {
			out[u.ID] = need
		}
	}
	return out
}

// stampForms are the spellings ParseStamp accepts. A coordinator writes the middle
// one -- "2026-09-19T12:00Z", RFC3339 without the seconds -- and a reader that took
// only the full one refused a real unit, so both are read and neither is guessed at.
var stampForms = []string{
	time.RFC3339,
	"2006-01-02T15:04Z07:00",
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02",
}

// ParseStamp reads any of those spellings as UTC and refuses anything else naming
// what it accepts, rather than falling back to a clock.
func ParseStamp(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty stamp")
	}
	for _, form := range stampForms {
		if t, err := time.Parse(form, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not an instant; write it as 2026-09-19T12:00:00Z or 2026-09-19T12:00Z", s)
}

// FindCycles names every `:needs` cycle in a graph, each once. refuseCycle stops at
// the first because a plan with one is refused whole; a checker prints them all,
// and both walk this one function so the two can never find different cycles.
//
// The walk is the same three-colour depth-first search: grey is the path being
// walked and a grey node reached again closes a cycle. The cycle is normalised to
// start at its smallest member so that the same loop entered by two of its members
// is reported once.
func FindCycles(order []string, needs map[string][]string) [][]string {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := map[string]int{}
	var path []string
	var found [][]string
	seen := map[string]bool{}
	var visit func(id string)
	visit = func(id string) {
		switch color[id] {
		case grey:
			start := 0
			for start < len(path) && path[start] != id {
				start++
			}
			cycle := normalizeCycle(append([]string(nil), path[start:]...))
			key := strings.Join(cycle, "\x00")
			if !seen[key] {
				seen[key] = true
				found = append(found, append(cycle, cycle[0]))
			}
			return
		case black:
			return
		}
		color[id] = grey
		path = append(path, id)
		for _, need := range needs[id] {
			visit(need)
		}
		path = path[:len(path)-1]
		color[id] = black
	}
	for _, id := range order {
		visit(id)
	}
	sort.Slice(found, func(i, j int) bool { return found[i][0] < found[j][0] })
	return found
}

// normalizeCycle rotates a cycle to begin at its smallest member, so one loop has
// one spelling however the walk entered it.
func normalizeCycle(cycle []string) []string {
	if len(cycle) < 2 {
		return cycle
	}
	min := 0
	for i := range cycle {
		if cycle[i] < cycle[min] {
			min = i
		}
	}
	return append(append([]string(nil), cycle[min:]...), cycle[:min]...)
}

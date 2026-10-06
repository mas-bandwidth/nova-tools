package sprint

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
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
	// Base is the branch the brief names on its BASE: line (swarm.ReadCardBase), "" for
	// none: add admits a card based on dev only into the promotion stream (SprintBranchWhy).
	Base string
	// Repo is the repository the brief names on its REPO: line (swarm.ReadCardBase),
	// recorded on its stream's control card as the stream's repository (FieldRepo).
	Repo string
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
	// Base is the branch Brief names on its BASE: line, as CardAdd.Base.
	Base string
	// Repo is the repository Brief names on its REPO: line, as CardAdd.Repo.
	Repo string
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
	// BriefOps is each card's brief decision op id (FieldBriefOp), by card id, and
	// BriefRecord the record that holds them (FieldBriefRecord): empty when add asked
	// none.
	BriefOps    map[string]string
	BriefRecord string
	// Replaces names the cards the one card this add admits replaces (add --replaces, twins.go):
	// it takes over every edge where a waiting card needs one of them, and each still on the
	// table is dropped "replaced by <the new id>", in the same step, raising no blocked
	// judgment.
	Replaces []string `json:",omitempty"`
	Only     []string
	Who      string
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
	if len(r.Replaces) > 0 {
		return Replace(s, r)
	}
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
	if RemovedStream(s, r.Stream) {
		// a removal is not a tombstone: the control card's record comes back
		// on the table (ComeBack), and the changes below are planned on it
		// as the place leaves it
		var pl PlaceAgain
		ctl, pl = ComeBack(ctl, r.Stream)
		p.Places = append(p.Places, pl)
	}
	var head []Change
	reopen := false
	switch {
	case ctl == nil:
		head = append(head, change(Merge, createEntry(CtlID(r.Stream), r.Stream, Ctl, 0,
			map[string]string{"kind": "stream", "state": StreamWaiting, "since": stamp(s.Now)})))
	case ctl.F("state") == StreamLanded && StopHasLanded(s, r.Stream):
		// a card past a landed stop reopens the stream; no stop, and it comes back waiting
		reopen = true
		head = append(head, change(Merge, setEntry(ctl, map[string]string{"state": StreamWorking, "since": stamp(s.Now)})))
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
	baseOf := func(i int) string {
		if len(r.Cards) > 0 {
			return r.Cards[i].Base
		}
		return r.Base
	}
	repoOf := func(i int) string {
		if len(r.Cards) > 0 {
			return r.Cards[i].Repo
		}
		return r.Repo
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
		model  string // the words of a card add tiered frontier (ModelTier)
		rules  string // FieldRules
		bench  string // FieldBench: the members its brief's BENCH line names (bench_deal.go)
		repo   string // the repository its brief's REPO: line names (FieldRepo)
		base   string // the base its brief's BASE: line names (FieldBase)
		behind string // the sentinel it waits behind by position
		gate   bool   // a stop of --sentinel-every
		sent   bool   // a stop: --sentinel or a many-brief card marked one
	}
	var in []admit
	lastGate := ""
	seen := map[string]bool{}
	for i, id := range ids {
		needs := append([]string(nil), needsOf(i)...)
		// A need names a primary that can still land: one on the table
		// (waiting, ready, working, review, merging or landed), a sentinel, or
		// one of this add. missing is the needs that name no record at all;
		// off is the needs that name a kept record off the table (a dropped
		// card), whose outcome the refusal names.
		var missing, off []string
		for _, n := range needs {
			if adding[n] {
				continue
			}
			c := s.Work.Card(n)
			switch {
			case c == nil:
				missing = append(missing, n)
			case !c.Placed() && !IsSentinel(c):
				off = append(off, n)
			}
		}
		// bench is the members its brief's BENCH line names: every placement of its work
		// cards deals it only to them, and a name that is no fleet member is refused with
		// the members there are (bench_deal.go)
		bench, benchWhy := BenchOfBrief(briefOf(i))
		if unknown := BenchKnown(s, bench); len(unknown) > 0 {
			bench, benchWhy = nil, BenchRefused(s, bench, unknown)
		}
		devWhy := SprintBranchWhy(s, r.Stream, baseOf(i), id)
		// a card whose PATHS name TLA+ model work is tiered frontier (tier_model.go)
		brief, modelSaid, modelWhy := ModelTier(briefOf(i))
		_, priorityWhy := PriorityOfBrief(briefOf(i)) // a PRIORITY line naming no level (priority.go)
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
		case devWhy != "": // a card cut on dev, outside the promotion stream (docs/SPEC-SPRINT.md section 7)
			p.refuse(id, devWhy)
			continue
		case len(missing) > 0 || len(off) > 0:
			var why []string
			if len(missing) > 0 {
				why = append(why, "needs "+strings.Join(missing, ",")+", which is no primary on the table or in this add")
			}
			for _, n := range off {
				why = append(why, "needs "+n+", which was "+orDash(s.Work.Card(n).F("outcome")))
			}
			msg := strings.Join(why, "; ")
			if len(r.Cards) > 0 {
				msg = r.Cards[i].File + ": " + msg
			}
			p.refuse(id, msg)
			continue
		case benchWhy != "":
			p.refuse(id, benchWhy)
			continue
		case modelWhy != "":
			p.refuse(id, modelWhy)
			continue
		case priorityWhy != "":
			p.refuse(id, priorityWhy)
			continue
		}
		seen[id] = true
		a := admit{id: id, score: scores[i], needs: needs, brief: brief, model: modelSaid, rules: rulesOf(i), bench: strings.Join(bench, ","), repo: repoOf(i), base: baseOf(i), gate: r.IsGate(id), sent: isSent(i)}
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
	// Every card admitted records its repository and base on its stream's control
	// card: the union of what the stream already records and this add's briefs, so a
	// stream whose cards name more than one repository keeps them all (FieldRepo,
	// FieldBase; docs/SPEC-SPRINT.md section 11, the streams verb).
	{
		var repos, bases []string
		if ctl != nil {
			repos, bases = Split(ctl.F(FieldRepo)), Split(ctl.F(FieldBase))
		}
		for _, a := range in {
			if a.repo != "" && !contains(repos, a.repo) {
				repos = append(repos, a.repo)
			}
			if a.base != "" && !contains(bases, a.base) {
				bases = append(bases, a.base)
			}
		}
		if len(repos) > 0 || len(bases) > 0 {
			set := map[string]string{}
			if len(repos) > 0 {
				sort.Strings(repos)
				set[FieldRepo] = strings.Join(repos, ",")
			}
			if len(bases) > 0 {
				sort.Strings(bases)
				set[FieldBase] = strings.Join(bases, ",")
			}
			if ctl == nil {
				for i := range head {
					e := &head[i].Entry
					if e.ID == CtlID(r.Stream) && e.Create != nil {
						if e.Set == nil {
							e.Set = map[string]string{}
						}
						maps.Copy(e.Set, set)
					}
				}
			} else {
				// one change of the control card a step: a state change already in
				// head is merged into, never changed twice (setStream's rule)
				merged := false
				for i := range head {
					e := &head[i].Entry
					if e.ID == CtlID(r.Stream) {
						if e.Set == nil {
							e.Set = map[string]string{}
						}
						maps.Copy(e.Set, set)
						merged = true
						break
					}
				}
				if !merged {
					head = append(head, change(Merge, setEntry(ctl, set)))
				}
			}
		}
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
	// changes is one it closes.
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
	// the weights the admission changes (weight.go): the cards admitted carry theirs, and
	// every primary they wait on is written its new one
	var admitted []*Card
	for _, a := range in {
		if !a.sent && !a.gate {
			admitted = append(admitted, &Card{ID: a.id, Fields: map[string]string{"needs": strings.Join(a.needs, ",")}})
		}
	}
	weighed := weighUnits(s, admitted, nil)
	weights := weightsOver(append(openPrimaries(s), admitted...))
	// the gate's measured wall, read once for the add (gate_wall.go)
	var walls map[string][]float64
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
		if kind == "primary" && col == Ready {
			fields[FieldReadyAt] = stamp(s.Now)
		}
		if r.Held {
			fields[FieldHeld] = stamp(s.Now)
		}
		gateSaid := ""
		if a.brief != "" && !a.gate {
			fields["brief"] = a.brief
			if a.rules != "" {
				fields[FieldRules] = a.rules
			}
			if who := WhoOfBrief(a.brief); who != "" {
				fields[FieldWho] = who // a friend's card: the tick deals it to a friend (friend_deal.go)
			}
			if a.bench != "" {
				fields[FieldBench] = a.bench // its bench: dealt only to the members it names (bench_deal.go)
			}
			if op := r.BriefOps[a.id]; op != "" {
				fields[FieldBriefOp], fields[FieldBriefRecord] = op, r.BriefRecord
			}
			if !a.sent {
				if walls == nil {
					walls = gateWalls(s)
				}
				set, said := gateTier(walls, a.brief)
				maps.Copy(fields, set)
				gateSaid = said
			}
		}
		if kind == "primary" {
			if lvl := seedPriority(ctl, a.brief); lvl != "" {
				fields[FieldPriority] = lvl // its brief's PRIORITY line, else its stream's default (priority.go)
			}
		}
		if len(a.needs) > 0 {
			fields["needs"] = strings.Join(a.needs, ",")
		}
		if n := weights[a.id]; n > 0 && !a.sent && !a.gate {
			fields[FieldBehind] = itoa(n)
		}
		u := Unit{Key: a.id, Stream: r.Stream, Moved: fmt.Sprintf("%s -> %s stream=%s score=%s", a.id, col, r.Stream, fmtScore(a.score))}
		if a.gate {
			u.Moved = "sentinel " + u.Moved
		}
		if r.Held {
			u.Moved += "; held until release"
		}
		if gateSaid != "" {
			u.Moved += "; " + gateSaid
		}
		if a.model != "" {
			u.Moved += "; " + a.model
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
	p.Units = append(p.Units, weighed...)
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
	if reopen && admits(p) {
		var stopID string
		var admitted, extra []string
		var got []float64
		for _, a := range in {
			extra = append(extra, a.id)
			if !planCreates(p, a.id) {
				continue
			}
			got = append(got, a.score)
			if a.sent || a.gate {
				stopID = a.id
				continue
			}
			admitted = append(admitted, a.id)
		}
		p = reopenAdd(p, s, r.Stream, r.Who, stopID, admitted, got, extra)
	}
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
		for _, pl := range q.Places {
			if !slices.ContainsFunc(p.Places, func(o PlaceAgain) bool { return o.Table == pl.Table && o.ID == pl.ID }) {
				p.Places = append(p.Places, pl)
			}
		}
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
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, moveEntry(c, c.Row, Ready, readyStamp(c, s.Now)))},
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
		out = append(out, Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, moveEntry(c, c.Row, Ready, readyStamp(c, s.Now)))},
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
// card is dealt to the next member round the fleet (round.go): the first from
// the rolling index, wrapping, that is up and holds fewer work cards, ready
// and working, than its width (width.go; tla/DirtyTick.tla, Room and
// WidthRespected); a card no up member has room for is refused, never dealt
// past a width; the index (the fleet table's deal_index, a counter) moves
// past the member dealt to, written with the deal.
// Every card of the selection is dealt in the one plan, one card at a time
// round the fleet. A card withdrawn because no member was up is the
// same card dealt again at a new generation, its attempt unchanged; otherwise
// the next attempt's card is cut.
func Deal(s *Snapshot, r DealReq) Plan {
	s, _ = s.withRests() // the resting routes, read once (rule 3, route_rest.go)
	rr, ri := dealRound(s), routeIndexesOf(s)
	p, moves := dealPlan(s, r, rr, ri)
	p = Lawful(p)
	roundWrites(&p, rr, moves)
	// each tier's route index moves by the cards dealt on it (route.go)
	ri.write(&p)
	// the streams take turns from the work table's stream index (round.go):
	// it moves past the stream of the last card dealt
	streamIndexWrite(&p, streamRound(s, PropStreamIndex), s.Work.Placed)
	return p
}

// noRoomWhy is the refusal of a card every up member is at its room for (DealAhead times its
// width, width.go): the tick deals it when one has room.
const noRoomWhy = "every up fleet member is at its room (DealAhead times its width): the tick deals it when one has room"

func dealPlan(s *Snapshot, r DealReq, rr *round, ri routeIndexes) (Plan, roundMoves) {
	var p Plan
	moves := roundMoves{}
	ready := func(c *Card) string {
		if StreamHeld(s, c.Row) {
			return "its stream " + c.Row + " is held by the coordinator (hold.go): nova-sprint unhold " + c.Row + " deals it again"
		}
		if OnlyFriend(c) {
			return friendCardWhy
		}
		return inState(c, Ready)
	}
	chosen := pick(&p, r.Sel, eligibleTurns(s.Work.Column(Ready), ready, streamRound(s, PropStreamIndex)), rowOf, ready, s.primaryCard)
	up := s.UpMembers()
	if s.FleetOff() {
		for _, c := range chosen {
			p.refuse(c.ID, "the fleet's work is off: no card is dealt to a machine; run: nova-sprint set --fleet on")
		}
		return p, moves
	}
	if len(up) == 0 {
		for _, c := range chosen {
			p.refuse(c.ID, "no fleet member is up: a member is up while its machine beats; start nova-sprint fleet beat <member> on a machine, or release a hold with nova-sprint fleet up <member>")
		}
		return p, moves
	}
	// a quiet member is dealt nothing until its quiet ends (fleet_quiet.go;
	// docs/SPEC-SPRINT.md section 5, fleet-quiet-machine-b.w7)
	quiet := quietWhy(s, up)
	up = notQuiet(s, up)
	q, widths := memberLoads(s, up), memberWidths(s, up)
	for _, c := range chosen {
		// its bench: the members its brief's BENCH line names, and the deal deals it to
		// none other; with no member of it up it waits ready (bench_deal.go)
		bench := Bench(c)
		members := onlyBench(up, bench)
		if len(members) == 0 {
			p.refuse(c.ID, benchRefusal(bench))
			continue
		}
		next := func() string { return rr.next(members, q, widths, "") }
		roomWhy := noRoomWhy
		if len(bench) > 0 {
			roomWhy = benchRoom(bench)
		}
		if quiet != "" {
			roomWhy += "; " + quiet
		}
		if wc := s.Fleet.Placed(WorkCardID(c.ID, c.Int("attempt"))); wc != nil && wc.Col == Withdrawn {
			if redealBound(wc) {
				tier := s.NextTier(c)
				atCard := c
				if c.F(FieldAttemptsRan) != "" {
					atCard = withField(c, "attempt", itoa(c.Int(FieldAttemptsRan)+c.Int(FieldBriefAttempt)))
				}
				if _, atCap := AtBriefBound(atCard, "", s.AttemptsCap(c.Row)); atCap {
					tier = "" // the attempt cap: not dealt again, the tick's judgment says so (AtRedealBound)
				}
				if tier == "" {
					why := ": rework it with a fix, or drop it"
					if held, _ := reworkAtTheSameBound(s, c, wc, ""); held != "" {
						why = "; " + held // the attempt before ended at its bound on its tier (failure.go)
					}
					p.refuse(c.ID, boundWhat(wc, c.ID)+why)
					continue
				}
				// below its ceiling: the machine escalates it, a new attempt on the next tier
				m := next()
				if m == "" {
					p.refuse(c.ID, roomWhy)
					continue
				}
				on, _ := CardTiers(c)
				why := fmt.Sprintf("attempt %s reached its bound on %s (redealt %d times)%s", wc.F("attempt"), on, wc.Int("redeals"), providerWhy(wc))
				if class := identicalEnds(wc); class != "" {
					// rule 2 inside one attempt: its last two takes ended the same way (failure.go)
					why = fmt.Sprintf("attempt %s reached its bound on %s (its last two takes ended the same way: %s)", wc.F("attempt"), on, class)
				}
				u, refused := escalate(s, c, wc, tier, why, m, q, ri)
				if refused != "" {
					p.refuse(c.ID, refused)
					continue
				}
				rr.moved(m)
				moves[c.ID] = m
				p.Units = append(p.Units, u)
				continue
			}
			if _, _, why, _ := s.routeOf(c, nil, nil); why != "" {
				p.refuse(c.ID, why) // a machine draws a route: a tier friends alone serve is theirs
				continue
			}
			// a member that refused it at staging is not dealt it again (StagingRefusers)
			m := next()
			if refused := StagingRefusers(wc); len(refused) > 0 {
				others := without(members, refused)
				if len(others) == 0 {
					p.refuse(c.ID, fmt.Sprintf("%s was refused at staging by every member up (%s): rework it with a fix, or drop it", wc.ID, strings.Join(refused, ", ")))
					continue
				}
				m = rr.next(others, q, widths, "")
			}
			if m == "" {
				p.refuse(c.ID, roomWhy)
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
		if _, _, why, _ := s.routeOf(c, nil, nil); why != "" {
			p.refuse(c.ID, why) // a machine draws a route: a tier friends alone serve is theirs
			continue
		}
		m := next()
		if m == "" {
			p.refuse(c.ID, roomWhy)
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
	route, _, why, _ := s.routeOf(c, nil, ri)
	if why != "" {
		return Unit{}, why
	}
	q[m]++
	fields := map[string]string{"kind": "work", "primary": c.ID, "stream": c.Row, "attempt": itoa(attempt), "gen": "1", "member": m,
		"dealt": stamp(s.Now), "first_dealt": stamp(s.Now), "untaken_since": stamp(s.Now)}
	if fix != "" {
		fields["fix"] = fix
	}
	priorityOnWork(fields, c)
	// the attempt decision's bars, which its failed finish is routed by (decide.go)
	bars, _ := s.attemptBars()
	maps.Copy(fields, bars)
	for k, v := range given { // what a rework adds on the attempt's work card: its finding and why
		if v != "" {
			fields[k] = v
		}
	}
	if set == nil {
		set = map[string]string{}
	}
	work, primary := splitRoute(route)
	s.dealDeadline(m, work) // the member's deadline, from the card's own (deadline.go)
	for k, v := range work {
		if v != "" {
			fields[k] = v
		}
	}
	maps.Copy(fields, s.gateFields())
	maps.Copy(set, primary)
	set["attempt"], set["work"] = itoa(attempt), card
	return Unit{Key: c.ID, Stream: c.Row, Changes: []Change{
		change(Fleet, createEntry(card, m, Ready, c.Score, fields)),
		change(Work, moveEntry(c, c.Row, Working, set, append(unset, "result")...)),
	}, Moved: fmt.Sprintf("%s work %s -> working card=%s member=%s (fleet ready)", c.ID, c.Col, card, m)}, ""
}

// escalate deals the primary c a new attempt on the next tier, tier (NextTier), into the
// ready queue of the up member m, when its attempt reached its bound below its ceiling:
// the machine's step, raising no judgment (route.go, tierLadder; the owner, 2026-10-02,
// cost rule 1 of nova-tools#5174: "Flash first on every card; pro only on escalation").
// The primary records the tier (FieldTierNow) and every later deal draws from it; the
// attempt that reached its bound, prev, is retired as a rework at the bound retires it;
// the new attempt's work card is told why (the bound and the tiers), the brief and the
// fix are the card's own.
func escalate(s *Snapshot, c, prev *Card, tier, why, m string, q map[string]int, ri routeIndexes) (Unit, string) {
	from, _ := CardTiers(c)
	set := map[string]string{FieldTierNow: tier}
	given := map[string]string{"finding": c.F("finding"), "why": fmt.Sprintf("escalated from %s to %s: %s", from, tier, why)}
	u, refused := deal(s, withField(c, FieldTierNow, tier), c.F("fix"), m, q, ri, set, given)
	if refused != "" {
		return Unit{}, refused
	}
	u.Changes = append([]Change{change(Fleet, removeEntry(prev, map[string]string{"retired": stamp(s.Now), "retired_by": "escalation"}))}, u.Changes...)
	u.Moved += fmt.Sprintf("; escalated %s -> %s: %s", from, tier, why)
	return u, ""
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
	route, _, why, _ := s.routeOf(c, wc, ri)
	if why != "" {
		return Unit{}, why
	}
	q[m]++
	set := nextGen(wc, m, s.Now)
	if wc.F(FieldTakeEnded) != "" {
		set["redeals"] = itoa(wc.Int("redeals") + 1)
	}
	unset := []string{"withdrawn", FieldTakeEnded, FieldProviderError, FieldDecided, FieldDecidedUsed}
	// the attempt decision's bars as the sprint row holds them now (decide.go)
	bars, none := s.attemptBars()
	maps.Copy(set, bars)
	unset = append(unset, none...)
	work, primary := splitRoute(route)
	s.dealDeadline(m, work) // the member's deadline, from the card's own (deadline.go)
	for k, v := range work {
		if v == "" {
			unset = append(unset, k)
			continue
		}
		set[k] = v
	}
	g := s.gateFields()
	for _, k := range []string{FieldDecideGateFlaky, FieldDecideGatePreexisting} {
		if v, ok := g[k]; ok {
			set[k] = v
		} else {
			unset = append(unset, k)
		}
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
	live := max(c.Int("gen"), 1) // legacy first read cards omit gen; their lease is g1
	switch {
	case !ok:
		return fmt.Sprintf("names no generation; the live one is %d: %s %s@%d", live, verb, c.ID, live)
	case g != live:
		return fmt.Sprintf("stale: generation %d is not the live one (%d): the card was dealt again to %s", g, live, orDash(c.Row))
	}
	return ""
}

// Take moves the member's work cards fleet ready -> working. As may name
// several members, comma separated: each takes from its own ready queue, up
// to the limit, in the one plan (the world's workers move in one batch a
// tick, every member's row at once); a take by id
// names one member.
func Take(s *Snapshot, r TakeReq) Plan {
	members := Split(r.As)
	if len(members) <= 1 {
		return takeOne(s, r)
	}
	var p Plan
	if named(r.Sel) {
		for _, id := range r.IDs {
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

// takeSeat is the width a take for as is held to, or why it takes nothing. A machine
// takes while its control card says up, to its width. A friend's row has no control card
// status (only a machine's has; docs/SPEC-SPRINT.md section 1, a take for a friend): she
// takes while FriendStatus says up, as the snapshot's friend seats carry it (her session's
// evidence: a wake ping her session answered, or a card of hers finished), to her width
// (1 in one-shot mode), and is refused when she is held, her beat says down, or her session
// has given no evidence within its window, the refusal naming which (FriendDownWhy).
// The model is tla/FriendPresence.tla (Take, TakeOnlyWhenUp, ReadyTakenWhileUp; the
// witness "ctlstatus" is the control card read that refused every friend up).
func takeSeat(s *Snapshot, as string) (width int, why string) {
	if f, ok := FriendOfRow(as); ok {
		i := slices.IndexFunc(s.Friends, func(x FriendSeat) bool { return x.Name == f })
		if i < 0 {
			return 0, "no friend " + f + " on the roster"
		}
		seat := s.Friends[i]
		if seat.Status != Up {
			return 0, "friend " + f + " is " + orDash(seat.Status) + ": " + cmp.Or(seat.Why, "not up")
		}
		_, width = friendRoom(seat)
		return width, ""
	}
	if !s.Fleet.HasRow(as) {
		return 0, "no fleet member " + as
	}
	if st := s.MemberCtl(as).F("status"); st != Up {
		return 0, "member " + as + " is " + orDash(st)
	}
	return s.Width(as), ""
}

func takeOne(s *Snapshot, r TakeReq) Plan {
	var p Plan
	sel := r.Sel
	if !named(sel) && sel.Limit == 0 {
		sel.Limit = 1
	}
	width, why := takeSeat(s, r.As)
	worker := "member"
	if IsFriendRow(r.As) {
		worker = "friend"
	}
	if why != "" {
		for _, id := range sel.IDs {
			p.refuse(id, why)
		}
		return p
	}
	byID := named(sel)
	// THE WIDTH IS HARD: a member's working cards never pass its width, held here, at the
	// sprint's one writer, whatever the member asks. A take by
	// count is cut to the room; a take by id past it is refused. While read cards are on a
	// read working holds half a slot (read_cards.go): the room is counted in half slots,
	// twice the width less twice the work and the reads working, a read taking one and a
	// work card two.
	halves := s.ReadCardsOn()
	room := max(width-len(s.Fleet.Cell(r.As, Working)), 0)
	if halves {
		ww, wr := rowWorking(s, r.As)
		room = max(2*width-2*ww-wr, 0)
	}
	cost := func(c *Card) int {
		if halves && !isRead(c) {
			return 2
		}
		return 1
	}
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
	// from every stream alike, never one stream's lowest scores first. Its reads are taken
	// before its work, a read being at reader priority (priority.go).
	ready := takeTurns(s.Fleet.Cell(r.As, Ready), slices.Index(s.Members(), r.As))
	if halves {
		slices.SortStableFunc(ready, func(a, b *Card) int {
			if isRead(a) == isRead(b) {
				return 0
			}
			if isRead(a) {
				return -1
			}
			return 1
		})
		if !byID {
			// a take by count takes the cards that fit, in order: a work card that does not
			// fit is passed over for the reads after it
			var fit []*Card
			left := room
			for _, c := range ready {
				if cost(c) <= left {
					fit = append(fit, c)
					left -= cost(c)
				}
			}
			ready = fit
		}
	}
	chosen := pick(&p, sel, ready, fieldStream, func(c *Card) string {
		if byID {
			if why := liveGen("take", c, r.Gens); why != "" {
				return why
			}
		}
		if !c.Placed() || c.Row != r.As || c.Col != Ready {
			return "not in " + r.As + " ready (it is " + placeWord(c) + ")"
		}
		// a card dealt before its route rested is never taken there: the provider would
		// refuse it, spending a redeal; the tick withdraws it (restWithdrawals)
		if rest, ok := cardRest(s, c); ok {
			return "its route " + c.F(FieldRoute) + " rests until " + rest.UntilSaid() + " (" + rest.Said() + "): the tick withdraws it and deals it again on a route that serves"
		}
		if byID {
			if room < cost(c) {
				return fmt.Sprintf("%s %s is at its width (%d working of %d): a card is taken when one is reported", worker, r.As, len(s.Fleet.Cell(r.As, Working)), width)
			}
			room -= cost(c)
		}
		return ""
	}, s.Fleet.Card)
	for _, c := range chosen {
		set, unset := takenStamps(c, s.Now), []string{"untaken_since"}
		if friend, ok := FriendOfRow(r.As); ok {
			set, unset = friendTaken(s, c, friend) // her deadline, as her deal and her next set it
		}
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, c.Row, Working, set, unset...))},
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
	// Decided is the take's attempt decision, as its member asked it (decide.Decided:
	// `<class> p=<p> op=<op>`; decide.go): its op names the take's card and attempt, else the
	// finish is refused (decidedFor); kept on the work card and the primary, and a failed
	// finish whose class is no-result or nothing-to-do at or above that class's bar on the
	// card is routed by it (finishKind). A finish carrying one names one card.
	Decided string
	// Reported is when the worker wrote its report, when the transport knows it (a
	// friend's REPORT.md, friend sync): kept on the work card as FieldReported, no later
	// than the finish, so the stats time a friend's run from her take to her report and
	// her report lag from it to the finish (RunWall). Zero is unknown.
	Reported time.Time `json:",omitzero"`
	Who      string
	Friends  []FriendSeat
}

// Finish moves work cards working -> done and their primaries working ->
// review. Fixed work that comes back ok is asked by the machine's ask, as
// any work is.
// A finish always names the generation it holds for every card it finishes:
// a card without one is refused, naming the live generation, and a finish
// by selection without --as is refused outright. As may name several
// members, comma separated: every card named is on one of them, each
// finished as its own member's, in the one plan.
func Finish(s *Snapshot, r FinishReq) Plan { return Lawful(finishPlan(s, r)) }

func finishPlan(s *Snapshot, r FinishReq) Plan {
	if len(r.Friends) > 0 {
		s.Friends = r.Friends
	}
	var p Plan
	declared := map[string]bool{}
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
		if c.F("kind") == "read" {
			// a read on a friend's row is no work: it is returned, never finished
			return "a read, not work: finish does not return it; " + FriendReadOutboxLine(c.Row, c.ID, s.Epoch)
		}
		if byID {
			if why := liveGen("finish", c, r.Gens); why != "" {
				return why
			}
		}
		late := byID && lateFinish(s, c)
		if (!c.Placed() || c.Col != Working) && !late {
			return "not working (it is " + placeWord(c) + ")"
		}
		if len(members) > 0 && !contains(members, c.Row) {
			return "dealt to " + c.Row + ", not " + r.As
		}
		if late {
			return lateFinishWhy(c, r)
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
	if r.Decided != "" && len(chosen) > 1 {
		// an attempt decision is one take's: it names the card it decided
		for _, c := range chosen {
			p.refuse(c.ID, "a finish carrying an attempt decision (--decision) names one card; finish each card in its own verb")
		}
		return p
	}
	for _, c := range chosen {
		// a finish is routed only by a decision of its own take (decide.go, decidedFor)
		if why := decidedFor(c, r.Decided); why != "" {
			p.refuse(c.ID, why)
			continue
		}
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
		late := c.Col == DoneFailed // a late report for the attempt a deadline failed (lateFinish)
		// how a failed finish is routed: by the reason line's prefix, or by the take's
		// attempt decision at or above its class's bar on the card (decide.go, finishKind)
		kind, class, used := "", "", false
		if r.Failed {
			kind, class, used = finishKind(c, r)
		}
		blame, defectClass, finding, fix := ClassifyAttempt(r.Report, r.Failed)
		// a lane ended at its tier's cap: the first cap re-deals the card one tier up before
		// it counts as a failure (lane_cap.go)
		lc, capped := LaneCap{}, false
		if r.Failed && kind == "" {
			lc, capped = ParseLaneCap(r.Report)
		}
		if next := capNextTier(pr); capped && next != "" {
			p.Units = append(p.Units, capRedeal(s, c, pr, r, lc, next, p.Units))
			continue
		}
		switch kind {
		case cardhdr.EndProvider, cardhdr.EndNoResult:
			p.Units = append(p.Units, takeEnded(s, c, pr, r, kind, used))
			continue
		case cardhdr.EndStaging:
			p.Units = append(p.Units, stagingRefused(s, c, pr, r))
			continue
		case cardhdr.EndLaunch:
			p.Units = append(p.Units, launchRefused(s, c, pr, r, cardhdr.EndLaunch))
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
		// It goes back to review at that head, where the machine's ask asks its readers
		// (docs/SPEC-SPRINT.md section 6; Rework sets FieldPassedHead).
		passed := r.Failed && r.Head == "" && IsNothingNew(r.Report) && pr.F(FieldPassedHead) != ""
		if passed {
			head, result, okWord, into = pr.F(FieldPassedHead), "ok", "yes", DoneOK
		}
		// A HOLD naming a brief defect is the brief's, never the worker's (brief_defect.go;
		// docs/SPEC-SPRINT.md section 1, a brief defect): its work card ends in the member's
		// defect cell, in neither done nor ok%, and the primary waits in review for the brief
		// to be cut again, its failure counted on the stream and never toward a tier.
		defect := ""
		if r.Failed && kind == "" && !passed {
			defect = BriefDefectOf(r.Report)
		}
		if defect != "" {
			into = DoneDefect
		}
		cardSet := map[string]string{"ok": okWord, "head": head, "finished": stamp(s.Now)}
		if blame != "" {
			cardSet[FieldBlame] = blame
		}
		if defectClass != "" {
			cardSet[FieldDefectClass] = defectClass
		}
		if finding != "" {
			cardSet["finding"] = finding
		}
		if fix != "" {
			cardSet["fix"] = fix
		}
		if !r.Reported.IsZero() {
			at := r.Reported
			if at.After(s.Now) {
				at = s.Now // her clock ahead of the sprint's: never after the finish
			}
			cardSet[FieldReported] = stamp(at)
		}
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
		if capped {
			capSets(lc, cardSet)
		}
		if defect != "" {
			cardSet[FieldBriefDefect] = defect
		}
		set := map[string]string{"head": head, "result": result}
		maps.Copy(set, finishStamps(pr, c, s.Now))
		decidedSets(r, used, pr, cardSet, set)
		identical := false
		if r.Failed && !passed && defect == "" {
			set["failed"] = itoa(pr.Int("failed") + 1)
			if late {
				set["failed"] = pr.F("failed") // the attempt failed once: its late HOLD is the same failure
			}
			// rule 2: the attempt before failed the same way, so this is the bound's (failure.go);
			// a decided class is the class when the decision routed the finish
			identical = failureSet(pr, pr.Int("attempt"), r.Report, class, cardTierOf(pr), set)
		}
		attemptsRan := pr.Int(FieldAttemptsRan)
		isRefusal := defectClass == DefectLaunchRefused || strings.HasPrefix(strings.TrimSpace(r.Report), cardhdr.EndLaunch)
		isProvider := defectClass == DefectProvider || IsProviderFailure(r.Report) || IsNoResult(r.Report)
		if !r.Failed || (!isRefusal && !isProvider) {
			attemptsRan++
			set[FieldAttemptsRan] = itoa(attemptsRan)
		} else {
			set[FieldAttemptsRan] = itoa(attemptsRan)
		}
		cons := workConsumer(s, c, 0, result, rec)
		if capped {
			cons.End, cons.Cap, cons.Overrun = laneCapEnd, lc.Cap.String(), lc.Overrun.String()
		}
		addConsumer(pr, set, cons)
		targetRow := c.Row
		if r.Failed && blame == BlameCoordinator {
			coord := s.Coordinator
			if coord == "" {
				coord = "coordinator"
			}
			targetRow = FriendRow(coord)
			if !s.Fleet.HasRow(targetRow) && !declared[targetRow] {
				p.Rows = append(p.Rows, RowAdd{Fleet, targetRow})
				declared[targetRow] = true
			}
		}
		moved := fmt.Sprintf("%s working -> done %s; %s working -> review", c.ID, result, pr.ID)
		if targetRow != c.Row {
			moved = fmt.Sprintf("%s working -> done %s on %s; %s working -> review", c.ID, result, targetRow, pr.ID)
		}
		u := Unit{Key: c.ID, Stream: pr.Row, Changes: []Change{change(Fleet, moveEntry(c, targetRow, into, cardSet))},
			Moved: moved}
		if late {
			// the attempt's failed judgments are this report's to answer: closed, and the
			// finish writes its own below
			u.Closes = closesFor(s.Open, []string{NWorkFailed, NBound, NStranded}, pr.ID)
			u.Moved = fmt.Sprintf("%s failed -> done %s (a late report for the attempt the deadline failed); %s review at attempt %d", c.ID, result, pr.ID, pr.Int("attempt"))
		}
		if defect != "" {
			set[FieldBriefDefect] = defect
			u.Moved = fmt.Sprintf("%s working -> done defect (a brief defect: %s); %s working -> review", c.ID, defect, pr.ID)
			if s.StreamCtl(pr.Row) != nil {
				u.Bumps = append(u.Bumps, Bump{Table: Merge, ID: CtlID(pr.Row), Field: FieldBriefDefects, Delta: 1})
			}
		}
		attempt := pr.Int("attempt")
		briefAt := pr.Int(FieldBriefAttempt)
		// the brief's bound as this finish leaves the card (brief_bound.go): the same failure
		// escalates below the ceiling only under the attempt cap
		effectiveCard := withField(withField(pr, FieldCostTotal, set[FieldCostTotal]), "attempt", itoa(attemptsRan+briefAt))
		bb, atBound := AtBriefBound(effectiveCard, r.Report, s.AttemptsCap(pr.Row))
		if next := s.NextTier(pr); identical && next != "" && !atBound {
			// the second identical failure below its ceiling (rules 1 and 2, nova-tools#5174:
			// "Flash first on every card; pro only on escalation"): no judgment; the primary
			// takes the next tier and its why and goes back to ready, as a rework with no
			// member up leaves it, and the tick's deal cuts its next attempt there. The new
			// tier counts its own failures: the record of this one is not carried up.
			from, _ := CardTiers(pr)
			why := fmt.Sprintf("escalated from %s to %s: attempts %d and %d failed the same way (%s) on %s", from, next, attempt-1, attempt, set[FieldFailure], from)
			set[FieldTierNow], set["why"] = next, why
			reworkPriority(s, pr, set)
			delete(set, "result")
			delete(set, FieldFailure)
			delete(set, FieldFailureAt)
			delete(set, FieldFailureTier)
			delete(set, FieldFailureBound)
			u.Changes = append(u.Changes, change(Work, moveEntry(pr, pr.Row, Ready, set, "result", FieldFailure, FieldFailureAt, FieldFailureTier, FieldFailureBound)))
			u.Moved = fmt.Sprintf("%s working -> done %s; %s working -> ready (%s)", c.ID, result, pr.ID, why)
			p.Units = append(p.Units, u)
			continue
		}
		// ONE PATH ASKS: the finish asks no reader. The machine's ask does, in the tick the
		// finish wakes, the earlier pair first (Ask, the primary's asked field), each read
		// with the route it draws. A read the finish creates itself carries no route (a
		// finish loads none), so no reader can start it.
		asked := map[string]string{}
		if passed {
			n := happened(NWorkOK, pr.Row, s.Now, pr.ID)
			n.Who, n.Attempt = who, attempt
			n.What = "nothing new at " + head + ", which a reader passed: back in review at it; " + r.Report
			u.Notes = append(u.Notes, n)
		} else if !r.Failed {
			n := happened(NWorkOK, pr.Row, s.Now, pr.ID)
			n.Who, n.Attempt = who, attempt
			if strings.Contains(r.Report, cardhdr.RemainderKey) {
				// a tree card that finished at the step before its failed step: the note
				// names the remainder card for the coordinator to add (docs/SPEC-SPRINT.md,
				// a card is a tree of steps)
				n.What = r.Report
			}
			u.Notes = append(u.Notes, n)
		} else if defect != "" {
			// the brief's judgment: re-cut it, never a redeal of the brief as cut
			n := judgment(NBriefDefect, pr.Row, s.Now, 0, pr.ID)
			n.Who, n.Attempt, n.Card = who, attempt, c.ID
			n.What = "a brief defect, " + defect + ": re-cut the brief; " + r.Report
			u.Notes = append(u.Notes, n)
		} else if atBound {
			// the attempt cap on one brief, whatever this attempt's failure: the brief is
			// wrong, not the worker (brief_bound.go)
			n := judgment(NBriefWrong, pr.Row, s.Now, 0, pr.ID) // its decisions alone: it is the repeat
			n.Who, n.Attempt, n.What = who, attempt, bb.String()+"; attempt "+itoa(attempt)+" failed: "+r.Report
			u.Notes = append(u.Notes, n)
		} else if identical {
			// the second identical failure (rule 2): the bound's judgment at once, in the words
			// the tick holds it by (AtIdenticalFailure), never a third try on the same tier
			n := Note{Kind: Judgment, Type: NBound, Stream: pr.Row, Primaries: []string{pr.ID}, Count: 1, At: s.Now, Who: who, Marked: true,
				Card: c.ID, Attempt: attempt, What: identicalWorkWhat(c.ID, attempt, set[FieldFailure], pr.ID),
				Decisions: append([]string(nil), TickDecisions[NBound]...)}
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
		if !late { // a late report frees no lane: the deadline's failure freed it
			friendNext(s, c, &u, p.Units)
		}
		p.Units = append(p.Units, u)
	}
	return p
}

// lateFinish says the finish of work card c is a late report for the attempt a deadline
// already failed (docs/SPEC-SPRINT.md section 8, "A late report finishes the failed
// attempt"; tla/SprintRules.tla, Part "answers", LateReportFinishes): c is done failed, and its primary is in review on it at its attempt, failed,
// so no later attempt has started. Such a report finishes that attempt rather than be
// refused, so the card does not go round again for work that is done. Only an attempt the
// deadline failed takes one (deadlineFailed): an attempt its own worker failed was
// reported already, and a finish for it again (a retry, a duplicate) is refused as before.
func lateFinish(s *Snapshot, c *Card) bool {
	if c == nil || !c.Placed() || c.Col != DoneFailed || c.F("kind") == "read" {
		return false
	}
	if !deadlineFailed(c.F("report")) {
		return false
	}
	pr := s.Work.Placed(c.F("primary"))
	return pr != nil && pr.Col == Review && pr.F("work") == c.ID && pr.F("result") == "failed" && pr.Int("attempt") == c.Int("attempt")
}

// deadlineFailed says a work card's failed report is the attempt's end with no report from
// its worker: the lane died or the runner ended it, the deadline passed, or no result came
// back (HarnessFault's classes "lane died", "deadline" and "no result"). Any other failed
// report, "red" or a HOLD, is the worker's own finish, and no later report answers it.
func deadlineFailed(report string) bool {
	switch HarnessFault(report) {
	case "lane died", "deadline", "no result":
		return true
	}
	return false
}

// lateFinishWhy is why a late report is refused: only a LAND with its head or a HOLD (a
// failed report that says the word) finishes a failed attempt. A FAIL, a provider failure, a
// take with no result, a staging refusal and a lane cap are the deadline's failure again, and
// the very report the attempt failed on already is a retry of the finish that failed it
// (store.TestARestartOnADumpWithoutTheResultsCannotAnswerTheRetry), not a late report.
func lateFinishWhy(c *Card, r FinishReq) string {
	if r.Decided != "" {
		return "failed already (" + placeWord(c) + "): a late report carries no attempt decision"
	}
	if !r.Failed {
		return ""
	}
	if !reportHolds(r.Report) {
		return "not working (it is " + placeWord(c) + "): its attempt failed already, and this report is no LAND or HOLD"
	}
	if strings.TrimSpace(r.Report) == strings.TrimSpace(c.F("report")) {
		return "not working (it is " + placeWord(c) + "): its attempt failed already on this same report"
	}
	return ""
}

// friendNext is a friend's own take: her finish moves the oldest ready card on her row
// (the deal dealt it ready behind her working cards: friendDealPass) into working in the
// same step, taken now, so she never waits for a tick between one card and the next
// (the owner, 2026-10-04: "just like the fleet"). In one-shot mode (docs/SPEC-SPRINT.md
// section 1, "A friend's card"), the next is only after the last finished: already-started
// work is preserved, but queued promotion is gated until shared work/read occupancy on her
// row reaches zero. A machine's finish does nothing of the kind: the member takes.
func friendNext(s *Snapshot, c *Card, u *Unit, prior []Unit) {
	if !IsFriendRow(c.Row) {
		return
	}
	name, _ := FriendOfRow(c.Row)
	if s.FriendMode(name) == config.FriendModeOneShot {
		if friendOccupancy(s, c.Row, u, prior) > 0 {
			return
		}
	}
	ready := append([]*Card(nil), s.Fleet.Cell(c.Row, Ready)...)
	ready = slices.DeleteFunc(ready, func(rc *Card) bool {
		return unitPromoted(prior, rc.ID)
	})
	if len(ready) == 0 {
		return
	}
	SortCards(ready)
	next := ready[0]
	set, unset := friendTaken(s, next, name)
	u.Changes = append(u.Changes, change(Fleet, moveEntry(next, c.Row, Working, set, unset...)))
	u.Moved += fmt.Sprintf("; %s ready -> working (her next, taken now)", next.ID)
}

func friendOccupancy(s *Snapshot, row string, u *Unit, prior []Unit) int {
	active := 0
	for _, card := range s.Fleet.Cell(row, Working) {
		if card.ID == u.Key || unitFinishes(prior, card.ID) {
			continue
		}
		active++
	}
	for _, p := range prior {
		for _, ch := range p.Changes {
			if ch.Table == Fleet && ch.Entry.Move != nil && ch.Entry.Move.Row == row && ch.Entry.Move.Col == Working {
				active++
			}
		}
	}
	return active
}

func unitFinishes(units []Unit, cardID string) bool {
	for _, u := range units {
		if u.Key == cardID {
			return true
		}
	}
	return false
}

func unitPromoted(units []Unit, cardID string) bool {
	for _, u := range units {
		for _, ch := range u.Changes {
			if ch.Table == Fleet && ch.Entry.ID == cardID && ch.Entry.Move != nil && ch.Entry.Move.Col == Working {
				return true
			}
		}
	}
	return false
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
func takeEnded(s *Snapshot, c, pr *Card, r FinishReq, kind string, decided bool) Unit {
	set := nextGen(c, "", s.Now)
	set["withdrawn"], set[FieldTakeEnded] = stamp(s.Now), stamp(s.Now)
	set[FieldBlame] = BlameProvider
	set[FieldDefectClass] = DefectProvider
	set["finding"] = ExtractFindingFirstLine(r.Report)
	line := cutText(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(r.Report, kind), ":")), MaxProviderErrorBytes)
	why := "the provider failed the take"
	if kind == cardhdr.EndNoResult {
		// the record's line says which kind it was: the routes' count of a route's ended
		// takes holds both, and a reader of the card tells them apart
		line, why = cutText(kind+": "+strings.TrimPrefix(line, kind+": "), MaxProviderErrorBytes), "the child left no result"
		if decided {
			why = "nova-decide classed the take " + decide.ClassNoResult // a provider failure is never decided (finishKind)
		}
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
		Finished: stamp(s.Now), Usage: usage, Error: line, Taken: taken}.String()
	// and the producer's record of it (cost.go): it still cost tokens and time
	prSet := map[string]string{}
	addConsumer(pr, prSet, workConsumer(s, c, take, kind, rec))
	decidedSets(r, decided, pr, set, prSet)
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
	return launchRefused(s, c, pr, r, cardhdr.EndStaging)
}

// launchRefused records a typed refusal before a worker lane began. The same
// attempt may be dealt again, so it is withdrawn without consuming the brief's
// attempt bound or the worker's failed-work count.
func launchRefused(s *Snapshot, c, pr *Card, r FinishReq, kind string) Unit {
	set := nextGen(c, "", s.Now)
	set["withdrawn"] = stamp(s.Now)
	set[FieldBlame] = BlameCoordinator
	set[FieldDefectClass] = DefectLaunchRefused
	set["finding"] = ExtractFindingFirstLine(r.Report)
	line := cutText(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(r.Report, kind), ":")), MaxProviderErrorBytes)
	var notes []Note
	if !contains(StagingRefusers(c), c.Row) {
		set[FieldStagingTake+itoa(c.Int("gen"))] = ProviderTake{Route: c.F(FieldRoute), Model: c.F(FieldModel), Member: c.Row,
			Finished: stamp(s.Now), Usage: r.Usage, Error: line}.String()
		n := happened(NStagingRefused, pr.Row, s.Now, pr.ID)
		n.Who, n.Attempt = c.Row, c.Int("attempt")
		n.What = kind + " on " + c.Row + ": " + line
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
	}, Notes: notes, Moved: fmt.Sprintf("%s working -> withdrawn gen=%d, %s refused its launch; %s working -> ready", c.ID, c.Int("gen")+1, c.Row, pr.ID)}
}

// withdrawCard is the unit that withdraws work card c from its member, the one path of a
// member going down (FleetStep), of a ready card on a resting route (restWithdrawals) and
// of a friend's card taken back (FriendTake, by withdrawUnit): a new generation and the
// withdrawn stamp, and FieldTakeEnded only when its take ended (ended), which spends a
// redeal (redeal); its primary, working on c, goes back to ready for the next deal, with a
// happened note of typ to its stream that says what, by who.
func withdrawCard(s *Snapshot, c *Card, ended bool, typ, who, what string) Unit {
	var set map[string]string
	if ended {
		set = map[string]string{FieldTakeEnded: stamp(s.Now)}
	}
	return withdrawUnit(s, c, set, nil, typ, who, what)
}

// withdrawUnit is withdrawCard with the fields extra sets and unset unsets on the card.
func withdrawUnit(s *Snapshot, c *Card, extra map[string]string, unset []string, typ, who, what string) Unit {
	set := nextGen(c, "", s.Now)
	set["withdrawn"] = stamp(s.Now)
	maps.Copy(set, extra)
	u := Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, c.Row, Withdrawn, set, append([]string{"taken", "dealt"}, unset...)...))},
		Moved: fmt.Sprintf("%s withdrawn gen=%d", c.ID, c.Int("gen")+1)}
	if what != "" {
		u.Moved += ", " + what
	}
	if pr := s.Work.Placed(c.F("primary")); pr != nil && pr.Col == Working && pr.F("work") == c.ID {
		u.Changes = append(u.Changes, change(Work, moveEntry(pr, pr.Row, Ready, nil, "work")))
		u.Moved += "; " + pr.ID + " working -> ready"
		n := happened(typ, pr.Row, s.Now, pr.ID)
		n.Who, n.What = who, what
		u.Notes = append(u.Notes, n)
	}
	return u
}

// FleetReq is a fleet move: a member up or down, or the ready queues
// levelled (the tick's moves, as a member's derived status changes), or the
// coordinator's hold on a member (hold) and its release (release), or the
// whole fleet made to match the inventory (sync).
type FleetReq struct {
	Op     string // up, down, level, hold, release, sync, quiet
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
	// machine's child cap, width.go); zero leaves the width as it is, unless
	// Drain says the member drains: its width is set to DrainWidth (0).
	Width int  `json:",omitempty"`
	Drain bool `json:",omitempty"`
	// Deadline, above zero, is the member's pinned deadline in seconds set by up or
	// release (deadline.go); DeadlineOff takes the pin off; neither leaves it as it is.
	Deadline    int  `json:",omitempty"`
	DeadlineOff bool `json:",omitempty"`
	// Sync, with Op sync, is every machine the inventory says is a member
	// and its width (fleet_sync.go); Member is empty.
	Sync []SyncMember `json:",omitempty"`
	// HeldBy, with hold, marks the hold as made by that mechanism (the sync's,
	// fleet_sync.go) and not the coordinator's: the control card's held_by.
	HeldBy string `json:",omitempty"`
	// Machines, with Op sync, is every machine row of the inventory, a member
	// or not (width 0): a fleet row it does not name has no machine row and
	// leaves the fleet once no card stays on it (fleet_sync.go).
	Machines []string `json:",omitempty"`
	// Remove, with hold, takes the member's control card off the fleet when no
	// card stays on it after the hold's redeal (memberKeeps): the sync's
	// removal of a member with no machine row.
	Remove bool `json:",omitempty"`
	// Reason, with hold, is the coordinator's reason, kept on the control card
	// (FieldHeldReason) beside its held status (hold.go).
	Reason string `json:",omitempty"`
	// Finish, with hold (and on a down move), lets the member's working cards finish:
	// only its ready cards, never begun, are dealt round the fleet, and the hold
	// is marked so (FieldHeldFinish) for the sweep to leave them (hold.go).
	Finish bool `json:",omitempty"`
	// Until, with quiet, is when the member's quiet ends, and End ends it now (fleet
	// quiet, fleet_quiet.go); Reason is its reason.
	Until time.Time `json:",omitzero"`
	End   bool      `json:",omitempty"`
	// keep is the streams whose cards a member's hold leaves where they are: streams
	// held in the same step, whose hold withdraws them (hold.go).
	keep map[string]bool
}

// Fleet brings a member up (and levels the ready queues), takes one down
// (dealing its unfinished work cards to up members, or withdrawing them when
// none is up), or levels the ready queues. hold marks a member held and takes
// it down; release clears the hold, adding a member it does not know, and
// brings it up when its beat is fresh.
func FleetStep(s *Snapshot, r FleetReq) Plan {
	// every card the step places on a member (a down member's cards dealt
	// again, the levelling) goes round the fleet from the deal's rolling index
	// and moves it (round.go), written with the step
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
		switch ctl {
		case nil:
			status := Down
			if comeUp {
				status = Up
				n = statusNote(s, r, NMemberUp, "up")
			} else {
				line = r.Member + " added, down until it beats"
			}
			fields := map[string]string{"kind": "member", "status": status, "since": stamp(s.Now)}
			if w, ok := r.width(); ok {
				fields[FieldWidth] = w
			}
			if r.Deadline > 0 {
				fields[FieldMemberDeadline] = itoa(r.Deadline)
			}
			head = append(head, change(Fleet, createEntry(CtlID(r.Member), r.Member, Ctl, 0, fields)))
		default:
			set := map[string]string{}
			var unset []string
			if comeUp && ctl.F("status") != Up {
				set["status"], set["since"] = Up, stamp(s.Now)
				n = statusNote(s, r, NMemberUp, "up")
			}
			if w, ok := r.width(); ok && ctl.F(FieldWidth) != w {
				set[FieldWidth] = w
			}
			if r.Deadline > 0 && ctl.F(FieldMemberDeadline) != itoa(r.Deadline) {
				set[FieldMemberDeadline] = itoa(r.Deadline)
			}
			if r.DeadlineOff && ctl.F(FieldMemberDeadline) != "" {
				unset = append(unset, FieldMemberDeadline)
			}
			if r.Op == "release" && ctl.F("held") != "" {
				unset = append(unset, "held", FieldHeldBy, FieldHeldReason, FieldHeldFinish)
				if !comeUp {
					line = r.Member + " released, down until it beats"
				}
			}
			if len(set) > 0 || len(unset) > 0 {
				head = append(head, change(Fleet, setEntry(ctl, set, unset...)))
			}
		}
		if w, ok := r.width(); ok && (ctl == nil || ctl.F(FieldWidth) != w) {
			line += " width=" + w
			if r.Drain {
				line += " (drains: no new deals, its working cards finish)"
			}
		}
		if r.Deadline > 0 && (ctl == nil || ctl.F(FieldMemberDeadline) != itoa(r.Deadline)) {
			line += " deadline=" + itoa(r.Deadline) + "s (pinned)"
		}
		if r.DeadlineOff && ctl != nil && ctl.F(FieldMemberDeadline) != "" {
			line += " deadline=the card's, or " + itoa(DeadlineK) + " times the member's median run wall (the pin taken off)"
		}
		if comeUp {
			// a quiet member is levelled no card (fleet_quiet.go)
			level(s, &p, notQuiet(s, orderLike(s.Members(), append(liveFor(s, r), r.Member), r.Member)), rr, moves, nil)
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
		// the room of each receiver is its width (width.go): its work cards
		// held, ready and working, under it
		up := notQuiet(s, liveFor(s, r)) // a quiet member is dealt no card (fleet_quiet.go)
		return downPlan(s, r, up, rr, moves, memberLoads(s, up), memberWidths(s, up))
	case "level":
		// a quiet member is swept and levelled no card, and a quiet past its time is
		// logged as ended (fleet_quiet.go)
		quietEnds(s, &p)
		up := notQuiet(s, s.UpMembers())
		held := memberLoads(s, up)
		sweep(s, &p, r, up, rr, moves, held)
		level(s, &p, up, rr, moves, held)
	case "quiet":
		return quietPlan(s, r)
	case "sync":
		return fleetSyncPlan(s, r, rr, moves)
	default:
		p.refuse(r.Op, "fleet wants up, down, level, hold, release, sync or quiet")
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
// down member's cards go round the fleet together (every row of every table
// moves every tick).
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
	if r.Op == "hold" {
		// the coordinator's reason and whether working cards finish (hold.go): a
		// later hold says them again, and a hold with neither clears both
		if r.Reason != "" {
			set[FieldHeldReason] = r.Reason
		} else {
			unset = append(unset, FieldHeldReason)
		}
		if r.Finish {
			set[FieldHeldFinish] = stamp(s.Now)
			line += "; its working cards finish"
		} else {
			unset = append(unset, FieldHeldFinish)
		}
	}
	var head []Change
	if len(set) > 0 || len(unset) > 0 {
		head = append(head, change(Fleet, setEntry(ctl, set, unset...)))
	}
	cards := append([]*Card{}, s.Fleet.Cell(r.Member, Ready)...)
	if !r.Finish {
		cards = append(cards, s.Fleet.Cell(r.Member, Working)...)
	}
	cards = slices.DeleteFunc(cards, func(c *Card) bool { return r.keep[c.F("stream")] })
	SortCards(cards)
	// a read card is its reader's: taken back off a member going down or held, by the
	// machine, which spends nothing of the reader's (read_cards.go, spentBy), and the
	// read-card deal deals it again
	cards = slices.DeleteFunc(cards, func(c *Card) bool {
		if !isRead(c) {
			return false
		}
		u := Unit{Key: c.ID, Stream: c.F("stream"),
			Changes: []Change{change(Fleet, removeEntry(c, map[string]string{"retired": stamp(s.Now), "retired_by": RetiredByAway}))},
			Moved:   c.ID + " taken back: " + r.Member + " is " + r.Op}
		// a working read has started: its cost stays. a ready read has not
		u.Changes = retiredReadCosts(s, u.Changes)
		p.Units = append(p.Units, u)
		return true
	})
	withdrew := 0
	for _, c := range cards {
		taken := c.Col == Working
		if len(up) > 0 && (!taken || c.Int("redeals") < MaxRedeals) {
			// the next member round the fleet below its width (round.go), the
			// index moved past it; with none below its width the card is
			// withdrawn, and the next deal places it where there is room: a
			// member at its width takes no more.
			// a bench card goes to a member of its bench alone: with none of it up it is
			// withdrawn, and waits ready for its bench (bench_deal.go)
			if m := rr.next(onlyBench(without(up, StagingRefusers(c)), benchOfWork(s, c)), q, widths, ""); m != "" {
				rr.moved(m)
				moves[c.ID] = m
				q[m]++
				set := nextGen(c, m, s.Now)
				s.movedDeadline(m, c, set) // the new member's deadline (deadline.go)
				if taken {
					set["redeals"] = itoa(c.Int("redeals") + 1)
				}
				p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, m, Ready, set, "taken"))},
					Moved: fmt.Sprintf("%s %s:%s -> %s:ready gen=%d; %s down", c.ID, c.Row, c.Col, m, c.Int("gen")+1, r.Member)})
				continue
			}
		}
		withdrew++
		p.Units = append(p.Units, withdrawCard(s, c, taken, NWithdrawn, r.Who, ""))
	}
	if r.Remove && withdrew == 0 && memberKeeps(s, r.Member) == 0 {
		// no card stays on it: its control card leaves the fleet, held by the
		// sync so a return to the inventory releases it (fleet_sync.go)
		head = []Change{change(Fleet, removeEntry(ctl, map[string]string{"status": Down, "since": stamp(s.Now), "held": stamp(s.Now), FieldHeldBy: r.HeldBy}))}
		n = statusNote(s, r, NMemberDown, "removed")
		line = r.Member + " removed: " + r.Why + ", and no card stays on it; its row and its width leave the fleet"
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
// cards placed here included, for the level after it. A member held to finish
// (hold with no --return, hold.go) keeps its working cards; its ready ones go.
func sweep(s *Snapshot, p *Plan, r FleetReq, up []string, rr *round, moves roundMoves, held map[string]int) {
	widths := memberWidths(s, up)
	for _, m := range s.Members() {
		ctl := s.MemberCtl(m)
		if ctl == nil || ctl.F("status") == Up {
			continue
		}
		// a member held to finish (hold with no --return, hold.go) keeps its working
		// cards: only its ready ones, never begun, go
		finish := HeldToFinish(ctl)
		if n := s.Fleet.Count(m, Ready); n == 0 && (finish || s.Fleet.Count(m, Working) == 0) {
			continue
		}
		why := "down"
		if ctl.F("held") != "" {
			why = "held"
		}
		q := downPlan(s, FleetReq{Op: "down", Member: m, Who: r.Who, Why: "the rebalance: cards on a " + why + " member", Finish: finish}, up, rr, moves, held, widths)
		p.Units = append(p.Units, q.Units...)
		p.Refused = append(p.Refused, q.Refused...)
	}
}

// level evens the up members' backlogs, once at the start of every tick.
// A member's backlog is the work cards it holds,
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
		// a read card is its reader's: the level moves work cards alone (read_cards.go)
		queues[m] = slices.DeleteFunc(append([]*Card{}, s.Fleet.Cell(m, Ready)...), isRead)
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
			// a bench card is never moved off its bench: the members its BENCH line does not
			// name are avoided as a member that refused it at staging is (bench_deal.go)
			to = target(rr, up, n, held, widths, long, append(StagingRefusers(q[i]), notBench(up, benchOfWork(s, q[i]))...))
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
		set := nextGen(c, to, s.Now)
		s.movedDeadline(to, c, set) // the new member's deadline (deadline.go)
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, to, Ready, set))},
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

// width is the width cell up or release writes: the width given, DrainWidth
// for a member that drains, or none (ok false) to leave it as it is.
func (r FleetReq) width() (string, bool) {
	switch {
	case r.Drain:
		return DrainWidth, true
	case r.Width > 0:
		return itoa(r.Width), true
	}
	return "", false
}

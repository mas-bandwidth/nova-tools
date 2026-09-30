package sprint

import (
	"slices"
	"sort"
	"strconv"
	"strings"
)

// R16 held, the rule that finds a stall on what changed (the upper design,
// version 2.1, 2.3 R16): for the cards the held queue names it reads what
// decides each one's local holder (holder.go), and a card nothing holds gets a
// judgment, "stalled", or in review the judgment today's reviewJudgment gives.
// It finds a stall the tick it happens; without it a stall is found only by
// the sweep, up to a full pass later.
//
// It is level triggered and reads the state, never the agenda: a card whose
// state meets a rule's condition is held by that rule whether its key is
// queued or its line is not yet ingested, and a card an open or held judgment
// names is held by it. A second run on the same state names nothing: a card
// already named by a judgment of the type the rule would raise is passed over,
// as J's one per cause does at apply.
//
// The cost is O(c) for the c cards of a plan, at most HeldChunk: one holder is
// O(1) reads (holder.go), the fleet's room is read once for all of them, and
// the judgments open are indexed once.
//
// What the read carries, against what the design lists (2.3 R16): each card's
// record and its primary (a work or read card's id is read as its primary's,
// by its shape), a line's `about` (its subjects, never its `ids`: see
// heldRelatedLine), the follows the holder uses (`work`, `rcards`, `merge`,
// `control`, `needs`, `jopen`), and `fleet`. Not the follows `index` and `due`:
// the holder reads a card's place, score and deadlines off the card, and the
// index memberships and due entries are derived from those, so a plan that
// carried them would carry what it does not read. Not `front(s)`: a read plan
// names its streams in advance and R16's cards are found by id or by line, so
// no stream is known when the read is planned (IT05 question 3); the holder
// judges without it (holder.go). Not the backlog, the dropping marks, the cut
// entries, the quarantine marks nor the judgments open on an op: the partial
// snapshot has no carrier for them (heldFactsOf).

const (
	// HeldBacklogBound is the backlog, in lines, above which R16 judges
	// nothing: the machine is catching up and "behind" names the sprint (0,
	// row 26; 2.3 R16 effect).
	HeldBacklogBound = 5000
	// HeldChunk is the most cards one tick judges: the held queue's own
	// budget (1.0 tick budget; 2.3 R16 read).
	HeldChunk = 2000
	// heldFixedQueries are the queries every R16 read asks besides its lines:
	// fleet, and the related over the cards its keys name by id.
	heldFixedQueries = 2
)

// typeVerbStopped is the type of the judgment "a verb in parts stopped before
// its end" (2.2), which no rule of this tree names yet: IT06's table and IT10's
// R11 name it in the design's words.
const typeVerbStopped = "a verb in parts stopped before its end"

// heldFields are the fields R16's read names (every rule read names its
// fields, 1.0): what the holder table reads off the cards.
var heldFields = []string{
	"kind", "open", "refused", "bound", "attempt", "needs", "waived", "result", "head", "ci", "ci_head",
	"work", "rcards", "primary", "reader", "gen", "state", "cause", "need_card", "other", "outcome",
	heldDueUntaken, heldDueUnfinished, heldDueUnbegun, heldDueUnreported, heldDueMergeIdle,
}

// heldFleetFields are the fields of the members' control cards R16 reads.
var heldFleetFields = []string{"status"}

// heldFollows are what related follows from each card (1.0): its live work
// card, its read cards, its merge card, its stream's control card, its needs
// with their places, and jopen of the card. The members' control cards and
// their ready counts come from fleet.
var heldFollows = []string{FollowWork, FollowRCards, FollowMerge, FollowControl, FollowNeeds, FollowJOpen}

func init() {
	p, ok := PriorityOf(ruleHeld)
	if !ok {
		panic("sprint: RulePriorities has no row for the held rule")
	}
	RegisterRule(Rule{Name: ruleHeld, Priority: p, MaxSteps: 0, Read: readHeld, Plan: planHeldRule})
}

// heldRelatedIDs is related over primaries by id.
func heldRelatedIDs(ids []string) SprintQ {
	return SprintQ{Kind: QueryRelated, Table: Work, Source: IDSource{Kind: SourceIDs, IDs: ids},
		Fields: heldFields, Follow: heldFollows}
}

// heldRelatedLine is related over the `about` of a line by seq, from the key's
// offset, at most limit of them. A line names its subjects by `about` (L2 1.1):
// on a member line it is the primaries its cards belong to, aligned with its
// `ids`, which are the cards' own (a work card's `<primary>.w<n>`), and on a
// note line it is the cards the note is about, which has no `ids` at all. R16
// judges primaries, and a key held@<seq> made by closing a note names its
// subjects and nothing else, so the read is of `about` and never of `ids`.
func heldRelatedLine(h heldKey, limit int) SprintQ {
	return SprintQ{Kind: QueryRelated, Table: Work,
		Source: IDSource{Kind: SourceLine, Seq: h.Line, About: true, Offset: h.Offset, Limit: limit},
		Fields: heldFields, Follow: heldFollows}
}

// heldFleet is fleet: every member's control card and its cell counts.
func heldFleet() SprintQ { return SprintQ{Kind: QueryFleet, Fields: heldFleetFields} }

// heldReadCards is how many cards one read may name: the chunk, or what fits in
// every bound of the read after fleet, by the declared cost of one card and its
// follows (1.4.2), whichever is fewer; halved once for each halving after a
// BUDGET (1.3.5), down to one. The bytes, not the records, bind at layer 1's
// bounds.
func heldReadCards(b ReadBounds, halvings int) int {
	fixed := QueryCost(heldFleet())
	per := QueryCost(heldRelatedIDs([]string{""}))
	n := HeldChunk
	for _, l := range []struct{ bound, fixed, per int }{
		{b.Records, fixed.Records, per.Records},
		{b.RangeIDs, fixed.RangeIDs, per.RangeIDs},
		{b.Bytes, fixed.Bytes, per.Bytes},
	} {
		if l.bound > 0 && l.per > 0 {
			n = min(n, (l.bound-l.fixed)/l.per)
		}
	}
	return max(1, Halved(max(n, 0), halvings))
}

// readHeld is R16's read: the related query over the primaries its keys name
// (a card by its id, or its primary's when it is a work or read card; a line by
// its seq, from the key's offset, for a share of the room) and the fleet. The
// keys are taken in the order they came, each costing a place of the room, and
// the ones that do not fit stay queued in that order; a line's `about` is read to
// the share of the room its key has, and planHeld requeues the key at the
// offset where the read stopped. The room a tick has is shared by the keys it
// takes, so many line keys at the head are each served a part of their line
// every tick and none takes the room the others need.
func readHeld(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	room := heldReadCards(b, halvings)
	var (
		ids   []string
		seen  = map[string]bool{}
		lines []heldKey
		left  []AgendaKey
		full  bool
	)
	for _, k := range keys {
		hk, ok := parseHeldKey(k)
		switch {
		case !ok:
			left = append(left, k)
		case full:
			left = append(left, k)
		case !hk.ByLine:
			p := heldPrimaryID(hk.Card)
			if seen[p] {
				continue // read already for an earlier key, at no more cost
			}
			if len(ids)+len(lines) >= room {
				full = true
				left = append(left, k)
				continue
			}
			seen[p] = true
			ids = append(ids, p)
		case len(ids)+len(lines) >= room, b.Queries > 0 && heldFixedQueries+len(lines)+1 > b.Queries:
			full = true
			left = append(left, k)
		default:
			lines = append(lines, hk)
		}
	}
	var rp ReadPlan
	if len(ids) == 0 && len(lines) == 0 {
		return rp, left
	}
	if len(ids) > 0 {
		rp.Sprint = append(rp.Sprint, heldRelatedIDs(ids))
	}
	if len(lines) > 0 {
		// each share is at most the room, which is at most the chunk: inside
		// the window of a note's about (MaxAboutIDs) and of a line's ids
		share, extra := (room-len(ids))/len(lines), (room-len(ids))%len(lines)
		for i, hk := range lines {
			limit := share
			if i < extra {
				limit++
			}
			rp.Sprint = append(rp.Sprint, heldRelatedLine(hk, limit))
		}
	}
	rp.Sprint = append(rp.Sprint, heldFleet())
	return rp, left
}

// heldFactsOf is what the snapshot carries of the sprint's own keys beside its
// tables. A snapshot loaded from a plan carries the subjects of each line the
// plan read by its seq (the answer of the `related` query over its `about`), and
// whether the read stopped at its limit; every snapshot carries the conditions the
// coordinator acknowledged, which hold a card the way a wait holds a judgment. A
// line read for its `ids` is not its subjects (a work card's id is no primary's),
// so a query of the plan over a line that does not read `about` carries nothing,
// and the key of that line is held back.
// The partial snapshot carries nothing else the rule reads: not the backlog
// (L2 last less the tick's cur), the dropping marks and the judgments open on
// their ops, the cut entries, nor the quarantine marks. Until it does the
// registered rule sees a backlog of zero, no stream dropping and no card
// quarantined, and the loop that runs it has to know the backlog itself (the
// pull request names the ask).
func heldFactsOf(s *Snapshot) HeldFacts {
	f := HeldFacts{Held: s.Acked}
	if s.Partial == nil {
		return f
	}
	for i, q := range s.Partial.Plan.Sprint {
		if q.Kind != QueryRelated || q.Source.Kind != SourceLine || !q.Source.About || i >= len(s.Partial.Answer.Sprint) {
			continue
		}
		about := s.Partial.Answer.Sprint[i].IDs
		if f.Lines == nil {
			f.Lines = map[HeldLineAt]HeldLine{}
		}
		f.Lines[HeldLineAt{Line: q.Source.Seq, Offset: q.Source.Offset}] = HeldLine{
			About: about,
			More:  q.Source.Limit > 0 && len(about) >= q.Source.Limit && q.Source.Offset+len(about) < MaxAboutIDs,
		}
	}
	return f
}

// planHeldRule is R16's plan over the snapshot the read built.
func planHeldRule(s *Snapshot, keys []AgendaKey, now Now) RulePlan {
	return planHeld(s, heldFactsOf(s), keys, now)
}

// stallGroup is the cards of one judgment R16 raises in a step: one note for
// every subject of one type and cause (1.3.4).
type stallGroup struct {
	typ, cause, text string
	decisions        []string
	ids              []string
	members          map[string]bool // the members the working cards sit at
}

// reviewStall is what R16 raises for a primary in review that nothing holds,
// where reviewJudgment finds its cause: the cause J keys on, the words, and the
// decisions of 2.2.
type reviewStall struct {
	Cause, Text string
	Decisions   []string
}

// reviewStalls are the two judgments of reviewJudgment R16 keeps (2.2): "ready
// to accept" is R9's now.
var reviewStalls = map[string]reviewStall{
	NReadsExhausted: {"exhausted", "its reads are done without two different readers saying ok at its head, and no judgment names it",
		[]string{"ask --another", "rework --fix", "drop"}},
	NStranded: {"stranded", "it is in review with no read asked at its attempt, and no judgment names it",
		[]string{"ask", "rework --fix", "drop"}},
}

// planHeld is R16's plan over the facts the read carries beside the tables.
// Above the backlog bound it requeues every key and judges nothing. Otherwise,
// for each card of the keys: held, nothing; not held, one judgment naming it,
// guarded on the card's place and revision so a change since the read refuses
// the step and the key comes back. Every key it finished it removes, and a
// line it read only to a limit it requeues at the offset where it stopped. A
// key the snapshot's read did not carry (a line it did not read, a card no
// query of its plan named) is held back and not put back unchanged, so that it
// cannot stand at the head of the queue and starve the keys behind it.
func planHeld(s *Snapshot, f HeldFacts, keys []AgendaKey, now Now) RulePlan {
	var rp RulePlan
	if f.Backlog > HeldBacklogBound {
		rp.Requeue = append(rp.Requeue, keys...)
		return rp
	}
	v := newHoldView(s, f, now)
	seen := map[string]bool{}   // the ids judged
	judged := map[string]bool{} // the primaries a card was judged as
	groups := map[string]*stallGroup{}
	raised := map[string]string{} // card -> the type raised on it
	for _, k := range keys {
		hk, ok := parseHeldKey(k)
		if !ok {
			rp.Requeue = append(rp.Requeue, k) // not a key of this rule: not its to finish
			continue
		}
		ids := []string{hk.Card}
		if hk.ByLine {
			line, ok := f.Lines[HeldLineAt{Line: hk.Line, Offset: hk.Offset}]
			if !ok {
				rp.HeldBack = append(rp.HeldBack, k) // its subjects were not read
				continue
			}
			ids = line.About
			if line.More && len(ids) > 0 {
				rp.Requeue = append(rp.Requeue, hk.resumedAt(k, hk.Offset+len(ids)))
			}
		} else if !v.read(hk.Card) {
			rp.HeldBack = append(rp.HeldBack, k) // its card was not read
			continue
		}
		rp.Done = append(rp.Done, k)
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			vd := v.verdict(id)
			if vd.row == nil || vd.Hold.By != "" || judged[vd.ID] {
				continue
			}
			judged[vd.ID] = true // a work card and its primary are one judgment of the primary
			g, member := stallOf(v, vd)
			if _, named := v.judgment(vd.ID, g.typ); named {
				continue
			}
			key := g.typ + "\x00" + g.cause
			if groups[key] == nil {
				groups[key] = g
			}
			groups[key].ids = append(groups[key].ids, vd.ID)
			if member != "" {
				if groups[key].members == nil {
					groups[key].members = map[string]bool{}
				}
				groups[key].members[member] = true
			}
			raised[vd.ID] = g.typ
		}
	}
	order := make([]string, 0, len(groups))
	for k := range groups {
		order = append(order, k)
	}
	sort.Strings(order)
	for _, k := range order {
		g := groups[k]
		sort.Strings(g.ids)
		members := make([]string, 0, len(g.members))
		for m := range g.members {
			members = append(members, m)
		}
		sort.Strings(members)
		rp.Notes = append(rp.Notes, NoteReq{Op: "open", Type: g.typ, Cause: g.cause, Subjects: g.ids, Text: g.text,
			Decisions: expandDecisions(g.decisions, members)})
	}
	guarded := make([]string, 0, len(raised))
	for id := range raised {
		guarded = append(guarded, id)
	}
	sort.Strings(guarded)
	for _, id := range guarded {
		p := s.Work.Card(id)
		rp.Plan.Units = append(rp.Plan.Units, Unit{Key: id, Stream: p.Row,
			Changes: []Change{change(Work, guardEntry(p))}, Moved: "guarded for its " + raised[id] + " judgment"})
	}
	return rp
}

// stallOf is the judgment a stalled card gets: for a card the review row found
// nothing to hold, the one reviewJudgment says when it says one of the two it
// keeps; otherwise (and for a card in review that an earlier row names: refused,
// at its bound, quarantined, of a stream being dropped) "stalled"
// with the words and decisions of the row that found nothing to hold it. The
// second result is the member a working card's work card sits at, for the
// decision the note offers about it.
func stallOf(v *holdView, vd verdict) (*stallGroup, string) {
	p := v.s.Work.Card(vd.ID)
	if vd.row.Name == "review" { // a card refused, at its bound or quarantined in review is named by that row
		if typ, ok := v.reviewStallType(p); ok {
			if rs, ok := reviewStalls[typ]; ok {
				return &stallGroup{typ: typ, cause: rs.Cause, text: rs.Text, decisions: slices.Clone(rs.Decisions)}, ""
			}
		}
	}
	member := ""
	if p.Col == Working {
		if wc := v.workCard(p); wc != nil {
			member = wc.Row
		}
	}
	cause, text := vd.row.Cause, vd.Why
	if cause == "" {
		cause = vd.row.Name
	}
	if vd.row.Stall != "" {
		text = vd.row.Stall
	}
	return &stallGroup{typ: NStalled, cause: cause, text: text,
		decisions: append(slices.Clone(vd.row.Decisions), stalledTail...)}, member
}

// heldKey is a key of the held rule read apart: held:<card>, held@<seq> or
// held@<seq>+<offset> (2.1, E6). It reads the tree's AgendaKey, a key and an
// order; 8.0 shapes the key as a rule, a subject, a line, an offset and an
// order (open question of IT11), and these three are what changes with it.
type heldKey struct {
	Card   string // the card, for held:<card>
	Line   uint64 // the line's seq, for held@<seq>
	Offset int    // where in the line's `about` the key resumes
	ByLine bool
}

// parseHeldKey reads a key of the held rule; false for any other key.
func parseHeldKey(k AgendaKey) (heldKey, bool) {
	if rest, ok := strings.CutPrefix(k.Key, ruleHeld+":"); ok && rest != "" {
		return heldKey{Card: rest}, true
	}
	rest, ok := strings.CutPrefix(k.Key, ruleHeld+"@")
	if !ok {
		return heldKey{}, false
	}
	seq, off, hasOff := strings.Cut(rest, "+")
	line, err := strconv.ParseUint(seq, 10, 64)
	if err != nil {
		return heldKey{}, false
	}
	h := heldKey{Line: line, ByLine: true}
	if hasOff {
		n, err := strconv.Atoi(off)
		if err != nil || n < 0 {
			return heldKey{}, false
		}
		h.Offset = n
	}
	return h, true
}

// agenda is the key as the agenda holds it, at the order given.
func (h heldKey) agenda(order uint64) AgendaKey {
	if !h.ByLine {
		return AgendaKey{Key: ruleHeld + ":" + h.Card, Seq: order}
	}
	key := ofLine(ruleHeld, h.Line)
	if h.Offset > 0 {
		key += "+" + strconv.Itoa(h.Offset)
	}
	return AgendaKey{Key: key, Seq: order}
}

// resumedAt is the key of a line the read stopped inside, at the offset where
// it stopped and at the order of the key it replaces.
func (h heldKey) resumedAt(k AgendaKey, offset int) AgendaKey {
	return heldKey{Line: h.Line, Offset: offset, ByLine: true}.agenda(k.Seq)
}

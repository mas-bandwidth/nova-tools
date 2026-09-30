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
// O(1) reads (holder.go), the fleet's room and each stream's front are read
// once for all of them, and the judgments open are indexed once.

const (
	// HeldBacklogBound is the backlog, in lines, above which R16 judges
	// nothing: the machine is catching up and "behind" names the sprint (0,
	// row 26; 2.3 R16 effect).
	HeldBacklogBound = 5000
	// HeldChunk is the most cards one tick judges: the held queue's own
	// budget (1.0 tick budget; 2.3 R16 read).
	HeldChunk = 2000
	// PriorityHeld is R16's place in the tick's order (1.4.2), the last: the
	// eighteenth of R1, R2, R4, R3, R5, R6, R7, R8, R9, R10, R11, R12, R13,
	// R14, R18, R15, R19 and then R16. The design gives the order, not
	// numbers; this counts places.
	PriorityHeld = 18
	// heldLineIDs is the ids one line names at most (1.0's table of step
	// bounds: a generated line is 2,000 ids).
	heldLineIDs = 2000
	// heldFixedQueries are the queries every R16 read asks besides its lines:
	// front, fleet, and the related over the cards its keys name by id.
	heldFixedQueries = 3
)

// heldFields are the fields R16's read names (every rule read names its
// fields, 1.0): what the holder table reads off the cards.
var heldFields = []string{
	"kind", "open", "refused", "bound", "attempt", "needs", "waived", "result", "head", "ci", "ci_head",
	"work", "rcards", "primary", "reader", "gen", "status", "state", "cause", "need_card", "other", "outcome",
	fieldDueUntaken, fieldDueUnfinished, fieldDueUnbegun, fieldDueUnreported, fieldDueMergeIdle,
}

// heldFollows are what related follows from each card (1.0): its live work
// card, its read cards, its merge card, its stream's control card, its needs
// with their places, and jopen of the card. The members' control cards come
// from fleet, and the first sentinel of each stream from front.
var heldFollows = []string{"work", "rcards", "merge", "control", "needs", "jopen"}

func init() {
	RegisterRule(Rule{Name: ruleHeld, Priority: PriorityHeld, MaxSteps: 0, Read: readHeld, Plan: planHeldRule})
}

// heldReadCards is how many cards one read may name: the chunk, or the records
// the read may return allow after front and fleet, by the declared cost of each
// card and its follows (1.4.2), whichever is fewer; halved once for each
// halving after a BUDGET (1.3.5), down to one.
func heldReadCards(b ReadBounds, halvings int) int {
	fixed := QueryCost(frontOfEveryStream()).Records + QueryCost(fleetOf()).Records
	per := QueryCost(relatedOf([]string{""}, heldFields, heldFollows)).Records
	n := min(HeldChunk, (b.Records-fixed)/per)
	for range halvings {
		n /= 2
	}
	return max(1, n)
}

// readHeld is R16's read: the related query over the cards its keys name (a
// card by its id, a line by its seq, from the key's offset, for as many ids as
// the read has room for), the fronts of the streams and the fleet. The keys
// that do not fit stay queued, in the order they came; a line that names more
// ids than fit is read to its limit and planHeld requeues it with the offset
// where the read stopped.
func readHeld(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	room := heldReadCards(b, halvings)
	var (
		ids   []string
		lines []SprintQ
		left  []AgendaKey
		full  bool
	)
	for _, k := range keys {
		hk, ok := parseHeldKey(k)
		switch {
		case !ok || full || room <= 0:
			full = full || ok
			left = append(left, k)
		case !hk.ByLine:
			ids = append(ids, hk.Card)
			room--
		case heldFixedQueries+len(lines)+1 > b.Queries:
			full = true
			left = append(left, k)
		default:
			limit := min(room, heldLineIDs)
			lines = append(lines, relatedLine(hk, limit, heldFields, heldFollows))
			room -= limit
		}
	}
	var rp ReadPlan
	if len(ids) == 0 && len(lines) == 0 {
		return rp, left
	}
	if len(ids) > 0 {
		rp.Sprint = append(rp.Sprint, relatedOf(ids, heldFields, heldFollows))
	}
	rp.Sprint = append(rp.Sprint, lines...)
	rp.Sprint = append(rp.Sprint, frontOfEveryStream(), fleetOf("status"))
	return rp, left
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
// line it read only to a limit it requeues at the offset where it stopped.
func planHeld(s *Snapshot, f HeldFacts, keys []AgendaKey, now Now) RulePlan {
	var rp RulePlan
	if f.Backlog > HeldBacklogBound {
		rp.Requeue = append(rp.Requeue, keys...)
		return rp
	}
	v := newHoldView(s, f, now)
	seen := map[string]bool{}
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
			line, ok := f.Lines[hk.Line]
			if !ok {
				rp.Requeue = append(rp.Requeue, k) // its ids were not read: it stays
				continue
			}
			ids = line.IDs
			if next := hk.Offset + len(ids); len(ids) > 0 && next < line.Total {
				rp.Requeue = append(rp.Requeue, hk.resumedAt(k, next))
			}
		}
		rp.Done = append(rp.Done, k)
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			vd := v.verdict(id)
			if vd.row == nil || vd.Hold.By != "" {
				continue
			}
			g := stallOf(v, vd)
			if _, named := v.judgment(vd.ID, g.typ); named {
				continue
			}
			key := g.typ + "\x00" + g.cause
			if groups[key] == nil {
				groups[key] = g
			}
			groups[key].ids = append(groups[key].ids, vd.ID)
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
		rp.Notes = append(rp.Notes, NoteReq{Op: "open", Type: g.typ, Cause: g.cause, Subjects: g.ids, Text: g.text, Decisions: g.decisions})
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

// stallOf is the judgment a stalled card gets: for a card in review, the one
// reviewJudgment says when it says one of the two it keeps; otherwise "stalled"
// with the words and decisions of the row that found nothing to hold it.
func stallOf(v *holdView, vd verdict) *stallGroup {
	p := v.s.Work.Card(vd.ID)
	if p.Col == Review {
		if n, ok := reviewJudgment(v.s, p, reviewStep{who: MachineActor}); ok {
			if rs, ok := reviewStalls[n.Type]; ok {
				decisions := slices.Clone(rs.Decisions)
				if p.F("result") == "failed" {
					decisions = slices.DeleteFunc(decisions, func(d string) bool { return d == "ask" })
				}
				return &stallGroup{typ: n.Type, cause: rs.Cause, text: rs.Text, decisions: decisions}
			}
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
		decisions: append(slices.Clone(vd.row.Decisions), stalledTail...)}
}

// heldKey is a key of the held rule read apart: held:<card>, held@<seq> or
// held@<seq>+<offset> (2.1, E6). It reads the tree's AgendaKey, a key and an
// order; 8.0 shapes the key as a rule, a subject, a line, an offset and an
// order (open question of IT11), and these three are what changes with it.
type heldKey struct {
	Card   string // the card, for held:<card>
	Line   uint64 // the line's seq, for held@<seq>
	Offset int    // where in the line's ids the key resumes
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

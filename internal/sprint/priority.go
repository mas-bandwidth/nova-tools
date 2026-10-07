package sprint

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
)

// A card's priority (docs/SPEC-SPRINT.md section 1, "Priority"; the owner, 2026-10-06): every
// card carries one level of the ladder blocker, critical, high, reader, normal, low. A read card
// is reader, or its primary's level when that is higher (ReadPriority), and never set by
// hand: every deal places, level by level, the reads of a level and then its work
// (reads_priority.go), and land takes the stream with the highest merging level first. A
// primary is normal unless a level is set on it: by its brief's line `PRIORITY: <level>` at
// admission, else by its stream's default at admission, or by the verb `priority`, each change
// on its timeline with the actor and the reason. Critical is computed too (CriticalBehind or
// more cards behind it, weight.go) unless a level is set by hand. Low is dealt only to a lane
// nothing higher can fill. The deal and the ask order their cards by the ladder, then stream
// turns within a level (ladderOrder, readOrder); land orders the streams by their merging
// sets' levels (LandOrder), each batch as it was. Owed with the reference model: the weight
// within a level and the computed critical in the deal's and the ask's order. A blocker that
// no row has room for evicts one running card (priority_evict.go, tla/Priority.tla).

// The priority ladder, highest first.
const (
	PriorityBlocker  = "blocker"
	PriorityCritical = "critical"
	PriorityHigh     = "high"
	PriorityReader   = "reader"
	PriorityNormal   = "normal"
	PriorityLow      = "low"
)

// PriorityLadder is the levels, highest first.
var PriorityLadder = []string{PriorityBlocker, PriorityCritical, PriorityHigh, PriorityReader, PriorityNormal, PriorityLow}

// PrioritySettable is the levels a primary may be given (reader is a read card's alone).
var PrioritySettable = []string{PriorityBlocker, PriorityCritical, PriorityHigh, PriorityNormal, PriorityLow}

// FieldPriority is a primary's priority as set (by its brief's PRIORITY line or its stream's
// default at admission, or by the verb priority); absent is normal, or critical by weight.
const FieldPriority = "priority"

// FieldPriorityDefault is a stream's control card's field: the level a card added to the
// stream later is given when its brief names none; absent is normal.
const FieldPriorityDefault = "priority_default"

// NPrioritySet is the happened note that records a priority change on the card's timeline
// (log --card), with the actor and the reason.
const NPrioritySet = "priority set"

// NStreamPrioritySet is the happened note that records a stream's default level changing, on
// the stream's timeline (log --stream; its control card's, log --card ctl-<s>), with the actor
// and the reason.
const NStreamPrioritySet = "stream priority set"

// priorityLine is the header line that seeds a card's priority at admission.
const priorityLine = "PRIORITY:"

// PriorityOfBrief is the level the brief's header line `PRIORITY: <level>` names, "" when its
// header names none; why says a line that names no settable level, with the level found and
// the six of the ladder (reader is a read card's alone). Add and Recut refuse a brief with why
// set, so a misspelled level is never admitted at normal or the stream's default.
func PriorityOfBrief(brief string) (level, why string) {
	started := false
	for _, l := range strings.Split(brief, "\n") {
		t := strings.TrimSpace(l)
		if t == "" {
			if started {
				break // the header ends at its first blank line
			}
			continue
		}
		started = true
		rest, ok := strings.CutPrefix(t, priorityLine)
		if !ok {
			continue
		}
		v := strings.ToLower(strings.TrimSpace(rest))
		if !slices.Contains(PrioritySettable, v) {
			return "", "PRIORITY: wants one of " + strings.Join(PrioritySettable, ", ") + " (the ladder is " + strings.Join(PriorityLadder, ", ") + "; reader is a read card's alone, never set); found " + strings.TrimSpace(rest)
		}
		return v, ""
	}
	return "", ""
}

// CardPriority is the card's level and where it comes from: reader for a read card ("read";
// its inherited level, the higher of reader and its primary's, is ReadPriority);
// its own set level ("set"); critical by its weight ("computed"); else normal ("default").
func CardPriority(c *Card) (level, source string) {
	if c == nil {
		return PriorityNormal, "default"
	}
	if c.F("kind") == "read" {
		return PriorityReader, "read"
	}
	if v := c.F(FieldPriority); v != "" {
		return v, "set"
	}
	if IsCritical(c) {
		return PriorityCritical, "computed"
	}
	return PriorityNormal, "default"
}

// priorityOnWork writes the primary's level on the work card a deal creates for it, when it
// is not normal, so a worker's queue shows it (queue) without reading the primary.
func priorityOnWork(fields map[string]string, pr *Card) {
	if l, _ := CardPriority(pr); l != PriorityNormal {
		fields[FieldPriority] = l
	}
}

// QueuePriority is the level a queue shows beside a card: a read card's reader, a work card's
// as its deal wrote it (priorityOnWork), else normal.
func QueuePriority(c *Card) string {
	if v := c.F(FieldPriority); v != "" {
		return v // a work card's as its deal wrote it; a read card's inherited level (priorityOnRead)
	}
	if c.F("kind") == "read" {
		return PriorityReader
	}
	return PriorityNormal
}

// priorityOnRead writes on a read card the ask creates its inherited level (ReadPriority) when
// it is above reader, so a reader's queue shows it.
func priorityOnRead(fields map[string]string, pr *Card) {
	if l := ReadPriority(pr); l != PriorityReader {
		fields[FieldPriority] = l
	}
}

// priorityRank is the level's place on the ladder, 0 the highest; an unknown level is normal's.
func priorityRank(level string) int {
	if i := slices.Index(PriorityLadder, level); i >= 0 {
		return i
	}
	return slices.Index(PriorityLadder, PriorityNormal)
}

// cardRank is the card's place on the ladder as the deal, the ask and the friends' order it:
// its set level (a read card's reader), never the critical its weight computes, which is
// shown (CriticalByWeight) and orders nothing until the reference model orders by it (owed
// with the model, tla/SprintTables.tla: ordering it made the slow differential tier, make
// test-slow, disagree on the ask's stream round).
func cardRank(c *Card) int {
	if c.F("kind") == "read" {
		return priorityRank(PriorityReader)
	}
	return priorityRank(c.F(FieldPriority))
}

// ladderOrder is the cards by the ladder, highest first, stable: cards of one level keep the
// order given (stream turns, work order). A deal that fills its room in this order gives a low
// card only a lane no higher card it was offered could fill. The weight (the cards behind) does
// not order it: the reference model deals in stream turns (TestEngineAgreesWithTheReferenceModel),
// and ordering by weight within a level is owed with the model.
func ladderOrder(cards []*Card) []*Card {
	out := slices.Clone(cards)
	sort.SliceStable(out, func(i, j int) bool { return cardRank(out[i]) < cardRank(out[j]) })
	return out
}

// readRank is the place on the ladder of the primary's read: the higher of reader and its
// primary's own level (cardRank), so the reads of a blocker, critical or high primary go to
// the front of the read queue, and a normal or low primary's read is reader (the owner,
// 2026-10-06: "that work stream jumps to the front of the reader and merge queue").
func readRank(pr *Card) int { return min(cardRank(pr), priorityRank(PriorityReader)) }

// ReadPriority is the level of a read of the primary (readRank): reader, or its primary's
// when that is higher.
func ReadPriority(pr *Card) string { return PriorityLadder[readRank(pr)] }

// readOrder is the primaries in review by the level of their reads (readRank), highest
// first, stable: equals keep the order given (the ask's stream turns, work order).
func readOrder(cards []*Card) []*Card {
	out := slices.Clone(cards)
	sort.SliceStable(out, func(i, j int) bool { return readRank(out[i]) < readRank(out[j]) })
	return out
}

// LandOrder is the streams in the order land takes their batches: by the highest level of any
// card in the stream's merging set (cardRank; never the stream's default, never its oldest
// card), highest first, then by the most cards behind any of them (FieldBehind), then the order
// given; inside a stream the batch stays as it is (the owner, 2026-10-06: "a normal stream with
// one critical card merging goes ahead of a high stream whose merging cards are all high").
func LandOrder(s *Snapshot, streams []string) []string {
	rank, weight := map[string]int{}, map[string]int{}
	for _, st := range streams {
		rank[st] = priorityRank(PriorityLow) + 1
		if s == nil || s.Work == nil {
			continue
		}
		for _, c := range s.Work.Cell(st, Merging) {
			rank[st] = min(rank[st], cardRank(c))
			weight[st] = max(weight[st], c.Int(FieldBehind))
		}
	}
	out := slices.Clone(streams)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if rank[a] != rank[b] {
			return rank[a] < rank[b]
		}
		return weight[a] > weight[b]
	})
	return out
}

// StreamPriority is the stream's default level for cards added later: its control card's
// field, else normal.
func (s *Snapshot) StreamPriority(stream string) string {
	if s != nil && s.Merge != nil {
		if v := s.StreamCtl(stream).F(FieldPriorityDefault); v != "" {
			return v
		}
	}
	return PriorityNormal
}

// seedPriority is the level a card admitted to the stream with the brief is given: its
// brief's PRIORITY line, else its stream's default (ctl, the stream's control card, nil for a
// new stream) when it is not normal; "" leaves the card normal.
func seedPriority(ctl *Card, brief string) string {
	// a line that names no level is refused before this (Add), so why is never set here
	if v, _ := PriorityOfBrief(brief); v != "" {
		return v
	}
	if v := ctl.F(FieldPriorityDefault); v != "" && v != PriorityNormal {
		return v
	}
	return ""
}

// PriorityReq is the verb priority's request: the cards named, or every card of the stream on
// the table and the stream's default, set to the level, with the reason.
type PriorityReq struct {
	IDs                        []string
	Stream, Level, Reason, Who string
}

// SetPriority sets the level on each primary named, or on every card of the stream on the
// table, whatever its column, and the stream's default for cards added later. Each change is
// a happened note on the card's timeline with the actor and the reason; a card at the level
// already is said, not changed. A read card is refused: its level is reader, so the remedy
// is its primary.
func SetPriority(s *Snapshot, r PriorityReq) Plan {
	var p Plan
	if !slices.Contains(PrioritySettable, r.Level) || (len(r.IDs) == 0) == (r.Stream == "") || strings.TrimSpace(r.Reason) == "" {
		p.refuse("priority", "wants <id>... or --stream <s>, one of --"+strings.Join(PrioritySettable, ", --")+", and --reason <text>; run: nova-sprint help priority")
		return p
	}
	why := ": " + r.Reason
	ids := slices.Clone(r.IDs)
	if r.Stream != "" {
		ctl := s.StreamCtl(r.Stream)
		if ctl == nil || !s.Work.HasRow(r.Stream) {
			p.refuse(r.Stream, "no such stream; run: nova-sprint where")
			return p
		}
		for _, c := range s.Work.Cards() {
			if c.Row == r.Stream && c.Placed() && !IsSentinel(c) {
				ids = append(ids, c.ID)
			}
		}
		if StreamHeld(s, r.Stream) {
			p.Said = append(p.Said, "stream "+r.Stream+" is held: its cards are set, and none is dealt until nova-sprint unhold "+r.Stream)
		}
		if was := s.StreamPriority(r.Stream); ctl.F(FieldPriorityDefault) != r.Level && was != r.Level {
			set, unset := map[string]string{FieldPriorityDefault: r.Level}, []string(nil)
			if r.Level == PriorityNormal {
				set, unset = nil, []string{FieldPriorityDefault}
			}
			// the default's change on the stream's own timeline (log --stream, log --card ctl-<s>)
			n := happened(NStreamPrioritySet, r.Stream, s.Now, ctl.ID)
			n.Who, n.What = r.Who, fmt.Sprintf("stream %s priority default %s -> %s (the cards added later)%s", r.Stream, was, r.Level, why)
			p.Units = append(p.Units, Unit{Key: ctl.ID, Stream: r.Stream, Changes: []Change{change(Merge, setEntry(ctl, set, unset...))},
				Notes: []Note{n}, Moved: n.What})
		}
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if pr, _, _, ok := ParseReadCard(id); ok {
			p.refuse(id, "a read card's priority is its primary's, or reader when that is lower; set its primary: nova-sprint priority "+pr+" --"+r.Level)
			continue
		}
		c := s.Work.Placed(id)
		if c == nil || IsSentinel(c) {
			if pr, _, ok := ParseWorkCard(id); ok {
				p.refuse(id, "a work card's priority is its primary's; set its primary: nova-sprint priority "+pr+" --"+r.Level)
				continue
			}
			p.refuse(id, "no such card on the table; run: nova-sprint card "+id)
			continue
		}
		was, _ := CardPriority(c)
		if c.F(FieldPriority) == r.Level {
			p.Said = append(p.Said, id+" priority "+r.Level+" already; no change")
			continue
		}
		n := happened(NPrioritySet, c.Row, s.Now, c.ID)
		n.Who, n.What = r.Who, fmt.Sprintf("%s priority %s -> %s%s", c.ID, was, r.Level, why)
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.Row,
			Changes: []Change{change(Work, setEntry(c, map[string]string{FieldPriority: r.Level}))}, Notes: []Note{n},
			Moved: n.What})
	}
	return p
}

// CriticalByWeight is the key and the words where and the dashboard show a computed critical
// under (CriticalBehind or more cards behind it, no level set): it is shown and orders nothing
// yet, owed with the reference model (a level ordered by weight made the slow differential tier
// disagree: the ask's stream round, seed 1004).
const CriticalByWeight = "critical (by weight, not yet ordered)"

// PriorityCounts is where's priority line's cards: every open primary whose level is not
// normal, by level, in work order.
func PriorityCounts(s *Snapshot) map[string][]string {
	out := map[string][]string{}
	for _, c := range openPrimaries(s) {
		l, src := CardPriority(c)
		if src == "computed" {
			l = CriticalByWeight // shown, not yet ordered (cardRank)
		}
		if l != PriorityNormal {
			out[l] = append(out[l], c.ID)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// StreamPriorities is each stream's default level that is not normal, by stream.
func StreamPriorities(s *Snapshot) map[string]string {
	if s == nil || s.Work == nil || s.Merge == nil {
		return nil
	}
	out := map[string]string{}
	for _, st := range s.Work.Rows() {
		if v := s.StreamPriority(st); v != PriorityNormal {
			out[st] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// PriorityLine is where's line beside the critical list: "priority: blocker s1-4; low s2-1,
// s2-2 (+3)", the levels high to low, at most five cards a level; "" when every card is
// normal.
func PriorityLine(counts map[string][]string, streams map[string]string) string {
	var parts []string
	ladder := slices.Insert(slices.Clone(PriorityLadder), 2, CriticalByWeight)
	for _, l := range ladder {
		ids := counts[l]
		if len(ids) == 0 {
			continue
		}
		shown := ids[:min(5, len(ids))]
		part := l + " " + strings.Join(shown, ", ")
		if len(ids) > len(shown) {
			part += fmt.Sprintf(" (+%d)", len(ids)-len(shown))
		}
		parts = append(parts, part)
	}
	var defs []string
	for _, st := range slices.Sorted(maps.Keys(streams)) {
		defs = append(defs, st+" "+streams[st])
	}
	if len(defs) > 0 {
		parts = append(parts, "stream defaults "+strings.Join(defs, ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	return "priority: " + strings.Join(parts, "; ")
}

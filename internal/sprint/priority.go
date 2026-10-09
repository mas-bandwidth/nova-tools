package sprint

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
)

// A card's priority (docs/SPEC-SPRINT.md section 1, "Priority"): every card carries one
// effective level: blocker, critical, fix, high, reader, normal, low. A read card is always
// reader (ReadPriority), with producer urgency ordering reads within its queue, never set by
// hand. A deal places eligible urgent work before READER consumers, then normal and low work
// (reads_priority.go); land takes the stream with the highest merging level first. A
// primary is normal unless a level is set on it: by its brief's line `PRIORITY: <level>` at
// admission, else by its stream's default at admission, or by the verb `priority`, each change
// on its timeline with the actor and the reason. Critical is computed too (CriticalBehind or
// more cards behind it, weight.go) unless a level is set by hand. Low is dealt only to a lane
// nothing higher can fill. The deal and the ask order their cards by the ladder, then stream
// turns within a level (ladderOrder, readOrder); land orders the streams by their merging
// sets' levels (LandOrder), then eligible cards inside a batch (MergePriorityOrder). Computed
// critical remains display-only. An occupied slot is never preempted in v1.2; blocker slot
// preemption is deferred to v1.3.

// The priority ladder, highest first.
const (
	PriorityBlocker  = "blocker"
	PriorityCritical = "critical"
	PriorityFix      = "fix"
	PriorityHigh     = "high"
	PriorityReader   = "reader"
	PriorityNormal   = "normal"
	PriorityLow      = "low"
)

// PriorityLadder is the levels, highest first.
var PriorityLadder = []string{PriorityBlocker, PriorityCritical, PriorityFix, PriorityHigh, PriorityReader, PriorityNormal, PriorityLow}

// PrioritySettable is the levels a primary may be given (reader is a read card's alone).
var PrioritySettable = []string{PriorityBlocker, PriorityCritical, PriorityFix, PriorityHigh, PriorityNormal, PriorityLow}

// FieldPriority is a primary's priority as set (by its brief's PRIORITY line or its stream's
// default at admission, or by the verb priority); absent is normal, or critical by weight.
const FieldPriority = "priority"

// FieldProducerPriority retains ordinary producer urgency within READ and FIX roles
// without changing their effective priority (SPEC-SPRINT, Priority).
const FieldProducerPriority = "producer_priority"

// ProducerPriority preserves ordinary urgency through repairs and reads (SPEC-SPRINT,
// Priority). Legacy fixed/read roles without that metadata have ordinary normal urgency.
func ProducerPriority(c *Card) string {
	if level := c.F(FieldProducerPriority); level != "" {
		return PriorityLadder[priorityRank(level)]
	}
	level := c.F(FieldPriority)
	if level == PriorityFix || level == PriorityReader {
		return PriorityNormal
	}
	return PriorityLadder[priorityRank(level)]
}

// priorityFields applies a manual level without removing a FIX role (SPEC-SPRINT,
// Priority). Entering FIX captures ordinary urgency; later changes update only urgency.
func priorityFields(c *Card, level string) map[string]string {
	set := map[string]string{FieldPriority: level}
	if level == PriorityFix || c.F(FieldPriority) == PriorityFix {
		set[FieldPriority] = PriorityFix
		set[FieldProducerPriority] = level
		if level == PriorityFix {
			set[FieldProducerPriority] = ProducerPriority(c)
		}
	}
	return set
}

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
// the seven of the ladder (reader is a read card's alone). Add and Recut refuse a brief with why
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
// producer urgency orders within that role, ReadPriority);
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

// priorityOnWork writes the primary's explicit level on a new work card (SPEC-SPRINT,
// Priority), when it is not normal. Computed critical remains display-only.
func priorityOnWork(fields map[string]string, pr *Card) {
	if l := PriorityLadder[cardRank(pr)]; l != PriorityNormal {
		fields[FieldPriority] = l
	}
	if pr.F(FieldPriority) == PriorityFix || pr.F(FieldProducerPriority) != "" {
		fields[FieldProducerPriority] = ProducerPriority(pr)
	}
}

// QueuePriority is a consumer's inherited level: seeded by its deal or ask, refreshed by
// SetPriority while queued, and retained by its active lease; absent is reader or normal.
func QueuePriority(c *Card) string {
	if isRead(c) {
		return PriorityReader
	}
	if v := c.F(FieldPriority); v != "" {
		return v
	}
	return PriorityNormal
}

// consumerPriority is the primary's explicit level for work and always reader for reads
// when a consumer enters a queue (SPEC-SPRINT, Priority; PriorityFlow.tla Rank).
func consumerPriority(c, pr *Card) string {
	if isRead(c) {
		return PriorityReader
	}
	return PriorityLadder[cardRank(pr)]
}

// consumerPriorityFields refreshes role and urgency whenever a consumer enters its queue
// (SPEC-SPRINT, Priority). Active leases retain the metadata they took.
func consumerPriorityFields(c, pr *Card) map[string]string {
	set := map[string]string{FieldPriority: consumerPriority(c, pr)}
	if isRead(c) || pr.F(FieldPriority) == PriorityFix || pr.F(FieldProducerPriority) != "" {
		set[FieldProducerPriority] = ProducerPriority(pr)
	}
	return set
}

// QueueOrder refines an existing queue by its inherited priority (docs/SPEC-SPRINT.md,
// Priority). SetPriority updates queued copies with their primary in one unit; reads win
// ties with work, and otherwise the existing score or stream-turn order stands. Working
// leases are never selected or cancelled here.
func QueueOrder(cards []*Card) []*Card {
	out := slices.Clone(cards)
	slices.SortStableFunc(out, func(a, b *Card) int {
		if d := priorityRank(QueuePriority(a)) - priorityRank(QueuePriority(b)); d != 0 {
			return d
		}
		if isRead(a) && isRead(b) || QueuePriority(a) == PriorityFix {
			return priorityRank(ProducerPriority(a)) - priorityRank(ProducerPriority(b))
		}
		if isRead(a) != isRead(b) {
			if isRead(a) {
				return -1
			}
			return 1
		}
		return 0
	})
	return out
}

// QueueAdmissionOrder is the queue packet and Take order: priority refines the
// existing stream turns from the member's fleet-row offset. In-flight cards do
// not spend a queued stream turn; they retain their order after the admission set.
func QueueAdmissionOrder(cards []*Card, offset int) []*Card {
	var ready, flight []*Card
	for _, c := range cards {
		if c.Col == Ready {
			ready = append(ready, c)
		} else {
			flight = append(flight, c)
		}
	}
	return append(QueueOrder(takeTurns(ready, offset)), flight...)
}

// priorityOnRead stamps the read role and its producer urgency for every ask or return
// (SPEC-SPRINT, Priority). Urgency orders reads without changing their role.
func priorityOnRead(fields map[string]string, pr *Card) {
	fields[FieldPriority] = PriorityReader
	fields[FieldProducerPriority] = ProducerPriority(pr)
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
	sort.SliceStable(out, func(i, j int) bool { return priorityLess(out[i], out[j]) })
	return out
}

// readRank is every read's role rank on the admission ladder (SPEC-SPRINT, Priority).
func readRank(_ *Card) int { return priorityRank(PriorityReader) }

// ReadPriority is always reader: producer urgency is a secondary read-queue order.
func ReadPriority(pr *Card) string { return PriorityLadder[readRank(pr)] }

// readOrder orders review by producer urgency within the READER queue (SPEC-SPRINT,
// Priority), stable: equals keep the ask's stream turns and work order.
func readOrder(cards []*Card) []*Card {
	out := slices.Clone(cards)
	sort.SliceStable(out, func(i, j int) bool {
		return priorityRank(ProducerPriority(out[i])) < priorityRank(ProducerPriority(out[j]))
	})
	return out
}

// LandOrder is the streams in the order land takes their batches: by the highest level of any
// card in the stream's merging set (cardRank; never the stream's default, never its oldest
// card), highest first, then by the most cards behind any of them (FieldBehind), then the order
// given; inside a stream the batch stays as it is (the owner, 2026-10-06: "a normal stream with
// one critical card merging goes ahead of a high stream whose merging cards are all high").
func LandOrder(s *Snapshot, streams []string) []string {
	rank, urgency, weight := map[string]int{}, map[string]int{}, map[string]int{}
	for _, st := range streams {
		rank[st] = priorityRank(PriorityLow) + 1
		urgency[st] = priorityRank(PriorityLow) + 1
		if s == nil || s.Work == nil {
			continue
		}
		for _, c := range s.Work.Cell(st, Merging) {
			rank[st] = min(rank[st], cardRank(c))
			if c.F(FieldPriority) == PriorityFix {
				urgency[st] = min(urgency[st], priorityRank(ProducerPriority(c)))
			}
			weight[st] = max(weight[st], c.Int(FieldBehind))
		}
	}
	out := slices.Clone(streams)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if rank[a] != rank[b] {
			return rank[a] < rank[b]
		}
		if rank[a] == priorityRank(PriorityFix) && urgency[a] != urgency[b] {
			return urgency[a] < urgency[b]
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
	queued := map[string][]Change{}
	for _, table := range []string{Fleet, Readers} {
		if s.T(table) == nil {
			continue
		}
		col := Ready
		if table == Readers {
			col = Asked
		}
		for _, child := range s.T(table).Column(col) {
			pr := &Card{Fields: priorityFields(s.Work.Card(child.F(PrimaryField)), r.Level)}
			set := consumerPriorityFields(child, pr)
			changed := false
			for key, value := range set {
				changed = changed || child.F(key) != value
			}
			if changed {
				queued[child.F(PrimaryField)] = append(queued[child.F(PrimaryField)],
					change(table, setEntry(child, set)))
			}
		}
	}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if pr, _, _, ok := ParseReadCard(id); ok {
			p.refuse(id, "a read card always has reader priority; change its producer urgency on its primary: nova-sprint priority "+pr+" --"+r.Level)
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
		set := priorityFields(c, r.Level)
		unchanged := true
		for key, value := range set {
			unchanged = unchanged && c.F(key) == value
		}
		if unchanged {
			p.Said = append(p.Said, id+" priority "+r.Level+" already; no change")
			if changes := queued[id]; len(changes) > 0 {
				p.Units = append(p.Units, Unit{Key: id, Stream: c.Row, Changes: changes,
					Moved: id + " queued cards inherit priority " + r.Level})
			}
			continue
		}
		n := happened(NPrioritySet, c.Row, s.Now, c.ID)
		n.Who, n.What = r.Who, fmt.Sprintf("%s priority %s -> %s%s", c.ID, was, r.Level, why)
		if set[FieldPriority] == PriorityFix {
			n.What = fmt.Sprintf("%s priority %s -> fix; producer urgency %s -> %s%s", c.ID, was, ProducerPriority(c), set[FieldProducerPriority], why)
		}
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.Row,
			Changes: append([]Change{change(Work, setEntry(c, set))}, queued[id]...), Notes: []Note{n},
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
	ladder := slices.Insert(slices.Clone(PriorityLadder), priorityRank(PriorityCritical)+1, CriticalByWeight)
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

// reworkPriority gives every repair FIX while retaining ordinary producer urgency
// (SPEC-SPRINT, Priority). Stored legacy high/keep settings also schedule FIX.
func reworkPriority(s *Snapshot, c *Card, set map[string]string) *Card {
	set[FieldPriority], set[FieldProducerPriority] = PriorityFix, ProducerPriority(c)
	return withField(withField(c, FieldPriority, PriorityFix), FieldProducerPriority, set[FieldProducerPriority])
}

// priorityLess compares effective levels and producer urgency within FIX; ordinary ties
// keep work order (SPEC-SPRINT, Priority).
func priorityLess(a, b *Card) bool {
	if cardRank(a) != cardRank(b) {
		return cardRank(a) < cardRank(b)
	}
	return a.F(FieldPriority) == PriorityFix && priorityRank(ProducerPriority(a)) < priorityRank(ProducerPriority(b))
}

// MergePriorityOrder orders eligible merge cards by priority without passing their
// named or positional dependencies (docs/SPEC-SPRINT.md, Priority). Equal levels
// retain work order; cards with unresolved dependencies are not eligible.
func MergePriorityOrder(s *Snapshot, cards []*Card) []*Card {
	pending := slices.Clone(cards)
	landed := map[string]bool{}
	out := make([]*Card, 0, len(cards))
	for len(pending) > 0 {
		best := -1
		for i, c := range pending {
			pr := s.Work.Card(c.ID)
			if len(WaitsFor(s, pr, landed)) != 0 {
				continue
			}
			if best < 0 || priorityLess(pr, s.Work.Card(pending[best].ID)) {
				best = i
			}
		}
		if best < 0 {
			return out
		}
		out = append(out, pending[best])
		landed[pending[best].ID] = true
		pending = slices.Delete(pending, best, best+1)
	}
	return out
}

// FixStateCounts is the dashboard's repair state, partitioned by original column.
// Completed work waiting for a read remains review, even when its priority is fix.
// Counts are captured by the tick, so the dashboard never scans cards per refresh.
func FixStateCounts(s *Snapshot) map[string]map[string]int {
	var counts map[string]map[string]int
	if s == nil || s.Work == nil {
		return counts
	}
	for _, col := range []string{Ready, Working, Review} {
		for _, c := range s.Work.Column(col) {
			level, _ := CardPriority(c)
			if level == PriorityBlocker || level == PriorityCritical {
				continue
			}
			repair := level == PriorityFix
			if col == Review {
				var work *Card
				if s.Fleet != nil {
					work = s.Fleet.Card(c.F("work"))
				}
				repair = work != nil && (work.Col == DoneFailed || work.Col == DoneDefect)
			}
			if !repair {
				continue
			}
			if counts == nil {
				counts = map[string]map[string]int{}
			}
			if counts[c.Row] == nil {
				counts[c.Row] = map[string]int{}
			}
			counts[c.Row][col]++
		}
	}
	return counts
}

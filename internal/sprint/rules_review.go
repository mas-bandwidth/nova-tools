package sprint

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The review rules of the event-driven tick (the upper design, version 2.1,
// item IT09, section 2.3): R8 ask, R9 accept and R10 rework, the three moves
// the machine makes of a primary in review, each on the keys its lines queue
// (2.1) and each a pure function of what its read loaded.
//
//	R8  ask     two read cards, to two different readers, for a primary whose
//	            work came back ok and that has no read at its attempt.
//	R9  accept  review -> merging, for a primary with ok reads from two
//	            different readers at its head, CI not red, not returned.
//	R10 rework  the next attempt, dealt at once, for a primary whose work came
//	            back failed or that a reader found broken; at the attempts
//	            bound, the bound.
//
// Every rule plans over the primaries its read loaded and decides from their
// state, not from what its key says: a key woken by its own line that finds
// nothing to do is removed (2.1). So a rule run a second time on the same keys
// with nothing changed writes nothing and raises nothing (1.3.3, E7), and a
// key is delivered at least once. A rule does not read the cells of the review
// column: it takes the primaries its read loaded (the cards of the work table
// the snapshot holds), so that a partial snapshot is never scanned (1.5.2).
//
// Every plan says the keys it finished (Done) and, for the askwait key, the one
// it puts back. A plan too large for one request is cut by the step builder,
// which then leaves the plan's keys queued (1.3.6): a rule does not cut its
// own plan.
//
// A card a rule chose and its planner refused is marked and named (1.3.5): the
// rule sets refused = "<rule>: <reason>" on the primary in its own step, which
// takes it out of the rule's conditions until ack clears it, and asks J for
// "the machine could not move a card". A judgment a rule raises is a NoteReq
// and every notice is one; a note names every subject of its step with the
// same type and cause (1.3.4), so the plans carry one request a type, cause
// and stream.

// The review rules' numbers. Each has the section of the upper design that
// gives it beside it.
const (
	// MaxAttempts is the attempt at which R10 no longer reworks: an attempt
	// below it is reworked by the machine, at it the primary reaches its
	// bound (2.3 R10).
	MaxAttempts = 3
	// AskReaders is how many different readers R8 asks of a primary (2.3 R8).
	AskReaders = 2
	// AcceptReaders is how many different readers must have said ok at a
	// primary's head for R9 to accept it (2.3 R9).
	AcceptReaders = 2
	// MaxRCards is how many read cards a primary ever has: `rcards` holds at
	// most 15 (1.3.1).
	MaxRCards = 15
	// BoundAttempts is the value of `bound` that says the primary reached the
	// attempts bound (1.3.1: `bound` is `redeals` or `attempts`).
	BoundAttempts = "attempts"
	// AskwaitChunk is how many of askwait one pass of R8 reads (2.3 R8: the
	// first 2,000 of askwait).
	AskwaitChunk = 2000
	// reviewLineIDs is the most ids a line names, so the most primaries a key
	// that names a line reads: one generated line is at most 2,000 ids (1.0).
	reviewLineIDs = 2000
	// reviewReasonBytes is how much of a fix or a finding a notice repeats.
	// The design bounds no notice's text; a notice lists at most MaxListed
	// primaries, so this keeps one under 64 KiB, the bound of a field value
	// (1.0).
	reviewReasonBytes = 200
)

// The types of the notices these rules raise (2.5). A judgment they raise is
// named by its constant of notes.go (NCannotAsk, NBound) or ingest.go
// (typeCouldNotMove).
const (
	typeAccepted = "accepted by the machine"
	typeReworked = "reworked by the machine"
)

// The words of a NoteReq's Op and of an XGuard's Kind that the review rules use
// (8.0), and the name of the set R8's askwait key reads the head of (1.3.1).
const (
	reviewOpen        = "open"
	reviewClose       = "close"
	reviewKnow        = "know"
	guardMemberUp     = "memberup"
	reviewHeadAskwait = "askwait"
)

// reviewCauses is the cause each judgment these rules raise is kept under
// (1.3.4: one judgment a type, cause and subject). "The machine could not move
// a card" is raised under the name of the rule that refused the card.
var reviewCauses = map[string]string{
	NCannotAsk: "readers",
	NBound:     BoundAttempts,
}

// reviewFixWhenSilent is the fix of a rework when the primary's evidence says
// nothing of its own, by the evidence: a work card that came back failed with
// no report, a read that found it broken with no finding.
var reviewFixWhenSilent = map[string]string{
	"failed": "the work came back failed and gave no report",
	"broken": "a reader found the work broken and gave no finding",
}

// reviewRule is one row of the table that registers the three rules: the name
// of the keys a rule takes, its place in the round robin (1.4.2: after R7 and
// before R11), what its read projects and follows, the queries that do not
// depend on its keys, and its plan.
type reviewRule struct {
	name     string
	priority int
	section  string
	fields   []string
	follow   []string
	fixed    []SprintQ
	plan     func(s *Snapshot, keys []AgendaKey, now Now) RulePlan
}

var reviewRules = []reviewRule{
	{
		name: ruleAsk, priority: 8, section: "2.3 R8",
		fields: []string{"attempt", "result", "asked", "refused", "rcards", "head"},
		follow: []string{"rcards", "jopen"},
		fixed:  []SprintQ{{Kind: qReaders}},
		plan:   planAsk,
	},
	{
		name: ruleAccept, priority: 9, section: "2.3 R9",
		fields: []string{"attempt", "head", "ci", "ci_head", "refused", "rcards"},
		follow: []string{"rcards", "merge", "control", "jopen"},
		plan:   planAccept,
	},
	{
		name: ruleRework, priority: 10, section: "2.3 R10",
		fields: []string{"attempt", "result", "head", "asked", "refused", "rcards", "work", "avoid", "rereads", "bound", "reworks", "broken_reads"},
		follow: []string{"rcards", "work"},
		fixed:  []SprintQ{{Kind: qFleet}},
		plan:   planRework,
	},
}

func init() {
	for _, r := range reviewRules {
		RegisterRule(Rule{Name: r.name, Priority: r.priority, Read: r.read, Plan: r.plan})
	}
}

// The kinds of key these rules take (E6).
const (
	keyPrimary = iota // ask:p names a primary
	keyLine           // ask@48213 names a line of the log, ask@48213+400 the same line from its 400th id
	keyHead           // askwait names the head of the set of primaries that could not be asked
)

// reviewKey is an agenda key of these rules taken apart.
type reviewKey struct {
	kind    int
	subject string
	line    uint64
	offset  int
}

// parseReviewKey reads a key of one of these rules; false for one that names
// nothing it can read.
func parseReviewKey(k AgendaKey) (reviewKey, bool) {
	rule := RuleOf(k.Key)
	rest := k.Key[len(rule):]
	switch {
	case rest == "" && rule == ruleAskwait:
		return reviewKey{kind: keyHead}, true
	case rest != "" && rest[0] == ':' && len(rest) > 1:
		return reviewKey{kind: keyPrimary, subject: rest[1:]}, true
	case rest != "" && rest[0] == '@':
		seq, off, _ := strings.Cut(rest[1:], "+")
		n, err := strconv.ParseUint(seq, 10, 64)
		if err != nil {
			return reviewKey{}, false
		}
		rk := reviewKey{kind: keyLine, line: n}
		if off != "" {
			if rk.offset, err = strconv.Atoi(off); err != nil || rk.offset < 0 {
				return reviewKey{}, false
			}
		}
		return rk, true
	}
	return reviewKey{}, false
}

// read is a rule's read (8.0): the queries its keys need, sized by their
// declared cost to what layer 1 lets one read hold, and the keys it left for
// later. A key that names a primary costs one primary's records; one that
// names a line costs the line's most ids (reviewLineIDs less the offset), and
// askwait its chunk, both more than one read holds, so such a key is read
// alone and the cut of its queries is ReadPlan.Split's; the first key is
// always read, since a read of one key is the least there is. A halving
// halves the records the read may cost. A key that names nothing costs
// nothing and is taken, for the plan removes it.
func (r reviewRule) read(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	var rp ReadPlan
	per := QueryCost(SprintQ{Kind: qRelated, Table: Work, IDs: []string{""}, Fields: r.fields, Follow: r.follow}).Records
	fixed := 0
	for _, q := range r.fixed {
		fixed += QueryCost(q).Records
	}
	budget := max(per, (b.Records-fixed)>>max(0, halvings))
	related := func(q SprintQ) SprintQ {
		q.Kind, q.Table, q.Fields, q.Follow = qRelated, Work, r.fields, r.follow
		return q
	}
	var ids []string
	var sources []SprintQ
	used, took, i := 0, 0, 0
	for ; i < len(keys); i++ {
		rk, ok := parseReviewKey(keys[i])
		cost := 0
		switch {
		case !ok:
		case rk.kind == keyLine:
			cost = per * max(1, reviewLineIDs-rk.offset)
		case rk.kind == keyHead:
			cost = per * AskwaitChunk
		default:
			cost = per
		}
		if took > 0 && used+cost > budget {
			break
		}
		used += cost
		if cost > 0 {
			took++
		}
		switch {
		case !ok:
		case rk.kind == keyLine:
			sources = append(sources, related(SprintQ{Line: rk.line, Offset: rk.offset, Limit: max(1, reviewLineIDs-rk.offset)}))
		case rk.kind == keyHead:
			sources = append(sources, related(SprintQ{Head: reviewHeadAskwait, Limit: min(AskwaitChunk, max(1, budget/per))}))
		default:
			ids = append(ids, rk.subject)
		}
	}
	if len(ids) > 0 {
		rp.Sprint = append(rp.Sprint, related(SprintQ{IDs: ids}))
	}
	rp.Sprint = append(rp.Sprint, sources...)
	if len(rp.Sprint) > 0 {
		rp.Sprint = append(rp.Sprint, r.fixed...)
	}
	return rp, keys[i:]
}

// The predicates of the rules' conditions, for the held rule's table of who
// holds a card in review (2.3 R16: R8 never asked at its attempt, R9 two oks,
// CI not red, not returned, R10 failed or broken): each says, of a snapshot
// that holds the primary and what its rule reads, that the rule would act on
// it now. A refused primary is out of all three, held by its judgment.

// AskDue says R8 would ask the primary: it is in review, its work did not come
// back failed, it is not refused, and it has no read card at its attempt.
func AskDue(s *Snapshot, c *Card) bool {
	if !reviewInReview(c) || c.F("refused") != "" || c.F("result") == "failed" {
		return false
	}
	for _, rc := range reviewReads(s, c, c.Int("attempt")) {
		if rc.Placed() {
			return false
		}
	}
	return true
}

// AcceptDue says R9 would accept the primary: it is in review and not refused,
// ok reads from two different readers stand at its head, its CI is not red at
// its head (a red CI is judged by "ci red on a primary") and "returned to
// review" is not open on it (the coordinator decides it).
func AcceptDue(s *Snapshot, c *Card) bool {
	if !reviewInReview(c) || c.F("refused") != "" || len(reviewOKReaders(s, c)) < AcceptReaders {
		return false
	}
	return !reviewCIRed(c) && !reviewHeld(s, NReturned, c.ID)
}

// ReworkDue says R10 would act on the primary: it is in review, not refused
// and not at its bound, and its work came back failed or a reader found it
// broken at its attempt. At MaxAttempts the rule sets the bound; below it, it
// reworks.
func ReworkDue(s *Snapshot, c *Card) bool {
	if !reviewInReview(c) || c.F("refused") != "" || c.F("bound") != "" {
		return false
	}
	return c.F("result") == "failed" || len(reviewBroken(s, c)) > 0
}

// reviewInReview says the primary is placed in review.
func reviewInReview(c *Card) bool { return c.Placed() && c.Col == Review }

// reviewCIRed says the primary's CI is red at its head: a red result of an
// older head does not hold it, and a red one that names no head is taken as its
// current head's.
func reviewCIRed(c *Card) bool {
	return c.F("ci") == "red" && (c.F("ci_head") == "" || c.F("ci_head") == c.F("head"))
}

// reviewHeld says a judgment of the type is open on the subject, or held on
// it by an acknowledgement.
func reviewHeld(s *Snapshot, typ, subject string) bool {
	return hasOpen(s.Open, typ, subject) || hasOpen(s.Acked, typ, subject)
}

// reviewPrimaries is the primaries in review the snapshot holds, in work
// order.
func reviewPrimaries(s *Snapshot) []*Card {
	if s == nil || s.Work == nil {
		return nil
	}
	var out []*Card
	for _, c := range s.Work.Cards {
		if reviewInReview(c) {
			out = append(out, c)
		}
	}
	SortCards(out)
	return out
}

// reviewReads is the read cards of a primary at an attempt that the snapshot
// holds, placed or retired: those its rcards lists, then those a reader's row
// derives (ReadCardID) that rcards does not (a read card made before rcards
// listed it). A retired card is a record kept, and its reader has read the
// attempt.
func reviewReads(s *Snapshot, c *Card, attempt int) []*Card {
	if s.Readers == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []*Card
	add := func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		if rc := s.Readers.Card(id); rc != nil && rc.Int("attempt") == attempt {
			out = append(out, rc)
		}
	}
	for _, id := range Split(c.F("rcards")) {
		add(id)
	}
	for _, rd := range s.Readers.Rows {
		add(ReadCardID(c.ID, attempt, rd))
	}
	return out
}

// reviewOKReaders is the ok read cards of two different readers at the
// primary's attempt and head, in reader row order: fewer when there are fewer.
// A card whose row, id and reader field do not name one reader counts for no
// reader (ReadCardAgrees).
func reviewOKReaders(s *Snapshot, c *Card) []*Card {
	if s.Readers == nil {
		return nil
	}
	seen := map[string]bool{}
	var oks []*Card
	for _, rc := range reviewReads(s, c, c.Int("attempt")) {
		rd := rc.F("reader")
		if rc.Placed() && rc.Col == OK && rc.F("head") == c.F("head") && ReadCardAgrees(rc) && !seen[rd] {
			seen[rd] = true
			oks = append(oks, rc)
		}
	}
	rank := map[string]int{}
	for i, rd := range s.Readers.Rows {
		rank[rd] = i
	}
	sort.SliceStable(oks, func(i, j int) bool {
		ri, iok := rank[oks[i].F("reader")]
		rj, jok := rank[oks[j].F("reader")]
		if iok != jok {
			return iok
		}
		if ri != rj {
			return ri < rj
		}
		return oks[i].F("reader") < oks[j].F("reader")
	})
	if len(oks) > AcceptReaders {
		oks = oks[:AcceptReaders]
	}
	return oks
}

// reviewBroken is the read cards at the primary's attempt that found it
// broken and still stand.
func reviewBroken(s *Snapshot, c *Card) []*Card {
	var out []*Card
	for _, rc := range reviewReads(s, c, c.Int("attempt")) {
		if rc.Placed() && rc.Col == Broken {
			out = append(out, rc)
		}
	}
	return out
}

// reviewWorkCard is the work card of the primary's attempt: the one it names,
// else the one its attempt derives; nil when the snapshot holds neither.
func reviewWorkCard(s *Snapshot, c *Card) *Card {
	if s.Fleet == nil {
		return nil
	}
	if wc := s.Fleet.Card(c.F("work")); wc != nil {
		return wc
	}
	return s.Fleet.Card(WorkCardID(c.ID, c.Int("attempt")))
}

// reviewKeys is the keys a plan finished, as a copy.
func reviewKeys(keys []AgendaKey) []AgendaKey { return append([]AgendaKey(nil), keys...) }

// reviewWall is the wall clock as the stamps for the reader are written
// (1.2: wall stamps stay for the reader, and no rule reads them).
func reviewWall(now Now) string { return stamp(time.UnixMilli(now.Wall)) }

// reviewDue is a due field's value: running milliseconds, R plus the span.
func reviewDue(now Now, span time.Duration) string {
	return strconv.FormatInt(now.R+span.Milliseconds(), 10)
}

// reviewNowOf is the clocks a verb plans at when it is given none: the
// snapshot's time as both. R is the wall time less the time the machine was
// STOPPED, so this is R only for a machine that never was; a caller that has R
// gives it to ReworkAt.
func reviewNowOf(s *Snapshot) Now {
	ms := s.Now.UnixMilli()
	return Now{R: ms, Wall: ms, Running: true}
}

// reviewCut is text cut to at most n bytes at a rune boundary, with "..." when
// it was cut.
func reviewCut(text string, n int) string {
	if len(text) <= n {
		return text
	}
	for n > 0 && n < len(text) && text[n]&0xC0 == 0x80 {
		n--
	}
	return text[:n] + "..."
}

// reviewList is the lines of a notice, at most MaxListed of them and the count
// of the rest.
func reviewList(lines []string) string {
	if len(lines) <= MaxListed {
		return strings.Join(lines, "; ")
	}
	return fmt.Sprintf("%s; and %d more", strings.Join(lines[:MaxListed], "; "), len(lines)-MaxListed)
}

// reviewItem is one primary a notice is about: its stream and what the notice
// says of it.
type reviewItem struct{ id, stream, line string }

// reviewNotices is the notices of a type, one a stream in the order the
// streams first appear (2.5: a notice is grouped by type and stream), each
// naming the primaries of its stream.
func reviewNotices(typ string, items []reviewItem) []NoteReq {
	var order []string
	byStream := map[string][]reviewItem{}
	for _, it := range items {
		if _, ok := byStream[it.stream]; !ok {
			order = append(order, it.stream)
		}
		byStream[it.stream] = append(byStream[it.stream], it)
	}
	var out []NoteReq
	for _, st := range order {
		var subjects, lines []string
		for _, it := range byStream[st] {
			subjects = append(subjects, it.id)
			lines = append(lines, it.line)
		}
		out = append(out, NoteReq{Op: reviewKnow, Type: typ, Subjects: subjects, Text: reviewList(lines)})
	}
	return out
}

// reviewRefused is what a rule does with the cards its planner refused (1.3.5):
// each primary gets refused = "<rule>: <reason>", in the plan's own step, and
// one request asks J for "the machine could not move a card" on all of them.
// The refusals stay in the plan too, for whoever counts them.
func reviewRefused(rp *RulePlan, s *Snapshot, rule string) {
	var ids []string
	var why string
	for _, x := range rp.Plan.Refused {
		c := s.Work.Card(x.Key)
		if !c.Placed() {
			continue
		}
		ids, why = append(ids, c.ID), x.Why
		rp.Plan.Units = append(rp.Plan.Units, Unit{Key: c.ID, Stream: c.Row,
			Changes: []Change{change(Work, setEntry(c, map[string]string{"refused": rule + ": " + x.Why}))},
			Moved:   c.ID + " refused by " + rule + ": " + x.Why})
	}
	switch len(ids) {
	case 0:
		return
	case 1:
		why = rule + ": " + why
	default:
		why = fmt.Sprintf("%s: %d cards could not be moved; each names its reason in its refused field", rule, len(ids))
	}
	rp.Notes = append(rp.Notes, NoteReq{Op: reviewOpen, Type: typeCouldNotMove, Cause: rule, Subjects: ids, Text: why})
}

// R8 ask. Trigger: ask:<p> or ask@<seq> (a line that put primaries in review
// with result ok), askwait (a reader added, start), the owner of "cannot ask".
// Read: each primary (attempt, result, the readers it names, refused, rcards,
// head) and its read cards (rcards), the judgments open on it, and the
// readers with their asked counts. Effect: each primary in review that is not
// refused, whose work did not fail and that has no read card at its attempt
// is asked of two different readers, the readers it names first and then the
// readers with the shortest asked queues; a reader that has a card at this
// attempt, retired too, is not asked again, so a read that replaces another
// goes to a reader not yet asked. Each new id is appended to rcards. With
// fewer than two readers able, "cannot ask" is opened on the primary, which J
// puts in askwait; asked, it is closed. Key: removed, askwait put back while
// the pass asked a primary, since a pass that asks none has nothing to make
// progress on. Cost: O(r) a primary and one step for every primary of the tick,
// O(p log p) to put the primaries in order.
func planAsk(s *Snapshot, keys []AgendaKey, now Now) RulePlan {
	var rp RulePlan
	var p Plan
	if s.Readers == nil {
		return RulePlan{Done: reviewKeys(keys)}
	}
	q := reviewAskedQueues(s)
	var asked, cannot, closing []string
	for _, c := range reviewPrimaries(s) {
		if !AskDue(s, c) {
			continue
		}
		attempt := c.Int("attempt")
		chosen := reviewAble(s, c, q)
		if len(chosen) < AskReaders {
			if !reviewHeld(s, NCannotAsk, c.ID) {
				cannot = append(cannot, c.ID)
			}
			continue
		}
		var ids []string
		for _, rd := range chosen {
			ids = append(ids, ReadCardID(c.ID, attempt, rd))
		}
		rcards := append(Split(c.F("rcards")), ids...)
		if len(rcards) > MaxRCards {
			p.refuse(c.ID, fmt.Sprintf("%s would have %d read cards, and a primary has at most %d", c.ID, len(rcards), MaxRCards))
			continue
		}
		u := Unit{Key: c.ID, Stream: c.Row}
		for i, rd := range chosen {
			q[rd]++
			u.Changes = append(u.Changes, change(Readers, createEntry(ids[i], rd, Asked, c.Score, map[string]string{
				"kind": "read", "primary": c.ID, "stream": c.Row, "reader": rd, "attempt": itoa(attempt), "head": c.F("head"),
				"asked": reviewWall(now), "asked_r": strconv.FormatInt(now.R, 10), "due_unbegun": reviewDue(now, DeadlineUnbegun)})))
		}
		u.Changes = append(u.Changes, change(Work, setEntry(c, map[string]string{"asked": strings.Join(chosen, ","), "rcards": strings.Join(rcards, ",")})))
		u.Moved = c.ID + " asked of " + strings.Join(chosen, ", ")
		p.Units = append(p.Units, u)
		asked = append(asked, c.ID)
		if reviewHeld(s, NCannotAsk, c.ID) {
			closing = append(closing, c.ID)
		}
	}
	rp.Plan = p
	reviewRefused(&rp, s, ruleAsk)
	if len(cannot) > 0 {
		rp.Notes = append(rp.Notes, NoteReq{Op: reviewOpen, Type: NCannotAsk, Cause: reviewCauses[NCannotAsk], Subjects: cannot,
			Text: "fewer than two different readers are free to read an attempt; add one: nova-sprint reader add <name>"})
	}
	if len(closing) > 0 {
		rp.Notes = append(rp.Notes, NoteReq{Op: reviewClose, Type: NCannotAsk, Cause: reviewCauses[NCannotAsk], Subjects: closing,
			Text: "asked"})
	}
	for _, k := range keys {
		if rk, ok := parseReviewKey(k); ok && rk.kind == keyHead && len(asked) > 0 {
			rp.Requeue = append(rp.Requeue, k)
			continue
		}
		rp.Done = append(rp.Done, k)
	}
	return rp
}

// reviewAskedQueues is each reader's asked count, the queue a new read joins.
func reviewAskedQueues(s *Snapshot) map[string]int {
	q := map[string]int{}
	if s.Readers == nil {
		return q
	}
	for _, rd := range s.Readers.Rows {
		q[rd] = s.Readers.Count(rd, Asked)
	}
	return q
}

// reviewAble is the readers to ask of a primary: up to AskReaders different
// ones among those that have no card at its attempt, retired ones included; the
// readers the primary names first, in the order it names them, then the ones
// with the shortest asked queues, the first in row order on a tie. Fewer than
// AskReaders when fewer are able.
func reviewAble(s *Snapshot, c *Card, q map[string]int) []string {
	attempt := c.Int("attempt")
	var free []string
	for _, rd := range s.Readers.Rows {
		if s.Readers.Card(ReadCardID(c.ID, attempt, rd)) == nil {
			free = append(free, rd)
		}
	}
	var chosen []string
	for _, rd := range Split(c.F("asked")) {
		if contains(free, rd) && !contains(chosen, rd) && len(chosen) < AskReaders {
			chosen = append(chosen, rd)
		}
	}
	for len(chosen) < AskReaders {
		var left []string
		for _, rd := range free {
			if !contains(chosen, rd) {
				left = append(left, rd)
			}
		}
		if len(left) == 0 {
			break
		}
		chosen = append(chosen, shortest(left, q))
	}
	return chosen
}

// R9 accept. Trigger: accept:<p> or accept@<seq> (a read card entered ok, a
// primary's CI fields set). Read: each primary (with ci, ci_head), its read
// cards at its attempt, its merge card, its stream's control card and the
// judgments open on it. Plan: a primary whose CI is red at its head, or with
// "returned to review" open, is skipped and its key removed (the coordinator
// decides it). Guard: the primary at review with its revision, so a CI report
// or a return since the read refuses the step; the two ok read cards at ok
// with their revisions, of two different readers; the merge card absent or at
// returned; the stream's control card with its revision. Effect: review ->
// merging, the merge card queued at the primary's score (created, or moved from
// returned), the stream merging with since and due_mergeidle set when it was
// waiting, and every read card of the attempt still asked or reading retired.
// The machine never merges: the merge step is the merger's verb. A merge card
// anywhere else, or a stream with no control card, is a planner refusal.
// Key: removed. Raises: "accepted by the machine" and, for a stream that was
// waiting, "stream started merging". Cost: O(r) a primary (its read cards are
// at most 15 and its reader rows r); the design's figure for the store's time
// is about 0.13 ms for one (measured, tracer 1).
func planAccept(s *Snapshot, keys []AgendaKey, now Now) RulePlan {
	rp := RulePlan{Done: reviewKeys(keys)}
	var p Plan
	readers := map[string]string{}
	for _, c := range reviewPrimaries(s) {
		if !AcceptDue(s, c) {
			continue
		}
		if s.StreamCtl(c.Row) == nil {
			p.refuse(c.ID, "stream "+c.Row+" has no merge row")
			continue
		}
		m := s.Merge.Card(c.ID)
		if m != nil && (!m.Placed() || m.Col != Returned) {
			p.refuse(c.ID, "its merge record is "+placeWord(m))
			continue
		}
		oks := reviewOKReaders(s, c)
		readers[c.ID] = strings.Join([]string{oks[0].F("reader"), oks[1].F("reader")}, ",")
		p.Units = append(p.Units, acceptUnit(s, c, oks, m, readers[c.ID], now))
	}
	p = Lawful(p)
	var accepted []reviewItem
	for _, u := range p.Units {
		if rd, ok := readers[u.Key]; ok {
			accepted = append(accepted, reviewItem{u.Key, u.Stream, fmt.Sprintf("%s (ok from %s)", u.Key, strings.Replace(rd, ",", " and ", 1))})
		}
	}
	var started []string
	for _, st := range unitStreams(p) {
		ctl := s.StreamCtl(st)
		if ctl.F("state") != StreamWaiting {
			reviewGuardStream(&p, st, ctl)
			continue
		}
		setStream(&p, s, st, map[string]string{"state": StreamMerging, "since": reviewWall(now), "due_mergeidle": reviewDue(now, DeadlineMergeIdle)})
		started = append(started, st)
	}
	rp.Plan = p
	reviewRefused(&rp, s, ruleAccept)
	rp.Notes = append(rp.Notes, reviewNotices(typeAccepted, accepted)...)
	for _, st := range started {
		rp.Notes = append(rp.Notes, NoteReq{Op: reviewKnow, Type: NStartedMerging, Subjects: []string{StreamSubject(st)}})
	}
	return rp
}

// acceptUnit is one primary's accept: the guards on its two ok reads, the
// retirement of its outstanding reads, its merge card queued and the primary
// moved into merging.
func acceptUnit(s *Snapshot, c *Card, oks []*Card, m *Card, readers string, now Now) Unit {
	u := Unit{Key: c.ID, Stream: c.Row}
	for _, o := range oks {
		u.Changes = append(u.Changes, change(Readers, guardEntry(o)))
	}
	retired := 0
	for _, rc := range reviewReads(s, c, c.Int("attempt")) {
		if rc.Placed() && (rc.Col == Asked || rc.Col == Reading) {
			u.Changes = append(u.Changes, change(Readers, removeEntry(rc, map[string]string{"retired": reviewWall(now), "retired_by": "accept"})))
			retired++
		}
	}
	if m != nil {
		e := moveEntry(m, c.Row, Queued, nil)
		score := c.Score
		e.Move.Score = &score
		u.Changes = append(u.Changes, change(Merge, e))
	} else {
		u.Changes = append(u.Changes, change(Merge, createEntry(c.ID, c.Row, Queued, c.Score,
			map[string]string{"kind": "merge", "primary": c.ID, "stream": c.Row})))
	}
	u.Changes = append(u.Changes, change(Work, moveEntry(c, c.Row, Merging, map[string]string{"readers": readers, "accepted": reviewWall(now)})))
	u.Moved = fmt.Sprintf("%s review -> merging queued (ok from %s)", c.ID, strings.Replace(readers, ",", ", ", 1))
	if retired > 0 {
		u.Moved += fmt.Sprintf("; %d outstanding read cards retired", retired)
	}
	return u
}

// reviewGuardStream guards a stream's control card at its revision in the
// first unit of the stream, when no unit changes it: an accept into a stream
// stopped since the read is refused (R9's guard).
func reviewGuardStream(p *Plan, stream string, ctl *Card) {
	for i := range p.Units {
		if p.Units[i].Stream == stream {
			p.Units[i].Changes = append(p.Units[i].Changes, change(Merge, guardEntry(ctl)))
			return
		}
	}
}

// R10 rework. Trigger: rework:<p> or rework@<seq> (work that came back failed,
// a read that found the work broken). Read: each primary, its read cards at
// its attempt and its work card, and the fleet. Guard: the primary at review
// with its revision, the failed work card and the broken read card at their
// places with their revisions, the receiving member up (memberup). Effect,
// for a primary below MaxAttempts: the fix is the failed work's report or the
// broken read's finding; avoid is the member of the attempt's work card;
// rereads is 0 (rereads count per attempt); the next attempt is dealt at once,
// to the up member with the shortest ready queue that is not avoid and has
// room, to avoid only when no other has room, and with no member able to take
// it the primary goes review -> ready with avoid, into again, for R6 to deal;
// the attempt's read cards are retired. At MaxAttempts: bound is set to
// BoundAttempts and the primary stays in review. Key: removed. Raises:
// "reworked by the machine", and at the bound "a card reached its bound".
// Cost: O(r + f) a primary.
func planRework(s *Snapshot, keys []AgendaKey, now Now) RulePlan {
	rp := RulePlan{Done: reviewKeys(keys)}
	var p Plan
	up, q := reviewMembers(s)
	dealt := map[string]string{}
	fixes := map[string]string{}
	bound := map[string]bool{}
	for _, c := range reviewPrimaries(s) {
		if !ReworkDue(s, c) {
			continue
		}
		if c.Int("attempt") >= MaxAttempts {
			p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.Row,
				Changes: []Change{change(Work, setEntry(c, map[string]string{"bound": BoundAttempts}))},
				Moved:   fmt.Sprintf("%s reached the attempts bound (attempt %d of %d)", c.ID, c.Int("attempt"), MaxAttempts)})
			bound[c.ID] = true
			continue
		}
		fix := reviewOwnFix(s, c)
		tag := "broken"
		if c.F("result") == "failed" {
			tag = "failed"
		}
		if fix == "" {
			fix = reviewFixWhenSilent[tag]
		}
		u, member, why := reworkUnit(s, c, fix, up, q, now)
		if why != "" {
			p.refuse(c.ID, why)
			continue
		}
		if wc := reviewWorkCard(s, c); wc != nil && wc.Placed() && wc.Col == DoneFailed && c.F("result") == "failed" {
			u.Changes = append(u.Changes, change(Fleet, guardEntry(wc)))
		}
		p.Units = append(p.Units, u)
		dealt[c.ID], fixes[c.ID] = member, fix
	}
	p = Lawful(p)
	var did, atBound []reviewItem
	var members []string
	for _, u := range p.Units {
		switch {
		case bound[u.Key]:
			atBound = append(atBound, reviewItem{u.Key, u.Stream, ""})
		case fixes[u.Key] != "":
			c := s.Work.Card(u.Key)
			did = append(did, reviewItem{u.Key, u.Stream, fmt.Sprintf("%s, attempt %d, because %s", u.Key, c.Int("attempt")+1, reviewCut(fixes[u.Key], reviewReasonBytes))})
			if m := dealt[u.Key]; m != "" && !contains(members, m) {
				members = append(members, m)
			}
		}
	}
	rp.Plan = p
	for _, m := range members {
		rp.Guards = append(rp.Guards, XGuard{Kind: guardMemberUp, Member: m})
	}
	reviewRefused(&rp, s, ruleRework)
	rp.Notes = append(rp.Notes, reviewNotices(typeReworked, did)...)
	if len(atBound) > 0 {
		var ids []string
		for _, it := range atBound {
			ids = append(ids, it.id)
		}
		text := fmt.Sprintf("%d primaries reached the attempts bound of %d; each one's history: nova-sprint log --card <primary>", len(ids), MaxAttempts)
		if len(ids) == 1 {
			text = fmt.Sprintf("%s: attempt %d, its bound, came back failed or was found broken; its history: nova-sprint log --card %s", ids[0], MaxAttempts, ids[0])
		}
		rp.Notes = append(rp.Notes, NoteReq{Op: reviewOpen, Type: NBound, Cause: reviewCauses[NBound], Subjects: ids, Text: text})
	}
	return rp
}

// reviewMembers is the up members and each one's ready queue, the queues a
// dealing reworked attempt joins.
func reviewMembers(s *Snapshot) ([]string, map[string]int) {
	if s.Fleet == nil {
		return nil, map[string]int{}
	}
	up := s.UpMembers()
	return up, readyQueues(s, up)
}

// reviewOwnFix is a primary's own fix: the findings of its broken reads at its
// attempt, else the report of its failed work; "" when it has neither.
func reviewOwnFix(s *Snapshot, c *Card) string {
	var found []string
	for _, rc := range reviewBroken(s, c) {
		if f := rc.F("finding"); f != "" && !contains(found, f) {
			found = append(found, f)
		}
	}
	if len(found) > 0 {
		return strings.Join(found, "; ")
	}
	if c.F("result") == "failed" {
		if wc := reviewWorkCard(s, c); wc != nil {
			return wc.F("report")
		}
	}
	return ""
}

// reviewMember is the member a reworked attempt goes to: the up member with the
// shortest ready queue among those that are not avoid and have room, avoid only
// when no other has room and it has, and "" when no member can take it.
func reviewMember(up []string, q map[string]int, avoid string) string {
	var others []string
	for _, m := range up {
		if m != avoid && q[m] < MaxReadyPerMember {
			others = append(others, m)
		}
	}
	switch {
	case len(others) > 0:
		return shortest(others, q)
	case avoid != "" && contains(up, avoid) && q[avoid] < MaxReadyPerMember:
		return avoid
	}
	return ""
}

// reworkUnit is the changes that send one primary back with a fix: the
// attempt's read cards retired (and the withdrawn work card, for a primary at
// its redeal bound), the fields of the next attempt set (fix, reworks,
// broken_reads, rereads 0, avoid, the readers it names kept, bound unset), and
// the next attempt's work card dealt to the member reviewMember picks, or the
// primary moved to ready with avoid when none can take it. It returns the
// member dealt to, "" when none, and a reason when it cannot be done.
func reworkUnit(s *Snapshot, c *Card, fix string, up []string, q map[string]int, now Now) (Unit, string, string) {
	attempt := c.Int("attempt")
	avoid := ""
	if wc := reviewWorkCard(s, c); wc != nil {
		if avoid = wc.F("member"); avoid == "" {
			avoid = wc.Row
		}
	}
	var retire []Change
	if wc := AtRedealBound(s, c); wc != nil {
		retire = append(retire, change(Fleet, removeEntry(wc, map[string]string{"retired": reviewWall(now), "retired_by": "rework"})))
	}
	broken := 0
	var readers []string
	for _, rc := range reviewReads(s, c, attempt) {
		if !contains(readers, rc.F("reader")) {
			readers = append(readers, rc.F("reader"))
		}
		if !rc.Placed() {
			continue
		}
		if rc.Col == Broken {
			broken++
		}
		retire = append(retire, change(Readers, removeEntry(rc, map[string]string{"retired": reviewWall(now), "retired_by": "rework"})))
	}
	// the readers kept are the pair the primary was asked of, so the fixed work
	// is asked of them again when it returns
	asked := c.F("asked")
	if asked == "" && len(readers) > 0 {
		asked = strings.Join(orderLike(s.Readers.Rows, readers, ""), ",")
	}
	set := map[string]string{"fix": fix, "reworks": itoa(c.Int("reworks") + 1), "broken_reads": itoa(c.Int("broken_reads") + broken), "rereads": "0"}
	unset := []string{"result", "readers", "bound"}
	if asked != "" {
		set["asked"] = asked
	}
	if avoid != "" {
		set["avoid"] = avoid
	} else {
		unset = append(unset, "avoid")
	}
	member := reviewMember(up, q, avoid)
	if member == "" {
		u := Unit{Key: c.ID, Stream: c.Row, Changes: append(retire, change(Work, moveEntry(c, c.Row, Ready, set, unset...)))}
		u.Moved = fmt.Sprintf("%s %s -> ready (rework, avoiding %s; no up member has room: the deal takes it)", c.ID, c.Col, orDash(avoid))
		u.Moved += fmt.Sprintf("; %d cards retired", len(retire))
		return u, "", ""
	}
	card := WorkCardID(c.ID, attempt+1)
	if s.Fleet.Card(card) != nil {
		return Unit{}, "", "work card " + card + " exists already"
	}
	q[member]++
	set["attempt"], set["work"] = itoa(attempt+1), card
	wall := reviewWall(now)
	fields := map[string]string{"kind": "work", "primary": c.ID, "stream": c.Row, "attempt": itoa(attempt + 1), "gen": "1", "member": member,
		"dealt": wall, "first_dealt": wall, "untaken_since": wall, "fix": fix,
		"untaken_r": strconv.FormatInt(now.R, 10), "due_untaken": reviewDue(now, DeadlineUntaken)}
	changes := append(retire, change(Fleet, createEntry(card, member, Ready, c.Score, fields)), change(Work, moveEntry(c, c.Row, Working, set, unset...)))
	u := Unit{Key: c.ID, Stream: c.Row, Changes: changes}
	u.Moved = fmt.Sprintf("%s %s -> working (rework, attempt %d) card=%s member=%s, avoiding %s; %d cards retired",
		c.ID, c.Col, attempt+1, card, member, orDash(avoid), len(retire))
	return u, member, ""
}

// reviewPool is the primaries a coordinator's rework may take: those in review
// the snapshot holds, and those in ready at their redeal bound, in work order.
func reviewPool(s *Snapshot) []*Card {
	if s == nil || s.Work == nil {
		return nil
	}
	var out []*Card
	for _, c := range s.Work.Cards {
		if reviewInReview(c) || c.Placed() && c.Col == Ready && AtRedealBound(s, c) != nil {
			out = append(out, c)
		}
	}
	SortCards(out)
	return out
}

// ReworkV21 is the coordinator's rework (2.3 R10 "by the coordinator", section
// 3): each named primary, or each of a stream, is sent back with the fix and
// its next attempt is delegated at once. It is R10's move with the
// coordinator's differences: it is accepted for a primary in review whatever
// its evidence and at whatever attempt (the attempts bound is the machine's,
// and the primary's bound is unset), and for one in ready at its redeal bound,
// whose withdrawn work card it retires; with no fix given, each primary's own
// is the finding of its broken read or the report of its failed work, and a
// primary with neither is refused by name. It plans at the snapshot's time
// taken as R and wall (ReworkAt has the clocks given).
func ReworkV21(s *Snapshot, r ReworkReq) Plan { return ReworkAt(s, r, reviewNowOf(s)) }

// ReworkAt is ReworkV21 with the clocks it plans at given: the due times of
// the work card it deals are in R.
func ReworkAt(s *Snapshot, r ReworkReq, now Now) Plan {
	var p Plan
	chosen := pick(&p, r.Sel, reviewPool(s), rowOf, func(c *Card) string {
		if c.Placed() && c.Col == Merging {
			return "merging: return it first: nova-sprint return " + c.ID
		}
		if AtRedealBound(s, c) != nil {
			return ""
		}
		return inState(c, Review)
	}, s.primaryCard)
	up, q := reviewMembers(s)
	for _, c := range chosen {
		// A primary the rework refuses stays in review: the judgment it needs is
		// written, if it has none.
		stays := func() {
			if j, ok := reviewJudgment(s, c, reviewStep{who: r.Who}); ok {
				p.Notes = append(p.Notes, j)
			}
		}
		fix := r.Fix
		if fix == "" {
			if fix = reviewOwnFix(s, c); fix == "" {
				p.refuse(c.ID, "no --fix, and no finding of a broken read or report of failed work to take as its fix; give --fix <text>")
				stays()
				continue
			}
		}
		u, _, why := reworkUnit(s, c, fix, up, q, now)
		if why != "" {
			p.refuse(c.ID, why)
			stays()
			continue
		}
		u.Closes = closesFor(s.Open, ReworkResolves, c.ID)
		p.Units = append(p.Units, u)
	}
	answered(&p, s, r.Answers, r.Who)
	return Lawful(p)
}

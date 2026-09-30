package sprint

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The fleet rules of the event-driven tick (the upper design, version 2.1,
// section 2.3): R1 seen, R2 down, R6 deal and R7 level. Each is a Rule of 8.0:
// a read that sizes what it asks of the store from the keys it was given, and
// a plan that decides, on what the read returned, every effect the rule has
// on the tables, the guards its step carries, the notes it asks J for and
// what becomes of its keys. Each plans once a tick over all the keys it has
// (T4) and is idempotent (1.3.3, E7): a rule run again on the same keys with
// nothing changed in between writes nothing and raises nothing, so a key
// delivered twice is harmless. A plan that a limit cut (R2's chunk, R6's room, a
// queue longer than R7's read) does what it read and leaves its key, and the
// next plan, on the state the first left, does the rest. Nothing here reads a
// store or a clock: the snapshot, the facts about the sprint's own keys and the
// two clocks are given.
//
// A plan is made on a snapshot loaded from its own read (LoadPartial): it
// reads only the cells, counts, rows, fields and sprint keys the read named, and
// a plan that reads more is refused in a release build and panics in a test. So
// each read below asks for every field its plan reads or unsets, every queue
// length a plan takes is a count the read asked for (Table.Count), never the
// length of the cards a head happened to return, and every sprint key a plan
// consults is a range its read asked for (factsOf). A plan that cannot see
// whether it has work, because its read could not name what to read, says so
// (unread) and keeps its key: it never removes a key it has not looked behind.
//
// Where the design gives a rule more than the shapes of 8.0 can carry, the
// rule carries it the narrowest way and the open question is in the pull
// request:
//
//	the count guard (over the receivers' ready cells) and the sorted set guard
//	of R6 (`S.zguard(sent:s, rcount, -inf, max, atmost 0)`): RulePlan has no
//	field for either, so each is an XGuard of kind count or rcount (guardCount,
//	guardRCount): a count guard's Key is the cell ("m1:ready", as layer 1
//	names a cell) and its Score the most the cell held when it was read; an
//	rcount guard's Key is the index and the score at or below which it may
//	hold no member ("sent:s1 20.5": no sentinel of s1 at or below 20.5).
//	a planner refusal (R6): the rule sets `refused` on the card and asks J for
//	"the machine could not move a card" in its own step, as 1.3.5 says, and
//	does not put the card in Plan.Refused.
//	the decisions of a judgment: the request names none; J takes them from
//	IT06's table for the type.
//	the sprint's own keys (the beat entries of the due set, {p}strangers, the
//	dropping marks): ReadPlan has no query kind for a member of a hash or one
//	entry's score, so each is read as a range over the key named (IT05's RangeQ
//	in its Key form) that answers the members present (factBeats, factStrangers,
//	factDropping); the store's side is IT30's (question 3).
//	the names R6 and R7 read by (the streams, the members): Rule.Read is given
//	neither, so the registered reads cannot name a stream's front(s) or a
//	member's ready head. They read what they can (the fleet, the dropping marks,
//	for R6 the list of streams), and their plans refuse, loudly, what that does
//	not show (unread); dealReadFor and levelReadFor take a fleetShape and are
//	the reads the tick will make when it has the names (question 4).

// The rules of the keys of this file. ruleDown, ruleDeal and ruleLevel are
// ingest's words (ingest.go); R1's key is made by the beat part, not by ingest.
const ruleSeen = "seen"

// RuleMaxRedeals is how many times the fleet rules deal one attempt's work
// card again after a take that ended without a finish, whether its member went
// down (R2) or the card was late (R11): the design's five (2.3 R2 and R11).
// It is the rules' own number. Today's machine keeps MaxRedeals (three),
// which the scanning tick and the reference model still pin: the two meet at
// the switch (IT23), and the pull request lists what differs.
const RuleMaxRedeals = 5

// The design's numbers that today's machine also has, each the rules' own
// constant so that a change to today's machine changes nothing here until the
// switch (IT23) makes it on purpose.
const (
	// RuleUntakenDeadline is how long a work card dealt at R may wait to be taken:
	// it is due, not taken, at R + 15 min (1.2).
	RuleUntakenDeadline = 15 * time.Minute
	// RuleBeatDeadline is how long of running time a member stays up after its last
	// beat (1.4.4: its beat entry is at R + 15 s).
	RuleBeatDeadline = 15 * time.Second
)

// The numbers of the fleet rules, each with the section that gives it.
const (
	// fleetChunk is the most cards of a member that R2 deals again or
	// withdraws in one plan: layer 1's chunk of changed members (1.0, "The
	// chunk"), which R2's read names as "the first 2,000".
	fleetChunk = 2000
	// dealMaxL is the most cards one stream gives R6 in a round (2.3 R6, as
	// errata 3 amendment 9 has it: L = min(room, the step bound,
	// ⌊10,000 / 3s⌋), the room the sum over the members of each one's width
	// less its ready and working cards, the step bound TickMaxDeal).
	dealMaxL = TickMaxDeal
	// levelHeadLimit is the most ready cards of a member R7 reads a pass (2.3
	// R7: two): a queue longer than the read keeps R7's key, and the next pass
	// reads its head again.
	levelHeadLimit = 2
	// seenRecords is what one key of R1 may return: the member's control card.
	seenRecords = 1
	// R6's read of one stream (2.3 R6): front(s)'s σ record, and three heads of
	// L records: fresh:s below σ, again:s and again:s's withdrawn work cards.
	dealSigmaRecords = 1
	dealHeads        = 3
)

// followPrimary is the follow R2's read takes from a work card to its primary
// (2.3 R2: "through related with each card's primary"). 1.0's list has none
// from a card to its primary (work goes the other way): open question 5.
const followPrimary = "primary"

// The fleet rules, as rows: the name and the two functions. The place in the
// round robin (1.4.2) is IT05's table (PriorityOf), taken where the rows are
// registered so that a change to the order is a changed row there.
var fleetRuleRows = []struct {
	name    string
	read    func(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey)
	plan    func(s *Snapshot, keys []AgendaKey, now Now) RulePlan
	readFor func(keys []AgendaKey, sh TickShape, b ReadBounds, halvings int) (ReadPlan, []AgendaKey)
}{
	{ruleSeen, readSeen, planSeen, nil},
	{ruleDown, readDown, planDown, nil},
	{ruleDeal, readDeal, planDeal, readDealFor},
	{ruleLevel, readLevel, planLevel, nil},
}

func init() {
	for _, r := range fleetRuleRows {
		p, ok := PriorityOf(r.name)
		if !ok {
			panic("sprint: the fleet rule " + r.name + " has no priority in RulePriorities")
		}
		RegisterRule(Rule{Name: r.name, Priority: p, Read: r.read, Plan: r.plan, ReadFor: r.readFor})
	}
}

// The kinds of XGuard the fleet rules carry (8.0's memberup, beatstale and
// stranger, and the two that guard a count: see above), and the names of the
// sprint's own keys and subjects they mention.
const (
	guardMemberUp  = "memberup"
	guardBeatStale = "beatstale"
	guardStranger  = "stranger"
	guardCount     = "count"
	guardRCount    = "rcount"
	// strangersKey is {p}strangers, the hash of machines beating with no fleet
	// row (1.3.1); beatKeyPrefix begins beat:<member> in the due set (1.2).
	strangersKey  = "strangers"
	beatKeyPrefix = "beat:"
	// sprintSubject is the subject of a judgment about the sprint (jopen:sprint
	// in R1's read); the verbs name it as SubjectSprint.
	sprintSubject = SubjectSprint
	// boundRedeals is the value of a primary's bound field, and the cause of the
	// judgment, when its work card has been dealt again as often as the design
	// allows (1.3.1, 2.2).
	boundRedeals = "redeals"
	// causeCouldNot and causeNoMember are the causes the rules give J's one per
	// cause (1.3.4): the rule that raises each.
	causeCouldNot = ruleDeal
	causeNoMember = CauseNoMember
)

// SubjectSprint is the subject of a judgment about the sprint, and
// CauseNoMember the cause "no fleet member is up" is kept under: R6 raises it
// with its rule's name, deal (2.2, 1.3.4); fleet up closes it.
const (
	SubjectSprint = "sprint"
	CauseNoMember = ruleDeal
)

// The card fields the rules write beyond the ones the lifecycle's own steps
// write (1.2, 1.3.1): the running-time stamps of the untaken and unfinished
// clocks and their due fields.
const (
	fleetFieldUntakenR      = "untaken_r"
	fleetFieldDueUntaken    = "due_untaken"
	fleetFieldFirstTakenR   = "first_taken_r"
	fleetFieldDueUnfinished = "due_unfinished"
)

// The projections of the reads: every read names its fields, so none fetches
// a whole record (1.0, "Bytes"), and each names every field its plan reads or
// unsets (an unset of a field the record was read without is dropped, as an
// unset of an absent field is). A query has one projection for all it returns,
// so where a query returns cards of two kinds (a work card and its primary,
// a primary and its withdrawn card) the list is both kinds'.
var (
	// The member's control card: status and its hold (isHeld). A fleet member's
	// `since` is written and never read.
	memberReadFields = []string{"status", "held"}
	// The members R2 reads: status, hold, and the width (R2's redeal goes to a
	// receiver below its width, width.go, errata 3 amendment 9).
	downMemberFields = []string{"status", "held", FieldWidth}
	// The members R6 and R7 read: the status (UpMembers) and the width (width.go,
	// errata 3 amendment 9); they never ask whether a member is held.
	upReadFields = []string{"status", FieldWidth}
	// R2's work cards and their primaries: the stream (a dropping stream is
	// skipped), the generation and the clocks nextGen, redealUnit and
	// withdrawUnit read or unset, the count of redeals, the primary and, on the
	// primary, the work card it names (withdrawUnit moves it only when it names
	// this card, and unsets the name) and its kind (Lawful asks whether a primary
	// it moves is a sentinel).
	downReadFields = []string{"stream", "primary", "gen", "redeals", "untaken_since", "taken", "dealt",
		fleetFieldFirstTakenR, fleetFieldDueUnfinished, "work", "kind"}
	// R6's primaries (kind, attempt, avoid, bound, refused, the fix a new work
	// card carries, the result a deal clears) and their withdrawn work cards (the
	// generation and the untaken clock, and the withdrawn mark a redeal unsets).
	dealReadFields = []string{"kind", "attempt", "avoid", "bound", "refused", "fix", "result",
		"gen", "untaken_since", fleetFieldUntakenR, "withdrawn"}
	// R7's work cards: the stream, and the generation and untaken_since of nextGen.
	levelReadFields = []string{"stream", "gen", "untaken_since"}
)

// fleetShape is what R6's and R7's reads name and Rule.Read is not given (8.0's
// Read has the keys, the bounds and the halvings, and no snapshot): the streams
// R6 asks front(s) of, and the members R7 asks the ready head of. It is the
// tick's TickShape: R6 registers ReadFor (readDealFor), which the tick calls with
// the streams its first read found (the work table's rows), so that R6's read
// names every stream's front (2.3 R6 "Read:"). The registered Read has no
// names: it reads what it can without one (the fleet, the dropping marks, the
// list of streams), and a plan made on it refuses what that leaves unseen
// (unread; the tick refuses the plan by name). R7's read has no ReadFor yet
// (open question 4).
type fleetShape = TickShape

// units is the members the fleet query is over: one for each member the shape
// names, and 0, which is the design's most (MaxMembers), when it names none: a
// read that does not name its members reads them all.
func (sh fleetShape) units() int { return len(sh.Members) }

// fleetFacts is what the fleet rules read of the sprint's own keys, beside the
// four tables. A name that is not in a map was not read, or is not there.
type fleetFacts struct {
	// BeatDue is the score of beat:<member> in the due set, in running time: a
	// member with no entry is absent.
	BeatDue map[string]int64
	// Strangers says {p}strangers holds the member and has marked it noticed.
	Strangers map[string]bool
	// Dropping says the stream is being dropped or removed ({p}dropping@e).
	Dropping map[string]bool
}

// The sprint's own keys the fleet rules read (1.3.1, 1.4.4). IT05's ReadPlan has
// no query kind for one member of a hash or for one entry's score, and the errata
// (E4) leave sprint-key reads to kinds the store registers: until it does, each
// key is asked as a range over the key named (RangeQ in its Key form), and its
// answer is the members present, with for a beat entry its score:
//
//	factBeats      the beat:<m> entries of the due set, each with its due time in
//	               running ms (the tick pops what is due before it reads, so an
//	               entry still there is above R)
//	factStrangers  the machines {p}strangers holds as noticed
//	factDropping   the streams {p}dropping@e holds (being dropped or removed)
const (
	factBeats     = "beat"
	factStrangers = "strangers"
	factDropping  = "dropping"
)

// factLimit is the most members a fact's range names: the most members (for the
// beats and strangers) or streams of a sprint (0.1, F1-20). A range that says it
// has more has not read them all, and the plan refuses (factsOf).
var factLimit = map[string]int{factBeats: MaxMembers, factStrangers: MaxMembers, factDropping: MaxStreams}

// ruleFacts are the sprint keys each rule's plan consults. The rule's read asks
// for exactly these (withFacts), and its plan takes exactly these from its
// snapshot (factsOf): the one table is both.
var ruleFacts = map[string][]string{
	ruleSeen:  {factStrangers},
	ruleDown:  {factBeats, factDropping},
	ruleDeal:  {factDropping},
	ruleLevel: {factDropping},
}

// withFacts is the read plan with the ranges of the sprint keys the rule's plan
// consults.
func withFacts(rp ReadPlan, rule string) ReadPlan {
	rp.Ranges = slices.Clone(rp.Ranges)
	for _, k := range ruleFacts[rule] {
		rp.Ranges = append(rp.Ranges, RangeQ{Key: k, Limit: factLimit[k]})
	}
	return rp
}

// unloadedKeyMessage is the refusal of a plan that consulted a sprint key its read
// did not ask for, or asked for and could not hold.
const unloadedKeyMessage = "the planner read a sprint key its plan did not load"

// unread records in the snapshot's log a read the plan needed and its read did
// not make: a panic in a test build, and in a release build a read the tick
// refuses the plan for (Snapshot.UnloadedErr). A snapshot built whole has
// nothing unread.
func unread(s *Snapshot, what string) {
	if s != nil && s.Partial != nil {
		s.Partial.log.note(what)
	}
}

// whenShort is the plan of a read that was short: a plan that read what its
// read did not load (Snapshot.Unloaded) saw less than its state holds, so it
// writes nothing and raises nothing, and every key of the rule stays. The tick
// refuses such a plan (Snapshot.UnloadedErr) and reads again; what the plan
// decided on a short read is never a key's end.
func whenShort(s *Snapshot, rule string, keys []AgendaKey, rp RulePlan) RulePlan {
	if len(s.Unloaded()) == 0 {
		return rp
	}
	var out RulePlan
	for _, k := range keys {
		if keyRule(k) == rule {
			out.Requeue = append(out.Requeue, k)
		}
	}
	return out
}

// sprintKeyRange is the answer of the range the plan asked over the sprint key.
func sprintKeyRange(p *Partial, key string) (TsetAnswer, bool) {
	for i, q := range p.Plan.Ranges {
		if q.Key != key {
			continue
		}
		for j, sl := range p.Plan.TsetSlots() {
			if sl.Kind == AnswerRange && sl.Index == i {
				return p.Answer.Tset[j], true
			}
		}
	}
	return TsetAnswer{}, false
}

// factsOf is the facts the rule's plan consults, taken from the answers of the
// ranges its read asked (ruleFacts): the plan takes them from the snapshot it
// was given and from nothing else. A fact the read did not ask for, one whose
// answer is cut (HasMore: the members beyond it were not read, so a member not in
// it is not known to be absent), or a beat with no score, is unread. A snapshot
// built whole holds no sprint key and gives none.
func factsOf(s *Snapshot, rule string) fleetFacts {
	f := fleetFacts{BeatDue: map[string]int64{}, Strangers: map[string]bool{}, Dropping: map[string]bool{}}
	if s.Partial == nil {
		return f
	}
	for _, kind := range ruleFacts[rule] {
		a, ok := sprintKeyRange(s.Partial, kind)
		switch {
		case !ok:
			unread(s, unloadedKeyMessage+": "+kind)
			continue
		case a.HasMore:
			unread(s, unloadedKeyMessage+": "+kind+" holds more than the read did")
			continue
		case kind == factBeats && len(a.Scores) != len(a.IDs):
			unread(s, unloadedKeyMessage+": "+kind+" without its scores")
			continue
		}
		for i, id := range a.IDs {
			switch kind {
			case factBeats:
				if m, isBeat := strings.CutPrefix(id, beatKeyPrefix); isBeat {
					f.BeatDue[m] = int64(a.Scores[i])
				}
			case factStrangers:
				f.Strangers[id] = true
			case factDropping:
				f.Dropping[id] = true
			}
		}
	}
	return f
}

// keyRule is the rule a key belongs to.
func keyRule(k AgendaKey) string { return RuleOf(k.Key) }

// keySubject is what a key names after its rule: "m1" for "down:m1", and ""
// for a key of the sprint ("deal").
func keySubject(k AgendaKey) string {
	_, sub, _ := strings.Cut(k.Key, ":")
	return sub
}

// fleetKeySet is the keys of one rule, by the subject each names, each subject
// once, in the order the keys came.
type fleetKeySet struct {
	subjects []string
	key      map[string]AgendaKey
	// junk are keys of the rule that name no subject: there is nothing to do
	// for one, and the plan removes it.
	junk []AgendaKey
}

// fleetKeysOf is the keys of the rule that name a subject (a member), and
// leaves out the keys of other rules.
func fleetKeysOf(rule string, keys []AgendaKey) fleetKeySet {
	ks := fleetKeySet{key: map[string]AgendaKey{}}
	for _, k := range keys {
		if keyRule(k) != rule {
			continue
		}
		sub := keySubject(k)
		if sub == "" {
			ks.junk = append(ks.junk, k)
			continue
		}
		if _, dup := ks.key[sub]; dup {
			continue
		}
		ks.key[sub] = k
		ks.subjects = append(ks.subjects, sub)
	}
	return ks
}

// sprintKey is the first key of a rule whose key is the sprint's own (deal,
// level): a second is the same key.
func sprintKey(rule string, keys []AgendaKey) (AgendaKey, bool) {
	for _, k := range keys {
		if keyRule(k) == rule {
			return k, true
		}
	}
	return AgendaKey{}, false
}

// What a plan does with a key it was given.
const (
	fateDone    = iota // finished: the step removes it
	fateRequeue        // made progress and has more: it stays, with its order
	fateHeld           // its only work was in dropping streams: it stays, held back (1.3.5)
)

func (rp *RulePlan) settle(k AgendaKey, fate int) {
	switch fate {
	case fateRequeue:
		rp.Requeue = append(rp.Requeue, k)
	case fateHeld:
		rp.HeldBack = append(rp.HeldBack, k)
	default:
		rp.Done = append(rp.Done, k)
	}
}

// fleetShare cuts the keys of a rule to what one read carries (1.4.2, "Reads
// never cost a fourth round trip"): as many as fit the read's records, less the
// fixed records the read holds beside its keys, at cost records a key, at
// least one; and after each halving (1.3.5) half as many, down to one key, and
// then half the limit, down to one. It returns the keys the read carries, the
// keys left for the next tick, and the limit.
func fleetShare(keys []AgendaKey, limit, fixed int, cost func(limit int) int, b ReadBounds, halvings int) (kept, left []AgendaKey, lim int) {
	lim = limit
	if len(keys) == 0 {
		return nil, nil, lim
	}
	keep := 1
	if b.Records > 0 {
		keep = max(1, (b.Records-fixed)/max(1, cost(lim)))
	}
	keep = min(keep, len(keys))
	for i := 0; i < halvings; i++ {
		switch {
		case keep > 1:
			keep = (keep + 1) / 2
		case lim > 1:
			lim = (lim + 1) / 2
		}
	}
	return keys[:keep], keys[keep:], lim
}

// isHeld says the member's control card is held down by the coordinator (fleet
// down): its status is held, or its record carries the hold beside a status
// of down.
func isHeld(ctl *Card) bool {
	return ctl.F("status") == Held || ctl.F("held") != ""
}

// wallOf is the wall clock of a plan as a time, for the stamps the readers
// keep (no rule reads them, 1.3.1).
func wallOf(now Now) time.Time { return time.UnixMilli(now.Wall).UTC() }

// fleetMs is a running-time stamp as a field's value.
func fleetMs(v int64) string { return strconv.FormatInt(v, 10) }

// untakenDue is when a card dealt at R is due, not taken: R + 15 min (1.2).
func untakenDue(r int64) int64 { return r + RuleUntakenDeadline.Milliseconds() }

// listed is the subjects of a note as text, at most MaxListed of them.
func listed(ids []string) string {
	if len(ids) <= MaxListed {
		return strings.Join(ids, ", ")
	}
	return strings.Join(ids[:MaxListed], ", ") + fmt.Sprintf(" and %d more", len(ids)-MaxListed)
}

// cellName is a cell as layer 1 names it, "<row>:<col>" (the reverse of cellRef).
func cellName(row, col string) string { return row + ":" + col }

func memberUpGuard(member string) XGuard { return XGuard{Kind: guardMemberUp, Member: member} }

// beatStaleGuard is X's check that beat:<m> is not in the due set above R, with
// the score the plan read it at (0 when the entry was popped).
func beatStaleGuard(member string, f fleetFacts) XGuard {
	return XGuard{Kind: guardBeatStale, Member: member, Key: beatKeyPrefix + member, Score: f.BeatDue[member]}
}

// countGuard is a member's ready cell held at most as many cards as the read
// saw (L1's count entry, one entry over all the cells the builder makes).
func countGuard(member string, most int) XGuard {
	return XGuard{Kind: guardCount, Member: member, Key: cellName(member, Ready), Score: int64(most)}
}

// sentGuard is S.zguard(sent:<stream>, rcount, -inf, most, atmost 0): no
// sentinel of the stream at or below the highest score the plan places.
func sentGuard(stream string, most float64) XGuard {
	return XGuard{Kind: guardRCount, Key: "sent:" + stream + " " + fmtScore(most)}
}

// R1 seen.
//
// Trigger: the pop of seen:<m>, which the beat part enters when m's status is
// down (not held) or m has no fleet row (once, by HSETNX on {p}strangers).
// Read: m's control card and {p}strangers[m]. (The design also lists m's beat
// record and jopen:sprint: the effect uses neither, since J closes the
// judgment or ends its hold from its own read of jopen.)
// Guard: the control card at its place and revision, so that it is down as
// read; for a stranger, XGUARD that {p}strangers[m] is not yet noticed.
// Effect: status up, and "no fleet member is up" closed (or its hold ended).
// A stranger: the notice "an unknown machine is beating", and
// {p}strangers[m] marked noticed, so that a second delivery writes nothing.
// A member that is up, or held, needs nothing: a held member is released only
// by fleet up, and a beat never undoes fleet down.
// Key: removed. Its line queues deal and level (2.1), which is not this
// step's to write (Q5).
// Cost: O(1) a member, about 100 µs of store time (2.3).
// Without it: every member's beat read every tick, O(f) a tick while RUNNING.
func planSeen(s *Snapshot, keys []AgendaKey, now Now) RulePlan {
	return whenShort(s, ruleSeen, keys, planSeenWith(s, keys, now, factsOf(s, ruleSeen)))
}

func planSeenWith(s *Snapshot, keys []AgendaKey, now Now, f fleetFacts) RulePlan {
	var rp RulePlan
	ks := fleetKeysOf(ruleSeen, keys)
	rp.Done = append(rp.Done, ks.junk...)
	wall := wallOf(now)
	var came []string
	for _, m := range ks.subjects {
		ctl := s.MemberCtl(m)
		switch {
		case ctl == nil:
			// no fleet row: a machine that beats and is nobody's, said once
			if f.Strangers[m] {
				break
			}
			rp.Guards = append(rp.Guards, XGuard{Kind: guardStranger, Member: m, Key: strangersKey})
			rp.Notes = append(rp.Notes, NoteReq{Op: "know", Type: NUnknownMachine, Subjects: []string{m},
				Text: fmt.Sprintf("an unknown machine is beating: %s; add it with fleet up %s", m, m)})
		case ctl.F("status") == Down && !isHeld(ctl):
			rp.Plan.Units = append(rp.Plan.Units, Unit{Key: ctl.ID,
				Changes: []Change{change(Fleet, setEntry(ctl, map[string]string{"status": Up, "since": stamp(wall)}))},
				Moved:   m + " up: it beats"})
			rp.Notes = append(rp.Notes, NoteReq{Op: "know", Type: NMemberUp, Subjects: []string{m}, Text: m + " up: it beats"})
			came = append(came, m)
		}
		rp.settle(ks.key[m], fateDone)
	}
	if len(came) > 0 {
		rp.Notes = append(rp.Notes, NoteReq{Op: "close", Type: NNoMember, Cause: causeNoMember, Subjects: []string{sprintSubject},
			Text: "a member is up: " + listed(came)})
	}
	rp.Plan = Lawful(rp.Plan)
	return rp
}

// readSeen is R1's read (8.0): each member's control card, for as many keys as
// one read holds, and the machines {p}strangers holds as noticed (a fact,
// factStrangers). The design also lists the member's beat record and
// jopen:sprint; the effect uses neither (J closes the judgment, or ends its
// hold, from its own read of jopen).
func readSeen(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	kept, left, _ := fleetShare(keys, 1, 0, func(int) int { return seenRecords }, b, halvings)
	ks := fleetKeysOf(ruleSeen, kept)
	if len(ks.subjects) == 0 {
		return ReadPlan{}, left
	}
	ids := make([]string, 0, len(ks.subjects))
	for _, m := range ks.subjects {
		ids = append(ids, CtlID(m))
	}
	return withFacts(ReadPlan{Sprint: []SprintQ{
		{Kind: QueryRelated, Table: Fleet, Source: IDSource{Kind: SourceIDs, IDs: ids}, Fields: memberReadFields},
	}}, ruleSeen), left
}

// R2 down.
//
// Trigger: the pop of beat:<m> (no beat for 15 s of running time), or the line
// of fleet down m (which holds m).
// Read: the fleet (every member's control card, and the counts of each member's
// cells: the receivers' ready counts and m's own) and the first 2,000 of m's
// ready and working cells by score, each with its primary; beat:<m>'s score in
// the due set is a fact.
// Guard: m's control card at its place and revision, whenever the step moves
// m's cards (the entry that marks m down, or, for a held member or one an
// earlier chunk marked, an entry that only guards it); each card at its place
// and revision; one count entry over the receivers' ready cells, each at most
// what was read; each receiver's status up (memberup); and, when the key came
// from the pop and m is not held, no beat:<m> entry above R (beatstale): a
// beat since moved it to R + 15 s and refuses the step XGUARD.
// Effect: first chunk: m up, not held, and its beat fresh (a beat raced the
// pop): nothing, and the key goes. Otherwise m's status goes down, unless m
// is held: a held member stays held, and only fleet up releases it. Each card
// is dealt again to the next member round the fleet (round.go, errata 3
// amendment 5: from the deal's rolling index, the first up below its width,
// width.go, errata 3 amendment 9, else the first up; the index moved past it and written with the step, guarded on the
// value read), at generation + 1. A card that was ready keeps untaken_r and redeals: it was
// never taken, so its redeal does not count. A card that was working ended a
// take without a finish, and its redeal counts: below RuleMaxRedeals it is
// dealt again with redeals + 1, first_taken_r unset and untaken_r = R; at
// RuleMaxRedeals it goes to withdrawn instead, and its primary working -> ready
// with bound = redeals (out of again). With no member up the cards go to
// withdrawn as one set and their primaries working -> ready as one set (into
// again); a working card's count is raised at its withdrawal, and its later
// redeal from withdrawn does not count again.
// A card of a stream being dropped is left as it is (X refuses DROPPING), and
// the key stays.
// Key: requeued while either cell still holds a card (the cells' counts say, not
// the cards the read loaded); removed when both are empty.
// Raises: "fleet member down: m, k redealt, j withdrawn" (the step that marks
// it, with that step's counts) and "cards returned to ready because no member
// is up"; "a card reached its bound" naming the primaries at the bound.
// Cost: O(f + k) a chunk; a member with 5 cards about 1 ms (2.3).
// Without it: a down member's cards found by scanning the fleet table.
func planDown(s *Snapshot, keys []AgendaKey, now Now) RulePlan {
	return whenShort(s, ruleDown, keys, planDownWith(s, keys, now, factsOf(s, ruleDown)))
}

func planDownWith(s *Snapshot, keys []AgendaKey, now Now, f fleetFacts) RulePlan {
	var rp RulePlan
	ks := fleetKeysOf(ruleDown, keys)
	rp.Done = append(rp.Done, ks.junk...)
	wall := wallOf(now)

	// Which members this step marks down: up, not held, and no fresh beat. A
	// member whose beat raced the pop stays a receiver.
	marking := map[string]bool{}
	for _, m := range ks.subjects {
		ctl := s.MemberCtl(m)
		if ctl != nil && ctl.F("status") == Up && !isHeld(ctl) && !fleetBeatFresh(f, m, now.R) {
			marking[m] = true
		}
	}
	var receivers []string
	for _, m := range s.UpMembers() {
		if !marking[m] {
			receivers = append(receivers, m)
		}
	}
	read := readyQueues(s, receivers)
	// the room of each receiver is its width (width.go, errata 3 amendment 9):
	// its work cards held, ready and working, under it
	q, widths := memberLoads(s, receivers), memberWidths(s, receivers)
	used := map[string]bool{}
	rr := dealRound(s)
	moves := roundMoves{}
	var withdrawnNoMember, atBound []string
	heads := headCells(s.Fleet, Ready, Working)

	for _, m := range ks.subjects {
		k := ks.key[m]
		ctl := s.MemberCtl(m)
		if ctl == nil {
			rp.settle(k, fateDone) // no such member: nothing is dealt from it
			continue
		}
		if ctl.F("status") == Up && !isHeld(ctl) && !marking[m] {
			rp.settle(k, fateDone) // its beat is fresh: a beat raced the pop
			continue
		}

		// the first chunk of its cards by score: the heads the read loaded of its
		// two cells, at most the chunk of them; more cards than that, by the cells'
		// counts, leave the key queued
		cards := append(append([]*Card(nil), heads[[2]string{m, Ready}]...), heads[[2]string{m, Working}]...)
		SortCards(cards)
		if len(cards) > fleetChunk {
			cards = cards[:fleetChunk]
		}
		cut := s.Fleet.Count(m, Ready)+s.Fleet.Count(m, Working) > len(cards)

		var units []Unit
		redealt, withdrawn, skipped := 0, 0, 0
		for _, c := range cards {
			if f.Dropping[c.F("stream")] {
				skipped++
				continue
			}
			switch {
			case c.Col == Working && c.Int("redeals") >= RuleMaxRedeals:
				units = append(units, withdrawUnit(s, c, m, now, wall, true))
				if pr := c.F("primary"); pr != "" {
					atBound = append(atBound, pr)
				}
				withdrawn++
			case len(receivers) > 0:
				to := rr.next(receivers, q, widths, "", true)
				rr.moved(to)
				moves[c.ID] = to
				q[to]++
				used[to] = true
				units = append(units, redealUnit(c, m, to, now, wall))
				redealt++
			default:
				units = append(units, withdrawUnit(s, c, m, now, wall, false))
				if pr := c.F("primary"); pr != "" {
					withdrawnNoMember = append(withdrawnNoMember, pr)
				}
				withdrawn++
			}
		}

		// m's control card is guarded by its revision whenever the step moves its
		// cards (2.3 R2: "m's control card with its revision"): a step that writes
		// the card (marking it down) guards it by that entry, and one that does not
		// (a held member, or one an earlier chunk marked) carries a guard alone, so
		// that a fleet up between the read and the apply refuses the step and no card
		// of a member that is up again is dealt away.
		//
		// A member that is not held is one whose key came from the pop of its beat
		// (a held member's key comes from the line of fleet down), and a step that
		// moves the cards of such a member carries beatstale whether this step marks
		// it down or an earlier chunk did: a beat that lands before the step applies
		// moves beat:<m> to R + 15 s and refuses it, and the next plan finds the beat
		// fresh (2.3 R2).
		switch {
		case marking[m]:
			rp.Plan.Units = append(rp.Plan.Units, Unit{Key: ctl.ID,
				Changes: []Change{change(Fleet, setEntry(ctl, map[string]string{"status": Down, "since": stamp(wall)}))},
				Moved:   fmt.Sprintf("%s down: no beat for %s", m, RuleBeatDeadline)})
			rp.Guards = append(rp.Guards, beatStaleGuard(m, f))
		case len(units) > 0:
			rp.Plan.Units = append(rp.Plan.Units, Unit{Key: ctl.ID,
				Changes: []Change{change(Fleet, guardEntry(ctl))},
				Moved:   m + " control card guarded: its cards are dealt away"})
			if !isHeld(ctl) {
				rp.Guards = append(rp.Guards, beatStaleGuard(m, f))
			}
		}
		rp.Plan.Units = append(rp.Plan.Units, units...)
		if marking[m] {
			rp.Notes = append(rp.Notes, NoteReq{Op: "know", Type: NMemberDown, Subjects: []string{m},
				Text: fmt.Sprintf("%s down: no beat for %s; %d redealt, %d withdrawn", m, RuleBeatDeadline, redealt, withdrawn)})
		}
		done := redealt + withdrawn
		switch {
		case skipped > 0 && done == 0:
			rp.settle(k, fateHeld)
		case skipped > 0 || cut:
			rp.settle(k, fateRequeue)
		default:
			rp.settle(k, fateDone)
		}
	}

	if len(withdrawnNoMember) > 0 {
		rp.Notes = append(rp.Notes, NoteReq{Op: "know", Type: NWithdrawn, Subjects: withdrawnNoMember,
			Text: "no fleet member is up: " + listed(withdrawnNoMember) + " went back to ready"})
	}
	if len(atBound) > 0 {
		rp.Notes = append(rp.Notes, NoteReq{Op: "open", Type: NBound, Cause: boundRedeals, Subjects: atBound,
			Text: fmt.Sprintf("an attempt's work card was dealt again %d times after a take that did not finish, its bound, and is not dealt again", RuleMaxRedeals)})
	}
	for _, m := range receivers {
		if used[m] {
			rp.Guards = append(rp.Guards, memberUpGuard(m), countGuard(m, read[m]))
		}
	}
	rp.Plan.on(s)
	rp.Plan = Lawful(rp.Plan)
	roundWrites(&rp.Plan, rr, moves)
	return rp
}

// headCells are the cards a snapshot holds at the cells of the columns, by
// (row, column), each cell in score order, in one pass over the table's loaded
// cards (Table.LoadedCards: the records that came, never a scan of the table). On
// a snapshot loaded from a read plan they are the heads the plan read of each
// cell (Table.Cell refuses a cell that was not read whole, and a head is not
// one), and on a snapshot built whole they are the cells. The number of cards a
// cell holds is Table.Count's.
func headCells(t *Table, cols ...string) map[[2]string][]*Card {
	out := map[[2]string][]*Card{}
	for _, c := range t.LoadedCards() {
		if !c.Placed() || !contains(cols, c.Col) {
			continue
		}
		k := [2]string{c.Row, c.Col}
		out[k] = append(out[k], c)
	}
	for _, cs := range out {
		SortCards(cs)
	}
	return out
}

// fleetBeatFresh says beat:<m> lies above R in the due set: a member's beat is
// fresh in running time, never by the wall stamp of its beat record (1.4.4).
func fleetBeatFresh(f fleetFacts, m string, r int64) bool {
	score, ok := f.BeatDue[m]
	return ok && score > r
}

// redealUnit deals a card of a member that went down to the up member to. A
// card that was ready keeps everything but its place and generation; a card
// that was working ended a take without a finish: its count is raised, its
// unfinished clock ends, and the untaken clock starts a new span at R (1.2).
func redealUnit(c *Card, from, to string, now Now, wall time.Time) Unit {
	set := nextGen(c, to, wall)
	var unset []string
	if c.Col == Working {
		set["redeals"] = itoa(c.Int("redeals") + 1)
		set[fleetFieldUntakenR] = fleetMs(now.R)
		set[fleetFieldDueUntaken] = fleetMs(untakenDue(now.R))
		unset = append(unset, "taken", fleetFieldFirstTakenR, fleetFieldDueUnfinished)
	}
	return Unit{Key: c.ID, Stream: c.F("stream"),
		Changes: []Change{change(Fleet, moveEntry(c, to, Ready, set, unset...))},
		Moved:   fmt.Sprintf("%s %s:%s -> %s:ready gen=%d; %s down", c.ID, c.Row, c.Col, to, c.Int("gen")+1, from)}
}

// withdrawUnit takes a card of a member that went down off the member, to
// withdrawn, and its primary from working back to ready. Not at its bound, a
// working card's count is raised and the untaken clock starts a new span, as
// in redealUnit; at its bound the count stays and the primary is marked
// bound = redeals, which takes it out of again.
func withdrawUnit(s *Snapshot, c *Card, from string, now Now, wall time.Time, atBound bool) Unit {
	set := nextGen(c, "", wall)
	set["withdrawn"] = stamp(wall)
	unset := []string{"taken", "dealt"}
	if c.Col == Working && !atBound {
		set["redeals"] = itoa(c.Int("redeals") + 1)
		set[fleetFieldUntakenR] = fleetMs(now.R)
		set[fleetFieldDueUntaken] = fleetMs(untakenDue(now.R))
		unset = append(unset, fleetFieldFirstTakenR, fleetFieldDueUnfinished)
	}
	u := Unit{Key: c.ID, Stream: c.F("stream"),
		Changes: []Change{change(Fleet, moveEntry(c, c.Row, Withdrawn, set, unset...))},
		Moved:   fmt.Sprintf("%s withdrawn gen=%d; %s down", c.ID, c.Int("gen")+1, from)}
	if pr := s.Work.Placed(c.F("primary")); pr != nil && pr.Col == Working && pr.F("work") == c.ID {
		var pset map[string]string
		if atBound {
			pset = map[string]string{"bound": boundRedeals}
		}
		u.Changes = append(u.Changes, change(Work, moveEntry(pr, pr.Row, Ready, pset, "work")))
		u.Moved += "; " + pr.ID + " working -> ready"
	}
	return u
}

// fleetQuery is the fleet query the rules read the members by (1.0): every
// member's control card, the rows of the fleet table and the counts of each
// member's cells (a receiver's ready count is one of them).
func fleetQuery(fields []string) SprintQ { return SprintQ{Kind: QueryFleet, Fields: fields} }

// downQueries are R2's queries for one member at a limit: the heads of its ready
// and working cells, each card with its primary (2.3 R2).
func downQueries(m string, limit int) []SprintQ {
	qs := make([]SprintQ, 0, 2)
	for _, cell := range []string{Ready, Working} {
		qs = append(qs, SprintQ{Kind: QueryRelated, Table: Fleet,
			Source: IDSource{Kind: SourceHead, Key: cellName(m, cell), Limit: limit},
			Fields: downReadFields, Follow: []string{followPrimary}})
	}
	return qs
}

// recordsOf is the records the queries may return.
func recordsOf(qs ...SprintQ) int {
	n := 0
	for _, q := range qs {
		n += QueryCost(q).Records
	}
	return n
}

// readDown is R2's read (8.0): for as many keys as one read holds, the fleet
// (every member's control card, the rows and the counts of the cells: the
// receivers and each member's own cells) and the heads of each member's ready
// and working cells with their primaries, and the beat entries and the dropping
// marks (the facts factBeats and factDropping).
func readDown(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	fleet := fleetQuery(downMemberFields)
	// the fleet with the deal's rolling index (round.go): a card dealt again
	// goes round the fleet and moves it
	fleet.Props = []string{PropDealIndex}
	kept, left, lim := fleetShare(keys, fleetChunk, recordsOf(fleet),
		func(limit int) int { return recordsOf(downQueries("m", limit)...) }, b, halvings)
	ks := fleetKeysOf(ruleDown, kept)
	if len(ks.subjects) == 0 {
		return ReadPlan{}, left
	}
	rp := ReadPlan{Sprint: []SprintQ{fleet}}
	for _, m := range ks.subjects {
		rp.Sprint = append(rp.Sprint, downQueries(m, lim)...)
	}
	return withFacts(rp, ruleDown), left
}

// R6 deal.
//
// Trigger: cards enter ready; a ready card's attempt, bound or refused
// changes; room frees; a member up; a sentinel line; the owner of "no fleet
// member is up"; start.
// Read: the fleet (the members' control cards with their widths and their
// cells' counts: room is the sum over the members of each one's width less its
// ready and working counts, errata 3 amendment 9); for each stream, front(s)
// for σ_s and the head of fresh:s below σ_s and of again:s, each up to
// L = min(room, TickMaxDeal, ⌊10,000 / 3s⌋), with the heads' records and, for
// again:s, their withdrawn work cards. The plan's candidates are those heads,
// not a cell of the table.
// Plan: the room first of what was read (every card the fleet has room for, up
// to TickMaxDeal, in the one plan) in stream turns (dealTurns: one card
// from each stream's front in turn, in stream order, each stream's cards in
// work order), each dealt: a new work
// card to the next member round the fleet (round.go, errata 3 amendment 5:
// the rolling index, scanned from in name order, wrapping, to the first
// member up and below its width, and moved past it, so the fleet fills round
// by round, one card a member a turn; the fleet table's deal_index, read
// with the fleet and written with the deal), the
// primary's avoid member only when no other up member has room; or the
// withdrawn card dealt again at generation + 1 (no change to redeals: its take, if any, was counted
// when it was withdrawn). untaken_r = R on a first deal since the last take.
// Guard: each primary at ready with its revision; each withdrawn card at
// withdrawn with its revision; each new work card absent; a count entry over
// the receiving ready cells, each at most what was read; every receiver up
// (memberup); for each stream with fresh cards dealt,
// S.zguard(sent:s, rcount, -inf, the highest fresh score dealt, atmost 0).
// Effect: as planned. A primary the planner refuses (its work card's id is
// taken by a record the read loaded: the read cannot ask for an id it does not
// know, and a card it did not load is refused by the guard, EXISTS) gets
// refused = "deal: <why>" and its judgment "the machine could not move a card". No member up and some card dealable
// (fresh:s below σ_s, or again:s not empty): "no fleet member is up", once
// (J's one per cause; a hold on it keeps it closed).
// A card of a stream being dropped is not dealt, and the key stays (1.3.5).
// A stream the read listed and named no front of (the registered read names
// none) is one the plan cannot look behind: where a card could be dealt it says
// the front was not read (unread), and the key stays.
// Key: requeued while it dealt or refused, room remains and a stream's head
// was read to L (the read may have been cut at L: dealCut); removed otherwise
// (errata 3 amendment 9: at a width of 64 room nearly always remains, and a
// read that was not cut leaves nothing for a second pass).
// Raises: "no fleet member is up"; "the machine could not move a card".
// Cost: O(f + i + k), i at most 2·s·L. A deal of 16 about 1 ms of store time
// (2.3). A stream gives at most L a round; one with more left keeps the key.
// The turns are the owner's word on the design's per-stream fronts: streams
// are worked in parallel, never one stream's backlog before another's first
// card (the body's "room lowest (score, id)" over every stream is superseded).
// Without it: deal scans ready in every stream.
func planDeal(s *Snapshot, keys []AgendaKey, now Now) RulePlan {
	return whenShort(s, ruleDeal, keys, planDealWith(s, keys, now, factsOf(s, ruleDeal)))
}

func planDealWith(s *Snapshot, keys []AgendaKey, now Now, f fleetFacts) RulePlan {
	var rp RulePlan
	key, ok := sprintKey(ruleDeal, keys)
	if !ok {
		return rp
	}
	wall := wallOf(now)
	up := s.UpMembers()
	// each member's cards held (ready and working) against its width; the
	// count guard is over the ready cell, at what the read saw of it
	q, widths := memberLoads(s, up), memberWidths(s, up)
	read := readyQueues(s, up)
	room := min(widthRoom(s, up), TickMaxDeal)

	// What can be dealt: the primaries of fresh below σ and of again, of the
	// streams that are not dropping, in work order. They are the heads the read
	// took of each stream's index (front(s)), not a cell of the table. A stream
	// the read listed and named no front of is one the plan cannot look behind:
	// where a card could be dealt (a member has room, or none is up and a card
	// would make the judgment) the plan says the front was not read.
	streams, unseen := dealStreams(s)
	if room > 0 || len(up) == 0 {
		for _, st := range unseen {
			unread(s, unloadedMessage+": front of "+st)
		}
	}
	ready := headCells(s.Work, Ready)
	var cands []*Card
	skipped := 0
	for _, st := range streams {
		sigma := math.Inf(1)
		if g := FirstSentinel(s, st); g != nil {
			sigma = g.Score
		}
		for _, c := range ready[[2]string{st, Ready}] {
			if !dealable(c, sigma) {
				continue
			}
			if f.Dropping[st] {
				skipped++
				continue
			}
			cands = append(cands, c)
		}
	}
	cands = dealTurns(cands, streams)

	if len(up) == 0 {
		if len(cands) > 0 {
			rp.Notes = append(rp.Notes, NoteReq{Op: "open", Type: NNoMember, Cause: causeNoMember, Subjects: []string{sprintSubject},
				Text: fmt.Sprintf("%d primaries wait to be dealt and no member is up: start nova-sprint fleet beat <member> on a machine, or release a hold with nova-sprint fleet up <member>", len(cands))})
		}
		rp.settle(key, dealFate(0, skipped, 0, false))
		return rp
	}

	used := map[string]bool{}
	freshMost := map[string]float64{}
	dealt, refused := 0, 0
	rr := dealRound(s)
	moves := roundMoves{}
	for _, c := range cands {
		if dealt >= room {
			break
		}
		attempt := c.Int("attempt")
		var u Unit
		if wc := s.Fleet.Placed(WorkCardID(c.ID, attempt)); wc != nil && wc.Col == Withdrawn {
			m := rr.member(up, q, widths, "")
			if m == "" {
				break
			}
			q[m]++
			used[m] = true
			rr.moved(m)
			moves[c.ID] = m
			u = dealAgainUnit(c, wc, m, now, wall)
		} else {
			card := WorkCardID(c.ID, attempt+1)
			if s.Fleet.Card(card) != nil {
				why := "work card " + card + " exists already"
				rp.Plan.Units = append(rp.Plan.Units, Unit{Key: c.ID, Stream: c.Row,
					Changes: []Change{change(Work, setEntry(c, map[string]string{"refused": ruleDeal + ": " + why}))},
					Moved:   c.ID + " refused: " + why})
				rp.Notes = append(rp.Notes, NoteReq{Op: "open", Type: typeCouldNotMove, Cause: causeCouldNot, Subjects: []string{c.ID},
					Text: c.ID + ": " + why})
				refused++
				continue
			}
			m := rr.member(up, q, widths, c.F("avoid"))
			if m == "" {
				break
			}
			q[m]++
			used[m] = true
			rr.moved(m)
			moves[c.ID] = m
			u = dealNewUnit(c, card, m, now, wall)
		}
		if attempt == 0 {
			if cur, seen := freshMost[c.Row]; !seen || c.Score > cur {
				freshMost[c.Row] = c.Score
			}
		}
		rp.Plan.Units = append(rp.Plan.Units, u)
		dealt++
	}

	for _, m := range up {
		if used[m] {
			rp.Guards = append(rp.Guards, memberUpGuard(m), countGuard(m, read[m]))
		}
	}
	for _, st := range streams {
		if most, ok := freshMost[st]; ok {
			rp.Guards = append(rp.Guards, sentGuard(st, most))
		}
	}
	rp.settle(key, dealFate(dealt+refused, skipped, room-dealt, dealCut(s, streams, ready)))
	rp.Plan.on(s)
	rp.Plan = Lawful(rp.Plan)
	roundWrites(&rp.Plan, rr, moves)
	return rp
}

// dealFate is what becomes of the deal key: held back when its only work was
// in dropping streams, kept while it made progress and room remains and a
// stream's head was read to its limit (cut: the stream may hold more than the
// read returned), or work was skipped for a drop; removed otherwise. At a
// width of 64 room nearly always remains after a deal, so the key is kept only
// when the read may have been cut (errata 3 amendment 9): a deal that took
// every card its read could hold has nothing left for a second pass.
func dealFate(progress, skipped, roomLeft int, cut bool) int {
	switch {
	case progress == 0 && skipped > 0:
		return fateHeld
	case progress > 0 && (roomLeft > 0 && cut || skipped > 0):
		return fateRequeue
	}
	return fateDone
}

// dealCut says a stream's front was read to its heads' limit: the ready
// primaries the read loaded of the stream are at least the limit of one of
// its heads, so the stream may hold more. A snapshot built whole is never cut.
func dealCut(s *Snapshot, streams []string, ready map[[2]string][]*Card) bool {
	if s.Partial == nil {
		return false
	}
	for _, q := range s.Partial.Plan.Sprint {
		if q.Kind != QueryFront || !slices.Contains(streams, q.Stream) {
			continue
		}
		for _, h := range q.Heads {
			if h.Limit > 0 && len(ready[[2]string{q.Stream, Ready}]) >= h.Limit {
				return true
			}
		}
	}
	return false
}

// dealTurns is the order R6 and T3 deal in (2.3 R6, front(s) per stream): one
// card from each stream in turn, in the order of streams, until every stream's
// cards are taken; a stream with none left is skipped, and within a stream the
// cards go in work order (SortCards). A card of a stream not in streams takes
// its turn after them, its streams in name order. So every stream with a
// ready card is worked in parallel: a deal of k cards over n streams gives
// each stream k/n, give or take one, never one stream's backlog first. The
// model is tla/SprintEvents.tla: TurnSorted is this order (one card from each
// stream's front in turn, a stream with none skipped, within a stream by work
// order), PlanDeal takes the room from it, and DealTakesTurns states it from
// the counts of a plan; the witness W28 (MCSprintEventsW28.cfg) is the order of
// the whole table. Errata 3, amendment 4.
func dealTurns(cards []*Card, streams []string) []*Card {
	by := map[string][]*Card{}
	order := append([]string(nil), streams...)
	known := map[string]bool{}
	for _, st := range streams {
		known[st] = true
	}
	var extra []string
	for _, c := range cards {
		if !known[c.Row] {
			known[c.Row] = true
			extra = append(extra, c.Row)
		}
		by[c.Row] = append(by[c.Row], c)
	}
	slices.Sort(extra)
	order = append(order, extra...)
	for _, st := range order {
		SortCards(by[st])
	}
	out := make([]*Card, 0, len(cards))
	for turn := 0; len(out) < len(cards); turn++ {
		for _, st := range order {
			if turn < len(by[st]) {
				out = append(out, by[st][turn])
			}
		}
	}
	return out
}

// dealable says a ready primary is in fresh below σ or in again (1.3.1): not a
// sentinel, not refused, and never dealt and below the first sentinel, or
// dealt before and not at a bound.
func dealable(c *Card, sigma float64) bool {
	if IsSentinel(c) || c.F("refused") != "" {
		return false
	}
	if c.Int("attempt") == 0 {
		return c.Score < sigma
	}
	return c.F("bound") == ""
}

// dealNewUnit cuts the primary's next attempt's work card into the ready
// queue of member m at generation 1, with the untaken clock started at R, and
// moves the primary to working.
func dealNewUnit(c *Card, card, m string, now Now, wall time.Time) Unit {
	attempt := c.Int("attempt") + 1
	fields := map[string]string{"kind": "work", "primary": c.ID, "stream": c.Row, "attempt": itoa(attempt), "gen": "1", "member": m,
		"dealt": stamp(wall), "first_dealt": stamp(wall), "untaken_since": stamp(wall),
		fleetFieldUntakenR: fleetMs(now.R), fleetFieldDueUntaken: fleetMs(untakenDue(now.R))}
	if fix := c.F("fix"); fix != "" {
		fields["fix"] = fix
	}
	set := map[string]string{"attempt": itoa(attempt), "work": card}
	return Unit{Key: c.ID, Stream: c.Row,
		Changes: []Change{
			change(Fleet, createEntry(card, m, Ready, c.Score, fields)),
			change(Work, moveEntry(c, c.Row, Working, set, "result")),
		},
		Moved: fmt.Sprintf("%s %s -> working card=%s member=%s", c.ID, c.Col, card, m)}
}

// dealAgainUnit deals a withdrawn work card again into the ready queue of
// member m at a new generation, and moves its primary to working on it. Its
// count of redeals is not changed: a take that ended was counted when it was
// withdrawn. The untaken clock starts only when it has none: a redeal does not
// move it (1.2).
func dealAgainUnit(c, wc *Card, m string, now Now, wall time.Time) Unit {
	set := nextGen(wc, m, wall)
	if wc.F(fleetFieldUntakenR) == "" {
		set[fleetFieldUntakenR] = fleetMs(now.R)
		set[fleetFieldDueUntaken] = fleetMs(untakenDue(now.R))
	}
	return Unit{Key: c.ID, Stream: c.Row,
		Changes: []Change{
			change(Fleet, moveEntry(wc, m, Ready, set, "withdrawn")),
			change(Work, moveEntry(c, c.Row, Working, map[string]string{"work": wc.ID}, "result")),
		},
		Moved: fmt.Sprintf("%s %s -> working card=%s member=%s gen=%d (dealt again)", c.ID, c.Col, wc.ID, m, wc.Int("gen")+1)}
}

// dealLimit is R6's L (2.3, errata 3 amendment 9): min(room, TickMaxDeal,
// ⌊10,000 / 3s⌋), taken against the
// records the answer may hold so that it fits: the fleet's members, and for
// each stream σ's record and three heads of L cards (fresh, again, and again's
// withdrawn cards). At least 1. A room or a stream count of zero is not known.
func dealLimit(room, streams, members, records int) int {
	l := dealMaxL
	if room > 0 {
		l = min(l, room)
	}
	if streams > 0 {
		l = min(l, ((records-members)/streams-dealSigmaRecords)/dealHeads)
	}
	return max(1, l)
}

// dealRecords is the most records R6's read may return for a limit: the
// fleet's members and, for each stream, σ's record and three heads.
func dealRecords(streams, members, limit int) int {
	return members + streams*(dealSigmaRecords+dealHeads*limit)
}

// dealQueries are R6's queries for one stream at a limit L: front(s) with the
// head of fresh:s below σ and the head of again:s, each up to L, and for again:s
// the withdrawn work card of each head (2.3 R6).
func dealQueries(stream string, limit int) []SprintQ {
	return []SprintQ{{Kind: QueryFront, Stream: stream, Fields: dealReadFields, Heads: []HeadQ{
		{Index: HeadFreshBelow, Limit: limit},
		{Index: HeadAgain, Limit: limit, Follow: []string{FollowWithdrawn}},
	}}}
}

// dealReadFor is R6's read of the shape's streams (8.0): the fleet, and front(s)
// of every stream at the limit L = min(room, TickMaxDeal, ⌊10,000 / 3s⌋) with the
// records of the answer fitting the read's bounds (dealLimit; the room is not
// known to a read, so L is the design's for the fullest fleet), and after each
// halving half of L, down to one (1.3.5). The plan takes only the room lowest
// of what it reads.
func dealReadFor(sh fleetShape, b ReadBounds, halvings int) ReadPlan {
	records := b.Records
	if records <= 0 {
		records = MaxReadRecords
	}
	lim := Halved(dealLimit(0, len(sh.Streams), len(sh.Members), records), halvings)
	rp := ReadPlan{Sprint: []SprintQ{fleetQuery(upReadFields)}}
	// the fleet with the deal's rolling index (round.go)
	rp.Sprint[0].Props = []string{PropDealIndex}
	rp.Sprint[0].Units = sh.units()
	for _, st := range sh.Streams {
		rp.Sprint = append(rp.Sprint, dealQueries(st, lim)...)
	}
	if len(sh.Streams) == 0 {
		// a read that names no stream lists them: the plan can then tell a sprint
		// with none (nothing to deal) from a read that could not name them
		rp.Sprint = append(rp.Sprint, SprintQ{Kind: QueryStreams, Fields: []string{}})
	}
	return withFacts(rp, ruleDeal)
}

// readDeal is R6's registered read (8.0). Rule.Read is given neither the streams
// nor the room, so it can name no stream's front(s): it asks for the fleet, the
// list of streams and the dropping marks, and a plan made on it deals nothing it
// cannot see. Where a card could be dealt it says the fronts were not read and
// keeps its key; with no room, or no stream, there is nothing to deal and the key
// goes. The tick that knows the streams reads with dealReadFor (open question 4).
func readDeal(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	return readDealFor(keys, TickShape{}, b, halvings)
}

// readDealFor is R6's read given the tick's shape (Rule.ReadFor; 2.3 R6
// "Read:"): the fleet and front(s) of each stream the tick's first read found,
// so that the plan sees every stream's front. A shape with no stream lists the
// streams instead (dealReadFor): a sprint with none has nothing to deal, and a
// stream made after the tick's first read is one the plan says it did not see
// (unread), which the tick refuses by name.
func readDealFor(keys []AgendaKey, sh TickShape, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	if _, ok := sprintKey(ruleDeal, keys); !ok {
		return ReadPlan{}, keys
	}
	return dealReadFor(TickShape{Streams: sh.Streams}, b, halvings), nil
}

// dealStreams are the streams a deal plans over, in name order: the ones whose
// front(s) the read asked for, or on a snapshot built whole the sprint's. A read
// that named no front lists the streams instead (dealReadFor), so that it knows
// whether the sprint has any: they come back as unseen, each a stream whose front
// was not read. A read that did neither is refused (Table.Rows).
func dealStreams(s *Snapshot) (streams, unseen []string) {
	if s.Partial == nil {
		streams = append(streams, s.Work.Rows()...)
		slices.Sort(streams)
		return streams, nil
	}
	for st := range s.Partial.Fronts {
		streams = append(streams, st)
	}
	if len(streams) == 0 {
		unseen = append(unseen, s.Work.Rows()...)
	}
	slices.Sort(streams)
	slices.Sort(unseen)
	return streams, unseen
}

// R7 level.
//
// Trigger: a member up; start.
// Read: the fleet (the members' control cards with their widths and their cells'
// counts) and the head of each member's ready cell (at most levelHeadLimit
// each).
// Guard: each moved card at its place and revision; a count entry on the
// members whose queues change, each at most what was read; every receiver up
// (memberup).
// Effect: while the longest and the shortest queues (by the cells' counts) differ
// by more than one, the newest card of the longest that the read loaded moves to
// the next member round the fleet below its width and below the mean
// (round.levelTo, errata 3 amendment 5: from the deal's rolling index, the
// fleet table's deal_index, the first up below its width whose queue is below
// the up members' mean rounded down, else the first at it; the index moved
// past it and written with the step, guarded on the value read; a member at
// its width takes no more, errata 3 amendment 9; the shortest queue is the
// shortest of the members below their width) at generation + 1. A ready card, not taken: untaken_r and redeals
// are unchanged. A card of a stream being dropped is not moved (X refuses
// DROPPING), and the key stays. R2 puts no cap on a receiver's queue, so a
// queue can hold more than the two cards the read loads: the plan moves what it
// loaded, and while queues still differ by more than one after that, the key
// stays and the next plan reads the heads again (open question 11). A queue
// whose count says it holds cards and of which the read loaded none (the
// registered read names no head) is one the plan cannot move a card of: it says
// the head was not read (unread), and the key stays.
// Key: removed.
// Raises: nothing (its line is the record).
// Cost: O(f + moved).
// Without it: levelling reads every member's cells whenever anything changes.
func planLevel(s *Snapshot, keys []AgendaKey, now Now) RulePlan {
	return whenShort(s, ruleLevel, keys, planLevelWith(s, keys, now, factsOf(s, ruleLevel)))
}

func planLevelWith(s *Snapshot, keys []AgendaKey, now Now, f fleetFacts) RulePlan {
	var rp RulePlan
	key, ok := sprintKey(ruleLevel, keys)
	if !ok {
		return rp
	}
	wall := wallOf(now)
	up := s.UpMembers()
	// The lengths are the cells' counts, whatever the read loaded of them; the
	// cards a queue can give are the ones the read loaded, oldest first.
	queues := readyQueues(s, up)
	read := map[string]int{}
	for m, n := range queues {
		read[m] = n
	}
	held, widths := memberLoads(s, up), memberWidths(s, up)
	heads := headCells(s.Fleet, Ready)
	cards, loaded := map[string][]*Card{}, map[string]int{}
	for _, m := range up {
		cards[m] = append([]*Card(nil), heads[[2]string{m, Ready}]...)
		loaded[m] = len(cards[m])
	}
	moved, skipped, unreached := 0, 0, false
	receivers, touched := map[string]bool{}, map[string]bool{}
	rr := dealRound(s)
	moves := roundMoves{}
	for len(up) > 1 {
		long, short := up[0], ""
		for _, m := range up {
			if queues[m] > queues[long] {
				long = m
			}
			if held[m] < widths[m] && (short == "" || queues[m] < queues[short]) {
				short = m
			}
		}
		if short == "" || queues[long]-queues[short] <= 1 {
			break
		}
		// the newest card of the longest queue that the read loaded and a drop has
		// not frozen
		i := len(cards[long]) - 1
		for i >= 0 && f.Dropping[cards[long][i].F("stream")] {
			i--
			skipped++
		}
		if i < 0 {
			if loaded[long] == 0 {
				// the count says this queue holds cards and the read loaded none: it
				// named no head of the cell, and the plan cannot tell level from not
				unread(s, unloadedMessage+": "+cellName(long, Ready)+" head")
			}
			unreached = true // the queue is longer than the cards the read loaded of it
			break
		}
		to := rr.levelTo(up, queues, held, widths)
		if to == "" {
			break
		}
		c := cards[long][i]
		cards[long] = append(cards[long][:i:i], cards[long][i+1:]...)
		queues[long]--
		queues[to]++
		held[long]--
		held[to]++
		moves[c.ID] = to
		rp.Plan.Units = append(rp.Plan.Units, Unit{Key: c.ID, Stream: c.F("stream"),
			Changes: []Change{change(Fleet, moveEntry(c, to, Ready, nextGen(c, to, wall)))},
			Moved:   fmt.Sprintf("%s %s:ready -> %s:ready gen=%d", c.ID, long, to, c.Int("gen")+1)})
		receivers[to], touched[long], touched[to] = true, true, true
		moved++
	}
	for _, m := range up {
		if receivers[m] {
			rp.Guards = append(rp.Guards, memberUpGuard(m))
		}
	}
	for _, m := range up {
		if touched[m] {
			rp.Guards = append(rp.Guards, countGuard(m, read[m]))
		}
	}
	switch {
	case skipped > 0 && moved == 0:
		rp.settle(key, fateHeld)
	case skipped > 0, unreached:
		// work skipped for a drop is never forgotten, and queues that are still
		// uneven after the cards the read loaded were moved (or when it loaded none)
		// are not level: the key stays and the next plan reads again
		rp.settle(key, fateRequeue)
	default:
		rp.settle(key, fateDone)
	}
	rp.Plan = Lawful(rp.Plan)
	roundWrites(&rp.Plan, rr, moves)
	return rp
}

// levelReadFor is R7's read of the shape's members (8.0): the fleet, and the
// head of each member's ready cell, at most levelHeadLimit cards each.
func levelReadFor(sh fleetShape) ReadPlan {
	rp := ReadPlan{Sprint: []SprintQ{fleetQuery(upReadFields)}}
	// the fleet with the deal's rolling index (round.go): a levelled card goes
	// round the fleet and moves it
	rp.Sprint[0].Props = []string{PropDealIndex}
	rp.Sprint[0].Units = sh.units()
	for _, m := range sh.Members {
		rp.Sprint = append(rp.Sprint, SprintQ{Kind: QueryRelated, Table: Fleet,
			Source: IDSource{Kind: SourceHead, Key: cellName(m, Ready), Limit: levelHeadLimit},
			Fields: levelReadFields})
	}
	return withFacts(rp, ruleLevel)
}

// readLevel is R7's registered read (8.0). Rule.Read is given no members, so it
// can name no member's ready head: it asks for the fleet, whose counts say whether
// the queues are level, and the dropping marks. A plan made on it removes its key
// when the queues are level, and when they are not says the heads were not read and
// keeps it. The tick that knows the members reads with levelReadFor (open
// question 4).
func readLevel(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	if _, ok := sprintKey(ruleLevel, keys); !ok {
		return ReadPlan{}, keys
	}
	return levelReadFor(fleetShape{}), nil
}

package sprint

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// The time rules of the event-driven tick (the upper design, version 2.1,
// section 2.3, item IT10 of 8.1): R11 late (with idle and the cut clock), R12
// overdue, R13 hold, R14 remind (phase 1, the claim), R18 behind, and the
// look of R17 while the machine is STOPPED, and the judgment that names a
// step the store refused (1.3.5), which parks or halves the key.
//
// Every rule is a pure function of a partial snapshot (IT05), the keys it was
// given and Now. Time comes only from Now (R for running time, Wall for wall
// time): nothing here reads a clock. Every rule is idempotent (1.3.3, E7): it
// decides from the state it read, its moves and creates carry the guards of
// the cards it read, and a rule run a second time on the same keys with
// nothing changed asks for nothing that changes anything. Each rule's Read asks
// for everything its Plan reads and no more: on the partial snapshot a read of
// what was not asked panics in a test and is refused in a release build.
//
// Each rule names its cost below. A plan is O(1) a key, and O(f) or O(r) once a
// plan for the members or readers a replacement chooses among (a heap: the
// choice is O(log f), O(15 + log r) a key).
//
// The writes to the sprint's own keys that a plan carries beside its cards
// (R14's move of its due entry and its claim on the goal record, R18's re-arm,
// R17's clock fields and the park of 1.3.5) are RulePlan.Sprint (TimeWrites).
// The sprint's own reads beyond the four tables (jnote, the goal records, the
// cut entries, the tick hash, the dropping marks) are answered in
// Answer.Time (TimeAnswer), by the sprint-key read kinds that the errata's
// addendum (from the cold read of layer 1 revision 4) says the registry
// includes beside the eight composite kinds: it is not closed at them.
//
// What the design leaves open, and the narrower reading taken (also listed in
// the pull request):
//
//   - No `count` guard on a receiving cell (R11): RulePlan and the entries of
//     Plan cannot carry one, so a replacement is guarded by the card's place
//     and revision and by X's memberup, not by the receiving queue's length.
//   - A guard "no entry above R" (R12's, R14's, R18's and the cut clock's) is
//     an XGuard of kind due whose Score is the highest score the entry may
//     have at apply: the pop has removed the entry, and a later part that
//     re-armed it has moved it above.
//   - R11 acts on a card in the cell of its kind only (a work card not taken
//     is in ready at a member): a card that moved on is quiet, and one
//     withdrawn is the deal's (R6).
//   - R11 does not read jopen: J's one per cause makes a second request for an
//     open judgment write nothing (1.3.4).

// The numbers of the time rules, each with its section. They are the design's
// own, named here for these rules: today's machine keeps its own (MaxRedeals
// is 3 in steps_tick.go), and each difference is a question for the switch
// (IT23) that the pull request lists. How many times a taken card that is late
// is replaced (2.3 R11, unfinished) is RuleMaxRedeals, the bound R2 counts
// against: it is the fleet rules' (rules_fleet.go, IT07), and R11 uses it.
const (
	// RuleMaxRereads is how many more readers a late read may be given at one
	// attempt (2.3 R11, unbegun and unreported).
	RuleMaxRereads = 3
	// RuleMaxReadCards is the read cards a primary may ever have (3, ask
	// --another; F1-26).
	RuleMaxReadCards = 15
	// RuleDeadlineUntaken is how long a work card dealt may wait to be taken
	// (1.2, untaken), and the replacement's own span.
	RuleDeadlineUntaken = 15 * time.Minute
	// RuleDeadlineUnfinished is how long a work card taken may go unfinished
	// (1.2, unfinished).
	RuleDeadlineUnfinished = 2 * time.Hour
	// RuleDeadlineUnbegun is how long a read card asked may go unbegun (1.2,
	// unbegun), and a replacement's own span.
	RuleDeadlineUnbegun = 30 * time.Minute
	// RuleDeadlineMergeIdle is how long a stream merging may go with no merge
	// step (1.2, mergeidle).
	RuleDeadlineMergeIdle = 30 * time.Minute
	// RuleRemindEvery is the running time between two pushes to one person
	// (1.2, remind).
	RuleRemindEvery = 5 * time.Minute
	// BehindSpan is the running time from when the backlog is first not zero
	// to the judgment that the machine is falling behind (1.2, R18).
	BehindSpan = 5 * time.Minute
	// StoppedDueSpan is the wall time from when moves became due to the
	// judgment that the machine is STOPPED and they are (2.3 R17).
	StoppedDueSpan = 10 * time.Minute
	// ruleIdleSpan is how long a stream with open cards may land nothing before
	// the owner is told (2.5): provisional so the build can run, the owner's to
	// set (7).
	ruleIdleSpan = 2 * time.Hour
)

// The card fields the time rules read and write (1.2, 1.3.1): every stamp and
// every due time is in running time, in milliseconds.
const (
	fieldDueUntaken      = "due_untaken"
	fieldDueUnfinished   = "due_unfinished"
	fieldDueUnbegun      = "due_unbegun"
	fieldDueUnreported   = "due_unreported"
	fieldDueMergeIdle    = "due_mergeidle"
	fieldDueIdle         = "due_idle"
	fieldUntakenR        = "untaken_r"
	fieldFirstTakenR     = "first_taken_r"
	fieldUntakenReplaced = "untaken_replaced"
	fieldAskedR          = "asked_r"
)

// The projections of the reads of the time rules: no rule read fetches a whole
// record (1.0, bytes). Each names what the rule's plan reads of the record, and
// the fields it unsets (an unset of a field the read did not name is dropped by
// unsetPresent as absent).
var (
	workCardFields = []string{"stream", "primary", "attempt", "gen", "member", "redeals",
		fieldDueUntaken, fieldDueUnfinished, fieldUntakenR, fieldUntakenReplaced, fieldFirstTakenR}
	readCardFields = []string{"stream", "primary", "attempt", fieldDueUnbegun, fieldDueUnreported}
	// primaryFields are a read card's primary, with the read cards it names:
	// the follow's cards are found by their primary field (Table.Of), which the
	// projection must keep (ReadPlan.Validate).
	primaryFields = []string{"stream", "attempt", "rereads", "rcards", "head", PrimaryField}
	// workPrimaryFields are a work card's primary: nothing follows from it.
	workPrimaryFields = []string{"stream", "attempt"}
	streamCtlFields   = []string{"state", fieldDueMergeIdle, fieldDueIdle}
	memberCtlFields   = []string{"status"}
	readerCtlFields   = []string{"status"}
	followReadCards   = []string{FollowRCards}

	// The projections of the sprint-key reads: what each returns of its subject
	// (1.0, the fields of a composite query; E6).
	noteFields     = []string{"type", "cause", "subjects", "jopen"}
	goalFields     = []string{"exists", "score", "claimed_gen", "claimed_r"}
	tickFields     = []string{"cur", "behind_n", "score", "judged"}
	cutFields      = []string{"score", "verb"}
	droppingFields = []string{"op"}
)

// The words of the requests and the guards the time rules use (8.0, 1.0), and
// of the entries and causes they name. The guard memberup is the fleet rules'
// guardMemberUp (rules_fleet.go, IT07).
const (
	requestOpen   = "open"
	requestClose  = "close"
	requestKnow   = "know"
	requestUnhold = "unhold"

	guardDue   = "due"
	guardHold  = "hold"
	guardClock = "clock"

	// codeLimit and codeBudget are the refusals of layer 1 that halve a key
	// (1.3.5); every other code of a bug parks it.
	codeLimit  = "LIMIT"
	codeBudget = "BUDGET"

	kindCut       = "cut"
	refusedCause  = "refused"
	retiredByLate = "late"
	entryOverdue  = "overdue:"
	entryRemind   = "remind:"
	entryBehind   = "behind"
	entryCut      = "cut:"
)

// The sprint-key read kinds beyond the eight composite kinds of 1.0 (the errata's
// addendum: the registry includes the eight, it is not closed at them): each
// reads bounded keys of the sprint through `S.read_probe`. IT05's table of query
// costs (queryCosts) has no row for them, so init registers theirs
// (timeQueryCosts), the same seam the rules are registered through: from then on
// QueryCost, ReadPlan.Cost and ReadPlan.Split charge them what they cost.
const (
	queryGoal     = "goal"
	queryTick     = "tick"
	queryCut      = "cut"
	queryDropping = "dropping"
)

// The costs of the sprint-key reads, in records: the goal record and the score
// of its due entry; the tick hash, the agenda's size, the due count and the
// `behind` entry; a cut op's entry score and verb; the dropping marks, at most
// one for each stream.
const (
	goalReadRecords     = 2
	tickReadRecords     = 4
	cutReadRecords      = 1
	droppingReadRecords = MaxStreams
)

// timeQueryCosts are the rows of the sprint-key reads for the table of query
// costs, one row a kind like QueryCost's: what a query of the kind may cost,
// from its arguments alone.
var timeQueryCosts = map[string]func(q SprintQ) Cost{
	// 2 a goal: its record, and the score of its due entry.
	queryGoal: func(q SprintQ) Cost {
		n, ranged := q.Source.size()
		return recordsCost(n*goalReadRecords, ranged, q.Fields)
	},
	// the tick hash, the agenda's size, the due count and the `behind` entry.
	queryTick: func(q SprintQ) Cost { return recordsCost(tickReadRecords, 0, q.Fields) },
	// 1 a cut op: its entry's score and verb.
	queryCut: func(q SprintQ) Cost {
		n, ranged := q.Source.size()
		return recordsCost(n*cutReadRecords, ranged, q.Fields)
	},
	// one mark at most for each stream.
	queryDropping: func(q SprintQ) Cost { return recordsCost(droppingReadRecords, 0, q.Fields) },
}

// timeQueryCost is what a query may cost the store, the same as QueryCost. It
// is for the package variables of this file (lateRows), which are costed before
// init has put the rows of timeQueryCosts into queryCosts; a test holds it to
// QueryCost.
func timeQueryCost(q SprintQ) Cost {
	if row, ok := timeQueryCosts[q.Kind]; ok {
		return row(q)
	}
	return QueryCost(q)
}

// sumCost is the cost of the queries together.
func sumCost(qs ...SprintQ) Cost {
	var c Cost
	for _, q := range qs {
		c = c.Add(timeQueryCost(q))
	}
	return c
}

// idsQ is a composite query over a list of ids.
func idsQ(kind string, ids []string, fields []string) SprintQ {
	return SprintQ{Kind: kind, Source: IDSource{Kind: SourceIDs, IDs: ids}, Fields: fields}
}

// relatedQ is `related` of the ids of a table, with the follows.
func relatedQ(table string, ids, fields, follow []string) SprintQ {
	q := idsQ(QueryRelated, ids, fields)
	q.Table, q.Follow = table, follow
	return q
}

// oneKeyID stands for the one id of a query costed for one key.
var oneKeyID = []string{"id"}

// NoteFact is what jnote returns for one note (1.0): its type and cause from
// its line, the subjects it is still open on, the subjects whose jopen holds
// it, and whether R12 has marked it.
type NoteFact struct {
	Type, Cause string
	// Open are the subjects the note is still open on, Holds those whose jopen
	// field is the hold `h<note>` (1.3.4).
	Open, Holds []string
	// Marked says R12 has written the note's overdue line.
	Marked bool
}

// GoalFact is one person's goal as R14 reads it: whether the goal record
// exists, and the due entry `remind:<person>` when one is in the set.
type GoalFact struct {
	Exists bool
	// Entry says the entry is in the due set, and At is its score in R.
	Entry bool
	At    int64
}

// CutFact is a cut op's entry in `{p}cut@e` (1.2): its score, in wall time,
// and the verb the op is a part of when the read knows it ("" when it does
// not).
type CutFact struct {
	Entry bool
	At    int64
	Verb  string
}

// TickFact is what R18 reads (2.3): the backlog, the agenda's size, the due
// count, `{p}tick@e.behind_n` (0 when `behind` is not armed), the `behind`
// entry, and whether "the machine is falling behind" is open or held.
type TickFact struct {
	Backlog, Agenda, DueNow int
	BehindN                 int
	Entry                   bool
	EntryAt                 int64
	Judged                  bool
}

// TimeAnswer is what the sprint-key reads of the time rules answer, in
// Answer.Time: the answers of jnote (by note), goal (by person), cut (by op),
// tick, and dropping (every stream being dropped, with the op that marked it).
// A note, goal or op the read asked for and that is not here is unknown: no
// such note, no goal record, no cut entry.
type TimeAnswer struct {
	Notes    map[string]NoteFact
	Goals    map[string]GoalFact
	Cuts     map[string]CutFact
	Tick     *TickFact
	Dropping map[string]string
}

// TimeFacts is what the time rules read of the sprint beside its tables, as
// one read gave it: the sprint-key queries of the snapshot's plan and their
// answers, and what was asked. A rule that reads a note, goal, cut entry, the
// tick or the dropping marks that its plan did not ask for is refused as any
// read of what was not loaded (Table.Loaded, 1.5.2). A snapshot built whole
// has none of these facts, and nothing is unloaded on it.
type TimeFacts struct {
	log      *unloadedLog // nil on a snapshot built whole
	asked    map[string]map[string]bool
	tick     bool
	marks    bool
	notes    map[string]NoteFact
	goals    map[string]GoalFact
	cuts     map[string]CutFact
	tickFact TickFact
	dropping map[string]string
}

// timeFactsOf is the sprint's facts as the snapshot's read gave them.
func timeFactsOf(s *Snapshot) *TimeFacts {
	f := &TimeFacts{}
	if s == nil || s.Partial == nil {
		return f
	}
	p := s.Partial
	f.log = p.log
	f.asked = map[string]map[string]bool{}
	for i, q := range p.Plan.Sprint {
		var ta *TimeAnswer
		if i < len(p.Answer.Sprint) {
			ta = p.Answer.Sprint[i].Time
		}
		switch q.Kind {
		case QueryJnote, queryGoal, queryCut:
			set := f.asked[q.Kind]
			if set == nil {
				set = map[string]bool{}
				f.asked[q.Kind] = set
			}
			for _, id := range q.Source.IDs {
				set[id] = true
			}
		case queryTick:
			f.tick = true
		case queryDropping:
			f.marks = true
		}
		if ta == nil {
			continue
		}
		switch q.Kind {
		case QueryJnote:
			f.notes = mergeMap(f.notes, ta.Notes)
		case queryGoal:
			f.goals = mergeMap(f.goals, ta.Goals)
		case queryCut:
			f.cuts = mergeMap(f.cuts, ta.Cuts)
		case queryTick:
			if ta.Tick != nil {
				f.tickFact = *ta.Tick
			}
		case queryDropping:
			f.dropping = mergeMap(f.dropping, ta.Dropping)
		}
	}
	return f
}

func mergeMap[V any](into, from map[string]V) map[string]V {
	if len(from) == 0 {
		return into
	}
	if into == nil {
		into = make(map[string]V, len(from))
	}
	for k, v := range from {
		into[k] = v
	}
	return into
}

// unloaded records a read of what the plan did not ask for.
func (f *TimeFacts) unloaded(what string) {
	if f.log != nil {
		f.log.note(unloadedMessage + ": " + what)
	}
}

// need says the fact of the kind and id was asked for, and otherwise records
// the read.
func (f *TimeFacts) need(kind, id string) {
	if f.log != nil && !f.asked[kind][id] {
		f.unloaded(kind + " of " + id)
	}
}

// Note is jnote's answer for the note, and whether the note is known.
func (f *TimeFacts) Note(id string) (NoteFact, bool) {
	f.need(QueryJnote, id)
	n, ok := f.notes[id]
	return n, ok
}

// Goal is the goal of the person: the zero fact when there is none.
func (f *TimeFacts) Goal(person string) GoalFact {
	f.need(queryGoal, person)
	return f.goals[person]
}

// Cut is the cut entry of the op: the zero fact when it has none.
func (f *TimeFacts) Cut(op string) CutFact {
	f.need(queryCut, op)
	return f.cuts[op]
}

// Tick is the tick hash's answer.
func (f *TimeFacts) Tick() TickFact {
	if f.log != nil && !f.tick {
		f.unloaded("the tick hash")
	}
	return f.tickFact
}

// DroppingOp is the op whose mark says the stream is being dropped, and
// whether it is.
func (f *TimeFacts) DroppingOp(stream string) (string, bool) {
	if f.log != nil && !f.marks {
		f.unloaded("the dropping marks")
	}
	op, ok := f.dropping[stream]
	return op, ok
}

// TimeWrites are the writes to the sprint's own keys that a time rule plans
// and that the tables' plan has no place for. The step that carries the plan
// carries them, in the order A1 gives: what records owed work first.
type TimeWrites struct {
	// Due sets due entries to a running time: R14's move of `remind:<person>`.
	Due []DueSet
	// Goal are R14's claims on goal records.
	Goal []GoalClaim
	// UnarmBehind is R18's re-arm: `{p}tick@e.behind_n` cleared, so the next
	// tick end arms `behind` again, the entry at R + 5 min and behind_n the
	// backlog it finds (the tick end is behind_n's one writer).
	UnarmBehind bool
	// Clock is the clock fields R17 writes.
	Clock *ClockSet
	// Park are the keys the step moves out of the agenda into
	// `{p}parked@e`, the park written before the agenda's ZREM (1.3.5).
	Park []ParkKey
}

// DueSet sets the due entry Key to the running time At (a ZADD that replaces
// its score).
type DueSet struct {
	Key string
	At  int64
}

// GoalClaim is R14's phase 1 on one goal record: `claimed_r` is R, and
// `claimed_gen` is the lease generation of the step that carries it, which the
// plan does not know.
type GoalClaim struct {
	Person string
	R      int64
}

// ClockSet is the clock fields R17 writes (2.3): a nil field is left as it
// is, DueSince is set to its value, ClearDueSince sets `due_since_ms` to "",
// StopRaised is set to its value, and ClearStopRaised sets `stopraised_ms` to
// "" (a close of the STOPPED judgment, errata 3 H16).
type ClockSet struct {
	DueSince        *int64
	ClearDueSince   bool
	StopRaised      *int64
	ClearStopRaised bool
}

// ParkKey is a key to park: its text, the rule that planned it and the code
// its step was refused with (1.3.5: the park names "the rule, the key, the
// code"), which the sprint part records in {p}parked@e (sprintfn.ParkedKey).
type ParkKey struct{ Key, Rule, Code string }

// Empty says the plan writes nothing to the sprint's keys.
func (w TimeWrites) Empty() bool {
	return len(w.Due) == 0 && len(w.Goal) == 0 && !w.UnarmBehind && w.Clock == nil && len(w.Park) == 0
}

// LateDue says a card whose deadline is due (its `due_<kind>` field, in R) is
// late at now: R is at least the deadline (2.3 R11, "a card is late only when
// R is at least its due"). It is the one place the condition is written, for
// the holder table (IT11) to call.
func LateDue(now Now, due int64) bool { return now.R >= due }

// CutDue says a cut entry (in wall time) is due at now: the wall time is at
// least the entry's score (1.2, `cut:<op>`). The one place the condition is
// written.
func CutDue(now Now, at int64) bool { return now.Wall >= at }

// timeRule is one row of the table of the rules this file registers: the
// rule's name (its place in the round robin is RulePriorities'), its read and
// its plan.
type timeRule struct {
	Name string
	Read func(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey)
	Plan func(s *Snapshot, keys []AgendaKey, now Now) RulePlan
}

var timeRules = []timeRule{
	{Name: "late", Read: readLate, Plan: planLate},
	{Name: "overdue", Read: readOverdue, Plan: planOverdue},
	{Name: "hold", Read: readHold, Plan: planHold},
	{Name: "remind", Read: readRemind, Plan: planRemind},
	{Name: "behind", Read: readBehind, Plan: planBehind},
}

// registerQueryCosts puts rows into a table of query costs, and panics on a
// kind the table already has: a collision is found at once, as a rule's is.
func registerQueryCosts(table, rows map[string]func(q SprintQ) Cost) {
	for kind, row := range rows {
		if _, taken := table[kind]; taken {
			panic("sprint: the query kind " + kind + " already has a row in the table of query costs")
		}
		table[kind] = row
	}
}

func init() {
	// The sprint-key read kinds go into IT05's table of query costs.
	registerQueryCosts(queryCosts, timeQueryCosts)
	for _, tr := range timeRules {
		p, ok := PriorityOf(tr.Name)
		if !ok {
			panic("sprint: the time rule " + tr.Name + " has no row in RulePriorities")
		}
		RegisterRule(Rule{Name: tr.Name, Priority: p, Read: tr.Read, Plan: tr.Plan})
	}
}

// timeBuilder assembles one plan: it puts into one note the requests that
// differ only in their subjects (a note names every subject of its step with
// the same type and cause, 1.3.4), and keeps each guard once.
type timeBuilder struct {
	rp     RulePlan
	noteAt map[noteKey]int
	guards map[XGuard]bool
}

type noteKey struct {
	op, typ, cause, text string
	until                int64
}

func newTimeBuilder() *timeBuilder {
	return &timeBuilder{noteAt: map[noteKey]int{}, guards: map[XGuard]bool{}}
}

func (b *timeBuilder) result() RulePlan { return b.rp }

func (b *timeBuilder) note(n NoteReq) {
	k := noteKey{n.Op, n.Type, n.Cause, n.Text, n.Until}
	if i, ok := b.noteAt[k]; ok {
		b.rp.Notes[i].Subjects = append(b.rp.Notes[i].Subjects, n.Subjects...)
		return
	}
	b.noteAt[k] = len(b.rp.Notes)
	n.Subjects = append([]string(nil), n.Subjects...)
	b.rp.Notes = append(b.rp.Notes, n)
}

func (b *timeBuilder) guard(g XGuard) {
	if !b.guards[g] {
		b.guards[g] = true
		b.rp.Guards = append(b.rp.Guards, g)
	}
}

// unit adds a unit and returns its place in the plan.
func (b *timeBuilder) unit(u Unit) int {
	b.rp.Plan.Units = append(b.rp.Plan.Units, u)
	return len(b.rp.Plan.Units) - 1
}

// DueAbsent is the score a due guard carries for an entry the read found
// absent (sprintfn.XGuardAbsent): the pop takes an entry, so the rule the pop
// delivered reads its own entry absent.
const DueAbsent int64 = -1

// entryAsRead is the guard that the due entry key is as the rule read it: at
// the score read (present), or absent (DueAbsent). The entry a later part or a
// second run has armed again refuses XGUARD. 2.3's R11 and R14: "the entry's
// score as read"; tla/SprintEvents.tla, cutj (cut[c] = x) and remind1
// (remind = x); R18 guards its entry the same way.
func entryAsRead(key string, present bool, at int64) XGuard {
	if !present {
		at = DueAbsent
	}
	return XGuard{Kind: guardDue, Key: key, Score: at}
}

// uniqueKeys is the keys with a key given twice (the same text) once: the
// agenda holds a key once, but the head's keys and the new keys of a tick may
// name one twice, and a plan must not do its work twice for it.
func uniqueKeys(keys []AgendaKey) []AgendaKey {
	if len(keys) < 2 {
		return keys
	}
	seen := make(map[string]struct{}, len(keys))
	out := make([]AgendaKey, 0, len(keys))
	for _, k := range keys {
		t := keyText(k)
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, k)
	}
	return out
}

// subjectOfKey is the subject of a key of the rule: what follows "<rule>:".
func subjectOfKey(text, rule string) (string, bool) {
	sub, ok := strings.CutPrefix(text, rule+":")
	return sub, ok && sub != ""
}

// msText is a running time in milliseconds as a field's text.
func msText(n int64) string { return strconv.FormatInt(n, 10) }

// spanMs is a duration in milliseconds.
func spanMs(d time.Duration) int64 { return d.Milliseconds() }

// runningSpan is a length of running time in milliseconds as a duration to read.
func runningSpan(ms int64) time.Duration {
	return (time.Duration(ms) * time.Millisecond).Round(time.Second)
}

// cardR is a field of a card that holds a running time, and whether it is
// set.
func cardR(c *Card, field string) (int64, bool) {
	n, err := strconv.ParseInt(c.F(field), 10, 64)
	return n, err == nil
}

// wallStamp is the wall time of Now as the reader's stamp: the stamps of the
// wall stay for the reader and no rule reads them (1.3.1).
func wallStamp(now Now) string { return stamp(time.UnixMilli(now.Wall)) }

// pool is a set of names with a load each, from which the one with the lowest
// load, the first in row order on a tie, is picked and counted: a heap, so a
// pick is O(log n) and the pool is built once a plan.
type pool struct {
	h   []poolItem
	buf []poolItem
}

type poolItem struct {
	name      string
	load, ord int
}

func (a poolItem) less(b poolItem) bool {
	return a.load < b.load || a.load == b.load && a.ord < b.ord
}

// newPool is the pool of the names, in row order, with their loads.
func newPool(names []string, load func(name string) int) *pool {
	p := &pool{h: make([]poolItem, len(names))}
	for i, n := range names {
		p.h[i] = poolItem{name: n, load: load(n), ord: i}
	}
	for i := len(p.h)/2 - 1; i >= 0; i-- {
		p.down(i)
	}
	return p
}

func (p *pool) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !p.h[i].less(p.h[parent]) {
			return
		}
		p.h[i], p.h[parent] = p.h[parent], p.h[i]
		i = parent
	}
}

func (p *pool) down(i int) {
	n := len(p.h)
	for {
		l, r, least := 2*i+1, 2*i+2, i
		if l < n && p.h[l].less(p.h[least]) {
			least = l
		}
		if r < n && p.h[r].less(p.h[least]) {
			least = r
		}
		if least == i {
			return
		}
		p.h[i], p.h[least] = p.h[least], p.h[i]
		i = least
	}
}

func (p *pool) pop() poolItem {
	top := p.h[0]
	last := len(p.h) - 1
	p.h[0] = p.h[last]
	p.h = p.h[:last]
	if last > 0 {
		p.down(0)
	}
	return top
}

func (p *pool) push(it poolItem) {
	p.h = append(p.h, it)
	p.up(len(p.h) - 1)
}

// pick is the name with the lowest load that skip does not refuse, "" when
// there is none. With count, the pick's load rises by one. The names skipped
// on the way are put back as they were.
func (p *pool) pick(skip func(name string) bool, count bool) string {
	p.buf = p.buf[:0]
	chosen := ""
	for len(p.h) > 0 {
		it := p.pop()
		if skip != nil && skip(it.name) {
			p.buf = append(p.buf, it)
			continue
		}
		chosen = it.name
		if count {
			it.load++
		}
		p.buf = append(p.buf, it)
		break
	}
	for _, it := range p.buf {
		p.push(it)
	}
	return chosen
}

// R11 late: the deadlines and the cut clock.

// lateKey is the text of a key of R11: late:<kind>:<id>.
type lateKey struct{ kind, id string }

// parseLateKey is the kind and id of a key R11 serves: late:<kind>:<id>, and
// idle:<stream>, the key ServingRule sends to R11 for a stream's idle span
// (rule.go, keyRules) whose pop form is late:idle:<stream>.
func parseLateKey(text string) (lateKey, bool) {
	if id, ok := strings.CutPrefix(text, "idle:"); ok {
		return lateKey{"idle", id}, id != ""
	}
	rest, ok := strings.CutPrefix(text, "late:")
	if !ok {
		return lateKey{}, false
	}
	kind, id, ok := strings.Cut(rest, ":")
	if !ok || id == "" {
		return lateKey{}, false
	}
	return lateKey{kind, id}, true
}

// lateRow is one kind of card R11 judges (1.2): where its card is, the field
// that holds when it is due, what one key reads, and the effect.
type lateRow struct {
	Kind string
	// Due is the card field holding the running time the card is due at.
	Due string
	// Cost is what one key of the kind adds to a read: the queries it names,
	// costed as QueryCost costs them.
	Cost Cost
	// Fleet and Readers say the read needs the fleet or the readers query,
	// once for all its keys; Marks that it needs the dropping marks.
	Fleet, Readers, Marks bool
	// OwnStream says the key's id is the stream, whose control card is the
	// card.
	OwnStream bool
	find      func(s *Snapshot, id string) *Card
	effect    func(w *lateRun, lk lateKey, c *Card)
}

var lateRows = []lateRow{
	{Kind: "untaken", Due: fieldDueUntaken, Fleet: true, Marks: true, find: inCell(Fleet, Ready), effect: (*lateRun).untaken,
		Cost: sumCost(relatedQ(Fleet, oneKeyID, workCardFields, nil), relatedQ(Work, oneKeyID, workPrimaryFields, nil))},
	{Kind: "unfinished", Due: fieldDueUnfinished, Fleet: true, Marks: true, find: inCell(Fleet, Working), effect: (*lateRun).unfinished,
		Cost: sumCost(relatedQ(Fleet, oneKeyID, workCardFields, nil), relatedQ(Work, oneKeyID, workPrimaryFields, nil))},
	{Kind: "unbegun", Due: fieldDueUnbegun, Readers: true, Marks: true, find: inCell(Readers, Asked), effect: (*lateRun).unread,
		Cost: sumCost(relatedQ(Readers, oneKeyID, readCardFields, nil), relatedQ(Work, oneKeyID, primaryFields, followReadCards))},
	{Kind: "unreported", Due: fieldDueUnreported, Readers: true, Marks: true, find: inCell(Readers, Reading), effect: (*lateRun).unread,
		Cost: sumCost(relatedQ(Readers, oneKeyID, readCardFields, nil), relatedQ(Work, oneKeyID, primaryFields, followReadCards))},
	{Kind: "mergeidle", Due: fieldDueMergeIdle, OwnStream: true, Marks: true, find: streamControl, effect: (*lateRun).mergeIdle,
		Cost: sumCost(relatedQ(Merge, oneKeyID, streamCtlFields, nil)).Add(Cost{Bytes: CountBytes})},
	{Kind: "idle", Due: fieldDueIdle, OwnStream: true, Marks: true, find: streamControl, effect: (*lateRun).idle,
		Cost: sumCost(relatedQ(Merge, oneKeyID, streamCtlFields, nil))},
	{Kind: kindCut, effect: (*lateRun).cut, Cost: sumCost(idsQ(queryCut, oneKeyID, cutFields))},
}

func lateRowOf(kind string) *lateRow {
	for i := range lateRows {
		if lateRows[i].Kind == kind {
			return &lateRows[i]
		}
	}
	return nil
}

// inCell finds a card placed in a column of a table.
func inCell(table string, cols ...string) func(*Snapshot, string) *Card {
	return func(s *Snapshot, id string) *Card {
		t := s.T(table)
		if t == nil {
			return nil
		}
		if c := t.Placed(id); c != nil && contains(cols, c.Col) {
			return c
		}
		return nil
	}
}

// streamControl finds a stream's control card.
func streamControl(s *Snapshot, stream string) *Card {
	if s.Merge == nil {
		return nil
	}
	return s.Merge.Placed(CtlID(stream))
}

// primaryRun is what the plan has of one primary whose read cards it replaces:
// the primary's rcards and rereads as they stand after the replacements the
// plan has made so far, so that two late reads of one primary in one run are
// planned as one change to it, with distinct readers.
type primaryRun struct {
	card    *Card
	cards   []string
	rereads int
	// used are the readers that have a read card at an attempt, from cards.
	used map[int]map[string]bool
	// unit is the place of the primary's unit in the plan, -1 until it has
	// one; added counts the replacements it holds.
	unit, added int
	moved       []string
}

// readers are the readers that have a read card of the primary at the attempt
// (a retired card counts: that reader has read it).
func (st *primaryRun) readers(attempt int) map[string]bool {
	if st.used == nil {
		st.used = map[int]map[string]bool{}
		for _, id := range st.cards {
			if _, at, rd, ok := ParseReadCard(id); ok {
				st.mark(at, rd)
			}
		}
	}
	return st.used[attempt]
}

func (st *primaryRun) mark(attempt int, reader string) {
	set := st.used[attempt]
	if set == nil {
		set = map[string]bool{}
		st.used[attempt] = set
	}
	set[reader] = true
}

// lateRun is R11 planning the keys of one tick.
type lateRun struct {
	s   *Snapshot
	f   *TimeFacts
	now Now
	b   *timeBuilder
	// readers is the pool a reread chooses from, built when the first needs it:
	// the readers by the length of their asked queue. Each pick counts against
	// the pool, so rereads spread as the plan fills them.
	readers *pool
	// deal is the deal's rolling index (round.go, errata 3 amendment 5) a
	// replaced work card goes round the fleet by, built with the members'
	// queues; moves is the member each replacement moved it past, by unit key.
	deal      *round
	queues    map[string]int
	moves     roundMoves
	primaries map[string]*primaryRun
	order     []*primaryRun
	// planned are the keys the run has planned, by kind and id, and whether each
	// was held back: idle:<stream> and late:idle:<stream> are two texts of one
	// key, and planned once.
	planned map[lateKey]bool
}

// planLate is R11.
//
// Trigger: the pop of a card kind, of mergeidle, of idle or of cut:<op>.
// Effect, and only when R is at least the card's due time: untaken, replaced
// once to another up member, then judged; unfinished, replaced while its
// redeals are below five, then judged; unbegun and unreported, the read card
// retired and one more reader asked while the attempt's rereads are below
// three, then judged; mergeidle and cut, judged; idle, said once.
// Key: removed, except a key of a stream being dropped, which is held back
// (1.3.5). Cost: O(1) a key, and O(f) or O(r) once a plan for the members or
// readers a replacement chooses among, then O(log f) or O(15 + log r) a key.
// Without it: every timed card read every tick.
func planLate(s *Snapshot, keys []AgendaKey, now Now) RulePlan {
	w := &lateRun{s: s, f: timeFactsOf(s), now: now, b: newTimeBuilder(), primaries: map[string]*primaryRun{}, planned: map[lateKey]bool{}}
	for _, k := range uniqueKeys(keys) {
		if w.key(k) {
			w.b.rp.HeldBack = append(w.b.rp.HeldBack, k)
		} else {
			w.b.rp.Done = append(w.b.rp.Done, k)
		}
	}
	w.finish()
	rp := w.b.result()
	if w.deal != nil {
		roundWrites(&rp.Plan, w.deal, w.moves)
	}
	return rp
}

// finish writes the one change each primary that had read cards replaced gets:
// its rcards and rereads as the plan has them, guarded at the place and
// revision it was read at.
func (w *lateRun) finish() {
	for _, st := range w.order {
		if st.added == 0 {
			continue
		}
		u := &w.b.rp.Plan.Units[st.unit]
		u.Changes = append(u.Changes, change(Work, setEntry(st.card, map[string]string{
			"rereads": itoa(st.rereads), "rcards": strings.Join(st.cards, ","),
		})))
		u.Moved = strings.Join(st.moved, "; ")
	}
}

// key plans one key, and says whether it is held back for a stream being
// dropped. A key that names no kind R11 reads, a card that moved on, and a
// card not yet due plan nothing, and the key goes. A key with the kind and id
// of one the run has planned (the two texts of an idle key) plans nothing
// again, and goes or is held back as the first did.
func (w *lateRun) key(k AgendaKey) (heldBack bool) {
	lk, ok := parseLateKey(keyText(k))
	if !ok {
		return false
	}
	if held, dup := w.planned[lk]; dup {
		return held
	}
	held := w.plan(lk)
	w.planned[lk] = held
	return held
}

// plan plans one key of R11 by its kind and id, and says whether it is held
// back for a stream being dropped.
func (w *lateRun) plan(lk lateKey) (heldBack bool) {
	row := lateRowOf(lk.kind)
	if row == nil {
		return false
	}
	var c *Card
	if row.find != nil {
		if c = row.find(w.s, lk.id); c == nil {
			return false
		}
		due, ok := cardR(c, row.Due)
		if !ok || !LateDue(w.now, due) {
			return false
		}
		stream := lk.id
		if !row.OwnStream {
			stream = c.F("stream")
		}
		if _, dropping := w.f.DroppingOp(stream); dropping {
			return true
		}
	}
	row.effect(w, lk, c)
	return false
}

// readerPool is the readers by the length of their asked queue.
func (w *lateRun) readerPool() *pool {
	if w.readers == nil {
		w.readers = newPool(w.s.Readers.Rows(), func(rd string) int { return w.s.Readers.Count(rd, Asked) })
	}
	return w.readers
}

// receiver is the next member round the fleet other than present (round.go,
// errata 3 amendment 5: from the deal's rolling index, the first up with room,
// else the first up), counted as receiving a card and the index moved past it
// for key; "" when no other member is up.
func (w *lateRun) receiver(present, key string) string {
	if w.deal == nil {
		w.deal, w.queues, w.moves = dealRound(w.s), readyQueues(w.s, w.s.UpMembers()), roundMoves{}
	}
	var others []string
	for _, m := range w.s.UpMembers() {
		if m != present {
			others = append(others, m)
		}
	}
	to := w.deal.next(others, w.queues, RuleReadyCap, "", true)
	if to != "" {
		w.deal.moved(to)
		w.queues[to]++
		w.moves[key] = to
	}
	return to
}

// redeal is a work card dealt again to an up member at the next generation:
// one unit, guarded by the card's place and revision, and by X that the
// member is up.
func (w *lateRun) redeal(c *Card, to string, set map[string]string, moved string, unset ...string) {
	set["gen"] = itoa(c.Int("gen") + 1)
	set["member"] = to
	w.b.unit(Unit{Key: c.F("primary"), Stream: c.F("stream"),
		Changes: []Change{change(Fleet, moveEntry(c, to, Ready, set, unset...))}, Moved: moved})
	w.b.guard(XGuard{Kind: guardMemberUp, Member: to})
}

// know asks for a notice on one subject.
func (w *lateRun) know(typ, cause, subject, text string) {
	w.b.note(NoteReq{Op: requestKnow, Type: typ, Cause: cause, Subjects: []string{subject}, Text: text})
}

// judge asks for a judgment on one subject, with the card it is about, of the
// stream, held at its place and revision (2.3 R11, guard).
func (w *lateRun) judge(typ, cause, subject, text string, decisions []string, table string, c *Card, stream string) {
	if c != nil {
		w.b.unit(Unit{Key: c.ID, Stream: stream, Changes: []Change{change(table, guardEntry(c))}})
	}
	w.b.note(NoteReq{Op: requestOpen, Type: typ, Cause: cause, Subjects: []string{subject}, Text: text, Decisions: decisions})
}

// untaken: a work card dealt and not taken by its deadline is replaced once
// to an up member other than its present one at generation + 1, its untaken
// time unchanged and its deadline the replacement's R + 15 min; a card whose
// one replacement is made, or with no other member up, is judged, and stays.
func (w *lateRun) untaken(lk lateKey, c *Card) {
	if c.F(fieldUntakenReplaced) == "" {
		if to := w.receiver(c.Row, c.F("primary")); to != "" {
			w.redeal(c, to, map[string]string{
				fieldUntakenReplaced: "1",
				fieldDueUntaken:      msText(w.now.R + spanMs(RuleDeadlineUntaken)),
			}, fmt.Sprintf("%s %s:ready -> %s:ready gen=%d (replaced, not taken)", c.ID, c.Row, to, c.Int("gen")+1))
			w.know(NReplacedUntaken, lk.kind, c.ID, "work cards not taken by their deadline were replaced")
			return
		}
	}
	decisions := []string{"drop " + c.F("primary"), "wait"}
	if c.F(fieldUntakenReplaced) != "" {
		// its member has held it since the replacement, its own whole deadline
		decisions = append([]string{"fleet down " + c.Row}, decisions...)
	}
	why := "its one replacement is made"
	if c.F(fieldUntakenReplaced) == "" {
		why = "no other member is up"
	}
	w.judge(NWorkLate, lk.kind, c.ID, fmt.Sprintf("%s is not taken by %s after %s of running time (%s)",
		c.ID, c.Row, RuleDeadlineUntaken, why), decisions, Fleet, c, c.F("stream"))
}

// unfinished: a work card taken and not finished by its deadline is replaced
// to an up member other than its present one at generation + 1 while its
// redeals are below five, counting that redeal and starting a new untaken
// span; at five, or with no other member up, it is judged and stays, since its
// worker may still finish.
func (w *lateRun) unfinished(lk lateKey, c *Card) {
	redeals := c.Int("redeals")
	if redeals < RuleMaxRedeals {
		if to := w.receiver(c.Row, c.F("primary")); to != "" {
			w.redeal(c, to, map[string]string{
				"redeals":       itoa(redeals + 1),
				fieldUntakenR:   msText(w.now.R),
				fieldDueUntaken: msText(w.now.R + spanMs(RuleDeadlineUntaken)),
			}, fmt.Sprintf("%s %s:working -> %s:ready gen=%d (replaced, late: redeal %d of %d)", c.ID, c.Row, to, c.Int("gen")+1, redeals+1, RuleMaxRedeals),
				fieldFirstTakenR, fieldDueUnfinished, fieldUntakenReplaced)
			w.know(NReplacedLateWork, lk.kind, c.ID, fmt.Sprintf("a late work card was replaced: redeal %d of %d", redeals+1, RuleMaxRedeals))
			return
		}
	}
	why := "no other member is up"
	if redeals >= RuleMaxRedeals {
		why = "its redeals are at their bound"
	}
	w.judge(NWorkLate, lk.kind, c.ID, fmt.Sprintf("%s is not finished by %s after %s of running time (%s); redealt %d of %d times; history: nova-sprint log --card %s",
		c.ID, c.Row, RuleDeadlineUnfinished, why, redeals, RuleMaxRedeals, c.F("primary")),
		[]string{"fleet down " + c.Row, "drop " + c.F("primary"), "wait"}, Fleet, c, c.F("stream"))
}

// primaryOf is what the plan has of the primary: made when the plan first
// meets it.
func (w *lateRun) primaryOf(pr *Card) *primaryRun {
	st := w.primaries[pr.ID]
	if st == nil {
		st = &primaryRun{card: pr, cards: Split(pr.F("rcards")), rereads: pr.Int("rereads"), unit: -1}
		w.primaries[pr.ID] = st
		w.order = append(w.order, st)
	}
	return st
}

// pickReader is the reader with the shortest asked queue that has no read
// card of the primary at the attempt, the first in row order on a tie; "" when
// there is none. A retired card counts: rcards keeps every read card the
// primary has had (at most fifteen), so the readers of rcards are the readers
// that have read the attempt. With count, the reader is counted as asked.
func (w *lateRun) pickReader(st *primaryRun, attempt int, count bool) string {
	read := st.readers(attempt)
	return w.readerPool().pick(func(rd string) bool { return read[rd] }, count)
}

// unread: a read card not begun or not reported by its deadline is retired
// and one more reader is asked, one not yet asked at the attempt, while the
// attempt's rereads are below three; at three, or with no such reader, it is
// judged. The read cards a primary has late in one run are one unit: each
// retired and replaced by a distinct reader, and the primary set once, with its
// rereads raised by the replacements.
func (w *lateRun) unread(lk lateKey, c *Card) {
	pr := w.s.Work.Card(c.F("primary"))
	if pr == nil || !pr.Placed() {
		return
	}
	st := w.primaryOf(pr)
	attempt := c.Int("attempt")
	reader := ""
	if st.rereads < RuleMaxRereads && len(st.cards) < RuleMaxReadCards {
		reader = w.pickReader(st, attempt, true)
	}
	word := "not begun"
	if lk.kind == "unreported" {
		word = "not reported"
	}
	if reader == "" {
		decisions := []string{"drop " + pr.ID, "wait"}
		if len(st.cards) < RuleMaxReadCards && w.pickReader(st, attempt, false) != "" {
			decisions = append([]string{"ask --another " + pr.ID}, decisions...)
		}
		w.judge(NReadLate, lk.kind, c.ID, fmt.Sprintf("%s (%s of %s) is %s after its deadline; rereads %d of %d at attempt %d",
			c.ID, kindWord(lk.kind), c.Row, word, st.rereads, RuleMaxRereads, attempt), decisions, Readers, c, c.F("stream"))
		return
	}
	id := ReadCardID(pr.ID, attempt, reader)
	if st.unit < 0 {
		st.unit = w.b.unit(Unit{Key: pr.ID, Stream: pr.Row})
	}
	u := &w.b.rp.Plan.Units[st.unit]
	u.Changes = append(u.Changes,
		change(Readers, removeEntry(c, map[string]string{"retired": wallStamp(w.now), "retired_by": retiredByLate})),
		change(Readers, createEntry(id, reader, Asked, pr.Score, map[string]string{
			"kind": "read", "primary": pr.ID, "stream": pr.Row, "reader": reader, "attempt": itoa(attempt), "head": pr.F("head"),
			"asked": wallStamp(w.now), fieldAskedR: msText(w.now.R), fieldDueUnbegun: msText(w.now.R + spanMs(RuleDeadlineUnbegun)),
		})))
	st.cards = append(st.cards, id)
	st.mark(attempt, reader)
	st.rereads++
	st.added++
	st.moved = append(st.moved, fmt.Sprintf("%s retired (%s); %s asked of %s", c.ID, word, id, reader))
	w.know(NReplacedLateRead, lk.kind, c.ID, "late reads were retired and asked of another reader")
}

// kindWord is the state a read card is late in, as a judgment says it.
func kindWord(kind string) string {
	if kind == "unreported" {
		return "reading"
	}
	return "asked"
}

// mergeIdle: a stream merging, or waiting with queued cards, that has had no
// merge step by its deadline is judged, with its control card held at its
// place and revision.
func (w *lateRun) mergeIdle(lk lateKey, ctl *Card) {
	state := ctl.F("state")
	if state != StreamMerging && !(state == StreamWaiting && w.s.Merge.Count(lk.id, Queued) > 0) {
		return
	}
	w.judge(NMergeLate, lk.kind, StreamSubject(lk.id), fmt.Sprintf("stream %s is %s and has had no merge step for %s of running time",
		lk.id, state, RuleDeadlineMergeIdle), []string{"merge --stream " + lk.id, "card", "wait"}, Merge, ctl, lk.id)
}

// idle: a stream with open cards that has landed none for IdleSpan is told
// once: `due_idle` is unset, and the next landing sets it again.
func (w *lateRun) idle(lk lateKey, ctl *Card) {
	w.b.unit(Unit{Key: lk.id, Stream: lk.id,
		Changes: []Change{change(Merge, setEntry(ctl, nil, fieldDueIdle))},
		Moved:   fmt.Sprintf("stream %s idle: %s unset", lk.id, fieldDueIdle)})
	w.know(NIdle, lk.kind, StreamSubject(lk.id), fmt.Sprintf("stream %s has landed nothing for %s", lk.id, ruleIdleSpan))
}

// cutDecisions are the decisions a verb in parts that stopped offers (2.2): the
// same command with its op; for a drop or a remove its abort, and `ack` only
// for the others, which it ends as they stand. A verb the read does not know
// offers them all, and IT06's Accepted filters by what each verb accepts.
func cutDecisions(op, verb string) []string {
	same := "the same command with --op " + op
	switch verb {
	case "":
		return []string{same, "drop --abort --op " + op, "remove --abort --op " + op, "ack", "wait"}
	case "drop":
		return []string{same, "drop --abort --op " + op, "wait"}
	case "remove":
		return []string{same, "remove --abort --op " + op, "wait"}
	}
	return []string{same, "ack", "wait"}
}

// cut: a verb in parts whose cut clock ran out is judged, RUNNING or STOPPED:
// the plan changes no card, so it is a step of notes and sprint keys only.
// The clock counts wall time. An entry above wall was armed again by a later
// part, and the op goes on.
func (w *lateRun) cut(lk lateKey, _ *Card) {
	cf := w.f.Cut(lk.id)
	if cf.Entry && !CutDue(w.now, cf.At) {
		return
	}
	w.b.guard(entryAsRead(entryCut+lk.id, cf.Entry, cf.At))
	w.b.note(NoteReq{Op: requestOpen, Type: NCutStopped, Cause: kindCut, Subjects: []string{lk.id},
		Text:      fmt.Sprintf("the verb of op %s stopped before its end: its parts ran out of time", lk.id),
		Decisions: cutDecisions(lk.id, cf.Verb)})
}

// lateFleetQuery is R11's read of the fleet: the members' control cards and
// the deal's rolling index (round.go), which a replaced work card goes round
// the fleet by and moves.
var lateFleetQuery = SprintQ{Kind: QueryFleet, Fields: memberCtlFields, Props: []string{PropDealIndex}}

// readLate is R11's read: each card kind's key reads its card and its primary
// through related (a read card's primary follows rcards, a work card's follows
// nothing), a stream kind its control card, a work or read card once the fleet
// or the readers, every kind but a cut the dropping marks once, and a merge
// idle key the queued count of its stream. The keys are cut to what the read
// bounds allow, each key costed by the queries it names (FitKeys).
func readLate(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	fixed := map[string]bool{}
	fixedCost := map[string]Cost{
		QueryFleet:    timeQueryCost(lateFleetQuery),
		QueryReaders:  timeQueryCost(SprintQ{Kind: QueryReaders, Fields: readerCtlFields}),
		queryDropping: timeQueryCost(SprintQ{Kind: queryDropping, Fields: droppingFields}),
	}
	take, rest := FitKeys(keys, func(k AgendaKey) Cost {
		lk, ok := parseLateKey(keyText(k))
		row := lateRowOf(lk.kind)
		if !ok || row == nil {
			return Cost{}
		}
		c := row.Cost
		for _, need := range []struct {
			on   bool
			name string
		}{{row.Fleet, QueryFleet}, {row.Readers, QueryReaders}, {row.Marks, queryDropping}} {
			if need.on && !fixed[need.name] {
				fixed[need.name] = true
				c = c.Add(fixedCost[need.name])
			}
		}
		return c
	}, Cost{}, b, halvings)

	var cards, reads, workPrimaries, readPrimaries, streams, mergeIdle, ops []string
	var fleet, readers, marks bool
	for _, k := range take {
		lk, ok := parseLateKey(keyText(k))
		row := lateRowOf(lk.kind)
		if !ok || row == nil {
			continue
		}
		fleet, readers, marks = fleet || row.Fleet, readers || row.Readers, marks || row.Marks
		switch lk.kind {
		case "untaken", "unfinished":
			cards = append(cards, lk.id)
			if p, _, ok := ParseWorkCard(lk.id); ok {
				workPrimaries = append(workPrimaries, p)
			}
		case "unbegun", "unreported":
			reads = append(reads, lk.id)
			if p, _, _, ok := ParseReadCard(lk.id); ok {
				readPrimaries = append(readPrimaries, p)
			}
		case "mergeidle", "idle":
			streams = append(streams, CtlID(lk.id))
			if lk.kind == "mergeidle" {
				mergeIdle = append(mergeIdle, lk.id+":"+Queued)
			}
		case kindCut:
			ops = append(ops, lk.id)
		}
	}
	var rp ReadPlan
	add := func(q SprintQ, ids []string) {
		if len(ids) > 0 {
			q.Source = IDSource{Kind: SourceIDs, IDs: uniqueSorted(ids)}
			rp.Sprint = append(rp.Sprint, q)
		}
	}
	add(relatedQ(Fleet, nil, workCardFields, nil), cards)
	add(relatedQ(Work, nil, workPrimaryFields, nil), workPrimaries)
	add(relatedQ(Readers, nil, readCardFields, nil), reads)
	add(relatedQ(Work, nil, primaryFields, followReadCards), readPrimaries)
	add(relatedQ(Merge, nil, streamCtlFields, nil), streams)
	if fleet {
		rp.Sprint = append(rp.Sprint, lateFleetQuery)
	}
	if readers {
		rp.Sprint = append(rp.Sprint, SprintQ{Kind: QueryReaders, Fields: readerCtlFields})
	}
	if marks {
		rp.Sprint = append(rp.Sprint, SprintQ{Kind: queryDropping, Fields: droppingFields})
	}
	add(SprintQ{Kind: queryCut, Fields: cutFields}, ops)
	if len(mergeIdle) > 0 {
		rp.Counts = []CountQ{{Table: Merge, Cells: uniqueSorted(mergeIdle)}}
	}
	return rp, rest
}

// R12 overdue.

// planOverdue is R12.
//
// Trigger: the pop of overdue:<note>. Read: jnote of the note. Guard: J marks
// only a note still open and not marked. Effect: one overdue line naming the
// note, and the mark. Key: removed. Cost: O(1) a key. Without it: every open
// judgment compared with the clock every tick.
func planOverdue(s *Snapshot, keys []AgendaKey, _ Now) RulePlan {
	f := timeFactsOf(s)
	b := newTimeBuilder()
	for _, k := range uniqueKeys(keys) {
		b.rp.Done = append(b.rp.Done, k)
		note, ok := subjectOfKey(keyText(k), "overdue")
		if !ok {
			continue
		}
		nf, ok := f.Note(note)
		if !ok || len(nf.Open) == 0 || nf.Marked {
			continue
		}
		b.guard(XGuard{Kind: guardHold, Key: entryOverdue + note})
		b.note(NoteReq{Op: requestKnow, Type: NPastDue, Cause: note, Subjects: []string{note},
			Text: fmt.Sprintf("judgment %s (%s) has waited past its due time; run: nova-sprint inbox", note, nf.Type)})
	}
	return b.result()
}

func readOverdue(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	return readNotes("overdue", keys, b, halvings)
}

// R13 hold.

// planHold is R13.
//
// Trigger: the pop of hold:<note>. Read: jnote of the note, the hold in jopen
// of each of its subjects. Guard: the field holds h<note>. Effect: J removes
// the hold and writes an unheld line naming the type, cause and subjects;
// ingest turns it into the owner key, and the owner rule raises the judgment
// again if its condition stands. A second run finds no hold and writes
// nothing. Key: removed. Cost: O(1) a key. Without it: every hold compared
// with the clock every tick.
func planHold(s *Snapshot, keys []AgendaKey, _ Now) RulePlan {
	f := timeFactsOf(s)
	b := newTimeBuilder()
	for _, k := range uniqueKeys(keys) {
		b.rp.Done = append(b.rp.Done, k)
		note, ok := subjectOfKey(keyText(k), "hold")
		if !ok {
			continue
		}
		nf, ok := f.Note(note)
		if !ok || len(nf.Holds) == 0 {
			continue
		}
		b.guard(XGuard{Kind: guardHold, Key: "h" + note})
		b.note(NoteReq{Op: requestUnhold, Type: nf.Type, Cause: nf.Cause, Subjects: nf.Holds,
			Text: fmt.Sprintf("the hold on judgment %s ran out", note)})
	}
	return b.result()
}

func readHold(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	return readNotes("hold", keys, b, halvings)
}

// readNotes is the read of R12 and R13: one jnote for the notes the kept keys
// name. A jnote of one note may return the note's line and jopen of each of
// the subjects a line can name (1.3.4, up to the chunk, and MaxAboutIDs to the
// `about` of a line), so each key is costed at 1 + MaxAboutIDs records by
// QueryCost, which is what the read may return whatever the note holds.
func readNotes(rule string, keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	one := timeQueryCost(idsQ(QueryJnote, oneKeyID, noteFields))
	take, rest := FitKeys(keys, func(AgendaKey) Cost { return one }, Cost{}, b, halvings)
	var notes []string
	for _, k := range take {
		if n, ok := subjectOfKey(keyText(k), rule); ok {
			notes = append(notes, n)
		}
	}
	var rp ReadPlan
	if len(notes) > 0 {
		rp.Sprint = []SprintQ{idsQ(QueryJnote, uniqueSorted(notes), noteFields)}
	}
	return rp, rest
}

// R14 remind.

// planRemind is R14's phase 1.
//
// Trigger: the pop of remind:<person>. Guard: no entry of the person above R
// (XGUARD). Effect: `remind:<person>` moves to R + 5 min and the goal record
// takes claimed_r = R and claimed_gen = the lease generation (the step's);
// the key is removed. A second run finds the entry moved and writes nothing,
// and a second loop's claim is refused XGUARD, so at most one push a period.
// Phase 2, the push outside the store, and phase 3, its outcome, are the
// loop's. Cost: O(1) a key. Without it: every goal compared with the clock
// every tick.
func planRemind(s *Snapshot, keys []AgendaKey, now Now) RulePlan {
	f := timeFactsOf(s)
	b := newTimeBuilder()
	for _, k := range uniqueKeys(keys) {
		b.rp.Done = append(b.rp.Done, k)
		person, ok := subjectOfKey(keyText(k), "remind")
		if !ok {
			continue
		}
		g := f.Goal(person)
		if !g.Exists || g.Entry && g.At > now.R {
			continue
		}
		b.guard(entryAsRead(entryRemind+person, g.Entry, g.At))
		b.rp.Sprint.Due = append(b.rp.Sprint.Due, DueSet{Key: entryRemind + person, At: now.R + spanMs(RuleRemindEvery)})
		b.rp.Sprint.Goal = append(b.rp.Sprint.Goal, GoalClaim{Person: person, R: now.R})
	}
	return b.result()
}

func readRemind(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	one := timeQueryCost(idsQ(queryGoal, oneKeyID, goalFields))
	take, rest := FitKeys(keys, func(AgendaKey) Cost { return one }, Cost{}, b, halvings)
	var people []string
	for _, k := range take {
		if p, ok := subjectOfKey(keyText(k), "remind"); ok {
			people = append(people, p)
		}
	}
	var rp ReadPlan
	if len(people) > 0 {
		rp.Sprint = []SprintQ{idsQ(queryGoal, uniqueSorted(people), goalFields)}
	}
	return rp, rest
}

// R18 behind.

// planBehind is R18.
//
// Trigger: the pop of behind, and its owner key. Guard: no `behind` entry
// above R. Effect, when it fires: a backlog at least behind_n is judged,
// "the machine is falling behind", once; a smaller one arms `behind` again
// "with the new backlog": the step clears behind_n (TimeWrites.UnarmBehind),
// and the next tick end, behind_n's one writer, arms both, the entry at R + 5
// min and behind_n the backlog it finds; a backlog of zero is disarmed (the
// tick-end part does that when the backlog reaches zero) and closes the
// judgment when it is open. Key: removed. Cost: O(1). Without it: nothing
// names a machine that never catches up.
func planBehind(s *Snapshot, keys []AgendaKey, now Now) RulePlan {
	f := timeFactsOf(s)
	b := newTimeBuilder()
	b.rp.Done = append(b.rp.Done, uniqueKeys(keys)...)
	if len(keys) == 0 {
		return b.result()
	}
	t := f.Tick()
	switch {
	case t.Entry && t.EntryAt > now.R:
		// the entry the first run armed again: nothing to do
	case t.Backlog == 0:
		if t.Judged {
			b.guard(entryAsRead(entryBehind, t.Entry, t.EntryAt))
			b.note(NoteReq{Op: requestClose, Type: typeFallingBehind, Cause: entryBehind, Subjects: []string{subjectSprint},
				Text: "the machine has caught up: the backlog is zero"})
		}
	case t.BehindN == 0:
		// not armed: the tick-end part arms it when the backlog changes
	case t.Backlog >= t.BehindN:
		if !t.Judged {
			decisions := []string{"wait", "where"}
			if now.Running {
				decisions = []string{"wait", "stop", "where"}
			}
			b.guard(entryAsRead(entryBehind, t.Entry, t.EntryAt))
			b.note(NoteReq{Op: requestOpen, Type: typeFallingBehind, Cause: entryBehind, Subjects: []string{subjectSprint},
				Text: fmt.Sprintf("the machine is falling behind: %d lines, %d keys, %d due for %d minutes of running time",
					t.Backlog, t.Agenda, t.DueNow, int(BehindSpan/time.Minute)),
				Decisions: decisions})
		}
	default:
		// Armed again with the new backlog: behind_n cleared, and the next
		// tick end (sprintfn.TickEnd), its one writer, arms the entry at R + 5
		// min and behind_n at the backlog it finds. A behind_n left as it was
		// would hold the first backlog for ever, and a backlog that shrank once
		// and then stalled would never be judged.
		b.guard(entryAsRead(entryBehind, t.Entry, t.EntryAt))
		b.rp.Sprint.UnarmBehind = true
	}
	return b.result()
}

func readBehind(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	one := timeQueryCost(SprintQ{Kind: queryTick, Fields: tickFields})
	take, rest := FitKeys(keys, func(AgendaKey) Cost { return one }, Cost{}, b, halvings)
	var rp ReadPlan
	if len(take) > 0 {
		rp.Sprint = []SprintQ{{Kind: queryTick, Fields: tickFields}}
	}
	return rp, rest
}

func uniqueSorted(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	out := append([]string(nil), ids...)
	sort.Strings(out)
	w := 0
	for i, id := range out {
		if i == 0 || id != out[i-1] {
			out[w] = id
			w++
		}
	}
	return out[:w]
}

// R17 the look while STOPPED.

// StopRead is what R17's look read beside its dry plans (2.3 R17): the clock
// as read, the store's wall time, whether "the machine is STOPPED and moves are
// due" is open (`jopen:sprint`), and the version of every input of the dry
// plans as read (errata 3, H14 and H17).
type StopRead struct {
	Clock  Clock
	Wall   int64
	Open   bool
	Inputs StopInputs
}

// StopInputs is the version of the inputs of R17's dry plans as the look read
// them (errata 3, H14, amended by H17): the revision of every card the plans
// read, the score and id counter and the stream-set counter of {p}next@e, the
// members' control cards, and the members' beat entries. R17's step is guarded
// on it as on the clock fields: an input that moved since the read refuses the
// step XGUARD, and the next look plans afresh.
//
// The model's form (stopinputs in tla/SprintEvents.tla) is the record of every
// card over every stream as read. The store keeps no one version of that set,
// so this is the set itself, as small as it can be: the cards the plans read,
// each by its revision (layer 1 moves it on every write of the card, a move
// and a removal included), folded into one version for each table (revFold), so
// the step carries at most one guard for each table and never one for each
// card: the read holds up to MaxReadRecords cards, and a guard for each would
// not fit a step. A card created since the read, which no read saw, is caught
// by the counters (every add and rank moves the score and id counter, and a
// stream created moves the stream-set counter), and a card asked for and read
// as absent is in the fold at revision 0, so its creation moves the fold. A
// member's control card is guarded by its revision, which every write of its
// status or stable_since moves; a beat by the score of beat:<m> as read (0
// when there was none).
//
// The store cannot fold the cards a step does not name, so each table's fold
// rides beside the table's version as read (Versions, {p}tver@e), which X moves
// on every step that changes a card of the table: the version holds only where
// the fold does, and it also refuses a write of a card of the table that the
// plans did not read (a stronger guard: such a step is refused where the model
// would apply it, and the next look plans afresh).
//
// The counts a dry plan reads (a cell's count, rcount `before`) are not
// inputs here: a count moved by a card the read did not load moves no input
// (a rework reopening a card before a sentinel). The model's DryIn is every
// card's record, so this is narrower; H14 words it as the cards read. A
// judgment raised on such a read lives at most one look (stopclose).
//
// ReadStopInputs is the producer of a look's inputs; a hand-built StopInputs is
// for a test.
type StopInputs struct {
	// Cards is the revision of each card the dry plans read, by its table and
	// id: 0 for a card read as absent.
	Cards map[CardRef]uint64
	// Next and Streams are {p}next@e's score and id counter and its streams
	// field, as read.
	Next, Streams uint64
	// Members is the revision of each member's control card, by member.
	Members map[string]uint64
	// Beats is the score of beat:<m> in the due set, by member.
	Beats map[string]int64
	// Versions is the version of each table in {p}tver@e as read before its
	// cards (StopFacts.Versions), 0 for none: X moves it on every step that
	// changes a card of the table, so it is the store's check of the fold of
	// that table's cards (errata 3 H17).
	Versions map[string]uint64
}

// CardRef names a card: its table and its id.
type CardRef struct{ Table, ID string }

// Read adds to the inputs every card the snapshot loaded, as it loaded it:
// the control cards of the fleet table to Members, every other card to Cards.
// A card read twice keeps its first revision, the one the plans read.
func (in *StopInputs) Read(s *Snapshot) {
	if s == nil {
		return
	}
	for _, name := range []string{Work, Readers, Merge, Fleet} {
		for _, c := range s.T(name).LoadedCards() {
			if name == Fleet && strings.HasPrefix(c.ID, CtlID("")) {
				member := strings.TrimPrefix(c.ID, CtlID(""))
				if in.Members == nil {
					in.Members = map[string]uint64{}
				}
				if _, ok := in.Members[member]; !ok {
					in.Members[member] = c.Rev
				}
				continue
			}
			ref := CardRef{Table: name, ID: c.ID}
			if in.Cards == nil {
				in.Cards = map[CardRef]uint64{}
			}
			if _, ok := in.Cards[ref]; !ok {
				in.Cards[ref] = c.Rev
			}
		}
	}
}

// StopFacts are what R17's look read beside the cards of its snapshot.
type StopFacts struct {
	// Next is {p}next@e's score and id counter as read: no query of a snapshot
	// answers it, so the look's caller gives it.
	Next uint64
	// Absent are the cards the dry plans asked for by id that the read found
	// absent: each is recorded at revision 0, so its creation moves the fold.
	Absent []CardRef
	// Versions is the version of each table ({p}tver@e) as read before the
	// cards: a read after them could miss a write between the two, and one
	// before refuses only a step that could have held. No query of a snapshot
	// answers it, so the look's caller gives it.
	Versions map[string]uint64
}

// ReadStopInputs is the inputs of R17's dry plans as the look read them: every
// card the snapshot loaded and every member's control card (Read), the ids the
// plans asked for and the read found absent (f.Absent) at revision 0, the
// stream-set counter the read answered, the score and id counter (f.Next), and
// the beat of every member: the score of beat:<m> the read's range over the due
// set gave, 0 for a member with none. A read that did not ask for the stream-set
// counter, or for the beats, or whose beats were cut, is refused as any plan's
// read is (Snapshot.Unloaded), and the inputs it gives are not to be used.
//
// Follows stopinputs in tla/SprintEvents.tla (errata 3, H14 and H17).
func ReadStopInputs(s *Snapshot, f StopFacts) StopInputs {
	in := StopInputs{Next: f.Next, Versions: f.Versions}
	in.Read(s)
	for _, ref := range f.Absent {
		if in.Cards == nil {
			in.Cards = map[CardRef]uint64{}
		}
		if _, ok := in.Cards[ref]; !ok {
			in.Cards[ref] = 0
		}
	}
	if n, ok := posNextStreams(s); ok {
		in.Streams = n
	}
	in.Beats = stopBeats(s)
	for m := range in.Members {
		if _, ok := in.Beats[m]; !ok {
			if in.Beats == nil {
				in.Beats = map[string]int64{}
			}
			in.Beats[m] = 0
		}
	}
	return in
}

// stopBeats is the beat entries of the due set as the snapshot's read gave them,
// by member: nothing for a snapshot built whole. A read that did not ask for the
// range, or whose answer was cut, or has a beat with no score, is noted unread.
func stopBeats(s *Snapshot) map[string]int64 {
	if s == nil || s.Partial == nil {
		return nil
	}
	a, ok := sprintKeyRange(s.Partial, factBeats)
	switch {
	case !ok:
		unread(s, unloadedKeyMessage+": "+factBeats)
		return nil
	case a.HasMore:
		unread(s, unloadedKeyMessage+": "+factBeats+" holds more than the read did")
		return nil
	case len(a.Scores) != len(a.IDs):
		unread(s, unloadedKeyMessage+": "+factBeats+" without its scores")
		return nil
	}
	out := map[string]int64{}
	for i, id := range a.IDs {
		if m, isBeat := strings.CutPrefix(id, beatKeyPrefix); isBeat {
			out[m] = int64(a.Scores[i])
		}
	}
	return out
}

// revFold is the version of the cards of a table as the plans read them: the
// sum, over its cards, of a hash of the card's id and revision. The sum is
// order-free, and no two moves cancel: a card written and another removed
// change it by two hashes, not by two revisions. It is a non-negative int64,
// as a guard's score is.
func revFold(cards map[CardRef]uint64, table string) int64 {
	var sum uint64
	for ref, rev := range cards {
		if ref.Table != table {
			continue
		}
		h := fnv.New64a()
		h.Write([]byte(ref.ID))
		var b [9]byte
		binary.BigEndian.PutUint64(b[1:], rev) // b[0] = 0 separates the id from the revision
		h.Write(b[:])
		sum += h.Sum64()
	}
	return int64(sum >> 1)
}

// The kinds of guard R17's step carries besides the clock fields: one for
// each input of its dry plans as read (StopInputs).
const (
	guardRevs    = "revs"    // the fold of a table's cards' revisions, Key <table> (revFold)
	guardCounter = "counter" // a counter of {p}next@e, Key next or next.streams
	guardCtl     = "ctl"     // a member's control card's revision, Member
	guardBeat    = "beat"    // a member's beat entry's score, Member, Key beat:<m>
	guardVersion = "version" // a table's version in {p}tver@e as read, Key <table> (StopInputs.Versions)
)

// guards are the guards of the inputs, in a fixed order: the fold of the cards
// of each table read and that table's version, the two counters, each member's
// control card, each beat. At most two for each table, however many cards were
// read. The fold is the model's guard; the store cannot compute it (the step
// names no card), and the version is how it checks it: every write of a card
// of the table moves the version, so a version as read holds only where the
// fold does (the step builder carries the fold by it).
//
// Follows stopinputs in tla/SprintEvents.tla (errata 3, H14 and H17).
func (in StopInputs) guards() []XGuard {
	var out []XGuard
	tables := map[string]bool{}
	for ref := range in.Cards {
		tables[ref.Table] = true
	}
	for _, t := range membersOf(tables) {
		out = append(out, XGuard{Kind: guardRevs, Key: t, Score: revFold(in.Cards, t)},
			XGuard{Kind: guardVersion, Key: t, Score: int64(in.Versions[t])})
	}
	out = append(out, XGuard{Kind: guardCounter, Key: "next", Score: int64(in.Next)},
		XGuard{Kind: guardCounter, Key: KeyNextStreams, Score: int64(in.Streams)})
	for _, m := range membersOf(in.Members) {
		out = append(out, XGuard{Kind: guardCtl, Member: m, Key: CtlID(m), Score: int64(in.Members[m])})
	}
	for _, m := range membersOf(in.Beats) {
		out = append(out, XGuard{Kind: guardBeat, Member: m, Key: beatKeyPrefix + m, Score: in.Beats[m]})
	}
	return out
}

// membersOf is the keys of a map by member, in order.
func membersOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// StoppedLook is R17 (2.3), the machine's look every ten seconds of wall time
// while it is STOPPED: dry are the plans the rules made without applying them,
// and r what the look read beside them. It is planned here and applied apart,
// as a guarded request like every rule's step (errata 3, H7): StoppedApplies
// is its guard.
//
// Moves are due when the dry plans would change at least one card: a card an
// entry changes, or one an intent changes (needmet lowers the open count of
// each of its waiters; waitfor and waive change their card; needgone opens a
// judgment and changes no card, 1.3.3); the count is of the cards, never of the
// backlog. Due, and `due_since_ms` empty: it is set to wall. Not due: it is
// cleared. The judgment "the machine is STOPPED and moves are due" is raised
// when wall is at least `due_since_ms` + 10 min and at least `stophold_ms`, and
// `stopraised_ms` is not this span's, and says the look's time it is as of.
// While it is open, a look that finds no move due closes it (stopclose, H7)
// and clears `stopraised_ms` (stoprearm, H16): R17 raises once per span and
// close, not once per span. The step is guarded on the clock fields and on the
// version of every input of the dry plans as read (stopinputs, H14 and H17);
// its writes are in RulePlan.Sprint.Clock. On a RUNNING machine it plans
// nothing. Cost: O(n) in the cards of the dry plans, every ten seconds; the
// guards are O(n) in the inputs read, and the writes O(1).
//
// Follows R17Unit, stopclose, stoprearm and stopinputs in
// tla/SprintEvents.tla (errata 3, H7, H14, H16, H17).
func StoppedLook(dry []RulePlan, r StopRead) RulePlan {
	b := newTimeBuilder()
	c, wall := r.Clock, r.Wall
	if c.StoppedSinceMs == 0 {
		return b.result()
	}
	moves := movesDue(dry)
	var set ClockSet
	changed := false
	switch {
	case moves > 0 && c.DueSinceMs == 0:
		set.DueSince, changed = &wall, true
	case moves == 0 && c.DueSinceMs != 0:
		set.ClearDueSince, changed = true, true
	}
	raised := c.StopRaisedMs != 0 && c.StopRaisedMs == c.StoppedSinceMs
	if moves > 0 && c.DueSinceMs != 0 &&
		wall >= c.DueSinceMs+spanMs(StoppedDueSpan) && wall >= c.StopHoldMs && !raised {
		since := c.StoppedSinceMs
		set.StopRaised, changed = &since, true
		b.note(NoteReq{Op: requestOpen, Type: NStoppedWithDue, Cause: "stopped", Subjects: []string{subjectSprint},
			Text: fmt.Sprintf("the machine is STOPPED and %d moves have been due for %s, as of %s: run: nova-sprint start",
				moves, runningSpan(wall-c.DueSinceMs), stamp(time.UnixMilli(wall))),
			Decisions: []string{"start", "wait --for <duration> --reason <text>"}})
	}
	// stopclose (H7): the judgment open and no move due any more; stoprearm
	// (H16): the close clears stopraised_ms, so a move due again later in the
	// span is named again.
	if moves == 0 && r.Open {
		b.note(NoteReq{Op: requestClose, Type: NStoppedWithDue, Cause: "stopped", Subjects: []string{subjectSprint}})
		changed = true
		if c.StopRaisedMs != 0 {
			set.ClearStopRaised = true
		}
	}
	if !changed {
		return b.result()
	}
	for _, g := range []XGuard{
		{Kind: guardClock, Key: "stopped_since_ms", Score: c.StoppedSinceMs},
		{Kind: guardClock, Key: "stophold_ms", Score: c.StopHoldMs},
		{Kind: guardClock, Key: "due_since_ms", Score: c.DueSinceMs},
		{Kind: guardClock, Key: "stopraised_ms", Score: c.StopRaisedMs},
	} {
		b.guard(g)
	}
	for _, g := range r.Inputs.guards() {
		b.guard(g)
	}
	b.rp.Sprint.Clock = &set
	return b.result()
}

// StoppedApplies is the guard of R17's step at apply, in Go (X's check of it
// is layer 1's): the step p applies when the clock fields and every input of
// the dry plans are as the look read them. It returns "" when the step
// applies, and otherwise the refusal, XGUARD and what moved: the step writes
// nothing, and the next look plans afresh on what is there now.
//
// Follows the r17 guard of ReqGuard (stopinputs) in tla/SprintEvents.tla
// (errata 3, H14 and H17).
func StoppedApplies(p RulePlan, c Clock, now StopInputs) string {
	clock := map[string]int64{"stopped_since_ms": c.StoppedSinceMs, "stophold_ms": c.StopHoldMs,
		"due_since_ms": c.DueSinceMs, "stopraised_ms": c.StopRaisedMs}
	for _, g := range p.Guards {
		var at int64
		switch g.Kind {
		case guardClock:
			at = clock[g.Key]
		case guardRevs:
			at = revFold(now.Cards, g.Key)
		case guardVersion:
			at = int64(now.Versions[g.Key])
		case guardCounter:
			at = int64(now.Next)
			if g.Key == KeyNextStreams {
				at = int64(now.Streams)
			}
		case guardCtl:
			at = int64(now.Members[g.Member])
		case guardBeat:
			at = now.Beats[g.Member]
		default:
			continue
		}
		if at != g.Score {
			return fmt.Sprintf("XGUARD: %s %s is %d, read as %d", g.Kind, g.Key, at, g.Score)
		}
	}
	return ""
}

// movesDue is how many cards the plans would change: each card once, by its
// table and id, whichever rules change it, and whether an entry or an intent
// changes it. A guard names a card and changes none.
func movesDue(dry []RulePlan) int {
	seen := map[[2]string]bool{}
	for _, p := range dry {
		for _, u := range p.Plan.Units {
			for _, ch := range u.Changes {
				if changesCard(ch.Entry) {
					seen[[2]string{ch.Table, ch.Entry.ID}] = true
				}
			}
		}
		for _, in := range p.Intents {
			for _, id := range intentCards(in) {
				seen[[2]string{Work, id}] = true
			}
		}
	}
	return len(seen)
}

// intentCards are the cards an intent changes when it is applied (1.3.3): the
// waiters of a need that landed lose one open need; the waiter of a waitfor
// gets its open count, and of a waive has one need waived. A need that was
// removed opens a judgment on each waiter and changes none.
func intentCards(in Intent) []string {
	switch in.Kind {
	case "needmet":
		return in.Waiters
	case "waitfor", "waive":
		return []string{in.Card}
	}
	return nil
}

// changesCard says an entry does more than guard its card.
func changesCard(e ntable.BatchMemberEntry) bool {
	return e.Create != nil || e.Move != nil || e.Remove || len(e.Set) > 0 || len(e.Unset) > 0
}

// The parked key.

// OnBug is what the tick does with a step or a read that layer 1 refused as a
// bug (1.3.5, 2.3): the rule and the key it planned, the refusal's code, the
// size of the step (its bound and its size, in words) and how many times the
// key was halved before.
//
// It returns the judgment "the machine's step was refused", naming the rule,
// the key, the code and the size, and the next halving: after a LIMIT, or a
// BUDGET on a read, the key stays in the agenda and is planned again at half
// its size, so the next halving is one more. Only a refusal of a key already
// at a chunk of one card parks it, and every other code parks it at once: the
// key leaves the agenda (Done), the park (RulePlan.Sprint.Park) written before
// its ZREM (A1), and the returned halving is 0, since a parked key is planned
// no more until `ack` of the judgment queues it again (2.1). The step wrote
// nothing, so nothing is resent unchanged.
func OnBug(rule string, key AgendaKey, code string, size string, halvings int) (RulePlan, int) {
	b := newTimeBuilder()
	text := keyText(key)
	halves := code == codeLimit || code == codeBudget
	park := !halves || chunkAfter(halvings) <= 1
	tail := fmt.Sprintf("the key is planned again at half its size (chunk %d)", chunkAfter(halvings+1))
	if park {
		tail = "the key is parked and is not planned again until this is acknowledged"
	}
	b.note(NoteReq{Op: requestOpen, Type: NStepRefused, Cause: refusedCause, Subjects: []string{text},
		Text:      fmt.Sprintf("the machine's step was refused: rule %s, key %s, code %s, %s; %s", rule, text, code, size, tail),
		Decisions: []string{"log --since", "ack", "stop", "wait"}})
	if park {
		b.rp.Done = []AgendaKey{key}
		b.rp.Sprint.Park = []ParkKey{{Key: text, Rule: rule, Code: code}}
		return b.result(), 0
	}
	b.rp.Requeue = []AgendaKey{key}
	return b.result(), halvings + 1
}

// chunkAfter is the chunk of a step after the halvings: layer 1's ceiling
// halved each time, to a chunk of one card.
func chunkAfter(halvings int) int {
	c := stepChunk
	for i := 0; i < halvings && c > 1; i++ {
		c /= 2
	}
	return c
}

// Stand-ins for what is not merged yet, each named for its item and deleted
// when the item merges: the time rules stand on them and are changed only
// where they say.
//
//	IT01  keyText, timeKeyOf: AgendaKey is {Key, Seq} in the tree, and 8.0 gives it
//	      {Rule, Subject, Line, Offset, Order} with String and ParseAgendaKey.
//	      Only these two functions know the shape; on IT01's revision they
//	      become k.String() and ParseAgendaKey.
//	IT03  Clock (8.1 IT03 gives it these five fields).
//	IT04  stepChunk: step.Bounds.Chunk before any halving (1.0, the chunk).
//	IT06  the words of the notices and judgments these rules raise (2.2, 2.5),
//	      and subjectSprint.

// keyText is the text of an agenda key: its rule, and after a ':' or '@' its
// subject (2.1).
func keyText(k AgendaKey) string { return k.Key }

// timeKeyOf is the agenda key with the text and the order (the score in the
// agenda, 1.1).
func timeKeyOf(text string, order uint64) AgendaKey { return AgendaKey{Key: text, Seq: order} }

// Clock is the running clock's record (1.2, 8.1 IT03): the fields of {p}clock.
// A field that is "" in the store is 0 here; StoppedSinceMs is 0 while the
// machine is RUNNING.
type Clock struct {
	// StoppedMs is the STOPPED time before the current span, StoppedSinceMs
	// when the current span began, StopHoldMs the wall time until which the
	// STOPPED judgment is held, DueSinceMs the wall time at which moves were
	// first found due in this span, and StopRaisedMs the StoppedSinceMs of the
	// span in which the judgment was raised.
	StoppedMs, StoppedSinceMs, StopHoldMs, DueSinceMs, StopRaisedMs int64
}

// stepChunk is the changed members of one step (1.0, the chunk): layer 1's
// ceiling, before any halving after a LIMIT (1.3.5).
const stepChunk = 2000

// The types of the notices and judgments the time rules raise, in the
// design's own words (2.2 and 2.5). The judgments of the old tick keep their
// constants (NWorkLate, NReadLate, NMergeLate, NStoppedWithDue).
const (
	NReplacedUntaken  = "replaced a work card not taken"
	NReplacedLateWork = "replaced a late work card"
	NReplacedLateRead = "replaced a late read"
	NIdle             = "stream s has landed nothing for IdleSpan"
	NPastDue          = "a judgment has waited past its due time"
	NCutStopped       = "a verb in parts stopped before its end"
	NStepRefused      = "the machine's step was refused"
)

// subjectSprint is the subject of a judgment about the sprint as a whole
// (`jopen:sprint`, R1 and R17).
const subjectSprint = "sprint"

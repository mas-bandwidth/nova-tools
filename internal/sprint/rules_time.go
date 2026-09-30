package sprint

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// The time rules of the event-driven tick (the upper design, version 2.1,
// section 2.3, item IT10 of 8.1): R11 late (with idle and the cut clock), R12
// overdue, R13 hold, R14 remind (phase 1, the claim), R18 behind, and the
// look of R17 while the machine is STOPPED, and the judgment that names a
// step the store refused (1.3.5), which parks or halves the key.
//
// Every rule is a pure function of a snapshot, the keys it was given and Now.
// Time comes only from Now (R for running time, Wall for wall time): nothing
// here reads a clock. Every rule is idempotent (1.3.3, E7): it decides from
// the state it read, its moves and creates carry the guards of the cards they
// read, and a rule run a second time on the same keys with nothing changed
// asks for nothing that changes anything. Each rule names its cost below; a
// plan is O(1) a key.
//
// What these rules stand on and that is not merged (the clock of IT03, the
// rule registry and read plans of IT05, the words of IT06, the agenda key of
// IT01's revision) is in rules_time_stub.go, each named for its item and
// deleted when the item merges. The sprint's own reads beyond the four tables
// are TimeFacts, which the partial snapshot will carry; until it does, a
// snapshot is given them beside it.
//
// What the design leaves open, and the narrower reading taken (also listed in
// the pull request):
//
//   - RulePlan (8.0) has no field for writes to the sprint's own keys: R14's
//     move of its due entry and its claim on the goal record, R18's re-arm,
//     R17's clock fields and the park of 1.3.5. They are TimeWrites, planned
//     beside the RulePlan (TimePlan, StoppedWrites, BugWrites); the step that
//     carries the plan must carry them.
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

// The numbers of the time rules, each with its section.
const (
	// lateMaxRedeals is how many times a taken card that is late is replaced
	// (2.3 R11, unfinished): the bound R2 counts against too.
	lateMaxRedeals = 5
	// lateMaxRereads is how many more readers a late read may be given at one
	// attempt (2.3 R11, unbegun and unreported).
	lateMaxRereads = 3
	// maxReadCards is the read cards a primary may ever have (3, ask
	// --another; F1-26).
	maxReadCards = 15
	// BehindSpan is the running time from when the backlog is first not zero
	// to the judgment that the machine is falling behind (1.2, R18).
	BehindSpan = 5 * time.Minute
	// StoppedDueSpan is the wall time from when moves became due to the
	// judgment that the machine is STOPPED and they are (2.3 R17).
	StoppedDueSpan = 10 * time.Minute
	// fleetReadRecords and readersReadRecords are the most records the fleet
	// and readers queries of R11's read may return: members and streams
	// together are at most 250, and members + readers + 2 x streams at most
	// 1,024 (3, the size of the sprint).
	fleetReadRecords   = 250
	readersReadRecords = 1024
	// noteReadRecords is what a jnote of one note may return: its line, and
	// jopen of each subject the line lists (1.0, MaxListed).
	noteReadRecords = 1 + MaxListed
	// goalReadRecords is the goal record and the score of its due entry.
	goalReadRecords = 2
	// tickReadRecords is the tick hash, the agenda's size, the due count and
	// the `behind` entry (R18's read).
	tickReadRecords = 4
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

// The projections of R11's reads and the other reads of the time rules: no
// rule read fetches a whole record (1.0, bytes).
var (
	workCardFields = []string{"stream", "primary", "attempt", "gen", "member", "redeals",
		fieldDueUntaken, fieldDueUnfinished, fieldUntakenR, fieldUntakenReplaced, fieldFirstTakenR}
	readCardFields  = []string{"stream", "primary", "attempt", "reader", fieldAskedR, fieldDueUnbegun, fieldDueUnreported}
	primaryFields   = []string{"stream", "attempt", "rereads", "rcards", "head"}
	streamCtlFields = []string{"state", fieldDueMergeIdle, fieldDueIdle}
	memberCtlFields = []string{"status"}
	readerCtlFields = []string{"status"}
	goalFields      = []string{"claimed_gen", "claimed_r"}
	tickFields      = []string{"cur", "behind_n"}
	followReadCards = []string{"rcards"}
)

// The words of the queries, the requests and the guards the time rules use
// (8.0, 1.0), and of the entries and causes they name.
const (
	queryRelated = "related"
	queryFleet   = "fleet"
	queryReaders = "readers"
	queryJNote   = "jnote"
	queryGoal    = "goal"
	queryTick    = "tick"
	queryCut     = "cut"

	requestOpen   = "open"
	requestClose  = "close"
	requestKnow   = "know"
	requestUnhold = "unhold"

	guardMemberUp = "memberup"
	guardDue      = "due"
	guardHold     = "hold"
	guardClock    = "clock"

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

// CutFact is a cut op's entry in `{p}cut@e` (1.2): its score, in wall time.
type CutFact struct {
	Entry bool
	At    int64
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

// TimeFacts is what the time rules read of the sprint beside its tables: the
// answers of jnote (R12, R13), the goal records and their entries (R14), the
// cut set (R11), the tick hash (R18) and the marks of the streams being
// dropped (1.3.5).
type TimeFacts struct {
	Notes    map[string]NoteFact
	Goals    map[string]GoalFact
	Cuts     map[string]CutFact
	Tick     TickFact
	Dropping map[string]bool
}

// TimeWrites are the writes to the sprint's own keys that a time rule plans
// and that RulePlan (8.0) has no field for. The step that carries the plan
// carries them, in the order A1 gives: what records owed work first.
type TimeWrites struct {
	// Due sets due entries to a running time: R14's move of `remind:<person>`
	// and R18's re-arm of `behind`.
	Due []DueSet
	// Goal are R14's claims on goal records.
	Goal []GoalClaim
	// Tick is `{p}tick@e.behind_n`, written when R18 arms `behind` again.
	Tick *TickSet
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

// TickSet is the value of `{p}tick@e.behind_n`.
type TickSet struct{ BehindN int }

// ClockSet is the clock fields R17 writes (2.3): a nil field is left as it
// is, DueSince is set to its value, ClearDueSince sets `due_since_ms` to "",
// and StopRaised is set to its value.
type ClockSet struct {
	DueSince      *int64
	ClearDueSince bool
	StopRaised    *int64
}

// ParkKey is a key to park: its text, the note's id being the step's to add.
type ParkKey struct{ Key string }

// Empty says the plan writes nothing to the sprint's keys.
func (w TimeWrites) Empty() bool {
	return len(w.Due) == 0 && len(w.Goal) == 0 && w.Tick == nil && w.Clock == nil && len(w.Park) == 0
}

// timeRule is one row of the table of the rules this file registers: the
// rule's name and its place in the round robin (1.4.2), its read and its plan.
type timeRule struct {
	Name string
	// Priority is the rule's place in 1.4.2's order: R1, R2, R4, R3, R5, R6,
	// R7, R8, R9, R10, R11, R12, R13, R14, R18, R15, R19.
	Priority int
	Read     func(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey)
	Plan     func(s *Snapshot, keys []AgendaKey, now Now) (RulePlan, TimeWrites)
}

var timeRules = []timeRule{
	{Name: "late", Priority: 11, Read: readLate, Plan: planLate},
	{Name: "overdue", Priority: 12, Read: readOverdue, Plan: planOverdue},
	{Name: "hold", Priority: 13, Read: readHold, Plan: planHold},
	{Name: "remind", Priority: 14, Read: readRemind, Plan: planRemind},
	{Name: "behind", Priority: 15, Read: readBehind, Plan: planBehind},
}

func init() {
	for _, tr := range timeRules {
		RegisterRule(Rule{Name: tr.Name, Priority: tr.Priority, Read: tr.Read,
			Plan: func(s *Snapshot, keys []AgendaKey, now Now) RulePlan {
				p, _ := tr.Plan(s, keys, now)
				return p
			}})
	}
}

// TimePlan is the plan of the time rule with the name over the keys, with the
// writes to the sprint's keys that RulePlan has no field for. A name that is
// no time rule plans nothing.
func TimePlan(name string, s *Snapshot, keys []AgendaKey, now Now) (RulePlan, TimeWrites) {
	for _, tr := range timeRules {
		if tr.Name == name {
			return tr.Plan(s, keys, now)
		}
	}
	return RulePlan{}, TimeWrites{}
}

// timeBuilder assembles one plan: it puts into one note the requests that
// differ only in their subjects (a note names every subject of its step with
// the same type and cause, 1.3.4), and keeps each guard once.
type timeBuilder struct {
	rp     RulePlan
	tw     TimeWrites
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

func (b *timeBuilder) result() (RulePlan, TimeWrites) { return b.rp, b.tw }

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

func (b *timeBuilder) unit(u Unit) { b.rp.Plan.Units = append(b.rp.Plan.Units, u) }

// noEntryAbove is the guard that the due entry key has no score above at:
// the entry a later part or a second run has moved on refuses XGUARD.
func noEntryAbove(key string, at int64) XGuard {
	return XGuard{Kind: guardDue, Key: key, Score: at}
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

// R11 late: the deadlines and the cut clock.

// lateKey is the text of a key of R11: late:<kind>:<id>.
type lateKey struct{ kind, id string }

func parseLateKey(text string) (lateKey, bool) {
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
// that holds when it is due, what its read costs, and the effect.
type lateRow struct {
	Kind string
	// Due is the card field holding the running time the card is due at.
	Due string
	// Records is the most records the kind's key reads: the card, its primary
	// and, for a read card, its read cards.
	Records int
	// Fleet and Readers say the read needs the fleet or the readers query,
	// once for all its keys.
	Fleet, Readers bool
	// OwnStream says the key's id is the stream, whose control card is the
	// card.
	OwnStream bool
	find      func(s *Snapshot, id string) *Card
	effect    func(w *lateRun, lk lateKey, c *Card)
}

var lateRows = []lateRow{
	{Kind: "untaken", Due: fieldDueUntaken, Records: 2, Fleet: true, find: inCell(Fleet, Ready), effect: (*lateRun).untaken},
	{Kind: "unfinished", Due: fieldDueUnfinished, Records: 2, Fleet: true, find: inCell(Fleet, Working), effect: (*lateRun).unfinished},
	{Kind: "unbegun", Due: fieldDueUnbegun, Records: 2 + maxReadCards, Readers: true, find: inCell(Readers, Asked), effect: (*lateRun).unread},
	{Kind: "unreported", Due: fieldDueUnreported, Records: 2 + maxReadCards, Readers: true, find: inCell(Readers, Reading), effect: (*lateRun).unread},
	{Kind: "mergeidle", Due: fieldDueMergeIdle, Records: 1, OwnStream: true, find: streamControl, effect: (*lateRun).mergeIdle},
	{Kind: "idle", Due: fieldDueIdle, Records: 1, OwnStream: true, find: streamControl, effect: (*lateRun).idle},
	{Kind: kindCut, effect: (*lateRun).cut},
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

// lateRun is R11 planning the keys of one tick.
type lateRun struct {
	s   *Snapshot
	f   *TimeFacts
	now Now
	b   *timeBuilder
	ups []string
	// ready and asked are the queue lengths, raised as the plan places cards,
	// so that replacements spread over the members and readers.
	ready, asked map[string]int
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
// (1.3.5). Cost: O(1) a key, and O(f) or O(r) once for the members or readers
// a replacement chooses among. Without it: every timed card read every tick.
func planLate(s *Snapshot, keys []AgendaKey, now Now) (RulePlan, TimeWrites) {
	w := &lateRun{s: s, f: timeFactsOf(s), now: now, b: newTimeBuilder(),
		ready: map[string]int{}, asked: map[string]int{}}
	if s.Fleet != nil {
		w.ups = s.UpMembers()
	}
	for _, k := range keys {
		if w.key(k) {
			w.b.rp.HeldBack = append(w.b.rp.HeldBack, k)
		} else {
			w.b.rp.Done = append(w.b.rp.Done, k)
		}
	}
	return w.b.result()
}

// key plans one key, and says whether it is held back for a stream being
// dropped. A key that names no kind R11 reads, a card that moved on, and a
// card not yet due plan nothing, and the key goes.
func (w *lateRun) key(k AgendaKey) (heldBack bool) {
	lk, ok := parseLateKey(keyText(k))
	if !ok {
		return false
	}
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
		if !ok || w.now.R < due {
			return false
		}
		stream := c.F("stream")
		if row.OwnStream {
			stream = lk.id
		}
		if w.f.Dropping[stream] {
			return true
		}
	}
	row.effect(w, lk, c)
	return false
}

// queue is a member's ready queue length as the plan has it.
func (w *lateRun) queue(member string) int {
	n, ok := w.ready[member]
	if !ok {
		n = w.s.Fleet.Count(member, Ready)
		w.ready[member] = n
	}
	return n
}

// receiver is the up member other than present with the shortest ready queue,
// the first in row order on a tie; "" when no other member is up.
func (w *lateRun) receiver(present string) string {
	best := ""
	for _, m := range w.ups {
		if m != present && (best == "" || w.queue(m) < w.queue(best)) {
			best = m
		}
	}
	return best
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
	w.ready[to] = w.queue(to) + 1
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
		if to := w.receiver(c.Row); to != "" {
			w.redeal(c, to, map[string]string{
				fieldUntakenReplaced: "1",
				fieldDueUntaken:      msText(w.now.R + spanMs(DeadlineUntaken)),
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
		c.ID, c.Row, DeadlineUntaken, why), decisions, Fleet, c, c.F("stream"))
}

// unfinished: a work card taken and not finished by its deadline is replaced
// to an up member other than its present one at generation + 1 while its
// redeals are below five, counting that redeal and starting a new untaken
// span; at five, or with no other member up, it is judged and stays, since its
// worker may still finish.
func (w *lateRun) unfinished(lk lateKey, c *Card) {
	redeals := c.Int("redeals")
	if redeals < lateMaxRedeals {
		if to := w.receiver(c.Row); to != "" {
			w.redeal(c, to, map[string]string{
				"redeals":       itoa(redeals + 1),
				fieldUntakenR:   msText(w.now.R),
				fieldDueUntaken: msText(w.now.R + spanMs(DeadlineUntaken)),
			}, fmt.Sprintf("%s %s:working -> %s:ready gen=%d (replaced, late: redeal %d of %d)", c.ID, c.Row, to, c.Int("gen")+1, redeals+1, lateMaxRedeals),
				fieldFirstTakenR, fieldDueUnfinished, fieldUntakenReplaced)
			w.know(NReplacedLateWork, lk.kind, c.ID, fmt.Sprintf("a late work card was replaced: redeal %d of %d", redeals+1, lateMaxRedeals))
			return
		}
	}
	why := "no other member is up"
	if redeals >= lateMaxRedeals {
		why = "its redeals are at their bound"
	}
	w.judge(NWorkLate, lk.kind, c.ID, fmt.Sprintf("%s is not finished by %s after %s of running time (%s); redealt %d of %d times; history: nova-sprint log --card %s",
		c.ID, c.Row, DeadlineUnfinished, why, redeals, lateMaxRedeals, c.F("primary")),
		[]string{"fleet down " + c.Row, "drop " + c.F("primary"), "wait"}, Fleet, c, c.F("stream"))
}

// unread: a read card not begun or not reported by its deadline is retired
// and one more reader is asked, one not yet asked at the attempt, while the
// attempt's rereads are below three; at three, or with no such reader, it is
// judged. The read card, the primary and the new card are one unit.
func (w *lateRun) unread(lk lateKey, c *Card) {
	pr := w.s.Work.Card(c.F("primary"))
	if pr == nil || !pr.Placed() {
		return
	}
	attempt := c.Int("attempt")
	cards := Split(pr.F("rcards"))
	reader := ""
	if pr.Int("rereads") < lateMaxRereads && len(cards) < maxReadCards {
		reader = w.freshReader(pr, attempt, cards)
	}
	word := "not begun"
	if lk.kind == "unreported" {
		word = "not reported"
	}
	if reader == "" {
		decisions := []string{"drop " + pr.ID, "wait"}
		if len(cards) < maxReadCards && w.freshReader(pr, attempt, cards) != "" {
			decisions = append([]string{"ask --another " + pr.ID}, decisions...)
		}
		w.judge(NReadLate, lk.kind, c.ID, fmt.Sprintf("%s (%s of %s) is %s after its deadline; rereads %d of %d at attempt %d",
			c.ID, kindWord(lk.kind), c.Row, word, pr.Int("rereads"), lateMaxRereads, attempt), decisions, Readers, c, c.F("stream"))
		return
	}
	id := ReadCardID(pr.ID, attempt, reader)
	w.asked[reader] = w.askedQueue(reader) + 1
	w.b.unit(Unit{Key: pr.ID, Stream: pr.Row, Changes: []Change{
		change(Readers, removeEntry(c, map[string]string{"retired": wallStamp(w.now), "retired_by": retiredByLate})),
		change(Readers, createEntry(id, reader, Asked, pr.Score, map[string]string{
			"kind": "read", "primary": pr.ID, "stream": pr.Row, "reader": reader, "attempt": itoa(attempt), "head": pr.F("head"),
			"asked": wallStamp(w.now), fieldAskedR: msText(w.now.R), fieldDueUnbegun: msText(w.now.R + spanMs(DeadlineUnbegun)),
		})),
		change(Work, setEntry(pr, map[string]string{"rereads": itoa(pr.Int("rereads") + 1), "rcards": strings.Join(append(cards, id), ",")})),
	}, Moved: fmt.Sprintf("%s retired (%s); %s asked of %s", c.ID, word, id, reader)})
	w.know(NReplacedLateRead, lk.kind, c.ID, "late reads were retired and asked of another reader")
}

// kindWord is the state a read card is late in, as a judgment says it.
func kindWord(kind string) string {
	if kind == "unreported" {
		return "reading"
	}
	return "asked"
}

func (w *lateRun) askedQueue(reader string) int {
	n, ok := w.asked[reader]
	if !ok {
		n = w.s.Readers.Count(reader, Asked)
		w.asked[reader] = n
	}
	return n
}

// freshReader is the reader with the shortest asked queue that has no read
// card of the primary at the attempt (a retired card counts: that reader has
// read it); "" when there is none.
func (w *lateRun) freshReader(pr *Card, attempt int, cards []string) string {
	best := ""
	for _, rd := range w.s.Readers.Rows() {
		id := ReadCardID(pr.ID, attempt, rd)
		if contains(cards, id) || w.s.Readers.Card(id) != nil {
			continue
		}
		if best == "" || w.askedQueue(rd) < w.askedQueue(best) {
			best = rd
		}
	}
	return best
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
		lk.id, state, DeadlineMergeIdle), []string{"merge --stream " + lk.id, "card", "wait"}, Merge, ctl, lk.id)
}

// idle: a stream with open cards that has landed none for IdleSpan is told
// once: `due_idle` is unset, and the next landing sets it again.
func (w *lateRun) idle(lk lateKey, ctl *Card) {
	w.b.unit(Unit{Key: lk.id, Stream: lk.id,
		Changes: []Change{change(Merge, setEntry(ctl, nil, fieldDueIdle))},
		Moved:   fmt.Sprintf("stream %s idle: %s unset", lk.id, fieldDueIdle)})
	w.know(NIdle, lk.kind, StreamSubject(lk.id), fmt.Sprintf("stream %s has landed nothing for %s", lk.id, IdleSpan))
}

// cut: a verb in parts whose cut clock ran out is judged, RUNNING or STOPPED:
// the plan changes no card, so it is a step of notes and sprint keys only.
// The clock counts wall time. An entry above wall was armed again by a later
// part, and the op goes on.
func (w *lateRun) cut(lk lateKey, _ *Card) {
	if cf := w.f.Cuts[lk.id]; cf.Entry && cf.At > w.now.Wall {
		return
	}
	w.b.guard(noEntryAbove(entryCut+lk.id, w.now.Wall))
	w.b.note(NoteReq{Op: requestOpen, Type: NCutStopped, Cause: kindCut, Subjects: []string{lk.id},
		Text:      fmt.Sprintf("the verb of op %s stopped before its end: its parts ran out of time", lk.id),
		Decisions: []string{"the same command with --op " + lk.id, "drop --abort --op " + lk.id, "remove --abort --op " + lk.id, "ack", "wait"}})
}

// readLate is R11's read: each card kind's key reads its card and its primary
// through related (a read card follows rcards), a stream kind its control
// card, and a work or read card once the fleet or the readers.
func readLate(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	kept, rest := cutKeys(keys, b, halvings, func(k AgendaKey, seen map[string]bool) int {
		lk, ok := parseLateKey(keyText(k))
		row := lateRowOf(lk.kind)
		if !ok || row == nil {
			return 0
		}
		n := row.Records
		if row.Fleet && !seen[queryFleet] {
			seen[queryFleet] = true
			n += fleetReadRecords
		}
		if row.Readers && !seen[queryReaders] {
			seen[queryReaders] = true
			n += readersReadRecords
		}
		return n
	})
	var cards, reads, primaries, streams, mergeIdle []string
	for _, k := range kept {
		lk, ok := parseLateKey(keyText(k))
		if !ok {
			continue
		}
		switch lk.kind {
		case "untaken", "unfinished":
			cards = append(cards, lk.id)
			if p, _, ok := ParseWorkCard(lk.id); ok {
				primaries = append(primaries, p)
			}
		case "unbegun", "unreported":
			reads = append(reads, lk.id)
			if p, _, _, ok := ParseReadCard(lk.id); ok {
				primaries = append(primaries, p)
			}
		case "mergeidle", "idle":
			streams = append(streams, CtlID(lk.id))
			if lk.kind == "mergeidle" {
				mergeIdle = append(mergeIdle, lk.id)
			}
		case kindCut:
			// its entry's score: the cut set
		}
	}
	var rp ReadPlan
	add := func(q SprintQ, ids []string) {
		if len(ids) > 0 || q.Kind == queryFleet || q.Kind == queryReaders {
			q.Source = IDSource{Kind: SourceIDs, IDs: ids}
			rp.Sprint = append(rp.Sprint, q)
		}
	}
	add(SprintQ{Kind: queryRelated, Table: Fleet, Fields: workCardFields}, uniqueSorted(cards))
	add(SprintQ{Kind: queryRelated, Table: Readers, Fields: readCardFields}, uniqueSorted(reads))
	add(SprintQ{Kind: queryRelated, Table: Work, Fields: primaryFields, Follow: followReadCards}, uniqueSorted(primaries))
	add(SprintQ{Kind: queryRelated, Table: Merge, Fields: streamCtlFields}, uniqueSorted(streams))
	if len(cards) > 0 {
		add(SprintQ{Kind: queryFleet, Fields: memberCtlFields}, nil)
	}
	if len(reads) > 0 {
		add(SprintQ{Kind: queryReaders, Fields: readerCtlFields}, nil)
	}
	for _, s := range uniqueSorted(mergeIdle) {
		rp.Counts = append(rp.Counts, CountQ{Table: Merge, Cells: []string{s + ":" + Queued}})
	}
	var ops []string
	for _, k := range kept {
		if lk, ok := parseLateKey(keyText(k)); ok && lk.kind == kindCut {
			ops = append(ops, lk.id)
		}
	}
	add(SprintQ{Kind: queryCut}, uniqueSorted(ops))
	return rp, rest
}

// R12 overdue.

// planOverdue is R12.
//
// Trigger: the pop of overdue:<note>. Read: jnote of the note. Guard: J marks
// only a note still open and not marked. Effect: one overdue line naming the
// note, and the mark. Key: removed. Cost: O(1) a key. Without it: every open
// judgment compared with the clock every tick.
func planOverdue(s *Snapshot, keys []AgendaKey, _ Now) (RulePlan, TimeWrites) {
	f := timeFactsOf(s)
	b := newTimeBuilder()
	for _, k := range keys {
		b.rp.Done = append(b.rp.Done, k)
		note, ok := subjectOfKey(keyText(k), "overdue")
		if !ok {
			continue
		}
		nf, ok := f.Notes[note]
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
func planHold(s *Snapshot, keys []AgendaKey, _ Now) (RulePlan, TimeWrites) {
	f := timeFactsOf(s)
	b := newTimeBuilder()
	for _, k := range keys {
		b.rp.Done = append(b.rp.Done, k)
		note, ok := subjectOfKey(keyText(k), "hold")
		if !ok {
			continue
		}
		nf, ok := f.Notes[note]
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
// name.
func readNotes(rule string, keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	kept, rest := cutKeys(keys, b, halvings, func(AgendaKey, map[string]bool) int { return noteReadRecords })
	var notes []string
	for _, k := range kept {
		if n, ok := subjectOfKey(keyText(k), rule); ok {
			notes = append(notes, n)
		}
	}
	var rp ReadPlan
	if len(notes) > 0 {
		rp.Sprint = []SprintQ{{Kind: queryJNote, Source: IDSource{Kind: SourceIDs, IDs: uniqueSorted(notes)}}}
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
func planRemind(s *Snapshot, keys []AgendaKey, now Now) (RulePlan, TimeWrites) {
	f := timeFactsOf(s)
	b := newTimeBuilder()
	for _, k := range keys {
		b.rp.Done = append(b.rp.Done, k)
		person, ok := subjectOfKey(keyText(k), "remind")
		if !ok {
			continue
		}
		g := f.Goals[person]
		if !g.Exists || g.Entry && g.At > now.R {
			continue
		}
		b.guard(noEntryAbove(entryRemind+person, now.R))
		b.tw.Due = append(b.tw.Due, DueSet{Key: entryRemind + person, At: now.R + spanMs(RemindEvery)})
		b.tw.Goal = append(b.tw.Goal, GoalClaim{Person: person, R: now.R})
	}
	return b.result()
}

func readRemind(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	kept, rest := cutKeys(keys, b, halvings, func(AgendaKey, map[string]bool) int { return goalReadRecords })
	var people []string
	for _, k := range kept {
		if p, ok := subjectOfKey(keyText(k), "remind"); ok {
			people = append(people, p)
		}
	}
	var rp ReadPlan
	if len(people) > 0 {
		rp.Sprint = []SprintQ{{Kind: queryGoal, Source: IDSource{Kind: SourceIDs, IDs: uniqueSorted(people)}, Fields: goalFields}}
	}
	return rp, rest
}

// R18 behind.

// planBehind is R18.
//
// Trigger: the pop of behind, and its owner key. Guard: no `behind` entry
// above R. Effect, when it fires: a backlog at least behind_n is judged,
// "the machine is falling behind", once; a smaller one arms `behind` again at
// R + 5 min with the new backlog; a backlog of zero is disarmed (the
// tick-end part does that when the backlog reaches zero) and closes the
// judgment when it is open. Key: removed. Cost: O(1). Without it: nothing
// names a machine that never catches up.
func planBehind(s *Snapshot, keys []AgendaKey, now Now) (RulePlan, TimeWrites) {
	f := timeFactsOf(s)
	b := newTimeBuilder()
	b.rp.Done = append(b.rp.Done, keys...)
	if len(keys) == 0 {
		return b.result()
	}
	t := f.Tick
	switch {
	case t.Entry && t.EntryAt > now.R:
		// the entry the first run armed again: nothing to do
	case t.Backlog == 0:
		if t.Judged {
			b.guard(noEntryAbove(entryBehind, now.R))
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
			b.guard(noEntryAbove(entryBehind, now.R))
			b.note(NoteReq{Op: requestOpen, Type: typeFallingBehind, Cause: entryBehind, Subjects: []string{subjectSprint},
				Text: fmt.Sprintf("the machine is falling behind: %d lines, %d keys, %d due for %d minutes of running time",
					t.Backlog, t.Agenda, t.DueNow, int(BehindSpan/time.Minute)),
				Decisions: decisions})
		}
	default:
		b.guard(noEntryAbove(entryBehind, now.R))
		b.tw.Due = append(b.tw.Due, DueSet{Key: entryBehind, At: now.R + spanMs(BehindSpan)})
		b.tw.Tick = &TickSet{BehindN: t.Backlog}
	}
	return b.result()
}

func readBehind(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey) {
	kept, rest := cutKeys(keys, b, halvings, func(AgendaKey, map[string]bool) int { return tickReadRecords })
	var rp ReadPlan
	if len(kept) > 0 {
		rp.Sprint = []SprintQ{{Kind: queryTick, Fields: tickFields}}
	}
	return rp, rest
}

// cutKeys keeps the keys a read may name: each halving halves the keys, down
// to one, and the rest are cut off at the records the bounds allow. It keeps
// at least one key whatever it costs, so a key always moves: a read of one key
// that the store still refuses is parked (1.3.5). The cost of a key may add a
// fixed cost once, by the names in seen.
func cutKeys(keys []AgendaKey, b ReadBounds, halvings int, cost func(k AgendaKey, seen map[string]bool) int) (kept, rest []AgendaKey) {
	n := len(keys)
	for i := 0; i < halvings && n > 1; i++ {
		n /= 2
	}
	seen := map[string]bool{}
	records := 0
	for i, k := range keys {
		if i >= n {
			break
		}
		c := cost(k, seen)
		if i > 0 && records+c > b.Records {
			break
		}
		records += c
		kept = append(kept, k)
	}
	return kept, keys[len(kept):]
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

// StoppedLook is R17 (2.3), the machine's look every ten seconds of wall time
// while it is STOPPED: dry are the plans the rules made without applying them,
// c the clock as read, wall the store's wall time.
//
// Moves are due when the dry plans would change at least one card; the count
// is of the cards they change, never of the backlog. Due, and `due_since_ms`
// empty: it is set to wall. Not due: it is cleared. The judgment "the machine
// is STOPPED and moves are due" is raised when wall is at least `due_since_ms`
// + 10 min and at least `stophold_ms`, and `stopraised_ms` is not this span's:
// once a STOPPED span. The plan is guarded on the clock fields as read; its
// writes are StoppedWrites. On a RUNNING machine it plans nothing. Cost: O(n)
// in the cards of the dry plans, every ten seconds; the writes are O(1).
func StoppedLook(dry []RulePlan, c Clock, wall int64) RulePlan {
	p, _ := stoppedLook(dry, c, wall)
	return p
}

// StoppedWrites is the clock fields StoppedLook's plan writes.
func StoppedWrites(dry []RulePlan, c Clock, wall int64) TimeWrites {
	_, w := stoppedLook(dry, c, wall)
	return w
}

func stoppedLook(dry []RulePlan, c Clock, wall int64) (RulePlan, TimeWrites) {
	b := newTimeBuilder()
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
	if moves > 0 && c.DueSinceMs != 0 &&
		wall >= c.DueSinceMs+spanMs(StoppedDueSpan) && wall >= c.StopHoldMs && c.StopRaisedMs != c.StoppedSinceMs {
		since := c.StoppedSinceMs
		set.StopRaised, changed = &since, true
		b.note(NoteReq{Op: requestOpen, Type: NStoppedWithDue, Cause: "stopped", Subjects: []string{subjectSprint},
			Text: fmt.Sprintf("the machine is STOPPED and %d moves have been due for %s: run: nova-sprint start",
				moves, runningSpan(wall-c.DueSinceMs)),
			Decisions: []string{"start", "wait --for <duration> --reason <text>"}})
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
	b.tw.Clock = &set
	return b.result()
}

// movesDue is how many cards the plans would change: each card once, by its
// table and id, whichever rules change it. A guard names a card and changes
// none.
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
	}
	return len(seen)
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
// key leaves the agenda (Done), the park (BugWrites) written before its ZREM
// (A1), and the returned halving is 0, since a parked key is planned no more
// until `ack` of the judgment queues it again (2.1). The step wrote nothing,
// so nothing is resent unchanged.
func OnBug(rule string, key AgendaKey, code string, size string, halvings int) (RulePlan, int) {
	p, _, next := onBug(rule, key, code, size, halvings)
	return p, next
}

// BugWrites is the park OnBug's plan writes: one key, or none when the key
// stays to be planned at half its size.
func BugWrites(rule string, key AgendaKey, code string, size string, halvings int) TimeWrites {
	_, w, _ := onBug(rule, key, code, size, halvings)
	return w
}

func onBug(rule string, key AgendaKey, code, size string, halvings int) (RulePlan, TimeWrites, int) {
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
		b.tw.Park = []ParkKey{{Key: text}}
		rp, tw := b.result()
		return rp, tw, 0
	}
	b.rp.Requeue = []AgendaKey{key}
	rp, tw := b.result()
	return rp, tw, halvings + 1
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
// when the item merges (IT05's are real, and their part of the old stub file is
// gone): the time rules stand on them and are changed only where they say.
//
//	IT01  keyText, keyOf: AgendaKey is {Key, Seq} in the tree, and 8.0 gives it
//	      {Rule, Subject, Line, Offset, Order} with String and ParseAgendaKey.
//	IT03  Clock (8.1 IT03 gives it these five fields).
//	IT04  stepChunk: step.Bounds.Chunk before any halving (1.0, the chunk).
//	IT05  timeFactsOf: the sprint's own reads that the partial snapshot does not
//	      carry yet (jnote, the due and cut sets, the tick hash, the goal
//	      record, the dropping marks).
//	IT06  the words of the notices and judgments these rules raise (2.2, 2.5),
//	      and subjectSprint.

// keyText is the text of an agenda key: its rule, and after a ':' or '@' its
// subject (2.1).
func keyText(k AgendaKey) string { return k.Key }

// keyOf is the agenda key with the text and the order (the score in the
// agenda, 1.1).
func keyOf(text string, order uint64) AgendaKey { return AgendaKey{Key: text, Seq: order} }

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

var (
	snapFacts  sync.Map // *Snapshot -> *TimeFacts
	stubNoFact = &TimeFacts{}
)

// withTimeFacts gives a snapshot the sprint's facts that the time rules read
// beyond its tables, and returns the snapshot.
func withTimeFacts(s *Snapshot, f *TimeFacts) *Snapshot {
	snapFacts.Store(s, f)
	return s
}

// timeFactsOf is the sprint's facts read with the snapshot: the one place a
// time rule takes them from. A snapshot with none read has none: no note, no
// goal, no cut entry, an idle tick and no dropping stream.
func timeFactsOf(s *Snapshot) *TimeFacts {
	if v, ok := snapFacts.Load(s); ok {
		return v.(*TimeFacts)
	}
	return stubNoFact
}

// The types of the notices and judgments the time rules raise, in the
// design's own words (2.2 and 2.5). The judgments of the old tick keep their
// constants (NWorkLate, NReadLate, NMergeLate, NStoppedWithDue).
const (
	NReplacedUntaken  = "replaced a work card not taken"
	NReplacedLateWork = "replaced a late work card"
	NReplacedLateRead = "replaced a late read"
	NIdle             = "stream has landed nothing for IdleSpan"
	NPastDue          = "a judgment has waited past its due time"
	NCutStopped       = "a verb in parts stopped before its end"
	NStepRefused      = "the machine's step was refused"
)

// IdleSpan is how long a stream with open cards may land nothing before the
// owner is told (2.5): provisional so the build can run, the owner's to set
// (7).
const IdleSpan = 2 * time.Hour

// subjectSprint is the subject of a judgment about the sprint as a whole
// (`jopen:sprint`, R1 and R17).
const subjectSprint = "sprint"

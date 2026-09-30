// Package sprintfn is nova-sprint's one write path and one read path, seen from
// Go (the upper design, EVENT-DRIVEN-TICK version 2.1, sections 1.0 and 8.0,
// as its errata 1 corrects them; item IT12).
//
// Every change the sprint makes is one call of ns_sprint_step, a Redis
// Function that runs Layer 1 (the table), Layer 2 (the log) and the sprint's
// own phases in one invocation, in the order of 1.0 and no other: open,
// before, X.pre, derive, J, parts, plan, log, X.plan, prepare, commit. Nothing
// before commit writes. Every read is one call of ns_sprint_read, one atomic
// snapshot. The Go client never retries a call by itself: a lost reply is
// OUTCOMEUNKNOWN, and the caller settles it (L1 5, 8).
//
// Two implementations of Client carry a request: Redis, which sends one
// FCALL per step over a connection that cannot resend, and Twin, the composed
// in-memory model that runs the same phases in Go over Layer 1's twin
// (tset.Mem), Layer 2's log twin and the sprint's own keys (errata E3).
//
// Before gate G0 (Layer 1's revision 4 pinned, Layer 2 accepted again against
// its hash) nothing here is run against a store: the Lua half is a skeleton
// that no store loads, and the tests run on the twin and on a fake connection
// with no socket.
package sprintfn

import (
	"encoding/json"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The phases of one step, in the one order ns_sprint_step and the twin run
// them (1.0; errata E4 for open). PhaseOrder lists them; the Lua core's
// SP.phase_order is the same list, and a test holds the two equal.
const (
	PhaseOpen    = "open"    // Layer 1: size, version, definitions, receipt replay; a fence returns here (E5)
	PhaseBefore  = "before"  // S.before: the before-state of every id the request names
	PhaseXPre    = "X.pre"   // X's guards: lease gen, epoch, STOPPED, DROPPING, NOTCOORD, COUNTER, XGUARD, MACHINESTATE
	PhaseDerive  = "derive"  // intents to entries, on the real before-state (1.3.3)
	PhaseJ       = "J"       // J.decide: note requests to the step's notes (1.3.4)
	PhaseParts   = "parts"   // parts.pre: lease, pop, ingest, beat, clock, sprint decide on what they read
	PhasePlan    = "plan"    // S.plan: Layer 1's table plan
	PhaseLog     = "log"     // L.plan: Layer 2's lines and seqs
	PhaseXPlan   = "X.plan"  // commands only: X's, J's and the parts' writes
	PhasePrepare = "prepare" // S.prepare: the whole list validated; the receipt last
	PhaseCommit  = "commit"  // S.commit: the list, deciding nothing
)

// PhaseOrder is 1.0's phase order.
var PhaseOrder = []string{PhaseOpen, PhaseBefore, PhaseXPre, PhaseDerive, PhaseJ, PhaseParts,
	PhasePlan, PhaseLog, PhaseXPlan, PhasePrepare, PhaseCommit}

// The parts a request may carry (1.0, "One write function"), by the names
// RegisterPart takes. PartOrder is the one order their pre and their commands
// run in; the Lua core's SP.part_order is the same list.
const (
	PartLease  = "lease"
	PartPop    = "pop"
	PartIngest = "ingest"
	PartBeat   = "beat"
	PartClock  = "clock"
	PartSprint = "sprint"
)

// PartOrder is 1.0's order of the parts.
var PartOrder = []string{PartLease, PartPop, PartIngest, PartBeat, PartClock, PartSprint}

// The sprint's refusal codes (1.0, "The sprint's refusal codes"), registered
// with Layer 1's registry through AL5 and kept apart from Layer 2's CURSOR
// (errata E6).
const (
	CodeStaleGen     = "STALEGEN"     // the lease generation is not current
	CodeIngestAt     = "INGESTAT"     // the cursor is not where the ingest starts; Detail.Cur says where it is
	CodeStopped      = "STOPPED"      // a tick step that moves a card while the machine is STOPPED
	CodeDropping     = "DROPPING"     // a step touching a stream a drop or a remove has frozen
	CodeCounter      = "COUNTER"      // the score, id or stream-set counter moved since the read
	CodeNotCoord     = "NOTCOORD"     // a coordinator's verb from another actor
	CodeXGuard       = "XGUARD"       // a guard on a sprint key: a due score, a hold, a beat, a member up
	CodeMachineState = "MACHINESTATE" // start while RUNNING, stop while STOPPED
)

// SprintCodes are the eight codes above, in 1.0's order.
var SprintCodes = []string{CodeStaleGen, CodeIngestAt, CodeStopped, CodeDropping, CodeCounter,
	CodeNotCoord, CodeXGuard, CodeMachineState}

// The Layer 1 registry codes this package itself refuses with (L1 8).
const (
	CodeRequest    = "REQUEST"    // a malformed request: the caller's fault, nothing sent or written
	CodeConfig     = "CONFIG"     // the library or the twin is assembled wrong: a phase or part missing
	CodeLimit      = "LIMIT"      // a write plan past a bound of L1 6
	CodeWrongType  = "WRONGTYPE"  // a command on a key of another type
	CodeStale      = "STALE"      // the step's epoch is behind the active one
	CodeEpochAhead = "EPOCHAHEAD" // the step's epoch is ahead of the active one
)

// Version is the wire version every call carries (L1 1.1): the sprint's calls
// ride Layer 1's tset/1.
const Version = tset.Version

// Request is one call of ns_sprint_step (errata E3, replacing 8.0's block).
// Body is the step builder's; each part is optional and runs only when set.
type Request struct {
	// Fence settles Body.Op without applying anything: "fence":true on the
	// wire, omitted when false (E5). A fence carries an op, no entries and no
	// sprint field.
	Fence bool
	// Epoch is the epoch the step is planned at: the active epoch its read
	// saw (AL2). 8.0 fixes no field for it and every Layer 1 step carries one;
	// this is the narrower reading, listed as an open question.
	Epoch  tset.Decimal
	Body   Body
	Meta   Meta
	Lease  *LeasePart
	Pop    *PopPart
	Ingest *IngestPart
	Beat   *BeatPart
	Clock  *ClockPart
	Sprint *SprintPart
}

// Item is one slot of a pipeline: exactly one of a step, an atomic read, or a
// page of lines.
type Item struct {
	Step *Request
	Read *ReadRequest
	// Page is Mode "page" with exactly one Kind "lines" query (errata E2),
	// served by Layer 2's L.read in the store and by the log twin on the twin.
	Page *tset.ReadPlan
}

// ReadRequest is one call of ns_sprint_read in atomic mode: Layer 1's and
// Layer 2's queries and the sprint's composite queries, answered from one
// snapshot (1.0, "One read function"; E6).
type ReadRequest struct {
	Epoch  tset.Decimal
	Tset   []tset.ReadQuery // range, count, rcount, ids, rows, done, last, lines
	Sprint []SprintQuery    // the sprint's kinds, after Tset on the wire
}

// SprintQuery is one of the sprint's composite or sprint-key queries (1.0's
// table; the errata's addendum), as its JSON object. Its meaning is IT30's;
// the write path carries it and aligns its answer.
type SprintQuery struct {
	Kind  string          // related, front, waiters, streams, fleet, readers, needchain, jnote, or a sprint-key kind
	Query json.RawMessage // the whole object, "kind" and an explicit "fields" array included (E6)
}

// Result is one slot of a pipeline's results, aligned with its Item.
// Exactly one of Step, Read, Page, Refusal and Err is set.
type Result struct {
	Step    *StepReply
	Read    *ReadReply
	Page    *tset.ReadReply
	Refusal *Refusal // a deliberate refusal: nothing was changed
	// Err is a failure with no reply. For a step that was dispatched it is an
	// *OutcomeUnknownError: whether the step applied is unknown, and nothing
	// here resends it (L1 1.5, 8).
	Err error
}

// StepReply is an applied, replayed or fenced step.
type StepReply struct {
	// Reply is Layer 1's success envelope; Status is "ok" or "fenced" (E5).
	// Its first_seq, last_seq and lines are the log plan's.
	Reply tset.Reply
	// Parts holds what each part of the request decided, by part name, as the
	// part's plan encodes: the lease part says who holds the lease, the ingest
	// part says the new cur (1.1). Empty on a replay and on a fence.
	Parts map[string]json.RawMessage
}

// ReadReply is an atomic read's answer, the shape IT05's sprint.ReadAnswer
// fixes (errata E3) with the sprint's answers still raw: IT30 decodes them.
type ReadReply struct {
	Epoch, ActiveEpoch, TimeMS tset.Decimal
	Tset                       []tset.ReadAnswer // aligned with ReadRequest.Tset
	Sprint                     []json.RawMessage // aligned with ReadRequest.Sprint
}

// Refusal is a deliberate refusal of a step or a read: nothing was changed
// (L1 1.4). It stays this package's and not tset's because the sprint's codes
// add detail fields (errata E3: INGESTAT carries cur).
type Refusal struct {
	Code    string        `json:"code"`
	Detail  RefusalDetail `json:"detail"`
	Message string        `json:"message"`
	// Phase is the phase that refused, set by the twin; the store does not
	// report it, so a comparison of twin and store leaves it out.
	Phase string `json:"-"`
}

// RefusalDetail is Layer 1's machine-readable detail, with the sprint's
// registered fields beside it.
type RefusalDetail struct {
	tset.RefusalDetail
	// Cur is INGESTAT's cursor as the store holds it, an exact decimal (E6).
	Cur tset.Decimal `json:"cur,omitempty"`
}

// Error reports the code and the message.
func (r *Refusal) Error() string {
	if r == nil {
		return ""
	}
	if r.Message != "" {
		return r.Code + ": " + r.Message
	}
	return r.Code
}

// refuse builds a refusal with the empty id, cell and row arrays Layer 1
// always carries, and its "nothing was changed" message.
func refuse(phase, code string, detail RefusalDetail) *Refusal {
	if detail.IDs == nil {
		detail.IDs = []string{}
	}
	if detail.Cells == nil {
		detail.Cells = []string{}
	}
	if detail.Rows == nil {
		detail.Rows = []string{}
	}
	return &Refusal{Code: code, Detail: detail, Message: code + "; nothing was changed", Phase: phase}
}

// fromTset carries a Layer 1 refusal over unchanged.
func fromTset(phase string, r *tset.Refusal) *Refusal {
	return &Refusal{Code: r.Code, Detail: RefusalDetail{RefusalDetail: r.Detail}, Message: r.Message, Phase: phase}
}

// TablePlan is the Go form of Layer 1's table_plan (L1 1.3; errata E3): every
// entry of the combined request, normalized, with its before and after. X.plan
// derives the indexes and due entries from it (1.3.2).
type TablePlan struct {
	Entries []PlannedEntry
	// Before is every record Layer 1 observed, guard-only ids included: table,
	// then stored id.
	Before map[string]map[string]tset.MemberRecord
	// Changed, Guarded and ChangedPerEntry are table_plan's counts. On the
	// twin they are counted from Layer 1's Mem plan, which does not carry
	// them, and equal the committed reply's.
	Changed         int
	Guarded         int
	ChangedPerEntry []int
}

// PlannedEntry is one normalized entry: effective ids only, with each id's
// record before and after. Rows and advance entries are included.
type PlannedEntry struct {
	Entry         tset.Entry
	Before, After []tset.MemberRecord // aligned with Entry.IDs
	// FieldChanges are the effective field changes of each id, aligned with
	// Entry.IDs, and Added the rows a rows entry added with their ranks. Both
	// are L1 1.3's table_plan fields (field_changes, added) that errata E3's
	// block leaves out and the log needs.
	FieldChanges []tset.MemFieldChange
	Added        []tset.RowRank
}

// LogPlan carries the fields of Layer 2's log_plan the parts use (errata E3;
// L1 1.3; L2 0).
type LogPlan struct {
	FirstSeq, LastSeq tset.Decimal // both "0" when the step emits no line
	LineCount         int
	NoteSeqs          []tset.Decimal // aligned with the step's notes
	AboutAppends      int            // the deduplicated history appends
	// Commands and ArgvBytes are the log's planned commands and their argv
	// bytes, which prepare adds to the table's and the sprint's against the
	// shared bounds (L1 6).
	Commands, ArgvBytes int
}

// JPlan is what J decided in the pre stage for X.plan to write once the log's
// seqs are known (1.3.4): each note of the step J made, and what jopen held
// for its cause when J read it. IT15 owns the meaning; IT12 carries it.
type JPlan struct {
	Notes []JNote
}

// JNote is one note J made: Index is its place in the step's notes, so
// LogPlan.NoteSeqs[Index] is its seq and "n<seq>" its id (1.3.4).
type JNote struct {
	Index    int
	Req      NoteReq
	Existing string // the note id or hold jopen held for the cause, "" when none
}

// Before is the before-state S.before observed for the request (L1 1.1, 1.2):
// table, then stored id, then the record with the fields asked.
type Before struct {
	Records map[string]map[string]tset.MemberRecord
}

// Record is one observation, and false when the id was not asked.
func (b *Before) Record(table, id string) (tset.MemberRecord, bool) {
	if b == nil {
		return tset.MemberRecord{}, false
	}
	r, ok := b.Records[table][id]
	return r, ok
}

// BeforeAsk names ids of one table, and fields of theirs, that a phase reads
// in the pre stage (S.before).
type BeforeAsk struct {
	Table  string
	IDs    []string
	Fields []string
}

// State is what the sprint's phases see of one call: the Go counterpart of the
// Lua ctx. One call has one time, sampled once (1.0, "The clock").
type State struct {
	Prefix string       // the deployment prefix every key of the call lies under (sprint.Names.Prefix)
	Epoch  tset.Decimal // the request epoch
	NowMS  tset.Decimal // the call's one TIME, in ms
	Names  sprint.Names
	// Keys is the sprint's own keys as the pre stage reads them. Phases read;
	// only commit writes.
	Keys *Keys

	// partCmds and partArgv are the commands and the argv bytes the parts have
	// planned so far in this call: the parts share one budget, so each part's
	// Pre adds its list to the running total (twin_parts.go, checkCommands). A
	// test that calls one part's Pre sets shares to a smaller budget; nil is
	// the real one.
	partCmds, partArgv int
	shares             *partShares
}

// Cmd is one write command descriptor (L1 1.4): a prepared scalar argv and
// the keys it touches. X.plan, J and the parts return them; only commit runs
// them.
type Cmd struct {
	Argv   []string
	Access []Access
}

// Access is one key a command touches: its expected type (hash, zset, list or
// string) and read or write.
type Access struct {
	Key, Kind, Mode string
}

// Command is S.command's Go form: a write of one key of one type.
func Command(name, key, kind string, args ...string) Cmd {
	argv := append([]string{name, key}, args...)
	return Cmd{Argv: argv, Access: []Access{{Key: key, Kind: kind, Mode: "write"}}}
}

// LeasePart takes the tick's lease when it is free or expired (gen + 1), or
// renews it for its own token (1.1, {p}lease). Held by another token, the
// step's only writes are idle_loop and idle_at on the heartbeat, and its
// reply says who holds it.
type LeasePart struct {
	Owner string // a random token per process (C15)
	Name  string // the loop's actor, for display
	// HoldMS is how long a take or a renewal holds the lease from the call's
	// time. The design names until_ms and no span: the caller's to choose.
	HoldMS int64
	// Heartbeat are the last tick's heartbeat fields, written by field (A3;
	// 1.4.1), so the heartbeat costs no round trip of its own.
	Heartbeat map[string]string
	// Stopped says the loop runs STOPPED: the step also writes looked_at (1.4.5).
	Stopped bool
}

// PopMax is the most due and cut entries one pop takes (1.0, the tick budget;
// 1.2: K + K' at most 1,000).
const PopMax = 1000

// PopPart takes what is due: due entries at or below R and cut entries at or
// below wall time, queuing each as its key before removing it (1.2, A1).
type PopPart struct {
	Limit int // at most PopMax
}

// IngestPart turns a page of lines into keys: refused unless the gen is
// current and cur equals From; ZADD NX each key into the agenda or the held
// queue; then cur = To (1.1, A1).
type IngestPart struct {
	From, To tset.Decimal
	Keys     []sprint.AgendaKey
}

// BeatPart is one beat of a set of members (1.4.4): for each, the beat record,
// beat:<m> moved to R + 15 s, and seen:<m> when the member is down or has no
// fleet row.
type BeatPart struct {
	Members []BeatMember
}

// BeatMember is one member's beat and its load samples, for the reader.
type BeatMember struct {
	Member string
	Load   string
}

// The verbs of the clock part (1.2).
const (
	ClockInit  = "init"
	ClockStart = "start"
	ClockStop  = "stop"
	ClockClear = "clear"
)

// ClockPart writes the running clock in its own step: init, start, stop or
// clear (1.2). stop while STOPPED and start while RUNNING are MACHINESTATE.
type ClockPart struct {
	Verb string
}

// SprintPart writes the sprint's own keys that no other part owns (1.0): the
// counter, the dropping marks, goals, the sweep's position, parked keys and
// the coordinator, and the quarantine. IT16 owns what each does; each field is
// a set of changes the part guards and writes.
//
// The quarantine and the parked keys are the sprint part's own data (errata 2,
// item 6, and the coordinator's decision on the cold read of IT16, findings 4
// and 5): the records ride in this request, so a request that quarantines or
// parks is a request that carries the sprint part, and the part writes the
// record from the refusal's detail and never from a note, whose seq Layer 2
// assigns after the parts have decided. The cursor is the ingest part's
// (IngestPart.From and To).
type SprintPart struct {
	// Counter is {p}next@e as the plan read it (Read) and as it is to be (Set):
	// score, id and streams (U2; COUNTER when it moved).
	Counter *CounterChange
	// Dropping maps a stream to the op that freezes it (1.5.4). Undrop maps a
	// stream to the op that holds its mark, and unfreezes it; a mark held by
	// another op is DROPPING. A stream is in one map at most.
	Dropping map[string]string
	Undrop   map[string]string
	// Goals maps a person to their goal, "" to drop it (1.4.1).
	Goals map[string]string
	// Sweep is the sweep's next position, "" when unchanged (1.6).
	Sweep string
	// Park is the error step (1.3.5): the agenda keys of rules whose step was
	// refused as a bug, each with the refusal's detail. The part moves each key
	// out of the agenda into {p}parked@e, the park written first. Unpark
	// removes keys from {p}parked@e (an acknowledged line queues them again). A
	// step that parks carries no pop and no ingest part: the error step is a
	// step of notes and sprint keys only (1.4.3, T2).
	Park   []ParkedKey
	Unpark []string
	// Coordinator is the coordinator's actor, "" when unchanged (F2-13).
	Coordinator string
	// Quarantine are the cards a lower layer refused, which the part writes into
	// {p}quarantine@e with no entry on the card (1.3.5). The first record of a
	// card stands.
	Quarantine []Quarantined
	// TickEnd is R18's write on the last step of a tick (1.2, 1.4.2), nil when
	// the step is not a tick's last.
	TickEnd *TickEnd
}

// ParkedKey is one rule key the machine's step was refused for, with the
// refusal's detail that names it: the rule, the code, the bound the step
// crossed and the step's size (1.3.5: "naming the rule, the key, the code, the
// bound and the step's size"). Actual and Limit are exact decimals, empty when
// the refusal has none.
type ParkedKey struct {
	Key    string
	Rule   string
	Code   string
	Budget string
	Actual string
	Limit  string
}

// TickEnd is R18's write on the last step of a tick (1.4.2, R18): Backlog is
// the loop's backlog at the end of the tick, Layer 2's last less the cursor, as
// an exact decimal. Not zero and not armed, the sprint part enters behind at R
// + 5 min and records the backlog in tick@e as behind_n; zero, it disarms; an
// armed backlog that is not zero is left alone, so the entry is not moved while
// it shrinks and R18 judges the backlog it armed (1.2). Armed means behind_n is
// set: the pop takes the entry when it fires, and behind_n stays until R18 has
// judged.
type TickEnd struct {
	Backlog tset.Decimal
}

// CounterChange is a guarded change of {p}next@e (1.5.4, U2).
type CounterChange struct {
	Read, Set map[string]string
}

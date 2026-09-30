package sprintfn

import (
	"container/list"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// X is the sprint's own checks and derivation (the upper design, version 2.1,
// 1.0's phase list, 1.3.2, 1.3.5; item IT13). It has two halves, both run by
// the write path for every step:
//
//   - XPre, in the pre stage, reads the sprint's own keys and decides: the
//     lease generation, STOPPED, DROPPING, NOTCOORD, COUNTER, the guards of
//     Body.Guards (XGUARD) and the clock's state (MACHINESTATE). It writes
//     nothing. A refusal here leaves the whole store as it was.
//   - XCmds, after the table plan and the log plan, turns what the step
//     changed into commands only (1.0, "X.plan"): the indexes and the card
//     kinds of the due set, derived from each changed card's before and
//     after by IT02's derivation (sprint.IndexOps); the quarantined ids'
//     removal from the indexes; and the agenda's edits, in A1's order.
//
// The Lua twin of both halves is internal/nsprint/fn/lua/sprint_x.lua. Before gate
// G0 the two are compared on the same steps under gopher-lua, beside the twin, over
// a model of Layer 1's helpers (twin_x_lua_test.go); after G0 they are compared
// against the store (1.3.2; IT12's TestTwinEqualsLua10000).
//
// What X reads it reads through State.Keys (the twin's copy of the sprint's
// own keys), typed first: a key of another type is WRONGTYPE before any value
// of it is used, as the store's typed reads map it (L1 1.1, S.readcmd).

// The sprint keys X reads and writes, named without the deployment prefix's
// "sprint:" and without the epoch (1.0, "Keys"; 1.3.1; 1.2). A key marked
// epoch is written at every epoch as name@<e>, "@0" included (0, change 1).
const (
	xKeyLease       = "lease"       // {p}lease (sprint), HASH: owner, name, until_ms, gen (1.1)
	xKeyClock       = "clock"       // {p}clock (sprint), HASH: the running clock's fields (1.2)
	xKeyCoordinator = "coordinator" // {p}coordinator (sprint), STRING: who may run the coordinator's verbs (1.3.1)
	xKeyStrangers   = "strangers"   // {p}strangers (sprint), HASH member -> ms, then "noticed" (1.3.1)
	xKeyNext        = "next"        // {p}next@e, HASH: score, id:<s>, gate:<s>, streams (1.3.1)
	xKeyDropping    = "dropping"    // {p}dropping@e, HASH stream -> op (1.3.1)
	xKeyQuarantine  = "quarantine"  // {p}quarantine@e, HASH id -> code (1.3.1)
	xKeyDue         = "due"         // {p}due@e, ZSET <kind>:<id> -> running ms (1.2)
	xKeyCut         = "cut"         // {p}cut@e, ZSET cut:<op> -> wall ms (1.2)
	xKeyAgenda      = "agenda"      // {p}agenda@e, ZSET rule key -> order (1.1)
	xKeyHeldQ       = "heldq"       // {p}heldq@e, ZSET held key -> order (1.1)
	xKeyAskwait     = "askwait"     // {p}askwait@e, ZSET id -> R (1.3.1)
	xKeyJopen       = "jopen:"      // {p}jopen:<subject>@e, HASH <type>|<cause> -> note id or h<note> (1.3.1)
	xKeyVersion     = "tver"        // {p}tver@e, HASH table -> version: X's count of the steps that changed a card of it (errata 3 H17)
)

// The fields of those keys X reads.
const (
	xFieldLeaseGen   = "gen"  // {p}lease: the generation (1.1)
	xFieldLeaseName  = "name" // {p}lease: the loop's actor, for display (1.1)
	xFieldNextScore  = "score"
	xFieldNextStream = "streams"
	xFieldStream     = "stream"  // a card's stream, on the cards not placed in a stream's row (1.3.2's before fields)
	xFieldStatus     = "status"  // a member's control card: up, down or held (1.3.1, "Card fields the rules read")
	xNoticed         = "noticed" // {p}strangers[m] once R1 has said it (2.3, R1)
)

// The running clock's fields (1.2, "The running clock"): R(t) = t - stopped_ms
// - (stopped_since_ms == "" ? 0 : t - stopped_since_ms), so a machine is
// STOPPED exactly when stopped_since_ms is set.
const (
	xClockStopped   = "stopped_ms"
	xClockSince     = "stopped_since_ms"
	xClockStopHold  = "stophold_ms"
	xClockDueSince  = "due_since_ms"
	xClockStopRaise = "stopraised_ms"
)

// xClockFields are the five fields of {p}clock, in 1.2's order: the ones a
// clock guard may name.
var xClockFields = []string{xClockStopped, xClockSince, xClockStopHold, xClockDueSince, xClockStopRaise}

// The kinds of XGuard (8.0; 1.3.5; 2.3). IT05's type gives the four fields
// Kind, Member, Key and Score; what each kind puts in them is this file's
// reading, listed as an open question:
//
//	memberup    Member: the receiving member. The member's control card has
//	            status up (R2's deal, R11, 2.3).
//	beatstale   Member: the member. No beat:<Member> entry of the due set lies
//	            above R: a beat since moved it to R + 15 s (2.3, R2).
//	due         Key: the due-set member <kind>:<id> (a cut:<op> member is read in
//	            the cut set, which counts wall time). Score: the entry's score as
//	            the plan read it, or XGuardAbsent for an entry that was absent
//	            (2.3, R11's cut clock, R14's phase 1 and R18: the time rules'
//	            entryAsRead; the pop takes the entry, so the rule it delivered
//	            reads it absent).
//	hold        Member: the subject. Key: "<type>|<cause>=<value>", the field of
//	            {p}jopen:<subject> and what it holds (2.3, R13: "h<note>").
//	clock       Key: one of the five clock fields. Score: its value as read, or
//	            XGuardAbsent for an empty or absent one (2.3, R17).
//	coordinator Member: the coordinator as the verb read it, "" for none
//	            (init --coordinator, 3).
//	stranger    Member: the machine. {p}strangers[Member] is not yet noticed
//	            (2.3, R1).
//	setguard    Key: a sprint.SetGuard of kind zguard as JSON. The count of
//	            the index's members within Min and Max is held within AtLeast
//	            and AtMost, or RANGECOUNT: S.zguard (2.3, R3's release and R6's
//	            deal as S.zguard(sent:<stream>, rcount, -inf, max, atmost 0)).
//	version     Key: a table. Score: its version in {p}tver@e as read, 0 for
//	            none (errata 3 H17: R17's guard on the cards of each table its
//	            dry plans read; X moves the version on every step that changes
//	            a card of the table, so it holds when no card of it changed).
//	counter     Key: a field of {p}next@e, score or streams. Score: its value
//	            as read, 0 for a field absent (2.3, R15's COUNTER on
//	            next.streams; R17's stopinputs, errata 3 H14). The sprint
//	            part's CounterChange guards the fields it writes; this guards
//	            a field a step reads and does not write.
const (
	XGuardMemberUp    = "memberup"
	XGuardBeatStale   = "beatstale"
	XGuardDue         = "due"
	XGuardHold        = "hold"
	XGuardClock       = "clock"
	XGuardCoordinator = "coordinator"
	XGuardStranger    = "stranger"
	// XGuardSet is a set guard over a sprint index (IT08's sprint.SetGuard of
	// kind zguard, its JSON the Key): Layer 1's S.zguard, the count of the
	// index's members within Min and Max held within AtLeast and AtMost, or
	// RANGECOUNT (a race). Added by IT19: an add's ready cards are guarded by
	// S.zguard(sent:s, rcount, -inf, the highest score admitted, atmost 0)
	// (1.5.4), and R3's release by the same (2.3). A SetGuard of kind rcount is
	// Layer 1's own entry, never an XGuard.
	XGuardSet     = "setguard"
	XGuardCounter = "counter"
	XGuardVersion = "version"
)

// XGuardAbsent is the Score of a due or clock guard over an entry or field
// that was absent (or empty) when the plan read it. No running or wall time is
// negative (R counts from the machine's start), so -1 is never a real score.
const XGuardAbsent int64 = -1

// xScoreCounterKey is the field of {p}next@e whose value every placed score
// lies below (1.3.1, U2).
const xScoreCounterKey = xFieldNextScore

// xPiece is the most members of one command X writes: Layer 1's pieces (L1
// 1.4: at most 1,000 collection elements a command), the same cut IT02 makes
// of a key's derivation (sprint.IndexPiece).
const xPiece = sprint.IndexPiece

// xMarkPiece is the most ids of one read of {p}quarantine@e: X needs only whether
// an id is marked, but a hash answers with the value, which nothing but the
// sprint part bounds, so each id is reserved at Layer 1's field cap (xMarkValueCap)
// and a read of 16 ids reserves 1 MiB of the step's 8 MiB. An empty mark key is
// found by one HLEN, which is the common case.
const xMarkPiece = 16

// xMarkValueCap is the most bytes of one id's value in {p}quarantine@e X reads:
// Layer 1's field cap (1.0's table: one field value, 64 KiB). A longer value is
// DRIFT, as the store's read reservation refuses it.
const xMarkValueCap = 64 * 1024

// xMaxSeq is the largest seq a line has (Layer 2's LOGID bound, 2^53 - 1), so the
// largest line a requeued key can name.
const xMaxSeq = 1<<53 - 1

// xWholeChars is the most characters of a whole-number field of a card the
// derivation reads (a count or a due time): the store's Lua holds numbers as
// doubles, and reads at most this many (sprint_x.lua, WHOLE_CHARS).
const xWholeChars = 18

// xScoreDigits is the most digits of the score counter: a double keeps 15 decimal
// digits exactly, and the counter is a whole number (U2).
const xScoreDigits = 15

// xStashMax is the most steps the twin may have between X.pre and X.plan at
// once before the oldest is forgotten (see xCarry). One step is in flight a
// call, under the twin's mutex, so the bound is reached only by steps refused
// between the two phases, whose carry is never taken.
const xStashMax = 4096

// xCoordinatorVerbs are the verbs the design names as the coordinator's, which
// X refuses from any actor but {p}coordinator (NOTCOORD): release (0, row 43),
// ack and wait (2.2: "Judgments are the coordinator's"), accept and rework
// (3: "as R9, by the coordinator", "as R10, by the coordinator"). The design
// gives no complete list; this is the narrower reading, listed as an open
// question.
var xCoordinatorVerbs = map[string]bool{"release": true, "ack": true, "wait": true, "accept": true, "rework": true}

// xStreamTables are the tables whose rows are streams: a card placed in one of
// them is in the stream its row names (1.3.1). Every other card names its
// stream in the field xFieldStream.
var xStreamTables = map[string]bool{sprint.Work: true, sprint.Merge: true}

// xCarry is what X.pre hands X.plan for one step: the request, the before-state
// of every card the derivation reads, the ids already quarantined, the
// requeued keys' orders and the epoch the step writes.
//
// errata 1 fixes Phases.XCmds as commands over (State, TablePlan, LogPlan), which
// carries neither the request nor the pre stage's observations, and Layer 1's
// twin exposes only the fields a plan projected. The Lua half keeps this on ctx
// (ctx.x); the twin's State has no such field and is not X's to change, so the
// carry is held here by the call's *State, taken exactly once by XCmds and
// bounded at xStashMax (open question).
type xCarry struct {
	req         *Request
	obs         *Before
	quarantined map[string]bool
	requeue     map[string]float64 // the requeued keys that are not queued, and the order each is added at
	writeEpoch  tset.Decimal
	probes      int             // the store reads X.pre made, for the coster's check
	readKeys    map[string]bool // the keys X.pre read, typed: prepare does not type-read them again
	// versions are the tables whose cards the step may change, with the version
	// each is moved to (xVersions).
	versions []xVersion
}

// xVersion is a table's version after the step: {p}tver@e's field of the table.
type xVersion struct{ table, to string }

// xStashT holds the carries by call, oldest first, at most max of them.
type xStashT struct {
	mu    sync.Mutex
	max   int
	byKey map[*State]*list.Element
	order *list.List // of *xStashEntry
}

type xStashEntry struct {
	st    *State
	carry *xCarry
}

func newXStash(max int) *xStashT {
	return &xStashT{max: max, byKey: map[*State]*list.Element{}, order: list.New()}
}

// xStash is the twin's: every call of every twin in the process keeps its carry
// here between X.pre and X.plan.
var xStash = newXStash(xStashMax)

// put stores a call's carry, forgetting the oldest carries past the bound.
func (s *xStashT) put(st *State, c *xCarry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if el, ok := s.byKey[st]; ok {
		s.order.Remove(el)
	}
	s.byKey[st] = s.order.PushBack(&xStashEntry{st: st, carry: c})
	for s.order.Len() > s.max {
		oldest := s.order.Front()
		s.order.Remove(oldest)
		delete(s.byKey, oldest.Value.(*xStashEntry).st)
	}
}

// take removes and returns a call's carry, nil when X.pre stored none.
func (s *xStashT) take(st *State) *xCarry {
	s.mu.Lock()
	defer s.mu.Unlock()
	el, ok := s.byKey[st]
	if !ok {
		return nil
	}
	s.order.Remove(el)
	delete(s.byKey, st)
	return el.Value.(*xStashEntry).carry
}

func init() {
	prev := defaultPhases.Before
	defaultPhases.Before = func(st *State, req *Request) []BeforeAsk {
		asks := xBefore(st, req)
		if prev != nil {
			asks = append(asks, prev(st, req)...)
		}
		return asks
	}
	defaultPhases.XPre = XPre
	defaultPhases.XCmds = XCmds
}

// xRead is X.pre's reader of the sprint's own keys: typed, and counting what it
// reads, since every read of a sprint key is one cell or key probe against the
// step's 20,000 (L1 6). A key of the wrong type is WRONGTYPE (1.0, "Refusals").
// It also keeps the keys it has read: Layer 1's prepare type-reads every key a
// step writes that no earlier read has typed, one probe each, which the coster
// adds for X's writes (CostX).
type xRead struct {
	k      *Keys
	prefix string
	epoch  tset.Decimal
	probes int
	read   map[string]bool // the keys read, typed
}

// sprint is the sprint-level key {p}name.
func (r *xRead) sprint(name string) string { return r.prefix + "sprint:" + name }

// at is the per-epoch key {p}name@e, at the request epoch.
func (r *xRead) at(name string) string { return r.sprint(name) + "@" + string(r.epoch) }

func (r *xRead) wrongType(key, want string) *Refusal {
	if r.read == nil {
		r.read = map[string]bool{}
	}
	r.read[key] = true
	if t := r.k.Type(key); t != kindNone && t != want {
		return refuse(PhaseXPre, CodeWrongType, RefusalDetail{})
	}
	return nil
}

// hget is one field of a hash, and whether it is set. One probe.
func (r *xRead) hget(key, field string) (string, bool, *Refusal) {
	r.probes++
	if ref := r.wrongType(key, kindHash); ref != nil {
		return "", false, ref
	}
	v, ok := r.k.HGet(key, field)
	return v, ok, nil
}

// hlen is the number of fields of a hash. One probe.
func (r *xRead) hlen(key string) (int, *Refusal) {
	r.probes++
	if ref := r.wrongType(key, kindHash); ref != nil {
		return 0, ref
	}
	if v := r.k.ks.vals[key]; v != nil && v.kind == kindHash {
		return len(v.hash), nil
	}
	return 0, nil
}

// zscore is one member's score of a sorted set, and whether it is there. One
// probe.
func (r *xRead) zscore(key, member string) (float64, bool, *Refusal) {
	r.probes++
	if ref := r.wrongType(key, kindZSet); ref != nil {
		return 0, false, ref
	}
	s, ok := r.k.ZScore(key, member)
	return s, ok, nil
}

// get is a string key's value, and whether it is set. One probe.
func (r *xRead) get(key string) (string, bool, *Refusal) {
	r.probes++
	if ref := r.wrongType(key, kindString); ref != nil {
		return "", false, ref
	}
	v, ok := r.k.Get(key)
	return v, ok, nil
}

// xProbesFor is the probes a batched read of n members costs in pieces of piece:
// one command of at most piece members each, so one probe a piece (and none for
// none, which the store does not send: the reader asks nothing of an empty list).
func xProbesFor(n, piece int) int { return (n + piece - 1) / piece }

// hmget is several fields of one hash, in commands of at most xPiece fields: one
// probe each.
func (r *xRead) hmget(key string, fields []string) (map[string]string, *Refusal) {
	return r.hmgetIn(key, fields, xPiece, 0)
}

// hmgetIn is hmget in commands of at most piece fields. When reserve is not 0 each
// field of a command is reserved reserve bytes, and a command whose values come to
// more than its fields' reservations is DRIFT, as the store's read reservation
// refuses it (S.readcmd compares what the command returned with what it reserved).
func (r *xRead) hmgetIn(key string, fields []string, piece, reserve int) (map[string]string, *Refusal) {
	r.probes += xProbesFor(len(fields), piece)
	if ref := r.wrongType(key, kindHash); ref != nil {
		return nil, ref
	}
	out := make(map[string]string, len(fields))
	for i := 0; i < len(fields); i += piece {
		last := min(i+piece, len(fields))
		bytes := 0
		for _, f := range fields[i:last] {
			if v, ok := r.k.HGet(key, f); ok {
				out[f] = v
				bytes += len(v)
			}
		}
		if reserve != 0 && bytes > reserve*(last-i) {
			return nil, xRefuse("DRIFT", RefusalDetail{}, "DRIFT: a read of %d fields of %s returned %d bytes, over the %d it reserves", last-i, key, bytes, reserve*(last-i))
		}
	}
	return out, nil
}

// xRefuse is a refusal of X.pre with its own words; every one of them ends in
// "nothing was changed" (L1 1.4).
func xRefuse(code string, detail RefusalDetail, format string, args ...any) *Refusal {
	ref := refuse(PhaseXPre, code, detail)
	if format != "" {
		ref.Message = fmt.Sprintf(format, args...) + "; nothing was changed"
	}
	return ref
}

// xTouchesMembers says a request changes a table member: a caller entry that
// creates, moves or removes one, or an intent (which the derive phase turns into
// an entry that changes one).
func xTouchesMembers(req *Request) bool {
	if len(req.Body.Intents) != 0 {
		return true // an intent becomes an entry that changes a member (1.3.3)
	}
	for _, e := range req.Body.Entries {
		switch e.Kind {
		case "create", "move", "remove":
			if len(e.IDs) != 0 {
				return true
			}
		}
	}
	return false
}

// xChanged says an entry changes members (a guard changes none).
func xChanged(e tset.Entry) bool {
	switch e.Kind {
	case "create", "move", "remove":
		return len(e.IDs) != 0
	}
	return false
}

// xBefore is what X asks S.before for in the pre stage, beyond the request's
// own before_fields (AL1; 1.3.2): every field the derivation reads
// (sprint.IndexFields) of every id a caller entry changes and every card an
// intent names, the stream field of the cards that are not placed in a stream's
// row (DROPPING), and the control card of each receiving member (memberup). The
// caller's own entries are all it names: a derived entry's card is one of the
// intent's.
func xBefore(st *State, req *Request) []BeforeAsk {
	byTable := map[string]*BeforeAsk{}
	ask := func(table string, ids []string, fields ...string) {
		a := byTable[table]
		if a == nil {
			a = &BeforeAsk{Table: table}
			byTable[table] = a
		}
		a.IDs = append(a.IDs, ids...)
		a.Fields = append(a.Fields, fields...)
	}
	indexFields := xIndexFields()
	for _, e := range req.Body.Entries {
		if !xChanged(e) {
			continue
		}
		fields := indexFields
		if !xStreamTables[e.Table] {
			fields = append(append([]string{}, indexFields...), xFieldStream)
		}
		ask(e.Table, e.IDs, fields...)
	}
	for _, in := range req.Body.Intents {
		ids := append([]string{in.Card, in.Need}, in.Needs...)
		ids = append(ids, in.Waiters...)
		live := ids[:0:0]
		for _, id := range ids {
			if id != "" {
				live = append(live, id)
			}
		}
		ask(sprint.Work, live, indexFields...)
	}
	if epoch, err := strconv.ParseUint(string(req.Epoch), 10, 64); err == nil {
		for _, g := range req.Body.Guards {
			if g.Kind == XGuardMemberUp && g.Member != "" {
				ask(sprint.Fleet, []string{sprint.StoredID(sprint.CtlID(g.Member), epoch)}, xFieldStatus)
			}
		}
	}
	tables := make([]string, 0, len(byTable))
	for t := range byTable {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	out := make([]BeforeAsk, 0, len(tables))
	for _, t := range tables {
		a := byTable[t]
		a.IDs, a.Fields = xDistinct(a.IDs), xDistinct(a.Fields)
		out = append(out, *a)
	}
	return out
}

// xSortedKeys is the keys of a map, sorted.
func xSortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// xDistinct is the distinct strings of a list, in order.
func xDistinct(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// xClockState is the clock as X.pre reads it.
type xClockState struct {
	fields  map[string]string
	stopped bool
	since   int64 // stopped_since_ms, when stopped
	past    int64 // stopped_ms
	now     int64 // the call's one time
}

// r is R(now): running time (1.2).
func (c xClockState) r() int64 {
	out := c.now - c.past
	if c.stopped {
		out -= c.now - c.since
	}
	return out
}

// xReadClock reads the clock's five fields in one probe. A field that is not a
// whole number is CONFIG: the machine never writes one.
func xReadClock(r *xRead, nowMS int64) (xClockState, *Refusal) {
	vals, ref := r.hmget(r.sprint(xKeyClock), xClockFields)
	if ref != nil {
		return xClockState{}, ref
	}
	c := xClockState{fields: vals, now: nowMS}
	for _, f := range xClockFields {
		// canonical, as Layer 1's S.uint reads it: the machine writes no other
		if v := vals[f]; v != "" && !tset.ValidDecimal(tset.Decimal(v)) {
			return xClockState{}, xRefuse(CodeConfig, RefusalDetail{}, "the clock's field %s holds %q, not a whole number", f, v)
		}
	}
	c.past, _ = strconv.ParseInt(vals[xClockStopped], 10, 64)
	if v := vals[xClockSince]; v != "" {
		c.stopped = true
		c.since, _ = strconv.ParseInt(v, 10, 64)
	}
	return c, nil
}

// value is a clock field as a guard compares it: its whole number, or
// XGuardAbsent when it is empty or absent.
func (c xClockState) value(field string) int64 {
	v := c.fields[field]
	if v == "" {
		return XGuardAbsent
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}

// xStreamsTouched is the streams a step's caller entries and intents change,
// with the cards that name each (1.3.5: a card of a dropping stream is refused
// DROPPING by X). A work or merge card is in the stream its cells name; any other
// card names its stream in its field, before or as the step sets it.
func xStreamsTouched(req *Request, obs *Before) (streams []string, cards map[string][]string, ref *Refusal) {
	cards = map[string][]string{}
	add := func(stream, id string) {
		if stream == "" {
			return
		}
		if _, ok := cards[stream]; !ok {
			streams = append(streams, stream)
		}
		cards[stream] = append(cards[stream], id)
	}
	cellRow := func(cell string) string {
		row, _, err := tset.ParseCellRef(cell)
		if err != nil {
			return ""
		}
		return row
	}
	for _, e := range req.Body.Entries {
		if !xChanged(e) {
			continue
		}
		for i, id := range e.IDs {
			if xStreamTables[e.Table] {
				add(cellRow(e.From), id)
				if to := cellRow(e.To); to != cellRow(e.From) {
					add(to, id)
				}
				continue
			}
			if e.Kind != "create" {
				rec, ok := obs.Record(e.Table, id)
				if !ok {
					return nil, nil, xRefuse(CodeConfig, RefusalDetail{}, "X.pre was not given the before-state of %s card %s", e.Table, id)
				}
				if f, set := rec.Fields[xFieldStream]; set && f.Present {
					add(f.Value, id)
				}
			}
			if v, ok := e.Set[xFieldStream]; ok {
				add(v, id)
			}
			if i < len(e.Each) {
				if v, ok := e.Each[i][xFieldStream]; ok {
					add(v, id)
				}
			}
		}
	}
	for _, in := range req.Body.Intents {
		for _, id := range append([]string{in.Card}, in.Waiters...) {
			if id == "" {
				continue
			}
			if rec, ok := obs.Record(sprint.Work, id); ok && rec.Exists && rec.Place != nil {
				add(rec.Place.Row, id)
			}
		}
	}
	return streams, cards, nil
}

// xOwnsPart says an op identity is a part of the op a stream's mark names:
// "<op>/p<k>" or "<op>/abort" (1.5.4).
func xOwnsPart(opID, mark string) bool {
	rest, ok := strings.CutPrefix(opID, mark+"/")
	if !ok {
		return false
	}
	if rest == "abort" {
		return true
	}
	n, ok := strings.CutPrefix(rest, "p")
	if !ok || n == "" {
		return false
	}
	_, err := strconv.ParseUint(n, 10, 64)
	return err == nil
}

// xCounterStored reads the fields of {p}next@e named, as they stand now: one probe.
func xCounterStored(r *xRead, fields []string) (map[string]string, *Refusal) {
	return r.hmget(r.at(xKeyNext), fields)
}

// xPlacedScoresBelow is the ScoresBelowCounter check (1.3.1, U2): every score a
// step places on a primary (a create or a move of the work table that carries
// scores) is below the counter {p}next@e.score after the step, which is the
// change's Set when the step raises it and the stored value otherwise. A score
// at or above it is COUNTER, naming the cards. A counter that does not exist is
// no bound the score could be below: the narrower reading refuses.
func xPlacedScoresBelow(req *Request, stored string, have bool) *Refusal {
	counter := stored
	haveCounter := have
	if c := req.Sprint; c != nil && c.Counter != nil {
		if v, ok := c.Counter.Set[xScoreCounterKey]; ok {
			counter, haveCounter = v, true
		}
	}
	var over []string
	limit, whole := xWholeScore(counter)
	if haveCounter && !whole {
		return xRefuse(CodeConfig, RefusalDetail{}, "the score counter holds %q, not a whole number", counter)
	}
	for _, e := range req.Body.Entries {
		if e.Table != sprint.Work || (e.Kind != "create" && e.Kind != "move") {
			continue
		}
		for i, s := range e.Scores {
			v, err := strconv.ParseFloat(s, 64)
			if err != nil || !haveCounter || v >= limit {
				over = append(over, e.IDs[i])
			}
		}
	}
	if len(over) == 0 {
		return nil
	}
	if !haveCounter {
		return xRefuse(CodeCounter, RefusalDetail{RefusalDetail: tset.RefusalDetail{IDs: over}}, "COUNTER: the sprint has no score counter, so no score is below it")
	}
	return xRefuse(CodeCounter, RefusalDetail{RefusalDetail: tset.RefusalDetail{IDs: over}},
		"COUNTER: a score is at or above the counter %s and the step does not raise it", counter)
}

// xCounterRises holds a step that sets the score counter to a value the counter
// can take: never below the value it holds (1.3.1, U2: the counter only rises, so
// a plan made on a stale read, or made wrong, can never take it below a score
// already placed). A step that sets it to the value it holds changes nothing.
func xCounterRises(req *Request, stored string, have bool) *Refusal {
	c := req.Sprint
	if c == nil || c.Counter == nil || !have {
		return nil
	}
	set, ok := c.Counter.Set[xScoreCounterKey]
	if !ok {
		return nil
	}
	was, whole := xWholeScore(stored)
	if !whole {
		return xRefuse(CodeConfig, RefusalDetail{}, "the score counter holds %q, not a whole number", stored)
	}
	if now, _ := xWholeScore(set); now < was {
		return xRefuse(CodeCounter, RefusalDetail{}, "COUNTER: the step sets the score counter to %s, below %s which it holds; the counter only rises", set, stored)
	}
	return nil
}

// XPre is X's pre stage (1.0): the checks that read the sprint's own keys and
// write nothing, in the design's order (lease generation, STOPPED, DROPPING,
// NOTCOORD, COUNTER, the guards, MACHINESTATE). The active epoch the list also
// names is Layer 1's: S.open refuses STALE and EPOCHAHEAD before any phase runs.
// It also validates every field the derivation will read, so that X.plan, which
// has no refusal channel, cannot meet a malformed one, and stores the carry
// XCmds takes.
func XPre(st *State, req *Request, obs *Before) *Refusal {
	carry, ref := xPre(st, req, obs)
	if ref != nil {
		return ref
	}
	xStash.put(st, carry)
	return nil
}

func xPre(st *State, req *Request, obs *Before) (*xCarry, *Refusal) {
	nowMS, err := strconv.ParseInt(string(st.NowMS), 10, 64)
	if err != nil {
		return nil, xRefuse(CodeRequest, RefusalDetail{}, "the call's time %q is not a whole number of milliseconds", st.NowMS)
	}
	if ref := xCheckShape(req); ref != nil {
		return nil, ref
	}
	r := &xRead{k: st.Keys, prefix: st.Prefix, epoch: st.Epoch}
	carry := &xCarry{req: req, obs: obs}

	// Lease generation (1.4.3, T1; E4). A step with a lease part is the lease's
	// own: that part takes or renews it (IT16), so X does not hold it to the
	// generation the loop knew.
	if req.Meta.Tick && req.Lease == nil {
		gen, ok, ref := r.hget(r.sprint(xKeyLease), xFieldLeaseGen)
		if ref != nil {
			return nil, ref
		}
		if !ok || gen != strconv.FormatUint(req.Meta.Gen, 10) {
			name, _, ref := r.hget(r.sprint(xKeyLease), xFieldLeaseName)
			if ref != nil {
				return nil, ref
			}
			if !ok {
				return nil, xRefuse(CodeStaleGen, RefusalDetail{}, "STALEGEN: no lease is held; this loop is not the tick")
			}
			return nil, xRefuse(CodeStaleGen, RefusalDetail{},
				"STALEGEN: the tick's lease is at generation %s, held by %s; this loop is not the tick", gen, name)
		}
	}

	// The clock: STOPPED, R, and (below) MACHINESTATE all read it once.
	var clock xClockState
	needClock := req.Meta.Tick || req.Clock != nil
	for _, g := range req.Body.Guards {
		if g.Kind == XGuardBeatStale || g.Kind == XGuardClock {
			needClock = true
		}
	}
	if needClock {
		var ref *Refusal
		if clock, ref = xReadClock(r, nowMS); ref != nil {
			return nil, ref
		}
	}

	// STOPPED (1.4.3, T2): a tick step that changes a table member is refused
	// while the machine is STOPPED. Verbs, and steps of notes and sprint keys
	// only, apply in either state.
	if req.Meta.Tick && clock.stopped && xTouchesMembers(req) {
		return nil, xRefuse(CodeStopped, RefusalDetail{}, "STOPPED: the machine is STOPPED, since %d, and the tick does not move cards", clock.since)
	}

	// DROPPING (1.3.5, 1.5.4): a card of a stream being dropped or removed is
	// refused, except by that op's own parts.
	streams, byStream, ref := xStreamsTouched(req, obs)
	if ref != nil {
		return nil, ref
	}
	if len(streams) != 0 {
		marks, ref := r.hmget(r.at(xKeyDropping), streams)
		if ref != nil {
			return nil, ref
		}
		for _, s := range streams {
			mark, ok := marks[s]
			if !ok || (req.Body.Op != nil && xOwnsPart(req.Body.Op.ID, mark)) {
				continue
			}
			return nil, xRefuse(CodeDropping, RefusalDetail{RefusalDetail: tset.RefusalDetail{IDs: byStream[s], Rows: []string{s}}},
				"DROPPING: stream %s is being dropped by op %s", s, mark)
		}
	}

	// NOTCOORD (1.5.3, 2.2): a coordinator's verb from another actor.
	if xCoordinatorVerbs[req.Meta.Verb] {
		coord, ok, ref := r.get(r.sprint(xKeyCoordinator))
		if ref != nil {
			return nil, ref
		}
		if !ok || coord != req.Meta.Actor {
			return nil, xRefuse(CodeNotCoord, RefusalDetail{}, "NOTCOORD: %s is a coordinator's verb and %q is not the coordinator", req.Meta.Verb, req.Meta.Actor)
		}
	}

	// COUNTER (1.3.1, U2): the counter as the plan read it, and every placed
	// score below the counter after the step.
	var counter *CounterChange
	if req.Sprint != nil {
		counter = req.Sprint.Counter
	}
	placesScores := false
	for _, e := range req.Body.Entries {
		if e.Table == sprint.Work && (e.Kind == "create" || e.Kind == "move") && len(e.Scores) != 0 {
			placesScores = true
		}
	}
	if counter != nil || placesScores {
		want := map[string]bool{xScoreCounterKey: true}
		if counter != nil {
			for f := range counter.Read {
				want[f] = true
			}
		}
		fields := make([]string, 0, len(want))
		for f := range want {
			fields = append(fields, f)
		}
		sort.Strings(fields)
		stored, ref := xCounterStored(r, fields)
		if ref != nil {
			return nil, ref
		}
		if counter != nil {
			for _, f := range fields {
				if read, ok := counter.Read[f]; ok && stored[f] != read {
					return nil, xRefuse(CodeCounter, RefusalDetail{}, "COUNTER: the counter %s moved since the read (it was %q, it is %q)", f, read, stored[f])
				}
			}
		}
		v, have := stored[xScoreCounterKey]
		if ref := xCounterRises(req, v, have); ref != nil {
			return nil, ref
		}
		if ref := xPlacedScoresBelow(req, v, have); ref != nil {
			return nil, ref
		}
	}

	// XGUARD (1.3.5): the guards on the sprint's keys, in the order given.
	for _, g := range req.Body.Guards {
		if ref := xGuard(r, st, g, obs, clock); ref != nil {
			return nil, ref
		}
	}

	// MACHINESTATE (1.2, A2): a second stop cannot move stopped_since_ms, so a
	// due time is never early; a start on a running machine, and a clear on one
	// that is not STOPPED (errata 1, the addendum: a clear runs only while STOPPED).
	if req.Clock != nil {
		switch req.Clock.Verb {
		case ClockStop:
			if clock.stopped {
				return nil, xRefuse(CodeMachineState, RefusalDetail{}, "MACHINESTATE: the machine is already stopped, since %d", clock.since)
			}
		case ClockStart:
			if !clock.stopped {
				return nil, xRefuse(CodeMachineState, RefusalDetail{}, "MACHINESTATE: the machine is already running")
			}
		case ClockClear:
			if !clock.stopped {
				return nil, xRefuse(CodeMachineState, RefusalDetail{}, "MACHINESTATE: a clear runs only while the machine is STOPPED")
			}
		}
	}

	// What the derivation reads: the quarantine of the step's cards, the orders of
	// the requeued keys, the write epoch; and every indexed field well formed.
	if ref := xPrepareDerivation(r, st, req, obs, carry); ref != nil {
		return nil, ref
	}
	versions, ref := xVersions(r, req)
	if ref != nil {
		return nil, ref
	}
	carry.versions = versions
	carry.probes = r.probes
	carry.readKeys = r.read
	return carry, nil
}

// xVersions reads the version of each table whose cards the step may change
// (a caller entry that creates, moves or removes one of its cards, or an intent,
// which becomes an entry of the work table), in one probe, and gives each one
// more: X.plan writes them to {p}tver@<write epoch> (errata 3 H17: the version
// R17's step is guarded on for the cards of a table it read). A step that
// changes no card moves no version; an entry Layer 1 finds changes nothing
// still moves its table's, which only refuses a guard that could have held. A
// version that is not a whole number below 2^53 is CONFIG.
func xVersions(r *xRead, req *Request) ([]xVersion, *Refusal) {
	seen := map[string]bool{}
	var tables []string
	add := func(t string) {
		if !seen[t] {
			seen[t] = true
			tables = append(tables, t)
		}
	}
	for _, e := range req.Body.Entries {
		if xChanged(e) {
			add(e.Table)
		}
	}
	if len(req.Body.Intents) != 0 {
		add(sprint.Work)
	}
	if len(tables) == 0 {
		return nil, nil
	}
	sort.Strings(tables)
	stored, ref := r.hmget(r.at(xKeyVersion), tables)
	if ref != nil {
		return nil, ref
	}
	out := make([]xVersion, 0, len(tables))
	for _, t := range tables {
		n, ref := xVersionValue(t, stored[t])
		if ref != nil {
			return nil, ref
		}
		out = append(out, xVersion{table: t, to: strconv.FormatUint(n+1, 10)})
	}
	return out, nil
}

// xVersionMax is the most a table's version may hold: the Lua half counts in
// doubles, exact below 2^53.
const xVersionMax = 1<<53 - 2

// xVersionValue is a stored version: 0 for none, CONFIG for one that is not a
// canonical whole number at most xVersionMax.
func xVersionValue(table, v string) (uint64, *Refusal) {
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil || strconv.FormatUint(n, 10) != v || n > xVersionMax {
		return 0, xRefuse(CodeConfig, RefusalDetail{}, "the version of table %s holds %q, not a whole number", table, v)
	}
	return n, nil
}

// xCheckShape holds the parts of a request X reads to their shapes, REQUEST for
// the caller's fault (1.0): a guard of a kind 7 does not have, or without what its
// kind names; a counter change that sets a field it did not read, or a score that
// is not a whole number (a compare-and-set names everything it writes, U2); an
// agenda key that names nothing, or that one step both finishes and requeues (the
// requeue would be undone by the finish: owed work lost).
func xCheckShape(req *Request) *Refusal {
	bad := func(format string, args ...any) *Refusal {
		return xRefuse(CodeRequest, RefusalDetail{}, format, args...)
	}
	for _, g := range req.Body.Guards {
		switch g.Kind {
		case XGuardMemberUp, XGuardBeatStale, XGuardStranger:
			if g.Member == "" {
				return bad("a %s guard names no member", g.Kind)
			}
		case XGuardDue:
			if g.Key == "" || (g.Score < 0 && g.Score != XGuardAbsent) {
				return bad("a due guard names no entry, or a score that is neither a time nor XGuardAbsent")
			}
		case XGuardHold:
			if g.Member == "" || !strings.Contains(g.Key, "=") {
				return bad("a hold guard names no subject, or a key that is not <type>|<cause>=<value>")
			}
		case XGuardClock:
			known := false
			for _, f := range xClockFields {
				known = known || f == g.Key
			}
			if !known || (g.Score < 0 && g.Score != XGuardAbsent) {
				return bad("a clock guard names %q, which is not a clock field, or a score that is neither a time nor XGuardAbsent", g.Key)
			}
		case XGuardCoordinator:
		case XGuardSet:
			if _, ok := xSetGuardOf(g); !ok {
				return bad("a set guard is not a zguard over a sprint index with bounds and a count: %q", g.Key)
			}
		case XGuardVersion:
			if g.Key == "" || strings.ContainsAny(g.Key, " \t\n\v\f\r@") || g.Score < 0 || g.Score > xVersionMax {
				return bad("a version guard names %q, which is not a table, or a version that is not a whole number", g.Key)
			}
		case XGuardCounter:
			if (g.Key != xFieldNextScore && g.Key != xFieldNextStream) || g.Score < 0 {
				return bad("a counter guard names %q, which is not score or streams, or a value below zero", g.Key)
			}
		default:
			return bad("%q is not a kind of guard", g.Kind)
		}
	}
	for _, q := range req.Body.Quarantine {
		if q.ID == "" {
			return bad("a quarantine names no card")
		}
	}
	if c := req.Sprint; c != nil && c.Counter != nil {
		for _, f := range xSortedKeys(c.Counter.Set) {
			if _, read := c.Counter.Read[f]; !read {
				return bad("the counter sets %s and did not read it: a counter change guards every field it writes", f)
			}
		}
		if v, ok := c.Counter.Set[xScoreCounterKey]; ok {
			if _, ok := xWholeScore(v); !ok {
				return bad("the score counter is set to %q, which is not a whole number of at most %d digits", v, xScoreDigits)
			}
		}
	}
	requeued := make(map[string]bool, len(req.Body.Requeue))
	for _, k := range req.Body.Requeue {
		if k == "" {
			return bad("an agenda key is requeued and names nothing")
		}
		requeued[k] = true
	}
	for _, k := range req.Body.Done {
		if k == "" {
			return bad("an agenda key is finished and names nothing")
		}
		if requeued[k] {
			return bad("the agenda key %s is both finished and requeued in one step", k)
		}
	}
	return nil
}

// xWholeScore is a whole number of at most xScoreDigits digits, as the score
// counter holds it.
func xWholeScore(v string) (float64, bool) {
	if v == "" || len(v) > xScoreDigits {
		return 0, false
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return float64(n), true
}

// xGuard checks one XGuard against the real keys.
func xGuard(r *xRead, st *State, g XGuard, obs *Before, clock xClockState) *Refusal {
	fail := func(format string, args ...any) *Refusal {
		return xRefuse(CodeXGuard, RefusalDetail{}, "XGUARD: "+format, args...)
	}
	switch g.Kind {
	case XGuardMemberUp:
		epoch, err := strconv.ParseUint(string(st.Epoch), 10, 64)
		if err != nil {
			return xRefuse(CodeRequest, RefusalDetail{}, "the epoch %q is not a whole number", st.Epoch)
		}
		id := sprint.StoredID(sprint.CtlID(g.Member), epoch)
		rec, ok := obs.Record(sprint.Fleet, id)
		if !ok {
			return xRefuse(CodeConfig, RefusalDetail{}, "X.pre was not given the control card of member %s", g.Member)
		}
		if f := rec.Fields[xFieldStatus]; !rec.Exists || !f.Present || f.Value != sprint.Up {
			return fail("member %s is not up", g.Member)
		}
	case XGuardBeatStale:
		score, ok, ref := r.zscore(r.at(xKeyDue), "beat:"+g.Member)
		if ref != nil {
			return ref
		}
		if ok && score > float64(clock.r()) {
			return fail("a beat of member %s since the read moved its beat to %s, above R (%d)", g.Member, xScore(score), clock.r())
		}
	case XGuardDue:
		set := xKeyDue
		if strings.HasPrefix(g.Key, "cut:") {
			set = xKeyCut
		}
		score, ok, ref := r.zscore(r.at(set), g.Key)
		if ref != nil {
			return ref
		}
		switch {
		case g.Score == XGuardAbsent && ok:
			return fail("the entry %s was absent when read and is at %s now", g.Key, xScore(score))
		case g.Score != XGuardAbsent && (!ok || score != float64(g.Score)):
			return fail("the entry %s moved since the read (it was %d)", g.Key, g.Score)
		}
	case XGuardHold:
		i := strings.LastIndex(g.Key, "=")
		field, want := g.Key[:i], g.Key[i+1:]
		got, ok, ref := r.hget(r.at(xKeyJopen+g.Member), field)
		if ref != nil {
			return ref
		}
		if !ok || got != want {
			return fail("the field %s of %s no longer holds %s", field, g.Member, want)
		}
	case XGuardClock:
		if got := clock.value(g.Key); got != g.Score {
			return fail("the clock's %s moved since the read", g.Key)
		}
	case XGuardCoordinator:
		got, _, ref := r.get(r.sprint(xKeyCoordinator))
		if ref != nil {
			return ref
		}
		if got != g.Member {
			return fail("the coordinator is %q now, not %q", got, g.Member)
		}
	case XGuardStranger:
		got, ok, ref := r.hget(r.sprint(xKeyStrangers), g.Member)
		if ref != nil {
			return ref
		}
		if ok && got == xNoticed {
			return fail("machine %s has been noticed already", g.Member)
		}
	case XGuardSet:
		sg, _ := xSetGuardOf(g)
		key := r.at(sg.Key)
		r.probes++
		if ref := r.wrongType(key, kindZSet); ref != nil {
			return ref
		}
		n := 0
		for _, p := range r.k.ks.zpairs(key) {
			if inBounds(p.score, sg.Min, sg.Max) {
				n++
			}
		}
		if sg.AtLeast != nil && n < *sg.AtLeast || sg.AtMost != nil && n > *sg.AtMost {
			return xRefuse("RANGECOUNT", RefusalDetail{RefusalDetail: tset.RefusalDetail{Cells: []string{key}}},
				"RANGECOUNT: %s holds %d members in [%s, %s] since the read", sg.Key, n, sg.Min, sg.Max)
		}
	case XGuardVersion:
		stored, ref := r.hmget(r.at(xKeyVersion), []string{g.Key})
		if ref != nil {
			return ref
		}
		n, ref := xVersionValue(g.Key, stored[g.Key])
		if ref != nil {
			return ref
		}
		if n != uint64(g.Score) {
			return fail("the version of table %s is %d now, read as %d", g.Key, n, g.Score)
		}
	case XGuardCounter:
		got, ok, ref := r.hget(r.at(xKeyNext), g.Key)
		if ref != nil {
			return ref
		}
		if !ok {
			got = "0"
		} else if n, err := strconv.ParseUint(got, 10, 64); err != nil || strconv.FormatUint(n, 10) != got {
			return xRefuse(CodeConfig, RefusalDetail{}, "the counter's %s holds %q, not a whole number", g.Key, got)
		}
		if want := strconv.FormatInt(g.Score, 10); got != want {
			return fail("the counter's %s is %s now, read as %s", g.Key, got, want)
		}
	}
	return nil
}

// xSetIndexes are the sprint indexes a set guard may count (1.3.1): the four
// indexes of a stream.
var xSetIndexes = []string{sprint.IndexSent, sprint.IndexElig, sprint.IndexFresh, sprint.IndexAgain}

// xSetCountMax is the largest count bound of a set guard: 2^53 - 1, the
// largest integer the Lua's numbers hold exactly.
const xSetCountMax = 1<<53 - 1

// xSetGuardOf is a set guard's own shape (sprint.SetGuard, kind zguard): a
// sprint index of a stream, two bounds in the store's grammar, and at least one
// count bound, the lower not above the upper, each at most 2^53 - 1 (a count
// the Lua holds exactly). A field beside kind, key, min, max, atleast and
// atmost (an rcount's table or cells among them) is refused, as the Lua's
// set_guard_of refuses it.
func xSetGuardOf(g XGuard) (sprint.SetGuard, bool) {
	var sg sprint.SetGuard
	var shape struct {
		Kind    string `json:"kind"`
		Key     string `json:"key"`
		Min     string `json:"min"`
		Max     string `json:"max"`
		AtLeast *int   `json:"atleast"`
		AtMost  *int   `json:"atmost"`
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(g.Key), &fields); err != nil {
		return sg, false
	}
	for f := range fields { // exact names: the Go decoder alone folds case
		switch f {
		case "kind", "key", "min", "max", "atleast", "atmost":
		default:
			return sg, false
		}
	}
	d := json.NewDecoder(strings.NewReader(g.Key))
	d.DisallowUnknownFields()
	if err := d.Decode(&shape); err != nil || d.More() || shape.Kind != sprint.GuardZGuard {
		return sg, false
	}
	sg = sprint.SetGuard{Kind: shape.Kind, Key: shape.Key, Min: shape.Min, Max: shape.Max, AtLeast: shape.AtLeast, AtMost: shape.AtMost}
	for _, b := range []*int{sg.AtLeast, sg.AtMost} {
		if b != nil && *b > xSetCountMax {
			return sg, false
		}
	}
	idx, stream, ok := strings.Cut(sg.Key, ":")
	if !ok || !sprint.ValidID(stream) {
		return sg, false
	}
	known := false
	for _, x := range xSetIndexes {
		known = known || x == idx
	}
	if _, _, ok1 := parseBound(sg.Min); !known || !ok1 {
		return sg, false
	}
	if _, _, ok2 := parseBound(sg.Max); !ok2 {
		return sg, false
	}
	if sg.AtLeast == nil && sg.AtMost == nil || sg.AtLeast != nil && *sg.AtLeast < 0 || sg.AtMost != nil && *sg.AtMost < 0 ||
		sg.AtLeast != nil && sg.AtMost != nil && *sg.AtLeast > *sg.AtMost {
		return sg, false
	}
	return sg, true
}

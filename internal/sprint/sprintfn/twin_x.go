package sprintfn

import (
	"container/list"
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
//	            (2.3, R11's cut clock and R14's phase 1).
//	hold        Member: the subject. Key: "<type>|<cause>=<value>", the field of
//	            {p}jopen:<subject> and what it holds (2.3, R13: "h<note>").
//	clock       Key: one of the five clock fields. Score: its value as read, or
//	            XGuardAbsent for an empty or absent one (2.3, R17).
//	coordinator Member: the coordinator as the verb read it, "" for none
//	            (init --coordinator, 3).
//	stranger    Member: the machine. {p}strangers[Member] is not yet noticed
//	            (2.3, R1).
const (
	XGuardMemberUp    = "memberup"
	XGuardBeatStale   = "beatstale"
	XGuardDue         = "due"
	XGuardHold        = "hold"
	XGuardClock       = "clock"
	XGuardCoordinator = "coordinator"
	XGuardStranger    = "stranger"
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
	probes      int // the store reads X.pre made, for the coster's check
}

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
type xRead struct {
	k      *Keys
	prefix string
	epoch  tset.Decimal
	probes int
}

// sprint is the sprint-level key {p}name.
func (r *xRead) sprint(name string) string { return r.prefix + "sprint:" + name }

// at is the per-epoch key {p}name@e, at the request epoch.
func (r *xRead) at(name string) string { return r.sprint(name) + "@" + string(r.epoch) }

func (r *xRead) wrongType(key, want string) *Refusal {
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

// xProbesFor is the probes a batched read of n members costs: one command of at
// most xPiece members, so one probe a piece (and one for none, which the store
// does not send: the reader asks nothing of an empty list, see xRead.hmget).
func xProbesFor(n int) int { return (n + xPiece - 1) / xPiece }

// hmget is several fields of one hash, in commands of at most xPiece fields: one
// probe each.
func (r *xRead) hmget(key string, fields []string) (map[string]string, *Refusal) {
	r.probes += xProbesFor(len(fields))
	if ref := r.wrongType(key, kindHash); ref != nil {
		return nil, ref
	}
	out := make(map[string]string, len(fields))
	for _, f := range fields {
		if v, ok := r.k.HGet(key, f); ok {
			out[f] = v
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
	indexFields := sprint.IndexFields()
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
	limit, err := strconv.ParseFloat(counter, 64)
	if haveCounter && err != nil {
		return xRefuse(CodeConfig, RefusalDetail{}, "the score counter holds %q, not a number", counter)
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
	carry.probes = r.probes
	return carry, nil
}

// xCheckShape holds the parts of a request X reads to their shapes, REQUEST
// for the caller's fault (1.0): a guard of a kind 7 does not have, or without
// what its kind names.
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
		default:
			return bad("%q is not a kind of guard", g.Kind)
		}
	}
	for _, q := range req.Body.Quarantine {
		if q.ID == "" {
			return bad("a quarantine names no card")
		}
	}
	return nil
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
		if ok && int64(score) > clock.r() {
			return fail("a beat of member %s since the read moved its beat to %d, above R (%d)", g.Member, int64(score), clock.r())
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
			return fail("the entry %s was absent when read and is at %d now", g.Key, int64(score))
		case g.Score != XGuardAbsent && (!ok || int64(score) != g.Score):
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
	}
	return nil
}

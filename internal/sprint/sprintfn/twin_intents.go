package sprintfn

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The derive phase: intents to Layer 1 entries (upper design version 2.1,
// 1.3.3; item IT14).
//
// Four kinds of effect cannot be planned from a read, because two plans from
// one read would compute them from the same stale value, or because the value
// belongs to the moment of apply: waitfor, needmet, needgone and waive. They
// travel as intents, and this phase turns each into what the real before-state
// says: the field changes of the waiting cards (one entry a card, folded, with
// revs equal to the revision just read, so Layer 1's guards agree with it by
// construction), the commands on the sprint's own keys wait:<n> and missing,
// and the requests to J for the judgments that open and close. It writes
// nothing: every read is of the call's one state, and the commands ride the
// commit list at X.plan.
//
// The phase interface of 8.0 and IT12 cannot carry this item in five places.
// Each is met in this file, in the smallest adapter, and listed as an open
// question on the pull request:
//
//  1. Derive returns entries and notes, and has no place for the commands on
//     wait:<n> and missing. The adapter carries them from derive to X.plan
//     (Twin.UseIntents wraps the twin's XCmds, and the Lua keeps them on ctx).
//  2. A waitfor walk reads records it cannot name in advance, and the phase
//     gets only what S.before read. The adapter reads them from the twin's
//     table twin, as S.before does in the store.
//  3. An admission's open "goes into the add's own create entry", and the
//     phase sees no entries and may only append (L1 1.1: "a trusted preplan
//     may append entries and notes"; a second entry on the created card is
//     TWICE). The narrower reading taken here: the add's create entry carries
//     the card's needs and its open as the builder read them, and derive
//     refuses XGUARD when the real count differs, so the verb plans again on a
//     fresh read. Derive never writes a card its step creates.
//  4. The phase is handed the intents and not the request, so the adapter
//     keeps the request X.pre saw, to find the create entry of a waitfor.
//  5. Phases.Before is one function, and this phase needs fields for the ids
//     its intents name: it reads its own, through the same cache.
//
// The derive phase is the twin of sprint_intents.lua; a test runs both on the
// same scenarios and holds their entries, notes, commands and refusals equal.

// The intent kinds of 1.3.3, in the words Intent.Kind carries.
const (
	// IntentWaitFor is add's admission of a card that waits for needs.
	IntentWaitFor = "waitfor"
	// IntentNeedMet is R4's report that a need landed, for the cards waiting for it.
	IntentNeedMet = "needmet"
	// IntentNeedGone is R4's report that a need was removed, for the cards waiting for it.
	IntentNeedGone = "needgone"
	// IntentWaive is ack's waiver of a blocked judgment's needs.
	IntentWaive = "waive"
)

// The bounds of the derive phase, each with the section that sets it.
const (
	// WalkRecordsMax is the most records the needs walk of one step reads
	// (1.3.3: "at most 2,000 records a step"); a walk that would read one more
	// is refused XGUARD naming its head.
	WalkRecordsMax = 2000
	// NeedsPerCardMax is the most needs one card names (3: "add refuses more
	// than 64 needs on a card"); a waitfor or a waive naming more is REQUEST.
	NeedsPerCardMax = 64
	// IntentWaitersMax is the most waiters the needmet and needgone intents of
	// one step name together: the chunk (1.0, StepChunk); more is REQUEST.
	IntentWaitersMax = 2000
)

const (
	// deriveWaitScore is the score of a member of wait:<n>: 1.3.1 gives them
	// none, a sorted set needs one, and equal scores keep them in member order.
	deriveWaitScore = "0"
	// deriveChainNamed is how many cards of a cycle's chain its message names
	// before "..." (1.3.3: "through c1, c2, ...").
	deriveChainNamed = 8
	// The card fields the phase reads and writes (1.3.1, "Card fields the rules read").
	deriveFieldNeeds  = "needs"
	deriveFieldOpen   = "open"
	deriveFieldWaived = "waived"
	// The clock's fields R is computed from (1.2).
	deriveClockStopped = "stopped_ms"
	deriveClockSince   = "stopped_since_ms"
	// deriveWalkBudget names the walk's bound in a refusal's detail.
	deriveWalkBudget = "walk_records"
)

// The text of the requests to J, by judgment type: one sentence about the
// need, since a note names every waiter of its step (1.3.4).
const (
	deriveTextMissing = "%s has no record yet; the cards that wait for it wait for it to be created"
	deriveTextDropped = "%s was dropped; the cards that wait for it cannot go on until the coordinator waives it or drops them"
)

// derivePlan is what the derive phase decided for one step: the entries it
// appends to the request's (none names a card the request names), the requests
// it gives J, and the commands on the sprint's own keys that X.plan puts in
// the commit list.
type derivePlan struct {
	Entries []tset.Entry
	Notes   []NoteReq
	Cmds    []Cmd
}

// deriveWorld is what the phase reads besides the intents and the sprint's
// keys: the work table's records, and the cards the step itself creates.
type deriveWorld interface {
	// records reads the work table's records of ids, each with the
	// application fields named (an explicit empty list reads none), from the
	// call's one state. A record that does not exist comes back with Exists
	// false. The result is aligned with ids.
	records(ids, fields []string) ([]tset.MemberRecord, *Refusal)
	// created says the step's entries create the card, and gives its column and
	// the fields its create entry sets.
	created(card string) (createdCard, bool)
}

// createdCard is a card a create entry of the work table makes.
type createdCard struct {
	col    string
	fields map[string]string
}

// createdIn finds the create entry of the work table that names the card: the
// column it is created in, and its fields, the shared set overridden by the
// card's own each.
func createdIn(entries []tset.Entry, card string) (createdCard, bool) {
	for _, e := range entries {
		if e.Kind != "create" || e.Table != sprint.Work {
			continue
		}
		for i, id := range e.IDs {
			if id != card {
				continue
			}
			fields := make(map[string]string, len(e.Set))
			for k, v := range e.Set {
				fields[k] = v
			}
			if i < len(e.Each) {
				for k, v := range e.Each[i] {
					fields[k] = v
				}
			}
			col := e.To
			if at := strings.LastIndexByte(col, ':'); at >= 0 {
				col = col[at+1:]
			}
			return createdCard{col: col, fields: fields}, true
		}
	}
	return createdCard{}, false
}

// intentKey is a per-epoch key of the sprint: {p}<name>@<e> (1.0, "Keys").
func intentKey(st *State, name string) string { return st.Names.Key(name) + "@" + string(st.Epoch) }

// deriveRefuse is a refusal of the derive phase: its code, the ids it names
// and its message, to which "; nothing was changed" is added as Layer 1's
// refusals have it.
func deriveRefuse(code string, ids []string, format string, args ...any) *Refusal {
	ref := refuse(PhaseDerive, code, RefusalDetail{RefusalDetail: tset.RefusalDetail{IDs: ids}})
	ref.Message = fmt.Sprintf(format, args...) + "; nothing was changed"
	return ref
}

// dedupIDs is the ids without repeats, in the order they first appear.
func dedupIDs(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func containsID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// fieldOf is a record's field as text: empty when it is absent or not read.
func fieldOf(rec *tset.MemberRecord, name string) string {
	if f, ok := rec.Fields[name]; ok && f.Present {
		return f.Value
	}
	return ""
}

// openOf is a waiting card's open: the number of its unmet needs and open
// judgments (I2). An absent or empty field is 0.
func openOf(rec *tset.MemberRecord) (int, bool) {
	v := fieldOf(rec, deriveFieldOpen)
	if v == "" {
		return 0, true
	}
	n, err := strconv.Atoi(v)
	return n, err == nil && n >= 0
}

// isWaiting says a record exists and is placed in the work table's waiting column.
func isWaiting(rec *tset.MemberRecord) bool {
	return rec.Exists && rec.Place != nil && rec.Place.Col == string(sprint.Waiting)
}

// deriveOp is what the step does to one key with one command: the members of
// a ZREM, or the score and member pairs of a ZADD.
type deriveOp struct {
	cmd, key string
	members  []string
	scores   []string
}

// deriveNote is one request to J, grouped by its operation, type and cause: a
// note names every subject of its step (1.3.4).
type deriveNote struct {
	op, typ, cause string
	subjects       []string
}

// deriveFold is what the step does to one existing card's fields: open lowered
// by delta (each unmet need that is met or waived, once), and needs waived.
type deriveFold struct {
	delta  int
	waived []string
}

// deriveRun is one run of the phase: what it read, and what it has decided.
type deriveRun struct {
	st     *State
	w      deriveWorld
	keys   *Keys
	recs   map[string]*tset.MemberRecord
	own    map[string][]string // the needs of each card a waitfor of this step admits
	walked map[string]bool     // the ids the walk looked up: the records it counts

	removed map[string]map[string]bool // per wait key: the members of the store this step removes
	added   map[string]map[string]bool // per wait key: the members this step adds

	folds     map[string]*deriveFold
	foldOrder []string
	ops       []*deriveOp
	opIndex   map[string]*deriveOp
	notes     []*deriveNote
	noteIndex map[string]*deriveNote

	missingAdded  map[string]bool // the needs this step enters into missing
	waivedMissing []string        // the missing needs a waive took a waiter of, in order
	r             int64
	rKnown        bool
}

func newDeriveRun(st *State, w deriveWorld) *deriveRun {
	return &deriveRun{st: st, w: w, keys: st.Keys, recs: map[string]*tset.MemberRecord{}, own: map[string][]string{},
		walked: map[string]bool{}, removed: map[string]map[string]bool{}, added: map[string]map[string]bool{},
		folds: map[string]*deriveFold{}, opIndex: map[string]*deriveOp{}, noteIndex: map[string]*deriveNote{},
		missingAdded: map[string]bool{}}
}

// deriveOver is the derive phase over a world: the intents of one step, in
// order, each decided on the state the world and the sprint's keys show. The
// first refusal ends it, and nothing it decided is used.
func deriveOver(st *State, w deriveWorld, in []Intent) (derivePlan, *Refusal) {
	if st == nil || st.Keys == nil || w == nil {
		return derivePlan{}, refuse(PhaseDerive, CodeConfig, RefusalDetail{})
	}
	if ref := deriveCheck(in); ref != nil {
		return derivePlan{}, ref
	}
	r := newDeriveRun(st, w)
	for _, it := range in {
		if it.Kind == IntentWaitFor {
			r.own[it.Card] = dedupIDs(it.Needs)
		}
	}
	for _, it := range in {
		var ref *Refusal
		switch it.Kind {
		case IntentWaitFor:
			ref = r.waitFor(it)
		case IntentNeedMet:
			ref = r.needMet(it)
		case IntentNeedGone:
			ref = r.needGone(it)
		case IntentWaive:
			ref = r.waive(it)
		}
		if ref != nil {
			return derivePlan{}, ref
		}
	}
	return r.finish()
}

// deriveCheck is the static shape of a step's intents: a known kind, the ids
// it needs, the bounds of 3 and 1.0, and one waitfor a card.
func deriveCheck(in []Intent) *Refusal {
	waiters := 0
	admitted := map[string]bool{}
	for i, it := range in {
		bad := func(format string, args ...any) *Refusal {
			return deriveRefuse(CodeRequest, nil, "intent %d (%s): %s", i, it.Kind, fmt.Sprintf(format, args...))
		}
		switch it.Kind {
		case IntentWaitFor, IntentWaive:
			if it.Card == "" {
				return bad("names no card")
			}
			needs := dedupIDs(it.Needs)
			if len(needs) == 0 || len(needs) > NeedsPerCardMax {
				return bad("names %d needs; a card names 1 to %d", len(needs), NeedsPerCardMax)
			}
			if containsID(needs, "") {
				return bad("names a need with no id")
			}
			if it.Kind == IntentWaitFor {
				if admitted[it.Card] {
					return bad("admits %s twice in one step", it.Card)
				}
				admitted[it.Card] = true
			}
		case IntentNeedMet, IntentNeedGone:
			if it.Need == "" {
				return bad("names no need")
			}
			if containsID(it.Waiters, "") {
				return bad("names a waiter with no id")
			}
			waiters += len(dedupIDs(it.Waiters))
			if waiters > IntentWaitersMax {
				return bad("the step's intents name %d waiters; at most %d", waiters, IntentWaitersMax)
			}
		default:
			return bad("is not a kind of intent (waitfor, needmet, needgone, waive)")
		}
	}
	return nil
}

// fetch reads the records of ids, with the fields named, that the run has not
// read with them, in one call of the world.
func (r *deriveRun) fetch(ids, fields []string) *Refusal {
	var ask []string
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if rec := r.recs[id]; rec != nil && (!rec.Exists || hasFields(rec, fields)) {
			continue
		}
		ask = append(ask, id)
	}
	if len(ask) == 0 {
		return nil
	}
	got, ref := r.w.records(ask, append([]string{}, fields...))
	if ref != nil {
		return ref
	}
	if len(got) != len(ask) {
		return refuse(PhaseDerive, CodeConfig, RefusalDetail{})
	}
	for i, id := range ask {
		rec := got[i]
		rec.ID = id
		cur := r.recs[id]
		if cur == nil {
			cp := rec
			cp.Fields = make(map[string]tset.FieldValue, len(rec.Fields))
			for f, v := range rec.Fields {
				cp.Fields[f] = v
			}
			r.recs[id] = &cp
			continue
		}
		if cur.Fields == nil {
			cur.Fields = map[string]tset.FieldValue{}
		}
		for f, v := range rec.Fields {
			cur.Fields[f] = v
		}
	}
	return nil
}

func hasFields(rec *tset.MemberRecord, fields []string) bool {
	for _, f := range fields {
		if _, ok := rec.Fields[f]; !ok {
			return false
		}
	}
	return true
}

// inWait says the card is in wait:<n> as the store has it and this step has
// not yet removed it. A card this step admits is not: no other intent names it.
func (r *deriveRun) inWait(key, card string) bool {
	if r.removed[key][card] {
		return false
	}
	_, ok := r.keys.ZScore(key, card)
	return ok
}

// waitSize is how many cards wait:<n> holds as this step leaves it.
func (r *deriveRun) waitSize(key string) int {
	return r.keys.ZCard(key) + len(r.added[key]) - len(r.removed[key])
}

// quarantined says the card is in {p}quarantine@e: every derivation leaves it out.
func (r *deriveRun) quarantined(card string) bool {
	_, ok := r.keys.HGet(intentKey(r.st, "quarantine"), card)
	return ok
}

// judgmentOpen says the card has an open judgment of the type for the cause:
// the field <type>|<cause> of {p}jopen:<card>@e (1.3.1).
func (r *deriveRun) judgmentOpen(card, typ, cause string) bool {
	_, ok := r.keys.HGet(intentKey(r.st, "jopen:"+card), typ+"|"+cause)
	return ok
}

func (r *deriveRun) op(cmd, key string) *deriveOp {
	id := cmd + "\x00" + key
	o := r.opIndex[id]
	if o == nil {
		o = &deriveOp{cmd: cmd, key: key}
		r.opIndex[id] = o
		r.ops = append(r.ops, o)
	}
	return o
}

// addWait puts the card in wait:<n>, unless it is there.
func (r *deriveRun) addWait(need, card string) {
	key := intentKey(r.st, "wait:"+need)
	if _, there := r.keys.ZScore(key, card); there || r.added[key][card] {
		return
	}
	o := r.op("ZADD", key)
	o.scores = append(o.scores, deriveWaitScore)
	o.members = append(o.members, card)
	if r.added[key] == nil {
		r.added[key] = map[string]bool{}
	}
	r.added[key][card] = true
}

// removeWait takes the card out of wait:<n>; the caller has seen it there.
func (r *deriveRun) removeWait(need, card string) {
	key := intentKey(r.st, "wait:"+need)
	o := r.op("ZREM", key)
	o.members = append(o.members, card)
	if r.removed[key] == nil {
		r.removed[key] = map[string]bool{}
	}
	r.removed[key][card] = true
}

// note asks J to open or close the judgment of the type for the cause on the
// subject.
func (r *deriveRun) note(op, typ, cause, subject string) {
	id := op + "\x00" + typ + "\x00" + cause
	n := r.noteIndex[id]
	if n == nil {
		n = &deriveNote{op: op, typ: typ, cause: cause}
		r.noteIndex[id] = n
		r.notes = append(r.notes, n)
	}
	if !containsID(n.subjects, subject) {
		n.subjects = append(n.subjects, subject)
	}
}

func (r *deriveRun) fold(card string) *deriveFold {
	f := r.folds[card]
	if f == nil {
		f = &deriveFold{}
		r.folds[card] = f
		r.foldOrder = append(r.foldOrder, card)
	}
	return f
}

// running is R, the running time, from the call's one TIME and the clock's
// fields (1.2): t - stopped_ms - (stopped_since_ms is "" ? 0 : t - stopped_since_ms).
func (r *deriveRun) running() (int64, *Refusal) {
	if r.rKnown {
		return r.r, nil
	}
	clock := r.st.Names.Key("clock")
	now, err := strconv.ParseInt(string(r.st.NowMS), 10, 64)
	if err != nil {
		return 0, deriveRefuse(CodeConfig, nil, "the call's time %q is not a whole number", r.st.NowMS)
	}
	var stopped, since int64
	hasSince := false
	if v, ok := r.keys.HGet(clock, deriveClockStopped); ok && v != "" {
		if stopped, err = strconv.ParseInt(v, 10, 64); err != nil {
			return 0, deriveRefuse(CodeConfig, nil, "the clock's %s is %q, not a whole number", deriveClockStopped, v)
		}
	}
	if v, ok := r.keys.HGet(clock, deriveClockSince); ok && v != "" {
		hasSince = true
		if since, err = strconv.ParseInt(v, 10, 64); err != nil {
			return 0, deriveRefuse(CodeConfig, nil, "the clock's %s is %q, not a whole number", deriveClockSince, v)
		}
	}
	r.r = now - stopped
	if hasSince {
		r.r -= now - since
	}
	r.rKnown = true
	return r.r, nil
}

// deriveCodeDrift is Layer 1's DRIFT (L1 8): a stored invariant is broken. The
// phase names it for a card a sprint key says waits and whose record does not
// say so; 1.3.5 quarantines a card named by it.
const deriveCodeDrift = "DRIFT"

func drift(card, why string) *Refusal {
	return deriveRefuse(deriveCodeDrift, []string{card}, "%s %s", card, why)
}

// The state of one need of a waitfor, on the real before-state (1.3.3).
const (
	needOpen    = iota // a card of the table that has not landed: the waiter waits for it
	needMissing        // no record: the waiter waits for it to be created
	needDropped        // a record with no place: removed, the waiter is blocked
	needLanded         // landed: nothing to wait for
)

// waitFor admits a card that waits for needs (1.3.3). It first walks the
// needs graph, refusing a cycle and a walk past the bound, then enters the
// card in wait:<n> of each need that is open or has no record, enters a need
// with no record in missing, and asks J to open the judgments. The card is
// created by this step, and its create entry carries its needs and its open;
// derive checks them against the real state. A need the same step creates is
// open: it will be a card of the table.
func (r *deriveRun) waitFor(in Intent) *Refusal {
	w, needs := in.Card, dedupIDs(in.Needs)
	made, ok := r.w.created(w)
	if !ok {
		return deriveRefuse(CodeRequest, []string{w}, "%s waits for %s and no create entry of the step makes it", w, strings.Join(needs, ", "))
	}
	if made.col != string(sprint.Waiting) {
		return deriveRefuse(CodeRequest, []string{w}, "%s has needs and is created in %s; a card with needs is created in waiting", w, made.col)
	}
	for _, n := range needs {
		if ref := r.walk(w, n); ref != nil {
			return ref
		}
	}
	if ref := r.fetch(needs, []string{}); ref != nil {
		return ref
	}
	state := make([]int, len(needs))
	open := 0
	for i, n := range needs {
		rec := r.recs[n]
		switch _, own := r.w.created(n); {
		case own:
			state[i] = needOpen
		case !rec.Exists:
			state[i] = needMissing
		case rec.Place == nil:
			state[i] = needDropped
		case rec.Place.Col == string(sprint.Landed):
			state[i] = needLanded
		default:
			state[i] = needOpen
		}
		if state[i] != needLanded {
			open++
		}
	}
	named := sprint.Split(made.fields[deriveFieldNeeds])
	for _, n := range needs {
		if !containsID(named, n) {
			return deriveRefuse(CodeRequest, []string{w}, "%s waits for %s and its create entry does not name it in needs", w, n)
		}
	}
	have := 0
	if v := made.fields[deriveFieldOpen]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return deriveRefuse(CodeRequest, []string{w}, "%s is created with open %q, not a whole number", w, v)
		}
		have = n
	}
	if have != open {
		return deriveRefuse(CodeXGuard, []string{w}, "the needs of %s changed since the plan read them: its create says open %d, the needs say %d", w, have, open)
	}
	for i, n := range needs {
		switch state[i] {
		case needOpen:
			r.addWait(n, w)
		case needMissing:
			r.addWait(n, w)
			if ref := r.enterMissing(n); ref != nil {
				return ref
			}
			r.note("open", sprint.NMissingNeed, n, w)
		case needDropped:
			r.note("open", sprint.NBlocked, n, w)
		}
	}
	return nil
}

// enterMissing enters a need that has no record into missing at R, once: the
// store has no ZADD NX, so the phase reads the score and decides (errata 2,
// item 5).
func (r *deriveRun) enterMissing(n string) *Refusal {
	key := intentKey(r.st, "missing")
	if _, ok := r.keys.ZScore(key, n); ok || r.missingAdded[n] {
		return nil
	}
	at, ref := r.running()
	if ref != nil {
		return ref
	}
	o := r.op("ZADD", key)
	o.scores = append(o.scores, strconv.FormatInt(at, 10))
	o.members = append(o.members, n)
	r.missingAdded[n] = true
	return nil
}

// walk is the cycle walk of a waitfor (1.3.3): from the need start, through
// the needs of every open waiting card it reaches (the cards this step
// admits included), looking for w. A need that reaches w is refused XGUARD
// naming the chain, and a walk that would read more than WalkRecordsMax
// records in the step is refused XGUARD naming its head, the need it began at.
func (r *deriveRun) walk(w, start string) *Refusal {
	parent := map[string]string{start: ""}
	if start == w {
		return r.cycle(parent, start, w)
	}
	stack := []string{start}
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		edges, ref := r.edges(c, start)
		if ref != nil {
			return ref
		}
		for i := len(edges) - 1; i >= 0; i-- {
			e := edges[i]
			if _, seen := parent[e]; seen {
				continue
			}
			parent[e] = c
			if e == w {
				return r.cycle(parent, start, w)
			}
			stack = append(stack, e)
		}
	}
	return nil
}

// edges are the needs the walk goes on through from the card c: those of a
// card this step admits, or of an open waiting card. Any other card ends the
// walk there. Looking a card up counts one of the step's WalkRecordsMax.
func (r *deriveRun) edges(c, head string) ([]string, *Refusal) {
	if needs, ok := r.own[c]; ok {
		return needs, nil
	}
	if !r.walked[c] {
		if len(r.walked) >= WalkRecordsMax {
			limit, actual := int64(WalkRecordsMax), int64(WalkRecordsMax+1)
			ref := deriveRefuse(CodeXGuard, []string{head}, "the needs walk from %s went past %d records; a needs cycle could not be ruled out", head, WalkRecordsMax)
			ref.Detail.Budget, ref.Detail.Limit, ref.Detail.Actual = deriveWalkBudget, &limit, &actual
			return nil, ref
		}
		r.walked[c] = true
	}
	if ref := r.fetch([]string{c}, []string{deriveFieldNeeds, deriveFieldOpen}); ref != nil {
		return nil, ref
	}
	rec := r.recs[c]
	if !isWaiting(rec) {
		return nil, nil
	}
	open, ok := openOf(rec)
	if !ok {
		return nil, drift(c, "has an open that is not a whole number")
	}
	if open == 0 {
		return nil, nil
	}
	return sprint.Split(fieldOf(rec, deriveFieldNeeds)), nil
}

// cycle is the refusal of a need that reaches its waiter: the chain from the
// need to the waiter, read back through the walk's parents.
func (r *deriveRun) cycle(parent map[string]string, start, w string) *Refusal {
	chain := []string{start, w}
	if start != w {
		chain = []string{w}
		for c := parent[w]; c != ""; c = parent[c] {
			chain = append(chain, c)
		}
		for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
			chain[i], chain[j] = chain[j], chain[i]
		}
	}
	through := ""
	if mid := chain[1 : len(chain)-1]; len(mid) > 0 {
		if len(mid) > deriveChainNamed {
			through = " through " + strings.Join(mid[:deriveChainNamed], ", ") + ", ..."
		} else {
			through = " through " + strings.Join(mid, ", ")
		}
	}
	return deriveRefuse(CodeXGuard, chain, "%s needs %s%s; a needs cycle", start, w, through)
}

// needMet lowers the open of each card still waiting for the landed need by
// one, takes it out of wait:<n>, and asks J to close its judgment "blocked on
// something missing" on the need; J closes it only if it is open (1.3.4). A
// card not in wait:<n>, or quarantined, is left out (1.3.3). Two needs of one
// waiter landing in one step lower its open by two, in one entry.
func (r *deriveRun) needMet(in Intent) *Refusal {
	n := in.Need
	key := intentKey(r.st, "wait:"+n)
	var live []string
	for _, w := range dedupIDs(in.Waiters) {
		if r.inWait(key, w) && !r.quarantined(w) {
			live = append(live, w)
		}
	}
	if len(live) == 0 {
		return nil
	}
	if ref := r.fetch(live, []string{deriveFieldOpen}); ref != nil {
		return ref
	}
	for _, w := range live {
		rec := r.recs[w]
		if !isWaiting(rec) {
			return drift(w, "is in wait:"+n+" and is not a waiting card")
		}
		open, ok := openOf(rec)
		f := r.fold(w)
		if !ok || open+f.delta < 1 {
			return drift(w, "is in wait:"+n+" and its open does not count the need")
		}
		f.delta--
		r.removeWait(n, w)
		r.note("close", sprint.NMissingNeed, n, w)
	}
	return nil
}

// needGone takes each card still waiting for the removed need out of
// wait:<n>, so the head of the set moves, and asks J to open "blocked on
// something dropped" on the need. The open of the card stays: the judgment
// counts in it (I2).
func (r *deriveRun) needGone(in Intent) *Refusal {
	n := in.Need
	key := intentKey(r.st, "wait:"+n)
	for _, w := range dedupIDs(in.Waiters) {
		if !r.inWait(key, w) || r.quarantined(w) {
			continue
		}
		r.removeWait(n, w)
		r.note("open", sprint.NBlocked, n, w)
	}
	return nil
}

// waive takes the needs of a blocked judgment out of a waiting card's count
// (1.3.3). A need whose judgment "blocked on something dropped" is open on the
// card is waived: the judgment closes, open is lowered by one, and the need
// enters waived. A need whose judgment "blocked on something missing" is open
// is waived only while it has no record: it leaves wait:<n>, and missing when
// wait:<n> is then empty; with a record now it is refused XGUARD, since a live
// prerequisite is never waived. A need with no open judgment on the card was
// settled since the read and is left out.
func (r *deriveRun) waive(in Intent) *Refusal {
	w := in.Card
	if r.quarantined(w) {
		return nil
	}
	needs := dedupIDs(in.Needs)
	if ref := r.fetch([]string{w}, []string{deriveFieldOpen, deriveFieldWaived}); ref != nil {
		return ref
	}
	rec := r.recs[w]
	for _, n := range needs {
		blocked := r.judgmentOpen(w, sprint.NBlocked, n)
		unmade := !blocked && r.judgmentOpen(w, sprint.NMissingNeed, n)
		if !blocked && !unmade {
			continue
		}
		f := r.fold(w)
		if containsID(sprint.Split(fieldOf(rec, deriveFieldWaived)), n) || containsID(f.waived, n) {
			continue
		}
		if !isWaiting(rec) {
			return drift(w, "has a judgment open on "+n+" and is not a waiting card")
		}
		open, ok := openOf(rec)
		if !ok || open+f.delta < 1 {
			return drift(w, "has a judgment open on "+n+" and its open does not count it")
		}
		if unmade {
			if ref := r.fetch([]string{n}, []string{}); ref != nil {
				return ref
			}
			if r.recs[n].Exists {
				return deriveRefuse(CodeXGuard, []string{n}, "%s exists now; %s waits for %s to land; drop %s or wait", n, w, n, w)
			}
			key := intentKey(r.st, "wait:"+n)
			if !r.inWait(key, w) {
				return drift(w, "has a judgment open on "+n+" and is not in wait:"+n)
			}
			r.removeWait(n, w)
			r.waivedMissing = append(r.waivedMissing, n)
			r.note("close", sprint.NMissingNeed, n, w)
		} else {
			r.note("close", sprint.NBlocked, n, w)
		}
		f.delta--
		f.waived = append(f.waived, n)
	}
	return nil
}

// finish builds the plan: the entries folded to one a card, grouped by the
// cell the cards are in; the requests to J; and the commands, each key's
// members in pieces of at most 1,000 (L1 1.4).
func (r *deriveRun) finish() (derivePlan, *Refusal) {
	var plan derivePlan
	for _, n := range dedupIDs(r.waivedMissing) {
		key := intentKey(r.st, "missing")
		_, there := r.keys.ZScore(key, n)
		if r.waitSize(intentKey(r.st, "wait:"+n)) == 0 && (there || r.missingAdded[n]) {
			o := r.op("ZREM", key)
			o.members = append(o.members, n)
		}
	}

	var groups []*tset.Entry
	byCell := map[string]*tset.Entry{}
	for _, id := range r.foldOrder {
		f := r.folds[id]
		if f.delta == 0 && len(f.waived) == 0 {
			continue
		}
		rec := r.recs[id]
		open, _ := openOf(rec)
		each := map[string]string{deriveFieldOpen: strconv.Itoa(open + f.delta)}
		if len(f.waived) > 0 {
			each[deriveFieldWaived] = strings.Join(dedupIDs(append(sprint.Split(fieldOf(rec, deriveFieldWaived)), f.waived...)), ",")
		}
		cell := rec.Place.Row + ":" + rec.Place.Col
		g := byCell[cell]
		if g == nil {
			g = &tset.Entry{Kind: "move", Table: sprint.Work, From: cell, BeforeFields: sprint.IndexFields()}
			byCell[cell] = g
			groups = append(groups, g)
		}
		g.IDs = append(g.IDs, id)
		g.Revs = append(g.Revs, rec.Revision)
		g.Each = append(g.Each, each)
		g.About = append(g.About, id)
	}
	for _, g := range groups {
		plan.Entries = append(plan.Entries, *g)
	}

	for _, n := range r.notes {
		req := NoteReq{Op: n.op, Type: n.typ, Cause: n.cause, Subjects: n.subjects}
		if n.op == "open" {
			switch n.typ {
			case sprint.NMissingNeed:
				req.Text = fmt.Sprintf(deriveTextMissing, n.cause)
			case sprint.NBlocked:
				req.Text = fmt.Sprintf(deriveTextDropped, n.cause)
			}
		}
		plan.Notes = append(plan.Notes, req)
	}

	for _, o := range r.ops {
		for first := 0; first < len(o.members); first += maxPieces {
			last := min(first+maxPieces, len(o.members))
			var args []string
			for i := first; i < last; i++ {
				if o.cmd == "ZADD" {
					args = append(args, o.scores[i])
				}
				args = append(args, o.members[i])
			}
			plan.Cmds = append(plan.Cmds, Command(o.cmd, o.key, kindZSet, args...))
		}
	}
	return plan, nil
}

// obsDeriveWorld is a world of what S.before read and nothing else: the
// phase as 8.0 fixes it, before an adapter gives it the store.
type obsDeriveWorld struct{ obs *Before }

func (w obsDeriveWorld) records(ids, fields []string) ([]tset.MemberRecord, *Refusal) {
	out := make([]tset.MemberRecord, len(ids))
	for i, id := range ids {
		rec, ok := w.obs.Record(sprint.Work, id)
		if !ok || rec.Exists && !hasFields(&rec, fields) {
			return nil, deriveRefuse(CodeConfig, []string{id}, "%s was not read with the fields the derive phase needs; bind the phase to a twin with UseIntents", id)
		}
		out[i] = rec
	}
	return out, nil
}

func (w obsDeriveWorld) created(string) (createdCard, bool) { return createdCard{}, false }

// Derive is the derive phase as 8.0 fixes its signature (1.3.3; IT14): the
// intents of a step turned into the entries and the requests to J they decide,
// on the before-state obs holds and the sprint's own keys. It reads no record
// obs does not hold, and it can give back no command, so it refuses CONFIG a
// step whose intents need either: a waitfor (which reads the walk's records and
// the step's create entries) and every intent that changes wait:<n> or
// missing. Twin.UseIntents binds the phase to a twin and carries its commands
// to X.plan; Derive stands for the signature and for a step that decides
// nothing.
func Derive(st *State, in []Intent, obs *Before) ([]tset.Entry, []NoteReq, *Refusal) {
	for _, it := range in {
		if it.Kind == IntentWaitFor {
			return nil, nil, deriveRefuse(CodeConfig, nil, "a waitfor reads the step's create entries and the walk's records; bind the derive phase to a twin with UseIntents")
		}
	}
	plan, ref := deriveOver(st, obsDeriveWorld{obs: obs}, in)
	if ref != nil {
		return nil, nil, ref
	}
	if len(plan.Cmds) != 0 {
		return nil, nil, deriveRefuse(CodeConfig, nil, "the derive phase decided commands on wait:<n> or missing, which its signature cannot carry; bind it to a twin with UseIntents")
	}
	return plan.Entries, plan.Notes, nil
}

func init() { defaultPhases.Derive = Derive }

// twinDeriveWorld is the world of a twin's step: the twin's table twin for the
// records, and the request X.pre saw for the create entries.
type twinDeriveWorld struct {
	tab *tset.Mem
	st  *State
	req *Request
	obs *Before
}

func (w twinDeriveWorld) records(ids, fields []string) ([]tset.MemberRecord, *Refusal) {
	out := make([]tset.MemberRecord, len(ids))
	ask, at := []string{}, []int{}
	for i, id := range ids {
		if rec, ok := w.obs.Record(sprint.Work, id); ok && (!rec.Exists || hasFields(&rec, fields)) {
			out[i] = rec
			continue
		}
		ask, at = append(ask, id), append(at, i)
	}
	if len(ask) == 0 {
		return out, nil
	}
	q := tset.ReadQuery{Kind: "ids", Table: sprint.Work, IDs: ask, Fields: fields}
	rep, err := w.tab.Read(context.Background(), newTSetReadPlan(w.st.Prefix, w.st.Epoch, "atomic", []tset.ReadQuery{q}))
	if err != nil {
		var lref *tset.Refusal
		if errors.As(err, &lref) {
			if lref.Code == "BUDGET" { // a write plan that exhausts a bound is LIMIT (L1 1.2)
				return nil, refuse(PhaseDerive, CodeLimit, RefusalDetail{RefusalDetail: tset.RefusalDetail{Budget: lref.Detail.Budget}})
			}
			return nil, fromTset(PhaseDerive, lref)
		}
		return nil, refuse(PhaseDerive, CodeConfig, RefusalDetail{})
	}
	if len(rep.Answers) != 1 || len(rep.Answers[0].Records) != len(ask) {
		return nil, refuse(PhaseDerive, CodeConfig, RefusalDetail{})
	}
	for j, rec := range rep.Answers[0].Records {
		out[at[j]] = rec
	}
	return out, nil
}

func (w twinDeriveWorld) created(card string) (createdCard, bool) {
	if w.req == nil {
		return createdCard{}, false
	}
	return createdIn(w.req.Body.Entries, card)
}

// intentBinding is the derive phase bound to one twin: its table twin for the
// walk's reads, the request X.pre saw, and the commands the last derive decided
// for X.plan to put in the commit list. A twin runs one call at a time, so one
// slot serves: X.pre sets it, derive fills it and X.plan takes it.
type intentBinding struct {
	tab  *tset.Mem
	st   *State
	req  *Request
	cmds []Cmd
}

// UseIntents binds the derive phase to the twin: the phase reads the walk's
// records from the twin's table twin, finds the create entries of a waitfor in
// the request, and hands its commands on wait:<n> and missing to X.plan, whose
// list the twin's XCmds then ends with. Call it once, after the twin's X phases
// are set: a twin without X refuses CONFIG as before, and a twin this was not
// called on refuses CONFIG a step whose intents need it (Derive).
func (t *Twin) UseIntents() {
	t.mu.Lock()
	defer t.mu.Unlock()
	b := &intentBinding{tab: t.tab}
	if inner := t.phases.XPre; inner != nil {
		t.phases.XPre = func(st *State, req *Request, obs *Before) *Refusal {
			b.st, b.req, b.cmds = st, req, nil
			return inner(st, req, obs)
		}
	}
	t.phases.Derive = func(st *State, in []Intent, obs *Before) ([]tset.Entry, []NoteReq, *Refusal) {
		if b.st != st || b.req == nil {
			return nil, nil, deriveRefuse(CodeConfig, nil, "the derive phase ran with no request: X.pre did not run before it")
		}
		plan, ref := deriveOver(st, twinDeriveWorld{tab: b.tab, st: st, req: b.req, obs: obs}, in)
		if ref != nil {
			return nil, nil, ref
		}
		b.cmds = plan.Cmds
		return plan.Entries, plan.Notes, nil
	}
	if inner := t.phases.XCmds; inner != nil {
		t.phases.XCmds = func(st *State, tp TablePlan, lp LogPlan) []Cmd {
			cmds := inner(st, tp, lp)
			if b.st == st {
				cmds = append(cmds, b.cmds...)
				b.cmds = nil
			}
			return cmds
		}
	}
}

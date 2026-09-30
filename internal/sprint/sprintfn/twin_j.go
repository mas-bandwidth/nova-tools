package sprintfn

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// J, the judgments of a step (the upper design, EVENT-DRIVEN-TICK version 2.1,
// 1.3.4 and 2.2, as errata 1 and 2 correct 8.0; item IT15). J turns the step's
// note requests into the step's notes in the pre stage, from the real state of
// the sprint's judgment keys, and writes the keys that follow from them in
// X.plan once Layer 2 has given the notes their seqs. Its Lua half is
// internal/nsprint/fn/lua/sprint_j.lua; both halves are held to the golden
// vectors in testdata/j_vectors.json.
//
// What J reads (the pre stage): {p}jopen:<subject>@e for each subject a
// request names, at the field <type>|<cause>, and all its fields for a subject
// that a close or an unhold may take out of askwait (another cause of "cannot
// ask" keeps it there); {p}jn@e for each note a close or a hold would shrink;
// {p}clock for R, when a note opens. What J writes (X.plan, commands only):
// {p}jopen:<subject>@e, {p}jnotes@e, {p}jn@e, {p}notes@e, {p}askwait@e, and in
// {p}due@e the overdue:<note> and hold:<note> entries, and {p}clock's
// stophold_ms for a wait on the STOPPED judgment. The note ids are "n" and the
// seq of the note's line (OpFamily at the epoch), taken from LogPlan.NoteSeqs.
//
// The ops (NoteReq.Op), each applied to each subject on the state its field
// holds, where the field is absent, a note id (open) or "h" and a note id
// (held):
//
//	open     absent: one new note on those subjects, with overdue:<note> at R +
//	         10 min (none for "the sprint is done", never overdue, 2.2), jnotes
//	         at R, jn the count, and askwait at R for "cannot ask". Open or held:
//	         nothing (one per cause: two steps racing open it once).
//	update   open: an update line on the note; nothing else changes.
//	close    open: the field leaves jopen and jn falls, the note leaving jnotes
//	         and losing its overdue entry when jn reaches zero. Held: the field
//	         leaves jopen. askwait loses the subject for "cannot ask". Absent:
//	         nothing ("J closes a subject only when the field is present").
//	hold     open, and the type one the tick keeps (Judgments[type].TickKept):
//	         the judgment closes with a hold line, the field becomes "h<note>"
//	         and hold:<note> is entered at Until (R + d). The STOPPED judgment
//	         enters no hold entry and sets stophold_ms to Until, a wall time,
//	         instead. Open, and any other type: the overdue entry moves to Until
//	         and the note stays open (a review wait). Held or absent: nothing.
//	unhold   held (R13): the field leaves jopen, an unheld line is written.
//	know     a notice (Notices[type]): its line, and nothing else.
//	request  a request line, and nothing else.
//
// JPlan is IT12's type and its meaning is J's: Notes has one JNote for each note
// J made, Index the note's place in the step's notes (so LogPlan.NoteSeqs[Index]
// is its line's seq), Req the request resolved to that line (the op, with a wait
// on a judgment the tick does not keep as "review", and the subjects the line is
// about) and Existing "" for a new note, else the note id the subjects' fields
// held, with "h" before it for a hold. A request that changes nothing has none.
//
// A step that ends a timed state closes the lateness judgment of that kind on
// the card without being asked (JDecideEntries). LatenessJudgment is the type
// and cause R11 must raise it with. JBefore names the fields that takes; JCost
// counts what a step's J costs, as the step builder does (8.0: Coster).
//
// What the twin's composition lacks for J, which is IT12's to give and not
// changed here: State carries no entries, so JDecide, the function the twin's J
// phase is, closes no lateness judgment, and a composing twin calls
// JDecideEntries itself (the tests do); the twin calls J only for a step that
// carries a note request, so a verb's step that ends a timed state and raises no
// note does not reach it; and the pre stage's asks are one function, so JBefore
// is for whoever composes them (Phases.Before).
//
// Where the design is silent or contradicts itself, the narrower reading is
// taken, and the rest is left to ask. What the reading took:
//
//	text changed (1.3.4): "J updates that note in place when its text
//	changed", and a field holds only the note's id, so J cannot compare the
//	text. The caller says update, and open on an open judgment writes nothing.
//	the hold entry on a close (1.3.4): "its close removes the hold and its
//	hold entry". A hold may name several subjects of one note, and J cannot
//	read the others, so a close of one would orphan their wake. A close and
//	an unhold leave hold:<note> to fire; R13 then finds no hold and writes
//	nothing, as it does a second time (2.3).
//	the lines' kinds (events.go): Layer 3 reads judgment, happened, decided
//	and acknowledged, and refuses any other. An open is judgment, an update
//	judgment with the verb "updated", a know happened, a close and an unhold
//	decided (ingest turns both into the owner key of the type, as 2.1 says of
//	an unheld line), a hold, a review wait and a request no kind (a plain
//	note, which queues nothing). NoteReq has no field that says decided from
//	acknowledged, so J writes decided.
//	ops and step: a (type, cause, subject) named by two state requests of one
//	step is REQUEST: a note names every subject of its step with the same
//	type and cause, and two requests on one field have no order.
//	the epoch: State carries the request epoch only, and a step that advances
//	writes at the next (L1 1.2, write_epoch). J writes at State.Epoch.
//	never overdue (2.2): Judgment.TickKept is one bool, where the wait column
//	has holds, review and never overdue; J holds the one type that is never
//	overdue itself.
//	the overdue span (1.2): written + 10 min; Until is not read on an open.
//	askwait's score (1.3.1: id -> R): an open writes R, also for a subject that
//	another cause keeps there, since Layer 1's registry has no ZADD NX (errata 2,
//	item 5).
//	the stream of a note (events.go): NoteReq has no stream, so a line's meta
//	has none, and ingest's rows that key on a stream find none.

// The ops of a NoteReq (8.0, IT05's shape; 1.3.4).
const (
	JOpOpen    = "open"    // raise a judgment on the subjects that have none
	JOpClose   = "close"   // end a judgment, or a hold, on the subjects that have one
	JOpUpdate  = "update"  // rewrite an open judgment in place
	JOpHold    = "hold"    // wait: hold a condition the tick keeps, or move a review time
	JOpUnhold  = "unhold"  // R13: a hold ran out
	JOpKnow    = "know"    // a notice (2.5)
	JOpRequest = "request" // a request line
)

// jOpReview is the op a JNote carries for a wait on a judgment the tick does
// not keep (1.3.4: it moves the overdue entry to the review time and leaves the
// note open). It is J's word and no caller's: a request says hold.
const jOpReview = "review"

// The judgment types J treats by name, the words of 2.2's table (a test holds
// each to a key of sprint.Judgments).
const (
	jTypeCannotAsk  = "cannot ask"                                       // askwait (1.3.1, 2.3 R8)
	jTypeStopped    = "the machine is STOPPED and moves are due"         // a wait sets stophold_ms (1.3.4)
	jTypeDone       = "the sprint is done"                               // never overdue (2.2)
	jTypeWorkLate   = "a work card is past its deadline"                 // R11 (2.2)
	jTypeReadLate   = "a read card is past its deadline"                 // R11 (2.2)
	jTypeStreamLate = "a stream has had no merge step past its deadline" // R11 (2.2)
	jCodeDrift      = "DRIFT"                                            // a key the sprint owns disagrees with itself (L1 8)
)

// The keys J reads and writes, by the name after {p} (1.3.1, 1.2). The per
// epoch ones carry "@" and the epoch after the name; the clock and nothing else
// of J's is a sprint key with no epoch.
const (
	jKeyJopen   = "jopen:"  // jopen:<subject>@e, HASH <type>|<cause> -> <note> or h<note>
	jKeyJnotes  = "jnotes"  // ZSET note -> R, every open note
	jKeyJn      = "jn"      // HASH note -> count of its open subjects
	jKeyNotes   = "notes"   // LIST of the seqs of every note line
	jKeyAskwait = "askwait" // ZSET primary -> R, "cannot ask" open or held
	jKeyDue     = "due"     // ZSET <kind>:<id> -> running ms
	jKeyClock   = "clock"   // HASH, sprint: the running clock (1.2)
)

// The words of the due set's members and of the clock's fields (1.2), of a
// judgment's field and of a note id (1.3.1, 1.3.4).
const (
	jMemberOverdue   = "overdue:"
	jMemberHold      = "hold:"
	jFieldSep        = "|"
	jNotePrefix      = "n"
	jHoldPrefix      = "h"
	jClockStoppedMS  = "stopped_ms"
	jClockSinceMS    = "stopped_since_ms"
	jClockStopHoldMS = "stophold_ms"
)

// The bounds of a step's notes, each one of L1 6's.
const (
	jSubjectsMax = tset.MaxIDsPerLine       // a note names at most 2,000 subjects (L2 1.2)
	jNotesMax    = tset.MaxNotes            // notes in a step: 100
	jAboutMax    = tset.MaxAboutBeforeDedup // about ids in a step before dedup: 4,000
	jNameMax     = tset.MaxIdentifierBytes  // a subject, type or cause: 256 bytes
	jPiece       = 1000                     // elements of one command (L1 1.4)
)

// jOverdueSpanMS is how long after it is written an open judgment waits before
// "a judgment has waited past its due time" (1.2: overdue:<note>, written + 10
// min).
const jOverdueSpanMS int64 = 10 * 60 * 1000

// jClockDigitsMax is the most digits a clock field or the call's time may
// have: a millisecond clock of 15 digits is below 2^53, so the Lua half holds
// it exactly.
const jClockDigitsMax = 15

// jUntilMax is the largest time a hold or a review may name: 15 digits, which the
// Lua half holds exactly as a number.
const jUntilMax int64 = 999_999_999_999_999

// jCountDigitsMax is the most digits a count in jn may have: a note names at
// most 2,000 subjects, so a count of 10 digits is a key that disagrees with
// itself.
const jCountDigitsMax = 9

// jSeqCeiling is the live sequence ceiling, 2^53 - 1 (L2 2): the seq JCost
// counts a command's bytes with, the longest one a line can have.
const jSeqCeiling = "9007199254740991"

// Lateness (1.3.4: a lateness judgment ends with its state). The judgment of
// each due kind of 1.2, by the cause it is raised under, which is the kind:
// R11's owner key is late:<kind>:<card> (2.2), and so the kind is what tells
// two lateness judgments of one card apart.

// LatenessJudgment is the type and cause of the lateness judgment of a due
// kind (sprint.DueKinds): the words R11 must raise it with, so that J closes it
// when the state ends. ok is false for a kind that has none.
func LatenessJudgment(kind string) (typ, cause string, ok bool) {
	switch kind {
	case "untaken", "unfinished":
		return jTypeWorkLate, kind, true
	case "unbegun", "unreported":
		return jTypeReadLate, kind, true
	case "mergeidle":
		return jTypeStreamLate, kind, true
	}
	return "", "", false
}

// jEndReason is the reason a close carries for each way a timed state ends.
var jEndReason = map[string]string{
	"untaken": "taken", "unfinished": "finished", "unbegun": "begun", "unreported": "reported",
	"mergeidle": "stream no longer merging",
}

// The other two ways a timed state ends.
const (
	jReasonRemoved = "removed" // the card left the table
	jReasonCleared = "cleared" // the card stayed and its due field went
)

// jJudgment is the adapter to IT06's table of 2.2 (sprint.Judgments): whether
// the type is one, and whether the tick keeps the condition (a wait holds it).
// It is the one place J reads the table, so a move of its shape is one change.
func jJudgment(typ string) (tickKept, ok bool) {
	j, ok := sprint.Judgments[typ]
	return j.TickKept, ok
}

// jNotice is the adapter to IT06's table of 2.5 (sprint.Notices).
func jNotice(typ string) bool {
	_, ok := sprint.Notices[typ]
	return ok
}

// jIsStateOp says an op reads and changes {p}jopen (the others write a line).
func jIsStateOp(op string) bool {
	switch op {
	case JOpOpen, JOpClose, JOpUpdate, JOpHold, JOpUnhold:
		return true
	}
	return false
}

// jKey is a per epoch sprint key: {p}<name>@<epoch>.
func jKey(st *State, name string) string {
	return st.Prefix + "sprint:" + name + "@" + string(st.Epoch)
}

func jClockKey(st *State) string { return st.Prefix + "sprint:" + jKeyClock }

func jField(typ, cause string) string { return typ + jFieldSep + cause }

// jNoteID is the id of the note whose line has the seq: "n" and the seq, with
// the epoch after it from epoch 1 (OpFamily, 1.3.4).
func jNoteID(st *State, seq tset.Decimal) string {
	epoch, err := strconv.ParseUint(string(st.Epoch), 10, 64)
	if err != nil {
		epoch = 0 // State.Epoch is a validated decimal; unreachable
	}
	return sprint.OpFamily(jNotePrefix+string(seq), epoch)
}

func jRefuse(index int, code, why string) *Refusal {
	ref := refuse(PhaseJ, code, RefusalDetail{})
	ref.Message = fmt.Sprintf("%s: note request %d %s; nothing was changed", code, index, why)
	return ref
}

func jLimit(index int, budget string, limit, actual int) *Refusal {
	l, a := int64(limit), int64(actual)
	ref := refuse(PhaseJ, CodeLimit, RefusalDetail{RefusalDetail: tset.RefusalDetail{Budget: budget, Limit: &l, Actual: &a}})
	ref.Message = fmt.Sprintf("LIMIT: note request %d is over the bound of %s; nothing was changed", index, budget)
	return ref
}

func jName(s string, allowEmpty bool) bool {
	return (allowEmpty || s != "") && len(s) <= jNameMax && utf8.ValidString(s)
}

// jDedup keeps the first of equal strings, in order.
func jDedup(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// jCheck is the static shape of one request: the op, the subjects, the words,
// the type against the tables. A request that fails is the caller's fault.
func jCheck(i int, r NoteReq) *Refusal {
	switch r.Op {
	case JOpOpen, JOpClose, JOpUpdate, JOpHold, JOpUnhold, JOpKnow, JOpRequest:
	default:
		return jRefuse(i, CodeRequest, "has an op J does not take")
	}
	if len(r.Subjects) == 0 {
		return jRefuse(i, CodeRequest, "names no subject")
	}
	if len(r.Subjects) > jSubjectsMax {
		return jLimit(i, "about", jSubjectsMax, len(r.Subjects))
	}
	for _, s := range r.Subjects {
		if !jName(s, false) {
			return jRefuse(i, CodeRequest, "names a subject that is empty, over 256 bytes or not UTF-8")
		}
	}
	state := jIsStateOp(r.Op)
	if !jName(r.Type, false) || (state && strings.Contains(r.Type, jFieldSep)) {
		return jRefuse(i, CodeRequest, "has a type that is empty, over 256 bytes, not UTF-8 or holds a bar")
	}
	if !jName(r.Cause, !state) {
		return jRefuse(i, CodeRequest, "has a cause that is empty, over 256 bytes or not UTF-8")
	}
	if !utf8.ValidString(r.Text) {
		return jRefuse(i, CodeRequest, "has text that is not UTF-8")
	}
	for _, d := range r.Decisions {
		if !utf8.ValidString(d) {
			return jRefuse(i, CodeRequest, "has a decision that is not UTF-8")
		}
	}
	switch r.Op {
	case JOpKnow:
		if !jNotice(r.Type) {
			return jRefuse(i, CodeRequest, "is a notice of a type 2.5 does not have")
		}
	case JOpRequest:
	default:
		if _, ok := jJudgment(r.Type); !ok {
			return jRefuse(i, CodeRequest, "is on a judgment type 2.2 does not have")
		}
	}
	if r.Until < 0 || r.Until > jUntilMax {
		return jRefuse(i, CodeRequest, "has a time that is negative or over 15 digits")
	}
	if r.Op == JOpHold && r.Until <= 0 {
		return jRefuse(i, CodeRequest, "holds with no time to hold until")
	}
	return nil
}

// jDecider is one call of J's pre stage: what it has read, counted as probes.
type jDecider struct {
	st     *State
	probes int
	fields map[string]jRead // by key, NUL, field
	jn     map[string]jRead // by note id
	all    map[string]map[string]string
}

// jRead is one key J read: its value, and whether it was there.
type jRead struct {
	val     string
	present bool
}

// hget reads one field of a hash, as a typed read does: a key of another type
// is WRONGTYPE.
func (d *jDecider) hget(key, field string) (jRead, *Refusal) {
	switch d.st.Keys.Type(key) {
	case kindNone, kindHash:
	default:
		return jRead{}, refuse(PhaseJ, CodeWrongType, RefusalDetail{})
	}
	v, ok := d.st.Keys.HGet(key, field)
	return jRead{v, ok}, nil
}

// field is the state of one subject's field, read once a call.
func (d *jDecider) field(subject, field string) (jRead, *Refusal) {
	key := jKey(d.st, jKeyJopen+subject)
	memo := key + "\x00" + field
	if r, ok := d.fields[memo]; ok {
		return r, nil
	}
	d.probes++
	r, ref := d.hget(key, field)
	if ref != nil {
		return jRead{}, ref
	}
	d.fields[memo] = r
	return r, nil
}

// jopenFields is every field of a subject's jopen hash, read once a call: the
// judgments open or held on it.
func (d *jDecider) jopenFields(subject string) map[string]string {
	if f, ok := d.all[subject]; ok {
		return f
	}
	f := d.st.Keys.HGetAll(jKey(d.st, jKeyJopen+subject))
	d.probes++ // HLEN
	if len(f) != 0 {
		d.probes++ // and HKEYS, which the Lua half reads bounded by the length
	}
	d.all[subject] = f
	return f
}

// jState is what a jopen field holds, parsed: none when absent, open with the
// note's id, or held with the note's id.
type jState struct {
	kind string // "", "open" or "held"
	id   string
}

// state parses a subject's field; a value that is neither a note id nor a hold
// of one is DRIFT: the sprint's own key disagrees with itself.
func (d *jDecider) state(subject, field string) (jState, *Refusal) {
	r, ref := d.field(subject, field)
	if ref != nil {
		return jState{}, ref
	}
	switch {
	case !r.present:
		return jState{}, nil
	case strings.HasPrefix(r.val, jHoldPrefix+jNotePrefix) && len(r.val) > 2:
		return jState{"held", r.val[1:]}, nil
	case strings.HasPrefix(r.val, jNotePrefix) && len(r.val) > 1:
		return jState{"open", r.val}, nil
	}
	return jState{}, refuse(PhaseJ, jCodeDrift, RefusalDetail{})
}

// count is jn[note], the open subjects of a note; it is DRIFT for a note that
// jopen says is open and jn has no count of.
func (d *jDecider) count(note string) (int, *Refusal) {
	r, ok := d.jn[note]
	if !ok {
		d.probes++
		var ref *Refusal
		if r, ref = d.hget(jKey(d.st, jKeyJn), note); ref != nil {
			return 0, ref
		}
		d.jn[note] = r
	}
	n, err := strconv.Atoi(r.val)
	if !r.present || len(r.val) > jCountDigitsMax || err != nil || n < 1 || strconv.Itoa(n) != r.val {
		return 0, refuse(PhaseJ, jCodeDrift, RefusalDetail{})
	}
	return n, nil
}

// digits reads a clock field: absent is 0, and anything but 1 to 15 digits is
// CONFIG (the machine's own key is wrong).
func jDigits(v string, present bool) (int64, bool) {
	if !present {
		return 0, true
	}
	if v == "" || len(v) > jClockDigitsMax {
		return 0, false
	}
	var n int64
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return 0, false
		}
		n = n*10 + int64(v[i]-'0')
	}
	return n, true
}

// jRunning is R, running time, from the call's one time and the clock (1.2):
// the wall time less the STOPPED time before now and the present STOPPED span.
func jRunning(st *State) (int64, *Refusal) {
	wall, ok := jDigits(string(st.NowMS), true)
	if !ok {
		return 0, refuse(PhaseJ, CodeConfig, RefusalDetail{})
	}
	if k := st.Keys.Type(jClockKey(st)); k != kindNone && k != kindHash {
		return 0, refuse(PhaseJ, CodeWrongType, RefusalDetail{})
	}
	sv, sp := st.Keys.HGet(jClockKey(st), jClockStoppedMS)
	stopped, ok := jDigits(sv, sp)
	if !ok {
		return 0, refuse(PhaseJ, CodeConfig, RefusalDetail{})
	}
	since, sp := st.Keys.HGet(jClockKey(st), jClockSinceMS)
	r := wall - stopped
	if sp && since != "" {
		s, ok := jDigits(since, true)
		if !ok || s > wall {
			return 0, refuse(PhaseJ, CodeConfig, RefusalDetail{})
		}
		r -= wall - s
	}
	if r < 0 {
		return 0, refuse(PhaseJ, CodeConfig, RefusalDetail{})
	}
	return r, nil
}

// jGroup is one note line J makes: the request's words, resolved to the
// subjects that line is about, and what their fields held.
type jGroup struct {
	req      NoteReq
	existing string // "" for a new note, else the note id, "h" before it for a hold
}

// decide expands one request into the lines it makes, reading each subject's
// field once.
func (d *jDecider) decide(r NoteReq) ([]jGroup, *Refusal) {
	subjects := jDedup(r.Subjects)
	if !jIsStateOp(r.Op) {
		r.Subjects = subjects
		return []jGroup{{req: r}}, nil
	}
	field := jField(r.Type, r.Cause)
	type bucket struct {
		state    jState
		subjects []string
	}
	var order, fresh []string
	buckets := map[string]*bucket{}
	for _, s := range subjects {
		js, ref := d.state(s, field)
		if ref != nil {
			return nil, ref
		}
		if js.kind == "" {
			fresh = append(fresh, s)
			continue
		}
		k := js.kind + "\x00" + js.id
		b := buckets[k]
		if b == nil {
			b = &bucket{state: js}
			buckets[k] = b
			order = append(order, k)
		}
		b.subjects = append(b.subjects, s)
	}
	if r.Op == JOpOpen {
		if len(fresh) == 0 {
			return nil, nil
		}
		r.Subjects = fresh
		return []jGroup{{req: r}}, nil
	}
	tickKept, _ := jJudgment(r.Type)
	var out []jGroup
	for _, k := range order {
		b := buckets[k]
		held := b.state.kind == "held"
		g := jGroup{req: r, existing: b.state.id}
		g.req.Subjects = b.subjects
		if held {
			g.existing = jHoldPrefix + b.state.id
		}
		switch r.Op {
		case JOpUpdate:
			if held {
				continue // the condition is held: nothing to rewrite
			}
		case JOpHold:
			if held {
				continue // already held
			}
			if !tickKept {
				g.req.Op = jOpReview
			}
		case JOpUnhold:
			if !held {
				continue
			}
		}
		out = append(out, g)
	}
	return out, nil
}

// jMetaOf is the line's meta: sorted keys, strings and one list of strings,
// no numbers (L2 1.1). Every key is omitted when empty.
func jMetaOf(g jGroup) json.RawMessage {
	r := g.req
	m := map[string]any{"op": r.Op, "type": r.Type}
	put := func(k, v string) {
		if v != "" {
			m[k] = v
		}
	}
	put("cause", r.Cause)
	put("text", r.Text)
	if len(r.Decisions) != 0 {
		m["decisions"] = r.Decisions
	}
	if g.existing != "" {
		m["note"] = jNoteOf(g.existing)
	}
	switch r.Op {
	case JOpOpen:
		m["kind"] = sprint.Judgment
	case JOpUpdate:
		m["kind"] = sprint.Judgment
		m["verb"] = "updated"
	case JOpClose, JOpUnhold:
		m["kind"] = sprint.Decided
	case JOpKnow:
		m["kind"] = sprint.Happened
	}
	if r.Op == JOpHold || r.Op == jOpReview {
		m["until"] = strconv.FormatInt(r.Until, 10)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(m) // a map of strings and a list of strings encodes
	return json.RawMessage(bytes.TrimRight(buf.Bytes(), "\n"))
}

// jNoteOf is the note id of an existing field value: the value itself, or the
// id after the "h" of a hold.
func jNoteOf(existing string) string {
	if strings.HasPrefix(existing, jHoldPrefix+jNotePrefix) {
		return existing[len(jHoldPrefix):]
	}
	return existing
}

// jEndings are the requests that close the lateness judgments of the timed
// states the step's entries end (1.3.4): for each entry that moves or removes a
// card out of a due kind's state, or clears its due field, one close on the
// card (the stream for a kind keyed by row), by the type and cause R11 raises
// it under. Only cards obs holds are seen; a request that already names a
// (type, cause, subject) is not added a second time.
func jEndings(entries []tset.Entry, obs *Before, named map[[3]string]bool) []NoteReq {
	type key struct{ kind, reason string }
	var order []key
	groups := map[key][]string{}
	for _, e := range entries {
		if e.Kind != "move" && e.Kind != "remove" {
			continue
		}
		for _, k := range sprint.DueKinds {
			if k.Table != e.Table {
				continue
			}
			for i, id := range e.IDs {
				rec, ok := obs.Record(e.Table, id)
				if !ok || !rec.Exists || rec.Place == nil || rec.Place.Col != k.Col {
					continue
				}
				due := rec.Fields[k.Field]
				if !due.Present || due.Value == "" {
					continue
				}
				reason, ends := jEnds(e, i, k, rec)
				if !ends {
					continue
				}
				subject := sprint.CardID(id)
				if k.OfRow {
					subject = rec.Place.Row
				}
				typ, cause, _ := LatenessJudgment(k.Kind)
				if named[[3]string{typ, cause, subject}] {
					continue
				}
				kr := key{k.Kind, reason}
				if _, seen := groups[kr]; !seen {
					order = append(order, kr)
				}
				groups[kr] = append(groups[kr], subject)
			}
		}
	}
	var out []NoteReq
	for _, kr := range order {
		typ, cause, _ := LatenessJudgment(kr.kind)
		out = append(out, NoteReq{Op: JOpClose, Type: typ, Cause: cause, Subjects: jDedup(groups[kr]), Text: kr.reason})
	}
	return out
}

// jEnds says whether an entry takes the ith card out of the due kind's state,
// and how: a remove; a move to another column (or, for a kind keyed by row,
// another row); or a change that unsets the due field or sets it empty (a card
// with no due field is in no due set, 1.2).
func jEnds(e tset.Entry, i int, k sprint.DueKind, rec tset.MemberRecord) (string, bool) {
	if e.Kind == "remove" {
		return jReasonRemoved, true
	}
	if e.To != "" && e.To != e.From {
		row, col := jCell(e.To)
		if col != k.Col || (k.OfRow && row != rec.Place.Row) {
			return jEndReason[k.Kind], true
		}
	}
	for _, f := range e.Unset {
		if f == k.Field {
			return jReasonCleared, true
		}
	}
	if v, ok := e.Set[k.Field]; ok && v == "" {
		return jReasonCleared, true
	}
	if i < len(e.Each) {
		if v, ok := e.Each[i][k.Field]; ok && v == "" {
			return jReasonCleared, true
		}
	}
	return "", false
}

// jCell splits a cell reference at its last colon (L1 3).
func jCell(ref string) (row, col string) {
	if i := strings.LastIndexByte(ref, ':'); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	return ref, ""
}

// JBefore names the fields J reads of the cards a step moves or removes, for
// whoever composes the pre stage's asks (Phases.Before): the due field of every
// timed state such a card may be in, so that JDecideEntries sees it end.
func JBefore(st *State, req *Request) []BeforeAsk {
	var asks []BeforeAsk
	for _, e := range req.Body.Entries {
		if e.Kind != "move" && e.Kind != "remove" {
			continue
		}
		var fields []string
		for _, k := range sprint.DueKinds {
			if k.Table == e.Table {
				fields = append(fields, k.Field)
			}
		}
		if len(fields) != 0 {
			asks = append(asks, BeforeAsk{Table: e.Table, IDs: e.IDs, Fields: fields})
		}
	}
	return asks
}

// jOutcome is what J's pre stage decided: the notes, the plan X.plan takes, and
// what the store reads cost.
type jOutcome struct {
	notes  []tset.Note
	plan   JPlan
	probes int
}

// jRun is J's pre stage. entries is the step's combined entries, nil where the
// caller has none to give.
func jRun(st *State, in []NoteReq, obs *Before, entries []tset.Entry) (jOutcome, *Refusal) {
	if st == nil || st.Keys == nil {
		return jOutcome{}, refuse(PhaseJ, CodeConfig, RefusalDetail{})
	}
	for i, r := range in {
		if ref := jCheck(i, r); ref != nil {
			return jOutcome{}, ref
		}
	}
	named := map[[3]string]bool{}
	for i, r := range in {
		if !jIsStateOp(r.Op) {
			continue
		}
		for _, s := range jDedup(r.Subjects) {
			k := [3]string{r.Type, r.Cause, s}
			if named[k] {
				return jOutcome{}, jRefuse(i, CodeRequest, "names a type, cause and subject that another request of the step names")
			}
			named[k] = true
		}
	}
	reqs := append([]NoteReq(nil), in...)
	if len(entries) != 0 {
		reqs = append(reqs, jEndings(entries, obs, named)...)
	}
	d := &jDecider{st: st, fields: map[string]jRead{}, jn: map[string]jRead{}, all: map[string]map[string]string{}}
	var out jOutcome
	about := 0
	opens := false
	for i, r := range reqs {
		groups, ref := d.decide(r)
		if ref != nil {
			return jOutcome{}, ref
		}
		for _, g := range groups {
			if len(out.notes) == jNotesMax {
				return jOutcome{}, jLimit(i, "notes", jNotesMax, jNotesMax+1)
			}
			opens = opens || g.req.Op == JOpOpen
			about += len(g.req.Subjects)
			if about > jAboutMax {
				return jOutcome{}, jLimit(i, "about", jAboutMax, about)
			}
			out.plan.Notes = append(out.plan.Notes, JNote{Index: len(out.notes), Req: g.req, Existing: g.existing})
			out.notes = append(out.notes, tset.Note{Line: tset.NoteLine{Kind: "note", Meta: jMetaOf(g)}, About: g.req.Subjects})
		}
	}
	// A close or a hold of open subjects takes them off their note's count: it
	// must have the count, and one to take them from (jn agrees with jopen).
	taken, order := jOpenTaken(out.plan)
	for _, note := range order {
		have, ref := d.count(note)
		if ref != nil {
			return jOutcome{}, ref
		}
		if have < taken[note] {
			return jOutcome{}, refuse(PhaseJ, jCodeDrift, RefusalDetail{})
		}
	}
	jAskwaitDrops(out.plan, d.jopenFields) // reads the fields of the subjects that may leave askwait
	if opens {                             // R, from the clock, is read once for the notes that open
		if _, ref := jRunning(st); ref != nil {
			return jOutcome{}, ref
		}
		d.probes++
	}
	out.probes = d.probes
	return out, nil
}

// jAskwaitDrops is the subjects that leave askwait in this step (1.3.1: a
// primary is in it while "cannot ask" is open or held on it): those whose last
// "cannot ask" field, of any cause, a close or an unhold takes from jopen, with
// none opened on them in the same step. fields reads a subject's jopen fields.
func jAskwaitDrops(jp JPlan, fields func(subject string) map[string]string) []string {
	leaving := map[string]map[string]bool{}
	staying := map[string]bool{}
	var order []string
	for _, n := range jp.Notes {
		if n.Req.Type != jTypeCannotAsk {
			continue
		}
		field := jField(n.Req.Type, n.Req.Cause)
		switch n.Req.Op {
		case JOpOpen:
			for _, s := range n.Req.Subjects {
				staying[s] = true
			}
		case JOpClose, JOpUnhold:
			for _, s := range n.Req.Subjects {
				if leaving[s] == nil {
					leaving[s] = map[string]bool{}
					order = append(order, s)
				}
				leaving[s][field] = true
			}
		}
	}
	var drops []string
	for _, s := range order {
		if staying[s] {
			continue
		}
		keep := false
		for f := range fields(s) {
			if strings.HasPrefix(f, jTypeCannotAsk+jFieldSep) && !leaving[s][f] {
				keep = true
				break
			}
		}
		if !keep {
			drops = append(drops, s)
		}
	}
	return drops
}

// jOpenTaken is how many subjects a step takes off each open note's count: a
// close of open subjects and a hold of them leave the open set. The notes are
// listed in the order they first appear in.
func jOpenTaken(jp JPlan) (map[string]int, []string) {
	out := map[string]int{}
	var order []string
	for _, n := range jp.Notes {
		if n.Existing == "" || strings.HasPrefix(n.Existing, jHoldPrefix) {
			continue
		}
		if n.Req.Op == JOpClose || n.Req.Op == JOpHold {
			if _, seen := out[n.Existing]; !seen {
				order = append(order, n.Existing)
			}
			out[n.Existing] += len(n.Req.Subjects)
		}
	}
	return out, order
}

// JDecide is J's pre stage for the requests of a step (1.3.4; Phases.JDecide):
// the notes, one line for each group of subjects whose judgment an op changes,
// and the plan JCmds writes from. A request with nothing to do makes no note.
// It does not see the step's entries, so it does not close a lateness judgment
// by itself; JDecideEntries does.
func JDecide(st *State, in []NoteReq, obs *Before) ([]tset.Note, JPlan, *Refusal) {
	return JDecideEntries(st, in, obs, nil)
}

// JDecideEntries is JDecide with the step's combined entries (the caller's and
// the derived ones), which let J close the lateness judgment of every timed
// state the step ends (1.3.4). State carries no entries, so the composing twin
// gives them here.
func JDecideEntries(st *State, in []NoteReq, obs *Before, entries []tset.Entry) ([]tset.Note, JPlan, *Refusal) {
	out, ref := jRun(st, in, obs, entries)
	if ref != nil {
		return nil, JPlan{}, ref
	}
	return out.notes, out.plan, nil
}

// jEmit builds J's commands in the order A1 wants: what records owed work
// (the notes list, the overdue and hold entries, the indexes) before the state
// it is owed for ({p}jopen), and what forgets a trigger (a count, a judgment
// leaving jnotes, a subject leaving askwait) after.
type jEmit struct {
	st                     *State
	records, states, after []Cmd
}

func (e *jEmit) cmd(into *[]Cmd, name, key, kind string, args ...string) {
	*into = append(*into, Command(name, key, kind, args...))
}

// pairs writes score-member pairs of one key in pieces of at most jPiece.
func (e *jEmit) pairs(into *[]Cmd, key, score string, members []string) {
	for len(members) != 0 {
		n := min(len(members), jPiece)
		args := make([]string, 0, 2*n)
		for _, m := range members[:n] {
			args = append(args, score, m)
		}
		e.cmd(into, "ZADD", key, kindZSet, args...)
		members = members[n:]
	}
}

// members removes members of one sorted set in pieces of at most jPiece.
func (e *jEmit) members(into *[]Cmd, key string, members []string) {
	for len(members) != 0 {
		n := min(len(members), jPiece)
		e.cmd(into, "ZREM", key, kindZSet, members[:n]...)
		members = members[n:]
	}
}

// JCmds is J's X.plan (Phases.JCmds): the commands of the plan, with each note
// id taken from the seq Layer 2 gave its line (LogPlan.NoteSeqs[Index]). It
// decides nothing: the counts and R it needs are the keys JDecide read, which
// nothing has written before commit. A note whose seq is missing writes
// nothing, which the twin's alignment of seqs with notes never produces.
func JCmds(st *State, jp JPlan, lp LogPlan) []Cmd {
	if st == nil || st.Keys == nil || len(jp.Notes) == 0 {
		return nil
	}
	e := &jEmit{st: st}
	due, jnotes, jn := jKey(st, jKeyDue), jKey(st, jKeyJnotes), jKey(st, jKeyJn)
	askwait := jKey(st, jKeyAskwait)
	var seqs, taken []string
	takenBy := map[string]int{}
	var r int64
	haveR := false
	running := func() (string, bool) {
		if !haveR {
			v, ref := jRunning(st)
			if ref != nil {
				return "", false
			}
			r, haveR = v, true
		}
		return strconv.FormatInt(r, 10), true
	}
	for _, n := range jp.Notes {
		if n.Index < 0 || n.Index >= len(lp.NoteSeqs) || !tset.ValidDecimal(lp.NoteSeqs[n.Index]) {
			continue
		}
		seq := string(lp.NoteSeqs[n.Index])
		seqs = append(seqs, seq)
		req := n.Req
		field := jField(req.Type, req.Cause)
		until := strconv.FormatInt(req.Until, 10)
		askingWait := req.Type == jTypeCannotAsk
		switch req.Op {
		case JOpOpen:
			id := jNoteID(st, tset.Decimal(seq))
			rs, ok := running()
			if !ok {
				continue
			}
			if req.Type != jTypeDone {
				e.cmd(&e.records, "ZADD", due, kindZSet, strconv.FormatInt(r+jOverdueSpanMS, 10), jMemberOverdue+id)
			}
			e.cmd(&e.records, "ZADD", jnotes, kindZSet, rs, id)
			e.cmd(&e.records, "HSET", jn, kindHash, id, strconv.Itoa(len(req.Subjects)))
			if askingWait {
				e.pairs(&e.records, askwait, rs, req.Subjects)
			}
			for _, s := range req.Subjects {
				e.cmd(&e.states, "HSET", jKey(st, jKeyJopen+s), kindHash, field, id)
			}
		case JOpClose, JOpHold, JOpUnhold:
			id := jNoteOf(n.Existing)
			held := strings.HasPrefix(n.Existing, jHoldPrefix)
			if req.Op == JOpHold {
				if req.Type == jTypeStopped {
					e.cmd(&e.records, "HSET", jClockKey(st), kindHash, jClockStopHoldMS, until)
				} else {
					e.cmd(&e.records, "ZADD", due, kindZSet, until, jMemberHold+id)
				}
				for _, s := range req.Subjects {
					e.cmd(&e.states, "HSET", jKey(st, jKeyJopen+s), kindHash, field, jHoldPrefix+id)
				}
			} else {
				for _, s := range req.Subjects {
					e.cmd(&e.states, "HDEL", jKey(st, jKeyJopen+s), kindHash, field)
				}
			}
			if !held && req.Op != JOpUnhold {
				if _, seen := takenBy[id]; !seen {
					taken = append(taken, id)
				}
				takenBy[id] += len(req.Subjects)
			}
		case jOpReview:
			if req.Type != jTypeDone {
				e.cmd(&e.records, "ZADD", due, kindZSet, until, jMemberOverdue+jNoteOf(n.Existing))
			}
		}
	}
	if len(seqs) == 0 {
		return nil
	}
	var head []Cmd
	for len(seqs) != 0 {
		n := min(len(seqs), jPiece)
		e.cmd(&head, "RPUSH", jKey(st, jKeyNotes), kindList, seqs[:n]...)
		seqs = seqs[n:]
	}
	e.members(&e.after, askwait, jAskwaitDrops(jp, func(s string) map[string]string {
		return st.Keys.HGetAll(jKey(st, jKeyJopen+s))
	}))
	for _, id := range taken {
		v, _ := st.Keys.HGet(jn, id)
		have, _ := strconv.Atoi(v)
		left := have - takenBy[id]
		if left > 0 {
			e.cmd(&e.after, "HSET", jn, kindHash, id, strconv.Itoa(left))
			continue
		}
		e.cmd(&e.after, "HDEL", jn, kindHash, id)
		e.cmd(&e.after, "ZREM", jnotes, kindZSet, id)
		e.cmd(&e.after, "ZREM", due, kindZSet, jMemberOverdue+id)
	}
	out := append(head, e.records...)
	out = append(out, e.states...)
	return append(out, e.after...)
}

// JCounts is what J costs a step: the commands and argv bytes it plans, the
// key reads it makes and the notes it writes (8.0's step.Cost, which IT04's
// builder counts a step's X and J with; the step package is not on this base).
// Probes are the typed reads the Lua half issues: one for each subject and
// field, one for each jn count, an HLEN and an HKEYS for each subject whose
// askwait a close may end, and one for the clock when a note opens.
type JCounts struct {
	Commands, ArgvBytes, Probes, Notes int
}

// JCost runs J on the requests and counts what it would plan. Notes are counted
// for each note line, never for each subject: a note naming 2,000 subjects is
// one note (the subjects count against the about bound instead). The seqs are
// the longest one the log can give, so the bytes are an over-estimate.
func JCost(st *State, in []NoteReq, obs *Before, entries []tset.Entry) (JCounts, *Refusal) {
	out, ref := jRun(st, in, obs, entries)
	if ref != nil {
		return JCounts{}, ref
	}
	lp := LogPlan{NoteSeqs: make([]tset.Decimal, len(out.notes))}
	for i := range lp.NoteSeqs {
		lp.NoteSeqs[i] = jSeqCeiling
	}
	c := JCounts{Probes: out.probes, Notes: len(out.notes)}
	for _, cmd := range JCmds(st, out.plan, lp) {
		c.Commands++
		for _, a := range cmd.Argv {
			c.ArgvBytes += len(a)
		}
	}
	return c, nil
}

func init() {
	defaultPhases.JDecide = JDecide
	defaultPhases.JCmds = JCmds
}

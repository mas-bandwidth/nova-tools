package sprintfn

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// J, the judgments of a step (the upper design, EVENT-DRIVEN-TICK version 2.1,
// 1.3.4 and 2.2, as errata 1 to 3 correct it; item IT15). J turns the step's
// note requests into the step's notes in the pre stage, from the real state of
// the sprint's judgment keys, and writes the keys that follow from them in
// X.plan once Layer 2 has given the notes their seqs. Its Lua half is
// internal/nsprint/fn/lua/sprint_j.lua; both halves are held to the golden
// vectors in testdata/j_vectors.json.
//
// What J reads (the pre stage): {p}jopen:<subject>@e for each subject a
// request names, at the field <type>|<cause>, and all its fields for a subject
// that a close or an unhold may take out of askwait (another cause of "cannot
// ask" keeps it there); {p}jn@e and {p}jh@e for each note a close, a hold or an
// unhold would change the counts of; {p}jtext@e for the note an update
// rewrites; {p}due@e for the overdue entry of the note a review wait moves;
// {p}quarantine@e for the primaries a "cannot ask" opens on; {p}clock for R,
// when a note opens. What J writes (X.plan, commands only):
// {p}jopen:<subject>@e, {p}jnotes@e, {p}jn@e, {p}jh@e, {p}jtext@e, {p}notes@e,
// {p}askwait@e, and in {p}due@e the overdue:<note> and hold:<note> entries, and
// {p}clock's stophold_ms for a wait on the STOPPED judgment. The note ids are
// "n" and the seq of the note's line (OpFamily at the epoch), taken from
// LogPlan.NoteSeqs. The epoch is the step's write epoch: the request's, or its
// successor when the step carries an advance entry (L1 1.2), which J reads from
// State.Entries and keeps in its plan, so that what it read and what it writes
// are at one epoch.
//
// The ops (NoteReq.Op), each applied to each subject on the state its field
// holds, where the field is absent, a note id (open) or "h" and a note id
// (held):
//
//	open     absent: one new note on those subjects, with overdue:<note> at R +
//	         10 min (none for "the sprint is done", never overdue, 2.2), jnotes
//	         at R, jn the count, jtext the digest of its text and decisions (when
//	         it has any), and askwait at R for "cannot ask", less the primaries
//	         quarantined (I1: a quarantined id is in no index but sent and
//	         wait:n; the judgment still opens on it). Open or held: nothing (one
//	         per cause: two steps racing open it once).
//	update   open: an update line on the note and its new digest, when the text
//	         or the decisions differ from the ones the note has; nothing at all
//	         when they do not (a second run writes nothing, E7).
//	close    open: the field leaves jopen and jn falls, the note leaving jnotes
//	         and jtext and losing its overdue entry when jn reaches zero. Held:
//	         the field leaves jopen and jh falls, and when it reaches zero the
//	         hold:<note> entry goes with it (the STOPPED judgment clears
//	         stophold_ms instead). askwait loses the subject for "cannot ask".
//	         Absent: nothing ("J closes a subject only when the field is
//	         present").
//	hold     open, and the type one the tick keeps (Judgments[type].TickKept):
//	         the judgment closes with a hold line, the field becomes "h<note>",
//	         jn falls and jh rises by the subjects held, and hold:<note> is
//	         entered at Until (R + d). The STOPPED judgment enters no hold entry
//	         and sets stophold_ms to Until, a wall time, instead. Open, and any
//	         other type: the overdue entry moves to Until and the note stays
//	         open (a review wait), and with the entry at Until already, which
//	         one read of {p}due@e for the note finds, nothing at all: a second
//	         run writes nothing (E7). The one review wait that has no entry to
//	         find is the wait on the type that is never overdue, which writes
//	         its line and nothing else every time it is run: it changes no
//	         state, so a second run cannot be told from a first, and it is a
//	         verb's event, which the verb layer deduplicates by op and intent,
//	         outside E7 (TestJWaitOnANeverOverdueTypeWritesOnlyItsLine pins it).
//	         Held or absent: nothing.
//	unhold   held (R13): the field leaves jopen, jh falls as a close's does, an
//	         unheld line is written.
//	know     a notice (Notices[type]): its line, and nothing else.
//	request  a request line, and nothing else.
//
// One note for a cause (1.3.4, "a note names every subject of its step with the
// same type and cause"): requests of one op, type, cause, text, decisions and
// time, whatever their subjects, are one request before they are decided, cut at
// 2,000 subjects, so that a caller that sends a request for each waiter makes
// one note and not a hundred. A caller that puts a different text in each makes
// a note each, since a note has one text.
//
// jh and jtext are J's own keys beside 1.3.1's list: jh, note to the number of
// its held subjects, is what lets the last close or unhold of a hold remove
// hold:<note> (a hold can name several subjects and J cannot read the others
// from one subject's field); jtext, note to the digest of its text and
// decisions, is what lets an update find that nothing changed.
//
// JPlan is IT12's type and its meaning is J's: Notes has one JNote for each note
// J made, Index the note's place in the step's notes (so LogPlan.NoteSeqs[Index]
// is its line's seq), Req the request resolved to that line (the op, with a wait
// on a judgment the tick does not keep as "review", and the subjects the line is
// about), Existing "" for a new note, else the note id the subjects' fields held,
// with "h" before it for a hold, and Digest the digest of its text and decisions.
// A request that changes nothing has none. The counts J read and the rest it
// carries to JCmds are in the plan, which JCmds reads nothing beside.
//
// A step that ends a timed state closes the lateness judgment of that kind on
// the subject R11 raised it on (the card; for the stream's, sprint.StreamSubject
// of the stream, jLateSubject) without being asked (1.3.4): J reads the step's
// combined entries from State.Entries, the twin calls J for any step that
// carries entries as well as for one that carries a note request
// (Phases.JOnEntries, which the defaults set, and the Lua core does the same
// once j_decide is registered), and Phases.JBefore names the fields that takes.
// LatenessJudgment is the type and cause R11 must raise it with. JCost counts
// what a step's J costs, as the step builder does (8.0: Coster).
//
// Where the design is silent or contradicts itself, the narrower reading is
// taken, and the rest is left to ask. What the reading took:
//
//	the lines' kinds (events.go): Layer 3 reads judgment, happened, decided
//	and acknowledged, and refuses any other. An open is judgment, an update
//	judgment with the verb "updated", a know happened, a close and an unhold
//	decided, a hold, a review wait and a request no kind (a plain note, which
//	queues nothing). NoteReq has no field that says decided from acknowledged,
//	so J writes decided. Every close J makes, the machine's own (the owner rule
//	finding its condition cleared, a timed state ending) as well as a verb's,
//	is therefore a decided line, and ingest queues the owner key of the type
//	for it: the rules are level-triggered, so a woken owner rule that finds
//	nothing to do removes its key (2.1). An unheld line queues it as 2.1 says.
//	ops and step: a (type, cause, subject) named by two state requests of one
//	step is REQUEST: two requests on one field have no order.
//	never overdue (2.2): Judgment.TickKept is one bool, where the wait column
//	has holds, review and never overdue; J holds the one type that is never
//	overdue itself.
//	the overdue span (1.2): written + 10 min; Until is not read on an open.
//	askwait's score (1.3.1: id -> R): an open writes R, also for a subject that
//	another cause keeps there, since Layer 1's registry has no ZADD NX (errata 2,
//	item 5).
//	the stream of a note (events.go): NoteReq has no stream, so a line's meta
//	has none, and ingest's rows that key on a stream find none.
//	the entries J sees: the caller's, and the derive phase's. J sees the
//	before-state of the cards the caller's entries name (Phases.JBefore asks for
//	it before derive runs); a derived entry that ends a timed state of a card
//	the caller did not name is seen by the Lua half, which asks S.before itself,
//	and not by the twin. Derive's entries stay on the work table (1.3.3), which
//	has no timed state of a due kind, so the two agree.

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
	jKeyJopen      = "jopen:"     // jopen:<subject>@e, HASH <type>|<cause> -> <note> or h<note>
	jKeyJnotes     = "jnotes"     // ZSET note -> R, every open note
	jKeyJn         = "jn"         // HASH note -> count of its open subjects
	jKeyJh         = "jh"         // HASH note -> count of its held subjects (J's own, beside 1.3.1)
	jKeyJtext      = "jtext"      // HASH note -> digest of its text and decisions (J's own, beside 1.3.1)
	jKeyNotes      = "notes"      // LIST of the seqs of every note line
	jKeyAskwait    = "askwait"    // ZSET primary -> R, "cannot ask" open or held
	jKeyDue        = "due"        // ZSET <kind>:<id> -> running ms
	jKeyClock      = "clock"      // HASH, sprint: the running clock (1.2)
	jKeyQuarantine = "quarantine" // HASH id -> the refusal that quarantined it (1.3.5)
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

// jCountDigitsMax is the most digits a count in jn or jh may have: a note names
// at most 2,000 subjects, so a count of 10 digits is a key that disagrees with
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

// jReason is the reason a close carries when a timed state of one due kind
// ends, by the way it ends: moved to a column (the reason is the one the column
// says), moved anywhere else, or its due field cleared in place.
type jReason struct {
	moved   map[string]string // by the column the card moves to
	other   string            // a move to any other column, or another row for a kind keyed by row
	cleared string            // the due field unset or set empty, the card staying
}

// jEndReasons are the reasons of each due kind. A card that moves to
// withdrawn was replaced by the machine (R11), not taken or finished, and a
// card that moves somewhere nothing names is said to have had its state end.
var jEndReasons = map[string]jReason{
	"untaken":    {moved: map[string]string{"working": "taken", "withdrawn": "replaced"}, other: jReasonEnded, cleared: jReasonCleared},
	"unfinished": {moved: map[string]string{"ok": "finished", "failed": "finished", "withdrawn": "replaced"}, other: jReasonEnded, cleared: jReasonCleared},
	"unbegun":    {moved: map[string]string{"reading": "begun", "ok": "reported", "broken": "reported"}, other: jReasonEnded, cleared: jReasonCleared},
	"unreported": {moved: map[string]string{"ok": "reported", "broken": "reported"}, other: jReasonEnded, cleared: jReasonCleared},
	"mergeidle":  {moved: map[string]string{}, other: jReasonMerging, cleared: jReasonMerging},
}

// The ways a timed state ends that are not a kind's own.
const (
	jReasonRemoved = "removed"                  // the card left the table
	jReasonCleared = "cleared"                  // the card stayed and its due field went
	jReasonEnded   = "state ended"              // the card moved where no reason names
	jReasonMerging = "stream no longer merging" // a stream's control card: it stopped merging
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

// jKeyAt is a per epoch sprint key: {p}<name>@<epoch>.
func jKeyAt(st *State, epoch tset.Decimal, name string) string {
	return st.Prefix + "sprint:" + name + "@" + string(epoch)
}

func jClockKey(st *State) string { return st.Prefix + "sprint:" + jKeyClock }

func jField(typ, cause string) string { return typ + jFieldSep + cause }

// jWriteEpoch is the epoch J reads and writes its keys at: the request's, or its
// successor when the step advances it (L1 1.2; the Lua half's ctx.write_epoch).
func jWriteEpoch(epoch tset.Decimal, entries []tset.Entry) (tset.Decimal, *Refusal) {
	for _, e := range entries {
		if e.Kind != "advance" {
			continue
		}
		next, err := tset.NextDecimal(epoch)
		if err != nil {
			code := CodeRequest
			var lref *tset.Refusal
			if errors.As(err, &lref) {
				code = lref.Code
			}
			return "", refuse(PhaseJ, code, RefusalDetail{})
		}
		return next, nil
	}
	return epoch, nil
}

// jNoteID is the id of the note whose line has the seq: "n" and the seq, with
// the epoch after it from epoch 1 (OpFamily, 1.3.4).
func jNoteID(epoch tset.Decimal, seq tset.Decimal) string {
	n, err := strconv.ParseUint(string(epoch), 10, 64)
	if err != nil {
		n = 0 // the epoch is a validated decimal; unreachable
	}
	return sprint.OpFamily(jNotePrefix+string(seq), n)
}

// jDigest is the digest of a note's text and decisions, which jtext holds for
// an open note: SHA-1 of the lengths and the words, so that no two different
// pairs read alike. The Lua half hashes the same bytes with redis.sha1hex.
func jDigest(text string, decisions []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d:%s%d;", len(text), text, len(decisions))
	for _, d := range decisions {
		fmt.Fprintf(&b, "%d:%s", len(d), d)
	}
	sum := sha1.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// jEmptyDigest is the digest of a note with no text and no decisions, which
// jtext does not store: an absent jtext field is that digest.
var jEmptyDigest = jDigest("", nil)

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

// jMergeKey is what makes two state requests one note: the op, the type and
// cause, the text and decisions, and the time. Every part is length-prefixed, so
// no two different requests make one key.
func jMergeKey(r NoteReq) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d:%s%d:%s%d:%s%d:%s%d;", len(r.Op), r.Op, len(r.Type), r.Type, len(r.Cause), r.Cause, len(r.Text), r.Text, len(r.Decisions))
	for _, d := range r.Decisions {
		fmt.Fprintf(&b, "%d:%s", len(d), d)
	}
	fmt.Fprintf(&b, "%d", r.Until)
	return b.String()
}

// jMerge makes the state requests of one op, type, cause, text, decisions and
// time one request, in the place of the first, and cuts a request of more than
// 2,000 subjects into pieces of 2,000, so that a step names one note for each
// type and cause (1.3.4) whether its caller sent a request for each waiter or
// one for all. The other requests keep their places. The work is one pass over
// the requests, and the key of a request is read once.
func jMerge(reqs []NoteReq) []NoteReq {
	out := make([]NoteReq, 0, len(reqs))
	at := map[string]int{}
	for _, r := range reqs {
		if !jIsStateOp(r.Op) {
			out = append(out, r)
			continue
		}
		subjects := jDedup(r.Subjects)
		k := jMergeKey(r)
		if i, ok := at[k]; ok {
			out[i].Subjects = append(out[i].Subjects, subjects...)
			continue
		}
		at[k] = len(out)
		r.Subjects = subjects
		out = append(out, r)
	}
	var cut []NoteReq
	for _, r := range out {
		if !jIsStateOp(r.Op) || len(r.Subjects) <= jSubjectsMax {
			cut = append(cut, r)
			continue
		}
		for rest := r.Subjects; len(rest) != 0; {
			n := min(len(rest), jSubjectsMax)
			piece := r
			piece.Subjects = rest[:n:n]
			cut = append(cut, piece)
			rest = rest[n:]
		}
	}
	return cut
}

// jDecider is one call of J's pre stage: what it has read, counted as probes.
type jDecider struct {
	st      *State
	epoch   tset.Decimal
	probes  int
	cells   map[string]jRead // by key, NUL, field
	all     map[string]map[string]bool
	overdue map[string]jScore // by note: the score of its overdue entry
}

// jScore is the score of a member of the due set: its value, and whether it was
// there.
type jScore struct {
	score   float64
	present bool
}

// jRead is one key J read: its value, and whether it was there.
type jRead struct {
	val     string
	present bool
}

// at is a per epoch key at the epoch the call writes.
func (d *jDecider) at(name string) string { return jKeyAt(d.st, d.epoch, name) }

// checkHash is the typed read's refusal for a key of another type.
func (d *jDecider) checkHash(key string) *Refusal {
	switch d.st.Keys.Type(key) {
	case kindNone, kindHash:
		return nil
	}
	return refuse(PhaseJ, CodeWrongType, RefusalDetail{})
}

// cell is one field of a hash, read once a call: a key of another type is
// WRONGTYPE.
func (d *jDecider) cell(key, field string) (jRead, *Refusal) {
	memo := key + "\x00" + field
	if r, ok := d.cells[memo]; ok {
		return r, nil
	}
	d.probes++
	if ref := d.checkHash(key); ref != nil {
		return jRead{}, ref
	}
	v, ok := d.st.Keys.HGet(key, field)
	d.cells[memo] = jRead{v, ok}
	return d.cells[memo], nil
}

// jopenFields is every field of a subject's jopen hash, read once a call: the
// judgments open or held on it.
func (d *jDecider) jopenFields(subject string) map[string]bool {
	if f, ok := d.all[subject]; ok {
		return f
	}
	got := d.st.Keys.HGetAll(d.at(jKeyJopen + subject))
	d.probes++ // HLEN
	if len(got) != 0 {
		d.probes++ // and HKEYS, which the Lua half reads bounded by the length
	}
	f := make(map[string]bool, len(got))
	for name := range got {
		f[name] = true
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
	r, ref := d.cell(d.at(jKeyJopen+subject), field)
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

// jCountOf reads a count as jn and jh hold it: one to nine digits, no leading
// zero, at least one. ok is false for a value that is no count.
func jCountOf(v string) (int, bool) {
	n, err := strconv.Atoi(v)
	if len(v) > jCountDigitsMax || err != nil || n < 1 || strconv.Itoa(n) != v {
		return 0, false
	}
	return n, true
}

// count is jn[note], the open subjects of a note; it is DRIFT for a note that
// jopen says is open and jn has no count of.
func (d *jDecider) count(note string) (int, *Refusal) {
	r, ref := d.cell(d.at(jKeyJn), note)
	if ref != nil {
		return 0, ref
	}
	n, ok := jCountOf(r.val)
	if !r.present || !ok {
		return 0, refuse(PhaseJ, jCodeDrift, RefusalDetail{})
	}
	return n, nil
}

// held is jh[note], the held subjects of a note. A note whose subjects a step
// takes out of the hold must have it (DRIFT when it has none); a note a step
// holds more subjects of may have none, which is zero.
func (d *jDecider) held(note string, must bool) (int, *Refusal) {
	r, ref := d.cell(d.at(jKeyJh), note)
	if ref != nil {
		return 0, ref
	}
	if !r.present && !must {
		return 0, nil
	}
	n, ok := jCountOf(r.val)
	if !r.present || !ok {
		return 0, refuse(PhaseJ, jCodeDrift, RefusalDetail{})
	}
	return n, nil
}

// textDigest is the digest of the text and decisions an open note has: jtext's
// field, or the empty digest when it has none.
func (d *jDecider) textDigest(note string) (string, *Refusal) {
	r, ref := d.cell(d.at(jKeyJtext), note)
	if ref != nil {
		return "", ref
	}
	if !r.present {
		return jEmptyDigest, nil
	}
	return r.val, nil
}

// overdueScore is the score of overdue:<note> in {p}due@e, read once a call for
// each note: a review wait finds the review time already there and writes
// nothing (E7). A key of another type is WRONGTYPE.
func (d *jDecider) overdueScore(note string) (jScore, *Refusal) {
	if r, ok := d.overdue[note]; ok {
		return r, nil
	}
	d.probes++
	key := d.at(jKeyDue)
	switch d.st.Keys.Type(key) {
	case kindNone, kindZSet:
	default:
		return jScore{}, refuse(PhaseJ, CodeWrongType, RefusalDetail{})
	}
	score, ok := d.st.Keys.ZScore(key, jMemberOverdue+note)
	d.overdue[note] = jScore{score, ok}
	return d.overdue[note], nil
}

// quarantined reads {p}quarantine@e over the subjects, in pieces of at most
// jPiece: a probe for each piece, and never one for each subject.
func (d *jDecider) quarantined(subjects []string, into map[string]bool) *Refusal {
	key := d.at(jKeyQuarantine)
	for len(subjects) != 0 {
		n := min(len(subjects), jPiece)
		d.probes++
		if ref := d.checkHash(key); ref != nil {
			return ref
		}
		for _, s := range subjects[:n] {
			if _, ok := d.st.Keys.HGet(key, s); ok {
				into[s] = true
			}
		}
		subjects = subjects[n:]
	}
	return nil
}

// jDigits reads a clock field: absent is 0, and anything but 1 to 15 digits is
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
	digest   string // the digest of the text and decisions, for an open and an update
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
	digest := jDigest(r.Text, r.Decisions)
	if r.Op == JOpOpen {
		if len(fresh) == 0 {
			return nil, nil
		}
		r.Subjects = fresh
		return []jGroup{{req: r, digest: digest}}, nil
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
			have, ref := d.textDigest(b.state.id)
			if ref != nil {
				return nil, ref
			}
			if have == digest {
				continue // the text and the decisions are the note's own: nothing changed
			}
			g.digest = digest
		case JOpHold:
			if held {
				continue // already held
			}
			if !tickKept {
				if r.Type != jTypeDone {
					// A review wait moves the overdue entry to the review time: with the
					// entry already there, the wait was run before on this state.
					od, ref := d.overdueScore(b.state.id)
					if ref != nil {
						return nil, ref
					}
					if od.present && od.score == float64(r.Until) {
						continue
					}
				}
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

// jLateSubject is the subject R11 raises the lateness judgment of a due kind on
// (1.3.4, 2.2), which J's close must name for the two to meet: the card's id
// (sprint.CardID of the stored id) for a kind keyed by card, and for a kind
// keyed by row the subject of a stream-level judgment, sprint.StreamSubject of
// the row, the form every reader of the stream's judgments uses (notes.go,
// check.go, held.go). The Lua half gets the prefix from the generated block
// (SP.j_stream_prefix), so that a change to StreamSubject is a changed block.
func jLateSubject(k sprint.DueKind, stored, row string) string {
	if k.OfRow {
		return sprint.StreamSubject(row)
	}
	return sprint.CardID(stored)
}

// jEndings are the requests that close the lateness judgments of the timed
// states the step's entries end (1.3.4): for each entry that moves or removes a
// card out of a due kind's state, or clears its due field, one close on the
// subject R11 raised it on (jLateSubject: the card, or the stream for a kind
// keyed by row), by the type and cause R11 raises it under. Only cards obs holds
// are seen; a request that already names a (type, cause, subject) is not added
// a second time.
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
				subject := jLateSubject(k, id, rec.Place.Row)
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
// with no due field is in no due set, 1.2). The reason is the kind's own for the
// way it ends (jEndReasons): where the card went, for a move.
func jEnds(e tset.Entry, i int, k sprint.DueKind, rec tset.MemberRecord) (string, bool) {
	if e.Kind == "remove" {
		return jReasonRemoved, true
	}
	why := jEndReasons[k.Kind]
	if e.To != "" && e.To != e.From {
		row, col := jCell(e.To)
		if col != k.Col || (k.OfRow && row != rec.Place.Row) {
			if r, ok := why.moved[col]; ok {
				return r, true
			}
			return why.other, true
		}
	}
	for _, f := range e.Unset {
		if f == k.Field {
			return why.cleared, true
		}
	}
	if v, ok := e.Set[k.Field]; ok && v == "" {
		return why.cleared, true
	}
	if i < len(e.Each) {
		if v, ok := e.Each[i][k.Field]; ok && v == "" {
			return why.cleared, true
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

// JBefore names the fields J reads of the cards a step moves or removes
// (Phases.JBefore): the due field of every timed state such a card may be in,
// so that JDecide sees it end.
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

// jCalc is what J read in the pre stage and its commands need: the epoch it
// writes at, R, the counts of the notes it changes, the jopen fields of the
// subjects that may leave askwait, and the primaries found quarantined. JCmds
// reads nothing beside it.
type jCalc struct {
	epoch  tset.Decimal
	r      int64
	haveN  map[string]int
	haveH  map[string]int
	fields map[string]map[string]bool
	quar   map[string]bool
}

// jOutcome is what J's pre stage decided: the notes, the plan X.plan takes, and
// what the store reads cost.
type jOutcome struct {
	notes  []tset.Note
	plan   JPlan
	probes int
}

// jHeld is what a step does to one note's held subjects: how many it puts in a
// hold and how many it takes out, and the type, which says whether the hold is a
// hold entry or the STOPPED judgment's stophold_ms.
type jHeld struct {
	enter, leave int
	typ          string
}

// jEffects is what a list of notes does to the counts and to askwait.
type jEffects struct {
	taken      map[string]int // open subjects a close or a hold takes off each note's count
	takenOrder []string
	held       map[string]*jHeld
	heldOrder  []string
	leaving    map[string]map[string]bool // subject -> the "cannot ask" fields a close or an unhold takes
	leaveOrder []string
	staying    map[string]bool // subjects a "cannot ask" opens on
}

// jEffectsOf is the effects of the notes, each in the order it first appears.
func jEffectsOf(notes []JNote) jEffects {
	e := jEffects{taken: map[string]int{}, held: map[string]*jHeld{}, leaving: map[string]map[string]bool{}, staying: map[string]bool{}}
	for _, n := range notes {
		req := n.Req
		isHeld := strings.HasPrefix(n.Existing, jHoldPrefix)
		switch {
		case n.Existing != "" && !isHeld && (req.Op == JOpClose || req.Op == JOpHold):
			if _, seen := e.taken[n.Existing]; !seen {
				e.takenOrder = append(e.takenOrder, n.Existing)
			}
			e.taken[n.Existing] += len(req.Subjects)
		}
		if n.Existing != "" && (req.Op == JOpHold || (isHeld && (req.Op == JOpClose || req.Op == JOpUnhold))) {
			note := jNoteOf(n.Existing)
			h := e.held[note]
			if h == nil {
				h = &jHeld{typ: req.Type}
				e.held[note] = h
				e.heldOrder = append(e.heldOrder, note)
			}
			if req.Op == JOpHold {
				h.enter += len(req.Subjects)
			} else {
				h.leave += len(req.Subjects)
			}
		}
		if req.Type != jTypeCannotAsk {
			continue
		}
		field := jField(req.Type, req.Cause)
		switch req.Op {
		case JOpOpen:
			for _, s := range req.Subjects {
				e.staying[s] = true
			}
		case JOpClose, JOpUnhold:
			for _, s := range req.Subjects {
				if e.leaving[s] == nil {
					e.leaving[s] = map[string]bool{}
					e.leaveOrder = append(e.leaveOrder, s)
				}
				e.leaving[s][field] = true
			}
		}
	}
	return e
}

// drops is the subjects that leave askwait in this step (1.3.1: a primary is in
// it while "cannot ask" is open or held on it): those whose last "cannot ask"
// field, of any cause, a close or an unhold takes from jopen, with none opened
// on them in the same step. fields is a subject's jopen fields.
func (e jEffects) drops(fields func(subject string) map[string]bool) []string {
	var out []string
	for _, s := range e.leaveOrder {
		if e.staying[s] {
			continue
		}
		keep := false
		for f := range fields(s) {
			if strings.HasPrefix(f, jTypeCannotAsk+jFieldSep) && !e.leaving[s][f] {
				keep = true
				break
			}
		}
		if !keep {
			out = append(out, s)
		}
	}
	return out
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
	epoch, ref := jWriteEpoch(st.Epoch, entries)
	if ref != nil {
		return jOutcome{}, ref
	}
	reqs := append([]NoteReq(nil), in...)
	if len(entries) != 0 {
		reqs = append(reqs, jEndings(entries, obs, named)...)
	}
	reqs = jMerge(reqs)
	d := &jDecider{st: st, epoch: epoch, cells: map[string]jRead{}, all: map[string]map[string]bool{}, overdue: map[string]jScore{}}
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
			out.plan.Notes = append(out.plan.Notes, JNote{Index: len(out.notes), Req: g.req, Existing: g.existing, Digest: g.digest})
			out.notes = append(out.notes, tset.Note{Line: tset.NoteLine{Kind: "note", Meta: jMetaOf(g)}, About: g.req.Subjects})
		}
	}
	calc := &jCalc{epoch: epoch, haveN: map[string]int{}, haveH: map[string]int{},
		fields: map[string]map[string]bool{}, quar: map[string]bool{}}
	out.plan.calc = calc
	eff := jEffectsOf(out.plan.Notes)
	// A close or a hold of open subjects takes them off their note's count: it
	// must have the count, and one to take them from (jn agrees with jopen).
	for _, note := range eff.takenOrder {
		have, ref := d.count(note)
		if ref != nil {
			return jOutcome{}, ref
		}
		if have < eff.taken[note] {
			return jOutcome{}, refuse(PhaseJ, jCodeDrift, RefusalDetail{})
		}
		calc.haveN[note] = have
	}
	// A hold puts subjects in the note's held count, and a close or an unhold of
	// held subjects takes them out (jh agrees with jopen).
	for _, note := range eff.heldOrder {
		h := eff.held[note]
		have, ref := d.held(note, h.leave != 0)
		if ref != nil {
			return jOutcome{}, ref
		}
		if have < h.leave {
			return jOutcome{}, refuse(PhaseJ, jCodeDrift, RefusalDetail{})
		}
		calc.haveH[note] = have
	}
	// The fields of each subject that may leave askwait, and the primaries a
	// "cannot ask" opens on that are quarantined.
	for _, s := range eff.leaveOrder {
		calc.fields[s] = d.jopenFields(s)
	}
	var asking []string
	seen := map[string]bool{}
	for _, n := range out.plan.Notes {
		if n.Req.Op != JOpOpen || n.Req.Type != jTypeCannotAsk {
			continue
		}
		for _, s := range n.Req.Subjects {
			if !seen[s] {
				seen[s] = true
				asking = append(asking, s)
			}
		}
	}
	if ref := d.quarantined(asking, calc.quar); ref != nil {
		return jOutcome{}, ref
	}
	if opens { // R, from the clock, is read once for the notes that open
		r, ref := jRunning(st)
		if ref != nil {
			return jOutcome{}, ref
		}
		d.probes++
		calc.r = r
	}
	out.probes = d.probes
	return out, nil
}

// JDecide is J's pre stage for the requests of a step (1.3.4; Phases.JDecide):
// the notes, one line for each group of subjects whose judgment an op changes,
// and the plan JCmds writes from. A request with nothing to do makes no note.
// It reads the step's combined entries from st.Entries, which let it close the
// lateness judgment of every timed state the step ends (1.3.4).
func JDecide(st *State, in []NoteReq, obs *Before) ([]tset.Note, JPlan, *Refusal) {
	if st == nil {
		return nil, JPlan{}, refuse(PhaseJ, CodeConfig, RefusalDetail{})
	}
	return JDecideEntries(st, in, obs, st.Entries)
}

// JDecideEntries is JDecide with the entries given, for a caller that has them
// apart from the State.
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
// leaving jnotes, a subject leaving askwait, a hold entry) after.
type jEmit struct {
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
// decides nothing and reads nothing: the counts, R and the fields it needs are
// in the plan, which JDecide read. A note whose seq is missing writes nothing,
// and neither do its counts, its holds or its askwait: what a step takes off a
// count, out of a hold or out of askwait is worked out over the notes that
// have a line, which the twin's alignment of seqs with notes never leaves short.
func JCmds(st *State, jp JPlan, lp LogPlan) []Cmd {
	if st == nil || jp.calc == nil || len(jp.Notes) == 0 {
		return nil
	}
	c := jp.calc
	at := func(name string) string { return jKeyAt(st, c.epoch, name) }
	var seqed []JNote
	var seqs []string
	for _, n := range jp.Notes {
		if n.Index < 0 || n.Index >= len(lp.NoteSeqs) || !tset.ValidDecimal(lp.NoteSeqs[n.Index]) {
			continue
		}
		seqed = append(seqed, n)
		seqs = append(seqs, string(lp.NoteSeqs[n.Index]))
	}
	if len(seqed) == 0 {
		return nil
	}
	e := &jEmit{}
	due, jnotes, jn, jh, jtext := at(jKeyDue), at(jKeyJnotes), at(jKeyJn), at(jKeyJh), at(jKeyJtext)
	askwait := at(jKeyAskwait)
	eff := jEffectsOf(seqed)
	rs := strconv.FormatInt(c.r, 10)
	for i, n := range seqed {
		seq := seqs[i]
		req := n.Req
		field := jField(req.Type, req.Cause)
		until := strconv.FormatInt(req.Until, 10)
		switch req.Op {
		case JOpOpen:
			id := jNoteID(c.epoch, tset.Decimal(seq))
			if req.Type != jTypeDone {
				e.cmd(&e.records, "ZADD", due, kindZSet, strconv.FormatInt(c.r+jOverdueSpanMS, 10), jMemberOverdue+id)
			}
			e.cmd(&e.records, "ZADD", jnotes, kindZSet, rs, id)
			e.cmd(&e.records, "HSET", jn, kindHash, id, strconv.Itoa(len(req.Subjects)))
			if n.Digest != jEmptyDigest {
				e.cmd(&e.records, "HSET", jtext, kindHash, id, n.Digest)
			}
			if req.Type == jTypeCannotAsk {
				var put []string
				for _, s := range req.Subjects {
					if !c.quar[s] {
						put = append(put, s)
					}
				}
				e.pairs(&e.records, askwait, rs, put)
			}
			for _, s := range req.Subjects {
				e.cmd(&e.states, "HSET", at(jKeyJopen+s), kindHash, field, id)
			}
		case JOpUpdate:
			e.cmd(&e.records, "HSET", jtext, kindHash, n.Existing, n.Digest)
		case JOpHold:
			id := jNoteOf(n.Existing)
			if req.Type == jTypeStopped {
				e.cmd(&e.records, "HSET", jClockKey(st), kindHash, jClockStopHoldMS, until)
			} else {
				e.cmd(&e.records, "ZADD", due, kindZSet, until, jMemberHold+id)
			}
			for _, s := range req.Subjects {
				e.cmd(&e.states, "HSET", at(jKeyJopen+s), kindHash, field, jHoldPrefix+id)
			}
		case JOpClose, JOpUnhold:
			for _, s := range req.Subjects {
				e.cmd(&e.states, "HDEL", at(jKeyJopen+s), kindHash, field)
			}
		case jOpReview:
			if req.Type != jTypeDone {
				e.cmd(&e.records, "ZADD", due, kindZSet, until, jMemberOverdue+jNoteOf(n.Existing))
			}
		}
	}
	// The held counts a hold raises are recorded before the fields become holds;
	// the ones a close or an unhold lowers are forgotten after the fields go, and
	// a count that reaches zero takes the hold entry with it (or the STOPPED
	// judgment's stophold_ms, which is that judgment's hold).
	for _, note := range eff.heldOrder {
		h := eff.held[note]
		net := c.haveH[note] + h.enter - h.leave
		into := &e.after
		if h.enter != 0 {
			into = &e.records
		}
		if net > 0 {
			e.cmd(into, "HSET", jh, kindHash, note, strconv.Itoa(net))
			continue
		}
		e.cmd(into, "HDEL", jh, kindHash, note)
		if h.typ == jTypeStopped {
			e.cmd(into, "HDEL", jClockKey(st), kindHash, jClockStopHoldMS)
		} else {
			e.cmd(into, "ZREM", due, kindZSet, jMemberHold+note)
		}
	}
	var head []Cmd
	for rest := seqs; len(rest) != 0; {
		n := min(len(rest), jPiece)
		e.cmd(&head, "RPUSH", at(jKeyNotes), kindList, rest[:n]...)
		rest = rest[n:]
	}
	e.members(&e.after, askwait, eff.drops(func(s string) map[string]bool { return c.fields[s] }))
	for _, id := range eff.takenOrder {
		left := c.haveN[id] - eff.taken[id]
		if left > 0 {
			e.cmd(&e.after, "HSET", jn, kindHash, id, strconv.Itoa(left))
			continue
		}
		e.cmd(&e.after, "HDEL", jn, kindHash, id)
		e.cmd(&e.after, "ZREM", jnotes, kindZSet, id)
		e.cmd(&e.after, "ZREM", due, kindZSet, jMemberOverdue+id)
		e.cmd(&e.after, "HDEL", jtext, kindHash, id)
	}
	out := append(head, e.records...)
	out = append(out, e.states...)
	return append(out, e.after...)
}

// JCounts is what J costs a step: the commands and argv bytes it plans, the
// key reads it makes and the notes it writes (8.0's step.Cost, which IT04's
// builder counts a step's X and J with; the step package is not on this base).
// Probes are the typed reads the Lua half issues: one for each subject and
// field, one for each jn or jh count, one for each jtext digest, an HLEN and an
// HKEYS for each subject whose askwait a close may end, one for each piece of
// 1,000 primaries whose quarantine a "cannot ask" reads, one for the overdue
// entry of each note a review wait moves, and one for the clock when a note
// opens.
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
	defaultPhases.JBefore = JBefore
	defaultPhases.JOnEntries = true
	defaultPhases.JDecide = JDecide
	defaultPhases.JCmds = JCmds
}

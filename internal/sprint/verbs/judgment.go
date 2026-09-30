package verbs

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The verbs that answer judgments (section 3, ack and wait; item IT22): each
// reads the notes it names through jnote (IT30: each note's type, cause and
// subjects from its line, and what each subject's jopen holds for the note's
// own type and cause), plans on that read, and writes one step whose note
// requests J turns into the closes and holds of 1.3.4 (IT15). Both are the
// coordinator's (2.2): X refuses them NOTCOORD from another actor (1.5.3).
// Both take a set of notes, n = 1 included (1.5.3, "every verb takes a set").

// AckNotesMax is the most notes one ack or wait names: a step's notes (L1 6),
// since each note it answers is at most one request to J.
const AckNotesMax = tset.MaxNotes

// noteSubjectsMax is the most subjects a note names: J cuts a note at 2,000
// (1.3.4; L2 1.2's ids a line), so a jnote of this many subjects reads every
// note J writes.
const noteSubjectsMax = tset.MaxIDsPerLine

// jnoteChunk is the notes one jnote query reads. IT30 declares a jnote at 1 +
// subjects records a note (sprint.QueryCost), and a query at most 10,000
// records (L1 7), so at 2,000 subjects a note one query reads four notes; a
// read carries as many queries as the notes need.
const jnoteChunk = 4

// The judgment types these verbs treat by name (2.2's words; IT06's rows).
const (
	typeBlockedDropped = "a primary is blocked on something dropped" // ack waives the dropped need
	typeBlockedMissing = "a primary is blocked on something missing" // ack waives n while n has no record
	typeCouldNotMove   = "the machine could not move a card"         // ack clears refused (1.3.5)
	typeStepRefused    = "the machine's step was refused"            // ack unparks the key (1.3.5; F1-22)
	typeStoppedDue     = "the machine is STOPPED and moves are due"  // wait sets stophold_ms (1.3.4)
)

// fieldRefused is the card field a planner refusal sets (1.3.5).
const fieldRefused = "refused"

// noteIDRE is a note id: "n" and the seq of its line, with the epoch suffix of
// OpFamily from epoch 1 (1.3.4).
var noteIDRE = regexp.MustCompile(`^n[1-9][0-9]{0,15}(~[1-9][0-9]{0,19})?$`)

// AckReq is ack's request: the notes, and the reason every close records.
type AckReq struct {
	Op     string
	Notes  []string
	Reason string
}

// WaitReq is wait's request: the notes, how long, and the reason.
type WaitReq struct {
	Op     string
	Notes  []string
	For    time.Duration
	Reason string
}

// noteSet checks a verb's notes before anything is read: at least one, at most
// AckNotesMax, each a note id, none twice. Sorted, so the op's intent is the
// set's and not the order it was typed in (1.5.3).
func noteSet(verb string, notes []string, reason string) ([]string, error) {
	if len(notes) == 0 {
		return nil, refuseLocal(verb, sprintfn.CodeRequest, "%s names no note: give the ids inbox prints", verb)
	}
	if len(notes) > AckNotesMax {
		return nil, refuseLocal(verb, sprintfn.CodeLimit, "%s names %d notes, over a step's %d", verb, len(notes), AckNotesMax)
	}
	if strings.TrimSpace(reason) == "" {
		return nil, refuseLocal(verb, sprintfn.CodeRequest, "%s wants --reason: the reason is the answer the note records", verb)
	}
	seen := make(map[string]bool, len(notes))
	out := make([]string, 0, len(notes))
	for _, n := range notes {
		if !noteIDRE.MatchString(n) {
			return nil, refuseLocal(verb, sprintfn.CodeRequest, "%q is not a note id (n<seq>, as inbox prints it)", n)
		}
		if seen[n] {
			return nil, refuseLocal(verb, sprintfn.CodeRequest, "note %s is named twice", n)
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// notesAt are the notes of the epoch the read is at: jnote refuses a note of
// another epoch (IT30), so those are left out of the read and refused by the
// plan, naming their epoch (sprint.OtherEpoch).
func notesAt(notes []string, epoch tset.Decimal) []string {
	n, err := strconv.ParseUint(string(epoch), 10, 64)
	if err != nil {
		return nil
	}
	var out []string
	for _, id := range notes {
		if sprint.IDEpoch(id) == n {
			out = append(out, id)
		}
	}
	return out
}

// jnoteQueries are the jnote queries that read the notes, jnoteChunk a query.
func jnoteQueries(notes []string) []sprintfn.SprintQuery {
	var out []sprintfn.SprintQuery
	for from := 0; from < len(notes); from += jnoteChunk {
		chunk := notes[from:min(from+jnoteChunk, len(notes))]
		q, ref := sprintfn.EncodeSprintQ(sprint.SprintQ{Kind: sprint.QueryJnote, Fields: []string{},
			Source: sprint.IDSource{Kind: sprint.SourceIDs, IDs: append([]string(nil), chunk...)}, Subjects: noteSubjectsMax})
		if ref != nil {
			panic(fmt.Sprintf("verbs: a jnote of checked note ids refused: %v", ref)) // noteSet checked each id
		}
		out = append(out, q)
	}
	return out
}

// noteItems decodes the jnote answers at the sprint slots from first on, by
// note id.
func noteItems(rd *sprintfn.ReadReply, first, queries int) (map[string]sprintfn.NoteItem, error) {
	out := map[string]sprintfn.NoteItem{}
	for i := first; i < first+queries; i++ {
		if i >= len(rd.Sprint) {
			return nil, errors.New("verbs: the read has fewer answers than it asked")
		}
		qr, err := sprintfn.DecodeResult(sprint.QueryJnote, rd.Sprint[i])
		if err != nil {
			return nil, err
		}
		jr, ok := qr.(sprintfn.JnoteResult)
		if !ok {
			return nil, errors.New("verbs: a jnote answer is of another kind")
		}
		for _, it := range jr.Items {
			out[it.Note] = it
		}
	}
	return out, nil
}

// openSubjects are the subjects of a note on which it is open now: their
// jopen holds the note's own id at the note's type and cause (1.3.4). A held
// subject ("h" and the id), and one closed since, are not.
func openSubjects(it sprintfn.NoteItem) []string {
	var out []string
	for _, s := range it.Subjects {
		if s.Own != nil && *s.Own == it.Note {
			out = append(out, s.ID)
		}
	}
	return out
}

// judgedNote is one note as a verb that answers it sees it: its item, its row
// of 2.2, and the subjects it is open on now.
type judgedNote struct {
	id   string
	item sprintfn.NoteItem
	row  sprint.JudgmentType
	open []string
}

// judged reads each note of the verb's set from the read, and refuses the set
// when any is not an open judgment of 2.2 at this epoch: a note of another
// epoch, a note that is no judgment (a notice), or one no line names.
func judged(verb string, notes []string, rd *sprintfn.ReadReply, items map[string]sprintfn.NoteItem) ([]judgedNote, error) {
	epoch, _ := strconv.ParseUint(string(rd.Epoch), 10, 64)
	var out []judgedNote
	var bad []string
	for _, id := range notes {
		if e := sprint.IDEpoch(id); e != epoch {
			bad = append(bad, sprint.OtherEpoch(id, e, epoch))
			continue
		}
		it, ok := items[id]
		if !ok {
			bad = append(bad, fmt.Sprintf("no note %s at epoch %d", id, epoch))
			continue
		}
		row, ok := sprint.Judgments[it.Type]
		if !ok {
			bad = append(bad, fmt.Sprintf("note %s is %q, a notice and not a judgment: it needs no answer", id, it.Type))
			continue
		}
		out = append(out, judgedNote{id: id, item: it, row: row, open: openSubjects(it)})
	}
	if len(bad) > 0 {
		return nil, refuseLocal(verb, sprintfn.CodeRequest, "%s", strings.Join(bad, "; "))
	}
	return out, nil
}

// decisionList is a row's decisions as the inbox prints them.
func decisionList(row sprint.JudgmentType) string {
	var ds []string
	for _, d := range row.Decisions {
		ds = append(ds, d.String())
	}
	return strings.Join(ds, "; ")
}

// notesRead is a verb's read of its notes at an epoch, with the sprint-key
// reads it needs after the notes' queries.
func notesRead(notes []string, keys ...sprintfn.KeyQ) func(tset.Decimal) *sprintfn.ReadRequest {
	return func(epoch tset.Decimal) *sprintfn.ReadRequest {
		rr := &sprintfn.ReadRequest{Epoch: epoch, Sprint: jnoteQueries(notesAt(notes, epoch))}
		for _, k := range keys {
			rr.Sprint = append(rr.Sprint, keyQuery(k))
		}
		return rr
	}
}

// ackRead is ack's read at an epoch: each note by jnote, the dropping marks'
// count and the marks of the streams named (1.3.1), the rows of the work table
// (a card's stream is its row there, as X reads it: twin_x.go,
// xStreamsTouched), and the records of the cards named, with their refused
// field. The first read names no stream and no card.
func ackRead(notes []string, need ackNeed) func(tset.Decimal) *sprintfn.ReadRequest {
	return func(epoch tset.Decimal) *sprintfn.ReadRequest {
		rr := &sprintfn.ReadRequest{Epoch: epoch, Sprint: jnoteQueries(notesAt(notes, epoch)),
			Tset: []tset.ReadQuery{{Kind: "rows", Table: sprint.Work}}}
		if len(need.cards) > 0 {
			rr.Tset = append(rr.Tset, tset.ReadQuery{Kind: "ids", Table: sprint.Work, IDs: append([]string{}, need.cards...), Fields: []string{fieldRefused}})
		}
		rr.Sprint = append(rr.Sprint, keyQuery(sprintfn.KeyQ{Kind: sprintfn.KeyDropping, Streams: append([]string{}, need.streams...)}))
		return rr
	}
}

// ackNeed is what an ack's read carries beyond its notes: the cards whose
// records it reads, and the streams whose dropping marks it reads.
type ackNeed struct{ cards, streams []string }

// needMore is the plan's ask for a read that carries more (ackNeed): the
// records of the cards a "could not move" note names, which only its line
// names; and, while some stream is being dropped, the records of every card
// the ack changes and the marks of the work table's streams, so that a
// frozen card is refused here and not by X (H8).
type needMore struct{ need ackNeed }

func (n *needMore) Error() string {
	return fmt.Sprintf("the ack reads %d cards' records and %d streams' dropping marks first", len(n.need.cards), len(n.need.streams))
}

// ackAnswers are the answers of ackRead: the notes, the work table's rows, the
// cards' records by id, and the dropping marks.
type ackAnswers struct {
	items    map[string]sprintfn.NoteItem
	rows     []string
	records  map[string]tset.MemberRecord
	dropping sprintfn.DroppingResult
}

func ackAnswersOf(notes []string, rd *sprintfn.ReadReply, need ackNeed) (ackAnswers, error) {
	nq := len(jnoteQueries(notesAt(notes, rd.Epoch)))
	items, err := noteItems(rd, 0, nq)
	if err != nil {
		return ackAnswers{}, err
	}
	want := 1
	if len(need.cards) > 0 {
		want = 2
	}
	if len(rd.Tset) != want {
		return ackAnswers{}, errors.New("ack: the read answered the wrong number of table queries")
	}
	out := ackAnswers{items: items, rows: rowNames(rd.Tset[0]), records: map[string]tset.MemberRecord{}}
	if want == 2 {
		for _, r := range rd.Tset[1].Records {
			out.records[r.ID] = r
		}
	}
	if out.dropping, err = sprintAnswer[sprintfn.DroppingResult](rd, nq, sprintfn.KeyDropping); err != nil {
		return ackAnswers{}, err
	}
	return out, nil
}

// Ack answers judgments whose row of 2.2 lists ack (section 3, ack; 1.3.4;
// 1.3.5; errata 3 H8). It is the model's AckEff (tla/SprintEvents.tla), under
// VGuard's ack row. For each note, on the subjects it is open on now:
//
//   - blocked on something dropped, or missing: a waive intent for each
//     waiter and the need the note names (its cause), which the derive phase
//     turns into the waiver and the close (1.3.3, IT14); a missing need is
//     waived only while it has no record, and with one the derive refuses
//     XGUARD naming it (VGuard: col[n] = "none");
//   - the machine could not move a card: the close, and `refused` unset on the
//     card, so the machine tries it again (1.3.5; AckEff's "refused" row);
//   - the machine's step was refused: the close, and the rule keys it names
//     unparked from {p}parked@e by the sprint part. AckEff adds the key to the
//     queue (`lk`); here the queueing is the decided line's, which ingest turns
//     into the key again (F1-22; Q5). Layer 3's ingest (IT01, closeRows) has no
//     row for that line yet, so until it lands the unparked key waits for
//     another trigger (TestAckUnparksThroughLine's skipped half);
//   - any other row that lists ack: the close.
//
// Every close records "ack: <reason>" on its decided line. A judgment whose
// row does not list ack is refused before anything is sent, naming its
// decisions (and wait, for a condition the tick keeps: an ack would close
// what the tick raises again). An ack that waives or clears a card of a
// stream being dropped is refused DROPPING before anything is sent (H8;
// VGuard: ~Frozen(w)): the plan reads the marks of the streams it touches, so
// the driver never plans again a step X would refuse the same way. A note
// closed or held since it was printed is left as it is.
//
// One step, two round trips. Two cases read once more before the step: an ack
// of "the machine could not move a card", whose cards only the note's line
// names (their records, for the clear's revision); and, while some stream is
// being dropped, an ack that changes a card (its record and the marks).
func Ack(ctx context.Context, e *Env, req AckReq) (Result, error) {
	const verb = "ack"
	notes, err := noteSet(verb, req.Notes, req.Reason)
	if err != nil {
		return Result{Verb: verb}, err
	}
	args := map[string]any{"notes": notes, "reason": req.Reason}
	var need ackNeed
	var res Result
	trips, retries := 0, 0
	for round := 0; ; round++ {
		n := need
		res, err = e.Do(ctx, Planned{Verb: verb, Op: req.Op, Args: args, Read: ackRead(notes, n),
			Plan: func(rd *sprintfn.ReadReply) (Part, error) {
				req, err := ackPlan(notes, req.Reason, rd, n)
				if err != nil || req == nil {
					return Part{}, err
				}
				return Part{Req: req}, nil
			}})
		trips += res.Trips
		retries += res.Retries
		var nm *needMore
		if !errors.As(err, &nm) {
			break
		}
		if round >= Retries {
			err = &Refused{Verb: verb, Retries: round, Op: req.Op, Refusal: &sprintfn.Refusal{Code: sprintfn.CodeStale,
				Message: "the cards and streams the ack changes kept moving between its reads"}}
			break
		}
		need = ackNeed{cards: dedup(append(append([]string{}, need.cards...), nm.need.cards...)),
			streams: dedup(append(append([]string{}, need.streams...), nm.need.streams...))}
	}
	res.Trips, res.Retries = trips, retries
	if err == nil && !res.Replay && res.Said == "" {
		res.Said = fmt.Sprintf("ack: %d notes answered", len(notes))
		if res.Step == nil {
			res.Said = "ack: nothing to answer: every note was closed or held since it was printed; nothing was written"
		}
	}
	return res, err
}

// ackPlan is ack's step from its read; need is what the read carries beyond
// the notes.
func ackPlan(notes []string, reason string, rd *sprintfn.ReadReply, need ackNeed) (*sprintfn.Request, error) {
	const verb = "ack"
	ans, err := ackAnswersOf(notes, rd, need)
	if err != nil {
		return nil, err
	}
	js, err := judged(verb, notes, rd, ans.items)
	if err != nil {
		return nil, err
	}
	var bad []string
	for _, j := range js {
		if !j.row.Ack {
			why := fmt.Sprintf("note %s (%s) is not answered by ack: its decisions are %s", j.id, j.row.Type, decisionList(j.row))
			if j.row.TickKept {
				why += "; it is a condition the tick keeps: wait holds it"
			}
			bad = append(bad, why)
		}
	}
	if len(bad) > 0 {
		return nil, refuseLocal(verb, sprintfn.CodeRequest, "%s", strings.Join(bad, "; "))
	}
	text := "ack: " + reason
	req := &sprintfn.Request{}
	waives := map[string][]string{} // waiter -> needs
	var waiters, clear, unpark []string
	changes := map[string]string{} // card -> the note whose ack changes it
	for _, j := range js {
		if len(j.open) == 0 {
			continue // closed or held since it was printed: nothing to answer
		}
		switch j.row.Type {
		case typeBlockedDropped, typeBlockedMissing:
			// The derive phase closes the judgment with the waiver (1.3.3): a
			// close beside it would be a second request on one field (J: REQUEST).
			for _, w := range j.open {
				if _, ok := waives[w]; !ok {
					waiters = append(waiters, w)
				}
				waives[w] = append(waives[w], j.item.Cause)
				changes[w] = j.id
			}
			continue
		case typeCouldNotMove:
			clear = append(clear, j.open...)
			for _, c := range j.open {
				changes[c] = j.id
			}
		case typeStepRefused:
			unpark = append(unpark, j.open...)
		}
		req.Body.Notes = append(req.Body.Notes, sprintfn.NoteReq{Op: sprintfn.JOpClose, Type: j.item.Type, Cause: j.item.Cause,
			Subjects: append([]string(nil), j.open...), Text: text})
	}
	clear = dedup(clear)
	if more, ok := ackMore(clear, changes, ans, need); ok {
		return nil, &needMore{need: more}
	}
	if err := ackFrozen(verb, changes, ans); err != nil {
		return nil, err
	}
	for _, w := range waiters {
		req.Body.Intents = append(req.Body.Intents, sprintfn.Intent{Kind: "waive", Card: w, Needs: dedup(waives[w])})
	}
	for _, id := range clear {
		if _, ok := waives[id]; ok {
			return nil, refuseLocal(verb, sprintfn.CodeRequest, "card %s is both waived and cleared by this ack: ack its notes in two steps", id)
		}
		rec, ok := ans.records[id]
		if !ok || !rec.Exists || rec.Place == nil {
			continue // the card left the table since: its refused went with it
		}
		if v, ok := rec.Fields[fieldRefused]; !ok || !v.Present {
			continue // cleared since
		}
		req.Body.Entries = append(req.Body.Entries, tset.Entry{Kind: "move", Table: sprint.Work,
			From: rec.Place.Row + ":" + rec.Place.Col, IDs: []string{id}, Revs: []tset.Decimal{rec.Revision},
			Unset: []string{fieldRefused}, About: []string{id}, BeforeFields: sprint.IndexFields()})
	}
	if len(unpark) > 0 {
		req.Sprint = &sprintfn.SprintPart{Unpark: dedup(unpark)}
	}
	if len(req.Body.Notes)+len(req.Body.Intents)+len(req.Body.Entries) == 0 && req.Sprint == nil {
		return nil, nil // every note was closed or held since it was printed
	}
	return req, nil
}

// ackMore is what the plan must read before it can plan: the records of the
// cards it clears; and while some stream is being dropped, the records of
// every card it changes and the marks of every stream of the work table.
// False when the read carries all of it.
func ackMore(clear []string, changes map[string]string, ans ackAnswers, need ackNeed) (ackNeed, bool) {
	cards := append([]string{}, clear...)
	var streams []string
	if ans.dropping.Count > 0 && len(changes) > 0 {
		for c := range changes {
			cards = append(cards, c)
		}
		streams = ans.rows
	}
	var more ackNeed
	for _, c := range dedup(cards) {
		if !contains(need.cards, c) {
			more.cards = append(more.cards, c)
		}
	}
	for _, s := range streams {
		if !contains(need.streams, s) {
			more.streams = append(more.streams, s)
		}
	}
	sort.Strings(more.cards)
	return more, len(more.cards)+len(more.streams) > 0
}

// ackFrozen refuses an ack that changes a card of a stream being dropped,
// DROPPING, before anything is sent (errata 3 H8; the model's VGuard for ack:
// a waive or a clear needs ~Frozen(w), Frozen(c) == dropping[S(c)] # None).
// A card's stream is the row of its place in the work table, as X reads it.
// The read carries the records and marks ackMore asked for.
func ackFrozen(verb string, changes map[string]string, ans ackAnswers) error {
	if ans.dropping.Count == 0 {
		return nil
	}
	cards := make([]string, 0, len(changes))
	for c := range changes {
		cards = append(cards, c)
	}
	sort.Strings(cards)
	var bad []string
	for _, c := range cards {
		rec, ok := ans.records[c]
		if !ok || !rec.Exists || rec.Place == nil {
			continue
		}
		if op, ok := ans.dropping.Marks[rec.Place.Row]; ok {
			bad = append(bad, fmt.Sprintf("note %s changes card %s of stream %s, which op %s is dropping", changes[c], c, rec.Place.Row, op))
		}
	}
	if len(bad) > 0 {
		return refuseLocal(verb, sprintfn.CodeDropping, "%s: a drop freezes its stream's cards; ack the notes once the drop ends", strings.Join(bad, "; "))
	}
	return nil
}

// dedup keeps the first of equal strings, in order.
func dedup(in []string) []string {
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

// Wait answers judgments by waiting (section 3, wait; 1.3.4; 2.2's wait
// column): for each note, on the subjects it is open on now, one hold request
// to J until the time d from now. On a condition the tick keeps, J closes the
// judgment with a hold line and holds it until R + d (hold:<note>); on the
// STOPPED judgment the hold is wall time, stophold_ms = wall + d (1.3.4); on
// any other judgment J moves its overdue entry to the review time R + d and
// leaves it open. These are 1.3.4's three hold forms (the hold, the wall hold,
// the review), and the model's VEff for "wait" and "waitstop"
// (tla/SprintEvents.tla: the judgment held, its line queueing nothing, W16)
// under VGuard's IsOpenJ. A note held already ("h" and its id) is not open,
// and is left as it is. --reason is required. One step, two round trips.
func Wait(ctx context.Context, e *Env, req WaitReq) (Result, error) {
	const verb = "wait"
	notes, err := noteSet(verb, req.Notes, req.Reason)
	if err != nil {
		return Result{Verb: verb}, err
	}
	if req.For <= 0 {
		return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "wait wants --for a duration above 0, got %s", req.For)
	}
	d := req.For.Milliseconds()
	if d < 1 {
		d = 1
	}
	args := map[string]any{"notes": notes, "reason": req.Reason, "for_ms": strconv.FormatInt(d, 10)}
	res, err := e.Do(ctx, Planned{Verb: verb, Op: req.Op, Args: args,
		Read: notesRead(notes, sprintfn.KeyQ{Kind: sprintfn.KeyClock}),
		Plan: func(rd *sprintfn.ReadReply) (Part, error) {
			n := len(jnoteQueries(notesAt(notes, rd.Epoch)))
			items, err := noteItems(rd, 0, n)
			if err != nil {
				return Part{}, err
			}
			js, err := judged(verb, notes, rd, items)
			if err != nil {
				return Part{}, err
			}
			c, _, err := clockOf(rd, n)
			if err != nil {
				return Part{}, err
			}
			r, err1 := strconv.ParseInt(c.R, 10, 64)
			wall, err2 := strconv.ParseInt(c.WallMS, 10, 64)
			if err1 != nil || err2 != nil {
				return Part{}, fmt.Errorf("wait: the clock's R %q or wall %q is not a number", c.R, c.WallMS)
			}
			step := &sprintfn.Request{}
			for _, j := range js {
				if len(j.open) == 0 {
					continue // closed or held since it was printed
				}
				at := r
				if j.row.Type == typeStoppedDue {
					at = wall // 1.3.4: the STOPPED judgment's hold is wall time
				}
				if at > jUntilMax-d {
					return Part{}, refuseLocal(verb, sprintfn.CodeRequest, "--for %s is past the latest time a hold may name", req.For)
				}
				step.Body.Notes = append(step.Body.Notes, sprintfn.NoteReq{Op: sprintfn.JOpHold, Type: j.item.Type, Cause: j.item.Cause,
					Subjects: append([]string(nil), j.open...), Text: "wait: " + req.Reason, Until: at + d})
			}
			if len(step.Body.Notes) == 0 {
				return Part{}, nil
			}
			return Part{Req: step}, nil
		}})
	if err == nil && !res.Replay && res.Said == "" {
		res.Said = fmt.Sprintf("wait: %d notes wait %s", len(notes), req.For)
		if res.Step == nil {
			res.Said = "wait: nothing to wait on: every note was closed or held since it was printed; nothing was written"
		}
	}
	return res, err
}

// jUntilMax is the latest time a hold or a review may name: J's bound, 15
// digits, which the Lua half holds exactly (IT15).
const jUntilMax int64 = 999_999_999_999_999

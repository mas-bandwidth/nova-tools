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

// needCards is the plan's ask for the records of cards whose refused field an
// ack clears: the cards a "could not move" note names are known only from its
// line, so their records are read in a second read (Ack).
type needCards struct{ ids []string }

func (n *needCards) Error() string {
	return fmt.Sprintf("the ack reads the records of %d cards first", len(n.ids))
}

// Ack answers judgments whose row of 2.2 lists ack (section 3, ack; 1.3.4;
// 1.3.5; errata 3 H8). For each note, on the subjects it is open on now:
//
//   - blocked on something dropped, or missing: a waive intent for each
//     waiter and the need the note names (its cause), which the derive phase
//     turns into the waiver and the close (1.3.3, IT14); a missing need is
//     waived only while it has no record, and with one the derive refuses
//     XGUARD naming it;
//   - the machine could not move a card: the close, and `refused` unset on the
//     card, so the machine tries it again (1.3.5);
//   - the machine's step was refused: the close, and the rule keys it names
//     unparked from {p}parked@e by the sprint part, whose decided line
//     queues each key again (F1-22; 2.1; Q5);
//   - any other row that lists ack: the close.
//
// Every close records "ack: <reason>" on its decided line. A judgment whose
// row does not list ack is refused before anything is sent, naming its
// decisions (and wait, for a condition the tick keeps: an ack would close
// what the tick raises again). An ack that waives or clears a card of a
// stream being dropped is refused DROPPING by X (H8): it changes the card. A
// note closed or held since it was printed is left as it is. One step, two
// round trips; an ack of "the machine could not move a card" reads the
// cards' records after the note names them, one round trip more.
func Ack(ctx context.Context, e *Env, req AckReq) (Result, error) {
	const verb = "ack"
	notes, err := noteSet(verb, req.Notes, req.Reason)
	if err != nil {
		return Result{Verb: verb}, err
	}
	args := map[string]any{"notes": notes, "reason": req.Reason}
	res, err := e.Do(ctx, Planned{Verb: verb, Op: req.Op, Args: args, Read: notesRead(notes),
		Plan: func(rd *sprintfn.ReadReply) (*sprintfn.Request, error) { return ackPlan(notes, req.Reason, rd, nil) }})
	var nc *needCards
	if errors.As(err, &nc) {
		first := res.Trips
		ids := nc.ids
		res, err = e.Do(ctx, Planned{Verb: verb, Op: req.Op, Args: args,
			Read: func(epoch tset.Decimal) *sprintfn.ReadRequest {
				rr := notesRead(notes)(epoch)
				rr.Tset = []tset.ReadQuery{{Kind: "ids", Table: sprint.Work, IDs: ids, Fields: []string{fieldRefused}}}
				return rr
			},
			Plan: func(rd *sprintfn.ReadReply) (*sprintfn.Request, error) {
				if len(rd.Tset) != 1 {
					return nil, errors.New("ack: the read has no answer for the cards")
				}
				recs := map[string]tset.MemberRecord{}
				for _, r := range rd.Tset[0].Records {
					recs[r.ID] = r
				}
				return ackPlan(notes, req.Reason, rd, recs)
			}})
		res.Trips += first
	}
	if err == nil && !res.Replay && res.Said == "" {
		res.Said = fmt.Sprintf("ack: %d notes answered", len(notes))
		if res.Step == nil {
			res.Said = "ack: nothing to answer: every note was closed or held since it was printed; nothing was written"
		}
	}
	return res, err
}

// ackPlan is ack's step from its read. cards are the records of the cards
// whose refused field it clears, nil before they were read.
func ackPlan(notes []string, reason string, rd *sprintfn.ReadReply, cards map[string]tset.MemberRecord) (*sprintfn.Request, error) {
	const verb = "ack"
	items, err := noteItems(rd, 0, len(jnoteQueries(notesAt(notes, rd.Epoch))))
	if err != nil {
		return nil, err
	}
	js, err := judged(verb, notes, rd, items)
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
			}
			continue
		case typeCouldNotMove:
			clear = append(clear, j.open...)
		case typeStepRefused:
			unpark = append(unpark, j.open...)
		}
		req.Body.Notes = append(req.Body.Notes, sprintfn.NoteReq{Op: sprintfn.JOpClose, Type: j.item.Type, Cause: j.item.Cause,
			Subjects: append([]string(nil), j.open...), Text: text})
	}
	for _, w := range waiters {
		req.Body.Intents = append(req.Body.Intents, sprintfn.Intent{Kind: "waive", Card: w, Needs: dedup(waives[w])})
	}
	clear = dedup(clear)
	if len(clear) > 0 {
		if cards == nil {
			return nil, &needCards{ids: clear}
		}
		for _, id := range clear {
			if _, ok := waives[id]; ok {
				return nil, refuseLocal(verb, sprintfn.CodeRequest, "card %s is both waived and cleared by this ack: ack its notes in two steps", id)
			}
			rec, ok := cards[id]
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
	}
	if len(unpark) > 0 {
		req.Sprint = &sprintfn.SprintPart{Unpark: dedup(unpark)}
	}
	if len(req.Body.Notes)+len(req.Body.Intents)+len(req.Body.Entries) == 0 && req.Sprint == nil {
		return nil, nil // every note was closed or held since it was printed
	}
	return req, nil
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
// leaves it open. --reason is required. One step, two round trips.
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
		Plan: func(rd *sprintfn.ReadReply) (*sprintfn.Request, error) {
			n := len(jnoteQueries(notesAt(notes, rd.Epoch)))
			items, err := noteItems(rd, 0, n)
			if err != nil {
				return nil, err
			}
			js, err := judged(verb, notes, rd, items)
			if err != nil {
				return nil, err
			}
			c, _, err := clockOf(rd, n)
			if err != nil {
				return nil, err
			}
			r, err1 := strconv.ParseInt(c.R, 10, 64)
			wall, err2 := strconv.ParseInt(c.WallMS, 10, 64)
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("wait: the clock's R %q or wall %q is not a number", c.R, c.WallMS)
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
					return nil, refuseLocal(verb, sprintfn.CodeRequest, "--for %s is past the latest time a hold may name", req.For)
				}
				step.Body.Notes = append(step.Body.Notes, sprintfn.NoteReq{Op: sprintfn.JOpHold, Type: j.item.Type, Cause: j.item.Cause,
					Subjects: append([]string(nil), j.open...), Text: "wait: " + req.Reason, Until: at + d})
			}
			if len(step.Body.Notes) == 0 {
				return nil, nil
			}
			return step, nil
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

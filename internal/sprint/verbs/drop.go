package verbs

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// drop (item IT21; section 3; 1.5.4): named primaries with their live cards,
// or every open card of streams (--stream, --col), in parts, and the abort of
// a drop in parts. `remove` (IT27) is not here.

// DropReq is drop's request: named primaries, or the streams whose open cells
// it drains (Col, one open cell of them), the reason, and the chunk its parts
// run at (0 is StepChunk).
type DropReq struct {
	Op      string
	IDs     []string
	Streams []string
	Col     string
	Reason  string
	Chunk   int
}

// dropFields is what drop reads of a primary: what its follows derive from,
// and its needs (the causes of the blocked judgments on a waiter).
var dropFields = []string{sprint.PrimaryField, "attempt", "rcards", "needs"}

// dropFollow are a primary's live cards (1.5.4: a primary goes with its live
// cards in the same part): its work card, live or withdrawn, its read cards,
// its merge card.
var dropFollow = []string{sprint.FollowWork, sprint.FollowWithdrawn, sprint.FollowRCards, sprint.FollowMerge}

// dropEach is the most members one primary's drop removes: itself, its work
// card, its read cards (at most 15) and its merge card.
const dropEach = 1 + 1 + sprint.MaxRCards + 1

// dropHeadMax is the most primaries a head of one cell reads in a part: five
// heads of it, each id with its record and its follows (at most dropEach),
// stay inside Layer 1's 10,000 records a read.
const dropHeadMax = 100

// Drop drops primaries with their live cards (section 3, `drop p...
// --reason`; 1.5.4), or with Streams every open card of each stream
// (`drop --stream s[,s...] [--col c]`). Named ids go in parts of the sorted
// list: each primary removed (dropped, the reason) with its live work card,
// its placed read cards and its merge card; R4 serves its waiters from the
// removal's line. n + 1 round trips. Streams: see dropStreams.
func Drop(ctx context.Context, e *Env, req DropReq) (Result, error) {
	const verb = "drop"
	if len(req.Streams) != 0 {
		if len(req.IDs) != 0 {
			return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "names cards and --stream both")
		}
		return dropStreams(ctx, e, req)
	}
	if req.Col != "" {
		return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "--col is a drop of streams: give --stream")
	}
	ids, err := sortedIDs(verb, req.IDs)
	if err != nil {
		return Result{Verb: verb}, err
	}
	return namedParts(ctx, e, verb, req.Op, ids, req.Chunk, dropEach, map[string]any{"reason": req.Reason},
		func(part []string) verbRead {
			return verbRead{plan: sprint.ReadPlan{Sprint: []sprint.SprintQ{related(part, dropFields, dropFollow)}}}
		},
		func(va *verbAnswer, part []string) (*sprintfn.Request, error) {
			var b stepOps
			var k closer
			var refused []sprint.Refusal
			for _, id := range part {
				c := primaryOf(va.snap, id)
				if c == nil || !c.Placed() || c.Col == sprint.Landed {
					refused = append(refused, sprint.Refusal{Key: id, Why: "not open on the table (" + cardPlace(c) + ")"})
					continue
				}
				dropPrimary(&b, &k, va, c, req.Reason)
			}
			if len(refused) != 0 {
				return nil, refusedIDs(verb, refused)
			}
			entries, err := b.entries()
			if err != nil {
				return nil, err
			}
			return &sprintfn.Request{Meta: sprintfn.Meta{Verb: verb}, Body: sprintfn.Body{Entries: entries, Notes: k.notes(dropText(req.Reason))}}, nil
		})
}

// dropPrimary removes a primary and its live cards: its work card of its
// attempt (live or withdrawn), every read card of it still placed, and its
// merge card when placed; and closes every judgment on it (the model's
// DropEff: JCloseSubj of the card's subjects, tla/SprintEvents.tla; section
// 3: "judgments on p closed"). No checked read lists a subject's jopen
// fields, so the close names every field a card can hold
// (sprint.CardJudgmentFields), and for a waiter the two blocked fields of
// each need it names; J closes those the card holds at apply and writes
// nothing for the rest (1.3.4), so nothing opened between the read and the
// step is left open. The judgments on its work card and read cards are the
// two lateness rows, which J closes itself as the step removes them (1.3.4).
func dropPrimary(b *stepOps, k *closer, va *verbAnswer, c *sprint.Card, reason string) {
	s, wall := va.snap, wallStamp(va.now)
	retired := map[string]string{"retired": wall, "retired_by": "drop"}
	if wc := s.Fleet.Card(sprint.WorkCardID(c.ID, c.Int("attempt"))); wc.Placed() {
		b.remove(sprint.Fleet, wc, retired)
	}
	for _, id := range sprint.Split(c.F("rcards")) {
		if rc := s.Readers.Card(id); rc.Placed() {
			b.remove(sprint.Readers, rc, retired)
		}
	}
	if m := s.Merge.Card(c.ID); m.Placed() && m.Col != sprint.Ctl {
		b.remove(sprint.Merge, m, retired)
	}
	set := map[string]string{"dropped": wall}
	if reason != "" {
		set["drop_reason"] = reason
	}
	b.remove(sprint.Work, c, set)
	for _, f := range sprint.CardJudgmentFields() {
		k.add(f, c.ID)
	}
	if c.Col == sprint.Waiting {
		for _, f := range sprint.BlockedJudgmentFields(sprint.Split(c.F("needs"))) {
			k.add(f, c.ID)
		}
	}
}

// dropText is the text of a drop's closes.
func dropText(reason string) string {
	if reason == "" {
		return "dropped by the coordinator"
	}
	return "dropped by the coordinator: " + reason
}

// dropCont is a drop of streams' continuation: the stream index and the cell
// index it drains next (1.5.4: "the continuation is the stream index and the
// cell").
type dropCont struct {
	Stream int `json:"s"`
	Cell   int `json:"c"`
}

// abortIntent is the intent of `drop --abort --op <op>`: its receipt is
// <op>/abort (1.5.4), and a resume of the op asks done for it.
func abortIntent(names sprint.Names, epoch tset.Decimal, op string) (string, error) {
	return Intent(names, epoch, "drop --abort", 0, map[string]any{"op": op})
}

// dropStreams drops every open card of each stream in parts (1.5.4, "Drop and
// remove in parts"). Part 1 writes {p}dropping@e[s] = op for every stream
// named, so every other step touching them is refused DROPPING until the last
// part or the abort (V6); a --col drop freezes the whole stream too. Each part
// removes the head of the current stream's first open cell that holds cards,
// streams in input order, cells in the order waiting, ready, working, review,
// merging (--col: that cell alone), each primary with its live cards: it
// drains the head, so nothing is skipped or taken twice and no offset is kept.
// Each part reads the heads of all the op's cells of the current stream and
// guards that every cell before the one it drains is empty (errata 3, H11
// freezefirst: part 1 reads before the freeze exists, and a card that moved
// back to a passed cell refuses the part RANGECOUNT, a race, which is planned
// again on a fresh read that finds it); a part that finds the stream empty
// guards all its cells empty and moves to the next, and a part that ends a
// stream guards the cell it drained to hold at most what it removes and the
// later cells empty, so the end is the state's at apply (PartApply's final).
// Each card removed has its judgments closed (dropPrimary, DropEff). The continuation's cell
// is where the last part drained, for the reader of the receipt. The last part deletes the marks and
// writes the request line naming the streams (typeUnfrozen), which queues the
// work skipped while they were frozen. A drop of one part freezes nothing.
// Each part's read asks done for <op>/abort, and a part of an aborted op is
// refused. The op is the caller's or one the verb makes before the first
// part: the marks carry it, and the driver's plan is not given it, so the
// driver reads done for it first (the resume's read), and a drop of streams
// costs n + 2 round trips, one more than 1.5.3's n + 1 (a finding for IT18).
func dropStreams(ctx context.Context, e *Env, req DropReq) (Result, error) {
	const verb = "drop --stream"
	streams := append([]string(nil), req.Streams...)
	seen := map[string]bool{}
	for _, s := range streams {
		if !sprint.ValidID(s) || seen[s] {
			return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "%q is not a stream, or is named twice", s)
		}
		seen[s] = true
	}
	cols := openCols
	if req.Col != "" {
		if !contains(openCols, req.Col) {
			return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "--col %q is not an open cell (%s)", req.Col, strings.Join(openCols, ", "))
		}
		cols = []string{req.Col}
	}
	op := req.Op
	if op == "" {
		op = NewOp()
	}
	if !validOp(op) {
		return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "--op %q is not an op", op)
	}
	decode := func(cont string) (dropCont, error) {
		var dc dropCont
		if cont == "" {
			return dc, nil
		}
		if err := json.Unmarshal([]byte(cont), &dc); err != nil || dc.Stream < 0 || dc.Stream >= len(streams) || dc.Cell < 0 || dc.Cell >= len(cols) {
			return dc, fmt.Errorf("verbs: the continuation %q is not a drop's", cont)
		}
		return dc, nil
	}
	headOf := func(chunk int) int { return min(dropHeadMax, perPart(chunk, dropEach)) }
	readOf := func(epoch tset.Decimal, dc dropCont, chunk int) (verbRead, error) {
		s := streams[dc.Stream]
		var vr verbRead
		var cells []string
		for _, col := range cols { // from the first cell every part: a card moved back to a passed cell is found (H11)
			vr.plan.Sprint = append(vr.plan.Sprint, sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work,
				Source: sprint.IDSource{Kind: sprint.SourceHead, Key: s + ":" + col, Limit: headOf(chunk)}, Fields: dropFields, Follow: dropFollow})
			cells = append(cells, s+":"+col)
		}
		vr.plan.Counts = []sprint.CountQ{{Table: sprint.Work, Cells: cells}}
		intent, err := abortIntent(e.Names, epoch, op)
		if err != nil {
			return vr, err
		}
		vr.extra = []tset.ReadQuery{{Kind: "done", Ops: []tset.DoneIdentity{{Epoch: epoch, Op: op + "/abort", IntentDigest: digest(intent)}}}}
		return vr, nil
	}
	var failed error
	return e.Parts(ctx, op, PartsPlan{Verb: verb, Chunk: req.Chunk,
		Args: map[string]any{"streams": streams, "col": req.Col, "reason": req.Reason},
		Read: func(epoch tset.Decimal, cont string, chunk int) *sprintfn.ReadRequest {
			dc, err := decode(cont)
			if err == nil {
				var vr verbRead
				if vr, err = readOf(epoch, dc, chunk); err == nil {
					return vr.readAt(e.Names, epoch, &failed)
				}
			}
			failed = err
			return clockRead(epoch)
		},
		Plan: func(rd *sprintfn.ReadReply, cont string, chunk int) (Part, error) {
			if failed != nil {
				return Part{}, failed
			}
			dc, err := decode(cont)
			if err != nil {
				return Part{}, err
			}
			vr, err := readOf(rd.Epoch, dc, chunk)
			if err != nil {
				return Part{}, err
			}
			va, err := vr.load(rd)
			if err != nil {
				return Part{}, err
			}
			if d := va.extra[0].Done; len(d) == 1 && d[0].Status != "absent" {
				return Part{}, refuseLocal(verb, "ABORTED", "op %s was aborted (drop --abort --op %s): its streams are unfrozen and it does not go on", op, op)
			}
			s := streams[dc.Stream]
			counts := countsOf(va, 0)
			var b stepOps
			// The first cell whose head holds cards: the continuation's, unless a
			// card moved back to one before it (errata 3, H11).
			at := -1
			for i := range cols {
				if len(va.snap.Partial.Answer.Sprint[i].IDs) != 0 {
					at = i
					break
				}
			}
			var k closer
			var guarded []string
			streamDone := at < 0
			if at >= 0 {
				guarded = cols[:at]
				removed := 0
				for _, id := range va.snap.Partial.Answer.Sprint[at].IDs {
					c := va.snap.Work.Card(id)
					if c == nil || !c.Placed() {
						continue
					}
					dropPrimary(&b, &k, va, c, req.Reason)
					removed++
				}
				left := counts[at] - removed
				for _, n := range counts[at+1:] {
					left += n
				}
				streamDone = left <= 0
				if streamDone {
					// Done is decided on the state at apply, as the model's
					// PartApply decides final on T1: the drained cell holds at
					// most the cards this part removes and each later cell
					// none, or the part is refused RANGECOUNT, a race planned
					// again on a fresh read (errata 3, H11's sibling).
					atMost(&b, s, cols[at:at+1], uint64(removed))
					atMost(&b, s, cols[at+1:], 0)
				}
			} else {
				guarded = cols // the stream holds nothing: every cell of it is guarded empty
			}
			atMost(&b, s, guarded, 0)
			next := dc
			last := false
			if streamDone {
				next = dropCont{Stream: dc.Stream + 1}
				last = next.Stream == len(streams)
			} else {
				next.Cell = at
			}
			entries, err := b.entries()
			if err != nil {
				return Part{}, err
			}
			r := &sprintfn.Request{Meta: sprintfn.Meta{Verb: "drop"}, Body: sprintfn.Body{Entries: entries, Notes: k.notes(dropText(req.Reason))}}
			first := cont == ""
			switch {
			case first && !last:
				r.Sprint = &sprintfn.SprintPart{Dropping: marks(streams, op)}
			case last && !first:
				r.Sprint = &sprintfn.SprintPart{Undrop: marks(streams, op)}
				r.Body.Notes = append(r.Body.Notes, unfrozen(streams, op))
			}
			nb := ""
			if !last {
				b, _ := json.Marshal(next)
				nb = string(b)
			}
			return Part{Req: r, Next: nb, Last: last}, nil
		}})
}

// atMost guards that the cells of stream s hold at most n cards together,
// on the state before the step (an rcount entry; none for no cell).
func atMost(b *stepOps, s string, cols []string, n uint64) {
	if len(cols) == 0 {
		return
	}
	cells := make([]string, len(cols))
	for i, col := range cols {
		cells[i] = s + ":" + col
	}
	b.entry(tset.Entry{Kind: "rcount", Table: sprint.Work, Cells: cells, ScoreMin: "-inf", ScoreMax: "+inf", AtMost: &n})
}

// marks is every stream frozen by op (1.5.4: {p}dropping@e[s] = op).
func marks(streams []string, op string) map[string]string {
	m := make(map[string]string, len(streams))
	for _, s := range streams {
		m[s] = op
	}
	return m
}

// unfrozen is the request line naming the streams a drop unfreezes (1.5.4;
// 2.1: it queues resolve:s and pullback:s for each, and deal).
func unfrozen(streams []string, op string) sprintfn.NoteReq {
	return note(sprintfn.JOpRequest, typeUnfrozen, op, "the drop of op "+op+" unfroze its streams", streamSubjects(streams))
}

// DropAbortReq is the abort's request: the op, and optionally the streams it
// reads the marks of. The design's `drop --abort --op <op>` names the op
// alone: with no streams, the abort reads the sprint's streams (at most
// sprint.MaxStreams) and the marks of each, and aborts every stream the op
// froze. Streams narrow the read to those named (a mark on a name that is no
// stream of the sprint is reached only so), and a named stream another op
// froze refuses the abort.
type DropAbortReq struct {
	Op      string
	Streams []string
}

// DropAbort ends a drop of streams that stopped before its end (1.5.4,
// "Abort"; section 3; the model's AbortApply, tla/SprintEvents.tla): in one
// step, it deletes the op's marks, writes the request line naming the streams,
// and names what was left (each stream's open cells and their counts not yet
// dropped), under its own receipt <op>/abort, so a resume of the op is
// refused. A repeat of an applied abort is its recorded result and writes
// nothing. The op's cut entry is not deleted here: no part writes one yet (the
// driver's cut clock is owed, 1.5.4). Two round trips with Streams; three
// without, the first reading the sprint's streams, since the marks are a hash
// by stream that a sprint-key read reads by name (an all-marks read, IT30,
// makes it two).
func DropAbort(ctx context.Context, e *Env, req DropAbortReq) (Result, error) {
	const verb = "drop --abort"
	if !validOp(req.Op) {
		return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "--op %q is not an op", req.Op)
	}
	named := len(req.Streams) != 0
	listed := 0
	var streams []string
	if named {
		var err error
		if streams, err = sortedIDs(verb, req.Streams); err != nil {
			return Result{Verb: verb}, err
		}
	} else {
		var err error
		streams, err = sprintStreams(ctx, e, verb)
		listed = 1
		if err != nil {
			return Result{Verb: verb, Trips: listed}, err
		}
		if len(streams) == 0 {
			return Result{Verb: verb, Trips: listed}, refuseLocal(verb, sprintfn.CodeRequest, "the sprint has no stream: op %s freezes nothing", req.Op)
		}
	}
	var cells []string
	for _, s := range streams {
		for _, col := range openCols {
			cells = append(cells, s+":"+col)
		}
	}
	var replayed bool
	var recorded string
	var failed error
	epochOf := func(epoch tset.Decimal) (string, error) { return abortIntent(e.Names, epoch, req.Op) }
	readOf := func(epoch tset.Decimal) (verbRead, error) {
		intent, err := epochOf(epoch)
		if err != nil {
			return verbRead{}, err
		}
		return verbRead{plan: sprint.ReadPlan{Counts: []sprint.CountQ{{Table: sprint.Work, Cells: cells}}},
			keys:  []sprintfn.KeyQ{{Kind: sprintfn.KeyDropping, Streams: streams}},
			extra: []tset.ReadQuery{{Kind: "done", Ops: []tset.DoneIdentity{{Epoch: epoch, Op: req.Op + "/abort", IntentDigest: digest(intent)}}}}}, nil
	}
	res, err := e.Do(ctx, Planned{Verb: verb,
		Read: func(epoch tset.Decimal) *sprintfn.ReadRequest {
			vr, err := readOf(epoch)
			if err != nil {
				failed = err
				return clockRead(epoch)
			}
			return vr.readAt(e.Names, epoch, &failed)
		},
		Plan: func(rd *sprintfn.ReadReply) (Part, error) {
			if failed != nil {
				return Part{}, failed
			}
			vr, err := readOf(rd.Epoch)
			if err != nil {
				return Part{}, err
			}
			va, err := vr.load(rd)
			if err != nil {
				return Part{}, err
			}
			if d := va.extra[0].Done; len(d) == 1 {
				switch d[0].Status {
				case "match":
					replayed = true
					if d[0].Receipt != nil {
						recorded = d[0].Receipt.Result
					}
					return Part{}, nil
				case "conflict":
					return Part{}, refuseLocal(verb, "OPCONFLICT", "op %s was aborted with other arguments", req.Op)
				}
			}
			dr, ok := va.keys[1].(sprintfn.DroppingResult)
			if !ok {
				return Part{}, fmt.Errorf("verbs: the dropping answer is of another kind")
			}
			var mine, others []string
			for _, s := range streams {
				switch m, marked := dr.Marks[s]; {
				case !marked:
				case m == req.Op:
					mine = append(mine, s)
				default:
					others = append(others, s+" (frozen by op "+m+")")
				}
			}
			if named && len(others) != 0 {
				return Part{}, refuseLocal(verb, sprintfn.CodeDropping, "not frozen by op %s: %s", req.Op, strings.Join(others, ", "))
			}
			if len(mine) == 0 {
				where := "any stream of the sprint"
				if named {
					where = strings.Join(streams, ", ")
				}
				return Part{}, refuseLocal(verb, sprintfn.CodeRequest, "op %s freezes none of %s: there is nothing to abort", req.Op, where)
			}
			counts := countsOf(va, 0)
			var left []string
			for i, s := range streams {
				if !contains(mine, s) {
					continue
				}
				var in []string
				for j, col := range openCols {
					if n := counts[i*len(openCols)+j]; n > 0 {
						in = append(in, fmt.Sprintf("%s %d", col, n))
					}
				}
				if len(in) != 0 {
					left = append(left, s+" ("+strings.Join(in, ", ")+")")
				}
			}
			said := "nothing was left"
			if len(left) != 0 {
				said = "left: " + strings.Join(left, "; ")
			}
			if len(said) > tset.MaxResultBytes {
				said = said[:tset.MaxResultBytes-3] + "..."
			}
			intent, err := epochOf(rd.Epoch)
			if err != nil {
				return Part{}, err
			}
			return Part{Req: &sprintfn.Request{Meta: sprintfn.Meta{Verb: "drop"},
				Sprint: &sprintfn.SprintPart{Undrop: marks(mine, req.Op)},
				Body: sprintfn.Body{Notes: []sprintfn.NoteReq{unfrozen(mine, req.Op)},
					Op: &sprintfn.Op{ID: req.Op + "/abort", Intent: intent, Result: said}}}}, nil
		}})
	res.Op = req.Op
	res.Trips += listed
	if err != nil {
		return res, err
	}
	switch {
	case replayed:
		res.Replay, res.Recorded = true, recorded
		res.Said = fmt.Sprintf("drop --abort: op %s was aborted already; nothing was written", req.Op)
	case res.Step != nil:
		res.Said = fmt.Sprintf("drop --abort: op %s aborted, its streams unfrozen; %s", req.Op, res.Recorded)
	}
	return res, nil
}

// sprintStreams are the sprint's streams, read by the `streams` listing (1.0;
// at most sprint.MaxStreams): the streams whose marks an abort reads.
func sprintStreams(ctx context.Context, e *Env, verb string) ([]string, error) {
	q, ref := sprintfn.EncodeSprintQ(sprint.SprintQ{Kind: sprint.QueryStreams, Fields: []string{}})
	if ref != nil {
		return nil, &Refused{Verb: verb, Refusal: ref, Local: true}
	}
	r, err := sprintfn.Read(ctx, e.C, &sprintfn.ReadRequest{Epoch: dec(e.epoch()), Sprint: []sprintfn.SprintQuery{q}})
	switch {
	case err != nil:
		return nil, err
	case r.Err != nil:
		return nil, r.Err
	case r.Refusal != nil:
		return nil, &Refused{Verb: verb, Refusal: r.Refusal}
	case len(r.Read.Sprint) != 1:
		return nil, fmt.Errorf("verbs: the streams listing answered %d queries", len(r.Read.Sprint))
	}
	qr, err := sprintfn.DecodeResult(sprint.QueryStreams, r.Read.Sprint[0])
	if err != nil {
		return nil, err
	}
	sr, ok := qr.(sprintfn.StreamsResult)
	if !ok {
		return nil, fmt.Errorf("verbs: the streams answer is of another kind")
	}
	if sr.HasMore {
		return nil, refuseLocal(verb, sprintfn.CodeLimit, "the sprint lists more than %d streams: name the streams with --stream", len(sr.Rows))
	}
	out := append([]string(nil), sr.Rows...)
	sort.Strings(out)
	return out, nil
}

package verbs

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The merger's verbs of section 3 (item IT21): merge, one stream's batch with
// its facts, and resume, a set of stopped streams moved again. Each is one
// step through Env.Do: two round trips.

// IdleSpan is how long a stream with open cards may land nothing before R11
// says so (1.2, `idle:<stream>`: R + IdleSpan at each landing of one of its
// cards; section 2.5: 2 h, the owner's value, section 7).
const IdleSpan = 2 * time.Hour

// The work table's open cells, in the order a drop drains them (1.5.4).
var openCols = []string{sprint.Waiting, sprint.Ready, sprint.Working, sprint.Review, sprint.Merging}

// MergeReq is merge's request: the stream, the batch (the first Batch cards
// of its queue, 0 for all of it before the first stuck card), and the facts,
// at most one of which stops the stream: a card of the batch that conflicted,
// a card that needs a card of another stream first (Cross "<card>=<other>"),
// the stream branch red on the batch (with its Suspects), or the batch
// rejected by the merge queue. With no fact the batch landed.
type MergeReq struct {
	Op       string
	Stream   string
	Batch    int
	Conflict string
	Cross    string
	Red      bool
	Suspects []string
	Rejected bool
	Note     string
}

// mergeBatchMax is the most cards one merge moves: each lands with its
// primary, two members, inside the chunk (1.0).
const mergeBatchMax = StepChunk / 2

// The queries of merge's read, by their index in its plan's ranges and counts.
const (
	mergeQueued  = 0 // merge s:queued, the first b
	mergeStuck   = 1 // merge s:stuck, the first one: the barrier
	mergeMerging = 2 // work s:merging, the first b: the batch's primaries
)

// Merge is one merge step of a stream (section 3): the first b cards of its
// queued cell, in work order (one stream by nature: its batch is one stream's
// queue), and the facts the merger gives. With no fact the batch lands: each
// merge card queued -> merged, each primary merging -> landed, and the control
// card's ci green, moved, and due_idle = R + IdleSpan (1.2); the stream
// landed when every open card of it has, waiting when nothing is queued or
// stuck after the batch (due_mergeidle unset), else merging with
// due_mergeidle = R + 30 min; KNOW "batch landed", and "stream landed". A fact
// that stops the stream stops it (state stopped, its cause, due_mergeidle
// unset) and opens its judgment (2.2's four "stream stopped" rows): a
// conflict moves its card queued -> stuck; a cross need moves the card
// queued -> stuck naming the card it needs; a red branch marks the batch's
// primaries red. The read is the first b of s's queued cell and the first of
// its stuck cell (a stuck card is a barrier: the batch is what is queued
// before it), the batch's primaries (the first b of s's merging cell: every
// merging primary is queued or stuck, so those before the barrier are the
// batch), the control card, and the counts of s's cells. Guard: each card
// moved at its cell with its revision, and the control card with its
// revision. Two round trips.
func Merge(ctx context.Context, e *Env, req MergeReq) (Result, error) {
	const verb = "merge"
	s := req.Stream
	if !sprint.ValidID(s) {
		return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "%q is not a stream", s)
	}
	facts := 0
	for _, f := range []bool{req.Conflict != "", req.Cross != "", req.Red, req.Rejected} {
		if f {
			facts++
		}
	}
	if facts > 1 {
		return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "gives %d facts that stop the stream; one step takes one", facts)
	}
	if req.Batch < 0 || req.Batch > mergeBatchMax {
		return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "--batch %d is not 1 to %d", req.Batch, mergeBatchMax)
	}
	b := req.Batch
	if b == 0 {
		b = mergeBatchMax
	}
	other := ""
	if req.Cross != "" {
		card, o, ok := strings.Cut(req.Cross, "=")
		if !ok || !sprint.ValidID(card) || !sprint.ValidID(o) {
			return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "the cross fact %q wants <card>=<other>", req.Cross)
		}
		other = o
	}
	cells := func(col ...string) []string {
		out := make([]string, len(col))
		for i, c := range col {
			out[i] = s + ":" + c
		}
		return out
	}
	rp := sprint.ReadPlan{
		IDs: map[string][]string{sprint.Merge: {sprint.CtlID(s)}},
		Ranges: []sprint.RangeQ{
			{Table: sprint.Merge, Cell: s + ":" + sprint.Queued, Limit: b, Records: true, Fields: []string{"need_card", "need_stream"}},
			{Table: sprint.Merge, Cell: s + ":" + sprint.Stuck, Limit: 1, Records: true, Fields: []string{}},
			{Table: sprint.Work, Cell: s + ":" + sprint.Merging, Limit: b, Records: true, Fields: []string{"stuck", "ci"}},
		},
		Counts: []sprint.CountQ{
			{Table: sprint.Work, Cells: cells(append(append([]string(nil), openCols...), sprint.Landed)...)},
			{Table: sprint.Merge, Cells: cells(sprint.Queued, sprint.Stuck)},
		},
	}
	if other != "" {
		rp.IDs[sprint.Work] = []string{other}
	}
	vr := verbRead{plan: rp}
	var failed error
	args := map[string]any{"stream": s, "batch": req.Batch, "conflict": req.Conflict, "cross": req.Cross, "red": req.Red,
		"suspects": append([]string{}, req.Suspects...), "rejected": req.Rejected, "note": req.Note}
	return e.Do(ctx, Planned{Verb: verb, Op: req.Op, Args: args,
		Read: func(epoch tset.Decimal) *sprintfn.ReadRequest { return vr.readAt(e.Names, epoch, &failed) },
		Plan: func(rd *sprintfn.ReadReply) (Part, error) {
			if failed != nil {
				return Part{}, failed
			}
			va, err := vr.load(rd)
			if err != nil {
				return Part{}, err
			}
			req, err := planMerge(verb, va, req, other)
			if err != nil {
				return Part{}, err
			}
			return Part{Req: req}, nil
		}})
}

// rangeIDs is the ids a range of the verb's plan answered, by its index.
func rangeIDs(va *verbAnswer, index int) []string {
	slots := va.snap.Partial.Plan.TsetSlots()
	for i, sl := range slots {
		if sl.Kind == sprint.AnswerRange && sl.Index == index {
			return va.snap.Partial.Answer.Tset[i].IDs
		}
	}
	return nil
}

// countsOf is the counts a count query of the verb's plan answered, by its
// index, cell by cell.
func countsOf(va *verbAnswer, index int) []int {
	slots := va.snap.Partial.Plan.TsetSlots()
	for i, sl := range slots {
		if sl.Kind == sprint.AnswerCount && sl.Index == index {
			return va.snap.Partial.Answer.Tset[i].Counts
		}
	}
	return nil
}

func sum(ns []int) int {
	t := 0
	for _, n := range ns {
		t += n
	}
	return t
}

func planMerge(verb string, va *verbAnswer, req MergeReq, other string) (*sprintfn.Request, error) {
	snap, now, s := va.snap, va.now, req.Stream
	ctl := snap.StreamCtl(s)
	if ctl == nil {
		return nil, refuseLocal(verb, sprintfn.CodeRequest, "stream %s has no control card", s)
	}
	switch st := ctl.F("state"); st {
	case sprint.StreamStopped:
		return nil, refuseLocal(verb, sprintfn.CodeRequest, "stream %s is stopped (%s): run resume --stream %s", s, ctl.F("cause"), s)
	case sprint.StreamLanded:
		return nil, refuseLocal(verb, sprintfn.CodeRequest, "stream %s has landed", s)
	}
	var batch []*sprint.Card
	var barrier *sprint.Card
	if st := rangeIDs(va, mergeStuck); len(st) != 0 {
		barrier = snap.Merge.Card(st[0])
	}
	for _, id := range rangeIDs(va, mergeQueued) {
		c := snap.Merge.Card(id)
		if barrier != nil && (c.Score > barrier.Score || c.Score == barrier.Score && c.ID > barrier.ID) {
			break // the step never passes an earlier stuck card
		}
		batch = append(batch, c)
	}
	if len(batch) == 0 {
		why := "nothing is queued in stream " + s
		if barrier != nil {
			why = "nothing is queued before the stuck card of stream " + s + ": resume it first"
		}
		return nil, refuseLocal(verb, sprintfn.CodeRequest, "%s", why)
	}
	var ids []string
	for _, c := range batch {
		ids = append(ids, c.ID)
	}
	inBatch := func(id string) bool { return contains(ids, id) }
	primary := func(id string) (*sprint.Card, string) {
		pr := snap.Work.Card(id)
		if pr == nil || !pr.Placed() || pr.Col != sprint.Merging || pr.Row != s {
			return nil, fmt.Sprintf("%s is queued in merge and not merging in work (%s)", id, cardPlace(pr))
		}
		return pr, ""
	}
	wall := wallStamp(now)
	var b stepOps
	ctlSet := map[string]string{}
	var notes []sprintfn.NoteReq
	started := ctl.F("state") == sprint.StreamWaiting
	stop := func(cause, text string, set map[string]string, unset ...string) {
		ctlSet["state"], ctlSet["since"], ctlSet["cause"] = sprint.StreamStopped, wall, cause
		for k, v := range set {
			ctlSet[k] = v
		}
		b.move(sprint.Merge, ctl, "", ctlSet, append(unset, "due_mergeidle")...)
		notes = append(notes, note(sprintfn.JOpOpen, stopTypes[cause], cause, text, []string{sprint.StreamSubject(s)}, decisionsOf(stopTypes[cause])...))
	}
	switch {
	case req.Conflict != "":
		if !inBatch(req.Conflict) {
			return nil, refuseLocal(verb, sprintfn.CodeRequest, "%s is not a card of the batch of %d (%s)", req.Conflict, len(ids), span(ids))
		}
		m := snap.Merge.Card(req.Conflict)
		pr, why := primary(req.Conflict)
		if why != "" {
			return nil, refuseLocal(verb, sprintfn.CodeRequest, "%s", why)
		}
		b.move(sprint.Merge, m, sprint.Stuck, nil, "need_card", "need_stream")
		b.move(sprint.Work, pr, "", map[string]string{"stuck": strconv.Itoa(pr.Int("stuck") + 1)})
		stop("conflict", fmt.Sprintf("%s did not merge: %s", req.Conflict, orText(req.Note, "a conflict")), map[string]string{"card": req.Conflict}, "other")
	case req.Cross != "":
		card, _, _ := strings.Cut(req.Cross, "=")
		if !inBatch(card) {
			return nil, refuseLocal(verb, sprintfn.CodeRequest, "%s is not a card of the batch of %d (%s)", card, len(ids), span(ids))
		}
		oc := snap.Work.Card(other)
		switch {
		case other == card:
			return nil, refuseLocal(verb, sprintfn.CodeRequest, "the cross fact names the card itself")
		case oc == nil || !oc.Placed():
			return nil, refuseLocal(verb, sprintfn.CodeRequest, "the other card %s is not on the table", other)
		case oc.Row == s:
			return nil, refuseLocal(verb, sprintfn.CodeRequest, "the other card %s is in the same stream %s", other, s)
		case oc.Col == sprint.Landed:
			return nil, refuseLocal(verb, sprintfn.CodeRequest, "the other card %s has landed already: nothing to wait for", other)
		}
		b.move(sprint.Merge, snap.Merge.Card(card), sprint.Stuck, map[string]string{"need_card": other, "need_stream": oc.Row})
		stop("cross", fmt.Sprintf("%s (stream %s) needs %s (stream %s) landed first", card, s, other, oc.Row), map[string]string{"card": card, "other": other})
	case req.Red:
		for _, x := range req.Suspects {
			if !inBatch(x) {
				return nil, refuseLocal(verb, sprintfn.CodeRequest, "the suspect %s is not a card of the batch of %d (%s)", x, len(ids), span(ids))
			}
		}
		for _, id := range ids {
			pr, why := primary(id)
			if why != "" {
				return nil, refuseLocal(verb, sprintfn.CodeRequest, "%s", why)
			}
			b.move(sprint.Work, pr, "", map[string]string{"ci": "red", "ci_at": wall})
		}
		set := map[string]string{"ci": "red"}
		text := "the stream branch went red on the batch " + span(ids)
		if len(req.Suspects) != 0 {
			set["suspects"] = strings.Join(req.Suspects, ",")
			text += "; suspects: " + strings.Join(req.Suspects, ", ")
		}
		stop("red", text, set, "card", "other")
	case req.Rejected:
		stop("rejected", "the merge queue rejected the batch "+span(ids), nil, "card", "other")
	default:
		work := countsOf(va, 0)  // waiting .. merging, landed
		merge := countsOf(va, 1) // queued, stuck
		if len(work) != len(openCols)+1 || len(merge) != 2 {
			return nil, fmt.Errorf("verbs: merge's counts are misaligned")
		}
		for _, c := range batch {
			pr, why := primary(c.ID)
			if why != "" {
				return nil, refuseLocal(verb, sprintfn.CodeRequest, "%s", why)
			}
			b.move(sprint.Merge, c, sprint.Merged, map[string]string{"merged": wall})
			b.move(sprint.Work, pr, sprint.Landed, map[string]string{"ci": "green", "landed": wall})
		}
		landing := len(batch)
		ctlSet["ci"], ctlSet["moved"], ctlSet["due_idle"] = "green", wall, dueAt(now, IdleSpan)
		var unset []string
		landed := false
		switch {
		case work[len(openCols)]+landing > 0 && sum(work[:len(openCols)]) == landing:
			ctlSet["state"], ctlSet["since"] = sprint.StreamLanded, wall
			unset = append(unset, "due_mergeidle")
			landed = true
		case merge[0]+merge[1] == landing:
			ctlSet["state"], ctlSet["since"] = sprint.StreamWaiting, wall
			unset = append(unset, "due_mergeidle")
			started = false
		default:
			ctlSet["state"] = sprint.StreamMerging
			if started {
				ctlSet["since"] = wall
			}
			ctlSet["due_mergeidle"] = dueAt(now, mergeIdleSpan) // a merge step moves the idle deadline
		}
		b.move(sprint.Merge, ctl, "", ctlSet, unset...)
		if started {
			notes = append(notes, note(sprintfn.JOpKnow, typeStarted, "", "", []string{sprint.StreamSubject(s)}))
		}
		notes = append(notes, note(sprintfn.JOpKnow, typeBatchLanded, "", fmt.Sprintf("%d landed: %s", landing, span(ids)), []string{sprint.StreamSubject(s)}))
		if landed {
			notes = append(notes, note(sprintfn.JOpKnow, typeStreamLanded, "", "", []string{sprint.StreamSubject(s)}))
		}
	}
	entries, err := b.entries()
	if err != nil {
		return nil, err
	}
	return &sprintfn.Request{Meta: sprintfn.Meta{Verb: verb}, Body: sprintfn.Body{Entries: entries, Notes: notes}}, nil
}

func orText(s, otherwise string) string {
	if s == "" {
		return otherwise
	}
	return s
}

// span is a batch by its first and last card.
func span(ids []string) string {
	switch len(ids) {
	case 0:
		return "empty"
	case 1:
		return ids[0]
	}
	return ids[0] + " .. " + ids[len(ids)-1]
}

// ResumeReq is resume's request: the streams, and what the coordinator did
// (required after a red branch).
type ResumeReq struct {
	Op      string
	Streams []string
	Did     string
}

// resumeStuckMax is the most stuck cards of one stream a resume moves: one
// range read, and within the chunk.
const resumeStuckMax = StepChunk / 2

// Resume moves stopped streams again (section 3, `resume --stream s[,s...]
// --did`), the set in one step: each stream's stuck cards stuck -> queued at
// their scores, its state merging (waiting when nothing is queued or stuck),
// due_mergeidle at R + 30 min, and its stop judgment closed. A stream not
// stopped, a red stop with no --did, and a cross stop whose needed card has
// not landed are refused, naming the stream: the set is all or nothing. The
// read is every stream's control card and, for one stopped on a cross need,
// the needed card (`streams`, 1.0), each named stream's stuck cell and the
// count of its queued cell. Guard: each control card and stuck card with its
// revision. Two round trips.
func Resume(ctx context.Context, e *Env, req ResumeReq) (Result, error) {
	const verb = "resume"
	streams, err := sortedIDs(verb, req.Streams)
	if err != nil {
		return Result{Verb: verb}, err
	}
	rp := sprint.ReadPlan{Sprint: []sprint.SprintQ{{Kind: sprint.QueryStreams, Fields: []string{"state", "cause", "other", "need_card", "card", "due_mergeidle"}, Limit: 1}}}
	var queued []string
	for _, s := range streams {
		rp.Ranges = append(rp.Ranges, sprint.RangeQ{Table: sprint.Merge, Cell: s + ":" + sprint.Stuck, Limit: resumeStuckMax, Records: true,
			Fields: []string{"need_card", "need_stream"}})
		queued = append(queued, s+":"+sprint.Queued)
	}
	rp.Counts = []sprint.CountQ{{Table: sprint.Merge, Cells: queued}}
	vr := verbRead{plan: rp}
	var failed error
	return e.Do(ctx, Planned{Verb: verb, Op: req.Op, Args: map[string]any{"streams": streams, "did": req.Did},
		Read: func(epoch tset.Decimal) *sprintfn.ReadRequest { return vr.readAt(e.Names, epoch, &failed) },
		Plan: func(rd *sprintfn.ReadReply) (Part, error) {
			if failed != nil {
				return Part{}, failed
			}
			va, err := vr.load(rd)
			if err != nil {
				return Part{}, err
			}
			snap, now := va.snap, va.now
			counts := countsOf(va, 0)
			var b stepOps
			var refused []sprint.Refusal
			var notes []sprintfn.NoteReq
			for i, s := range streams {
				ctl := snap.StreamCtl(s)
				if ctl == nil {
					refused = append(refused, sprint.Refusal{Key: s, Why: "no such stream"})
					continue
				}
				cause := ctl.F("cause")
				switch {
				case ctl.F("state") != sprint.StreamStopped:
					refused = append(refused, sprint.Refusal{Key: s, Why: "not stopped (it is " + orText(ctl.F("state"), "-") + ")"})
					continue
				case cause == "red" && strings.TrimSpace(req.Did) == "":
					refused = append(refused, sprint.Refusal{Key: s, Why: "stopped for a red branch: say what was done with --did"})
					continue
				case cause == "cross":
					need := ctl.F("other")
					if need == "" {
						need = ctl.F("need_card")
					}
					if nc := snap.Work.Card(need); nc == nil || nc.Col != sprint.Landed {
						refused = append(refused, sprint.Refusal{Key: s, Why: fmt.Sprintf("unresolved: it needs %s landed first, and it is %s", need, cardPlace(nc))})
						continue
					}
				}
				stuck := rangeIDs(va, i)
				if len(stuck) >= resumeStuckMax {
					refused = append(refused, sprint.Refusal{Key: s, Why: fmt.Sprintf("holds more than %d stuck cards, past one step", resumeStuckMax-1)})
					continue
				}
				for _, id := range stuck {
					b.move(sprint.Merge, snap.Merge.Card(id), sprint.Queued, nil, "need_card", "need_stream")
				}
				set := map[string]string{"since": wallStamp(now)}
				unset := []string{"cause", "card", "other"}
				if len(stuck)+counts[i] > 0 {
					set["state"], set["due_mergeidle"] = sprint.StreamMerging, dueAt(now, mergeIdleSpan)
				} else {
					set["state"] = sprint.StreamWaiting
					unset = append(unset, "due_mergeidle")
				}
				if req.Did != "" {
					set["did"] = req.Did
				}
				b.move(sprint.Merge, ctl, "", set, unset...)
				if typ := stopTypes[cause]; typ != "" {
					notes = append(notes, note(sprintfn.JOpClose, typ, cause, orText(req.Did, "resumed"), []string{sprint.StreamSubject(s)}))
				}
			}
			if len(refused) != 0 {
				return Part{}, refusedIDs(verb, refused)
			}
			entries, err := b.entries()
			if err != nil {
				return Part{}, err
			}
			return Part{Req: &sprintfn.Request{Meta: sprintfn.Meta{Verb: verb}, Body: sprintfn.Body{Entries: entries, Notes: notes}}}, nil
		}})
}

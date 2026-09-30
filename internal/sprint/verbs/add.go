package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The verbs add, release and rank (the upper design, version 2.1, section 3's
// rows, 1.5.4 and U2 of 1.3.1; item IT19). add goes in parts through Parts:
// part 1 reserves the op from the counter {p}next@e and creates its first
// slice, every later part its own slice from the reservation (sprint.Reserve,
// sprint.AddPart). release and rank are one step each through Do.
//
// Where the stack's shapes do not reach the design's, the narrower reading is
// taken and listed here; the pull request repeats them.
//
//	Round trips. An insertion (add --before/--after), a release and a rank
//	--before/--after read one more time than section 3's column says (n + 2
//	and 3): the rows of a stream and the scores around a card cannot be named
//	in a read until a read has said which stream and which score (Layer 1's
//	count refuses a row it does not hold, NOROW, and a range needs its bounds),
//	so the anchor, or the sentinel, is read first. add --count and named ids
//	keep n + 1; rank --score reads its cards first too (H13, below), 3.
//	The counter's read is the sprint-key kind next (sprintfn, IT19's addition):
//	the addendum's kinds have none for it.
//	The size bounds are read from Layer 1's rows (AL3) of the four tables in
//	part 1's read: members, readers, and the streams of work and merge.
//	release closes "sentinel reached" on G and writes KNOW "sentinel landed"
//	with the reason, both on G; it is refused while any open card of G's
//	stream sorts before G (the rcount the design names, at most 0), from the
//	read and at apply.
//	rank --score x with several cards gives them x, x + 1, ... in the order of
//	their scores, so x must be at or above the counter (which it raises);
//	below the counter, several cards go in line with --before or --after.
//	H13 (rankclose) is taken in the model's form (errata 3, amendment 2; VEff
//	"rank" with ReachedPassed): an insertion or any rank that places an open
//	card before the first sentinel closes its "sentinel reached" in its own
//	step. rank --score pays a third round trip for it: its first read names
//	the cards' streams, whose fronts the step's read holds. R3's unreach, which
//	the rank's line queues (2.1), stays the fallback. The cause of "sentinel
//	reached" is the model's "-" (sprint.ReachedCause).
//	An insertion resumed with --op reads its anchor again before the op (n +
//	3), and refuses an anchor that has landed since, which the later parts'
//	own guards would accept: kept, so a resume plans from the anchor as it is.
//
// The model: tla/SprintEvents.tla's verb actions, VGuard and VEff of "add"
// (AddDest, the COUNTER guard, the zguard, madeclose, rankclose), "release"
// (the rcount of the open cells before G) and "rank" (U2, ScoreTaken).

// ---- add

// addCont is the continuation a part of an add leaves (1.5.4): the reservation
// part 1 made and the offset of the next part's first item.
type addCont struct {
	Res sprint.Reservation `json:"res"`
	K   int                `json:"k"`
}

// addArgs are the add's arguments as its intent carries them (L1 5; 1.5.4: a
// named id list is its digest).
func addArgs(r sprint.AddReq) map[string]any {
	args := map[string]any{"stream": r.Stream}
	if len(r.IDs) > 0 {
		args["ids"] = IDsDigest(r.IDs)
		args["n"] = len(r.IDs)
	}
	if r.Count > 0 {
		args["count"] = r.Count
	}
	if len(r.Needs) > 0 {
		args["needs"] = append([]string(nil), r.Needs...)
	}
	if r.Brief != "" {
		args["brief"] = IDsDigest([]string{r.Brief})
	}
	if r.Sentinel {
		args["sentinel"] = true
	}
	if r.Before != "" {
		args["before"] = r.Before
	}
	if r.After != "" {
		args["after"] = r.After
	}
	if r.Every > 0 {
		args["sentinel-every"] = r.Every
	}
	if r.Last {
		args["sentinel-last"] = true
	}
	return args
}

// storedAll is ids as the table holds them at an epoch.
func storedAll(ids []string, epoch uint64) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = sprint.StoredID(id, epoch)
	}
	return out
}

// anchorRead is what an insertion or a rank reads of its anchor first: its
// stream, its score and whether it is placed and open.
type anchorRead struct {
	ID     string // stored
	Stream string
	Col    string
	Score  float64
}

// readRecords reads the work table's records of ids (one round trip): the
// anchor of an insertion or of a rank, and the sentinels of a release, whose
// streams and scores the next read needs.
func readRecords(ctx context.Context, e *Env, res *Result, ids []string, fields []string) ([]sprintfn.Record, error) {
	epoch := dec(e.epoch())
	q, ref := sprintfn.EncodeSprintQ(sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work,
		Source: sprint.IDSource{Kind: sprint.SourceIDs, IDs: storedAll(ids, e.epoch())}, Fields: fields})
	if ref != nil {
		return nil, &Refused{Verb: res.Verb, Refusal: ref, Local: true}
	}
	r, err := sprintfn.Read(ctx, e.C, &sprintfn.ReadRequest{Epoch: epoch, Sprint: []sprintfn.SprintQuery{q}})
	res.Trips++
	if err != nil {
		return nil, err
	}
	if r.Err != nil {
		return nil, r.Err
	}
	if r.Refusal != nil {
		return nil, &Refused{Verb: res.Verb, Refusal: r.Refusal}
	}
	if active, ok := undec(r.Read.ActiveEpoch); ok && active != e.epoch() {
		e.setEpoch(active)
		return readRecords(ctx, e, res, ids, fields)
	}
	rel, err := decodeRelated(r.Read.Sprint[0])
	if err != nil {
		return nil, err
	}
	out := make([]sprintfn.Record, 0, len(ids))
	byID := map[string]sprintfn.Record{}
	for _, it := range rel.Items {
		byID[it.ID] = it.Record
	}
	for _, id := range storedAll(ids, e.epoch()) {
		out = append(out, byID[id])
	}
	return out, nil
}

// decodeRelated is a `related` answer.
func decodeRelated(raw json.RawMessage) (sprintfn.RelatedResult, error) {
	res, err := sprintfn.DecodeResult(sprint.QueryRelated, raw)
	if err != nil {
		return sprintfn.RelatedResult{}, err
	}
	return res.(sprintfn.RelatedResult), nil
}

// scoreOf is a record's score as a number.
func scoreOf(r sprintfn.Record) float64 {
	f, _ := strconv.ParseFloat(r.Score, 64)
	return f
}

// anchorOf reads the anchor of an insertion or a rank (one round trip): it must
// be placed and not landed, in the stream named when one is.
func anchorOf(ctx context.Context, e *Env, res *Result, verb, id, stream string) (anchorRead, error) {
	recs, err := readRecords(ctx, e, res, []string{id}, []string{"kind"})
	if err != nil {
		return anchorRead{}, err
	}
	rec := recs[0]
	row, col := placeOf(rec)
	switch {
	case !rec.Exists || row == "":
		return anchorRead{}, refuseLocal(verb, "MISSING", "%s is not a card on the table", id)
	case col == string(sprint.Landed):
		return anchorRead{}, refuseLocal(verb, sprintfn.CodeRequest, "%s has landed; a card goes in line before or after an open card", id)
	case stream != "" && row != stream:
		return anchorRead{}, refuseLocal(verb, sprintfn.CodeRequest, "%s is a card of stream %s, not of %s", id, row, stream)
	}
	return anchorRead{ID: sprint.StoredID(id, e.epoch()), Stream: row, Col: col, Score: scoreOf(rec)}, nil
}

// neighbourQs are the reads of the card next to a score in a stream, in every
// one of its six cells (1.5.4: "the stream's six cells"): the first after the
// score (or before, desc), limit each, with their scores.
func neighbourQs(stream string, score float64, desc bool, limit int) []tset.ReadQuery {
	var out []tset.ReadQuery
	for _, st := range sprint.States {
		q := tset.ReadQuery{Kind: "range", Table: sprint.Work, Cell: stream + ":" + string(st), Limit: limit}
		if desc {
			q.Min, q.Max, q.Desc = "-inf", "("+fmtF(score), true
		} else {
			q.Min, q.Max = "("+fmtF(score), "+inf"
		}
		out = append(out, q)
	}
	return out
}

// nearest is the nearest card to the anchor among the six answers, skipping the
// ids in skip: its id and score; "" when there is none.
func nearest(ans []tset.ReadAnswer, desc bool, skip map[string]bool) (string, float64) {
	id, best := "", 0.0
	for _, a := range ans {
		for i, m := range a.IDs {
			if skip[m] || i >= len(a.Scores) {
				continue
			}
			f, err := strconv.ParseFloat(a.Scores[i], 64)
			if err != nil {
				continue
			}
			if id == "" || (!desc && f < best) || (desc && f > best) {
				id, best = m, f
			}
			break
		}
	}
	return id, best
}

// fmtF is a score as the store's grammar writes it: the shortest decimal.
func fmtF(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// bounds are the scores an insertion's or a rank's cards lie between: the
// anchor and its neighbour, or, with no neighbour, the next integer after the
// anchor (after) or the one before it (before), which U2 keeps at or below the
// counter.
func bounds(anchor float64, nb string, nbScore float64, before bool) (lo, hi float64) {
	if before {
		hi = anchor
		if nb != "" {
			lo = nbScore
		} else {
			lo = math.Ceil(anchor) - 1
		}
		return lo, hi
	}
	lo = anchor
	if nb != "" {
		hi = nbScore
	} else {
		hi = math.Floor(anchor) + 1
	}
	return lo, hi
}

// Add admits cards (section 3's add rows; 1.5.4): named ids of any number, or
// --count n in each stream of --stream s[,s...] with a gate after every
// --sentinel-every cards, or named ids in line --before/--after a card. It
// goes in parts: part 1 reserves the op from the counter and creates the first
// slice; each part creates its slice, and a card that names needs, a gate, a
// sentinel and a card behind an unlanded sentinel go to waiting, the others to
// ready. A needs cycle is refused before part 1 (the needchain walk from the
// named needs, at most 2,000 records, refused past it naming its head), and so
// is a stream past the size bounds. n parts cost n + 1 round trips (n + 2 for
// an insertion, whose anchor is read first).
func Add(ctx context.Context, e *Env, r sprint.AddReq) (Result, error) {
	const verb = "add"
	res := Result{Verb: verb}
	if err := sprint.CheckAdd(r); err != nil {
		return res, refuseLocal(verb, sprintfn.CodeRequest, "%v", err)
	}
	var anchor anchorRead
	if a := r.Before + r.After; a != "" {
		var err error
		if anchor, err = anchorOf(ctx, e, &res, verb, a, sprint.AddStreams(r)[0]); err != nil {
			return res, err
		}
	}
	pre := res.Trips
	// guess is the slice part 1 of a --count add planned last: its read names
	// ids it has not reserved yet, so a retry of part 1 reads the ids its last
	// plan made (addRead). Read and Plan run in turn on one goroutine (Parts).
	var guess []string
	pp := PartsPlan{Verb: verb, Args: addArgs(r), Chunk: sprint.AddChunk,
		Read: func(epoch tset.Decimal, cont string, chunk int) *sprintfn.ReadRequest {
			rr, _, err := addRead(e.Names, epoch, r, anchor, cont, chunk, guess)
			if err != nil {
				return nil
			}
			return rr
		},
		Plan: func(rd *sprintfn.ReadReply, cont string, chunk int) (Part, error) {
			return addPlan(e, r, anchor, rd, cont, chunk, &guess)
		}}
	out, err := e.Parts(ctx, r.Op, pp)
	out.Trips += pre
	return out, err
}

// addQueries is what a part's read asks beyond its plan, and where the answers
// are: Layer 1's rows of the four tables (part 1), and the sprint keys.
type addQueries struct {
	rp                    sprint.ReadPlan
	rows                  int // the first of the four rows queries in Tset, -1 when none
	neighbours            int // the first of the six neighbour ranges in Tset, -1 when none
	next, clock, dropping int // their places in Sprint, -1 when none
	chain                 int // the needchain's place in Sprint, -1 when none
	made                  int // the made read's place in Sprint (AddMadeQ), -1 when none
	nextFields            []string
	slice                 []string // the stored ids the part may create (the made read)
}

// addTables are the tables whose rows part 1 reads: the streams (work, merge),
// the members and the readers (the size bounds, 3).
var addTables = []string{sprint.Work, sprint.Merge, sprint.Fleet, sprint.Readers}

// addRead is the read of a part: for part 1 (cont "") the counter, the clock,
// the dropping marks and the rows beside what every part reads; for every part
// front(s) of each stream it creates in, the needs' records, and the waiters of
// the ids it creates that are missing (H3). The part's plan is made from the
// same function's answer, so the read and the plan agree by construction.
// The made read also says whether each id of the slice has a record (section
// 3: add reads the ids' absence), so an id the table holds is refused before
// the step. Part 1 of a --count add cannot name its slice before it reads the
// counter: it reads guess, the slice its last plan made, when it has one (a
// retry after its step was refused).
func addRead(names sprint.Names, epoch tset.Decimal, r sprint.AddReq, anchor anchorRead, cont string, chunk int, guess []string) (*sprintfn.ReadRequest, addQueries, error) {
	q := addQueries{rows: -1, neighbours: -1, next: -1, clock: -1, dropping: -1, chain: -1, made: -1}
	ep, err := epochOf(epoch)
	if err != nil {
		return nil, q, err
	}
	streams := sprint.AddStreams(r)
	var res sprint.Reservation
	k := 0
	first := cont == ""
	if !first {
		var c addCont
		if err := json.Unmarshal([]byte(cont), &c); err != nil {
			return nil, q, fmt.Errorf("add: the continuation %q: %w", cont, err)
		}
		res, k = c.Res, c.K
		res.Chunk = chunk
	}
	// The streams the part creates in, and the ids it may create.
	partStreams := streams
	var slice []string
	switch {
	case first && r.Count > 0:
		// part 1 of a --count add cannot name ids it has not reserved: it reads
		// the slice its last plan made, if any
		slice = append([]string(nil), guess...)
	case first:
		n := min(len(r.IDs), max(chunk, 1))
		slice = storedAll(r.IDs[:n], ep)
	default:
		size := res.PartSize(k)
		partStreams = nil
		seen := map[string]bool{}
		var scores []float64
		if res.Insert {
			if scores, err = sprint.InsertScores(res.Lo, res.Hi, res.Total); err != nil {
				return nil, q, err
			}
		}
		for j := k; j < k+size; j++ {
			it := res.Item(r, j, scores)
			slice = append(slice, sprint.StoredID(it.ID, ep))
			if !seen[it.Stream] {
				seen[it.Stream] = true
				partStreams = append(partStreams, it.Stream)
			}
		}
	}
	for _, s := range partStreams {
		q.rp.Sprint = append(q.rp.Sprint, sprint.SprintQ{Kind: sprint.QueryFront, Stream: s, Fields: []string{"kind"}})
	}
	needs := storedAll(r.Needs, ep)
	if len(needs) > 0 {
		q.rp.Sprint = append(q.rp.Sprint, sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work,
			Source: sprint.IDSource{Kind: sprint.SourceIDs, IDs: needs}, Fields: []string{"kind"}})
		if first {
			q.chain = len(q.rp.Sprint)
			q.rp.Sprint = append(q.rp.Sprint, sprint.SprintQ{Kind: sprint.QueryNeedchain,
				Source: sprint.IDSource{Kind: sprint.SourceIDs, IDs: needs}, Fields: []string{"needs", "kind"}, Limit: sprint.AddWalkMax})
		}
	}
	if len(slice) > 0 {
		q.made = len(q.rp.Sprint)
		q.rp.Sprint = append(q.rp.Sprint, sprint.AddMadeQ(slice))
	}
	// an insertion guards its anchor and its neighbour at their places (1.5.4):
	// part 1 reads the anchor (its neighbour's place is the range's), each part
	// after it both
	var around []string
	switch {
	case first && anchor.ID != "":
		around = []string{anchor.ID}
	case !first && res.Insert:
		around = []string{res.Anchor}
		if res.Neighbour != "" {
			around = append(around, res.Neighbour)
		}
	}
	if len(around) > 0 {
		q.rp.Sprint = append(q.rp.Sprint, sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work,
			Source: sprint.IDSource{Kind: sprint.SourceIDs, IDs: around}, Fields: []string{"kind"}})
	}
	q.slice = slice
	rr := &sprintfn.ReadRequest{Epoch: epoch}
	for _, sq := range q.rp.Sprint {
		w, ref := sprintfn.EncodeSprintQ(sq)
		if ref != nil {
			return nil, q, ref
		}
		rr.Sprint = append(rr.Sprint, w)
	}
	if first {
		q.rows = len(rr.Tset)
		for _, t := range addTables {
			rr.Tset = append(rr.Tset, tset.ReadQuery{Kind: "rows", Table: t})
		}
		if anchor.ID != "" {
			q.neighbours = len(rr.Tset)
			rr.Tset = append(rr.Tset, neighbourQs(anchor.Stream, anchor.Score, r.Before != "", 1)...)
		}
		q.nextFields = sprint.NextFields(streams)
		keys := []sprintfn.KeyQ{{Kind: sprintfn.KeyNext, Names: q.nextFields}, {Kind: sprintfn.KeyClock},
			{Kind: sprintfn.KeyDropping, Streams: streams}}
		q.next, q.clock, q.dropping = len(rr.Sprint), len(rr.Sprint)+1, len(rr.Sprint)+2
		for _, kq := range keys {
			rr.Sprint = append(rr.Sprint, keyQuery(kq))
		}
	}
	return rr, q, nil
}

// addPlan is a part's step from its read (1.5.4): part 1 reserves the op and
// checks the needs cycle and the size bounds before any write; every part
// creates its slice, guarded, with the waitfor intents of its cards with
// needs and the closes of H3.
func addPlan(e *Env, r sprint.AddReq, anchor anchorRead, rd *sprintfn.ReadReply, cont string, chunk int, guess *[]string) (Part, error) {
	const verb = "add"
	_, q, err := addRead(e.Names, rd.Epoch, r, anchor, cont, chunk, *guess)
	if err != nil {
		return Part{}, err
	}
	if len(rd.Sprint) < len(q.rp.Sprint) {
		return Part{}, fmt.Errorf("add: the read answered %d sprint queries, asked at least %d", len(rd.Sprint), len(q.rp.Sprint))
	}
	ans := sprint.ReadAnswer{Epoch: sprint.Decimal(rd.Epoch), ActiveEpoch: sprint.Decimal(rd.ActiveEpoch), TimeMS: sprint.Decimal(rd.TimeMS)}
	for i, sq := range q.rp.Sprint {
		res, err := sprintfn.DecodeResult(sq.Kind, rd.Sprint[i])
		if err != nil {
			return Part{}, err
		}
		ans.Sprint = append(ans.Sprint, res.Project(sq))
	}
	s, err := sprint.LoadPartial(q.rp, ans)
	if err != nil {
		return Part{}, err
	}
	ep := s.Epoch
	var res sprint.Reservation
	k := 0
	var counter *sprintfn.CounterChange
	if cont == "" {
		res, counter, err = addReserve(e, r, anchor, rd, q, ep)
		if err != nil {
			return Part{}, err
		}
	} else {
		var c addCont
		if err := json.Unmarshal([]byte(cont), &c); err != nil {
			return Part{}, fmt.Errorf("add: the continuation: %w", err)
		}
		res, k = c.Res, c.K
	}
	res.Chunk = chunk
	plan, intents, err := sprint.AddPart(s, r, res, k)
	if err != nil {
		if errors.Is(err, sprint.ErrNoRoom) {
			return Part{}, refuseLocal(verb, sprintfn.CodeRequest, "%v: rank the neighbours apart first (rank <id> --score or --before/--after), then add", err)
		}
		return Part{}, err
	}
	size := res.PartSize(k)
	made := map[string]bool{}
	var madeIDs []string
	for _, u := range plan.Units {
		for _, c := range u.Changes {
			if c.Table == sprint.Work && c.Entry.Create != nil {
				made[c.Entry.ID] = true
				madeIDs = append(madeIDs, c.Entry.ID)
			}
		}
	}
	if cont == "" && r.Count > 0 {
		*guess = madeIDs
	}
	if err := addTaken(rd, q, r, madeIDs, cont); err != nil {
		return Part{}, err
	}
	entries, err := planEntries(plan)
	if err != nil {
		return Part{}, err
	}
	var guards []sprintfn.XGuard
	var head []tset.Entry
	for _, g := range sprint.AddGuards(plan, res, k) {
		if g.Kind == sprint.GuardZGuard {
			guards = append(guards, g.XGuard())
			continue
		}
		head = append(head, rcountEntry(g))
	}
	if res.Insert {
		// the anchor and its neighbour at their places (1.5.4): part 1 finds the
		// neighbour's place in the range that found it
		for _, id := range []string{res.Anchor, res.Neighbour} {
			if id == "" {
				continue
			}
			if cont == "" && id == res.Neighbour {
				head = append(head, nbGuard(rd.Tset[q.neighbours:q.neighbours+len(sprint.States)], id, res.Streams[0].Stream))
				continue
			}
			c := s.Work.Card(id)
			if c == nil || !c.Placed() || c.Row != res.Streams[0].Stream {
				return Part{}, refuseLocal(verb, sprintfn.CodeRequest, "%s, a neighbour of the insertion, is no longer in stream %s; the parts before stay applied", sprint.CardID(id), res.Streams[0].Stream)
			}
			head = append(head, tset.Entry{Kind: "guard", Table: sprint.Work, From: c.Row + ":" + string(c.Col), IDs: []string{id}})
		}
	}
	req := &sprintfn.Request{Meta: sprintfn.Meta{Verb: verb},
		Body: sprintfn.Body{Entries: append(head, entries...), Intents: intents, Guards: guards,
			Notes: append(sprint.AddMadeCloses(s, made), sprint.AddReachedCloses(s, r, res, k)...)}}
	if counter != nil {
		req.Sprint = &sprintfn.SprintPart{Counter: counter}
	}
	next, _ := json.Marshal(addCont{Res: res, K: k + size})
	moved := make([]ID, 0, len(madeIDs))
	for _, id := range madeIDs {
		moved = append(moved, sprint.CardID(id))
	}
	return Part{Req: req, Moved: moved, Next: string(next), Last: k+size >= res.Total}, nil
}

// addReserve is part 1's reservation from its read (1.5.4): the counter as
// read, the streams it opens (no rows as read) within the size bounds (3), the
// dropping marks (a stream being dropped is refused DROPPING, V6), the needs
// cycle (3: before any part applies), and for an insertion its bounds.
func addReserve(e *Env, r sprint.AddReq, anchor anchorRead, rd *sprintfn.ReadReply, q addQueries, ep uint64) (sprint.Reservation, *sprintfn.CounterChange, error) {
	const verb = "add"
	var res sprint.Reservation
	nr, err := sprintfn.DecodeResult(sprintfn.KeyNext, rd.Sprint[q.next])
	if err != nil {
		return res, nil, err
	}
	next, err := sprint.ParseNext(q.nextFields, nr.(sprintfn.NextResult).Fields)
	if err != nil {
		return res, nil, err
	}
	dr, err := sprintfn.DecodeResult(sprintfn.KeyDropping, rd.Sprint[q.dropping])
	if err != nil {
		return res, nil, err
	}
	if marks := dr.(sprintfn.DroppingResult).Marks; len(marks) > 0 {
		var ss []string
		for s, op := range marks {
			ss = append(ss, s+" (op "+op+")")
		}
		sort.Strings(ss)
		return res, nil, refuseLocal(verb, sprintfn.CodeDropping, "stream %s is being dropped", strings.Join(ss, ", "))
	}
	rows := map[string]map[string]bool{}
	for i, t := range addTables {
		rows[t] = map[string]bool{}
		for _, rr := range rd.Tset[q.rows+i].Rows {
			rows[t][rr.Row] = true
		}
	}
	streams := map[string]bool{}
	for s := range rows[sprint.Work] {
		streams[s] = true
	}
	for s := range rows[sprint.Merge] {
		streams[s] = true
	}
	var fresh []string
	for _, s := range sprint.AddStreams(r) {
		if !rows[sprint.Work][s] {
			fresh = append(fresh, s)
		}
	}
	if len(fresh) > 0 {
		if err := SizeBounds(len(rows[sprint.Fleet]), len(rows[sprint.Readers]), len(streams)+len(fresh)); err != nil {
			return res, nil, refuseLocal(verb, sprintfn.CodeLimit, "adding stream %s: %v", strings.Join(fresh, ", "), err)
		}
	}
	res = sprint.Reserve(next, r)
	res.Open(next, fresh)
	var cards []sprint.ChainCard
	if q.chain >= 0 {
		cr, err := sprintfn.DecodeResult(sprint.QueryNeedchain, rd.Sprint[q.chain])
		if err != nil {
			return res, nil, err
		}
		chain := cr.(sprintfn.NeedchainResult)
		for _, it := range chain.Items {
			cc := sprint.ChainCard{ID: it.ID, Needs: it.Needs, Start: it.Start}
			if rec := it.Record; rec.Exists && rec.Place != nil {
				cc.Stream, cc.Waiting = rec.Place.Row, rec.Place.Col == string(sprint.Waiting)
				cc.Score, _ = strconv.ParseFloat(rec.Score, 64)
				cc.Sentinel = fieldOf(rec, "kind") == sprint.Sentinel
			}
			cards = append(cards, cc)
		}
		makes := func(stored string) bool { return sprint.IDEpoch(stored) == ep && res.Makes(r, sprint.CardID(stored)) }
		if ep == 0 {
			makes = func(stored string) bool { return res.Makes(r, stored) }
		}
		if why := sprint.AddCycle(cards, chain.Cut, sprint.StoredID(r.Needs[0], ep), makes); why != "" {
			return res, nil, refuseLocal(verb, "XGUARD", "%s", why)
		}
	}

	cr, err := sprintfn.DecodeResult(sprintfn.KeyClock, rd.Sprint[q.clock])
	if err != nil {
		return res, nil, err
	}
	if rr, err := strconv.ParseInt(cr.(sprintfn.ClockResult).R, 10, 64); err == nil {
		res.R = rr
	}
	if res.Insert {
		nb, nbScore := nearest(rd.Tset[q.neighbours:q.neighbours+len(sprint.States)], r.Before != "", nil)
		res.Lo, res.Hi = bounds(anchor.Score, nb, nbScore, r.Before != "")
		res.Anchor, res.Neighbour = anchor.ID, nb
		if _, err := sprint.InsertScores(res.Lo, res.Hi, res.Total); err != nil {
			return res, nil, refuseLocal(verb, sprintfn.CodeRequest, "%v: rank the neighbours apart first (rank <id> --score or --before/--after), then add", err)
		}
		// a loop through the gates: the cards placed after the anchor are waited
		// for by the stream's sentinels after them (errata 3 amendment 7)
		if len(cards) > 0 {
			placed := r.IDs
			if len(placed) == 0 {
				placed = []string{fmt.Sprintf("the %d cards of this add", res.Total)}
			}
			if why := sprint.AddPlaceCycle(cards, placed, anchor.Stream, res.Lo, r.Sentinel); why != "" {
				return res, nil, refuseLocal(verb, "XGUARD", "%s", why)
			}
		}
	}
	counter := &sprintfn.CounterChange{Read: next.Read, Set: map[string]string{}}
	for f, v := range res.Counter {
		counter.Set[f] = v
	}
	if len(counter.Set) == 0 {
		counter = nil
	}
	return res, counter, nil
}

// addTaken refuses a part that would create an id the table holds, from the
// part's made read (section 3: add reads the ids' absence; VGuard "add",
// col[a] = "none", tla/SprintEvents.tla): EXISTS, local, naming the ids,
// before the step, and never retried. An id the read did not cover (part 1 of
// a --count add before its slice is known, or an id held in quarantine) is
// left to the step, whose create refuses EXISTS.
func addTaken(rd *sprintfn.ReadReply, q addQueries, r sprint.AddReq, made []string, cont string) error {
	const verb = "add"
	if q.made < 0 || q.made >= len(rd.Sprint) {
		return nil
	}
	wr, err := sprintfn.DecodeResult(sprint.QueryWaiters, rd.Sprint[q.made])
	if err != nil {
		return err
	}
	held := map[string]bool{}
	for _, it := range wr.(sprintfn.WaitersResult).Items {
		if it.Record.Exists {
			held[it.ID] = true
		}
	}
	var taken []string
	for _, id := range made {
		if held[id] {
			taken = append(taken, sprint.CardID(id))
		}
	}
	if len(taken) == 0 {
		return nil
	}
	after := ""
	if cont != "" {
		after = "; the parts before stay applied"
	}
	if r.Count > 0 {
		return refuseLocal(verb, "EXISTS", "the ids this add generates are taken: %s (an add of named ids took them)%s", strings.Join(taken, ", "), after)
	}
	return refuseLocal(verb, "EXISTS", "%s already on the table: an add creates new ids%s", strings.Join(taken, ", "), after)
}

// rcountEntry is a set guard of kind rcount as Layer 1's entry (L1 3).
func rcountEntry(g sprint.SetGuard) tset.Entry {
	en := tset.Entry{Kind: "rcount", Table: g.Table, Cells: g.Cells, ScoreMin: g.Min, ScoreMax: g.Max}
	if g.AtLeast != nil {
		v := uint64(*g.AtLeast)
		en.AtLeast = &v
	}
	if g.AtMost != nil {
		v := uint64(*g.AtMost)
		en.AtMost = &v
	}
	return en
}

// planEntries is a plan's changes as Layer 1's entries (a local stand-in for
// the conversion the tick loop owns, IT17, replaced at integration): the rows
// it adds, one rows entry a table; its creates, one entry a table and cell with
// each card's fields in Each, in the order the plan first names the cell; and
// its guards. A change of another kind is an error: the add plans none.
func planEntries(p sprint.Plan) ([]tset.Entry, error) {
	var out []tset.Entry
	rowsOf := map[string][]string{}
	var rowTables []string
	for _, ra := range p.Rows {
		if _, ok := rowsOf[ra.Table]; !ok {
			rowTables = append(rowTables, ra.Table)
		}
		rowsOf[ra.Table] = append(rowsOf[ra.Table], ra.Row)
	}
	for _, t := range rowTables {
		out = append(out, tset.Entry{Kind: "rows", Table: t, Add: rowsOf[t]})
	}
	at := map[string]int{}
	for _, u := range p.Units {
		for _, c := range u.Changes {
			en := c.Entry
			switch {
			case en.Create != nil:
				cell := en.Create.Row + ":" + string(en.Create.Col)
				key := c.Table + "\x00" + cell
				i, ok := at[key]
				if !ok {
					i = len(out)
					at[key] = i
					out = append(out, tset.Entry{Kind: "create", Table: c.Table, To: cell})
				}
				e := &out[i]
				e.IDs = append(e.IDs, en.ID)
				e.About = append(e.About, en.ID)
				e.Scores = append(e.Scores, fmtF(en.Create.Score))
				set := map[string]string{}
				for f, v := range en.Set {
					set[f] = v
				}
				e.Each = append(e.Each, set)
			case en.Expect != nil && en.Expect.Place != nil && en.Move == nil && !en.Remove && len(en.Set) == 0 && len(en.Unset) == 0:
				g := tset.Entry{Kind: "guard", Table: c.Table, From: en.Expect.Place.Row + ":" + string(en.Expect.Place.Col), IDs: []string{en.ID}}
				if en.Expect.Revision != "" {
					g.Revs = []tset.Decimal{tset.Decimal(en.Expect.Revision)}
				}
				out = append(out, g)
			default:
				return nil, fmt.Errorf("add: a change of %s that the add does not plan", en.ID)
			}
		}
	}
	return out, nil
}

// ---- release

// Release lands sentinels (section 3's release row): each G waiting -> landed,
// its "sentinel reached" closed, KNOW "sentinel landed", in one step. --reason
// is required: what the coordinator looked at and found. A sentinel is released
// only when no open card of its stream sorts before it (the rcount of its five
// open cells below σ_G, at most 0, from the read and guarded at apply); X
// refuses the verb from anyone but the coordinator (NOTCOORD). Three round
// trips: the sentinels, then their streams' fronts with the step's read, then
// the step.
func Release(ctx context.Context, e *Env, r sprint.ReleaseReq) (Result, error) {
	const verb = "release"
	res := Result{Verb: verb, Op: r.Op}
	if strings.TrimSpace(r.Reason) == "" {
		return res, refuseLocal(verb, sprintfn.CodeRequest, "release needs --reason: what you looked at, and what you found")
	}
	if len(r.Reason) > 64<<10 {
		return res, refuseLocal(verb, sprintfn.CodeRequest, "--reason is %d bytes, over a field's 64 KiB", len(r.Reason))
	}
	if len(r.IDs) == 0 {
		return res, refuseLocal(verb, sprintfn.CodeRequest, "release names no sentinel")
	}
	ids := append([]string(nil), r.IDs...)
	sort.Strings(ids)
	for i, id := range ids {
		if !sprint.ValidID(id) || (i > 0 && ids[i-1] == id) {
			return res, refuseLocal(verb, sprintfn.CodeRequest, "%q is not a card's id, or is named twice", id)
		}
	}
	recs, err := readRecords(ctx, e, &res, ids, []string{"kind"})
	if err != nil {
		return res, err
	}
	var streams []string
	seen := map[string]bool{}
	for i, rec := range recs {
		row, col := placeOf(rec)
		switch {
		case !rec.Exists || row == "":
			return res, refuseLocal(verb, "MISSING", "%s is not a card on the table", ids[i])
		case fieldOf(rec, "kind") != sprint.Sentinel:
			return res, refuseLocal(verb, sprintfn.CodeRequest, "%s is not a sentinel", ids[i])
		case col != string(sprint.Waiting):
			return res, refuseLocal(verb, sprintfn.CodeRequest, "%s is %s; a sentinel is released from waiting", ids[i], col)
		}
		if !seen[row] {
			seen[row] = true
			streams = append(streams, row)
		}
	}
	pre := res.Trips
	args := map[string]any{"ids": IDsDigest(ids), "reason": IDsDigest([]string{r.Reason})}
	out, err := e.Do(ctx, Planned{Verb: verb, Op: r.Op, Args: args,
		Read: func(epoch tset.Decimal) *sprintfn.ReadRequest {
			ep, _ := epochOf(epoch)
			rr := &sprintfn.ReadRequest{Epoch: epoch}
			q, _ := sprintfn.EncodeSprintQ(sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work,
				Source: sprint.IDSource{Kind: sprint.SourceIDs, IDs: storedAll(ids, ep)}, Fields: []string{"kind"}})
			rr.Sprint = append(rr.Sprint, q)
			for _, s := range streams {
				f, _ := sprintfn.EncodeSprintQ(sprint.SprintQ{Kind: sprint.QueryFront, Stream: s, Fields: []string{"kind"}})
				rr.Sprint = append(rr.Sprint, f)
			}
			return rr
		},
		Plan: func(rd *sprintfn.ReadReply) (Part, error) {
			req, err := releasePlan(rd, ids, streams, r.Reason)
			if err != nil {
				return Part{}, err
			}
			return Part{Req: req, Moved: ids}, nil
		}})
	out.Trips += pre
	if err == nil && !out.Replay {
		out.Said = fmt.Sprintf("release: %s landed", strings.Join(ids, ", "))
	}
	return out, err
}

// releasePlan is release's step from its read: each G placed in waiting with
// its revision, the first sentinel of its stream with nothing open before it,
// moved to landed; the rcount of its stream's five open cells below it at most
// 0; the close of "sentinel reached" and the KNOW, on G.
func releasePlan(rd *sprintfn.ReadReply, ids, streams []string, reason string) (*sprintfn.Request, error) {
	const verb = "release"
	rel, err := decodeRelated(rd.Sprint[0])
	if err != nil {
		return nil, err
	}
	front := map[string]sprintfn.FrontResult{}
	for i, s := range streams {
		fr, err := sprintfn.DecodeResult(sprint.QueryFront, rd.Sprint[1+i])
		if err != nil {
			return nil, err
		}
		front[s] = fr.(sprintfn.FrontResult)
	}
	byID := map[string]sprintfn.Record{}
	for _, it := range rel.Items {
		byID[it.ID] = it.Record
	}
	var entries, guards []tset.Entry
	var notes []sprintfn.NoteReq
	byStream := map[string]int{}
	for _, it := range rel.Items {
		rec := it.Record
		row, col := placeOf(rec)
		if !rec.Exists || row == "" || col != string(sprint.Waiting) || fieldOf(rec, "kind") != sprint.Sentinel {
			return nil, refuseLocal(verb, sprintfn.CodeRequest, "%s is no longer a sentinel in waiting", sprint.CardID(it.ID))
		}
		byStream[row]++
		f := front[row]
		if f.G != it.ID || byStream[row] > 1 {
			return nil, refuseLocal(verb, sprintfn.CodeRequest, "%s is not the first sentinel of stream %s (%s is before it); release that one first", sprint.CardID(it.ID), row, sprint.CardID(f.G))
		}
		if f.NBefore > 0 {
			return nil, refuseLocal(verb, sprintfn.CodeRequest, "%d open cards of stream %s sort before %s; a sentinel is released once nothing before it is open", f.NBefore, row, sprint.CardID(it.ID))
		}
		guards = append(guards, rcountEntry(sprint.SetGuard{Kind: sprint.GuardRCount, Table: sprint.Work, Cells: sprint.OpenCells(row),
			Min: "-inf", Max: "(" + rec.Score, AtMost: intp(0)}))
		entries = append(entries, tset.Entry{Kind: "move", Table: sprint.Work, From: row + ":" + string(sprint.Waiting),
			To: row + ":" + string(sprint.Landed), IDs: []string{it.ID}, Revs: []tset.Decimal{rec.Revision}, About: []string{it.ID}})
		notes = append(notes,
			sprintfn.NoteReq{Op: "close", Type: sprint.NSentinelReached, Cause: sprint.ReachedCause, Subjects: []string{it.ID}, Text: "released: " + reason},
			sprintfn.NoteReq{Op: "know", Type: sprint.NSentinelLanded, Subjects: []string{it.ID}, Text: sprint.CardID(it.ID) + " landed: " + reason})
	}
	if len(rel.Items) != len(ids) {
		return nil, refuseLocal(verb, "MISSING", "the read found %d of the %d sentinels", len(rel.Items), len(ids))
	}
	return &sprintfn.Request{Meta: sprintfn.Meta{Verb: verb}, Body: sprintfn.Body{Entries: append(guards, entries...), Notes: notes}}, nil
}

func intp(n int) *int { return &n }

// ---- rank

// Rank changes the scores of cards (section 3's rank row; U2): with --score x,
// the cards take x, x + 1, ... in the order of their scores, and x at or above
// the counter raises it past them (COUNTER, guarded as read), while an integer
// x below the counter is refused REQUEST, since only an add places an integer
// there; with --before or --after a card, they take scores between it and its
// neighbour that are not integers, keeping their order. Each card is guarded
// at its place and revision, every copy of it (its work cards, read cards and
// merge card) is rescored with it, and a landed card is refused. Every rank
// closes "sentinel reached" in its own step when it places an open card before
// the first sentinel of its stream (H13 rankclose; VEff "rank" with
// ReachedPassed, tla/SprintEvents.tla), so each reads front(s) of its streams.
// One step, three round trips: the anchor (in line) or the cards (--score, whose
// streams the front reads name) are read first.
func Rank(ctx context.Context, e *Env, r sprint.RankReq) (Result, error) {
	const verb = "rank"
	res := Result{Verb: verb, Op: r.Op}
	modes := 0
	for _, on := range []bool{r.Score != nil, r.Before != "", r.After != ""} {
		if on {
			modes++
		}
	}
	switch {
	case len(r.IDs) == 0:
		return res, refuseLocal(verb, sprintfn.CodeRequest, "rank names no card")
	case modes != 1 || r.First:
		return res, refuseLocal(verb, sprintfn.CodeRequest, "rank takes one of --score x, --before <id> and --after <id>")
	case r.Score != nil && (math.IsNaN(*r.Score) || math.IsInf(*r.Score, 0)):
		return res, refuseLocal(verb, sprintfn.CodeRequest, "--score is a number")
	}
	ids := append([]string(nil), r.IDs...)
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	for i, id := range sorted {
		if !sprint.ValidID(id) || (i > 0 && sorted[i-1] == id) {
			return res, refuseLocal(verb, sprintfn.CodeRequest, "%q is not a card's id, or is named twice", id)
		}
	}
	var anchor anchorRead
	if a := r.Before + r.After; a != "" {
		for _, id := range ids {
			if id == a {
				return res, refuseLocal(verb, sprintfn.CodeRequest, "%s is both ranked and the anchor", a)
			}
		}
		var err error
		if anchor, err = anchorOf(ctx, e, &res, verb, a, ""); err != nil {
			return res, err
		}
	}
	var sp rankScorePre
	if r.Score != nil {
		recs, err := readRecords(ctx, e, &res, ids, []string{"kind"})
		if err != nil {
			return res, err
		}
		sp = rankScorePreOf(recs, *r.Score)
	}
	pre := res.Trips
	args := map[string]any{"ids": IDsDigest(sorted)}
	if r.Score != nil {
		args["score"] = fmtF(*r.Score)
	}
	if r.Before != "" {
		args["before"] = r.Before
	}
	if r.After != "" {
		args["after"] = r.After
	}
	out, err := e.Do(ctx, Planned{Verb: verb, Op: r.Op, Args: args,
		Read: func(epoch tset.Decimal) *sprintfn.ReadRequest {
			ep, _ := epochOf(epoch)
			rr := &sprintfn.ReadRequest{Epoch: epoch}
			q, _ := sprintfn.EncodeSprintQ(sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work,
				Source: sprint.IDSource{Kind: sprint.SourceIDs, IDs: storedAll(ids, ep)}, Fields: []string{"kind", sprint.PrimaryField},
				Follow: []string{sprint.FollowWork, sprint.FollowWithdrawn, sprint.FollowRCards, sprint.FollowMerge}})
			rr.Sprint = append(rr.Sprint, q, keyQuery(sprintfn.KeyQ{Kind: sprintfn.KeyNext, Names: []string{sprint.NextScore}}))
			if anchor.ID != "" {
				a, _ := sprintfn.EncodeSprintQ(sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work,
					Source: sprint.IDSource{Kind: sprint.SourceIDs, IDs: []string{anchor.ID}}, Fields: []string{"kind"}})
				f, _ := sprintfn.EncodeSprintQ(sprint.SprintQ{Kind: sprint.QueryFront, Stream: anchor.Stream, Fields: []string{"kind"}})
				rr.Sprint = append(rr.Sprint, a, f)
				rr.Tset = neighbourQs(anchor.Stream, anchor.Score, r.Before != "", len(ids)+1)
			}
			for _, s := range sp.streams {
				f, _ := sprintfn.EncodeSprintQ(sprint.SprintQ{Kind: sprint.QueryFront, Stream: s, Fields: []string{"kind"}})
				rr.Sprint = append(rr.Sprint, f)
			}
			rr.Tset = append(rr.Tset, sp.reads()...)
			return rr
		},
		Plan: func(rd *sprintfn.ReadReply) (Part, error) {
			req, err := rankPlan(rd, r, ids, anchor, sp)
			if err != nil {
				return Part{}, err
			}
			return Part{Req: req, Moved: ids}, nil
		}})
	out.Trips += pre
	if err == nil && !out.Replay {
		out.Said = "rank: " + strings.Join(ids, ", ") + " rescored"
	}
	return out, err
}

// rankCard is a card rank moves, with its copies.
type rankCard struct {
	rec    sprintfn.Record
	copies []rankCopy
}

// rankCopy is a copy of a card in another table: its record and table.
type rankCopy struct {
	table string
	rec   sprintfn.Record
}

// rankProbe is a count rank --score reads beside its step: the cards of a
// stream at a card's score (U1), or the open cards of a stream before a
// sentinel's score (H13), at the score the first read's order gives the card.
type rankProbe struct {
	id, stream string
	score      float64
}

// rankScorePre is what rank --score learns from its first read of the cards:
// their streams, whose fronts the step's read names, and the counts it reads,
// the U1 ones first (Tset in order), then the H13 ones.
type rankScorePre struct {
	streams []string
	taken   []rankProbe
	before  []rankProbe
}

// rankScorePreOf is rank --score's reads from the cards as first read: each
// placed card at x, x + 1, ... in the order of its score, then id.
func rankScorePreOf(recs []sprintfn.Record, x float64) rankScorePre {
	var sp rankScorePre
	var placed []sprintfn.Record
	seen := map[string]bool{}
	for _, rec := range recs {
		row, _ := placeOf(rec)
		if !rec.Exists || row == "" {
			continue
		}
		placed = append(placed, rec)
		if !seen[row] {
			seen[row] = true
			sp.streams = append(sp.streams, row)
		}
	}
	sort.SliceStable(placed, func(i, j int) bool {
		a, b := scoreOf(placed[i]), scoreOf(placed[j])
		if a != b {
			return a < b
		}
		return placed[i].ID < placed[j].ID
	})
	for i, rec := range placed {
		row, _ := placeOf(rec)
		p := rankProbe{id: rec.ID, stream: row, score: x + float64(i)}
		sp.taken = append(sp.taken, p)
		if fieldOf(rec, "kind") == sprint.Sentinel {
			sp.before = append(sp.before, p)
		}
	}
	return sp
}

// reads are the counts as Layer 1's reads: the rcount of a stream's six cells
// at [x, x], and of its five open cells in [-inf, x).
func (sp rankScorePre) reads() []tset.ReadQuery {
	var out []tset.ReadQuery
	for _, p := range sp.taken {
		out = append(out, tset.ReadQuery{Kind: "rcount", Table: sprint.Work, Cells: allCells(p.stream), Min: fmtF(p.score), Max: fmtF(p.score)})
	}
	for _, p := range sp.before {
		out = append(out, tset.ReadQuery{Kind: "rcount", Table: sprint.Work, Cells: sprint.OpenCells(p.stream), Min: "-inf", Max: "(" + fmtF(p.score)})
	}
	return out
}

// count is the count read for the card id at score x (from probes starting at
// Tset[at]); false when the read counted another score (the order moved since
// the first read).
func (sp rankScorePre) count(ans []tset.ReadAnswer, probes []rankProbe, at int, id string, x float64) (int, bool) {
	for i, p := range probes {
		if p.id == id && p.score == x && at+i < len(ans) {
			return int(ans[at+i].Sum), true
		}
	}
	return 0, false
}

// rankPlan is rank's step from its read (U2, 1.3.1).
func rankPlan(rd *sprintfn.ReadReply, r sprint.RankReq, ids []string, anchor anchorRead, sp rankScorePre) (*sprintfn.Request, error) {
	const verb = "rank"
	rel, err := decodeRelated(rd.Sprint[0])
	if err != nil {
		return nil, err
	}
	nr, err := sprintfn.DecodeResult(sprintfn.KeyNext, rd.Sprint[1])
	if err != nil {
		return nil, err
	}
	counterText := nr.(sprintfn.NextResult).Fields[sprint.NextScore]
	counter, _ := strconv.ParseUint(counterText, 10, 64)
	ep, err := epochOf(rd.Epoch)
	if err != nil {
		return nil, err
	}
	byID := map[string]sprintfn.RelatedItem{}
	for _, it := range rel.Items {
		byID[it.ID] = it
	}
	var cards []rankCard
	skip := map[string]bool{}
	for _, id := range ids {
		it, ok := byID[sprint.StoredID(id, ep)]
		row, col := placeOf(it.Record)
		switch {
		case !ok || !it.Record.Exists || row == "":
			return nil, refuseLocal(verb, "MISSING", "%s is not a card on the table", id)
		case col == string(sprint.Landed):
			return nil, refuseLocal(verb, sprintfn.CodeRequest, "%s has landed; landed is final", id)
		case anchor.ID != "" && row != anchor.Stream:
			return nil, refuseLocal(verb, sprintfn.CodeRequest, "%s is a card of stream %s, and %s of %s: a card goes in line in its own stream", id, row, sprint.CardID(anchor.ID), anchor.Stream)
		}
		c := rankCard{rec: it.Record}
		if f := it.Follows; f != nil {
			for _, w := range f.Work {
				c.copies = append(c.copies, rankCopy{sprint.Fleet, w})
			}
			for _, w := range f.Withdrawn {
				c.copies = append(c.copies, rankCopy{sprint.Fleet, w})
			}
			for _, rc := range f.RCards {
				c.copies = append(c.copies, rankCopy{sprint.Readers, rc})
			}
			for _, m := range f.Merge {
				c.copies = append(c.copies, rankCopy{sprint.Merge, m})
			}
		}
		cards = append(cards, c)
		skip[it.ID] = true
	}
	// The cards keep their order: by score, then id.
	sort.SliceStable(cards, func(i, j int) bool {
		a, b := scoreOf(cards[i].rec), scoreOf(cards[j].rec)
		if a != b {
			return a < b
		}
		return cards[i].rec.ID < cards[j].rec.ID
	})
	n := len(cards)
	scores := make([]float64, n)
	var head []tset.Entry
	var notes []sprintfn.NoteReq
	var change *sprintfn.CounterChange
	switch {
	case r.Score != nil:
		x := *r.Score
		whole := x == math.Trunc(x)
		switch {
		case x < float64(counter) && whole:
			return nil, refuseLocal(verb, sprintfn.CodeRequest, "--score %s is an integer below the counter %d: integers below it are the adds' (U2); choose a score that is not an integer, or one at or above %d", fmtF(x), counter, counter)
		case x < float64(counter) && n > 1:
			return nil, refuseLocal(verb, sprintfn.CodeRequest, "several cards below the counter %d go in line with --before or --after; --score below it takes one card", counter)
		case x >= float64(counter):
			// at or above the counter: raise it past the last score (U2)
			top := uint64(math.Floor(x+float64(n-1))) + 1
			change = &sprintfn.CounterChange{Read: map[string]string{sprint.NextScore: counterText},
				Set: map[string]string{sprint.NextScore: strconv.FormatUint(top, 10)}}
		}
		for i := range scores {
			scores[i] = x + float64(i)
		}
		// U1: no other card of the stream at a score taken below the counter,
		// refused from the read (a card ranked away from it does not count) and
		// guarded at apply
		for i, c := range cards {
			if scores[i] >= float64(counter) {
				continue
			}
			row, _ := placeOf(c.rec)
			if n, ok := sp.count(rd.Tset, sp.taken, 0, c.rec.ID, scores[i]); ok {
				for _, o := range cards {
					if orow, _ := placeOf(o.rec); orow == row && scoreOf(o.rec) == scores[i] {
						n--
					}
				}
				if n > 0 {
					return nil, refuseLocal(verb, sprintfn.CodeRequest, "--score %s is taken in stream %s (U1: one card a score); choose another", fmtF(scores[i]), row)
				}
			}
			head = append(head, rcountEntry(sprint.SetGuard{Kind: sprint.GuardRCount, Table: sprint.Work, Cells: allCells(row),
				Min: fmtF(scores[i]), Max: fmtF(scores[i]), AtMost: intp(0)}))
		}
		fronts := map[string]sprintfn.FrontResult{}
		for i, s := range sp.streams {
			if 2+i >= len(rd.Sprint) {
				return nil, fmt.Errorf("rank: the read has no front of stream %s", s)
			}
			fr, err := sprintfn.DecodeResult(sprint.QueryFront, rd.Sprint[2+i])
			if err != nil {
				return nil, err
			}
			fronts[s] = fr.(sprintfn.FrontResult)
		}
		notes = rankScoreClosesReached(fronts, sp, rd.Tset, cards, scores)
	default:
		if len(rd.Sprint) < 3 {
			return nil, fmt.Errorf("rank: the read has no anchor")
		}
		ar, err := decodeRelated(rd.Sprint[2])
		if err != nil {
			return nil, err
		}
		if len(ar.Items) != 1 || !ar.Items[0].Record.Exists || scoreOf(ar.Items[0].Record) != anchor.Score {
			return nil, refuseLocal(verb, "PLACE", "%s moved since it was read; run the rank again", sprint.CardID(anchor.ID))
		}
		arow, acol := placeOf(ar.Items[0].Record)
		if arow != anchor.Stream || acol == string(sprint.Landed) {
			return nil, refuseLocal(verb, "PLACE", "%s moved since it was read; run the rank again", sprint.CardID(anchor.ID))
		}
		before := r.Before != ""
		nb, nbScore := nearest(rd.Tset, before, skip)
		lo, hi := bounds(anchor.Score, nb, nbScore, before)
		sc, err := sprint.InsertScores(lo, hi, n)
		if err != nil {
			other := "the next integer"
			if nb != "" {
				other = sprint.CardID(nb)
			}
			return nil, refuseLocal(verb, sprintfn.CodeRequest, "%v: rank %s further from %s first", err, other, sprint.CardID(anchor.ID))
		}
		copy(scores, sc)
		// the anchor and its neighbour at their places, and no other card
		// between them (the cards ranked that lie between now count)
		head = append(head, tset.Entry{Kind: "guard", Table: sprint.Work, From: arow + ":" + acol, IDs: []string{anchor.ID}})
		if nb != "" {
			head = append(head, nbGuard(rd.Tset, nb, anchor.Stream))
		}
		between := 0
		for _, c := range cards {
			if v := scoreOf(c.rec); v > lo && v < hi {
				between++
			}
		}
		head = append(head, rcountEntry(sprint.SetGuard{Kind: sprint.GuardRCount, Table: sprint.Work, Cells: allCells(anchor.Stream),
			Min: "(" + fmtF(lo), Max: "(" + fmtF(hi), AtLeast: intp(between), AtMost: intp(between)}))
		fr, err := sprintfn.DecodeResult(sprint.QueryFront, rd.Sprint[3])
		if err != nil {
			return nil, err
		}
		notes = rankClosesReached(fr.(sprintfn.FrontResult), cards, scores, before, nb != "" && nbOpen(rd.Tset, nb))
	}
	var entries []tset.Entry
	for i, c := range cards {
		row, col := placeOf(c.rec)
		sc := fmtF(scores[i])
		if scoreOf(c.rec) == scores[i] {
			return nil, refuseLocal(verb, sprintfn.CodeRequest, "%s is at score %s already", sprint.CardID(c.rec.ID), sc)
		}
		cell := row + ":" + col
		entries = append(entries, tset.Entry{Kind: "move", Table: sprint.Work, From: cell, To: cell, IDs: []string{c.rec.ID},
			Scores: []string{sc}, Revs: []tset.Decimal{c.rec.Revision}, About: []string{c.rec.ID}})
		for _, cp := range c.copies {
			crow, ccol := placeOf(cp.rec)
			if crow == "" || !cp.rec.Exists {
				continue
			}
			ccell := crow + ":" + ccol
			entries = append(entries, tset.Entry{Kind: "move", Table: cp.table, From: ccell, To: ccell, IDs: []string{cp.rec.ID},
				Scores: []string{sc}, About: []string{cp.rec.ID}})
		}
	}
	req := &sprintfn.Request{Meta: sprintfn.Meta{Verb: verb}, Body: sprintfn.Body{Entries: append(head, entries...), Notes: notes}}
	if change != nil {
		req.Sprint = &sprintfn.SprintPart{Counter: change}
	}
	return req, nil
}

// rankClosesReached is H13's rankclose in the model's form (errata 3,
// amendment 2; SprintEvents.tla ReachedPassed), for a rank in line, whose read
// has front(s) of the stream: a card ranked before the first sentinel closes
// its "sentinel reached"; the first sentinel ranked after an open card closes
// its own (--after: the anchor is open; --before: when the neighbour below is).
// A close of a judgment that is not open changes nothing (rank --score:
// rankScoreClosesReached).
func rankClosesReached(f sprintfn.FrontResult, cards []rankCard, scores []float64, before, lowerOpen bool) []sprintfn.NoteReq {
	if f.G == "" {
		return nil
	}
	sigma, _ := strconv.ParseFloat(f.Sigma, 64)
	passed := false
	for i, c := range cards {
		switch {
		case c.rec.ID != f.G && scores[i] < sigma:
			passed = true
		case c.rec.ID == f.G && (!before || lowerOpen):
			passed = true
		}
	}
	if !passed {
		return nil
	}
	return []sprintfn.NoteReq{{Op: "close", Type: sprint.NSentinelReached, Cause: sprint.ReachedCause, Subjects: []string{f.G},
		Text: "a card was ranked before it"}}
}

// rankScoreClosesReached is H13's rankclose for rank --score (errata 3,
// amendment 2; VEff "rank" with ReachedPassed, tla/SprintEvents.tla): for each
// stream with a first sentinel G, the step closes G's "sentinel reached" when
// after it an open card sorts before G, that is when a card other than G is
// ranked below G's score (as ranked), or when G is ranked to x and an open card
// other than G lies before x (the read's count of the open cells before x, less
// the ranked cards that were before it). A close of a judgment that is not open
// changes nothing. A count read at a score the order no longer gives closes
// nothing on that disjunct: R3's unreach stays the fallback (2.1).
func rankScoreClosesReached(fronts map[string]sprintfn.FrontResult, sp rankScorePre, ans []tset.ReadAnswer, cards []rankCard, scores []float64) []sprintfn.NoteReq {
	var out []sprintfn.NoteReq
	for _, st := range sp.streams {
		f := fronts[st]
		if f.G == "" {
			continue
		}
		gNew, err := strconv.ParseFloat(f.Sigma, 64)
		if err != nil {
			continue
		}
		ranked := false
		for i, c := range cards {
			if c.rec.ID == f.G {
				gNew, ranked = scores[i], true
			}
		}
		passed := false
		for i, c := range cards {
			if row, _ := placeOf(c.rec); row == st && c.rec.ID != f.G && scores[i] < gNew {
				passed = true
			}
		}
		if !passed && ranked {
			if n, ok := sp.count(ans, sp.before, len(sp.taken), f.G, gNew); ok {
				for _, c := range cards {
					if row, _ := placeOf(c.rec); row == st && scoreOf(c.rec) < gNew {
						n-- // it moves, and no ranked card lands before gNew (passed is false)
					}
				}
				passed = n > 0
			}
		}
		if passed {
			out = append(out, sprintfn.NoteReq{Op: "close", Type: sprint.NSentinelReached, Cause: sprint.ReachedCause,
				Subjects: []string{f.G}, Text: "a card was ranked before it"})
		}
	}
	return out
}

// nbOpen says the neighbour a range found is in an open cell (not landed).
func nbOpen(ans []tset.ReadAnswer, nb string) bool {
	for i, a := range ans {
		for _, m := range a.IDs {
			if m == nb && i < len(sprint.States) {
				return sprint.States[i] != sprint.Landed
			}
		}
	}
	return false
}

// nbGuard is the neighbour guarded at the cell a range found it in.
func nbGuard(ans []tset.ReadAnswer, nb, stream string) tset.Entry {
	for i, a := range ans {
		for _, m := range a.IDs {
			if m == nb && i < len(sprint.States) {
				return tset.Entry{Kind: "guard", Table: sprint.Work, From: stream + ":" + string(sprint.States[i]), IDs: []string{nb}}
			}
		}
	}
	return tset.Entry{Kind: "guard", Table: sprint.Work, IDs: []string{nb}}
}

// allCells are the six cells of a stream in the work table.
func allCells(stream string) []string {
	out := make([]string, 0, len(sprint.States))
	for _, st := range sprint.States {
		out = append(out, stream+":"+string(st))
	}
	return out
}

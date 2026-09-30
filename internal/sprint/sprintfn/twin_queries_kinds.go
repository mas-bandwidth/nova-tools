package sprintfn

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// ---- the composite queries

// evalComposite answers one composite query.
func (e *qeval) evalComposite(q sprint.SprintQ) (QueryResult, *Refusal) {
	switch q.Kind {
	case sprint.QueryRelated:
		r, ref := e.related(q)
		return r, ref
	case sprint.QueryFront:
		r, ref := e.front(q)
		return r, ref
	case sprint.QueryWaiters:
		r, ref := e.waiters(q)
		return r, ref
	case sprint.QueryStreams:
		r, ref := e.streams(q)
		return r, ref
	case sprint.QueryFleet, sprint.QueryReaders:
		r, ref := e.listing(q)
		return r, ref
	case sprint.QueryNeedchain:
		r, ref := e.needchain(q)
		return r, ref
	case sprint.QueryJnote:
		r, ref := e.jnote(q)
		return r, ref
	}
	return nil, requestRefusal()
}

// related reads each id's record and what the follows reach from it.
func (e *qeval) related(q sprint.SprintQ) (RelatedResult, *Refusal) {
	res := RelatedResult{Kind: q.Kind, IDs: []string{}, LeftOut: []string{}, Items: []RelatedItem{}}
	if ref := e.ensureTable(q.Table); ref != nil {
		return res, ref
	}
	ids, named, ref := e.sourceIDs(q.Source, q.Table)
	if ref != nil {
		return res, ref
	}
	kept, ref := e.leave(ids, &res.LeftOut)
	if ref != nil {
		return res, ref
	}
	recs, ref := e.records(q.Table, kept, fieldUnion(q.Fields, q.Follow))
	if ref != nil {
		return res, ref
	}
	if named {
		if ref := e.present(q.Table, recs); ref != nil {
			return res, ref
		}
	}
	var existing []Record
	var at []int
	for i, r := range recs {
		if r.Exists {
			existing, at = append(existing, r), append(at, i)
		}
	}
	fs, ref := e.follows(q.Table, existing, q.Follow, q.Fields, &res.LeftOut)
	if ref != nil {
		return res, ref
	}
	byRec := map[int]*Follows{}
	for j, i := range at {
		byRec[i] = fs[j]
	}
	res.IDs = kept
	for i, r := range recs {
		it := RelatedItem{ID: r.ID, Record: project(r, q.Fields)}
		if len(q.Follow) > 0 {
			it.Follows = byRec[i]
			if it.Follows == nil {
				it.Follows = &Follows{}
			}
			it.Follows.project(q.Fields)
		}
		res.Items = append(res.Items, it)
	}
	return res, nil
}

// project leaves every record of the follows with the projection's fields only.
func (f *Follows) project(fields []string) {
	for _, list := range []*[]Record{&f.Work, &f.Withdrawn, &f.RCards, &f.Merge, &f.Control, &f.Member} {
		*list = projectAll(*list, fields)
	}
	for i := range f.Needs {
		f.Needs[i].Record = project(f.Needs[i].Record, fields)
	}
}

// The five cells of a stream's table whose cards are open (1.0's `front`:
// "the count of s's five open cells below sigma"): every cell of the work
// table but the last, landed.
var openCells = []string{sprint.Waiting, sprint.Ready, sprint.Working, sprint.Review, sprint.Merging}

// front reads the first sentinel of a stream, the count of its open cards
// before it, and the heads of its indexes around it, from the one snapshot.
func (e *qeval) front(q sprint.SprintQ) (FrontResult, *Refusal) {
	res := FrontResult{Kind: q.Kind, Stream: q.Stream, Heads: []HeadResult{}, LeftOut: []string{}}
	if ref := e.ensureTable(sprint.Work); ref != nil {
		return res, ref
	}
	gs, scores, _, ref := e.rangeHead(e.key(sprint.IndexSent+":"+q.Stream), "-inf", "+inf", 1)
	if ref != nil {
		return res, ref
	}
	hasG := len(gs) == 1
	if hasG {
		res.G, res.Sigma = gs[0], scores[0]
		bad, ref := e.quarantined(gs)
		if ref != nil {
			return res, ref
		}
		res.GQuarantined = bad[res.G]
		n, ref := e.openBefore(q.Stream, res.Sigma)
		if ref != nil {
			return res, ref
		}
		res.NBefore = n
		if !res.GQuarantined {
			rs, ref := e.records(sprint.Work, []string{res.G}, fieldUnion(q.Fields, nil))
			if ref != nil {
				return res, ref
			}
			if ref := e.present(sprint.Work, rs); ref != nil {
				return res, ref
			}
			r := project(rs[0], q.Fields)
			res.GRecord = &r
		}
	}
	for _, h := range q.Heads {
		hr := HeadResult{Index: h.Index, IDs: []string{}, Scores: []string{}, Items: []RelatedItem{}}
		var name, min, max string
		skip := false
		switch h.Index {
		case sprint.HeadEligBelow, sprint.HeadFreshBelow:
			name, min, max = sprint.IndexElig, "-inf", "+inf"
			if h.Index == sprint.HeadFreshBelow {
				name = sprint.IndexFresh
			}
			if hasG {
				max = "(" + res.Sigma
			}
		case sprint.HeadFreshAbove:
			name, min, max = sprint.IndexFresh, "+inf", "+inf"
			if hasG {
				min = "(" + res.Sigma
			} else {
				skip = true // no sentinel: nothing lies above it
			}
		case sprint.HeadAgain:
			name, min, max = sprint.IndexAgain, "-inf", "+inf"
		}
		if !skip {
			ids, scs, more, ref := e.rangeHead(e.key(name+":"+q.Stream), min, max, h.Limit)
			if ref != nil {
				return res, ref
			}
			kept, ref := e.leave(ids, &res.LeftOut)
			if ref != nil {
				return res, ref
			}
			keptScores := make([]string, 0, len(kept))
			at := map[string]string{}
			for i, id := range ids {
				at[id] = scs[i]
			}
			for _, id := range kept {
				keptScores = append(keptScores, at[id])
			}
			hr.IDs, hr.Scores, hr.HasMore = kept, keptScores, more
			recs, ref := e.records(sprint.Work, kept, fieldUnion(q.Fields, h.Follow))
			if ref != nil {
				return res, ref
			}
			if ref := e.present(sprint.Work, recs); ref != nil {
				return res, ref
			}
			fs, ref := e.follows(sprint.Work, recs, h.Follow, q.Fields, &res.LeftOut)
			if ref != nil {
				return res, ref
			}
			for i, r := range recs {
				it := RelatedItem{ID: r.ID, Record: project(r, q.Fields)}
				if len(h.Follow) > 0 {
					it.Follows = fs[i]
					it.Follows.project(q.Fields)
				}
				hr.Items = append(hr.Items, it)
			}
		}
		res.Heads = append(res.Heads, hr)
	}
	return res, nil
}

// openBefore is the count of the stream's five open cells below sigma: one
// probe for the row (a row the table does not have is NOROW, as Layer 1's own
// count refuses it) and one ZCOUNT a cell, from the Mem's rcount.
func (e *qeval) openBefore(stream, sigma string) (int, *Refusal) {
	cells := make([]string, len(openCells))
	for i, c := range openCells {
		cells[i] = stream + ":" + c
	}
	for i := 0; i < 1+len(cells); i++ {
		if ref := e.probe(); ref != nil {
			return 0, ref
		}
	}
	ans, ref := e.memRead([]tset.ReadQuery{{Kind: "rcount", Table: sprint.Work, Cells: cells, Min: "-inf", Max: "(" + sigma}})
	if ref != nil {
		return 0, named(ref, sprint.Work, cells[0])
	}
	return int(ans[0].Sum), nil
}

// waiters reads, for each id n of the source, its record (whose place is
// absent when it has none), its score in {p}missing@e, and the head of wait:n.
func (e *qeval) waiters(q sprint.SprintQ) (WaitersResult, *Refusal) {
	res := WaitersResult{Kind: q.Kind, IDs: []string{}, LeftOut: []string{}, Items: []WaiterItem{}}
	if ref := e.ensureTable(sprint.Work); ref != nil {
		return res, ref
	}
	ids, _, ref := e.sourceIDs(q.Source, sprint.Work)
	if ref != nil {
		return res, ref
	}
	kept, ref := e.leave(ids, &res.LeftOut)
	if ref != nil {
		return res, ref
	}
	res.IDs = kept
	recs, ref := e.records(sprint.Work, kept, q.Fields)
	if ref != nil {
		return res, ref
	}
	missing := make([]*string, 0, len(kept))
	for from := 0; from < len(kept); from += probeChunk {
		got, ref := e.zscores(e.key(indexMissing), kept[from:min(from+probeChunk, len(kept))])
		if ref != nil {
			return res, ref
		}
		missing = append(missing, got...)
	}
	for i, n := range kept {
		it := WaiterItem{ID: n, Record: recs[i], Missing: missing[i],
			Wait: WaitHead{IDs: []string{}, LeftOut: []string{}, Items: []WaiterRef{}}}
		ws, _, more, ref := e.rangeHead(e.key(sprint.IndexWait+":"+n), "-inf", "+inf", q.Limit)
		if ref != nil {
			return res, ref
		}
		wk, ref := e.leave(ws, &it.Wait.LeftOut)
		if ref != nil {
			return res, ref
		}
		wrecs, ref := e.records(sprint.Work, wk, q.Fields)
		if ref != nil {
			return res, ref
		}
		if ref := e.present(sprint.Work, wrecs); ref != nil {
			return res, ref
		}
		it.Wait.IDs, it.Wait.HasMore = wk, more
		for _, w := range wrecs {
			it.Wait.Items = append(it.Wait.Items, WaiterRef{ID: w.ID, Record: w})
		}
		res.Items = append(res.Items, it)
	}
	return res, nil
}

// streams reads every stream of the work table (up to the units), its control
// card, and for one stopped on a cross need that need card's record and the
// first `limit` ids of its stuck cell.
func (e *qeval) streams(q sprint.SprintQ) (StreamsResult, *Refusal) {
	res := StreamsResult{Kind: q.Kind, Rows: []string{}, Items: []StreamItem{}}
	rows, more, ref := e.rowsOf(sprint.Work, unitsOf(q))
	if ref != nil {
		return res, ref
	}
	res.Rows, res.HasMore = rows, more
	ctlIDs := make([]string, len(rows))
	for i, s := range rows {
		ctlIDs[i] = sprint.CtlID(s)
	}
	ctls, ref := e.records(sprint.Merge, ctlIDs, fieldUnion(q.Fields, nil, ctlFieldState, ctlFieldCause, ctlFieldNeedCard))
	if ref != nil {
		return res, ref
	}
	type cross struct {
		i    int
		need string
	}
	var crossed []cross
	for i, c := range ctls {
		if c.Exists && fieldOf(c, ctlFieldState) == sprint.StreamStopped && fieldOf(c, ctlFieldCause) == causeCross &&
			fieldOf(c, ctlFieldNeedCard) != "" {
			crossed = append(crossed, cross{i, fieldOf(c, ctlFieldNeedCard)})
		}
	}
	needIDs := make([]string, len(crossed))
	for j, c := range crossed {
		needIDs[j] = c.need
	}
	needs, ref := e.records(sprint.Work, needIDs, q.Fields)
	if ref != nil {
		return res, ref
	}
	items := make([]StreamItem, len(rows))
	for i, s := range rows {
		items[i].Stream = s
		if ctls[i].Exists {
			c := project(ctls[i], q.Fields)
			items[i].Control = &c
		}
	}
	for j, c := range crossed {
		n := needs[j]
		items[c.i].Need = &n
		st := &StuckIDs{IDs: []string{}, LeftOut: []string{}}
		if q.Limit > 0 {
			ids, _, more, ref := e.cellIDs(sprint.Merge, rows[c.i], sprint.Stuck, q.Limit)
			if ref != nil {
				return res, ref
			}
			kept, ref := e.leave(ids, &st.LeftOut)
			if ref != nil {
				return res, ref
			}
			st.IDs, st.HasMore = kept, more
		}
		items[c.i].Stuck = st
	}
	res.Items = items
	return res, nil
}

// unitsOf is how many streams, members or readers a listing is over: the
// query's units, or the design's most when it names none (sprint.SprintQ's
// own units is not exported).
func unitsOf(q sprint.SprintQ) int {
	if q.Units > 0 {
		return q.Units
	}
	switch q.Kind {
	case sprint.QueryStreams:
		return sprint.MaxStreams
	case sprint.QueryFleet:
		return sprint.MaxMembers
	}
	return sprint.MaxReaders
}

// listing reads every member's (reader's) row: its control card and the
// counts of its cells, one ZCARD a cell. The cells are every column of the
// table but the control's.
func (e *qeval) listing(q sprint.SprintQ) (ListingResult, *Refusal) {
	table := sprint.Fleet
	if q.Kind == sprint.QueryReaders {
		table = sprint.Readers
	}
	res := ListingResult{Kind: q.Kind, Rows: []string{}, Items: []ListingItem{}}
	rows, more, ref := e.rowsOf(table, unitsOf(q))
	if ref != nil {
		return res, ref
	}
	res.Rows, res.HasMore = rows, more
	cols, ref := e.columnsOf(table)
	if ref != nil {
		return res, ref
	}
	var counted []string
	for _, c := range cols {
		if c != sprint.Ctl {
			counted = append(counted, c)
		}
	}
	ctlIDs := make([]string, len(rows))
	var cells []string
	for i, r := range rows {
		ctlIDs[i] = sprint.CtlID(r)
		for _, c := range counted {
			cells = append(cells, r+":"+c)
		}
	}
	ctls, ref := e.records(table, ctlIDs, q.Fields)
	if ref != nil {
		return res, ref
	}
	var counts []int
	if len(cells) > 0 {
		if counts, ref = e.cellCounts(table, cells); ref != nil {
			return res, ref
		}
	}
	for i, r := range rows {
		it := ListingItem{Row: r, Counts: make([]CellN, 0, len(counted))}
		if ctls[i].Exists {
			c := ctls[i]
			it.Control = &c
		}
		for j, col := range counted {
			it.Counts = append(it.Counts, CellN{Col: col, N: counts[i*len(counted)+j]})
		}
		res.Items = append(res.Items, it)
	}
	return res, nil
}

// needchain walks the needs reachable from the ids through open waiting cards,
// breadth first, reading each card once, up to the query's limit of records.
// A card is expanded when it exists and waits (is in the work table's waiting
// cell); its needs are the ids of its `needs` field. Cut says cards remained
// to read when the limit was reached. Quarantined ids are left out.
func (e *qeval) needchain(q sprint.SprintQ) (NeedchainResult, *Refusal) {
	res := NeedchainResult{Kind: q.Kind, Items: []ChainItem{}, LeftOut: []string{}}
	if ref := e.ensureTable(sprint.Work); ref != nil {
		return res, ref
	}
	ids, named, ref := e.sourceIDs(q.Source, sprint.Work)
	if ref != nil {
		return res, ref
	}
	frontier, ref := e.leave(ids, &res.LeftOut)
	if ref != nil {
		return res, ref
	}
	visited := map[string]bool{}
	start := map[string]bool{}
	for _, id := range frontier {
		visited[id], start[id] = true, true
	}
	read := 0
	for len(frontier) > 0 && read < q.Limit {
		batch := frontier
		if room := q.Limit - read; len(batch) > room {
			batch = frontier[:room]
		}
		rest := frontier[len(batch):]
		recs, ref := e.records(sprint.Work, batch, fieldUnion(q.Fields, nil, fieldNeeds))
		if ref != nil {
			return res, ref
		}
		read += len(batch)
		var next []string
		for _, r := range recs {
			if named && start[r.ID] {
				if ref := e.present(sprint.Work, []Record{r}); ref != nil {
					return res, ref
				}
			}
			it := ChainItem{ID: r.ID, Record: project(r, q.Fields), Needs: []string{}, Start: start[r.ID]}
			if r.Exists {
				needs := sprint.Split(fieldOf(r, fieldNeeds))
				if len(needs) > followMaxNeeds {
					return res, e.failIDs(codeDrift, sprint.Work, r.ID)
				}
				it.Needs = append(it.Needs, needs...)
				if r.Place != nil && r.Place.Col == sprint.Waiting {
					for _, n := range needs {
						if !visited[n] {
							visited[n] = true
							next = append(next, n)
						}
					}
				}
			}
			res.Items = append(res.Items, it)
		}
		next, ref = e.leave(next, &res.LeftOut)
		if ref != nil {
			return res, ref
		}
		frontier = append(append([]string{}, rest...), next...)
	}
	res.Cut = len(frontier) > 0
	return res, nil
}

// noteSeq is the seq of a note's id, n<seq> with the epoch suffix ~<epoch> of a
// later epoch (1.3.4), and the epoch the id belongs to.
func noteSeq(id string) (seq uint64, epoch string, ok bool) {
	if len(id) < 2 || id[:1] != noteIDPrefix {
		return 0, "", false
	}
	body, epoch := id[1:], "0"
	if i := strings.IndexByte(body, '~'); i >= 0 {
		body, epoch = body[:i], body[i+1:]
		if !tsetDecimal(epoch) {
			return 0, "", false
		}
	}
	n, err := strconv.ParseUint(body, 10, 64)
	if err != nil || n < 1 || n > maxSeq || strconv.FormatUint(n, 10) != body {
		return 0, "", false
	}
	return n, epoch, true
}

func tsetDecimal(s string) bool { return tset.ValidDecimal(tset.Decimal(s)) }

// jnote reads each note from its line (by its seq): its type and cause from
// the line's meta, its subjects from the line's `about` (quarantined ones left
// out), and for each subject the count of the judgments open on it and what
// its jopen holds for this note's own type and cause.
func (e *qeval) jnote(q sprint.SprintQ) (JnoteResult, *Refusal) {
	res := JnoteResult{Kind: q.Kind, IDs: []string{}, Items: []NoteItem{}}
	ids, _, ref := e.sourceIDs(q.Source, "")
	if ref != nil {
		return res, ref
	}
	res.IDs = ids
	most := q.Subjects
	if most <= 0 {
		most = sprint.MaxAboutIDs
	}
	for _, id := range ids {
		seq, epoch, ok := noteSeq(id)
		if !ok || epoch != string(e.epoch) {
			return res, requestRefusal()
		}
		line, ref := e.lineAt(seq)
		if ref != nil {
			return res, ref
		}
		var meta map[string]any
		if line.Kind != noteKind || json.Unmarshal(line.Meta, &meta) != nil {
			return res, e.failIDs(codeDrift, "", id)
		}
		typ, _ := meta[noteMetaType].(string)
		cause, _ := meta[noteMetaCause].(string)
		if typ == "" || cause == "" {
			return res, e.failIDs(codeDrift, "", id)
		}
		if len(line.About) > most {
			return res, e.over("subjects", len(line.About), most)
		}
		it := NoteItem{Note: id, Seq: strconv.FormatUint(seq, 10), Type: typ, Cause: cause, Subjects: []SubjectOpen{}}
		var left []string
		kept, ref := e.leave(line.About, &left)
		if ref != nil {
			return res, ref
		}
		it.LeftOut = len(left)
		field := typ + "|" + cause
		for _, s := range kept {
			key := e.key("jopen:" + s)
			n, ref := e.hlen(key)
			if ref != nil {
				return res, ref
			}
			own, ref := e.hmget(key, []string{field})
			if ref != nil {
				return res, ref
			}
			it.Subjects = append(it.Subjects, SubjectOpen{ID: s, Count: n, Own: own[0]})
		}
		res.Items = append(res.Items, it)
	}
	return res, nil
}

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
	return nil, queryRequestRefusal()
}

// related reads each id's record and what the follows reach from it.
func (e *qeval) related(q sprint.SprintQ) (RelatedResult, *Refusal) {
	res := RelatedResult{Kind: q.Kind, IDs: []string{}, LeftOut: []string{}, Items: []RelatedItem{}}
	var left leftOut
	if ref := e.ensureTable(q.Table); ref != nil {
		return res, ref
	}
	ids, named, ref := e.sourceIDs(q.Source, q.Table)
	if ref != nil {
		return res, ref
	}
	kept, ref := e.leave(ids, &left)
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
	fs, ref := e.follows(q.Table, existing, q.Follow, q.Fields, &left)
	if ref != nil {
		return res, ref
	}
	byRec := map[int]*Follows{}
	for j, i := range at {
		byRec[i] = fs[j]
	}
	res.IDs, res.LeftOut = kept, left.ids()
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
	var left leftOut
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
			kept, ref := e.leave(ids, &left)
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
			fs, ref := e.follows(sprint.Work, recs, h.Follow, q.Fields, &left)
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
	res.LeftOut = left.ids()
	keys, ref := e.sprintKeys(q, []string{q.Stream}, res.G)
	if ref != nil {
		return res, ref
	}
	res.Keys = keys
	return res, nil
}

// sprintKeys reads the sprint keys a composite query names beside its answer
// (sprint.SprintQ.Keys; the errata's seam of E6), in the query's snapshot and
// after everything else it read, in the order named: the dropping marks of the
// streams the query reached (reach, in order: for `front` its stream, for
// `waiters` the streams of the waiters it returned, for `streams` every stream
// it lists; 1.3.5), one HMGET of {p}dropping@e naming each stream once and none
// when it reached none; {p}next@e.streams, one HMGET of one field (2.3 R15);
// and a jopen key, one HMGET of the one field its rule tests (jopenFields):
// jopen:G of the first sentinel g of a `front` (none when the stream has none;
// 2.3 R3), and jopen:sprint of the sprint's subject (2.3 R15).
func (e *qeval) sprintKeys(q sprint.SprintQ, reach []string, g string) ([]KeyResult, *Refusal) {
	var out []KeyResult
	for _, k := range q.Keys {
		kr := KeyResult{Key: k, Streams: []string{}}
		switch k {
		case sprint.KeyDropping:
			seen := map[string]bool{}
			var names []string
			for _, st := range reach {
				if st != "" && !seen[st] {
					seen[st] = true
					names = append(names, st)
				}
			}
			if len(names) > 0 {
				marks, ref := e.hmget(e.key(keyDropping), names, hashFieldBytes)
				if ref != nil {
					return nil, ref
				}
				for i, st := range names {
					if marks[i] != nil {
						kr.Streams = append(kr.Streams, st)
					}
				}
			}
		case sprint.KeyNextStreams:
			v, ref := e.hmget(e.key(keyNext), []string{xFieldNextStream}, hashFieldBytes)
			if ref != nil {
				return nil, ref
			}
			kr.N = "0"
			if v[0] != nil {
				n, err := strconv.ParseUint(*v[0], 10, 64)
				if err != nil || strconv.FormatUint(n, 10) != *v[0] {
					return nil, e.fail(codeDrift, tset.RefusalDetail{})
				}
				kr.N = *v[0]
			}
		case sprint.KeyJOpenG, sprint.KeyJOpenSprint:
			subject := g
			if k == sprint.KeyJOpenSprint {
				subject = sprint.SprintSubject
			}
			if subject != "" {
				v, ref := e.hmget(e.key("jopen:"+subject), []string{jopenFields[k]}, hashFieldBytes)
				if ref != nil {
					return nil, ref
				}
				state, ok := jopenState(v[0])
				if !ok {
					return nil, e.fail(codeDrift, tset.RefusalDetail{})
				}
				kr.Subject, kr.State = subject, state
			}
		default:
			return nil, queryRequestRefusal()
		}
		out = append(out, kr)
	}
	return out, nil
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
	var left leftOut
	var reach []string // the streams of the waiters returned, for the dropping marks
	if ref := e.ensureTable(sprint.Work); ref != nil {
		return res, ref
	}
	ids, _, moreIDs, ref := e.sourceIDsMore(q.Source, sprint.Work)
	if ref != nil {
		return res, ref
	}
	kept, ref := e.leave(ids, &left)
	if ref != nil {
		return res, ref
	}
	res.IDs, res.LeftOut, res.MoreIDs = kept, left.ids(), moreIDs
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
		if q.Missing && missing[i] == nil {
			// a query of the made needs reads the head of wait:n only for the ids
			// with a score in {p}missing@e (sprint.SprintQ.Missing)
			res.Items = append(res.Items, it)
			continue
		}
		ws, more, ref := e.waitHead(n, q.WaiterAfter, q.Limit)
		if ref != nil {
			return res, ref
		}
		if len(ws) > 0 {
			it.Wait.Last = ws[len(ws)-1]
		}
		var wleft leftOut
		wk, ref := e.leave(ws, &wleft)
		if ref != nil {
			return res, ref
		}
		it.Wait.LeftOut = wleft.ids()
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
			if w.Place != nil {
				reach = append(reach, w.Place.Row)
			}
		}
		res.Items = append(res.Items, it)
	}
	keys, ref := e.sprintKeys(q, reach, "")
	if ref != nil {
		return res, ref
	}
	res.Keys = keys
	return res, nil
}

// waitHead is the head of wait:n after the waiter (sprint.SprintQ.WaiterAfter,
// "" from the first), up to the limit, in the order of wait:n's members: a
// place in that order, so a waiter that has left the set moves nothing. It is
// one probe, as rangeHead is, and says whether the set has members beyond the
// head. Its last id is the last member read (WaitHead.Last), before the
// quarantined are left out. The store's half answers no cursor yet: wait:n's
// members are all scored 0, Layer 1's checked head is by score only, and the Lua
// refuses a cursor CONFIG until Layer 1 has a lexicographic head
// (TestLuaWaitersCursorAwaitsAnOrderedHead).
func (e *qeval) waitHead(n, after string, limit int) (ids []string, more bool, ref *Refusal) {
	key := e.key(sprint.IndexWait + ":" + n)
	if after == "" {
		ids, _, more, ref = e.rangeHead(key, "-inf", "+inf", limit)
		return ids, more, ref
	}
	if ref = e.probe(); ref != nil {
		return nil, false, ref
	}
	if ref = e.typed(key, kindZSet); ref != nil {
		return nil, false, ref
	}
	for _, p := range e.t.keys.zpairs(key) {
		if p.member <= after {
			continue
		}
		if len(ids) == limit {
			more = true
			break
		}
		ids = append(ids, p.member)
	}
	return nonNilStrings(ids), more, e.rangeIDs(len(ids))
}

// streams reads every stream of the work table (up to the units), its control
// card, and for one stopped on a cross need that need card's record and the
// first `limit` ids of its stuck cell. Every card it would read leaves out the
// quarantined ones, as every sprint query does (1.0, 1.3.5): a stream whose
// control card is quarantined is listed with no control card, and a stream
// stopped on a need card that is quarantined has no need record; the ids are
// named in left_out, and the stream is still told apart as stopped on a cross
// need by its stuck list, which it has either way.
func (e *qeval) streams(q sprint.SprintQ) (StreamsResult, *Refusal) {
	res := StreamsResult{Kind: q.Kind, Rows: []string{}, LeftOut: []string{}, Items: []StreamItem{}}
	var left leftOut
	rows, more, ref := e.rowsOf(sprint.Work, unitsOf(q))
	if ref != nil {
		return res, ref
	}
	res.Rows, res.HasMore = rows, more
	ctlIDs := make([]string, len(rows))
	for i, s := range rows {
		ctlIDs[i] = sprint.CtlID(s)
	}
	keptCtl, ref := e.leave(ctlIDs, &left)
	if ref != nil {
		return res, ref
	}
	ctlRecs, ref := e.records(sprint.Merge, keptCtl, fieldUnion(q.Fields, nil, ctlFieldState, ctlFieldCause, ctlFieldOther, ctlFieldNeedCard))
	if ref != nil {
		return res, ref
	}
	ctls := make([]*Record, len(rows)) // nil: the control card was left out
	for i, k := 0, 0; i < len(rows) && k < len(keptCtl); i++ {
		if keptCtl[k] == ctlIDs[i] {
			ctls[i] = &ctlRecs[k]
			k++
		}
	}
	type cross struct {
		i    int
		need string
	}
	var crossed []cross
	for i, c := range ctls {
		if c == nil || !c.Exists || recordField(*c, ctlFieldState) != sprint.StreamStopped || recordField(*c, ctlFieldCause) != causeCross {
			continue
		}
		if need := crossNeed(*c); need != "" {
			crossed = append(crossed, cross{i, need})
		}
	}
	needIDs := make([]string, len(crossed))
	for j, c := range crossed {
		needIDs[j] = c.need
	}
	keptNeeds, ref := e.leave(needIDs, &left)
	if ref != nil {
		return res, ref
	}
	needRecs, ref := e.records(sprint.Work, keptNeeds, q.Fields)
	if ref != nil {
		return res, ref
	}
	needs := make([]*Record, len(crossed)) // nil: the need card was left out
	for j, k := 0, 0; j < len(crossed) && k < len(keptNeeds); j++ {
		if keptNeeds[k] == needIDs[j] {
			needs[j] = &needRecs[k]
			k++
		}
	}
	items := make([]StreamItem, len(rows))
	for i, s := range rows {
		items[i].Stream = s
		if ctls[i] != nil && ctls[i].Exists {
			c := project(*ctls[i], q.Fields)
			items[i].Control = &c
		}
	}
	for j, c := range crossed {
		if needs[j] != nil {
			items[c.i].Need = needs[j]
		}
		st := &StuckIDs{IDs: []string{}, LeftOut: []string{}}
		if q.Limit > 0 {
			ids, _, more, ref := e.cellIDs(sprint.Merge, rows[c.i], sprint.Stuck, q.Limit)
			if ref != nil {
				return res, ref
			}
			var sleft leftOut
			kept, ref := e.leave(ids, &sleft)
			if ref != nil {
				return res, ref
			}
			st.IDs, st.HasMore, st.LeftOut = kept, more, sleft.ids()
		}
		items[c.i].Stuck = st
	}
	if len(q.Counts) > 0 && len(rows) > 0 {
		// the counts of the cells named, of every stream listed (2.3 R15): a
		// column the work table does not have is NOCOL, as Layer 1's own count
		// refuses it, and each cell is one ZCARD
		cols, ref := e.columnsOf(sprint.Work)
		if ref != nil {
			return res, ref
		}
		has := map[string]bool{}
		for _, c := range cols {
			has[c] = true
		}
		cells := make([]string, 0, len(rows)*len(q.Counts))
		for _, r := range rows {
			for _, c := range q.Counts {
				if !has[c] {
					return res, e.fail("NOCOL", tset.RefusalDetail{Table: sprint.Work, Cells: []string{r + ":" + c}})
				}
				cells = append(cells, r+":"+c)
			}
		}
		counts, ref := e.cellCounts(sprint.Work, cells)
		if ref != nil {
			return res, ref
		}
		for i := range rows {
			items[i].Counts = make([]CellN, 0, len(q.Counts))
			for j, c := range q.Counts {
				items[i].Counts = append(items[i].Counts, CellN{Col: c, N: counts[i*len(q.Counts)+j]})
			}
		}
	}
	res.Items, res.LeftOut = items, left.ids()
	keys, ref := e.sprintKeys(q, rows, "")
	if ref != nil {
		return res, ref
	}
	res.Keys = keys
	return res, nil
}

// crossNeed is the card a stopped stream's control card says it waits for: the
// `other` field, which is where the writer records the cross fact, and when
// that is empty `need_card`, which IT11's held rule reads beside it.
func crossNeed(ctl Record) string {
	if need := recordField(ctl, ctlFieldOther); need != "" {
		return need
	}
	return recordField(ctl, ctlFieldNeedCard)
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
// table but the control's. A control card that is quarantined is left out, as
// every sprint query leaves the id out (1.0, 1.3.5): the row is listed, its
// counts with it, and the card is named in left_out.
func (e *qeval) listing(q sprint.SprintQ) (ListingResult, *Refusal) {
	table := sprint.Fleet
	if q.Kind == sprint.QueryReaders {
		table = sprint.Readers
	}
	res := ListingResult{Kind: q.Kind, Rows: []string{}, LeftOut: []string{}, Items: []ListingItem{}}
	var left leftOut
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
	keptCtl, ref := e.leave(ctlIDs, &left)
	if ref != nil {
		return res, ref
	}
	ctlRecs, ref := e.records(table, keptCtl, q.Fields)
	if ref != nil {
		return res, ref
	}
	ctls := make([]*Record, len(rows)) // nil: the control card was left out
	for i, k := 0, 0; i < len(rows) && k < len(keptCtl); i++ {
		if keptCtl[k] == ctlIDs[i] {
			ctls[i] = &ctlRecs[k]
			k++
		}
	}
	var counts []int
	if len(cells) > 0 {
		if counts, ref = e.cellCounts(table, cells); ref != nil {
			return res, ref
		}
	}
	for i, r := range rows {
		it := ListingItem{Row: r, Counts: make([]CellN, 0, len(counted))}
		if ctls[i] != nil && ctls[i].Exists {
			c := *ctls[i]
			it.Control = &c
		}
		for j, col := range counted {
			it.Counts = append(it.Counts, CellN{Col: col, N: counts[i*len(counted)+j]})
		}
		res.Items = append(res.Items, it)
	}
	res.LeftOut = left.ids()
	if len(q.Props) > 0 {
		// the table properties the query names, read with the listing: one
		// HMGET of the table's property hash, a cell for each name (the Lua's
		// S.read_probe)
		if ref := e.probes(len(q.Props)); ref != nil {
			return res, ref
		}
		ans, ref := e.memRead([]tset.ReadQuery{{Kind: "props", Table: table, Names: q.Props}})
		if ref != nil {
			return res, ref
		}
		res.Props = ans[0].Props
	}
	return res, nil
}

// needchain walks the needs reachable from the ids through open waiting cards,
// breadth first, reading each card once, up to the query's limit of records.
// A card is expanded when it exists and waits (is in the work table's waiting
// cell). What it waits for (errata 3 amendment 7) is the ids of its `needs`
// field and its place in line: a sentinel waits for the waiting cards of its
// stream back to the sentinel before it, and any card for the nearest open
// sentinel before it (placeNeeds): the edges of cycle.go's graph, each read
// linear. The place reads take at most the query's limit of ids in all.
// Cut says cards remained to read when the limit was reached, or a place read
// was cut. Quarantined ids are left out.
func (e *qeval) needchain(q sprint.SprintQ) (NeedchainResult, *Refusal) {
	res := NeedchainResult{Kind: q.Kind, Items: []ChainItem{}, LeftOut: []string{}}
	var left leftOut
	if ref := e.ensureTable(sprint.Work); ref != nil {
		return res, ref
	}
	ids, named, ref := e.sourceIDs(q.Source, sprint.Work)
	if ref != nil {
		return res, ref
	}
	frontier, ref := e.leave(ids, &left)
	if ref != nil {
		return res, ref
	}
	visited := map[string]bool{}
	start := map[string]bool{}
	for _, id := range frontier {
		visited[id], start[id] = true, true
	}
	read := 0
	placeLeft, placeCut := q.Limit, false
	for len(frontier) > 0 && read < q.Limit {
		batch := frontier
		if room := q.Limit - read; len(batch) > room {
			batch = frontier[:room]
		}
		rest := frontier[len(batch):]
		recs, ref := e.records(sprint.Work, batch, fieldUnion(q.Fields, nil, fieldNeeds, fieldKind))
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
				needs := sprint.Split(recordField(r, fieldNeeds))
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
					place, cut, ref := e.placeNeeds(r, placeLeft)
					if ref != nil {
						return res, ref
					}
					placeLeft -= len(place)
					placeCut = placeCut || cut
					for _, n := range place {
						if !visited[n] {
							visited[n] = true
							next = append(next, n)
						}
					}
				}
			}
			res.Items = append(res.Items, it)
		}
		next, ref = e.leave(next, &left)
		if ref != nil {
			return res, ref
		}
		frontier = append(append([]string{}, rest...), next...)
	}
	res.Cut, res.LeftOut = len(frontier) > 0 || placeCut, left.ids()
	return res, nil
}

// placeNeeds is what a waiting card waits for by its place in line (errata 3
// amendment 7): every card waits for the nearest open sentinel of its stream
// before it (sent:<s>, the highest score below its own: one read of one id),
// and a sentinel also for the waiting cards back to that sentinel, itself
// included (the stream's waiting cell from its score up to the sentinel's). A
// card before the sentinel before is needed through it, so the reach is the
// graph of cycle.go's, and the reads are linear: each card of a line is in one
// sentinel's interval. The ids come to at most left; cut when a read held more,
// or nothing is left.
func (e *qeval) placeNeeds(r Record, left int) (ids []string, cut bool, ref *Refusal) {
	if r.Score == "" {
		return nil, false, nil
	}
	if left <= 0 {
		return nil, true, nil
	}
	below := "(" + r.Score
	prev, prevScores, _, ref := e.rangeHeadDesc(e.key("sent:"+r.Place.Row), "-inf", below, 1)
	if ref != nil {
		return nil, false, ref
	}
	if recordField(r, fieldKind) != kindSentinel {
		return prev, false, nil
	}
	from := "-inf"
	if len(prev) == 1 {
		from = prevScores[0]
	}
	if ref = e.probe(); ref != nil {
		return nil, false, ref
	}
	ans, ref := e.memRead([]tset.ReadQuery{{Kind: "range", Table: sprint.Work, Cell: r.Place.Row + ":" + sprint.Waiting, Min: from, Max: below, Limit: left}})
	if ref != nil {
		return nil, false, ref
	}
	ids = nonNilStrings(ans[0].IDs)
	return ids, ans[0].HasMore, e.rangeIDs(len(ids))
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
			return res, queryRequestRefusal()
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
		var left leftOut
		kept, ref := e.leave(line.About, &left)
		if ref != nil {
			return res, ref
		}
		it.LeftOut = len(left.list)
		field := typ + "|" + cause
		for _, s := range kept {
			key := e.key("jopen:" + s)
			n, ref := e.hlen(key)
			if ref != nil {
				return res, ref
			}
			own, ref := e.hmget(key, []string{field}, hashFieldBytes)
			if ref != nil {
				return res, ref
			}
			it.Subjects = append(it.Subjects, SubjectOpen{ID: s, Count: n, Own: own[0]})
		}
		res.Items = append(res.Items, it)
	}
	return res, nil
}

// jopenFields is the one field of a subject's jopen hash that the rule reading
// a jopen key tests, `<type>|<cause>` (J's field; twin_j.go jKeyJopen): R3 the
// sentinel reached on G, R15 the sprint done on the sprint (2.3). Each rule
// tests one known type, so the read is an HMGET of one field and needs no
// enumeration of the hash (Layer 1's probes have none).
var jopenFields = map[string]string{
	sprint.KeyJOpenG:      sprint.NSentinelReached + "|" + sprint.ReachedCause,
	sprint.KeyJOpenSprint: sprint.NSprintDone + "|" + sprint.SprintDoneCause,
}

// jopenState is what a jopen field holds, as J writes it: "" when absent,
// "open" for a note's id, "held" for h and a note's id; ok is false for any
// other value, which is DRIFT (twin_j.go, jDecider.state).
func jopenState(v *string) (state string, ok bool) {
	switch {
	case v == nil:
		return "", true
	case strings.HasPrefix(*v, jHoldPrefix+jNotePrefix) && len(*v) > 2:
		return jopenHeld, true
	case strings.HasPrefix(*v, jNotePrefix) && len(*v) > 1:
		return jopenOpen, true
	}
	return "", false
}

// The states of a jopen field in a KeyResult.
const (
	jopenOpen = "open"
	jopenHeld = "held"
)

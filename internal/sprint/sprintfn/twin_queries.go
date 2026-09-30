package sprintfn

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The composed twin's sprint queries (upper design 1.0, "Composite queries";
// errata 1 E3 and E6; item IT30). Twin.Query answers one sprint.SprintQ as
// the store's ns_sprint_read will: from one snapshot (the twin's lock), under
// one time, through the same reads the Lua makes of Layer 1's checked helpers
// (a record once for every occurrence, a sorted-set head with its lookahead, a
// cell or key probe, a line by seq), each charged against the bounds of L1 6
// and 7, complete or refused. It reads tables through tset.Mem, a line
// through the log twin and the sprint's keys from the twin's own keyspace, and
// never writes. The Lua half is sprint_queries.lua; TestTwinEqualsLuaQueries
// holds the two equal once a store runs it.
//
// Every query leaves out the ids in {p}quarantine@e (1.3.5), naming them in
// the answer's left_out, except front(s), which still takes a quarantined
// sentinel as sigma and returns it marked.

// The refusal codes a query adds to the write path's (1.3.5; L1 8).
const (
	codeBudget      = "BUDGET"      // a read past a bound of L1 6 or 7: no answer, never a partial one
	codeDrift       = "DRIFT"       // a record, a cell or a key that does not agree with what names it
	codeLogID       = "LOGID"       // a seq that names no line of the log (L2 decision 12)
	codeMissing     = "MISSING"     // an id taken from an index has no record
	codeMemberEpoch = "MEMBEREPOCH" // an id taken from an index has a record of another epoch
)

// The fields of a stream's control card that `streams` reads to find a stop
// on a cross need (1.0's `streams`, 2.3 R5), and the values it tests. The card
// a cross stop waits for is where the writer puts it, the control card's
// `other` (steps_merge.go: ctlSet["other"]); `need_card` is read when `other`
// is empty, as IT11's held rule reads the two.
const (
	ctlFieldState    = "state"     // the stream's state (sprint.StreamStopped)
	ctlFieldCause    = "cause"     // why a stopped stream stopped
	ctlFieldOther    = "other"     // the card a cross stop waits for, as the writer records it
	ctlFieldNeedCard = "need_card" // the same card, when `other` is empty
	causeCross       = "cross"     // "needs a card of another stream first" (2.2, R5)
)

// The fields of a card that follows read (1.0's `related`).
const (
	fieldAttempt = "attempt" // a primary's attempt: its live work card is <p>.w<attempt>
	fieldRCards  = "rcards"  // a primary's read cards, a comma list
	fieldNeeds   = "needs"   // a card's needs, a comma list
	fieldMember  = "member"  // the member a card names
	fieldPrimary = "primary" // the primary a card of a member's cells names (FollowPrimary)
	fieldKind    = "kind"    // a card's kind: work, or sentinel (needchain's place in line)
	kindSentinel = "sentinel"
)

// The bounds of one query's own reads (L1 6, 7).
const (
	// queryMaxProbes is the cell and key probes of one read (L1 6).
	queryMaxProbes = 20000
	// recordChunk is the ids of one table read of a query, at most what Layer 1
	// plans in one (L1 6: 2,000 candidates); Layer 1 bounds a read at 10,000
	// records altogether, which the query's own charge checks.
	recordChunk = 1000
)

// The bytes a checked HMGET reserves for each field it names, and its slack
// (the Lua's Q.hmget and Q.quarantined: fields*per + 64). Layer 1's readcmd
// refuses DRIFT (budget read_reservation) a reply larger than its reserve, so
// a value over its share makes the query DRIFT in the store; the twin answers
// the same (finding 7 of the cold read).
const (
	hashFieldBytes       = 1024 // a sprint hash field's value
	heartbeatFieldBytes  = 8192 // the heartbeat's `rules` field holds a count for every rule
	quarantineEntryBytes = 1024 // a quarantine mark: code, rule, stream, cells and note (1.3.1)
	reserveSlack         = 64
)

// The notes' lines (1.3.4): a note's type and cause are in its line's meta,
// and its id is n<seq>, with the epoch suffix ~<epoch> at a later epoch
// (sprint.OpFamily).
const (
	noteKind      = "note"
	noteMetaType  = "type"
	noteMetaCause = "cause"
	noteIDPrefix  = "n"
)

// qeval is one query's evaluation: the twin's state read under its lock, the
// call's one epoch and time, and what has been charged so far.
type qeval struct {
	t     *Twin
	ctx   context.Context
	epoch tset.Decimal
	nowMS tset.Decimal
	sk    string // the prefix and "sprint:": the sprint's own keys lie under it
	c     QueryCharge
}

func (t *Twin) newEval(epoch, nowMS tset.Decimal) *qeval {
	return &qeval{t: t, ctx: context.Background(), epoch: epoch, nowMS: nowMS, sk: t.prefix + "sprint:"}
}

// key is a per-epoch sprint key: {p}<name>@e.
func (e *qeval) key(name string) string { return e.sk + name + "@" + string(e.epoch) }

// bare is a sprint key of no epoch: {p}<name>.
func (e *qeval) bare(name string) string { return e.sk + name }

func (e *qeval) fail(code string, d tset.RefusalDetail) *Refusal {
	return refuse(PhaseOpen, code, RefusalDetail{RefusalDetail: d})
}

func (e *qeval) failIDs(code, table string, ids ...string) *Refusal {
	return e.fail(code, tset.RefusalDetail{Table: table, IDs: ids})
}

// over refuses a read that has gone past a bound: BUDGET naming it.
func (e *qeval) over(budget string, actual, limit int) *Refusal {
	a, l := int64(actual), int64(limit)
	return e.fail(codeBudget, tset.RefusalDetail{Budget: budget, Actual: &a, Limit: &l})
}

// probe charges one cell or key probe.
func (e *qeval) probe() *Refusal { return e.probes(1) }

// probes charges n cell or key probes: Layer 1's checked probe charges every
// name an HMGET or a ZMSCORE asks for, a cell each, and any other probe one.
func (e *qeval) probes(n int) *Refusal {
	e.c.Probes += n
	if e.c.Probes > queryMaxProbes {
		return e.over("cell", e.c.Probes, queryMaxProbes)
	}
	return nil
}

// memRead is one atomic Mem read of the queries given.
func (e *qeval) memRead(qs []tset.ReadQuery) ([]tset.ReadAnswer, *Refusal) {
	rep, err := e.t.tab.Read(e.ctx, newTSetReadPlan(e.t.prefix, e.epoch, "atomic", qs))
	if err != nil {
		var lref *tset.Refusal
		if errors.As(err, &lref) {
			return nil, fromTset(PhaseOpen, lref)
		}
		return nil, e.fail(codeDrift, tset.RefusalDetail{})
	}
	if len(rep.Answers) != len(qs) {
		return nil, e.fail(codeDrift, tset.RefusalDetail{})
	}
	return rep.Answers, nil
}

// ensureTable is S.ensure_read_table: the table is defined, at the read's epoch.
func (e *qeval) ensureTable(table string) *Refusal {
	_, ref := e.memRead([]tset.ReadQuery{{Kind: "rows", Table: table}})
	return ref
}

// records reads the records of ids of a table with a projection, one record
// for every occurrence (S.read_record charges each, cached payload or not).
func (e *qeval) records(table string, ids, fields []string) ([]Record, *Refusal) {
	out := make([]Record, 0, len(ids))
	if fields == nil {
		fields = []string{}
	}
	for from := 0; from < len(ids); from += recordChunk {
		chunk := ids[from:min(from+recordChunk, len(ids))]
		e.c.Records += len(chunk)
		if e.c.Records > queryMaxRecords {
			return nil, e.over("record", e.c.Records, queryMaxRecords)
		}
		ans, ref := e.memRead([]tset.ReadQuery{{Kind: "ids", Table: table, IDs: chunk, Fields: fields}})
		if ref != nil {
			return nil, ref
		}
		if len(ans[0].Records) != len(chunk) {
			return nil, e.failIDs(codeDrift, table)
		}
		out = append(out, ans[0].Records...)
	}
	return out, nil
}

// cellIDs is the first limit ids of a table's cell, with their scores (a range
// head: one probe, the ids returned charged as range ids), and whether it had
// more. A row the table does not have is NOROW.
func (e *qeval) cellIDs(table, row, col string, limit int) (ids, scores []string, more bool, ref *Refusal) {
	// One probe for the row, which Layer 1's own cell reads check (NOROW), and one
	// for the range.
	for i := 0; i < 2; i++ {
		if ref = e.probe(); ref != nil {
			return nil, nil, false, ref
		}
	}
	ans, ref := e.memRead([]tset.ReadQuery{{Kind: "range", Table: table, Cell: row + ":" + col, Min: "-inf", Max: "+inf", Limit: limit}})
	if ref != nil {
		return nil, nil, false, named(ref, table, row+":"+col)
	}
	if ref = e.rangeIDs(len(ans[0].IDs)); ref != nil {
		return nil, nil, false, ref
	}
	return nonNilStrings(ans[0].IDs), nonNilStrings(ans[0].Scores), ans[0].HasMore, nil
}

// named gives a NOROW or NOCOL refusal the table and the cell it is about, as
// the store's read names them (Layer 1's twin leaves them out).
func named(ref *Refusal, table, cell string) *Refusal {
	if ref.Code == "NOROW" || ref.Code == "NOCOL" {
		ref.Detail.Table, ref.Detail.Cells = table, []string{cell}
	}
	return ref
}

// rangeIDs charges range ids returned.
func (e *qeval) rangeIDs(n int) *Refusal {
	e.c.RangeIDs += n
	if e.c.RangeIDs > queryMaxRangeIDs {
		return e.over("range_id", e.c.RangeIDs, queryMaxRangeIDs)
	}
	return nil
}

// rowsOf is the first limit rows of a table in rank order, and whether it had
// more: a range head of the rows, whose ids are charged as range ids and as
// row ids.
func (e *qeval) rowsOf(table string, limit int) (rows []string, more bool, ref *Refusal) {
	if ref = e.probe(); ref != nil {
		return nil, false, ref
	}
	ans, ref := e.memRead([]tset.ReadQuery{{Kind: "rows", Table: table}})
	if ref != nil {
		return nil, false, ref
	}
	all := ans[0].Rows
	if len(all) > limit {
		more, all = true, all[:limit]
	}
	for _, r := range all {
		rows = append(rows, r.Row)
	}
	e.c.RowIDs += len(rows)
	return nonNilStrings(rows), more, e.rangeIDs(len(rows))
}

// cellCounts is the count of each cell of a table, one probe a cell.
func (e *qeval) cellCounts(table string, cells []string) ([]int, *Refusal) {
	for range cells {
		if ref := e.probe(); ref != nil {
			return nil, ref
		}
	}
	ans, ref := e.memRead([]tset.ReadQuery{{Kind: "count", Table: table, Cells: cells}})
	if ref != nil {
		return nil, ref
	}
	out := make([]int, len(ans[0].Counts))
	for i, n := range ans[0].Counts {
		out[i] = int(n)
	}
	return out, nil
}

// columnsOf are a table's set columns at the read's epoch, in definition order.
func (e *qeval) columnsOf(table string) ([]string, *Refusal) {
	snap, err := e.t.tab.Snapshot(e.t.prefix)
	if err != nil {
		return nil, e.fail(codeDrift, tset.RefusalDetail{})
	}
	def, ok := snap.Definitions[table]
	if !ok {
		return nil, e.fail("NOTABLE", tset.RefusalDetail{Table: table})
	}
	return def.Columns, nil
}

// typed says the sprint key holds the type asked, or nothing.
func (e *qeval) typed(key, kind string) *Refusal {
	if got := e.t.keys.typeOf(key); got != kindNone && got != kind {
		return e.fail("WRONGTYPE", tset.RefusalDetail{})
	}
	return nil
}

// hmget is one HMGET probe: each field's value, nil for one the hash lacks. A
// reply larger than the reserve the probe asked for, per bytes for each field
// and a slack, is DRIFT (budget read_reservation), as Layer 1's checked probe
// refuses it.
func (e *qeval) hmget(key string, fields []string, per int) ([]*string, *Refusal) {
	if ref := e.probes(len(fields)); ref != nil {
		return nil, ref
	}
	if ref := e.typed(key, kindHash); ref != nil {
		return nil, ref
	}
	out := make([]*string, len(fields))
	bytes := 0
	for i, f := range fields {
		if v, ok := (&Keys{ks: e.t.keys}).HGet(key, f); ok {
			v := v
			out[i] = &v
			bytes += len(v)
		}
	}
	if reserve := len(fields)*per + reserveSlack; bytes > reserve {
		a, l := int64(bytes), int64(reserve)
		return nil, e.fail(codeDrift, tset.RefusalDetail{Budget: "read_reservation", Actual: &a, Limit: &l})
	}
	return out, nil
}

// hlen is one HLEN probe.
func (e *qeval) hlen(key string) (int, *Refusal) {
	if ref := e.probe(); ref != nil {
		return 0, ref
	}
	if ref := e.typed(key, kindHash); ref != nil {
		return 0, ref
	}
	if v := e.t.keys.vals[key]; v != nil {
		return len(v.hash), nil
	}
	return 0, nil
}

// zscores is one ZMSCORE probe: each member's score, nil for one the set lacks.
func (e *qeval) zscores(key string, members []string) ([]*string, *Refusal) {
	if ref := e.probes(len(members)); ref != nil {
		return nil, ref
	}
	if ref := e.typed(key, kindZSet); ref != nil {
		return nil, ref
	}
	out := make([]*string, len(members))
	for i, m := range members {
		if s, ok := (&Keys{ks: e.t.keys}).ZScore(key, m); ok {
			v := formatScore(s)
			out[i] = &v
		}
	}
	return out, nil
}

// zscore is one ZSCORE probe.
func (e *qeval) zscore(key, member string) (*string, *Refusal) {
	out, ref := e.zscores(key, []string{member})
	if ref != nil {
		return nil, ref
	}
	return out[0], nil
}

// zcount is one ZCOUNT probe over [min, max] in Redis's bound grammar.
func (e *qeval) zcount(key, min, max string) (int, *Refusal) {
	if ref := e.probe(); ref != nil {
		return 0, ref
	}
	if ref := e.typed(key, kindZSet); ref != nil {
		return 0, ref
	}
	n := 0
	for _, p := range e.t.keys.zpairs(key) {
		if inBounds(p.score, min, max) {
			n++
		}
	}
	return n, nil
}

// rangeHead is S.read_range_head: the first limit members of a sorted set in
// [min, max] by score (then by member), with the lookahead that says whether
// there were more. One probe; the ids returned are charged as range ids.
func (e *qeval) rangeHead(key, min, max string, limit int) (ids, scores []string, more bool, ref *Refusal) {
	if ref = e.probe(); ref != nil {
		return nil, nil, false, ref
	}
	if ref = e.typed(key, kindZSet); ref != nil {
		return nil, nil, false, ref
	}
	for _, p := range e.t.keys.zpairs(key) {
		if !inBounds(p.score, min, max) {
			continue
		}
		if len(ids) == limit {
			more = true
			break
		}
		ids, scores = append(ids, p.member), append(scores, formatScore(p.score))
	}
	return nonNilStrings(ids), nonNilStrings(scores), more, e.rangeIDs(len(ids))
}

// ---- keyspace reads

type zpair struct {
	member string
	score  float64
}

// zpairs are a sorted set's members ordered by score, then member, as Redis
// orders them.
func (ks *keyspace) zpairs(key string) []zpair {
	v := ks.vals[key]
	if v == nil || v.kind != kindZSet {
		return nil
	}
	out := make([]zpair, 0, len(v.zset))
	for m, s := range v.zset {
		out = append(out, zpair{m, s})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score < out[j].score
		}
		return out[i].member < out[j].member
	})
	return out
}

// formatScore writes a score as Redis replies it: the shortest text that reads
// back as the same number.
func formatScore(s float64) string { return strconv.FormatFloat(s, 'f', -1, 64) }

// inBounds says a score is in [min, max] as Redis's ZRANGE BYSCORE reads the
// bounds: "-inf", "+inf", a number, or "(" and a number for an open end.
func inBounds(score float64, min, max string) bool {
	lo, loOpen, ok1 := parseBound(min)
	hi, hiOpen, ok2 := parseBound(max)
	if !ok1 || !ok2 {
		return false
	}
	if score < lo || loOpen && score == lo {
		return false
	}
	if score > hi || hiOpen && score == hi {
		return false
	}
	return true
}

func parseBound(s string) (v float64, open, ok bool) {
	if len(s) > 0 && s[0] == '(' {
		open, s = true, s[1:]
	}
	f, ok := parseScore(s)
	return f, open, ok
}

// ---- the sources of ids

// lineBody is a line of the log as a query reads it: the ids and `about` of
// its entry, and its meta, decoded from the stored body (L2 1.1).
type lineBody struct {
	Seq   string          `json:"seq"`
	Kind  string          `json:"kind"`
	IDs   []string        `json:"ids"`
	About []string        `json:"about"`
	Meta  json.RawMessage `json:"meta"`
}

// lineAt is L.read_line_at: the line at exactly seq, from the log twin. A seq
// the log does not have, past its tail, is LOGID with the budget log_line, as
// the store answers (table_set_log.lua's fetch_line; decision 12: LOGID when
// a history seq has no line).
func (e *qeval) lineAt(seq uint64) (lineBody, *Refusal) {
	e.c.Lines++
	item, ref := e.t.log.LineAt(e.t.prefix, e.epoch, strconv.FormatUint(seq, 10))
	if ref != nil {
		d := ref.Detail.RefusalDetail
		d.QueryIndex = nil // the query's own index is set where it is answered
		return lineBody{}, e.fail(ref.Code, d)
	}
	body, err := tset.ParseLogBody(item.D)
	if err != nil {
		return lineBody{}, e.fail(codeDrift, tset.RefusalDetail{})
	}
	return lineBody{Seq: string(item.Seq), Kind: tset.LogWords[body.K], IDs: body.IDs, About: body.About, Meta: body.Meta}, nil
}

// sourceIDs are the ids a source names: a list as it is, the first ids of an
// index or of a cell, or the ids (or the `about`) of a line from an offset.
// Named says the ids were taken from an index or a line, which must have
// records (an id a list names may have none).
func (e *qeval) sourceIDs(src sprint.IDSource, table string) (ids []string, named bool, ref *Refusal) {
	ids, named, _, ref = e.sourceIDsMore(src, table)
	return ids, named, ref
}

// sourceIDsMore is sourceIDs and whether a line has ids beyond the window read
// (sprint.Answer.MoreIDs); a list and an index head say none.
func (e *qeval) sourceIDsMore(src sprint.IDSource, table string) (ids []string, named, more bool, ref *Refusal) {
	switch src.Kind {
	case sprint.SourceIDs:
		return append([]string{}, src.IDs...), false, false, nil
	case sprint.SourceHead:
		if index, arg, ok := headKind(src.Key); ok {
			name := index
			if arg != "" {
				name = index + ":" + arg
			}
			ids, _, _, ref = e.rangeHead(e.key(name), "-inf", "+inf", src.Limit)
			return ids, index != indexMissing, false, ref
		}
		row, col, _ := cellOf(src.Key)
		ids, _, _, ref = e.cellIDs(table, row, col, src.Limit)
		return ids, true, false, ref
	case sprint.SourceLine:
		l, ref := e.lineAt(src.Seq)
		if ref != nil {
			return nil, false, false, ref
		}
		list, most := l.IDs, sprint.MaxLineIDs
		if src.About {
			list, most = l.About, sprint.MaxAboutIDs
		}
		offset := max(src.Offset, 0)
		n := max(most-offset, 0)
		if src.Limit > 0 && src.Limit < n {
			n = src.Limit
		}
		if offset > len(list) {
			offset = len(list)
		}
		end := min(len(list), offset+n)
		return append([]string{}, list[offset:end]...), true, end < len(list), nil
	}
	return nil, false, false, e.fail(codeDrift, tset.RefusalDetail{})
}

// quarantined says which of the ids are in {p}quarantine@e: one HMGET for each
// 2,000 ids, a set of the ones that are.
func (e *qeval) quarantined(ids []string) (map[string]bool, *Refusal) {
	out := map[string]bool{}
	for from := 0; from < len(ids); from += probeChunk {
		chunk := ids[from:min(from+probeChunk, len(ids))]
		vals, ref := e.hmget(e.key("quarantine"), chunk, quarantineEntryBytes)
		if ref != nil {
			return nil, ref
		}
		e.c.Work += len(chunk)
		for i, v := range vals {
			if v != nil {
				out[chunk[i]] = true
			}
		}
	}
	return out, nil
}

// leftOut collects the ids a query left out, once each, in the order they were
// left. Membership is a set, so collecting n ids is n steps and never n times
// n (a query can leave out every one of 20,000 ids); Work counts the steps.
type leftOut struct {
	list []string
	seen map[string]struct{}
}

// ids are the ids left out, never nil.
func (l *leftOut) ids() []string {
	if l.list == nil {
		return []string{}
	}
	return l.list
}

// leave is the ids without the quarantined ones, in order; the quarantined are
// added to left, once each.
func (e *qeval) leave(ids []string, left *leftOut) ([]string, *Refusal) {
	if len(ids) == 0 {
		return ids, nil
	}
	bad, ref := e.quarantined(ids)
	if ref != nil {
		return nil, ref
	}
	kept := make([]string, 0, len(ids))
	for _, id := range ids {
		e.c.Work++
		if !bad[id] {
			kept = append(kept, id)
			continue
		}
		if left.seen == nil {
			left.seen = map[string]struct{}{}
		}
		e.c.Work++
		if _, dup := left.seen[id]; !dup {
			left.seen[id] = struct{}{}
			left.list = append(left.list, id)
		}
	}
	return kept, nil
}

// present refuses an id taken from an index that has no record (MISSING) or a
// record of another epoch (MEMBEREPOCH): the lower layers' refusals of a card,
// which quarantine it (1.3.5).
func (e *qeval) present(table string, recs []Record) *Refusal {
	for _, r := range recs {
		if !r.Exists {
			return e.failIDs(codeMissing, table, r.ID)
		}
		if r.Epoch != e.epoch {
			return e.failIDs(codeMemberEpoch, table, r.ID)
		}
	}
	return nil
}

// ---- projections

// fieldUnion is the fields a read asks of a record: the projection, and the
// fields the follows derive their ids from.
func fieldUnion(fields, follow []string, extra ...string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(f string) {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	for _, f := range fields {
		add(f)
	}
	for _, f := range extra {
		add(f)
	}
	for _, f := range follow {
		switch f {
		case sprint.FollowWork, sprint.FollowWithdrawn:
			add(fieldAttempt)
		case sprint.FollowRCards:
			add(fieldRCards)
		case sprint.FollowNeeds:
			add(fieldNeeds)
		case sprint.FollowMember:
			add(fieldMember)
		case sprint.FollowPrimary:
			add(fieldPrimary)
		}
	}
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return out
}

// project is a record with only the fields of the projection: a read asks the
// store for more (the fields its follows derive from) and returns the
// projection.
func project(r Record, fields []string) Record {
	out := r
	out.Fields = make(map[string]tset.FieldValue, len(fields))
	for _, f := range fields {
		if v, ok := r.Fields[f]; ok {
			out.Fields[f] = v
		}
	}
	return out
}

func projectAll(recs []Record, fields []string) []Record {
	out := make([]Record, len(recs))
	for i, r := range recs {
		out[i] = project(r, fields)
	}
	return out
}

func recordField(r Record, name string) string {
	if v, ok := r.Fields[name]; ok && v.Present {
		return v.Value
	}
	return ""
}

// ---- the follows

// followTarget is a record a follow reads: its table and id.
type followTarget struct {
	table, id string
}

// followTargets are the records a follow reads from a record, from its own
// fields and place (1.0's table of follows), or a refusal when what it names
// is past a bound (DRIFT: needs are at most 64 a card and rcards 15).
func (e *qeval) followTargets(follow, table string, r Record) ([]followTarget, *Refusal) {
	switch follow {
	case sprint.FollowWork, sprint.FollowWithdrawn:
		v := recordField(r, fieldAttempt)
		if v == "" {
			return nil, nil
		}
		// A whole number of at most nine digits, as the Lua reads it.
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || len(v) > 9 || strings.Trim(v, "0123456789") != "" {
			return nil, e.failIDs(codeDrift, table, r.ID)
		}
		if n == 0 {
			return nil, nil // never dealt: no work card
		}
		return []followTarget{{sprint.Fleet, sprint.WorkCardID(r.ID, n)}}, nil
	case sprint.FollowRCards:
		ids := sprint.Split(recordField(r, fieldRCards))
		if len(ids) > followMaxRCards {
			return nil, e.failIDs(codeDrift, table, r.ID)
		}
		out := make([]followTarget, len(ids))
		for i, id := range ids {
			out[i] = followTarget{sprint.Readers, id}
		}
		return out, nil
	case sprint.FollowMerge:
		return []followTarget{{sprint.Merge, r.ID}}, nil
	case sprint.FollowControl:
		if r.Place == nil {
			return nil, nil
		}
		return []followTarget{{sprint.Merge, sprint.CtlID(r.Place.Row)}}, nil
	case sprint.FollowNeeds:
		ids := sprint.Split(recordField(r, fieldNeeds))
		if len(ids) > followMaxNeeds {
			return nil, e.failIDs(codeDrift, table, r.ID)
		}
		out := make([]followTarget, len(ids))
		for i, id := range ids {
			out[i] = followTarget{sprint.Work, id}
		}
		return out, nil
	case sprint.FollowMember:
		if m := recordField(r, fieldMember); m != "" {
			return []followTarget{{sprint.Fleet, sprint.CtlID(m)}}, nil
		}
	case sprint.FollowPrimary:
		if p := recordField(r, fieldPrimary); p != "" {
			return []followTarget{{sprint.Work, p}}, nil
		}
	}
	return nil, nil
}

// follows reads what the follows of a list of records (of one table, all
// existing) reach, and returns each record's Follows, aligned. The targets of
// every record are found first, quarantined ones left out together, and read a
// table at a time, so the quarantine costs one probe for each 2,000 targets.
// left collects the ids left out.
func (e *qeval) follows(table string, recs []Record, follow, fields []string, left *leftOut) ([]*Follows, *Refusal) {
	out := make([]*Follows, len(recs))
	if len(follow) == 0 {
		return out, nil
	}
	type slot struct {
		rec    int
		follow string
		target followTarget
	}
	var slots []slot
	var all []string
	for i, r := range recs {
		out[i] = &Follows{}
		for _, f := range follow {
			ts, ref := e.followTargets(f, table, r)
			if ref != nil {
				return nil, ref
			}
			for _, t := range ts {
				slots = append(slots, slot{i, f, t})
				all = append(all, t.id)
			}
		}
	}
	kept, ref := e.leave(all, left)
	if ref != nil {
		return nil, ref
	}
	keep := map[string]bool{}
	for _, id := range kept {
		keep[id] = true
	}
	// Read a table's targets in one go, in the order of the slots.
	read := map[string][]int{} // table -> slot indexes
	var order []string
	for i, s := range slots {
		if !keep[s.target.id] {
			continue
		}
		if _, ok := read[s.target.table]; !ok {
			order = append(order, s.target.table)
		}
		read[s.target.table] = append(read[s.target.table], i)
	}
	got := make([]Record, len(slots))
	for _, tbl := range order {
		idx := read[tbl]
		ids := make([]string, len(idx))
		for j, i := range idx {
			ids[j] = slots[i].target.id
		}
		rs, ref := e.records(tbl, ids, fields)
		if ref != nil {
			return nil, ref
		}
		for j, i := range idx {
			got[i] = rs[j]
		}
	}
	for i, s := range slots {
		if !keep[s.target.id] {
			continue
		}
		f, r := out[s.rec], got[i]
		switch s.follow {
		case sprint.FollowWork:
			if r.Exists && r.Place != nil && r.Place.Col != sprint.Withdrawn {
				f.Work = append(f.Work, r)
			}
		case sprint.FollowWithdrawn:
			if r.Exists && r.Place != nil && r.Place.Col == sprint.Withdrawn {
				f.Withdrawn = append(f.Withdrawn, r)
			}
		case sprint.FollowRCards:
			f.RCards = append(f.RCards, r)
		case sprint.FollowMerge:
			if r.Exists {
				f.Merge = append(f.Merge, r)
			}
		case sprint.FollowControl:
			if r.Exists {
				f.Control = append(f.Control, r)
			}
		case sprint.FollowNeeds:
			in, ref := e.zscore(e.key(sprint.IndexWait+":"+s.target.id), recs[s.rec].ID)
			if ref != nil {
				return nil, ref
			}
			f.Needs = append(f.Needs, NeedLink{ID: s.target.id, Record: r, InWait: in != nil})
		case sprint.FollowMember:
			if r.Exists {
				f.Member = append(f.Member, r)
			}
		case sprint.FollowPrimary:
			if r.Exists {
				f.Primary = append(f.Primary, r)
			}
		}
	}
	// The follows that probe a key of the sprint's: jopen, due and index.
	for i, r := range recs {
		for _, name := range follow {
			switch name {
			case sprint.FollowJOpen:
				n, ref := e.hlen(e.key("jopen:" + r.ID))
				if ref != nil {
					return nil, ref
				}
				out[i].JOpen = &JOpenCount{Count: n}
			case sprint.FollowDue:
				d, ref := e.dueOf(table, r)
				if ref != nil {
					return nil, ref
				}
				out[i].Due = d
			case sprint.FollowIndex:
				x, ref := e.indexOf(table, r)
				if ref != nil {
					return nil, ref
				}
				out[i].Index = x
			}
		}
	}
	return out, nil
}

// dueOf are a card's entries in the due set: the kinds of 1.2 whose state the
// card is in (sprint.DueKinds), by one ZMSCORE.
func (e *qeval) dueOf(table string, r Record) ([]DueEntry, *Refusal) {
	if r.Place == nil {
		return nil, nil
	}
	var members []string
	for _, k := range sprint.DueKinds {
		if k.Table != table || k.Col != r.Place.Col {
			continue
		}
		id := r.ID
		if k.OfRow {
			id = r.Place.Row
		}
		members = append(members, k.Kind+":"+id)
	}
	if len(members) == 0 {
		return nil, nil
	}
	scores, ref := e.zscores(e.key("due"), members)
	if ref != nil {
		return nil, ref
	}
	var out []DueEntry
	for i, s := range scores {
		if s != nil {
			out = append(out, DueEntry{Key: members[i], Score: *s})
		}
	}
	return out, nil
}

// indexOf are the indexes of a work card that it is a member of (1.3.1): the
// ones its place could put it in (sprint.IndexDefs), and askwait for a card in
// review.
func (e *qeval) indexOf(table string, r Record) ([]IndexEntry, *Refusal) {
	if table != sprint.Work || r.Place == nil {
		return nil, nil
	}
	var out []IndexEntry
	for _, d := range sprint.IndexDefs {
		if d.Table != table || d.Col != r.Place.Col {
			continue
		}
		name := d.Index + ":" + r.Place.Row
		s, ref := e.zscore(e.key(name), r.ID)
		if ref != nil {
			return nil, ref
		}
		if s != nil {
			out = append(out, IndexEntry{Key: name, Score: *s})
		}
	}
	if r.Place.Col == sprint.Review {
		s, ref := e.zscore(e.key(indexAskWait), r.ID)
		if ref != nil {
			return nil, ref
		}
		if s != nil {
			out = append(out, IndexEntry{Key: indexAskWait, Score: *s})
		}
	}
	return out, nil
}

// ---- the twin's entry points

// Query answers one composite query from one snapshot of the twin, at its
// active epoch and under one time: what ns_sprint_read answers for the same
// query (errata E3's signature). Its Answer is what IT05's sprint.Answer holds
// of the result; QueryFull returns the whole result. A refusal is the
// returned error (a *Refusal), and nothing was read past it: a query is
// complete or refused. It reads and never writes.
func (t *Twin) Query(q sprint.SprintQ) (sprint.Answer, error) {
	res, _, err := t.QueryFull(q)
	if err != nil {
		return sprint.Answer{}, err
	}
	return res.Project(q), nil
}

// QueryFull is Query with the whole result, which IT05's Answer cannot hold,
// and what the query read (QueryCharge), which a test holds to its declared
// cost.
func (t *Twin) QueryFull(q sprint.SprintQ) (QueryResult, QueryCharge, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.broken != nil {
		return nil, QueryCharge{}, t.broken
	}
	if ref := ValidateSprintQ(q); ref != nil {
		return nil, QueryCharge{}, ref
	}
	_, nowMS := t.begin()
	e := t.newEval(t.active, nowMS)
	res, ref := e.evalComposite(q)
	if ref == nil {
		ref = e.fits(res)
	}
	if ref != nil {
		return nil, e.c, ref
	}
	return res, e.c, nil
}

// KeyQuery answers one sprint-key read, as QueryFull does a composite query.
func (t *Twin) KeyQuery(q KeyQ) (QueryResult, QueryCharge, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.broken != nil {
		return nil, QueryCharge{}, t.broken
	}
	if ref := ValidateKeyQ(q); ref != nil {
		return nil, QueryCharge{}, ref
	}
	_, nowMS := t.begin()
	e := t.newEval(t.active, nowMS)
	res, ref := e.evalKey(q)
	if ref == nil {
		ref = e.fits(res)
	}
	if ref != nil {
		return nil, e.c, ref
	}
	return res, e.c, nil
}

// fits holds a result to the reply bound of one read (L1 7: 8 MiB of reply).
func (e *qeval) fits(res QueryResult) *Refusal {
	b, err := json.Marshal(res)
	if err != nil {
		return e.fail(codeDrift, tset.RefusalDetail{})
	}
	if len(b) > tset.MaxReadReplyBytes {
		return e.over("encoded_reply", len(b), tset.MaxReadReplyBytes)
	}
	return nil
}

// evalWire answers a query as ns_sprint_read receives it: its wire object,
// decoded and checked as the Lua's validate checks it, then read.
func (e *qeval) evalWire(q SprintQuery) (QueryResult, *Refusal) {
	for _, k := range CompositeKinds {
		if k == q.Kind {
			sq, ref := DecodeSprintQ(q)
			if ref != nil {
				return nil, ref
			}
			return e.evalComposite(sq)
		}
	}
	for _, k := range SprintKeyKinds {
		if k == q.Kind {
			kq, ref := DecodeKeyQ(q)
			if ref != nil {
				return nil, ref
			}
			return e.evalKey(kq)
		}
	}
	return nil, queryRequestRefusal()
}

// UseQueries installs the sprint's queries as the twin's Query phase, so that a
// ReadRequest's Sprint queries are answered: each in the read's snapshot and
// time, from its wire object to its JSON answer, as the store's function
// answers them. It installs three hooks of Phases (IT12's core has no twin to
// hand a phase, so the adapter is a method the caller calls once after
// NewTwin; a twin without it refuses a sprint query REQUEST, as IT12 built it):
//
//   - QueryCheck, the static phase: every sprint query of a read is validated
//     before TIME and before any Layer 1 or Layer 2 query runs, as the store's
//     S.validate runs every validate before it reads anything (errata E6), so a
//     malformed query is REQUEST at its index whatever an earlier query would
//     have found (MISSING, NOTABLE) by reading;
//   - QueryStart, which begins the read's one budget with what its Layer 1
//     and Layer 2 queries charged (L1 6, 7: records, range ids and probes are
//     counted for the whole read, not for each query);
//   - Query, which answers a query that passed the check, reading only.
func (t *Twin) UseQueries() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.phases.QueryCheck = checkWire
	t.phases.QueryStart = t.queryStart
	t.phases.Query = t.queryPhase
}

// checkWire is the pure validation of one sprint query as ns_sprint_read
// receives it: its wire object decoded and checked exactly as the Lua kind's
// validate(q, index) checks it. A kind that is no sprint kind is REQUEST.
func checkWire(q SprintQuery) *Refusal {
	for _, k := range CompositeKinds {
		if k == q.Kind {
			_, ref := DecodeSprintQ(q)
			return ref
		}
	}
	for _, k := range SprintKeyKinds {
		if k == q.Kind {
			_, ref := DecodeKeyQ(q)
			return ref
		}
	}
	return queryRequestRefusal()
}

// sprintRead is the budget of one read's sprint queries: what the read's other
// queries and its earlier sprint queries have charged, carried from query to
// query (the store keeps one ctx.budget for a whole read).
type sprintRead struct {
	st *State
	c  QueryCharge
}

// readCounters is the part of a Layer 1 or Layer 2 reply's counters that a
// sprint query's own charges are added to.
type readCounters struct {
	Record  int `json:"record"`
	RangeID int `json:"range_id"`
	Cell    int `json:"cell"`
}

// addCounters adds the counters of one reply (the Mem's `counters`) to what the
// read has charged so far: a reply with none (the log twin's) adds nothing.
func (c *QueryCharge) addCounters(raw json.RawMessage) {
	var rc readCounters
	if len(raw) == 0 || json.Unmarshal(raw, &rc) != nil {
		return
	}
	c.Records += rc.Record
	c.RangeIDs += rc.RangeID
	c.Probes += rc.Cell
}

// queryStart begins the sprint queries of one read over what the rest of it
// charged. The twin calls it inside its read, which holds the lock.
func (t *Twin) queryStart(st *State, charged QueryCharge) {
	t.sprintRead = &sprintRead{st: st, c: charged}
}

// queryPhase is the Query phase of a twin that has UseQueries: it answers a
// query the static phase accepted, from the state, charging the read's budget.
// It runs inside the twin's read, which holds the lock, so it reads the twin's
// state and never calls a method that locks.
func (t *Twin) queryPhase(st *State, q SprintQuery) (json.RawMessage, *Refusal) {
	e := t.newEval(st.Epoch, st.NowMS)
	shared := t.sprintRead != nil && t.sprintRead.st == st
	if shared {
		e.c = t.sprintRead.c
	}
	res, ref := e.evalWire(q)
	if ref == nil {
		ref = e.fits(res)
	}
	if shared {
		t.sprintRead.c = e.c
	}
	if ref != nil {
		return nil, ref
	}
	b, err := json.Marshal(res)
	if err != nil {
		return nil, e.fail(codeDrift, tset.RefusalDetail{})
	}
	return b, nil
}

// ---- the probes a query declares

// Layer 1's checked probe charges a cell for every name an HMGET or a ZMSCORE
// asks for (S.read_probe: count - 3 more than the command's own cell) and one
// for any other probe, so the probes a query declares are names and commands,
// not commands: an id left out or looked up is a probe.

// maxTableColumns is the columns of a table (L1 1.2: at most 32), which bounds
// the cells a listing counts for each row.
const maxTableColumns = 32

// maxQueryProps is the most table properties a fleet or readers query names:
// a table's (L1 contract amendment, table properties).
const maxQueryProps = 64

// sourceSize is how many ids a source names at most, and the probes that find
// them: a list costs none, a head one range read, a line one line.
func sourceSize(src sprint.IDSource) (n, probes int) {
	switch src.Kind {
	case sprint.SourceIDs:
		return len(src.IDs), 0
	case sprint.SourceHead:
		if _, _, ok := headKind(src.Key); !ok {
			return src.Limit, 2 // a cell: its row is probed, then its range
		}
		return src.Limit, 1
	case sprint.SourceLine:
		most := sprint.MaxLineIDs
		if src.About {
			most = sprint.MaxAboutIDs
		}
		offset := max(src.Offset, 0)
		n := max(most-offset, 0)
		if src.Limit > 0 && src.Limit < n {
			n = src.Limit
		}
		return n, 0
	}
	return 0, 0
}

// followProbes are the most probes the follows make for one record, and the
// most records they name besides it (whose quarantine is probed, a name each):
// a need costs a probe for its wait:n, jopen one (its HLEN), due one (a card's
// kinds share a ZMSCORE, and a card is in one state, so one name) and index at
// most two (a card's place is in at most two definitions, and askwait is read
// for a card in review).
func followProbes(follow []string) (perRecord, targets int) {
	for _, f := range follow {
		switch f {
		case sprint.FollowWork, sprint.FollowWithdrawn, sprint.FollowMerge, sprint.FollowControl, sprint.FollowMember, sprint.FollowPrimary:
			targets++
		case sprint.FollowRCards:
			targets += followMaxRCards
		case sprint.FollowNeeds:
			targets += followMaxNeeds
			perRecord += followMaxNeeds
		case sprint.FollowJOpen, sprint.FollowDue:
			perRecord++
		case sprint.FollowIndex:
			perRecord += 2
		}
	}
	return perRecord, targets
}

// QueryProbes is the most cell and key probes a composite query may make, from
// its arguments alone, as QueryCost is the most records and range ids: the
// probes beside the records in the design's cost table (errata 1, the
// addendum). The twin's own count is held to it (TestQueryCostHolds), and the
// Lua makes the same probes.
func QueryProbes(q sprint.SprintQ) int {
	return queryProbes(q) + extensionProbes(q)
}

// extensionProbes are the probes of IT08's extensions of a query: a name for
// each stream the dropping marks are read of (the most a query reaches: its
// stream, the waiters it may return, or the streams it lists), one for
// {p}next@e.streams, one for each jopen key's field, and a ZCARD for each cell
// a `streams` query counts.
func extensionProbes(q sprint.SprintQ) int {
	total := 0
	for _, k := range q.Keys {
		switch k {
		case sprint.KeyDropping:
			switch q.Kind {
			case sprint.QueryFront:
				total++
			case sprint.QueryWaiters:
				n, _ := sourceSize(q.Source)
				total += n * q.Limit
			case sprint.QueryStreams:
				total += unitsOf(q)
			}
		case sprint.KeyNextStreams, sprint.KeyJOpenG, sprint.KeyJOpenSprint:
			total++
		}
	}
	if q.Kind == sprint.QueryStreams {
		total += unitsOf(q) * len(q.Counts)
	}
	return total
}

// queryProbes are the probes of the query itself, as 1.0 has it.
func queryProbes(q sprint.SprintQ) int {
	n, found := sourceSize(q.Source)
	switch q.Kind {
	case sprint.QueryRelated:
		// the source's own probes, the quarantine of its ids and of the ids the
		// follows name, and what each follow probes of each record
		per, targets := followProbes(q.Follow)
		return found + n + n*targets + n*per
	case sprint.QueryFront:
		total := 1 + 1 + 1 + len(openCells) // sigma's head, its quarantine, its row, its five counts
		for _, h := range q.Heads {
			per, targets := followProbes(h.Follow)
			total += 1 + h.Limit + h.Limit*targets + h.Limit*per
		}
		return total
	case sprint.QueryWaiters:
		// the quarantine and the missing score of each id, and for each the head of
		// its wait:n and the quarantine of the ids in it
		return found + 2*n + n*(1+q.Limit)
	case sprint.QueryStreams:
		// the rows' head; the quarantine of the control cards and of the need cards;
		// a stuck cell's row and range and the quarantine of its ids, for each stream
		return 1 + 2*unitsOf(q) + unitsOf(q)*(2+q.Limit)
	case sprint.QueryFleet, sprint.QueryReaders:
		// the rows' head, the quarantine of the control cards, a cell's count
		// for each column of each row, and a cell for each property named (one
		// HMGET of the table's property hash)
		return 1 + unitsOf(q) + unitsOf(q)*maxTableColumns + len(q.Props)
	case sprint.QueryNeedchain:
		// the quarantine of the source's ids, and of every need of every card
		// read; for each card read its place read, and the quarantine of the ids
		// the place reads return (at most the limit in all)
		return found + n + q.Limit*followMaxNeeds + 2*q.Limit
	case sprint.QueryJnote:
		// for each note the quarantine of its subjects, and for each an HLEN and an
		// HMGET of its own field
		s := q.Subjects
		if s <= 0 {
			s = sprint.MaxAboutIDs
		}
		return found + n*3*s
	}
	return queryMaxProbes
}

// KeyProbes is the most probes a sprint-key read makes: an HMGET of a hash's
// fixed fields is a probe for each field, an HLEN with it for the hashes whose
// names the query gives, a ZMSCORE a probe for each id, and the due count the
// clock's HMGET with a ZCOUNT.
func KeyProbes(q KeyQ) int {
	switch q.Kind {
	case KeyClock:
		return len(ClockFieldNames)
	case KeyLease:
		return len(LeaseFieldNames)
	case KeyTick:
		return len(TickFieldNames)
	case KeyHeartbeat:
		return len(HeartbeatReadFields)
	case KeyDueCount:
		return len(ClockFieldNames) + 1
	case KeyDropping:
		return 1 + len(q.Streams)
	case KeyParked:
		return 1 + len(q.Keys)
	case KeyMissing, KeyBeat:
		return len(q.IDs)
	case KeyJOpen:
		return len(q.Subjects) * (1 + len(q.Names))
	case KeyNext:
		return len(q.Names)
	}
	return queryMaxProbes
}

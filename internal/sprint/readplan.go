package sprint

import (
	"errors"
	"fmt"
	"slices"
	"sort"
)

// Read plans, the second part of IT05 (the upper design, version 2.1, sections
// 1.0, 1.4.2, 1.5.1 and 8.1): what a rule or a verb asks the store to read in
// one call, and what that costs, so that a read is sized before it is sent and
// never refused BUDGET. Nothing here reads a store: a plan is data, its cost is
// arithmetic on the tables below, and Split cuts a plan into plans that each
// fit layer 1's bounds.
//
// Every number is a named constant with the section it comes from. Every
// mapping is a table (the follows, the kinds of query and what a source of ids
// is), so a change to the design is a changed row.
//
// What the design leaves open, and what this file takes (each is listed in the
// pull request):
//
//	the shapes of RangeQ, CountQ, RCountQ, LinesQ and SprintQ: 8.0 names the
//	types and gives no fields; the fields are layer 1's query words (L1 7) and
//	the composite queries' arguments (1.0).
//	front's cost: the table of 1.0 says "1 + the limits, times the follows";
//	here each head has its own follow, and a head's records cost one plus its
//	follows, so that R6's read is 1 + 3L records a stream, as 1.4.2 says.
//	bytes: the design gives no bytes a record; the constants below are an
//	over-estimate from 1.8's measured 976 bytes for a whole record.

// Layer 1's read bounds (L1 6, AL4), the values a read is planned within.
const (
	// MaxReadQueries is the queries in one read (AL4).
	MaxReadQueries = 1024
	// MaxReadRecords is the records a read may request or return, counting
	// occurrences, across id queries and ranges with records (L1 6).
	MaxReadRecords = 10000
	// MaxReadRangeIDs is the range ids a read may return, across its queries (L1 6).
	MaxReadRangeIDs = 20000
	// MaxReadBytes is the raw store payload a read may fetch (L1 6).
	MaxReadBytes = 8 << 20
)

// L1ReadBounds are layer 1's bounds on one read, the largest ReadBounds a
// tick may plan with.
func L1ReadBounds() ReadBounds {
	return ReadBounds{Queries: MaxReadQueries, Records: MaxReadRecords, RangeIDs: MaxReadRangeIDs, Bytes: MaxReadBytes}
}

// The sizes a query is planned against where its answer's size depends on the
// store's state, each the design's own bound for it.
const (
	// MaxRangeLimit is the most ids a range returns (L1 7: limit at most 2,000).
	MaxRangeLimit = 2000
	// MaxLineIDs is the ids in one generated line (1.0: one line, 2,000 ids).
	MaxLineIDs = 2000
	// MaxAboutIDs is the ids a note line's `about` may name, before dedup (1.0:
	// notes and `about` ids, 100 and 4,000).
	MaxAboutIDs = 4000
	// MaxLineBytes is the bytes of one generated line (1.0: one line, 1 MiB).
	MaxLineBytes = 1 << 20
	// MaxStreams is the streams of a sprint (0.1, F1-20: members and streams
	// together at most 250).
	MaxStreams = 250
	// MaxMembers is the fleet members of a sprint (0.1, F1-20).
	MaxMembers = 250
	// MaxReaders is the readers of a sprint (0.1, F1-20: members + readers +
	// 2 x streams at most 1,024).
	MaxReaders = 1024
)

// The bytes a read's answer is estimated to carry. The design gives none
// (IT17 measures a busy tick's bytes at the far store), so these are
// over-estimates, and an over-estimate cuts a read smaller and never refuses
// it.
const (
	// WholeRecordBytes is a record read with no projection (1.8: 976 bytes,
	// measured, tracer 4).
	WholeRecordBytes = 976
	// RecordEnvelopeBytes is what a projected record carries besides its
	// fields: id, place, score and revision (estimated).
	RecordEnvelopeBytes = 128
	// FieldBytes is one projected field's name and value (estimated: a counter,
	// a stamp or a short status).
	FieldBytes = 32
	// RangeIDBytes is one range id and its score (an id is at most 128 bytes,
	// ValidID, and a score at most 24; estimated).
	RangeIDBytes = 152
	// CountBytes is one count in an answer (estimated).
	CountBytes = 24
)

// recordBytes is the bytes of one record read with the projection: a nil list
// is the whole record, and a list with no field (not nil) is the summary, which
// is the envelope alone.
func recordBytes(fields []string) int {
	if fields == nil {
		return WholeRecordBytes
	}
	return RecordEnvelopeBytes + FieldBytes*len(fields)
}

// Add is the cost of both.
func (c Cost) Add(o Cost) Cost {
	return Cost{Records: c.Records + o.Records, RangeIDs: c.RangeIDs + o.RangeIDs, Bytes: c.Bytes + o.Bytes}
}

// scale is the cost of n of them.
func (c Cost) scale(n int) Cost {
	return Cost{Records: c.Records * n, RangeIDs: c.RangeIDs * n, Bytes: c.Bytes * n}
}

// The follows of `related` and `front` (1.0): the records reached from a
// record, and what each adds to a record's cost. A follow not in the table
// adds one.
const (
	FollowWork      = "work"
	FollowWithdrawn = "withdrawn"
	FollowRCards    = "rcards"
	FollowMerge     = "merge"
	FollowControl   = "control"
	FollowNeeds     = "needs"
	FollowMember    = "member"
	FollowJOpen     = "jopen"
	FollowDue       = "due"
	FollowIndex     = "index"
)

// Follows are the follows of 1.0, in its order.
var Follows = []string{FollowWork, FollowWithdrawn, FollowRCards, FollowMerge, FollowControl, FollowNeeds, FollowMember, FollowJOpen, FollowDue, FollowIndex}

// followCosts are the records a follow adds to a record: the read cards of a
// primary are at most 15 (1.3.1, `rcards`), and a card names at most 64
// needs (1.0); every other follow reaches one record.
var followCosts = map[string]int{
	FollowRCards: 15,
	FollowNeeds:  64,
}

// followTables are the tables whose cards of a primary a follow reads: the read
// cards are in the readers' table, the merge card in the merge table, and the
// work card in the fleet's (a member's cells), which 1.0 gives as two follows:
// `work`, the live work card, and `withdrawn`, the same card when it is
// withdrawn. They are disjoint states of one card, so the fleet's cards of a
// primary are all read only when both were followed (followsInto, Table.Of): a
// withdrawn follow alone does not read the live card, and a work follow alone
// does not read the withdrawn one. A follow not here reaches no card that
// Table.Of answers for.
var followTables = map[string]string{
	FollowRCards:    Readers,
	FollowMerge:     Merge,
	FollowWork:      Fleet,
	FollowWithdrawn: Fleet,
}

// followsInto are the follows that read a table's cards of a primary, by table
// (followTables turned around, in the order of Follows): Table.Of answers for a
// primary when every one of them was read of it.
var followsInto = func() map[string][]string {
	out := map[string][]string{}
	for _, f := range Follows {
		if t, ok := followTables[f]; ok {
			out[t] = append(out[t], f)
		}
	}
	return out
}()

// followCostDefault is what a follow not in followCosts adds (1.0: 1 a follow).
const followCostDefault = 1

// followCost is the records a set of follows adds to one record.
func followCost(follow []string) int {
	n := 0
	for _, f := range follow {
		if c, ok := followCosts[f]; ok {
			n += c
		} else {
			n += followCostDefault
		}
	}
	return n
}

// The composite queries of 1.0.
const (
	QueryRelated   = "related"
	QueryFront     = "front"
	QueryWaiters   = "waiters"
	QueryStreams   = "streams"
	QueryFleet     = "fleet"
	QueryReaders   = "readers"
	QueryNeedchain = "needchain"
	QueryJnote     = "jnote"
)

// The kinds of id source (1.0): a list of ids, an index or cell head with a
// limit, or a line by seq (its ids, or its `about`).
const (
	SourceIDs  = "ids"
	SourceHead = "head"
	SourceLine = "line"
)

// The heads of `front(s)` (1.0): the indexes of s it reads the first ids of.
const (
	HeadEligBelow  = "elig"        // elig:s below sigma
	HeadFreshBelow = "fresh-below" // fresh:s below sigma
	HeadFreshAbove = "fresh-above" // fresh:s above sigma
	HeadAgain      = "again"       // again:s
)

// RangeQ is layer 1's range query (L1 7): the first Limit ids, with their
// scores, of one cell of a table or of one sorted set of the sprint, and with
// Records their records.
type RangeQ struct {
	// Table and Cell name a cell, "<row>:<col>" (split at the last colon); Key
	// instead names a sorted set of the sprint (an index, the agenda). Exactly
	// one form.
	Table, Cell, Key string
	// Min and Max are score bounds in Redis's grammar ("(5" exclusive, "-inf");
	// empty is unbounded.
	Min, Max string
	// Limit is the most ids returned (at most MaxRangeLimit).
	Limit int
	// Desc reads from the highest score; Records asks for the records of a
	// cell's members, with Fields their projection (nil: whole records; a list
	// that is not nil and has no field: the summary, no field of a record).
	Desc, Records bool
	Fields        []string
}

// CountQ is layer 1's count query (L1 7): the number of members of each cell.
type CountQ struct {
	Table string
	Cells []string // "<row>:<col>", in the order the answer gives its counts
}

// RCountQ is layer 1's rcount query (L1 7): the members of each cell with a
// score in [Min, Max], and their sum.
type RCountQ struct {
	Table    string
	Cells    []string
	Min, Max string
}

// LinesQ is layer 2's atomic lines query (L2 4): the lines after seq After up
// to seq Through (0: no upper seq), at most Limit of them. A rule that needs
// one line reads it with After = seq - 1, Through = seq, Limit = 1 (1.0).
type LinesQ struct {
	After, Through uint64
	Limit          int
}

// IDSource is where a composite query takes the ids it reads (1.0).
type IDSource struct {
	// Kind is SourceIDs, SourceHead or SourceLine.
	Kind string
	// IDs are the ids of SourceIDs.
	IDs []string
	// Key and Limit are the head of SourceHead: an index or cell, and how many
	// of its first ids.
	Key   string
	Limit int
	// Seq is the line of SourceLine; About says its `about` and not its ids.
	Seq   uint64
	About bool
	// Offset and Limit are the part of a line that SourceLine reads: the ids
	// after the first Offset of them (the `+offset` of a key cut across ticks),
	// at most Limit of them; a Limit of 0 or less is every id the line may hold
	// from Offset. A query over a line is costed by the ids it reads, so a rule
	// cuts it by lowering Limit (Split, IDSource.Halved) and reads the rest
	// from a later Offset.
	Offset int
}

// lineWindow is the ids of a line that a SourceLine reads: from Offset, at most
// Limit of them (every one a line may hold when Limit is not above 0), and no
// more than a line holds (MaxLineIDs, or MaxAboutIDs for its `about`): the
// line's ids from offset to offset + n. The most a line holds is what the
// design bounds one generated line by, so a line with fewer ids answers fewer.
func (src IDSource) lineWindow() (offset, n int) {
	most := MaxLineIDs
	if src.About {
		most = MaxAboutIDs
	}
	offset = max(src.Offset, 0)
	n = max(most-offset, 0)
	if src.Limit > 0 && src.Limit < n {
		n = src.Limit
	}
	return offset, n
}

// size is how many ids the source may name, and how many range ids it reads to
// find them: the ids of a list cost no range read, a head reads its limit, and
// a line names at most the ids of its window (lineWindow).
func (src IDSource) size() (ids, rangeIDs int) {
	switch src.Kind {
	case SourceIDs:
		return len(src.IDs), 0
	case SourceHead:
		return src.Limit, src.Limit
	case SourceLine:
		_, n := src.lineWindow()
		return n, 0
	}
	return 0, 0
}

// Halved is the source after halvings halvings of what it reads (1.4.2, 1.3.5:
// a rule's read after a BUDGET or a LIMIT): a head's Limit, and a line's window
// (from what the line may hold from Offset when its Limit is not above 0),
// each halved by Halved and never below one id. A list of ids is the caller's
// to part (1.5.4) and is as it was, and so is a source when halvings is 0 or
// less, or when a line has nothing to read from its Offset.
func (src IDSource) Halved(halvings int) IDSource {
	if halvings <= 0 {
		return src
	}
	switch src.Kind {
	case SourceHead:
		src.Limit = Halved(src.Limit, halvings)
	case SourceLine:
		if _, n := src.lineWindow(); n > 0 {
			src.Limit = Halved(n, halvings)
		}
	}
	return src
}

// HeadQ is one head `front(s)` reads: an index of s, how many of its first
// ids, and the follows of their records.
type HeadQ struct {
	// Index is HeadEligBelow, HeadFreshBelow, HeadFreshAbove or HeadAgain.
	Index  string
	Limit  int
	Follow []string
}

// SprintQ is one of the sprint's composite queries (1.0), evaluated in Lua in
// the read's one snapshot and charged to its shared budget.
type SprintQ struct {
	// Kind is one of the Query constants.
	Kind string
	// Table is the table of the ids of `related`.
	Table string
	// Source is where `related`, `waiters`, `needchain` and `jnote` take their
	// ids (the notes, for `jnote`).
	Source IDSource
	// Stream is the stream of `front`.
	Stream string
	// Fields is the projection of the records returned (nil: whole records; a
	// list that is not nil and has no field: the summary, no field of a record).
	Fields []string
	// Follow is what `related` follows from each id's record.
	Follow []string
	// Heads are what `front` reads besides its sentinel.
	Heads []HeadQ
	// Limit is `waiters`' head of wait:n, `needchain`'s most records and
	// `streams`' stuck ids a stream.
	Limit int
	// Units bounds how many streams, members or readers `streams`, `fleet` and
	// `readers` are over; 0 is the design's most (MaxStreams, MaxMembers,
	// MaxReaders). Subjects bounds the subjects a note of `jnote` has; 0 is
	// MaxAboutIDs.
	Units, Subjects int
	// Keys are the sprint keys the query also reads, in the one snapshot (the
	// Key constants, rules_position_read.go): a plan that reads a sprint key
	// its query did not name is refused as it is for a cell it did not load
	// (Snapshot.Unloaded). Counts are the columns of the work table whose count
	// a `streams` query gives for every stream it lists (the rows are the
	// query's own, AL3).
	Keys, Counts []string
	// WaiterOffset is where `waiters` starts the head of wait:n, for a source
	// of one id: after the first WaiterOffset waiters. Missing says the head of
	// wait:n is read only for the ids that have a score in {p}missing@e (a made
	// need, 2.3 R4).
	WaiterOffset int
	Missing      bool
}

// ReadPlan is what one read asks the store (1.5.1): the records of ids by
// table, ranges, counts, rcounts, lines, and the sprint's composite queries,
// answered together from one snapshot with the store's time. IDs names a
// table's logical name (Work, Readers, Merge, Fleet).
type ReadPlan struct {
	IDs     map[string][]string
	Ranges  []RangeQ
	Counts  []CountQ
	RCounts []RCountQ
	Lines   []LinesQ
	Sprint  []SprintQ
}

// ErrBadPlan is the refusal of a read plan that cannot be read into a partial
// snapshot with its guards whole: a plan is refused when a rule builds it
// (Validate) and again when its answer is loaded (LoadPartial), and nothing is
// read for it.
var ErrBadPlan = errors.New("the read plan cannot be loaded as a partial snapshot")

// Validate refuses a plan whose answer could not be read back without a read
// going silent. A `related` query with a follow that reaches the cards Table.Of
// finds by their primary field (followTables) must read that field: its fields
// are nil (the whole record) or name PrimaryField. Left out, a store that
// honours the projection returns the follow's cards without it, and Of would
// find none of them and say nothing. The error is ErrBadPlan, and names the
// query, its follow and the field.
func (rp ReadPlan) Validate() error {
	for i, q := range rp.Sprint {
		if q.Kind != QueryRelated || q.Fields == nil || slices.Contains(q.Fields, PrimaryField) {
			continue
		}
		for _, f := range q.Follow {
			if _, opens := followTables[f]; opens {
				return fmt.Errorf("%w: composite query %d (%s) follows %s, which Table.Of finds by the field %s, and its fields %q leave that field out",
					ErrBadPlan, i, q.Kind, f, PrimaryField, q.Fields)
			}
		}
	}
	return nil
}

// queryCosts are the composite queries' costs (1.0's table), one row a kind:
// what a query of that kind may cost, from its arguments alone.
var queryCosts = map[string]func(q SprintQ) Cost{
	// 1, plus 1 a follow, 15 for rcards and 64 for needs, an id.
	QueryRelated: func(q SprintQ) Cost {
		n, ranged := q.Source.size()
		return recordsCost(n*(1+followCost(q.Follow)), ranged, q.Fields)
	},
	// 1 (sigma's card) + the limits, each head's records with its follows.
	QueryFront: func(q SprintQ) Cost {
		records, ranged := 1, 1
		for _, h := range q.Heads {
			records += h.Limit * (1 + followCost(h.Follow))
			ranged += h.Limit
		}
		return recordsCost(records, ranged, q.Fields)
	},
	// 1 + lim an id of the source: n's place and the head of wait:n.
	QueryWaiters: func(q SprintQ) Cost {
		n, ranged := q.Source.size()
		return recordsCost(n*(1+q.Limit), ranged+n*q.Limit, q.Fields)
	},
	// 2 a stream (its control card and the card it needs), plus stuck range
	// ids a stream.
	QueryStreams: func(q SprintQ) Cost {
		s := q.units()
		return recordsCost(2*s, s*q.Limit, q.Fields)
	},
	// 1 a member, and 1 a reader.
	QueryFleet: func(q SprintQ) Cost {
		return recordsCost(q.units(), 0, q.Fields)
	},
	QueryReaders: func(q SprintQ) Cost {
		return recordsCost(q.units(), 0, q.Fields)
	},
	// up to max records.
	QueryNeedchain: func(q SprintQ) Cost {
		_, ranged := q.Source.size()
		return recordsCost(q.Limit, ranged, q.Fields)
	},
	// 1 + subjects a note.
	QueryJnote: func(q SprintQ) Cost {
		n, ranged := q.Source.size()
		return recordsCost(n*(1+unitsOr(q.Subjects, MaxAboutIDs)), ranged, q.Fields)
	},
}

// unitsOr is n, or the design's most when n is 0.
func unitsOr(n, most int) int {
	if n > 0 {
		return n
	}
	return most
}

// listingMost is the design's most of what a listing query is over (0.1, F1-20):
// the streams of `streams`, the members of `fleet` and the readers of `readers`.
var listingMost = map[string]int{QueryStreams: MaxStreams, QueryFleet: MaxMembers, QueryReaders: MaxReaders}

// units is how many streams, members or readers a listing query is over, and so
// the most rows it may return: its Units, or the design's most when it has none.
// A query that is not a listing has none.
func (q SprintQ) units() int { return unitsOr(q.Units, listingMost[q.Kind]) }

// recordsCost is a query that returns that many records and reads that many
// range ids.
func recordsCost(records, rangeIDs int, fields []string) Cost {
	return Cost{Records: records, RangeIDs: rangeIDs, Bytes: records*recordBytes(fields) + rangeIDs*RangeIDBytes}
}

// QueryCost is what the query may cost (1.0), from its arguments alone, so a
// read is sized before it is sent. A query of a kind the table does not know is
// charged a whole read's bounds: no read holds it beside another, and the store
// refuses a plan that names it.
func QueryCost(q SprintQ) Cost {
	if f, ok := queryCosts[q.Kind]; ok {
		return f(q).Add(sprintKeysCost(q))
	}
	return Cost{Records: MaxReadRecords, RangeIDs: MaxReadRangeIDs, Bytes: MaxReadBytes}
}

// rangeCost is a range's cost: its limit of ids, and the same of records when
// it asks for them.
func rangeCost(q RangeQ) Cost {
	n := q.Limit
	if n <= 0 {
		n = MaxRangeLimit
	}
	c := Cost{RangeIDs: n, Bytes: n * RangeIDBytes}
	if q.Records {
		c.Records = n
		c.Bytes += n * recordBytes(q.Fields)
	}
	return c
}

// linesCost is a lines query's cost: its lines, each at most a line's bytes. A
// limit under one is one line.
func linesCost(q LinesQ) Cost {
	n := q.Limit
	if n < 1 {
		n = 1
	}
	return Cost{Bytes: n * MaxLineBytes}
}

// TsetSlot is one Layer 1 or Layer 2 query of a plan as its answer's place in
// ReadAnswer.Tset.
type TsetSlot struct {
	// Kind is the answer's kind (AnswerIDs, AnswerRange, AnswerCount,
	// AnswerRCount or AnswerLines).
	Kind string
	// Table is the table of an ids query; empty for the others.
	Table string
	// Index is the place of the query in the plan's Ranges, Counts, RCounts or
	// Lines; 0 for an ids query.
	Index int
}

// TsetSlots are the plan's Layer 1 and Layer 2 queries in the order their
// answers are in ReadAnswer.Tset: one ids query for each table that names an
// id, by the table's name; then the plan's ranges, counts, rcounts and lines,
// each in the plan's order. The sprint's own queries are not here: they are
// answered in ReadAnswer.Sprint, one for one.
//
// This order is IT05's own choice. The errata to version 2.1 (E3) say only that
// Tset is "aligned with the plan's Layer 1 and Layer 2 queries", and give no
// order, so the item that answers a plan (IT12, the twin's read) and the one that
// answers the composite queries (IT30) build this one, and read it here.
func (rp ReadPlan) TsetSlots() []TsetSlot {
	tables := make([]string, 0, len(rp.IDs))
	for t, ids := range rp.IDs {
		if len(ids) > 0 {
			tables = append(tables, t)
		}
	}
	sort.Strings(tables)
	out := make([]TsetSlot, 0, len(tables)+len(rp.Ranges)+len(rp.Counts)+len(rp.RCounts)+len(rp.Lines))
	for _, t := range tables {
		out = append(out, TsetSlot{Kind: AnswerIDs, Table: t})
	}
	for i := range rp.Ranges {
		out = append(out, TsetSlot{Kind: AnswerRange, Index: i})
	}
	for i := range rp.Counts {
		out = append(out, TsetSlot{Kind: AnswerCount, Index: i})
	}
	for i := range rp.RCounts {
		out = append(out, TsetSlot{Kind: AnswerRCount, Index: i})
	}
	for i := range rp.Lines {
		out = append(out, TsetSlot{Kind: AnswerLines, Index: i})
	}
	return out
}

// Queries is the queries the plan sends: one ids query for each table that
// names an id, and one for each of the others.
func (rp ReadPlan) Queries() int {
	n := len(rp.Ranges) + len(rp.Counts) + len(rp.RCounts) + len(rp.Lines) + len(rp.Sprint)
	for _, ids := range rp.IDs {
		if len(ids) > 0 {
			n++
		}
	}
	return n
}

// Cost is what the plan may cost the store: the sum of its queries' costs. An
// id counts once for every occurrence (L1 6: records requested, counting
// occurrences).
func (rp ReadPlan) Cost() Cost {
	var c Cost
	for _, ids := range rp.IDs {
		c = c.Add(Cost{Records: len(ids), Bytes: len(ids) * WholeRecordBytes})
	}
	for _, q := range rp.Ranges {
		c = c.Add(rangeCost(q))
	}
	for _, q := range rp.Counts {
		c.Bytes += len(q.Cells) * CountBytes
	}
	for _, q := range rp.RCounts {
		c.Bytes += (len(q.Cells) + 1) * CountBytes
	}
	for _, q := range rp.Lines {
		c = c.Add(linesCost(q))
	}
	for _, q := range rp.Sprint {
		c = c.Add(QueryCost(q))
	}
	return c
}

// within says a read of that many queries and that cost fits the bounds. A
// bound of 0 or less is no bound.
func within(queries int, c Cost, b ReadBounds) bool {
	return (b.Queries <= 0 || queries <= b.Queries) &&
		(b.Records <= 0 || c.Records <= b.Records) &&
		(b.RangeIDs <= 0 || c.RangeIDs <= b.RangeIDs) &&
		(b.Bytes <= 0 || c.Bytes <= b.Bytes)
}

// empty says the plan asks nothing.
func (rp ReadPlan) empty() bool { return rp.Queries() == 0 }

// splittable says the query may be cut into queries over parts of its ids,
// each independent of the others: the ones whose cost is a sum over an id list
// (`related`, `waiters`, `jnote`), whether the ids are named in a list or are
// the window of a line (a line is cut by Offset and Limit). `needchain` walks
// from all its ids together and its cost is one `max`, a head is the first ids
// of an index and has no place to cut it at, and the rest name no ids.
func splittable(q SprintQ) bool {
	switch q.Kind {
	case QueryRelated, QueryWaiters, QueryJnote:
	default:
		return false
	}
	switch q.Source.Kind {
	case SourceIDs:
		return len(q.Source.IDs) > 0
	case SourceLine:
		_, n := q.Source.lineWindow()
		return n > 0
	}
	return false
}

// Split cuts the plan into plans that each fit the bounds (1.4.2): what does
// not fit one read of a tick is cut before it is sent, never refused. It keeps
// every query whole except a list of ids (one table's ids, or the ids of a
// splittable composite query) and a composite query over a line, which it cuts
// where a bound falls: a list into lists, and a line into pieces with a lower
// Limit, each from its own Offset, that together read the ids the line query
// read. The plans together ask what the plan asked, in the order it asked it,
// and the pieces of one query are in consecutive plans. A plan that fits is
// returned alone, and an empty plan gives none. A piece that alone is over a
// bound (one query that cannot be cut, or one id of a query over a bound by
// itself) is placed alone in a plan of its own, over the bound: it is the
// caller's to halve (1.3.5). A bound of 0 or less is no bound. The plans are
// separate reads, so they are separate snapshots: a caller that must see one
// state does not split.
func (rp ReadPlan) Split(b ReadBounds) []ReadPlan {
	if rp.empty() {
		return nil
	}
	if within(rp.Queries(), rp.Cost(), b) {
		return []ReadPlan{rp}
	}
	var sp splitter
	sp.b = b
	tables := make([]string, 0, len(rp.IDs))
	for t, ids := range rp.IDs {
		if len(ids) > 0 {
			tables = append(tables, t)
		}
	}
	sort.Strings(tables)
	for _, t := range tables {
		t := t
		ids := rp.IDs[t]
		per := Cost{Records: 1, Bytes: WholeRecordBytes}
		sp.listed(len(ids), per, func(cur *ReadPlan, from, to int) {
			if cur.IDs == nil {
				cur.IDs = map[string][]string{}
			}
			cur.IDs[t] = append(cur.IDs[t], ids[from:to]...)
		})
	}
	for _, q := range rp.Ranges {
		q := q
		sp.whole(rangeCost(q), func(cur *ReadPlan) { cur.Ranges = append(cur.Ranges, q) })
	}
	for _, q := range rp.Counts {
		q := q
		sp.whole(Cost{Bytes: len(q.Cells) * CountBytes}, func(cur *ReadPlan) { cur.Counts = append(cur.Counts, q) })
	}
	for _, q := range rp.RCounts {
		q := q
		sp.whole(Cost{Bytes: (len(q.Cells) + 1) * CountBytes}, func(cur *ReadPlan) { cur.RCounts = append(cur.RCounts, q) })
	}
	for _, q := range rp.Lines {
		q := q
		sp.whole(linesCost(q), func(cur *ReadPlan) { cur.Lines = append(cur.Lines, q) })
	}
	for _, q := range rp.Sprint {
		q := q
		if !splittable(q) {
			sp.whole(QueryCost(q), func(cur *ReadPlan) { cur.Sprint = append(cur.Sprint, q) })
			continue
		}
		one := q
		if q.Source.Kind == SourceLine {
			offset, n := q.Source.lineWindow()
			one.Source.Offset, one.Source.Limit = offset, 1
			sp.listed(n, QueryCost(one), func(cur *ReadPlan, from, to int) {
				piece := q
				piece.Source.Offset, piece.Source.Limit = offset+from, to-from
				cur.Sprint = append(cur.Sprint, piece)
			})
			continue
		}
		one.Source.IDs = q.Source.IDs[:1]
		ids := q.Source.IDs
		sp.listed(len(ids), QueryCost(one), func(cur *ReadPlan, from, to int) {
			piece := q
			piece.Source.IDs = ids[from:to:to]
			cur.Sprint = append(cur.Sprint, piece)
		})
	}
	return sp.finish()
}

// splitter packs queries into plans in order.
type splitter struct {
	b       ReadBounds
	out     []ReadPlan
	cur     ReadPlan
	queries int
	used    Cost
}

// flush closes the plan being filled.
func (sp *splitter) flush() {
	if !sp.cur.empty() {
		sp.out = append(sp.out, sp.cur)
	}
	sp.cur, sp.queries, sp.used = ReadPlan{}, 0, Cost{}
}

// finish returns the plans.
func (sp *splitter) finish() []ReadPlan {
	sp.flush()
	return sp.out
}

// whole adds a query that is not cut: to the plan being filled if it fits
// beside what is there, and otherwise to the next (alone, over a bound if it
// must).
func (sp *splitter) whole(c Cost, add func(cur *ReadPlan)) {
	if sp.queries > 0 && !within(sp.queries+1, sp.used.Add(c), sp.b) {
		sp.flush()
	}
	add(&sp.cur)
	sp.queries++
	sp.used = sp.used.Add(c)
}

// listed adds a query over n ids, each costing per, cutting it where a bound
// falls: add(cur, from, to) puts the ids from to to in the plan being filled.
func (sp *splitter) listed(n int, per Cost, add func(cur *ReadPlan, from, to int)) {
	from := 0
	for from < n {
		if sp.queries > 0 && !within(sp.queries+1, sp.used, sp.b) {
			sp.flush() // no room for another query in this plan
		}
		k := sp.room(per)
		if k > n-from {
			k = n - from
		}
		if k < 1 {
			if sp.queries > 0 {
				sp.flush()
				continue
			}
			k = 1 // one id alone is over a bound: it goes alone
		}
		add(&sp.cur, from, from+k)
		sp.queries++
		sp.used = sp.used.Add(per.scale(k))
		from += k
		if from < n {
			sp.flush()
		}
	}
}

// room is how many ids costing per each fit beside what the plan being filled
// holds.
func (sp *splitter) room(per Cost) int {
	k := int(^uint(0) >> 1)
	limit := func(bound, used, each int) {
		if bound > 0 && each > 0 {
			if r := (bound - used) / each; r < k {
				k = r
			}
		}
	}
	limit(sp.b.Records, sp.used.Records, per.Records)
	limit(sp.b.RangeIDs, sp.used.RangeIDs, per.RangeIDs)
	limit(sp.b.Bytes, sp.used.Bytes, per.Bytes)
	if k < 0 {
		return 0
	}
	return k
}

// Halved is n after halvings halvings, each taking half (the larger half of
// an odd n) and never below one (1.4.2): what a rule reads after a BUDGET or a
// LIMIT on its read (1.3.5), its keys or its limits. A count of 0 stays 0, and
// a halvings of 0 or less leaves n as it is.
func Halved(n, halvings int) int {
	if n <= 0 {
		return 0
	}
	for i := 0; i < halvings && n > 1; i++ {
		n = (n + 1) / 2
	}
	return n
}

// FitKeys is the keys a rule reads this tick and the keys it leaves for a
// later one (1.4.2): the longest prefix of keys, in agenda order, whose costs
// with fixed (what the read costs whatever the keys) fit the bounds, halved
// halvings times (Halved) after a BUDGET or a LIMIT. It reads at least one key
// however large: a key that alone is over a bound is the caller's to name.
// Keys are taken in order and none is skipped for a smaller one behind it, so
// the oldest key is never starved. b.Queries is not counted: the keys share a
// read's queries.
func FitKeys(keys []AgendaKey, cost func(AgendaKey) Cost, fixed Cost, b ReadBounds, halvings int) (take, rest []AgendaKey) {
	used := fixed
	n := 0
	for _, k := range keys {
		next := used.Add(cost(k))
		if n > 0 && !within(0, next, b) {
			break
		}
		used = next
		n++
	}
	n = Halved(n, halvings)
	return keys[:n:n], keys[n:]
}

package sprint

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The partial snapshot, the first part of IT05 after the shared types (the
// upper design, version 2.1, sections 1.5.2 and 8.1): a Snapshot loaded from a
// read plan and its answer. The planners stay pure functions of a Snapshot; a
// snapshot loaded here knows which cells it loaded whole, which counts it was
// given and which positions the read answered, and a planner that reads
// anything else is refused (Table.Loaded, Snapshot.Unloaded), so a scan of a
// table cannot come back unnoticed.
//
// The design gives the answer's shape nowhere (8.0 names ReadAnswer and Answer
// and defines neither); these are the fields LoadPartial reads, layer 1's
// answer words (L1 7) for the queries of a ReadPlan, and for the sprint's
// composite queries only what a snapshot needs: the records they returned,
// the rows of the tables they list, and the first sentinel of a stream with
// its position. The queries' other results (the heads of an index, the waiters
// of a need) are for the rules that read them, and a later item adds them to
// Answer beside these.

// ReadAnswer is the answer of one atomic read, aligned with the ReadPlan that
// asked it (L1 7): one answer for each query, in the plan's order.
type ReadAnswer struct {
	// Epoch is the epoch read, and TimeMS the store's time in milliseconds, read
	// once for the whole call (1.0).
	Epoch  uint64
	TimeMS int64
	// IDs are, for each table of the plan's IDs, one record for each id it
	// named, in order: nil where the table has no record. A record's Row and
	// Col are empty when it is kept but not placed.
	IDs map[string][]*Card
	// Ranges, Counts, RCounts, Lines and Sprint answer the plan's queries of
	// that name, one for one.
	Ranges  []RangeA
	Counts  [][]int
	RCounts []RCountA
	Lines   []LinesA
	Sprint  []Answer
}

// RangeA is the answer of a range: the ids in order, their scores, whether
// members beyond them match (L1 7: has_more), and their records when the query
// asked for them.
type RangeA struct {
	IDs     []string
	Scores  []float64
	HasMore bool
	Cards   []*Card
}

// RCountA is the answer of an rcount: each cell's count and their sum.
type RCountA struct {
	Counts []int
	Sum    int
}

// LinesA is the answer of a lines query: the log's lines as layer 2 returns
// them, each with its stream id, which is its seq (L2 2).
type LinesA struct{ Lines []LogLine }

// LogLine is a line of the log: its stream id and its body.
type LogLine struct {
	ID   string
	Body []byte
}

// TableCard is a record with the table it belongs to.
type TableCard struct {
	Table string
	Card  *Card
}

// FrontAnswer is what `front(s)` returns of the stream's line (1.0): the first
// sentinel G and its score sigma, and the count of the stream's open cards
// before it.
type FrontAnswer struct {
	// Stream is the stream, and G the first sentinel of its sent:s index, empty
	// when it has none (then Sigma and NBefore mean nothing).
	Stream, G string
	// Sigma is G's score, and NBefore the count of the stream's five open cells
	// below it.
	Sigma   float64
	NBefore int
	// GQuarantined says G is quarantined: its record was not returned, and it
	// stays the first sentinel so that nothing behind it is released (1.0).
	GQuarantined bool
}

// Answer is the answer of one composite query (1.0), of the kind of its query.
// Only what a snapshot is loaded from is here.
type Answer struct {
	// Kind is the query's kind; empty is the query's.
	Kind string
	// Records are the records the query returned, each with its table.
	Records []TableCard
	// Rows are the rows of the tables a `streams`, `fleet` or `readers` query
	// lists, in the tables' order.
	Rows []string
	// Front is the answer of `front`.
	Front *FrontAnswer
}

// rowsOf are the tables whose rows a query lists: the streams are the rows of
// the work and merge tables, the members of the fleet's, the readers of the
// readers'.
var rowsOf = map[string][]string{
	QueryStreams: {Work, Merge},
	QueryFleet:   {Fleet},
	QueryReaders: {Readers},
}

// Partial is what a snapshot loaded from a read plan was loaded from: the plan
// and its answer, kept whole so that a rule reads the results of a query by
// its index, and what the loading derived from them.
type Partial struct {
	Plan   ReadPlan
	Answer ReadAnswer
	// Fronts are the answers of `front`, by stream.
	Fronts map[string]FrontAnswer
	// Events are the lines the read returned, parsed, in the plan's order.
	Events []Event

	before map[beforeKey]int
	log    *unloadedLog
}

// beforeKey names an rcount over a stream's open cells below a bound.
type beforeKey struct{ stream, bound string }

// ErrMisaligned is the answer of a read that does not answer its plan: LoadPartial
// refuses it, and nothing is planned on it (1.5.1: a read is complete or it is
// not used).
var ErrMisaligned = errors.New("the answer does not answer the read plan")

func misaligned(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrMisaligned, fmt.Sprintf(format, a...))
}

// LoadPartial loads a snapshot from a read plan and its answer: the records the
// answer holds in their tables, the cells it gave whole, the counts it gave,
// the rows the composite queries listed, the positions `front` answered and the
// lines it returned. Every table of the sprint exists in the snapshot, and any
// cell of it that the read did not load is refused (Table.Loaded). The answer
// is not changed and the snapshot shares none of its records. The clock, the
// coordinator and the open judgments are not in a read of the tables: the
// caller sets them.
//
// In a test build a planner that reads what was not loaded panics; in a
// release build it is recorded, and Snapshot.Unloaded names it (1.5.2).
func LoadPartial(rp ReadPlan, ans ReadAnswer) (*Snapshot, error) {
	return loadPartial(rp, ans, testing.Testing())
}

func loadPartial(rp ReadPlan, ans ReadAnswer, strict bool) (*Snapshot, error) {
	log := &unloadedLog{strict: strict}
	s := &Snapshot{Epoch: ans.Epoch}
	if ans.TimeMS != 0 {
		s.Now = time.UnixMilli(ans.TimeMS).UTC()
	}
	for _, name := range ViewOrder {
		t := NewTable(name)
		t.Epoch = ans.Epoch
		t.part = &loadedCells{whole: map[[2]string]bool{}, counts: map[[2]string]int{}, log: log}
		switch name {
		case Work:
			s.Work = t
		case Readers:
			s.Readers = t
		case Merge:
			s.Merge = t
		case Fleet:
			s.Fleet = t
		}
	}
	p := &Partial{Plan: rp, Answer: ans, Fronts: map[string]FrontAnswer{}, before: map[beforeKey]int{}, log: log}
	s.Partial = p

	if err := p.loadIDs(s, rp, ans); err != nil {
		return nil, err
	}
	if err := p.loadRanges(s, rp, ans); err != nil {
		return nil, err
	}
	if err := p.loadCounts(s, rp, ans); err != nil {
		return nil, err
	}
	if err := p.loadLines(rp, ans); err != nil {
		return nil, err
	}
	if err := p.loadSprint(s, rp, ans); err != nil {
		return nil, err
	}
	return s, nil
}

// put places a copy of the card in the table: the snapshot shares nothing
// with the answer it was loaded from. A record the table already has, read by
// another query of the same read with another projection, gains this one's
// fields (both are of one snapshot, so a field they share has one value) and
// takes its place, score and revision.
func put(t *Table, c *Card) {
	if old := t.Cards[c.ID]; old != nil {
		old.Row, old.Col, old.Score, old.Rev = c.Row, c.Col, c.Score, c.Rev
		for k, v := range c.Fields {
			old.Fields[k] = v
		}
		t.Put(old)
		return
	}
	cc := *c
	cc.Fields = make(map[string]string, len(c.Fields))
	for k, v := range c.Fields {
		cc.Fields[k] = v
	}
	t.Put(&cc)
}

// loadIDs loads the records of the plan's ids.
func (p *Partial) loadIDs(s *Snapshot, rp ReadPlan, ans ReadAnswer) error {
	tables := make([]string, 0, len(rp.IDs))
	for name := range rp.IDs {
		tables = append(tables, name)
	}
	sort.Strings(tables)
	for _, name := range tables {
		ids, recs := rp.IDs[name], ans.IDs[name]
		if len(ids) == 0 && len(recs) == 0 {
			continue
		}
		t := s.T(name)
		if t == nil {
			return misaligned("the plan reads ids of the unknown table %q", name)
		}
		if len(recs) != len(ids) {
			return misaligned("%s: %d ids read, %d records answered", name, len(ids), len(recs))
		}
		for i, c := range recs {
			if c == nil {
				continue
			}
			if c.ID != ids[i] {
				return misaligned("%s: record %d is %q, the id read is %q", name, i, c.ID, ids[i])
			}
			put(t, c)
		}
	}
	for name := range ans.IDs {
		if _, ok := rp.IDs[name]; !ok {
			return misaligned("the answer holds records of the table %q, which the plan did not read", name)
		}
	}
	return nil
}

// unbounded says the score bounds of a range or an rcount take every member.
func unbounded(min, max string) bool {
	return (min == "" || min == "-inf") && (max == "" || max == "+inf" || max == "inf")
}

// cellRef splits a cell reference "<row>:<col>" at its last colon (L1 3).
func cellRef(cell string) (row, col string, ok bool) {
	i := strings.LastIndexByte(cell, ':')
	if i <= 0 || i == len(cell)-1 {
		return "", "", false
	}
	return cell[:i], cell[i+1:], true
}

// loadRanges loads the plan's ranges: the records of a cell's range, and the
// cell whole when the range took every member and had no more.
func (p *Partial) loadRanges(s *Snapshot, rp ReadPlan, ans ReadAnswer) error {
	if len(ans.Ranges) != len(rp.Ranges) {
		return misaligned("%d ranges read, %d answered", len(rp.Ranges), len(ans.Ranges))
	}
	for i, q := range rp.Ranges {
		a := ans.Ranges[i]
		if len(a.Scores) != 0 && len(a.Scores) != len(a.IDs) {
			return misaligned("range %d: %d ids, %d scores", i, len(a.IDs), len(a.Scores))
		}
		if q.Records && len(a.Cards) != len(a.IDs) || !q.Records && len(a.Cards) != 0 {
			return misaligned("range %d: %d ids, %d records, and the query asked for records: %v", i, len(a.IDs), len(a.Cards), q.Records)
		}
		cellForm, keyForm := q.Table != "" || q.Cell != "", q.Key != ""
		if cellForm == keyForm || cellForm && (q.Table == "" || q.Cell == "") {
			return misaligned("range %d names a cell and a key, or neither, or half a cell", i)
		}
		if keyForm {
			continue // a sorted set of the sprint: the answer is read by its index
		}
		t := s.T(q.Table)
		row, col, ok := cellRef(q.Cell)
		if t == nil || !ok {
			return misaligned("range %d reads the cell %q of the table %q", i, q.Cell, q.Table)
		}
		for j, c := range a.Cards {
			if c == nil || c.ID != a.IDs[j] || c.Row != row || c.Col != col {
				return misaligned("range %d: record %d is not the member %q of %s", i, j, a.IDs[j], q.Cell)
			}
			put(t, c)
		}
		if q.Records && !a.HasMore && unbounded(q.Min, q.Max) {
			t.part.whole[[2]string{row, col}] = true
		}
	}
	return nil
}

// loadCounts loads the plan's counts and rcounts. An rcount over every score
// is a count; one over the stream's open cells below a bound is a position,
// which OpenBefore reads.
func (p *Partial) loadCounts(s *Snapshot, rp ReadPlan, ans ReadAnswer) error {
	if len(ans.Counts) != len(rp.Counts) {
		return misaligned("%d counts read, %d answered", len(rp.Counts), len(ans.Counts))
	}
	if len(ans.RCounts) != len(rp.RCounts) {
		return misaligned("%d rcounts read, %d answered", len(rp.RCounts), len(ans.RCounts))
	}
	for i, q := range rp.Counts {
		if err := p.count(s, "count", i, q.Table, q.Cells, ans.Counts[i]); err != nil {
			return err
		}
	}
	for i, q := range rp.RCounts {
		a := ans.RCounts[i]
		if unbounded(q.Min, q.Max) {
			if err := p.count(s, "rcount", i, q.Table, q.Cells, a.Counts); err != nil {
				return err
			}
			continue
		}
		if len(a.Counts) != len(q.Cells) {
			return misaligned("rcount %d: %d cells read, %d counts answered", i, len(q.Cells), len(a.Counts))
		}
		if row, ok := openCellsOf(q); ok && q.Table == Work && unbounded(q.Min, "") {
			p.before[beforeKey{row, q.Max}] = a.Sum
		}
	}
	return nil
}

// count records the counts of cells.
func (p *Partial) count(s *Snapshot, what string, i int, table string, cells []string, counts []int) error {
	t := s.T(table)
	if t == nil {
		return misaligned("%s %d reads the unknown table %q", what, i, table)
	}
	if len(counts) != len(cells) {
		return misaligned("%s %d: %d cells read, %d counts answered", what, i, len(cells), len(counts))
	}
	for j, cell := range cells {
		row, col, ok := cellRef(cell)
		if !ok {
			return misaligned("%s %d reads the cell %q", what, i, cell)
		}
		t.part.counts[[2]string{row, col}] = counts[j]
	}
	return nil
}

// openCellsOf is the stream an rcount reads when its cells are exactly the
// five open cells of one stream of the work table.
func openCellsOf(q RCountQ) (stream string, ok bool) {
	want := len(States) - 1
	if len(q.Cells) != want {
		return "", false
	}
	seen := map[string]bool{}
	for _, cell := range q.Cells {
		row, col, ok := cellRef(cell)
		if !ok || col == Landed || (stream != "" && row != stream) {
			return "", false
		}
		stream = row
		seen[col] = true
	}
	for _, st := range States {
		if st != Landed && !seen[st] {
			return "", false
		}
	}
	return stream, true
}

// OpenCells are the five open cells of a stream in the work table (every
// state but landed), in the states' order.
func OpenCells(stream string) []string {
	var out []string
	for _, st := range States {
		if st != Landed {
			out = append(out, stream+":"+st)
		}
	}
	return out
}

// beforeBound is the rcount bound that takes the scores below score.
func beforeBound(score float64) string {
	return "(" + strconv.FormatFloat(score, 'f', -1, 64)
}

// OpenBeforeQ is the query whose answer OpenBefore reads for a score other
// than the first sentinel's: the count of the stream's open cells below the
// score.
func OpenBeforeQ(stream string, score float64) RCountQ {
	return RCountQ{Table: Work, Cells: OpenCells(stream), Min: "-inf", Max: beforeBound(score)}
}

// loadLines parses the lines the plan read.
func (p *Partial) loadLines(rp ReadPlan, ans ReadAnswer) error {
	if len(ans.Lines) != len(rp.Lines) {
		return misaligned("%d lines queries read, %d answered", len(rp.Lines), len(ans.Lines))
	}
	for i, a := range ans.Lines {
		for _, l := range a.Lines {
			e, err := ParseEvent(l.ID, l.Body)
			if err != nil {
				return fmt.Errorf("lines query %d, line %s: %w", i, l.ID, err)
			}
			p.Events = append(p.Events, e)
		}
	}
	return nil
}

// loadSprint loads the answers of the composite queries.
func (p *Partial) loadSprint(s *Snapshot, rp ReadPlan, ans ReadAnswer) error {
	if len(ans.Sprint) != len(rp.Sprint) {
		return misaligned("%d composite queries read, %d answered", len(rp.Sprint), len(ans.Sprint))
	}
	for i, q := range rp.Sprint {
		a := ans.Sprint[i]
		if a.Kind != "" && a.Kind != q.Kind {
			return misaligned("composite query %d is %q, the answer is %q", i, q.Kind, a.Kind)
		}
		for _, r := range a.Records {
			t := s.T(r.Table)
			if t == nil || r.Card == nil {
				return misaligned("composite query %d returned a record of the table %q", i, r.Table)
			}
			put(t, r.Card)
		}
		if len(a.Rows) > 0 {
			tables, ok := rowsOf[q.Kind]
			if !ok {
				return misaligned("composite query %d (%s) lists rows, and no such query does", i, q.Kind)
			}
			for _, name := range tables {
				t := s.T(name)
				t.Rows = append([]string(nil), a.Rows...)
				t.part.rows = true
			}
		}
		if _, lists := rowsOf[q.Kind]; lists && len(a.Rows) == 0 {
			for _, name := range rowsOf[q.Kind] {
				s.T(name).part.rows = true // a sprint with no rows has none
			}
		}
		if q.Kind == QueryFront {
			if a.Front == nil || a.Front.Stream != q.Stream {
				return misaligned("composite query %d is front of %q and the answer names another", i, q.Stream)
			}
			p.Fronts[q.Stream] = *a.Front
		}
	}
	return nil
}

// Unloaded is what the planners read of the snapshot that its plan did not
// load (1.5.2): the cells, counts and positions, each named. A release build
// does not panic at such a read: it plans on nothing, and the caller refuses
// the plan when this is not empty. A snapshot built whole has none.
func (s *Snapshot) Unloaded() []string {
	if s == nil || s.Partial == nil {
		return nil
	}
	return s.Partial.log.list()
}

// FirstSentinel is the first sentinel of the stream (the lowest score of its
// sent:s index): from the read's `front` answer for a snapshot loaded from a
// plan, and from the waiting cell of a snapshot built whole. Nil when the
// stream has none. A sentinel that is quarantined has no record, and is
// returned as the card the answer names (its id, its place, its score and its
// kind), so that nothing behind it is released (1.0); the answer says it is
// quarantined (Partial.Fronts). A plan that did not read `front` of the stream
// is refused (Snapshot.Unloaded) and gets nil.
func FirstSentinel(s *Snapshot, stream string) *Card {
	if s.Partial == nil {
		for _, c := range s.Work.Cell(stream, Waiting) {
			if IsSentinel(c) {
				return c
			}
		}
		return nil
	}
	f, ok := s.Partial.Fronts[stream]
	if !ok {
		s.Partial.log.note("the planner asked for a position its plan did not load: front of " + stream)
		return nil
	}
	if f.G == "" {
		return nil
	}
	if c := s.Work.Placed(f.G); c != nil {
		return c
	}
	return &Card{ID: f.G, Row: stream, Col: Waiting, Score: f.Sigma, Fields: map[string]string{"kind": Sentinel}}
}

// OpenBefore is the count of the stream's open cards (its five open cells)
// with a score below score: for a snapshot loaded from a plan, the first
// sentinel's n_before when score is its score, and otherwise the answer of the
// rcount OpenBeforeQ(stream, score) asks; for a snapshot built whole, the
// count of its cards. A plan that read neither is refused (Snapshot.Unloaded)
// and gets -1, never a count that could mean "none before it".
func OpenBefore(s *Snapshot, stream string, score float64) int {
	if s.Partial == nil {
		line := s.Work.openLine(stream)
		return sort.Search(len(line), func(i int) bool { return line[i].Score >= score })
	}
	if f, ok := s.Partial.Fronts[stream]; ok && f.G != "" && f.Sigma == score {
		return f.NBefore
	}
	if n, ok := s.Partial.before[beforeKey{stream, beforeBound(score)}]; ok {
		return n
	}
	s.Partial.log.note("the planner asked for a position its plan did not load: " + stream + " before " + strconv.FormatFloat(score, 'f', -1, 64))
	return -1
}

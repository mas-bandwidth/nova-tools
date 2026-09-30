package sprint

import (
	"errors"
	"fmt"
	"math"
	"slices"
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
// The answer's types are in plan_types.go (the errata to version 2.1 give them
// to IT05's shared types): ReadAnswer, with the answer of each Layer 1 and
// Layer 2 query in the order ReadPlan.TsetSlots gives, and Answer, the answer
// of each composite query, which holds only what a snapshot is loaded from.

// rowsOf are the tables whose rows a query lists: the streams are the rows of
// the work and merge tables, the members of the fleet's, the readers of the
// readers'.
var rowsOf = map[string][]string{
	QueryStreams: {Work, Merge},
	QueryFleet:   {Fleet},
	QueryReaders: {Readers},
}

// countsOf are the tables whose cells a query counts for each row it lists: the
// members' cells of the fleet, the readers' of the readers (1.0).
var countsOf = map[string]string{
	QueryFleet:   Fleet,
	QueryReaders: Readers,
}

// Partial is what a snapshot loaded from a read plan was loaded from: the plan
// and its answer, kept whole so that a rule reads the results of a query by
// its index, and what the loading derived from them.
type Partial struct {
	Plan   ReadPlan
	Answer ReadAnswer
	// ActiveEpoch is the sprint's active epoch the read gave (0 when it did not).
	ActiveEpoch uint64
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
// is not changed, and the records in the snapshot's tables are copies that
// share nothing with it; the snapshot keeps the plan and the answer themselves
// (Partial.Plan and Partial.Answer), the caller's slices, maps and records, to
// be read and not changed. The clock, the coordinator and the open judgments are
// not in a read of the tables: the caller sets them.
//
// In a test build a planner that reads what was not loaded panics; in a
// release build it is recorded, and Snapshot.Unloaded names it (1.5.2). What is
// not loaded is a cell not read whole, a count, position, row or line the plan
// did not ask for, the cards of a primary no follow read, and a field of a
// record that no query that read it named.
func LoadPartial(rp ReadPlan, ans ReadAnswer) (*Snapshot, error) {
	return loadPartial(rp, ans, testing.Testing())
}

func loadPartial(rp ReadPlan, ans ReadAnswer, strict bool) (*Snapshot, error) {
	if err := rp.Validate(); err != nil {
		return nil, err
	}
	log := &unloadedLog{strict: strict}
	epoch, err := ans.Epoch.Uint64()
	if err != nil {
		return nil, misaligned("the epoch %v", err)
	}
	active, err := ans.ActiveEpoch.Uint64()
	if err != nil {
		return nil, misaligned("the active epoch %v", err)
	}
	ms, err := ans.TimeMS.Uint64()
	if err != nil || ms > math.MaxInt64 {
		return nil, misaligned("the time %q is not a number of milliseconds", string(ans.TimeMS))
	}
	s := &Snapshot{Epoch: epoch}
	if ms != 0 {
		s.Now = time.UnixMilli(int64(ms)).UTC()
	}
	for _, name := range ViewOrder {
		t := NewTable(name)
		t.Epoch = epoch
		t.part = &loadedCells{whole: map[[2]string]bool{}, counts: map[[2]string]int{}, followed: map[[2]string]bool{}, log: log}
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
	p := &Partial{Plan: rp, Answer: ans, ActiveEpoch: active, Fronts: map[string]FrontAnswer{}, before: map[beforeKey]int{}, log: log}
	s.Partial = p

	slots := rp.TsetSlots()
	if len(ans.Tset) != len(slots) {
		return nil, misaligned("%d Layer 1 and Layer 2 queries read, %d answered", len(slots), len(ans.Tset))
	}
	for i, sl := range slots {
		a := ans.Tset[i]
		if a.Kind != "" && a.Kind != sl.Kind {
			return nil, misaligned("query %d is a %s, the answer is a %s", i, sl.Kind, a.Kind)
		}
		var err error
		switch sl.Kind {
		case AnswerIDs:
			err = p.loadIDs(s, sl.Table, rp.IDs[sl.Table], a)
		case AnswerRange:
			err = p.loadRange(s, sl.Index, rp.Ranges[sl.Index], a)
		case AnswerCount:
			q := rp.Counts[sl.Index]
			err = p.count(s, "count", sl.Index, q.Table, q.Cells, a.Counts)
		case AnswerRCount:
			err = p.loadRCount(s, sl.Index, rp.RCounts[sl.Index], a)
		case AnswerLines:
			err = p.loadLines(sl.Index, a)
		}
		if err != nil {
			return nil, err
		}
	}
	if err := p.loadSprint(s, rp, ans); err != nil {
		return nil, err
	}
	return s, nil
}

// Uint64 is the number. The empty string is 0, and anything that is not digits
// alone, or does not fit 64 bits, is an error.
func (d Decimal) Uint64() (uint64, error) {
	if d == "" {
		return 0, nil
	}
	for i := 0; i < len(d); i++ {
		if d[i] < '0' || d[i] > '9' {
			return 0, fmt.Errorf("%q is not a decimal", string(d))
		}
	}
	n, err := strconv.ParseUint(string(d), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a decimal: %w", string(d), err)
	}
	return n, nil
}

// put places a copy of the card in the table: the table's records share
// nothing with the answer's (Partial.Answer keeps the answer as the caller
// gave it). ld says which fields the query that read it named. A record the
// table already has, read by another query of the same read with another
// projection, gains this one's fields (both are of one snapshot, so a field
// they share has one value) and takes its place, score and revision.
func put(t *Table, c *Card, ld *cardLoad) {
	if old := t.cards[c.ID]; old != nil {
		old.Row, old.Col, old.Score, old.Rev = c.Row, c.Col, c.Score, c.Rev
		for k, v := range c.Fields {
			old.Fields[k] = v
		}
		old.load = old.load.with(ld)
		t.Put(old)
		return
	}
	cc := *c
	cc.Fields = make(map[string]string, len(c.Fields))
	for k, v := range c.Fields {
		cc.Fields[k] = v
	}
	cc.load = ld
	t.Put(&cc)
}

// loadIDs loads the records of the ids the plan names in one table.
func (p *Partial) loadIDs(s *Snapshot, name string, ids []string, a TsetAnswer) error {
	t := s.T(name)
	if t == nil {
		return misaligned("the plan reads ids of the unknown table %q", name)
	}
	if len(a.Records) != len(ids) {
		return misaligned("%s: %d ids read, %d records answered", name, len(ids), len(a.Records))
	}
	whole := newCardLoad(nil, t.part.log) // an ids query names no fields: whole records (6)
	for i, c := range a.Records {
		if c == nil {
			continue
		}
		if c.ID != ids[i] {
			return misaligned("%s: record %d is %q, the id read is %q", name, i, c.ID, ids[i])
		}
		put(t, c, whole)
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

// loadRange loads a range: the records of a cell's range, and the cell whole
// when the range took every member and had no more.
func (p *Partial) loadRange(s *Snapshot, i int, q RangeQ, a TsetAnswer) error {
	if len(a.Scores) != 0 && len(a.Scores) != len(a.IDs) {
		return misaligned("range %d: %d ids, %d scores", i, len(a.IDs), len(a.Scores))
	}
	if q.Records && len(a.Records) != len(a.IDs) || !q.Records && len(a.Records) != 0 {
		return misaligned("range %d: %d ids, %d records, and the query asked for records: %v", i, len(a.IDs), len(a.Records), q.Records)
	}
	cellForm, keyForm := q.Table != "" || q.Cell != "", q.Key != ""
	if cellForm == keyForm || cellForm && (q.Table == "" || q.Cell == "") {
		return misaligned("range %d names a cell and a key, or neither, or half a cell", i)
	}
	if keyForm {
		if q.Records {
			return misaligned("range %d reads the sorted set %q with records: only a cell has any (L1 7)", i, q.Key)
		}
		return nil // a sorted set of the sprint: the answer is read by its index
	}
	t := s.T(q.Table)
	row, col, ok := cellRef(q.Cell)
	if t == nil || !ok {
		return misaligned("range %d reads the cell %q of the table %q", i, q.Cell, q.Table)
	}
	ld := newCardLoad(q.Fields, t.part.log)
	for j, c := range a.Records {
		if c == nil || c.ID != a.IDs[j] || c.Row != row || c.Col != col {
			return misaligned("range %d: record %d is not the member %q of %s", i, j, a.IDs[j], q.Cell)
		}
		put(t, c, ld)
	}
	if q.Records && !a.HasMore && unbounded(q.Min, q.Max) {
		t.part.whole[[2]string{row, col}] = true
	}
	return nil
}

// loadRCount loads an rcount. One over every score is a count; one over the
// stream's open cells below a bound is a position, which OpenBefore reads.
func (p *Partial) loadRCount(s *Snapshot, i int, q RCountQ, a TsetAnswer) error {
	sum := 0
	for _, n := range a.Counts {
		sum += n
	}
	if sum != a.Sum {
		return misaligned("rcount %d: the counts sum to %d, the answer's sum is %d", i, sum, a.Sum)
	}
	if unbounded(q.Min, q.Max) {
		return p.count(s, "rcount", i, q.Table, q.Cells, a.Counts)
	}
	if len(a.Counts) != len(q.Cells) {
		return misaligned("rcount %d: %d cells read, %d counts answered", i, len(q.Cells), len(a.Counts))
	}
	if row, ok := openCellsOf(q); ok && q.Table == Work && unbounded(q.Min, "") {
		p.before[beforeKey{row, q.Max}] = a.Sum
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

// loadLines parses the lines a lines query returned.
func (p *Partial) loadLines(i int, a TsetAnswer) error {
	for _, l := range a.Lines {
		e, err := ParseEvent(l.ID, l.Body)
		if err != nil {
			return fmt.Errorf("lines query %d, line %s: %w", i, l.ID, err)
		}
		p.Events = append(p.Events, e)
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
		ld := newCardLoad(q.Fields, p.log)
		for _, r := range a.Records {
			t := s.T(r.Table)
			if t == nil || r.Card == nil {
				return misaligned("composite query %d returned a record of the table %q", i, r.Table)
			}
			put(t, r.Card, ld)
		}
		_, lists := rowsOf[q.Kind]
		if a.HasMore && (!lists || len(a.Rows) == 0) {
			return misaligned("composite query %d (%s) says more rows than it returned, and it lists %d of them", i, q.Kind, len(a.Rows))
		}
		if len(a.Rows) > 0 {
			tables, ok := rowsOf[q.Kind]
			if !ok {
				return misaligned("composite query %d (%s) lists rows, and no such query does", i, q.Kind)
			}
			if most := q.units(); len(a.Rows) > most {
				return misaligned("composite query %d (%s) lists %d rows, and it was read for at most %d", i, q.Kind, len(a.Rows), most)
			}
			// A listing with more rows than it returned is not the table's rows:
			// what it leaves the table's rows as they were (read whole by another
			// query, or not read) and never marks them read.
			if !a.HasMore {
				for _, name := range tables {
					t := s.T(name)
					t.SetRows(append([]string(nil), a.Rows...))
					t.part.rows = true
				}
			}
		}
		if len(a.Counts) > 0 {
			name, ok := countsOf[q.Kind]
			if !ok {
				return misaligned("composite query %d (%s) gives counts, and no such query does", i, q.Kind)
			}
			t := s.T(name)
			for _, c := range a.Counts {
				if c.Row == "" || c.Col == "" || c.N < 0 {
					return misaligned("composite query %d gives the count %d of the cell %q:%q", i, c.N, c.Row, c.Col)
				}
				t.part.counts[[2]string{c.Row, c.Col}] = c.N
			}
		}
		if lists && len(a.Rows) == 0 {
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
		if q.Kind == QueryRelated {
			if err := p.followed(s, i, q, a); err != nil {
				return err
			}
		}
	}
	return nil
}

// followed records, for each id `related` read, the follows that reach a
// table's cards of a primary (followTables), so that Table.Of answers for the
// primaries every such follow was read of. The ids are the ones the query's
// source lists, or, for a head or a line, the ones the answer says the source
// named.
func (p *Partial) followed(s *Snapshot, i int, q SprintQ, a Answer) error {
	ids := q.Source.IDs
	if q.Source.Kind == SourceIDs {
		if len(a.IDs) != 0 && !slices.Equal(a.IDs, q.Source.IDs) {
			return misaligned("composite query %d names ids, and its answer names others", i)
		}
	} else {
		most, _ := q.Source.size()
		if len(a.IDs) > most {
			return misaligned("composite query %d: its source names at most %d ids, the answer names %d", i, most, len(a.IDs))
		}
		ids = a.IDs
	}
	for _, f := range q.Follow {
		name, ok := followTables[f]
		if !ok {
			continue
		}
		t := s.T(name)
		for _, id := range ids {
			t.part.followed[[2]string{id, f}] = true
		}
	}
	return nil
}

// Unloaded is what the planners read of the snapshot that its plan did not
// load (1.5.2): the cells, counts, rows, lines, cards of a primary and
// positions, each named. A release build does not panic at such a read: it
// plans on nothing, and the caller refuses the plan when this is not empty
// (UnloadedErr). A snapshot built whole has none.
func (s *Snapshot) Unloaded() []string {
	if s == nil || s.Partial == nil {
		return nil
	}
	return s.Partial.log.list()
}

// ErrUnloaded is the refusal of a plan made on a read that did not load what
// the planner read (1.5.2): errors.Is(err, ErrUnloaded) names it.
var ErrUnloaded = errors.New(unloadedMessage)

// UnloadedError is ErrUnloaded with the reads that were refused, each named.
type UnloadedError struct{ Reads []string }

func (e *UnloadedError) Error() string { return strings.Join(e.Reads, "; ") }

// Is says the error is ErrUnloaded.
func (e *UnloadedError) Is(target error) bool { return target == ErrUnloaded }

// UnloadedErr is nil when the planners read only what the snapshot's plan
// loaded, and otherwise an *UnloadedError naming what they read that it did not:
// the tick refuses the rule's plan then, as a release build refuses (1.5.2).
func (s *Snapshot) UnloadedErr() error {
	if reads := s.Unloaded(); len(reads) > 0 {
		return &UnloadedError{Reads: reads}
	}
	return nil
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

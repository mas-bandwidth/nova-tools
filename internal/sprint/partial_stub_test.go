package sprint

import (
	"sort"
	"strconv"
	"strings"
)

// This file stands in for two things that are not in the tree yet, and is
// deleted when they merge: layer 1's in-memory twin (internal/tset, item S4a),
// whose atomic read answers the ids, ranges, counts and rcounts of a
// ReadPlan, and the sprint's composite queries (IT30, Twin.Query), whose
// `front`, `streams`, `fleet` and `readers` answer the rest. The stub answers a
// ReadPlan from a snapshot built whole, so that a test can load the plan's
// answer with LoadPartial and compare what it holds with the whole. It reads
// nothing else and touches no store.

// readAnswerer is what the tests read a plan through: the twin's read.
type readAnswerer interface {
	Answer(rp ReadPlan) ReadAnswer
}

// wholeStore answers a plan from a snapshot built whole.
type wholeStore struct{ s *Snapshot }

var _ readAnswerer = wholeStore{}

// scoreBound is a score bound of a range or an rcount as the number it means
// and whether it is exclusive; an empty or infinite bound takes everything.
func scoreBound(b string) (v float64, exclusive, none bool) {
	switch b {
	case "", "-inf", "+inf", "inf":
		return 0, false, true
	}
	if strings.HasPrefix(b, "(") {
		exclusive = true
		b = b[1:]
	}
	v, err := strconv.ParseFloat(b, 64)
	if err != nil {
		panic("stub: score bound " + b)
	}
	return v, exclusive, false
}

// inBounds says a score is inside [min, max].
func inBounds(score float64, min, max string) bool {
	if v, ex, none := scoreBound(min); !none && (score < v || ex && score == v) {
		return false
	}
	if v, ex, none := scoreBound(max); !none && (score > v || ex && score == v) {
		return false
	}
	return true
}

// cellCards are the cards of a cell of the whole table with a score in
// [min, max], in score order.
func (w wholeStore) cellCards(table, cell, min, max string) []*Card {
	row, col, ok := cellRef(cell)
	if !ok {
		panic("stub: cell " + cell)
	}
	var out []*Card
	for _, c := range w.s.T(table).Cell(row, col) {
		if inBounds(c.Score, min, max) {
			out = append(out, c)
		}
	}
	return out
}

// Answer answers the plan's queries in its order.
func (w wholeStore) Answer(rp ReadPlan) ReadAnswer {
	ans := ReadAnswer{Epoch: w.s.Epoch, TimeMS: 1790000000123, IDs: map[string][]*Card{}}
	for table, ids := range rp.IDs {
		if len(ids) == 0 {
			continue
		}
		recs := make([]*Card, len(ids))
		for i, id := range ids {
			recs[i] = w.s.T(table).Card(id)
		}
		ans.IDs[table] = recs
	}
	for _, q := range rp.Ranges {
		var a RangeA
		cards := w.cellCards(q.Table, q.Cell, q.Min, q.Max)
		if q.Desc {
			for i, j := 0, len(cards)-1; i < j; i, j = i+1, j-1 {
				cards[i], cards[j] = cards[j], cards[i]
			}
		}
		if len(cards) > q.Limit {
			cards, a.HasMore = cards[:q.Limit], true
		}
		for _, c := range cards {
			a.IDs = append(a.IDs, c.ID)
			a.Scores = append(a.Scores, c.Score)
			if q.Records {
				a.Cards = append(a.Cards, c)
			}
		}
		ans.Ranges = append(ans.Ranges, a)
	}
	for _, q := range rp.Counts {
		var counts []int
		for _, cell := range q.Cells {
			counts = append(counts, len(w.cellCards(q.Table, cell, "", "")))
		}
		ans.Counts = append(ans.Counts, counts)
	}
	for _, q := range rp.RCounts {
		var a RCountA
		for _, cell := range q.Cells {
			n := len(w.cellCards(q.Table, cell, q.Min, q.Max))
			a.Counts = append(a.Counts, n)
			a.Sum += n
		}
		ans.RCounts = append(ans.RCounts, a)
	}
	for range rp.Lines {
		ans.Lines = append(ans.Lines, LinesA{})
	}
	for _, q := range rp.Sprint {
		ans.Sprint = append(ans.Sprint, w.query(q))
	}
	return ans
}

// query answers one composite query: `front` by scanning the stream's cells
// (what the store's indexes make O(1)), and the listing queries by their rows.
func (w wholeStore) query(q SprintQ) Answer {
	a := Answer{Kind: q.Kind}
	switch q.Kind {
	case QueryFront:
		f := FrontAnswer{Stream: q.Stream}
		for _, c := range w.s.Work.Cell(q.Stream, Waiting) {
			if IsSentinel(c) {
				f.G, f.Sigma = c.ID, c.Score
				a.Records = append(a.Records, TableCard{Work, c})
				break
			}
		}
		if f.G != "" {
			f.NBefore = sort.Search(len(w.s.Work.openLine(q.Stream)), func(i int) bool {
				return w.s.Work.openLine(q.Stream)[i].Score >= f.Sigma
			})
		}
		a.Front = &f
	case QueryStreams:
		a.Rows = append([]string(nil), w.s.Work.Rows...)
	case QueryFleet:
		a.Rows = append([]string(nil), w.s.Fleet.Rows...)
	case QueryReaders:
		a.Rows = append([]string(nil), w.s.Readers.Rows...)
	}
	return a
}

package sprint

import (
	"sort"
	"strconv"
	"strings"
)

// This file stands in for what is not in the tree yet, and is deleted when it
// merges: the table twin, tset.Mem (the tset package, on Layer 1's branch), whose
// atomic read answers the ids, ranges, counts and rcounts of a ReadPlan. It has
// no answer for a line or for a composite query (the errata to version 2.1,
// E1 and E3), and this stub builds those by hand, as IT05's tests do: a lines
// query answers no line, and a composite query is answered from the whole
// snapshot the stub holds (`front` by scanning the stream's cells, what the
// store's indexes make O(1); the listing queries by their rows; IT30's
// Twin.Query answers them for real). The stub answers a ReadPlan from a
// snapshot built whole, so that a test can load the plan's answer with
// LoadPartial and compare what it holds with the whole. It reads nothing else
// and touches no store, and it composes no twin: tset.Mem is the table twin
// only.

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

// Answer answers the plan's queries: the Layer 1 answers in the order of
// ReadPlan.TsetSlots, and the composite queries in the plan's.
func (w wholeStore) Answer(rp ReadPlan) ReadAnswer {
	ans := ReadAnswer{Epoch: Decimal(itoa(int(w.s.Epoch))), ActiveEpoch: Decimal(itoa(int(w.s.Epoch))), TimeMS: "1790000000123"}
	for _, sl := range rp.TsetSlots() {
		switch sl.Kind {
		case AnswerIDs:
			ids := rp.IDs[sl.Table]
			recs := make([]*Card, len(ids))
			for i, id := range ids {
				recs[i] = w.s.T(sl.Table).Card(id)
			}
			ans.Tset = append(ans.Tset, TsetAnswer{Kind: AnswerIDs, Records: recs})
		case AnswerRange:
			ans.Tset = append(ans.Tset, w.rangeOf(rp.Ranges[sl.Index]))
		case AnswerCount:
			q := rp.Counts[sl.Index]
			a := TsetAnswer{Kind: AnswerCount}
			for _, cell := range q.Cells {
				a.Counts = append(a.Counts, len(w.cellCards(q.Table, cell, "", "")))
			}
			ans.Tset = append(ans.Tset, a)
		case AnswerRCount:
			q := rp.RCounts[sl.Index]
			a := TsetAnswer{Kind: AnswerRCount}
			for _, cell := range q.Cells {
				n := len(w.cellCards(q.Table, cell, q.Min, q.Max))
				a.Counts = append(a.Counts, n)
				a.Sum += n
			}
			ans.Tset = append(ans.Tset, a)
		case AnswerLines:
			ans.Tset = append(ans.Tset, TsetAnswer{Kind: AnswerLines})
		}
	}
	for _, q := range rp.Sprint {
		ans.Sprint = append(ans.Sprint, w.query(q))
	}
	return ans
}

// rangeOf answers a range of a cell.
func (w wholeStore) rangeOf(q RangeQ) TsetAnswer {
	a := TsetAnswer{Kind: AnswerRange}
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
			a.Records = append(a.Records, c)
		}
	}
	return a
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

package sprint

import (
	"cmp"
	"math"
	"reflect"
	"slices"
	"testing"
)

// The fleet rules' read twin. A plan is made on a snapshot loaded from its own
// read (LoadPartial of the read plan and an answer), so that it can read only
// what the read named. The twin answers a ReadPlan from a snapshot built whole,
// the way the store's composite queries would (1.0): every record cut to the
// fields of its query, heads by score and cut at their limit, the counts of a
// fleet's cells, the position front answers, and the ranges over the sprint's own
// keys (the members present; a beat entry with its score) from the facts the test
// holds as the store's state. It answers the queries the fleet rules ask (related
// over ids or a cell head, fleet, streams, front, and those ranges) and nothing
// else, and it panics on any other, so that a rule that starts asking for more is
// found here.

// cutTo is a record read with a projection: the fields the query named, and
// only those a record has.
func cutTo(c *Card, fields []string) *Card {
	cc := &Card{ID: c.ID, Row: c.Row, Col: c.Col, Score: c.Score, Rev: c.Rev, Fields: map[string]string{}}
	for _, f := range fields {
		if v, ok := c.Fields[f]; ok {
			cc.Fields[f] = v
		}
	}
	return cc
}

// fleetTwin answers read plans from a snapshot built whole.
type fleetTwin struct {
	whole *Snapshot
	// extra are records the first composite query also returns, for a test that
	// needs a record the read does not ask for by itself.
	extra []TableCard
	// facts are the sprint's own keys as the store holds them: the beat entries of
	// the due set, {p}strangers as noticed, and the dropping marks.
	facts fleetFacts
}

// keyRange answers a range over a sprint key: its members present, in the order
// of a sorted set (by score, then name; the beat entries have scores, the others
// none), cut at the query's limit with HasMore when there are more.
func (tw fleetTwin) keyRange(q RangeQ) TsetAnswer {
	if q.Key == "" || q.Table != "" || q.Cell != "" || q.Records || q.Desc || q.Min != "" || q.Max != "" {
		panic("the twin: a range that is not over a whole sprint key")
	}
	a := TsetAnswer{Kind: AnswerRange}
	names := func(m map[string]bool) []string {
		var out []string
		for k, ok := range m {
			if ok {
				out = append(out, k)
			}
		}
		slices.Sort(out)
		return out
	}
	switch q.Key {
	case factBeats:
		var ms []string
		for m := range tw.facts.BeatDue {
			ms = append(ms, m)
		}
		slices.SortFunc(ms, func(x, y string) int {
			if c := cmp.Compare(tw.facts.BeatDue[x], tw.facts.BeatDue[y]); c != 0 {
				return c
			}
			return cmp.Compare(x, y)
		})
		for _, m := range ms {
			a.IDs = append(a.IDs, beatKeyPrefix+m)
			a.Scores = append(a.Scores, float64(tw.facts.BeatDue[m]))
		}
	case factStrangers:
		a.IDs = names(tw.facts.Strangers)
	case factDropping:
		a.IDs = names(tw.facts.Dropping)
	default:
		panic("the twin: a range over the sprint key " + q.Key)
	}
	if q.Limit > 0 && len(a.IDs) > q.Limit {
		a.IDs, a.HasMore = a.IDs[:q.Limit], true
		if a.Scores != nil {
			a.Scores = a.Scores[:q.Limit]
		}
	}
	return a
}

// Answer answers the plan: the ranges over the sprint's own keys (the only Layer
// 1 query the fleet rules ask), and each composite query in the plan's order. The
// times are the snapshot's epoch and a fixed time.
func (tw fleetTwin) Answer(rp ReadPlan) ReadAnswer {
	if len(rp.IDs)+len(rp.Counts)+len(rp.RCounts)+len(rp.Lines) != 0 {
		panic("the fleet rules' twin answers composite queries and ranges over sprint keys only")
	}
	ans := ReadAnswer{Epoch: Decimal(itoa(int(tw.whole.Epoch))), ActiveEpoch: Decimal(itoa(int(tw.whole.Epoch))), TimeMS: "1790000000123"}
	for _, sl := range rp.TsetSlots() {
		ans.Tset = append(ans.Tset, tw.keyRange(rp.Ranges[sl.Index]))
	}
	for i, q := range rp.Sprint {
		a := tw.query(q)
		if i == 0 {
			a.Records = append(a.Records, tw.extra...)
		}
		ans.Sprint = append(ans.Sprint, a)
	}
	return ans
}

// query answers one composite query.
func (tw fleetTwin) query(q SprintQ) Answer {
	w := tw.whole
	a := Answer{Kind: q.Kind}
	switch q.Kind {
	case QueryFleet:
		rows := w.Fleet.Rows()
		if q.Units > 0 && q.Units < len(rows) {
			rows, a.HasMore = rows[:q.Units], true // a listing cut at its units says there are more
		}
		a.Rows = append([]string(nil), rows...)
		for _, m := range rows {
			if ctl := w.MemberCtl(m); ctl != nil {
				a.Records = append(a.Records, TableCard{Fleet, cutTo(ctl, q.Fields)})
			}
			for _, col := range []string{Ready, Working, Withdrawn} {
				a.Counts = append(a.Counts, CellCount{Row: m, Col: col, N: w.Fleet.Count(m, col)})
			}
		}
		a.Props = propsAnswer(w.Fleet, q.Props)
	case QueryStreams:
		rows := w.Work.Rows()
		if q.Units > 0 && q.Units < len(rows) {
			rows, a.HasMore = rows[:q.Units], true
		}
		a.Rows = append([]string(nil), rows...)
		for _, st := range rows {
			if ctl := w.StreamCtl(st); ctl != nil {
				a.Records = append(a.Records, TableCard{Merge, cutTo(ctl, q.Fields)})
			}
		}
	case QueryRelated:
		t := w.T(q.Table)
		var ids []string
		switch q.Source.Kind {
		case SourceIDs:
			ids = q.Source.IDs
		case SourceHead:
			row, col, ok := cellRef(q.Source.Key)
			if !ok {
				panic("the twin: a head of " + q.Source.Key)
			}
			cards := t.Cell(row, col)
			if len(cards) > q.Source.Limit {
				cards = cards[:q.Source.Limit]
			}
			for _, c := range cards {
				ids = append(ids, c.ID)
			}
			a.IDs = ids
		default:
			panic("the twin: a source of kind " + q.Source.Kind)
		}
		for _, id := range ids {
			c := t.Card(id)
			if c == nil {
				continue // an id with no record has none in the answer
			}
			a.Records = append(a.Records, TableCard{q.Table, cutTo(c, q.Fields)})
			for _, f := range q.Follow {
				if f != followPrimary {
					panic("the twin: a follow " + f)
				}
				if p := w.Work.Card(c.F("primary")); p != nil {
					a.Records = append(a.Records, TableCard{Work, cutTo(p, q.Fields)})
				}
			}
		}
	case QueryFront:
		f := FrontAnswer{Stream: q.Stream}
		sigma := math.Inf(1)
		for _, c := range w.Work.Cell(q.Stream, Waiting) {
			if IsSentinel(c) {
				f.G, f.Sigma, sigma = c.ID, c.Score, c.Score
				a.Records = append(a.Records, TableCard{Work, cutTo(c, q.Fields)})
				break
			}
		}
		if f.G != "" {
			f.NBefore = OpenBefore(w, q.Stream, f.Sigma)
		}
		a.Front = &f
		for _, h := range q.Heads {
			n := 0
			for _, c := range w.Work.Cell(q.Stream, Ready) {
				if n >= h.Limit {
					break
				}
				if IsSentinel(c) || c.F("refused") != "" {
					continue
				}
				var in bool
				switch h.Index {
				case HeadFreshBelow:
					in = c.Int("attempt") == 0 && c.Score < sigma
				case HeadAgain:
					in = c.Int("attempt") >= 1 && c.F("bound") == ""
				default:
					panic("the twin: a head " + h.Index)
				}
				if !in {
					continue
				}
				n++
				a.Records = append(a.Records, TableCard{Work, cutTo(c, q.Fields)})
				for _, fo := range h.Follow {
					if fo != FollowWithdrawn {
						panic("the twin: a follow " + fo)
					}
					if wc := w.Fleet.Card(WorkCardID(c.ID, c.Int("attempt"))); wc != nil && wc.Col == Withdrawn {
						a.Records = append(a.Records, TableCard{Fleet, cutTo(wc, q.Fields)})
					}
				}
			}
		}
	case QueryBeat:
		// each member's beat entry in the due set, with its score
		for _, m := range q.Source.IDs {
			if score, ok := tw.facts.BeatDue[m]; ok {
				a.Keys = append(a.Keys, KeyAnswer{Key: QueryBeat, Subject: m, N: uint64(score)})
			}
		}
	default:
		panic("the twin: a query of kind " + q.Kind)
	}
	return a
}

// samePlan fails unless the two plans are the same, ignoring the snapshot each
// was built on.
func samePlan(t *testing.T, what string, partial, whole RulePlan) {
	t.Helper()
	partial.Plan.pre, whole.Plan.pre = nil, nil
	if !reflect.DeepEqual(partial, whole) {
		t.Fatalf("%s: the plan made on what the read loaded is not the plan made on the whole sprint:\n partial %+v\n whole   %+v", what, partial, whole)
	}
}

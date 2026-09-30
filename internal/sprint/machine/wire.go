package machine

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The read of one rule, from its plan to its snapshot (1.4.2 RT2; 1.5.1). A
// rule's Read gives a sprint.ReadPlan; the tick sends it as one ns_sprint_read
// (sprintfn.ReadRequest) and loads the answer with sprint.LoadPartial. The
// Layer 1 and Layer 2 queries go in the order ReadPlan.TsetSlots gives, which
// is the order LoadPartial reads their answers in (IT05's choice); the
// sprint's queries follow, one for one.

// sprintKey is the raw key of a sorted set a RangeQ names in its Key form: the
// key itself when it is already under the deployment's prefix, and otherwise
// the per-epoch sprint key {p}<key>@e (1.0, "Keys"), the form of the indexes
// (fresh:s, elig:s) and the agenda.
func sprintKey(names sprint.Names, epoch tset.Decimal, key string) string {
	if strings.HasPrefix(key, names.Prefix) {
		return key
	}
	return names.Key(key) + "@" + string(epoch)
}

// decimal is an unsigned number as an exact decimal string.
func decimal(n uint64) tset.Decimal { return tset.Decimal(strconv.FormatUint(n, 10)) }

// readRequest is the one atomic read of a rule's plan.
func readRequest(names sprint.Names, epoch tset.Decimal, rp sprint.ReadPlan) (*sprintfn.ReadRequest, error) {
	if err := rp.Validate(); err != nil {
		return nil, err
	}
	rr := &sprintfn.ReadRequest{Epoch: epoch}
	for _, sl := range rp.TsetSlots() {
		var q tset.ReadQuery
		switch sl.Kind {
		case sprint.AnswerIDs:
			q = tset.ReadQuery{Kind: "ids", Table: sl.Table, IDs: rp.IDs[sl.Table]}
		case sprint.AnswerRange:
			r := rp.Ranges[sl.Index]
			q = tset.ReadQuery{Kind: "range", Table: r.Table, Cell: r.Cell, Min: bound(r.Min, "-inf"), Max: bound(r.Max, "+inf"),
				Limit: r.Limit, Desc: r.Desc, Records: r.Records, Fields: r.Fields}
			if r.Key != "" {
				q.Key = sprintKey(names, epoch, r.Key)
			}
		case sprint.AnswerCount:
			c := rp.Counts[sl.Index]
			q = tset.ReadQuery{Kind: "count", Table: c.Table, Cells: c.Cells}
		case sprint.AnswerRCount:
			c := rp.RCounts[sl.Index]
			q = tset.ReadQuery{Kind: "rcount", Table: c.Table, Cells: c.Cells, Min: bound(c.Min, "-inf"), Max: bound(c.Max, "+inf")}
		case sprint.AnswerLines:
			l := rp.Lines[sl.Index]
			q = tset.ReadQuery{Kind: "lines", AfterSeq: decimal(l.After), Limit: l.Limit}
			if l.Through > 0 {
				through := decimal(l.Through)
				q.ThroughSeq = &through
			}
		default:
			return nil, fmt.Errorf("machine: a read plan's query of the kind %q", sl.Kind)
		}
		rr.Tset = append(rr.Tset, q)
	}
	for _, q := range rp.Sprint {
		var w sprintfn.SprintQuery
		var ref *sprintfn.Refusal
		if q.Kind == sprint.QueryBeat {
			// the beat read is a sprint-key read (sprintfn KeyBeat): one ZMSCORE
			// of the due set for the members named
			w, ref = sprintfn.EncodeKeyQ(sprintfn.KeyQ{Kind: sprintfn.KeyBeat, IDs: q.Source.IDs})
		} else {
			w, ref = sprintfn.EncodeSprintQ(q)
		}
		if ref != nil {
			return nil, ref
		}
		rr.Sprint = append(rr.Sprint, w)
	}
	return rr, nil
}

// beatAnswer is the beat read's reply as sprint.QueryBeat's answer: a KeyAnswer
// for each member named whose beat entry is in the due set, with its score.
func beatAnswer(q sprint.SprintQ, raw []byte) (sprint.Answer, error) {
	res, err := sprintfn.DecodeResult(sprintfn.KeyBeat, raw)
	if err != nil {
		return sprint.Answer{}, err
	}
	b, ok := res.(sprintfn.BeatResult)
	if !ok || len(b.Scores) != len(q.Source.IDs) {
		return sprint.Answer{}, fmt.Errorf("machine: the beat read of %d members answered %T", len(q.Source.IDs), res)
	}
	a := sprint.Answer{Kind: sprint.QueryBeat}
	for i, s := range b.Scores {
		if s == nil {
			continue
		}
		f, err := strconv.ParseFloat(*s, 64)
		if err != nil || f < 0 {
			return sprint.Answer{}, fmt.Errorf("machine: the beat of %s is scored %q", q.Source.IDs[i], *s)
		}
		a.Keys = append(a.Keys, sprint.KeyAnswer{Key: sprint.QueryBeat, Subject: q.Source.IDs[i], N: uint64(f)})
	}
	return a, nil
}

// bound is a score bound, or its default when the plan leaves it empty.
func bound(b, otherwise string) string {
	if b == "" {
		return otherwise
	}
	return b
}

// readAnswer is a read's reply in the words LoadPartial reads (errata E3).
func readAnswer(rp sprint.ReadPlan, rep *sprintfn.ReadReply) (sprint.ReadAnswer, error) {
	ans := sprint.ReadAnswer{Epoch: sprint.Decimal(rep.Epoch), ActiveEpoch: sprint.Decimal(rep.ActiveEpoch), TimeMS: sprint.Decimal(rep.TimeMS)}
	slots := rp.TsetSlots()
	if len(rep.Tset) != len(slots) || len(rep.Sprint) != len(rp.Sprint) {
		return ans, fmt.Errorf("machine: %d and %d queries read, %d and %d answered", len(slots), len(rp.Sprint), len(rep.Tset), len(rep.Sprint))
	}
	for i, sl := range slots {
		a := rep.Tset[i]
		out := sprint.TsetAnswer{Kind: sl.Kind, IDs: a.IDs, HasMore: a.HasMore, Sum: int(a.Sum)}
		for _, s := range a.Scores {
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return ans, fmt.Errorf("machine: query %d: the score %q", i, s)
			}
			out.Scores = append(out.Scores, f)
		}
		for _, c := range a.Counts {
			out.Counts = append(out.Counts, int(c))
		}
		for _, r := range a.Records {
			if !r.Exists {
				out.Records = append(out.Records, nil)
				continue
			}
			out.Records = append(out.Records, cardOf(r))
		}
		for _, raw := range a.Lines {
			id, err := lineID(raw)
			if err != nil {
				return ans, fmt.Errorf("machine: query %d: %w", i, err)
			}
			out.Lines = append(out.Lines, sprint.LogLine{ID: id, Body: lineBody(raw)})
		}
		ans.Tset = append(ans.Tset, out)
	}
	for i, q := range rp.Sprint {
		if q.Kind == sprint.QueryBeat {
			a, err := beatAnswer(q, rep.Sprint[i])
			if err != nil {
				return ans, err
			}
			ans.Sprint = append(ans.Sprint, a)
			continue
		}
		res, err := sprintfn.DecodeResult(q.Kind, rep.Sprint[i])
		if err != nil {
			return ans, err
		}
		ans.Sprint = append(ans.Sprint, res.Project(q))
	}
	return ans, nil
}

// cardOf is a record of Layer 1 as the sprint's card.
func cardOf(r tset.MemberRecord) *sprint.Card {
	c := &sprint.Card{ID: r.ID, Fields: map[string]string{}}
	if r.Place != nil {
		c.Row, c.Col = r.Place.Row, r.Place.Col
	}
	if r.Score != "" {
		c.Score, _ = strconv.ParseFloat(r.Score, 64)
	}
	if r.Revision != "" {
		c.Rev, _ = strconv.ParseUint(string(r.Revision), 10, 64)
	}
	for name, v := range r.Fields {
		if v.Present {
			c.Fields[name] = v.Value
		}
	}
	return c
}

// lineID is the stream id of a line as the log returns it: <seq>-0 (L2 2),
// from the line's own seq. A line with no seq cannot be placed.
func lineID(raw json.RawMessage) (string, error) {
	seq, err := lineSeq(raw)
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(seq, 10) + "-0", nil
}

// lineBody is the body of a line as a lines query returns it: Layer 1's item
// {seq, n, d} carries the stored body verbatim in d (L1 7), which is the
// line ParseEvent reads; an item with no string d is the line itself.
func lineBody(raw json.RawMessage) []byte {
	var item struct {
		D *string `json:"d"`
	}
	if json.Unmarshal(raw, &item) == nil && item.D != nil {
		return []byte(*item.D)
	}
	return raw
}

// lineSeq is a line's seq, an exact decimal string or a JSON number.
func lineSeq(raw json.RawMessage) (uint64, error) {
	var l struct {
		Seq json.RawMessage `json:"seq"`
	}
	if err := json.Unmarshal(raw, &l); err != nil || len(l.Seq) == 0 {
		return 0, fmt.Errorf("a line with no seq")
	}
	s := strings.Trim(string(l.Seq), `"`)
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("a line whose seq is %s", l.Seq)
	}
	return n, nil
}

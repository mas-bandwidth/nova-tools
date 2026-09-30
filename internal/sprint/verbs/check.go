package verbs

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// CheckCycleCode is check's refusal of a table holding a cycle of needs (errata
// 3 amendment 7: "check reports it"): exit 2, the loops said.
const CheckCycleCode = "CYCLE"

// checkFields are what check reads of a card of the work table: what it waits
// for (cycle.go: its needs less the waived ones, and a sentinel's place).
var checkFields = []string{"needs", "waived", "kind"}

// Check is the new path's check (section 3; errata 3 amendment 7, check's rule
// 11): the work table's open line of every stream, read whole, and every cycle
// of needs on it, through the named needs and the gates' implicit ones
// (sprint.Cycles, cycle.go), each said as "a cycle through <loop>: <n> cards
// can never be reached". A table with a cycle is refused CYCLE (exit 2) with
// the loops; a clean one says so. The rest of check's rules are IT26's. One
// round trip for the rows, and one a stream for its open cells; a cell past
// what one range returns (MaxRangeLimit) is refused LIMIT, never read in part.
func Check(ctx context.Context, e *Env) (Result, error) {
	const verb = "check"
	res, err := e.Do(ctx, Planned{Verb: verb, Read: func(epoch tset.Decimal) *sprintfn.ReadRequest {
		return &sprintfn.ReadRequest{Epoch: epoch, Tset: []tset.ReadQuery{{Kind: "rows", Table: sprint.Work}}}
	}})
	if err != nil {
		return res, err
	}
	if res.Read == nil || len(res.Read.Tset) != 1 {
		return res, fmt.Errorf("check: the rows read answered nothing")
	}
	var rows []string
	for _, r := range res.Read.Tset[0].Rows {
		rows = append(rows, r.Row)
	}
	work := sprint.NewTable(sprint.Work)
	work.SetRows(rows)
	var open []string
	for _, st := range sprint.States {
		if st != sprint.Landed {
			open = append(open, string(st))
		}
	}
	trips := res.Trips
	for _, row := range rows {
		cells, err := e.Do(ctx, Planned{Verb: verb, Read: func(epoch tset.Decimal) *sprintfn.ReadRequest {
			rr := &sprintfn.ReadRequest{Epoch: epoch}
			for _, col := range open {
				rr.Tset = append(rr.Tset, tset.ReadQuery{Kind: "range", Table: sprint.Work, Cell: row + ":" + col,
					Min: "-inf", Max: "+inf", Limit: sprint.MaxRangeLimit, Records: true, Fields: checkFields})
			}
			return rr
		}})
		trips += cells.Trips
		if err != nil {
			return cells, err
		}
		if cells.Read == nil || len(cells.Read.Tset) != len(open) {
			return cells, fmt.Errorf("check: the read of stream %s answered %d cells, asked %d", row, len(cells.Read.Tset), len(open))
		}
		for i, a := range cells.Read.Tset {
			if a.HasMore {
				return cells, refuseLocal(verb, sprintfn.CodeLimit, "stream %s's %s cell holds more than %d cards, which one read returns", row, open[i], sprint.MaxRangeLimit)
			}
			for _, r := range a.Records {
				if c := checkCard(r); c != nil {
					work.Put(c)
				}
			}
		}
	}
	res.Trips = trips
	found := sprint.Cycles(&sprint.Snapshot{Work: work})
	if len(found) == 0 {
		res.Said = fmt.Sprintf("check: no cycle of needs in %d streams", len(rows))
		return res, nil
	}
	var said []string
	for _, f := range found {
		said = append(said, f.String())
	}
	res.Said = "check: " + strings.Join(said, "\ncheck: ")
	return res, refuseLocal(verb, CheckCycleCode, "%d cycles of needs: %s", len(found), strings.Join(said, "; "))
}

// checkCard is a record of the work table as the sprint's card: its place,
// score and the fields read.
func checkCard(r tset.MemberRecord) *sprint.Card {
	if !r.Exists || r.Place == nil {
		return nil
	}
	c := &sprint.Card{ID: r.ID, Row: r.Place.Row, Col: r.Place.Col, Fields: map[string]string{}}
	c.Score, _ = strconv.ParseFloat(r.Score, 64)
	if rev, err := strconv.ParseUint(string(r.Revision), 10, 64); err == nil {
		c.Rev = rev
	}
	for k, v := range r.Fields {
		if v.Present {
			c.Fields[k] = v.Value
		}
	}
	return c
}

package store

import (
	"context"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// TierCosts is where's cost visibility (docs/SPEC-SPRINT.md section 1, the where view;
// sprint/cost_view.go): every card counted by its brief's tier, and each stream's tiers,
// dollars per landed card and spend by the tier each attempt ran on, from the cost records
// the cards already hold. It reads the work table's cards in read sets of at most
// ntable.LimitReadSetMembers and, as a display, takes them as they come: a set that saw the
// table move since its shape is used as read and never taken again, so a busy table costs
// one pass and never fails the view. Placed cards only; a card moved off the table between
// the shape and its set is left out.
func (st *Store) TierCosts(ctx context.Context) (map[string]int, map[string]sprint.TierCosts, error) {
	st, err := st.pin(ctx)
	if err != nil {
		return nil, nil, err
	}
	shapes, err := st.shapes(ctx, []string{st.Names.Table(sprint.Work)})
	if err != nil {
		return nil, nil, err
	}
	ids, err := st.B.CellIDs(ctx, shapes)
	if err != nil {
		return nil, nil, err
	}
	shape := shapes[0]
	t := sprint.NewTable(sprint.Work)
	t.Epoch, t.Revision = shape.Epoch, shape.Revision
	for _, r := range shape.Rows {
		t.SetRows(append(t.Rows(), r.Key))
	}
	all := ids[shape.Name]
	for start := 0; start < len(all); start += ntable.LimitReadSetMembers {
		res, err := st.readSet(ctx, shape.Name, all[start:min(start+ntable.LimitReadSetMembers, len(all))])
		if err != nil {
			return nil, nil, err
		}
		for _, m := range res.Members {
			if !m.Placed {
				continue
			}
			c := &sprint.Card{ID: sprint.CardID(m.ID), Score: m.Score, Rev: m.Revision, Fields: m.Fields, Row: m.Row, Col: m.Col}
			t.Put(c)
		}
	}
	// a record with no tier of its own takes one by the route table (sprint.RecordTier)
	routes, _, err := st.Routes(ctx)
	if err != nil {
		return nil, nil, err
	}
	s := &sprint.Snapshot{Work: t}
	return sprint.TierCounts(s), sprint.StreamTierCosts(s, sprint.RulesOf(routes)), nil
}

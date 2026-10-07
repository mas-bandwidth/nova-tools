package store

import (
	"context"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Recount re-derives every friend, member, route and stream counter from the
// attempt records (sprint.RecountPlan) and writes the moves. dry prints nothing
// itself: the caller prints the rows, and a dry call writes nothing. A second
// call finds an empty plan.
func (st *Store) Recount(ctx context.Context, dry bool) (rows []sprint.RowRecount, epoch uint64, err error) {
	st, err = st.pin(ctx)
	if err != nil {
		return nil, 0, err
	}
	s, err := st.Load(ctx, []string{sprint.Work, sprint.Fleet, sprint.Readers}, sprint.StatsRecords)
	if err != nil {
		return nil, 0, err
	}
	epoch = s.Epoch
	plan, rows := sprint.RecountPlan(s)
	if dry || plan.Empty() {
		return rows, epoch, nil
	}
	_, err = st.Run(ctx, Step{
		Verb:   "stats recount",
		Load:   []string{sprint.Work, sprint.Fleet, sprint.Readers},
		Extras: sprint.StatsRecords,
		Plan: func(s *sprint.Snapshot) sprint.Plan {
			p, _ := sprint.RecountPlan(s)
			return p
		},
	})
	return rows, epoch, err
}

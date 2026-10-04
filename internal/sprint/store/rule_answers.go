package store

import (
	"context"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// RuleAnswers is what the tick's rules would do with every open judgment now
// (sprint.RuleAnswers), read as the tick's rule parts read: every table with the
// work table's queue applied (sprint.WithQueue, as every step but the pump plans),
// the needs off the table, the routes and the rules nova-config turns off, and
// the machine's STOPPED time for running time. It writes nothing: `rules` prints it.
func (st *Store) RuleAnswers(ctx context.Context) ([]sprint.RuleAnswer, error) {
	pinned, err := st.pin(ctx)
	if err != nil {
		return nil, err
	}
	s, err := pinned.Load(ctx, All, tickExtras)
	if err != nil {
		return nil, err
	}
	q, err := pinned.B.QueueRead(ctx)
	if err != nil {
		return nil, err
	}
	s = sprint.WithQueue(s, q)
	set, err := pinned.routes(ctx)
	if err != nil {
		return nil, err
	}
	set.into(s)
	m, _, err := pinned.Machine(ctx)
	if err != nil {
		return nil, err
	}
	return sprint.RuleAnswers(s, sprint.TickReq{Who: sprint.MachineActor, Stopped: m.StoppedBetween, AnswerRules: true}), nil
}

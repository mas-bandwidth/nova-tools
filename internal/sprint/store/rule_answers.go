package store

import (
	"context"

	"github.com/nova-tools/internal/sprint"
)

// RuleAnswers is what the tick's rules would do with every open judgment now
// (sprint.RuleAnswers), read as the tick's rule parts read: every table with the
// work table's queue applied (sprint.WithQueue, as every step but the pump plans),
// the needs off the table, the routes and the rules nova-config turns off, and
// the machine's STOPPED time for running time; and the fleet's working and width, the
// waiting cards and what the idle alarm would say of them now (sprint.TraceIdle). It
// writes nothing: `rules` prints it.
func (st *Store) RuleAnswers(ctx context.Context) ([]sprint.RuleAnswer, IdleNow, error) {
	var idle IdleNow
	pinned, err := st.pin(ctx)
	if err != nil {
		return nil, idle, err
	}
	s, err := pinned.Load(ctx, All, tickExtras)
	if err != nil {
		return nil, idle, err
	}
	q, err := pinned.B.QueueRead(ctx)
	if err != nil {
		return nil, idle, err
	}
	s = sprint.WithQueue(s, q)
	set, err := pinned.routes(ctx)
	if err != nil {
		return nil, idle, err
	}
	set.into(s)
	m, _, err := pinned.Machine(ctx)
	if err != nil {
		return nil, idle, err
	}
	req := sprint.TickReq{Who: sprint.MachineActor, Stopped: m.StoppedBetween, AnswerRules: true, IdleAlarm: true}
	idle.Working, idle.Width = sprint.FleetWorking(s)
	for _, c := range s.Work.Column(sprint.Waiting) {
		if !sprint.IsSentinel(c) {
			idle.Waiting++
		}
	}
	idle.Since, _ = s.Fleet.Prop(sprint.PropIdleSince)
	idle.Trace = sprint.TraceIdle(s, req)
	return sprint.RuleAnswers(s, req), idle, nil
}

// IdleNow is the fleet's working and width, the waiting cards, the idle episode's start
// ("" for none) and the roots the waiting cards are behind (sprint.TraceIdle).
type IdleNow struct {
	Working, Width, Waiting int
	Since, Trace            string
}

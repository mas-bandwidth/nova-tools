package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// startAfterQuiescence holds the table fence while checking the STOP-owned
// leases and writing RUNNING. A take/read begin cannot slip between the check
// and start: those operations use the same fence and refuse while STOPPED.
func (st *Store) startAfterQuiescence(ctx context.Context, next Machine) error {
	pinned, err := st.pin(ctx)
	if err != nil {
		return err
	}
	r := pinned.retry(ctx)
	for r.next(pinned.attempts()) {
		s, gen, err := pinned.Fenced(ctx, []string{sprint.Fleet, sprint.Readers}, nil, nil)
		if err != nil {
			return err
		}
		lock := OpRecord{ID: "start-" + pinned.newID() + "-lock", Verb: "start lock", At: pinned.now(), Lock: true}
		ok, err := pinned.B.Acquire(ctx, gen, lock)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		var active []string
		for _, table := range []struct {
			t   *sprint.Table
			col string
		}{{s.Fleet, sprint.Working}, {s.Readers, sprint.Reading}} {
			if table.t == nil {
				continue
			}
			for _, row := range table.t.Rows() {
				for _, c := range table.t.Cell(row, table.col) {
					active = append(active, row+":"+c.ID+"@"+fmt.Sprint(max(c.Int("gen"), 1)))
				}
			}
		}
		var guardErr error
		if len(active) > 0 {
			shown := active
			if len(shown) > 8 {
				shown = shown[:8]
			}
			guardErr = fmt.Errorf("%d STOP-owned work/read jobs still active (%s): cancel their child processes, then stop-return each on its same owner row before start", len(active), strings.Join(shown, ", "))
		} else {
			guardErr = pinned.putMachine(ctx, next)
		}
		return errors.Join(guardErr, pinned.B.Release(context.WithoutCancel(ctx), lock, false))
	}
	return fmt.Errorf("start: the sprint kept changing under it (%d tries); nothing was changed; run it again", r.tries)
}

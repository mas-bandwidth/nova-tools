package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"strconv"
	"time"
)

type seatPushObservation struct {
	Now   time.Time          `json:"now"`
	Set   sprint.SeatPushSet `json:"set"`
	Seat  store.SeatState    `json:"seat"`
	State json.RawMessage    `json:"state"`
}

// observeSeatPush uses the public store view, never a private friend directory
// or a harness guess (SPEC-SPRINT section 8, the native push set).
func observeSeatPush(ctx context.Context, st *store.Store, name, source string) (seatPushObservation, error) {
	var o seatPushObservation
	o.Now = st.Now()
	if _, ok := sprint.SeatPushPeriods()[source]; !ok {
		return o, fmt.Errorf("unknown observer %q: friends or transitions required", source)
	}
	var err error
	if o.Seat, err = st.SeatState(ctx); err != nil {
		return o, err
	}
	if o.Seat.Holder != name {
		return o, fmt.Errorf("%s is not the current seat %s; rearm the watch as that seat", name, o.Seat.Holder)
	}
	if o.Set, _, err = st.SeatPushes(ctx, name); err != nil {
		return o, err
	}
	var state any
	if source == "friends" {
		state, err = friendPushState(ctx, st)
	} else if source == "transitions" {
		state, err = transitionPushState(ctx, st)
	} else {
		state = nil
	}
	if err != nil {
		return o, err
	}
	o.State, err = json.Marshal(struct {
		Seat store.SeatState
		View any
	}{o.Seat, state})
	return o, err
}

func friendPushState(ctx context.Context, st *store.Store) (any, error) {
	rows, err := st.FriendRows(ctx, st.Now())
	if err != nil {
		return nil, err
	}
	state := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		state = append(state, map[string]any{"name": r.Name, "status": r.Status, "ready": r.Ready, "working": r.Working, "width": r.Width, "reason": r.Reason})
	}
	return state, nil
}

// transitionPushState takes all four shapes in one batch and keeps stable
// status/count facts; a revision or an age changing alone is no new event.
func transitionPushState(ctx context.Context, st *store.Store) (any, error) {
	names := make([]string, 0, len(sprint.ViewOrder))
	for _, n := range sprint.ViewOrder {
		names = append(names, st.Names.Table(n))
	}
	shapes, err := st.B.Shapes(ctx, names)
	if err != nil {
		return nil, err
	}
	if len(shapes) != len(names) {
		return nil, fmt.Errorf("the transitions view is incomplete: got %d tables, want %d", len(shapes), len(names))
	}
	state := map[string]any{}
	for _, table := range shapes {
		rows := map[string]any{}
		for _, row := range table.Rows {
			cells := map[string]any{}
			for i, col := range table.Columns {
				if i >= len(row.Cells) || !col.HasSet() {
					continue
				}
				cell := row.Cells[i]
				if cell.Unread {
					return nil, fmt.Errorf("transition cell %s is unreadable: %s", cell.Key, cell.UnreadWhy)
				}
				cells[col.Name] = cell.Count
			}
			if word := row.Texts["status"]; word != "" {
				cells["status"] = word
			}
			rows[row.Key] = cells
		}
		state[table.Name] = rows
	}
	machine, _, err := st.Machine(ctx)
	state["machine"] = machine
	return state, err
}

func (a *app) readPushObservation(ctx context.Context, c common, source, server string) (seatPushObservation, error) {
	if server == "" {
		st, err := a.storeCtx(ctx, c)
		if err != nil {
			return seatPushObservation{}, err
		}
		return observeSeatPush(ctx, st, c.actor, source)
	}
	res, err := a.ask(ctx, server, []string{"seat"}, []string{"push", "--actor", c.actor, "--observe", source, "--json"})
	if err != nil {
		return seatPushObservation{}, err
	}
	if res.Code != 0 {
		return seatPushObservation{}, fmt.Errorf("seat observation refused: %s", res.Stderr)
	}
	var o seatPushObservation
	err = json.Unmarshal([]byte(res.Stdout), &o)
	return o, err
}

func (a *app) beatPushObserver(ctx context.Context, c common, source, failed, server string, observed ...seatPushObservation) error {
	if server == "" {
		st, err := a.storeCtx(ctx, c)
		if err != nil {
			return err
		}
		if len(observed) > 0 {
			o := observed[0]
			return st.BeatSeatPushObserved(ctx, c.actor, source, failed, sprint.SeatWatchProof{Epoch: o.Seat.Epoch, Generation: o.Seat.Generation, Target: o.Set.Target, Session: o.Set.Session})
		}
		return st.BeatSeatPush(ctx, c.actor, source, failed)
	}
	args := []string{"push", "--actor", c.actor, "--beat", source}
	if failed != "" {
		args = append(args, "--failed", failed)
	}
	if len(observed) > 0 {
		o := observed[0]
		args = append(args, "--epoch", strconv.FormatUint(o.Seat.Epoch, 10), "--seat-generation", strconv.FormatUint(o.Seat.Generation, 10), "--observed-target", o.Set.Target, "--observed-session", o.Set.Session)
	}
	res, err := a.ask(ctx, server, []string{"seat"}, args)
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return fmt.Errorf("seat observer beat refused: %s", res.Stderr)
	}
	return nil
}

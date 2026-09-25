package reconcile

// The stream table, true end to end (nova-tools #3999). One card walks the
// five stream columns by events only: card push puts it in waiting, the deal
// duty cuts its work copy (waiting -> working), its consumer's card end with
// a PR moves it to reading and cuts its read copy (the copy's ok is the only
// event that advances a primary, #3929; a CI verdict moves nothing), the
// read copy's passing score at a head CI calls OK moves it to merging, and
// the land duty's merge moves it to landed.
//
// Walk is the control's observer: after every event it reads the five cells
// (one pipeline of ZCARDs, the table's own read), the record's where and the
// ws:log entries since the last event, then runs both fsck walks; a step
// whose cells, pointer, log or links are not exactly the one move is a
// *StepError that names the step.

import (
	"context"
	"errors"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

// StreamColumns are the stream table's columns, in the order a card walks.
var StreamColumns = []string{"waiting", "working", "reading", "merging", "landed"}

// StepError is a control step that did not make exactly its one move.
type StepError struct{ Step, Why string }

func (e *StepError) Error() string { return "stream control step " + e.Step + ": " + e.Why }

// WalkStep is one observed move: the event that made it is the ws:log
// entry's why, by its by.
type WalkStep struct{ Name, From, To, Event, By string }

// Walk observes one card across the stream table.
type Walk struct {
	c                  redis.Cmdable
	Stream, ID, Sprint string
	cells              map[string]int64
	where, logAt       string
	entered            map[string]int
	Steps              []WalkStep
}

// NewWalk reads the table and the log head before the card's first event.
func NewWalk(ctx context.Context, c redis.Cmdable, stream, id, sprint string) (*Walk, error) {
	w := &Walk{c: c, Stream: stream, ID: id, Sprint: sprint, entered: map[string]int{}}
	cells, where, _, err := w.read(ctx)
	if err != nil {
		return nil, err
	}
	last, err := c.XRevRangeN(ctx, "ws:log", "+", "-", 1).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	if len(last) > 0 {
		w.logAt = last[0].ID
	}
	w.cells, w.where = cells, where
	return w, nil
}

// read is one pipeline: the five cells, the record's where, and the ws:log
// entries after the last one seen.
func (w *Walk) read(ctx context.Context) (map[string]int64, string, []redis.XMessage, error) {
	pipe := w.c.Pipeline()
	cmds := make([]*redis.IntCmd, len(StreamColumns))
	for i, col := range StreamColumns {
		cmds[i] = pipe.ZCard(ctx, taskcard.StreamKey(w.Stream, col))
	}
	where := pipe.HGet(ctx, taskcard.Key(w.ID), "where")
	start := "-"
	if w.logAt != "" {
		start = "(" + w.logAt
	}
	log := pipe.XRange(ctx, "ws:log", start, "+")
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, "", nil, err
	}
	cells := map[string]int64{}
	for i, col := range StreamColumns {
		cells[col] = cmds[i].Val()
	}
	return cells, where.Val(), log.Val(), nil
}

// Move asserts the event just made is exactly the card's one move to `to`:
// the record's where is to, the to cell gained one and the from cell lost
// one (nothing else changed), ws:log holds exactly one move entry for the
// card (from the old where to the new, naming the event), and both fsck
// walks are clean.
func (w *Walk) Move(ctx context.Context, name, to string) error {
	return w.step(ctx, name, to)
}

// Hold asserts an event that moves nothing on the stream table (a CI
// verdict, a fill of the reader's working set): the cells,
// the where and the log's move entries are unchanged, and fsck is clean.
func (w *Walk) Hold(ctx context.Context, name string) error {
	return w.step(ctx, name, "")
}

func (w *Walk) step(ctx context.Context, name, to string) error {
	fail := func(format string, a ...any) error { return &StepError{Step: name, Why: fmt.Sprintf(format, a...)} }
	cells, where, log, err := w.read(ctx)
	if err != nil {
		return fail("read: %v", err)
	}
	from := w.where
	want := to
	if to == "" {
		want = from
	}
	if where != want {
		return fail("task:%s is %q, want %q", w.ID, where, want)
	}
	for _, col := range StreamColumns {
		d := int64(0)
		if to != "" && col == to {
			d++
		}
		if to != "" && col == from {
			d--
		}
		if cells[col]-w.cells[col] != d {
			return fail("cell %s went %d -> %d, want %+d", col, w.cells[col], cells[col], d)
		}
	}
	var moves []redis.XMessage
	for _, e := range log {
		if fmt.Sprint(e.Values["id"]) == w.ID && fmt.Sprint(e.Values["from"]) != fmt.Sprint(e.Values["to"]) {
			moves = append(moves, e)
		}
	}
	if len(log) > 0 {
		w.logAt = log[len(log)-1].ID
	}
	switch {
	case to == "" && len(moves) != 0:
		return fail("ws:log has %d move entries for task:%s, want none", len(moves), w.ID)
	case to != "" && len(moves) != 1:
		return fail("ws:log has %d move entries for task:%s, want one", len(moves), w.ID)
	}
	if to != "" {
		e := moves[0].Values
		lf, lt, why := fmt.Sprint(e["from"]), fmt.Sprint(e["to"]), fmt.Sprint(e["why"])
		if lf != from || lt != to {
			return fail("ws:log entry %s -> %s, want %s -> %s", lf, lt, from, to)
		}
		if why == "" || why == "<nil>" {
			return fail("ws:log entry %s -> %s names no event", lf, lt)
		}
		w.Steps = append(w.Steps, WalkStep{Name: name, From: from, To: to, Event: why, By: fmt.Sprint(e["by"])})
		w.entered[to]++
	}
	fk, err := taskcard.Fsck(ctx, w.c, w.Sprint)
	if err != nil {
		return fail("task fsck: %v", err)
	}
	if fk.Drift != 0 {
		return fail("task fsck drift %d: %v", fk.Drift, fk.Lines)
	}
	fm, err := taskcard.FsckMoves(ctx, w.c, false)
	if err != nil {
		return fail("card fsck: %v", err)
	}
	if fm.Drift != 0 {
		return fail("card fsck drift %d: %v", fm.Drift, fm.Lines)
	}
	w.cells, w.where = cells, where
	return nil
}

// Walked asserts the card entered every stream column exactly once.
func (w *Walk) Walked() error {
	for _, col := range StreamColumns {
		if w.entered[col] != 1 {
			return &StepError{Step: "walk", Why: fmt.Sprintf("column %s entered %d times, want 1", col, w.entered[col])}
		}
	}
	return nil
}

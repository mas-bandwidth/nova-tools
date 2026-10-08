package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// machineTransition takes the same operation fence as work and read steps.
// Acquire advances its generation, so a worker that read RUNNING before STOP
// cannot commit afterward using its old read. A repeated STOP and a START also
// re-read the machine under this lock, never overwriting a newer stop reason.
func (st *Store) machineTransition(ctx context.Context, running bool, who, reason string, until time.Time) (before, after Machine, changed bool, err error) {
	pinned, err := st.pin(ctx)
	if err != nil {
		return before, after, false, err
	}
	r := pinned.retry(ctx)
	for r.next(pinned.attempts()) {
		var tables []string
		if running {
			tables = []string{sprint.Fleet, sprint.Readers}
		}
		s, gen, ferr := pinned.Fenced(ctx, tables, nil, nil)
		if ferr != nil {
			return before, after, false, ferr
		}
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return before, after, false, err
		}
		lock := OpRecord{ID: "machine-" + hex.EncodeToString(nonce[:]) + "-lock", Verb: "machine transition lock", At: pinned.now(), Lock: true}
		ok, aerr := pinned.B.Acquire(ctx, gen, lock)
		if aerr != nil {
			return before, after, false, aerr
		}
		if !ok {
			continue
		}
		before, _, err = pinned.Machine(ctx)
		if err == nil {
			after, changed, err = pinned.machineRecord(ctx, before, s, running, who, reason, until)
		}
		err = errors.Join(err, pinned.B.Release(context.WithoutCancel(ctx), lock, false))
		if err != nil {
			return before, before, false, err
		}
		return before, after, changed, nil
	}
	return before, after, false, fmt.Errorf("machine transition: the sprint kept changing under it (%d tries); nothing was changed; run it again", r.tries)
}

func (st *Store) machineRecord(ctx context.Context, before Machine, s *sprint.Snapshot, running bool, who, reason string, until time.Time) (after Machine, changed bool, err error) {
	if before.Running() == running && (before.Cause != "" || !running && (reason != "" || before.Reason != "")) {
		after = before
		after.Cause, after.Reason, after.Until = "", reason, until
		if reason != "" {
			after.Who = who
		}
		return after, false, st.putMachine(ctx, after)
	}
	if before.Running() == running {
		if kv, ok := st.B.(KV); ok {
			err = kv.ShowState(ctx, st.Names.View(), ViewState(before))
		}
		return before, false, err
	}
	if running && before.Reason != "" {
		if err := stopActiveJobs(s); err != nil {
			return before, false, err
		}
	}
	now := st.now()
	after = before
	after.Spans = append([]Span(nil), before.Spans...)
	if running {
		if after.RunSeq == ^uint64(0) {
			return before, false, errors.New("machine start generation is exhausted; nothing was changed")
		}
		after.RunSeq++
		if n := len(after.Spans); n > 0 && after.Spans[n-1].To.IsZero() {
			after.Spans[n-1].To = now
			after.StoppedFor += now.Sub(after.Spans[n-1].From)
		}
		after.State = Running
	} else {
		after.Spans = append(after.Spans, Span{From: now})
		if len(after.Spans) > MaxStopSpans {
			after.Spans = after.Spans[len(after.Spans)-MaxStopSpans:]
		}
		after.State = Stopped
	}
	after.Since, after.Who, after.Cause, after.Reason, after.Until = now, who, "", reason, until
	return after, true, st.putMachine(ctx, after)
}

func stopActiveJobs(s *sprint.Snapshot) error {
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
	if len(active) == 0 {
		return nil
	}
	shown := active
	if len(shown) > 8 {
		shown = shown[:8]
	}
	return fmt.Errorf("%d STOP-owned work/read jobs still active (%s): cancel their child processes, then stop-return each on its same owner row before start", len(active), strings.Join(shown, ", "))
}

// stopWithCause serializes the tick's DONE and funds stops with a manual STOP.
// A tick that observed RUNNING before an operator's STOP re-reads it after the
// operator's fenced write, leaving that later explicit reason intact. Its
// observed START generation also prevents it from stopping a later restart.
func (st *Store) stopWithCause(ctx context.Context, cause string, runSeq uint64) (before, after Machine, changed bool, err error) {
	pinned, err := st.pin(ctx)
	if err != nil {
		return before, after, false, err
	}
	r := pinned.retry(ctx)
	for r.next(pinned.attempts()) {
		_, gen, ferr := pinned.Fenced(ctx, nil, nil, nil)
		if ferr != nil {
			return before, after, false, ferr
		}
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return before, after, false, err
		}
		lock := OpRecord{ID: "machine-cause-" + hex.EncodeToString(nonce[:]) + "-lock", Verb: "machine cause lock", At: pinned.now(), Lock: true}
		ok, aerr := pinned.B.Acquire(ctx, gen, lock)
		if aerr != nil {
			return before, after, false, aerr
		}
		if !ok {
			continue
		}
		before, _, err = pinned.Machine(ctx)
		if err == nil {
			if !before.Running() || before.RunSeq != runSeq {
				after = before
			} else {
				now := pinned.now()
				after = before
				after.Spans = append(append([]Span(nil), before.Spans...), Span{From: now})
				if len(after.Spans) > MaxStopSpans {
					after.Spans = after.Spans[len(after.Spans)-MaxStopSpans:]
				}
				after.State, after.Since, after.Who, after.Cause = Stopped, now, sprint.MachineActor, cause
				err = pinned.putMachine(ctx, after)
				changed = err == nil
			}
		}
		err = errors.Join(err, pinned.B.Release(context.WithoutCancel(ctx), lock, false))
		if err != nil {
			return before, before, false, err
		}
		return before, after, changed, nil
	}
	return before, after, false, fmt.Errorf("machine cause: the sprint kept changing under it (%d tries); nothing was changed; run it again", r.tries)
}

// clearDoneCause is the post-add counterpart to stopWithCause. The add has
// committed, but a later operator STOP must not be replaced by an old DONE
// record that the post-add hook observed before acquiring the machine fence.
func (st *Store) clearDoneCause(ctx context.Context) error {
	pinned, err := st.pin(ctx)
	if err != nil {
		return err
	}
	r := pinned.retry(ctx)
	for r.next(pinned.attempts()) {
		_, gen, err := pinned.Fenced(ctx, nil, nil, nil)
		if err != nil {
			return err
		}
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return err
		}
		lock := OpRecord{ID: "machine-undone-" + hex.EncodeToString(nonce[:]) + "-lock", Verb: "machine undone lock", At: pinned.now(), Lock: true}
		ok, err := pinned.B.Acquire(ctx, gen, lock)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		m, _, err := pinned.Machine(ctx)
		if err == nil && m.Done() {
			m.Cause = ""
			err = pinned.putMachine(ctx, m)
		}
		err = errors.Join(err, pinned.B.Release(context.WithoutCancel(ctx), lock, false))
		return err
	}
	return fmt.Errorf("machine undone: the sprint kept changing under it (%d tries); nothing was changed; run it again", r.tries)
}

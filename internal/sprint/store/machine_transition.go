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
		// STOP records the same live owner leases that START later checks.
		s, gen, ferr := pinned.Fenced(ctx, []string{sprint.Fleet, sprint.Readers}, nil, nil)
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
		after.StopIssued = !running
		if !running && len(before.StopDebt) == 0 {
			after.StopDebt = activeStopLeases(s)
		}
		after.Cause, after.Reason, after.Until = "", reason, until
		if reason != "" {
			after.Who = who
		}
		return after, false, st.putMachine(ctx, after)
	}
	if before.Running() == running {
		if !running {
			after = before
			after.StopIssued = true
			if len(before.StopDebt) == 0 {
				after.StopDebt = activeStopLeases(s)
			}
			return after, !before.StopIssued || len(after.StopDebt) > 0 && len(before.StopDebt) == 0, st.putMachine(ctx, after)
		}
		if kv, ok := st.B.(KV); ok {
			err = kv.ShowState(ctx, st.Names.View(), ViewState(before))
		}
		return before, false, err
	}
	if running {
		if err := unsettledStopDebt(before, s); err != nil {
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
		after.StopIssued = false
		after.StopDebt = nil
	} else {
		after.StopIssued = true
		after.StopDebt = activeStopLeases(s)
		after.Spans = append(after.Spans, Span{From: now})
		if len(after.Spans) > MaxStopSpans {
			after.Spans = after.Spans[len(after.Spans)-MaxStopSpans:]
		}
		after.State = Stopped
	}
	after.Since, after.Who, after.Cause, after.Reason, after.Until = now, who, "", reason, until
	return after, true, st.putMachine(ctx, after)
}

// activeStopLeases captures StopReturn.tla's cancellation debt at the STOP
// fence (docs/SPEC-SPRINT.md section 14).
func activeStopLeases(s *sprint.Snapshot) []StopLease {
	var active []StopLease
	for _, table := range []struct {
		t    *sprint.Table
		name string
		col  string
	}{{s.Fleet, sprint.Fleet, sprint.Working}, {s.Readers, sprint.Readers, sprint.Reading}} {
		if table.t == nil {
			continue
		}
		for _, row := range table.t.Rows() {
			for _, c := range table.t.Cell(row, table.col) {
				active = append(active, StopLease{Table: table.name, Row: row, ID: c.ID, Gen: max(c.Int("gen"), 1)})
			}
		}
	}
	return active
}

// unsettledStopDebt enforces StopReturn.tla ExplicitStart against each exact
// same-owner return receipt (docs/SPEC-SPRINT.md section 14).
func unsettledStopDebt(m Machine, s *sprint.Snapshot) error {
	var open []string
	for _, d := range m.StopDebt {
		if d.Gen < 1 || d.Table != sprint.Fleet && d.Table != sprint.Readers {
			open = append(open, d.Row+":"+d.ID+"@"+fmt.Sprint(d.Gen))
			continue
		}
		t := s.Fleet
		ready := sprint.Ready
		if d.Table == sprint.Readers {
			t, ready = s.Readers, sprint.Asked
		}
		var c *sprint.Card
		if t != nil {
			c = t.Card(d.ID)
		}
		if c == nil || !c.Placed() || c.Row != d.Row || c.Col != ready || c.Int("stopped_from_gen") != d.Gen || c.Int("gen") != d.Gen+1 {
			open = append(open, d.Row+":"+d.ID+"@"+fmt.Sprint(d.Gen))
		}
	}
	// Only pre-upgrade records lack a persisted STOP-issued flag and debt.
	// New records use StopDebt as the sole lease authority; an old live lease
	// without a receipt remains a safety refusal during migration.
	if !m.StopIssued {
		for _, a := range activeStopLeases(s) {
			open = append(open, a.Row+":"+a.ID+"@"+fmt.Sprint(a.Gen))
		}
	}
	if len(open) == 0 {
		return nil
	}
	shown := open
	if len(shown) > 8 {
		shown = shown[:8]
	}
	return fmt.Errorf("%d STOP-owned work/read jobs lack same-owner cancellation receipts (%s): cancel their child processes, then stop-return each on its same owner row before start", len(open), strings.Join(shown, ", "))
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
		s, gen, ferr := pinned.Fenced(ctx, []string{sprint.Fleet, sprint.Readers}, nil, nil)
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
				after.StopIssued = true
				after.StopDebt = activeStopLeases(s)
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

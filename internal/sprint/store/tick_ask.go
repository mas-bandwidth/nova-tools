package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The tick's ask in small fenced steps (docs/SPEC-SPRINT.md section 6, "The
// tick's ask in small fenced steps"). On 2026-10-06, with 297 cards in review
// and three friends reading, the ask planned forty primaries in one fenced
// write; the other writers (reads finishing, lanes taking and finishing,
// beats) moved the fence between its read and its write, it planned the whole
// again twelve times, re-reading about two hundred round trips, and gave every
// primary up ("the sprint kept changing under this step (12 attempts)"): no
// read asked for ten minutes, and the tick held the server's line for 20 s.
// So the ask writes at most AskBatch primaries a step, each step planned on a
// fresh read with AskTries tries; a batch that loses its tries is tried again
// a primary at a time, and a primary that loses its own tries is refused alone,
// and asked again by the next tick. The steps stop at the ask's budget.
const (
	// AskBatch is the most primaries one fenced step of the tick's ask writes.
	// Twenty avoids replanning the whole review table for every five cards;
	// a batch that loses its tries is retried one primary at a time below.
	AskBatch = 20
	// AskTries is the plans one step of the tick's ask makes before it gives
	// its primaries up for this tick.
	AskTries = 3
	// AskBudget is the time the tick's ask takes at most: past it, no further
	// step begins (a step in flight plans no further try), and what is left is
	// due for the next tick.
	AskBudget = 2 * time.Second
	// AskBy is how far into its tick the ask may begin a step after its first:
	// half of the tick's second (TickEvery), the owner's law of 2026-09-30
	// ("the whole intent is sub-second ticks. This is a requirement"). The ask
	// is the tick's only part that writes in steps until a budget, and a budget
	// of its own (AskBudget, 2 s) longer than the tick it runs in made every
	// tick with a backlog of reads to ask a two-second tick: on 2026-10-10 the
	// certification drive (cmd/nova-sprint TestTheDirtyTickDriveOnAStore) failed
	// 11 times since 2026-10-09 18Z, and in run 38059743591 five of its eight
	// ticks over 1 s spent 2.006 to 2.063 s in readers/ask alone. The parts after
	// the ask (display, archive, where, the tick's end: 100 to 400 ms at load)
	// take the other half. The first step always begins, so every tick asks
	// some reads however long its earlier parts took; what is left is due, and
	// the next tick asks it.
	AskBy = TickEvery / 2
)

// askTally is what the tick's ask did, for the tick's TIMES line and tables.
type askTally struct {
	asked, refused int
	// rows is the rows the committed steps changed, by table.
	rows map[string][]string
	// unfinished says the ask left something for the next tick: a primary
	// given up, the budget spent with primaries left, or a batch lost and
	// not tried again alone before the ask stopped.
	unfinished bool
	// left is the primaries the ask did not reach before its budget.
	left int
	// lost is the primaries of a batch that lost its tries and that the ask
	// stopped before trying again alone: due for the next tick, and said in
	// the TIMES line (TickResult.TimesLine, <n>lost).
	lost int
}

// askBudget is the ask's budget: AskBudget, or half the time the tick's
// context has left at now (the store's clock, Store.now) when that is less.
func askBudget(ctx context.Context, now time.Time) time.Duration {
	b := AskBudget
	if dl, ok := ctx.Deadline(); ok {
		if share := dl.Sub(now) / 2; share < b {
			b = max(share, 0)
		}
	}
	return b
}

// askUntil is when the ask begins no further step: its budget from now
// (askBudget), or AskBy past the tick's beginning when that is sooner. A zero
// beginning is a tick that recorded none, and the budget alone bounds it.
func askUntil(ctx context.Context, began, now time.Time) time.Time {
	until := now.Add(askBudget(ctx, now))
	if !began.IsZero() {
		if by := began.Add(AskBy); by.Before(until) {
			until = by
		}
	}
	return until
}

// askInSteps runs the tick's ask part as small fenced steps: each step plans
// the part on a fresh read and writes the first AskBatch primaries of the plan
// not yet written or given up in this tick (sprint.KeepUnits), with the
// plan's judgments while none has committed yet. A step that loses its
// AskTries tries to other writers is tried again a primary at a time; a
// primary that loses its own tries is one refusal, its own, and the next tick
// asks it. No step begins past the budget: a lost batch the ask stops before
// trying again alone is counted due (askTally.lost) and leaves the ask
// unfinished, as the primaries it did not reach do. The tick holds the server's line
// through the ask, as through every part (cmd/nova-sprint run.go, TickLock):
// the budget is what bounds the wait of the verbs behind it.
func (t *tickRun) askInSteps(step Step) (Result, askTally, error) {
	st := t.st
	tally := askTally{rows: map[string][]string{}}
	out := Result{Verb: step.Verb, Tables: map[string]int{}}
	begin := st.now()
	until := askUntil(t.ctx, t.res.began, begin)
	done := map[string]bool{}   // primaries written this tick, or refused by the plan
	gaveUp := map[string]bool{} // primaries given up this tick: the next tick asks them
	single := map[string]bool{} // primaries of a lost batch: tried again alone
	notesDone := false          // a step committed the plan's judgments
	committed, lost := false, false
	inner := step.Plan
	// lostBatch is the last step's batch when it lost its tries: tried again
	// alone by the steps after it, and due while no plan has counted it since
	var lostBatch []string
	for n := 0; ; n++ {
		if n > 0 && !st.now().Before(until) {
			tally.lost = len(lostBatch)
			if tally.left > 0 || tally.lost > 0 {
				tally.unfinished = true
			}
			break
		}
		var chosen []string
		var kept sprint.Plan
		left, planned := 0, false
		s := step
		s.Tries, s.Until = AskTries, until
		s.Plan = func(sn *sprint.Snapshot) sprint.Plan {
			p := inner(sn)
			if notesDone {
				// the judgments rode the first step that committed; a later plan
				// on a fresh read finds them open, and a happened note is said once
				p.Notes, p.Closes, p.Updates, p.Said = nil, nil, nil, nil
			}
			chosen, left = askBatchOf(p, done, gaveUp, single)
			planned = true
			kept = sprint.KeepUnits(p, func(u sprint.Unit) bool { return slices.Contains(chosen, u.Key) })
			return kept
		}
		// the step's sleeps between its tries come from what is left of the budget
		ctx := context.WithValue(t.ctx, budgetKey{}, &budget{slept: RetryBudget - max(until.Sub(st.now()), 0)})
		r, err := st.Run(ctx, s)
		if err != nil && IsFenceBusy(err) {
			// the fenced read itself lost to other writers: this step changed
			// nothing, and the rest is the next tick's
			tally.unfinished, lost = true, true
			if planned {
				tally.left = left + len(chosen)
			} else {
				tally.lost = len(lostBatch) // no plan counted the lost batch since
			}
			break
		}
		if err != nil {
			return out, tally, err
		}
		if r.Halted {
			if n == 0 {
				return r, tally, nil
			}
			// stopped since the ask began: no further step of it begins, and the
			// tick halts before its next part
			break
		}
		tally.left = left
		if planned {
			lostBatch = nil // this plan counted it: chosen alone, or in left
		}
		out.Attempts += r.Attempts
		out.Repaired = append(out.Repaired, r.Repaired...)
		out.Drained = append(out.Drained, r.Drained...)
		if r.Lost {
			lost = true
			if len(chosen) == 0 {
				break
			}
			if len(chosen) > 1 {
				for _, k := range chosen {
					single[k] = true
				}
				lostBatch = chosen
				continue
			}
			k := chosen[0]
			gaveUp[k] = true
			tally.unfinished = true
			out.Refused = append(out.Refused, sprint.Refusal{Key: k, Why: fmt.Sprintf(
				"its ask lost %d tries this tick in a step of its own (another writer moved the fence, or the store refused the write as planned); nothing was written for it; the next tick asks it again", r.Attempts)})
			continue
		}
		committed = true
		notesDone = notesDone || r.Notes > 0 || len(r.Moved) > 0
		out.Op = r.Op
		out.Moved = append(out.Moved, r.Moved...)
		out.Refused = append(out.Refused, r.Refused...)
		out.Said = append(out.Said, r.Said...)
		out.Notes += r.Notes
		for tb, c := range r.Tables {
			out.Tables[tb] += c
		}
		if len(r.Moved) > 0 {
			for tb, rows := range sprint.PlanRows(kept) {
				for _, row := range rows {
					if !slices.Contains(tally.rows[tb], row) {
						tally.rows[tb] = append(tally.rows[tb], row)
					}
				}
			}
		}
		if staleRefusal(r.Refused, t.at) {
			break
		}
		for _, k := range chosen {
			done[k] = true
		}
		if len(chosen) == 0 || left == 0 {
			break
		}
	}
	out.Lost = lost && !committed
	for _, m := range out.Moved {
		if strings.Contains(m, " asked of ") {
			tally.asked++
		}
	}
	tally.refused = len(out.Refused)
	return out, tally, nil
}

// askBatchOf is the primaries one step of the ask writes from its plan: the
// first not written, refused or given up in this tick, at most AskBatch of
// them, or one alone when the first is from a batch that lost its tries; and
// how many such primaries the plan holds beyond them.
func askBatchOf(p sprint.Plan, done, gaveUp, single map[string]bool) (chosen []string, left int) {
	seen := map[string]bool{}
	alone := false
	for _, u := range p.Units {
		k := u.Key
		if seen[k] || done[k] || gaveUp[k] {
			continue
		}
		seen[k] = true
		switch {
		case alone || len(chosen) == AskBatch:
			left++
		case single[k] && len(chosen) == 0:
			chosen, alone = []string{k}, true
		case single[k]:
			left++
		default:
			chosen = append(chosen, k)
		}
	}
	return chosen, left
}

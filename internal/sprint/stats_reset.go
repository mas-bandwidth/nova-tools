package sprint

import (
	"maps"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// Stats reset (`nova-sprint stats reset`; docs/SPEC-SPRINT.md section 11, Statistics): the
// owner, 2026-10-09 ~8:40 PM ET, "Clear the cost per-card right now. Clear the per-tier
// costs. Clear the total cost." and "Clear the done and the ok% for all friends now.", and
// ~9:25 PM, "everything that I described should happen when you go reset. tidy is
// different. tidy removes the already done work streams and cards." A reset moves nothing:
// it writes one mark, the counters as they stand, and every figure the mark covers is shown
// from then on as the epoch's figure less the mark's, never below zero. A figure the mark
// does not cover (a row or a stream it never knew, a figure it does not name) reads as
// before. A second reset replaces the mark. Pure: the snapshot in, the mark out; the
// subtractions are of the figures as shown. tla/StatsReset.tla models it.

// ResetRow is one fleet row's done cells at the mark: its ok and failed counts.
type ResetRow struct {
	OK     int `json:"ok"`
	Failed int `json:"failed"`
}

// Done is the row's done at the mark, ok and failed together (the table's done formula).
func (r ResetRow) Done() int { return r.OK + r.Failed }

// ResetStream is one stream's figures at the mark: the control card's exact landed cost and
// the landed count (the work table's cost cell and per landed count from them, as a tidy's
// StreamBase), and its spend as the where view showed it (TierCosts: the total, the work
// and the reads, and by tier, dollars and cents).
type ResetStream struct {
	Cost       string            `json:"cost,omitempty"`
	Landed     int               `json:"landed"`
	TotalCost  string            `json:"total_cost,omitempty"`
	WorkCost   string            `json:"work_cost,omitempty"`
	ReadCost   string            `json:"read_cost,omitempty"`
	CostByTier map[string]string `json:"cost_by_tier,omitempty"`
}

// Base is the stream's landed cost and count at the mark, as a tidy's base names them.
func (r ResetStream) Base() StreamBase { return StreamBase{Cost: r.Cost, Landed: r.Landed} }

// ResetMark is a stats reset: when, by whom and why, in which epoch, and the counters at
// the mark: each fleet row's (machines' and friends' rows alike, FriendRow), each stream's,
// and the epoch's total cost (every stream's TotalCost summed).
type ResetMark struct {
	At        time.Time              `json:"at"`
	Epoch     uint64                 `json:"epoch"`
	By        string                 `json:"by,omitempty"`
	Reason    string                 `json:"reason"`
	Rows      map[string]ResetRow    `json:"rows,omitempty"`
	Streams   map[string]ResetStream `json:"streams,omitempty"`
	TotalCost string                 `json:"total_cost,omitempty"`
	// Op is the caller's operation id the mark was written under (--op): the same id again
	// returns this mark and writes nothing.
	Op string `json:"op,omitempty"`
}

// ResetMarkOf is the mark of the sprint s at at: every fleet row's done cells, every stream's
// landed cost and count (StreamBases) and its spend (StreamTierCosts, the figures the tick
// counts for the where view), and the epoch's total.
func ResetMarkOf(s *Snapshot, at time.Time, by, reason string) ResetMark {
	m := ResetMark{At: at.UTC(), Epoch: s.Epoch, By: by, Reason: reason, Rows: map[string]ResetRow{}, Streams: map[string]ResetStream{}}
	if s.Fleet != nil {
		for _, row := range s.Fleet.Rows() {
			m.Rows[row] = ResetRow{OK: s.Fleet.Count(row, DoneOK), Failed: s.Fleet.Count(row, DoneFailed)}
		}
	}
	bases := StreamBases(s)
	costs := StreamTierCosts(s)
	total := new(big.Rat)
	priced := false
	for stream, b := range bases {
		tc := costs[stream]
		m.Streams[stream] = ResetStream{Cost: b.Cost, Landed: b.Landed, TotalCost: tc.TotalCost, WorkCost: tc.WorkCost, ReadCost: tc.ReadCost,
			CostByTier: maps.Clone(tc.CostByTier)}
		if v, ok := moneyOf(tc.TotalCost); ok {
			total.Add(total, v)
			priced = true
		}
	}
	if priced {
		m.TotalCost = cardcost.Cents(total)
	}
	return m
}

// moneyOf is a money figure ("$1.25", or a bare decimal) as a rational; false when it is
// empty or does not read.
func moneyOf(v string) (*big.Rat, bool) {
	r, err := amountOf(strings.TrimPrefix(v, "$"))
	if err != nil || r == nil {
		return nil, false
	}
	return r, true
}

// MoneyLess is the money figure now less the one at the mark, as the where view shows money
// (dollars and cents, "$0.25"): "" when that is nothing or below (never below zero: the
// figure's "-"), now unchanged when the mark names none or either does not read.
func MoneyLess(now, was string) string {
	if was == "" || now == "" {
		return now
	}
	a, ok := moneyOf(now)
	b, ok2 := moneyOf(was)
	if !ok || !ok2 {
		return now
	}
	d := new(big.Rat).Sub(a, b)
	if d.Sign() <= 0 {
		return ""
	}
	return cardcost.Cents(d)
}

// TierCostsSince is the stream's spend since the mark: its total, work and read costs and
// each tier's less the mark's, never below zero, a tier with nothing since left out (the pie
// leaves a tier at $0 out). Every other figure (the cards by tier, per landed, the reads of
// the day, the readers' spend, the unpriced runs, the reconciliation) is as it was: per
// landed is counted from the mark's base by the caller (PerLandedSince).
func TierCostsSince(tc TierCosts, m ResetStream) TierCosts {
	tc.TotalCost = MoneyLess(tc.TotalCost, m.TotalCost)
	tc.WorkCost = MoneyLess(tc.WorkCost, m.WorkCost)
	tc.ReadCost = MoneyLess(tc.ReadCost, m.ReadCost)
	if len(tc.CostByTier) > 0 {
		by := map[string]string{}
		for tier, v := range tc.CostByTier {
			if d := MoneyLess(v, m.CostByTier[tier]); d != "" {
				by[tier] = d
			}
		}
		tc.CostByTier = by
	}
	return tc
}

// countLess is a count less the mark's, never below zero.
func countLess(now int64, was int) int64 { return max(0, now-int64(was)) }

// FleetSince is the fleet table as the where view draws it since the mark: each row the
// mark knows has its ok and failed cells less the mark's, never below zero, so the table's
// done (ok+failed) and ok% (ok over ok+failed) formulas count the cards since the mark; a
// row the mark does not know reads as before. Nothing is written: the cells are the view's.
func (m *ResetMark) FleetSince(t ntable.Table) ntable.Table {
	if m == nil || len(m.Rows) == 0 {
		return t
	}
	ok, failed := t.Column(DoneOK), t.Column(DoneFailed)
	rows := make([]ntable.Row, len(t.Rows))
	for i, r := range t.Rows {
		base, known := m.Rows[r.Key]
		if known {
			r.Cells = slices.Clone(r.Cells)
			for _, at := range []struct {
				j   int
				was int
			}{{ok, base.OK}, {failed, base.Failed}} {
				if at.j >= 0 && at.j < len(r.Cells) && !r.Cells[at.j].Unread {
					r.Cells[at.j].Count = countLess(r.Cells[at.j].Count, at.was)
				}
			}
		}
		rows[i] = r
	}
	t.Rows = rows
	return t
}

// LandedSince is the cards landed since the mark over the work table's rows that in says
// to count: each stream's landed count less the mark's, never below zero; a stream the mark
// does not know counts every landed card (it had none at the mark).
func (m *ResetMark) LandedSince(t ntable.Table, in func(ntable.Row) bool) int64 {
	var n int64
	for _, c := range m.LandedSinceBy(t, in) {
		n += c
	}
	return n
}

// LandedSinceBy is LandedSince stream by stream: every row in says to count, by its key.
func (m *ResetMark) LandedSinceBy(t ntable.Table, in func(ntable.Row) bool) map[string]int64 {
	j := t.Column(Landed)
	out := map[string]int64{}
	for _, r := range t.Rows {
		if j < 0 || j >= len(r.Cells) || r.Cells[j].Unread || (in != nil && !in(r)) {
			continue
		}
		was := 0
		if m != nil {
			was = m.Streams[r.Key].Landed
		}
		out[r.Key] = countLess(r.Cells[j].Count, was)
	}
	return out
}

// Rebase is the mark after a tidy took cards off the done cells of rows it knows (the
// tidy's rows, sprint.TidyDone): each row's ok and failed at the mark less the moved cards
// of that cell that finished before the mark (TidyCard.Finished; a card with no stamp is
// history, before it), never below zero. A moved card that finished after the mark is not in
// the mark's counters: it leaves the row's done since the mark, as the tidy, later, says. A
// tidy does not move the oldest cards first (TidyKept keeps a card whose primary has not
// landed, whatever its age), so the rule is by each card's stamp, never by count. The row's
// done since the mark is then the cards finished since the mark still on the row.
// tla/StatsReset.tla, TidyMove and TidyEnd.
func (m *ResetMark) Rebase(rows []TidyRow) {
	if m == nil {
		return
	}
	for _, r := range rows {
		base, ok := m.Rows[r.Row]
		if !ok {
			continue
		}
		for _, c := range r.Moved {
			if !c.Finished.IsZero() && !c.Finished.Before(m.At) {
				continue // finished since the mark: not in its counters
			}
			if c.Cell == "failed" {
				base.Failed = max(0, base.Failed-1)
			} else {
				base.OK = max(0, base.OK-1)
			}
		}
		m.Rows[r.Row] = base
	}
}

// Later is whether the mark is later than t: a reset after the last tidy of a kind is where
// that kind's figures count from, and a tidy after the reset is.
func (m *ResetMark) Later(t time.Time) bool { return m != nil && m.At.After(t) }

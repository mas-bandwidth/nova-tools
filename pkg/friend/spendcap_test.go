package friend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type spendHarness struct {
	spent   func(dayStart time.Time) float64
	deliver int
	err     error
}

func (h *spendHarness) Deliver(ctx context.Context, text string) (int, error) {
	h.deliver++
	return 0, h.err
}

func TestSpendCapStopsAtProviderFundsRefusalAndRaisesOneJudgment(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	h := &spendHarness{err: OutOfFunds{Session: "s1", Reason: "402 insufficient balance"}}
	var judgments []string
	s := &SpendCap{
		Friend: "alice", Provider: "deepseek", Now: func() time.Time { return now },
		Cap: func() float64 { return 20 }, Spent: func(time.Time) float64 { return 4.25 },
		Judgment: func(_ time.Time, spent, cap float64) string {
			line := fmt.Sprintf("alice deepseek spend $%.2f of $%.2f", spent, cap)
			judgments = append(judgments, line)
			return line
		},
	}
	g := s.Gate(h)
	_, err := g.Deliver(context.Background(), "one")
	var funds OutOfFunds
	require.ErrorAs(t, err, &funds, "the real provider failure reaches the lane governor")
	_, err = g.Deliver(context.Background(), "two")
	var deferred Deferred
	require.ErrorAs(t, err, &deferred, "a later turn stays stopped")
	assert.Contains(t, deferred.Reason, "alice")
	assert.Contains(t, deferred.Reason, "deepseek")
	assert.Contains(t, deferred.Reason, "4.25")
	assert.Equal(t, 1, h.deliver, "the provider refusal is not retried")
	require.Len(t, judgments, 1, "the seat and owner receive one judgment")
}

func TestSpendCapUnknownUsageFailsClosedOnLaneHarness(t *testing.T) {
	t.Parallel()
	h := &lanesHarness{dir: t.TempDir()}
	s := &SpendCap{Friend: "zhi", Provider: "deepseek", Usage: func(time.Time) (float64, bool) { return 0, false }}
	wrapped, ok := s.Gate(h).(LaneHarness)
	require.True(t, ok, "the actual lane interface remains gated")
	_, err := wrapped.DeliverTo(context.Background(), "session-zhi", "one")
	var stopped SpendCapReached
	require.ErrorAs(t, err, &stopped)
	assert.Contains(t, stopped.Reason, "usage is unavailable")
	turns, _, _ := h.got()
	assert.Empty(t, turns, "unknown spend is never estimated or sent to the provider")
}

func TestSpendCapPersistsMeasuredCapAcrossRestartAndUTCDayRollover(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	now := time.Date(2026, 10, 12, 23, 30, 0, 0, time.UTC)
	spent := 25.0
	var judgments int
	newCap := func() *SpendCap {
		return &SpendCap{Friend: "alice", Provider: "deepseek", StateDir: state, Now: func() time.Time { return now },
			Spent: func(time.Time) float64 { return spent }, OnJudgment: func(string) { judgments++ }}
	}
	firstHarness := &spendHarness{}
	_, err := newCap().Gate(firstHarness).Deliver(context.Background(), "over cap")
	require.ErrorAs(t, err, &Deferred{})
	var saved spendLedger
	found, err := read(filepath.Join(state, SpendLedgerFile), &saved)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, 25.0, saved.Spend["deepseek/2026-10-12"], "the measured amount survives a daemon restart")

	spent = 0 // a restarted harness process starts its local counter at zero
	secondHarness := &spendHarness{}
	_, err = newCap().Gate(secondHarness).Deliver(context.Background(), "restart")
	require.ErrorAs(t, err, &Deferred{})
	assert.Equal(t, 0, secondHarness.deliver, "persisted cap blocks delivery after restart")
	assert.Equal(t, 1, judgments, "a persisted stop does not raise a duplicate judgment")

	now = time.Date(2026, 10, 13, 0, 0, 1, 0, time.UTC)
	_, err = newCap().Gate(secondHarness).Deliver(context.Background(), "new UTC day")
	require.NoError(t, err)
	assert.Equal(t, 1, secondHarness.deliver, "the UTC day rollover starts a fresh measured budget")
}

func TestSpendCapPersistsProviderRefusalAcrossRestart(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	var judgments int
	makeCap := func() *SpendCap {
		return &SpendCap{Friend: "zhi", Provider: "deepseek", StateDir: state, Now: func() time.Time { return now },
			Spent: func(time.Time) float64 { return 3.10 }, OnJudgment: func(string) { judgments++ }}
	}
	first := &spendHarness{err: OutOfFunds{Session: "s1", Reason: "402 insufficient balance"}}
	_, err := makeCap().Gate(first).Deliver(context.Background(), "one")
	var funds OutOfFunds
	require.ErrorAs(t, err, &funds)

	second := &spendHarness{}
	_, err = makeCap().Gate(second).Deliver(context.Background(), "after restart")
	var deferred Deferred
	require.ErrorAs(t, err, &deferred)
	assert.Contains(t, deferred.Reason, "3.10")
	assert.Equal(t, 0, second.deliver, "persisted provider refusal prevents a retry after restart")
	assert.Equal(t, 1, judgments)
}

func TestSpendCapLedgerReadFailureFailsClosed(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(state, SpendLedgerFile), []byte("not json"), 0o600))
	h := &spendHarness{}
	s := &SpendCap{Friend: "alice", Provider: "deepseek", StateDir: state}
	_, err := s.Gate(h).Deliver(context.Background(), "unknown")
	var deferred Deferred
	require.ErrorAs(t, err, &deferred)
	assert.Contains(t, deferred.Reason, "ledger")
	assert.Equal(t, 0, h.deliver, "corrupt ledger cannot be bypassed with a guessed zero")
}

func TestSpendCapLedgerWriteFailureFailsClosed(t *testing.T) {
	t.Parallel()
	// A missing state directory can be read as an empty first-run ledger, but
	// the first atomic ledger write then fails. The cap must stop before
	// handing work to a provider rather than losing the durable stop marker.
	state := filepath.Join(t.TempDir(), "missing-state")
	h := &spendHarness{}
	s := &SpendCap{Friend: "alice", Provider: "deepseek", StateDir: state,
		Spent: func(time.Time) float64 { return 0 }}
	_, err := s.Gate(h).Deliver(context.Background(), "unknown")
	var deferred Deferred
	require.ErrorAs(t, err, &deferred)
	assert.Contains(t, deferred.Reason, "ledger")
	assert.Equal(t, 0, h.deliver, "failed persistence cannot allow an unaccounted provider turn")
}

func TestSpendCapStopsTurnsPastTheCapAndRaisesOneJudgment(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	spent := 10.0
	h := &spendHarness{}
	var judged []string
	s := &SpendCap{
		Friend:   "alice",
		Provider: "deepseek",
		Now:      func() time.Time { return now },
		Cap:      func() float64 { return 20 },
		Spent:    func(time.Time) float64 { return spent },
		Judgment: func(day time.Time, sp, cap float64) string {
			msg := fmt.Sprintf("alice deepseek spend $%.2f of $%.2f", sp, cap)
			judged = append(judged, msg)
			return msg
		},
	}
	g := s.Gate(h)
	_, err := g.Deliver(context.Background(), "one")
	require.NoError(t, err)
	assert.Equal(t, 1, h.deliver, "under the cap a turn launches")
	assert.Empty(t, judged, "under the cap no judgment")

	spent = 25
	_, err = g.Deliver(context.Background(), "two")
	assert.ErrorAs(t, err, &Deferred{}, "past the cap the turn is stopped")
	var def Deferred
	require.ErrorAs(t, err, &def)
	assert.Contains(t, def.Reason, "alice")
	assert.Contains(t, def.Reason, "deepseek")
	assert.Contains(t, def.Reason, "25")
	assert.Equal(t, 1, h.deliver, "past the cap no turn launches")
	require.Len(t, judged, 1, "one judgment raised")

	_, err = g.Deliver(context.Background(), "three")
	require.ErrorAs(t, err, &Deferred{})
	assert.Len(t, judged, 1, "the judgment is raised once until the day resets")
}

func TestSpendCapResetsOnANewSpendDay(t *testing.T) {
	t.Parallel()
	day := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	now := day.Add(12 * time.Hour)
	spent := 30.0
	h := &spendHarness{}
	var judged []string
	s := &SpendCap{
		Provider: "deepseek",
		Now:      func() time.Time { return now },
		Spent:    func(time.Time) float64 { return spent },
		Judgment: func(day time.Time, sp, cap float64) string { judged = append(judged, "j"); return "j" },
	}
	g := s.Gate(h)
	_, err := g.Deliver(context.Background(), "over")
	require.ErrorAs(t, err, &Deferred{})
	require.Len(t, judged, 1)

	now = day.AddDate(0, 0, 1).Add(9 * time.Hour)
	spent = 5
	_, err = g.Deliver(context.Background(), "new day")
	require.NoError(t, err, "a fresh spend day launches again")
	assert.Equal(t, 1, h.deliver)
	assert.Len(t, judged, 1, "the old day's judgment is not repeated")
}

func TestSpendCapDefaultIsTwentyDollars(t *testing.T) {
	t.Parallel()
	s := &SpendCap{Friend: "bob", Provider: "x", Spent: func(time.Time) float64 { return 21 }}
	assert.Equal(t, DefaultSpendCapUSD, s.capOf())
	assert.True(t, s.over(s.dayStartOf(time.Now())))
}

func TestSpendCapJudgmentNamesFriendProviderAndSpend(t *testing.T) {
	t.Parallel()
	s := &SpendCap{Friend: "bob", Provider: "deepseek", Spent: func(time.Time) float64 { return 25 }}
	h := &spendHarness{}
	g := s.Gate(h)
	_, err := g.Deliver(context.Background(), "over")
	require.ErrorAs(t, err, &Deferred{})
	text := s.Judged(time.Now())
	assert.Contains(t, text, "bob")
	assert.Contains(t, text, "deepseek")
	assert.Contains(t, text, "25")
}

package friend

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type spendHarness struct {
	spent   func(dayStart time.Time) float64
	deliver int
}

func (h *spendHarness) Deliver(ctx context.Context, text string) (int, error) {
	h.deliver++
	return 0, nil
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


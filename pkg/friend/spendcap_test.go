package friend

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testkit/must"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type spendHarness struct {
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
	must.ContainsAll(t, def.Reason, "alice", "deepseek", "25")
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

type spendLaneHarness struct {
	deliverTo int
	open      int
}

func (h *spendLaneHarness) Deliver(ctx context.Context, text string) (int, error) { return 0, nil }

func (h *spendLaneHarness) OpenSession(ctx context.Context, seed string) (string, error) {
	h.open++
	return "session-1", nil
}

func (h *spendLaneHarness) DeliverTo(ctx context.Context, session, text string) (LaneTurn, error) {
	h.deliverTo++
	return LaneTurn{}, nil
}

func TestSpendCapRefuseTellsTheSeatAndTheOwner(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	var judged []string
	s := &SpendCap{
		Friend:   "alice",
		Provider: "deepseek",
		Now:      func() time.Time { return now },
		Spent:    func(time.Time) float64 { return 30 },
		Judgment: func(day time.Time, sp, cap float64) string {
			msg := fmt.Sprintf("alice deepseek $%.2f of $%.2f", sp, cap)
			judged = append(judged, msg)
			return msg
		},
	}
	s.Refuse("QUOTA: Insufficient Balance")
	require.Len(t, judged, 1, "the refusal raises the one judgment to the seat and the owner")
	require.Contains(t, judged[0], "alice")
	require.Contains(t, judged[0], "deepseek")
	g := s.Gate(&spendHarness{})
	_, err := g.Deliver(context.Background(), "one")
	require.ErrorAs(t, err, &Deferred{}, "after a refusal no turn launches")
	require.Len(t, judged, 1, "the refusal's judgment is raised once")
}

func TestSpendCapLanesRespectTheCapAndIdle(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	spent := 10.0
	h := &spendLaneHarness{}
	s := &SpendCap{
		Now:   func() time.Time { return now },
		Cap:   func() float64 { return 20 },
		Spent: func(time.Time) float64 { return spent },
	}
	g := s.Gate(h).(LaneHarness)
	_, err := g.OpenSession(context.Background(), "seed")
	require.NoError(t, err)
	require.Equal(t, 1, h.open, "under the cap a lane opens")

	spent = 25
	_, err = g.OpenSession(context.Background(), "seed")
	require.ErrorAs(t, err, &Deferred{}, "past the cap a lane opens none")
	_, err = g.DeliverTo(context.Background(), "session-1", "text")
	require.ErrorAs(t, err, &Deferred{}, "past the cap a lane delivers none")
	require.Equal(t, 0, h.deliverTo)

	s.Idle = func() bool { return true }
	spent = 5
	_, err = g.OpenSession(context.Background(), "seed")
	require.ErrorAs(t, err, &Deferred{}, "idle a lane opens none")
	require.Contains(t, err.Error(), "no dealt work")
}

func TestSpendCapIdleLaunchesNothing(t *testing.T) {
	t.Parallel()
	h := &spendHarness{}
	s := &SpendCap{Idle: func() bool { return true }}
	g := s.Gate(h)
	_, err := g.Deliver(context.Background(), "one")
	require.ErrorAs(t, err, &Deferred{}, "idle no turn launches")
	require.Contains(t, err.Error(), "no dealt work")
	require.Equal(t, 0, h.deliver)
}

func TestSpendCapJudgmentNamesFriendProviderAndSpend(t *testing.T) {
	t.Parallel()
	s := &SpendCap{Friend: "bob", Provider: "deepseek", Spent: func(time.Time) float64 { return 25 }}
	h := &spendHarness{}
	g := s.Gate(h)
	_, err := g.Deliver(context.Background(), "over")
	require.ErrorAs(t, err, &Deferred{})
	text := s.Judged(time.Now())
	must.ContainsAll(t, text, "bob", "deepseek", "25")
}

// TestSpendCapStopsWhenTheLimitGateReadsAFundsRefusal drives the real gate
// chain the daemon wires, the limit gate under the spend cap: a harness turn
// whose own wording is the incident's "QUOTA: Insufficient Balance" (or a
// 402) is read by the limit gate's Watch, classified as a provider refusal
// for funds, and handed to the spend cap before the limit gate's own Rest
// retry, so the daemon stops at once with the spend-cap judgment naming the
// friend, the provider and the spend, and the harness is never run again
// (docs/SPEC-FRIEND.md, spend caps).
func TestSpendCapStopsWhenTheLimitGateReadsAFundsRefusal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		out  string
	}{
		{"the incident wording", "QUOTA: Insufficient Balance\n"},
		{"a 402", "HTTP 402 Payment Required\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			clock := &limitClock{t: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)}
			var judged []string
			var halted []string
			sc := &SpendCap{
				Friend:   "zhi",
				Provider: "deepseek",
				Now:      clock.now,
				Cap:      func() float64 { return 20 },
				Spent:    func(time.Time) float64 { return 3 },
				Judgment: func(_ time.Time, spent, cap float64) string {
					msg := fmt.Sprintf("zhi deepseek $%.2f of $%.2f", spent, cap)
					judged = append(judged, msg)
					return msg
				},
				Halt: func(reason string, until time.Time) { halted = append(halted, reason) },
			}
			l := &Limits{Now: clock.now, Harness: "opencode"}
			l.Funds = sc.Refuse
			se := &scriptExec{outs: []string{tc.out}, exits: []int{1}}
			h := &OpenCode{Dir: t.TempDir(), Session: "s1", Run: l.Watch(se.run)}
			d := sc.Gate(l.Gate(h))

			_, err := d.Deliver(context.Background(), "hello")
			require.ErrorAs(t, err, &Deferred{}, "the refusal stops the turn")
			require.Len(t, judged, 1, "the one judgment is raised")
			assert.Contains(t, judged[0], "zhi")
			assert.Contains(t, judged[0], "deepseek")
			require.Len(t, halted, 1, "the stop is pushed to the limit gate")

			_, err = d.Deliver(context.Background(), "again")
			require.ErrorAs(t, err, &Deferred{}, "after the refusal no turn launches")
			assert.Len(t, se.texts, 1, "the harness is never run again")
			assert.Len(t, judged, 1, "the judgment is raised once")
		})
	}
}

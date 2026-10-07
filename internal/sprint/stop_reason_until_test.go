package sprint_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nova-tools/internal/sprint"
	"github.com/nova-tools/internal/sprint/store"
)

// stopAt is the fake clock's first reading: 1:04 PM.
var stopAt = time.Date(2030, 1, 2, 13, 4, 0, 0, time.UTC)

// twinClock is a twin store ticked by hand on a fake clock the test steps.
type twinClock struct {
	mu  sync.Mutex
	now time.Time
	st  *store.Store
}

func newTwinClock(t *testing.T) *twinClock {
	t.Helper()
	c := &twinClock{now: stopAt}
	n := 0
	c.st = &store.Store{B: store.NewMem(), Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator", ByHand: true,
		Now:   func() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now },
		NewID: func() string { c.mu.Lock(); defer c.mu.Unlock(); n++; return fmt.Sprint(n) },
		Sleep: func(time.Duration) {}}
	require.NoError(t, c.st.Init(context.Background()))
	return c
}

func (c *twinClock) step(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

func makeProviderOutOfCredit(t *testing.T, c *twinClock) {
	t.Helper()
	mem := c.st.B.(*store.Mem)
	mem.SetRoutes([]sprint.Route{{Name: "flash-a", Tier: "flash", Provider: "provider-a", Enabled: true}})
	mem.SetTiers(map[string][]string{"flash": {"flash-a"}})
	_, err := c.st.Run(context.Background(), store.BalanceStep(sprint.BalanceReq{
		Reads: []sprint.ProviderRead{{Provider: "provider-a", Known: true}}, Who: "coordinator",
	}))
	require.NoError(t, err)
}

// TestStopCarriesReasonAndUntilAndTheMachineRestartsItself pins a stop by hand
// (docs/SPEC-SPRINT.md section 14): both --reason and --until are wanted, the
// machine line says who stopped it, why, and when it is back, and at --until
// the tick starts the machine itself unless it was stopped again since.
func TestStopCarriesReasonAndUntilAndTheMachineRestartsItself(t *testing.T) {
	t.Parallel()
	t.Run("a stop without --reason or --until is refused, naming each", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct {
			name, reason, until string
			want                []string
		}{
			{"neither", "", "", []string{"--reason", "--until"}},
			{"no reason", "", "1h", []string{"--reason"}},
			{"no until", "a bench", "", []string{"--until"}},
			{"until in the past", "a bench", "2030-01-02T12:00:00Z", []string{"--until", "after now"}},
			{"until is now", "a bench", "2030-01-02T13:04:00Z", []string{"--until", "after now"}},
			{"until unreadable", "a bench", "soonish", []string{"--until", "soonish"}},
		} {
			t.Run(c.name, func(t *testing.T) {
				t.Parallel()
				_, err := sprint.StopArgs(c.reason, c.until, stopAt)
				require.Error(t, err)
				for _, w := range c.want {
					assert.Contains(t, err.Error(), w)
				}
			})
		}
	})
	t.Run("--until is a duration, a clock time or an RFC 3339 time", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct {
			until string
			want  time.Time
		}{
			{"1h", stopAt.Add(time.Hour)},
			{"2:04 PM", time.Date(2030, 1, 2, 14, 4, 0, 0, time.UTC)},
			{"14:04", time.Date(2030, 1, 2, 14, 4, 0, 0, time.UTC)},
			{"1:04 PM", stopAt.Add(24 * time.Hour)},                  // exactly now is tomorrow for a clock-time form
			{"9:00 AM", time.Date(2030, 1, 3, 9, 0, 0, 0, time.UTC)}, // passed today: tomorrow's
			{"2030-01-02T14:04:00Z", time.Date(2030, 1, 2, 14, 4, 0, 0, time.UTC)},
		} {
			t.Run(c.until, func(t *testing.T) {
				t.Parallel()
				got, err := sprint.StopArgs("a bench", c.until, stopAt)
				require.NoError(t, err)
				assert.True(t, c.want.Equal(got), "%s: got %s, want %s", c.until, got, c.want)
			})
		}
	})
	t.Run("where and the dashboard say who, why and when", func(t *testing.T) {
		t.Parallel()
		c := newTwinClock(t)
		ctx := context.Background()
		_, _, _, err := c.st.SetMachine(ctx, true)
		require.NoError(t, err)
		until, err := sprint.StopArgs("the bench is rebooting", "1h", stopAt)
		require.NoError(t, err)
		before, after, res, err := c.st.StopUntil(ctx, "the bench is rebooting", until)
		require.NoError(t, err)
		assert.True(t, before.Running())
		assert.False(t, after.Running())
		assert.Equal(t, 1, res.Notes, "the stop is a happened note")
		assert.Equal(t, "machine: STOPPED by coordinator: the bench is rebooting, back by 2:04 PM", c.st.MachineLine(ctx))
	})
	t.Run("the machine starts itself at --until on the twin with a fake clock", func(t *testing.T) {
		t.Parallel()
		c := newTwinClock(t)
		ctx := context.Background()
		_, _, _, err := c.st.SetMachine(ctx, true)
		require.NoError(t, err)
		_, _, _, err = c.st.StopUntil(ctx, "a bench", stopAt.Add(time.Hour))
		require.NoError(t, err)
		c.step(59 * time.Minute)
		res, err := c.st.Tick(ctx)
		require.NoError(t, err)
		assert.Equal(t, store.Stopped, res.State, "a minute before --until")
		c.step(time.Minute)
		res, err = c.st.Tick(ctx)
		require.NoError(t, err)
		assert.Equal(t, store.Running, res.State, "at --until the tick starts the machine")
		m, _, err := c.st.Machine(ctx)
		require.NoError(t, err)
		assert.True(t, m.Running())
		assert.Equal(t, sprint.MachineActor, m.Who, "the start is the machine's own")
		assert.Empty(t, m.Reason)
		assert.True(t, m.Until.IsZero())
		assert.Equal(t, "machine: running", c.st.MachineLine(ctx))
	})
	t.Run("a stop again before --until moves the time back", func(t *testing.T) {
		t.Parallel()
		c := newTwinClock(t)
		ctx := context.Background()
		_, _, _, err := c.st.SetMachine(ctx, true)
		require.NoError(t, err)
		_, _, _, err = c.st.StopUntil(ctx, "a bench", stopAt.Add(time.Hour))
		require.NoError(t, err)
		c.step(30 * time.Minute)
		_, after, _, err := c.st.StopUntil(ctx, "a longer bench", stopAt.Add(2*time.Hour))
		require.NoError(t, err)
		assert.Equal(t, "a longer bench", after.Reason)
		c.step(30 * time.Minute)
		res, err := c.st.Tick(ctx)
		require.NoError(t, err)
		assert.Equal(t, store.Stopped, res.State, "the first --until has passed, the second has not")
		assert.Equal(t, "machine: STOPPED by coordinator: a longer bench, back by 3:04 PM", c.st.MachineLine(ctx))
		c.step(time.Hour)
		res, err = c.st.Tick(ctx)
		require.NoError(t, err)
		assert.Equal(t, store.Running, res.State, "at the second --until")
	})
	t.Run("an expiry with every provider out keeps the machine stopped for funds", func(t *testing.T) {
		t.Parallel()
		c := newTwinClock(t)
		ctx := context.Background()
		makeProviderOutOfCredit(t, c)
		_, _, _, err := c.st.StopUntil(ctx, "a bench", stopAt.Add(time.Hour))
		require.NoError(t, err)
		c.step(time.Hour)
		res, err := c.st.Tick(ctx)
		require.NoError(t, err)
		assert.Equal(t, store.Stopped, res.State)
		m, _, err := c.st.Machine(ctx)
		require.NoError(t, err)
		assert.Equal(t, sprint.FundsCause, m.Cause)
		assert.Empty(t, m.Reason)
		assert.True(t, m.Until.IsZero())
		assert.Contains(t, res.Parts[0].Refused[0].Why, sprint.FundsCause)
	})
	t.Run("a failed funds read leaves the due stop unchanged", func(t *testing.T) {
		t.Parallel()
		c := newTwinClock(t)
		ctx := context.Background()
		mem := c.st.B.(*store.Mem)
		mem.SetRoutes([]sprint.Route{{Name: "flash-a", Tier: "flash", Provider: "provider-a", Enabled: true}})
		mem.SetTiers(map[string][]string{"flash": {"flash-a"}})
		_, _, _, err := c.st.StopUntil(ctx, "a bench", stopAt.Add(time.Hour))
		require.NoError(t, err)
		c.step(time.Hour)
		mem.Fail = func(point string) error {
			if point == "routes" {
				return errors.New("route read failed")
			}
			return nil
		}
		_, err = c.st.Tick(ctx)
		require.ErrorContains(t, err, "route read failed")
		mem.Fail = nil
		m, _, err := c.st.Machine(ctx)
		require.NoError(t, err)
		assert.False(t, m.Running())
		assert.Equal(t, "a bench", m.Reason)
		assert.True(t, stopAt.Add(time.Hour).Equal(m.Until))
	})
	t.Run("a clear takes the time back off: nothing starts a cleared sprint", func(t *testing.T) {
		t.Parallel()
		c := newTwinClock(t)
		ctx := context.Background()
		_, _, _, err := c.st.SetMachine(ctx, true)
		require.NoError(t, err)
		_, _, _, err = c.st.StopUntil(ctx, "a bench", stopAt.Add(time.Hour))
		require.NoError(t, err)
		_, err = c.st.Clear(ctx)
		require.NoError(t, err)
		c.step(2 * time.Hour)
		res, err := c.st.Tick(ctx)
		require.NoError(t, err)
		assert.Equal(t, store.Stopped, res.State)
		assert.Equal(t, "machine: STOPPED", c.st.MachineLine(ctx))
	})
}

package update

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deadlineFake accepts exactly the version transport the inventory invokes,
// and reports timeout only when the propagated deadline expires in virtual time.
func deadlineFake(t *testing.T, remaining time.Duration) processFunc {
	t.Helper()
	return func(ctx context.Context, args []string, input io.Reader, cap int) ProcessResult {
		require.Len(t, args, 2)
		require.Equal(t, "version", args[1])
		require.Nil(t, input)
		require.Equal(t, ChildCap, cap)
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.Equal(t, remaining, deadline.Sub(time.Now()))
		<-ctx.Done()
		require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
		return ProcessResult{Reason: "timeout"}
	}
}

func TestInstalledPropagatesOneDeadlineAcrossTheLadder(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		calls := 0
		fake := func(ctx context.Context, args []string, input io.Reader, cap int) ProcessResult {
			calls++
			want := [][]string{{"fake", "version"}, {"fake", "--version"}}
			require.LessOrEqual(t, calls, len(want))
			require.Equal(t, want[calls-1], args)
			deadline, ok := ctx.Deadline()
			require.True(t, ok)
			require.Equal(t, start.Add(time.Minute), deadline)
			if calls == 1 {
				// Spend part of the single deadline before trying the next rung.
				// Both contexts and this channel are owned by the synctest bubble.
				firstRung, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				finished := firstRung.Done()
				<-finished
				require.Equal(t, start.Add(10*time.Second), time.Now())
				return ProcessResult{Reason: "exit 2"}
			}
			<-ctx.Done()
			return ProcessResult{Reason: "timeout"}
		}
		got := installed(context.Background(), Entry{Kind: "tool", Installed: []string{"fake"}}, time.Minute, true, fake)
		assert.Equal(t, "timeout", got.Reason)
		assert.Equal(t, 2, calls, "a spent child deadline must stop the ladder")
	})
}

func TestInstalledWholeBudgetOverridesChildTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		parent, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		fake := func(ctx context.Context, args []string, input io.Reader, cap int) ProcessResult {
			deadline, ok := ctx.Deadline()
			require.True(t, ok)
			require.Equal(t, time.Minute, deadline.Sub(time.Now()))
			<-ctx.Done()
			return ProcessResult{Stdout: "fake 1.2.3", Reason: "timeout"}
		}
		got := installed(parent, Entry{Kind: "tool", Installed: []string{"fake"}}, 2*time.Minute, true, fake)
		assert.Equal(t, "budget", got.Reason)
		assert.Equal(t, "increase --budget", got.Remedy)
	})
}

func TestBusBoundsCapsAttemptsAtTwentyFive(t *testing.T) {
	t.Parallel()
	attempts, seconds := busBounds(time.Hour)
	assert.Equal(t, 25, attempts)
	assert.Equal(t, 60, seconds)
}

func TestDrainAllowanceUsesPassedTimeAndFloor(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	// The deadline is an inert value: it never starts a real timer.
	for _, tc := range []struct {
		name            string
		remaining, want time.Duration
	}{
		{"expired", -time.Second, drainFloor},
		{"below floor", time.Millisecond, drainFloor},
		{"remaining budget", time.Second, time.Second},
		{"grace cap", 10 * time.Second, killGrace},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := deadlineValue{Context: context.Background(), deadline: now.Add(tc.remaining)}
			assert.Equal(t, tc.want, drainAllowanceAt(ctx, now))
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Equal(t, drainFloor, drainAllowanceAt(ctx, now))
	assert.Equal(t, killGrace, drainAllowanceAt(context.Background(), now))
}

type deadlineValue struct {
	context.Context
	deadline time.Time
}

func (c deadlineValue) Deadline() (time.Time, bool) { return c.deadline, true }

func TestDiffVerbOrdersChangedToolsByName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	before, after := filepath.Join(dir, "before.tsv"), filepath.Join(dir, "after.tsv")
	require.NoError(t, os.WriteFile(before, []byte(snapshotHeader+"\nzeta\told\tr\tp\nalpha\told\tr\tp\nmiddle\told\tr\tp\n"), 0600))
	require.NoError(t, os.WriteFile(after, []byte(snapshotHeader+"\nmiddle\tnew\tr\tp\nalpha\tnew\tr\tp\nzeta\tnew\tr\tp\n"), 0600))
	// Map traversal changes between invocations; each must keep the same order.
	for i := 0; i < 32; i++ {
		var out, errs bytes.Buffer
		require.Zero(t, Run("nova-version", []string{"diff", "--from", before, "--to", after}, "test", &out, &errs, Environment{}), errs.String())
		var names []string
		for _, line := range strings.Split(out.String(), "\n") {
			if strings.HasPrefix(line, "DIFF CHANGED ") {
				names = append(names, strings.Fields(line)[2])
			}
		}
		assert.Equal(t, []string{"name=alpha", "name=middle", "name=zeta"}, names)
	}
}

func TestDiffMovedOrdersFlagChanges(t *testing.T) {
	t.Parallel()
	before := movedInv{"tool": {"verb": {"--zeta": true, "--middle": true}}}
	after := movedInv{"tool": {"verb": {"--alpha": true, "--middle": true}}}
	for i := 0; i < 32; i++ {
		entries, _ := diffMoved(before, after, nil)
		assert.Equal(t, []string{"added=--alpha tool=tool verb=verb", "deleted=--zeta tool=tool verb=verb"}, entries)
	}
}

func TestReportOrdersChangedNames(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "snapshot.json")
	before := emptySnapshot()
	before.Observed = map[string]observed{"zeta": {Raw: "old", Status: "known"}, "alpha": {Raw: "old", Status: "known"}, "middle": {Raw: "old", Status: "known"}}
	entries := []Entry{{Name: "zeta", Kind: "tool", Installed: []string{"2.0.0"}}, {Name: "middle", Kind: "tool", Installed: []string{"2.0.0"}}, {Name: "alpha", Kind: "tool", Installed: []string{"2.0.0"}}}
	fixed := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	env := Environment{Now: func() time.Time { return fixed }, Process: func(context.Context, []string, io.Reader, int) ProcessResult {
		assert.Fail(t, "recorded versions must not start a child")
		return ProcessResult{Reason: "unexpected child"}
	}}
	for i := 0; i < 32; i++ {
		require.NoError(t, writeSnapshot(path, before))
		out := report(context.Background(), "report", entries, entries, options{snapshot: path, timeout: time.Minute, budget: time.Minute}, "tool", "help", fixed, env)
		require.Zero(t, out.Exit)
		var names []string
		for _, item := range out.Items {
			if item.Kind == "changed" {
				for _, field := range item.Fields {
					if field.K == "name" {
						names = append(names, fmt.Sprint(field.V))
					}
				}
			}
		}
		assert.Equal(t, []string{"alpha", "middle", "zeta"}, names)
	}
}

func TestInstalledKeepsRawOnChildFailure(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{"exit 3", "output", "not_found"} {
		t.Run(reason, func(t *testing.T) {
			t.Parallel()
			calls := 0
			fake := func(ctx context.Context, args []string, input io.Reader, cap int) ProcessResult {
				calls++
				assert.Equal(t, []string{"fake", "--flag"}, args)
				return ProcessResult{Stdout: "v9.9.9\nmore", Reason: reason}
			}
			got := installed(context.Background(), Entry{Kind: "tool", Installed: []string{"fake", "--flag"}}, time.Minute, true, fake)
			assert.Equal(t, reason, got.Reason)
			assert.Equal(t, "v9.9.9", got.Raw)
			assert.Equal(t, 1, calls)
		})
	}
}

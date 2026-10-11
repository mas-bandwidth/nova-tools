package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// friendRowsFault wraps the store so its Members (the friend rows) fail while
// fn says so, every other command answering: the rows are the one read the loop
// retries each RowsEvery, not the store's own answer. The loop reads fn on its
// one goroutine, so no lock is needed.
type friendRowsFault struct {
	bus.Store
	fn func() bool
}

func (s friendRowsFault) Members(ctx context.Context) ([]string, []string, time.Time, error) {
	if s.fn() {
		return nil, nil, time.Time{}, errors.New("the friends set is down")
	}
	return s.Store.Members(ctx)
}

// The dispatcher runs the wall verb before the tool's table (main.go:315-319):
// a wall with nothing after -- is refused by friend.RunWall with
// reason=no_command (internal/friend/lanes.go:1028-1030) and nothing runs.
func TestRunWallWithNoCommandIsRefused(t *testing.T) {
	t.Parallel()
	var out, errb strings.Builder
	code := run([]string{"wall"}, strings.NewReader(""), &out, &errb, newRig(t).world())
	assert.Equal(t, sandbox.ExitRefused, code)
	assert.Contains(t, errb.String(), "WALL REFUSED reason=no_command")
}

// An unknown wall flag is refused the same way: the flag set refuses before any
// command is built (internal/friend/lanes.go:1018-1022).
func TestRunWallWithAnUnknownFlagIsRefused(t *testing.T) {
	t.Parallel()
	var out, errb strings.Builder
	code := run([]string{"wall", "--no-such-flag"}, strings.NewReader(""), &out, &errb, newRig(t).world())
	assert.Equal(t, sandbox.ExitRefused, code)
	assert.Contains(t, errb.String(), "no-such-flag")
}

// wall -h is help, never a refusal: the flag set's ErrHelp is exit 0
// (internal/friend/lanes.go:1019-1021).
func TestRunWallHelpExitsZero(t *testing.T) {
	t.Parallel()
	var out, errb strings.Builder
	code := run([]string{"wall", "-h"}, strings.NewReader(""), &out, &errb, newRig(t).world())
	assert.Equal(t, 0, code)
}

// The dispatcher answers hook -h with the tool's own help at exit 0, never the
// claude hook (main.go:309-312): the reader who asks for help gets help.
func TestRunHookDashHPrintsToolHelp(t *testing.T) {
	t.Parallel()
	var out, errb strings.Builder
	code := run([]string{"hook", "-h"}, strings.NewReader(""), &out, &errb, newRig(t).world())
	assert.Equal(t, 0, code)
	assert.Contains(t, out.String(), "nova-friend")
	assert.NotContains(t, out.String(), "hookSpecificOutput")
}

// serve notes a store that fails for a tick once, not once per tick, and notes
// its recovery (serve.go:88-96, 144-147).
func TestServeNotesAStoreFaultOnceAndItsRecovery(t *testing.T) {
	t.Parallel()
	s := newServeRig(t)
	w := s.world()
	w.now = func() time.Time { return s.clock }
	w.random = func() string { return "n00001" }
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	w.sleep = func(_ context.Context, d time.Duration) {
		s.clock = s.clock.Add(d)
		switch s.clock {
		case start.Add(1 * time.Second):
			s.store.Fail = errors.New("connection refused")
		case start.Add(4 * time.Second):
			s.store.Fail = nil
		}
		if !s.clock.Before(start.Add(8 * time.Second)) {
			cancel()
		}
	}
	var out, errb strings.Builder
	code := run([]string{"serve", "--as", "ada"}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	got := out.String()
	assert.Equal(t, 1, strings.Count(got, "the store did not answer"), "one NOTE while it fails, not one per tick\n%s", got)
	assert.Contains(t, got, "SERVE NOTE")
	assert.Contains(t, got, "trying again each tick")
	assert.Contains(t, got, "the rows and the store answer again")
	assert.Contains(t, got, "SERVE STOP interrupted")
}

// A friend-rows re-read that fails after RowsEvery is noted once, and the loop
// pings the names it had (serve.go:101-109, 135-143).
func TestServeNotesAFriendRowsFaultAndKeepsPinging(t *testing.T) {
	t.Parallel()
	s := newServeRig(t)
	w := s.world()
	w.now = func() time.Time { return s.clock }
	w.random = func() string { return "n00001" }
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	rowsDown := false
	w.open = func(context.Context, string) (bus.Store, func(), error) {
		return friendRowsFault{Store: s.store, fn: func() bool { return rowsDown }}, func() {}, nil
	}
	w.sleep = func(_ context.Context, d time.Duration) {
		s.clock = s.clock.Add(d)
		switch s.clock {
		case start.Add(60 * time.Second):
			rowsDown = true
		case start.Add(62 * time.Second):
			rowsDown = false
		}
		if !s.clock.Before(start.Add(66 * time.Second)) {
			cancel()
		}
	}
	var out, errb strings.Builder
	code := run([]string{"serve", "--as", "ada"}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	got := out.String()
	assert.Equal(t, 1, strings.Count(got, "the friend rows cannot be read"), "one NOTE for the failed re-read\n%s", got)
	assert.GreaterOrEqual(t, s.store.Len(bus.StreamOf("bob")), 10, "the loop keeps pinging the names it had")
}

// A store that will not open is waited on, not exited (main.go:1069-1090): each
// wait is said, and a signal during the wait ends the run at exit 0
// (serve.go:67-71).
func TestServeWaitsForAStoreThatWillNotOpenThenStops(t *testing.T) {
	t.Parallel()
	s := newServeRig(t)
	s.store.Fail = errors.New("connection refused")
	w := s.world()
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	w.sleep = func(_ context.Context, d time.Duration) {
		if d == 2*time.Second && cancel != nil {
			cancel()
		}
	}
	var out, errb strings.Builder
	code := run([]string{"serve", "--as", "ada"}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	got := out.String()
	assert.Contains(t, got, "SERVE NOTE")
	assert.Contains(t, got, "opening again in 1s")
	assert.Contains(t, got, "opening again in 2s")
	assert.Contains(t, got, "SERVE STOP interrupted")
}

// A dry run with a store that will not open exits 2 with the open error
// (serve.go:46-58, main.go:1047-1063).
func TestServeDryRunWithAnUnopenableStoreRefuses(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.store.Friends = []string{"ada", "bob"}
	r.store.Fail = errors.New("connection refused")
	var out, errb strings.Builder
	code := run([]string{"serve", "--as", "ada", "--dry-run"}, strings.NewReader(""), &out, &errb, r.world())
	assert.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "connection refused")
}

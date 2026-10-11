package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The wall verb runs one command inside a lane's wall (pkg/friend.RunWall);
// with nothing after it, it refuses with reason=no_command and ExitRefused,
// and runs nothing.
func TestTheWallWithNoCommandIsRefused(t *testing.T) {
	t.Parallel()
	var out, errb strings.Builder
	code := run([]string{"wall"}, strings.NewReader(""), &out, &errb, newRig(t).world())
	assert.Equal(t, sandbox.ExitRefused, code)
	assert.Contains(t, errb.String(), "WALL REFUSED reason=no_command")
}

// An unknown flag is refused before anything is executed, the same
// ExitRefused the no_command refusal takes.
func TestTheWallWithAnUnknownFlagIsRefused(t *testing.T) {
	t.Parallel()
	var out, errb strings.Builder
	code := run([]string{"wall", "--no-such-flag"}, strings.NewReader(""), &out, &errb, newRig(t).world())
	assert.Equal(t, sandbox.ExitRefused, code)
	assert.Contains(t, errb.String(), "flag provided but not defined")
}

// wall -h is help, never a run (ONBOARDING point 3: help is never a refusal).
func TestTheWallDashHIsHelp(t *testing.T) {
	t.Parallel()
	var out, errb strings.Builder
	code := run([]string{"wall", "-h"}, strings.NewReader(""), &out, &errb, newRig(t).world())
	assert.Equal(t, 0, code)
}

// hook -h (and --help) is the tool's own help, exit 0, and never the
// PreToolUse JSON path (main.go's hook dispatch before runClaudeHook).
func TestTheHookDashHIsTheToolHelp(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"-h", "--help"} {
		var out, errb strings.Builder
		code := run([]string{"hook", flag}, strings.NewReader(""), &out, &errb, newRig(t).world())
		assert.Equal(t, 0, code, "%s", errb.String())
		assert.Contains(t, out.String(), "hook")
	}
}

// serve goes on when the store fails one tick and answers the next: one NOTE
// while it fails, never one per tick, then the note that the rows and the
// store answer again (serve.go's fail and its recovery note).
func TestServeNotesAStoreThatFailsForOneTick(t *testing.T) {
	t.Parallel()
	s := newServeRig(t)
	w := s.world()
	w.now = func() time.Time { return s.clock }
	n := 0
	w.random = func() string { n++; return fmt.Sprintf("n%05d", n) }
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	tick := 0
	w.sleep = func(_ context.Context, d time.Duration) {
		tick++
		if tick == 1 {
			s.store.Fail = fmt.Errorf("connection refused")
		}
		if tick == 2 {
			s.store.Fail = nil
		}
		s.clock = s.clock.Add(d)
		if tick >= 4 {
			cancel()
		}
	}
	var out, errb strings.Builder
	code := run([]string{"serve", "--as", "ada"}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	assert.Equal(t, 1, strings.Count(out.String(), "the store did not answer"), "one note while it fails, not one per tick\n%s", out.String())
	assert.Contains(t, out.String(), "the rows and the store answer again")
}

// A friend-rows read that fails on a later re-read is one NOTE saying the rows
// cannot be read, and the pings go on to the names it had (serve.go's fail).
func TestServeNotesAFriendRowsReadThatFailsLater(t *testing.T) {
	t.Parallel()
	s := newServeRig(t)
	w := s.world()
	w.now = func() time.Time { return s.clock }
	n := 0
	w.random = func() string { n++; return fmt.Sprintf("n%05d", n) }
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	tick := 0
	w.sleep = func(_ context.Context, d time.Duration) {
		tick++
		if tick == 2 {
			s.store.Fail = fmt.Errorf("connection refused")
		}
		if tick == 3 {
			s.store.Fail = nil
		}
		s.clock = s.clock.Add(30 * time.Second)
		if tick >= 5 {
			cancel()
		}
	}
	var out, errb strings.Builder
	code := run([]string{"serve", "--as", "ada"}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	assert.Equal(t, 1, strings.Count(out.String(), "the friend rows cannot be read"), "\n%s", out.String())
	assert.Greater(t, s.store.Len(bus.StreamOf("bob")), 0, "pings go on to the names it had")
}

// A store that will not open is retried, its wait said each time, and a signal
// while it is down ends the daemon cleanly: openUntil's doc (main.go) and
// serve's STOP interrupted, exit 0.
func TestServeWaitsForAStoreThatWillNotOpenThenStops(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.store.Friends = []string{"ada", "bob"}
	r.store.Fail = fmt.Errorf("connection refused")
	w := r.world()
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	var slept []time.Duration
	w.sleep = func(_ context.Context, d time.Duration) {
		slept = append(slept, d)
		if d == 2*time.Second {
			cancel()
		}
	}
	var out, errb strings.Builder
	code := run([]string{"serve", "--as", "ada"}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	require.GreaterOrEqual(t, len(slept), 2)
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second}, slept[:2], "longer each time")
	assert.Contains(t, out.String(), "opening again in 1s")
	assert.Contains(t, out.String(), "opening again in 2s")
	assert.Contains(t, out.String(), "SERVE STOP interrupted")
}

// serve --dry-run reads the rows once and refuses with the open error, exit 2,
// when the store will not open (serve.go's dry path through w.bus).
func TestServeDryRunWithAnUnopenableStoreExitsTwo(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.store.Friends = []string{"ada", "bob"}
	r.store.Fail = fmt.Errorf("connection refused")
	w := r.world()
	var out, errb strings.Builder
	code := run([]string{"serve", "--as", "ada", "--dry-run"}, strings.NewReader(""), &out, &errb, w)
	assert.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "connection refused")
}

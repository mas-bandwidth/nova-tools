package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// run wall with nothing after the flags is one WALL REFUSED line on stderr and
// sandbox.ExitRefused, and nothing runs (pkg/friend RunWall, docs/SPEC-FRIEND.md).
func TestWallWithNoCommandIsRefusedAndHelpExitsZero(t *testing.T) {
	t.Parallel()
	var out, errb strings.Builder
	code := run([]string{"wall"}, strings.NewReader(""), &out, &errb, newRig(t).world())
	assert.Equal(t, sandbox.ExitRefused, code, "a wall with nothing to run is refused, not run")
	assert.Contains(t, errb.String(), "WALL REFUSED reason=no_command")
	assert.Empty(t, out.String(), "a refusal says nothing on stdout")

	out.Reset()
	errb.Reset()
	code = run([]string{"wall", "--no-such-flag"}, strings.NewReader(""), &out, &errb, newRig(t).world())
	assert.Equal(t, sandbox.ExitRefused, code, "an unknown wall flag is refused the same way")

	out.Reset()
	errb.Reset()
	code = run([]string{"wall", "-h"}, strings.NewReader(""), &out, &errb, newRig(t).world())
	assert.Equal(t, 0, code, "-h is help and exits 0, never a refusal")
}

// run hook -h (and --help) is the tool's help for the hook verb and exit 0,
// never the claude PreToolUse protocol (main.go's run dispatch).
func TestHookHelpIsTheToolsHelpNotTheClaudeHook(t *testing.T) {
	t.Parallel()
	for _, spelling := range []string{"-h", "--help"} {
		t.Run(spelling, func(t *testing.T) {
			t.Parallel()
			var out, errb strings.Builder
			code := run([]string{"hook", spelling}, strings.NewReader(""), &out, &errb, newRig(t).world())
			assert.Equal(t, 0, code, errb.String())
			assert.Contains(t, out.String(), "hook", "the tool's own hook help, not the PreToolUse protocol")
			assert.Empty(t, errb.String(), "help is never a refusal")
		})
	}
}

// serve says one NOTE while the store does not answer, not one per tick, and
// says so again when it answers (serve.go's fail/failing).
func TestServeSaysAStoreFaultOnceAndWhenItAnswersAgain(t *testing.T) {
	t.Parallel()
	s := newServeRig(t)
	w := s.world()
	clock := start
	w.now = func() time.Time { return clock }
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	n := 0
	w.random = func() string { n++; return fmt.Sprintf("n%05d", n) }
	sleeps := 0
	w.sleep = func(_ context.Context, d time.Duration) {
		sleeps++
		if sleeps == 2 {
			s.store.Fail = fmt.Errorf("connection refused") // the next tick's read fails
		} else {
			s.store.Fail = nil
		}
		clock = clock.Add(d)
		if clock.Sub(start) >= 6*time.Second {
			cancel()
		}
	}
	var out, errb strings.Builder
	code := run([]string{"serve", "--as", "ada"}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	fault, again := 0, 0
	for _, l := range lines {
		switch {
		case strings.Contains(l, "the store did not answer") && strings.Contains(l, "trying again each tick"):
			fault++
		case strings.Contains(l, "the rows and the store answer again"):
			again++
		}
	}
	assert.Equal(t, 1, fault, "one NOTE while it fails, never one per tick: %v", lines)
	assert.Equal(t, 1, again, "one NOTE when it answers again: %v", lines)
}

// serve says one NOTE when a later re-read of the friend rows fails, and its
// pings go on to the names it had (serve.go's re-read, friend.RowsEvery).
func TestServeSaysTheFriendRowsCannotBeReadOnALaterReread(t *testing.T) {
	t.Parallel()
	s := newServeRig(t)
	w := s.world()
	clock := start
	w.now = func() time.Time { return clock }
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	n := 0
	w.random = func() string { n++; return fmt.Sprintf("n%05d", n) }
	sleeps := 0
	w.sleep = func(_ context.Context, d time.Duration) {
		sleeps++
		if sleeps == int(friend.RowsEvery/time.Second) {
			s.store.Fail = fmt.Errorf("connection refused") // the rows re-read one tick later fails
		} else {
			s.store.Fail = nil
		}
		clock = clock.Add(d)
		if clock.Sub(start) >= friend.RowsEvery+3*time.Second {
			cancel()
		}
	}
	var out, errb strings.Builder
	code := run([]string{"serve", "--as", "ada"}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	assert.Contains(t, out.String(), "the friend rows cannot be read", "the failed re-read is one NOTE: %s", out.String())
	assert.Contains(t, out.String(), "trying again each tick", "the note says it keeps trying")
	for _, name := range []string{"bob", "cy", "dee"} {
		assert.NotZero(t, s.store.Len(bus.StreamOf(name)), "%s is still pinged from the names it had", name)
	}
}

// serve retries a store that will not open (1s, 2s, ...) and stops clean on a
// signal, exit 0 (serve.go's openUntil).
func TestServeRetriesAnUnopenableStoreThenStopsOnInterrupt(t *testing.T) {
	t.Parallel()
	s := newServeRig(t)
	s.store.Fail = fmt.Errorf("connection refused")
	s.store.Friends = []string{"ada", "bob"}
	w := s.world()
	clock := start
	w.now = func() time.Time { return clock }
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	sleeps := 0
	w.sleep = func(_ context.Context, d time.Duration) {
		sleeps++
		clock = clock.Add(d)
		if sleeps == 2 {
			cancel()
		}
	}
	var out, errb strings.Builder
	code := run([]string{"serve", "--as", "ada"}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	assert.Contains(t, out.String(), "opening again in 1s")
	assert.Contains(t, out.String(), "opening again in 2s")
	assert.Contains(t, out.String(), "SERVE STOP interrupted")
}

// a dry run with a store that will not open is exit 2 with the open error: a
// dry run makes the rows read, and a read it cannot make is refused.
func TestServeDryRunRefusesAnUnopenableStore(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.store.Fail = fmt.Errorf("connection refused")
	var out, errb strings.Builder
	code := run([]string{"serve", "--as", "ada", "--dry-run"}, strings.NewReader(""), &out, &errb, r.world())
	assert.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "connection refused", "the open error is named, exit 2")
}

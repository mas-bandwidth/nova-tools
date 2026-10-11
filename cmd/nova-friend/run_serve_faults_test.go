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

// This file covers the dispatcher (func run, main.go:308), the wall verb
// (friend.RunWall, pkg/friend/lanes.go:1096) and serve's faults
// (world.serve, serve.go:39). RunWall's doc: "a refusal is one WALL REFUSED
// line per problem on stderr and sandbox.ExitRefused".

// TestARunWallWithNoCommandIsRefusedAndRunsNothing pins friend.RunWall's
// no_command refusal (pkg/friend/lanes.go:1116-1118): nothing after -- is a
// refusal, sandbox.ExitRefused, and no command runs.
func TestARunWallWithNoCommandIsRefusedAndRunsNothing(t *testing.T) {
	t.Parallel()
	var out, errb strings.Builder
	code := run([]string{"wall"}, strings.NewReader(""), &out, &errb, newRig(t).world())
	assert.Equal(t, sandbox.ExitRefused, code)
	assert.Contains(t, errb.String(), "WALL REFUSED reason=no_command")
	assert.Empty(t, out.String(), "nothing is executed, so nothing is printed")
}

// TestARunWallWithAnUnknownFlagIsRefusedTheSameWay pins RunWall's parse
// refusal (pkg/friend/lanes.go:1106-1110): an unknown flag is sandbox.ExitRefused.
func TestARunWallWithAnUnknownFlagIsRefusedTheSameWay(t *testing.T) {
	t.Parallel()
	var out, errb strings.Builder
	code := run([]string{"wall", "--no-such-flag"}, strings.NewReader(""), &out, &errb, newRig(t).world())
	assert.Equal(t, sandbox.ExitRefused, code)
}

// TestARunWallWithDashHExitsZero pins RunWall's help path (pkg/friend/lanes.go:1107-1109).
func TestARunWallWithDashHExitsZero(t *testing.T) {
	t.Parallel()
	var out, errb strings.Builder
	code := run([]string{"wall", "-h"}, strings.NewReader(""), &out, &errb, newRig(t).world())
	assert.Equal(t, 0, code)
}

// TestARunHookDashHPrintsTheToolHelpAndNeverTheClaudeHook pins run's hook
// dispatch (main.go:309-314): hook -h is answered by the tool's help, exit 0,
// never by the PreToolUse hook.
func TestARunHookDashHPrintsTheToolHelpAndNeverTheClaudeHook(t *testing.T) {
	t.Parallel()
	var out, errb strings.Builder
	code := run([]string{"hook", "-h"}, strings.NewReader(""), &out, &errb, newRig(t).world())
	assert.Equal(t, 0, code)
	assert.NotEmpty(t, out.String(), "the tool's help is on stdout")
	assert.NotContains(t, out.String(), "hookDecision")
}

// TestARunHookDashDashHelpPrintsTheToolHelp pins the same dispatch for --help.
func TestARunHookDashDashHelpPrintsTheToolHelp(t *testing.T) {
	t.Parallel()
	var out, errb strings.Builder
	code := run([]string{"hook", "--help"}, strings.NewReader(""), &out, &errb, newRig(t).world())
	assert.Equal(t, 0, code)
	assert.NotEmpty(t, out.String())
}

// serve_faults_test.go behaviours.

// TestServeKeepsOneNoteWhileTheStoreIsDownAndSaysWhenItAnswers pins serve.go's
// fail() coalescing (serve.go:90-96, 144-147): one NOTE while the store does
// not answer, whatever its ticks, then one NOTE when it answers again.
func TestServeKeepsOneNoteWhileTheStoreIsDownAndSaysWhenItAnswers(t *testing.T) {
	t.Parallel()
	s := newServeRig(t)
	w := s.world()
	w.now = func() time.Time { return s.clock }
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	down := fmt.Errorf("connection refused")
	step := 0
	w.sleep = func(_ context.Context, _ time.Duration) {
		step++
		switch step {
		case 1, 2:
			s.store.Fail = down // the next tick's Range fails
		case 3:
			s.store.Fail = nil // the store answers again
		default:
			cancel()
		}
		s.clock = s.clock.Add(friend.PingEvery)
	}
	var out, errb strings.Builder
	code := run([]string{"serve", "--as", "ada"}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	notDown, answered := 0, 0
	for _, l := range lines {
		if strings.Contains(l, "the store did not answer") && strings.Contains(l, "trying again each tick") {
			notDown++
		}
		if strings.Contains(l, "the rows and the store answer again") {
			answered++
		}
	}
	assert.Equal(t, 1, notDown, "one note however many ticks the store is down")
	assert.Equal(t, 1, answered, "one note when the store and the rows answer again")
}

// TestServeNotesARowReReadFailureAndPingsTheNamesItHad pins serve.go's re-read
// fault (serve.go:101-109): a later RowsEvery read that fails is one NOTE, and
// the loop pings on to the names it had.
func TestServeNotesARowReReadFailureAndPingsTheNamesItHad(t *testing.T) {
	t.Parallel()
	s := newServeRig(t)
	w := s.world()
	w.now = func() time.Time { return s.clock }
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	step := 0
	w.sleep = func(_ context.Context, _ time.Duration) {
		step++
		if step == 1 {
			s.clock = s.clock.Add(friend.RowsEvery) // the next tick re-reads the rows
			s.store.Fail = fmt.Errorf("connection refused")
			return
		}
		s.store.Fail = nil
		cancel()
	}
	var out, errb strings.Builder
	code := run([]string{"serve", "--as", "ada"}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	assert.Contains(t, out.String(), "the friend rows cannot be read")
	assert.Positive(t, s.store.Len(bus.StreamOf("bob")), "the pings go on to the names it had")
}

// TestServeRetriesAnUnopenableStoreThenStopsOnInterrupt pins openUntil's doc
// (main.go:1095-1099) through serve (serve.go:67-71): a store that will not
// open is retried with a doubling wait and named on the record, and a
// cancelled run stops at exit 0.
func TestServeRetriesAnUnopenableStoreThenStopsOnInterrupt(t *testing.T) {
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
	step := 0
	w.sleep = func(_ context.Context, _ time.Duration) {
		step++
		if step >= 2 {
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

// TestServeDryRunOnAnUnopenableStoreExits2 pins w.bus' refusal (main.go:1073-1085)
// through serve's dry run (serve.go:46-51): a store that will not open is exit 2
// with the open error, and nothing is pinged.
func TestServeDryRunOnAnUnopenableStoreExits2(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.store.Friends = []string{"ada", "bob"}
	r.store.Fail = fmt.Errorf("connection refused")
	var out, errb strings.Builder
	code := run([]string{"serve", "--as", "ada", "--dry-run"}, strings.NewReader(""), &out, &errb, r.world())
	assert.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "connection refused")
}

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// proveJudgments is a seat whose judgments push is live and whose other
// three proofs are not. The store is the test's one mem backend.
func proveJudgments(t *testing.T, name string) (*testApp, *store.Store) {
	t.Helper()
	ta, session := pushProofSprint(t, name)
	ctx := context.Background()
	ta.ok("init --readers reader-a,reader-b --members m1,m2 --owner glenn")
	target := t.TempDir()
	ta.ok("seat install --redis 127.0.0.1:6381 --harness opencode --target " + target)
	st, err := ta.a.store(common{redis: "mem:0", actor: name, verb: "where"})
	require.NoError(t, err)
	var said bytes.Buffer
	ta.a.prove(ctx, &storeSource{st: st}, name, false, &said)
	ta.ok("seat pong " + nonceOf(t, session.last()))
	return ta, st
}

// A start whose bus proof is older than three minutes is refused, naming
// the command that arms it, and the machine is not started.
func TestAStartWithAStaleBusPushIsRefusedNamingTheCommand(t *testing.T) {
	t.Parallel()
	const name = "pushset-a"
	ta, st := proveJudgments(t, name)
	ctx := context.Background()
	now := ta.a.now()
	require.NoError(t, beatWatch(ctx, st, name, sprint.PushFieldBus, now))
	require.NoError(t, beatWatch(ctx, st, name, sprint.PushFieldFriends, now))
	require.NoError(t, beatWatch(ctx, st, name, sprint.PushFieldTransitions, now))
	ta.step(3*time.Minute + time.Second)
	require.NoError(t, beatWatch(ctx, st, name, sprint.PushFieldTransitions, ta.a.now()))
	before := ta.applies()
	code, out, errs := ta.do("start")
	require.Equal(t, 2, code, "start: exit %d\n%s%s", code, out, errs)
	require.Contains(t, errs, "REFUSED: PUSH DOWN:")
	require.Contains(t, errs, "nova-bus recv --as "+name+" --forever")
	require.Equal(t, 1, strings.Count(errs, "\n"), "one line: %q", errs)
	require.Equal(t, before, ta.applies(), "a refused start wrote")
	require.NotContains(t, out, "START OK")
}

// Fresh proofs let start run, and where shows the same four lines.
func TestAFreshPushSetLetsStartAndWhereShowsTheFourLines(t *testing.T) {
	t.Parallel()
	const name = "pushset-b"
	ta, st := proveJudgments(t, name)
	ctx := context.Background()
	out := ta.ok("friends watch --once")
	require.NotContains(t, out, "FRIENDS WATCH")
	out = ta.ok("status watch --once")
	require.Contains(t, out, "STATUS WATCH STOPPED")
	require.NoError(t, beatWatch(ctx, st, name, sprint.PushFieldBus, ta.a.now()))
	file, ok, err := readSeatPushes(ctx, st, name)
	require.NoError(t, err)
	require.True(t, ok)
	require.False(t, file.FriendsAt.IsZero())
	require.False(t, file.TransitionsAt.IsZero())
	require.False(t, file.BusAt.IsZero())
	frame := ta.ok("where")
	require.Contains(t, frame, "PUSH judgments proven=")
	require.Contains(t, frame, "PUSH bus proven=")
	require.Contains(t, frame, "PUSH friends check proven=")
	require.Contains(t, frame, "PUSH transitions proven=")
	started := ta.ok("start")
	require.Contains(t, started, "PUSH bus proven=")
	require.Contains(t, started, "START OK")
}

package main

import (
	"bytes"
	"context"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestNativeObserversProveOnlyCompletedReadsAndDeliveries(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"friends", "transitions"} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			name := "native-" + source
			ta, session := pushProofSprint(t, name)
			session.beat = nil
			ta.ok("init --readers reader-a --members m1")
			st, err := ta.a.store(common{redis: "mem:0", actor: name})
			require.NoError(t, err)
			rec := sprint.PushRecord{Name: name, Harness: "codex", Target: "job", Session: "session-a", Nonce: "n1", PongOf: "n1", Proven: ta.now}
			require.NoError(t, writePush(context.Background(), st, rec))
			state := pushObserverState{Actor: name, Source: source}
			require.NoError(t, ta.a.pushObserverPass(context.Background(), common{redis: "mem:0", actor: name}, source, "", "", &state))
			set, _, err := st.SeatPushes(context.Background(), name)
			require.NoError(t, err)
			assert.Len(t, set.Watches, 1, "a successful observer proves its own push only")
			assert.NotZero(t, set.Watches[source].At)
			assert.Len(t, session.texts, 1)
			ta.step(time.Second)
			require.NoError(t, ta.a.pushObserverPass(context.Background(), common{redis: "mem:0", actor: name}, source, "", "", &state))
			assert.Len(t, session.texts, 1, "the same snapshot is not delivered twice")
			prior := state.Snapshot
			session.exit = 1
			// A changed seat generation makes the snapshot owe a new delivery.
			require.NoError(t, ta.m.SetKey(context.Background(), "seat", `{"holder":"`+name+`","generation":2}`))
			err = ta.a.pushObserverPass(context.Background(), common{redis: "mem:0", actor: name}, source, "", "", &state)
			require.Error(t, err)
			assert.Equal(t, prior, state.Snapshot, "failed delivery never advances the cursor")
			require.NoError(t, ta.a.beatPushObserver(context.Background(), common{redis: "mem:0", actor: name}, source, err.Error(), ""))
			set, _, err = st.SeatPushes(context.Background(), name)
			require.NoError(t, err)
			assert.NotEmpty(t, set.Watches[source].Failed)
		})
	}
}

func TestTheNativeWatchKeepsStateAndStopsOnItsOwnInterrupt(t *testing.T) {
	t.Parallel()
	const name = "native-interrupt"
	ta, session := pushProofSprint(t, name)
	session.beat = nil
	ta.ok("init --readers reader-a --members m1")
	st, err := ta.a.store(common{redis: "mem:0", actor: name})
	require.NoError(t, err)
	require.NoError(t, writePush(context.Background(), st, sprint.PushRecord{Name: name, Harness: "codex", Target: "job", Session: "session-a", Nonce: "n1", PongOf: "n1", Proven: ta.now}))
	ctx, cancel := context.WithCancel(context.Background())
	ta.a.notify = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }
	ta.a.after = func(time.Duration) <-chan time.Time { cancel(); return nil }
	stateFile := t.TempDir() + "/watch.json"
	code, out, errs := ta.do("friends watch --state " + stateFile)
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "PUSH source=friends")
	state, err := readPushObserverState(stateFile, name, "friends")
	require.NoError(t, err)
	assert.NotEmpty(t, state.Snapshot)
	assert.Len(t, session.texts, 1)
}

func TestChangingVerbsAndTheOneShotWherePrintEveryPush(t *testing.T) {
	t.Parallel()
	const name = "push-lines"
	ta, session := pushProofSprint(t, name)
	session.beat = nil
	ta.ok("init --readers reader-a --members m1")
	st, err := ta.a.store(common{redis: "mem:0", actor: name})
	require.NoError(t, err)
	require.NoError(t, writePush(context.Background(), st, sprint.PushRecord{Name: name, Harness: "codex", Target: "job", Session: "session-a", Nonce: "n1", PongOf: "n1", Proven: ta.now}))
	for _, source := range []string{"bus", "friends", "transitions"} {
		require.NoError(t, st.BeatSeatPush(context.Background(), name, source, ""))
	}
	for _, line := range []string{"start", "stop --reason pause --until 30m", "where"} {
		code, out, errs := ta.do(line)
		require.Equal(t, 0, code, out+errs)
		for _, source := range []string{"judgments", "bus", "friends", "transitions"} {
			assert.Contains(t, out, "PUSH source="+source+" status=UP last=")
		}
	}
}

func TestWhereWatchRendersTheSeatPushSetInItsFrame(t *testing.T) {
	t.Parallel()
	const name = "push-watch-frame"
	ta, session := pushProofSprint(t, name)
	session.beat = nil
	ta.ok("init --readers reader-a --members m1")
	st, err := ta.a.store(common{redis: "mem:0", actor: name})
	require.NoError(t, err)
	require.NoError(t, writePush(context.Background(), st, sprint.PushRecord{Name: name, Harness: "codex", Target: "job", Session: "session-a", Nonce: "n1", PongOf: "n1", Proven: ta.now}))
	for _, source := range []string{"bus", "friends", "transitions"} {
		require.NoError(t, st.BeatSeatPush(context.Background(), name, source, ""))
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	screen := &writeLog{after: func(n int, _ string) {
		if n == 2 {
			cancel()
		}
	}}
	var errs bytes.Buffer
	require.Zero(t, ta.a.whereLoop(ctx, ta.whereRun(true), screen, &errs), errs.String())
	require.Len(t, screen.writes, 3)
	for _, source := range []string{"judgments", "bus", "friends", "transitions"} {
		assert.Contains(t, screen.writes[1], "PUSH source="+source+" status=UP last=")
	}
	code, out, stderr := ta.do("where --json")
	require.Zero(t, code, stderr)
	var view whereView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Len(t, view.Pushes, 4)
}

func TestAnObserverCannotRelabelAnOldPassAfterTheSeatChanges(t *testing.T) {
	t.Parallel()
	const name = "native-old-pass"
	ta, session := pushProofSprint(t, name)
	session.beat = nil
	ta.ok("init --readers reader-a --members m1")
	st, err := ta.a.store(common{redis: "mem:0", actor: name})
	require.NoError(t, err)
	require.NoError(t, writePush(context.Background(), st, sprint.PushRecord{Name: name, Harness: "codex", Target: "job", Session: "session-a", Nonce: "n1", PongOf: "n1", Proven: ta.now}))
	session.beat = func() {
		require.NoError(t, ta.m.SetKey(context.Background(), "seat", `{"holder":"`+name+`","generation":2}`))
	}
	state := pushObserverState{Actor: name, Source: "friends"}
	err = ta.a.pushObserverPass(context.Background(), common{redis: "mem:0", actor: name}, "friends", "", "", &state)
	require.ErrorContains(t, err, "seat or epoch changed")
	set, _, err := st.SeatPushes(context.Background(), name)
	require.NoError(t, err)
	assert.Empty(t, set.Watches, "an old observation cannot renew a new seat's proof")
}

func TestTheNativeBusDeliverCommandUsesTheProvenCurrentSessionWithoutMintingProof(t *testing.T) {
	t.Parallel()
	const name = "native-bus-delivery"
	ta, session := pushProofSprint(t, name)
	session.beat = nil
	ta.ok("init --readers reader-a --members m1")
	st, err := ta.a.store(common{redis: "mem:0", actor: name})
	require.NoError(t, err)
	rec := sprint.PushRecord{Name: name, Harness: "codex", Target: "job", Session: "session-a", Nonce: "n1", PongOf: "n1", Proven: ta.now}
	require.NoError(t, writePush(context.Background(), st, rec))
	code, out, errs := ta.do("seat deliver --text 'bus message'")
	require.Equal(t, 0, code, out+errs)
	assert.Equal(t, "bus message", session.last())
	set, _, err := st.SeatPushes(context.Background(), name)
	require.NoError(t, err)
	assert.Empty(t, set.Watches, "delivery alone is no receiver beat")
	rec.Session = ""
	require.NoError(t, writePush(context.Background(), st, rec))
	code, _, errs = ta.do("seat deliver --text 'another message'")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "session id is unknown")
	assert.Len(t, session.texts, 1, "no discovery silently selects another conversation")
}

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// laneServer answers a lane's take for amy: lane 1 holds s1-1.w1, every other lane none.
func laneServer(asks *int) friend.Ask {
	return func(_ context.Context, argv []string) (string, error) {
		if argv[0] == "friend" {
			return `{"friend":"amy","cards":[]}`, nil
		}
		*asks++
		if argv[4] == "1" {
			return `{"says":["LANE friend.amy lane=1 epoch=2 holds=s1-1.w1@1 kind=work job=s1-1.w1~2"],"packets":[{"card":"s1-1.w1","brief":"b"}]}`, nil
		}
		return fmt.Sprintf(`{"says":["LANE friend.amy lane=%s epoch=2 holds=-"]}`, argv[4]), nil
	}
}

// The lane round in the hook loop: nothing asked while the model does not prove; one notice
// to her own name for new holds, sent once; a notice not sent is sent again next round.
func TestTheLanePassSendsOneNoticeOnlyWhileTheModelProves(t *testing.T) {
	t.Parallel()
	asks := 0
	lc := &friend.Client{Name: "amy", Ask: laneServer(&asks), Dir: t.TempDir(), Fetch: widthRow(2, nil)}
	lc.Apply(context.Background(), time.Unix(1000, 0))
	var sent []bus.Message
	send := func(_ context.Context, m bus.Message) error { sent = append(sent, m); return nil }

	assert.Empty(t, lanePass(context.Background(), lc, false, send))
	assert.Zero(t, asks, "an unproven model's lanes are not asked, so no lease is renewed")
	assert.Empty(t, sent)

	failing := func(context.Context, bus.Message) error { return errors.New("the bus is down") }
	out := lanePass(context.Background(), lc, true, failing)
	require.Len(t, out, 1)
	assert.Equal(t, "lane-notice-unsent", stateFact(out[0]))

	out = lanePass(context.Background(), lc, true, send)
	require.Len(t, sent, 1, "sent again on the next round")
	assert.Equal(t, "lanes-taken", stateFact(out[len(out)-1]))
	m := sent[0]
	assert.Equal(t, "amy", m.From)
	assert.Equal(t, []string{"amy"}, m.To)
	assert.True(t, laneSelf(m, "amy"), "the loop hands its own notice to the core")
	assert.True(t, strings.HasPrefix(m.Body, "1 card taken; briefs at "), m.Body)

	lanePass(context.Background(), lc, true, send)
	assert.Len(t, sent, 1, "told once: the next round sends nothing")
	assert.False(t, laneSelf(bus.Message{From: "amy", Subject: "hello"}, "amy"))
}

// Only a proven record with its evidence standing renews the lanes.
func TestOnlyAProvenRecordRenewsTheLanes(t *testing.T) {
	t.Parallel()
	assert.True(t, proven(hook.Status{State: hook.StateProven}))
	assert.False(t, proven(hook.Status{State: hook.StateProven, Down: "no heartbeat server"}))
	assert.False(t, proven(hook.Status{State: hook.StateLive}))
	assert.False(t, proven(hook.Status{State: hook.StateUnhooked}))
}

// stateFact is a STATE line's state= fact.
func stateFact(o *tool.Out) any {
	for _, f := range o.Facts {
		if f.K == "state" {
			return f.V
		}
	}
	return nil
}

// The lane client renews well inside the server's lane lease: a round every LaneEvery, each
// bounded by laneRoundWait, even late by one whole round, lands before the lease runs out
// (tla/FriendLaneClient.tla: RenewTicks < Bound, LiveHoldNeverExpires; witness
// MCFriendLaneClientBrokenSlowRenew.cfg). It fails if the renewal is ever paced at or past
// the lease again (nova-tools#5798 at 23aec479: the 30m challenge cadence against 10m).
func TestLaneRenewalIsWellInsideTheLease(t *testing.T) {
	t.Parallel()
	assert.Less(t, LaneEvery+laneRoundWait, ServerLaneLease, "a round's renewal lands inside the lease")
	assert.Less(t, 2*(LaneEvery+laneRoundWait), ServerLaneLease, "one late or failed round still renews inside the lease")
	assert.Less(t, laneRoundWait, hook.RenewEvery, "a round never holds the hook loop past its own lease renewal")
	assert.LessOrEqual(t, laneAskWait, laneRoundWait)
}

// widthRow is a fake canonical nova-config read: amy's row at width, or err.
func widthRow(width int, err error) friend.Fetch {
	return func(context.Context, string) (string, error) {
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`{"result":{"status":"ok"},"items":[{"kind":"friend","fields":{"name":"amy","tiers":"flash","width":"%d"}}]}`, width), nil
	}
}

// The loop's config tick: a row applied is one STATE line, the same row again nothing, a read
// that fails one line that closes intake, and the row back one line that opens it; each
// change asks the lanes on the same pass. Clock from 1000, no real time.
func TestTheConfigPassSaysEachChangeOnceAndClosesIntakeOnAFailedRead(t *testing.T) {
	t.Parallel()
	var fail error
	width := 2
	lc := &friend.Client{Name: "amy", Fetch: func(ctx context.Context, n string) (string, error) { return widthRow(width, fail)(ctx, n) }}
	now := time.Unix(1000, 0)
	o, changed := configPass(context.Background(), lc, now)
	require.True(t, changed)
	assert.Equal(t, "config-applied", stateFact(o))
	assert.Equal(t, 2, lc.Intake())
	_, changed = configPass(context.Background(), lc, now.Add(friend.ConfigEvery))
	assert.False(t, changed)

	width = 5
	o, changed = configPass(context.Background(), lc, now.Add(2*friend.ConfigEvery))
	require.True(t, changed, "a raised width takes effect on the next cycle")
	assert.Equal(t, "config-applied", stateFact(o))
	assert.Equal(t, 5, lc.Intake())

	fail = errors.New("the store did not answer")
	o, changed = configPass(context.Background(), lc, now.Add(3*friend.ConfigEvery))
	require.True(t, changed)
	assert.Equal(t, "config-unread", stateFact(o))
	assert.Zero(t, lc.Intake(), "no row read: no lane takes")
	_, changed = configPass(context.Background(), lc, now.Add(4*friend.ConfigEvery))
	assert.False(t, changed, "said once per change")

	fail = nil
	o, changed = configPass(context.Background(), lc, now.Add(5*friend.ConfigEvery))
	require.True(t, changed)
	assert.Equal(t, "config-applied", stateFact(o))
	assert.Equal(t, 5, lc.Intake())
}

// Challenge responsiveness: the config read is bounded well inside a tick's worth of the
// loop, and a read that hangs ends with its context (closing intake), never holding the loop.
func TestAHungConfigReadNeverHoldsTheLoop(t *testing.T) {
	t.Parallel()
	assert.Less(t, configWait+laneRoundWait, hook.RenewEvery, "a config read and a lane round together stay inside the hook's own renewal")
	assert.Less(t, configWait, hook.LeaseTTL/10)
	hung := &friend.Client{Name: "amy", Fetch: func(ctx context.Context, _ string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the deadline already passed: no real time waited
	o, changed := configPass(ctx, hung, time.Unix(1000, 0))
	require.True(t, changed)
	assert.Equal(t, "config-unread", stateFact(o))
	assert.Zero(t, hung.Intake())
}

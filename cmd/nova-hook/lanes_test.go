package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

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
	lc := &friend.Client{Name: "amy", Width: 2, Ask: laneServer(&asks), Dir: t.TempDir()}
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

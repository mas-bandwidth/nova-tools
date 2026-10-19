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
	return func(_ context.Context, argvs [][]string) ([]friend.Reply, error) {
		rs := make([]friend.Reply, len(argvs))
		for i, argv := range argvs {
			if argv[0] == "friend" {
				rs[i].Out = `{"friend":"amy","cards":[]}`
				continue
			}
			*asks++
			if argv[4] == "1" {
				rs[i].Out = `{"says":["LANE friend.amy lane=1 epoch=2 holds=s1-1.w1@1 kind=work job=s1-1.w1~2"],"packets":[{"card":"s1-1.w1","brief":"b"}]}`
			} else {
				rs[i].Out = fmt.Sprintf(`{"says":["LANE friend.amy lane=%s epoch=2 holds=-"]}`, argv[4])
			}
		}
		return rs, nil
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

	out, _ := lanePass(context.Background(), lc, false, send)
	assert.Empty(t, out)
	assert.Zero(t, asks, "an unproven model's lanes are not asked, so no lease is renewed")
	assert.Empty(t, sent)

	failing := func(context.Context, bus.Message) error { return errors.New("the bus is down") }
	out, _ = lanePass(context.Background(), lc, true, failing)
	require.Len(t, out, 1)
	assert.Equal(t, "lane-notice-unsent", stateFact(out[0]))

	out, _ = lanePass(context.Background(), lc, true, send)
	require.Len(t, sent, 1, "sent again on the next round")
	assert.Equal(t, "lanes-taken", stateFact(out[len(out)-1]))
	m := sent[0]
	assert.Equal(t, "amy", m.From)
	assert.Equal(t, []string{"amy"}, m.To)
	assert.True(t, laneSelf(m, "amy"), "the loop hands its own notice to the core")
	assert.True(t, strings.HasPrefix(m.Body, "1 card taken; briefs at "), m.Body)

	_, _ = lanePass(context.Background(), lc, true, send)
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
	assert.GreaterOrEqual(t, laneRoundWait, 30*time.Second, "one request waits out a server answering in 30 s")
	assert.Less(t, LaneEvery+laneRoundWait, ServerLaneLease, "one request's bound stays inside the lease margin")
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
	assert.Less(t, configWait+laneRoundWait, 2*hook.RenewEvery, "a config read and a lane round together stay inside two hook renewals")
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

// The coordinator works her own friend row: her service carries the lanes as a
// friend's does, so taking the seat never drops the cards her row holds.
func TestACoordinatorsServiceCarriesHerRowsLanes(t *testing.T) {
	t.Parallel()
	assert.True(t, laneRole(hook.Friend))
	assert.True(t, laneRole(hook.Coordinator))
	for _, r := range []hook.Role{hook.Comms, hook.Member, hook.Reader} {
		assert.False(t, laneRole(r), string(r))
	}
}

// laneClientOf runs a one-verb tool carrying the hook's lane flags and answers whether a
// friend's service run with args carries a lane client.
func laneClientOf(t *testing.T, args ...string) bool {
	t.Helper()
	w := world{
		getenv: func(k string) string {
			if k == serviceRunEnv {
				return "1"
			}
			return ""
		},
		sprint: func(context.Context, string, []string) (string, error) { return "", nil },
	}
	got := false
	tl := &tool.Tool{Name: "nova-hook", What: "test", Verbs: []tool.Verb{{
		Name: "hook", Effect: tool.Inspection,
		Flags: func(f *tool.Flags) { f.Bool("once", false, "once"); laneFlags(w, f) },
		Run: func(c *tool.Call) *tool.Out {
			got = w.laneClient(c, "amy", hook.Friend) != nil
			return tool.Done()
		},
	}}}
	var out, errb strings.Builder
	require.Equal(t, 0, tl.Run(append([]string{"hook"}, args...), strings.NewReader(""), &out, &errb), errb.String())
	return got
}

// A service installed with an explicit --lanes 0 takes nothing; without the flag, or with
// any other value, the lanes run to her nova-config width as before.
func TestAnExplicitLanesZeroCarriesNoLanes(t *testing.T) {
	t.Parallel()
	assert.False(t, laneClientOf(t, "--lanes", "0"))
	assert.False(t, laneClientOf(t, "--lanes=0"))
	assert.True(t, laneClientOf(t))
	assert.True(t, laneClientOf(t, "--lanes", "3"))
	assert.False(t, laneClientOf(t, "--once"))
}

// serviceArgsOf parses args with the hook verb's own flags and answers the service's argv.
func serviceArgsOf(t *testing.T, args ...string) []string {
	t.Helper()
	w := world{getenv: func(string) string { return "" }}
	tl := hookTool(w)
	var got []string
	for i := range tl.Verbs {
		if tl.Verbs[i].Name == "hook" {
			tl.Verbs[i].Run = func(c *tool.Call) *tool.Out {
				got = hookServiceArgs(c, "amy", hook.Friend, "")
				return tool.Done()
			}
		}
	}
	var out, errb strings.Builder
	require.Equal(t, 0, tl.Run(append([]string{"hook", "--as", "amy", "--role", "friend"}, args...), strings.NewReader(""), &out, &errb), errb.String())
	return got
}

// An install keeps an explicit --lanes 0 in the service's argv, so the service it starts
// carries no lanes; no --lanes, or any other value, is never written.
func TestAnInstallKeepsAnExplicitLanesZero(t *testing.T) {
	t.Parallel()
	assert.Contains(t, strings.Join(serviceArgsOf(t, "--lanes", "0"), " "), "--lanes 0")
	assert.NotContains(t, serviceArgsOf(t), "--lanes")
	assert.NotContains(t, serviceArgsOf(t, "--lanes", "3"), "--lanes")
}

// The round's one request may wait out a server answering in 30 s, and a round the server
// did not answer at all is retried one RenewEvery away, not one LaneEvery away. Fake time
// only: the clock starts at 1000.0 s and nothing waits.
func TestTheLaneRoundBoundCoversASlowServer(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	assert.GreaterOrEqual(t, laneRoundWait, 30*time.Second, "one request waits out a server answering in 30 s")
	assert.Equal(t, now.Add(LaneEvery), laneNext(now, true), "an answered round keeps the LaneEvery pace")
	assert.Equal(t, now.Add(hook.RenewEvery), laneNext(now, false), "an unanswered round is asked again in RenewEvery")

	slow := now.Add(20 * time.Second)
	ask := func(_ context.Context, argvs [][]string) ([]friend.Reply, error) {
		require.LessOrEqual(t, slow.Sub(now), laneRoundWait, "the fake server answers inside the round's bound")
		rs := make([]friend.Reply, len(argvs))
		for i, argv := range argvs {
			if i == 0 {
				rs[i].Out = `{"friend":"amy","cards":[]}`
			} else {
				rs[i].Out = fmt.Sprintf(`{"says":["LANE friend.amy lane=%s epoch=2 holds=-"]}`, argv[4])
			}
		}
		return rs, nil
	}
	lc := &friend.Client{Name: "amy", Ask: ask, Dir: t.TempDir(), Fetch: widthRow(2, nil)}
	lc.Apply(context.Background(), now)
	out, answered := lanePass(context.Background(), lc, true, func(context.Context, bus.Message) error { return nil })
	assert.True(t, answered)
	assert.Empty(t, out)
}

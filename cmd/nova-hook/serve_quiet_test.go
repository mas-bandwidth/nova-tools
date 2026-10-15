package main

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/bus/bustest"
	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/mas-bandwidth/nova-tools/pkg/testredis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestQuietHeartbeatUsesActivityAndAnOutstandingDeadline pins HookBeat Pass:
// passing is not challenging; a pending nonce is not replaced before its deadline.
func TestQuietHeartbeatUsesActivityAndAnOutstandingDeadline(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		role  hook.Role
		quiet time.Duration
		busy  bool
	}{
		{"idle friend", hook.Friend, 10 * time.Minute, false},
		{"busy friend", hook.Friend, 5 * time.Minute, true},
		{"comms", hook.Comms, 10 * time.Minute, false},
		{"coordinator", hook.Coordinator, 30 * time.Minute, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newQuietRig(t)
			r.h.Hook(t, "bob", tc.role)
			ctx := context.Background()
			next := func() string { return "quiet-nonce" }
			pass := func() hook.Beat {
				var busy []string
				if tc.busy {
					busy = []string{"bob"}
				}
				p, err := r.h.Store.Pass(ctx, []string{"bob"}, hook.BeatEvery, hook.DownAfter, next, "test", busy...)
				require.NoError(t, err)
				require.Len(t, p.Beats, 1)
				if p.Beats[0].Did == hook.BeatChallenged {
					require.NoError(t, r.h.Store.Delivered(ctx, p.Beats[0]))
				}
				return p.Beats[0]
			}
			assert.Equal(t, hook.BeatLeft, pass().Did, "initial activity gets a quiet window")
			r.h.Pass(tc.quiet)
			assert.Equal(t, hook.BeatLeft, pass().Did, "the boundary is not older than the quiet window")
			r.h.Pass(hook.BeatEvery)
			first := pass()
			require.Equal(t, hook.BeatChallenged, first.Did)
			r.h.Pass(hook.AnswerBound)
			assert.Equal(t, hook.BeatLeft, pass().Did, "the full answer bound is allowed and the nonce stands")
			r.h.Pass(hook.BeatEvery)
			assert.Equal(t, hook.BeatUnhooked, pass().Did, "past the answer bound and silence bound is down")
			count, err := r.h.Client.XLen(ctx, hook.EventsKey).Result()
			require.NoError(t, err)
			assert.Equal(t, int64(1), count)
		})
	}
}

// TestTalkingFriendPassesThirtyTimesWithoutAChallenge pins genuine send activity
// without renewing any nonce proof. TIME is the fake store's deterministic clock.
func TestTalkingFriendPassesThirtyTimesWithoutAChallenge(t *testing.T) {
	t.Parallel()
	r := newQuietRig(t)
	r.h.Hook(t, "bob", hook.Friend)
	ctx := context.Background()
	b := &bus.Bus{Store: bus.Redis{C: r.h.Client}}
	initial := r.h.State(t, "bob").AnswerAt
	for range 30 {
		_, err := b.Send(ctx, bus.Message{From: "bob", To: []string{"ada"}, Kind: bus.KindReport, Subject: "work progress", Body: "still working", ModelActivity: true})
		require.NoError(t, err)
		p, err := r.h.Store.Pass(ctx, []string{"bob"}, hook.BeatEvery, hook.DownAfter, func() string { return "nonce" }, "test")
		require.NoError(t, err)
		require.Len(t, p.Beats, 1)
		assert.Equal(t, hook.BeatLeft, p.Beats[0].Did)
		r.h.Pass(hook.BeatEvery)
	}
	assert.Equal(t, initial, r.h.State(t, "bob").AnswerAt, "activity never forges a proof")
}

// TestActivityStampMatchesTheFake pins the actual Redis Lua and the in-memory
// bus fake against the same commit, duplicate, and automation cases.
func TestActivityStampMatchesTheFake(t *testing.T) {
	t.Parallel()
	r := newQuietRig(t)
	ctx := context.Background()
	now, err := r.h.Client.Time(ctx).Result()
	require.NoError(t, err)
	fake := bustest.NewFake(now, "bob")
	real := bus.Redis{C: r.h.Client}
	key := hook.RecordKey("bob")
	for _, store := range []bus.Store{real, fake} {
		require.NoError(t, store.AddAll(ctx, nil, nil, bus.Mark{Key: key, Field: "state", Value: "hooked"}))
	}
	cases := []struct {
		name     string
		activity bool
		once     bool
		advance  time.Duration
	}{
		{"daemon report", false, false, time.Minute},
		{"model report", true, false, time.Minute},
		{"model once", true, true, time.Minute},
		{"duplicate once", true, true, time.Minute},
		{"daemon ack", false, false, time.Minute},
	}
	var previous string
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r.h.Pass(tc.advance)
			fake.Advance(tc.advance)
			fields := map[string]string{"from": "bob", "kind": "report", "subject": "work"}
			if tc.activity {
				fields["model_activity"] = "1"
			}
			for _, store := range []bus.Store{real, fake} {
				if tc.once {
					_, _, err := store.AddOnce(ctx, "once", "receipt", time.Hour, []string{bus.LogKey}, fields)
					require.NoError(t, err)
				} else {
					require.NoError(t, store.AddAll(ctx, []string{bus.LogKey}, fields))
				}
			}
			got, err := real.Marks(ctx, key)
			require.NoError(t, err)
			want, err := fake.Marks(ctx, key)
			require.NoError(t, err)
			assert.Equal(t, want, got)
			if !tc.activity || tc.name == "duplicate once" {
				assert.Equal(t, previous, got[0]["bus_sent_at"])
			}
			previous = got[0]["bus_sent_at"]
			assert.Empty(t, got[0]["answered_at"], "a bus send never forges a proof")
		})
	}
}

// TestTalkingStillRenewsProofAndDaemonTrafficDoesNotCount pins the two
// hazards in HookBeat: ignored proof expiry and daemon activity attribution.
func TestTalkingStillRenewsProofAndDaemonTrafficDoesNotCount(t *testing.T) {
	t.Parallel()
	for _, model := range []bool{false, true} {
		t.Run(map[bool]string{false: "daemon", true: "model"}[model], func(t *testing.T) {
			t.Parallel()
			r := newQuietRig(t)
			r.h.Hook(t, "bob", hook.Comms)
			ctx := context.Background()
			b := &bus.Bus{Store: bus.Redis{C: r.h.Client}}
			challenges := 0
			down := false
			for i := 0; i < 45; i++ {
				_, err := b.Send(ctx, bus.Message{From: "bob", To: []string{"ada"}, Kind: bus.KindReport, Subject: "work report", Body: "progress", ModelActivity: model})
				require.NoError(t, err)
				p, err := r.h.Store.Pass(ctx, []string{"bob"}, hook.BeatEvery, hook.DownAfter, func() string { return "renewal" }, "test")
				require.NoError(t, err)
				bt := p.Beats[0]
				if bt.Did == hook.BeatChallenged {
					require.NoError(t, r.h.Store.Delivered(ctx, bt))
					challenges++
					if model {
						assert.GreaterOrEqual(t, i, 33, "conversation suppresses silence challenges until proof renewal")
						require.NoError(t, r.h.Store.Renew(ctx, "bob", hook.Comms, hook.Bus, bt.Epoch, "holder"))
						_, err := r.h.Store.Answer(ctx, "bob", bt.Nonce, "model")
						require.NoError(t, err)
					}
				}
				if bt.Did == hook.BeatUnhooked {
					down = true
					break
				}
				r.h.Pass(time.Minute)
			}
			assert.Equal(t, 1, challenges)
			assert.Equal(t, !model, down)
		})
	}
}

// A transport failure preserves its nonce and deadline; committed once-keys
// reconcile a crash between the bus commit and its delivery acknowledgement.
func TestChallengeDeliveryRetriesAndRecoversAfterCrash(t *testing.T) {
	t.Parallel()
	r := newQuietRig(t)
	r.h.Hook(t, "bob", hook.Comms)
	ctx := context.Background()
	serial := 0
	pass := func() hook.Beat {
		serial++
		p, e := r.h.Store.Pass(ctx, []string{"bob"}, hook.BeatEvery, hook.DownAfter, func() string { return fmt.Sprint("nonce-", serial) }, "test")
		require.NoError(t, e)
		return p.Beats[0]
	}
	r.h.Pass(hook.QuietAfter + time.Minute)
	first := pass()
	require.Equal(t, hook.BeatChallenged, first.Did)
	deadline := r.h.State(t, "bob").PendingUntil
	r.h.Pass(time.Minute)
	retry := pass()
	require.Equal(t, first.Nonce, retry.Nonce)
	require.Equal(t, deadline, r.h.State(t, "bob").PendingUntil)
	b := &bus.Bus{Store: bus.Redis{C: r.h.Client}}
	_, err := b.Send(ctx, bus.Message{From: "ada", To: []string{"bob"}, Subject: "challenge", Body: retry.Nonce, Once: hook.DeliveryOnce(retry.Name, retry.Epoch, retry.Nonce)})
	require.NoError(t, err) // crash here: no Delivered call
	r.h.Pass(time.Minute)
	assert.Equal(t, hook.BeatLeft, pass().Did)
	assert.True(t, r.h.State(t, "bob").PendingSent)
	r.h.Pass(4 * time.Minute)
	assert.Equal(t, hook.BeatUnhooked, pass().Did)
}

func TestExpiredPendingTrafficCanRecoverWithoutOldNonceReplay(t *testing.T) {
	t.Parallel()
	r := newQuietRig(t)
	r.h.Hook(t, "bob", hook.Comms)
	ctx := context.Background()
	pass := func(n string) hook.Beat {
		p, e := r.h.Store.Pass(ctx, []string{"bob"}, hook.BeatEvery, 10*time.Minute, func() string { return n }, "test")
		require.NoError(t, e)
		return p.Beats[0]
	}
	r.h.Pass(hook.QuietAfter + time.Minute)
	first := pass("old")
	require.NoError(t, r.h.Store.Delivered(ctx, first))
	r.h.Pass(hook.AnswerBound + time.Minute)
	b := &bus.Bus{Store: bus.Redis{C: r.h.Client}}
	send := func() {
		_, e := b.Send(ctx, bus.Message{From: "bob", To: []string{"ada"}, Kind: bus.KindReport, Subject: "work", Body: "progress", ModelActivity: true})
		require.NoError(t, e)
	}
	send()
	assert.Equal(t, hook.BeatLeft, pass("not-due").Did)
	_, err := r.h.Store.Answer(ctx, "bob", "old", "model")
	require.Error(t, err, "expired pending must be invalidated even with custom down-after=10m")
	r.h.Pass(18 * time.Minute)
	send()
	next := pass("fresh")
	require.Equal(t, hook.BeatChallenged, next.Did)
	require.NoError(t, r.h.Store.Renew(ctx, "bob", hook.Comms, hook.Bus, next.Epoch, "holder"))
	_, err = r.h.Store.Answer(ctx, "bob", next.Nonce, "model")
	require.NoError(t, err)
	assert.True(t, r.h.State(t, "bob").PendingUntil.IsZero())
}

func TestQuietEligibilityAndUndeliveredChallengeDoNotReadDown(t *testing.T) {
	t.Parallel()
	r := newQuietRig(t)
	r.h.Hook(t, "bob", hook.Comms)
	ctx := context.Background()
	pass := func() {
		_, e := r.h.Store.Pass(ctx, []string{"bob"}, hook.BeatEvery, hook.DownAfter, func() string { return "nonce" }, "test")
		require.NoError(t, e)
	}
	r.h.Pass(hook.QuietAfter)
	pass()
	r.h.Pass(time.Second)
	assert.Empty(t, r.h.State(t, "bob").Down, "quiet threshold only makes a challenge eligible")
	r.h.Pass(time.Minute)
	pass()
	r.h.Pass(hook.AnswerBound + time.Second)
	// Keep the server fresh while reading the undelivered expired challenge.
	require.NoError(t, r.h.Client.HSet(ctx, hook.ServeKey, "at", r.h.State(t, "bob").PendingUntil.Add(time.Second).UnixMilli()).Err())
	assert.Empty(t, r.h.State(t, "bob").Down, "a prepared but undelivered challenge is not a missed answer")
}

func TestFailedXADDNeverStampsActivityOrOnceReceipt(t *testing.T) {
	t.Parallel()
	for _, once := range []bool{false, true} {
		t.Run(fmt.Sprint(once), func(t *testing.T) {
			t.Parallel()
			r := newQuietRig(t)
			r.h.Hook(t, "bob", hook.Comms)
			ctx := context.Background()
			store := bus.Redis{C: r.h.Client}
			require.NoError(t, r.h.Client.Set(ctx, "bad-stream", "wrong-type", 0).Err())
			fields := map[string]string{"from": "bob", "model_activity": "1"}
			streams := []string{"good-stream", "bad-stream"}
			for range 2 { // a refused retry cannot append twice to the first destination
				if once {
					_, _, err := store.AddOnce(ctx, "failed-once", "receipt", time.Hour, streams, fields)
					require.Error(t, err)
				} else {
					require.Error(t, store.AddAll(ctx, streams, fields))
				}
			}
			assert.Zero(t, r.h.Client.XLen(ctx, "good-stream").Val())
			assert.Empty(t, r.h.Client.HGet(ctx, hook.RecordKey("bob"), "bus_sent_at").Val())
			assert.Zero(t, r.h.Client.Exists(ctx, "failed-once").Val())
			_, _, err := store.AddOnce(ctx, "bad-lifetime", "receipt", 0, []string{"good-stream"}, fields)
			require.Error(t, err)
			require.Error(t, store.AddAll(ctx, []string{"good-stream"}, fields, bus.Mark{Key: "bad-stream", Field: "x", Value: "y"}))
			require.Error(t, store.AddAll(ctx, []string{hook.RecordKey("bob")}, fields))
			assert.Zero(t, r.h.Client.XLen(ctx, "good-stream").Val())
		})
	}
}

func TestActivitySendUsesOneColdStoreRoundTrip(t *testing.T) {
	t.Parallel()
	r := newQuietRig(t)
	ctx := context.Background()
	trips, countHook := testredis.RoundTrips(t)
	r.h.Client.AddHook(countHook)
	trips.Expect(t, 1, func() {
		require.NoError(t, (bus.Redis{C: r.h.Client}).AddAll(ctx, []string{"stream"}, map[string]string{"from": "bob", "model_activity": "1"}))
	})
}

func TestProofRenewalReservesOnePassAtFractionalOffset(t *testing.T) {
	t.Parallel()
	r := newQuietRig(t)
	ctx := context.Background()
	r.h.Pass(30 * time.Second)
	r.h.Hook(t, "bob", hook.Comms)
	answered := r.h.State(t, "bob").AnswerAt
	r.h.Pass(34*time.Minute + 30*time.Second)
	b := &bus.Bus{Store: bus.Redis{C: r.h.Client}}
	_, err := b.Send(ctx, bus.Message{From: "bob", To: []string{"ada"}, Kind: bus.KindReport, Subject: "work", Body: "progress", ModelActivity: true})
	require.NoError(t, err)
	p, err := r.h.Store.Pass(ctx, []string{"bob"}, hook.BeatEvery, hook.DownAfter, func() string { return "renew" }, "test")
	require.NoError(t, err)
	require.Equal(t, hook.BeatChallenged, p.Beats[0].Did)
	assert.False(t, r.h.State(t, "bob").PendingUntil.After(answered.Add(hook.ProofTTL)), "the full answer window ends before the current proof expires")
}

// The generic rig synthesizes answers while its own fake server owns the row.
// Silence tests transfer that row first, so clock movement cannot forge activity.
func newQuietRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	ctx := context.Background()
	now, err := r.h.Client.Time(ctx).Result()
	require.NoError(t, err)
	require.NoError(t, r.h.Client.HSet(ctx, hook.ServeKey, "by", "silence-test", "at", now.Add(-hook.BeatEvery).UnixMilli(), "every", hook.BeatEvery.Milliseconds()).Err())
	return r
}

func TestBusyPolicyBatchesReadyWorkingIdleAndUnknownRows(t *testing.T) {
	t.Parallel()
	r := newQuietRig(t)
	r.h.Hook(t, "ada", hook.Coordinator)
	for _, n := range []string{"bob", "carl", "dana", "erin"} {
		r.h.Hook(t, n, hook.Friend)
	}
	w := r.world()
	calls := 0
	w.sprintBatch = func(_ context.Context, _ string, argv ...[]string) ([]sprintwire.Result, error) {
		calls++
		require.Len(t, argv, 4)
		return []sprintwire.Result{{Stdout: `{"friend":"bob","cards":[{"col":"working"}]}`}, {Stdout: `{"friend":"carl","cards":[{"col":"ready"}]}`}, {Stdout: `{"friend":"dana","cards":[]}`}, {Code: 1, Stdout: "unavailable"}}, nil
	}
	cli := testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return run(args, stdin, stdout, stderr, w)
	})
	res := cli.Run("serve", "--as", "ada", "--once")
	require.Zero(t, res.Code, res.Stdout+res.Stderr)
	assert.Equal(t, 1, calls)
	assert.Contains(t, res.Stdout, "using busy silence window")
	for _, n := range []string{"bob", "carl", "erin"} {
		assert.Equal(t, hook.BusyQuietAfter, r.h.State(t, n).QuietAfter)
	}
	assert.Equal(t, hook.QuietAfter, r.h.State(t, "dana").QuietAfter)
}

// A pass may take work time before the loop sleeps; a gap under two intervals
// remains healthy. Its whole answer window must still precede proof expiry.
func TestProofRenewalAllowsAHealthySlowPass(t *testing.T) {
	t.Parallel()
	r := newQuietRig(t)
	r.h.Hook(t, "bob", hook.Comms)
	ctx := context.Background()
	expiry := r.h.State(t, "bob").AnswerAt.Add(hook.ProofTTL)
	b := &bus.Bus{Store: bus.Redis{C: r.h.Client}}
	pass := func() {
		_, err := b.Send(ctx, bus.Message{From: "bob", To: []string{"ada"}, Kind: bus.KindReport, Subject: "work", Body: "progress", ModelActivity: true})
		require.NoError(t, err)
		_, err = r.h.Store.Pass(ctx, []string{"bob"}, hook.BeatEvery, hook.DownAfter, func() string { return "slow" }, "test")
		require.NoError(t, err)
	}
	r.h.Pass(33*time.Minute + 59*time.Second)
	pass()
	r.h.Pass(90 * time.Second)
	pass()
	deadline := r.h.State(t, "bob").PendingUntil
	require.False(t, deadline.IsZero())
	assert.False(t, deadline.After(expiry), "a healthy slow pass must retain the full answer bound before proof expiry")
}

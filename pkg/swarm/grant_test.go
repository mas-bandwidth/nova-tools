package swarm

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook/hooktest"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/bus/bustest"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// grantRig owns both fakes; its store clock changes only when a test advances it.
type grantRig struct {
	*bustest.Fake
	now    time.Time
	grants []BenchGrant
	cursor string
}

func (r *grantRig) Members(context.Context) ([]string, []string, time.Time, error) {
	return nil, []string{"ada", "seat", "watcher"}, r.now, nil
}
func (r *grantRig) Snapshot(ctx context.Context) (GrantSnapshot, error) {
	out := append([]BenchGrant(nil), r.grants...)
	for i := range out {
		marks, err := r.Fake.Marks(ctx, GrantPrefix+out[i].ID)
		if err != nil {
			return GrantSnapshot{}, err
		}
		out[i].Told = marks[0]["told"] != ""
	}
	return GrantSnapshot{Grants: out, Now: r.now, Cursor: r.cursor}, nil
}
func (r *grantRig) Wait(context.Context, string, time.Time) error { return nil }

// TestBenchGrantExpiryIsPushed pins Wake/Sweep/Send in tla/BenchGrant.tla:
// one watcher event delivers without another clock step, including retries.
func TestBenchGrantExpiryIsPushed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                                string
		extended, lost, responseLost, ended bool
	}{
		{name: "deadline"}, {name: "extended", extended: true},
		{name: "lost send backstop", lost: true}, {name: "lost response", responseLost: true},
		{name: "ended after deadline retains obligation", ended: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			r := &grantRig{Fake: bustest.NewFake(start, "ada", "seat", "watcher"), now: start, cursor: "1-0"}
			g := BenchGrant{ID: "one", Bench: "bench-a", Grantee: "ada", By: "seat", Granted: start, Deadline: start.Add(30 * time.Minute)}
			if tc.extended {
				g.Deadline = start.Add(time.Hour)
			}
			if tc.ended {
				g.Ended, g.OwedDeadline = true, g.Deadline
			}
			r.grants = []BenchGrant{g}
			w := GrantWatcher{Store: r, Bus: &bus.Bus{Store: r}, From: "watcher", Seat: "seat"}
			r.now = start.Add(30 * time.Minute)
			before := r.now
			if tc.lost {
				r.Fake.Fail = errors.New("transport lost before commit")
			}
			if tc.responseLost {
				r.Fake.Lose = errors.New("response lost after commit")
			}
			// Snapshot is a separate store seam; a transport failure affects sends only.
			if tc.lost {
				r.Fake.Fail = nil
				w.Bus.Store = &grantLostBus{grantRig: r}
			}
			next, err := w.Step(ctx)
			require.NoError(t, err)
			assert.Equal(t, before, r.now, "one event needs no clock advance")
			if tc.extended {
				assert.Equal(t, 0, r.Len(bus.StreamOf("ada")))
				assert.Equal(t, g.Deadline, next.Until)
				return
			}
			if tc.lost {
				assert.Equal(t, 0, r.Len(bus.StreamOf("ada")))
				require.Len(t, next.Failures, 1)
				assert.Equal(t, before.Add(GrantBackstop), next.Until)
				r.now = before.Add(GrantBackstop + time.Second)
				w.Bus.Store = r
				_, err = w.Step(ctx)
				require.NoError(t, err)
			}
			for _, to := range []string{"ada", "seat"} {
				entries, err := r.Range(ctx, bus.StreamOf(to), "-", "+", 0)
				require.NoError(t, err)
				require.Len(t, entries, 1)
				assert.Equal(t, "BENCH GRANT EXPIRED bench-a grantee=ada deadline=2026-01-01T00:30:00Z", entries[0].Message().Subject)
				assert.Equal(t, bus.KindStatus, entries[0].Message().Kind)
			}
			_, err = w.Step(ctx)
			require.NoError(t, err)
			assert.Equal(t, 1, r.Len(bus.StreamOf("ada")), "every wake sweeps but never duplicates")
		})
	}
}

type grantLostBus struct{ *grantRig }

func (r *grantLostBus) AddOnce(context.Context, string, string, time.Duration, []string, map[string]string, ...bus.Mark) (string, bool, error) {
	return "", false, errors.New("transport lost before commit")
}

// The scope's deduplication bound is seven days, not an unbounded promise.
// A freshly restarted watcher reads told forever; this deliberately stale
// sender models one that resumes without rereading the persistent mark.
func TestBenchGrantDeduplicationHasSevenDayBoundary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		elapsed  time.Duration
		messages int
	}{
		{"inside retention", bus.DefaultTokenCleanup - time.Minute, 1},
		{"stale sender beyond retention is outside contract", bus.DefaultTokenCleanup + time.Minute, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f := bustest.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "watcher", "ada", "seat")
			b := &bus.Bus{Store: f}
			m := bus.Message{From: "watcher", To: []string{"ada", "seat"}, Kind: bus.KindStatus,
				Subject: "BENCH GRANT EXPIRED bench-a grantee=ada deadline=2026-01-01T00:00:00Z",
				Body:    "The grant has expired.", Once: GrantPrefix + "once:one",
				Marks: []bus.Mark{{Key: GrantPrefix + "one", Field: "told", Value: "1"}}}
			_, err := b.Send(ctx, m)
			require.NoError(t, err)
			f.Advance(tc.elapsed)
			_, err = b.Send(ctx, m)
			if tc.messages == 1 {
				require.ErrorContains(t, err, "past its life")
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.messages, f.Len(bus.StreamOf("ada")))
		})
	}
}

// The real Lua and pipeline paths run over hooktest's pipe-only fake Redis.
// Mutations are one store transaction; time and expiry survive end/extension.
func TestBenchGrantMutationsUseStoreTime(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, action string
		elapsed      time.Duration
		owed         bool
	}{
		{"extend before expiry", "extend", 10 * time.Minute, false},
		{"end before expiry", "end", 10 * time.Minute, false},
		{"extend after expiry", "extend", 30 * time.Minute, true},
		{"end at expiry", "end", 30 * time.Minute, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			r := hooktest.New(t)
			store := RedisGrantStore{C: r.Client}
			now, err := r.Client.Time(ctx).Result()
			require.NoError(t, err)
			now = now.UTC()
			g, err := store.Change(ctx, "add", BenchGrant{Bench: "bench-a", Grantee: "ada", By: "seat"}, 30*time.Minute)
			require.NoError(t, err)
			assert.Equal(t, now, g.Granted)
			assert.Equal(t, now.Add(30*time.Minute), g.Deadline)
			before, err := store.Snapshot(ctx)
			require.NoError(t, err)
			require.Len(t, before.Grants, 1)
			r.Pass(tc.elapsed)
			changed, err := store.Change(ctx, tc.action, g, time.Hour)
			require.NoError(t, err)
			assert.Equal(t, g.Granted, changed.Granted)
			if tc.owed {
				assert.Equal(t, g.Deadline, changed.OwedDeadline)
			} else {
				assert.True(t, changed.OwedDeadline.IsZero())
			}
			if tc.action == "extend" {
				assert.Equal(t, now.Add(tc.elapsed+time.Hour), changed.Deadline)
			} else {
				assert.True(t, changed.Ended)
			}
			// The change remains after the cursor and wakes immediately, without
			// moving the fake clock or waiting for the old deadline.
			require.NoError(t, store.Wait(ctx, before.Cursor, time.Time{}))
			after, err := store.Snapshot(ctx)
			require.NoError(t, err)
			assert.Equal(t, now.Add(tc.elapsed), after.Now)
			assert.NotEqual(t, before.Cursor, after.Cursor)
			assert.Equal(t, changed, after.Grants[0])
		})
	}
}

func TestBenchGrantRedisSendAndToldCommitTogether(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := hooktest.New(t)
	require.NoError(t, r.Client.SAdd(ctx, "machines", "ada", "seat", "watcher").Err())
	store := RedisGrantStore{C: r.Client}
	g, err := store.Change(ctx, "add", BenchGrant{Bench: "bench-a", Grantee: "ada", By: "seat"}, 30*time.Minute)
	require.NoError(t, err)
	r.Pass(30 * time.Minute)
	w := GrantWatcher{Store: store, Bus: &bus.Bus{Store: bus.Redis{C: r.Client}}, From: "watcher", Seat: "seat"}
	_, err = w.Step(ctx)
	require.NoError(t, err)
	snapshot, err := store.Snapshot(ctx)
	require.NoError(t, err)
	require.Len(t, snapshot.Grants, 1)
	assert.True(t, snapshot.Grants[0].Told)
	for _, name := range []string{"ada", "seat"} {
		n, err := r.Client.XLen(ctx, bus.StreamOf(name)).Result()
		require.NoError(t, err)
		assert.EqualValues(t, 1, n)
	}
	// A restart beyond retention uses the permanent told field, not a stale
	// snapshot: normal restarts never duplicate even after once-key cleanup.
	r.Pass(bus.DefaultTokenCleanup + time.Hour)
	_, err = w.Step(ctx)
	require.NoError(t, err)
	n, err := r.Client.XLen(ctx, bus.StreamOf("ada")).Result()
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	ended, err := store.Change(ctx, "end", g, 0)
	require.NoError(t, err)
	assert.True(t, ended.Told)
}

// grantReadProbe intercepts only the blocking transport, so fake time decides
// the requested wait without opening a socket or waiting on real time.
type grantReadProbe struct {
	args      []any
	pipelines int
}

func (p *grantReadProbe) DialHook(next redis.DialHook) redis.DialHook { return next }
func (p *grantReadProbe) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error { p.pipelines++; return next(ctx, cmds) }
}
func (p *grantReadProbe) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "xread" {
			p.args = append([]any(nil), cmd.Args()...)
			return redis.Nil
		}
		return next(ctx, cmd)
	}
}

func TestBenchGrantWaitUsesServerDeadlineAndCursor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		until time.Duration
		block int64
		read  bool
	}{
		{"deadline", 30 * time.Minute, 1800000, true},
		{"expired deadline", -time.Second, 0, false},
		{"no open deadline", 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			r := hooktest.New(t)
			now, err := r.Client.Time(ctx).Result()
			require.NoError(t, err)
			probe := &grantReadProbe{}
			r.Client.AddHook(probe)
			deadline := time.Time{}
			if tc.until != 0 {
				deadline = now.Add(tc.until)
			}
			require.NoError(t, (RedisGrantStore{C: r.Client}).Wait(ctx, "123-4", deadline))
			if !tc.read {
				assert.Empty(t, probe.args)
				return
			}
			assert.Equal(t, []any{"xread", "count", int64(1), "block", tc.block, "streams", grantChanges, "123-4"}, probe.args)
		})
	}
}

func TestBenchGrantSnapshotUsesTwoBatchesIndependentOfRows(t *testing.T) {
	t.Parallel()
	for _, count := range []int{1, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			r := hooktest.New(t)
			now, err := r.Client.Time(ctx).Result()
			require.NoError(t, err)
			stamp := fmt.Sprint(now.UnixMilli())
			for i := 0; i < count; i++ {
				id := fmt.Sprint(i)
				r.MR.SAdd(grantIndex, id)
				r.MR.HSet(GrantPrefix+id, "bench", "bench-a", "grantee", "ada", "by", "seat", "granted", stamp, "deadline", stamp, "ended", "0", "owed", "0", "told", "")
			}
			probe := &grantReadProbe{}
			r.Client.AddHook(probe)
			snapshot, err := (RedisGrantStore{C: r.Client}).Snapshot(ctx)
			require.NoError(t, err)
			assert.Len(t, snapshot.Grants, count)
			assert.Equal(t, 2, probe.pipelines, "snapshot cost is two batches for every row count")
			t.Logf("grants=%d snapshot_round_trips=%d bound=2", count, probe.pipelines)
		})
	}
}

//go:build functional

package main

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/testredis"
)

type remoteWorkerRedis struct {
	*store.Redis
	locks atomic.Int64
}

func (b *remoteWorkerRedis) Unwrap() store.Backend { return b.Redis }

func (b *remoteWorkerRedis) Acquire(ctx context.Context, gen uint64, op store.OpRecord) (bool, error) {
	ok, err := b.Redis.Acquire(ctx, gen, op)
	if ok && op.Lock {
		b.locks.Add(1)
	}
	return ok, err
}

func (b *remoteWorkerRedis) Lock(ctx context.Context, op store.OpRecord) (bool, error) {
	ok, err := b.Redis.Lock(ctx, op)
	if ok {
		b.locks.Add(1)
	}
	return ok, err
}

// A fresh worker process reads the store across a 100ms link; connection
// setup is warmed separately so the ledger measures the verb's exchanges.
func TestRemoteWorkerVerbsAtOneHundredMilliseconds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	addr := testutil.Start(t)
	admin := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = admin.Close() })
	require.NoError(t, fn.Load(ctx, admin))
	coord := newApp(func(k string) string {
		return map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "coordinator"}[k]
	})
	t.Cleanup(coord.close)
	do := func(args ...string) {
		t.Helper()
		var out, errb bytes.Buffer
		require.Equal(t, 0, coord.run(args, &out, &errb), "%v: %s %s", args, out.String(), errb.String())
	}
	do("init", "--members", "m1:8", "--readers", "reader-a,reader-b,reader-c")
	do("add", "--stream", "s1", "--count", "4")
	do("fleet", "beat", "m1", "--load", "0")
	do("fleet", "up", "m1")
	seed := &store.Store{B: &store.Redis{C: admin, Now: time.Now}, Actor: "coordinator"}
	dealt, err := seed.Run(ctx, store.DealStep(sprint.DealReq{}))
	require.NoError(t, err)
	require.Empty(t, dealt.Refused)
	require.Len(t, dealt.Moved, 4)
	link := testredis.FarLink(t, addr, 100*time.Millisecond)
	c := redis.NewClient(&redis.Options{Addr: link.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	require.NoError(t, c.Ping(ctx).Err())
	b := &remoteWorkerRedis{Redis: &store.Redis{C: c, Now: time.Now}}
	b.CountTrips()
	remote := func(want int, args ...string) string {
		t.Helper()
		a := newApp(func(k string) string {
			return map[string]string{"NOVA_SPRINT_REDIS": link.Addr(), "NOVA_SPRINT_ACTOR": "m1"}[k]
		})
		a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return b, nil }
		var out, errb bytes.Buffer
		before, writes, began := b.Trips(), link.Writes(), time.Now()
		code := a.run(args, &out, &errb)
		fmt.Printf("REMOTE VERB %v exit=%d trips=%d writes=%d wall=%s\n", args, code, b.Trips()-before, link.Writes()-writes, time.Since(began))
		t.Logf("%v: exit=%d trips=%d writes=%d wall=%s stdout=%s stderr=%s", args, code, b.Trips()-before, link.Writes()-writes, time.Since(began), out.String(), errb.String())
		require.Equal(t, want, code, "%v: %s %s", args, out.String(), errb.String())
		return out.String()
	}
	remote(0, "fleet", "beat", "m1", "--load", "0")
	remote(0, "queue", "--as", "m1", "--json")
	remote(0, "take", "--as", "m1", "s1-1.w1@1", "--epoch", "0")
	remote(0, "finish", "--as", "m1", "s1-1.w1@1", "--epoch", "0")
	remote(1, "finish", "--as", "m1", "s1-1.w1@1", "--epoch", "0")
	do("take", "--as", "m1", "s1-2.w1@1", "s1-3.w1@1", "--epoch", "0")
	do("finish", "--as", "m1", "s1-2.w1@1", "s1-3.w1@1", "--epoch", "0")
	require.NoError(t, seed.BeatReaders(ctx))
	do("ask")
	remote(0, "read", "--as", "reader-a", "--begin", "s1-1.r1.reader-a", "--epoch", "0")
	remote(0, "read", "--as", "reader-a", "--ok", "s1-1.r1.reader-a", "--epoch", "0")
	remote(0, "read", "--as", "reader-c", "--broken", "s1-2.r1.reader-c", "--finding", "bad", "--epoch", "0")
	remote(0, "read", "--as", "reader-b", "--return", "s1-3.r1.reader-b", "--reason", "interrupted", "--epoch", "0")

	// Every tick writes all three tables finish reads, so catching up a stale
	// snapshot still costs the remote worker a read of each changed table.
	do("take", "--as", "m1", "s1-4.w1@1", "--epoch", "0")
	do("start")
	tick := &store.Store{B: &store.Redis{C: admin, Now: time.Now}, Actor: sprint.MachineActor, LockAfterLoss: true}
	for _, pulse := range []struct{ table, id string }{
		{sprint.Work, "s1-1"}, {sprint.Readers, "s1-1.r1.reader-a"}, {sprint.Fleet, sprint.CtlID("m1")},
	} {
		tick.Updates = append(tick.Updates, sprint.TableUpdate{Table: pulse.table, Parts: []sprint.TickPartDef{{Name: "pulse", Fn: func(s *sprint.Snapshot, _ sprint.TickReq) (sprint.Plan, int) {
			card := s.T(pulse.table).Card(pulse.id)
			e := ntable.BatchMemberEntry{ID: card.ID, Expect: &ntable.MemberExpect{Revision: strconv.FormatUint(card.Rev, 10)}, Set: map[string]string{"pulse": strconv.Itoa(card.Int("pulse") + 1)}}
			return sprint.Plan{Units: []sprint.Unit{{Key: card.ID, Changes: []sprint.Change{{Table: pulse.table, Entry: e}}, Moved: "pulse " + pulse.table}}}, 0
		}}}})
	}
	done, stop := make(chan error, 1), make(chan struct{})
	var ticks atomic.Int64
	go func() {
		timer := time.NewTicker(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-stop:
				done <- nil
				return
			case <-timer.C:
				ticks.Add(1)
				if _, err := tick.Tick(ctx); err != nil {
					done <- fmt.Errorf("tick: %w", err)
					return
				}
			}
		}
	}()
	t.Cleanup(func() { close(stop); require.NoError(t, <-done) })
	// Five nearby writers keep advancing the same fence while the distant
	// worker reads. Their ordinary guarded plans also wait behind its claim.
	writersStop := make(chan struct{})
	var writers sync.WaitGroup
	var commits [5]atomic.Int64
	for i := range commits {
		writers.Add(1)
		go func() {
			defer writers.Done()
			local := &store.Store{B: &store.Redis{C: admin, Now: time.Now}, Actor: "local-writer"}
			timer := time.NewTicker(200 * time.Millisecond)
			defer timer.Stop()
			for {
				select {
				case <-writersStop:
					return
				case <-timer.C:
					res, err := local.Run(ctx, store.Step{Verb: "local pulse", Load: []string{sprint.Fleet}, Plan: func(s *sprint.Snapshot) sprint.Plan {
						card := s.Fleet.Card(sprint.CtlID("m1"))
						field := "writer" + strconv.Itoa(i)
						entry := ntable.BatchMemberEntry{ID: card.ID, Expect: &ntable.MemberExpect{Revision: strconv.FormatUint(card.Rev, 10)}, Set: map[string]string{field: strconv.Itoa(card.Int(field) + 1)}}
						return sprint.Plan{Units: []sprint.Unit{{Key: card.ID, Changes: []sprint.Change{{Table: sprint.Fleet, Entry: entry}}, Moved: field}}}
					}})
					if !assert.NoError(t, err, "writer %d", i) {
						return
					}
					if !res.Lost && len(res.Moved) > 0 {
						commits[i].Add(1)
					}
				}
			}
		}()
	}
	t.Cleanup(func() { close(writersStop); writers.Wait() })
	before := b.Trips()
	remote(0, "finish", "--as", "m1", "s1-4.w1@1", "--epoch", "0", "--op", "remote-finish")
	require.LessOrEqual(t, b.Trips()-before, int64(90), "the 315-trip exhausted baseline must converge within 90 exchanges")
	require.Positive(t, ticks.Load(), "the finish overlapped a writing tick")
	require.Positive(t, b.locks.Load(), "a contended remote worker takes the existing fence lock")
	for i := range commits {
		require.Positive(t, commits[i].Load(), "local writer %d overlaps the remote finish", i)
	}
	require.Contains(t, remote(0, "finish", "--as", "m1", "s1-4.w1@1", "--epoch", "0", "--op", "remote-finish", "--json"), `"replay":true`)
	require.GreaterOrEqual(t, link.Shortest(), 100*time.Millisecond)
}

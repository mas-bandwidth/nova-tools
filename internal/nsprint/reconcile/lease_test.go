//go:build functional

package reconcile_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// controlRedis starts a throwaway redis-server with the nova_sprint library
// loaded. Same shape as the task controls: internal/nsprint/testutil fails a
// missing binary under NOVA_CI and skips otherwise.
func controlRedis(t *testing.T) (*store.Store, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(context.Background(), client); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	return store.New(client), client
}

func redisNowMS(t *testing.T, ctx context.Context, client *redis.Client) int64 {
	t.Helper()
	now, err := client.Time(ctx).Result()
	if err != nil {
		t.Fatalf("redis TIME: %v", err)
	}
	return now.UnixMilli()
}

func procField(t *testing.T, ctx context.Context, client *redis.Client, field string) string {
	t.Helper()
	v, err := client.HGet(ctx, reconcile.ProcKey, field).Result()
	if err != nil && err != redis.Nil {
		t.Fatalf("HGET %s %s: %v", reconcile.ProcKey, field, err)
	}
	return v
}

// TestControl10StaleInstanceCannotDeal is nova-tools #2726 (#2756 section
// 5.1.1): the reconciler's liveness is its lease:reconciler hash with a random
// instance id and a fencing token, never a pid.
//
// Instance A takes the lease and stops renewing (a hung or dead process). The
// pid it recorded stays alive because another process now has it: here the
// test process itself, which also runs the new instance B. A pid check would
// say A is alive forever (the 4.5 h reused-pid dealer outage). Instead:
//   - while A's lease is fresh, a second instance refuses with the holder;
//   - once the TTL lapses B takes the lease with a new token, although the pid
//     in the stale record is alive;
//   - A cannot renew, cannot run a pass, and its deal duty is never called;
//     its release cannot delete B's lease;
//   - B writes the pass age to proc:reconciler on every pass, and a third
//     instance refuses while B holds the lease.
func TestControl10StaleInstanceCannotDeal(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (calls time.Sleep). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")

	st, client := controlRedis(t)
	ctx := context.Background()
	const shortTTL = 300 * time.Millisecond

	a, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "space", TTL: shortTTL})
	if err != nil {
		t.Fatalf("A acquire: %v", err)
	}
	if got, _ := client.HGet(ctx, reconcile.LeaseKey, "instance").Result(); got != a.Instance() {
		t.Fatalf("lease instance = %q; want A %q", got, a.Instance())
	}
	stalePID, err := client.HGet(ctx, reconcile.LeaseKey, "pid").Int()
	if err != nil {
		t.Fatalf("lease pid: %v", err)
	}

	// A second instance refuses while A's lease is fresh, and names the holder.
	_, err = reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "space", TTL: 6 * time.Second})
	var held *reconcile.HeldError
	if !errors.As(err, &held) {
		t.Fatalf("second acquire while A is fresh = %v; want HeldError", err)
	}
	if held.Instance != a.Instance() || held.Host != "space" {
		t.Fatalf("held by %q on %q; want A %q on space", held.Instance, held.Host, a.Instance())
	}

	// A stops renewing. Its lease lapses on Redis TTL alone.
	deadline := time.Now().Add(5 * time.Second)
	for {
		n, err := client.Exists(ctx, reconcile.LeaseKey).Result()
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("A's lease did not lapse within 5 s of a %s TTL", shortTTL)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The pid A recorded is alive, reused by another process: this test
	// process, which runs instance B. It is not evidence: B takes the lease
	// anyway, with a different instance and token.
	if stalePID != os.Getpid() {
		t.Fatalf("stale pid %d is not this live process %d; the control needs a reused, live pid", stalePID, os.Getpid())
	}
	b, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "space"})
	if err != nil {
		t.Fatalf("B acquire over a lapsed lease with a live pid: %v", err)
	}
	if b.Instance() == a.Instance() || b.Token() == a.Token() {
		t.Fatalf("B reused A's identity: instance %q token_sha %s", b.Instance(), b.TokenSHA())
	}

	// A is fenced everywhere: renew, pass (its deal duty never runs), release.
	if err := a.Renew(ctx); !errors.Is(err, reconcile.ErrFenced) {
		t.Fatalf("A renew = %v; want ErrFenced", err)
	}
	aDeals := 0
	aLoop := &reconcile.Loop{Lease: a, Interval: 100 * time.Millisecond, Duties: []reconcile.Duty{
		func(ctx context.Context, l *reconcile.Lease) (reconcile.Counts, error) {
			aDeals++
			return reconcile.Counts{Dealt: 1}, nil
		},
	}}
	if _, err := aLoop.Pass(ctx); !errors.Is(err, reconcile.ErrFenced) {
		t.Fatalf("A pass = %v; want ErrFenced", err)
	}
	if err := aLoop.Run(ctx); !errors.Is(err, reconcile.ErrFenced) {
		t.Fatalf("A run = %v; want ErrFenced", err)
	}
	if aDeals != 0 {
		t.Fatalf("stale instance A dealt %d times; want 0", aDeals)
	}
	if got := procField(t, ctx, client, "instance"); got != "" {
		t.Fatalf("proc:reconciler written by %q before B's first pass; A must write nothing", got)
	}
	if err := a.Release(ctx); !errors.Is(err, reconcile.ErrFenced) {
		t.Fatalf("A release = %v; want ErrFenced", err)
	}
	if got, _ := client.HGet(ctx, reconcile.LeaseKey, "instance").Result(); got != b.Instance() {
		t.Fatalf("after A's release the lease names %q; want B %q", got, b.Instance())
	}

	// B passes: the pass age is written every pass, by B, at Redis time.
	const passes = 4
	var passAts []int64
	bDeals := 0
	bLoop := &reconcile.Loop{
		Lease:    b,
		Interval: 30 * time.Millisecond,
		Passes:   passes,
		Duties: []reconcile.Duty{
			func(ctx context.Context, l *reconcile.Lease) (reconcile.Counts, error) {
				bDeals++
				return reconcile.Counts{Dealt: 2, Routed: 1}, nil
			},
		},
		AfterPass: func(r reconcile.PassResult) {
			at, err := strconv.ParseInt(procField(t, ctx, client, "pass_at"), 10, 64)
			if err != nil {
				t.Errorf("pass %d: proc:reconciler pass_at: %v", len(passAts)+1, err)
				return
			}
			if now := redisNowMS(t, ctx, client); now-at > 1000 || at > now {
				t.Errorf("pass %d: pass_at %d is %d ms from Redis TIME %d", len(passAts)+1, at, now-at, now)
			}
			if got := procField(t, ctx, client, "instance"); got != b.Instance() {
				t.Errorf("pass %d: proc:reconciler instance %q; want B %q", len(passAts)+1, got, b.Instance())
			}
			if r.PassAt != at {
				t.Errorf("pass %d: result pass_at %d; stored %d", len(passAts)+1, r.PassAt, at)
			}
			passAts = append(passAts, at)
		},
	}
	if err := bLoop.Run(ctx); err != nil {
		t.Fatalf("B run: %v", err)
	}
	if len(passAts) != passes || bDeals != passes {
		t.Fatalf("B ran %d recorded passes and %d deals; want %d each", len(passAts), bDeals, passes)
	}
	for i := 1; i < len(passAts); i++ {
		if passAts[i] <= passAts[i-1] {
			t.Fatalf("pass_at did not advance on pass %d: %v", i+1, passAts)
		}
	}
	for field, want := range map[string]string{"dealt": "2", "routed": "1", "expired": "0", "n": "3", "err": ""} {
		if got := procField(t, ctx, client, field); got != want {
			t.Errorf("proc:reconciler %s = %q; want %q", field, got, want)
		}
	}
	for _, field := range []string{"took_ms", "at", "host"} {
		if procField(t, ctx, client, field) == "" {
			t.Errorf("proc:reconciler %s missing", field)
		}
	}

	// Every pass renewed B's lease to the full TTL; a third instance refuses.
	if ttl, err := client.PTTL(ctx, reconcile.LeaseKey).Result(); err != nil || ttl <= 0 || ttl > reconcile.DefaultTTL {
		t.Fatalf("B lease PTTL = %v, %v; want (0, %s]", ttl, err, reconcile.DefaultTTL)
	}
	if _, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "hulk"}); !errors.As(err, &held) || held.Instance != b.Instance() {
		t.Fatalf("third acquire = %v; want HeldError naming B", err)
	}

	// B's release frees the lease for the next start; A's token still refuses.
	if err := b.Release(ctx); err != nil {
		t.Fatalf("B release: %v", err)
	}
	if n, _ := client.Exists(ctx, reconcile.LeaseKey).Result(); n != 0 {
		t.Fatalf("lease still present after B's release")
	}
	if err := a.Renew(ctx); !errors.Is(err, reconcile.ErrFenced) {
		t.Fatalf("A renew after B released = %v; want ErrFenced (a lapsed token never comes back)", err)
	}
	if reconcile.DefaultInterval != time.Second || reconcile.DefaultTTL != 6*time.Second {
		t.Fatalf("defaults interval %s ttl %s; want 1s and 6s (spec 5.1)", reconcile.DefaultInterval, reconcile.DefaultTTL)
	}
}

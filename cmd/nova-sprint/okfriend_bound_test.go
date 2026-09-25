package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// marginClock is a lease clock the test moves by hand.
type marginClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *marginClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *marginClock) After(time.Duration) <-chan time.Time { return make(chan time.Time) }

func (c *marginClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// TestOkFriendDutyStopsAtLeaseMargin is nova-tools #3805 for ok-to-friend:
// with less than the write margin of the reconciler lease left the duty
// starts no sprint (no group created, nothing read) and says its pass did
// not start (its one read, #3831); with the lease renewed the same duty
// passes the sprint.
func TestOkFriendDutyStopsAtLeaseMargin(t *testing.T) {
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	const S = "control-3805-okf"
	if err := c.SAdd(ctx, "sprints", S).Err(); err != nil {
		t.Fatal(err)
	}
	c.HSet(ctx, "s:"+S, "status", "open")
	st := store.New(c)
	clk := &marginClock{now: time.Unix(1_800_000_000, 0)}
	l, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "ctl-host", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Release(ctx) })
	d := &okFriendDuty{st: st}
	groups := func() int {
		gs, _ := c.XInfoGroups(ctx, "s:"+S+":log").Result()
		return len(gs)
	}

	clk.advance(l.TTL() - 500*time.Millisecond)
	_, err = d.Run(ctx, l)
	if !errors.Is(err, reconcile.ErrLeaseMargin) || !strings.Contains(err.Error(), "pass not started") {
		t.Fatalf("ok-to-friend at 500ms left: %v; want LEASE-MARGIN, pass not started", err)
	}
	if c.Exists(ctx, "s:"+S+":log").Val() != 0 {
		t.Fatal("ok-to-friend started the sprint at the margin")
	}

	if err := l.Renew(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Run(ctx, l); err != nil {
		t.Fatalf("ok-to-friend with the lease renewed: %v", err)
	}
	if groups() != 1 {
		t.Fatalf("groups on s:%s:log: %d, want the sprint started", S, groups())
	}
}

//go:build functional

package verbs

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/machine"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/testredis"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// jrWaitBound is the allowed poll bound: NOVA_TEST_WAIT when set, thirty
// seconds otherwise.
func jrWaitBound(t *testing.T) time.Duration {
	t.Helper()
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatalf("NOVA_TEST_WAIT=%q: %v", v, err)
		}
		return d
	}
	return 30 * time.Second
}

// TestInboxWaitWakesOnTheStore: on a real store holding this build's sprint
// library, the machine's loop runs a real tick whose rule opens two judgments,
// and the coordinator, blocked in inbox --wait (XREAD BLOCK on the log from its
// cursor), wakes ONCE: it reads past the two judgment lines the tick's steps
// XADD and returns on the tick-end note of the tick's last step, judgments=2
// (errata 3 amendment 8). It then blocks again from the tick-end, and a tick
// that addresses it nothing leaves it blocked. The time from the tick's end to
// the wake is logged, not asserted (a wall-clock bound is a load test,
// docs/SPEC-CI.md's waits rule). Then the blocked reader's connection is
// killed and the next tick with a judgment written: the wait reads again, on a
// new connection, from where it had read to, and wakes on that tick's end.
func TestInboxWaitWakesOnTheStore(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	addr := testredis.Start(t)
	admin := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
	reader := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
	t.Cleanup(func() { _ = reader.Close(); _ = admin.Close() })
	if err := fn.LoadTSet(ctx, admin, fn.TSetSprint); err != nil {
		t.Fatalf("FUNCTION LOAD of the sprint profile: %v", err)
	}
	build, err := fn.TSetBuild(fn.TSetSprint)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := tset.NewRedis(addr, "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lc.Close() })
	if _, err := Define(ctx, lc, storeNames, build); err != nil {
		t.Fatalf("define: %v", err)
	}
	c, err := sprintfn.NewRedis(addr, "", "", storeNames, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	e := &Env{C: c, Names: storeNames, Actor: "coordinator"}
	if _, err := Init(ctx, e, InitReq{Coordinator: "coordinator"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := Start(ctx, e, ClockReq{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	n := 2
	loop, err := machine.NewLoop(machine.Config{Names: storeNames, Owner: "token-a", Name: "a", Rules: []sprint.Rule{raiseRule("deal", &n)}})
	if err != nil {
		t.Fatal(err)
	}
	bound := jrWaitBound(t)
	tick := func() (machine.Report, time.Time) {
		t.Helper()
		rep, err := machine.Tick(ctx, c, loop)
		if err != nil {
			t.Fatalf("tick: %v", err)
		}
		return rep, time.Now()
	}

	// The coordinator's loop, on its own connection: wait, and on a wake send it
	// and wait again from the tick-end.
	type wake struct {
		v  InboxView
		at time.Time
	}
	wakes := make(chan wake, 16)
	ce := &Env{C: c, Names: storeNames, Actor: "coordinator"}
	go func() {
		cursor := uint64(0)
		for ctx.Err() == nil {
			var v InboxView
			if _, err := Inbox(ctx, ce, InboxReq{After: cursor, Wait: &InboxWait{Notes: RedisNotes{C: reader}, Timeout: bound}, Out: &v}); err != nil {
				return
			}
			if v.Woke {
				wakes <- wake{v: v, at: time.Now()}
				cursor, _ = strconv.ParseUint(v.Last, 10, 64)
			}
		}
	}()
	// blocked is the id of the client blocked in XREAD, polled for up to the
	// bound.
	blocked := func() string {
		t.Helper()
		deadline := time.Now().Add(bound)
		for time.Now().Before(deadline) {
			list, err := admin.ClientList(ctx).Result()
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(list, "\n") {
				f := map[string]string{}
				for _, kv := range strings.Fields(line) {
					if k, v, ok := strings.Cut(kv, "="); ok {
						f[k] = v
					}
				}
				if f["cmd"] == "xread" && strings.Contains(f["flags"], "b") {
					return f["id"]
				}
			}
			time.Sleep(2 * time.Millisecond)
		}
		t.Fatalf("no reader blocked in XREAD within %s", bound)
		return ""
	}
	next := func() wake {
		t.Helper()
		select {
		case w := <-wakes:
			return w
		case <-time.After(bound):
			t.Fatalf("the coordinator was not woken within %s", bound)
		}
		return wake{}
	}

	blocked()
	tick() // the lease taken, the cursor learned
	rep, ended := tick()
	if rep.Wake != 2 {
		t.Fatalf("the tick of two judgments on the store: %+v", rep)
	}
	w := next()
	t.Logf("the blocked wait returned %s after the tick's end", w.at.Sub(ended))
	if w.v.Judgments != 2 || w.v.Cursor != "0" {
		t.Fatalf("the wake: %+v, want the tick-end of 2 from cursor 0", w.v)
	}
	blocked()
	n = 0
	if rep, _ := tick(); rep.Wake != 0 {
		t.Fatalf("a quiet tick wrote a tick-end: %+v", rep)
	}
	blocked()
	select {
	case w := <-wakes:
		t.Fatalf("two judgments in one tick, and a quiet tick, woke the coordinator twice: the second %+v", w.v)
	default:
	}

	if err := admin.ClientKillByFilter(ctx, "ID", blocked()).Err(); err != nil {
		t.Fatal(err)
	}
	n = 1
	tick()
	if w := next(); w.v.Judgments != 1 {
		t.Fatalf("after the reader's connection was killed the wait returned %+v, want the next tick-end, of 1", w.v)
	}
}

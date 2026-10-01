//go:build functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/redis/go-redis/v9"
)

// tripCounter counts a client's round trips: one a command, one a pipeline.
type tripCounter struct{ n atomic.Int64 }

func (c *tripCounter) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}

func (c *tripCounter) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error { c.n.Add(1); return next(ctx, cmd) }
}

func (c *tripCounter) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error { c.n.Add(1); return next(ctx, cmds) }
}

// storeTick is one tick of the loop on the store, as its hook told of it.
type storeTick struct {
	loopTick
	ended time.Time
}

// On the store (store/waitlog.go): 8 machines of width 4 and 3 streams
// of 20 ready, the loop running on the real clock. Three times, m1 takes its
// 4 and finishes them; the tick after each finish is woken by the log, not
// the clock, and deals m1 back to its width; each finish-to-deal gap is
// logged (the ten-second law: no wall-clock bound under ten seconds is
// asserted; the loop's own count and why are). The wait between ticks is one
// round trip, woken or quiet.
func TestTheLoopWakesOnTheLogOnTheStore(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "coordinator"}
	world := newApp(func(k string) string { return env[k] })
	defer world.close()
	do := func(args ...string) string {
		t.Helper()
		var out, errb bytes.Buffer
		if code := world.run(args, &out, &errb); code != 0 {
			t.Fatalf("%v: %d %s", args, code, errb.String())
		}
		return out.String()
	}
	members := make([]string, 8)
	var spec []string
	for i := range members {
		members[i] = "m" + strconv.Itoa(i+1)
		spec = append(spec, members[i]+":4")
	}
	do("init", "--readers", "reader-a,reader-b", "--members", strings.Join(spec, ","))
	do("add", "--stream", "a,b,c", "--count", "40") // more than the fleet's room, DealAhead x 4 x 8: the finish is dealt back
	beat := func() {
		for _, m := range members {
			do("fleet", "beat", m)
		}
		for _, r := range []string{"reader-a", "reader-b"} {
			do("queue", "--as", r) // a reader's queue is its beat
		}
	}
	beat()
	do("start")
	m1 := func() []queueCard {
		t.Helper()
		var q struct {
			Cards []queueCard `json:"cards"`
		}
		if err := json.Unmarshal([]byte(do("queue", "--as", "m1", "--json")), &q); err != nil {
			t.Fatal(err)
		}
		return q.Cards
	}

	loop := newApp(func(k string) string { return env[k] })
	defer loop.close()
	st, _, code := loop.machineVerb("run", nil, &bytes.Buffer{})
	if st == nil {
		t.Fatalf("run: %d", code)
	}
	trips := &tripCounter{}
	loop.conns[addr].Client().AddHook(trips)

	// the wait alone, quiet and woken: one round trip each
	cursor, err := st.LogTail(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := trips.n.Load()
	if _, why := loop.pace(ctx, st, st.PinnedEpoch(), cursor, time.Now()); why != tickClock || trips.n.Load()-before != 1 {
		t.Fatalf("a quiet wait: %s in %d round trips, want the clock in 1", why, trips.n.Load()-before)
	}
	do("add", "--stream", "d", "--count", "1")
	before = trips.n.Load()
	if _, why := loop.pace(ctx, st, st.PinnedEpoch(), cursor, time.Now()); why != tickLog || trips.n.Load()-before != 1 {
		t.Fatalf("a woken wait: %s in %d round trips, want the log in 1", why, trips.n.Load()-before)
	}

	ticks := make(chan storeTick, 4096)
	loop.ticked = func(n int, began time.Time, why string) {
		select {
		case ticks <- storeTick{loopTick{n, began, why}, time.Now()}:
		default:
		}
	}
	lctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		var out, errb bytes.Buffer
		loop.runLoop(lctx, st, 20, 0, &out, &errb)
	}()
	defer func() { cancel(); <-done }()

	// quiet waits for a tick of the clock: the log has nothing more to say
	quiet := func() {
		t.Helper()
		since := time.Now()
		ceiling := time.After(60 * time.Second)
		for {
			select {
			case k := <-ticks:
				if k.why == tickClock && k.began.After(since) {
					return
				}
			case <-ceiling:
				t.Fatal("no quiet tick in 60s")
			}
		}
	}
	quiet()
	for round := 1; round <= 3; round++ {
		beat()
		if n := len(m1()); n != sprint.DealAhead*4 {
			t.Fatalf("round %d: m1 holds %d cards before its take, want DealAhead times its width 4", round, n)
		}
		do("take", "--as", "m1", "--limit", "4")
		quiet()
		var ids []string
		for _, x := range m1() {
			if x.Col == "working" {
				ids = append(ids, x.ID+"@"+strconv.Itoa(x.Gen))
			}
		}
		do(append([]string{"finish", "--as", "m1", "--epoch", "0"}, ids...)...)
		finished := time.Now()
		ceiling := time.After(60 * time.Second)
		for dealt := false; !dealt; {
			select {
			case k := <-ticks:
				if k.began.Before(finished) {
					continue // in flight as the finish committed
				}
				if k.why != tickLog {
					t.Fatalf("round %d: the tick after the finish was woken by %s, want the log", round, k.why)
				}
				if n := len(m1()); n == sprint.DealAhead*4 {
					t.Logf("round %d: finish to deal %s (tick #%d began %s after the finish; the floor is %s)",
						round, k.ended.Sub(finished).Round(time.Millisecond), k.n, k.began.Sub(finished).Round(time.Millisecond), store.TickFloor)
					dealt = true
				}
			case <-ceiling:
				t.Fatalf("round %d: m1 was not dealt back to DealAhead times its width in 60s", round)
			}
		}
		quiet()
	}
}

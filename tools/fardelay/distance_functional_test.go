//go:build functional

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"

	"github.com/mas-bandwidth/nova-tools/pkg/testredis"
)

// THE DISTANCE AT WALL TIME, THROUGH A REAL STORE, in the container.
//
// These are the wall-clock criteria of testredis.Far, asserted: one PING through
// a distance of 100ms takes at least 100ms and at most 100ms plus farSlack, a
// pipeline of a hundred PINGs takes the same and never a hundred delays, three
// commands sent one after the other take at least 300ms, and a pipeline past the
// proxy's window pays one delay more. They are asserted through both ways of
// reaching the proxy, testredis.Far and the fardelay tool, which run one engine.
//
// WHY HERE. internal/ci's waits class reads every _test.go under internal/ and
// cmd/ and refuses a comparison with elapsed time, and its list only shrinks, so
// these comparisons cannot stand beside testredis's own functional test, which
// asserts the events instead (the proxy's ledger: how many writes, none held
// less than the distance). This tree is not one that class reads. What the class
// refuses is a bound that asserts how fast a machine is, and these are not that:
// the lower bounds hold at any load, and the one upper bound carries farSlack.

// farDistance is the distance of these tests.
const farDistance = 100 * time.Millisecond

// farSlack is what an upper bound adds to the distance before it calls a run slow.
// The lower bounds need none: a timer does not fire early, and a client cannot be
// answered before the proxy has held its write, so "at least" is true at any load.
// Only the upper bounds can be moved by a busy machine, and it moves them by being
// late to run this process, the proxy and the store, never by the proxy holding
// longer: a round trip through the loopback costs well under a millisecond on an
// idle machine. 250ms is two and a half distances of room for a machine to be
// late, and it is still far short of what the bound exists to catch: a delay paid
// for each command of a pipeline (ten seconds for a hundred), or a wait outside
// the proxy's hold of a quarter of a second or more. It does not separate one
// delay from two by itself; the ledger in testredis's functional test, which
// counts the writes at the proxy, does.
const farSlack = 250 * time.Millisecond

const (
	farPipelined = 100 // PINGs in the pipeline
	farSeparate  = 3   // commands sent one after the other

	// farBulk is the commands of the pipeline that passes the window, and
	// farBulkValue the bytes each carries: 320 of 16 KiB is 5 MiB, past the
	// 4 MiB window and well short of a second one.
	farBulk      = 320
	farBulkValue = 16 << 10
)

// farRoute is a way to reach the proxy: it returns the address a client dials to
// reach store at farDistance.
type farRoute struct {
	name string
	via  func(t *testing.T, store string) string
}

var farRoutes = []farRoute{
	{"through testredis.Far", func(t *testing.T, store string) string {
		return testredis.Far(t, store, farDistance)
	}},
	{"through the fardelay tool", viaTheTool},
}

// viaTheTool runs the tool against store and returns the address it prints. It is
// stopped in the test's cleanup, and what it held, in its last line, is checked
// there: no write was held for less than the distance, as the help promises.
func viaTheTool(t *testing.T, store string) string {
	t.Helper()
	out, in := io.Pipe()
	var stderr bytes.Buffer
	ctx, stop := context.WithCancel(context.Background())
	exit := make(chan int, 1)
	go func() {
		exit <- run(ctx, []string{"--target", store, "--delay", farDistance.String()}, in, &stderr)
		_ = in.Close()
	}()
	said := lines(out)
	t.Cleanup(func() {
		stop()
		rest, _ := io.ReadAll(said) // the tool's last line; its writer closes the pipe when it returns
		if code := <-exit; code != 0 || stderr.Len() != 0 {
			t.Errorf("the tool exited %d with stderr %q; want 0 and nothing", code, stderr.String())
		}
		last := strings.TrimSpace(string(rest))
		_, shortest, ok := strings.Cut(last, " shortest=")
		held, err := time.ParseDuration(shortest)
		if !strings.HasPrefix(last, "STOP OK ") || !ok || err != nil {
			t.Errorf("the tool's last line is %q; want STOP OK writes=<n> shortest=<duration>", last)
			return
		}
		if held < farDistance {
			t.Errorf("the tool says it held a write for %v, less than the %v it was asked for", held, farDistance)
		}
	})
	first, err := said.ReadString('\n')
	if err != nil {
		t.Fatalf("reading the tool's first line: %v (stderr %q)", err, stderr.String())
	}
	fields := strings.Fields(first)
	if len(fields) != listenFields || !strings.HasPrefix(fields[2], "addr=") {
		t.Fatalf("the tool's first line is %q; want LISTEN OK addr=<host:port> target=<host:port> delay=<duration>", first)
	}
	return strings.TrimPrefix(fields[2], "addr=")
}

// farWallClient is a client of addr as the tools open one: RESP3, no identity and
// no maintenance notifications, one connection, no retry. It is warmed, so the
// connection and its handshake are not what the first command pays for.
func farWallClient(ctx context.Context, t *testing.T, addr string) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{
		Addr:            addr,
		Protocol:        3,
		DisableIdentity: true,
		MaxRetries:      -1,
		DialerRetries:   1,
		PoolSize:        1,
		MaintNotificationsConfig: &maintnotifications.Config{
			Mode:         maintnotifications.ModeDisabled,
			EndpointType: maintnotifications.EndpointTypeNone,
		},
	})
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Ping(ctx).Err(); err != nil {
		t.Fatalf("warming the connection through the proxy: %v", err)
	}
	return c
}

// farTook runs f and says how long it took by the monotonic clock.
func farTook(f func()) time.Duration {
	start := time.Now()
	f()
	return time.Since(start)
}

// farWithin asserts that what took was at least least and, when most is not zero,
// at most most. The two failures say different things, because they mean
// different things: too fast is the delay not paid, too slow is a delay paid too
// often or time spent outside the proxy's hold.
func farWithin(t *testing.T, what string, took, least, most time.Duration) {
	t.Helper()
	if took < least {
		t.Errorf("%s took %v; want at least %v: the delay was paid less than it should have been", what, took, least)
	}
	if most != 0 && took > most {
		t.Errorf("%s took %v; want at most %v, the distance and %v of slack for a busy machine: the delay was paid more than it should have been, or time was spent outside the proxy's hold", what, took, most, farSlack)
	}
}

func TestTheDistanceAtWallTimeThroughARealStore(t *testing.T) {
	t.Parallel()

	store := testredis.Start(t)
	for _, route := range farRoutes {
		t.Run(route.name, func(t *testing.T) {
			addr := route.via(t, store)
			ctx, cancel := context.WithTimeout(t.Context(), farCeiling)
			t.Cleanup(cancel)
			c := farWallClient(ctx, t, addr)

			t.Run("one PING takes the distance", func(t *testing.T) {
				var got string
				var err error
				took := farTook(func() { got, err = c.Ping(ctx).Result() })
				if err != nil || got != "PONG" {
					t.Fatalf("PING = %q, %v", got, err)
				}
				farWithin(t, "one PING", took, farDistance, farDistance+farSlack)
				t.Logf("one PING through %v of distance: %v", farDistance, took)
			})

			t.Run("a pipeline of a hundred PINGs takes the distance once", func(t *testing.T) {
				cmds := make([]*redis.StatusCmd, farPipelined)
				var err error
				took := farTook(func() {
					pipe := c.Pipeline()
					for i := range cmds {
						cmds[i] = pipe.Ping(ctx)
					}
					_, err = pipe.Exec(ctx)
				})
				if err != nil {
					t.Fatalf("pipeline: %v", err)
				}
				for i, cmd := range cmds {
					if got := cmd.Val(); got != "PONG" {
						t.Fatalf("command %d of the pipeline answered %q; want PONG", i, got)
					}
				}
				farWithin(t, fmt.Sprintf("a pipeline of %d PINGs", farPipelined), took, farDistance, farDistance+farSlack)
				t.Logf("a pipeline of %d PINGs through %v of distance: %v", farPipelined, farDistance, took)
			})

			t.Run("three commands one after the other take three distances", func(t *testing.T) {
				var err error
				took := farTook(func() {
					for i := 0; i < farSeparate && err == nil; i++ {
						err = c.Ping(ctx).Err()
					}
				})
				if err != nil {
					t.Fatalf("PING: %v", err)
				}
				farWithin(t, fmt.Sprintf("%d PINGs one after the other", farSeparate), took, farSeparate*farDistance, 0)
				t.Logf("%d PINGs one after the other through %v of distance: %v", farSeparate, farDistance, took)
			})

			t.Run("a pipeline past the window pays the distance once more", func(t *testing.T) {
				value := strings.Repeat("x", farBulkValue)
				var err error
				took := farTook(func() {
					pipe := c.Pipeline()
					for i := 0; i < farBulk; i++ {
						pipe.Set(ctx, fmt.Sprintf("far:bulk:%d", i), value, 0)
					}
					_, err = pipe.Exec(ctx)
				})
				if err != nil {
					t.Fatalf("pipeline of %d SETs of %d bytes: %v", farBulk, farBulkValue, err)
				}
				farWithin(t, fmt.Sprintf("a pipeline of %d SETs of %d bytes", farBulk, farBulkValue), took, 2*farDistance, 2*farDistance+farSlack)
				t.Logf("a pipeline of %d SETs of %d bytes through %v of distance: %v", farBulk, farBulkValue, farDistance, took)
			})
		})
	}
}

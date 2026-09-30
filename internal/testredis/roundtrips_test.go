package testredis

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"
)

// The unit tier of RoundTrips: a hook is handed a fake processor, or a real
// go-redis client is handed a net.Pipe with a fake store behind it. No test
// here opens a socket or starts a process.

const (
	loopCommands          = 3   // the spec's three commands
	batchCommands         = 100 // a pipeline no one would send one by one
	wrongTripCount        = 2   // a count the three commands do not make
	longRunCommands       = 40  // more commands than a failure names
	tripWorkers           = 8   // goroutines that share one client
	tripCommandsPerWorker = 50  // commands each of them sends
)

// answerCommand is the processor behind the hook in these tests: it answers every
// command and every pipeline and does nothing else.
func answerCommand(context.Context, redis.Cmder) error    { return nil }
func answerPipeline(context.Context, []redis.Cmder) error { return nil }

// getBatch is a pipeline of n GETs, the commands of one batch.
func getBatch(ctx context.Context, n int) []redis.Cmder {
	cmds := make([]redis.Cmder, n)
	for i := range cmds {
		cmds[i] = redis.NewCmd(ctx, "get", "k")
	}
	return cmds
}

// sendGets hands n GETs to the processor one at a time, as a loop in a verb does.
func sendGets(t testing.TB, process redis.ProcessHook, n int) {
	t.Helper()
	ctx := context.Background()
	for range n {
		if err := process(ctx, redis.NewCmd(ctx, "get", "k")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEveryCommandIsOneRoundTrip(t *testing.T) {
	t.Parallel()

	trips, hook := RoundTrips(t)
	process := hook.ProcessHook(answerCommand)
	trips.Expect(t, loopCommands, func() { sendGets(t, process, loopCommands) })
	// Expect fails a count that differs, so the count is not vacuously true: the
	// same three commands are not one and are not four.
	for _, wrong := range []int{1, loopCommands + 1} {
		r := provoke(t, func(tb testing.TB) { trips.Expect(tb, wrong, func() { sendGets(tb, process, loopCommands) }) })
		if r.fatal == "" {
			t.Fatalf("%d commands were taken for %d round trips", loopCommands, wrong)
		}
	}
}

func TestAPipelineIsOneRoundTripWhateverItHolds(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	trips, hook := RoundTrips(t)
	pipeline := hook.ProcessPipelineHook(answerPipeline)
	for _, n := range []int{loopCommands, batchCommands} {
		trips.Expect(t, 1, func() {
			if err := pipeline(ctx, getBatch(ctx, n)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAnEmptyPipelineIsNoRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	trips, hook := RoundTrips(t)
	pipeline := hook.ProcessPipelineHook(answerPipeline)
	trips.Expect(t, 0, func() {
		if err := pipeline(ctx, nil); err != nil {
			t.Fatal(err)
		}
	})
}

func TestTheHookPassesTheAnswerThroughAndCountsAFailedCommand(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	refused := errors.New("the store refused")
	trips, hook := RoundTrips(t)
	trips.Expect(t, 2, func() {
		if err := hook.ProcessHook(func(context.Context, redis.Cmder) error { return refused })(ctx, redis.NewCmd(ctx, "get", "k")); !errors.Is(err, refused) {
			t.Fatalf("the command was answerCommand %v; want what the client answerCommand", err)
		}
		if err := hook.ProcessPipelineHook(func(context.Context, []redis.Cmder) error { return refused })(ctx, getBatch(ctx, loopCommands)); !errors.Is(err, refused) {
			t.Fatalf("the pipeline was answerCommand %v; want what the client answerCommand", err)
		}
	})
	dial := func(context.Context, string, string) (net.Conn, error) { return nil, refused }
	if _, err := hook.DialHook(dial)(ctx, "tcp", "in-process"); !errors.Is(err, refused) {
		t.Fatalf("the dial was answerCommand %v; want what the client answerCommand", err)
	}
	trips.Expect(t, 0, func() {})
}

func TestExpectFailsWhenTheCountDiffersAndSaysBothNumbers(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct{ want, made int }{
		"too many": {want: wrongTripCount, made: loopCommands},
		"too few":  {want: loopCommands, made: wrongTripCount},
		"none":     {want: 1, made: 0},
		"one":      {want: 0, made: 1},
	} {
		trips, hook := RoundTrips(t)
		process := hook.ProcessHook(answerCommand)
		r := provoke(t, func(tb testing.TB) { trips.Expect(tb, c.want, func() { sendGets(tb, process, c.made) }) })
		if r.fatal == "" {
			t.Errorf("%s: expected %d round trips, made %d, and the test passed", name, c.want, c.made)
			continue
		}
		for _, number := range []string{
			"expected " + countOf(c.want),
			"counted " + countOf(c.made),
		} {
			if !strings.Contains(r.fatal, number) {
				t.Errorf("%s: the failure %q does not say %q", name, r.fatal, number)
			}
		}
	}
	// The whole text, once: the two numbers, and what was counted.
	trips, hook := RoundTrips(t)
	process := hook.ProcessHook(answerCommand)
	r := provoke(t, func(tb testing.TB) { trips.Expect(tb, wrongTripCount, func() { sendGets(tb, process, loopCommands) }) })
	if want := "testredis: expected 2 round trips, counted 3 round trips: get; get; get"; r.fatal != want {
		t.Fatalf("the failure:\n got %q\nwant %q", r.fatal, want)
	}
	r = provoke(t, func(tb testing.TB) { trips.Expect(tb, 1, func() {}) })
	if want := "testredis: expected 1 round trip, counted 0 round trips"; r.fatal != want {
		t.Fatalf("the failure:\n got %q\nwant %q", r.fatal, want)
	}
}

func TestExpectNamesWhatItCountedAndNoArgument(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	trips, hook := RoundTrips(t)
	process, pipeline := hook.ProcessHook(answerCommand), hook.ProcessPipelineHook(answerPipeline)
	const password, key = "not-for-a-failure", "not-a-key-for-a-failure"
	r := provoke(t, func(tb testing.TB) {
		trips.Expect(tb, 0, func() {
			_ = process(ctx, redis.NewCmd(ctx, "auth", password))
			_ = pipeline(ctx, []redis.Cmder{redis.NewCmd(ctx, "hset", key, "f", "v"), redis.NewCmd(ctx, "expire", key, "1")})
			_ = pipeline(ctx, []redis.Cmder{redis.NewCmd(ctx, "multi"), redis.NewCmd(ctx, "set", key, "v"), redis.NewCmd(ctx, "del", key), redis.NewCmd(ctx, "exec")})
		})
	})
	if want := "testredis: expected 0 round trips, counted 3 round trips: auth; pipeline(hset, expire); transaction(set, del)"; r.fatal != want {
		t.Fatalf("the failure:\n got %q\nwant %q", r.fatal, want)
	}
	if strings.Contains(r.fatal, password) || strings.Contains(r.fatal, key) {
		t.Fatalf("an argument is in the failure: %q", r.fatal)
	}
}

func TestAFailureNamesTheFirstOfWhatItCountedAndSaysHowManyMore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	trips, hook := RoundTrips(t)
	process, pipeline := hook.ProcessHook(answerCommand), hook.ProcessPipelineHook(answerPipeline)
	r := provoke(t, func(tb testing.TB) {
		trips.Expect(tb, 0, func() {
			if err := pipeline(ctx, getBatch(ctx, longRunCommands)); err != nil {
				tb.Fatal(err)
			}
		})
	})
	want := fmt.Sprintf("counted 1 round trip: pipeline(%s, and %d more)", strings.TrimSuffix(strings.Repeat("get, ", tripNamesShown), ", "), longRunCommands-tripNamesShown)
	if !strings.HasSuffix(r.fatal, want) {
		t.Fatalf("the failure %q does not end %q", r.fatal, want)
	}
	r = provoke(t, func(tb testing.TB) { trips.Expect(tb, 0, func() { sendGets(tb, process, longRunCommands) }) })
	want = fmt.Sprintf("counted %d round trips: %s; and %d more", longRunCommands, strings.TrimSuffix(strings.Repeat("get; ", tripsShown), "; "), longRunCommands-tripsShown)
	if !strings.HasSuffix(r.fatal, want) {
		t.Fatalf("the failure %q does not end %q", r.fatal, want)
	}
}

func TestExpectCountsOnlyWhatHappensWhileItRuns(t *testing.T) {
	t.Parallel()

	trips, hook := RoundTrips(t)
	process := hook.ProcessHook(answerCommand)
	sendGets(t, process, wrongTripCount)
	trips.Expect(t, loopCommands, func() { sendGets(t, process, loopCommands) })
	sendGets(t, process, wrongTripCount)
	trips.Expect(t, 0, func() {})
}

func TestExpectRefusesANegativeCountAndNoFunction(t *testing.T) {
	t.Parallel()

	trips, _ := RoundTrips(t)
	r := provoke(t, func(tb testing.TB) { trips.Expect(tb, -1, func() {}) })
	if !strings.Contains(r.fatal, "-1 round trips") {
		t.Fatalf("a negative count failed with %q; want the number it was given", r.fatal)
	}
	r = provoke(t, func(tb testing.TB) { trips.Expect(tb, 0, nil) })
	if !strings.Contains(r.fatal, "no function") {
		t.Fatalf("no function failed with %q; want that said", r.fatal)
	}
}

// One Counter serves every goroutine of a client, and -race reads the proof.
func TestManyGoroutinesAreCountedWhole(t *testing.T) {
	t.Parallel()

	trips, hook := RoundTrips(t)
	process := hook.ProcessHook(answerCommand)
	trips.Expect(t, tripWorkers*tripCommandsPerWorker, func() {
		var wg sync.WaitGroup
		for range tripWorkers {
			wg.Go(func() {
				ctx := context.Background()
				for range tripCommandsPerWorker {
					if err := process(ctx, redis.NewCmd(ctx, "get", "k")); err != nil {
						t.Error(err)
					}
				}
			})
		}
		wg.Wait()
	})
}

func TestRoundTripsRefusesToRunOutsideATestBinary(t *testing.T) {
	t.Parallel()

	l := real
	l.inTest = func() bool { return false }
	said, _ := panics(func() { l.roundTrips(t) }).(string)
	if !strings.Contains(said, "outside a test binary") {
		t.Fatalf("RoundTrips outside a test binary panicked with %q; want the refusal", said)
	}
}

// A real go-redis client, its connections the near ends of net.Pipes and the
// far ends a fake store: the hook is fed by what go-redis itself hands it.

// servePipedStore is the fake store: it reads commands the way a server does and
// answers them. HELLO is refused, so the client speaks RESP2; a command inside
// MULTI is QUEUED and EXEC answers one OK for each; the rest is OK, and a PING
// is PONG.
func servePipedStore(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	queued, inTransaction := 0, false
	for {
		args, err := readArray(r)
		if err != nil {
			return
		}
		reply := "+OK\r\n"
		switch name := strings.ToUpper(args[0]); {
		case name == "HELLO":
			reply = "-ERR unknown command 'HELLO'\r\n"
		case name == "MULTI":
			queued, inTransaction = 0, true
		case name == "EXEC":
			reply = "*" + strconv.Itoa(queued) + "\r\n" + strings.Repeat("+OK\r\n", queued)
			inTransaction = false
		case inTransaction:
			queued++
			reply = "+QUEUED\r\n"
		case name == "PING":
			reply = "+PONG\r\n"
		}
		if _, err := io.WriteString(c, reply); err != nil {
			return
		}
	}
}

// pipedClient is a go-redis client with the hooks given. It is not connected
// until its first command.
func pipedClient(t testing.TB, hooks ...redis.Hook) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{
		Addr: "in-process",
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			near, far := net.Pipe()
			go servePipedStore(far)
			return near, nil
		},
	})
	for _, h := range hooks {
		c.AddHook(h)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// connectClient opens the client's connection, so what is counted after it is the
// caller's own.
func connectClient(t testing.TB, c *redis.Client) {
	t.Helper()
	if err := c.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestAPipelineAndATransactionOfARealClientAreOneRoundTripEach(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	trips, hook := RoundTrips(t)
	c := pipedClient(t, hook)
	connectClient(t, c)
	trips.Expect(t, loopCommands, func() {
		for range loopCommands {
			if err := c.Set(ctx, "k", "v", 0).Err(); err != nil {
				t.Fatal(err)
			}
		}
	})
	fill := func(p redis.Pipeliner) error {
		for range loopCommands {
			p.Set(ctx, "k", "v", 0)
		}
		return nil
	}
	trips.Expect(t, 1, func() {
		if _, err := c.Pipelined(ctx, fill); err != nil {
			t.Fatal(err)
		}
	})
	trips.Expect(t, 1, func() {
		if _, err := c.TxPipelined(ctx, fill); err != nil {
			t.Fatal(err)
		}
	})
	// The transaction is named as one: its MULTI and EXEC are not commands of
	// the test's.
	r := provoke(t, func(tb testing.TB) {
		trips.Expect(tb, 0, func() {
			if _, err := c.TxPipelined(ctx, fill); err != nil {
				tb.Fatal(err)
			}
		})
	})
	if want := "counted 1 round trip: transaction(set, set, set)"; !strings.HasSuffix(r.fatal, want) {
		t.Fatalf("the failure %q does not end %q", r.fatal, want)
	}
}

func TestTheHandshakeOfAClientThatHasNotConnectedIsCounted(t *testing.T) {
	t.Parallel()

	trips, hook := RoundTrips(t)
	c := pipedClient(t, hook)
	r := provoke(t, func(tb testing.TB) { trips.Expect(tb, 1, func() { connectClient(tb, c) }) })
	if want := "hello; pipeline(client, client); ping"; !strings.HasSuffix(r.fatal, want) {
		t.Fatalf("the first PING of a client that had not connected failed with %q; want it to end with the handshake and the PING: %q", r.fatal, want)
	}
	// Connected, the same PING is one round trip.
	trips.Expect(t, 1, func() { connectClient(t, c) })
}

// go-redis runs the hook added first outermost, so the hook added last counts
// what a hook above it sends again.
func TestTheHookAddedLastCountsWhatAnEarlierHookSendsAgain(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fill := func(p redis.Pipeliner) error {
		for range loopCommands {
			p.Set(ctx, "k", "v", 0)
		}
		return nil
	}
	for name, c := range map[string]struct {
		countedLast bool
		want        int
	}{
		"added last":  {countedLast: true, want: 2},
		"added first": {countedLast: false, want: 1},
	} {
		trips, hook := RoundTrips(t)
		hooks := []redis.Hook{resendingHook{}, hook}
		if !c.countedLast {
			hooks = []redis.Hook{hook, resendingHook{}}
		}
		client := pipedClient(t, hooks...)
		connectClient(t, client)
		trips.Expect(t, c.want, func() {
			if _, err := client.Pipelined(ctx, fill); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		})
	}
}

// resendingHook is a hook that sends every pipeline again, as a loader that
// retries one does.
type resendingHook struct{}

func (resendingHook) DialHook(next redis.DialHook) redis.DialHook          { return next }
func (resendingHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }
func (resendingHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if err := next(ctx, cmds); err != nil {
			return err
		}
		return next(ctx, cmds)
	}
}

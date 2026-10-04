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
	"sync/atomic"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			require.NoError(t, err, err)
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
			require.NotEmpty(t, r.fatal, "%d commands were taken for %d round trips", loopCommands, wrong)
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
				require.NoError(t, err, err)
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
			require.NoError(t, err, err)
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
			require.ErrorIs(t, err, refused, "the command was answered %v; want what the client answered", err)
		}
		if err := hook.ProcessPipelineHook(func(context.Context, []redis.Cmder) error { return refused })(ctx, getBatch(ctx, loopCommands)); !errors.Is(err, refused) {
			require.ErrorIs(t, err, refused, "the pipeline was answered %v; want what the client answered", err)
		}
	})
	// A dial is not a round trip, and it is counted as none only if it happens
	// while Expect is counting.
	dial := func(context.Context, string, string) (net.Conn, error) { return nil, refused }
	trips.Expect(t, 0, func() {
		if _, err := hook.DialHook(dial)(ctx, "tcp", "in-process"); !errors.Is(err, refused) {
			require.ErrorIs(t, err, refused, "the dial was answered %v; want what the client answered", err)
		}
	})
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
			assert.NotEmpty(t, r.fatal, "%s: expected %d round trips, made %d, and the test passed", name, c.want, c.made)
			continue
		}
		for _, number := range []string{
			"expected " + countOf(c.want),
			"counted " + countOf(c.made),
		} {
			if !strings.Contains(r.fatal, number) {
				assert.Contains(t, r.fatal, number, "%s: the failure %q does not say %q", name, r.fatal, number)
			}
		}
	}
	// The whole text, once: the two numbers, and what was counted.
	trips, hook := RoundTrips(t)
	process := hook.ProcessHook(answerCommand)
	r := provoke(t, func(tb testing.TB) { trips.Expect(tb, wrongTripCount, func() { sendGets(tb, process, loopCommands) }) })
	if want := "testredis: expected 2 round trips, counted 3 round trips: get; get; get"; r.fatal != want {
		require.Equal(t, want, r.fatal, "the failure:\n got %q\nwant %q", r.fatal, want)
	}
	r = provoke(t, func(tb testing.TB) { trips.Expect(tb, 1, func() {}) })
	if want := "testredis: expected 1 round trip, counted 0 round trips"; r.fatal != want {
		require.Equal(t, want, r.fatal, "the failure:\n got %q\nwant %q", r.fatal, want)
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
		require.Equal(t, want, r.fatal, "the failure:\n got %q\nwant %q", r.fatal, want)
	}
	if strings.Contains(r.fatal, password) || strings.Contains(r.fatal, key) {
		require.Failf(t, "", "an argument is in the failure: %q", r.fatal)
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
				require.NoError(tb, err, err)
			}
		})
	})
	want := fmt.Sprintf("counted 1 round trip: pipeline(%s, and %d more)", strings.TrimSuffix(strings.Repeat("get, ", tripNamesShown), ", "), longRunCommands-tripNamesShown)
	if !strings.HasSuffix(r.fatal, want) {
		require.Failf(t, "", "the failure %q does not end %q", r.fatal, want)
	}
	r = provoke(t, func(tb testing.TB) { trips.Expect(tb, 0, func() { sendGets(tb, process, longRunCommands) }) })
	want = fmt.Sprintf("counted %d round trips: %s; and %d more", longRunCommands, strings.TrimSuffix(strings.Repeat("get; ", tripsShown), "; "), longRunCommands-tripsShown)
	if !strings.HasSuffix(r.fatal, want) {
		require.Failf(t, "", "the failure %q does not end %q", r.fatal, want)
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
		require.Contains(t, r.fatal, "-1 round trips", "a negative count failed with %q; want the number it was given", r.fatal)
	}
	r = provoke(t, func(tb testing.TB) { trips.Expect(tb, 0, nil) })
	if !strings.Contains(r.fatal, "no function") {
		require.Contains(t, r.fatal, "no function", "no function failed with %q; want that said", r.fatal)
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
						assert.NoError(t, err, err)
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
		require.Contains(t, said, "outside a test binary", "RoundTrips outside a test binary panicked with %q; want the refusal", said)
	}
}

// A real go-redis client, its connections the near ends of net.Pipes and the
// far ends a fake store: the hook is fed by what go-redis itself hands it.

// servePipedStore is the fake store: it reads commands the way a server does and
// answers them. HELLO is refused, so the client speaks RESP2; a command inside
// MULTI is QUEUED and EXEC answers one OK for each; the rest is OK, and a PING
// is PONG. A SET outside a transaction first calls gate, when there is one, so
// a test can hold every connection's command until it chooses.
//
// The fixture has a size limit: net.Pipe has no buffer, so the store blocks
// writing a reply while the client is still writing its pipeline, and a
// pipeline larger than the store's 4 KB read buffer can deadlock (measured: a
// pipeline of 150 small SETs is answered and one of 300 times out writing).
// The pipelines here hold a few commands. Do not reuse it to count a large
// pipeline: the hook's size-independence is shown with a fake processor, which
// has no pipe.
func servePipedStore(c net.Conn, gate func()) {
	// ignored: a fake store's connection ends when the client hangs up; the test's own assertions are the report
	defer func() { _ = c.Close() }()
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
		case name == "SET" && gate != nil:
			gate()
		}
		if _, err := io.WriteString(c, reply); err != nil {
			return
		}
	}
}

// pipedStore is a fake store a real client reaches over net.Pipes, with the
// number of connections opened to it.
type pipedStore struct {
	dials atomic.Int64
	gate  func() // called before a SET outside a transaction is answered; nil for none
	pool  int    // the client's pool size; 0 is go-redis's own
}

// client is a go-redis client with the hooks given. It is not connected until
// its first command, and every connection it opens is counted in dials.
func (s *pipedStore) client(t testing.TB, hooks ...redis.Hook) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{
		Addr:     "in-process",
		PoolSize: s.pool,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			s.dials.Add(1)
			near, far := net.Pipe()
			go servePipedStore(far, s.gate)
			return near, nil
		},
	})
	for _, h := range hooks {
		c.AddHook(h)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// pipedClient is a go-redis client on a fake store of its own, with the hooks
// given. It is not connected until its first command.
func pipedClient(t testing.TB, hooks ...redis.Hook) *redis.Client {
	t.Helper()
	return (&pipedStore{}).client(t, hooks...)
}

// connectClient opens the client's connection, so what is counted after it is the
// caller's own.
func connectClient(t testing.TB, c *redis.Client) {
	t.Helper()
	if err := c.Ping(context.Background()).Err(); err != nil {
		require.NoError(t, err, "ping: %v", err)
	}
}

// meeting holds each goroutine that arrives until all of them have, or until
// the test ends, so that every one of them is at the store at the same time.
type meeting struct {
	mu   sync.Mutex
	need int
	here int
	open chan struct{}
	once sync.Once
}

func newMeeting(t testing.TB, need int) *meeting {
	m := &meeting{need: need, open: make(chan struct{})}
	t.Cleanup(m.release)
	return m
}

func (m *meeting) release() { m.once.Do(func() { close(m.open) }) }

// arrive waits for the rest.
func (m *meeting) arrive() {
	m.mu.Lock()
	m.here++
	if m.here == m.need {
		m.release()
	}
	m.mu.Unlock()
	<-m.open
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
				require.NoError(t, err, err)
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
			require.NoError(t, err, err)
		}
	})
	trips.Expect(t, 1, func() {
		if _, err := c.TxPipelined(ctx, fill); err != nil {
			require.NoError(t, err, err)
		}
	})
	// The transaction is named as one: its MULTI and EXEC are not commands of
	// the test's.
	r := provoke(t, func(tb testing.TB) {
		trips.Expect(tb, 0, func() {
			if _, err := c.TxPipelined(ctx, fill); err != nil {
				require.NoError(tb, err, err)
			}
		})
	})
	if want := "counted 1 round trip: transaction(set, set, set)"; !strings.HasSuffix(r.fatal, want) {
		require.Failf(t, "", "the failure %q does not end %q", r.fatal, want)
	}
}

// A connection's setup is not a round trip the caller made. go-redis runs it
// through the hooks inside the caller's first command, so the hook tells it
// from the caller's commands by who sent it, never by name.

func TestAColdClientsFirstCommandIsOneRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := &pipedStore{}
	trips, hook := RoundTrips(t)
	c := store.client(t, hook)
	trips.Expect(t, 1, func() { connectClient(t, c) })
	// The count did cover the dial and the handshake: they happened inside it.
	if dialed := store.dials.Load(); dialed != 1 {
		require.EqualValues(t, 1, dialed, "the first PING of a client that had not connected opened %d connections; want 1, so that the setup ran while it was counted", dialed)
	}
	// Connected, the same PING is one round trip as well.
	trips.Expect(t, 1, func() { connectClient(t, c) })

	// A cold client's first work may be a pipeline: one round trip as well.
	cold := store.client(t, hook)
	trips.Expect(t, 1, func() {
		if _, err := cold.Pipelined(ctx, func(p redis.Pipeliner) error {
			for range loopCommands {
				p.Set(ctx, "k", "v", 0)
			}
			return nil
		}); err != nil {
			require.NoError(t, err, err)
		}
	})
	if dialed := store.dials.Load(); dialed != 2 {
		require.EqualValues(t, 2, dialed, "a second cold client brought the connections to %d; want 2", dialed)
	}
}

func TestTheCallersOwnHelloAndClientAreRoundTripsWhateverTheirNames(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	trips, hook := RoundTrips(t)
	c := pipedClient(t, hook)
	// On a cold client the handshake sends a HELLO and a CLIENT of its own, and
	// the caller's command of the same name is the one counted.
	said := provoke(t, func(tb testing.TB) {
		trips.Expect(tb, 0, func() { _ = c.Do(ctx, "hello", "2").Err() })
	})
	if want := "testredis: expected 0 round trips, counted 1 round trip: hello"; said.fatal != want {
		require.Equal(t, want, said.fatal, "the caller's own HELLO on a cold client:\n got %q\nwant %q", said.fatal, want)
	}
	trips.Expect(t, 1, func() { _ = c.Do(ctx, "hello", "2").Err() })
	said = provoke(t, func(tb testing.TB) {
		trips.Expect(tb, 0, func() {
			if err := c.Do(ctx, "client", "setname", "a-name").Err(); err != nil {
				require.NoError(tb, err, err)
			}
		})
	})
	if want := "testredis: expected 0 round trips, counted 1 round trip: client"; said.fatal != want {
		require.Equal(t, want, said.fatal, "the caller's own CLIENT SETNAME:\n got %q\nwant %q", said.fatal, want)
	}
	trips.Expect(t, 1, func() {
		if err := c.Do(ctx, "client", "setname", "a-name").Err(); err != nil {
			require.NoError(t, err, err)
		}
	})
}

// A crowd on one client, every member at the store at once: the pool must open
// a connection for each, each connection shakes hands, and the count is the
// crowd's own commands and nothing else. The meeting makes the handshakes
// certain; left to the scheduler they came in any number.
func TestACrowdOnOneClientIsCountedExactlyWhateverTheHandshakes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const crowd = 100
	meet := newMeeting(t, crowd)
	store := &pipedStore{gate: meet.arrive, pool: crowd}
	trips, hook := RoundTrips(t)
	c := store.client(t, hook)
	connectClient(t, c)
	before := store.dials.Load()
	trips.Expect(t, crowd, func() {
		var wg sync.WaitGroup
		for range crowd {
			wg.Go(func() {
				if err := c.Set(ctx, "k", "v", 0).Err(); err != nil {
					assert.NoError(t, err, err)
				}
			})
		}
		wg.Wait()
	})
	// Every member was at the store at once, so all but the one connection the
	// client had dialed a connection of their own, inside the count.
	if opened := store.dials.Load() - before; opened < crowd-1 {
		require.GreaterOrEqual(t, opened, int64(crowd-1), "the crowd opened %d connections; want at least %d, so that their handshakes were sent while they were counted", opened, crowd-1)
	}
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
				require.NoError(t, err, "%s: %v", name, err)
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

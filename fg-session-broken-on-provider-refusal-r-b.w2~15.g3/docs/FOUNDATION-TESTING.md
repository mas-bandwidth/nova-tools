# FOUNDATION-TESTING.md: the two tiers, the class tests and the four store helpers

How to run each tier of the tests, and how a test uses the four helpers of
`internal/testredis` that check what a change did to a store: `Image` and `Diff`
(what changed), `OnlyFCALL` (only FCALL writes), `RoundTrips` (how many times the
client waited) and `Far` (a store at a distance). What the two tiers are, and how
the class tests' allowlists are regenerated, is in [TESTING.md](TESTING.md); how
`nova-ci local` and the container run work is in [../TESTING.md](../TESTING.md).
This page does not repeat them.

## Run the unit tier

A unit test starts no server and dials nothing. Run the packages you touched, the
way CI runs them:

```
nova-ci local --base origin/sprint/foundation
make test PKGS=./internal/testredis/
```

`nova-ci local` selects the packages from your committed diff, prints one `PKG`
line for each, and sums the run up:

```
nova-ci local: packages=2 seconds=7.8s red=0 make-exit=0
```

`make test` runs the packages you name and then the time budget. Its output is
`go test -json`; the lines that carry the verdict are the `CI-` ones:

```
make test PKGS=./internal/testredis/ 2>&1 | grep -E '^(CI-SLOW|--- FAIL|FAIL)'
CI-SLOW OK packages=1 slowest=github.com/mas-bandwidth/nova-tools/internal/testredis:0.2s
```

Seconds differ from machine to machine; `OK` is the verdict.

## Run the functional tier in the container

A test that needs a real `redis-server` lives in a `_test.go` whose first line is
`//go:build functional`, and it runs inside one container per run, never bare.
Name the packages:

```
make test-functional-container PKGS="./internal/testredis/ ./tools/fardelay/" FUNCTIONAL_FLAGS=--fresh-gocache FUNCTIONAL_DEADLINE=15m
```

The run prints the packages it selected, one line for each package that ran, and
ends with the container's own line (the run id differs on every run):

```
functional: ./internal/testredis/ ./tools/fardelay/
ok  	github.com/mas-bandwidth/nova-tools/internal/testredis	1.560s
ok  	github.com/mas-bandwidth/nova-tools/tools/fardelay	1.848s
FUNCTIONAL RUN run=<id> ended=finished exit=0 wall=2.5s build=0.0s modcache=0.4s total=3.4s containers_left=0
```

`exit=0` and `containers_left=0` are the verdict. The steps of the run (the reaper, the image, the caches, the limits) are in
[../TESTING.md](../TESTING.md).

## Run the class tests

The class tests read the repository itself: every test opens with
`t.Parallel()`, a file that starts a `redis-server` carries the functional tag,
no unit test waits on the wall clock. They live in `internal/ci`, and the docs
they read in `internal/docs`. Run both packages, or one test:

```
go test -count=1 -timeout 600s ./internal/ci/ ./internal/docs/
go test -count=1 -timeout 600s -run TestEveryTestOpensWithTParallel ./internal/ci/
```

The class tests a test that uses the helpers below meets:

| Test | What it holds |
|---|---|
| `TestEveryTestOpensWithTParallel` | `t.Parallel()` is the first statement of every test |
| `TestRedisBackedTestsCarryTheFunctionalTag` | a file that starts a `redis-server` builds only under `-tags functional` |
| `TestNoUnitTestWaitsOnTheWallClock` | a unit test does not sleep or wait on a timer |
| `TestNoFixedWaitsOnTheCIPath` | no test asserts on elapsed time |
| `TestNoTestAssertsAWallClockBoundUnderTenSeconds` | no test leans on a duration under ten seconds |
| `TestNoRealNetworkHostsOnTheCIPath` | no test names a real host |

[SPEC-CI.md](SPEC-CI.md), under "The class tests", says what each one reads and
what it refuses. A failure names the file and the remedy.

## The helpers

All four are in `internal/testredis`. `Image`, the FCALL-only hook and the round-trip counter panic outside a test binary; `Diff` is a plain function over two images.
`testredis.Start(t)` runs a `redis-server` of the test's alone on a loopback port
and returns its `host:port`; the test's cleanup kills it. A missing
`redis-server` skips the test on a laptop and fails it under `NOVA_CI=1`. The
tests below that reach a store call `Start`, so they are functional tests. The two
hooks also work against a fake, in a unit test.

### E6: `Image` and `Diff`, the whole-store image

```go
func Image(ctx context.Context, c redis.UniversalClient, prefix string) (map[string]Entry, error)
func Diff(before, after map[string]Entry) []string
```

`Image` reads every key under `prefix` and returns, for each, its type and a sum
of its content and its expiry times (`Entry{Type, Sum}`). `Diff` names what
changed between two images, one line per key in key order: `+key` added, `-key`
removed, `~key` changed. Two equal images give an empty, non-nil slice. A test
that must show a step wrote nothing takes an image before it and one after it and
expects `Diff` to be empty; a test that must show it wrote exactly one key
expects exactly that line.

- The prefix is a literal, so `a[1]:` matches keys that begin with those five
  characters. The empty prefix is every key of the database.
- The keys are found by `SCAN` and read in pipelines, so an image of a large
  store is a few round trips and never a long call. A `ClusterClient` or a `Ring`
  is refused: `SCAN` reads one node.
- A read changes no sum. A hash, set or sorted set rewritten in another order
  with the same content has the same sum. A write of an expiry alone (the key's,
  or a hash field's) changes it.
- Take both images through a client with no hook (see below): an image runs
  commands, and a counting hook would count them.

### E7: `OnlyFCALL`, the FCALL-only client hook

```go
func OnlyFCALL(t testing.TB) redis.Hook
```

`client.AddHook(testredis.OnlyFCALL(t))` makes the client fail the test on any
write command that is not `FCALL`: `SET`, `HSET`, `XADD`, `DEL`, `EVAL`,
`EVALSHA` and the rest of the list in `writeGroups` (`onlyfcall.go`). The write is
not sent. The caller gets an error and the test gets one line:

```
testredis: OnlyFCALL: SET is a write command other than FCALL; it was not sent
```

Reads, `FCALL`, `FCALL_RO`, `MULTI` and `EXEC` pass. A pipeline is judged whole:
one write in it names every write in it and none of it is sent. Loading the
function library is a write (`FUNCTION LOAD`), so load it through another client
and give the code under test the client that has the hook.

### E8: `RoundTrips`, the round-trip counter hook

```go
func RoundTrips(t testing.TB) (*Counter, redis.Hook)
func (c *Counter) Expect(t testing.TB, n int, do func())
```

A round trip is one command, one pipeline or one transaction: what the client
sends and waits for, however many commands it carries. A pipeline of a hundred
commands is one. `Expect` runs `do` and fails the test unless the client made
exactly `n`, and names what it counted:

```
testredis: expected 2 round trips, counted 1 round trip: fcall
```

A connection's own handshake is not counted, so a cold client's first command is
one. Add this hook last: go-redis runs the hook added last nearest the
connection, and a hook added after this one that sends again would not be
counted. Only command names are written down, never keys or values.

Against a fake, a unit test hands the hook the processor and no store is
started:

```go
ctx := context.Background()
trips, hook := testredis.RoundTrips(t)
process := hook.ProcessHook(func(context.Context, redis.Cmder) error { return nil }) // the store, faked
trips.Expect(t, 3, func() {
	for range 3 {
		_ = process(ctx, redis.NewCmd(ctx, "get", "k"))
	}
})
```

### The three together, against a real store

This is a functional test, in a file that starts with `//go:build functional`:

```go
const library = `#!lua name=example
redis.register_function('put', function(keys, args)
  return redis.call('SET', keys[1], args[1])
end)`

func TestAVerbWritesOneKeyInOneRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	addr := testredis.Start(t)

	// The setup client has no hook: loading the library is a write.
	setup := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = setup.Close() })
	if _, err := setup.FunctionLoad(ctx, library).Result(); err != nil {
		t.Fatal(err)
	}

	// The client under test refuses every write but FCALL and counts round trips.
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	client.AddHook(testredis.OnlyFCALL(t))
	trips, hook := testredis.RoundTrips(t)
	client.AddHook(hook) // last: nearest the connection

	before, err := testredis.Image(ctx, setup, "app:")
	if err != nil {
		t.Fatal(err)
	}
	trips.Expect(t, 1, func() {
		if err := client.FCall(ctx, "put", []string{"app:k"}, "v").Err(); err != nil {
			t.Fatal(err)
		}
	})
	after, err := testredis.Image(ctx, setup, "app:")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := testredis.Diff(before, after), []string{"+app:k"}; !slices.Equal(got, want) {
		t.Fatalf("the verb changed %v, want %v", got, want)
	}
}
```

### E9: `Far` and `tools/fardelay`, a store at a distance

```go
func Far(t testing.TB, target string, delay time.Duration) (addr string)
func FarLink(t testing.TB, target string, delay time.Duration) *delayproxy.Proxy
```

`Far` listens on the loopback, forwards every connection to `target` (the address
`Start` returned), and holds each write of the client back by `delay` before it
forwards it. The answer is not held, so one command and its reply cost `delay`
once, and a `delay` of 128ms is a store 128ms away by round trip. A pipeline the
client writes in one go pays it once; three commands sent one after the other,
each waiting for its reply, pay it three times. The proxy stops with the test.
The target must be a loopback address the test owns: `Far` is a way to make a
local store far, never a way out of the machine.

Assert the ledger, not a clock. `FarLink` returns the proxy: `Addr()` is the
address to dial, `Writes()` counts the writes it held and forwarded, and
`Shortest()` is the least time any write is held. A test that measures elapsed time
asserts on its machine's load, and `TestNoFixedWaitsOnTheCIPath` refuses it.

```go
func TestAPipelineOfAHundredPaysTheDelayOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	link := testredis.FarLink(t, testredis.Start(t), 100*time.Millisecond)
	client := redis.NewClient(&redis.Options{Addr: link.Addr(), PoolSize: 1})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(ctx).Err(); err != nil { // the connection and its handshake are paid here
		t.Fatal(err)
	}

	before := link.Writes()
	pipe := client.Pipeline()
	for range 100 {
		pipe.Ping(ctx)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if got := link.Writes() - before; got != 1 {
		t.Fatalf("a pipeline of 100 was %d writes at the proxy, want 1", got)
	}
	if got := link.Shortest(); got < 100*time.Millisecond {
		t.Fatalf("a write was held %v, want at least the delay", got)
	}
}
```

`tools/fardelay` is the same proxy as a process, for a person driving a tool by
hand against a far store. It takes `--target` and `--delay`, and `--listen`, which
must be on the loopback (default `127.0.0.1:0`, a port the kernel picks):

```
go run ./tools/fardelay -h
go run ./tools/fardelay --target 127.0.0.1:6379 --delay 64ms
LISTEN OK addr=127.0.0.1:60299 target=127.0.0.1:6379 delay=64ms
```

Point the client at the `addr` of the `LISTEN` line. The port differs on each
run. The target is dialled once for each client connection, so it may start after
`fardelay` does. It runs until interrupted, then prints
`STOP OK writes=<n> shortest=<duration>`. It exits 0 when stopped as asked and 2
when it could not run.

The wall-clock windows of the proxy (one `PING` takes the delay, a pipeline takes
it once, three commands take it three times) are asserted in the container by
`tools/fardelay/distance_functional_test.go`, with a slack on the upper bound for
a busy machine.

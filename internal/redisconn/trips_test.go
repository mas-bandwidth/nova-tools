package redisconn

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"
)

// opened is a connection to a fake store that accepts everything, closed
// with the test.
func opened(t *testing.T, reply func(int, []string) string) (*Conn, *fakeStore) {
	t.Helper()
	store := newFakeStore(t, reply)
	conn, err := open(context.Background(), Options{Addr: storeAddr}, nothing, store.dial)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	store.commands()
	return conn, store
}

// TestTripsCountWhatTheCallerSends: a single command is one trip, a pipeline
// and a transaction are one each whatever they carry, a command the store
// refuses is one, and a pipeline with nothing in it is none. A command is
// the caller's by who sent it and not by its name: the caller's own CLIENT
// and SELECT count.
func TestTripsCountWhatTheCallerSends(t *testing.T) {
	t.Parallel()
	conn, store := opened(t, accepting)
	client := conn.Client()
	trips := CountTrips(client)
	if n := trips.N(); n != 0 {
		t.Fatalf("a new counter reads %d", n)
	}
	ctx := context.Background()
	for _, c := range []struct {
		name string
		work func() error
		want int64
		sent []string
	}{
		{"a command", func() error { return client.Set(ctx, "k", "v", 0).Err() }, 1, []string{"1: set k v"}},
		{"a read", func() error { return client.Get(ctx, "k").Err() }, 1, []string{"1: get k"}},
		{"the caller's own CLIENT", func() error { return client.Do(ctx, "CLIENT", "ID").Err() }, 1, []string{"1: CLIENT ID"}},
		{"the caller's own SELECT", func() error { return client.Do(ctx, "SELECT", "0").Err() }, 1, []string{"1: SELECT 0"}},
		{"the caller's own PING", func() error { return client.Ping(ctx).Err() }, 1, []string{"1: ping"}},
		{"a command the store refuses", func() error {
			if err := client.LPush(ctx, "k", "v").Err(); err == nil {
				return fmt.Errorf("the store took it")
			}
			return nil
		}, 1, []string{"1: lpush k v"}},
		{"a pipeline of three", func() error {
			pipe := client.Pipeline()
			pipe.Get(ctx, "a")
			pipe.Get(ctx, "b")
			pipe.Set(ctx, "c", "v", 0)
			_, err := pipe.Exec(ctx)
			return err
		}, 1, []string{"1: get a", "1: get b", "1: set c v"}},
		{"a transaction", func() error {
			_, err := client.TxPipelined(ctx, func(p redis.Pipeliner) error { return p.Set(ctx, "k", "v", 0).Err() })
			return err
		}, 1, []string{"1: multi", "1: set k v", "1: exec"}},
		{"a pipeline with nothing in it", func() error {
			_, err := client.Pipeline().Exec(ctx)
			return err
		}, 0, nil},
		{"three commands", func() error {
			for i := 0; i < 3; i++ {
				if err := client.Set(ctx, "k", "v", 0).Err(); err != nil {
					return err
				}
			}
			return nil
		}, 3, []string{"1: set k v", "1: set k v", "1: set k v"}},
	} {
		before := trips.N()
		if err := c.work(); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if got := trips.N() - before; got != c.want {
			t.Errorf("%s took %d trips; want %d", c.name, got, c.want)
		}
		if got := store.commands(); !reflect.DeepEqual(got, c.sent) {
			t.Errorf("%s: the store received %q; want %q", c.name, got, c.sent)
		}
	}
	if got := trips.ByLabel(); len(got) != 0 {
		t.Errorf("trips made under no label were counted under %v", got)
	}
}

// TestTripsDoNotCountUnsentCommands: a command counts when it was written,
// which is what the fleet pays. One the caller cancelled, and one on a
// client that is already closed, return before any byte is written: the
// store sees nothing and Total does not move, on the caller's counter or
// on the connection's own. A command the store receives still counts, and
// so does one the store refuses, because that one was sent.
func TestTripsDoNotCountUnsentCommands(t *testing.T) {
	t.Parallel()
	conn, store := opened(t, accepting)
	client := conn.Client()
	trips := CountTrips(client)
	own := conn.Trips()
	ctx := context.Background()

	if err := client.Set(ctx, "k", "v", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.LPush(ctx, "k", "v").Err(); err == nil {
		t.Fatal("the store took a push onto a string")
	}
	if trips.Total() != 2 || trips.N() != 2 || own.N() != 2 {
		t.Fatalf("commands the store received: counter %s (N %d), the connection's N %d; want 2", trips, trips.N(), own.N())
	}
	if got := store.commands(); !reflect.DeepEqual(got, []string{"1: set k v", "1: lpush k v"}) {
		t.Fatalf("the store received %q; want the set and the push", got)
	}

	base, ownBase := trips.Total(), own.Total()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := client.Set(cancelled, "k", "v", 0).Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled command: %v", err)
	}
	pipe := client.Pipeline()
	pipe.Get(cancelled, "a")
	if _, err := pipe.Exec(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled pipeline: %v", err)
	}
	if got := store.commands(); len(got) != 0 {
		t.Fatalf("the store received %q from a call that was cancelled", got)
	}
	if trips.Total() != base || own.Total() != ownBase || trips.Setup() != 0 {
		t.Fatalf("cancelled calls counted: %s, connection %s; want totals %d and %d", trips, own, base, ownBase)
	}

	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "k", "v", 0).Err(); !errors.Is(err, redis.ErrClosed) {
		t.Fatalf("a command on a closed client: %v", err)
	}
	pipe = client.Pipeline()
	pipe.Get(ctx, "a")
	if _, err := pipe.Exec(ctx); !errors.Is(err, redis.ErrClosed) {
		t.Fatalf("a pipeline on a closed client: %v", err)
	}
	if trips.Total() != base || own.Total() != ownBase {
		t.Fatalf("closed-client calls counted: %s, connection %s; want totals %d and %d", trips, own, base, ownBase)
	}
}

// TestTripsDoNotCountAConnectionsSetup: go-redis shakes hands inside the
// first command that uses a connection. Whatever the handshake sends, the
// command is one trip. The client here is go-redis's own, as it comes, so
// the handshake is the longest there is: HELLO, CLIENT MAINT_NOTIFICATIONS
// and a pipeline of CLIENT SETINFO.
func TestTripsDoNotCountAConnectionsSetup(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t, accepting)
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379", Dialer: store.dial})
	t.Cleanup(func() { _ = client.Close() })
	trips := CountTrips(client)
	ctx := WithTripLabel(context.Background(), "first")
	if err := client.Set(ctx, "k", "v", 0).Err(); err != nil {
		t.Fatal(err)
	}
	sent := store.commands()
	if len(sent) < 3 || sent[0] != "1: hello 3" || sent[len(sent)-1] != "1: set k v" {
		t.Fatalf("the store received %q; want a handshake of several commands and then the command", sent)
	}
	if trips.N() != 1 || trips.Of("first") != 1 {
		t.Errorf("a command that set its connection up with %d commands took %d trips, %d under its label; want 1 and 1", len(sent)-1, trips.N(), trips.Of("first"))
	}
	pipe := client.Pipeline()
	pipe.Get(ctx, "a")
	pipe.Get(ctx, "b")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if trips.N() != 2 {
		t.Errorf("a command and a pipeline took %d trips; want 2", trips.N())
	}
}

// TestTripsDoNotCountTheSetupOfAConnectionDialedLater: the store drops the
// connection under a command, which is one trip; the next command dials,
// shakes hands and runs, which is one trip, in a pipeline as in a command.
func TestTripsDoNotCountTheSetupOfAConnectionDialedLater(t *testing.T) {
	t.Parallel()
	conn, store := opened(t, func(conn int, cmd []string) string {
		if cmd[0] == "incr" {
			return hangUp
		}
		return accepting(conn, cmd)
	})
	trips := CountTrips(conn.Client())
	ctx := context.Background()
	if err := conn.Client().Incr(ctx, "n").Err(); Classify(err) != Unconfirmed {
		t.Fatalf("the command the drop met = %v", err)
	}
	if trips.N() != 1 {
		t.Errorf("the command the drop met took %d trips; want 1", trips.N())
	}
	pipe := conn.Client().Pipeline()
	pipe.Get(ctx, "a")
	pipe.Get(ctx, "b")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if trips.N() != 2 {
		t.Errorf("after the pipeline that dialed again: %d trips; want 2", trips.N())
	}
	want := []string{"1: incr n", "2: hello 3", "2: get a", "2: get b"}
	if got := store.commands(); !reflect.DeepEqual(got, want) {
		t.Errorf("the store received %q; want %q", got, want)
	}
	if trips.Setup() != 1 || trips.Total() != 3 {
		t.Errorf("the setup of the connection dialed again: setup %d, total %d; want 1 and 3", trips.Setup(), trips.Total())
	}
}

// TestAVerbCostsItsHandshakeAndOneTrip: what a verb pays on the wire, on the
// connection's own counter (Conn.Trips): Open's handshake is Setup, one
// round trip when the store accepts HELLO, and the verb's one pipeline is
// N, one, so the receipt says trips=2 setup=1. A store older than HELLO
// makes the probe travel, and a login it takes by AUTH is one more; each
// is a round trip of the handshake, and each is counted in Setup.
func TestAVerbCostsItsHandshakeAndOneTrip(t *testing.T) {
	t.Parallel()
	older := func(conn int, cmd []string) string {
		if cmd[0] == "hello" {
			return "-ERR unknown command 'hello'\r\n"
		}
		return accepting(conn, cmd)
	}
	for _, c := range []struct {
		name  string
		reply func(int, []string) string
		env   map[string]string
		o     Options
		setup int64
		wire  int // what the store received during Open
	}{
		{"a store that accepts HELLO", accepting, nil, Options{Addr: storeAddr}, 1, 1},
		{"a store that accepts HELLO, with a login", accepting, map[string]string{"PW": "s3cret"}, Options{Addr: storeAddr, User: "bench", PasswordEnv: "PW"}, 1, 1},
		{"a store older than HELLO", older, nil, Options{Addr: storeAddr}, 2, 2},
		{"a store older than HELLO, with a login", older, map[string]string{"PW": "s3cret"}, Options{Addr: storeAddr, User: "bench", PasswordEnv: "PW"}, 3, 3},
	} {
		store := newFakeStore(t, c.reply)
		conn, err := open(context.Background(), c.o, environment(c.env), store.dial)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := store.commands(); len(got) != c.wire {
			t.Errorf("%s: Open sent %q; want %d commands", c.name, got, c.wire)
		}
		trips := conn.Trips()
		if trips.N() != 0 || trips.Setup() != c.setup {
			t.Errorf("%s: after Open, %d trips and %d of setup; want 0 and %d", c.name, trips.N(), trips.Setup(), c.setup)
		}
		pipe := conn.Client().Pipeline()
		pipe.Get(context.Background(), "a")
		pipe.Get(context.Background(), "b")
		if _, err := pipe.Exec(context.Background()); err != nil {
			t.Fatal(err)
		}
		want := "trips=" + strconv.FormatInt(1+c.setup, 10) + " setup=" + strconv.FormatInt(c.setup, 10)
		if trips.N() != 1 || trips.Total() != 1+c.setup || trips.String() != want || fmt.Sprint(trips) != want {
			t.Errorf("%s: the verb reads %d, total %d, %q; want 1, %d, %q", c.name, trips.N(), trips.Total(), trips.String(), 1+c.setup, want)
		}
		_ = conn.Close()
	}
}

// TestTripLabels: a trip is counted under the label its context carries, and
// under a context derived from that one; the counts by label are the
// caller's own copy.
func TestTripLabels(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if got := TripLabel(ctx); got != "" {
		t.Errorf("a context with no label reads %q", got)
	}
	read := WithTripLabel(ctx, "read")
	type key struct{}
	derived, cancel := context.WithCancel(context.WithValue(read, key{}, 1))
	defer cancel()
	if TripLabel(read) != "read" || TripLabel(derived) != "read" {
		t.Errorf("labels %q and %q; want read and read", TripLabel(read), TripLabel(derived))
	}
	if got := TripLabel(WithTripLabel(read, "write")); got != "write" {
		t.Errorf("a label over a label reads %q; want the newer", got)
	}

	conn, _ := opened(t, accepting)
	client := conn.Client()
	trips := CountTrips(client)
	for _, c := range []context.Context{ctx, read, read, derived, WithTripLabel(ctx, "write"), WithTripLabel(read, "")} {
		if err := client.Set(c, "k", "v", 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	pipe := client.Pipeline()
	pipe.Get(read, "a")
	if _, err := pipe.Exec(WithTripLabel(ctx, "batch")); err != nil {
		t.Fatal(err)
	}
	if trips.N() != 7 {
		t.Errorf("%d trips; want 7", trips.N())
	}
	want := map[string]int64{"read": 3, "write": 1, "batch": 1}
	got := trips.ByLabel()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("by label %v; want %v", got, want)
	}
	for label, n := range map[string]int64{"read": 3, "write": 1, "batch": 1, "": 0, "absent": 0} {
		if trips.Of(label) != n {
			t.Errorf("Of(%q) = %d; want %d", label, trips.Of(label), n)
		}
	}
	got["read"] = 99
	got["new"] = 1
	if !reflect.DeepEqual(trips.ByLabel(), want) {
		t.Errorf("a change to the map ByLabel returned changed the counter: %v", trips.ByLabel())
	}
}

// TestTripsOfNothing: a nil counter has counted nothing.
func TestTripsOfNothing(t *testing.T) {
	t.Parallel()
	var trips *Trips
	if trips.N() != 0 || trips.Of("read") != 0 || trips.Setup() != 0 || trips.Total() != 0 || trips.String() != "trips=0 setup=0" {
		t.Errorf("a nil counter reads %d, %d, %d, %d, %q", trips.N(), trips.Of("read"), trips.Setup(), trips.Total(), trips.String())
	}
	if got := trips.ByLabel(); got == nil || len(got) != 0 {
		t.Errorf("a nil counter's labels are %#v; want an empty map", got)
	}
}

// TestTwoCountersEachCount: a second counter on the same client counts on
// its own, from when it was attached.
func TestTwoCountersEachCount(t *testing.T) {
	t.Parallel()
	conn, _ := opened(t, accepting)
	client := conn.Client()
	ctx := WithTripLabel(context.Background(), "read")
	first := CountTrips(client)
	if err := client.Get(ctx, "k").Err(); err != nil {
		t.Fatal(err)
	}
	second := CountTrips(client)
	for i := 0; i < 2; i++ {
		if err := client.Get(ctx, "k").Err(); err != nil {
			t.Fatal(err)
		}
	}
	if first.N() != 3 || second.N() != 2 || first.Of("read") != 3 || second.Of("read") != 2 {
		t.Errorf("the counters read %d (%d) and %d (%d); want 3 and 2", first.N(), first.Of("read"), second.N(), second.Of("read"))
	}
}

// TestTripsFromManyGoroutines: the count holds when many goroutines use the
// client at once, each over a connection of its own.
func TestTripsFromManyGoroutines(t *testing.T) {
	t.Parallel()
	conn, store := opened(t, accepting)
	client := conn.Client()
	trips := CountTrips(client)
	const workers, each = 8, 25
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := WithTripLabel(context.Background(), fmt.Sprint("worker-", w))
			for i := 0; i < each; i++ {
				if err := client.Set(ctx, "k", "v", 0).Err(); err != nil {
					errs <- err
					return
				}
				_ = trips.N() + trips.Of("worker-0") + int64(len(trips.ByLabel()))
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if trips.N() != workers*each {
		t.Errorf("%d trips; want %d", trips.N(), workers*each)
	}
	for w := 0; w < workers; w++ {
		if n := trips.Of(fmt.Sprint("worker-", w)); n != each {
			t.Errorf("worker %d: %d trips; want %d", w, n, each)
		}
	}
	if faults := store.faults(); len(faults) != 0 {
		t.Errorf("%q", faults)
	}
}

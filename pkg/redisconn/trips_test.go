package redisconn

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// opened is a connection to a fake store that accepts everything, closed
// with the test.
func opened(t *testing.T, reply func(int, []string) string) (*Conn, *fakeStore) {
	t.Helper()
	store := newFakeStore(t, reply)
	conn, err := open(context.Background(), Options{Addr: storeAddr}, nothing, store.dial)
	if err != nil {
		require.NoError(t, err, err)
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
		require.Zero(t, n, "a new counter reads %d", n)
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
			assert.NoError(t, err, "%s: %v", c.name, err)
		}
		if got := trips.N() - before; got != c.want {
			assert.EqualValues(t, c.want, got, "%s took %d trips; want %d", c.name, got, c.want)
		}
		if got := store.commands(); !reflect.DeepEqual(got, c.sent) {
			assert.Equal(t, c.sent, got, "%s: the store received %q; want %q", c.name, got, c.sent)
		}
	}
	if got := trips.ByLabel(); len(got) != 0 {
		assert.Len(t, got, 0, "trips made under no label were counted under %v", got)
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
	ctx := context.Background()
	if err := client.Set(ctx, "k", "v", 0).Err(); err != nil {
		require.NoError(t, err, err)
	}
	sent := store.commands()
	if len(sent) < 3 || sent[0] != "1: hello 3" || sent[len(sent)-1] != "1: set k v" {
		require.FailNowf(t, "", "the store received %q; want a handshake of several commands and then the command", sent)
	}
	if trips.N() != 1 {
		assert.EqualValues(t, 1, trips.N(), "a command that set its connection up with %d commands took %d trips; want 1", len(sent)-1, trips.N())
	}
	pipe := client.Pipeline()
	pipe.Get(ctx, "a")
	pipe.Get(ctx, "b")
	if _, err := pipe.Exec(ctx); err != nil {
		require.NoError(t, err, err)
	}
	if trips.N() != 2 {
		assert.EqualValues(t, 2, trips.N(), "a command and a pipeline took %d trips; want 2", trips.N())
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
	if err := conn.Client().Incr(ctx, "n").Err(); Classify(err) != Unreachable {
		require.EqualValues(t, Unreachable, Classify(err), "the command the drop met = %v", err)
	}
	if trips.N() != 1 {
		assert.EqualValues(t, 1, trips.N(), "the command the drop met took %d trips; want 1", trips.N())
	}
	pipe := conn.Client().Pipeline()
	pipe.Get(ctx, "a")
	pipe.Get(ctx, "b")
	if _, err := pipe.Exec(ctx); err != nil {
		require.NoError(t, err, err)
	}
	if trips.N() != 2 {
		assert.EqualValues(t, 2, trips.N(), "after the pipeline that dialed again: %d trips; want 2", trips.N())
	}
	want := []string{"1: incr n", "2: hello 3", "2: get a", "2: get b"}
	if got := store.commands(); !reflect.DeepEqual(got, want) {
		assert.Equal(t, want, got, "the store received %q; want %q", got, want)
	}
}

// TestTripsOfNothing: a nil counter has counted nothing.
func TestTripsOfNothing(t *testing.T) {
	t.Parallel()
	var trips *Trips
	if trips.N() != 0 || trips.Of("read") != 0 {
		assert.Failf(t, "", "a nil counter reads %d and %d", trips.N(), trips.Of("read"))
	}
	if got := trips.ByLabel(); got == nil || len(got) != 0 {
		assert.Failf(t, "", "a nil counter's labels are %#v; want an empty map", got)
	}
}

// TestTwoCountersEachCount: a second counter on the same client counts on
// its own, from when it was attached.
func TestTwoCountersEachCount(t *testing.T) {
	t.Parallel()
	conn, _ := opened(t, accepting)
	client := conn.Client()
	ctx := context.Background()
	first := CountTrips(client)
	if err := client.Get(ctx, "k").Err(); err != nil {
		require.NoError(t, err, err)
	}
	second := CountTrips(client)
	for i := 0; i < 2; i++ {
		if err := client.Get(ctx, "k").Err(); err != nil {
			require.NoError(t, err, err)
		}
	}
	if first.N() != 3 || second.N() != 2 {
		assert.Failf(t, "", "the counters read %d and %d; want 3 and 2", first.N(), second.N())
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
			ctx := context.Background()
			for i := 0; i < each; i++ {
				if err := client.Set(ctx, "k", "v", 0).Err(); err != nil {
					errs <- err
					return
				}
				_ = trips.N() + int64(len(trips.ByLabel()))
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		assert.Fail(t, fmt.Sprint(err))
	}
	if trips.N() != workers*each {
		assert.EqualValues(t, workers*each, trips.N(), "%d trips; want %d", trips.N(), workers*each)
	}
	if faults := store.faults(); len(faults) != 0 {
		assert.Len(t, faults, 0, "%q", faults)
	}
}

// Package wstest builds the ws index fixtures the ws package and the
// nova-sprint ws, scope and stream verbs are timed on: a throwaway
// redis-server (testutil.Start) with the nova_sprint library loaded, and N
// tasks across S streams written straight into the ws keys.
package wstest

import (
	"context"
	"fmt"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// Start runs a throwaway redis-server with the library loaded and returns its
// address and a client closed at cleanup.
func Start(t *testing.T) (string, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return addr, c
}

// StreamName is fixture stream i's display name (a colon and a space, like
// the fleet's "swarm: cards").
func StreamName(i int) string { return fmt.Sprintf("s%d: work", i) }

// Mix is the fixture's state for the j-th task of a stream, per 20: 8
// waiting, 4 ready, 3 working, 2 merging, 2 landed, 1 closed.
func Mix(j int) string {
	switch k := j % 20; {
	case k < 8:
		return "waiting"
	case k < 12:
		return "ready"
	case k < 15:
		return "working"
	case k < 17:
		return "merging"
	case k < 19:
		return "landed"
	}
	return "closed"
}

// Created is fixture task i's created_at in ms, its score in every set.
func Created(i int) int64 { return 1700000000000 + int64(i) }

// Fixture writes n tasks t00000.. round-robin across streams streams, in the
// ws shape (hash + the one set scored by created_at), in one pipeline, and
// returns the ids.
func Fixture(t *testing.T, c *redis.Client, n, streams int) []string {
	t.Helper()
	ctx := context.Background()
	pipe := c.Pipeline()
	for s := 0; s < streams; s++ {
		pipe.SAdd(ctx, "ws:names", StreamName(s))
		pipe.ZAdd(ctx, "ws:order", redis.Z{Score: float64(s + 1), Member: StreamName(s)})
	}
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("t%05d", i)
		ids[i] = id
		stream, j := StreamName(i%streams), i/streams
		state := Mix(j)
		score := float64(Created(i))
		pipe.HSet(ctx, "task:"+id, "stream", stream, "state", state, "order", fmt.Sprint(1000+j), "created_at", fmt.Sprint(Created(i)),
			"title", fmt.Sprintf("STREAM: %s | task %d", stream, i), "owner", "f1", "kind", "code")
		if state != "closed" {
			pipe.ZAdd(ctx, "ws:"+stream+":"+state, redis.Z{Score: score, Member: id})
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return ids
}

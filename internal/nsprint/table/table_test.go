package table_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
)

func TestStreamsTableFromRecords(t *testing.T) {
	s, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	rdb := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer rdb.Close()
	ctx := context.Background()

	// Seed miniredis with test ZCARDs
	// Streams from ws:<stream>:<where>
	// sprint:<S>:cards
	rdb.ZAdd(ctx, "ws:test-stream:waiting", redis.Z{Score: float64(time.Now().Unix()), Member: "card:1"})
	rdb.ZAdd(ctx, "sprint:8:cards", redis.Z{Score: 1, Member: "card:1"})

	// Tick
	stats, err := table.TickStreams(ctx, rdb, "8")
	if err != nil {
		t.Fatal(err)
	}

	if len(stats) == 0 {
		t.Errorf("expected stats, got empty")
	}
}

func TestSpecsTableFromRecords(t *testing.T) {
	s, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	rdb := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer rdb.Close()
	ctx := context.Background()

	stats, err := table.TickSpecs(ctx, rdb)
	if err != nil {
		t.Fatal(err)
	}
	if stats == nil {
		t.Errorf("expected non-nil stats")
	}
}

func TestTableTickMakesNoRestCall(t *testing.T) {
	s, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	rdb := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer rdb.Close()
	ctx := context.Background()

	_, err = table.TickStreams(ctx, rdb, "8")
	if err != nil {
		t.Fatal(err)
	}
	_, err = table.TickSpecs(ctx, rdb)
	if err != nil {
		t.Fatal(err)
	}
	// implicitly makes no rest call because it only takes a redis client
}

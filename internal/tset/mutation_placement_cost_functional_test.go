//go:build functional

package tset

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// Direct Redis commands here are isolated fixture initialization, before the
// production writer is installed. Every dummy cell member has a valid record.
func mutationPlacementFixture(t *testing.T, occupants int) *tsetFixture {
	t.Helper()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c", "d")
	fx.AddRow(t, "work", "r", 0)
	ctx := context.Background()
	cell := fixtureCellKey(fx.Space, "work", "0", "r", "c")
	zs := make([]redis.Z, occupants)
	for start := 0; start < occupants; start += 500 {
		end := start + 500
		if end > occupants {
			end = occupants
		}
		pipe := fx.Client.Pipeline()
		for i := start; i < end; i++ {
			id := fmt.Sprintf("occupant%05d", i)
			zs[i] = redis.Z{Score: 1, Member: id}
			pipe.HSet(ctx, fixtureRecordKey(fx.Space, "work", id), map[string]any{
				"epoch": "0", "revision": "1", "place:work": "r:c",
			})
		}
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatalf("seed records %d..%d: %v", start, end, err)
		}
	}
	if err := fx.Client.ZAdd(ctx, cell, zs...).Err(); err != nil {
		t.Fatalf("seed %d destination members: %v", occupants, err)
	}
	pipe := fx.Client.Pipeline()
	pipe.HSet(ctx, fixtureRecordKey(fx.Space, "work", "moving"), map[string]any{
		"epoch": "0", "revision": "1", "place:work": "r:d",
	})
	pipe.ZAdd(ctx, fixtureCellKey(fx.Space, "work", "0", "r", "d"),
		redis.Z{Score: 2, Member: "moving"})
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatalf("seed move source: %v", err)
	}
	fx.ActivateWithLua(t, observationProbeLua)
	return fx
}

func mutationPlacementTrace(t *testing.T, fx *tsetFixture, entry Entry) observationTrace {
	t.Helper()
	trace := observationCall(t, fx, []Entry{entry})
	// Four fixed structural probes in S.open (definition HLEN/HGETALL and
	// epoch engine/n HGET) consume cell/key slots before planning. Allow at
	// most four more point probes for this one-member mutation. Exact trace
	// equality across 10 and 10,000 occupants below is the cardinality gate.
	if trace.Changed != 1 || trace.Record > 2 || trace.Cell > 8 || trace.PlanStoreCommands > 32 {
		t.Fatalf("single-member placement cost is not bounded: %+v", trace)
	}
	for _, argv := range append(trace.OpenTrace, trace.Trace...) {
		if len(argv) == 0 {
			t.Fatalf("empty checked-read argv in %+v", trace)
		}
		command := strings.ToUpper(argv[0])
		switch command {
		case "SCAN", "KEYS", "ZRANGE", "ZREVRANGE", "ZRANGEBYSCORE", "ZREVRANGEBYSCORE", "ZSCAN":
			t.Fatalf("placement planner enumerated cell/table: %v", argv)
		}
		if command == "HGETALL" && len(argv) > 1 && strings.HasPrefix(argv[1], fx.Space+"member:work:") {
			t.Fatalf("placement planner read an entire member record: %v", argv)
		}
	}
	return trace
}

func assertMutationPlacementTraceEqual(t *testing.T, label string, small, large observationTrace) {
	t.Helper()
	// TIME payload width can vary between calls. Subtract only that measured
	// payload; the exact command argv and all non-clock costs must agree.
	if !reflect.DeepEqual(small.OpenTrace, large.OpenTrace) ||
		!reflect.DeepEqual(small.Trace, large.Trace) ||
		small.OpenCommands != large.OpenCommands ||
		small.PlanStoreCommands != large.PlanStoreCommands ||
		small.PlannedCommands != large.PlannedCommands ||
		small.Record != large.Record || small.Field != large.Field || small.Cell != large.Cell ||
		small.FetchedBytes-small.ClockFetchedBytes != large.FetchedBytes-large.ClockFetchedBytes {
		t.Fatalf("%s point work grew with destination occupancy: 10=%+v 10000=%+v", label, small, large)
	}
}

func TestNoHiddenMutationPlacementScan(t *testing.T) {
	t.Parallel()
	var smallCreate, smallMove, largeCreate, largeMove observationTrace
	for _, size := range []int{10, 10000} {
		fx := mutationPlacementFixture(t, size)
		create := Entry{Kind: "create", Table: "work", To: "r:c",
			IDs: []string{"created"}, Scores: []string{"3"}}
		move := Entry{Kind: "move", Table: "work", From: "r:d", To: "r:c",
			IDs: []string{"moving"}, Revs: []Decimal{"1"}}
		createTrace := mutationPlacementTrace(t, fx, create)
		moveTrace := mutationPlacementTrace(t, fx, move)
		if size == 10 {
			smallCreate, smallMove = createTrace, moveTrace
		} else {
			largeCreate, largeMove = createTrace, moveTrace
		}
		store := newFixtureRedis(t, fx.Client)
		for _, entry := range []Entry{create, move} {
			reply, err := store.Step(context.Background(), stateStep(fx.Space, "0", entry))
			if err != nil || reply.Status != "ok" || reply.Changed != 1 {
				t.Fatalf("%d-member %s commit: reply=%+v err=%v", size, entry.Kind, reply, err)
			}
		}
		for id, wantScore := range map[string]float64{"created": 3, "moving": 2} {
			member, err := fx.Client.ZScore(context.Background(),
				fixtureCellKey(fx.Space, "work", "0", "r", "c"), id).Result()
			if err != nil || member != wantScore {
				t.Fatalf("%d-member destination %q score=%v err=%v", size, id, member, err)
			}
			place, err := fx.Client.HGet(context.Background(),
				fixtureRecordKey(fx.Space, "work", id), "place:work").Result()
			if err != nil || place != "r:c" {
				t.Fatalf("%d-member record %q placement=%q err=%v", size, id, place, err)
			}
		}
		if _, err := fx.Client.ZScore(context.Background(),
			fixtureCellKey(fx.Space, "work", "0", "r", "d"), "moving").Result(); err != redis.Nil {
			t.Fatalf("%d-member move retained source placement: %v", size, err)
		}
	}
	assertMutationPlacementTraceEqual(t, "create", smallCreate, largeCreate)
	assertMutationPlacementTraceEqual(t, "move", smallMove, largeMove)
}

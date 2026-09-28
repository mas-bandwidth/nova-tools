//go:build functional

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

// TestWatchCheckDetectsDuplicateMemberPlaceWithoutRepair verifies that watch
// with --check runs invariant verification every tick, reports invariant drift
// (a member duplicated across two cells) as a stall row without exiting non-zero,
// and strictly preserves the corrupt store state without performing any repair.
func TestWatchCheckDetectsDuplicateMemberPlaceWithoutRepair(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()

	// 1. Setup a table and view, add row, place member.
	for _, args := range [][]string{
		{"create", "work", "--columns", "todo,done"},
		{"row", "add", "work", "build"},
		{"view", "set", "today", "--tables", "work", "--title", "Today's Work"},
		{"cell", "add", "work", "build", "todo", "job-1"},
	} {
		if code, out, errout := runTable(at(addr, args...)...); code != 0 {
			t.Fatalf("%v: exit %d: %s %s", args, code, out, errout)
		}
	}

	// Verify unflagged watch produces no stall row.
	code, out, errout := runTable(at(addr, "watch", "--view", "today", "--once")...)
	if code != 0 || errout != "" {
		t.Fatalf("unflagged watch: exit %d: %s %s", code, out, errout)
	}
	if strings.Contains(out, "stall:") {
		t.Fatalf("unflagged watch has stall row:\n%s", out)
	}

	// 2. Plant a duplicate member place by adding the member to a second cell directly
	// with raw redis ZAdd (bypassing table operations).
	cellTodo := ntable.CellKey("work", "build", "todo")
	cellDone := ntable.CellKey("work", "build", "done")
	if err := c.ZAdd(ctx, cellDone, redis.Z{Score: 10, Member: "job-1"}).Err(); err != nil {
		t.Fatalf("planting duplicate place via raw ZAdd: %v", err)
	}

	// 3. Run watch --view today --check --once.
	code, out, errout = runTable(at(addr, "watch", "--view", "today", "--check", "--once")...)
	if code != 0 {
		t.Fatalf("watch --view --check exit %d: %s %s", code, out, errout)
	}
	if !strings.Contains(out, "stall: work:") || !strings.Contains(out, "duplicate place") {
		t.Fatalf("watch --view --check did not report duplicate place stall row:\nstdout:\n%s\nstderr:\n%s", out, errout)
	}

	// Also verify watch work --check --once directly on the table.
	code, outTbl, erroutTbl := runTable(at(addr, "watch", "work", "--check", "--once")...)
	if code != 0 {
		t.Fatalf("watch work --check exit %d: %s %s", code, outTbl, erroutTbl)
	}
	if !strings.Contains(outTbl, "stall: work:") || !strings.Contains(outTbl, "duplicate place") {
		t.Fatalf("watch work --check did not report duplicate place stall row:\nstdout:\n%s\nstderr:\n%s", outTbl, erroutTbl)
	}

	// 4. CRITICAL: Verify the corrupted state in Redis is NOT repaired (both cells still contain the member).
	if score, err := c.ZScore(ctx, cellTodo, "job-1").Result(); err != nil {
		t.Fatalf("member removed from first cell (repaired!): %v", err)
	} else if score == 0 {
		// valid score retrieved
	}
	if score, err := c.ZScore(ctx, cellDone, "job-1").Result(); err != nil {
		t.Fatalf("member removed from second cell (repaired!): %v", err)
	} else if score != 10 {
		t.Fatalf("second cell member score changed: got %v want 10", score)
	}

	// Verify cardinalities remain 1 in both cells.
	n1, err := c.ZCard(ctx, cellTodo).Result()
	if err != nil || n1 != 1 {
		t.Fatalf("cellTodo count = %d, err = %v; want 1", n1, err)
	}
	n2, err := c.ZCard(ctx, cellDone).Result()
	if err != nil || n2 != 1 {
		t.Fatalf("cellDone count = %d, err = %v; want 1", n2, err)
	}
}

//go:build functional

package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

type tickRecorder struct {
	sync.Mutex
	frames []string
	drawn  chan struct{}
}

func newTickRecorder() *tickRecorder {
	return &tickRecorder{drawn: make(chan struct{}, 16)}
}

func (r *tickRecorder) Write(p []byte) (int, error) {
	r.Lock()
	r.frames = append(r.frames, string(p))
	r.Unlock()
	r.drawn <- struct{}{}
	return len(p), nil
}

func (r *tickRecorder) getFrames() []string {
	r.Lock()
	defer r.Unlock()
	out := make([]string, len(r.frames))
	copy(out, r.frames)
	return out
}

func driveTwoTicks(t *testing.T, ctx context.Context, read func(context.Context) (string, error)) []string {
	t.Helper()
	ticks := make(chan time.Time)
	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	rec := newTickRecorder()
	var errOut bytes.Buffer
	clock := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	done := make(chan int, 1)

	go func() {
		done <- watchLoop(loopCtx, rec, &errOut, read, ticks, now, "")
	}()

	// Tick 1 (initial frame on entry)
	select {
	case <-rec.drawn:
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for tick 1")
	}

	// Tick 2 (advance ticker)
	clock = clock.Add(time.Second)
	ticks <- clock
	select {
	case <-rec.drawn:
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for tick 2")
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("watchLoop exited %d", code)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for watchLoop exit")
	}

	frames := rec.getFrames()
	if len(frames) < 2 {
		t.Fatalf("expected at least 2 ticks, got %d", len(frames))
	}
	return frames
}

func countMonitorCommands(t *testing.T, ctx context.Context, admin *redis.Client, reader *bufio.Reader, tag string, fn func()) int {
	t.Helper()
	startMarker := tag + "-start"
	if err := admin.Echo(ctx, startMarker).Err(); err != nil {
		t.Fatal(err)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, `"echo" "`+startMarker+`"`) {
			break
		}
	}

	fn()

	endMarker := tag + "-end"
	if err := admin.Echo(ctx, endMarker).Err(); err != nil {
		t.Fatal(err)
	}
	var commands []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, `"echo" "`+endMarker+`"`) {
			break
		}
		if strings.Contains(line, "[0 lua]") {
			continue
		}
		parts := strings.SplitN(line, "] ", 2)
		if len(parts) != 2 {
			continue
		}
		command := strings.TrimSpace(parts[1])
		lower := strings.ToLower(command)
		if strings.HasPrefix(lower, `"hello"`) || strings.HasPrefix(lower, `"client"`) || strings.HasPrefix(lower, `"auth"`) {
			continue
		}
		commands = append(commands, command)
	}
	return len(commands)
}

// TestWatchCheckDetectsStaleMemberEpochWithoutRepair tests the real
// stale-member-epoch functional fault:
// - sets up an owned Redis fixture at table epoch 2
// - plants a mismatching member epoch (epoch 1) in the member record
// - runs watch --check for BOTH direct-table and stored-view routes
// - drives at least two ticks, verifying the stall row appears on each tick
// - verifies the corrupt state in Redis remains unchanged (strictly no repair)
// - preserves unflagged trip behavior and single round-trip invariant
func TestWatchCheckDetectsStaleMemberEpochWithoutRepair(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()

	// 1. Setup table and view at epoch 2 with domain:epoch.
	if err := c.HSet(ctx, "domain:epoch", "n", "2").Err(); err != nil {
		t.Fatalf("setting domain epoch: %v", err)
	}
	for _, args := range [][]string{
		{"create", "work", "--columns", "todo,done", "--epoch-key", "domain:epoch", "--epoch", "2"},
		{"row", "add", "work", "build", "--epoch", "2"},
		{"view", "set", "today", "--tables", "work", "--title", "Today's Work"},
		{"cell", "add", "work", "build", "todo", "job-1", "--epoch", "2"},
	} {
		if code, out, errout := runTable(at(addr, args...)...); code != 0 {
			t.Fatalf("%v: exit %d: %s %s", args, code, out, errout)
		}
	}

	// Setup Redis MONITOR connection for measuring wire exchanges.
	conn, err := net.DialTimeout("tcp", addr, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprint(conn, "*1\r\n$7\r\nMONITOR\r\n"); err != nil {
		t.Fatal(err)
	}
	monReader := bufio.NewReader(conn)
	if line, err := monReader.ReadString('\n'); err != nil || line != "+OK\r\n" {
		t.Fatalf("MONITOR ready: %q %v", line, err)
	}

	// Verify unflagged watch produces no stall row and preserves single round trip.
	var unflaggedDirectOut, unflaggedViewOut string
	directTrips := countMonitorCommands(t, ctx, c, monReader, "unflagged-direct", func() {
		code, out, errout := runTable(at(addr, "watch", "work", "--once")...)
		if code != 0 || errout != "" {
			t.Fatalf("unflagged direct watch: exit %d: %s %s", code, out, errout)
		}
		unflaggedDirectOut = out
	})
	if strings.Contains(unflaggedDirectOut, "stall:") {
		t.Fatalf("unflagged direct watch has stall row:\n%s", unflaggedDirectOut)
	}
	if directTrips != 1 {
		t.Fatalf("unflagged direct watch sent %d commands; want 1 (single round-trip invariant)", directTrips)
	}

	viewTrips := countMonitorCommands(t, ctx, c, monReader, "unflagged-view", func() {
		code, out, errout := runTable(at(addr, "watch", "--view", "today", "--once")...)
		if code != 0 || errout != "" {
			t.Fatalf("unflagged view watch: exit %d: %s %s", code, out, errout)
		}
		unflaggedViewOut = out
	})
	if strings.Contains(unflaggedViewOut, "stall:") {
		t.Fatalf("unflagged view watch has stall row:\n%s", unflaggedViewOut)
	}
	if viewTrips != 2 {
		t.Fatalf("unflagged view watch sent %d commands; want 2 (view definition + table pipeline)", viewTrips)
	}

	// 2. Plant the real stale-member-epoch functional fault:
	// Table active epoch is 2; plant member record epoch 1.
	memberKey := ntable.MemberKey("job-1")
	if err := c.HSet(ctx, memberKey, "epoch", "1").Err(); err != nil {
		t.Fatalf("planting stale member epoch: %v", err)
	}

	// 3. Run watch --check for BOTH direct table route and stored-view route,
	// driving at least two ticks each.
	// Route A: Direct table route (watch work --check)
	readTable := tablesReader(c, []string{"work"}, "", ntable.RenderOpts{}, true)
	directFrames := driveTwoTicks(t, ctx, readTable)
	for i, frame := range directFrames {
		if !strings.Contains(frame, "stall: work:") || !strings.Contains(frame, "member belongs to another epoch") {
			t.Fatalf("direct-table tick %d missing stall row:\n%s", i+1, frame)
		}
	}

	// CLI check for direct table route (--once)
	code, outTbl, erroutTbl := runTable(at(addr, "watch", "work", "--check", "--once")...)
	if code != 0 {
		t.Fatalf("watch work --check exit %d: %s %s", code, outTbl, erroutTbl)
	}
	if !strings.Contains(outTbl, "stall: work:") || !strings.Contains(outTbl, "member belongs to another epoch") {
		t.Fatalf("watch work --check --once missing stall row:\nstdout:\n%s\nstderr:\n%s", outTbl, erroutTbl)
	}

	// Route B: Stored-view route (watch --view today --check)
	readView := viewReader(c, "today", ntable.RenderOpts{}, true)
	viewFrames := driveTwoTicks(t, ctx, readView)
	for i, frame := range viewFrames {
		if !strings.Contains(frame, "stall: work:") || !strings.Contains(frame, "member belongs to another epoch") {
			t.Fatalf("stored-view tick %d missing stall row:\n%s", i+1, frame)
		}
	}

	// CLI check for stored-view route (--once)
	code, outV, erroutV := runTable(at(addr, "watch", "--view", "today", "--check", "--once")...)
	if code != 0 {
		t.Fatalf("watch --view today --check exit %d: %s %s", code, outV, erroutV)
	}
	if !strings.Contains(outV, "stall: work:") || !strings.Contains(outV, "member belongs to another epoch") {
		t.Fatalf("watch --view today --check --once missing stall row:\nstdout:\n%s\nstderr:\n%s", outV, erroutV)
	}

	// 4. CRITICAL: Verify the corrupt state in Redis remains unchanged after (no repair).
	if epoch := c.HGet(ctx, memberKey, "epoch").Val(); epoch != "1" {
		t.Fatalf("member epoch was repaired: got %q, want %q", epoch, "1")
	}
	if place := c.HGet(ctx, memberKey, "place:work").Val(); place != "build:todo" {
		t.Fatalf("member place was modified: got %q, want %q", place, "build:todo")
	}
	cellKey := ntable.CellKeyAt("work", "build", "todo", 2)
	if score, err := c.ZScore(ctx, cellKey, "job-1").Result(); err != nil {
		t.Fatalf("member removed from cell (repaired!): %v", err)
	} else if score == 0 {
		// Valid score present.
	}
	if ep := c.HGet(ctx, "domain:epoch", "n").Val(); ep != "2" {
		t.Fatalf("domain epoch was modified: got %q, want %q", ep, "2")
	}

	// 5. Preserve unflagged trip behavior and single round-trip invariant:
	// Running unflagged watch on the corrupt table still produces NO stall row
	// and preserves the single round-trip count.
	tripsCorrupt := countMonitorCommands(t, ctx, c, monReader, "unflagged-corrupt", func() {
		code, out, errout := runTable(at(addr, "watch", "work", "--once")...)
		if code != 0 || errout != "" {
			t.Fatalf("unflagged direct watch on corrupt table: exit %d: %s %s", code, out, errout)
		}
		if strings.Contains(out, "stall:") {
			t.Fatalf("unflagged direct watch has stall row on corrupt table:\n%s", out)
		}
	})
	if tripsCorrupt != 1 {
		t.Fatalf("unflagged watch on corrupt table sent %d commands; want 1 (single round-trip invariant)", tripsCorrupt)
	}
}

// TestWatchCheckDetectsDuplicateMemberPlaceWithoutRepair verifies that watch
// with --check reports duplicate member placement as a stall row across ticks
// without repairing or mutating Redis keys.
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

	// 3. Drive at least two ticks for both direct-table and view routes.
	readTable := tablesReader(c, []string{"work"}, "", ntable.RenderOpts{}, true)
	directFrames := driveTwoTicks(t, ctx, readTable)
	for i, frame := range directFrames {
		if !strings.Contains(frame, "stall: work:") || !strings.Contains(frame, "duplicate place") {
			t.Fatalf("direct-table tick %d missing duplicate place stall row:\n%s", i+1, frame)
		}
	}

	readView := viewReader(c, "today", ntable.RenderOpts{}, true)
	viewFrames := driveTwoTicks(t, ctx, readView)
	for i, frame := range viewFrames {
		if !strings.Contains(frame, "stall: work:") || !strings.Contains(frame, "duplicate place") {
			t.Fatalf("view route tick %d missing duplicate place stall row:\n%s", i+1, frame)
		}
	}

	// Run CLI check --once for both routes.
	code, out, errout = runTable(at(addr, "watch", "--view", "today", "--check", "--once")...)
	if code != 0 {
		t.Fatalf("watch --view --check exit %d: %s %s", code, out, errout)
	}
	if !strings.Contains(out, "stall: work:") || !strings.Contains(out, "duplicate place") {
		t.Fatalf("watch --view --check did not report duplicate place stall row:\nstdout:\n%s\nstderr:\n%s", out, errout)
	}

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

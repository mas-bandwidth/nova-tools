//go:build functional

package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"github.com/redis/go-redis/v9"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"

	"github.com/stretchr/testify/require"
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
		require.FailNow(t, "timed out waiting for tick 1")
	}

	// Tick 2 (advance ticker)
	clock = clock.Add(time.Second)
	ticks <- clock
	select {
	case <-rec.drawn:
	case <-time.After(30 * time.Second):
		require.FailNow(t, "timed out waiting for tick 2")
	}

	cancel()
	select {
	case code := <-done:
		require.EqualValues(t, 0, code, "watchLoop exited %d", code)
	case <-time.After(30 * time.Second):
		require.FailNow(t, "timed out waiting for watchLoop exit")
	}

	frames := rec.getFrames()
	require.GreaterOrEqual(t, len(frames), 2, "expected at least 2 ticks, got %d", len(frames))
	return frames
}

func countMonitorCommands(t *testing.T, ctx context.Context, admin *redis.Client, reader *bufio.Reader, tag string, fn func()) int {
	t.Helper()
	startMarker := tag + "-start"
	require.NoError(t, admin.Echo(ctx, startMarker).Err())
	for {
		line, err := reader.ReadString('\n')
		require.NoError(t, err, "%v", err)
		if strings.Contains(line, `"echo" "`+startMarker+`"`) {
			break
		}
	}

	fn()

	endMarker := tag + "-end"
	require.NoError(t, admin.Echo(ctx, endMarker).Err())
	var commands []string
	for {
		line, err := reader.ReadString('\n')
		require.NoError(t, err, "%v", err)
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
	{
		err := c.HSet(ctx, "domain:epoch", "n", "2").Err()
		require.NoError(t, err, "setting domain epoch: %v", err)
	}
	for _, args := range [][]string{
		{"create", "work", "--columns", "todo,done", "--epoch-key", "domain:epoch", "--epoch", "2"},
		{"row", "add", "work", "build", "--epoch", "2"},
		{"view", "set", "today", "--tables", "work", "--title", "Today's Work"},
		{"cell", "add", "work", "build", "todo", "job-1", "--epoch", "2"},
	} {
		{
			code, out, errout := runTable(at(addr, args...)...)
			require.EqualValues(t, 0, code, "%v: exit %d: %s %s", args, code, out, errout)
		}
	}

	// Setup Redis MONITOR connection for measuring wire exchanges.
	conn, err := net.DialTimeout("tcp", addr, 30*time.Second)
	require.NoError(t, err, "%v", err)
	defer conn.Close()
	require.NoError(t, conn.SetDeadline(time.Now().Add(30*time.Second)))
	{
		_, err := fmt.Fprint(conn, "*1\r\n$7\r\nMONITOR\r\n")
		require.NoError(t, err, "%v", err)
	}
	monReader := bufio.NewReader(conn)
	{
		line, err := monReader.ReadString('\n')
		require.NoError(t, err, "MONITOR ready: %q %v", line, err)
		require.Equal(t, "+OK\r\n", line, "MONITOR ready: %q %v", line, err)
	}

	// Verify unflagged watch produces no stall row and preserves single round trip.
	var unflaggedDirectOut, unflaggedViewOut string
	directTrips := countMonitorCommands(t, ctx, c, monReader, "unflagged-direct", func() {
		code, out, errout := runTable(at(addr, "watch", "work", "--once")...)
		require.EqualValues(t, 0, code, "unflagged direct watch: exit %d: %s %s", code, out, errout)
		require.Empty(t, errout, "unflagged direct watch: exit %d: %s %s", code, out, errout)
		unflaggedDirectOut = out
	})
	require.NotContains(t, unflaggedDirectOut, "stall:", "unflagged direct watch has stall row:\n%s", unflaggedDirectOut)
	require.EqualValues(t, 1, directTrips, "unflagged direct watch sent %d commands; want 1 (single round-trip invariant)", directTrips)

	viewTrips := countMonitorCommands(t, ctx, c, monReader, "unflagged-view", func() {
		code, out, errout := runTable(at(addr, "watch", "--view", "today", "--once")...)
		require.EqualValues(t, 0, code, "unflagged view watch: exit %d: %s %s", code, out, errout)
		require.Empty(t, errout, "unflagged view watch: exit %d: %s %s", code, out, errout)
		unflaggedViewOut = out
	})
	require.NotContains(t, unflaggedViewOut, "stall:", "unflagged view watch has stall row:\n%s", unflaggedViewOut)
	require.EqualValues(t, 2, viewTrips, "unflagged view watch sent %d commands; want 2 (view definition + table pipeline)", viewTrips)

	// 2. Plant the real stale-member-epoch functional fault:
	// Table active epoch is 2; plant member record epoch 1.
	memberKey := ntable.MemberKey("job-1")
	{
		err := c.HSet(ctx, memberKey, "epoch", "1").Err()
		require.NoError(t, err, "planting stale member epoch: %v", err)
	}

	// 3. Run watch --check for BOTH direct table route and stored-view route,
	// driving at least two ticks each.
	// Route A: Direct table route (watch work --check)
	readTable := tablesReader(c, []string{"work"}, "", ntable.RenderOpts{}, true)
	directFrames := driveTwoTicks(t, ctx, readTable)
	for i, frame := range directFrames {
		require.Contains(t, frame, "stall: work:", "direct-table tick %d missing stall row:\n%s", i+1, frame)
		require.Contains(t, frame, "member belongs to another epoch", "direct-table tick %d missing stall row:\n%s", i+1, frame)
	}

	// CLI check for direct table route (--once)
	code, outTbl, erroutTbl := runTable(at(addr, "watch", "work", "--check", "--once")...)
	require.EqualValues(t, 0, code, "watch work --check exit %d: %s %s", code, outTbl, erroutTbl)
	require.Contains(t, outTbl, "stall: work:", "watch work --check --once missing stall row:\nstdout:\n%s\nstderr:\n%s", outTbl, erroutTbl)
	require.Contains(t, outTbl, "member belongs to another epoch", "watch work --check --once missing stall row:\nstdout:\n%s\nstderr:\n%s", outTbl, erroutTbl)

	// Route B: Stored-view route (watch --view today --check)
	readView := viewReader(c, "today", ntable.RenderOpts{}, true)
	viewFrames := driveTwoTicks(t, ctx, readView)
	for i, frame := range viewFrames {
		require.Contains(t, frame, "stall: work:", "stored-view tick %d missing stall row:\n%s", i+1, frame)
		require.Contains(t, frame, "member belongs to another epoch", "stored-view tick %d missing stall row:\n%s", i+1, frame)
	}

	// CLI check for stored-view route (--once)
	code, outV, erroutV := runTable(at(addr, "watch", "--view", "today", "--check", "--once")...)
	require.EqualValues(t, 0, code, "watch --view today --check exit %d: %s %s", code, outV, erroutV)
	require.Contains(t, outV, "stall: work:", "watch --view today --check --once missing stall row:\nstdout:\n%s\nstderr:\n%s", outV, erroutV)
	require.Contains(t, outV, "member belongs to another epoch", "watch --view today --check --once missing stall row:\nstdout:\n%s\nstderr:\n%s", outV, erroutV)

	// 4. CRITICAL: Verify the corrupt state in Redis remains unchanged after (no repair).
	{
		epoch := c.HGet(ctx, memberKey, "epoch").Val()
		require.Equal(t, "1", epoch, "member epoch was repaired: got %q, want %q", epoch, "1")
	}
	{
		place := c.HGet(ctx, memberKey, "place:work").Val()
		require.Equal(t, "build:todo", place, "member place was modified: got %q, want %q", place, "build:todo")
	}
	cellKey := ntable.CellKeyAt("work", "build", "todo", 2)
	{
		_, err := c.ZScore(ctx, cellKey, "job-1").Result()
		require.NoError(t, err, "member removed from cell (repaired!): %v", err)
	}
	{
		ep := c.HGet(ctx, "domain:epoch", "n").Val()
		require.Equal(t, "2", ep, "domain epoch was modified: got %q, want %q", ep, "2")
	}

	// 5. Preserve unflagged trip behavior and single round-trip invariant:
	// Running unflagged watch on the corrupt table still produces NO stall row
	// and preserves the single round-trip count.
	tripsCorrupt := countMonitorCommands(t, ctx, c, monReader, "unflagged-corrupt", func() {
		code, out, errout := runTable(at(addr, "watch", "work", "--once")...)
		require.EqualValues(t, 0, code, "unflagged direct watch on corrupt table: exit %d: %s %s", code, out, errout)
		require.Empty(t, errout, "unflagged direct watch on corrupt table: exit %d: %s %s", code, out, errout)
		require.NotContains(t, out, "stall:", "unflagged direct watch has stall row on corrupt table:\n%s", out)
	})
	require.EqualValues(t, 1, tripsCorrupt, "unflagged watch on corrupt table sent %d commands; want 1 (single round-trip invariant)", tripsCorrupt)
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
		{
			code, out, errout := runTable(at(addr, args...)...)
			require.EqualValues(t, 0, code, "%v: exit %d: %s %s", args, code, out, errout)
		}
	}

	// Verify unflagged watch produces no stall row.
	code, out, errout := runTable(at(addr, "watch", "--view", "today", "--once")...)
	require.EqualValues(t, 0, code, "unflagged watch: exit %d: %s %s", code, out, errout)
	require.Empty(t, errout, "unflagged watch: exit %d: %s %s", code, out, errout)
	require.NotContains(t, out, "stall:", "unflagged watch has stall row:\n%s", out)

	// 2. Plant a duplicate member place by adding the member to a second cell directly
	// with raw redis ZAdd (bypassing table operations).
	cellTodo := ntable.CellKey("work", "build", "todo")
	cellDone := ntable.CellKey("work", "build", "done")
	{
		err := c.ZAdd(ctx, cellDone, redis.Z{Score: 10, Member: "job-1"}).Err()
		require.NoError(t, err, "planting duplicate place via raw ZAdd: %v", err)
	}

	// 3. Drive at least two ticks for both direct-table and view routes.
	readTable := tablesReader(c, []string{"work"}, "", ntable.RenderOpts{}, true)
	directFrames := driveTwoTicks(t, ctx, readTable)
	for i, frame := range directFrames {
		require.Contains(t, frame, "stall: work:", "direct-table tick %d missing duplicate place stall row:\n%s", i+1, frame)
		require.Contains(t, frame, "duplicate place", "direct-table tick %d missing duplicate place stall row:\n%s", i+1, frame)
	}

	readView := viewReader(c, "today", ntable.RenderOpts{}, true)
	viewFrames := driveTwoTicks(t, ctx, readView)
	for i, frame := range viewFrames {
		require.Contains(t, frame, "stall: work:", "view route tick %d missing duplicate place stall row:\n%s", i+1, frame)
		require.Contains(t, frame, "duplicate place", "view route tick %d missing duplicate place stall row:\n%s", i+1, frame)
	}

	// Run CLI check --once for both routes.
	code, out, errout = runTable(at(addr, "watch", "--view", "today", "--check", "--once")...)
	require.EqualValues(t, 0, code, "watch --view --check exit %d: %s %s", code, out, errout)
	require.Contains(t, out, "stall: work:", "watch --view --check did not report duplicate place stall row:\nstdout:\n%s\nstderr:\n%s", out, errout)
	require.Contains(t, out, "duplicate place", "watch --view --check did not report duplicate place stall row:\nstdout:\n%s\nstderr:\n%s", out, errout)

	code, outTbl, erroutTbl := runTable(at(addr, "watch", "work", "--check", "--once")...)
	require.EqualValues(t, 0, code, "watch work --check exit %d: %s %s", code, outTbl, erroutTbl)
	require.Contains(t, outTbl, "stall: work:", "watch work --check did not report duplicate place stall row:\nstdout:\n%s\nstderr:\n%s", outTbl, erroutTbl)
	require.Contains(t, outTbl, "duplicate place", "watch work --check did not report duplicate place stall row:\nstdout:\n%s\nstderr:\n%s", outTbl, erroutTbl)

	// 4. CRITICAL: Verify the corrupted state in Redis is NOT repaired (both cells still contain the member).
	{
		_, err := c.ZScore(ctx, cellTodo, "job-1").Result()
		require.NoError(t, err, "member removed from first cell (repaired!): %v", err)
	}
	{
		score, err := c.ZScore(ctx, cellDone, "job-1").Result()
		require.NoError(t, err, "member removed from second cell (repaired!): %v", err)
		require.EqualValues(t, 10, score, "second cell member score changed: got %v want 10", score)
	}

	// Verify cardinalities remain 1 in both cells.
	n1, err := c.ZCard(ctx, cellTodo).Result()
	require.NoError(t, err, "cellTodo count = %d, err = %v; want 1", n1, err)
	require.EqualValues(t, 1, n1, "cellTodo count = %d, err = %v; want 1", n1, err)
	n2, err := c.ZCard(ctx, cellDone).Result()
	require.NoError(t, err, "cellDone count = %d, err = %v; want 1", n2, err)
	require.EqualValues(t, 1, n2, "cellDone count = %d, err = %v; want 1", n2, err)
}

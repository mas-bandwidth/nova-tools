//go:build functional

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// TestLoopBindsTheStreamsTable is the stream block as a nova-table, end to
// end: the live loop over the #3530 fixture binds the streams table in the
// store on its first tick (one row per stream of ws:order, every cell bound
// to its ws:<s>:<state> set, the sentinel excluded), a read of that table
// through ntable renders byte for byte the block the loop published, and a
// `sprint clear` moves the epoch, the loop binds the new epoch's sets, they
// are empty, and the block hides -- the table reads empty and renders as
// nothing. Events, never the wall clock: every wait polls a condition.
func TestLoopBindsTheStreamsTable(t *testing.T) {
	t.Parallel()

	// a throwaway redis-server with the library (sprint clear is one
	// FCALL), seeded with the fixture; the store's login is this process's
	// environment, as every parallel test here dials
	addr, client := wstest.Start(t)
	ctx := context.Background()
	for _, cmd := range table.SprintFixture() {
		args := make([]any, len(cmd))
		for i, v := range cmd {
			args[i] = v
		}
		if err := client.Do(ctx, args...).Err(); err != nil {
			t.Fatalf("seed %v: %v", cmd, err)
		}
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "TABLE.txt")
	cfg := table.SprintConfig{Sprint: "fix", Friends: []string{"rowan", "johnny", "emma", "stella"}}
	opts := tableOpts{layout: "live", loop: true, every: 20 * time.Millisecond, out: out}
	loopCtx, cancel := context.WithCancel(ctx)
	var stdout, stderr lockedBuffer
	done := make(chan int, 1)
	go func() { done <- loopTable(loopCtx, addr, cfg, "--sprint", opts, &stdout, &stderr) }()
	defer func() {
		cancel()
		<-done
	}()

	// the first publish, and the table bound with it
	var body []byte
	waitFor(t, "the first publish", func() bool {
		b, err := os.ReadFile(out)
		body = b
		return err == nil && len(b) > 0
	})
	waitFor(t, "the streams table bound", func() bool {
		n, err := client.ZCard(ctx, ntable.RowsKey(table.StreamsTable)).Result()
		return err == nil && n == int64(len(table.SprintFixtureStreams))
	})
	got, err := ntable.Read(ctx, client, table.StreamsTable)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range table.SprintFixtureStreams {
		r := got.Rows[i]
		if r.Key != s.Name || r.Exclude != ws.SentinelID(s.Name) || r.Owner != table.StreamsOwner {
			t.Fatalf("row %d = %+v; want stream %s, its sentinel excluded, owner %s", i, r, s.Name, table.StreamsOwner)
		}
		for j, state := range table.WSStates {
			if cell := r.Cells[j+1]; !cell.Bound || cell.Key != ws.KeyAt(0, s.Name, state) {
				t.Fatalf("%s %s cell = %+v; want bound to %s", s.Name, state, cell, ws.KeyAt(0, s.Name, state))
			}
		}
	}
	block := ntable.Render(got, table.StreamsRenderOpts)
	if block == "" || !strings.Contains(string(body), block) {
		t.Fatalf("the table read through ntable:\n%s\nis not in the published table:\n%s", block, body)
	}
	// the block is the published one, from the headline's blank line to the
	// worker table: the same bytes
	published := string(body)
	start := strings.Index(published, "stream ")
	end := strings.Index(published, "worker ")
	if start < 0 || end < 0 || published[start:end] != block+"\n" {
		t.Fatalf("published block:\n%q\nntable render:\n%q", published[start:end], block+"\n")
	}
	if line := stdout.String(); !strings.Contains(line, "TABLE loop out="+out) {
		t.Fatalf("stdout %q, want the one TABLE loop line", line)
	}

	// sprint clear: the epoch moves, nothing is copied or cleared here
	epochBefore, err := ws.Epoch(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	code, clearOut, clearErr := runSprint("sprint", "clear", "--redis", addr, "--why", "the streams table test", "--force", "--checkpoint", filepath.Join(dir, "checkpoint.txt"))
	if code != 0 {
		t.Fatalf("sprint clear: exit %d stdout %q stderr %q", code, clearOut, clearErr)
	}
	epochAfter, err := ws.Epoch(ctx, client)
	if err != nil || epochAfter != epochBefore+1 {
		t.Fatalf("epoch %d -> %d (%v), want one more", epochBefore, epochAfter, err)
	}
	waitFor(t, "the table bound to the new epoch", func() bool {
		shape, err := ntable.Shape(ctx, client, table.StreamsTable)
		return err == nil && len(shape.Rows) > 0 && shape.Rows[0].Cells[1].Key == ws.KeyAt(epochAfter, shape.Rows[0].Key, ws.Waiting)
	})
	waitFor(t, "the block hidden", func() bool {
		b, err := os.ReadFile(out)
		return err == nil && !strings.Contains(string(b), "stream ")
	})
	got, err = ntable.Read(ctx, client, table.StreamsTable)
	if err != nil {
		t.Fatal(err)
	}
	if rendered := ntable.Render(got, table.StreamsRenderOpts); rendered != "" {
		t.Fatalf("after sprint clear the streams table renders:\n%s\nwant nothing", rendered)
	}
	if len(got.Rows) != len(table.SprintFixtureStreams) {
		t.Fatalf("after sprint clear the table has %d rows, want the %d streams still bound (to empty sets)", len(got.Rows), len(table.SprintFixtureStreams))
	}
	if stderr.String() != "" {
		t.Fatalf("the loop said on stderr: %q", stderr.String())
	}
}

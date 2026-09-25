package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
)

func TestTableLoopAnsiStdout(t *testing.T) {
	addr, _ := wholeTableRedis(t)
	cfg := table.SprintConfig{Sprint: "fix", Friends: []string{"rowan", "johnny", "emma", "stella"}, RowStale: 10 * time.Second}
	opts := tableOpts{layout: "live", loop: true, every: 50 * time.Millisecond, out: ""}
	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr lockedBuffer

	done := make(chan int, 1)
	go func() { done <- loopTable(ctx, addr, cfg, opts, &stdout, &stderr) }()

	// Wait for at least two ticks by checking if cursor home was printed.
	waitFor(t, "cursor home", func() bool {
		return strings.Contains(stdout.String(), "\033[H")
	})

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("first writer exit %d; stderr %s", code, stderr.String())
		}
	case <-time.After(holdWait()):
		t.Fatal("first writer did not stop on cancel")
	}

	out := stdout.String()
	if !strings.HasPrefix(out, "\033[?25l") {
		t.Errorf("expected cursor hide at start, got %q", out[:min(len(out), 10)])
	}
	if !strings.HasSuffix(out, "\033[?25h") {
		t.Errorf("expected cursor show at end, got suffix %q", out[max(0, len(out)-10):])
	}
	if !strings.Contains(out, "\033[H\033[J") {
		t.Errorf("expected cursor home and clear to end between ticks, got %q", out)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

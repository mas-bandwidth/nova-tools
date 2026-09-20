package pulse

// A DOWN bench must never hang the fill tick, the dealer or a neighbour (#2009).
// The 2026-09-20 load test wedged the 10-second sampler for minutes on a blocking
// ssh to a dead host, so the width table froze and the dealer went blind.
//
// Fake control (Glenn): one hanging host beside one healthy host returns within
// the bound, retains the healthy result and marks the other missed; no live
// probe ran here.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hangCap answers a fixed capacity for every bench except Hang, which blocks
// until block is closed. That is a fake bench that accepted the connection and
// never answered.
type hangCap struct {
	hang    string
	healthy map[string]int
	block   chan struct{}
}

func (c hangCap) Capacity(bench string) (int, error) {
	if bench == c.hang {
		<-c.block
		return 0, nil
	}
	return c.healthy[bench], nil
}

func fillTestWait(t *testing.T) time.Duration {
	t.Helper()
	wait := 30 * time.Second
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			wait = d
		}
	}
	return wait
}

// TestFillHangingBenchDoesNotBlockAHealthyNeighbour is the #2009 red: a capacity
// probe that never answers used to serialize the tick, so a DOWN bench hung the
// dealer and starved every other host. The hanging probe is a channel, not a
// live ssh.
func TestFillHangingBenchDoesNotBlockAHealthyNeighbour(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "a card\n")
	writeCard(t, ready, "card-002.md", "a card\n")
	block := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-block:
		default:
			close(block)
		}
	})
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	in := FillInput{
		Ready:    ready,
		Launched: launched,
		Machines: machinesFile(t, dir, []string{"down-bench", "up-bench"}, nil),
		Benches:  []string{"down-bench", "up-bench"},
		Once:     true,
		Timeout:  time.Second, // duration input to Fill, not a wall-clock assertion
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: hangCap{
			hang:    "down-bench",
			healthy: map[string]int{"up-bench": 2},
			block:   block,
		},
		Launcher: l,
	}

	done := make(chan int, 1)
	go func() { done <- Fill(in) }()
	var code int
	select {
	case code = <-done:
	case <-time.After(fillTestWait(t)):
		t.Fatal("fill hung on the down bench; a missed probe must not block the tick")
	}
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0 (the healthy bench still filled); stderr=%q", code, errb.String())
	}
	if !strings.Contains(errb.String(), "FILL MISSED bench=down-bench") {
		t.Fatalf("stderr does not mark the hanging bench missed: %q", errb.String())
	}
	if strings.Contains(errb.String(), "FILL UNREADABLE bench=down-bench free=0") {
		t.Fatalf("a missed tick must not read as bench full (free=0): %q", errb.String())
	}
	line := strings.SplitN(strings.TrimSpace(out.String()), "\n", 2)[0]
	if !strings.Contains(line, "down-bench:missed=1") {
		t.Fatalf("FILL line does not receipt the miss: %q", line)
	}
	if !strings.Contains(line, "up-bench:launched=2,failed=0") {
		t.Fatalf("FILL line dropped the healthy bench: %q", line)
	}
	if len(l.calls) != 2 {
		t.Fatalf("launcher calls = %d, want 2 on the healthy bench: %q", len(l.calls), l.calls)
	}
	for _, c := range l.calls {
		if strings.HasPrefix(c, "down-bench ") {
			t.Fatalf("a missed bench was still launched onto: %q", l.calls)
		}
	}
}

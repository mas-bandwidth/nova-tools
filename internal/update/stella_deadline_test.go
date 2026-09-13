package update

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// The deadline repair must not starve a healthy child: one that writes a bounded
// but substantial stream and exits at once is drained in full, never cut short
// by a drain allowance that only the deadline case may shrink.
func TestDeadlineHealthyChildUnderLoadIsFullyDrained(t *testing.T) {
	args, err := argv(command(t, "flood", strconv.Itoa(40)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := process(ctx, args, nil, ChildCap)
	if p.Reason != "" {
		t.Fatalf("a healthy child was refused: %s", p.Reason)
	}
	// The version line ("x 1.2.3\n") plus 40 KiB of padding, every byte captured.
	if want := 8 + 40*1024; len(p.Stdout) != want {
		t.Fatalf("drained %d bytes, want %d", len(p.Stdout), want)
	}
}

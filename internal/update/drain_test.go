package update

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The deadline repair must not starve a healthy child: one that writes a bounded
// but substantial stream and exits at once is drained in full, never cut short
// by a drain allowance that only the deadline case may shrink.
func TestDeadlineHealthyChildUnderLoadIsFullyDrained(t *testing.T) {
	args, err := argv(command(t, "flood", strconv.Itoa(40)))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p := process(ctx, args, nil, ChildCap)
	require.Empty(t, p.Reason, "a healthy child was refused")
	// The version line ("x 1.2.3\n") plus 40 KiB of padding, every byte captured.
	require.Len(t, p.Stdout, 8+40*1024, "the drain cut the stream short")
}

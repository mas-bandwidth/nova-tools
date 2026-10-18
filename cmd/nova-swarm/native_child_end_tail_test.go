package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The child-end tail keeps the end of the last line: a failure is said at the
// end of what the child wrote, so a long final line must lose its prefix, not
// its suffix, when the tail is cut to fit.
func TestTheChildEndTailKeepsTheEndOfTheLastLine(t *testing.T) {
	t.Parallel()
	const (
		max    = 4096
		prefix = "PREFIX-MARKER-"
		suffix = "-SUFFIX-MARKER"
		size   = 10000
	)
	stderr := prefix + strings.Repeat("x", size-len(prefix)-len(suffix)) + suffix
	require.Len(t, stderr, size)

	tail := nativeChildEndTail(stderr, max)
	require.LessOrEqual(t, len(tail), max, "the tail is bounded to max")
	require.Contains(t, tail, suffix, "the tail keeps the end of the last line")
	require.NotContains(t, tail, prefix, "the tail drops the start of the last line")
}

// A stderr ending in a newline spends no line on the empty string after it:
// twenty terminated lines are twenty lines, so the last twenty of twenty-five
// are lines 6 to 25.
func TestTwentyTerminatedLinesAreTwentyLines(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	for i := 1; i <= 25; i++ {
		fmt.Fprintf(&b, "line-%02d\n", i)
	}

	tail := nativeChildEndTail(b.String(), 4096)
	require.Contains(t, tail, "line-06", "the tail starts at the sixth line")
	require.Contains(t, tail, "line-25", "the tail keeps the last line")
	require.NotContains(t, tail, "line-05", "the line before the tail is dropped")
	require.Equal(t, 19, strings.Count(tail, `\x0a`), "twenty terminated lines are twenty lines")
}

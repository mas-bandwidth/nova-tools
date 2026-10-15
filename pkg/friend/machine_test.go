package friend

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)

func TestPongLinesRoundTrip(t *testing.T) {
	t.Parallel()
	n, q, w, wd, ok := ParsePong(PongLine("abc123", 5, 3, 8))
	require.True(t, ok)
	assert.Equal(t, []int{5, 3, 8}, []int{q, w, wd})
	assert.Equal(t, "abc123", n)
	_, _, _, _, ok = ParsePong("pong abc123")
	assert.False(t, ok, "the bare legacy line is refused, never read as zeros (pong_schema_test.go)")
	_, _, _, _, ok = ParsePong("hello pong abc123")
	assert.False(t, ok)
}

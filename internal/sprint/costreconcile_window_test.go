package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The sprint's records of a window are every take and read of each provider that ended in
// [from, to), whatever its end, at its charged figure and in tokens: never one ended before
// the window or at its end, and a record with no dollar figure is counted unpriced.
func TestTheRecordsOfAWindowAreThatWindowsOnly(t *testing.T) {
	t.Parallel()
	w := reconcileWorld(t)
	from := w.s.Now.Add(-2 * time.Hour)
	got := RecordedSpendBetween(w.s, from, w.s.Now)
	assert.InDelta(t, 10.0, got["openrouter"].USD, 1e-9, "today's take and read, never yesterday's $40")
	assert.Equal(t, int64(150), got["openrouter"].Tokens)
	assert.Equal(t, 2, got["openrouter"].Records)
	assert.InDelta(t, 5.0, got["opencode"].USD, 1e-9)

	all := RecordedSpendBetween(w.s, from.Add(-48*time.Hour), w.s.Now)
	assert.InDelta(t, 50.0, all["openrouter"].USD, 1e-9)
	assert.Empty(t, RecordedSpendBetween(w.s, w.s.Now, w.s.Now.Add(time.Hour)), "the window's end is excluded")
}

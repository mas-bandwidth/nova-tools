package friend

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The hold is the bound a row's harness fault is down for.
func TestTheLaneFaultHoldIsFifteenMinutes(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 15*time.Minute, LaneFaultHold)
	assert.Equal(t, FaultDownFor, LaneFaultHold)
}

// A watch that was cleared counts a fresh run of refusals anew: a fault that clears itself and
// then meets the same refusal twice faults again, one judgment per episode.
func TestAClearedLaneFaultCountsAnew(t *testing.T) {
	t.Parallel()
	var w LaneFaultWatch
	_, yes := w.Observe("content_filter_error: no")
	require.False(t, yes)
	_, yes = w.Observe("content_filter_error: no")
	require.True(t, yes)
	require.True(t, w.Faulted())
	w.Clear()
	require.False(t, w.Faulted())
	_, yes = w.Observe("content_filter_error: no")
	assert.False(t, yes, "the first refusal after a clear is the first of a new run")
	_, yes = w.Observe("content_filter_error: no")
	assert.True(t, yes, "the second faults again")
}

// A refusal that is not a real refusal, an empty line or a bare brace, is no lane fault however
// often it is seen: it is a read fault, and counts toward nothing.
func TestABareLineIsNeverALaneFault(t *testing.T) {
	t.Parallel()
	for _, line := range []string{"", " ", "{", "}", "[{", "{}", "[]", `{"`, ",", `"":`} {
		assert.True(t, BareLine(line), "%q has no word", line)
		var w LaneFaultWatch
		for i := 0; i < 2*LaneFaultAlike; i++ {
			_, yes := w.Observe(line)
			assert.False(t, yes, "%q", line)
		}
		assert.False(t, w.Faulted(), "%q", line)
		assert.Empty(t, w.Report(), "%q", line)
	}
	for _, line := range []string{`"error": "boom"`, "a", "1", "error: no", `{"error":"x"}`} {
		assert.False(t, BareLine(line), "%q has a word", line)
	}
}

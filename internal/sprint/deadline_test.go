package sprint

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The member's median run wall is over its last DeadlineSamples ok attempts, newest
// finished first: an older attempt is not counted (deadline.go).
func TestTheMedianWallIsOverTheLastFiftyOkAttempts(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	put := func(i int, finished, wall string) {
		w.s.Fleet.Put(&Card{ID: fmt.Sprintf("p%d.w1", i), Row: "m1", Col: DoneOK, Rev: 1,
			Fields: map[string]string{"kind": "work", "primary": fmt.Sprintf("p%d", i), "attempt": "1", "ok": "yes", "finished": finished, FieldUsage: "wall=" + wall}})
	}
	// the oldest ran an hour; the fifty after it 100 s each
	put(0, "2030-01-01T00:00:00Z", "3600s")
	for i := 1; i <= DeadlineSamples; i++ {
		put(i, fmt.Sprintf("2030-01-01T01:%02d:00Z", i%60), "100s")
	}
	w.s.Fleet.cells = nil
	median, n := MemberMedianWall(w.s, "m1")
	assert.Equal(t, DeadlineSamples, n, "the last fifty")
	assert.Equal(t, 100.0, median, "the hour-long attempt is older than the window")
	assert.Equal(t, 600, w.s.memberDeadline("m1", 600), "the card's own deadline when it is the larger")
	assert.Equal(t, 300, w.s.memberDeadline("m1", 200), "three times the median when that is")
	for _, text := range []string{"0", "-1m", "25h", "soon"} {
		_, _, err := ParseDeadline(text)
		assert.Error(t, err, text)
	}
	secs, off, err := ParseDeadline("45m")
	assert.NoError(t, err)
	assert.Equal(t, 2700, secs)
	assert.False(t, off)
	_, off, err = ParseDeadline("default")
	assert.NoError(t, err)
	assert.True(t, off)
}

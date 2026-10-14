package friend

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A server that does not answer is asked again after a longer wait each time, not at the same
// cadence: over two minutes of deadlines the daemon asks about eight times, not once a second
// (the finding of 2026-10-14 in emma's log: "did not answer ... context deadline exceeded",
// each abandoned ask still run by a server that runs one verb at a time). This test uses only
// the daemon's surface from before the change, so it runs on the code before it and fails there.
func TestAServerThatDoesNotAnswerIsAskedWithBackoff(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	var mu sync.Mutex
	asks := 0
	down := true
	r.d.Held = func(context.Context) (Row, error) {
		mu.Lock()
		defer mu.Unlock()
		asks++
		if down {
			return Row{}, errors.New(`the sprint server at 127.0.0.1:6390 did not answer: Post "http://127.0.0.1:6390/verbs": context deadline exceeded`)
		}
		return Row{Reads: true, From: FromCards}, nil
	}
	count := func() int { mu.Lock(); defer mu.Unlock(); return asks }
	r.run(t, 120)
	failing := count()
	assert.LessOrEqual(t, failing, 10, "two minutes of deadlines are asked at 0,1,3,7,15,31,61,91 seconds, not once a second")
	assert.GreaterOrEqual(t, failing, 6, "and the server is still asked")

	// the server comes back: the first answer ends the back-off, and she is asked every loop again
	mu.Lock()
	down = false
	mu.Unlock()
	r.run(t, 40)
	assert.GreaterOrEqual(t, count()-failing, 10, "an answer ends the back-off: every loop again after the one wait still owed")
}

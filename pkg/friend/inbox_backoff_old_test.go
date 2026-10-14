package friend

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// A server that does not answer is asked again after a longer wait each time, not at the same
// cadence: over a hundred seconds of deadlines the daemon asks about eight times, not once a
// second (the finding of 2026-10-14 in emma's log: "did not answer ... context deadline
// exceeded", each abandoned ask still run by a server that runs one verb at a time); and the
// first answer ends the back-off, so she is asked every loop again. This test uses only the
// daemon's surface from before the change, so it runs on the code before it and fails there.
func TestAServerThatDoesNotAnswerIsAskedWithBackoff(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	var mu sync.Mutex
	failed, answered := 0, 0
	r.d.Held = func(context.Context) (Row, error) {
		r.mu.Lock()
		down := r.now.Sub(t0) < 100*time.Second
		r.mu.Unlock()
		mu.Lock()
		defer mu.Unlock()
		if down {
			failed++
			return Row{}, errors.New(`the sprint server at 127.0.0.1:6390 did not answer: Post "http://127.0.0.1:6390/verbs": context deadline exceeded`)
		}
		answered++
		return Row{Reads: true, From: FromCards}, nil
	}
	r.run(t, 160)
	mu.Lock()
	defer mu.Unlock()
	assert.LessOrEqual(t, failed, 10, "a hundred seconds of deadlines are asked at 0,1,3,7,15,31,61,91 seconds, not once a second")
	assert.GreaterOrEqual(t, failed, 6, "and the server is still asked")
	assert.GreaterOrEqual(t, answered, 30, "an answer ends the back-off: every loop again after the one wait still owed (the ask at 121 s)")
}

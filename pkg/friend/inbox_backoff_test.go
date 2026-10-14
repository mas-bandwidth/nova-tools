package friend

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInboxWaitDoublesFromTheLoopToTheBound(t *testing.T) {
	t.Parallel()
	want := []time.Duration{1, 2, 4, 8, 16, 30, 30, 30}
	for n, w := range want {
		assert.Equal(t, w*time.Second, inboxWait(n), "after %d unanswered", n)
	}
	assert.Equal(t, 30*time.Second, inboxWait(1<<20), "however many")
	assert.Equal(t, InboxEvery, inboxWait(0), "a healthy server is asked every loop, as before")
}

// A server that does not answer is asked again after a longer wait each time, not at the same
// cadence: over two minutes of deadlines the daemon asks a handful of times, not once a second
// (the finding of 2026-10-14 in emma's log: "did not answer ... context deadline exceeded",
// each abandoned ask still run by a server that runs one verb at a time).
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
	assert.LessOrEqual(t, failing, 10, "two minutes of deadlines are asked about nine times (1,2,4,8,16,30,30s), not once a second")
	assert.GreaterOrEqual(t, failing, 6, "and the server is still asked")

	// the server comes back: the first answer ends the back-off, and she is asked every loop again
	mu.Lock()
	down = false
	mu.Unlock()
	r.run(t, 40)
	assert.GreaterOrEqual(t, count()-failing, 10, "an answer ends the back-off: every loop again after the one wait still owed")
	assert.Equal(t, 0, r.d.inboxUnanswered)
}

// An answer that is a refusal is an answer: the server is not slow, and the wait stays one loop.
func TestARefusedAskIsNotAnUnansweredOne(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.d.Held = func(context.Context) (Row, error) {
		return Row{}, &NotServed{Why: "friend cards refused: the server runs the workers' verbs only"}
	}
	r.d.inboxUnanswered = 5
	r.run(t, 3)
	require.Equal(t, 0, r.d.inboxUnanswered, "a refusal ends the back-off")
}

package friend

import (
	"context"
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
	assert.Equal(t, InboxBackoffMax, inboxWait(1<<20), "however many")
	assert.Equal(t, InboxEvery, inboxWait(0), "a healthy server is asked every loop, as before")
}

// An answer that is a refusal is an answer: the server is not slow, and the wait is one loop.
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

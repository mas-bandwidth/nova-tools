package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// rejectedRig is a stream stopped by a refused push (the base moved under both pushes of
// a landing, standing for a protected-branch refusal), with a clock the test moves.
func rejectedRig(t *testing.T) (*landRig, *time.Time, []string) {
	t.Helper()
	r := newLandRig(t)
	clock := time.Date(2026, 10, 4, 10, 40, 0, 0, time.UTC)
	r.a.now = func() time.Time { return clock }
	r.ok("add --stream s1 --count 2")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n"), "s1-2": r.head("s1-2", "main", "b.txt", "b\n")}, "s1-1", "s1-2")
	return r, &clock, []string{"--repo-dir", r.clone, "--base", "main"}
}

func (r *landRig) openRejected() int {
	n := 0
	for _, line := range strings.Split(r.ok("inbox"), "\n") {
		if strings.Contains(line, "stream stopped: the merge queue rejected") && !strings.Contains(line, "answered by") {
			n++
		}
	}
	return n
}

// A stream stopped by a transient push refusal resumes by itself once the push succeeds:
// the push is refused through the landing's two tries, the stream stops with one
// judgment, and the loop's round after sprint.RejectedRetries[0] resumes it (closing
// the judgment) and lands it. No real time: the clock is the test's.
func TestAStreamStoppedByATransientPushRefusalResumesItselfWhenThePushSucceeds(t *testing.T) {
	t.Parallel()
	r, clock, more := rejectedRig(t)
	pushes := 0
	r.a.beforePush = func(int) {
		pushes++
		if pushes <= 2 {
			r.moveBase("main", "moved"+strings.Repeat("x", pushes)+".txt")
		}
	}
	var out bytes.Buffer
	ctx := context.Background()
	r.a.landRound(ctx, "mem:0", more, &out)
	assert.Equal(t, "stopped rejected", r.streamState("s1"), out.String())
	assert.Equal(t, 1, r.openRejected())

	*clock = clock.Add(sprint.RejectedRetries[0] - time.Second)
	r.a.landRound(ctx, "mem:0", more, &out)
	assert.Equal(t, "stopped rejected", r.streamState("s1"), "too early to retry")
	assert.Equal(t, 1, r.openRejected())
	assert.Equal(t, 2, pushes)

	*clock = clock.Add(time.Second)
	out.Reset()
	require.Equal(t, 0, r.a.landRound(ctx, "mem:0", more, &out), out.String())
	assert.Contains(t, out.String(), "RESUMED stream s1")
	assert.Contains(t, out.String(), "LAND OK stream=s1 cards=2")
	assert.Equal(t, "landed", r.streamState("s1"))
	assert.Equal(t, 0, r.openRejected(), "the resume closed the judgment")
	r.clean()
}

// A refusal that does not clear is retried len(sprint.RejectedRetries) times, each after
// its wait, and then the stream stays stopped with exactly one open judgment.
func TestAStreamStoppedByAPushRefusalThatDoesNotClearRaisesExactlyOneJudgment(t *testing.T) {
	t.Parallel()
	r, clock, more := rejectedRig(t)
	moves := 0
	r.a.beforePush = func(int) {
		moves++
		r.moveBase("main", "moved"+strings.Repeat("x", moves)+".txt")
	}
	var out bytes.Buffer
	ctx := context.Background()
	r.a.landRound(ctx, "mem:0", more, &out)
	for _, wait := range sprint.RejectedRetries {
		assert.Equal(t, "stopped rejected", r.streamState("s1"))
		assert.Equal(t, 1, r.openRejected())
		*clock = clock.Add(wait)
		r.a.landRound(ctx, "mem:0", more, &out)
	}
	assert.Equal(t, 1+len(sprint.RejectedRetries), strings.Count(out.String(), "LAND REFUSED"), out.String())
	assert.Equal(t, len(sprint.RejectedRetries), strings.Count(out.String(), "RESUMED stream s1"), out.String())
	assert.Equal(t, "stopped rejected", r.streamState("s1"))
	assert.Equal(t, 1, r.openRejected())

	before := moves
	*clock = clock.Add(24 * time.Hour)
	r.a.landRound(ctx, "mem:0", more, &out)
	assert.Equal(t, before, moves, "retries are spent: no further push")
	assert.Equal(t, "stopped rejected", r.streamState("s1"))
	assert.Equal(t, 1, r.openRejected())
	r.clean()
}

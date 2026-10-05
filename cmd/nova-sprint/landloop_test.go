package main

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The server lands what the readers passed (run --land): a round lands every stream
// with cards queued to merge, as the sprint's coordinator, and says so; a round with
// nothing queued runs no land and says nothing.
func TestTheServerLandsWhatIsQueued(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	more := []string{"--repo-dir", r.clone, "--base", "main"}
	var out bytes.Buffer
	assert.Equal(t, 0, r.a.landRound(context.Background(), "mem:0", more, &out))
	assert.Empty(t, out.String(), "nothing is queued: nothing is said")

	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n"), "s1-2": r.head("s1-2", "main", "b.txt", "b\n")}, "s1-1", "s1-2")
	pushes := 0
	r.a.beforePush = func(int) {
		pushes++
		// land's git runs outside the server's line of control: a tick or a worker's
		// batch is not held for as long as a push takes
		require.True(t, r.a.serial.TryLock(), "the server's lock is held across land's push")
		r.a.serial.Unlock()
	}
	require.Equal(t, 0, r.a.landRound(context.Background(), "mem:0", more, &out), out.String())
	assert.Equal(t, 1, pushes)
	assert.Contains(t, out.String(), "LAND OK stream=s1 cards=2")
	assert.NotContains(t, out.String(), "LAND DONE")
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
	assert.Contains(t, r.ok("log"), "by coordinator", "the landing is the coordinator's")

	out.Reset()
	assert.Equal(t, 0, r.a.landRound(context.Background(), "mem:0", more, &out))
	assert.Empty(t, out.String(), "landed: the next round has nothing to do")
	r.clean()
}

// A failing round is said when the failure begins, not every round: ten rounds with the
// store down print its line once; a round that does not fail clears it, and the same
// failure coming back is said again; a different failure is said once.
func TestALandFailureIsSaidOnceUntilItChangesOrClears(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	a, up := ta.a, ta.a.backend
	outage := errors.New("injected store outage")
	down := func(context.Context, string, sprint.Names) (store.Backend, error) { return nil, outage }
	var out bytes.Buffer
	rounds := func(n int) int {
		out.Reset()
		for range n {
			assert.Equal(t, 2, a.landRound(context.Background(), "mem:0", nil, &out))
		}
		return strings.Count(out.String(), "LAND FAILED")
	}
	a.backend = down
	assert.Equal(t, 1, rounds(10), out.String())
	outage = errors.New("another outage")
	assert.Equal(t, 1, rounds(10), "a different failure is said once: %s", out.String())
	assert.Contains(t, out.String(), "another outage")

	a.backend = up
	out.Reset()
	assert.Equal(t, 0, a.landRound(context.Background(), "mem:0", nil, &out))
	assert.Empty(t, out.String(), "the store is back and nothing is queued: nothing is said")
	a.backend = down
	assert.Equal(t, 1, rounds(3), "the failure came back after it cleared: said again")
}

// A push the remote refuses stops the stream (the merge queue rejected), and the loop
// resumes it once by itself: the next round lands the batch when the refusal is gone, and
// when it is not the stream stays stopped with the one judgment, however many rounds
// follow (docs/SPEC-SPRINT.md, a stopped stream; no real time, the rounds are driven).
func TestAStreamStoppedByATransientPushRefusalResumesOrRaisesOneJudgment(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		refuses int // pushes refused before the remote takes one; 0 refuses always
		state   string
		places  string
		open    int
	}{
		{"refuses once", 2, "landed", "landed/merged", 0},
		{"refuses always", 0, "stopped rejected", "merging/queued", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			r.ok("add --stream s1 --count 2")
			r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n"), "s1-2": r.head("s1-2", "main", "b.txt", "b\n")}, "s1-1", "s1-2")
			more := []string{"--repo-dir", r.clone, "--base", "main"}
			pushes := 0
			r.a.beforePush = func(int) {
				pushes++
				if tc.refuses == 0 || pushes <= tc.refuses {
					r.moveBase("main", "moved"+strconv.Itoa(pushes)+".txt")
				}
			}
			var out bytes.Buffer
			assert.Equal(t, 1, r.a.landRound(context.Background(), "mem:0", more, &out), out.String())
			assert.Equal(t, "stopped rejected", r.streamState("s1"), "the first refusal stops the stream")
			for range 5 {
				r.a.landRound(context.Background(), "mem:0", more, &out)
			}
			assert.Equal(t, 1, strings.Count(out.String(), "LAND RESUMED stream=s1"), "resumed once: %s", out.String())
			assert.Equal(t, tc.state, r.streamState("s1"), out.String())
			assert.Equal(t, map[string]string{"s1-1": tc.places, "s1-2": tc.places}, r.places("s1-1", "s1-2"))
			inbox := r.ok("inbox")
			assert.Equal(t, tc.open, strings.Count(inbox, "JUDGMENT "), "judgments open: %s", inbox)
			if tc.refuses == 0 {
				assert.Equal(t, 4, pushes, "two rounds of two pushes, and no more")
			}
			r.clean()
		})
	}
}

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

// A stream stopped by a push refused twice (a rule on the base, a base that moved) is
// resumed by the loop once, and lands when the push then succeeds, with no person and no
// real time; a push refused again leaves the stream stopped with exactly one judgment, and
// a further round does not resume it (docs/SPEC-SPRINT.md, the stream lifecycle).
func TestAStreamStoppedByATransientPushRefusalResumesItselfOrRaisesOneJudgment(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		refused int // pushes refused before one succeeds
		state   string
		places  string
		pushes  int
		judged  int
	}{
		{"refused once then lands", 2, "landed", "landed/merged", 3, 0},
		{"refused again", 4, "stopped rejected", "merging/queued", 4, 1},
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
				if pushes <= tc.refused {
					r.moveBase("main", "moved"+strconv.Itoa(pushes)+".txt")
				}
			}
			var out bytes.Buffer
			assert.Equal(t, 1, r.a.landRound(context.Background(), "mem:0", more, &out), out.String())
			require.Equal(t, "stopped rejected", r.streamState("s1"), "the first round stops on the refusal")
			for range 3 {
				r.a.landRound(context.Background(), "mem:0", more, &out)
			}
			assert.Equal(t, tc.state, r.streamState("s1"), out.String())
			assert.Equal(t, map[string]string{"s1-1": tc.places, "s1-2": tc.places}, r.places("s1-1", "s1-2"))
			assert.Equal(t, tc.pushes, pushes, "a stream rejected again is not resumed again")
			assert.Equal(t, tc.judged, strings.Count(r.ok("inbox"), "JUDGMENT "), "judgments open")
			r.clean()
		})
	}
}

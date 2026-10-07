package sprint

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeQueue is a forge's merge queues as the lander asks them, and no forge: the branches
// whose queue holds a group, an error every ask returns when set, and the asks made.
type fakeQueue struct {
	mu    sync.Mutex
	held  map[string]bool
	err   error
	asked []string
}

func (q *fakeQueue) HoldsGroup(_ context.Context, repo, branch string) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.asked = append(q.asked, repo+" "+branch)
	return q.held[branch], q.err
}

func (q *fakeQueue) set(branch string, held bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.held[branch] = held
}

// The lander pauses landing onto a branch while that branch's merge queue holds a group,
// and lands again when it clears; a queue it cannot read pauses it too (an unreadable
// queue is not an empty one); another branch's group pauses nothing; an open merge window
// pauses every landing for its duration with its reason shown, and asks no queue; with no
// queue to ask (a dry run) only the window pauses (docs/SPEC-SPRINT.md section 7, the
// lander's pause).
func TestLanderPausesWhileTheQueueHoldsAGroup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 18, 0, 0, 0, time.UTC)
	const repo = "https://example.com/o/r.git"
	t.Run("a group held on the branch pauses it, and clearing it resumes", func(t *testing.T) {
		t.Parallel()
		q := &fakeQueue{held: map[string]bool{"dev": true}}
		why := LandPause(ctx, MergeWindow{}, now, q, repo, "dev")
		assert.Contains(t, why, "paused")
		assert.Contains(t, why, "the merge queue of dev holds a group")
		assert.Contains(t, why, "the cards stay queued")
		q.set("dev", false)
		assert.Empty(t, LandPause(ctx, MergeWindow{}, now, q, repo, "dev"), "the queue cleared: the landing goes on")
		assert.Equal(t, []string{repo + " dev", repo + " dev"}, q.asked, "the queue is asked of the repository and branch landed onto")
	})
	t.Run("a group on another branch pauses nothing", func(t *testing.T) {
		t.Parallel()
		q := &fakeQueue{held: map[string]bool{"main": true}}
		assert.Empty(t, LandPause(ctx, MergeWindow{}, now, q, repo, "dev"))
	})
	t.Run("a queue that cannot be read pauses, naming why", func(t *testing.T) {
		t.Parallel()
		q := &fakeQueue{held: map[string]bool{}, err: errors.New("the forge said 502")}
		why := LandPause(ctx, MergeWindow{}, now, q, repo, "dev")
		assert.Contains(t, why, "paused")
		assert.Contains(t, why, "the merge queue of dev could not be read")
		assert.Contains(t, why, "the forge said 502")
	})
	t.Run("an open window pauses every branch with its reason, and asks no queue", func(t *testing.T) {
		t.Parallel()
		q := &fakeQueue{held: map[string]bool{}}
		w := MergeWindow{Until: now.Add(10 * time.Minute), Reason: "the release merges by hand"}
		why := LandPause(ctx, w, now, q, repo, "dev")
		assert.Contains(t, why, "paused")
		assert.Contains(t, why, "a merge window is open until 2026-10-04T18:10:00Z")
		assert.Contains(t, why, "the release merges by hand")
		assert.Empty(t, q.asked, "a window open pauses before any forge is asked")
		assert.Empty(t, LandPause(ctx, w, now.Add(10*time.Minute), q, repo, "dev"), "the window closes at its end")
	})
	t.Run("no queue to ask: only the window pauses", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, LandPause(ctx, MergeWindow{}, now, nil, repo, "dev"))
		why := LandPause(ctx, MergeWindow{Until: now.Add(time.Minute), Reason: "r"}, now, nil, repo, "dev")
		assert.True(t, strings.HasPrefix(why, "paused: a merge window is open"), why)
	})
}

// merge-window open writes the window as the merge table's properties, guarded on what was
// read: its end (now plus --for) and its reason, which the lander reads back; refused whole,
// nothing written, naming every problem: another actor, a duration that is none or not above
// zero, no reason, a reason over the text bound (docs/SPEC-SPRINT.md section 7).
func TestMergeWindowOpenPausesForItsDurationWithTheReason(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 4, 18, 0, 0, 0, time.UTC)
	snap := func(props map[string]string) *Snapshot {
		s := &Snapshot{Now: now, Coordinator: "coord", Work: NewTable(Work), Merge: NewTable(Merge)}
		s.Merge.SetProps(props)
		return s
	}
	t.Run("opened: the end and the reason, read back as the window", func(t *testing.T) {
		t.Parallel()
		p := MergeWindowOpen(snap(map[string]string{PropMergeWindowReason: "an older one"}), MergeWindowReq{For: "10m", Reason: "the release merges by hand", Who: "coord"})
		require.Empty(t, p.Refused)
		require.Len(t, p.Props, 2)
		assert.Equal(t, PropWrite{Table: Merge, Name: PropMergeWindowUntil, Value: "2026-10-04T18:10:00Z", WasAbsent: true}, p.Props[0])
		assert.Equal(t, PropWrite{Table: Merge, Name: PropMergeWindowReason, Value: "the release merges by hand", Was: "an older one"}, p.Props[1])
		require.Len(t, p.Units, 1)
		assert.Equal(t, "merge-window", p.Units[0].Key)
		assert.Equal(t, "merge window open until 2026-10-04T18:10:00Z (the release merges by hand): landing pauses", p.Units[0].Moved)
		after := snap(map[string]string{PropMergeWindowUntil: p.Props[0].Value, PropMergeWindowReason: p.Props[1].Value})
		w, err := after.MergeWindow()
		require.NoError(t, err)
		assert.Equal(t, MergeWindow{Until: now.Add(10 * time.Minute), Reason: "the release merges by hand"}, w)
	})
	t.Run("none opened: no window", func(t *testing.T) {
		t.Parallel()
		w, err := snap(nil).MergeWindow()
		require.NoError(t, err)
		assert.Equal(t, MergeWindow{}, w)
	})
	t.Run("an end that cannot be read is an error, never no window", func(t *testing.T) {
		t.Parallel()
		_, err := snap(map[string]string{PropMergeWindowUntil: "soon"}).MergeWindow()
		assert.ErrorContains(t, err, "merge_window_until")
	})
	for _, c := range []struct {
		name string
		req  MergeWindowReq
		why  []string
	}{
		{"another actor", MergeWindowReq{For: "10m", Reason: "r", Who: "someone"}, []string{"is the coordinator's alone: coord"}},
		{"no duration and no reason, named together", MergeWindowReq{Who: "coord"}, []string{"--for wants a duration above zero", "--reason wants"}},
		{"a duration not above zero", MergeWindowReq{For: "-5m", Reason: "r", Who: "coord"}, []string{"--for wants a duration above zero (10m, 1h); found -5m"}},
		{"a reason over the bound", MergeWindowReq{For: "10m", Reason: strings.Repeat("x", MaxCardTextBytes+1), Who: "coord"}, []string{"--reason is 8193 bytes, over the bound of 8192"}},
	} {
		t.Run("refused whole: "+c.name, func(t *testing.T) {
			t.Parallel()
			p := MergeWindowOpen(snap(nil), c.req)
			require.Len(t, p.Refused, 1, "%+v", p)
			assert.Equal(t, "merge-window", p.Refused[0].Key)
			for _, w := range c.why {
				assert.Contains(t, p.Refused[0].Why, w)
			}
			assert.Empty(t, p.Props, "a refusal writes nothing")
			assert.Empty(t, p.Units, "a refusal writes nothing")
		})
	}
}

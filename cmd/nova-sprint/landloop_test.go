package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nova-tools/internal/sprint"
	"github.com/nova-tools/internal/sprint/store"
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

func TestLandOnceRollsBackOnFailedLandInWindowAndNotOutside(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 s1-1 --one")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n")}, "s1-1")

	dir := t.TempDir()
	target := filepath.Join(dir, "nova-sprint")
	candidate := filepath.Join(dir, "nova-sprint-candidate")

	require.NoError(t, os.WriteFile(target, []byte("binary-v1"), 0o755))
	require.NoError(t, os.WriteFile(candidate, []byte("binary-v2"), 0o755))

	t0 := time.Date(2026, 10, 4, 16, 0, 0, 0, time.UTC)
	now := t0
	r.a.now = func() time.Time { return now }
	r.a.executable = func() (string, error) { return target, nil }

	// Switch target to candidate with rollback enabled, 15m window
	err := sprint.ServerSwitch(context.Background(), sprint.ServerSwitchOptions{
		Binary:   candidate,
		Target:   target,
		Rollback: true,
		Window:   15 * time.Minute,
		Now:      func() time.Time { return now },
	})
	require.NoError(t, err)

	// Subtest 1: Land fails inside the window (5 minutes in) => rolls back
	now = t0.Add(5 * time.Minute)
	var out bytes.Buffer
	code, _ := r.a.landOnce(context.Background(), "mem:0", []string{"--repo-dir", filepath.Join(dir, "missing"), "--base", "main"}, &out)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, out.String(), "SERVER ROLLBACK land failed within switch window")

	// Target must be restored to binary-v1
	content, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "binary-v1", string(content), "failed land in window must roll back target to binary-v1")

	// Subtest 2: Switch again, but land fails outside window (20 minutes in) => does not roll back
	require.NoError(t, os.WriteFile(target, []byte("binary-v1"), 0o755))
	now = t0
	err = sprint.ServerSwitch(context.Background(), sprint.ServerSwitchOptions{
		Binary:   candidate,
		Target:   target,
		Rollback: true,
		Window:   15 * time.Minute,
		Now:      func() time.Time { return now },
	})
	require.NoError(t, err)

	now = t0.Add(20 * time.Minute)
	out.Reset()
	code, _ = r.a.landOnce(context.Background(), "mem:0", []string{"--repo-dir", filepath.Join(dir, "missing"), "--base", "main"}, &out)
	assert.NotEqual(t, 0, code)
	assert.NotContains(t, out.String(), "SERVER ROLLBACK land failed within switch window")

	content, err = os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "binary-v2", string(content), "failed land outside window must not roll back target")
}

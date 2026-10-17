package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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

// A later successful push retries a rejected stream, closes its one judgment,
// and records the queued batch (docs/SPEC-SPRINT.md, land).
func TestTheLandLoopResumesARejectedStreamAfterThePushSucceeds(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.git(r.worker, "push", "-q", "origin", "main:refs/heads/alt")
	briefDir := t.TempDir()
	brief := func(id, base string) string {
		path := filepath.Join(briefDir, id+".md")
		require.NoError(t, os.WriteFile(path, []byte(passingBrief("REPO: "+r.remote+"\nBASE: "+base+"\n\nWrite "+id+".txt.")), 0o600))
		return path
	}
	r.ok("add --stream s1 --brief-file " + brief("s1-1", "main") + " --brief-file " + brief("s1-2", "alt"))
	r.queued(map[string]string{
		"s1-1": r.head("s1-1", "main", "s1-1.txt", "one\n"),
		"s1-2": r.head("s1-2", "alt", "s1-2.txt", "two\n"),
	}, "s1-1", "s1-2")
	firstRound, repeatedRefusal, pushes := true, false, 0
	r.a.beforePush = func(int) {
		pushes++
		if firstRound || repeatedRefusal {
			r.moveBase("main", "moved"+strconv.Itoa(pushes)+".txt")
		}
	}
	more := []string{"--repo-dir", r.clone, "--base", "main"}
	var out bytes.Buffer
	assert.Equal(t, 1, r.a.landRound(context.Background(), "mem:0", more, &out), out.String())
	assert.Equal(t, "stopped rejected", r.streamState("s1"))
	assert.Equal(t, 1, strings.Count(r.ok("inbox"), "stream stopped: the merge queue rejected"), "repeated refusal keeps one judgment open")
	assert.Equal(t, 2, pushes, "one rejection and one rebuild refusal open the judgment")

	firstRound, repeatedRefusal = false, true
	out.Reset()
	assert.Equal(t, 1, r.a.landRound(context.Background(), "mem:0", more, &out), out.String())
	assert.Equal(t, 4, pushes, "another rejected retry is attempted once with one rebuild")
	assert.Equal(t, 1, strings.Count(r.ok("inbox"), "stream stopped: the merge queue rejected"), "repeated refusal keeps the same judgment open")

	repeatedRefusal = false
	out.Reset()
	assert.Equal(t, 0, r.a.landRound(context.Background(), "mem:0", more, &out), out.String())
	assert.Equal(t, 6, pushes, "the later round retries both distinct base batches")
	assert.Contains(t, out.String(), "LAND OK stream=s1 cards=1 base=main")
	assert.Contains(t, out.String(), "LAND OK stream=s1 cards=1 base=alt")
	assert.Equal(t, "landed", r.streamState("s1"))
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
	assert.NotRegexp(t, `(?m)^JUDGMENT .*stream stopped: the merge queue rejected`, r.ok("inbox"), "successful retry closes the original open judgment")
	r.clean()
}

// Only rejected-push stops are retried by land; other causes remain for the
// coordinator, even when a stream is named explicitly (docs/SPEC-SPRINT.md).
func TestTheLandLoopDoesNotRetryOtherStoppedStreams(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		stop string
	}{
		{"red", "merge --stream s1 --red --suspect s1-1"},
		{"conflict", "merge --stream s1 --conflict s1-1"},
		{"base", "merge --stream s1 --base-red 'base gate failed'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			r.ok("add --stream s1 --count 2")
			r.queued(map[string]string{
				"s1-1": r.head("s1-1", "main", "a.txt", "a\n"),
				"s1-2": r.head("s1-2", "main", "b.txt", "b\n"),
			}, "s1-1", "s1-2")
			r.ok(tc.stop)
			pushes := 0
			r.a.beforePush = func(int) { pushes++ }
			var out bytes.Buffer
			assert.Equal(t, 0, r.a.landRound(context.Background(), "mem:0", nil, &out), out.String())
			assert.Empty(t, out.String(), "the loop excludes non-rejected stops")
			assert.Equal(t, 1, r.a.cmdLand([]string{"--stream", "s1", "--repo-dir", r.clone, "--base", "main"}, &out, &out))
			assert.Contains(t, out.String(), "stopped")
			assert.Zero(t, pushes, "an explicit stream name does not bypass its stop")
			assert.Equal(t, "stopped "+tc.name, r.streamState("s1"))
			r.clean()
		})
	}
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

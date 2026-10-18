package main

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The push repository's tidy (memberpush.go, tidyEvery): push.git keeps gc.auto=0 and every
// push drops its refs, so what it keeps is unreachable; on 2026-10-17 one bench's held 4.4 GB
// in 195 packs. A push's end repacks and prunes it once tidyEvery has passed, and never while
// another push holds it. The git is scripted and the clock is the test's, from 1000 s.

// tidyCalls is the repack and prune calls the scripted git saw, each "<dir> <argv>".
func tidyCalls(h *pushHarness) []string {
	h.git.mu.Lock()
	defer h.git.mu.Unlock()
	var out []string
	for i, a := range h.git.argv {
		if a[0] == "repack" || a[0] == "prune" {
			out = append(out, h.git.calls[i].C+" "+strings.Join(a, " "))
		}
	}
	return out
}

func TestThePushRepositoryIsTidiedAtMostHourlyAfterAPush(t *testing.T) {
	t.Parallel()
	h := newPushHarness(t)
	g := h.pusher()
	now := time.Unix(1000, 0)
	g.now = func() time.Time { return now }
	push := func() {
		t.Helper()
		require.Equal(t, member.Push{Sha: h.git.head}, g.Push(h.p, member.Result{Head: h.git.head}))
	}
	push() // the first push starts the count
	assert.Empty(t, tidyCalls(h), "the first push of the process tidies nothing")
	now = now.Add(tidyEvery - time.Second)
	push()
	assert.Empty(t, tidyCalls(h), "a push before tidyEvery has passed tidies nothing")
	now = now.Add(time.Second)
	push()
	repo := filepath.Join(h.root, "push.git")
	assert.Equal(t, []string{repo + " repack -adq", repo + " prune --expire=now"}, tidyCalls(h),
		"a push's end once tidyEvery has passed repacks the push repository and prunes what no ref names")
	h.git.mu.Lock()
	last := h.git.argv[len(h.git.argv)-1]
	pushAt := slices.IndexFunc(h.git.argv, func(a []string) bool { return a[0] == "push" })
	repackAt := slices.IndexFunc(h.git.argv, func(a []string) bool { return a[0] == "repack" })
	h.git.mu.Unlock()
	assert.Equal(t, "prune", last[0], "the tidy is the push's last git")
	assert.Less(t, pushAt, repackAt, "the tidy runs after the push to origin")
	now = now.Add(tidyEvery - time.Second)
	push()
	assert.Len(t, tidyCalls(h), 2, "the next tidy waits tidyEvery from the last one")
}

func TestThePushRepositoryIsNeverTidiedWhileAnotherPushHoldsIt(t *testing.T) {
	t.Parallel()
	h := newPushHarness(t)
	g := h.pusher()
	now := time.Unix(1000, 0)
	g.now = func() time.Time { return now }
	require.Equal(t, member.Push{Sha: h.git.head}, g.Push(h.p, member.Result{Head: h.git.head}))
	now = now.Add(2 * tidyEvery)
	g.pushing.RLock() // another push is in flight
	require.Equal(t, member.Push{Sha: h.git.head}, g.Push(h.p, member.Result{Head: h.git.head}))
	assert.Empty(t, tidyCalls(h), "a due tidy is skipped while another push holds the repository")
	g.pushing.RUnlock()
	require.Equal(t, member.Push{Sha: h.git.head}, g.Push(h.p, member.Result{Head: h.git.head}))
	assert.Len(t, tidyCalls(h), 2, "the skipped tidy runs at the end of the next push that finds the repository free")
}

func TestAFailedTidyIsANoteAndThePushStands(t *testing.T) {
	t.Parallel()
	h := newPushHarness(t)
	g := h.pusher()
	var notes strings.Builder
	g.notes = &notes
	g.git = func(ctx context.Context, o gitrun.Options, args ...string) (gitrun.Result, error) {
		if args[0] == "repack" {
			return gitrun.Result{Stderr: []byte("fatal: injected repack failure\n")}, errors.New("exit status 128")
		}
		return h.git.run(ctx, o, args...)
	}
	now := time.Unix(1000, 0)
	g.now = func() time.Time { return now }
	require.Equal(t, member.Push{Sha: h.git.head}, g.Push(h.p, member.Result{Head: h.git.head}))
	now = now.Add(tidyEvery)
	assert.Equal(t, member.Push{Sha: h.git.head}, g.Push(h.p, member.Result{Head: h.git.head}), "a tidy that fails never changes the push it follows")
	assert.Contains(t, notes.String(), "NOTE push repository")
	assert.Contains(t, notes.String(), "git repack: fatal: injected repack failure")
	assert.Empty(t, tidyCalls(h), "a failed repack is not followed by a prune")
}

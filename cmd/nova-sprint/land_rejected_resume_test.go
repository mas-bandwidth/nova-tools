package main

import (
	"bytes"
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rejectedRig is a stream of two queued cards whose push the remote refuses
// while refuse(n) says so, n counting every push the loop makes.
func rejectedRig(t *testing.T, refuse func(n int) bool) (*landRig, func() (int, string), *int) {
	t.Helper()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n"), "s1-2": r.head("s1-2", "main", "b.txt", "b\n")}, "s1-1", "s1-2")
	pushes := 0
	r.a.beforePush = func(int) {
		pushes++
		if refuse(pushes) {
			r.moveBase("main", "moved"+strconv.Itoa(pushes)+".txt")
		}
	}
	round := func() (int, string) {
		var out bytes.Buffer
		code := r.a.landRound(context.Background(), "mem:0", []string{"--repo-dir", r.clone, "--base", "main"}, &out)
		return code, out.String()
	}
	return r, round, &pushes
}

// A stream the push refusal stopped resumes by itself when the next push
// succeeds, and the judgment the stop raised closes (docs/SPEC-SPRINT.md,
// the stream lifecycle: a stopped stream).
func TestAStreamStoppedByATransientPushRefusalResumesWhenThePushSucceeds(t *testing.T) {
	t.Parallel()
	r, round, pushes := rejectedRig(t, func(n int) bool { return n <= 2 })

	code, out := round()
	require.Equal(t, 1, code, out)
	require.Equal(t, "stopped rejected", r.streamState("s1"))
	require.Contains(t, r.ok("inbox"), "judgments=1 ")

	code, out = round()
	assert.Equal(t, 0, code, out)
	assert.Equal(t, 3, *pushes, "the retry pushed once")
	assert.Equal(t, "landed", r.streamState("s1"))
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
	assert.Contains(t, r.ok("inbox"), "judgments=0 ")
}

// A refusal that does not pass is retried once, then left to the coordinator
// as the one judgment the stop raised: no loop of resumes.
func TestAStreamStoppedByAPersistentPushRefusalIsRetriedOnceAndRaisesOneJudgment(t *testing.T) {
	t.Parallel()
	r, round, pushes := rejectedRig(t, func(int) bool { return true })

	round()
	require.Equal(t, 2, *pushes)
	round()
	assert.Equal(t, 4, *pushes, "one retry, two attempts")
	for range 3 {
		round()
	}
	assert.Equal(t, 4, *pushes, "no retry after the first")
	assert.Equal(t, "stopped rejected", r.streamState("s1"))
	assert.Contains(t, r.ok("inbox"), "judgments=1 ")
}

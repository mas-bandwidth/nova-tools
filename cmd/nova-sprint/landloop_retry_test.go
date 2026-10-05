package main

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// openRejections is the open judgments of the stream stopped on a rejected push.
func openRejections(out string) int {
	n := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "JUDGMENT") && strings.Contains(l, sprint.NRejected) {
			n++
		}
	}
	return n
}

// A stream stopped by a transient push refusal resumes by itself: the loop lets
// LandRetryAfter pass, resumes the stream and lands it, and the stop's judgment
// closes with the resume; before that time the stream stays stopped and the one
// judgment stays open (docs/SPEC-SPRINT.md, the stream lifecycle).
func TestAStreamStoppedByATransientPushRefusalResumesByItself(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n"), "s1-2": r.head("s1-2", "main", "b.txt", "b\n")}, "s1-1", "s1-2")
	refused := 2 // both pushes of the first landing: the attempt and its rebuild
	r.a.beforePush = func(attempt int) {
		if refused > 0 {
			refused--
			r.moveBase("main", "moved"+strconv.Itoa(attempt)+".txt")
		}
	}
	more := []string{"--repo-dir", r.clone, "--base", "main"}
	var out bytes.Buffer
	require.NotEqual(t, 0, r.a.landRound(context.Background(), "mem:0", more, &out), out.String())
	require.Equal(t, "stopped rejected", r.streamState("s1"))

	out.Reset()
	r.a.landRound(context.Background(), "mem:0", more, &out)
	assert.Equal(t, "stopped rejected", r.streamState("s1"), "before LandRetryAfter the stream stays stopped: %s", out.String())
	assert.Equal(t, 1, openRejections(r.ok("inbox")), "one judgment while it waits")

	r.a.sleep(sprint.LandRetryAfter)
	out.Reset()
	require.Equal(t, 0, r.a.landRound(context.Background(), "mem:0", more, &out), out.String())
	assert.Contains(t, out.String(), "LAND OK stream=s1 cards=2")
	assert.Equal(t, "landed", r.streamState("s1"))
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
	assert.Zero(t, openRejections(r.ok("inbox")), "the resume closed the judgment")
	r.clean()
}

// A push that keeps refusing is retried LandRetries times, LandRetryAfter apart,
// and then raises its one judgment and waits for a person: never a loop.
func TestAStreamWhosePushKeepsRefusingRaisesOneJudgmentAfterTheRetries(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n"), "s1-2": r.head("s1-2", "main", "b.txt", "b\n")}, "s1-1", "s1-2")
	pushes := 0
	r.a.beforePush = func(int) {
		pushes++
		r.moveBase("main", "moved"+strconv.Itoa(pushes)+".txt")
	}
	more := []string{"--repo-dir", r.clone, "--base", "main"}
	var out bytes.Buffer
	for range sprint.LandRetries + 1 {
		r.a.landRound(context.Background(), "mem:0", more, &out)
		r.a.sleep(sprint.LandRetryAfter)
	}
	assert.Equal(t, 2*(1+sprint.LandRetries), pushes, "the first landing and each retry push twice, and no more")
	assert.Equal(t, "stopped rejected", r.streamState("s1"))
	assert.Equal(t, 1, openRejections(r.ok("inbox")), "exactly one judgment")
	r.clean()
}

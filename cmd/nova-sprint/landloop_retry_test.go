package main

import (
	"bytes"
	"context"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openJudgments is how many judgments the inbox holds open: its history lines (DECIDED)
// name a closed one too, so the count is the inbox's own.
func openJudgments(r *landRig) int {
	out := r.ok("inbox")
	m := regexp.MustCompile(`INBOX OK judgments=(\d+)`).FindStringSubmatch(out)
	require.NotNil(r.t, m, out)
	n, err := strconv.Atoi(m[1])
	require.NoError(r.t, err)
	return n
}

// refuseRounds makes the next n landings' pushes refuse, both attempts of each (the base
// moves under the push, as a protected branch's GH006 refuses it): the stream stops, rejected.
func refuseRounds(r *landRig, n int) *int {
	rounds := new(int)
	r.a.beforePush = func(attempt int) {
		if attempt == 1 {
			*rounds++
		}
		if *rounds <= n {
			r.moveBase("main", "moved"+strconv.Itoa(*rounds)+"-"+strconv.Itoa(attempt)+".txt")
		}
	}
	return rounds
}

// A stream stopped by a push the remote refused once stays stopped until a person
// resumes it (libs, GH006 at 10:40, found at 11:20): the loop now resumes it by itself
// once the retry wait has passed, and the push that then succeeds lands it, with no
// judgment left open. Until that wait has passed the stream stays stopped, under its one
// judgment. No real time: the clock is the twin's.
func TestAStreamStoppedByATransientPushRefusalResumesItselfOnceThePushSucceeds(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n"), "s1-2": r.head("s1-2", "main", "b.txt", "b\n")}, "s1-1", "s1-2")
	more := []string{"--repo-dir", r.clone, "--base", "main"}
	refuseRounds(r, 1)
	var out bytes.Buffer

	r.a.landRound(context.Background(), "mem:0", more, &out)
	require.Equal(t, "stopped rejected", r.streamState("s1"), out.String())
	assert.Equal(t, 1, openJudgments(r))

	out.Reset()
	r.a.landRound(context.Background(), "mem:0", more, &out)
	assert.Equal(t, "stopped rejected", r.streamState("s1"), "the retry wait has not passed: it stays stopped")
	assert.Equal(t, 1, openJudgments(r), "one judgment, not one a round")

	r.a.sleep(PushRetryAfter)
	out.Reset()
	assert.Equal(t, 0, r.a.landRound(context.Background(), "mem:0", more, &out), out.String())
	assert.Contains(t, out.String(), "LAND OK stream=s1 cards=2")
	assert.Equal(t, "landed", r.streamState("s1"))
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
	assert.Zero(t, openJudgments(r), "resumed: its judgment is closed")
	r.clean()
}

// A push the remote keeps refusing is retried PushRetries times, each after a longer wait,
// and then the stream stays stopped under exactly one open judgment for a person.
func TestAStreamWhosePushIsRefusedForGoodStaysStoppedUnderOneJudgment(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 s1-1 --one")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n")}, "s1-1")
	more := []string{"--repo-dir", r.clone, "--base", "main"}
	landings := refuseRounds(r, 100)
	var out bytes.Buffer
	r.a.landRound(context.Background(), "mem:0", more, &out)
	for i := range PushRetries + 3 {
		wait := PushRetryAfter << i
		for range 5 {
			r.a.sleep(wait)
			r.a.landRound(context.Background(), "mem:0", more, &out)
		}
		assert.Equal(t, "stopped rejected", r.streamState("s1"), "round %d", i)
		assert.Equal(t, 1, openJudgments(r), "round %d", i)
	}
	assert.Equal(t, 1+PushRetries, *landings, "the first landing and each retry, no more: %s", out.String())
	r.clean()
}

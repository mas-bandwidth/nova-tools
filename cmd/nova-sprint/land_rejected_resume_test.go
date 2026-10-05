package main

import (
	"bytes"
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A stream stopped by a transient push refusal resumes by itself once a push
// succeeds, and while the push keeps refusing it holds exactly one judgment
// (docs/SPEC-SPRINT.md, the stream lifecycle: a stop by a rejected push). The
// server's land loop drives it round by round, no clock: the push is refused in
// rounds one and two (the base moves under both attempts of a round), and
// accepted in round three.
func TestAStreamStoppedByARejectedPushResumesWhenThePushSucceeds(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n"), "s1-2": r.head("s1-2", "main", "b.txt", "b\n")}, "s1-1", "s1-2")
	more := []string{"--repo-dir", r.clone, "--base", "main"}
	refusing, moves := true, 0
	r.a.beforePush = func(int) {
		if refusing {
			moves++
			r.moveBase("main", "moved"+strconv.Itoa(moves)+".txt")
		}
	}
	rejections := func() int {
		var in struct{ Judgments []inboxJudgment }
		r.json("inbox", &in)
		n := 0
		for _, j := range in.Judgments {
			if j.Type == sprint.NRejected {
				n++
			}
		}
		return n
	}
	var out bytes.Buffer
	for round := 1; round <= 2; round++ {
		r.a.landFailed = ""
		r.a.landRound(context.Background(), "mem:0", more, &out)
		assert.Equal(t, "stopped rejected", r.streamState("s1"), "round %d: %s", round, out.String())
		assert.Equal(t, 1, rejections(), "round %d: a push that keeps refusing holds one judgment", round)
		assert.Equal(t, map[string]string{"s1-1": "merging/queued", "s1-2": "merging/queued"}, r.places("s1-1", "s1-2"))
	}

	refusing = false
	out.Reset()
	require.Equal(t, 0, r.a.landRound(context.Background(), "mem:0", more, &out), out.String())
	assert.Contains(t, out.String(), "LAND OK stream=s1 cards=2")
	assert.Equal(t, "landed", r.streamState("s1"), "the push succeeded: the machine resumed the stream, no person")
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
	assert.Zero(t, rejections(), "the push succeeded: the stop's judgment is closed")
	r.clean()
}

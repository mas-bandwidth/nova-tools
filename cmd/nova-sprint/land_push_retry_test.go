package main

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// openRefusals is the open judgments of the inbox that the merge queue's rejection raised:
// the inbox's own count, its HAPPENED and DECIDED lines being history.
func openRefusals(r *landRig) int {
	var open int
	for _, line := range strings.Split(r.ok("inbox"), "\n") {
		if strings.HasPrefix(line, "JUDGMENT") && strings.Contains(line, "stream stopped: the merge queue rejected") {
			open++
		}
	}
	return open
}

// A stream a transient push refusal stopped (GH006 on a protected base) resumes by
// itself once the push succeeds: the base refuses both pushes of a round, the stream
// stops, a round before the retry is due leaves it stopped, and the first round after
// it lands the cards with no person's resume. The clock is the twin's, never real time.
func TestAStreamStoppedByATransientPushRefusalResumesByItself(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n"), "s1-2": r.head("s1-2", "main", "b.txt", "b\n")}, "s1-1", "s1-2")
	more := []string{"--repo-dir", r.clone, "--base", "main"}
	moves := 2
	r.a.beforePush = func(int) {
		if moves > 0 {
			moves--
			r.moveBase("main", "moved"+strconv.Itoa(moves)+".txt")
		}
	}
	var out bytes.Buffer
	assert.Equal(t, 1, r.a.landRound(context.Background(), "mem:0", more, &out), out.String())
	assert.Equal(t, "stopped rejected", r.streamState("s1"))
	assert.Equal(t, 1, openRefusals(r), "the refusal raised its one judgment")

	out.Reset()
	r.a.landRound(context.Background(), "mem:0", more, &out)
	assert.Equal(t, "stopped rejected", r.streamState("s1"), "the retry is not due yet: "+out.String())

	r.a.sleep(sprint.PushRetryAfter)
	out.Reset()
	assert.Equal(t, 0, r.a.landRound(context.Background(), "mem:0", more, &out), out.String())
	assert.Contains(t, out.String(), "LAND RETRY stream=s1")
	assert.Contains(t, out.String(), "LAND OK stream=s1 cards=2")
	assert.Equal(t, "landed", r.streamState("s1"))
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
	assert.Zero(t, openRefusals(r), "the resume closed the judgment")
	r.clean()
}

// A refusal that does not pass is retried PushRetryMax times and then left to the
// coordinator: the stream stays stopped with exactly one judgment open, and no round
// after that resumes it again.
func TestAPushRefusedForGoodRaisesExactlyOneJudgmentAfterTheRetries(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n"), "s1-2": r.head("s1-2", "main", "b.txt", "b\n")}, "s1-1", "s1-2")
	more := []string{"--repo-dir", r.clone, "--base", "main"}
	n := 0
	r.a.beforePush = func(int) { n++; r.moveBase("main", "moved"+strconv.Itoa(n)+".txt") }
	var out bytes.Buffer
	for range 1 + sprint.PushRetryMax + 3 {
		r.a.landRound(context.Background(), "mem:0", more, &out)
		r.a.sleep(sprint.PushRetryAfter + time.Second)
	}
	assert.Equal(t, 2*(1+sprint.PushRetryMax), n, "one landing and each retry made two pushes, and no more")
	assert.Equal(t, "stopped rejected", r.streamState("s1"))
	assert.Equal(t, sprint.PushRetryMax, strings.Count(out.String(), "LAND RETRY"))
	assert.Equal(t, 1, openRefusals(r), "one judgment, for the coordinator")
	r.clean()
}

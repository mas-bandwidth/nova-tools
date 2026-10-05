package main

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The push-retry rule (docs/SPEC-SPRINT.md section 8, answered by rule; a stream stopped by a
// transient push refusal, GH006 at 10:40, stayed stopped until a person found it at 11:20).
// A push refused again after its rebuild refuses the batch and stops nothing; it is pushed
// again after 2 minutes and after 5, and only the refusal after them stops the stream, as
// the one judgment. A push that succeeds in between lands, and the stream never stopped.
func TestAStreamStoppedByATransientPushRefusalResumesByItselfOrRaisesOneJudgment(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		refuses int // landings whose push is refused, first to last
		state   string
		places  string
		judged  int
	}{
		{"refused once then pushed", 1, "landed", "landed/merged", 0},
		{"refused twice then pushed", 2, "landed", "landed/merged", 0},
		{"refused every time", 3, "stopped rejected", "merging/queued", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			r.ok("add --stream s1 --count 1 --one")
			r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n")}, "s1-1")
			landing := 0
			r.a.beforePush = func(attempt int) {
				if landing <= tc.refuses {
					r.moveBase("main", "moved"+strconv.Itoa(landing)+"-"+strconv.Itoa(attempt)+".txt")
				}
			}
			waits := append([]time.Duration{0}, sprint.PushRetries...)
			for i, wait := range waits {
				if r.streamState("s1") != "merging" || r.places("s1-1")["s1-1"] == "landed/merged" {
					break
				}
				r.after(wait)
				landing = i + 1
				_, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
				if i < tc.refuses && i < len(sprint.PushRetries) {
					assert.Contains(t, out+errs, "pushed again at", out+errs)
					assert.Equal(t, "merging", r.streamState("s1"), "a refusal inside the retries stops nothing")
				}
			}
			assert.Equal(t, tc.state, r.streamState("s1"))
			assert.Equal(t, map[string]string{"s1-1": tc.places}, r.places("s1-1"))
			assert.Equal(t, tc.judged, strings.Count(r.ok("inbox"), sprint.NRejected))
			r.clean()
		})
	}
}

// Inside the wait a landing on the stream is refused before any push or build.
func TestALandingInsideThePushRetryWaitIsRefusedBeforeAnyPush(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 1 --one")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n")}, "s1-1")
	pushes := 0
	r.a.beforePush = func(attempt int) {
		pushes++
		r.moveBase("main", "moved"+strconv.Itoa(pushes)+".txt")
	}
	_, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.Contains(t, out+errs, "(refusal 1 of 3)")
	assert.Equal(t, 2, pushes)
	r.after(time.Minute)
	_, out, errs = r.do("land --repo-dir " + r.clone + " --base main")
	assert.Contains(t, out+errs, "(refusal 1 of 3)")
	assert.Equal(t, 2, pushes, "no push inside the wait")
	r.clean()
}

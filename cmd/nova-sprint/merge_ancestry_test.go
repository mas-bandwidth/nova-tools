package main

import (
	"io"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// merge is a report: a record by place lands no card whose head is not an ancestor of the
// base's tip. It refuses the record whole, naming the card, its head, why and land, and
// writes nothing; a head on the base is recorded as before; land, the act, lands the rest
// (docs/SPEC-SPRINT.md section 8, no-merge-step-prints-land-and-merge-refuses-a-head-off-the-base).
// A bare merge --stream once recorded a card landed whose head was not on the base.
func TestMergeRefusesAHeadThatIsNotOnTheBase(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	heads := map[string]string{"s1-1": r.head("s1-1", "main", "s1-1.txt", "s1-1\n"), "s1-2": r.head("s1-2", "main", "s1-2.txt", "s1-2\n")}
	r.queued(heads, "s1-1", "s1-2")
	queued := map[string]string{"s1-1": "merging/queued", "s1-2": "merging/queued"}
	require.Equal(t, queued, r.places("s1-1", "s1-2"))
	r.git(r.clone, "fetch", "-q", "origin")
	tip := r.git(r.clone, "rev-parse", "origin/main")

	// the record as it was made: neither head was pushed to the base
	code, out, errs := r.do("merge --stream s1 --batch 1 --repo " + r.clone + " --base-ref origin/main")
	require.NotEqual(t, 0, code, "a false landing was recorded: %s%s", out, errs)
	assert.Contains(t, errs, "s1-1")
	assert.Contains(t, errs, "head "+heads["s1-1"]+" is not an ancestor of the base branch's tip "+tip+"; it was not landed")
	assert.Contains(t, errs, "run: nova-sprint land --stream s1")
	assert.Equal(t, queued, r.places("s1-1", "s1-2"), "a refused record wrote")

	// with no clone named, each card's base is its brief's, and these name none: not
	// checked, refused the same
	code, out, errs = r.do("merge --stream s1")
	require.NotEqual(t, 0, code, "an unchecked landing was recorded: %s%s", out, errs)
	assert.Contains(t, errs, "could not be checked against the base branch's tip: its brief names no BASE: line")
	assert.Equal(t, queued, r.places("s1-1", "s1-2"), "a refused record wrote")

	// a head pushed to the base is recorded by place as before
	r.git(r.worker, "push", "-q", "origin", heads["s1-1"]+":refs/heads/main")
	r.git(r.clone, "fetch", "-q", "origin")
	r.ok("merge --stream s1 --batch 1 --repo " + r.clone + " --base-ref origin/main")
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "merging/queued"}, r.places("s1-1", "s1-2"))

	// land is the act: it merges, pushes and records; its pass reconciles the landed
	// records, and every one is on the base
	out = r.ok("land --repo-dir " + r.clone + " --base main")
	assert.Contains(t, out, "LAND OK stream=s1 cards=1")
	assert.NotContains(t, out, "LANDED-MISSING")
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
	r.clean()
}

// recordLanded is a landing recorded with no ancestry facts, as the lander records the batch
// it pushed: the stand-in of a test whose cards carry commit ids no git holds, which merge
// now refuses to record.
func (ta *testApp) recordLanded(stream string, batch int) {
	ta.t.Helper()
	c := common{redis: "mem:0", actor: "tester", epoch: 0}
	st, err := ta.a.store(c)
	require.NoError(ta.t, err)
	var errb strings.Builder
	require.Equal(ta.t, 0, ta.a.runStep("merge", c, st, store.MergeStep(sprint.MergeReq{Stream: stream, Batch: batch}), io.Discard, &errb), errb.String())
}

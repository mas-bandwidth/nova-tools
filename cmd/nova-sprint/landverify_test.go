package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A card the store records landed whose head is not on origin's base is listed by
// verify-landed, by git alone, and the verb exits 1; a card land put on the branch is not
// listed, and nothing in the store moves (docs/SPEC-SPRINT.md section 7,
// land-verify-landed-ancestry-r.w1). The false record is seeded as an old unverified report
// could have recorded it; land now catches it before another push and names the final-state
// lifecycle move that prevents an automatic repair.
func TestVerifyLandedListsALandedRecordMissingFromTheBase(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	heads := map[string]string{"s1-1": r.head("s1-1", "main", "s1-1.txt", "s1-1\n"), "s1-2": r.head("s1-2", "main", "s1-2.txt", "s1-2\n")}
	r.queued(heads, "s1-1", "s1-2")
	// Seed the bad historical record directly: the old bare merge report trusted its caller.
	st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	_, err = st.Run(t.Context(), store.MergeStep(sprint.MergeReq{Stream: "s1", Landed: []sprint.LandedPin{{ID: "s1-1", Head: heads["s1-1"], InBase: true}}, Who: "tester"}))
	require.NoError(t, err)
	require.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "merging/queued"}, r.places("s1-1", "s1-2"))
	tip := r.git(r.remote, "rev-parse", "main")

	code, out, errs := r.do("verify-landed --repo-dir " + r.clone + " --base main")
	require.Equal(t, 1, code, "verify-landed: %s%s", out, errs)
	assert.Contains(t, out, "LANDED-MISSING s1-1 stream=s1 head="+heads["s1-1"]+" base=main tip="+tip+"\n")
	assert.NotContains(t, out, "s1-2")
	assert.Contains(t, out, "VERIFY-LANDED checked=1 missing=1 unchecked=0\n")
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "merging/queued"}, r.places("s1-1", "s1-2"), "verify-landed is a read")
	code, out, errs = r.do("land --repo-dir " + r.clone + " --base main")
	require.Equal(t, 1, code, "land reconciliation: %s%s", out, errs)
	assert.Contains(t, errs, "LANDED-MISSING s1-1 stream=s1 head="+heads["s1-1"])
	assert.Contains(t, errs, "the lifecycle has no landed -> merging move")
	assert.Contains(t, errs, "LAND DONE batches=0 cards=0 refused=1")
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "merging/queued"}, r.places("s1-1", "s1-2"), "reconciliation names the lifecycle block and writes nothing")

	// another stream is not checked; a stream with every record on the branch exits 0
	assert.Contains(t, r.ok("verify-landed --stream s2 --repo-dir "+r.clone+" --base main"), "VERIFY-LANDED checked=0 missing=0 unchecked=0\n")
	// with no base for a card it says so and does not guess: exit 2
	code, out, _ = r.do("verify-landed --repo-dir " + r.clone)
	assert.Equal(t, 2, code)
	assert.Contains(t, out, "LANDED-UNCHECKED s1-1 stream=s1 reason=its brief names no BASE: line")
	r.clean()
}

func TestMergeRefusesAHeadThatIsNotOnTheBase(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 1 --one")
	head := r.head("s1-1", "main", "off-base.txt", "not on main\n")
	r.queued(map[string]string{"s1-1": head}, "s1-1")

	code, out, errs := r.do("merge --stream s1")
	require.Equal(t, 1, code, "merge: %s%s", out, errs)
	assert.Contains(t, errs, "head "+head+" cannot be recorded landed without Git ancestry proof")
	assert.Contains(t, errs, "run: nova-sprint merge --stream s1 --landed s1-1@"+head)
	assert.Equal(t, map[string]string{"s1-1": "merging/queued"}, r.places("s1-1"), "a report without ancestry proof writes nothing")
	r.git(r.clone, "fetch", "-q", "origin", "refs/heads/sprint/s1-1:refs/remotes/origin/sprint/s1-1")
	code, out, errs = r.do("merge --stream s1 --landed s1-1@" + head + " --repo " + r.clone + " --base-ref refs/remotes/origin/main")
	require.Equal(t, 1, code, "verified merge refusal: %s%s", out, errs)
	assert.Contains(t, errs, "head "+head+" is not an ancestor of the base branch's tip")
	assert.Contains(t, errs, "run: nova-sprint land --stream s1")
	assert.Equal(t, map[string]string{"s1-1": "merging/queued"}, r.places("s1-1"), "the failed ancestry check writes nothing")
	r.clean()
}

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A card the store records landed whose head is not on origin's base is listed by
// verify-landed, by git alone, and the verb exits 1; a card land put on the branch is not
// listed, and nothing in the store moves (docs/SPEC-SPRINT.md section 7,
// land-verify-landed-ancestry-r.w1). The false record is made as it was found: the merge step
// recorded the card landed and nothing pushed it; the land pass finds it too.
func TestVerifyLandedListsALandedRecordMissingFromTheBase(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	heads := map[string]string{"s1-1": r.head("s1-1", "main", "s1-1.txt", "s1-1\n"), "s1-2": r.head("s1-2", "main", "s1-2.txt", "s1-2\n")}
	r.queued(heads, "s1-1", "s1-2")
	// merge now refuses that record (merge_ancestry_test.go): the step is run as the lander
	// runs it, with no ancestry facts asked for
	r.recordLanded("s1", 1)
	landOut := r.ok("land --repo-dir " + r.clone + " --base main")
	assert.Contains(t, landOut, "LAND OK stream=s1 cards=1")
	require.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
	tip := r.git(r.remote, "rev-parse", "main")
	// the land pass reconciles the landed records against the base and finds the false one
	assert.Contains(t, landOut, "LANDED-MISSING s1-1 stream=s1 head="+heads["s1-1"]+" base=main tip="+tip+"\n")
	assert.NotContains(t, landOut, "LANDED-MISSING s1-2")

	code, out, errs := r.do("verify-landed --repo-dir " + r.clone + " --base main")
	require.Equal(t, 1, code, "verify-landed: %s%s", out, errs)
	assert.Contains(t, out, "LANDED-MISSING s1-1 stream=s1 head="+heads["s1-1"]+" base=main tip="+tip+"\n")
	assert.NotContains(t, out, "s1-2")
	assert.Contains(t, out, "VERIFY-LANDED checked=2 missing=1 unchecked=0\n")
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"), "verify-landed is a read")

	// another stream is not checked; a stream with every record on the branch exits 0
	assert.Contains(t, r.ok("verify-landed --stream s2 --repo-dir "+r.clone+" --base main"), "VERIFY-LANDED checked=0 missing=0 unchecked=0\n")
	// with no base for a card it says so and does not guess: exit 2
	code, out, _ = r.do("verify-landed --repo-dir " + r.clone)
	assert.Equal(t, 2, code)
	assert.Contains(t, out, "LANDED-UNCHECKED s1-1 stream=s1 reason=its brief names no BASE: line")
	r.clean()
}

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every land pass reconciles the landed records of its streams against the base and prints
// each false one as one LANDED-MISSING line, naming the head and the base tip, without
// moving the card: landed is final in the lifecycle
// (docs/SPEC-SPRINT.md section 7, no-merge-step-prints-land-and-merge-refuses-a-head-off-the-base).
func TestLandPassFindsAFalseLandedRecord(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	heads := map[string]string{"s1-1": r.head("s1-1", "main", "s1-1.txt", "s1-1\n"), "s1-2": r.head("s1-2", "main", "s1-2.txt", "s1-2\n")}
	r.queued(heads, "s1-1", "s1-2")
	// the false record as it was found: the merge step recorded the card landed and nothing
	// pushed it (merge_ancestry_test.go: the verb now refuses that record)
	r.recordLanded("s1", 1)
	landOut := r.ok("land --repo-dir " + r.clone + " --base main")
	assert.Contains(t, landOut, "LAND OK stream=s1 cards=1")
	require.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
	tip := r.git(r.remote, "rev-parse", "main")
	assert.Contains(t, landOut, "LANDED-MISSING s1-1 stream=s1 head="+heads["s1-1"]+" base=main tip="+tip+"\n")
	assert.NotContains(t, landOut, "LANDED-MISSING s1-2")
	assert.Equal(t, "landed/merged", r.places("s1-1")["s1-1"], "the false landing is not moved: landed is final")
}

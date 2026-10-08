package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// land takes the streams by priority (docs/SPEC-SPRINT.md section 1, "Priority";
// sprint.LandOrder; the owner, 2026-10-06): a stream's level is the highest of any card in its
// merging set, never its default or its oldest card; the higher stream lands first, the stream
// order the tie-break; inside a stream the batch stays as it is.
func TestLandTakesTheHighestPriorityStreamFirst(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		set     []string // priority verbs run once the cards merge
		first   string
		message string
	}{
		{"a high stream queued later", []string{"priority c d --high --reason urgent"}, "s2", "the high stream lands first though it was queued later"},
		{"one critical card among normal ones beats a high stream", []string{"priority a b --high --reason urgent", "priority d --critical --reason now"}, "s2", "its merging set's highest level is critical, above high"},
		{"none set: stream order", nil, "s1", "equal levels keep stream order"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			briefs := t.TempDir()
			brief := func(id string) string {
				path := filepath.Join(briefs, id+".md")
				require.NoError(t, os.WriteFile(path, []byte(passingBrief("REPO: "+r.remote+"\nBASE: main\n\nWrite "+id+".txt.")), 0o600))
				return path
			}
			r.promotionStream("s1")
			r.promotionStream("s2")
			r.ok("add --stream s1 --brief-file " + brief("a") + " --brief-file " + brief("b"))
			r.ok("add --stream s2 --brief-file " + brief("c") + " --brief-file " + brief("d"))
			heads := map[string]string{}
			for _, id := range []string{"a", "b", "c", "d"} {
				heads[id] = r.head(id, "main", id+".txt", id+"\n")
			}
			r.queued(heads, "a", "b", "c", "d") // s1 queued first
			for _, line := range tc.set {
				r.ok(line)
			}
			// the streams merged one at a time, so the landing order is the pass's order; with
			// the merges in parallel each batch lands as its gate finishes, the gates' race
			// (landpass.go, the pass)
			out := r.ok("land --land-parallel 1")
			other := map[string]string{"s1": "s2", "s2": "s1"}[tc.first]
			assert.Less(t, strings.Index(out, "LAND OK stream="+tc.first), strings.Index(out, "LAND OK stream="+other), "%s:\n%s", tc.message, out)
			assert.Contains(t, out, "LAND DONE batches=2 cards=4 refused=0")
		})
	}
}

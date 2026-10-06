package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A scope amendment of the same change is allowed by rule and recorded on the batch's
// line (sprint.ScopeAmended; docs/SPEC-SPRINT.md section 7): a card whose head changes
// its PATHS file and a doc under docs/ lands with scope=<card>:<doc>; a test or a ledger
// elsewhere is inside every card's PATHS (cardgen.AlwaysInPathsRule) and lands with no
// amendment; code outside its PATHS is still refused (E12).
func TestLandAllowsAScopeAmendmentOfTheSameChangeAndRecordsIt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, file     string
		landed, scoped bool
	}{
		{"a doc of the same change", "docs/A.md", true, true},
		{"a test in another package", "internal/x/x_test.go", true, false},
		{"a TLA+ ledger", "tla/RUNS.tsv", true, false},
		{"code outside its PATHS", "b.go", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			brief := writeNeedsBrief(t, t.TempDir(), "a", "RESULT: a tier: flash\nPATHS: a.txt", "")
			r.ok("add --stream s1 a --one --brief-file " + brief)
			r.head("a", "main", "a.txt", "a\n")
			require.NoError(t, os.MkdirAll(filepath.Join(r.worker, filepath.Dir(tc.file)), 0o755))
			head := r.commit(tc.file, "The lane is one a machine.\n", "the amendment of a")
			r.queued(map[string]string{"a": head}, "a")
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
			if tc.landed {
				assert.Equal(t, 0, code, out+errs)
				if tc.scoped {
					assert.Contains(t, out, "scope=a:"+tc.file)
				} else {
					assert.NotContains(t, out, "scope=")
				}
				assert.Equal(t, map[string]string{"a": "landed/merged"}, r.places("a"))
			} else {
				assert.Equal(t, 1, code, out+errs)
				assert.Contains(t, errs, "it changes files outside its PATHS (E12): "+tc.file)
				assert.Equal(t, map[string]string{"a": "merging/stuck"}, r.places("a"))
			}
			r.clean()
		})
	}
}

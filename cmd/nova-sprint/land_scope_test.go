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
// its PATHS file and a doc under docs/ lands with scope=<card>:<doc>; code outside its
// PATHS is still refused (E12).
func TestLandAllowsAScopeAmendmentOfTheSameChangeAndRecordsIt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, file string
		landed     bool
	}{
		{"a doc of the same change", "docs/A.md", true},
		{"code outside its PATHS", "b.go", false},
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
				assert.Contains(t, out, "scope=a:"+tc.file)
				assert.Equal(t, map[string]string{"a": "landed/merged"}, r.places("a"))
			} else {
				assert.Equal(t, 1, code, out+errs)
				assert.Contains(t, errs, "it changes files outside its PATHS (E12): "+tc.file)
				assert.Equal(t, map[string]string{"a": "review/returned"}, r.places("a"))
			}
			r.clean()
		})
	}
}

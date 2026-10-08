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

// The lander's E12 never refuses a file a change must touch to keep the tree green
// (cardgen.AlwaysInPathsRule; sprint.LandScope; docs/SPEC-SPRINT.md section 7): a head
// that changes its PATHS file and, outside its PATHS and in no directory of its own, a
// test, a testdata file, a TLA+ ledger, the docs catalog or an AGENTS.md map lands;
// one that changes a non-test source file outside its PATHS is still refused (E12).
func TestLandNeverRefusesATestOrALedgerOutsidePaths(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		files  map[string]string
		landed bool
		out    string
	}{
		{"a test and a ledger", map[string]string{"other/b_test.go": "package other\n", "tla/RUNS.tsv": "case\tgroup\n"}, true, ""},
		{"a testdata file at depth and the CASES ledger", map[string]string{"other/testdata/deep/g.txt": "g\n", "tla/CASES.tsv": "case\n"}, true, ""},
		{"the catalog and a map", map[string]string{"internal/docs/catalog.go": "package docs\n", "other/AGENTS.md": "# other\n"}, true, ""},
		{"a non-test source file", map[string]string{"other/b_test.go": "package other\n", "other/b.go": "package other\n"}, false, "other/b.go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			brief := writeNeedsBrief(t, t.TempDir(), "a", "RESULT: a tier: flash\nPATHS: a.txt", "")
			r.ok("add --stream s1 a --one --brief-file " + brief)
			files := map[string]string{"a.txt": "a\n"}
			for f, text := range tc.files {
				files[f] = text
			}
			head := r.card("a", files)
			r.queued(map[string]string{"a": head}, "a")
			before := r.git(r.remote, "rev-parse", "main")
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
			if tc.landed {
				assert.Equal(t, 0, code, out+errs)
				assert.NotContains(t, errs, "(E12)")
				assert.Equal(t, map[string]string{"a": "landed/merged"}, r.places("a"))
			} else {
				assert.Equal(t, 1, code, out+errs)
				assert.Contains(t, errs, "it changes files outside its PATHS (E12): "+tc.out)
				assert.Equal(t, map[string]string{"a": "review/returned"}, r.places("a"))
				assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"))
			}
			r.clean()
		})
	}
}

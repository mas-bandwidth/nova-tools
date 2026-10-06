package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTlaBrief writes a passing brief whose header block carries paths on its PATHS:
// line and whose body carries steps, and returns its path.
func writeTlaBrief(t *testing.T, dir, id, tier, paths, steps string) string {
	t.Helper()
	lead := "RESULT: " + id + " sha=000000000000 tier: " + tier + "\nKIND: model\nDEPENDS-ON: -\nPATHS: " + paths + "\n\nChange the model of " + id + ".\n\n" + steps
	path := filepath.Join(dir, id+".md")
	require.NoError(t, os.WriteFile(path, []byte(passingBrief(lead)), 0o600))
	return path
}

// A card that edits a tla/*.tla model and does not refresh tla/RUNS.tsv lands records
// the TLC records class test (internal/ci/tlc_records_class_test.go) then calls stale
// on the base: add refuses a brief whose PATHS cover a model but not tla/RUNS.tsv, or
// whose STEPs do not name tlacheck merge --keep, naming the remedy tla/README.md gives,
// exit 2, nothing written; the same card covering tla/** with the step is added, and a
// card that only reads tla/ is untouched (docs/SPEC-SPRINT.md section 11,
// add-tla-edit-refreshes-records-b.w3).
func TestAddRefusesATlaEditWithoutARecordsRefresh(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	const step = "STEP 1. Change the model.\n\nSTEP 2. Run the changed groups on a Linux bench, then: /tmp/tlacheck merge --root . --keep tla/RUNS.tsv --out tla/RUNS.tsv \"$runs\"/*/RUNS.tsv"
	refused := func(why, path string, says ...string) {
		t.Helper()
		code, out, errs := ta.do("add --stream s --one --brief-file " + path)
		require.Equal(t, 2, code, "%s: %s%s", why, out, errs)
		assert.Contains(t, errs, "tlacheck merge --keep tla/RUNS.tsv", why)
		assert.Contains(t, errs, "tla/README.md", why)
		assert.Contains(t, errs, "Linux bench", why)
		for _, s := range says {
			assert.Contains(t, errs, s, why)
		}
		assert.NotContains(t, out, "MOVED", "%s: nothing written", why)
	}
	refused("a model edit with no records step", writeTlaBrief(t, dir, "m1", "flash", "tla/Land.tla", "STEP 1. Change the model."), "tla/Land.tla", "does not cover tla/RUNS.tsv", "names no STEP")
	refused("the records covered and no step", writeTlaBrief(t, dir, "m2", "flash", "tla/Land.tla, tla/RUNS.tsv", "STEP 1. Change the model."), "names no STEP")
	refused("the step and the records uncovered", writeTlaBrief(t, dir, "m3", "flash", "tla/Land.tla", step), "does not cover tla/RUNS.tsv")
	refused("a glob over the models", writeTlaBrief(t, dir, "m4", "flash", "tla/*.tla internal/ci/x.go", step), "tla/*.tla")
	refused("the merge named outside a STEP", writeTlaBrief(t, dir, "m5", "flash", "tla/**", "STEP 1. Change the model.\n\nThe records are refreshed with tlacheck merge --keep."), "names no STEP")

	assert.Contains(t, ta.ok("add --stream s --one --brief-file "+writeTlaBrief(t, dir, "m6", "frontier", "tla/**", step)), "MOVED m6 -> ready", "tla/** covers the records and the step merges them")
	assert.Contains(t, ta.ok("add --stream s --one --brief-file "+writeTlaBrief(t, dir, "m7", "frontier", "tla/Land.tla tla/RUNS.tsv", step)), "MOVED m7 -> ready")
	assert.Contains(t, ta.ok("add --stream s --one --brief-file "+writeTlaBrief(t, dir, "r1", "flash", "internal/ci/x.go", "STEP 1. Read tla/Land.tla and change internal/ci/x.go to match it.")), "MOVED r1 -> ready", "a card that only reads tla/ is untouched")
	assert.Contains(t, ta.ok("add --stream s --one --brief-file "+writeTlaBrief(t, dir, "r2", "frontier", "tla/README.md", "STEP 1. Fix the README.")), "MOVED r2 -> ready", "a card that edits no model is untouched")

	many := t.TempDir()
	writeTlaBrief(t, many, "w1", "flash", "internal/w1.go", "STEP 1. Fix w1.")
	writeTlaBrief(t, many, "w2", "flash", "tla/Land.tla", "STEP 1. Change the model.")
	code, out, errs := ta.do("add --stream t --brief-dir " + many)
	require.Equal(t, 2, code, "the many-brief form: %s%s", out, errs)
	assert.Contains(t, errs, "w2", "the refusal names the card")
	assert.Contains(t, errs, "tlacheck merge --keep tla/RUNS.tsv")
	assert.NotContains(t, out, "MOVED", "one model edit refuses the whole call")
}

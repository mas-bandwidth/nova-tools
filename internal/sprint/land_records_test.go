package sprint_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/tlc"
)

// The models of the rig: MCA extends A, MCB stands alone, each case in its own group.
var recordsModels = map[string]string{
	"tla/CASES.tsv": "config\tmodule\texpected\tproperty\tdeadlock\tgroup\tgate\tdebt\n" +
		"MCA.cfg\tMCA.tla\tpass\t-\tcheck\talpha\trequired\t-\n" +
		"MCB.cfg\tMCB.tla\tpass\t-\tcheck\tbeta\trequired\t-\n",
	"tla/A.tla":   "---- MODULE A ----\nEXTENDS Naturals\nVARIABLE x\nInit == x = 0\nNext == x' = x\n====\n",
	"tla/MCA.tla": "---- MODULE MCA ----\nEXTENDS A\n====\n",
	"tla/MCA.cfg": "INIT Init\nNEXT Next\n",
	"tla/MCB.tla": "---- MODULE MCB ----\nVARIABLE y\nInit == y = 0\nNext == y' = y\n====\n",
	"tla/MCB.cfg": "INIT Init\nNEXT Next\n",
}

// runsFor is the tla/RUNS.tsv `make tlc` and `tlacheck merge` would write for files: one
// current record per case, its fingerprint the one the tree gives it.
func runsFor(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for f, s := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(s), 0o600))
	}
	src, err := tlc.SourceAt(dir)
	require.NoError(t, err)
	cases, err := tlc.ParseCases(strings.NewReader(files["tla/CASES.tsv"]))
	require.NoError(t, err)
	var records []tlc.Record
	for _, c := range cases {
		fp, n, err := src.Fingerprint(c.Config)
		require.NoError(t, err)
		records = append(records, tlc.Record{Config: c.Config, Module: c.Module, InputSHA256: fp, InputFiles: n,
			JarSHA256: strings.Repeat("a", 64), JavaVersion: "21.0.12.1", Host: "linux-amd64", CPUs: 4,
			StartedUTC: "2026-10-06T12:00:00.000000+00:00", Workers: 2, Generated: "2", Distinct: "1", Seconds: "0.500",
			Exit: 0, Result: "PASS", Expected: c.Expected, Property: c.Property, Budget: "110", Mode: "bounded"})
	}
	var b bytes.Buffer
	require.NoError(t, tlc.WriteRecords(&b, records))
	return b.String()
}

// with is files with over laid on them.
func with(files map[string]string, over map[string]string) map[string]string {
	out := map[string]string{}
	for f, s := range files {
		out[f] = s
	}
	for f, s := range over {
		out[f] = s
	}
	return out
}

// The lander's run-record check (docs/SPEC-SPRINT.md section 7; tla/README.md): a head that
// edits a model without the record `make tlc` writes for each case the edit touches is
// refused with one line naming the case; the same edit with the record lands; a case the
// edit does not reach, and a change with no model in it, are not asked for one.
func TestTheLanderWantsARunRecordForAnEditedModel(t *testing.T) {
	t.Parallel()
	base := with(recordsModels, map[string]string{"tla/RUNS.tsv": runsFor(t, recordsModels)})
	edited := with(recordsModels, map[string]string{"tla/A.tla": strings.Replace(recordsModels["tla/A.tla"], "x' = x", "x' = 1 - x", 1)})

	t.Run("an edited model without its record is refused naming the case", func(t *testing.T) {
		t.Parallel()
		r := newMergeRig(t, base, map[string]string{"tla/A.tla": edited["tla/A.tla"]})
		merged := r.must("rev-parse", "HEAD")
		note, refused, err := sprint.RepairMerge(r.dir, r.git, r.before, nil)
		require.NoError(t, err)
		assert.Empty(t, note)
		require.Len(t, refused, 1, "MCB reads nothing the change edits: %v", refused)
		assert.Equal(t, "tla/RUNS.tsv:2 has no current run record for the case MCA.cfg (group alpha) and the change edits tla/A.tla; "+
			"run make tlc TLC_GROUP=alpha on a TLC bench, merge its RUNS.tsv with tlacheck merge --keep tla/RUNS.tsv and commit it (tla/README.md)",
			refused[0].String())
		assert.NotContains(t, refused[0].String(), "\n", "one line")
		assert.Equal(t, merged, r.must("rev-parse", "HEAD"), "a refusal writes nothing")
	})
	t.Run("a new case with no row at all is refused naming it", func(t *testing.T) {
		t.Parallel()
		head := map[string]string{
			"tla/CASES.tsv": recordsModels["tla/CASES.tsv"] + "MCC.cfg\tMCC.tla\tpass\t-\tcheck\tgamma\trequired\t-\n",
			"tla/MCC.tla":   "---- MODULE MCC ----\nEXTENDS A\n====\n",
			"tla/MCC.cfg":   "INIT Init\nNEXT Next\n",
		}
		r := newMergeRig(t, base, head)
		_, refused, err := sprint.RepairMerge(r.dir, r.git, r.before, nil)
		require.NoError(t, err)
		require.Len(t, refused, 1, "%v", refused)
		assert.Contains(t, refused[0].String(), "tla/RUNS.tsv:4 has no current run record for the case MCC.cfg (group gamma) and the change edits tla/MCC.cfg, tla/MCC.tla;")
	})
	t.Run("the same edit with the record make tlc writes lands", func(t *testing.T) {
		t.Parallel()
		r := newMergeRig(t, base, map[string]string{"tla/A.tla": edited["tla/A.tla"], "tla/RUNS.tsv": runsFor(t, edited)})
		note, refused, err := sprint.RepairMerge(r.dir, r.git, r.before, nil)
		require.NoError(t, err)
		assert.Empty(t, note)
		assert.Empty(t, refused)
	})
	t.Run("a change with no model in it is not asked for a record", func(t *testing.T) {
		t.Parallel()
		stale := with(recordsModels, map[string]string{"tla/RUNS.tsv": runsFor(t, edited)}) // every record stale at base
		r := newMergeRig(t, stale, map[string]string{"tla/README.md": "# tla\n", "internal/x/x.go": "package x\n"})
		_, refused, err := sprint.RepairMerge(r.dir, r.git, r.before, nil)
		require.NoError(t, err)
		assert.Empty(t, refused, "a base's stale record is not the card's")
	})
	t.Run("a tree with no case plan is not checked", func(t *testing.T) {
		t.Parallel()
		r := newMergeRig(t, map[string]string{"README.md": "r\n"}, map[string]string{"tla/Spec.tla": "---- MODULE Spec ----\n====\n"})
		_, refused, err := sprint.RepairMerge(r.dir, r.git, r.before, nil)
		require.NoError(t, err)
		assert.Empty(t, refused)
	})
}

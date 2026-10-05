package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordFile is an append-only record the tree's own .gitattributes merges by union.
const recordFile = "tla/CASES.tsv"

// Two cards that each append a row to an append-only record at its end touch the same
// place; under the tree's own .gitattributes the merge keeps both rows and both land
// without a stop. Rows both sides appended are kept once, the merge amended and the
// batch's NOTE and the card's timeline saying so. A side that changes a row the other
// side's appended hunk holds leaves its key in two rows, refused as a conflict, the card
// stuck. A shrink-only ledger that both sides take rows from merges by intersection: a row
// is kept only when both sides keep it.
func TestAnAppendOnlyRecordConflictMergesByUnion(t *testing.T) {
	t.Parallel()
	attrs, err := os.ReadFile("../../.gitattributes")
	require.NoError(t, err)
	const head = "config\tmodule\n"
	for _, tc := range []struct {
		name, file, base string
		first, second    string
		want             string // the file on the base after both land
		note             string // the NOTE line, "" for none
		why              string // "" lands both
	}{
		{"two appended rows land", recordFile, head + "MCA.cfg\tA.tla\n",
			head + "MCA.cfg\tA.tla\nMCB.cfg\tB.tla\n",
			head + "MCA.cfg\tA.tla\nMCC.cfg\tC.tla\n",
			head + "MCA.cfg\tA.tla\nMCB.cfg\tB.tla\nMCC.cfg\tC.tla", "", ""},
		{"rows both sides append are kept once", "tla/RUNS.tsv", head + "MCA.cfg\tA.tla\n",
			head + "MCA.cfg\tA.tla\nMCB.cfg\tB.tla\nMCC.cfg\tC.tla\n",
			head + "MCA.cfg\tA.tla\nMCC.cfg\tC.tla\nMCB.cfg\tB.tla\n",
			head + "MCA.cfg\tA.tla\nMCB.cfg\tB.tla\nMCC.cfg\tC.tla", recordLine("tla/RUNS.tsv", 2), ""},
		{"a changed row beside an appended one is refused", recordFile, head + "MCA.cfg\tA.tla\n",
			head + "MCA.cfg\tA2.tla\n",
			head + "MCA.cfg\tA.tla\nMCB.cfg\tB.tla\n",
			"", "", "its append-only record " + recordFile + " merged by union holds the key MCA.cfg in two different rows, which is no append"},
		{"a shrink-only ledger merges by intersection", shrinkLedger, "# ceiling: 3\ninternal/a\tTestA\ninternal/b\tTestB\ninternal/c\tTestC\n",
			"# ceiling: 2\ninternal/b\tTestB\ninternal/c\tTestC\n",
			"# ceiling: 2\ninternal/a\tTestA\ninternal/c\tTestC\n",
			"# ceiling: 2\ninternal/c\tTestC", unionLine(shrinkLedger, 1, 1), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			r.git(r.worker, "switch", "-q", "--detach", "origin/main")
			r.files("the records", map[string]string{".gitattributes": string(attrs), tc.file: tc.base, "notes.md": "one\n"})
			r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
			r.git(r.worker, "fetch", "-q", "origin")
			r.ok("add --stream s1 --count 2")
			heads := map[string]string{"s1-1": r.card("s1-1", map[string]string{tc.file: tc.first}), "s1-2": r.card("s1-2", map[string]string{tc.file: tc.second})}
			r.queued(heads, "s1-1", "s1-2")
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
			if tc.why != "" {
				assert.Equal(t, 1, code, out+errs)
				assert.Contains(t, errs, "LAND REFUSED stream=s1 cards=1 base=- tip=- ids=s1-2 fact=conflict")
				assert.Contains(t, errs, tc.why)
				assert.Equal(t, []string{"land s1-1 (sprint stream s1)", "the records", "base"}, r.mainLog())
				assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "merging/stuck"}, r.places("s1-1", "s1-2"))
				assert.Empty(t, r.git(r.clone, "status", "--porcelain", "--untracked-files=all"), "the refused merge is taken back")
				r.clean()
				return
			}
			assert.Equal(t, 0, code, out+errs)
			assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main")
			assert.Equal(t, []string{"land s1-2 (sprint stream s1)", "land s1-1 (sprint stream s1)", "the records", "base"}, r.mainLog())
			assert.Equal(t, tc.want, r.git(r.remote, "show", "main:"+tc.file), "both sides' rows, each once")
			assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
			if tc.note == "" {
				assert.NotContains(t, out, "NOTE", "a union merge with nothing to drop says nothing more")
				r.clean()
				return
			}
			assert.Contains(t, out, "NOTE "+tc.note)
			if tc.file != shrinkLedger {
				assert.Contains(t, r.git(r.remote, "log", "-1", "--format=%b", "main"), "The append-only records were merged by union and their repeated rows dropped: "+tc.note+".")
				assert.Contains(t, r.ok("card s1-2"), recordNote([]string{tc.file}))
			}
			r.clean()
		})
	}
}

// uniqueRows keeps the header, the order and the first of each repeated row, and names a
// key two different rows hold; recordPaths keeps only the `.tsv` paths merged by union.
func TestUniqueRowsKeepsTheFirstOfEachRow(t *testing.T) {
	t.Parallel()
	out, dropped, key := uniqueRows([]byte("k\tv\nb\t1\na\t1\nb\t1\na\t1\n"))
	assert.Equal(t, "k\tv\nb\t1\na\t1\n", string(out))
	assert.Equal(t, 2, dropped)
	assert.Empty(t, key)
	_, _, key = uniqueRows([]byte("k\tv\na\t1\na\t2"))
	assert.Equal(t, "a", key)
	assert.Equal(t, []string{"tla/RUNS.tsv"}, recordPaths("tla/RUNS.tsv\x00merge\x00union\x00x.tsv\x00merge\x00unspecified\x00d.txt\x00merge\x00union\x00"))
}

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTablesLockRun is the tables lock's update run as TestTheTablesLockIsRegenerated's
// is (NOVA_CI_UPDATE=1): the lock's comment lines kept, its body the generator's input
// (schema.txt here, the sprint schema there) as the merged tree has it; a run that
// writes says "updated, rerun" and fails, one with nothing to write passes.
const fakeTablesLockRun = `L=` + tablesLock + `
new=$(grep '^#' $L; cat schema.txt)
[ "$new" = "$(cat $L)" ] && exit 0
printf '%s\n' "$new" > $L
echo 'updated, rerun'
exit 1`

// A conflict in a file of the three merge classes (docs/SPEC-SPRINT.md section 7, the merge classes
// of a file) never fails a landing: two cards that each append rows to the append-only
// records tla/CASES.tsv and tla/RUNS.tsv land with both sides' rows, and a row both
// sides add is one row, with or without the union merge .gitattributes declares; two
// cards that each add a column and a change paragraph to the tables lock land with
// both paragraphs and the body regenerated from the merged schema; two cards that each
// take a row out of a shrink-only ledger land with the rows both kept (the
// intersection). A record a side rewrites keeps both rows, as union does; a lock whose
// comment a side rewrites is refused as any conflict is.
func TestAnAppendOnlyRecordConflictMergesByUnion(t *testing.T) {
	t.Parallel()
	const (
		cases   = "tla/CASES.tsv"
		runs    = "tla/RUNS.tsv"
		casesH  = "config\tmodule\n"
		runsH   = "config\tresult\n"
		lockTop = "# THE TABLES ARE LOCKED.\n#\n# Change, 2026-10-01, x.\n#\n"
	)
	for _, tc := range []struct {
		name          string
		base          map[string]string
		first, second map[string]string
		want          map[string]string // each file on main after both land
		lines         []string          // the land log's NOTE lines
		note          string            // the second card's timeline says
		why           string            // "" lands both
	}{
		{"two appended rows conflict and land as the union",
			map[string]string{cases: casesH + "A.cfg\tA.tla\n", runs: runsH + "A.cfg\tPASS\n"},
			map[string]string{cases: casesH + "A.cfg\tA.tla\nB.cfg\tB.tla\n", runs: runsH + "A.cfg\tPASS\nB.cfg\tPASS\n"},
			map[string]string{cases: casesH + "A.cfg\tA.tla\nC.cfg\tC.tla\n", runs: runsH + "A.cfg\tPASS\nC.cfg\tPASS\n"},
			map[string]string{cases: casesH + "A.cfg\tA.tla\nB.cfg\tB.tla\nC.cfg\tC.tla\n", runs: runsH + "A.cfg\tPASS\nB.cfg\tPASS\nC.cfg\tPASS\n"},
			[]string{recordLine(cases, 1, 1), recordLine(runs, 1, 1)},
			"the append-only records " + cases + ", " + runs + " conflicted and were resolved at the merge as the union of both sides' rows", ""},
		{"a union merge's repeated row is removed after the merge",
			map[string]string{".gitattributes": cases + " merge=union\n", cases: casesH + "A.cfg\tA.tla\n"},
			map[string]string{cases: casesH + "A.cfg\tA.tla\nB.cfg\tB.tla\n"},
			map[string]string{cases: casesH + "B.cfg\tB.tla\nA.cfg\tA.tla\nC.cfg\tC.tla\n"},
			map[string]string{cases: casesH + "B.cfg\tB.tla\nA.cfg\tA.tla\nC.cfg\tC.tla\n"},
			[]string{dedupeLine(cases, 1)},
			"the append-only records " + cases + " held a repeated row after the merge, taken out", ""},
		{"a rewritten row is kept beside the old one",
			map[string]string{cases: casesH + "A.cfg\tA.tla\n"},
			map[string]string{cases: casesH + "A.cfg\tA2.tla\n"},
			map[string]string{cases: casesH + "A.cfg\tA3.tla\n"},
			map[string]string{cases: casesH + "A.cfg\tA2.tla\nA.cfg\tA3.tla\n"},
			[]string{recordLine(cases, 1, 1)}, "", ""},
		{"a tables lock conflict is regenerated",
			map[string]string{"schema.txt": "work.x\nwork.y\nwork.z\n", tablesLock: lockTop + "work.x\nwork.y\nwork.z\n"},
			map[string]string{"schema.txt": "work.a\nwork.x\nwork.y\nwork.z\n", tablesLock: "# THE TABLES ARE LOCKED.\n#\n# Change, 2026-10-05, a.\n#\n# Change, 2026-10-01, x.\n#\nwork.a\nwork.x\nwork.y\nwork.z\n"},
			map[string]string{"schema.txt": "work.x\nwork.y\nwork.z\nwork.b\n", tablesLock: "# THE TABLES ARE LOCKED.\n#\n# Change, 2026-10-06, b.\n#\n# Change, 2026-10-01, x.\n#\nwork.x\nwork.y\nwork.z\nwork.b\n"},
			map[string]string{tablesLock: "# THE TABLES ARE LOCKED.\n#\n# Change, 2026-10-05, a.\n#\n# Change, 2026-10-06, b.\n#\n# Change, 2026-10-01, x.\n#\nwork.a\nwork.x\nwork.y\nwork.z\nwork.b\n"},
			nil, "the generated ledgers " + tablesLock + " conflicted and were regenerated at the merge by TestTheTablesLockIsRegenerated", ""},
		{"a tables lock whose comment a side rewrites is refused",
			map[string]string{"schema.txt": "work.x\n", tablesLock: lockTop + "work.x\n"},
			map[string]string{"schema.txt": "work.x\nwork.a\n", tablesLock: "# THE TABLES ARE LOCKED.\n#\n# Change, 2026-10-01, x, rewritten.\n#\nwork.x\nwork.a\n"},
			map[string]string{"schema.txt": "work.b\nwork.x\n", tablesLock: "# THE TABLES ARE LOCKED.\n#\n# Change, 2026-10-06, b.\n#\n# Change, 2026-10-01, x.\n#\nwork.b\nwork.x\n"},
			nil, nil, "", "its generated ledgers conflict and " + tablesLock + "'s comment is not the base's with lines added"},
		{"a shrink-only ledger merges by intersection",
			map[string]string{shrinkLedger: "# ceiling: 3\ninternal/a\tTestA\ninternal/b\tTestB\ninternal/c\tTestC\n"},
			map[string]string{shrinkLedger: "# ceiling: 2\ninternal/b\tTestB\ninternal/c\tTestC\n"},
			map[string]string{shrinkLedger: "# ceiling: 2\ninternal/a\tTestA\ninternal/c\tTestC\n"},
			map[string]string{shrinkLedger: "# ceiling: 2\ninternal/c\tTestC\n"},
			[]string{unionLine(shrinkLedger, 1, 1)}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			lock := tablesLockLedger
			lock.run = []string{"sh", "-c", fakeTablesLockRun}
			r.a.ledgers = []landLedger{lock}
			r.git(r.worker, "switch", "-q", "--detach", "origin/main")
			r.files("the records", tc.base)
			r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
			r.git(r.worker, "fetch", "-q", "origin")
			r.ok("add --stream s1 --count 2")
			heads := map[string]string{"s1-1": r.card("s1-1", tc.first), "s1-2": r.card("s1-2", tc.second)}
			r.queued(heads, "s1-1", "s1-2")
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
			if tc.why != "" {
				assert.Equal(t, 1, code, out+errs)
				assert.Contains(t, errs, "LAND REFUSED stream=s1 cards=1 base=- tip=- ids=s1-2 fact=conflict reason=the head "+heads["s1-2"]+" of s1-2 does not merge")
				assert.Contains(t, errs, tc.why)
				assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "merging/stuck"}, r.places("s1-1", "s1-2"))
				assert.Empty(t, r.git(r.clone, "status", "--porcelain", "--untracked-files=all"), "the refused merge is aborted")
				r.clean()
				return
			}
			require.Equal(t, 0, code, out+errs)
			assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main")
			for _, line := range tc.lines {
				assert.Contains(t, out, "NOTE "+line)
			}
			for f, want := range tc.want {
				assert.Equal(t, want, r.git(r.remote, "show", "main:"+f)+"\n", f)
			}
			assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
			if tc.note != "" {
				assert.Contains(t, r.ok("card s1-2"), tc.note)
			}
			r.clean()
		})
	}
}

// The append-only union is both sides' rows, the tip's first, each row once; the
// insertion union is the base with both sides' added lines, the tip's first where they
// add at one place, and refuses a side that is not the base with lines added.
func TestTheMergeClassesResolveAsTheyAreDeclared(t *testing.T) {
	t.Parallel()
	got, nLeft, nRight := unionRecords([]byte("h\na\n"), []byte("h\na\nb\n"), []byte("h\na\nc\nb\n"))
	assert.Equal(t, "h\na\nb\nc\n", string(got))
	assert.Equal(t, [2]int{1, 1}, [2]int{nLeft, nRight})
	out, n := dedupeRows([]byte("h\nb\na\nb\n"))
	assert.Equal(t, "h\nb\na\n", string(out))
	assert.Equal(t, 1, n)
	merged, err := unionInserts([]string{"#", "x"}, []string{"#", "p", "#", "x"}, []string{"#", "q", "#", "x"})
	require.NoError(t, err)
	assert.Equal(t, []string{"#", "p", "#", "q", "#", "x"}, merged)
	_, err = unionInserts([]string{"#", "x"}, []string{"#", "y"}, []string{"#", "x"})
	assert.Error(t, err)
	lock, err := seedTablesLock([]byte("# t\nw.x\n"), []byte("# t\n# a\nw.x\nw.a\n"), []byte("# t\n# b\nw.b\nw.x\n"))
	require.NoError(t, err)
	assert.Equal(t, "# t\n# a\n# b\nw.x\nw.a\n", string(lock), "the comments' union over the tip's body, which the update run rewrites")
}

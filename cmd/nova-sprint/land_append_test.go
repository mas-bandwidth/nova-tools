package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tlc"

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

// tlcRow and tlcRun are a tla/CASES.tsv row and a tla/RUNS.tsv record of config cfg as
// internal/tlc reads them: deadlock and gate edit the plan row, started and seconds the
// run.
func tlcRow(cfg, deadlock, gate string) string {
	return cfg + "\tMC" + strings.TrimSuffix(strings.TrimPrefix(cfg, "MC"), ".cfg") + ".tla\tpass\t-\t" + deadlock + "\talpha\t" + gate + "\t-\n"
}

func tlcRun(cfg, started, seconds string) string {
	sha := strings.Repeat("ab", 32)
	return cfg + "\tMC" + strings.TrimSuffix(strings.TrimPrefix(cfg, "MC"), ".cfg") + ".tla\t" + sha + "\t8\t" + sha + "\t21.0.12.1\tlinux-amd64\t32\t" +
		started + "\t2\t33\t27\t" + seconds + "\t0\tPASS\tpass\t-\t110\tbounded\n"
}

// tlcCase is the configuration and module of config cfg, the files a plan row names.
func tlcCase(files map[string]string, cfg string) map[string]string {
	name := strings.TrimSuffix(cfg, ".cfg")
	files["tla/"+cfg] = "SPECIFICATION Spec\n"
	files["tla/"+name+".tla"] = "---- MODULE " + name + " ----\n====\n"
	return files
}

// A conflict in a file of the merge classes (docs/SPEC-SPRINT.md section 7, the merge
// classes of a file; docs/STANDARD.md section 10) never fails a landing it can resolve.
// tla/CASES.tsv and tla/RUNS.tsv are keyed tables: two cards that each add a config land
// with both; a config both rewrite lands as one row, the newer run's, whichever side ran
// it; a line merge that names a config twice is merged by config again; and each result
// is a plan and records internal/tlc accepts. A config both rewrite with no newer run
// between them is refused. An append-only record's row held twice after a union merge
// is one row. Two cards that each add a column and a change paragraph to the tables lock
// land with both paragraphs and the body regenerated from the merged schema; a lock whose
// comment a side rewrites is refused. Two cards that each take a row out of a
// shrink-only ledger land with the rows both kept (the intersection).
func TestAnAppendOnlyRecordConflictMergesByUnion(t *testing.T) {
	t.Parallel()
	const (
		cases   = "tla/CASES.tsv"
		runs    = "tla/RUNS.tsv"
		deleted = "internal/ci/testdata/deleted-tests.txt"
		casesH  = "config\tmodule\texpected\tproperty\tdeadlock\tgroup\tgate\tdebt\n"
		t0      = "2026-10-01T10:00:00.000001+00:00"
		t1      = "2026-10-05T10:00:00.000001+00:00"
		t2      = "2026-10-06T10:00:00.000001+00:00"
		lockTop = "# THE TABLES ARE LOCKED.\n#\n# Change, 2026-10-01, x.\n#\n"
	)
	runsH := strings.Join(tlc.RecordsHeader, "\t") + "\n"
	a, aRun := tlcRow("MCA.cfg", "check", "required"), tlcRun("MCA.cfg", t0, "1.0")
	x, xRun := tlcRow("MCX.cfg", "check", "required"), tlcRun("MCX.cfg", t0, "1.0")
	y, yRun := tlcRow("MCY.cfg", "check", "required"), tlcRun("MCY.cfg", t0, "1.0")
	c, cRun := tlcRow("MCC.cfg", "check", "required"), tlcRun("MCC.cfg", t0, "1.0")
	// every configuration is in the base: a card that edits one owes a measured run
	base := tlcCase(map[string]string{cases: casesH + a, runs: runsH + aRun}, "MCA.cfg")
	plan := func(files map[string]string, cfgs ...string) map[string]string {
		for _, cfg := range cfgs {
			tlcCase(files, cfg)
		}
		return files
	}
	for _, tc := range []struct {
		name          string
		base          map[string]string
		first, second map[string]string
		want          map[string]string // each file on main after both land
		lines         []string          // the land log's NOTE lines
		note          string            // the second card's timeline says
		why           string            // "" lands both
		tlc           bool              // main's tla/ is a plan and records internal/tlc accepts
	}{
		{"two added configs conflict and land one row each",
			plan(map[string]string{cases: casesH + a, runs: runsH + aRun}, "MCA.cfg", "MCB.cfg", "MCC.cfg"),
			map[string]string{cases: casesH + a + tlcRow("MCB.cfg", "check", "required"), runs: runsH + aRun + tlcRun("MCB.cfg", t1, "1.0")},
			map[string]string{cases: casesH + a + c, runs: runsH + aRun + cRun},
			map[string]string{cases: casesH + a + tlcRow("MCB.cfg", "check", "required") + c, runs: runsH + aRun + tlcRun("MCB.cfg", t1, "1.0") + cRun},
			[]string{recordLine(cases, 1, 1), recordLine(runs, 1, 1)},
			"the records " + cases + ", " + runs + " conflicted and were resolved at the merge by their class", "", true},
		{"a config both sides rewrote lands as one row, the card's newer run",
			base,
			map[string]string{cases: casesH + tlcRow("MCA.cfg", "ignore-terminal", "required"), runs: runsH + tlcRun("MCA.cfg", t1, "1.5")},
			map[string]string{cases: casesH + tlcRow("MCA.cfg", "check", "bench"), runs: runsH + tlcRun("MCA.cfg", t2, "2.5")},
			map[string]string{cases: casesH + tlcRow("MCA.cfg", "check", "bench"), runs: runsH + tlcRun("MCA.cfg", t2, "2.5")},
			[]string{recordLine(cases, 0, 1), recordLine(runs, 0, 1)}, "", "", true},
		{"a config both sides rewrote lands as one row, the tip's newer run",
			base,
			map[string]string{cases: casesH + tlcRow("MCA.cfg", "ignore-terminal", "required"), runs: runsH + tlcRun("MCA.cfg", t2, "1.5")},
			map[string]string{cases: casesH + tlcRow("MCA.cfg", "check", "bench"), runs: runsH + tlcRun("MCA.cfg", t1, "2.5")},
			map[string]string{cases: casesH + tlcRow("MCA.cfg", "ignore-terminal", "required"), runs: runsH + tlcRun("MCA.cfg", t2, "1.5")},
			[]string{recordLine(cases, 1, 0), recordLine(runs, 1, 0)}, "", "", true},
		{"a config both sides rewrote with no newer run is refused",
			base,
			map[string]string{cases: casesH + tlcRow("MCA.cfg", "ignore-terminal", "required")},
			map[string]string{cases: casesH + tlcRow("MCA.cfg", "check", "bench")},
			nil, nil, "", "its keyed table " + cases + " conflicts and MCA.cfg was rewritten on both sides with no newer run to choose by", false},
		{"a line merge that names a config twice is merged by config",
			plan(map[string]string{cases: casesH + a + x + y + c, runs: runsH + aRun + xRun + yRun + cRun}, "MCA.cfg", "MCB.cfg", "MCX.cfg", "MCY.cfg", "MCC.cfg"),
			map[string]string{cases: casesH + a + tlcRow("MCB.cfg", "check", "required") + x + y + c, runs: runsH + aRun + tlcRun("MCB.cfg", t1, "1.0") + xRun + yRun + cRun},
			map[string]string{cases: casesH + a + x + y + c + tlcRow("MCB.cfg", "check", "bench"), runs: runsH + aRun + xRun + yRun + cRun + tlcRun("MCB.cfg", t2, "2.0")},
			map[string]string{cases: casesH + a + tlcRow("MCB.cfg", "check", "bench") + x + y + c, runs: runsH + aRun + tlcRun("MCB.cfg", t2, "2.0") + xRun + yRun + cRun},
			[]string{dedupeLine(cases, 1), dedupeLine(runs, 1)},
			"the records " + cases + ", " + runs + " held a repeated row or config after the merge, taken out", "", true},
		{"an append-only record's repeated row is removed after the union merge",
			map[string]string{".gitattributes": deleted + " merge=union\n", deleted: "a\n"},
			map[string]string{deleted: "a\nb\n"},
			map[string]string{deleted: "b\na\nc\n"},
			map[string]string{deleted: "b\na\nc\n"},
			[]string{dedupeLine(deleted, 1)},
			"the records " + deleted + " held a repeated row or config after the merge, taken out", "", false},
		{"a tables lock conflict is regenerated",
			map[string]string{"schema.txt": "work.x\nwork.y\nwork.z\n", tablesLock: lockTop + "work.x\nwork.y\nwork.z\n"},
			map[string]string{"schema.txt": "work.a\nwork.x\nwork.y\nwork.z\n", tablesLock: "# THE TABLES ARE LOCKED.\n#\n# Change, 2026-10-05, a.\n#\n# Change, 2026-10-01, x.\n#\nwork.a\nwork.x\nwork.y\nwork.z\n"},
			map[string]string{"schema.txt": "work.x\nwork.y\nwork.z\nwork.b\n", tablesLock: "# THE TABLES ARE LOCKED.\n#\n# Change, 2026-10-06, b.\n#\n# Change, 2026-10-01, x.\n#\nwork.x\nwork.y\nwork.z\nwork.b\n"},
			map[string]string{tablesLock: "# THE TABLES ARE LOCKED.\n#\n# Change, 2026-10-05, a.\n#\n# Change, 2026-10-06, b.\n#\n# Change, 2026-10-01, x.\n#\nwork.a\nwork.x\nwork.y\nwork.z\nwork.b\n"},
			nil, "the generated ledgers " + tablesLock + " conflicted and were regenerated at the merge by TestTheTablesLockIsRegenerated", "", false},
		{"a tables lock whose comment a side rewrites is refused",
			map[string]string{"schema.txt": "work.x\n", tablesLock: lockTop + "work.x\n"},
			map[string]string{"schema.txt": "work.x\nwork.a\n", tablesLock: "# THE TABLES ARE LOCKED.\n#\n# Change, 2026-10-01, x, rewritten.\n#\nwork.x\nwork.a\n"},
			map[string]string{"schema.txt": "work.b\nwork.x\n", tablesLock: "# THE TABLES ARE LOCKED.\n#\n# Change, 2026-10-06, b.\n#\n# Change, 2026-10-01, x.\n#\nwork.b\nwork.x\n"},
			nil, nil, "", "its generated ledgers conflict and " + tablesLock + "'s comment is not the base's with lines added", false},
		{"a shrink-only ledger merges by intersection",
			map[string]string{shrinkLedger: "# ceiling: 3\ninternal/a\tTestA\ninternal/b\tTestB\ninternal/c\tTestC\n"},
			map[string]string{shrinkLedger: "# ceiling: 2\ninternal/b\tTestB\ninternal/c\tTestC\n"},
			map[string]string{shrinkLedger: "# ceiling: 2\ninternal/a\tTestA\ninternal/c\tTestC\n"},
			map[string]string{shrinkLedger: "# ceiling: 2\ninternal/c\tTestC\n"},
			[]string{unionLine(shrinkLedger, 1, 1)}, "", "", false},
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
			if tc.tlc {
				root := t.TempDir()
				for _, f := range strings.Split(r.git(r.remote, "ls-tree", "-r", "--name-only", "main", "tla/"), "\n") {
					require.NoError(t, os.MkdirAll(filepath.Join(root, "tla"), 0o755))
					require.NoError(t, os.WriteFile(filepath.Join(root, filepath.FromSlash(f)), []byte(r.git(r.remote, "show", "main:"+f)+"\n"), 0o600))
				}
				plan, err := tlc.LoadCases(root)
				require.NoError(t, err, "internal/tlc refuses the merged plan")
				recs, err := tlc.ReadRecordsFile(filepath.Join(root, runs))
				require.NoError(t, err, "internal/tlc refuses the merged records")
				var planned, recorded []string
				for _, c := range plan {
					planned = append(planned, c.Config)
				}
				for _, rec := range recs {
					recorded = append(recorded, rec.Config)
				}
				assert.ElementsMatch(t, planned, recorded, "one record per case")
			}
			r.clean()
		})
	}
}

// The append-only union is both sides' rows, the tip's first, each row once; the keyed
// merge is one row per config, the changed side's, the newer run's where both changed
// it, and refuses a config removed on one side and changed on the other or rewritten on
// both with no newer run; the insertion union is the base with both sides' added lines,
// the tip's first where they add at one place, and refuses a side that is not the base
// with lines added.
func TestTheMergeClassesResolveAsTheyAreDeclared(t *testing.T) {
	t.Parallel()
	got, nLeft, nRight := unionRecords([]byte("h\na\n"), []byte("h\na\nb\n"), []byte("h\na\nc\nb\n"))
	assert.Equal(t, "h\na\nb\nc\n", string(got))
	assert.Equal(t, [2]int{1, 1}, [2]int{nLeft, nRight})
	early, late := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	keyedOut, nLeft, nRight, err := mergeKeyed([]byte("h\na\t1\nb\t1\n"), []byte("h\na\t2\nb\t1\nc\t1\n"), []byte("h\na\t3\nd\t1\n"),
		map[string]time.Time{"a": early}, map[string]time.Time{"a": late})
	require.NoError(t, err)
	assert.Equal(t, "h\na\t3\nc\t1\nd\t1\n", string(keyedOut), "a rewritten on both: the newer run's row; b removed by one side; c and d added")
	assert.Equal(t, [2]int{1, 3}, [2]int{nLeft, nRight})
	_, _, _, err = mergeKeyed([]byte("h\na\t1\n"), []byte("h\na\t2\n"), []byte("h\na\t3\n"), nil, nil)
	assert.ErrorContains(t, err, "a was rewritten on both sides with no newer run to choose by")
	_, _, _, err = mergeKeyed([]byte("h\na\t1\n"), []byte("h\n"), []byte("h\na\t3\n"), nil, nil)
	assert.ErrorContains(t, err, "a was removed on one side and changed on the other")
	assert.Equal(t, 1, keyedTwice([]byte("h\na\t1\nb\t1\na\t2\n")))
	started := runStarted([]byte("config\tstarted_utc\na\t2026-10-06T00:00:00.000000+00:00\nb\tnot a time\n"))
	assert.Len(t, started, 1, "a start that does not read is left out")
	assert.True(t, started["a"].Equal(late))
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

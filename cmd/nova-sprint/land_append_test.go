package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const caseHead = "config\tmodule\texpected\n"
const runHead = "config\tmodule\tinput_sha256\tinput_files\tjar_sha256\tjava_version\thost\tcpus\tstarted_utc\tworkers\tgenerated\tdistinct\tseconds\texit\tresult\texpected\tproperty\tbudget\tmode\n"

// Two cards that each append a row to an append-only record land with both rows, whether
// git merges the record itself (.gitattributes merge=union, the base's) or stops on it;
// a row both appended is one row; a side that removes a row is no append, refused as a
// conflict as before, the merge aborted; a keyed RUNS.tsv record merges by config.
func TestAnAppendOnlyRecordConflictMergesByUnion(t *testing.T) {
	t.Parallel()
	const attrs = "tla/CASES.tsv merge=union\n"
	base := caseHead + "A.cfg\tA.tla\tpass\n"
	baseRun := runHead + "A.cfg\tA.tla\tsha1\t8\tjar1\t21\thost1\t64\t2026-10-06T00:00:00Z\t2\t10\t10\t1.0\t0\tPASS\tpass\t-\t110\tbounded\n"
	for _, tc := range []struct {
		name, attrs   string
		path          string
		base          string
		first, second string
		want          string // "" is a refusal
		log           string
		checkNoDup    string
	}{
		{name: "git unions the rows", attrs: attrs, first: base + "B.cfg\tB.tla\tpass\n", second: base + "C.cfg\tC.tla\tpass\n",
			want: base + "B.cfg\tB.tla\tpass\nC.cfg\tC.tla\tpass\n"},
		{name: "a row both append is one", attrs: attrs, first: base + "B.cfg\tB.tla\tpass\n", second: base + "B.cfg\tB.tla\tpass\nC.cfg\tC.tla\tpass\n",
			want: base + "B.cfg\tB.tla\tpass\nC.cfg\tC.tla\tpass\n"},
		{name: "the lander unions a conflict git stops on", attrs: "", first: base + "B.cfg\tB.tla\tpass\n", second: base + "C.cfg\tC.tla\tpass\n",
			want: base + "B.cfg\tB.tla\tpass\nC.cfg\tC.tla\tpass\n", log: "record tla/CASES.tsv: resolved as the union of appended rows (+1 tip, +1 card)"},
		{name: "a removed row is no append", attrs: "", first: caseHead + "B.cfg\tB.tla\tpass\n", second: base + "C.cfg\tC.tla\tpass\n"},
		{name: "a removed row with attrs is refused", attrs: attrs, first: caseHead + "B.cfg\tB.tla\tpass\n", second: base + "C.cfg\tC.tla\tpass\n"},
		{name: "runs record merges by config: one replaces, one appends", attrs: attrs, path: "tla/RUNS.tsv", base: baseRun,
			first:  runHead + "A.cfg\tA.tla\tsha1\t8\tjar1\t21\thost1\t64\t2026-10-06T01:00:00Z\t2\t10\t10\t1.5\t0\tPASS\tpass\t-\t110\tbounded\n",
			second: baseRun + "B.cfg\tB.tla\tsha2\t8\tjar1\t21\thost1\t64\t2026-10-06T02:00:00Z\t2\t20\t20\t2.0\t0\tPASS\tpass\t-\t110\tbounded\n",
			want: runHead +
				"A.cfg\tA.tla\tsha1\t8\tjar1\t21\thost1\t64\t2026-10-06T01:00:00Z\t2\t10\t10\t1.5\t0\tPASS\tpass\t-\t110\tbounded\n" +
				"B.cfg\tB.tla\tsha2\t8\tjar1\t21\thost1\t64\t2026-10-06T02:00:00Z\t2\t20\t20\t2.0\t0\tPASS\tpass\t-\t110\tbounded\n",
			log:        "record tla/RUNS.tsv: resolved by config (+1 tip, +1 card)",
			checkNoDup: "A.cfg\t",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			r.git(r.worker, "switch", "-q", "--detach", "origin/main")
			p := tc.path
			if p == "" {
				p = "tla/CASES.tsv"
			}
			baseContent := tc.base
			if baseContent == "" {
				baseContent = base
			}
			files := map[string]string{p: baseContent, "notes.tsv": "one\n"}
			if tc.attrs != "" {
				files[".gitattributes"] = tc.attrs
			}
			r.files("the records", files)
			r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
			r.git(r.worker, "fetch", "-q", "origin")
			r.ok("add --stream s1 --count 2")
			heads := map[string]string{"s1-1": r.card("s1-1", map[string]string{p: tc.first}), "s1-2": r.card("s1-2", map[string]string{p: tc.second})}
			r.queued(heads, "s1-1", "s1-2")
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
			if tc.want == "" {
				assert.Equal(t, 1, code, out+errs)
				assert.Contains(t, errs, "is not an append: the left side removes a row, which is no append")
				assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "merging/stuck"}, r.places("s1-1", "s1-2"))
				assert.Empty(t, r.git(r.clone, "status", "--porcelain", "--untracked-files=all"), "the refused merge is aborted")
				return
			}
			assert.Equal(t, 0, code, out+errs)
			assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main")
			if tc.log != "" {
				assert.Contains(t, out, "NOTE "+tc.log)
			}
			got := r.git(r.remote, "show", "main:"+p) + "\n"
			assert.Equal(t, tc.want, got, "both cards' rows, each once")
			if tc.checkNoDup != "" {
				assert.Equal(t, 1, strings.Count(got, tc.checkNoDup), "pinned that no config appears twice: %s", tc.checkNoDup)
			}
		})
	}
}

// resolveRunsByConfig resolves each case by config, keeping each case once.
func TestResolveRunsByConfig(t *testing.T) {
	t.Parallel()
	base := []byte(runHead + "A.cfg\tA.tla\tsha1\t8\tjar1\t21\thost1\t64\t2026-10-06T00:00:00Z\t2\t10\t10\t1.0\t0\tPASS\tpass\t-\t110\tbounded\n")
	ours := []byte(runHead + "A.cfg\tA.tla\tsha1\t8\tjar1\t21\thost1\t64\t2026-10-06T01:00:00Z\t2\t10\t10\t1.5\t0\tPASS\tpass\t-\t110\tbounded\n")
	theirs := []byte(runHead + "A.cfg\tA.tla\tsha1\t8\tjar1\t21\thost1\t64\t2026-10-06T00:00:00Z\t2\t10\t10\t1.0\t0\tPASS\tpass\t-\t110\tbounded\n" +
		"B.cfg\tB.tla\tsha2\t8\tjar1\t21\thost1\t64\t2026-10-06T02:00:00Z\t2\t20\t20\t2.0\t0\tPASS\tpass\t-\t110\tbounded\n")
	out, nOurs, nTheirs, err := resolveRunsByConfig(base, ours, theirs)
	require.NoError(t, err)
	assert.Equal(t, 1, nOurs)
	assert.Equal(t, 1, nTheirs)
	want := runHead + "A.cfg\tA.tla\tsha1\t8\tjar1\t21\thost1\t64\t2026-10-06T01:00:00Z\t2\t10\t10\t1.5\t0\tPASS\tpass\t-\t110\tbounded\n" +
		"B.cfg\tB.tla\tsha2\t8\tjar1\t21\thost1\t64\t2026-10-06T02:00:00Z\t2\t20\t20\t2.0\t0\tPASS\tpass\t-\t110\tbounded\n"
	assert.Equal(t, want, string(out))

	theirsConflict := []byte(runHead + "A.cfg\tA.tla\tsha1\t8\tjar1\t21\thost1\t64\t2026-10-06T03:00:00Z\t2\t10\t10\t1.8\t0\tPASS\tpass\t-\t110\tbounded\n")
	_, _, _, err = resolveRunsByConfig(base, ours, theirsConflict)
	assert.ErrorContains(t, err, "both sides changed the record for A.cfg")
}

// A conflict in the tables lock is regenerated: the tip's comments, the card's added
// comment paragraph, and the body the schema renders, whichever side's body line was
// edited by hand.
func TestATablesLockConflictIsRegeneratedFromTheSchema(t *testing.T) {
	t.Parallel()
	body := renderTablesLock()
	stale := strings.Replace(body, "view ", "view stale ", 1)
	head := "# THE TABLES ARE LOCKED.\n#\n# Change, one.\n\n"
	r := newLandRig(t)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the lock", map[string]string{tablesLockFile: head + stale})
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	r.ok("add --stream s1 --count 2")
	heads := map[string]string{
		"s1-1": r.card("s1-1", map[string]string{tablesLockFile: head + "# Change, tip.\n\n" + strings.Replace(stale, "view stale ", "view tip ", 1)}),
		"s1-2": r.card("s1-2", map[string]string{tablesLockFile: head + "# Change, card.\n\n" + strings.Replace(stale, "view stale ", "view card ", 1)}),
	}
	r.queued(heads, "s1-1", "s1-2")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, 0, code, out+errs)
	assert.Contains(t, out, "NOTE generated "+tablesLockFile+": regenerated from the schema at the merge")
	got := r.git(r.remote, "show", "main:"+tablesLockFile) + "\n"
	assert.True(t, strings.HasSuffix(got, "\n"+body), "the body is the schema's: %s", got)
	assert.Contains(t, got, "# Change, tip.")
	assert.Contains(t, got, "# Change, card.")
	assert.NotContains(t, got, "view tip")
}

// The renderer here is the one internal/ci pins: the repository's own lock, regenerated
// from itself, is unchanged.
func TestTheTablesLockRegeneratesToItself(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(tablesLockFile)))
	require.NoError(t, err)
	assert.Equal(t, string(raw), string(regenTablesLock(raw, raw, raw, renderTablesLock())))
}

// Rows that came twice are dropped after their first, and only then.
func TestDedupeRowsKeepsTheFirstOfEach(t *testing.T) {
	t.Parallel()
	out, n := dedupeRows([]byte("h\na\nb\na\nb\nc\n"))
	assert.Equal(t, "h\na\nb\nc\n", string(out))
	assert.Equal(t, 2, n)
	out, n = dedupeRows([]byte("h\na"))
	assert.Equal(t, "h\na", string(out))
	assert.Zero(t, n)
}

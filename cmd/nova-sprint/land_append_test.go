package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const caseHead = "config\tmodule\texpected\tproperty\tdeadlock\tgroup\tgate\tdebt\n"
const runHead = "config\tmodule\tinput_sha256\tinput_files\tjar_sha256\tjava_version\thost\tcpus\tstarted_utc\tworkers\tgenerated\tdistinct\tseconds\texit\tresult\texpected\tproperty\tbudget\tmode\n"

// TestAnAppendOnlyRecordConflictMergesByUnion tests that keyed TLA+ tables merge by config,
// append-only files merge by union, and no config appears twice.
func TestAnAppendOnlyRecordConflictMergesByUnion(t *testing.T) {
	t.Parallel()
	baseCase := caseHead + "A.cfg\tA.tla\tpass\t-\tignore-terminal\tbench\trequired\t-\n"
	baseRun := runHead + "A.cfg\tA.tla\tsha1\t8\tjar1\t21\thost1\t64\t2026-10-06T00:00:00Z\t2\t10\t10\t1.0\t0\tPASS\tpass\t-\t110\tbounded\n"
	for _, tc := range []struct {
		name, attrs   string
		path          string
		base          string
		first, second string
		want          string // "" is a refusal
		log           string
		checkNoDup    string
		errSubstr     string
	}{
		{
			name:   "two branches each appending a CASES.tsv row merge with both rows",
			path:   "tla/CASES.tsv",
			base:   baseCase,
			first:  baseCase + "B.cfg\tB.tla\tpass\t-\tignore-terminal\tbench\trequired\t-\n",
			second: baseCase + "C.cfg\tC.tla\tpass\t-\tignore-terminal\tbench\trequired\t-\n",
			want: baseCase +
				"B.cfg\tB.tla\tpass\t-\tignore-terminal\tbench\trequired\t-\n" +
				"C.cfg\tC.tla\tpass\t-\tignore-terminal\tbench\trequired\t-\n",
			log:        "record tla/CASES.tsv: resolved by config (+1 tip, +1 card)",
			checkNoDup: "B.cfg\t",
		},
		{
			name:   "a CASES.tsv row both append alike is one row",
			path:   "tla/CASES.tsv",
			base:   baseCase,
			first:  baseCase + "B.cfg\tB.tla\tpass\t-\tignore-terminal\tbench\trequired\t-\n",
			second: baseCase + "B.cfg\tB.tla\tpass\t-\tignore-terminal\tbench\trequired\t-\nC.cfg\tC.tla\tpass\t-\tignore-terminal\tbench\trequired\t-\n",
			want: baseCase +
				"B.cfg\tB.tla\tpass\t-\tignore-terminal\tbench\trequired\t-\n" +
				"C.cfg\tC.tla\tpass\t-\tignore-terminal\tbench\trequired\t-\n",
			log:        "record tla/CASES.tsv: resolved by config (+1 tip, +1 card)",
			checkNoDup: "B.cfg\t",
		},
		{
			name: "one side rerunning a RUNS.tsv row while the other appends one merges with no config twice",
			path: "tla/RUNS.tsv",
			base: baseRun,
			first: runHead +
				"A.cfg\tA.tla\tsha1\t8\tjar1\t21\thost1\t64\t2026-10-06T01:00:00Z\t2\t10\t10\t1.5\t0\tPASS\tpass\t-\t110\tbounded\n",
			second: baseRun +
				"B.cfg\tB.tla\tsha2\t8\tjar1\t21\thost1\t64\t2026-10-06T02:00:00Z\t2\t20\t20\t2.0\t0\tPASS\tpass\t-\t110\tbounded\n",
			want: runHead +
				"A.cfg\tA.tla\tsha1\t8\tjar1\t21\thost1\t64\t2026-10-06T01:00:00Z\t2\t10\t10\t1.5\t0\tPASS\tpass\t-\t110\tbounded\n" +
				"B.cfg\tB.tla\tsha2\t8\tjar1\t21\thost1\t64\t2026-10-06T02:00:00Z\t2\t20\t20\t2.0\t0\tPASS\tpass\t-\t110\tbounded\n",
			log:        "record tla/RUNS.tsv: resolved by config (+1 tip, +1 card)",
			checkNoDup: "A.cfg\t",
		},
		{
			name: "a CASES.tsv row the tip removed stays removed and the card still lands",
			path: "tla/CASES.tsv",
			base: baseCase,
			// tip removed A.cfg, keeping only header
			first: caseHead,
			// card kept base (A) and added B.cfg
			second: baseCase + "B.cfg\tB.tla\tpass\t-\tignore-terminal\tbench\trequired\t-\n",
			want: caseHead +
				"B.cfg\tB.tla\tpass\t-\tignore-terminal\tbench\trequired\t-\n",
			log: "record tla/CASES.tsv: resolved by config (+0 tip, +1 card)",
		},
		{
			name:      "a CASES.tsv config both sides changed differently is refused",
			path:      "tla/CASES.tsv",
			base:      baseCase,
			first:     caseHead + "A.cfg\tA.tla\tfail\t-\tignore-terminal\tbench\trequired\t-\n",
			second:    caseHead + "A.cfg\tA.tla\tinvariant\t-\tignore-terminal\tbench\trequired\t-\n",
			want:      "",
			errSubstr: "both sides changed the record for A.cfg",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			r.git(r.worker, "switch", "-q", "--detach", "origin/main")
			p := tc.path
			baseContent := tc.base
			files := map[string]string{p: baseContent, "notes.tsv": "one\n"}
			if tc.attrs != "" {
				files[".gitattributes"] = tc.attrs
			}
			r.files("the records", files)
			r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
			r.git(r.worker, "fetch", "-q", "origin")
			r.ok("add --stream s1 --count 2")
			heads := map[string]string{
				"s1-1": r.card("s1-1", map[string]string{p: tc.first}),
				"s1-2": r.card("s1-2", map[string]string{p: tc.second}),
			}
			r.queued(heads, "s1-1", "s1-2")
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
			if tc.want == "" {
				assert.Equal(t, 1, code, out+errs)
				if tc.errSubstr != "" {
					assert.Contains(t, errs, tc.errSubstr)
				}
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

// TestResolveTableByConfig tests keyed table resolution by config.
func TestResolveTableByConfig(t *testing.T) {
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

	// When both sides rerun the same config in RUNS.tsv, the later started_utc wins
	theirsLater := []byte(runHead + "A.cfg\tA.tla\tsha1\t8\tjar1\t21\thost1\t64\t2026-10-06T03:00:00Z\t2\t10\t10\t1.8\t0\tPASS\tpass\t-\t110\tbounded\n")
	out, nOurs, nTheirs, err = resolveRunsByConfig(base, ours, theirsLater)
	require.NoError(t, err)
	assert.Equal(t, 0, nOurs)
	assert.Equal(t, 1, nTheirs)
	wantLater := runHead + "A.cfg\tA.tla\tsha1\t8\tjar1\t21\thost1\t64\t2026-10-06T03:00:00Z\t2\t10\t10\t1.8\t0\tPASS\tpass\t-\t110\tbounded\n"
	assert.Equal(t, wantLater, string(out))

	// In CASES.tsv, both sides changing the same config differently is refused
	baseCase := []byte(caseHead + "A.cfg\tA.tla\tpass\n")
	oursCase := []byte(caseHead + "A.cfg\tA.tla\tfail\n")
	theirsCase := []byte(caseHead + "A.cfg\tA.tla\tinvariant\n")
	_, _, _, err = resolveCasesByConfig(baseCase, oursCase, theirsCase)
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

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeGoScript is the go program of these tests (app.landGo): it logs each run as
// GOCACHE|args and runs nothing; its vet fails when the tree holds a red-*.txt, so a head
// that adds one turns every tree after it red.
const fakeGoScript = `#!/bin/sh
printf '%s|%s\n' "${GOCACHE:--}" "$*" >> LOG
case "$1" in
vet) if ls red-*.txt >/dev/null 2>&1; then echo './red.txt:1:1: the tree is red'; exit 1; fi ;;
esac
exit 0
`

// fakeGo puts the fake go in the rig and a go.mod (and extra) on origin's main, so the lander gates;
// it returns the run log.
func fakeGo(r *landRig, extra map[string]string) string {
	r.t.Helper()
	log := filepath.Join(r.dir, "go-runs")
	bin := filepath.Join(r.dir, "fake-go")
	require.NoError(r.t, os.WriteFile(bin, []byte(strings.Replace(fakeGoScript, "LOG", log, 1)), 0o700))
	r.a.landGo = bin
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	module := map[string]string{"go.mod": "module example.com/m\n\ngo 1.21\n"}
	for f, text := range extra {
		module[f] = text
	}
	r.files("the module", module)
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	return log
}

// goRuns is the fake go's runs whose arguments start with args, each as GOCACHE|args.
func goRuns(t *testing.T, log, args string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if _, a, ok := strings.Cut(line, "|"); ok && strings.HasPrefix(a, args) {
			out = append(out, line)
		}
	}
	return out
}

// parents is how many parents each first-parent commit of origin's main has, newest first,
// down to (not including) the commit subject stop.
func (r *landRig) parents(stop string) []int {
	r.t.Helper()
	var out []int
	for _, line := range strings.Split(r.git(r.remote, "log", "--first-parent", "--format=%s|%p", "main"), "\n") {
		subject, ps, _ := strings.Cut(line, "|")
		if subject == stop {
			break
		}
		out = append(out, len(strings.Fields(ps)))
	}
	return out
}

// A batch is merged head by head, each --no-ff with its scripted checks, then gated once on
// its tip: a green batch of five costs the base's gate and one more. A red tip is bisected:
// the first head whose prefix is red ends the batch as a conflict, the green prefix before
// it lands (gated green), and the heads behind it stay queued, as a head that does not
// merge ends a batch. A head that fails the scripted checks stops the merges, and the gate
// runs once on the heads before it.
func TestLandMergesEveryHeadThenGatesTheBatchOnce(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		red    int    // the card (1-based) whose head adds red-<id>.txt; 0 for none
		check  int    // the card whose head fails the scripted checks (a file outside its PATHS); 0 for none
		landed int    // how many land
		builds int    // the gate's builds: the base's, the batch's and the bisection's
		blamed string // the reason's end for the card after the landed ones; "" when all land
	}{
		{"five green heads: one gate for the batch", 0, 0, 5, 2, ""},
		{"the third of five is red: three gates to find it", 3, 0, 2, 4, "fails the tree gate: go vet ./...: exit status 1: ./red.txt:1:1: the tree is red"},
		{"the first is red: nothing lands", 1, 0, 0, 4, "fails the tree gate: go vet ./...: exit status 1: ./red.txt:1:1: the tree is red"},
		{"the last is red: the four before it land", 5, 0, 4, 5, "fails the tree gate: go vet ./...: exit status 1: ./red.txt:1:1: the tree is red"},
		{"the scripted checks stop the merges at the third: one gate on the two before", 0, 3, 2, 2, "fails the lander's checks: it changes files outside its PATHS (E12): other.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			log := fakeGo(r, nil)
			dir := t.TempDir()
			ids := []string{"c1", "c2", "c3", "c4", "c5"}
			var briefs string
			for _, id := range ids {
				briefs += " --brief-file " + writeNeedsBrief(t, dir, id, "Write "+id+".\nPATHS: "+id+".txt, red-"+id+".txt", "")
			}
			r.ok("add --stream s1" + briefs)
			heads := map[string]string{}
			for i, id := range ids {
				files := map[string]string{id + ".txt": id + "\n"}
				switch i + 1 {
				case tc.red:
					files["red-"+id+".txt"] = "red\n"
				case tc.check:
					files["other.txt"] = "other\n"
				}
				heads[id] = r.card(id, files)
			}
			r.queued(heads, ids...)
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
			assert.Len(t, goRuns(t, log, "build ./..."), tc.builds, "the gate's builds")
			want := map[string]string{}
			for i, id := range ids {
				switch {
				case i < tc.landed:
					want[id] = "landed/merged"
				case i == tc.landed:
					want[id] = "merging/stuck"
				default:
					want[id] = "merging/queued"
				}
			}
			if tc.landed == len(ids) {
				assert.Equal(t, 0, code, out+errs)
			} else {
				assert.Equal(t, 1, code, out+errs)
				bad := ids[tc.landed]
				assert.Contains(t, errs, "LAND REFUSED stream=s1 cards=1 base=- tip=- ids="+bad+" fact=conflict reason=the head "+heads[bad]+" of "+bad+" "+tc.blamed)
			}
			if tc.landed > 0 {
				assert.Contains(t, out, "LAND OK stream=s1 cards="+strconv.Itoa(tc.landed)+" base=main")
			} else {
				assert.NotContains(t, out, "LAND OK")
			}
			assert.Equal(t, want, r.places(ids...))
			var subjects []string
			for i := tc.landed; i > 0; i-- {
				subjects = append(subjects, "land "+ids[i-1]+" (sprint stream s1)")
			}
			assert.Equal(t, append(subjects, "the module", "base"), r.mainLog(), "one merge commit a landed card, in queue order")
			var want2 []int // nil when nothing landed, as parents returns it
			for range tc.landed {
				want2 = append(want2, 2)
			}
			assert.Equal(t, want2, r.parents("the module"), "every head is merged --no-ff")
			assert.NotContains(t, r.git(r.remote, "ls-tree", "--name-only", "main"), "red-", "no red tree is pushed")
			assert.Empty(t, r.git(r.clone, "status", "--porcelain", "--untracked-files=all"), "the clone is clean")
			r.clean()
		})
	}
}

// The generated ledgers are regenerated once a batch, not once a head: three cards whose
// merges each conflict on the shared ledger take the tip's side at each merge, and one
// regeneration at the batch's tip (its update run to a fixed point: two runs) writes one
// commit naming the cards; every card keeps its own --no-ff merge.
func TestLandRegeneratesTheLedgersOncePerBatch(t *testing.T) {
	t.Parallel()
	count := filepath.Join(t.TempDir(), "runs")
	r := ledgerRig(t, "echo run >> "+count+"\n"+fakeLedgerRun)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("more debt", map[string]string{"debt/c": "c\n", fakeLedger: "# ceiling: 3\na\nb\nc\n"})
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	r.ok("add --stream s1 --count 3")
	heads := map[string]string{
		"s1-1": r.card("s1-1", map[string]string{"debt/a": "", fakeLedger: "# ceiling: 2\nb\nc\n"}),
		"s1-2": r.card("s1-2", map[string]string{"debt/b": "", fakeLedger: "# ceiling: 2\na\nc\n"}),
		"s1-3": r.card("s1-3", map[string]string{"debt/c": "", fakeLedger: "# ceiling: 2\na\nb\n"}),
	}
	r.queued(heads, "s1-1", "s1-2", "s1-3")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, 0, code, out+errs)
	assert.Contains(t, out, "LAND OK stream=s1 cards=3 base=main")
	runs, err := os.ReadFile(count)
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(string(runs), "run"), "one regeneration for the batch: a run that writes, a run that settles")
	assert.Equal(t, []string{"land: the generated ledgers regenerated (sprint stream s1)", "land s1-3 (sprint stream s1)", "land s1-2 (sprint stream s1)",
		"land s1-1 (sprint stream s1)", "more debt", "the debt", "base"}, r.mainLog())
	assert.Equal(t, []int{1, 2, 2, 2}, r.parents("more debt"), "the regeneration is one commit after the three --no-ff merges")
	assert.Equal(t, "# ceiling: 0", r.git(r.remote, "show", "main:"+fakeLedger), "the ledger is the batch tip's, regenerated")
	assert.Contains(t, r.git(r.remote, "log", "-1", "--format=%b", "main"), "conflicted at the merges of s1-2, s1-3.")
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged", "s1-3": "landed/merged"}, r.places("s1-1", "s1-2", "s1-3"))
	r.clean()
}

// A stream with more cards queued than --batch-max lands them in batches of at most that
// many, in queue order, each merged and gated once; a batch's tip the gate passed with the
// tree tests is the next batch's base, which is not gated again. --batch-max under one is
// refused before anything is read.
func TestLandBatchesAtMostBatchMaxCards(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	log := fakeGo(r, map[string]string{"internal/docs/doc.go": "package docs\n"}) // a tree test package
	r.ok("add --stream s1 --count 5")
	ids := []string{"s1-1", "s1-2", "s1-3", "s1-4", "s1-5"}
	heads := map[string]string{}
	for _, id := range ids {
		heads[id] = r.card(id, map[string]string{id + ".md": id + "\n"}) // a document: the tree tests run
	}
	r.queued(heads, ids...)
	code, _, errs := r.do("land --repo-dir " + r.clone + " --base main --batch-max 0")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--batch-max wants a count of at least 1, not 0")
	out := r.ok("land --repo-dir " + r.clone + " --base main --batch-max 2")
	assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main tip="+r.git(r.remote, "rev-parse", "main~3")+" ids=s1-1..s1-2 ")
	assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main tip="+r.git(r.remote, "rev-parse", "main~1")+" ids=s1-3..s1-4 ")
	assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main tip="+r.git(r.remote, "rev-parse", "main")+" ids=s1-5 ")
	assert.Equal(t, []string{"land s1-5 (sprint stream s1)", "land s1-4 (sprint stream s1)", "land s1-3 (sprint stream s1)", "land s1-2 (sprint stream s1)",
		"land s1-1 (sprint stream s1)", "the module", "base"}, r.mainLog())
	assert.Len(t, goRuns(t, log, "build ./..."), 4, "the base once, then each of the three batches once")
	assert.Len(t, goRuns(t, log, "test "), 4, "every gate ran the tree tests")
	for _, id := range ids {
		assert.Equal(t, "landed/merged", r.places(id)[id])
	}
	r.clean()
}

// Every go run the lander makes uses its own GOCACHE when it has one (app.landGoCache): the
// gate's and the update runs, never the caller's.
func TestLandGoRunsUseTheLandersOwnCache(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	log := fakeGo(r, nil)
	cache := filepath.Join(r.dir, "land", "go-build")
	r.a.landGoCache = func() (string, error) { return cache, nil }
	r.a.gitEnv = append(r.a.gitEnv, "GOCACHE="+filepath.Join(r.dir, "callers-cache"))
	heads := landTwo(r)
	require.Len(t, heads, 2)
	r.ok("land --repo-dir " + r.clone + " --base main")
	runs := goRuns(t, log, "")
	require.NotEmpty(t, runs)
	for _, run := range runs {
		assert.True(t, strings.HasPrefix(run, cache+"|"), run)
	}
	assert.DirExists(t, cache)
	r.clean()
}

// The batch's decisions, apart from git: the bisection asks the whole batch first and then
// halves between the base and the shortest prefix known red; a batch is the run of cards of
// one repository and base, at most the cap; the cap's default; the words a red probe blames
// a card with.
func TestLandBatchDecisions(t *testing.T) {
	t.Parallel()
	t.Run("bisect", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name  string
			n     int
			red   int // the first red head (1-based): red(k) is k >= red; 0 for none
			green int
			asked []int
		}{
			{"an empty batch", 0, 0, 0, nil},
			{"a green batch is one gate", 8, 0, 8, []int{8}},
			{"one head, red", 1, 1, 0, []int{1}},
			{"the first of eight", 8, 1, 0, []int{8, 4, 2, 1}},
			{"the fifth of eight", 8, 5, 4, []int{8, 4, 6, 5}},
			{"the last of eight", 8, 8, 7, []int{8, 4, 6, 7}},
			{"the third of five", 5, 3, 2, []int{5, 2, 3}},
			{"the 20th of 32: five more gates", 32, 20, 19, []int{32, 16, 24, 20, 18, 19}},
		} {
			var asked []int
			got := greenPrefix(tc.n, func(k int) bool { asked = append(asked, k); return tc.red > 0 && k >= tc.red })
			assert.Equal(t, tc.green, got, tc.name)
			assert.Equal(t, tc.asked, asked, tc.name)
		}
	})
	t.Run("batches", func(t *testing.T) {
		t.Parallel()
		card := func(repo, base string) landCard { return landCard{repo: repo, base: base} }
		five := []landCard{card("r", "main"), card("r", "main"), card("r", "main"), card("r", "dev"), card("r", "main")}
		for _, tc := range []struct {
			name  string
			cards []landCard
			max   int
			n     int
		}{
			{"the run of one repository and base", five, 32, 3},
			{"cut at the cap", five, 2, 2},
			{"a cap of one", five, 1, 1},
			{"one card", five[3:4], 32, 1},
			{"another repository ends it", []landCard{card("r", "main"), card("s", "main")}, 32, 1},
		} {
			assert.Equal(t, tc.n, batchLen(tc.cards, tc.max), tc.name)
		}
		env := func(v string) func(string) string {
			return func(k string) string {
				if k == landBatchMaxEnv {
					return v
				}
				return ""
			}
		}
		assert.Equal(t, landBatchMax, landBatchMaxDefault(env("")))
		assert.Equal(t, 8, landBatchMaxDefault(env(" 8 ")))
		assert.Equal(t, landBatchMax, landBatchMaxDefault(env("0")), "a cap under one is not a cap")
		assert.Equal(t, landBatchMax, landBatchMaxDefault(env("many")))
	})
	t.Run("blame", func(t *testing.T) {
		t.Parallel()
		plain := landCard{id: "c2", head: "abc1234"}
		deferred := landCard{id: "c3", head: "def5678", regen: []string{"l.txt"}, mergeWhy: "git merge: CONFLICT (content)", mergeKind: "ledger", mergePaths: []string{"l.txt"}}
		gate := blamed(plain, landProbe{why: "go vet ./...: exit status 1: x"})
		assert.Equal(t, "the head abc1234 of c2 fails the tree gate: go vet ./...: exit status 1: x", gate.why)
		assert.Equal(t, "", gate.kind)
		regen := blamed(deferred, landProbe{why: "its generated ledgers conflict and T did not regenerate them", ledger: true})
		assert.Equal(t, "the head def5678 of c3 does not merge: git merge: CONFLICT (content); its generated ledgers conflict and T did not regenerate them", regen.why)
		assert.Equal(t, "ledger", regen.kind)
		assert.Equal(t, []string{"l.txt"}, regen.paths)
		other := blamed(plain, landProbe{why: "w", ledger: true})
		assert.Equal(t, "the head abc1234 of c2 fails the generated ledgers' regeneration at the batch's tip: w", other.why)
		assert.False(t, deferredLedgers([]landCard{plain}))
		assert.True(t, deferredLedgers([]landCard{plain, deferred}))
	})
}

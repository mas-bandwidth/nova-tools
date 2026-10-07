package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The merge step's receipt for a landing holds more than the batch's landings: the waiting
// cards the landing released (`<id> waiting -> ready (its needs landed)`) and the sentinels
// it reached (`sentinel <id> reached`) move in the same step. movedExactly reads the lines
// about the batch's cards alone: each landed exactly once, no other line about one of them,
// lines about other cards allowed. Until 2026-10-07 any landing that released a waiting
// card was LAND FAILED ("pushed ... and NOT reported (moved N+k)") although it committed,
// which stopped the stream's next batches, exited 2 and could roll the server back.
func TestMovedExactlyReadsTheBatchsLandingsAmongTheStepsOtherMoves(t *testing.T) {
	t.Parallel()
	ids := []string{"a", "b"}
	for _, tc := range []struct {
		name  string
		moved []string
		want  bool
	}{
		{"the landings alone", []string{"a merging -> landed", "b merging -> landed"}, true},
		{"in the queue's order at the report", []string{"b merging -> landed", "a merging -> landed"}, true},
		{"with a waiting card released and a sentinel reached", []string{"a merging -> landed", "c waiting -> ready (its needs landed)", "b merging -> landed", "sentinel s reached"}, true},
		{"with a landing note", []string{"a merging -> landed (regenerated)", "b merging -> landed"}, true},
		{"a card of the batch not landed", []string{"a merging -> landed", "c waiting -> ready (its needs landed)"}, false},
		{"a card of the batch moved elsewhere", []string{"a merging -> landed", "b merging -> stuck"}, false},
		{"a card of the batch landed twice", []string{"a merging -> landed", "a merging -> landed", "b merging -> landed"}, false},
		{"another batch's receipt", []string{"c merging -> landed", "d merging -> landed"}, false},
		{"nothing moved", nil, false},
	} {
		assert.Equal(t, tc.want, movedExactly(tc.moved, ids), tc.name)
	}
	assert.False(t, movedExactly([]string{"a merging -> landed", "a merging -> landed"}, []string{"a", "a"}), "a card named twice is not a batch")
}

// The pass's decision for a batch merged again onto a base that moved (landpass.go,
// again): the same cards merged and the files the batch changes disjoint from the files
// landed since is pushed with no gate; a file both changed, or a batch that merges fewer
// cards, gates the combined tree once, naming the files that collided.
func TestNeedsGateIsTheDisjointFilesRule(t *testing.T) {
	t.Parallel()
	collide, gate := needsGate([]string{"a", "b"}, []string{"a", "b"}, []string{"x.go", "y.go"}, []string{"z.go", "NOTES.md"})
	assert.False(t, gate, "disjoint files, the same cards: no gate")
	assert.Empty(t, collide)
	collide, gate = needsGate([]string{"a"}, []string{"a"}, []string{"y.go", "x.go", "NOTES.md"}, []string{"NOTES.md", "x.go", "other.go"})
	assert.True(t, gate, "a file both changed gates the combined tree")
	assert.Equal(t, []string{"NOTES.md", "x.go"}, collide, "the files that collided, sorted")
	collide, gate = needsGate([]string{"a", "b"}, []string{"a"}, []string{"x.go"}, []string{"z.go"})
	assert.True(t, gate, "fewer cards merged onto the new tip: the tree is not the one gated")
	assert.Empty(t, collide)
	_, gate = needsGate(nil, nil, nil, nil)
	assert.False(t, gate)
}

// runBounded runs its jobs at most width at a time and every one of them (the worker
// bound of phase 1, --land-parallel). The bound is read as the jobs run: each job counts
// itself in, and the first width jobs wait for each other before any counts out, so the
// width is reached exactly and never passed; no clock.
func TestRunBoundedRunsAtMostWidthAtATime(t *testing.T) {
	t.Parallel()
	for _, width := range []int{1, 2, 3} {
		var mu sync.Mutex
		running, most, ran := 0, 0, 0
		full := make(chan struct{})
		var once sync.Once
		runBounded(width, 5, func(i int) {
			mu.Lock()
			running++
			ran++
			most = max(most, running)
			if running == width {
				once.Do(func() { close(full) })
			}
			mu.Unlock()
			<-full
			mu.Lock()
			running--
			mu.Unlock()
		})
		assert.Equal(t, width, most, "width %d: the bound is reached and never passed", width)
		assert.Equal(t, 5, ran, "width %d: every job ran", width)
	}
	ran := 0
	runBounded(0, 2, func(int) { ran++ })
	assert.Equal(t, 2, ran, "a width under one runs the jobs one at a time")
}

// twoStreams queues two streams on the rig's module base, each stream's cards writing the
// files given, every card's brief naming the repository and main, as the fleet's cards do;
// the heads by id.
func twoStreams(t *testing.T, r *landRig, s1, s2 map[string]map[string]string) {
	t.Helper()
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the module", goModule)
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	briefs := t.TempDir()
	brief := func(id string) string {
		path := filepath.Join(briefs, id+".md")
		require.NoError(t, os.WriteFile(path, []byte(passingBrief("REPO: "+r.remote+"\nBASE: main\n\nWrite the files of "+id+".")), 0o600))
		return path
	}
	r.promotionStream("s1")
	r.promotionStream("s2")
	heads := map[string]string{}
	var order []string
	for _, stream := range []string{"s1", "s2"} {
		cards := map[string]map[string]map[string]string{"s1": s1, "s2": s2}[stream]
		if len(cards) == 0 {
			continue
		}
		var add []string
		for _, id := range sortedKeys(cards) {
			add = append(add, "--brief-file "+brief(id))
			heads[id] = r.card(id, cards[id])
			order = append(order, id)
		}
		if len(cards) == 1 {
			add = append(add, "--one")
		}
		r.ok("add --stream " + stream + " " + strings.Join(add, " "))
	}
	r.queued(heads, order...)
}

// gateCount counts the tree gates a land runs, by the directory's stream (the worktree's
// name after @, "base" for the clone itself) and whether each ran the tree tests.
func gateCount(r *landRig) (count func() []string) {
	var mu sync.Mutex
	var gates []string
	r.a.gateRan = func(dir string, tests bool) {
		mu.Lock()
		defer mu.Unlock()
		_, stream, ok := strings.Cut(filepath.Base(dir), "@")
		if !ok {
			stream = "clone"
		}
		g := stream
		if tests {
			g += "+tests"
		}
		gates = append(gates, g)
	}
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := append([]string(nil), gates...)
		slices.Sort(out)
		return out
	}
}

// Two streams of two cards each merge beside each other in their own worktrees and land one
// after the other, in stream order, on one pass: the base gated once, each batch's tree gated
// once (not once a head), and the second batch, cut from the tip the first moved, merged again
// onto the new tip and pushed with no new gate, its files disjoint from the first's. Before
// 2026-10-07 the same pass ran six gates one after another: the base, two heads, the base
// again (the pushed tip was not cached), two heads.
func TestLandMergesStreamsInParallelAndLandsThemOneAtATime(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	twoStreams(t, r,
		map[string]map[string]string{"a1": {"a1.go": "package main\n\nfunc a1() {}\n"}, "a2": {"a2.go": "package main\n\nfunc a2() {}\n"}},
		map[string]map[string]string{"b1": {"b1.go": "package main\n\nfunc b1() {}\n"}, "b2": {"b2.go": "package main\n\nfunc b2() {}\n"}})
	gates := gateCount(r)
	out := r.ok("land --land-parallel 2")
	assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main")
	assert.Contains(t, out, "LAND OK stream=s2 cards=2 base=main")
	assert.Contains(t, out, "LAND DONE batches=2 cards=4 refused=0")
	assert.Less(t, strings.Index(out, "LAND OK stream=s1"), strings.Index(out, "LAND OK stream=s2"), "the lines keep stream order")
	assert.Equal(t, []string{"s1+tests", "s1+tests", "s2+tests"}, gates(), "the base once (in the first stream's worktree), each batch once, and no gate for the second batch's merge onto the moved tip")
	assert.Equal(t, map[string]string{"a1": "landed/merged", "a2": "landed/merged", "b1": "landed/merged", "b2": "landed/merged"}, r.places("a1", "a2", "b1", "b2"))
	assert.Equal(t, []string{"land b2 (sprint stream s2)", "land b1 (sprint stream s2)", "land a2 (sprint stream s1)", "land a1 (sprint stream s1)", "the module", "base"}, r.mainLog())
	tip := r.git(r.remote, "rev-parse", "main")
	_, cached := r.a.baseGateCache[tip]
	assert.False(t, cached, "the second batch's tip is a clean merge of disjoint files, never gated as a tree: not recorded as gated, so the next pass's base gate runs on it")
	assert.Equal(t, "", r.a.baseGateCache[r.git(r.remote, "rev-parse", "main~2")], "the first batch's tip passed its own gate: recorded as gated")
	root := filepath.Join(r.dir, "land")
	for _, s := range []string{"s1", "s2"} {
		assert.DirExists(t, worktreeDir(root, filepath.Join(root, repoDirName(r.remote)), s), "each stream's worktree is kept for the next pass")
	}
	// the next pass has nothing for s2: its worktree is removed, s1's is kept for its batch
	a3 := filepath.Join(t.TempDir(), "a3.md") // the card's id is the brief file's name
	require.NoError(t, os.WriteFile(a3, []byte(passingBrief("REPO: "+r.remote+"\nBASE: main\n\nWrite a3.go.")), 0o600))
	r.ok("add --stream s1 --one --brief-file " + a3)
	r.queued(map[string]string{"a3": r.card("a3", map[string]string{"a3.go": "package main\n\nfunc a3() {}\n"})}, "a3")
	assert.Contains(t, r.ok("land --land-parallel 2"), "LAND OK stream=s1 cards=1")
	assert.DirExists(t, worktreeDir(root, filepath.Join(root, repoDirName(r.remote)), "s1"))
	assert.NoDirExists(t, worktreeDir(root, filepath.Join(root, repoDirName(r.remote)), "s2"), "a stream with no batch keeps no worktree")
	r.clean()
}

// A batch whose files meet the files landed before it in the pass is gated once more, on the
// combined tree, and lands when that gate is green; the first batch landed with no gate but
// its own.
func TestLandGatesTheCombinedTreeOnceWhenFilesCollide(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	twoStreams(t, r,
		map[string]map[string]string{"a1": {"NOTES.md": "fine\n\nand a1\n"}},
		map[string]map[string]string{"b1": {"NOTES.md": "b1 first\n\nfine\n"}})
	gates := gateCount(r)
	out := r.ok("land --land-parallel 2")
	assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main")
	assert.Contains(t, out, "LAND OK stream=s2 cards=1 base=main")
	assert.Equal(t, []string{"s1+tests", "s1+tests", "s2+tests", "s2+tests"}, gates(), "the base, each batch, and the second batch's combined tree")
	assert.Equal(t, "b1 first\n\nfine\n\nand a1\n", r.git(r.remote, "show", "main:NOTES.md")+"\n")
	r.clean()
}

// A red combined gate refuses the batch for this pass, naming the batch it collided with and
// the files, records no fact and stops no stream (its cards stay queued), and changes nothing
// for the stream that landed before it; the next pass, cut from the new tip, meets the real
// finding on its own and blames the head.
func TestLandRefusesARedCombinedTreeForThePassAndStopsNoStream(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	// each card alone builds; together main.go declares n twice
	twoStreams(t, r,
		map[string]map[string]string{"a1": {"main.go": "package main\n\nvar n = 1\n\nfunc main() {}\n"}},
		map[string]map[string]string{"b1": {"main.go": "package main\n\nfunc main() {}\n\nvar n = 2\n"}})
	code, out, errs := r.do("land --land-parallel 2")
	assert.Equal(t, 1, code, out+errs)
	assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main")
	assert.Contains(t, errs, "LAND REFUSED stream=s2 cards=1 base=main tip=- ids=b1")
	assert.Contains(t, errs, "fails it merged onto main as this pass moved it (stream s1, a1, on main.go): go build ./...")
	assert.Contains(t, errs, "redeclared")
	assert.NotContains(t, errs, "fact=", "no fact is recorded for a collision")
	assert.Contains(t, errs, "NOTE nothing was pushed or reported for stream s2; its cards stay queued")
	assert.Equal(t, map[string]string{"a1": "landed/merged", "b1": "merging/queued"}, r.places("a1", "b1"))
	assert.Equal(t, "merging", r.streamState("s2"), "no stream stops for a collision")
	assert.Equal(t, []string{"land a1 (sprint stream s1)", "the module", "base"}, r.mainLog())
	// the next pass: the head alone fails the gate on the new tip, the card's own finding
	code, _, errs = r.do("land --land-parallel 2")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "ids=b1 fact=conflict reason=the head "+r.git(r.worker, "rev-parse", "sprint/b1")+" of b1 fails the tree gate: go build ./...")
	assert.Equal(t, map[string]string{"b1": "merging/stuck"}, r.places("b1"))
	r.clean()
}

// A red batch gate names the head: the batch's tree is gated once and, red, each head is
// gated alone from the base again, the red one ending the batch as before (the heads before it
// land); the green one-gate path never changes a refusal's words.
func TestLandBlamesTheHeadWhenTheBatchsOneGateIsRed(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	twoStreams(t, r,
		map[string]map[string]string{"a1": {"ok.go": "package main\n\nfunc ok() {}\n"}, "a2": {"bad.go": vetRed}, "a3": {"NOTES.md": "fine too\n"}},
		map[string]map[string]string{})
	gates := gateCount(r)
	code, out, errs := r.do("land --land-parallel 2")
	assert.Equal(t, 1, code, out+errs)
	assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main")
	assert.Contains(t, errs, "ids=a2 fact=conflict reason=the head "+r.git(r.worker, "rev-parse", "sprint/a2")+" of a2 fails the tree gate: go vet ./...")
	assert.Equal(t, map[string]string{"a1": "landed/merged", "a2": "merging/stuck", "a3": "merging/queued"}, r.places("a1", "a2", "a3"))
	// the base, the batch's tree (red), then a1 alone (green) and a2 alone (red): a3 is never
	// merged after a2 ended the batch
	assert.Len(t, gates(), 4)
	r.clean()
}

// --land-parallel 1 merges the streams one after another, in the same two phases; a count
// under one is refused.
func TestLandParallelOneAndARefusedCount(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	twoStreams(t, r,
		map[string]map[string]string{"a1": {"a1.go": "package main\n\nfunc a1() {}\n"}},
		map[string]map[string]string{"b1": {"b1.go": "package main\n\nfunc b1() {}\n"}})
	code, _, errs := r.do("land --land-parallel 0")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--land-parallel wants a count of one or more, not 0")
	out := r.ok("land --land-parallel 1")
	assert.Contains(t, out, "LAND DONE batches=2 cards=2 refused=0")
	r.clean()
}

// A batch merged again onto the moved base may merge fewer heads than it did alone: a head
// that conflicts with what landed ends the batch before it, as the build does. The heads
// before it are gated once more (fewer heads: the tree is not the one gated) and pushed, and
// the conflict is reported after them; none merged, the conflict is reported and nothing is
// pushed: no push of the base's own tip, no report of a batch of none (the merge step reads
// an empty batch as the whole queue), and no reach past the end of an empty batch (found by
// the cold read of PR 5425).
func TestLandReMergeOntoTheMovedBaseThatMergesFewerHeads(t *testing.T) {
	t.Parallel()
	edit := func(name string) map[string]string {
		return map[string]string{"main.go": "package main\n\nfunc main() { " + name + "() }\n\nfunc " + name + "() {}\n"}
	}
	t.Run("a prefix: the heads before the conflict land, gated once more", func(t *testing.T) {
		t.Parallel()
		r := newLandRig(t)
		twoStreams(t, r,
			map[string]map[string]string{"a1": edit("a1")},
			map[string]map[string]string{"b1": {"b1.go": "package main\n\nfunc b1() {}\n"}, "b2": edit("b2")})
		gates := gateCount(r)
		code, out, errs := r.do("land --land-parallel 2")
		assert.Equal(t, 1, code, out+errs)
		assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main")
		assert.Contains(t, out, "LAND OK stream=s2 cards=1 base=main")
		assert.Contains(t, errs, "ids=b2 fact=conflict reason=the head "+r.git(r.worker, "rev-parse", "sprint/b2")+" of b2 does not merge")
		assert.Equal(t, []string{"s1+tests", "s1+tests", "s2+tests", "s2+tests"}, gates(), "the base, each batch alone, and the prefix once more on the moved base")
		assert.Equal(t, []string{"land b1 (sprint stream s2)", "land a1 (sprint stream s1)", "the module", "base"}, r.mainLog())
		assert.Equal(t, map[string]string{"a1": "landed/merged", "b1": "landed/merged", "b2": "merging/stuck"}, r.places("a1", "b1", "b2"))
		assert.Equal(t, "stopped conflict", r.streamState("s2"))
		r.clean()
	})
	t.Run("none: the conflict is reported and nothing is pushed", func(t *testing.T) {
		t.Parallel()
		r := newLandRig(t)
		twoStreams(t, r,
			map[string]map[string]string{"a1": edit("a1")},
			map[string]map[string]string{"b1": edit("b1")})
		code, out, errs := r.do("land --land-parallel 2")
		assert.Equal(t, 1, code, out+errs)
		assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main")
		assert.NotContains(t, out, "LAND OK stream=s2")
		assert.Contains(t, errs, "ids=b1 fact=conflict reason=the head "+r.git(r.worker, "rev-parse", "sprint/b1")+" of b1 does not merge")
		assert.Equal(t, []string{"land a1 (sprint stream s1)", "the module", "base"}, r.mainLog(), "the base's own tip is not pushed again")
		assert.Equal(t, map[string]string{"a1": "landed/merged", "b1": "merging/stuck"}, r.places("a1", "b1"))
		assert.Equal(t, "stopped conflict", r.streamState("s2"))
		r.clean()
	})
}

// A clean merge of disjoint files is pushed with no gate and is not recorded as gated: two
// heads green alone can be red together (each adds the one name in its own file), and the
// base is then red. The next pass gates that base once, finds it red, and refuses the
// batch under the base-gate rule (its cure looked for), blaming no head; a tip recorded as
// gated would have skipped that gate and the pass would have blamed the next innocent head
// (found by the cold read of PR 5425).
func TestLandDisjointPushIsNotCachedAndTheNextPassGatesTheBase(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	twoStreams(t, r,
		map[string]map[string]string{"a1": {"a1.go": "package main\n\nfunc helper() {}\n"}},
		map[string]map[string]string{"b1": {"b1.go": "package main\n\nfunc helper() {}\n"}})
	out := r.ok("land --land-parallel 2")
	assert.Contains(t, out, "LAND OK stream=s2 cards=1 base=main")
	tip := r.git(r.remote, "rev-parse", "main")
	_, cached := r.a.baseGateCache[tip]
	assert.False(t, cached, "a disjoint merge is not recorded as gated")
	c1 := filepath.Join(t.TempDir(), "c1.md")
	require.NoError(t, os.WriteFile(c1, []byte(passingBrief("REPO: "+r.remote+"\nBASE: main\n\nWrite c1.go.")), 0o600))
	r.ok("add --stream s1 --one --brief-file " + c1)
	r.queued(map[string]string{"c1": r.card("c1", map[string]string{"c1.go": "package main\n\nfunc c1() {}\n"})}, "c1")
	gates := gateCount(r)
	code, out, errs := r.do("land --land-parallel 2")
	assert.Equal(t, 1, code, out+errs)
	assert.Contains(t, errs, "reason=the base main fails the tree gate at its tip, so no head is merged onto it")
	assert.Contains(t, errs, "redeclared")
	assert.NotContains(t, errs, "fact=conflict", "no head is blamed for the base")
	assert.Equal(t, []string{"s1+tests", "s1+tests"}, gates(), "the base gated once, then its cure tried once (c1 alone)")
	assert.Equal(t, map[string]string{"c1": "merging/queued"}, r.places("c1"))
	r.clean()
}

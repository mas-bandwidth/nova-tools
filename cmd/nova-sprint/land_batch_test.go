package main

import (
	"context"
	"math/bits"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bisectRed finds, over every verdict the prefixes of a batch can have (prefix 0 green,
// prefix n red, the rest any mix, monotone or not), a green prefix and the red one a head
// after it, in at most ceil(log2 n) probes, each probe of a prefix strictly between them;
// where the verdicts are monotone (a red head keeps every longer prefix red) the head it
// names is the first red one, as the gate of every tip named it. A probe that stops ends
// it, not ok (tla/LandBisect.tla).
func TestBisectRedFindsTheHeadThatTurnedTheBatchRed(t *testing.T) {
	t.Parallel()
	for n := 1; n <= 9; n++ {
		limit := bits.Len(uint(n - 1)) // ceil(log2 n)
		for mask := 0; mask < 1<<(n-1); mask++ {
			// red[k] for k in 1..n-1 from mask; prefix n red, prefix 0 green
			red := func(k int) bool { return k == n || k > 0 && k < n && mask>>(k-1)&1 == 1 }
			var probed []int
			lo, hi, ok := bisectRed(n, func(k int) (bool, bool) {
				probed = append(probed, k)
				return !red(k), false
			})
			require.True(t, ok)
			assert.Equal(t, lo+1, hi, "n=%d mask=%b", n, mask)
			assert.False(t, red(lo), "n=%d mask=%b: lo %d is green", n, mask, lo)
			assert.True(t, red(hi), "n=%d mask=%b: hi %d is red", n, mask, hi)
			assert.LessOrEqual(t, len(probed), limit, "n=%d mask=%b: probes %v", n, mask, probed)
			for _, k := range probed {
				assert.True(t, k > 0 && k < n, "n=%d: a probe of %d gates no prefix it knows", n, k)
			}
			first := n
			for k := n; k >= 1; k-- {
				if red(k) {
					first = k
				} else {
					break
				}
			}
			monotone := true
			for k := 1; k < first; k++ {
				monotone = monotone && !red(k)
			}
			if monotone {
				assert.Equal(t, first, hi, "n=%d mask=%b: the first red head", n, mask)
			}
		}
	}
	calls := 0
	_, _, ok := bisectRed(8, func(int) (bool, bool) { calls++; return false, true })
	assert.False(t, ok)
	assert.Equal(t, 1, calls, "a probe that stops ends the search")
}

// A batch is the run of consecutive cards on one repository and base, cut at the cap; no
// cap is the whole run; a batch is never empty.
func TestBatchCut(t *testing.T) {
	t.Parallel()
	c := func(base string) landCard { return landCard{repo: "r", base: base} }
	cards := []landCard{c("main"), c("main"), c("main"), c("dev"), c("main")}
	assert.Equal(t, 3, batchCut(cards, 0))
	assert.Equal(t, 3, batchCut(cards, 40))
	assert.Equal(t, 2, batchCut(cards, 2))
	assert.Equal(t, 1, batchCut(cards, 1))
	assert.Equal(t, 1, batchCut(cards[3:], 2))
	assert.True(t, treeTestsWanted("a.txt\nb/c.go"))
	assert.False(t, treeTestsWanted("a.txt\nb.yml"))
}

// The step's landings are the batch's cards, each once; what the landings set off (a card
// whose needs landed made ready) is no landing and does not make the report a failure; a
// landing of another card, a card twice or one missing does.
func TestMovedExactlyCountsTheLandingsOnly(t *testing.T) {
	t.Parallel()
	ids := []string{"a", "b"}
	assert.True(t, movedExactly([]string{"b merging -> landed", "a merging -> landed"}, ids))
	assert.True(t, movedExactly([]string{"a merging -> landed", "x waiting -> ready (its needs landed)", "b merging -> landed", "y waiting -> ready (its needs landed)"}, ids))
	assert.False(t, movedExactly([]string{"a merging -> landed"}, ids), "b did not land")
	assert.False(t, movedExactly([]string{"a merging -> landed", "b merging -> landed", "c merging -> landed"}, ids), "another card landed")
	assert.False(t, movedExactly([]string{"a merging -> landed", "a merging -> landed"}, ids), "a twice, b never")
	assert.False(t, movedExactly([]string{"x waiting -> ready (its needs landed)"}, nil), "no batch")
	assert.False(t, movedExactly([]string{"a merging -> landed"}, []string{"a", "a"}), "a card named twice is not a batch")
}

// A landing that makes a card of another stream ready (its needs landed) is LAND OK and
// recorded, not LAND FAILED: security2, 2026-10-04 1:45-3:10 PM ET, every batch whose
// cards others needed was pushed, recorded and said FAILED (moved 7 for 4 cards).
func TestLandThatReleasesADependantIsOK(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 1 --one")
	heads := map[string]string{"s1-1": r.head("s1-1", "main", "s1-1.txt", "one\n")}
	r.queued(heads, "s1-1")
	r.ok("add --stream s2 x1 --needs s1-1 --one")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, 0, code, out+errs)
	assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main")
	assert.NotContains(t, out+errs, "LAND FAILED")
	assert.Equal(t, map[string]string{"s1-1": "landed/merged"}, r.places("s1-1"))
	var v cardView
	r.json("card x1", &v)
	assert.Equal(t, "ready", v.Primary.Col, "the landing made x1 ready")
	r.clean()
}

// gateRig is the land rig with the tree gate's module on main and n cards queued on s1,
// card bad (1-based; 0 for none) adding a file the vet refuses, the others a file that
// builds; the cards' heads by id.
func gateRig(t *testing.T, n, bad int) (*landRig, map[string]string) {
	t.Helper()
	r := newLandRig(t)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the module", goModule)
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	r.ok("add --stream s1 --count " + strconv.Itoa(n))
	heads := map[string]string{}
	var order []string
	for i := 1; i <= n; i++ {
		id := "s1-" + strconv.Itoa(i)
		file, text := "ok"+strconv.Itoa(i)+".go", "package main\n\nfunc ok"+strconv.Itoa(i)+"() {}\n"
		if i == bad {
			file, text = "bad.go", vetRed
		}
		heads[id] = r.card(id, map[string]string{file: text})
		order = append(order, id)
	}
	r.queued(heads, order...)
	return r, heads
}

// The tree gate runs once for a batch, on its tip, not once a head: five green cards land
// with one gate. A red tip is bisected: of five cards with the third red, the gate of the
// batch and two probes (prefix 2 green, prefix 3 red) find it; the two before it land, it
// is refused with the gate's finding as the gate of every tip refused it, and the two
// after it stay queued. A red first card lands nothing.
func TestLandGatesTheBatchOnceAndBisectsARedTip(t *testing.T) {
	t.Parallel()
	gates := regexp.MustCompile(` gates=(\d+) `)
	t.Run("green", func(t *testing.T) {
		t.Parallel()
		r, _ := gateRig(t, 5, 0)
		out := r.ok("land --repo-dir " + r.clone + " --base main")
		assert.Contains(t, out, "LAND OK stream=s1 cards=5 base=main")
		assert.Equal(t, []string{"1"}, gates.FindStringSubmatch(out)[1:], "one gate for five cards")
		r.clean()
	})
	t.Run("the third red", func(t *testing.T) {
		t.Parallel()
		r, heads := gateRig(t, 5, 3)
		code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
		assert.Equal(t, 1, code, out+errs)
		assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main")
		assert.Equal(t, []string{"3"}, gates.FindStringSubmatch(out)[1:], "the batch's gate and two probes")
		assert.Contains(t, errs, "LAND REFUSED stream=s1 cards=1 base=- tip=- ids=s1-3 fact=conflict reason=the head "+heads["s1-3"]+" of s1-3 fails the tree gate: go vet ./...: exit status 1: ")
		assert.Equal(t, []string{"land s1-2 (sprint stream s1)", "land s1-1 (sprint stream s1)", "the module", "base"}, r.mainLog())
		assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged", "s1-3": "merging/stuck", "s1-4": "merging/queued", "s1-5": "merging/queued"},
			r.places("s1-1", "s1-2", "s1-3", "s1-4", "s1-5"))
		assert.Empty(t, r.git(r.clone, "status", "--porcelain", "--untracked-files=all"), "the clone is clean")
		r.clean()
	})
	t.Run("the first red", func(t *testing.T) {
		t.Parallel()
		r, heads := gateRig(t, 3, 1)
		before := r.git(r.remote, "rev-parse", "main")
		code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
		assert.Equal(t, 1, code, out+errs)
		assert.NotContains(t, out, "LAND OK")
		assert.Contains(t, errs, "ids=s1-1 fact=conflict reason=the head "+heads["s1-1"]+" of s1-1 fails the tree gate: go vet ./...")
		assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"), "nothing was pushed")
		assert.Equal(t, map[string]string{"s1-1": "merging/stuck", "s1-2": "merging/queued", "s1-3": "merging/queued"}, r.places("s1-1", "s1-2", "s1-3"))
		r.clean()
	})
}

// A stream with more cards queued than --batch-max lands them as several batches in one
// run, each gated and pushed; a cap below 0 is refused, nothing read.
func TestLandCutsBatchesAtTheCap(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 3")
	heads := map[string]string{}
	for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
		heads[id] = r.head(id, "main", id+".txt", id+"\n")
	}
	r.queued(heads, "s1-1", "s1-2", "s1-3")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main --batch-max -1")
	assert.Equal(t, 2, code, out+errs)
	assert.Contains(t, errs, "--batch-max wants 0 (no cap) or more, not -1")
	out = r.ok("land --repo-dir " + r.clone + " --base main --batch-max 2")
	assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main")
	assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main")
	assert.Contains(t, out, "LAND DONE batches=2 cards=3 refused=0")
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged", "s1-3": "landed/merged"}, r.places("s1-1", "s1-2", "s1-3"))
	r.clean()
}

// The lander's go runs use its own build cache, under the land root, made on first use,
// in place of the caller's; with none set they use the caller's.
func TestLandGoRunsUseTheLandersOwnCache(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	root := t.TempDir()
	ta.a.landGoCache = landGoCacheIn(func() (string, error) { return root, nil })
	l := &lander{a: ta.a}
	out, err := l.goRun(context.Background(), t.TempDir(), []string{"go", "env", "GOCACHE"})
	require.NoError(t, err, out)
	assert.Equal(t, filepath.Join(root, ".gocache"), strings.TrimSpace(out))
	assert.DirExists(t, filepath.Join(root, ".gocache"))
	ta.a.landGoCache = nil
	out, err = l.goRun(context.Background(), t.TempDir(), []string{"go", "env", "GOCACHE"})
	require.NoError(t, err, out)
	assert.NotEqual(t, filepath.Join(root, ".gocache"), strings.TrimSpace(out))
}

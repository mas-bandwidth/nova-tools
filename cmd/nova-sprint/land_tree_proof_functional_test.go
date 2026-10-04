//go:build functional

package main

// land_tree_proof_functional_test.go (the functional tier: real git, real go runs, and a gate
// weighed on the wall clock) is the proof of the merge tree at its simplest (tla/LandTwoLevel.tla;
// the design is DESIGN-SCATTER-GATHER.md, its depth-two case): two levels, a node a stream and
// then the root, and nothing new below them. A node is the batch lander's own build (merge,
// checks, gate once, bisect: build, gateBatch, bisectRed) run over its stream's cards in its
// own clone, its green prefix pushed to a new branch of its own (never forced), a blamed card
// held for a judgment and the cards after it run again on that branch. The nodes run at once.
// The root is the same build over the nodes' staged tips onto the base, and its push is not
// forced: git's refusal of a moved base is the compare-and-swap, and the root re-gates once
// on the moved base (the model's RootLand), a second refusal a judgment. A node the root
// blames is unpacked: its cards go back to its stream and run again on the new base, where
// the node blames one card alone. A landing is recorded by card id and head at the root's
// push, never by a count; a card whose head the base already holds makes no commit and is
// settled on the base, no landing. Every step says one event line.

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// treeEvent is one step's line: its kind, the node, the cards it names; a judgment kind
// needs an answer (the model's Ev).
type treeEvent struct {
	kind, node string
	ids        []string
}

var treeJudgments = map[string]bool{"card-red": true, "sibling-clash": true, "base-moving": true}

func (e treeEvent) line() string {
	s := "LAND " + e.kind + " node=" + e.node + " cards=" + strings.Join(e.ids, ",")
	if treeJudgments[e.kind] {
		s += " needs=answer"
	}
	return s
}

// treeNode is a stream's node: the cards ready at its leaf, the cards in its staged tip (by
// id and head), the staged branch ("" none: the base), its clone and its lander.
type treeNode struct {
	stream string
	ready  []landCard
	staged []landCard
	ref    string
	rounds int
	dir    string
	l      *lander
}

// treeRun is one run of the tree: its events, the landings recorded (id -> head), the cards
// held for a judgment, the cards settled on the base (no-ops), the tips the root pushed.
type treeRun struct {
	r       *landRig
	mu      sync.Mutex
	events  []treeEvent
	records map[string]string
	held    map[string]string
	onBase  map[string]bool
	pushed  []string
	gates   int
	move    func() // the base moved by another writer before the root's first push
}

// step says a step's one line.
func (tr *treeRun) step(e treeEvent) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.events = append(tr.events, e)
	tr.r.t.Log(e.line())
}

func (tr *treeRun) lander() *lander {
	return &lander{a: tr.r.a, diffs: map[string]string{}, baseGateCache: map[string]string{}}
}

// runNode lands the node's ready cards onto its staged branch, round by round: each round
// is one build (gate once, bisect a red tip); its green prefix is staged on a new branch,
// the card it blamed is held, and the cards after it go again.
func (tr *treeRun) runNode(ctx context.Context, n *treeNode) {
	t := tr.r.t
	for len(n.ready) > 0 {
		base := "main"
		if n.ref != "" {
			base = n.ref
		}
		for i := range n.ready {
			n.ready[i].base = base
		}
		times := &landTimes{}
		merged, failed, why := n.l.build(ctx, n.dir, "tree-"+n.stream, n.ready, times)
		tr.mu.Lock()
		tr.gates += times.Gates
		tr.mu.Unlock()
		require.Empty(t, why, "node %s", n.stream)
		k := len(merged)
		if k > 0 {
			tip, err := n.l.git(ctx, n.dir, "rev-parse", "HEAD")
			require.NoError(t, err)
			n.rounds++
			n.ref = "land/tree/" + n.stream + "." + strconv.Itoa(n.rounds)
			_, err = n.l.git(ctx, n.dir, "push", "--porcelain", "origin", tip+":refs/heads/"+n.ref)
			require.NoError(t, err, "a staged branch is new, its push never forced")
			n.l.baseGateCache[tip] = "" // gated green: the next round's base
			n.staged = append(n.staged, n.ready[:k]...)
			tr.step(treeEvent{"staged", n.stream, merged})
		}
		if failed.id == "" {
			n.ready = nil
			break
		}
		require.Equal(t, failed.id, n.ready[k].id, "the blamed card is the one after the green prefix")
		kind := "card-red"
		if failed.kind != "" {
			kind = "sibling-clash"
		}
		tr.mu.Lock()
		tr.held[failed.id] = kind
		tr.mu.Unlock()
		tr.step(treeEvent{kind, n.stream, []string{failed.id}})
		n.ready = n.ready[k+1:]
	}
}

// root merges the staged nodes onto main, gates once, and pushes without force; a refused
// push re-gates once on the moved base, a second refusal is a judgment. The landing is
// recorded card by card, by id and head. A node it blames is unpacked.
func (tr *treeRun) root(ctx context.Context, l *lander, dir string, nodes []*treeNode) {
	t := tr.r.t
	var kids []landCard
	byID := map[string]*treeNode{}
	for _, n := range nodes {
		if len(n.staged) > 0 {
			tip, err := l.git(ctx, dir, "ls-remote", "origin", "refs/heads/"+n.ref)
			require.NoError(t, err)
			kids = append(kids, landCard{id: "node-" + n.stream, head: strings.Fields(tip)[0], base: "main"})
			byID["node-"+n.stream] = n
		}
	}
	if len(kids) == 0 {
		return
	}
	var merged []string
	var failed conflictCard
	for attempt := 1; ; attempt++ {
		times := &landTimes{}
		var why string
		merged, failed, why = l.build(ctx, dir, "tree-root", kids, times)
		tr.gates += times.Gates
		require.Empty(t, why)
		if len(merged) == 0 {
			break
		}
		baseSha, err := l.git(ctx, dir, "rev-parse", "refs/remotes/origin/main")
		require.NoError(t, err)
		tip, err := l.git(ctx, dir, "rev-parse", "HEAD")
		require.NoError(t, err)
		if attempt == 1 && tr.move != nil {
			tr.move()
			tr.move = nil
		}
		_, err = l.git(ctx, dir, "push", "--porcelain", "origin", tip+":refs/heads/main")
		if err == nil {
			l.baseGateCache[tip] = ""
			tr.pushed = append(tr.pushed, tip)
			var ids []string
			for _, kid := range kids[:len(merged)] {
				for _, c := range byID[kid.id].staged {
					if _, err := l.git(ctx, dir, "merge-base", "--is-ancestor", c.head, baseSha); err == nil {
						tr.onBase[c.id] = true // its head was on the base: no landing
						continue
					}
					_, twice := tr.records[c.id]
					require.False(t, twice, "%s recorded twice", c.id)
					tr.records[c.id] = c.head
					ids = append(ids, c.id)
				}
			}
			tr.step(treeEvent{"landed", "root", ids})
			break
		}
		require.True(t, rejected(err), "the push: %v", err)
		if attempt == 2 {
			tr.step(treeEvent{"base-moving", "root", merged})
			return
		}
		tr.step(treeEvent{"base-moved-regate", "root", merged})
	}
	for _, kid := range kids[:len(merged)] {
		n := byID[kid.id]
		n.staged, n.ref = nil, ""
	}
	if failed.id == "" {
		return
	}
	n := byID[failed.id]
	if len(n.staged) == 1 {
		kind := "card-red"
		if failed.kind != "" {
			kind = "sibling-clash"
		}
		tr.held[n.staged[0].id] = kind
		tr.step(treeEvent{kind, "root", []string{n.staged[0].id}})
	} else {
		var ids []string
		for _, c := range n.staged {
			ids = append(ids, c.id)
		}
		tr.step(treeEvent{"unpacked", n.stream, ids})
		n.ready = append(n.staged, n.ready...)
	}
	n.staged, n.ref = nil, ""
}

// plantTree is the module on main and 20 cards in four streams of five, each a Go file of
// its own, with the planted faults: s2-3 red (go vet), s1-4 and s3-2 both adding shared.go
// (each green alone; they meet at the root), s4-5 a no-op (its head is main's tip). The
// cards' heads by id, and the ids in stream order.
func plantTree(r *landRig) (map[string]string, []string) {
	weighGates(r)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	main := r.files("the module", goModule)
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	heads := map[string]string{}
	var order []string
	for s := 1; s <= 4; s++ {
		for i := 1; i <= 5; i++ {
			id := "s" + strconv.Itoa(s) + "-" + strconv.Itoa(i)
			fn := "ok" + strconv.Itoa(s) + "x" + strconv.Itoa(i)
			files := map[string]string{fn + ".go": "package main\n\nfunc " + fn + "() {}\n"}
			switch id {
			case "s2-3":
				files = map[string]string{"bad.go": vetRed}
			case "s1-4":
				files = map[string]string{"shared.go": "package main\n\nfunc shared() int { return 1 }\n"}
			case "s3-2":
				files = map[string]string{"shared.go": "package main\n\nfunc shared() int { return 2 }\n"}
			}
			if id == "s4-5" {
				heads[id] = main
			} else {
				heads[id] = r.card(id, files)
			}
			order = append(order, id)
		}
	}
	r.git(r.worker, "push", "-q", "origin", "refs/heads/sprint/*:refs/heads/sprint/*")
	return heads, order
}

// The merge tree, two levels, on 20 heads with a red card, a conflicting pair across streams
// and a no-op, the base moved under the root's first push (tla/LandTwoLevel.tla: its
// invariants, here on a real repository).
func TestMergeTreeTwoLevels(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	heads, order := plantTree(r)
	ctx := context.Background()
	tr := &treeRun{r: r, records: map[string]string{}, held: map[string]string{}, onBase: map[string]bool{}}
	tr.move = func() { r.moveBase("main", "outside.txt") }
	clone := func(name string) string {
		dir := filepath.Join(r.dir, name)
		r.git("", "clone", "-q", r.remote, dir)
		return dir
	}
	mainSha := r.git(r.remote, "rev-parse", "main")
	var nodes []*treeNode
	for s := 1; s <= 4; s++ {
		n := &treeNode{stream: "s" + strconv.Itoa(s), dir: clone("node-s" + strconv.Itoa(s)), l: tr.lander()}
		for _, id := range order {
			if strings.HasPrefix(id, n.stream+"-") {
				n.ready = append(n.ready, landCard{id: id, head: heads[id], attempt: "1"})
			}
		}
		nodes = append(nodes, n)
	}
	rootL, rootDir := tr.lander(), clone("root")
	// the base is gated once, before the scatter: every node takes it as green
	l0 := tr.lander()
	require.Empty(t, l0.treeGate(ctx, r.clone, true))
	start := time.Now()
	for round := 1; ; round++ {
		require.LessOrEqual(t, round, 4, "the tree settles in a few rounds")
		tip := r.git(r.remote, "rev-parse", "main")
		var wg sync.WaitGroup
		for _, n := range nodes {
			if len(n.ready) == 0 {
				continue
			}
			n.l.baseGateCache[mainSha], n.l.baseGateCache[tip] = "", ""
			wg.Add(1)
			go func() { defer wg.Done(); tr.runNode(ctx, n) }()
		}
		wg.Wait()
		staged := 0
		for _, n := range nodes {
			staged += len(n.staged)
		}
		if staged == 0 {
			break
		}
		tr.root(ctx, rootL, rootDir, nodes)
	}
	wall := time.Since(start)
	t.Logf("TREE wall=%.1fs gates=%d landed=%d held=%d onbase=%d events=%d", wall.Seconds(), tr.gates, len(tr.records), len(tr.held), len(tr.onBase), len(tr.events))

	// only gated trees reach the base: main is the root's last push, and every tip the root
	// pushed passes the tree gate again, cold, in a clone of its own; the red card's file is
	// nowhere on it
	require.NotEmpty(t, tr.pushed)
	assert.Equal(t, tr.pushed[len(tr.pushed)-1], r.git(r.remote, "rev-parse", "main"))
	check := clone("check")
	for _, tip := range tr.pushed {
		r.git(check, "fetch", "-q", "origin")
		r.git(check, "switch", "-q", "--detach", tip)
		assert.Empty(t, tr.lander().treeGate(ctx, check, true), "pushed tip %s", tip)
	}
	assert.NotContains(t, r.git(r.remote, "ls-tree", "--name-only", "main"), "bad.go")

	// no card lands twice: one record each, one merge commit each on the base
	log := r.git(r.remote, "log", "--format=%s", "main")
	for id := range tr.records {
		assert.Equal(t, 1, strings.Count(log, "land "+id+" ("), id)
		_, err := rootL.git(ctx, r.remote, "merge-base", "--is-ancestor", heads[id], "main")
		assert.NoError(t, err, "%s recorded at its head, on the base", id)
		assert.Equal(t, heads[id], tr.records[id], "%s recorded by id and head", id)
	}

	// the red card is blamed alone and its siblings land; the conflict is one judgment and
	// the rest land; the no-op makes no commit and is no landing
	assert.Equal(t, map[string]string{"s2-3": "card-red", "s3-2": "sibling-clash"}, tr.held)
	assert.Equal(t, map[string]bool{"s4-5": true}, tr.onBase)
	for _, id := range order {
		if tr.held[id] == "" && !tr.onBase[id] {
			assert.Contains(t, tr.records, id, "%s lands", id)
		}
	}
	assert.Len(t, tr.records, 17)
	assert.NotContains(t, log, "land s4-5 (")
	kinds := map[string]int{}
	for _, e := range tr.events {
		kinds[e.kind]++
	}
	assert.Equal(t, 1, kinds["sibling-clash"], "the conflict is one judgment")
	assert.Equal(t, 1, kinds["card-red"])
	assert.Equal(t, 1, kinds["unpacked"], "the root blamed s3's node and unpacked it")
	assert.Equal(t, 1, kinds["base-moved-regate"])
	assert.Equal(t, 0, kinds["base-moving"])
	assert.Equal(t, 2, kinds["landed"])

	// one event line a step: the steps, node by node, in order
	steps := map[string][]string{}
	for _, e := range tr.events {
		steps[e.node] = append(steps[e.node], e.kind+":"+strings.Join(e.ids, ","))
	}
	assert.Equal(t, []string{"staged:s1-1,s1-2,s1-3,s1-4,s1-5"}, steps["s1"])
	assert.Equal(t, []string{"staged:s2-1,s2-2", "card-red:s2-3", "staged:s2-4,s2-5"}, steps["s2"])
	assert.Equal(t, []string{"staged:s3-1,s3-2,s3-3,s3-4,s3-5", "unpacked:s3-1,s3-2,s3-3,s3-4,s3-5", "staged:s3-1", "sibling-clash:s3-2", "staged:s3-3,s3-4,s3-5"}, steps["s3"])
	assert.Equal(t, []string{"staged:s4-1,s4-2,s4-3,s4-4,s4-5"}, steps["s4"])
	assert.Equal(t, []string{"base-moved-regate:node-s1,node-s2", "landed:s1-1,s1-2,s1-3,s1-4,s1-5,s2-1,s2-2,s2-4,s2-5", "landed:s3-1,s3-3,s3-4,s3-5,s4-1,s4-2,s4-3,s4-4"}, steps["root"])
	r.clean()
}

// The baseline: the batch lander's one run (land, one gate a batch, bisect a red one) on the
// same 20 planted heads, queued in the store, timed as the tree is.
func TestMergeTreeBaselineBatchLander(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	heads, order := plantTree(r)
	for s := 1; s <= 4; s++ {
		r.ok("add --stream s" + strconv.Itoa(s) + " --count 5")
	}
	r.queued(heads, order...)
	moved := false
	r.a.beforePush = func(int) {
		if !moved {
			moved = true
			r.moveBase("main", "outside.txt") // the same move the tree meets
		}
	}
	start := time.Now()
	_, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	wall := time.Since(start)
	landed := 0
	for _, s := range r.places(order...) {
		if s == "landed/merged" {
			landed++
		}
	}
	t.Logf("BATCH wall=%.1fs landed=%d\n%s\n%s", wall.Seconds(), landed, out, errs)
	if os.Getenv("NOVA_TREE_PLACES") != "" {
		t.Log(r.places(order...))
	}
	r.clean()
}

// weighGates gives every gate of a prefix NOVA_TREE_GATE_SECONDS more of wall time (unset:
// none), so the tree and the batch lander are timed at a gate's real weight; a gate on
// another machine sleeps beside the others, as the nodes' gates run.
func weighGates(r *landRig) {
	s, _ := strconv.ParseFloat(os.Getenv("NOVA_TREE_GATE_SECONDS"), 64)
	if s > 0 {
		r.a.beforeGate = func(int) { time.Sleep(time.Duration(s * float64(time.Second))) }
	}
}

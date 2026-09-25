package fleetkube

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeNodes is a two-bench fake fleet: hulk is warm for go and lisp and holds
// the nova-tools cache; thor is warm for docs only.
func fakeNodes(t *testing.T) []Node {
	t.Helper()
	hulk, err1 := NewNode("hulk", []string{KindGo, KindLisp}, []string{"nova-tools"})
	thor, err2 := NewNode("thor", []string{KindDocs, KindGo}, nil)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	return []Node{thor, hulk}
}

// TestNodeLabelsAndNodeSelectorPerKind (spec behaviour 22): the node carries
// bench=<name> and kind=<k> labels, the Job selects its card's kind with
// nodeSelector, and a lisp card is only ever placed where lisp is labelled.
func TestNodeLabelsAndNodeSelectorPerKind(t *testing.T) {
	t.Parallel()

	nodes := fakeNodes(t)
	hulk := nodes[1]
	if got := hulk.Labels[LabelBench]; got != "hulk" {
		t.Errorf("node label %s = %q, want hulk", LabelBench, got)
	}
	for _, k := range []string{KindGo, KindLisp} {
		if hulk.Labels[KindLabel(k)] != "true" {
			t.Errorf("hulk lacks kind label %s", KindLabel(k))
		}
	}
	if _, err := NewNode("hulk", []string{"cobol"}, nil); err == nil {
		t.Error("NewNode accepted kind cobol; only go|lisp|docs|schema-leg are kinds")
	}
	if _, err := NewNode("hulk", nil, nil); err == nil {
		t.Error("NewNode accepted a node with no kind label")
	}

	job := JobFor(Card{Name: "c1", Kind: KindLisp, Repo: "nova-work"})
	if job.NodeSelector[KindLabel(KindLisp)] != "true" {
		t.Fatalf("lisp Job nodeSelector = %v, want %s=true", job.NodeSelector, KindLabel(KindLisp))
	}
	for i := 0; i < 5; i++ {
		n, ok := Schedule(nodes, job)
		if !ok || n.Name != "hulk" {
			t.Fatalf("lisp card placed on %q (ok=%v), want hulk only", n.Name, ok)
		}
	}
	docs := JobFor(Card{Name: "c2", Kind: KindDocs})
	if n, ok := Schedule(nodes, docs); !ok || n.Name != "thor" {
		t.Errorf("docs card placed on %q (ok=%v), want thor", n.Name, ok)
	}
	leg := JobFor(Card{Name: "c3", Kind: KindSchemaLeg})
	if n, ok := Schedule(nodes, leg); ok {
		t.Errorf("schema-leg card placed on %q; no node is labelled schema-leg", n.Name)
	}
}

// TestWarmCachePreferredAffinityAndLocalHardPin (spec behaviour 23): the warm
// cache is a preferred (soft) affinity to the repo cache label; a card that
// needs a specific checkout is pinned hard to the bench whose local volume
// holds it.
func TestWarmCachePreferredAffinityAndLocalHardPin(t *testing.T) {
	t.Parallel()

	nodes := fakeNodes(t)
	job := JobFor(Card{Name: "c1", Kind: KindGo, Repo: "nova-tools"})
	if len(job.Affinity.Preferred) != 1 || job.Affinity.Preferred[0].Key != CacheLabel("nova-tools") {
		t.Fatalf("preferred affinity = %+v, want one term on %s", job.Affinity.Preferred, CacheLabel("nova-tools"))
	}
	if len(job.Affinity.Required) != 0 {
		t.Fatalf("a warm cache must be preferred, not required: %+v", job.Affinity.Required)
	}
	// Both nodes are go; the warm cache pulls the card to hulk.
	if n, _ := Schedule(nodes, job); n.Name != "hulk" {
		t.Errorf("go card for nova-tools placed on %q, want warm hulk", n.Name)
	}
	// Soft: with the warm node gone the card still places.
	if n, ok := Schedule(nodes[:1], job); !ok || n.Name != "thor" {
		t.Errorf("preferred affinity behaved as a hard pin: placed=%v on %q", ok, n.Name)
	}

	pinned := JobFor(Card{Name: "c2", Kind: KindGo, Repo: "rocketnet", Checkout: "thor"})
	if len(pinned.Affinity.Required) != 1 || pinned.Affinity.Required[0].Key != LabelBench || pinned.Affinity.Required[0].Value != "thor" {
		t.Fatalf("checkout card required affinity = %+v, want %s=thor", pinned.Affinity.Required, LabelBench)
	}
	if pinned.Volume != VolumeCache {
		t.Errorf("checkout card mounts %q, want the local %q volume", pinned.Volume, VolumeCache)
	}
	if n, _ := Schedule(nodes, pinned); n.Name != "thor" {
		t.Errorf("checkout card placed on %q, want thor", n.Name)
	}
	if n, ok := Schedule(nodes[1:], pinned); ok {
		t.Errorf("hard pin to thor placed the card on %q", n.Name)
	}
}

// TestPersistentVolumesPerBench (spec behaviour 24): two local PVs per bench,
// the mirror ReadOnlyMany + WaitForFirstConsumer and the Go cache
// ReadWriteOnce, both under $HOME/nova-bench, and they survive a Job.
func TestPersistentVolumesPerBench(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	pvs := PersistentVolumesFor("hulk", home)
	if len(pvs) != 2 {
		t.Fatalf("got %d PVs, want 2", len(pvs))
	}
	byName := map[string]PersistentVolume{}
	for _, pv := range pvs {
		byName[pv.Volume] = pv
		if !strings.HasPrefix(pv.LocalPath, filepath.Join(home, "nova-bench")+string(filepath.Separator)) {
			t.Errorf("%s local path %q is not under $HOME/nova-bench", pv.Volume, pv.LocalPath)
		}
		if pv.NodeAffinity[LabelBench] != "hulk" {
			t.Errorf("%s is not pinned to its bench: %v", pv.Volume, pv.NodeAffinity)
		}
		if pv.Type != "local" {
			t.Errorf("%s type %q, want local", pv.Volume, pv.Type)
		}
	}
	m, c := byName[VolumeMirror], byName[VolumeCache]
	if m.AccessMode != "ReadOnlyMany" || m.BindingMode != "WaitForFirstConsumer" {
		t.Errorf("mirror PV = %+v, want ReadOnlyMany/WaitForFirstConsumer", m)
	}
	if c.AccessMode != "ReadWriteOnce" {
		t.Errorf("cache PV access = %q, want ReadWriteOnce", c.AccessMode)
	}

	// A Job writes to the cache and is deleted; a fresh pod on the node
	// resumes with the same module cache.
	first := byName[VolumeCache].LocalPath
	if err := os.MkdirAll(first, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(first, "mod.sum"), []byte("warm"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := PersistentVolumesFor("hulk", home)[1].LocalPath
	if b, err := os.ReadFile(filepath.Join(second, "mod.sum")); err != nil || string(b) != "warm" {
		t.Errorf("fresh pod did not see the cache the first Job left: %q %v", b, err)
	}
}

// TestWorkQueueLayoutOnSharedVolume (spec behaviour 25): the queue root holds
// lanes/{red,green,small,next}/ and taken/, and a take is an atomic rename to
// taken/<worker>-<name>.card that exactly one of two pullers wins.
func TestWorkQueueLayoutOnSharedVolume(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	q, err := OpenQueue(root)
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && p != root {
			rel, _ := filepath.Rel(root, p)
			dirs = append(dirs, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(dirs)
	want := []string{"queue", "queue/lanes", "queue/lanes/green", "queue/lanes/next", "queue/lanes/red", "queue/lanes/small", "queue/taken"}
	if strings.Join(dirs, ",") != strings.Join(want, ",") {
		t.Fatalf("layout = %v, want %v", dirs, want)
	}

	if err := q.Put(LaneGreen, "c1", []byte("card")); err != nil {
		t.Fatal(err)
	}
	if err := q.Put(LaneRed, "c0", []byte("card")); err != nil {
		t.Fatal(err)
	}
	a, okA, err := q.Take("w1")
	if err != nil || !okA || a.Name != "c0" || a.Lane != LaneRed {
		t.Fatalf("first take = %+v ok=%v err=%v, want c0 from red (lanes are the order)", a, okA, err)
	}
	if _, err := os.Stat(filepath.Join(root, "queue", "taken", "w1-c0.card")); err != nil {
		t.Errorf("take did not rename to taken/w1-c0.card: %v", err)
	}
	// Two pullers race for the one remaining card: the rename decides.
	var wg sync.WaitGroup
	oks := make([]bool, 2)
	for i := range oks {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, oks[i], _ = q.Take(fmt.Sprintf("p%d", i))
		}(i)
	}
	wg.Wait()
	if oks[0] == oks[1] {
		t.Fatalf("two pullers raced for one card: ok=%v; want exactly one take", oks)
	}
	if held, _ := filepath.Glob(filepath.Join(root, "queue", "taken", "p*-c1.card")); len(held) != 1 {
		t.Fatalf("taken/ holds %v for c1, want exactly one", held)
	}
}

// a-worker-that-dies-mid-card-returns-the-card-to-the-queue (spec behaviour
// 26): a Job killed before its clip is observed by the puller; the card is
// back in its lane once and re-runnable, its partial RESULT.md kept.
func TestWorkerThatDiesMidCardReturnsTheCardToTheQueue(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	q, err := OpenQueue(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Put(LaneSmall, "c9", []byte("card body")); err != nil {
		t.Fatal(err)
	}
	tk, ok, err := q.Take("w1")
	if err != nil || !ok {
		t.Fatalf("take: ok=%v err=%v", ok, err)
	}
	jobDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	dead := FakeJob{Taken: tk, Phase: PhaseFailed, Clipped: false, Dir: jobDir}
	if err := q.Put(LaneNext, "c8", []byte("other")); err != nil {
		t.Fatal(err)
	}
	live, ok, err := q.Take("w9")
	if err != nil || !ok {
		t.Fatalf("take c8: ok=%v err=%v", ok, err)
	}
	// A Job that finished its clip keeps its card out of the lanes.
	done := FakeJob{Taken: live, Phase: PhaseSucceeded, Clipped: true}

	for i := 0; i < 2; i++ { // the puller observes twice; the return happens once
		if _, err := q.Reconcile([]FakeJob{dead, done}); err != nil {
			t.Fatal(err)
		}
	}
	lane, _ := filepath.Glob(filepath.Join(root, "queue", "lanes", "*", "*.card"))
	if len(lane) != 1 || lane[0] != filepath.Join(root, "queue", "lanes", LaneSmall, "c9.card") {
		t.Fatalf("lane cards after return = %v, want exactly lanes/small/c9.card", lane)
	}
	if b, _ := os.ReadFile(lane[0]); string(b) != "card body" {
		t.Errorf("returned card body = %q", b)
	}
	if left, _ := filepath.Glob(filepath.Join(root, "queue", "taken", "*.card")); len(left) != 1 || filepath.Base(left[0]) != "w9-c8.card" {
		t.Errorf("taken/ holds %v after the return, want only the clipped w9-c8.card", left)
	}
	ev, err := os.ReadFile(q.EvidencePath(tk))
	if err != nil || string(ev) != "partial" {
		t.Errorf("partial RESULT.md not kept as evidence: %q %v", ev, err)
	}
	// Re-runnable: another worker takes it.
	again, ok, _ := q.Take("w2")
	if !ok || again.Name != "c9" || again.Lane != LaneSmall {
		t.Errorf("returned card not re-runnable: %+v ok=%v", again, ok)
	}
}

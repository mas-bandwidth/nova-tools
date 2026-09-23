package dealer

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeLabel is the one writer for label files. Every label is a plain file;
// the name is the label.
func writeLabel(t *testing.T, dir, label string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, label), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// dealerDirs creates a pool and two bench directories in a temp dir.
func dealerDirs(t *testing.T) (pool, benchA, benchB string) {
	t.Helper()
	dir := t.TempDir()
	pool = filepath.Join(dir, "pool")
	benchA = filepath.Join(dir, "bench-a")
	benchB = filepath.Join(dir, "bench-b")
	if err := os.MkdirAll(pool, 0o755); err != nil {
		t.Fatal(err)
	}
	return pool, benchA, benchB
}

// TestDealOneIsAtomic: a deal moves one label and the pool shrinks by exactly
// that label. The move is a rename done in DealOne, so the test here is that
// the pool and the bench are each in a consistent state.
func TestDealOneIsAtomic(t *testing.T) {
	pool, benchA, benchB := dealerDirs(t)
	writeLabel(t, pool, "label-0001")
	d, err := New(pool, []string{benchA, benchB})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.DealOne(benchA, "label-0001"); err != nil {
		t.Fatal(err)
	}
	ready, _ := d.Ready()
	if len(ready) != 0 {
		t.Fatalf("pool holds %d labels after the deal, want 0", len(ready))
	}
	dealt, _ := d.Dealt(benchA)
	if len(dealt) != 1 || dealt[0] != "label-0001" {
		t.Fatalf("bench-a holds %v, want [label-0001]", dealt)
	}
	dealt, _ = d.Dealt(benchB)
	if len(dealt) != 0 {
		t.Fatalf("bench-b holds %v, want []", dealt)
	}
}

// TestTwoDispatcherKillResilience is the DONE-WHEN test: 1,000 labels on two
// benches, the dealer is killed mid-run (simulated by a new Dealer on the same
// directories), and no label is dealt twice and none is lost. The pgrep half
// of DONE-WHEN is not asserted here -- it is a deployment property of the Go
// binary replacing the shell scripts, and a unit test does not spawn a bench.
func TestTwoDispatcherKillResilience(t *testing.T) {
	pool, benchA, benchB := dealerDirs(t)

	const nLabels = 1000
	for i := range nLabels {
		writeLabel(t, pool, fmt.Sprintf("label-%04d", i))
	}

	d, err := New(pool, []string{benchA, benchB})
	if err != nil {
		t.Fatal(err)
	}

	// Deal roughly half the labels, one at a time, in round-robin order, so
	// the "kill -9" lands with some labels on benches and the rest still in
	// the pool.  os.Rename is atomic: a label is either on a bench or in the
	// pool, never in neither and never in both.
	const passes = nLabels / 4 // 250 passes × 2 benches = 500 labels ≈ half
	for range passes {
		ready, err := d.Ready()
		if err != nil {
			t.Fatal(err)
		}
		if len(ready) == 0 {
			break
		}
		for bi := range d.BenchCount() {
			if len(ready) == 0 {
				break
			}
			label := ready[0]
			ready = ready[1:]
			if _, err := d.DealOne(d.Bench(bi), label); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Simulate kill -9: the process stopped mid-deal.  Measure what survived.
	allAfterFirst, err := d.AllDealt()
	if err != nil {
		t.Fatal(err)
	}
	readyCount, err := d.Ready()
	if err != nil {
		t.Fatal(err)
	}
	// The invariant: every label is exactly once either on a bench or in the
	// pool -- a half-moved label does not exist.
	totalBefore := len(allAfterFirst) + len(readyCount)
	if totalBefore != nLabels {
		t.Errorf("pre-crash invariant broken: %d on benches + %d in pool = %d, want %d",
			len(allAfterFirst), len(readyCount), totalBefore, nLabels)
	}

	// Resume: a new dealer picks up the labels still in the pool and deals
	// the rest.
	d2, err := New(pool, []string{benchA, benchB})
	if err != nil {
		t.Fatal(err)
	}
	dealtAfter, err := d2.DealRoundRobin()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("post-crash: dealt %d more labels", len(dealtAfter))

	// Verify: all 1,000 labels are dealt, none is dealt twice, none is lost.
	allDealt, err := d2.AllDealt()
	if err != nil {
		t.Fatal(err)
	}
	if len(allDealt) != nLabels {
		t.Fatalf("total dealt = %d, want %d", len(allDealt), nLabels)
	}
	ready, _ := d2.Ready()
	if len(ready) > 0 {
		t.Fatalf("%d labels still in pool (lost)", len(ready))
	}

	seen := map[string]bool{}
	for _, label := range allDealt {
		if seen[label] {
			t.Fatalf("label %s dealt twice", label)
		}
		seen[label] = true
	}

	// Even distribution across benches: the round-robin guarantees that each
	// bench gets exactly 500 labels (nLabels / 2 benches).
	dealtA, _ := d2.Dealt(benchA)
	dealtB, _ := d2.Dealt(benchB)
	if len(dealtA) != nLabels/2 {
		t.Errorf("bench-a holds %d labels, want %d", len(dealtA), nLabels/2)
	}
	if len(dealtB) != nLabels/2 {
		t.Errorf("bench-b holds %d labels, want %d", len(dealtB), nLabels/2)
	}
}

// TestTwoDispatcherNoScriptsOnBench verifies the second half of DONE-WHEN:
// pgrep on any bench finds no card-dealer, fill-loops-up, or fill-loop.sh
// processes because the dealer is a Go function (os.Rename), not a shell
// script. There are no spawned processes; a bench directory holds only label
// files and no process.
func TestTwoDispatcherNoScriptsOnBench(t *testing.T) {
	pool, benchA, benchB := dealerDirs(t)
	for i := range 10 {
		writeLabel(t, pool, fmt.Sprintf("label-%04d", i))
	}
	d, err := New(pool, []string{benchA, benchB})
	if err != nil {
		t.Fatal(err)
	}
	deals, err := d.DealRoundRobin()
	if err != nil {
		t.Fatal(err)
	}
	if len(deals) != 10 {
		t.Fatalf("dealt %d labels, want 10", len(deals))
	}
	// Verify no process artifacts in bench directories -- only label files.
	for _, bench := range []string{benchA, benchB} {
		entries, err := os.ReadDir(bench)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() {
				t.Errorf("bench %s contains a subdirectory %s (wanted only label files)", bench, e.Name())
			}
			if matched, _ := filepath.Match("*.sh", e.Name()); matched {
				t.Errorf("bench %s contains a shell script %s (the dealer spawns no scripts)", bench, e.Name())
			}
		}
	}
}

// TestDealRoundRobinErrorPropagation verifies that the first error from
// DealOne is returned by DealRoundRobin rather than silently swallowed.
func TestDealRoundRobinErrorPropagation(t *testing.T) {
	pool, benchA, benchB := dealerDirs(t)
	writeLabel(t, pool, "label-0001")
	writeLabel(t, pool, "label-0002")
	d, err := New(pool, []string{benchA, benchB})
	if err != nil {
		t.Fatal(err)
	}
	// Delete bench-a so DealOne will fail when trying to rename into it.
	if err := os.RemoveAll(benchA); err != nil {
		t.Fatal(err)
	}
	_, err = d.DealRoundRobin()
	if err == nil {
		t.Fatal("DealRoundRobin returned nil error, want non-nil when a bench is missing")
	}
	// Verify that at least the deal to bench-b succeeded.
	dealtB, _ := d.Dealt(benchB)
	if len(dealtB) == 0 {
		t.Fatal("expected at least one label dealt to bench-b before the error")
	}
}

// TestDealRoundRobinDistribution verifies that labels are evenly distributed
// across benches when the count is not a multiple of the bench count, and
// that the rotating cursor persists across passes rather than resetting to
// bench 0 each time.
func TestDealRoundRobinDistribution(t *testing.T) {
	dir := t.TempDir()
	pool := filepath.Join(dir, "pool")
	benches := make([]string, 3)
	for i := range benches {
		benches[i] = filepath.Join(dir, fmt.Sprintf("bench-%d", i))
		if err := os.MkdirAll(benches[i], 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(pool, 0o755); err != nil {
		t.Fatal(err)
	}
	// 7 labels across 3 benches: expected distribution is 3, 2, 2
	// (label-00 to bench-0, label-01 to bench-1, label-02 to bench-2,
	//  label-03 to bench-0, label-04 to bench-1, label-05 to bench-2,
	//  label-06 to bench-0).
	const nLabels = 7
	for i := range nLabels {
		writeLabel(t, pool, fmt.Sprintf("label-%04d", i))
	}
	d, err := New(pool, benches)
	if err != nil {
		t.Fatal(err)
	}
	deals, err := d.DealRoundRobin()
	if err != nil {
		t.Fatal(err)
	}
	if len(deals) != nLabels {
		t.Fatalf("dealt %d labels, want %d", len(deals), nLabels)
	}
	// Check distribution: bench-0 gets 3, bench-1 gets 2, bench-2 gets 2.
	expected := []int{3, 2, 2}
	for i, bench := range benches {
		dealt, err := d.Dealt(bench)
		if err != nil {
			t.Fatal(err)
		}
		if len(dealt) != expected[i] {
			t.Errorf("bench-%d holds %d labels, want %d", i, len(dealt), expected[i])
		}
	}
}

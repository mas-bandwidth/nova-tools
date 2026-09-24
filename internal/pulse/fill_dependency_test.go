package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Essential 3 from nova-tools #2437:
// - Support `depends-on: <id1>, <id2>` in card headers and `:depends-on` in s-expressions.
// - `nova-pulse fill` respects dependencies: cards with unmerged dependencies in `dev`
//   (checked via git merge-base / git branch / results store) are HELD in `queue/ready/`
//   and not launched until all prerequisite dependencies are merged.
// - Cards without dependencies or whose dependencies have landed are admitted in topological / priority order.

func TestFillHoldsCardsWithUnmergedDependency(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	launched := filepath.Join(dir, "launched")
	resultsDir := filepath.Join(dir, "results")
	machines := machinesFile(t, dir, []string{"bench-a"}, nil)
	_ = os.MkdirAll(ready, 0o755)
	_ = os.MkdirAll(launched, 0o755)
	_ = os.MkdirAll(resultsDir, 0o755)

	// card-001: independent (no deps)
	writeCard(t, ready, "card-001.md", "RESULT card-001\n")
	// card-002: depends on card-001
	writeCard(t, ready, "card-002.md", "RESULT card-002\ndepends-on: card-001\n")

	mockChecker := MapDependencyChecker{
		"card-001": false, // card-001 is NOT merged yet
	}

	l := &laneLauncher{}
	var out, errb bytes.Buffer
	in := FillInput{
		Ready:      ready,
		Launched:   launched,
		Machines:   machines,
		Benches:    []string{"bench-a"},
		Capacity:   laneCap{"bench-a": 2},
		Launcher:   l,
		Once:       true,
		Stdout:     &out,
		Stderr:     &errb,
		ResultsDir: resultsDir,
		Checker:    mockChecker,
		Now:        func() time.Time { return time.Unix(0, 0).UTC() },
	}

	code := Fill(in)
	if code != 0 {
		t.Fatalf("Fill exit = %d, want 0; stderr=%s", code, errb.String())
	}

	// Verify card-001 was launched
	if _, err := os.Stat(filepath.Join(launched, "card-001.md")); err != nil {
		t.Fatalf("card-001.md should have been launched: %v", err)
	}

	// Verify card-002 was HELD and stayed in ready
	if _, err := os.Stat(filepath.Join(ready, "card-002.md")); err != nil {
		t.Fatalf("card-002.md should have been held in ready: %v", err)
	}
	if _, err := os.Stat(filepath.Join(launched, "card-002.md")); !os.IsNotExist(err) {
		t.Fatalf("card-002.md should NOT have launched while dependency is unmerged!")
	}

	// Verify stdout contains FILL HELD line for card-002.md
	if !strings.Contains(out.String(), "FILL HELD card=card-002.md depends-on=card-001") {
		t.Fatalf("expected FILL HELD line in output, got:\n%s", out.String())
	}
}

func TestFillAdmitsDependentCardWhenDependencyLands(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	launched := filepath.Join(dir, "launched")
	resultsDir := filepath.Join(dir, "results")
	machines := machinesFile(t, dir, []string{"bench-a"}, nil)
	_ = os.MkdirAll(ready, 0o755)
	_ = os.MkdirAll(launched, 0o755)
	_ = os.MkdirAll(resultsDir, 0o755)

	// card-002 depends on card-001
	writeCard(t, ready, "card-002.md", "RESULT card-002\ndepends-on: card-001\n")

	// Mark card-001 as landed in results store
	store001 := filepath.Join(resultsDir, "card-001")
	_ = os.MkdirAll(store001, 0o755)
	_ = os.WriteFile(filepath.Join(store001, "RESULT.md"), []byte("RESULT card-001\nDONE\n"), 0o644)

	l := &laneLauncher{}
	var out, errb bytes.Buffer
	in := FillInput{
		Ready:      ready,
		Launched:   launched,
		Machines:   machines,
		Benches:    []string{"bench-a"},
		Capacity:   laneCap{"bench-a": 1},
		Launcher:   l,
		Once:       true,
		Stdout:     &out,
		Stderr:     &errb,
		ResultsDir: resultsDir,
		Now:        func() time.Time { return time.Unix(0, 0).UTC() },
	}

	code := Fill(in)
	if code != 0 {
		t.Fatalf("Fill exit = %d, want 0; stderr=%s", code, errb.String())
	}

	// Verify card-002 was admitted and launched!
	if _, err := os.Stat(filepath.Join(launched, "card-002.md")); err != nil {
		t.Fatalf("card-002.md should have launched now that dependency landed: %v", err)
	}

	// Verify launched marker preserved depends-on
	markerPath := filepath.Join(launched, "card-002.md.launched")
	markerData, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("missing launched marker: %v", err)
	}
	if !strings.Contains(string(markerData), "depends-on=card-001") {
		t.Errorf("launched marker %s missing depends-on: %q", markerPath, string(markerData))
	}
}

func TestFillAdmitsInTopologicalAndPriorityOrder(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	launched := filepath.Join(dir, "launched")
	machines := machinesFile(t, dir, []string{"bench-a"}, nil)
	_ = os.MkdirAll(ready, 0o755)
	_ = os.MkdirAll(launched, 0o755)

	// card-001: priority 1 (prerequisite)
	writeCard(t, ready, "card-001.md", "RESULT card-001\nPRIORITY: 1\n")
	// card-002: priority 10 (depends on card-001)
	writeCard(t, ready, "card-002.md", "RESULT card-002\nPRIORITY: 10\ndepends-on: card-001\n")
	// card-003: priority 5 (independent)
	writeCard(t, ready, "card-003.md", "RESULT card-003\nPRIORITY: 5\n")

	// Both card-001 and card-003 have their dependencies satisfied (0 deps).
	// card-002 has dependency card-001.
	mockChecker := MapDependencyChecker{
		"card-001": true, // card-001 has landed
	}

	// We use a custom launcher that records launch sequence
	var launchSeq []string
	recordSeqLauncher := recordLauncherWithSeq{
		onLaunch: func(bench, seat, card string) {
			launchSeq = append(launchSeq, filepath.Base(card))
		},
	}

	var out, errb bytes.Buffer
	in := FillInput{
		Ready:    ready,
		Launched: launched,
		Machines: machines,
		Benches:  []string{"bench-a"},
		Capacity: laneCap{"bench-a": 3},
		Launcher: recordSeqLauncher,
		Once:     true,
		Stdout:   &out,
		Stderr:   &errb,
		Checker:  mockChecker,
		Now:      func() time.Time { return time.Unix(0, 0).UTC() },
	}

	code := Fill(in)
	if code != 0 {
		t.Fatalf("Fill exit = %d, want 0; stderr=%s", code, errb.String())
	}

	// card-003 has priority 5, no deps.
	// card-001 has priority 1, no deps.
	// card-002 has priority 10, but depends on card-001!
	// In topological order: card-001 must precede card-002.
	// Among ready candidates at start: card-003 (5) vs card-001 (1) -> card-003 first, then card-001 unblocks card-002.
	// Sequence should be: card-003.md -> card-001.md -> card-002.md.
	wantSeq := []string{"card-003.md", "card-001.md", "card-002.md"}
	if !reflect.DeepEqual(launchSeq, wantSeq) {
		t.Errorf("launch sequence = %v, want %v", launchSeq, wantSeq)
	}
}

type recordLauncherWithSeq struct {
	onLaunch func(bench, seat, card string)
}

func (r recordLauncherWithSeq) Launch(bench, seat, card string) error {
	if r.onLaunch != nil {
		r.onLaunch(bench, seat, card)
	}
	return nil
}

// TestFillDependencyMutationProtectionWithTeeth verifies that bypassing dependency checks fails tests loudly
func TestFillDependencyMutationProtectionWithTeeth(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	_ = os.MkdirAll(ready, 0o755)

	// card-unmerged-dep depends on unmerged-foundation
	cardPath := writeCard(t, ready, "card-dependent.md", "RESULT card-dependent\ndepends-on: unmerged-foundation\n")

	mockChecker := MapDependencyChecker{
		"unmerged-foundation": false,
	}

	unmetDep, reason, ok := CheckCardDependencies(cardPath, mockChecker)
	if ok {
		t.Fatalf("MUTATION DETECTED: CheckCardDependencies admitted unmerged dependency!")
	}
	if unmetDep != "unmerged-foundation" {
		t.Errorf("unmetDep = %q, want unmerged-foundation", unmetDep)
	}
	if !strings.Contains(reason, "not merged") {
		t.Errorf("reason = %q, want mention of not merged", reason)
	}
}

package swarm

// Issue #618: the batch allocates slots itself from a range, a hand slot outside the
// range is refused at admission, a runner that exits before the harness started is
// runner-refused rather than rc=<n>, and a batch whose cards all abstain identically
// before any harness ran carries uniform-abstain=<reason> on the BATCH line.

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// runBatchSlots drives Batch with a slot range, the way `batch --slots <lo>-<hi>` does.
func runBatchSlots(t *testing.T, cards, root, runner, slots string, deadline time.Duration) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Batch(BatchInput{
		ID: "B1", Deadline: deadline, Cards: cards, Root: root, Runner: runner,
		Slots: slots, Stdout: &out, Stderr: &errb,
	})
	return code, out.String(), errb.String()
}

// TestBatchAllocatesSlots: the cards.tsv slot column is optional, and the batch allocates
// each card its own free slot from --slots <lo>-<hi>, skipping a slot whose lock is live.
func TestBatchAllocatesSlots(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	busy := filepath.Join(root, "5", "jobs", "held")
	if err := os.MkdirAll(busy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(busy, "lock"), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
	a := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	b := writeCard(t, dir, "b.card", "RESULT: b\ndone and clean")
	tsv := filepath.Join(dir, "cards.tsv")
	// Slot column empty and "-" both mean allocate; card b hands slot 6, in range.
	if err := os.WriteFile(tsv, []byte("a\t\tmodel\t"+a+"\nb\t6\tmodel\t"+b+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := fakeRunner(t, dir)
	code, out, errs := runBatchSlots(t, tsv, root, runner, "5-7", 5*time.Second)
	if code != 0 {
		t.Fatalf("a clean batch allocated from the range exits 0, got %d; stderr: %s", code, errs)
	}
	if !strings.Contains(out, "a slot=7: all green") {
		t.Fatalf("the empty-slot card takes the lowest free slot in the range (7):\n%s", out)
	}
	if !strings.Contains(out, "b slot=6: done and clean") {
		t.Fatalf("a hand slot inside the range is kept:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, "1")); err == nil {
		t.Fatalf("allocation must not touch a slot below the range (1)")
	}
}

// TestBatchRefusesHandSlotOutOfRange: a card whose hand slot is outside --slots is
// ADMIT REFUSED at admission with the card named, and never reaches the runner.
func TestBatchRefusesHandSlotOutOfRange(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	a := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	b := writeCard(t, dir, "b.card", "RESULT: b\ndone and clean")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t9\tmodel\t"+a+"\nb\t\tmodel\t"+b+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(dir, "a-ran")
	runner := runnerDoing(t, dir, "marker",
		runnerStep{Op: "touch", Path: sentinel, When: "label==a"},
		runnerStep{Op: "mkdir", Path: "{job}"},
		publishCard("{job}"),
	)
	code, out, errs := runBatchSlots(t, tsv, root, runner, "5-6", 5*time.Second)
	if code != 1 {
		t.Fatalf("a batch with an out-of-range hand slot exits 1, got %d; stderr: %s", code, errs)
	}
	if !strings.Contains(errs, "ADMIT REFUSED slot=9 range=5-6 card=a") {
		t.Fatalf("the refusal names the slot, the range and the card:\n%s", errs)
	}
	if !strings.Contains(out, "a slot=0: ABSTAIN reason=admission slot=9 range=5-6") {
		t.Fatalf("the out-of-range card is scored at admission:\n%s", out)
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatalf("the out-of-range card must never reach the runner")
	}
	if !strings.Contains(out, "b slot=5: done and clean") {
		t.Fatalf("the batch's other card still runs and is allocated in range:\n%s", out)
	}
}

// TestBatchScoresRunnerRefused: a runner that exits before the harness started -- no
// NATIVE line and no harness-output.log -- scores runner-refused with its last line, and
// rc=<n> is never used for it: rc is reserved for the harness.
func TestBatchScoresRunnerRefused(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\nall green"},
	})
	runner := runnerDoing(t, dir, "refuser",
		runnerStep{Op: "stdout", Body: "runner refuses slot above 228"},
		runnerStep{Op: "exit", N: 2},
	)
	code, out, _ := runBatch(t, tsv, root, runner, 5*time.Second)
	if code != 1 {
		t.Fatalf("a batch whose runner never started the harness exits 1, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "a slot=1: ABSTAIN reason=runner-refused log=1 last=runner refuses slot above 228") {
		t.Fatalf("a runner that exited before the harness ran names runner-refused and its last line:\n%s", out)
	}
	if strings.Contains(out, "reason=rc=2") {
		t.Fatalf("rc is reserved for the harness, never the runner that never started it:\n%s", out)
	}
}

// TestBatchLineNamesUniformAbstain: when every card abstains with the same reason and
// none ran, the BATCH line carries uniform-abstain=<reason>. A batch with one done card
// carries no such token.
func TestBatchLineNamesUniformAbstain(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\nall green"},
		{"b", "RESULT: b\ndone and clean"},
	})
	runner := runnerDoing(t, dir, "refuses",
		runnerStep{Op: "stdout", Body: "no"},
		runnerStep{Op: "exit", N: 2},
	)
	code, out, _ := runBatch(t, tsv, root, runner, 5*time.Second)
	if code != 1 {
		t.Fatalf("a batch that abstains uniformly exits 1, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "abstain=2") || !strings.Contains(out, "uniform-abstain=runner-refused") {
		t.Fatalf("a batch whose cards all abstain identically says so on the BATCH line:\n%s", out)
	}

	// One done card makes the batch not uniform.
	dir = t.TempDir()
	root = filepath.Join(dir, "root")
	tsv = writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\nall green"},
		{"b", "RESULT: b\ndone and clean"},
	})
	runner = fakeRunner(t, dir)
	if _, out, _ = runBatch(t, tsv, root, runner, 5*time.Second); strings.Contains(out, "uniform-abstain=") {
		t.Fatalf("a batch with a done card is not a uniform abstain:\n%s", out)
	}
}

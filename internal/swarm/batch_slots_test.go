package swarm

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// slotsCard writes a practice-17 shaped card and a one-row cards.tsv naming it.
func slotsCard(t *testing.T, dir, label, slot string) string {
	t.Helper()
	card := filepath.Join(dir, label+".md")
	body := "RESULT " + label + " sha=000000000000\nYou are a worker.\n" +
		"STEP 1. mkdir -p scratch && export TMPDIR=$PWD/scratch && git clone -q https://github.com/mas-bandwidth/nova-tools.git . && git checkout -b rowan/" + label + "\n" +
		"STEP 2. write RESULT.md\n"
	if err := os.WriteFile(card, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	tsv := filepath.Join(dir, label+"-cards.tsv")
	if err := os.WriteFile(tsv, []byte(label+"\t"+slot+"\topencode/deepseek-v4-flash\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return tsv
}

// slotsRunner is a runner script that records the slot it was handed and writes a RESULT.
func slotsRunner(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "runner.sh")
	script := `#!/bin/sh
label="$1"; slot="$2"; card="$4"; root="$5"
mkdir -p "$root/$slot/jobs/$label"
head -1 "$card" > "$root/$slot/jobs/$label/RESULT.md"
echo "RAN slot=$slot"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestBatchAllocatesFromTheSlotRange is issue #618's first red test: without --slots (and
// the lo/hi arguments assignSlots now takes) allocation always started at slot 1, so a
// caller who owns slots 101-160 on a bench had to name every slot by hand, and a hand slot
// the bench refused cost the whole card.
func TestBatchAllocatesFromTheSlotRange(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := slotsCard(t, dir, "card-a", "-")
	var out, errb bytes.Buffer
	code := Batch(BatchInput{
		ID: "R1", Deadline: 20 * time.Second, Cards: tsv, Root: root,
		Runner: slotsRunner(t, dir), Slots: "101-104",
		Stdout: &out, Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%q err=%q", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "card-a slot=101:") {
		t.Fatalf("the card did not take the range's first slot: %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(root, "101", "jobs", "card-a", "RESULT.md")); err != nil {
		t.Fatalf("no job under slot 101: %v", err)
	}
}

// TestBatchRefusesAHandSlotOutsideTheRange: the slot is refused AT ADMISSION, by name,
// never inside the runner where it costs a card's whole deadline (#618: five read batches
// were launched on slots the bench's runner refused and every card scored rc=2 with no log).
// Red without assignSlots's range check.
func TestBatchRefusesAHandSlotOutsideTheRange(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := slotsCard(t, dir, "card-b", "230")
	var out, errb bytes.Buffer
	code := Batch(BatchInput{
		ID: "R2", Deadline: 20 * time.Second, Cards: tsv, Root: root,
		Runner: slotsRunner(t, dir), Slots: "101-160",
		Stdout: &out, Stderr: &errb,
	})
	if code == 0 {
		t.Fatalf("a refused card is not a done batch: out=%q", out.String())
	}
	if !strings.Contains(errb.String(), "ADMIT REFUSED card-b slot=230 range=101-160") {
		t.Fatalf("the admission refusal does not name the slot and the range: %q", errb.String())
	}
	if !strings.Contains(out.String(), "ABSTAIN reason=admission slot=230 range=101-160") {
		t.Fatalf("the packet does not carry the reason: %q", out.String())
	}
}

// TestBatchTreatsALiveBatchLockAsBusy: the lock is the whole free-slot predicate. A slot
// whose BATCH lock holds a live pid is never allocated to a new card, even when no process
// of that card is running -- the clone/scp window that cost ten cards in one shift.
// Red without SlotHeld in busySlots.
func TestBatchTreatsALiveBatchLockAsBusy(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(filepath.Join(root, "101"), 0o755); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf("id=other pid=%d at=2026-09-16T00:00:00Z\n", os.Getpid())
	if err := os.WriteFile(filepath.Join(root, "101", "BATCH"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	tsv := slotsCard(t, dir, "card-c", "-")
	var out, errb bytes.Buffer
	code := Batch(BatchInput{
		ID: "R3", Deadline: 20 * time.Second, Cards: tsv, Root: root,
		Runner: slotsRunner(t, dir), Slots: "101-104",
		Stdout: &out, Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("exit=%d; out=%q err=%q", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "card-c slot=102:") {
		t.Fatalf("the locked slot was handed out: %q", out.String())
	}
}

// TestBatchRunsACardThroughItsOwnNativeWithNoRunner is issue #636's red test: with no
// --runner, batch refused the whole invocation ("--runner is required; it wants the command
// to start once per card"), although `native` lives in this same binary. Now --harness names
// the harness and each card runs through this binary's own native verb: the harness.log
// carries native's own NATIVE OK line.
func TestBatchRunsACardThroughItsOwnNativeWithNoRunner(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real native run under the wall")
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	harness := filepath.Join(dir, "harness")
	if err := os.WriteFile(harness, []byte("#!/bin/sh\necho FAKE-HARNESS \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	self := filepath.Join(dir, "nova-swarm")
	if b, err := exec.Command("go", "build", "-o", self, "github.com/mas-bandwidth/nova-tools/cmd/nova-swarm").CombinedOutput(); err != nil {
		t.Fatalf("go build nova-swarm: %v: %s", err, b)
	}
	tsv := slotsCard(t, dir, "card-d", "-")
	var out, errb bytes.Buffer
	Batch(BatchInput{
		ID: "R4", Deadline: 25 * time.Second, Cards: tsv, Root: root,
		Harness: harness, Slots: "1-2", Self: self,
		Stdout: &out, Stderr: &errb,
	})
	if strings.Contains(errb.String(), "--runner is required") {
		t.Fatalf("batch still demands a runner script: %q", errb.String())
	}
	raw, err := os.ReadFile(filepath.Join(root, "1", "jobs", "card-d", "harness.log"))
	if err != nil {
		t.Fatalf("no harness log under the slot, so no native ran: %v; err=%q", err, errb.String())
	}
	if !strings.Contains(string(raw), "NATIVE OK label=card-d") {
		t.Fatalf("the card did not run through this binary's own native: %q", raw)
	}
}

// TestBatchRefusesWithNeitherRunnerNorHarness: one refusal naming both doors.
func TestBatchRefusesWithNeitherRunnerNorHarness(t *testing.T) {
	dir := t.TempDir()
	tsv := slotsCard(t, dir, "card-e", "-")
	var out, errb bytes.Buffer
	code := Batch(BatchInput{
		ID: "R5", Deadline: 20 * time.Second, Cards: tsv, Root: filepath.Join(dir, "root"),
		Stdout: &out, Stderr: &errb,
	})
	if code != 2 {
		t.Fatalf("exit=%d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--runner or --harness is required") {
		t.Fatalf("the refusal names neither door: %q", errb.String())
	}
}

func TestParseSlotRange(t *testing.T) {
	for _, tc := range []struct {
		in         string
		lo, hi     int
		wantRefuse bool
	}{
		{"", 1, 0, false},
		{"16", 1, 16, false},
		{"101-160", 101, 160, false},
		{"160-101", 0, 0, true},
		{"x", 0, 0, true},
		{"0", 0, 0, true},
	} {
		lo, hi, err := ParseSlotRange(tc.in)
		if tc.wantRefuse {
			if err == nil {
				t.Fatalf("%q should be refused", tc.in)
			}
			continue
		}
		if err != nil || lo != tc.lo || hi != tc.hi {
			t.Fatalf("%q -> %d,%d,%v", tc.in, lo, hi, err)
		}
	}
}

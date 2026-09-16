package swarm

import (
	"bytes"
	"fmt"
	"os"
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
		// NO github URL in a test card: admission probes every repository a card names
		// over the real network (admitrepo.go), which is seconds per card and a flake in
		// CI. The shape practice 17 wants is the STEP 1 line, not a clone.
		"STEP 1. mkdir -p scratch && export TMPDIR=$PWD/scratch && work in the job directory\n" +
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

// TestSelfNativeBuildsTheNativeArgv is #636's cheap half, and it is the half that runs on
// every PR: the end-to-end case (a real nova-swarm, a real wall) costs a `go build` and
// lives behind the slow tag in batch_selfnative_slow_test.go. This one asserts the argv the
// batch hands its own `native` -- the contract bin/nova-native-runner.sh used to carry by
// hand -- in process, in microseconds.
func TestSelfNativeBuildsTheNativeArgv(t *testing.T) {
	dir := t.TempDir()
	log, err := os.CreateTemp(dir, "log")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	c := batchCard{label: "card-f", slot: 7, model: "opencode/deepseek-v4-flash", cardPath: filepath.Join(dir, "card.md")}
	in := BatchInput{Root: dir, Deadline: 1500 * time.Second, Harness: "/opt/harness/opencode", Auth: "/opt/auth.json", Self: "/opt/bin/nova-swarm"}
	cmd, err := selfNative(c, in, log)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/opt/bin/nova-swarm", "native",
		"--harness", "/opt/harness/opencode",
		"--model", "opencode/deepseek-v4-flash",
		"--label", "card-f",
		"--card", c.cardPath,
		// THE SLOT DIRECTORY, not the job directory: native makes <slot>/jobs/<label>
		// itself, which is what bin/nova-native-runner.sh passes and what the space runner
		// passes. A job directory here would nest jobs/<label>/jobs/<label>.
		"--slot", filepath.Join(dir, "7"),
		"--root", dir,
		"--deadline", "1500s",
		"--auth", "/opt/auth.json",
	}
	if strings.Join(cmd.Args, " ") != strings.Join(want, " ") {
		t.Fatalf("argv\n got: %v\nwant: %v", cmd.Args, want)
	}
	if cmd.SysProcAttr == nil {
		t.Fatalf("a card runs in its own process group, so the deadline reaches everything it started")
	}
}

// TestSwarmSelfRefusesWithNoBinaryToRunNativeFrom: the refusal names both doors rather than
// running whatever binary happens to be executing (a test binary, once).
func TestSwarmSelfRefusesWithNoBinaryToRunNativeFrom(t *testing.T) {
	if got, err := swarmSelf("/opt/bin/nova-swarm"); err != nil || got != "/opt/bin/nova-swarm" {
		t.Fatalf("a named binary is used as named: %q %v", got, err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := swarmSelf(""); err == nil || !strings.Contains(err.Error(), "--runner") {
		t.Fatalf("with no nova-swarm anywhere the refusal names --runner: %v", err)
	}
}

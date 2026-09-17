package swarm

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// card636 writes a practice-17 shaped card and a one-row cards.tsv naming it, with the
// slot column set to "-" so the batch allocates one.
func card636(t *testing.T, dir, label string) string {
	t.Helper()
	card := filepath.Join(dir, label+".md")
	body := "RESULT " + label + " sha=000000000000\nYou are a worker.\n" +
		"STEP 1. mkdir -p scratch && export TMPDIR=$PWD/scratch && git clone -q https://github.com/mas-bandwidth/nova-tools.git . && git checkout -b rowan/" + label + "\n" +
		"STEP 2. write RESULT.md\n"
	if err := os.WriteFile(card, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	tsv := filepath.Join(dir, label+"-cards.tsv")
	if err := os.WriteFile(tsv, []byte(label+"\t-\topencode/deepseek-v4-flash\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return tsv
}

// self636 writes a stand-in for this same binary: it records the argv it was handed and
// prints the NATIVE OK line a real `nova-swarm native` prints, so the test can prove the
// batch reached its own native verb with no runner script at all.
func self636(t *testing.T, dir string) string {
	t.Helper()
	argvFile := filepath.Join(dir, "self-argv")
	self := filepath.Join(dir, "nova-swarm")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > " + argvFile + "\n" +
		"echo NATIVE OK label=card-f\n"
	if err := os.WriteFile(self, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return self
}

// TestCard8909BatchRunsNativeWithNoRunner is issue #636's red test: with no --runner the
// batch refused the whole invocation ("--runner is required; it wants the command one
// process per card runs"), although `native` lives in the same binary. With --harness the
// card must run through this binary's own native verb, no runner script.
func TestCard8909BatchRunsNativeWithNoRunner(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	harness := filepath.Join(dir, "harness")
	if err := os.WriteFile(harness, []byte("#!/bin/sh\necho FAKE-HARNESS \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tsv := card636(t, dir, "card-f")
	var out, errb bytes.Buffer
	Batch(BatchInput{
		ID: "TP1", Deadline: 10 * time.Second, Cards: tsv, Root: root,
		Harness: harness, Self: self636(t, dir),
		Stdout: &out, Stderr: &errb,
	})
	if strings.Contains(errb.String(), "--runner is required") {
		t.Fatalf("batch still demands a runner script: %q", errb.String())
	}
	raw, err := os.ReadFile(filepath.Join(root, "1", "jobs", "card-f", "harness.log"))
	if err != nil {
		t.Fatalf("no harness log under the slot, so no native ran: %v; err=%q", err, errb.String())
	}
	if !strings.Contains(string(raw), "NATIVE OK") {
		t.Fatalf("the card did not run through this binary's own native: %q", raw)
	}
	argv, err := os.ReadFile(filepath.Join(dir, "self-argv"))
	if err != nil {
		t.Fatalf("the runnerless batch never reached its own native verb: %v", err)
	}
	if !strings.Contains(string(argv), "native") || !strings.Contains(string(argv), "--harness") {
		t.Fatalf("the self invocation is not a native run: %q", argv)
	}
}

// TestCard8909BatchRefusesWithNeitherRunnerNorHarness: one refusal naming both doors.
func TestCard8909BatchRefusesWithNeitherRunnerNorHarness(t *testing.T) {
	dir := t.TempDir()
	tsv := card636(t, dir, "card-g")
	var out, errb bytes.Buffer
	code := Batch(BatchInput{
		ID: "TP1", Deadline: 10 * time.Second, Cards: tsv, Root: filepath.Join(dir, "root"),
		Stdout: &out, Stderr: &errb,
	})
	if code != 2 {
		t.Fatalf("exit=%d, want 2; stderr=%q", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--runner or --harness is required") {
		t.Fatalf("the refusal names neither door: %q", errb.String())
	}
}

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The gate's blind spot at the command line. On 2026-09-18 the gate reported
// `open-edges=0` at exit 0 with not-ok receipts sitting in the directory it had
// just read, and `findings=1` while three more sat unmatched beside it: every
// receipt for a verb the list does not declare -- `harvest`, `ledger` for
// `dogfood ledger`, every nova-sandbox verb, `nova-merge batch` and
// `nova-merge queue` at 65e23fb0 -- simply vanished from the arithmetic. A
// count that silently leaves evidence out is worse than no count.

// The number is on the line both reads print, so nobody has to read a note to
// learn that evidence was discarded.
func TestDogfoodLineCountsTheUnmatchedReceipts(t *testing.T) {
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	writeReceipt(t, receipts, "a.json", map[string]any{
		"tool": "nova-example", "verb": "links", "by": "Stella",
		"at": "2026-09-18T09:00:00Z", "ok": true, "notes": "real work",
	})
	writeReceipt(t, receipts, "b.json", map[string]any{
		"tool": "nova-example", "verb": "harvest", "by": "Stella",
		"at": "2026-09-18T09:05:00Z", "ok": true, "notes": "real work",
	})
	code, stdout, stderr := dogfoodRun(t, "dogfood", "ledger", "--cli", cli, "--receipts", receipts)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "unmatched=1") {
		t.Errorf("the ledger line does not count what it threw away:\n%s", stdout)
	}
	code, stdout, stderr = dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts)
	if code != 0 {
		t.Fatalf("gate exit %d, want 0 (an unmatched OK receipt is not a failure)\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "unmatched=1") {
		t.Errorf("the gate line does not count what it threw away:\n%s", stdout)
	}
}

// A not-ok receipt that matched nothing is a FAILURE, and the failure names the
// receipt's file and the verb it claimed.
func TestDogfoodGateFailsOnAnUnmatchedNotOkReceipt(t *testing.T) {
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	writeReceipt(t, receipts, "a.json", map[string]any{
		"tool": "nova-example", "verb": "links", "by": "Stella",
		"at": "2026-09-18T09:00:00Z", "ok": true, "notes": "real work",
	})
	writeReceipt(t, receipts, "bad.json", map[string]any{
		"tool": "nova-merge", "verb": "batch", "by": "Stella",
		"at": "2026-09-18T09:05:00Z", "ok": false, "notes": "it refused a batch that was on dev",
	})
	code, stdout, stderr := dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts)
	if code != 1 {
		t.Fatalf("exit %d, want 1: a not-ok receipt nobody can match is not an open-edges=0 bench\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{"bad.json", "nova-merge", "batch"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the gate does not name %q:\n%s", want, stderr)
		}
	}
	if !strings.Contains(stderr, "unmatched=1") {
		t.Errorf("the red count line does not carry the unmatched count:\n%s", stderr)
	}
}

// `record --tools <dir>` takes the verb list from the binaries themselves. The
// report found no --tools on record at 65e23fb0; this locks in that it is there
// and that it is the list the spelling is checked against.
func TestDogfoodRecordChecksTheSpellingAgainstTheBinaries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture is a shell script")
	}
	tools := t.TempDir()
	script := "#!/bin/sh\ncat <<'EOF'\nnova-example: a fixture\n\nusage:\n  nova-example links --dir <dir>\n  nova-example ask   delivers ONE unit to the FRIEND who owns it\nEOF\n"
	if err := os.WriteFile(filepath.Join(tools, "nova-example"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	receipts := filepath.Join(t.TempDir(), "receipts")
	// `ask` is in the binary and in no reference: --tools alone accepts it.
	code, stdout, stderr := dogfoodRun(t, "dogfood", "record", "--tools", tools,
		"--tool", "nova-example", "--verb", "ask", "--by", "Stella", "--ok",
		"--notes", "one real ask sent", "--receipts", receipts)
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "DOGFOOD RECORD OK ") {
		t.Fatalf("record said:\n%s", stdout)
	}
	// And a spelling neither source declares is still refused, by name.
	code, _, stderr = dogfoodRun(t, "dogfood", "record", "--tools", tools,
		"--tool", "nova-example", "--verb", "aks", "--by", "Stella", "--ok",
		"--notes", "real work", "--receipts", receipts)
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(stderr, "nova-example ask") {
		t.Errorf("the refusal does not name the nearest declared verb:\n%s", stderr)
	}
}

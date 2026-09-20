package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Sprint Row 12 (#1839):
// Witness test for evidence holds and verified receipts in nova-check.
//
// PROVES:
// 1. An unverified evidence record admits nothing (evidence hold):
//    - An empty receipts directory refuses admission (exit 1), naming the path.
//    - An unparseable/corrupted receipt file refuses admission (exit 1), naming the broken file.
//    - An unmatched not-ok receipt refuses admission (exit 1), naming the unmatched file and verb.
//    - An uncleared/open edge refuses admission (exit 1), naming the edge/issue.
//    - A declared verb with missing non-author receipts under --require-all refuses admission (exit 1).
// 2. Verified receipts admit:
//    - When valid non-author receipts with ok=true and no open edges cover all declared verbs,
//      the gate passes with exit 0 ("DOGFOOD GATE OK ..."), admitting the release.
// 3. Stale evidence does not unmeet a need:
//    - A verb dogfooded by a non-author at an older timestamp remains dogfooded (ok=yes, by-nonauthor=1)
//      even after a newer author receipt is recorded; older/stale evidence does not unmeet the requirement.
//    - An older edge that has been cleared by a subsequent clean run remains cleared (open-edges=0),
//      and the presence of the older edge does not unmeet the gate.

func TestWitnessEvidenceHoldAndVerifiedReceipts(t *testing.T) {
	dir := t.TempDir()
	cli := writeCLI(t, dir)

	// ------------------------------------------------------------------
	// 1. AN UNVERIFIED EVIDENCE RECORD ADMITS NOTHING (EVIDENCE HOLD)
	// ------------------------------------------------------------------

	// 1a. Empty receipts directory without --allow-empty refuses admission
	emptyReceipts := filepath.Join(dir, "empty-receipts")
	if err := os.MkdirAll(emptyReceipts, 0o755); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", emptyReceipts)
	if code != 1 {
		t.Fatalf("empty receipts: exit %d, want 1 (must refuse admission on zero evidence)", code)
	}
	if !strings.Contains(stderr, "no receipts were read from") ||
		!strings.Contains(stderr, "so the gate has nothing to pass on; add receipts, or pass --allow-empty to say that is deliberate") {
		t.Fatalf("refusal line did not name empty receipts cause:\n%s", stderr)
	}

	// 1b. Corrupted/unparseable receipt file refuses admission
	brokenReceipts := filepath.Join(dir, "broken-receipts")
	if err := os.MkdirAll(brokenReceipts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(brokenReceipts, "corrupt.json"), []byte("{not-valid-json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", brokenReceipts)
	if code != 1 {
		t.Fatalf("broken receipt: exit %d, want 1", code)
	}
	if strings.Contains(stdout, "DOGFOOD GATE OK") {
		t.Fatalf("gate passed over corrupt receipt:\n%s", stdout)
	}
	if !strings.Contains(stderr, "DOGFOOD FAIL") || !strings.Contains(stderr, "corrupt.json") {
		t.Fatalf("stderr did not name corrupt receipt:\n%s", stderr)
	}

	// 1c. Unmatched not-ok receipt refuses admission
	unmatchedReceipts := filepath.Join(dir, "unmatched-receipts")
	writeReceipt(t, unmatchedReceipts, "unmatched.json", map[string]any{
		"tool": "nova-example", "verb": "ghost-verb", "by": "Stella",
		"at": "2026-09-18T09:00:00Z", "ok": false, "notes": "failed run on undeclared verb",
	})
	code, _, stderr = dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", unmatchedReceipts)
	if code != 1 {
		t.Fatalf("unmatched not-ok receipt: exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "unmatched") || !strings.Contains(stderr, "ghost-verb") {
		t.Fatalf("stderr did not report unmatched negative receipt:\n%s", stderr)
	}

	// 1d. Uncleared open edge refuses admission
	edgeReceipts := filepath.Join(dir, "edge-receipts")
	writeReceipt(t, edgeReceipts, "edge.json", map[string]any{
		"tool": "nova-example", "verb": "links", "by": "Stella",
		"at": "2026-09-18T09:00:00Z", "ok": false, "notes": "broken link parser", "issue": 1839,
	})
	code, _, stderr = dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", edgeReceipts)
	if code != 1 {
		t.Fatalf("open edge: exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "#1839") {
		t.Fatalf("stderr did not name open edge issue:\n%s", stderr)
	}

	// 1e. Under --require-all, missing non-author receipts for declared verbs refuses admission
	partialReceipts := filepath.Join(dir, "partial-receipts")
	writeReceipt(t, partialReceipts, "links.json", map[string]any{
		"tool": "nova-example", "verb": "links", "by": "Stella",
		"at": "2026-09-18T09:00:00Z", "ok": true, "notes": "verified links pass",
	})
	code, _, stderr = dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", partialReceipts, "--require-all")
	if code != 1 {
		t.Fatalf("missing non-author pass on --require-all: exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "verb=quickstart") || !strings.Contains(stderr, "verb=nocode") {
		t.Fatalf("stderr did not name un-dogfooded verbs:\n%s", stderr)
	}

	// ------------------------------------------------------------------
	// 2. VERIFIED RECEIPTS ADMIT
	// ------------------------------------------------------------------

	verifiedReceipts := filepath.Join(dir, "verified-receipts")
	for i, verb := range []string{"quickstart", "links", "nocode"} {
		writeReceipt(t, verifiedReceipts, string(rune('a'+i))+".json", map[string]any{
			"tool": "nova-example", "verb": verb, "by": "Stella",
			"at": "2026-09-18T10:00:00Z", "ok": true, "notes": "clean pass on real work",
		})
	}
	code, stdout, stderr = dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", verifiedReceipts, "--require-all")
	if code != 0 {
		t.Fatalf("verified receipts: exit %d, want 0\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "DOGFOOD GATE OK verbs=3 by-nonauthor=3 open-edges=0 unfiled=0 unmatched=0 require-all=yes") {
		t.Fatalf("unexpected gate output for verified receipts:\n%s", stdout)
	}

	// ------------------------------------------------------------------
	// 3. STALE EVIDENCE DOES NOT UNMEET A NEED
	// ------------------------------------------------------------------

	staleReceipts := filepath.Join(dir, "stale-receipts")
	// An older receipt from non-author Stella at an early date:
	writeReceipt(t, staleReceipts, "stale-nonauthor.json", map[string]any{
		"tool": "nova-example", "verb": "links", "by": "Stella",
		"at": "2026-09-10T08:00:00Z", "ok": true, "notes": "early non-author dogfood run",
	})
	// A newer receipt from author Rowan at a later date:
	writeReceipt(t, staleReceipts, "newer-author.json", map[string]any{
		"tool": "nova-example", "verb": "links", "by": "Rowan",
		"at": "2026-09-18T12:00:00Z", "ok": true, "notes": "author run on updated code",
	})
	authors := writeAuthors(t, dir, "nova-example links = Rowan\n")

	// Ledger inspection: the older non-author receipt still satisfies by-nonauthor requirement
	code, stdout, stderr = dogfoodRun(t, "dogfood", "ledger", "--cli", cli, "--receipts", staleReceipts, "--authors", authors)
	if code != 0 {
		t.Fatalf("ledger with stale receipt: exit %d, want 0\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "dogfooded=1 by-nonauthor=1") {
		t.Fatalf("stale non-author evidence was improperly uncounted by newer author run:\n%s", stdout)
	}
	// The best row shown in ledger is the non-author pass:
	if !strings.Contains(stdout, "DOGFOOD tool=nova-example verb=links by=Stella") {
		t.Fatalf("ledger did not prioritize non-author receipt over author receipt:\n%s", stdout)
	}

	// Gate inspection: gate passes on the single dogfooded verb
	code, stdout, stderr = dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", staleReceipts, "--authors", authors)
	if code != 0 {
		t.Fatalf("gate with stale receipt: exit %d, want 0\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "DOGFOOD GATE OK verbs=3 by-nonauthor=1 open-edges=0") {
		t.Fatalf("gate did not pass with stale non-author evidence:\n%s", stdout)
	}

	// 3b. Stale cleared edge does not unmeet the gate
	clearedReceipts := filepath.Join(dir, "cleared-receipts")
	// Older receipt with an edge:
	writeReceipt(t, clearedReceipts, "old-edge.json", map[string]any{
		"tool": "nova-example", "verb": "links", "by": "Stella",
		"at": "2026-09-10T08:00:00Z", "ok": false, "notes": "old defect", "issue": 1839,
	})
	// Later receipt by non-author with clean pass clearing the edge:
	writeReceipt(t, clearedReceipts, "new-clean.json", map[string]any{
		"tool": "nova-example", "verb": "links", "by": "Stella",
		"at": "2026-09-15T08:00:00Z", "ok": true, "notes": "defect resolved, verified clean",
	})
	code, stdout, stderr = dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", clearedReceipts)
	if code != 0 {
		t.Fatalf("cleared edge: exit %d, want 0 (stale edge was cleared and must not unmeet gate)\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "open-edges=0") {
		t.Fatalf("stale edge was not cleared:\n%s", stdout)
	}
}

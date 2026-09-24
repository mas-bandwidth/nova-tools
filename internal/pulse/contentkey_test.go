package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubLauncher records launched cards.
type stubLauncher struct {
	calls []string
}

func (s *stubLauncher) Launch(bench, seat, card string) error {
	s.calls = append(s.calls, bench+" "+card)
	return nil
}

// stubCapacity returns a fixed capacity.
type stubCapacity int

func (c stubCapacity) Capacity(bench string) (int, error) {
	return int(c), nil
}

func writeTestMachines(t *testing.T, dir string, names ...string) string {
	t.Helper()
	var b strings.Builder
	for _, name := range names {
		b.WriteString(name + "\t" + name + "\tlinux/x64\tbench\tswarm-" + name + "\t64\t-\n")
	}
	path := filepath.Join(dir, "machines.tsv")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCardContentKey_DeterministicWithoutLabelSuffix(t *testing.T) {
	card1 := `RESULT CARD-001 sha=60df906d1234
LABEL: card-001
Do the implementation of feature X.
Check error bounds.`

	card2 := `RESULT card-999 sha=60df906d1234
LABEL: card-999
Do the implementation of feature X.
Check error bounds.`

	card3 := `RESULT read-812 sha=60df906d1234
Do the implementation of feature X.
Check error bounds.`

	norm1 := StripCardLabel(card1)
	norm2 := StripCardLabel(card2)
	norm3 := StripCardLabel(card3)

	if norm1 != norm2 {
		t.Fatalf("StripCardLabel not deterministic across CARD-001 and card-999:\n--- norm1 ---\n%s\n--- norm2 ---\n%s", norm1, norm2)
	}
	if norm1 != norm3 {
		t.Fatalf("StripCardLabel not deterministic across CARD-001 and read-812:\n--- norm1 ---\n%s\n--- norm3 ---\n%s", norm1, norm3)
	}

	k1 := ComputeCardKey(card1, "60df906d1234", "default")
	k2 := ComputeCardKey(card2, "60df906d1234", "default")
	k3 := ComputeCardKey(card3, "60df906d1234", "default")

	if k1.String() != k2.String() {
		t.Fatalf("CardKey mismatch: k1=%s, k2=%s", k1.String(), k2.String())
	}
	if k1.String() != k3.String() {
		t.Fatalf("CardKey mismatch: k1=%s, k3=%s", k1.String(), k3.String())
	}
	if len(k1.TextHash) != 16 {
		t.Fatalf("expected 16-hex text hash, got %q (len %d)", k1.TextHash, len(k1.TextHash))
	}
}

func TestCardContentKey_BaseMovedDistinguishesOlderBase(t *testing.T) {
	queueDir := t.TempDir()
	ks := NewKeyStore(queueDir)

	cardTextOld := `RESULT card-1 sha=aaaa11112222
Do task A.`
	cardTextNew := `RESULT card-1 sha=bbbb33334444
Do task A.`

	keyOld := ComputeCardKey(cardTextOld, "aaaa11112222", "default")
	keyNew := ComputeCardKey(cardTextNew, "bbbb33334444", "default")

	if keyOld.String() == keyNew.String() {
		t.Fatalf("expected different keys for different bases, got %s", keyOld.String())
	}
	if keyOld.TextHash != keyNew.TextHash {
		t.Fatalf("expected same text hash, got %s vs %s", keyOld.TextHash, keyNew.TextHash)
	}

	// Before recording anything: never run
	decision, _ := ks.CheckKey(keyOld, false)
	if decision != DecisionNeverRun {
		t.Fatalf("expected DecisionNeverRun, got %v", decision)
	}

	// Record DONE for keyOld
	if err := ks.RecordDone(keyOld, "card-1.md", 2501, "c0ffee", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// Now check keyOld: should be cached
	decisionOld, recOld := ks.CheckKey(keyOld, false)
	if decisionOld != DecisionCached {
		t.Fatalf("expected DecisionCached for keyOld, got %v", decisionOld)
	}
	if recOld.PR != 2501 {
		t.Fatalf("expected PR 2501, got %d", recOld.PR)
	}

	// Now check keyNew at new base: should be DecisionOlderBase
	decisionNew, recOlder := ks.CheckKey(keyNew, false)
	if decisionNew != DecisionOlderBase {
		t.Fatalf("expected DecisionOlderBase for keyNew, got %v", decisionNew)
	}
	if recOlder.Base != "aaaa11112222" {
		t.Fatalf("expected older base aaaa11112222, got %s", recOlder.Base)
	}

	// Check a completely different card: should be DecisionNeverRun
	cardOther := `RESULT card-2 sha=bbbb33334444
Something completely different.`
	keyOther := ComputeCardKey(cardOther, "bbbb33334444", "default")
	decisionOther, _ := ks.CheckKey(keyOther, false)
	if decisionOther != DecisionNeverRun {
		t.Fatalf("expected DecisionNeverRun for keyOther, got %v", decisionOther)
	}
}

func TestDealer_RefusesCachedDoneWithoutForce(t *testing.T) {
	dir := t.TempDir()
	queueDir := filepath.Join(dir, "queue")
	readyDir := filepath.Join(queueDir, "ready")
	launchedDir := filepath.Join(queueDir, "launched")
	doneDir := filepath.Join(queueDir, "done")
	_ = os.MkdirAll(readyDir, 0o755)

	cardContent := "RESULT card-1 sha=60df906d1234\nDo task 1.\n"
	cardPath := filepath.Join(readyDir, "card-1.md")
	if err := os.WriteFile(cardPath, []byte(cardContent), 0o644); err != nil {
		t.Fatal(err)
	}

	ks := NewKeyStore(queueDir)
	ck := ComputeCardKey(cardContent, "60df906d1234", "")
	if err := ks.RecordDone(ck, "card-1.md", 2502, "5fd1ca5f8616", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	launcher := &stubLauncher{}
	var stdout, stderr bytes.Buffer

	code := Fill(FillInput{
		Ready:    readyDir,
		Launched: launchedDir,
		Done:     doneDir,
		Queue:    queueDir,
		Machines: writeTestMachines(t, dir, "bench-a"),
		Benches:  []string{"bench-a"},
		Once:     true,
		Capacity: stubCapacity(2),
		Launcher: launcher,
		KeyStore: ks,
		Stdout:   &stdout,
		Stderr:   &stderr,
	})

	if code != 0 {
		t.Fatalf("Fill failed with code %d: %s", code, stderr.String())
	}
	if len(launcher.calls) != 0 {
		t.Fatalf("expected 0 launches for cached card, got %d: %v", len(launcher.calls), launcher.calls)
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "FILL CACHED") || !strings.Contains(outStr, "pr=2502") {
		t.Fatalf("expected FILL CACHED with pr=2502 in stdout, got:\n%s", outStr)
	}

	// Card should have moved from ready to done
	if _, err := os.Stat(cardPath); !os.IsNotExist(err) {
		t.Fatalf("card-1.md still exists in ready/")
	}
	if _, err := os.Stat(filepath.Join(doneDir, "card-1.md")); err != nil {
		t.Fatalf("card-1.md was not moved to done/: %v", err)
	}
}

func TestDealer_ForceReexecutesCachedDone(t *testing.T) {
	dir := t.TempDir()
	queueDir := filepath.Join(dir, "queue")
	readyDir := filepath.Join(queueDir, "ready")
	launchedDir := filepath.Join(queueDir, "launched")
	doneDir := filepath.Join(queueDir, "done")
	_ = os.MkdirAll(readyDir, 0o755)

	cardContent := "RESULT card-1 sha=60df906d1234\nDo task 1.\n"
	cardPath := filepath.Join(readyDir, "card-1.md")
	if err := os.WriteFile(cardPath, []byte(cardContent), 0o644); err != nil {
		t.Fatal(err)
	}

	ks := NewKeyStore(queueDir)
	ck := ComputeCardKey(cardContent, "60df906d1234", "")
	if err := ks.RecordDone(ck, "card-1.md", 2502, "5fd1ca5f8616", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	launcher := &stubLauncher{}
	var stdout, stderr bytes.Buffer

	code := Fill(FillInput{
		Ready:    readyDir,
		Launched: launchedDir,
		Done:     doneDir,
		Queue:    queueDir,
		Machines: writeTestMachines(t, dir, "bench-a"),
		Benches:  []string{"bench-a"},
		Once:     true,
		Force:    true, // Force re-execution
		Capacity: stubCapacity(2),
		Launcher: launcher,
		KeyStore: ks,
		Stdout:   &stdout,
		Stderr:   &stderr,
	})

	if code != 0 {
		t.Fatalf("Fill failed with code %d: %s", code, stderr.String())
	}
	if len(launcher.calls) != 1 {
		t.Fatalf("expected 1 launch under Force, got %d: %v", len(launcher.calls), launcher.calls)
	}
	if !strings.Contains(launcher.calls[0], "card-1.md") {
		t.Fatalf("expected launcher call for card-1.md, got %s", launcher.calls[0])
	}
}

func TestDealer_ActiveAttemptRefusesEvenWithForce(t *testing.T) {
	// Stella Rule 3: Active attempts retain lease and fence and CANNOT be duplicated, including with --force.
	dir := t.TempDir()
	queueDir := filepath.Join(dir, "queue")
	readyDir := filepath.Join(queueDir, "ready")
	launchedDir := filepath.Join(queueDir, "launched")
	_ = os.MkdirAll(readyDir, 0o755)

	cardContent := "RESULT card-1 sha=60df906d1234\nIn flight task.\n"
	cardPath := filepath.Join(readyDir, "card-1.md")
	if err := os.WriteFile(cardPath, []byte(cardContent), 0o644); err != nil {
		t.Fatal(err)
	}

	ks := NewKeyStore(queueDir)
	ck := ComputeCardKey(cardContent, "60df906d1234", "")
	if err := ks.RecordActive(ck, "card-1.md", "attempt-active-99", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	launcher := &stubLauncher{}
	var stdout, stderr bytes.Buffer

	// Run with Force: true! Must STILL refuse to duplicate active attempt.
	code := Fill(FillInput{
		Ready:    readyDir,
		Launched: launchedDir,
		Queue:    queueDir,
		Machines: writeTestMachines(t, dir, "bench-a"),
		Benches:  []string{"bench-a"},
		Once:     true,
		Force:    true,
		Capacity: stubCapacity(2),
		Launcher: launcher,
		KeyStore: ks,
		Stdout:   &stdout,
		Stderr:   &stderr,
	})

	if code != 0 {
		t.Fatalf("Fill failed with code %d: %s", code, stderr.String())
	}
	if len(launcher.calls) != 0 {
		t.Fatalf("expected 0 launches for active attempt under force, got %d: %v", len(launcher.calls), launcher.calls)
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "FILL ACTIVE") || !strings.Contains(outStr, "attempt-active-99") {
		t.Fatalf("expected FILL ACTIVE note in stdout, got:\n%s", outStr)
	}
}

func TestDealer_DoneAtOlderBaseOfferedForRebase(t *testing.T) {
	dir := t.TempDir()
	queueDir := filepath.Join(dir, "queue")
	readyDir := filepath.Join(queueDir, "ready")
	launchedDir := filepath.Join(queueDir, "launched")
	_ = os.MkdirAll(readyDir, 0o755)

	// Card was run at base sha=aaaa11112222, and now is ready at base sha=bbbb33334444
	cardContentOld := "RESULT card-1 sha=aaaa11112222\nDo task A.\n"
	cardContentNew := "RESULT card-1 sha=bbbb33334444\nDo task A.\n"

	cardPath := filepath.Join(readyDir, "card-1.md")
	if err := os.WriteFile(cardPath, []byte(cardContentNew), 0o644); err != nil {
		t.Fatal(err)
	}

	ks := NewKeyStore(queueDir)
	ckOld := ComputeCardKey(cardContentOld, "aaaa11112222", "")
	if err := ks.RecordDone(ckOld, "card-1.md", 2490, "111122223333", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	launcher := &stubLauncher{}
	var stdout, stderr bytes.Buffer

	// Without --force: should emit FILL BASE-MOVED and stay in ready (held for mechanical rebase)
	code := Fill(FillInput{
		Ready:    readyDir,
		Launched: launchedDir,
		Queue:    queueDir,
		Machines: writeTestMachines(t, dir, "bench-a"),
		Benches:  []string{"bench-a"},
		Once:     true,
		Force:    false,
		Capacity: stubCapacity(2),
		Launcher: launcher,
		KeyStore: ks,
		Stdout:   &stdout,
		Stderr:   &stderr,
	})

	if code != 0 {
		t.Fatalf("Fill failed with code %d: %s", code, stderr.String())
	}
	if len(launcher.calls) != 0 {
		t.Fatalf("expected 0 launches when done at older base without force, got %d", len(launcher.calls))
	}
	outStr := stdout.String()
	if !strings.Contains(outStr, "FILL BASE-MOVED") || !strings.Contains(outStr, "prior-base=aaaa11112222") {
		t.Fatalf("expected FILL BASE-MOVED in stdout, got:\n%s", outStr)
	}

	// Now with --force: should re-execute
	launcher.calls = nil
	stdout.Reset()
	codeForce := Fill(FillInput{
		Ready:    readyDir,
		Launched: launchedDir,
		Queue:    queueDir,
		Machines: writeTestMachines(t, dir, "bench-a"),
		Benches:  []string{"bench-a"},
		Once:     true,
		Force:    true,
		Capacity: stubCapacity(2),
		Launcher: launcher,
		KeyStore: ks,
		Stdout:   &stdout,
		Stderr:   &stderr,
	})

	if codeForce != 0 {
		t.Fatalf("Fill with force failed with code %d: %s", codeForce, stderr.String())
	}
	if len(launcher.calls) != 1 {
		t.Fatalf("expected 1 launch under Force for older base card, got %d", len(launcher.calls))
	}
}

func TestLedger_CarriesKeyAcrossReRuns(t *testing.T) {
	queueDir := t.TempDir()

	row1 := LedgerRow{
		PR:         100,
		Head:       "aaaa00001111",
		Card:       "card-100.md",
		Verdict:    "APPROVE",
		At:         "2026-09-21T10:00:00Z",
		EnqueuedAt: "-",
		ClosedAt:   "-",
		Key:        "f00dbabe12345678:60df906d1234:route-a",
	}

	if err := AppendLedger(queueDir, row1); err != nil {
		t.Fatal(err)
	}

	rows, err := ReadLedger(queueDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Key != "f00dbabe12345678:60df906d1234:route-a" {
		t.Fatalf("expected key to be preserved, got %q", rows[0].Key)
	}

	// Verify backwards compatibility: write a 7-column legacy row
	legacyRow := "101\tbbbb11112222\tcard-101.md\tAPPROVE\t2026-09-21T10:05:00Z\t-\t-\n"
	ledgerFile := filepath.Join(queueDir, ledgerFileName)
	f, err := os.OpenFile(ledgerFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(legacyRow); err != nil {
		t.Fatal(err)
	}
	f.Close()

	rowsAfter, err := ReadLedger(queueDir)
	if err != nil {
		t.Fatalf("ReadLedger failed with legacy 7-column row: %v", err)
	}
	if len(rowsAfter) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rowsAfter))
	}
	if rowsAfter[1].Key != ledgerDash {
		t.Fatalf("expected legacy row key to default to %q, got %q", ledgerDash, rowsAfter[1].Key)
	}

	// Verify KeyStore.Load() picks up row1's key as DONE
	ks := NewKeyStore(queueDir)
	parsedKey, err := ParseCardKey(row1.Key)
	if err != nil {
		t.Fatal(err)
	}
	dec, rec := ks.CheckKey(parsedKey, false)
	if dec != DecisionCached {
		t.Fatalf("expected KeyStore to join ledger row as DecisionCached, got %v", dec)
	}
	if rec.PR != 100 {
		t.Fatalf("expected PR 100, got %d", rec.PR)
	}
}

func TestPerModelReport_GroupsByKey(t *testing.T) {
	records := []KeyRecord{
		{Key: "hash1:baseA:flash", Route: "flash", Status: KeyStatusDone, PR: 201, Base: "baseA"},
		{Key: "hash1:baseA:flash", Route: "flash", Status: KeyStatusDone, PR: 201, Base: "baseA"},
		{Key: "hash2:baseA:pro", Route: "pro", Status: KeyStatusDone, PR: 202, Base: "baseA"},
		{Key: "hash3:baseA:flash", Route: "flash", Status: KeyStatusFailed, Base: "baseA"},
	}

	grouped := GroupByKey(records)
	if len(grouped["flash"]) != 2 {
		t.Fatalf("expected 2 keys under flash, got %d", len(grouped["flash"]))
	}
	if len(grouped["pro"]) != 1 {
		t.Fatalf("expected 1 key under pro, got %d", len(grouped["pro"]))
	}

	flashSum := grouped["flash"]["hash1:baseA:flash"]
	if flashSum.Runs != 2 || flashSum.Verified != 2 {
		t.Fatalf("expected 2 runs and 2 verified for hash1, got %d runs, %d verified", flashSum.Runs, flashSum.Verified)
	}

	report := FormatPerModelKeyReport(records)
	if !strings.Contains(report, "## Model: flash") || !strings.Contains(report, "## Model: pro") {
		t.Fatalf("expected report to contain sections for flash and pro, got:\n%s", report)
	}
	if !strings.Contains(report, "hash1:baseA:flash") {
		t.Fatalf("expected hash1:baseA:flash in report, got:\n%s", report)
	}
}

// TestMutation_ContentKeyRedTest is our mutation red-test with teeth:
// It explicitly tests that:
// 1. Active lease check cannot be bypassed under --force (Stella rule 3).
// 2. Cache check prevents duplicate launch without --force.
// 3. Changing base changes the key and prevents false cache hits.
func TestMutation_ContentKeyRedTest(t *testing.T) {
	queueDir := t.TempDir()
	ks := NewKeyStore(queueDir)

	ck := CardKey{TextHash: "mutation12345678", BaseSHA: "base11112222", Route: "fast"}

	// MUTATION 1: Active attempt MUST return DecisionActive even with force=true
	if err := ks.RecordActive(ck, "card-m.md", "attempt-active", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	decWithForce, _ := ks.CheckKey(ck, true)
	if decWithForce != DecisionActive {
		t.Fatalf("MUTATION RED-TEST TEETH: active lease must return DecisionActive under force=true, got %v", decWithForce)
	}

	// MUTATION 2: Completed attempt without force MUST return DecisionCached, never DecisionForce
	ck2 := CardKey{TextHash: "mutation87654321", BaseSHA: "base11112222", Route: "fast"}
	if err := ks.RecordDone(ck2, "card-m2.md", 9999, "head1234", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	decWithoutForce, _ := ks.CheckKey(ck2, false)
	if decWithoutForce != DecisionCached {
		t.Fatalf("MUTATION RED-TEST TEETH: done attempt without force must return DecisionCached, got %v", decWithoutForce)
	}

	// MUTATION 3: Key string formatting must include text hash, base, and route
	keyStr := ck.String()
	if keyStr != "mutation12345678:base11112222:fast" {
		t.Fatalf("MUTATION RED-TEST TEETH: key string must format as <textHash>:<baseSHA>:<route>, got %q", keyStr)
	}
}

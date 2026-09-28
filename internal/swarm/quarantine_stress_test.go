package swarm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLimboLeakAndQuarantineGCStress executes an end-to-end stress test of the limbo leak fix
// and quarantine garbage collection (Issue #2061 / PR #2086).
//
// Requirements:
// 1. In a scratch directory with mock card queues:
// 2. Create 50 rapid cancellations and failures across all taxonomy categories and limbo states.
// 3. Verify all failed cards transition to quarantine/ without leaving orphan locks or leaked limbo slots.
// 4. Verify quarantine metadata files are well-formed JSON.
func TestLimboLeakAndQuarantineGCStress(t *testing.T) {
	t.Parallel()

	// 1. Scratch directory: the test's own temp dir, removed by the testing package.
	scratchRoot := t.TempDir()

	benchDir := filepath.Join(scratchRoot, "bench")
	queueDir := filepath.Join(benchDir, QueueName) // bench/queue
	takenDir := filepath.Join(benchDir, TakenName) // bench/taken
	if err := os.MkdirAll(queueDir, 0o755); err != nil {
		t.Fatalf("mkdir queue: %v", err)
	}
	if err := os.MkdirAll(takenDir, 0o755); err != nil {
		t.Fatalf("mkdir taken: %v", err)
	}

	const totalCards = 50
	t.Logf("=== Starting Stress Test: 50 rapid cancellations & failures in %s ===", scratchRoot)

	// Define card spec helper
	type mockCardSpec struct {
		id       string
		category string
		reason   string
		kind     FailureKind
		attempts int
		log      string
		content  string
		limboWay string // "route", "limbo-file", "limbo-taken", "limbo-dir", "rapid-cancel"
	}

	specs := make([]mockCardSpec, totalCards)

	for i := 0; i < totalCards; i++ {
		cardNum := i + 1
		cardID := fmt.Sprintf("card-%02d", cardNum)

		switch {
		// Group 1: Cards 01-05: input-limit unrecoverable defect (Immediate Quarantine)
		case cardNum >= 1 && cardNum <= 5:
			specs[i] = mockCardSpec{
				id:       cardID,
				category: "card-defect",
				reason:   "input-limit",
				kind:     FailureCardDefect,
				attempts: 1,
				log:      "Rate limit reached: input token limit exceeded (204800 > 131072)",
				content:  fmt.Sprintf(":kind prompt-eval\n:card-id %s\n:tokens 204800\nPrompt exceeds context window limit.", cardID),
				limboWay: "route",
			}

		// Group 2: Cards 06-08: malformed syntax unrecoverable defect (Immediate Quarantine)
		case cardNum >= 6 && cardNum <= 8:
			specs[i] = mockCardSpec{
				id:       cardID,
				category: "card-defect",
				reason:   "malformed-syntax",
				kind:     FailureCardDefect,
				attempts: 1,
				log:      "nova-swarm: malformed card: missing required field :kind",
				content:  fmt.Sprintf(":invalid-yaml-eof\n:card-id %s\nMalformed syntax header.", cardID),
				limboWay: "route",
			}

		// Group 3: Cards 09-10: impossible budget defect (Immediate Quarantine)
		case cardNum >= 9 && cardNum <= 10:
			specs[i] = mockCardSpec{
				id:       cardID,
				category: "card-defect",
				reason:   "malformed-syntax",
				kind:     FailureCardDefect,
				attempts: 1,
				log:      "malformed card: negative effort budget -5",
				content:  fmt.Sprintf(":kind test\n:card-id %s\n:effort -5\nNegative budget.", cardID),
				limboWay: "route",
			}

		// Group 4: Cards 11-15: HTTP 429 rate limit exhausted (Attempts = 3, Quarantine)
		case cardNum >= 11 && cardNum <= 15:
			specs[i] = mockCardSpec{
				id:       cardID,
				category: "transient-provider",
				reason:   "rate-limit",
				kind:     FailureTransientProvider,
				attempts: 3,
				log:      "HTTP 429: Rate limit reached for tokens per minute (TPM)",
				content:  fmt.Sprintf(":kind test\n:card-id %s\n:attempts 3\nHTTP 429 rate limit retry exhausted.", cardID),
				limboWay: "route",
			}

		// Group 5: Cards 16-20: HTTP 503 service unavailable exhausted (Attempts = 3, Quarantine)
		case cardNum >= 16 && cardNum <= 20:
			specs[i] = mockCardSpec{
				id:       cardID,
				category: "transient-provider",
				reason:   "provider-5xx",
				kind:     FailureTransientProvider,
				attempts: 3,
				log:      "HTTP 503 Service Unavailable: upstream overloaded",
				content:  fmt.Sprintf(":kind test\n:card-id %s\n:attempts 3\nHTTP 503 service unavailable exhausted.", cardID),
				limboWay: "route",
			}

		// Group 6: Cards 21-25: Provider network reset exhausted (Attempts = 3, Quarantine)
		case cardNum >= 21 && cardNum <= 25:
			specs[i] = mockCardSpec{
				id:       cardID,
				category: "transient-provider",
				reason:   "provider-network",
				kind:     FailureTransientProvider,
				attempts: 3,
				log:      "post https://api.provider.invalid/v1: connection reset by peer",
				content:  fmt.Sprintf(":kind test\n:card-id %s\n:attempts 3\nNetwork connection reset exhausted.", cardID),
				limboWay: "route",
			}

		// Group 7: Cards 26-28: OOM killed infrastructure crash (Attempts = 3, Quarantine)
		case cardNum >= 26 && cardNum <= 28:
			specs[i] = mockCardSpec{
				id:       cardID,
				category: "infra-crash",
				reason:   "oom-killed",
				kind:     FailureInfraCrash,
				attempts: 3,
				log:      "Killed (process 9988): out of memory",
				content:  fmt.Sprintf(":kind test\n:card-id %s\n:attempts 3\nRunner OOM killed.", cardID),
				limboWay: "route",
			}

		// Group 8: Cards 29-31: Runner segfault (Attempts = 3, Quarantine)
		case cardNum >= 29 && cardNum <= 31:
			specs[i] = mockCardSpec{
				id:       cardID,
				category: "infra-crash",
				reason:   "segfault",
				kind:     FailureInfraCrash,
				attempts: 3,
				log:      "fatal error: segmentation fault (exit 139)",
				content:  fmt.Sprintf(":kind test\n:card-id %s\n:attempts 3\nRunner segfault.", cardID),
				limboWay: "route",
			}

		// Group 9: Cards 32-35: Bench disk full (Unrecoverable infra crash -> Quarantine)
		case cardNum >= 32 && cardNum <= 35:
			specs[i] = mockCardSpec{
				id:       cardID,
				category: "infra-crash",
				reason:   "disk-full",
				kind:     FailureInfraCrash,
				attempts: 1,
				log:      "write /var/tmp/slot: no space left on device",
				content:  fmt.Sprintf(":kind test\n:card-id %s\n:attempts 1\nBench disk full.", cardID),
				limboWay: "route",
			}

		// Group 10: Cards 36-40: Rapid cancellation in taken/ with .provider-failed limbo
		case cardNum >= 36 && cardNum <= 40:
			specs[i] = mockCardSpec{
				id:       cardID,
				category: "transient-provider",
				reason:   "provider-exhausted",
				kind:     FailureTransientProvider,
				attempts: 3,
				log:      "worker cancelled: context deadline exceeded",
				content:  fmt.Sprintf(":kind test\n:card-id %s\nattempts: 3\nWorker aborted in taken directory.", cardID),
				limboWay: "limbo-taken",
			}

		// Group 11: Cards 41-45: Legacy .provider-failed files left in queue/
		case cardNum >= 41 && cardNum <= 45:
			specs[i] = mockCardSpec{
				id:       cardID,
				category: "transient-provider",
				reason:   "provider-exhausted",
				kind:     FailureTransientProvider,
				attempts: 3,
				log:      "provider failed during launch attempt 3",
				content:  fmt.Sprintf(":kind test\n:card-id %s\n:attempts 3\nLegacy provider-failed file in queue.", cardID),
				limboWay: "limbo-file",
			}

		// Group 12: Cards 46-48: Legacy .provider-failed directory in queue/
		case cardNum >= 46 && cardNum <= 48:
			specs[i] = mockCardSpec{
				id:       cardID,
				category: "transient-provider",
				reason:   "provider-exhausted",
				kind:     FailureTransientProvider,
				attempts: 3,
				log:      "abandoned in dot provider-failed directory",
				content:  fmt.Sprintf(":kind test\n:card-id %s\nattempts: 3\nInside dotdir provider-failed.", cardID),
				limboWay: "limbo-dir",
			}

		// Group 13: Cards 49-50: Rapid cancellation with lock file contention
		case cardNum >= 49 && cardNum <= 50:
			specs[i] = mockCardSpec{
				id:       cardID,
				category: "transient-provider",
				reason:   "provider-exhausted",
				kind:     FailureTransientProvider,
				attempts: 3,
				log:      "rapid worker kill with locks held",
				content:  fmt.Sprintf(":kind test\n:card-id %s\n:attempts 3\nRapid kill with lock file.", cardID),
				limboWay: "rapid-cancel",
			}
		}
	}

	// 2. Execute 50 rapid cancellations and failures across concurrent workers
	var wg sync.WaitGroup
	errCh := make(chan error, totalCards)

	workerCount := 10
	t.Logf("Spawning %d concurrent workers to execute 50 rapid cancellations and failures...", workerCount)

	for w := 0; w < workerCount; w++ {
		workerID := fmt.Sprintf("worker-%02d", w)
		startIdx := w * 5
		endIdx := startIdx + 5

		wg.Add(1)
		go func(worker string, cardSlice []mockCardSpec) {
			defer wg.Done()

			for _, spec := range cardSlice {
				contentBytes := []byte(spec.content)

				switch spec.limboWay {
				case "route":
					// Normal pipeline: card failed during execution, classified and routed
					fc := ClassifyFailure(nil, spec.log, 1, "")
					// For infra crashes that are unrecoverable or exhausted
					if spec.kind == FailureInfraCrash && spec.reason == "disk-full" {
						fc.Quarantinable = true
					} else if spec.kind == FailureInfraCrash && spec.attempts >= MaxProviderAttempts {
						fc.Quarantinable = true
					}

					res, err := RouteFailure(queueDir, spec.id, contentBytes, spec.attempts, MaxProviderAttempts, fc)
					if err != nil {
						errCh <- fmt.Errorf("RouteFailure %s: %w", spec.id, err)
						return
					}
					if res.Action != ActionQuarantine {
						errCh <- fmt.Errorf("RouteFailure %s expected ActionQuarantine, got %s", spec.id, res.Action)
						return
					}

				case "limbo-taken":
					// Card was in taken/, worker aborted leaving .provider-failed file
					limboPath := filepath.Join(takenDir, fmt.Sprintf("%s-%s.card%s", worker, spec.id, ProviderFailedExt))
					if err := os.WriteFile(limboPath, contentBytes, 0o644); err != nil {
						errCh <- fmt.Errorf("write limbo-taken %s: %w", spec.id, err)
						return
					}

				case "limbo-file":
					// Card in queue/ left as .provider-failed
					limboPath := filepath.Join(queueDir, fmt.Sprintf("%s%s", spec.id, ProviderFailedExt))
					if err := os.WriteFile(limboPath, contentBytes, 0o644); err != nil {
						errCh <- fmt.Errorf("write limbo-file %s: %w", spec.id, err)
						return
					}

				case "limbo-dir":
					// Card left inside queue/.provider-failed/
					pfDir := filepath.Join(queueDir, ".provider-failed")
					_ = os.MkdirAll(pfDir, 0o755)
					limboPath := filepath.Join(pfDir, fmt.Sprintf("%s.card", spec.id))
					if err := os.WriteFile(limboPath, contentBytes, 0o644); err != nil {
						errCh <- fmt.Errorf("write limbo-dir %s: %w", spec.id, err)
						return
					}

				case "rapid-cancel":
					// Simulate lock file creation during take, followed by abrupt cancel
					lockPath := filepath.Join(queueDir, fmt.Sprintf("%s.lock", spec.id))
					f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
					if err == nil {
						// Lock briefly then release/close, leaving stale lock file
						if ok, _ := tryLockFile(f); ok {
							unlockFile(f)
						}
						_ = f.Close()
					}
					// Write limbo file in queue
					limboPath := filepath.Join(queueDir, fmt.Sprintf("%s%s", spec.id, ProviderFailedExt))
					if err := os.WriteFile(limboPath, contentBytes, 0o644); err != nil {
						errCh <- fmt.Errorf("write rapid-cancel %s: %w", spec.id, err)
						return
					}
				}
			}
		}(workerID, specs[startIdx:endIdx])
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("Concurrent worker failure: %v", err)
	}
	t.Logf("All 50 failure and cancellation scenarios generated successfully.")

	// Also simulate an orphan lock in takenDir
	staleLock := filepath.Join(takenDir, "orphan-worker.lock")
	if f, err := os.OpenFile(staleLock, os.O_CREATE|os.O_RDWR, 0o644); err == nil {
		if ok, _ := tryLockFile(f); ok {
			unlockFile(f)
		}
		_ = f.Close()
	}

	// 3. Run QuarantineGC / ReconcileQueueLimbo to reconcile limbo files, quarantine cards, and clean locks
	t.Logf("Running QuarantineGC on %s...", queueDir)
	reconciled, err := QuarantineGC(queueDir, MaxProviderAttempts)
	if err != nil {
		t.Fatalf("QuarantineGC failed: %v", err)
	}
	t.Logf("QuarantineGC reconciled %d limbo files.", len(reconciled))

	// 4. VERIFICATION STEP 1: Verify all 50 failed cards transitioned to quarantine/
	quarantinedList, err := ListQuarantined(queueDir)
	if err != nil {
		t.Fatalf("ListQuarantined: %v", err)
	}

	if len(quarantinedList) != totalCards {
		t.Errorf("Quarantined card count = %d, want %d", len(quarantinedList), totalCards)
	} else {
		t.Logf("VERIFIED: Exactly %d cards transitioned to quarantine/", len(quarantinedList))
	}

	quarantinedMap := make(map[string]QuarantineRecord)
	for _, rec := range quarantinedList {
		quarantinedMap[rec.CardID] = rec
	}

	// 5. VERIFICATION STEP 2: Verify zero orphan locks anywhere in the tree
	t.Logf("Scanning for orphan lock files across %s...", scratchRoot)
	var orphanLocks []string
	err = filepath.Walk(scratchRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".lock") {
			orphanLocks = append(orphanLocks, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Walk for locks: %v", err)
	}
	if len(orphanLocks) > 0 {
		t.Errorf("FAIL: Found %d orphan lock files: %v", len(orphanLocks), orphanLocks)
	} else {
		t.Logf("VERIFIED: Zero orphan lock files found (all locks cleanly reaped/GC'd).")
	}

	// 6. VERIFICATION STEP 3: Verify zero leaked limbo slots
	t.Logf("Scanning for leaked limbo slots (*.provider-failed*) across %s...", scratchRoot)
	var leakedLimboFiles []string
	var leakedLimboDirs []string
	err = filepath.Walk(scratchRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".provider-failed" || info.Name() == "provider-failed" {
				leakedLimboDirs = append(leakedLimboDirs, path)
			}
			return nil
		}
		name := info.Name()
		if strings.HasSuffix(name, ProviderFailedExt) || strings.Contains(name, ".provider-failed.") {
			leakedLimboFiles = append(leakedLimboFiles, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Walk for limbo: %v", err)
	}
	if len(leakedLimboFiles) > 0 {
		t.Errorf("FAIL: Found %d leaked limbo files: %v", len(leakedLimboFiles), leakedLimboFiles)
	} else {
		t.Logf("VERIFIED: Zero leaked limbo files found.")
	}
	if len(leakedLimboDirs) > 0 {
		t.Errorf("FAIL: Found %d leaked limbo directories: %v", len(leakedLimboDirs), leakedLimboDirs)
	} else {
		t.Logf("VERIFIED: Zero leaked limbo directories found.")
	}

	// Also verify takenDir and queueDir have 0 orphan .card files remaining
	takenEntries, _ := os.ReadDir(takenDir)
	for _, te := range takenEntries {
		if strings.HasSuffix(te.Name(), CardExt) {
			t.Errorf("FAIL: Orphan taken card file remaining: %s", te.Name())
		}
	}
	queueCards, _ := QueueCards(benchDir)
	if len(queueCards) != 0 {
		t.Errorf("FAIL: QueueCards still holds %d cards, want 0 (all should be in quarantine): %v", len(queueCards), queueCards)
	} else {
		t.Logf("VERIFIED: Active queue and taken dirs have zero orphan cards.")
	}

	// 7. VERIFICATION STEP 4: Verify quarantine metadata files are well-formed JSON
	t.Logf("Validating quarantine metadata files (quarantine.json) for all %d cards...", totalCards)

	for _, spec := range specs {
		rec, found := quarantinedMap[spec.id]
		if !found {
			t.Errorf("Card %s missing from quarantinedMap", spec.id)
			continue
		}

		// Find actual directory on disk: queue/quarantine/<reason>/<card-id>
		cardDir := QuarantineCardDir(queueDir, rec.Reason, spec.id)
		if fi, err := os.Stat(cardDir); err != nil || !fi.IsDir() {
			t.Errorf("Expected quarantine directory at %s", cardDir)
			continue
		}

		// 4a. Verify card file exists and content matches verbatim
		cardFilePath := filepath.Join(cardDir, spec.id+CardExt)
		cardBytes, err := os.ReadFile(cardFilePath)
		if err != nil {
			t.Errorf("Missing card file %s: %v", cardFilePath, err)
		} else if string(cardBytes) != spec.content {
			t.Errorf("Card %s content mismatch: got %q, want %q", spec.id, string(cardBytes), spec.content)
		}

		// 4b. Verify quarantine.json exists and is well-formed JSON
		metaPath := filepath.Join(cardDir, QuarantineMetadataFile)
		rawJSON, err := os.ReadFile(metaPath)
		if err != nil {
			t.Errorf("Missing quarantine.json at %s: %v", metaPath, err)
			continue
		}

		var parsed QuarantineRecord
		if err := json.Unmarshal(rawJSON, &parsed); err != nil {
			t.Errorf("FAIL: quarantine.json for %s is NOT well-formed JSON: %v\nRaw:\n%s", spec.id, err, string(rawJSON))
			continue
		}

		// 4c. Verify metadata fields
		if parsed.CardID != spec.id {
			t.Errorf("Card %s: meta.CardID = %q, want %q", spec.id, parsed.CardID, spec.id)
		}
		if parsed.Reason == "" {
			t.Errorf("Card %s: meta.Reason is empty", spec.id)
		}
		if parsed.FailureKind == "" {
			t.Errorf("Card %s: meta.FailureKind is empty", spec.id)
		}
		if parsed.Attempts <= 0 {
			t.Errorf("Card %s: meta.Attempts = %d, want > 0", spec.id, parsed.Attempts)
		}
		if parsed.QuarantinedAt == "" {
			t.Errorf("Card %s: meta.QuarantinedAt is empty", spec.id)
		} else {
			if _, err := time.Parse(time.RFC3339, parsed.QuarantinedAt); err != nil {
				t.Errorf("Card %s: meta.QuarantinedAt invalid RFC3339 %q: %v", spec.id, parsed.QuarantinedAt, err)
			}
		}
	}
	t.Logf("VERIFIED: All 50 quarantine metadata files are valid, well-formed JSON with correct taxonomy fields.")

	// 8. VERIFICATION STEP 5: Triage summary & ReleaseQuarantined validation
	summary := TriageQuarantineSummary(queueDir)
	t.Logf("Triage Summary: %s", summary)
	if !strings.Contains(summary, "total=50") {
		t.Errorf("TriageQuarantineSummary total != 50: %s", summary)
	}

	// Test release of card-01 back to queue
	rec01 := quarantinedMap["card-01"]
	restoredPath, err := ReleaseQuarantined(queueDir, rec01.Reason, "card-01")
	if err != nil {
		t.Fatalf("ReleaseQuarantined card-01: %v", err)
	}
	if _, err := os.Stat(restoredPath); err != nil {
		t.Errorf("Restored card-01 not found at %s: %v", restoredPath, err)
	}
	// Verify quarantine dir was cleaned up
	oldDir := QuarantineCardDir(queueDir, rec01.Reason, "card-01")
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("Quarantine dir for card-01 should have been removed: %s", oldDir)
	}
	t.Logf("VERIFIED: ReleaseQuarantined restored card-01 to queue and removed quarantine artifacts.")

	t.Logf("=== Stress Test Complete: ALL 50 CARDS VERIFIED CLEAN ===")
}

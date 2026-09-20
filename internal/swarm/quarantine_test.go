package swarm

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFailureTaxonomy validates the 4-way classification of Sprint Row 10 (#2061):
// transient provider errors, infrastructure crashes, test failures, and unrecoverable card defects.
func TestFailureTaxonomy(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		output        string
		rc            int
		end           string
		wantKind      FailureKind
		wantReason    string
		wantRetriable bool
		wantQuarantine bool
	}{
		// 1. Transient Provider Errors
		{
			name:          "provider 429 rate limit in rc",
			output:        "HTTP 429: Too Many Requests",
			rc:            429,
			wantKind:      FailureTransientProvider,
			wantReason:    "rate-limit",
			wantRetriable: true,
			wantQuarantine: false,
		},
		{
			name:          "provider rate limit in log text",
			output:        "error: Rate limit reached for tokens per minute (TPM)",
			rc:            1,
			wantKind:      FailureTransientProvider,
			wantReason:    "rate-limit",
			wantRetriable: true,
			wantQuarantine: false,
		},
		{
			name:          "provider 503 service unavailable",
			output:        "HTTP 503 Service Unavailable: upstream overloaded",
			end:           EndProvider,
			wantKind:      FailureTransientProvider,
			wantReason:    "provider-5xx",
			wantRetriable: true,
			wantQuarantine: false,
		},
		{
			name:          "provider 500 internal server error",
			output:        "status 500: unexpected server error err_deadbeef",
			wantKind:      FailureTransientProvider,
			wantReason:    "provider-5xx",
			wantRetriable: true,
			wantQuarantine: false,
		},
		{
			name:          "provider connection reset network error",
			err:           errors.New("post https://api.deepseek.com/v1: connection reset by peer"),
			wantKind:      FailureTransientProvider,
			wantReason:    "provider-network",
			wantRetriable: true,
			wantQuarantine: false,
		},

		// 2. Infrastructure Crashes
		{
			name:          "out of memory killed exit 137",
			output:        "Killed (process 12345)",
			rc:            137,
			wantKind:      FailureInfraCrash,
			wantReason:    "oom-killed",
			wantRetriable: true,
			wantQuarantine: true,
		},
		{
			name:          "runner segfault exit 139",
			output:        "fatal error: segmentation fault",
			rc:            139,
			wantKind:      FailureInfraCrash,
			wantReason:    "segfault",
			wantRetriable: true,
			wantQuarantine: true,
		},
		{
			name:          "bench host unreachable",
			output:        "ssh: connect to host superman port 22: connection timed out; bench unreachable",
			wantKind:      FailureInfraCrash,
			wantReason:    "bench-unreachable",
			wantRetriable: true,
			wantQuarantine: false,
		},
		{
			name:          "bench disk full",
			output:        "write /var/tmp/slot-1/repo: no space left on device",
			wantKind:      FailureInfraCrash,
			wantReason:    "disk-full",
			wantRetriable: false,
			wantQuarantine: true,
		},
		{
			name:          "darwin exit 255 without verdict",
			output:        "process exited with status 255",
			rc:            255,
			wantKind:      FailureInfraCrash,
			wantReason:    "darwin-exit-255",
			wantRetriable: true,
			wantQuarantine: false,
		},

		// 3. Test Failures
		{
			name:          "go test failure",
			output:        "=== RUN   TestSomething\n    foo_test.go:42: assertion failed: got 1 want 2\n--- FAIL: TestSomething (0.01s)",
			rc:            1,
			end:           EndFailed,
			wantKind:      FailureTestFailure,
			wantReason:    "test-failed",
			wantRetriable: false,
			wantQuarantine: false,
		},
		{
			name:          "generic test suite failure",
			output:        "pytest: failed 3 of 15 tests in 2.3s",
			rc:            1,
			wantKind:      FailureTestFailure,
			wantReason:    "test-failed",
			wantRetriable: false,
			wantQuarantine: false,
		},

		// 4. Unrecoverable Card Defects
		{
			name:          "prompt exceeds context limit input-limit end",
			output:        "Rate limit reached: input token limit exceeded (204800 > 131072)",
			end:           EndInputLimit,
			wantKind:      FailureCardDefect,
			wantReason:    "input-limit",
			wantRetriable: false,
			wantQuarantine: true,
		},
		{
			name:          "prompt exceeds context window in log",
			output:        "error: maximum context length is 65536 tokens, but request contains 92000 tokens",
			rc:            1,
			wantKind:      FailureCardDefect,
			wantReason:    "input-limit",
			wantRetriable: false,
			wantQuarantine: true,
		},
		{
			name:          "malformed card syntax in class",
			output:        "cannot parse frontmatter: unexpected EOF",
			end:           ClassMalformed,
			wantKind:      FailureCardDefect,
			wantReason:    "malformed-syntax",
			wantRetriable: false,
			wantQuarantine: true,
		},
		{
			name:          "malformed card in log text",
			output:        "nova-swarm: malformed card: missing required field :kind",
			rc:            2,
			wantKind:      FailureCardDefect,
			wantReason:    "malformed-syntax",
			wantRetriable: false,
			wantQuarantine: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyFailure(tc.err, tc.output, tc.rc, tc.end)
			if got.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", got.Kind, tc.wantKind)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tc.wantReason)
			}
			if got.Retriable != tc.wantRetriable {
				t.Errorf("Retriable = %v, want %v", got.Retriable, tc.wantRetriable)
			}
			if got.Quarantinable != tc.wantQuarantine {
				t.Errorf("Quarantinable = %v, want %v", got.Quarantinable, tc.wantQuarantine)
			}
			if got.Details == "" {
				t.Errorf("Details is empty")
			}
		})
	}
}

// TestQuarantineDirectoryStructure verifies the exact directory layout:
// queue/quarantine/<reason>/<card-id>/
// holding <card-id>.card and quarantine.json.
func TestQuarantineDirectoryStructure(t *testing.T) {
	bench := t.TempDir()
	queue := filepath.Join(bench, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatalf("mkdir queue: %v", err)
	}

	cardID := "card-test-defect-01"
	cardContent := []byte(":kind test\n:repo mas-bandwidth/nova-tools\n:effort 5\n\nFix input limit defect.")
	meta := QuarantineRecord{
		CardID:      cardID,
		Reason:      "input-limit",
		FailureKind: FailureCardDefect,
		Attempts:    1,
		Details:     "prompt size 150000 exceeds model context 128000",
		Worker:      "worker-1",
	}

	targetDir, err := QuarantineCard(queue, "input-limit", cardID, cardContent, meta)
	if err != nil {
		t.Fatalf("QuarantineCard: %v", err)
	}

	// 1. Verify exact path structure: queue/quarantine/<reason>/<card-id>
	expectedDir := filepath.Join(queue, "quarantine", "input-limit", cardID)
	if targetDir != expectedDir {
		t.Errorf("targetDir = %q, want %q", targetDir, expectedDir)
	}
	if fi, err := os.Stat(targetDir); err != nil || !fi.IsDir() {
		t.Fatalf("expected directory at %s", targetDir)
	}

	// 2. Verify <card-id>.card exists and has verbatim content
	cardPath := filepath.Join(targetDir, cardID+".card")
	gotContent, err := os.ReadFile(cardPath)
	if err != nil {
		t.Fatalf("read quarantined card file: %v", err)
	}
	if string(gotContent) != string(cardContent) {
		t.Errorf("card content mismatch: got %q, want %q", string(gotContent), string(cardContent))
	}

	// 3. Verify quarantine.json exists and contains correct metadata
	metaPath := filepath.Join(targetDir, "quarantine.json")
	metaRaw, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read quarantine.json: %v", err)
	}
	var readMeta QuarantineRecord
	if err := json.Unmarshal(metaRaw, &readMeta); err != nil {
		t.Fatalf("unmarshal quarantine.json: %v", err)
	}
	if readMeta.CardID != cardID {
		t.Errorf("meta.CardID = %q, want %q", readMeta.CardID, cardID)
	}
	if readMeta.Reason != "input-limit" {
		t.Errorf("meta.Reason = %q, want input-limit", readMeta.Reason)
	}
	if readMeta.FailureKind != FailureCardDefect {
		t.Errorf("meta.FailureKind = %q, want %q", readMeta.FailureKind, FailureCardDefect)
	}
	if readMeta.QuarantinedAt == "" {
		t.Errorf("meta.QuarantinedAt is empty")
	}

	// 4. Verify ListQuarantined finds this card
	records, err := ListQuarantined(queue)
	if err != nil {
		t.Fatalf("ListQuarantined: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("ListQuarantined returned %d records, want 1", len(records))
	}
	if records[0].CardID != cardID || records[0].Reason != "input-limit" {
		t.Errorf("record mismatch: %+v", records[0])
	}

	// 5. Test ReleaseQuarantined restores the card back to queue/
	restoredPath, err := ReleaseQuarantined(queue, "input-limit", cardID)
	if err != nil {
		t.Fatalf("ReleaseQuarantined: %v", err)
	}
	expectedRestored := filepath.Join(queue, cardID+".card")
	if restoredPath != expectedRestored {
		t.Errorf("restoredPath = %q, want %q", restoredPath, expectedRestored)
	}
	if _, err := os.Stat(expectedRestored); err != nil {
		t.Errorf("expected restored card at %s", expectedRestored)
	}
	// Quarantine directory should be cleaned up
	if _, err := os.Stat(targetDir); !os.IsNotExist(err) {
		t.Errorf("quarantine dir %s should have been removed", targetDir)
	}
}

// TestFixLimboLeakProviderFailed reproduces and verifies the fix for the leak
// where .provider-failed left cards in limbo.
func TestFixLimboLeakProviderFailed(t *testing.T) {
	bench := t.TempDir()
	queue := filepath.Join(bench, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatalf("mkdir queue: %v", err)
	}

	// Step 1: Simulate the limbo state where a previous failure wrote .provider-failed
	// Card A: attempt 1 of 3 (within budget -> should be recovered to queue)
	cardAContent := []byte(":kind test\n:attempts 1\ncard A text\n")
	limboA := filepath.Join(queue, "card-a.provider-failed")
	if err := os.WriteFile(limboA, cardAContent, 0o644); err != nil {
		t.Fatalf("write limbo card A: %v", err)
	}

	// Card B: attempt 3 of 3 (exhausted -> should be quarantined)
	cardBContent := []byte(":kind test\n:attempts 3\ncard B text\n")
	limboB := filepath.Join(queue, "card-b.card.provider-failed")
	if err := os.WriteFile(limboB, cardBContent, 0o644); err != nil {
		t.Fatalf("write limbo card B: %v", err)
	}

	// Prior to reconciliation, QueueCards() walks only *.card, so both cards are in limbo!
	cardsBefore, err := QueueCards(bench)
	if err != nil {
		t.Fatalf("QueueCards: %v", err)
	}
	// Because QueueCards calls ReconcileQueueLimbo automatically, let's verify that
	// QueueCards ALREADY rescued card-a and quarantined card-b!
	// Card A was recovered to queue/card-a.card
	// Card B was quarantined under queue/quarantine/provider-exhausted/card-b/
	if len(cardsBefore) != 1 || cardsBefore[0] != "card-a" {
		t.Errorf("QueueCards after auto-reconcile = %v, want [card-a]", cardsBefore)
	}

	// Verify limbo files are GONE (no leak in limbo)
	if _, err := os.Stat(limboA); !os.IsNotExist(err) {
		t.Errorf("limbo file %s still exists", limboA)
	}
	if _, err := os.Stat(limboB); !os.IsNotExist(err) {
		t.Errorf("limbo file %s still exists", limboB)
	}

	// Verify Card B is in quarantine under queue/quarantine/provider-exhausted/card-b/
	qDirB := filepath.Join(queue, "quarantine", "provider-exhausted", "card-b")
	if fi, err := os.Stat(qDirB); err != nil || !fi.IsDir() {
		t.Errorf("expected Card B quarantined at %s", qDirB)
	}
	cardBPath := filepath.Join(qDirB, "card-b.card")
	if _, err := os.Stat(cardBPath); err != nil {
		t.Errorf("expected quarantined card file at %s", cardBPath)
	}

	// Test explicit ReconcileQueueLimbo on an additional .provider-failed directory
	providerFailedDir := filepath.Join(queue, ".provider-failed")
	if err := os.MkdirAll(providerFailedDir, 0o755); err != nil {
		t.Fatalf("mkdir .provider-failed: %v", err)
	}
	cardCContent := []byte(":kind test\n:attempts 1\ncard C in dotdir\n")
	limboC := filepath.Join(providerFailedDir, "card-c.card")
	if err := os.WriteFile(limboC, cardCContent, 0o644); err != nil {
		t.Fatalf("write limbo card C: %v", err)
	}

	reconciled, err := ReconcileQueueLimbo(queue, 3)
	if err != nil {
		t.Fatalf("ReconcileQueueLimbo: %v", err)
	}
	if len(reconciled) != 1 {
		t.Fatalf("ReconcileQueueLimbo returned %d items, want 1", len(reconciled))
	}
	if reconciled[0].CardID != "card-c" || reconciled[0].Action != ActionRetry {
		t.Errorf("reconciled record mismatch: %+v", reconciled[0])
	}
	// Card C should now be in queue/card-c.card
	if _, err := os.Stat(filepath.Join(queue, "card-c.card")); err != nil {
		t.Errorf("card-c was not restored to queue: %v", err)
	}
}

// TestTriageRouting verifies routing according to the failure taxonomy and
// formatting of quarantine summaries in triage.
func TestTriageRouting(t *testing.T) {
	queue := t.TempDir()

	cardContent := []byte(":kind test\ncard content\n")

	// 1. Card defect -> ActionQuarantine
	fcDefect := FailureClassification{
		Kind:          FailureCardDefect,
		Reason:        "input-limit",
		Retriable:     false,
		Quarantinable: true,
		Details:       "prompt exceeded 128k context window",
	}
	resDefect, err := RouteFailure(queue, "defect-card", cardContent, 1, 3, fcDefect)
	if err != nil {
		t.Fatalf("RouteFailure defect: %v", err)
	}
	if resDefect.Action != ActionQuarantine {
		t.Errorf("resDefect.Action = %q, want %q", resDefect.Action, ActionQuarantine)
	}
	if resDefect.Reason != "input-limit" {
		t.Errorf("resDefect.Reason = %q, want input-limit", resDefect.Reason)
	}
	if _, err := os.Stat(filepath.Join(queue, "quarantine", "input-limit", "defect-card")); err != nil {
		t.Errorf("defect card directory not created: %v", err)
	}

	// 2. Transient provider error, attempt 1 of 3 -> ActionRetry (do NOT quarantine)
	fcProvider := FailureClassification{
		Kind:          FailureTransientProvider,
		Reason:        "rate-limit",
		Retriable:     true,
		Quarantinable: false,
		Details:       "HTTP 429",
	}
	resProv1, err := RouteFailure(queue, "prov-card", cardContent, 1, 3, fcProvider)
	if err != nil {
		t.Fatalf("RouteFailure provider 1: %v", err)
	}
	if resProv1.Action != ActionRetry {
		t.Errorf("resProv1.Action = %q, want %q", resProv1.Action, ActionRetry)
	}

	// 3. Transient provider error, attempt 3 of 3 -> ActionQuarantine (exhausted)
	resProv3, err := RouteFailure(queue, "prov-card", cardContent, 3, 3, fcProvider)
	if err != nil {
		t.Fatalf("RouteFailure provider 3: %v", err)
	}
	if resProv3.Action != ActionQuarantine {
		t.Errorf("resProv3.Action = %q, want %q", resProv3.Action, ActionQuarantine)
	}
	if resProv3.Reason != "provider-exhausted" {
		t.Errorf("resProv3.Reason = %q, want provider-exhausted", resProv3.Reason)
	}
	if _, err := os.Stat(filepath.Join(queue, "quarantine", "provider-exhausted", "prov-card")); err != nil {
		t.Errorf("exhausted provider card directory not created: %v", err)
	}

	// 4. Test failure -> ActionFail (no quarantine)
	fcTest := FailureClassification{
		Kind:          FailureTestFailure,
		Reason:        "test-failed",
		Retriable:     false,
		Quarantinable: false,
		Details:       "exit code 1",
	}
	resTest, err := RouteFailure(queue, "test-fail-card", cardContent, 1, 3, fcTest)
	if err != nil {
		t.Fatalf("RouteFailure test: %v", err)
	}
	if resTest.Action != ActionFail {
		t.Errorf("resTest.Action = %q, want %q", resTest.Action, ActionFail)
	}

	// 5. Test TriageQuarantineSummary formatting
	summary := TriageQuarantineSummary(queue)
	if !strings.HasPrefix(summary, "TRIAGE QUARANTINE total=2") {
		t.Errorf("summary mismatch: %q", summary)
	}
	if !strings.Contains(summary, "input_limit=1") || !strings.Contains(summary, "provider_exhausted=1") {
		t.Errorf("summary counts mismatch: %q", summary)
	}
}

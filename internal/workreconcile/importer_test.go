package workreconcile

import (
	"context"
	"fmt"
	"testing"
)

func generateTestManifest(repo string, count int) *CaptureManifest {
	issues := make([]CapturedIssue, count)
	for i := 0; i < count; i++ {
		num := i + 1
		issues[i] = CapturedIssue{
			Provider:     "github",
			Repo:         repo,
			Number:       num,
			NodeID:       fmt.Sprintf("node_%d", num),
			Title:        fmt.Sprintf("Issue #%d", num),
			Body:         fmt.Sprintf("Body of issue %d", num),
			State:        "open",
			Author:       "author",
			Revision:     fmt.Sprintf("rev-%d", num),
			CommentCount: num % 5,
			Labels:       []string{fmt.Sprintf("label-%d", num%3)},
			URL:          fmt.Sprintf("https://github.com/%s/issues/%d", repo, num),
		}
	}
	return &CaptureManifest{
		Provider:    "github",
		Repo:        repo,
		FetchedAt:   "2026-09-20T12:00:00Z",
		TotalIssues: count,
		Issues:      issues,
	}
}

func TestInterruptionAndResumptionZeroDuplicates(t *testing.T) {
	ctx := context.Background()
	repo := "mas-bandwidth/nova-tools"
	totalCount := 80
	batchSize := 20 // 4 batches of 20
	manifest := generateTestManifest(repo, totalCount)

	store := NewStore()
	importer := NewBatchImporter(batchSize)

	// Step 1: Set up interrupt hook to interrupt on batch 2 (after 40 issues)
	interruptTriggered := false
	importer.InterruptHook = func(batchNum int, lastApplied int) error {
		if batchNum == 2 {
			interruptTriggered = true
			return fmt.Errorf("simulated crash during batch 2 after issue #%d", lastApplied)
		}
		return nil
	}

	summary1, err := importer.Import(ctx, manifest, store)
	if err != ErrInterrupted {
		t.Fatalf("expected ErrInterrupted, got %v", err)
	}
	if !interruptTriggered {
		t.Fatal("interrupt hook was not triggered")
	}

	// At this interruption point, exactly 40 nodes should be in the store
	nodesAfterInterrupt := store.TotalNodeCount()
	if nodesAfterInterrupt != 40 {
		t.Fatalf("expected 40 nodes after interruption, got %d", nodesAfterInterrupt)
	}
	if summary1.NewNodes != 40 {
		t.Fatalf("expected summary1 to report 40 new nodes, got %d", summary1.NewNodes)
	}

	// Checkpoint should indicate LastAppliedNumber = 40, BatchNumber = 2
	cp, ok := store.GetCheckpoint(repo)
	if !ok {
		t.Fatal("expected checkpoint to be present after interruption")
	}
	if cp.LastAppliedNumber != 40 || cp.BatchNumber != 2 {
		t.Fatalf("unexpected checkpoint: %+v", cp)
	}

	// Step 2: Resume import without interrupt hook
	importer.InterruptHook = nil
	summary2, err := importer.Import(ctx, manifest, store)
	if err != nil {
		t.Fatalf("resumption failed: %v", err)
	}

	if !summary2.Completed {
		t.Fatal("expected summary2 to be completed")
	}

	// Total nodes must now be EXACTLY 80
	nodesAfterResume := store.TotalNodeCount()
	if nodesAfterResume != totalCount {
		t.Fatalf("expected %d nodes after resumption, got %d", totalCount, nodesAfterResume)
	}

	// summary2 should report 40 new nodes (for issues 41..80) and 0 duplicates
	if summary2.NewNodes != 40 {
		t.Fatalf("expected 40 new nodes in resumption pass, got %d", summary2.NewNodes)
	}

	// ZERO DUPLICATES PROOF:
	// Verify that every issue from 1 to 80 has EXACTLY ONE node in the store
	seenUIDs := make(map[string]bool)
	for i := 1; i <= totalCount; i++ {
		node, found := store.GetNodeByIssue(repo, i)
		if !found {
			t.Fatalf("issue #%d missing after resumption", i)
		}
		if seenUIDs[node.UID] {
			t.Fatalf("duplicate node UID %s found for issue #%d", node.UID, i)
		}
		seenUIDs[node.UID] = true
	}

	// Step 3: Run import AGAIN to prove complete idempotency
	summary3, err := importer.Import(ctx, manifest, store)
	if err != nil {
		t.Fatalf("re-import failed: %v", err)
	}

	if summary3.NewNodes != 0 {
		t.Fatalf("expected 0 new nodes on repeated import, got %d", summary3.NewNodes)
	}
	if summary3.DeduplicatedNodes != totalCount {
		t.Fatalf("expected %d deduplicated nodes on repeated import, got %d", totalCount, summary3.DeduplicatedNodes)
	}
	if store.TotalNodeCount() != totalCount {
		t.Fatalf("store total node count grew from %d to %d on repeated import", totalCount, store.TotalNodeCount())
	}
}

func TestSourceMovementUpdate(t *testing.T) {
	ctx := context.Background()
	repo := "mas-bandwidth/nova-tools"
	manifest := generateTestManifest(repo, 10)

	store := NewStore()
	importer := NewBatchImporter(5)

	// Initial import
	if _, err := importer.Import(ctx, manifest, store); err != nil {
		t.Fatalf("initial import failed: %v", err)
	}

	// Capture initial node UIDs
	originalUIDs := make(map[int]string)
	for i := 1; i <= 10; i++ {
		node, _ := store.GetNodeByIssue(repo, i)
		originalUIDs[i] = node.UID
	}

	// Move source: update revision and comment count on issue #3 and #7
	manifest2 := generateTestManifest(repo, 10)
	manifest2.Issues[2].Revision = "rev-3-updated"
	manifest2.Issues[2].CommentCount = 99
	manifest2.Issues[6].Revision = "rev-7-updated"
	manifest2.Issues[6].Labels = []string{"urgent", "fixed"}

	// Clear checkpoint to simulate full re-capture re-import
	store.SaveCheckpoint(&Checkpoint{Repo: repo, LastAppliedNumber: 0, BatchNumber: 0})

	summary, err := importer.Import(ctx, manifest2, store)
	if err != nil {
		t.Fatalf("re-import with moved source failed: %v", err)
	}

	if summary.NewNodes != 0 {
		t.Errorf("expected 0 new nodes, got %d", summary.NewNodes)
	}
	if summary.UpdatedNodes != 2 {
		t.Errorf("expected 2 updated nodes, got %d", summary.UpdatedNodes)
	}
	if summary.DeduplicatedNodes != 8 {
		t.Errorf("expected 8 deduplicated nodes, got %d", summary.DeduplicatedNodes)
	}

	// Verify that UIDs never changed (stable identity)
	for i := 1; i <= 10; i++ {
		node, _ := store.GetNodeByIssue(repo, i)
		if node.UID != originalUIDs[i] {
			t.Fatalf("node UID changed for issue #%d: was %s, now %s", i, originalUIDs[i], node.UID)
		}
	}

	// Verify updated fields
	n3, _ := store.GetNodeByIssue(repo, 3)
	if n3.Revision != "rev-3-updated" || n3.CommentCount != 99 {
		t.Fatalf("node #3 fields not updated: %+v", n3)
	}
	n7, _ := store.GetNodeByIssue(repo, 7)
	if n7.Revision != "rev-7-updated" || len(n7.Labels) != 2 {
		t.Fatalf("node #7 fields not updated: %+v", n7)
	}
}

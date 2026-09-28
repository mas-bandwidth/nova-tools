package workreconcile

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// ErrInterrupted is returned when an import operation is deliberately interrupted at a checkpoint boundary.
var ErrInterrupted = errors.New("import operation interrupted at batch checkpoint")

// ImportSummary provides metrics for an import run.
type ImportSummary struct {
	Repo              string `json:"repo"`
	TotalCaptured     int    `json:"total_captured"`
	NewNodes          int    `json:"new_nodes"`
	UpdatedNodes      int    `json:"updated_nodes"`
	DeduplicatedNodes int    `json:"deduplicated_nodes"`
	TotalBatches      int    `json:"total_batches"`
	Completed         bool   `json:"completed"`
}

// BatchImporter imports captured issues in bounded batches with durable checkpoints.
// It supports clean interruption and resumption with zero duplicate nodes (E09-F02, E09-F03).
type BatchImporter struct {
	BatchSize     int
	InterruptHook func(batchNum int, lastAppliedNumber int) error
}

// NewBatchImporter creates a batch importer with the specified batch size.
func NewBatchImporter(batchSize int) *BatchImporter {
	if batchSize <= 0 {
		batchSize = 50
	}
	return &BatchImporter{BatchSize: batchSize}
}

// Import executes the batched import of manifest issues into the store.
// If an existing checkpoint is found, it resumes from the issue immediately following
// the checkpoint's LastAppliedNumber.
func (bi *BatchImporter) Import(ctx context.Context, manifest *CaptureManifest, store *Store) (*ImportSummary, error) {
	if manifest == nil {
		return nil, errors.New("import requires a non-nil manifest")
	}
	if store == nil {
		return nil, errors.New("import requires a non-nil store")
	}

	repo := manifest.Repo
	// Sort issues deterministically by issue number
	sortedIssues := make([]CapturedIssue, len(manifest.Issues))
	copy(sortedIssues, manifest.Issues)
	sort.Slice(sortedIssues, func(i, j int) bool {
		return sortedIssues[i].Number < sortedIssues[j].Number
	})

	// Check for existing checkpoint
	cp, hasCheckpoint := store.GetCheckpoint(repo)
	startIndex := 0
	batchNum := 0

	if hasCheckpoint && cp.LastAppliedNumber > 0 {
		batchNum = cp.BatchNumber
		if len(sortedIssues) > 0 && cp.LastAppliedNumber >= sortedIssues[len(sortedIssues)-1].Number {
			// Previous import completed the entire manifest.
			// This invocation is a full re-import / update check across all issues.
			startIndex = 0
		} else {
			// Previous import was interrupted mid-way; resume from the next unimported issue.
			startIndex = len(sortedIssues)
			for i, issue := range sortedIssues {
				if issue.Number > cp.LastAppliedNumber {
					startIndex = i
					break
				}
			}
		}
	}

	summary := &ImportSummary{
		Repo:          repo,
		TotalCaptured: len(manifest.Issues),
	}

	if startIndex >= len(sortedIssues) {
		summary.Completed = true
		return summary, nil
	}

	remaining := sortedIssues[startIndex:]
	batchSize := bi.BatchSize
	if batchSize <= 0 {
		batchSize = 50
	}

	for i := 0; i < len(remaining); i += batchSize {
		select {
		case <-ctx.Done():
			return summary, ctx.Err()
		default:
		}

		end := i + batchSize
		if end > len(remaining) {
			end = len(remaining)
		}
		batch := remaining[i:end]
		batchNum++

		for _, issue := range batch {
			existing, found := store.GetNodeByIssue(repo, issue.Number)
			if found {
				// Issue already in store: check for source movement or duplicate
				if existing.Revision == issue.Revision &&
					existing.CommentCount == issue.CommentCount &&
					slicesEqual(existing.NormalizedLabels(), issue.NormalizedLabels()) &&
					existing.State == issue.State {
					// Identical revision and state: idempotent no-op, deduplicate
					summary.DeduplicatedNodes++
				} else {
					// Source moved or updated: update existing node, append evidence, never duplicate
					existing.Title = issue.Title
					existing.State = issue.State
					existing.Revision = issue.Revision
					existing.CommentCount = issue.CommentCount
					existing.Labels = append([]string(nil), issue.Labels...)
					existing.URL = issue.URL
					existing.Evidence = append(existing.Evidence,
						fmt.Sprintf("re-import:source-update revision=%s stamp=%s",
							issue.Revision, time.Now().UTC().Format(time.RFC3339)))
					if err := store.UpdateNode(existing); err != nil {
						return summary, fmt.Errorf("updating node %s for issue #%d: %w", existing.UID, issue.Number, err)
					}
					summary.UpdatedNodes++
				}
			} else {
				// New issue: mint generic 128-bit UID
				uid, err := MintUID()
				if err != nil {
					return summary, fmt.Errorf("minting uid for issue #%d: %w", issue.Number, err)
				}

				nodeState := issue.State
				if nodeState == "" {
					nodeState = "open"
				}

				node := &WorkNode{
					UID:          uid,
					ID:           fmt.Sprintf("%s/issues/%d", repo, issue.Number),
					Provider:     issue.Provider,
					Repo:         repo,
					IssueNumber:  issue.Number,
					Title:        issue.Title,
					State:        nodeState,
					Revision:     issue.Revision,
					CommentCount: issue.CommentCount,
					Labels:       append([]string(nil), issue.Labels...),
					URL:          issue.URL,
					ImportedAt:   time.Now().UTC().Format(time.RFC3339),
					Evidence: []string{
						fmt.Sprintf("intake:captured-revision=%s", issue.Revision),
					},
				}

				if err := store.AddNode(node); err != nil {
					return summary, fmt.Errorf("adding node for issue #%d: %w", issue.Number, err)
				}
				summary.NewNodes++
			}
		}

		lastNumInBatch := batch[len(batch)-1].Number
		summary.TotalBatches++

		// Check interrupt hook BEFORE persisting this batch's checkpoint
		// to test realistic crash before checkpoint save, or after batch processing
		if bi.InterruptHook != nil {
			if err := bi.InterruptHook(batchNum, lastNumInBatch); err != nil {
				// Save checkpoint for what completed, then interrupt
				store.SaveCheckpoint(&Checkpoint{
					Repo:              repo,
					LastAppliedNumber: lastNumInBatch,
					BatchNumber:       batchNum,
					ImportedCount:     summary.NewNodes + summary.UpdatedNodes + summary.DeduplicatedNodes,
					UpdatedAt:         time.Now().UTC().Format(time.RFC3339),
				})
				return summary, ErrInterrupted
			}
		}

		// Save checkpoint for successfully processed batch
		store.SaveCheckpoint(&Checkpoint{
			Repo:              repo,
			LastAppliedNumber: lastNumInBatch,
			BatchNumber:       batchNum,
			ImportedCount:     summary.NewNodes + summary.UpdatedNodes + summary.DeduplicatedNodes,
			UpdatedAt:         time.Now().UTC().Format(time.RFC3339),
		})
	}

	summary.Completed = true
	return summary, nil
}

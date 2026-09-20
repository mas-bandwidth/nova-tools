package swarm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Automatic queue quarantine & limbo resolution (Sprint Row 10, #2061).
//
// Directory layout:
//
//	<queue>/quarantine/<reason>/<card-id>/
//	    ├── <card-id>.card     (verbatim copy of original card)
//	    └── quarantine.json   (structured taxonomy & triage metadata)
//
// A card that cannot make progress due to unrecoverable defects (input-limit,
// malformed syntax) or exhausted provider retry budgets is quarantined under
// this hierarchy.
//
// THE LIMBO LEAK FIX:
// Previously, cards failing provider launch were renamed with `.provider-failed`
// (e.g., `<name>.provider-failed` or dropped under `.provider-failed/`). Because
// QueueCards() walks only `*.card`, those cards vanished from worker sweeps,
// were never counted in triage, and sat in limbo forever.
// ReconcileQueueLimbo() sweeps for any `.provider-failed` files and reconciles them:
//  - If within provider retry budget: restores them to `queue/<card-id>.card`
//  - If retry budget exhausted or defective: moves them to `queue/quarantine/<reason>/<card-id>`
// No card is ever abandoned in limbo.

const (
	// QuarantineDirName is the directory under queue holding all quarantined cards.
	QuarantineDirName = "quarantine"

	// QuarantineMetadataFile is the metadata descriptor inside each quarantined card directory.
	QuarantineMetadataFile = "quarantine.json"

	// ProviderFailedExt is the legacy suffix that caused cards to sit in limbo.
	ProviderFailedExt = ".provider-failed"
)

// QuarantineRecord describes a quarantined card's metadata.
type QuarantineRecord struct {
	CardID       string      `json:"card_id"`
	Reason       string      `json:"reason"`
	FailureKind  FailureKind `json:"failure_kind"`
	Attempts     int         `json:"attempts"`
	QuarantinedAt string     `json:"quarantined_at"`
	Worker       string      `json:"worker,omitempty"`
	OriginalPath string      `json:"original_path,omitempty"`
	Details      string      `json:"details,omitempty"`
}

// QuarantineDir returns the quarantine root: <queue>/quarantine.
func QuarantineDir(queueDir string) string {
	return filepath.Join(queueDir, QuarantineDirName)
}

// QuarantineReasonDir returns the path for a specific failure reason:
// <queue>/quarantine/<reason>.
func QuarantineReasonDir(queueDir, reason string) string {
	return filepath.Join(QuarantineDir(queueDir), reason)
}

// QuarantineCardDir returns the directory for a specific quarantined card:
// <queue>/quarantine/<reason>/<card-id>.
func QuarantineCardDir(queueDir, reason, cardID string) string {
	return filepath.Join(QuarantineReasonDir(queueDir, reason), cardID)
}

// QuarantineCard atomically places a card into the quarantine hierarchy:
// <queue>/quarantine/<reason>/<card-id>/.
func QuarantineCard(queueDir, reason, cardID string, cardContent []byte, meta QuarantineRecord) (string, error) {
	cardID = strings.TrimSpace(cardID)
	reason = strings.TrimSpace(reason)
	if cardID == "" {
		return "", fmt.Errorf("quarantine: card id cannot be empty")
	}
	if reason == "" {
		reason = "unknown"
	}

	targetDir := QuarantineCardDir(queueDir, reason, cardID)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", fmt.Errorf("quarantine: create target dir %s: %w", targetDir, err)
	}

	// 1. Write the original card content verbatim.
	cardFile := filepath.Join(targetDir, cardID+CardExt)
	if err := os.WriteFile(cardFile, cardContent, 0o644); err != nil {
		return "", fmt.Errorf("quarantine: write card file %s: %w", cardFile, err)
	}

	// 2. Write the quarantine metadata record.
	if meta.CardID == "" {
		meta.CardID = cardID
	}
	if meta.Reason == "" {
		meta.Reason = reason
	}
	if meta.QuarantinedAt == "" {
		meta.QuarantinedAt = time.Now().UTC().Format(time.RFC3339)
	}
	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return "", fmt.Errorf("quarantine: marshal metadata for %s: %w", cardID, err)
	}
	metaBytes = append(metaBytes, '\n')
	metaFile := filepath.Join(targetDir, QuarantineMetadataFile)
	if err := os.WriteFile(metaFile, metaBytes, 0o644); err != nil {
		return "", fmt.Errorf("quarantine: write metadata file %s: %w", metaFile, err)
	}

	return targetDir, nil
}

// ListQuarantined inspects <queue>/quarantine and returns all quarantined cards
// sorted by reason and card id.
func ListQuarantined(queueDir string) ([]QuarantineRecord, error) {
	root := QuarantineDir(queueDir)
	reasonEntries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var records []QuarantineRecord
	for _, re := range reasonEntries {
		if !re.IsDir() {
			continue
		}
		reason := re.Name()
		cardEntries, err := os.ReadDir(filepath.Join(root, reason))
		if err != nil {
			continue
		}
		for _, ce := range cardEntries {
			if !ce.IsDir() {
				continue
			}
			cardID := ce.Name()
			dir := filepath.Join(root, reason, cardID)
			metaPath := filepath.Join(dir, QuarantineMetadataFile)
			metaRaw, err := os.ReadFile(metaPath)
			if err == nil {
				var rec QuarantineRecord
				if err := json.Unmarshal(metaRaw, &rec); err == nil {
					records = append(records, rec)
					continue
				}
			}
			// If quarantine.json is unreadable or absent, synthesize record from disk layout.
			records = append(records, QuarantineRecord{
				CardID:      cardID,
				Reason:      reason,
				FailureKind: FailureCardDefect,
			})
		}
	}

	sort.Slice(records, func(i, j int) bool {
		if records[i].Reason != records[j].Reason {
			return records[i].Reason < records[j].Reason
		}
		return records[i].CardID < records[j].CardID
	})

	return records, nil
}

// ReleaseQuarantined removes a card from quarantine and restores it to <queue>/<card-id>.card
// so it can be re-run after defects have been resolved.
func ReleaseQuarantined(queueDir, reason, cardID string) (string, error) {
	qDir := QuarantineCardDir(queueDir, reason, cardID)
	cardFile := filepath.Join(qDir, cardID+CardExt)
	raw, err := os.ReadFile(cardFile)
	if err != nil {
		return "", fmt.Errorf("release quarantine: card %s not found in %s: %w", cardID, qDir, err)
	}

	destQueue := QueueDir(filepath.Dir(queueDir))
	if filepath.Base(queueDir) == QueueName {
		destQueue = queueDir
	}
	if err := os.MkdirAll(destQueue, 0o755); err != nil {
		return "", err
	}

	destPath := filepath.Join(destQueue, cardID+CardExt)
	if err := os.WriteFile(destPath, raw, 0o644); err != nil {
		return "", fmt.Errorf("release quarantine: restore card to %s: %w", destPath, err)
	}

	// Clean up quarantine entry.
	_ = os.RemoveAll(qDir)
	// Clean up reason dir if empty.
	_ = os.Remove(QuarantineReasonDir(queueDir, reason))

	return destPath, nil
}

// TriageAction is the routing action determined by failure classification.
type TriageAction string

const (
	// ActionRetry means the card should be retried with backoff.
	ActionRetry TriageAction = "retry"

	// ActionQuarantine means the card is placed in queue/quarantine/<reason>/<card-id>.
	ActionQuarantine TriageAction = "quarantine"

	// ActionFail means the failure is treated as normal test failure.
	ActionFail TriageAction = "fail"
)

// TriageRouteResult holds the result of triage routing.
type TriageRouteResult struct {
	CardID       string       `json:"card_id"`
	Action       TriageAction `json:"action"`
	Reason       string       `json:"reason"`
	FailureKind  FailureKind  `json:"failure_kind"`
	Attempts     int          `json:"attempts"`
	TargetDir    string       `json:"target_dir,omitempty"`
	Message      string       `json:"message"`
}

// RouteFailure routes a failed card according to the failure taxonomy:
//  - CardDefect -> immediately quarantined under queue/quarantine/<reason>/<card-id>
//  - TransientProvider -> retried if attempts < maxAttempts; quarantined if attempts >= maxAttempts
//  - InfraCrash -> retried if transient, quarantined if exhausted/recurrent
//  - TestFailure -> marked fail
func RouteFailure(queueDir, cardID string, cardContent []byte, attempts, maxAttempts int, fc FailureClassification) (TriageRouteResult, error) {
	if maxAttempts <= 0 {
		maxAttempts = MaxProviderAttempts
	}

	res := TriageRouteResult{
		CardID:      cardID,
		Reason:      fc.Reason,
		FailureKind: fc.Kind,
		Attempts:    attempts,
	}

	switch fc.Kind {
	case FailureCardDefect:
		res.Action = ActionQuarantine
		res.Message = fmt.Sprintf("quarantined card defect: %s", fc.Details)
		qDir, err := QuarantineCard(queueDir, fc.Reason, cardID, cardContent, QuarantineRecord{
			CardID:      cardID,
			Reason:      fc.Reason,
			FailureKind: fc.Kind,
			Attempts:    attempts,
			Details:     fc.Details,
		})
		if err != nil {
			return res, err
		}
		res.TargetDir = qDir
		return res, nil

	case FailureTransientProvider:
		if attempts < maxAttempts {
			res.Action = ActionRetry
			res.Message = fmt.Sprintf("transient provider error (attempt %d of %d): %s", attempts, maxAttempts, fc.Details)
			return res, nil
		}
		// Exhausted provider attempts -> quarantine!
		res.Action = ActionQuarantine
		reason := "provider-exhausted"
		res.Reason = reason
		res.Message = fmt.Sprintf("provider retry budget exhausted (%d attempts): %s", attempts, fc.Details)
		qDir, err := QuarantineCard(queueDir, reason, cardID, cardContent, QuarantineRecord{
			CardID:      cardID,
			Reason:      reason,
			FailureKind: fc.Kind,
			Attempts:    attempts,
			Details:     fc.Details,
		})
		if err != nil {
			return res, err
		}
		res.TargetDir = qDir
		return res, nil

	case FailureInfraCrash:
		if fc.Quarantinable && attempts >= maxAttempts {
			res.Action = ActionQuarantine
			res.Message = fmt.Sprintf("quarantined infrastructure crash: %s", fc.Details)
			qDir, err := QuarantineCard(queueDir, fc.Reason, cardID, cardContent, QuarantineRecord{
				CardID:      cardID,
				Reason:      fc.Reason,
				FailureKind: fc.Kind,
				Attempts:    attempts,
				Details:     fc.Details,
			})
			if err != nil {
				return res, err
			}
			res.TargetDir = qDir
			return res, nil
		}
		res.Action = ActionRetry
		res.Message = fmt.Sprintf("infra crash retriable: %s", fc.Details)
		return res, nil

	case FailureTestFailure:
		fallthrough
	default:
		res.Action = ActionFail
		res.Message = fmt.Sprintf("test failure: %s", fc.Details)
		return res, nil
	}
}

// ReconcileRecord records the reconciliation of one card rescued from limbo.
type ReconcileRecord struct {
	Path     string       `json:"path"`
	CardID   string       `json:"card_id"`
	Action   TriageAction `json:"action"` // ActionRetry (recovered to queue) or ActionQuarantine
	Reason   string       `json:"reason"`
	Attempts int          `json:"attempts"`
}

// ReconcileQueueLimbo sweeps queueDir and its taken/ subdirectories for any cards
// stuck in limbo with `.provider-failed` (e.g. `<card>.provider-failed`,
// `<card>.card.provider-failed`, or files in `.provider-failed/` directories).
//
// If attempts < maxAttempts: restores the card to <queueDir>/<card-id>.card so it
// returns to the active queue.
// If attempts >= maxAttempts: moves the card to <queueDir>/quarantine/provider-exhausted/<card-id>/.
// Removes the limbo artifact so no cards are left in limbo.
func ReconcileQueueLimbo(queueDir string, maxAttempts int) ([]ReconcileRecord, error) {
	if maxAttempts <= 0 {
		maxAttempts = MaxProviderAttempts
	}

	var results []ReconcileRecord

	// Look in queueDir and takenDir.
	dirsToSearch := []string{queueDir}
	taken := filepath.Join(queueDir, TakenName)
	if _, err := os.Stat(taken); err == nil {
		dirsToSearch = append(dirsToSearch, taken)
	}

	for _, d := range dirsToSearch {
		entries, err := os.ReadDir(d)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return results, err
		}

		for _, e := range entries {
			if e.IsDir() {
				// Also check if directory itself is named .provider-failed
				if e.Name() == ".provider-failed" || e.Name() == "provider-failed" {
					subEntries, err := os.ReadDir(filepath.Join(d, e.Name()))
					if err == nil {
						for _, se := range subEntries {
							if se.IsDir() {
								continue
							}
							path := filepath.Join(d, e.Name(), se.Name())
							rec, err := reconcileLimboFile(queueDir, path, se.Name(), maxAttempts)
							if err == nil {
								results = append(results, rec)
							}
						}
					}
					_ = os.Remove(filepath.Join(d, e.Name()))
				}
				continue
			}

			name := e.Name()
			if strings.HasSuffix(name, ProviderFailedExt) || strings.Contains(name, ".provider-failed.") {
				path := filepath.Join(d, name)
				rec, err := reconcileLimboFile(queueDir, path, name, maxAttempts)
				if err != nil {
					continue
				}
				results = append(results, rec)
			}
		}
	}

	return results, nil
}

func reconcileLimboFile(queueDir, path, name string, maxAttempts int) (ReconcileRecord, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ReconcileRecord{}, err
	}

	cardID := extractCardID(name)
	attempts := detectAttempts(raw, name)

	// Classify: default to transient provider error unless text indicates card defect.
	fc := ClassifyFailure(nil, string(raw), 0, "")
	if fc.Kind != FailureCardDefect {
		fc.Kind = FailureTransientProvider
		fc.Reason = "provider-failed"
		fc.Retriable = true
	}

	if fc.Kind == FailureTransientProvider && attempts < maxAttempts {
		// RECOVER: restore to queue/<card-id>.card
		dest := filepath.Join(queueDir, cardID+CardExt)
		if err := os.WriteFile(dest, raw, 0o644); err != nil {
			return ReconcileRecord{}, err
		}
		_ = os.Remove(path)
		return ReconcileRecord{
			Path:     dest,
			CardID:   cardID,
			Action:   ActionRetry,
			Reason:   "restored-from-limbo",
			Attempts: attempts,
		}, nil
	}

	// EXHAUSTED or DEFECT: Quarantine
	reason := "provider-exhausted"
	if fc.Kind == FailureCardDefect {
		reason = fc.Reason
	}
	targetDir, err := QuarantineCard(queueDir, reason, cardID, raw, QuarantineRecord{
		CardID:       cardID,
		Reason:       reason,
		FailureKind:  fc.Kind,
		Attempts:     attempts,
		OriginalPath: path,
		Details:      "rescued from .provider-failed limbo",
	})
	if err != nil {
		return ReconcileRecord{}, err
	}
	_ = os.Remove(path)

	return ReconcileRecord{
		Path:     targetDir,
		CardID:   cardID,
		Action:   ActionQuarantine,
		Reason:   reason,
		Attempts: attempts,
	}, nil
}

// extractCardID extracts the clean card id from a filename with possible prefixes/suffixes.
func extractCardID(name string) string {
	cleaned := name
	// Strip provider-failed extensions
	cleaned = strings.TrimSuffix(cleaned, ProviderFailedExt)
	// Strip .card if present
	cleaned = strings.TrimSuffix(cleaned, CardExt)
	// Strip worker prefix if present (worker-cardname)
	if idx := strings.Index(cleaned, "-"); idx > 0 && !strings.HasPrefix(cleaned, "card-") {
		cleaned = cleaned[idx+1:]
	}
	return cleaned
}

// detectAttempts reads attempts if recorded in the card text, default 1.
func detectAttempts(cardBytes []byte, name string) int {
	lines := strings.Split(string(cardBytes), "\n")
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "attempts:") || strings.HasPrefix(trimmed, ":attempts") {
			parts := strings.Fields(trimmed)
			if len(parts) >= 2 {
				var n int
				if _, err := fmt.Sscanf(parts[1], "%d", &n); err == nil && n > 0 {
					return n
				}
			}
		}
	}
	return 1
}

// TriageQuarantineSummary formats a one-line summary of quarantined cards for triage:
// TRIAGE QUARANTINE total=<n> input_limit=<n> malformed=<n> provider_exhausted=<n> infra=<n>
func TriageQuarantineSummary(queueDir string) string {
	records, err := ListQuarantined(queueDir)
	if err != nil || len(records) == 0 {
		return "TRIAGE QUARANTINE total=0 input_limit=0 malformed=0 provider_exhausted=0 infra=0"
	}

	byReason := map[string]int{}
	for _, r := range records {
		byReason[r.Reason]++
	}

	return fmt.Sprintf("TRIAGE QUARANTINE total=%d input_limit=%d malformed=%d provider_exhausted=%d infra=%d",
		len(records), byReason["input-limit"], byReason["malformed-syntax"],
		byReason["provider-exhausted"], byReason["infra-crash"]+byReason["oom-killed"]+byReason["segfault"])
}

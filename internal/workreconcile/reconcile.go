package workreconcile

import (
	"fmt"
	"sort"
	"strings"
)

// DiscrepancyKind identifies the category of divergence between captured GitHub state and nova-work state.
type DiscrepancyKind string

const (
	DiscrepancyMissingInStore   DiscrepancyKind = "missing_in_store"
	DiscrepancyExtraInStore     DiscrepancyKind = "extra_in_store"
	DiscrepancyRevisionMismatch DiscrepancyKind = "revision_mismatch"
	DiscrepancyCommentCount     DiscrepancyKind = "comment_count_mismatch"
	DiscrepancyLabelSetMismatch DiscrepancyKind = "label_set_mismatch"
	DiscrepancyStateMismatch    DiscrepancyKind = "state_mismatch"
)

// Discrepancy represents an exact, unsummarized divergence on a single issue.
type Discrepancy struct {
	Repo          string          `json:"repo"`
	IssueNumber   int             `json:"issue_number"`
	Kind          DiscrepancyKind `json:"kind"`
	Detail        string          `json:"detail"`
	CapturedValue string          `json:"captured_value,omitempty"`
	StoreValue    string          `json:"store_value,omitempty"`
}

// String renders the discrepancy in a deterministic, one-line format.
func (d Discrepancy) String() string {
	var parts []string
	parts = append(parts, fmt.Sprintf("DISCREPANCY issue=%d kind=%s detail=%q", d.IssueNumber, d.Kind, d.Detail))
	if d.CapturedValue != "" {
		parts = append(parts, fmt.Sprintf("captured=%q", d.CapturedValue))
	}
	if d.StoreValue != "" {
		parts = append(parts, fmt.Sprintf("nova_work=%q", d.StoreValue))
	}
	return strings.Join(parts, " ")
}

// ReconciliationReport records the result of comparing captured GitHub issues against nova-work state.
type ReconciliationReport struct {
	Repo          string        `json:"repo"`
	CapturedCount int           `json:"captured_count"`
	StoreCount    int           `json:"store_count"`
	MatchedCount  int           `json:"matched_count"`
	Discrepancies []Discrepancy `json:"discrepancies"`
	Passed        bool          `json:"passed"`
}

// ReportText returns a deterministic text rendering of the reconciliation report.
// It lists every single discrepancy by issue number and never summarizes away differences.
func (r *ReconciliationReport) ReportText() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("RECONCILE repo=%s captured=%d imported=%d discrepancies=%d\n",
		r.Repo, r.CapturedCount, r.StoreCount, len(r.Discrepancies)))

	for _, d := range r.Discrepancies {
		sb.WriteString(d.String())
		sb.WriteByte('\n')
	}

	if r.Passed {
		sb.WriteString(fmt.Sprintf("RECONCILE RESULT repo=%s PASS\n", r.Repo))
	} else {
		sb.WriteString(fmt.Sprintf("RECONCILE RESULT repo=%s FAIL discrepancies=%d\n", r.Repo, len(r.Discrepancies)))
	}

	return sb.String()
}

// ReconciliationEngine compares captured GitHub issues against nova-work state per repository.
type ReconciliationEngine struct{}

// NewReconciliationEngine returns a new engine instance.
func NewReconciliationEngine() *ReconciliationEngine {
	return &ReconciliationEngine{}
}

// Reconcile compares the given captured manifest against the store's nodes for that repository.
// It verifies:
// 1. Issue count: captured issue count equals store node count.
// 2. Per issue: presence, comment count, label set, revision, and open/closed state.
// Discrepancies are sorted deterministically by issue number ascending, then by kind.
func (e *ReconciliationEngine) Reconcile(manifest *CaptureManifest, store *Store) (*ReconciliationReport, error) {
	if manifest == nil {
		return nil, fmt.Errorf("reconcile requires a non-nil capture manifest")
	}
	if store == nil {
		return nil, fmt.Errorf("reconcile requires a non-nil store")
	}

	repo := manifest.Repo
	storeNodes := store.NodesForRepo(repo)

	// Map of store nodes by issue number
	storeMap := make(map[int]*WorkNode, len(storeNodes))
	for _, node := range storeNodes {
		storeMap[node.IssueNumber] = node
	}

	// Map of captured issues by issue number
	capturedMap := make(map[int]CapturedIssue, len(manifest.Issues))
	for _, issue := range manifest.Issues {
		capturedMap[issue.Number] = issue
	}

	var discrepancies []Discrepancy
	matchedCount := 0

	// 1. Check every captured issue against the store
	for _, issue := range manifest.Issues {
		node, found := storeMap[issue.Number]
		if !found {
			discrepancies = append(discrepancies, Discrepancy{
				Repo:          repo,
				IssueNumber:   issue.Number,
				Kind:          DiscrepancyMissingInStore,
				Detail:        fmt.Sprintf("issue #%d present in capture but missing from nova-work store", issue.Number),
				CapturedValue: fmt.Sprintf("revision=%s state=%s", issue.Revision, issue.State),
			})
			continue
		}

		issueHadDiscrepancy := false

		// Check Revision
		if issue.Revision != node.Revision {
			issueHadDiscrepancy = true
			discrepancies = append(discrepancies, Discrepancy{
				Repo:          repo,
				IssueNumber:   issue.Number,
				Kind:          DiscrepancyRevisionMismatch,
				Detail:        fmt.Sprintf("issue #%d revision mismatch", issue.Number),
				CapturedValue: issue.Revision,
				StoreValue:    node.Revision,
			})
		}

		// Check Comment Count
		if issue.CommentCount != node.CommentCount {
			issueHadDiscrepancy = true
			discrepancies = append(discrepancies, Discrepancy{
				Repo:          repo,
				IssueNumber:   issue.Number,
				Kind:          DiscrepancyCommentCount,
				Detail:        fmt.Sprintf("issue #%d comment count mismatch: captured %d vs nova-work %d", issue.Number, issue.CommentCount, node.CommentCount),
				CapturedValue: fmt.Sprintf("%d", issue.CommentCount),
				StoreValue:    fmt.Sprintf("%d", node.CommentCount),
			})
		}

		// Check Label Set
		capturedLabels := issue.NormalizedLabels()
		nodeLabels := node.NormalizedLabels()
		if !slicesEqual(capturedLabels, nodeLabels) {
			issueHadDiscrepancy = true
			missing, extra := diffStringSlices(capturedLabels, nodeLabels)
			detail := fmt.Sprintf("issue #%d label set mismatch", issue.Number)
			if len(missing) > 0 {
				detail += fmt.Sprintf(" (missing in store: [%s])", strings.Join(missing, ", "))
			}
			if len(extra) > 0 {
				detail += fmt.Sprintf(" (extra in store: [%s])", strings.Join(extra, ", "))
			}
			discrepancies = append(discrepancies, Discrepancy{
				Repo:          repo,
				IssueNumber:   issue.Number,
				Kind:          DiscrepancyLabelSetMismatch,
				Detail:        detail,
				CapturedValue: strings.Join(capturedLabels, ","),
				StoreValue:    strings.Join(nodeLabels, ","),
			})
		}

		// Check State (open vs closed/settled)
		capState := strings.ToLower(issue.State)
		nodeState := strings.ToLower(node.State)
		// in nova-work, closed issues import as "closed" or "settled" (E09-F05-01)
		isNodeClosed := nodeState == "closed" || nodeState == "settled"
		isCapClosed := capState == "closed"
		if isCapClosed != isNodeClosed {
			issueHadDiscrepancy = true
			discrepancies = append(discrepancies, Discrepancy{
				Repo:          repo,
				IssueNumber:   issue.Number,
				Kind:          DiscrepancyStateMismatch,
				Detail:        fmt.Sprintf("issue #%d state mismatch: captured %s vs nova-work %s", issue.Number, issue.State, node.State),
				CapturedValue: issue.State,
				StoreValue:    node.State,
			})
		}

		if !issueHadDiscrepancy {
			matchedCount++
		}
	}

	// 2. Check for extra nodes in store that were not in capture
	for _, node := range storeNodes {
		if _, found := capturedMap[node.IssueNumber]; !found {
			discrepancies = append(discrepancies, Discrepancy{
				Repo:        repo,
				IssueNumber: node.IssueNumber,
				Kind:        DiscrepancyExtraInStore,
				Detail:      fmt.Sprintf("issue #%d exists in nova-work (uid=%s) but was not in captured manifest", node.IssueNumber, node.UID),
				StoreValue:  fmt.Sprintf("uid=%s revision=%s", node.UID, node.Revision),
			})
		}
	}

	// Sort discrepancies deterministically: IssueNumber ASC, then Kind ASC
	sort.Slice(discrepancies, func(i, j int) bool {
		if discrepancies[i].IssueNumber != discrepancies[j].IssueNumber {
			return discrepancies[i].IssueNumber < discrepancies[j].IssueNumber
		}
		return discrepancies[i].Kind < discrepancies[j].Kind
	})

	passed := len(discrepancies) == 0 && len(manifest.Issues) == len(storeNodes)

	return &ReconciliationReport{
		Repo:          repo,
		CapturedCount: len(manifest.Issues),
		StoreCount:    len(storeNodes),
		MatchedCount:  matchedCount,
		Discrepancies: discrepancies,
		Passed:        passed,
	}, nil
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func diffStringSlices(expected, actual []string) (missing, extra []string) {
	expMap := make(map[string]bool, len(expected))
	for _, s := range expected {
		expMap[s] = true
	}
	actMap := make(map[string]bool, len(actual))
	for _, s := range actual {
		actMap[s] = true
	}

	for _, s := range expected {
		if !actMap[s] {
			missing = append(missing, s)
		}
	}
	for _, s := range actual {
		if !expMap[s] {
			extra = append(extra, s)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}

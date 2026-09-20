package roadmap

import (
	"fmt"
	"math"
	"strings"
)

// Status represents the lifecycle status of a criterion, feature, or epic.
type Status string

const (
	// StatusVerified indicates an item has been implemented and verified by tests.
	StatusVerified Status = "verified"
	// StatusUnverified indicates an item is not yet verified or implemented.
	StatusUnverified Status = "unverified"
	// StatusInProgress indicates an item is actively in development or partially implemented.
	StatusInProgress Status = "in-progress"
)

// ParseStatus normalizes a raw status string into a canonical Status.
func ParseStatus(s string) Status {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "verified", "done", "closed", "landed", "merged", "complete", "completed", "green", "pass", "passed":
		return StatusVerified
	case "in-progress", "in_progress", "inprogress", "doing", "live", "active", "partial", "wip":
		return StatusInProgress
	case "unverified", "unmet", "missing", "todo", "open", "proposed", "blocked", "pending", "deferred":
		return StatusUnverified
	default:
		if strings.Contains(s, "verif") || strings.Contains(s, "done") || strings.Contains(s, "pass") {
			return StatusVerified
		}
		if strings.Contains(s, "progress") || strings.Contains(s, "partial") || strings.Contains(s, "doing") {
			return StatusInProgress
		}
		return StatusUnverified
	}
}

// Criterion represents a single acceptance criterion in a roadmap.
type Criterion struct {
	// ID is the unique identifier (e.g., "E01-F01-01").
	ID string `json:"id"`
	// Title or description of the criterion.
	Title string `json:"title"`
	// Status is the normalized status (verified, unverified, in-progress).
	Status Status `json:"status"`
	// RawStatus is the exact status string from the source.
	RawStatus string `json:"raw_status"`
	// VerificationNotes contains notes, proving tests, or evidence.
	VerificationNotes string `json:"verification_notes,omitempty"`
	// Dependencies lists UIDs this criterion depends on.
	Dependencies []string `json:"dependencies,omitempty"`
	// FeatureID is the UID of the parent feature.
	FeatureID string `json:"feature_id"`
	// EpicID is the UID of the parent epic.
	EpicID string `json:"epic_id"`
	// Line is the 1-based line number where the criterion was defined.
	Line int `json:"line"`
	// Column is the 1-based column number where the criterion was defined.
	Column int `json:"column"`
	// Fields preserves any additional raw fields for extensibility.
	Fields map[string]any `json:"fields,omitempty"`
}

// Feature represents a feature within an epic.
type Feature struct {
	// ID is the unique identifier (e.g., "E01-F01").
	ID string `json:"id"`
	// Title of the feature.
	Title string `json:"title"`
	// Status is the normalized status of the feature.
	Status Status `json:"status"`
	// RawStatus is the exact status string from the source.
	RawStatus string `json:"raw_status"`
	// Dependencies lists feature IDs this feature depends on.
	Dependencies []string `json:"dependencies,omitempty"`
	// VerificationNotes contains verification notes or proving tests.
	VerificationNotes string `json:"verification_notes,omitempty"`
	// Criteria is the ordered list of criteria under this feature.
	Criteria []*Criterion `json:"criteria"`
	// EpicID is the UID of the parent epic.
	EpicID string `json:"epic_id"`
	// Line is the 1-based line number where the feature was defined.
	Line int `json:"line"`
	// Column is the 1-based column number where the feature was defined.
	Column int `json:"column"`
	// Fields preserves any additional raw fields for extensibility.
	Fields map[string]any `json:"fields,omitempty"`
}

// Epic represents a top-level epic containing features.
type Epic struct {
	// ID is the unique identifier (e.g., "E01").
	ID string `json:"id"`
	// Title of the epic.
	Title string `json:"title"`
	// Status is the normalized status of the epic.
	Status Status `json:"status"`
	// RawStatus is the exact status string from the source.
	RawStatus string `json:"raw_status"`
	// Dependencies lists epic IDs this epic depends on.
	Dependencies []string `json:"dependencies,omitempty"`
	// VerificationNotes contains verification notes or evidence for the epic.
	VerificationNotes string `json:"verification_notes,omitempty"`
	// Features is the ordered list of features under this epic.
	Features []*Feature `json:"features"`
	// Line is the 1-based line number where the epic was defined.
	Line int `json:"line"`
	// Column is the 1-based column number where the epic was defined.
	Column int `json:"column"`
	// Fields preserves any additional raw fields for extensibility.
	Fields map[string]any `json:"fields,omitempty"`
}

// Verification contains verification metadata from the roadmap.
type Verification struct {
	MeasuredAt              string   `json:"measured_at,omitempty"`
	Revision                string   `json:"revision,omitempty"`
	Branch                  string   `json:"branch,omitempty"`
	Suite                   string   `json:"suite,omitempty"`
	SuiteResult             string   `json:"suite_result,omitempty"`
	CriteriaRule            string   `json:"criteria_rule,omitempty"`
	Rule                    string   `json:"rule,omitempty"`
	VerifiedFeatures        int      `json:"verified_features,omitempty"`
	VerifiedAcceptanceItems int      `json:"verified_acceptance_items,omitempty"`
	RawFields               map[string]any `json:"raw_fields,omitempty"`
}

// Roadmap represents a fully parsed roadmap criteria document.
type Roadmap struct {
	// File path where the roadmap was loaded from (if applicable).
	File string `json:"file,omitempty"`
	// Schema version/name of the roadmap document.
	Schema string `json:"schema,omitempty"`
	// Title of the roadmap.
	Title string `json:"title,omitempty"`
	// Epics in document order.
	Epics []*Epic `json:"epics"`
	// Verification metadata if present.
	Verification Verification `json:"verification,omitempty"`
	// Metadata preserves top-level key-values.
	Metadata map[string]any `json:"metadata,omitempty"`

	// Lookup caches
	epicMap      map[string]*Epic
	featureMap   map[string]*Feature
	criterionMap map[string]*Criterion
}

// Progress summarizes the criteria counts and completion percentage.
type Progress struct {
	Total      int     `json:"total"`
	Verified   int     `json:"verified"`
	Unverified int     `json:"unverified"`
	InProgress int     `json:"in_progress"`
	Percentage float64 `json:"percentage"`
}

// PercentString formats the completion percentage as a string, e.g. "67.1%".
func (p Progress) PercentString() string {
	return fmt.Sprintf("%.1f%%", p.Percentage)
}

// CalculateProgress calculates Progress from counts.
func CalculateProgress(verified, unverified, inProgress int) Progress {
	total := verified + unverified + inProgress
	pct := 0.0
	if total > 0 {
		pct = (float64(verified) / float64(total)) * 100.0
		pct = math.Round(pct*100.0) / 100.0
	}
	return Progress{
		Total:      total,
		Verified:   verified,
		Unverified: unverified,
		InProgress: inProgress,
		Percentage: pct,
	}
}

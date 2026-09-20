package capture

import (
	"errors"
	"time"
)

// Invariant errors: strictly read-only and bounded validation.
var (
	ErrReadOnly          = errors.New("read-only adapter: outbound mutation refused")
	ErrManifestMismatch  = errors.New("capture bundle manifest totals disagree with contents")
	ErrPageGap           = errors.New("capture bundle pagination has a gap")
	ErrByteBoundExceeded = errors.New("capture exceeded byte bound")
	ErrCorruptPayload    = errors.New("staged payload corrupt or checksum mismatch")
)

// Provider names the external forge.
const (
	ProviderGitHub = "github"
	HostGitHub     = "github.com"
)

// Manifest is the root metadata file staged as manifest.json.
type Manifest struct {
	Provider    string         `json:"provider"`
	Host        string         `json:"host"`
	Repository  string         `json:"repository"`
	FetchedAt   time.Time      `json:"fetched_at"`
	PageCount   int            `json:"page_count"`
	TotalIssues int            `json:"total_issues"`
	StagedBytes int64          `json:"staged_bytes"`
	Issues      []IssueSummary `json:"issues"`
}

// IssueSummary is one issue's summary entry inside Manifest.
type IssueSummary struct {
	Number   int    `json:"number"`
	NodeID   string `json:"node_id"`
	RESTID   int64  `json:"rest_id"`
	Revision string `json:"revision"` // updated_at ISO string
	URL      string `json:"url"`
	File     string `json:"file"` // relative path, e.g. "issues/issue-2080.json"
	Bytes    int64  `json:"bytes"`
	SHA256   string `json:"sha256"`
}

// CapturedIssue is the complete raw payload saved per issue under issues/issue-<num>.json.
type CapturedIssue struct {
	Provider       string                 `json:"provider"`
	Host           string                 `json:"host"`
	Repository     string                 `json:"repository"`
	Number         int                    `json:"number"`
	NodeID         string                 `json:"node_id"`
	RESTID         int64                  `json:"rest_id"`
	Title          string                 `json:"title"`
	URL            string                 `json:"url"`
	Revision       string                 `json:"revision"` // updated_at ISO
	State          string                 `json:"state"`
	Author         string                 `json:"author"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
	ClosedAt       *time.Time             `json:"closed_at,omitempty"`
	Body           string                 `json:"body"`
	Truncated      bool                   `json:"truncated,omitempty"`
	TruncatedHash  string                 `json:"truncated_hash,omitempty"`
	TruncatedAt    int64                  `json:"truncated_at,omitempty"`
	Comments       []CapturedComment      `json:"comments"`
	Labels         []CapturedLabel        `json:"labels"`
	Relationships  []CapturedRelationship `json:"relationships"`
	Attachments    []CapturedAttachment   `json:"attachments"`
	Page           int                    `json:"page"`
}

// CapturedComment holds one issue comment.
type CapturedComment struct {
	ID        int64     `json:"id"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Body      string    `json:"body"`
	URL       string    `json:"url"`
}

// CapturedLabel holds one issue label.
type CapturedLabel struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

// CapturedRelationship holds one related item (e.g. cross-reference, linked PR, parent/child).
type CapturedRelationship struct {
	Kind      string    `json:"kind"` // "cross-referenced", "pull_request", etc.
	Target    string    `json:"target"` // URL, repo#num, or node_id
	Source    string    `json:"source"` // source identity
	CreatedAt time.Time `json:"created_at"`
}

// CapturedAttachment holds one detected attachment (recorded with status="not fetched").
type CapturedAttachment struct {
	URL     string `json:"url"`
	Status  string `json:"status"` // "not fetched"
	FoundIn string `json:"found_in"` // "body" or "comment:<id>"
}

// Options controls capture execution.
type Options struct {
	Repo         string        // "owner/repo"
	StagingDir   string        // directory to stage files under
	MaxBytes     int64         // total byte limit (default 10MB)
	MaxBodyBytes int64         // per-body byte limit (default 64KB)
	Timeout      time.Duration // subprocess / network timeout
	Resume       bool          // whether to resume from existing staged files
	Issues       []int         // optional explicit list of issue numbers to capture (empty for all)
}

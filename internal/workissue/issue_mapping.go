// Package workissue implements mapping captured GitHub issue records to
// nova-work tree nodes and generating two-sided back-pointers for proving runs
// (Issue #2081, #2084; SPEC-WORK.md §E09-F02/F03).
//
// Every imported issue receives:
//  1. A 128-bit generic UID (Issue #2084): 16 random bytes rendered as 32
//     lowercase hexadecimal characters, untyped and unhashed.
//  2. A faithful mapping of title, body, state (open/closed), labels, comments,
//     and author into generic node attributes.
//  3. A two-sided back-pointer receipt formatted as:
//     `nova-work: uid=<hex> store=<journal-identity-prefix>`
//     ensuring idempotent application (second pass detects existing pointer and
//     writes nothing).
package workissue

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

var (
	// ErrInvalidUID is returned when a UID is not 32 lowercase hex characters.
	ErrInvalidUID = errors.New("invalid UID: must be 32 lowercase hex characters")

	// ErrShortByteSource is returned when a byte source provides fewer than 16 bytes.
	ErrShortByteSource = errors.New("short byte source: requires 16 bytes for 128-bit UID")

	// ErrAlreadyPresent is returned when an issue already carries the back-pointer.
	ErrAlreadyPresent = errors.New("back-pointer already present on issue")
)

// CapturedComment represents a single comment in an issue thread.
type CapturedComment struct {
	ID        int64     `json:"id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// CapturedIssue represents a captured GitHub issue record.
type CapturedIssue struct {
	Provider  string            `json:"provider"`
	Repo      string            `json:"repo"`
	Number    int               `json:"number"`
	Revision  string            `json:"revision"`
	Title     string            `json:"title"`
	Body      string            `json:"body"`
	State     string            `json:"state"` // "open" or "closed"
	Author    string            `json:"author"`
	Labels    []string          `json:"labels"`
	Comments  []CapturedComment `json:"comments"`
	URL       string            `json:"url"`
}

// IssueCorrespondence tracks upstream identity coordinates.
type IssueCorrespondence struct {
	Provider string `json:"provider"`
	Repo     string `json:"repo"`
	Number   int    `json:"number"`
	Revision string `json:"revision"`
	URL      string `json:"url"`
}

// GenericNodeAttributes holds the mapped attributes stored on the nova-work node.
type GenericNodeAttributes struct {
	UID            string              `json:"uid"`
	Title          string              `json:"title"`
	Body           string              `json:"body"`
	State          string              `json:"state"`
	Author         string              `json:"author"`
	Labels         []string            `json:"labels"`
	Comments       []CapturedComment   `json:"comments"`
	Correspondence IssueCorrespondence `json:"correspondence"`
}

// WorkNode represents an imported node in the nova-work tree.
type WorkNode struct {
	ID          string                `json:"id"`
	Type        string                `json:"type"`
	Parent      string                `json:"parent,omitempty"`
	Coordinator string                `json:"coordinator,omitempty"`
	Children    []string              `json:"children,omitempty"`
	Required    bool                  `json:"required"`
	State       string                `json:"state"` // "open" or "settled"
	Branch      string                `json:"branch"` // "o" or "c"
	OpenCount   int                   `json:"open_count"`
	Title       string                `json:"title"`
	Links       []string              `json:"links"`
	UID         string                `json:"uid"`
	Attributes  GenericNodeAttributes `json:"attributes"`
}

// BackPointerReceipt records a back-pointer generation receipt for the proving run.
type BackPointerReceipt struct {
	Status      string `json:"status"` // "confirmed", "skipped"
	Action      string `json:"action"` // "issue.back-pointer"
	IssueRef    string `json:"issue_ref"`
	UID         string `json:"uid"`
	BackPointer string `json:"back_pointer"`
	Applied     bool   `json:"applied"`
	Reason      string `json:"reason,omitempty"`
}

// MintUID mints a 128-bit generic UID (Issue #2084) using the system crypto/rand.
// The result is 32 lowercase hexadecimal characters.
func MintUID() (string, error) {
	return MintUIDFromSource(rand.Reader)
}

// MintUIDFromSource reads exactly 16 bytes from source and hex-encodes them.
// Refuses if source returns fewer than 16 bytes or an error.
func MintUIDFromSource(source io.Reader) (string, error) {
	if source == nil {
		return "", errors.New("nil byte source")
	}
	var buf [16]byte
	n, err := io.ReadFull(source, buf[:])
	if err != nil || n < 16 {
		if err == io.EOF || err == io.ErrUnexpectedEOF || n < 16 {
			return "", ErrShortByteSource
		}
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

// IsValidUID checks whether uid is a valid 128-bit generic UID:
// exactly 32 lowercase hexadecimal characters.
func IsValidUID(uid string) bool {
	if len(uid) != 32 {
		return false
	}
	for i := 0; i < len(uid); i++ {
		c := uid[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// MapOptions configures issue mapping.
type MapOptions struct {
	UID         string
	NodeID      string
	NodeType    string
	Parent      string
	Coordinator string
	Store       string
	ByteSource  io.Reader
}

// Option configures MapOptions.
type Option func(*MapOptions)

// WithUID sets a pre-assigned UID.
func WithUID(uid string) Option {
	return func(o *MapOptions) {
		o.UID = uid
	}
}

// WithNodeID overrides the default node ID.
func WithNodeID(id string) Option {
	return func(o *MapOptions) {
		o.NodeID = id
	}
}

// WithParent sets the parent node ID.
func WithParent(parent string) Option {
	return func(o *MapOptions) {
		o.Parent = parent
	}
}

// WithCoordinator sets the coordinator identity.
func WithCoordinator(coordinator string) Option {
	return func(o *MapOptions) {
		o.Coordinator = coordinator
	}
}

// WithByteSource provides an alternative entropy source for minting UID.
func WithByteSource(src io.Reader) Option {
	return func(o *MapOptions) {
		o.ByteSource = src
	}
}

// MapCapturedIssue maps a CapturedIssue to a WorkNode and GenericNodeAttributes.
// Mappings:
// - Title, body, state (open/closed), author, labels, comments to generic node attributes.
// - Assigns a 128-bit generic UID (minted via MintUID or supplied via WithUID).
// - Preserves all comments, author signatures, and timestamps intact.
// - Sets branch: open -> "o", closed -> "c".
func MapCapturedIssue(issue CapturedIssue, opts ...Option) (*WorkNode, *GenericNodeAttributes, error) {
	var cfg MapOptions
	for _, opt := range opts {
		opt(&cfg)
	}

	uid := cfg.UID
	if uid == "" {
		var err error
		if cfg.ByteSource != nil {
			uid, err = MintUIDFromSource(cfg.ByteSource)
		} else {
			uid, err = MintUID()
		}
		if err != nil {
			return nil, nil, fmt.Errorf("mint UID: %w", err)
		}
	}

	if !IsValidUID(uid) {
		return nil, nil, fmt.Errorf("%w: %q", ErrInvalidUID, uid)
	}

	provider := issue.Provider
	if provider == "" {
		provider = "github"
	}
	repo := issue.Repo
	if repo == "" {
		repo = "mas-bandwidth/nova-tools"
	}
	revision := issue.Revision
	if revision == "" {
		revision = "rev-1"
	}
	url := issue.URL
	if url == "" {
		url = fmt.Sprintf("https://%s.com/%s/issues/%d", provider, repo, issue.Number)
	}

	normalizedState := strings.ToLower(strings.TrimSpace(issue.State))
	isOpen := normalizedState == "open" || normalizedState == "o" || normalizedState == ""
	var nodeState string
	var branch string
	var openCount int
	if isOpen {
		nodeState = "open"
		branch = "o"
		openCount = 1
	} else {
		nodeState = "settled"
		branch = "c"
		openCount = 0
	}

	nodeID := cfg.NodeID
	if nodeID == "" {
		nodeID = fmt.Sprintf("imported/%s/%d", repo, issue.Number)
	}
	nodeType := cfg.NodeType
	if nodeType == "" {
		nodeType = "task"
	}

	// Copy labels and comments to ensure immutability
	labels := make([]string, len(issue.Labels))
	copy(labels, issue.Labels)

	comments := make([]CapturedComment, len(issue.Comments))
	copy(comments, issue.Comments)

	corr := IssueCorrespondence{
		Provider: provider,
		Repo:     repo,
		Number:   issue.Number,
		Revision: revision,
		URL:      url,
	}

	attrs := GenericNodeAttributes{
		UID:            uid,
		Title:          issue.Title,
		Body:           issue.Body,
		State:          issue.State,
		Author:         issue.Author,
		Labels:         labels,
		Comments:       comments,
		Correspondence: corr,
	}

	node := &WorkNode{
		ID:          nodeID,
		Type:        nodeType,
		Parent:      cfg.Parent,
		Coordinator: cfg.Coordinator,
		Required:    true,
		State:       nodeState,
		Branch:      branch,
		OpenCount:   openCount,
		Title:       issue.Title,
		Links:       []string{url},
		UID:         uid,
		Attributes:  attrs,
	}

	return node, &attrs, nil
}

// FormatBackPointer formats the exact back-pointer string for GitHub issues
// linking to the nova-work UID (Issue #2081):
//   `nova-work: uid=<hex> store=<journal-identity-prefix>`
// If store is empty, formats:
//   `nova-work: uid=<hex>`
func FormatBackPointer(uid string, store string) string {
	store = strings.TrimSpace(store)
	if store != "" {
		return fmt.Sprintf("nova-work: uid=%s store=%s", uid, store)
	}
	return fmt.Sprintf("nova-work: uid=%s", uid)
}

// ParseBackPointer extracts the UID and optional store from a back-pointer string.
// Returns (uid, store, true) if found, or ("", "", false) if absent or malformed.
func ParseBackPointer(text string) (uid string, store string, ok bool) {
	const marker = "nova-work: uid="
	idx := strings.Index(text, marker)
	if idx < 0 {
		return "", "", false
	}
	rest := text[idx+len(marker):]
	// UID runs until space, newline, or tab
	end := strings.IndexAny(rest, " \t\r\n")
	if end < 0 {
		uid = rest
		rest = ""
	} else {
		uid = rest[:end]
		rest = rest[end:]
	}
	if !IsValidUID(uid) {
		return "", "", false
	}

	const storeMarker = "store="
	sIdx := strings.Index(rest, storeMarker)
	if sIdx >= 0 {
		sRest := rest[sIdx+len(storeMarker):]
		sEnd := strings.IndexAny(sRest, " \t\r\n")
		if sEnd < 0 {
			store = sRest
		} else {
			store = sRest[:sEnd]
		}
	}
	return uid, store, true
}

// HasBackPointer checks whether issue already contains a back-pointer comment
// or body referencing the given UID (or any valid back-pointer if uid is empty).
func HasBackPointer(issue CapturedIssue, uid string) bool {
	marker := "nova-work: uid="
	if uid != "" {
		marker = fmt.Sprintf("nova-work: uid=%s", uid)
	}

	if strings.Contains(issue.Body, marker) {
		return true
	}
	for _, comment := range issue.Comments {
		if strings.Contains(comment.Body, marker) {
			return true
		}
	}
	return false
}

// GenerateBackPointerReceipt produces a two-sided back-pointer receipt for the
// proving run (Issue #2081).
//
// Idempotency:
// If issue already carries the back-pointer for uid, it returns (nil, ErrAlreadyPresent)
// without performing any write or generating an active outbound receipt.
func GenerateBackPointerReceipt(issue CapturedIssue, uid string, store string) (*BackPointerReceipt, error) {
	if HasBackPointer(issue, uid) {
		return nil, ErrAlreadyPresent
	}

	repo := issue.Repo
	if repo == "" {
		repo = "mas-bandwidth/nova-tools"
	}
	issueRef := fmt.Sprintf("%s#%d", repo, issue.Number)
	bp := FormatBackPointer(uid, store)

	return &BackPointerReceipt{
		Status:      "confirmed",
		Action:      "issue.back-pointer",
		IssueRef:    issueRef,
		UID:         uid,
		BackPointer: bp,
		Applied:     true,
	}, nil
}

// ApplyBackPointerReceipt applies a receipt to issue by appending the back-pointer
// comment to comments, simulating the proving run's outbound comment creation.
// Returns false if receipt is nil, not applied, or if the issue already had the back-pointer.
func ApplyBackPointerReceipt(issue *CapturedIssue, receipt *BackPointerReceipt, author string) bool {
	if issue == nil || receipt == nil || !receipt.Applied {
		return false
	}
	if HasBackPointer(*issue, receipt.UID) {
		return false
	}
	if author == "" {
		author = "nova-work[bot]"
	}

	newComment := CapturedComment{
		ID:        int64(len(issue.Comments) + 1),
		Author:    author,
		Body:      receipt.BackPointer,
		CreatedAt: time.Now().UTC(),
	}
	issue.Comments = append(issue.Comments, newComment)
	return true
}

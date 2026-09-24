package workreconcile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// CapturedIssue represents an issue captured from GitHub via read-only intake.
type CapturedIssue struct {
	Provider     string   `json:"provider"` // e.g. "github"
	Repo         string   `json:"repo"`     // "owner/name", e.g. "mas-bandwidth/nova-tools"
	Number       int      `json:"number"`
	NodeID       string   `json:"node_id"`
	Title        string   `json:"title"`
	Body         string   `json:"body"`
	State        string   `json:"state"` // "open" or "closed"
	Author       string   `json:"author"`
	Revision     string   `json:"revision"` // stable revision, e.g. updated_at ISO string or etag
	CommentCount int      `json:"comment_count"`
	Labels       []string `json:"labels"`
	URL          string   `json:"url"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
	ClosedAt     string   `json:"closed_at,omitempty"`
}

// CanonicalKey returns the stable external identity (provider, repo, number).
func (c *CapturedIssue) CanonicalKey() string {
	return fmt.Sprintf("%s:%s#%d", c.Provider, c.Repo, c.Number)
}

// NormalizedLabels returns a sorted, deduplicated copy of labels.
func (c *CapturedIssue) NormalizedLabels() []string {
	return normalizeLabels(c.Labels)
}

func normalizeLabels(labels []string) []string {
	if len(labels) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(labels))
	var out []string
	for _, l := range labels {
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return out
}

// CaptureManifest is the byte-exact manifest accompanying a repository issue capture.
type CaptureManifest struct {
	Provider    string          `json:"provider"`
	Repo        string          `json:"repo"`
	FetchedAt   string          `json:"fetched_at"`
	TotalIssues int             `json:"total_issues"`
	Issues      []CapturedIssue `json:"issues"`
}

// WorkNode represents an imported issue or work task in the nova-work kernel.
type WorkNode struct {
	UID                string   `json:"uid"` // 128-bit lowercase hex, kernel-minted global uid
	ID                 string   `json:"id"`  // address path, e.g. "mas-bandwidth/nova-tools/issues/1945"
	Provider           string   `json:"provider"`
	Repo               string   `json:"repo"`
	IssueNumber        int      `json:"issue_number"`
	Title              string   `json:"title"`
	State              string   `json:"state"` // "open", "closed", "settled"
	Revision           string   `json:"revision"`
	CommentCount       int      `json:"comment_count"`
	Labels             []string `json:"labels"`
	URL                string   `json:"url"`
	Needs              []string `json:"needs,omitempty"` // UIDs or IDs of dependencies
	Blocks             []string `json:"blocks,omitempty"`
	BackPointerReceipt string   `json:"back_pointer_receipt,omitempty"`
	ImportedAt         string   `json:"imported_at"`
	Evidence           []string `json:"evidence,omitempty"`
}

// CanonicalKey returns the stable external identity.
func (n *WorkNode) CanonicalKey() string {
	return fmt.Sprintf("%s:%s#%d", n.Provider, n.Repo, n.IssueNumber)
}

// NormalizedLabels returns a sorted, deduplicated copy of labels.
func (n *WorkNode) NormalizedLabels() []string {
	return normalizeLabels(n.Labels)
}

// Checkpoint records the last applied source identity and batch state so an interrupted
// import resumes cleanly.
type Checkpoint struct {
	Repo              string `json:"repo"`
	LastAppliedNumber int    `json:"last_applied_number"`
	BatchNumber       int    `json:"batch_number"`
	ImportedCount     int    `json:"imported_count"`
	UpdatedAt         string `json:"updated_at"`
}

// MintUID generates a 128-bit lowercase hex unique identifier from the OS CSPRNG.
// It refuses on short reads and never derives from time, host, or counter (Issue #2084).
func MintUID() (string, error) {
	var b [16]byte
	n, err := rand.Read(b[:])
	if err != nil {
		return "", fmt.Errorf("entropy source failure: %w", err)
	}
	if n != 16 {
		return "", errors.New("short read from entropy source")
	}
	return hex.EncodeToString(b[:]), nil
}

// Store holds the imported work nodes, two-way correspondence index, and checkpoints.
type Store struct {
	mu            sync.RWMutex
	nodes         map[string]*WorkNode           // uid -> node
	byRepoAndNum  map[string]map[int]*WorkNode   // repo -> number -> node
	forwardIndex  map[string][]string            // node uid -> []external keys
	reverseIndex  map[string][]string            // external key -> []node uids
	checkpoints   map[string]*Checkpoint         // repo -> checkpoint
}

// NewStore initializes a new empty work store.
func NewStore() *Store {
	return &Store{
		nodes:        make(map[string]*WorkNode),
		byRepoAndNum: make(map[string]map[int]*WorkNode),
		forwardIndex: make(map[string][]string),
		reverseIndex: make(map[string][]string),
		checkpoints:  make(map[string]*Checkpoint),
	}
}

// AddNode adds a new node to the store and updates correspondence indices.
// If a node already exists with the same UID or the same (repo, issueNumber), an error is returned.
func (s *Store) AddNode(node *WorkNode) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if node.UID == "" {
		return errors.New("node has empty UID")
	}
	if _, exists := s.nodes[node.UID]; exists {
		return fmt.Errorf("node with UID %q already exists", node.UID)
	}

	repoMap, ok := s.byRepoAndNum[node.Repo]
	if !ok {
		repoMap = make(map[int]*WorkNode)
		s.byRepoAndNum[node.Repo] = repoMap
	}
	if node.IssueNumber > 0 {
		if existing, exists := repoMap[node.IssueNumber]; exists {
			return fmt.Errorf("duplicate node for %s#%d already exists with UID %s", node.Repo, node.IssueNumber, existing.UID)
		}
		repoMap[node.IssueNumber] = node
	}

	s.nodes[node.UID] = node

	// Update indices
	if node.IssueNumber > 0 {
		extKey := node.CanonicalKey()
		s.forwardIndex[node.UID] = append(s.forwardIndex[node.UID], extKey)
		s.reverseIndex[extKey] = append(s.reverseIndex[extKey], node.UID)
	}

	return nil
}

// UpdateNode updates an existing node's mutable captured fields without changing its UID.
func (s *Store) UpdateNode(node *WorkNode) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.nodes[node.UID]
	if !ok {
		return fmt.Errorf("node with UID %q not found", node.UID)
	}

	existing.Title = node.Title
	existing.State = node.State
	existing.Revision = node.Revision
	existing.CommentCount = node.CommentCount
	existing.Labels = append([]string(nil), node.Labels...)
	existing.URL = node.URL
	existing.Evidence = append(existing.Evidence, node.Evidence...)
	existing.BackPointerReceipt = node.BackPointerReceipt
	return nil
}

// GetNodeByUID returns a node by its UID.
func (s *Store) GetNodeByUID(uid string) (*WorkNode, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	node, ok := s.nodes[uid]
	return node, ok
}

// GetNodeByIssue returns a node by repository and issue number.
func (s *Store) GetNodeByIssue(repo string, number int) (*WorkNode, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	repoMap, ok := s.byRepoAndNum[repo]
	if !ok {
		return nil, false
	}
	node, ok := repoMap[number]
	return node, ok
}

// NodesForRepo returns all nodes imported for a repository, sorted by issue number ascending.
func (s *Store) NodesForRepo(repo string) []*WorkNode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	repoMap, ok := s.byRepoAndNum[repo]
	if !ok {
		return nil
	}
	out := make([]*WorkNode, 0, len(repoMap))
	for _, n := range repoMap {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].IssueNumber < out[j].IssueNumber
	})
	return out
}

// TotalNodeCount returns the total number of nodes in the store.
func (s *Store) TotalNodeCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.nodes)
}

// SaveCheckpoint records a checkpoint for a repository.
func (s *Store) SaveCheckpoint(cp *Checkpoint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cpCopy := *cp
	if cpCopy.UpdatedAt == "" {
		cpCopy.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	s.checkpoints[cp.Repo] = &cpCopy
}

// GetCheckpoint retrieves the checkpoint for a repository.
func (s *Store) GetCheckpoint(repo string) (*Checkpoint, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp, ok := s.checkpoints[repo]
	if !ok {
		return nil, false
	}
	cpCopy := *cp
	return &cpCopy, true
}

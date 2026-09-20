package capture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// ReadOnlyCaptureAdapter defines the read-only interface for forge issue capture.
type ReadOnlyCaptureAdapter interface {
	Capture(ctx context.Context, opts Options) (*Manifest, error)
	ValidateBundle(stagingDir string) (*Manifest, error)

	// Invariant: Strictly read-only! Any outbound mutation must refuse.
	CloseIssue(ctx context.Context, repo string, number int, reason string) error
	AddComment(ctx context.Context, repo string, number int, body string) error
	EditIssue(ctx context.Context, repo string, number int, edits map[string]any) error
	DeleteIssue(ctx context.Context, repo string, number int) error
}

// ghRunner executes a gh command.
type ghRunner func(ctx context.Context, stdin string, args ...string) (string, error)

// GitHubAdapter implements ReadOnlyCaptureAdapter for GitHub.
type GitHubAdapter struct {
	timeout time.Duration
	run     ghRunner
}

// ghBinary holds the path or name of the gh binary.
var ghBinary atomic.Value

func init() {
	ghBinary.Store("gh")
}

// SetGHBinary sets the gh binary path for tests and returns a restore function.
func SetGHBinary(path string) (restore func()) {
	prev := ghBinary.Load().(string)
	ghBinary.Store(path)
	return func() { ghBinary.Store(prev) }
}

func defaultRunGH(ctx context.Context, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, ghBinary.Load().(string), args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(cmd.Environ(), "GH_PAGER=cat", "GH_PROMPT_DISABLED=1", "NO_COLOR=1")
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("gh %s did not finish in time and was killed", strings.Join(args, " "))
	}
	if err != nil {
		return string(out), fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// NewGitHubAdapter creates a new read-only GitHub capture adapter.
func NewGitHubAdapter(timeout time.Duration) *GitHubAdapter {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &GitHubAdapter{
		timeout: timeout,
		run:     defaultRunGH,
	}
}

// SetRunner overrides the runner (useful for hermetic tests).
func (a *GitHubAdapter) SetRunner(r ghRunner) {
	a.run = r
}

// -----------------------------------------------------------------------------
// Invariant: Strictly read-only! Outbound mutations are unconditionally refused.
// -----------------------------------------------------------------------------

func (a *GitHubAdapter) CloseIssue(ctx context.Context, repo string, number int, reason string) error {
	return ErrReadOnly
}

func (a *GitHubAdapter) AddComment(ctx context.Context, repo string, number int, body string) error {
	return ErrReadOnly
}

func (a *GitHubAdapter) EditIssue(ctx context.Context, repo string, number int, edits map[string]any) error {
	return ErrReadOnly
}

func (a *GitHubAdapter) DeleteIssue(ctx context.Context, repo string, number int) error {
	return ErrReadOnly
}

// -----------------------------------------------------------------------------
// Validation & Helpers
// -----------------------------------------------------------------------------

// ParseRepo validates and parses "owner/repo".
func ParseRepo(repo string) (owner, name string, err error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid repo %q: want owner/repo", repo)
	}
	for _, p := range parts {
		if strings.HasPrefix(p, "-") || strings.Trim(p, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.") != "" {
			return "", "", fmt.Errorf("invalid repository part %q", p)
		}
	}
	return parts[0], parts[1], nil
}

var attachmentRe = regexp.MustCompile(`https://github\.com/(?:user-attachments/assets|[a-zA-Z0-9_-]+/[a-zA-Z0-9_.-]+/(?:assets|files))/[a-zA-Z0-9_-]+(?:\.[a-zA-Z0-9]+)?`)

func extractAttachments(text, location string) []CapturedAttachment {
	matches := attachmentRe.FindAllString(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var atts []CapturedAttachment
	for _, m := range matches {
		if !seen[m] {
			seen[m] = true
			atts = append(atts, CapturedAttachment{
				URL:     m,
				Status:  "not fetched",
				FoundIn: location,
			})
		}
	}
	return atts
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// -----------------------------------------------------------------------------
// Capture & Staging
// -----------------------------------------------------------------------------

type ghIssueRaw struct {
	Number    int        `json:"number"`
	NodeID    string     `json:"node_id"`
	ID        int64      `json:"id"`
	Title     string     `json:"title"`
	HTMLURL   string     `json:"html_url"`
	State     string     `json:"state"`
	Body      string     `json:"body"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ClosedAt  *time.Time `json:"closed_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
	Labels []struct {
		Name        string `json:"name"`
		Color       string `json:"color"`
		Description string `json:"description"`
	} `json:"labels"`
	PullRequest *struct {
		HTMLURL string `json:"html_url"`
	} `json:"pull_request,omitempty"`
}

type ghCommentRaw struct {
	ID        int64     `json:"id"`
	HTMLURL   string    `json:"html_url"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

type ghTimelineEventRaw struct {
	Event     string    `json:"event"`
	CreatedAt time.Time `json:"created_at"`
	Source    *struct {
		Issue *struct {
			Number  int    `json:"number"`
			HTMLURL string `json:"html_url"`
			NodeID  string `json:"node_id"`
		} `json:"issue"`
	} `json:"source"`
}

// Capture fetches issues, comments, labels, relationships and stages them under opts.StagingDir.
func (a *GitHubAdapter) Capture(ctx context.Context, opts Options) (*Manifest, error) {
	owner, repo, err := ParseRepo(opts.Repo)
	if err != nil {
		return nil, err
	}
	if opts.StagingDir == "" {
		return nil, fmt.Errorf("staging directory is required")
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = 10 * 1024 * 1024 // 10 MiB default total bound
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = 64 * 1024 // 64 KiB default per-body bound
	}
	if opts.Timeout <= 0 {
		opts.Timeout = a.timeout
	}

	issuesDir := filepath.Join(opts.StagingDir, "issues")
	if err := os.MkdirAll(issuesDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating staging directory: %w", err)
	}

	// Read existing manifest if resuming.
	existingIssues := make(map[int]IssueSummary)
	if opts.Resume {
		if oldM, oerr := a.loadManifest(opts.StagingDir); oerr == nil {
			for _, iss := range oldM.Issues {
				// Verify file still matches
				filePath := filepath.Join(opts.StagingDir, iss.File)
				if data, rerr := os.ReadFile(filePath); rerr == nil {
					if int64(len(data)) == iss.Bytes && sha256Hex(data) == iss.SHA256 {
						existingIssues[iss.Number] = iss
					}
				}
			}
		}
	}

	// Fetch issues list.
	var rawIssues []ghIssueRaw
	var pageMap = make(map[int]int) // number -> page

	if len(opts.Issues) > 0 {
		// Fetch specific issues individually.
		for _, num := range opts.Issues {
			subCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
			out, serr := a.run(subCtx, "", "api", fmt.Sprintf("repos/%s/%s/issues/%d", owner, repo, num))
			cancel()
			if serr != nil {
				return nil, fmt.Errorf("fetching issue #%d: %w", num, serr)
			}
			var iss ghIssueRaw
			if jerr := json.Unmarshal([]byte(out), &iss); jerr != nil {
				return nil, fmt.Errorf("decoding issue #%d: %w", num, jerr)
			}
			rawIssues = append(rawIssues, iss)
			pageMap[iss.Number] = 1
		}
	} else {
		// Paginated fetch of all issues.
		subCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
		out, serr := a.run(subCtx, "", "api", "--paginate",
			fmt.Sprintf("repos/%s/%s/issues?state=all&per_page=100", owner, repo))
		cancel()
		if serr != nil {
			return nil, fmt.Errorf("fetching issues: %w", serr)
		}
		dec := json.NewDecoder(strings.NewReader(out))
		currentPage := 1
		for {
			var page []ghIssueRaw
			if derr := dec.Decode(&page); derr != nil {
				if derr == io.EOF {
					break
				}
				return nil, fmt.Errorf("decoding issues page %d: %w", currentPage, derr)
			}
			for _, iss := range page {
				rawIssues = append(rawIssues, iss)
				pageMap[iss.Number] = currentPage
			}
			currentPage++
		}
	}

	manifest := &Manifest{
		Provider:    ProviderGitHub,
		Host:        HostGitHub,
		Repository:  fmt.Sprintf("%s/%s", owner, repo),
		FetchedAt:   time.Now().UTC(),
		TotalIssues: len(rawIssues),
	}

	var totalBytes int64
	maxPageSeen := 1

	for _, iss := range rawIssues {
		page := pageMap[iss.Number]
		if page > maxPageSeen {
			maxPageSeen = page
		}

		// Check if we can reuse verified staged issue
		if oldSummary, ok := existingIssues[iss.Number]; ok && oldSummary.Revision == iss.UpdatedAt.Format(time.RFC3339) {
			manifest.Issues = append(manifest.Issues, oldSummary)
			totalBytes += oldSummary.Bytes
			if totalBytes > opts.MaxBytes {
				return nil, fmt.Errorf("%w: total staged bytes %d > limit %d", ErrByteBoundExceeded, totalBytes, opts.MaxBytes)
			}
			continue
		}

		// Fetch comments for issue
		var comments []CapturedComment
		var attachments []CapturedAttachment

		// Extract attachments from issue body
		bodyAtts := extractAttachments(iss.Body, "body")
		attachments = append(attachments, bodyAtts...)

		subCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
		cout, cerr := a.run(subCtx, "", "api", "--paginate",
			fmt.Sprintf("repos/%s/%s/issues/%d/comments", owner, repo, iss.Number))
		cancel()
		if cerr == nil && strings.TrimSpace(cout) != "" {
			dec := json.NewDecoder(strings.NewReader(cout))
			for {
				var cpage []ghCommentRaw
				if cderr := dec.Decode(&cpage); cderr != nil {
					if cderr == io.EOF {
						break
					}
					break
				}
				for _, c := range cpage {
					comments = append(comments, CapturedComment{
						ID:        c.ID,
						Author:    c.User.Login,
						CreatedAt: c.CreatedAt,
						UpdatedAt: c.UpdatedAt,
						Body:      c.Body,
						URL:       c.HTMLURL,
					})
					loc := fmt.Sprintf("comment:%d", c.ID)
					attachments = append(attachments, extractAttachments(c.Body, loc)...)
				}
			}
		}

		// Fetch timeline for relationships
		var rels []CapturedRelationship
		if iss.PullRequest != nil && iss.PullRequest.HTMLURL != "" {
			rels = append(rels, CapturedRelationship{
				Kind:      "pull_request",
				Target:    iss.PullRequest.HTMLURL,
				Source:    iss.HTMLURL,
				CreatedAt: iss.CreatedAt,
			})
		}

		subCtx2, cancel2 := context.WithTimeout(ctx, opts.Timeout)
		tout, terr := a.run(subCtx2, "", "api", "--paginate",
			fmt.Sprintf("repos/%s/%s/issues/%d/timeline", owner, repo, iss.Number))
		cancel2()
		if terr == nil && strings.TrimSpace(tout) != "" {
			dec := json.NewDecoder(strings.NewReader(tout))
			for {
				var tpage []ghTimelineEventRaw
				if tderr := dec.Decode(&tpage); tderr != nil {
					if tderr == io.EOF {
						break
					}
					break
				}
				for _, ev := range tpage {
					if ev.Event == "cross-referenced" && ev.Source != nil && ev.Source.Issue != nil {
						rels = append(rels, CapturedRelationship{
							Kind:      "cross-referenced",
							Target:    ev.Source.Issue.HTMLURL,
							Source:    iss.HTMLURL,
							CreatedAt: ev.CreatedAt,
						})
					}
				}
			}
		}

		// Convert labels
		var labels []CapturedLabel
		for _, l := range iss.Labels {
			labels = append(labels, CapturedLabel{
				Name:        l.Name,
				Color:       l.Color,
				Description: l.Description,
			})
		}

		// Check body bounds
		body := iss.Body
		truncated := false
		var truncatedHash string
		var truncatedAt int64

		if opts.MaxBodyBytes > 0 && int64(len(body)) > opts.MaxBodyBytes {
			truncated = true
			truncatedHash = sha256Hex([]byte(body))
			truncatedAt = opts.MaxBodyBytes
			body = body[:opts.MaxBodyBytes]
		}

		captured := CapturedIssue{
			Provider:       ProviderGitHub,
			Host:           HostGitHub,
			Repository:     fmt.Sprintf("%s/%s", owner, repo),
			Number:         iss.Number,
			NodeID:         iss.NodeID,
			RESTID:         iss.ID,
			Title:          iss.Title,
			URL:            iss.HTMLURL,
			Revision:       iss.UpdatedAt.Format(time.RFC3339),
			State:          iss.State,
			Author:         iss.User.Login,
			CreatedAt:      iss.CreatedAt,
			UpdatedAt:      iss.UpdatedAt,
			ClosedAt:       iss.ClosedAt,
			Body:           body,
			Truncated:      truncated,
			TruncatedHash:  truncatedHash,
			TruncatedAt:    truncatedAt,
			Comments:       comments,
			Labels:         labels,
			Relationships:  rels,
			Attachments:    attachments,
			Page:           page,
		}

		payload, merr := json.MarshalIndent(captured, "", "  ")
		if merr != nil {
			return nil, fmt.Errorf("marshaling issue #%d: %w", iss.Number, merr)
		}
		payload = append(payload, '\n')

		issueBytes := int64(len(payload))
		totalBytes += issueBytes
		if totalBytes > opts.MaxBytes {
			return nil, fmt.Errorf("%w: total staged bytes %d > limit %d", ErrByteBoundExceeded, totalBytes, opts.MaxBytes)
		}

		relFile := fmt.Sprintf("issues/issue-%d.json", iss.Number)
		absFile := filepath.Join(opts.StagingDir, relFile)

		// Atomic write
		tmpFile := absFile + ".tmp"
		if werr := os.WriteFile(tmpFile, payload, 0o644); werr != nil {
			return nil, fmt.Errorf("writing %s: %w", relFile, werr)
		}
		if rerr := os.Rename(tmpFile, absFile); rerr != nil {
			return nil, fmt.Errorf("renaming %s: %w", relFile, rerr)
		}

		summary := IssueSummary{
			Number:   iss.Number,
			NodeID:   iss.NodeID,
			RESTID:   iss.ID,
			Revision: iss.UpdatedAt.Format(time.RFC3339),
			URL:      iss.HTMLURL,
			File:     relFile,
			Bytes:    issueBytes,
			SHA256:   sha256Hex(payload),
		}
		manifest.Issues = append(manifest.Issues, summary)
	}

	manifest.PageCount = maxPageSeen
	manifest.StagedBytes = totalBytes

	// Sort issues in manifest by number for deterministic order
	sort.Slice(manifest.Issues, func(i, j int) bool {
		return manifest.Issues[i].Number < manifest.Issues[j].Number
	})

	manifestBytes, merr := json.MarshalIndent(manifest, "", "  ")
	if merr != nil {
		return nil, fmt.Errorf("marshaling manifest: %w", merr)
	}
	manifestBytes = append(manifestBytes, '\n')

	manifestPath := filepath.Join(opts.StagingDir, "manifest.json")
	tmpManifest := manifestPath + ".tmp"
	if werr := os.WriteFile(tmpManifest, manifestBytes, 0o644); werr != nil {
		return nil, fmt.Errorf("writing manifest: %w", werr)
	}
	if rerr := os.Rename(tmpManifest, manifestPath); rerr != nil {
		return nil, fmt.Errorf("renaming manifest: %w", rerr)
	}

	return manifest, nil
}

func (a *GitHubAdapter) loadManifest(stagingDir string) (*Manifest, error) {
	manifestPath := filepath.Join(stagingDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// ValidateBundle checks a staged bundle against all invariants.
func (a *GitHubAdapter) ValidateBundle(stagingDir string) (*Manifest, error) {
	m, err := a.loadManifest(stagingDir)
	if err != nil {
		return nil, fmt.Errorf("cannot read manifest in %s: %w", stagingDir, err)
	}

	// Invariant: manifest totals must match actual issue count
	if m.TotalIssues != len(m.Issues) {
		return nil, fmt.Errorf("%w: manifest declares %d issues but lists %d",
			ErrManifestMismatch, m.TotalIssues, len(m.Issues))
	}

	// Verify files on disk
	issuesDir := filepath.Join(stagingDir, "issues")
	entries, err := os.ReadDir(issuesDir)
	if err != nil {
		return nil, fmt.Errorf("reading issues dir: %w", err)
	}

	jsonCount := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			jsonCount++
		}
	}
	if jsonCount != m.TotalIssues {
		return nil, fmt.Errorf("%w: manifest declares %d issues but %d files exist in %s",
			ErrManifestMismatch, m.TotalIssues, jsonCount, issuesDir)
	}

	// Check pagination continuity
	pageSeen := make(map[int]bool)
	for _, iss := range m.Issues {
		absPath := filepath.Join(stagingDir, iss.File)
		data, rerr := os.ReadFile(absPath)
		if rerr != nil {
			return nil, fmt.Errorf("%w: missing staged file %s", ErrCorruptPayload, iss.File)
		}
		if int64(len(data)) != iss.Bytes {
			return nil, fmt.Errorf("%w: file %s size %d != manifest %d",
				ErrCorruptPayload, iss.File, len(data), iss.Bytes)
		}
		if sha256Hex(data) != iss.SHA256 {
			return nil, fmt.Errorf("%w: file %s checksum mismatch", ErrCorruptPayload, iss.File)
		}

		var parsed CapturedIssue
		if jerr := json.Unmarshal(data, &parsed); jerr != nil {
			return nil, fmt.Errorf("%w: invalid JSON in %s: %v", ErrCorruptPayload, iss.File, jerr)
		}
		if parsed.Page > 0 {
			pageSeen[parsed.Page] = true
		}
	}

	// Page gap check: if maxPage > 1, all pages 1..maxPage must be present
	if m.PageCount > 1 {
		for p := 1; p <= m.PageCount; p++ {
			if !pageSeen[p] {
				return nil, fmt.Errorf("%w: page %d missing among 1..%d", ErrPageGap, p, m.PageCount)
			}
		}
	}

	return m, nil
}

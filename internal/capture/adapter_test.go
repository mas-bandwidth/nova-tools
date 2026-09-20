package capture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGH creates a mock gh runner for testing.
type fakeGH struct {
	mu        sync.Mutex
	responses map[string]string
	calls     []string
}

func newFakeGH() *fakeGH {
	return &fakeGH{responses: make(map[string]string)}
}

func (f *fakeGH) set(endpoint string, response string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[endpoint] = response
}

func (f *fakeGH) run(ctx context.Context, stdin string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	callKey := strings.Join(args, " ")
	f.calls = append(f.calls, callKey)

	// Match endpoint by suffix or substring
	for endpoint, resp := range f.responses {
		if strings.Contains(callKey, endpoint) {
			return resp, nil
		}
	}
	return "[]", nil
}

func (f *fakeGH) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// -----------------------------------------------------------------------------
// Test 1: Invariant - Strictly Read-Only! Refuses Any Outbound Mutation
// -----------------------------------------------------------------------------

func TestReadOnlyEnforcement(t *testing.T) {
	fake := newFakeGH()
	adapter := NewGitHubAdapter(10 * time.Second)
	adapter.SetRunner(fake.run)
	ctx := context.Background()

	// 1. CloseIssue must refuse
	err := adapter.CloseIssue(ctx, "mas-bandwidth/nova-tools", 2080, "completed")
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("CloseIssue err = %v; want %v", err, ErrReadOnly)
	}

	// 2. AddComment must refuse
	err = adapter.AddComment(ctx, "mas-bandwidth/nova-tools", 2080, "test comment")
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("AddComment err = %v; want %v", err, ErrReadOnly)
	}

	// 3. EditIssue must refuse
	err = adapter.EditIssue(ctx, "mas-bandwidth/nova-tools", 2080, map[string]any{"title": "new title"})
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("EditIssue err = %v; want %v", err, ErrReadOnly)
	}

	// 4. DeleteIssue must refuse
	err = adapter.DeleteIssue(ctx, "mas-bandwidth/nova-tools", 2080)
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("DeleteIssue err = %v; want %v", err, ErrReadOnly)
	}

	// Zero outbound subprocess calls occurred
	if count := fake.callCount(); count != 0 {
		t.Fatalf("mutation calls made %d requests; want 0", count)
	}
}

// -----------------------------------------------------------------------------
// Test 2: Byte Bounds & Body Truncation with Hash
// -----------------------------------------------------------------------------

func TestByteBounds(t *testing.T) {
	tmpDir := t.TempDir()
	staging := filepath.Join(tmpDir, "staging")

	fake := newFakeGH()
	longBody := strings.Repeat("A", 2000)
	fake.set("repos/mas-bandwidth/nova-tools/issues?state=all&per_page=100", fmt.Sprintf(`[
		{
			"number": 1,
			"node_id": "I_kw1",
			"id": 101,
			"title": "Issue 1",
			"html_url": "https://github.com/mas-bandwidth/nova-tools/issues/1",
			"state": "open",
			"body": %q,
			"created_at": "2026-09-20T10:00:00Z",
			"updated_at": "2026-09-20T10:00:00Z",
			"user": {"login": "glenn"},
			"labels": []
		}
	]`, longBody))

	adapter := NewGitHubAdapter(10 * time.Second)
	adapter.SetRunner(fake.run)
	ctx := context.Background()

	// MaxBodyBytes = 500: body should be truncated to 500, with hash preserved
	manifest, err := adapter.Capture(ctx, Options{
		Repo:         "mas-bandwidth/nova-tools",
		StagingDir:   staging,
		MaxBodyBytes: 500,
		MaxBytes:     1024 * 1024,
	})
	if err != nil {
		t.Fatalf("Capture failed: %v", err)
	}

	if manifest.TotalIssues != 1 {
		t.Fatalf("total issues = %d; want 1", manifest.TotalIssues)
	}

	// Read staged issue file
	issueFile := filepath.Join(staging, manifest.Issues[0].File)
	data, err := os.ReadFile(issueFile)
	if err != nil {
		t.Fatalf("reading issue file: %v", err)
	}

	var issue CapturedIssue
	if err := json.Unmarshal(data, &issue); err != nil {
		t.Fatalf("unmarshaling issue: %v", err)
	}

	if !issue.Truncated {
		t.Fatalf("issue.Truncated = false; want true")
	}
	if len(issue.Body) != 500 {
		t.Fatalf("issue.Body len = %d; want 500", len(issue.Body))
	}
	if issue.TruncatedAt != 500 {
		t.Fatalf("issue.TruncatedAt = %d; want 500", issue.TruncatedAt)
	}
	expectedHash := sha256Hex([]byte(longBody))
	if issue.TruncatedHash != expectedHash {
		t.Fatalf("issue.TruncatedHash = %s; want %s", issue.TruncatedHash, expectedHash)
	}

	// Total byte bound breach
	staging2 := filepath.Join(tmpDir, "staging2")
	_, err = adapter.Capture(ctx, Options{
		Repo:         "mas-bandwidth/nova-tools",
		StagingDir:   staging2,
		MaxBodyBytes: 500,
		MaxBytes:     100, // Impossibly small total bound
	})
	if !errors.Is(err, ErrByteBoundExceeded) {
		t.Fatalf("expected ErrByteBoundExceeded, got: %v", err)
	}
}

// -----------------------------------------------------------------------------
// Test 3: Pagination Across Multiple Pages & Gap Detection
// -----------------------------------------------------------------------------

func TestPaginationAndGap(t *testing.T) {
	tmpDir := t.TempDir()
	staging := filepath.Join(tmpDir, "staging")

	fake := newFakeGH()
	// Two pages of issues
	page1 := `[
		{
			"number": 1,
			"node_id": "I_kw1",
			"id": 101,
			"title": "Issue 1",
			"html_url": "https://github.com/mas-bandwidth/nova-tools/issues/1",
			"state": "open",
			"body": "Page 1 issue",
			"created_at": "2026-09-20T10:00:00Z",
			"updated_at": "2026-09-20T10:00:00Z",
			"user": {"login": "rowan"},
			"labels": []
		}
	]`
	page2 := `[
		{
			"number": 2,
			"node_id": "I_kw2",
			"id": 102,
			"title": "Issue 2",
			"html_url": "https://github.com/mas-bandwidth/nova-tools/issues/2",
			"state": "open",
			"body": "Page 2 issue",
			"created_at": "2026-09-20T10:05:00Z",
			"updated_at": "2026-09-20T10:05:00Z",
			"user": {"login": "emma"},
			"labels": []
		}
	]`
	// gh api --paginate returns concatenated json arrays
	fake.set("repos/mas-bandwidth/nova-tools/issues?state=all&per_page=100", page1+page2)

	adapter := NewGitHubAdapter(10 * time.Second)
	adapter.SetRunner(fake.run)
	ctx := context.Background()

	manifest, err := adapter.Capture(ctx, Options{
		Repo:       "mas-bandwidth/nova-tools",
		StagingDir: staging,
	})
	if err != nil {
		t.Fatalf("Capture failed: %v", err)
	}

	if manifest.TotalIssues != 2 {
		t.Fatalf("total issues = %d; want 2", manifest.TotalIssues)
	}
	if manifest.PageCount != 2 {
		t.Fatalf("page count = %d; want 2", manifest.PageCount)
	}

	// Validate bundle
	vManifest, err := adapter.ValidateBundle(staging)
	if err != nil {
		t.Fatalf("ValidateBundle failed: %v", err)
	}
	if vManifest.TotalIssues != 2 {
		t.Fatalf("vManifest.TotalIssues = %d; want 2", vManifest.TotalIssues)
	}

	// Tamper: simulate page gap by altering page number of issue 1 to 3
	issue1Path := filepath.Join(staging, manifest.Issues[0].File)
	data, _ := os.ReadFile(issue1Path)
	var iss CapturedIssue
	json.Unmarshal(data, &iss)
	iss.Page = 3 // Now pages seen are 3 and 2, missing page 1!
	tBytes, _ := json.MarshalIndent(iss, "", "  ")
	tBytes = append(tBytes, '\n')
	os.WriteFile(issue1Path, tBytes, 0o644)

	// Update manifest hash/bytes to bypass file corruption check and test page gap
	manifest.PageCount = 3
	manifest.Issues[0].Bytes = int64(len(tBytes))
	manifest.Issues[0].SHA256 = sha256Hex(tBytes)
	mBytes, _ := json.MarshalIndent(manifest, "", "  ")
	os.WriteFile(filepath.Join(staging, "manifest.json"), append(mBytes, '\n'), 0o644)

	_, err = adapter.ValidateBundle(staging)
	if !errors.Is(err, ErrPageGap) {
		t.Fatalf("expected ErrPageGap, got: %v", err)
	}
}

// -----------------------------------------------------------------------------
// Test 4: Resume Capability
// -----------------------------------------------------------------------------

func TestResumeCapability(t *testing.T) {
	tmpDir := t.TempDir()
	staging := filepath.Join(tmpDir, "staging")

	fake := newFakeGH()
	issue1JSON := `{
		"number": 1,
		"node_id": "I_kw1",
		"id": 101,
		"title": "Issue 1",
		"html_url": "https://github.com/mas-bandwidth/nova-tools/issues/1",
		"state": "open",
		"body": "First issue",
		"created_at": "2026-09-20T10:00:00Z",
		"updated_at": "2026-09-20T10:00:00Z",
		"user": {"login": "glenn"},
		"labels": []
	}`
	fake.set("repos/mas-bandwidth/nova-tools/issues?state=all&per_page=100", fmt.Sprintf("[%s]", issue1JSON))
	fake.set("repos/mas-bandwidth/nova-tools/issues/1/comments", `[
		{
			"id": 501,
			"html_url": "https://github.com/mas-bandwidth/nova-tools/issues/1#issuecomment-501",
			"body": "A comment on issue 1",
			"created_at": "2026-09-20T10:01:00Z",
			"updated_at": "2026-09-20T10:01:00Z",
			"user": {"login": "emma"}
		}
	]`)

	adapter := NewGitHubAdapter(10 * time.Second)
	adapter.SetRunner(fake.run)
	ctx := context.Background()

	// Initial capture of issue 1
	m1, err := adapter.Capture(ctx, Options{
		Repo:       "mas-bandwidth/nova-tools",
		StagingDir: staging,
		Resume:     true,
	})
	if err != nil {
		t.Fatalf("Initial capture failed: %v", err)
	}
	if m1.TotalIssues != 1 {
		t.Fatalf("m1.TotalIssues = %d; want 1", m1.TotalIssues)
	}
	initialCalls := fake.callCount()

	// Second run with Issue 1 UNCHANGED and Issue 2 ADDED
	issue2JSON := `{
		"number": 2,
		"node_id": "I_kw2",
		"id": 102,
		"title": "Issue 2",
		"html_url": "https://github.com/mas-bandwidth/nova-tools/issues/2",
		"state": "open",
		"body": "Second issue",
		"created_at": "2026-09-20T10:10:00Z",
		"updated_at": "2026-09-20T10:10:00Z",
		"user": {"login": "rowan"},
		"labels": []
	}`
	fake.set("repos/mas-bandwidth/nova-tools/issues?state=all&per_page=100", fmt.Sprintf("[%s, %s]", issue1JSON, issue2JSON))

	m2, err := adapter.Capture(ctx, Options{
		Repo:       "mas-bandwidth/nova-tools",
		StagingDir: staging,
		Resume:     true,
	})
	if err != nil {
		t.Fatalf("Resumed capture failed: %v", err)
	}
	if m2.TotalIssues != 2 {
		t.Fatalf("m2.TotalIssues = %d; want 2", m2.TotalIssues)
	}

	// Verify Issue 1 comments were NOT re-fetched during resume
	// In the second run, we should see:
	// 1 call for issues list, 1 call for issue 2 comments, 1 call for issue 2 timeline.
	// We should NOT see new calls for issue 1 comments or timeline!
	for _, call := range fake.calls[initialCalls:] {
		if strings.Contains(call, "issues/1/comments") || strings.Contains(call, "issues/1/timeline") {
			t.Fatalf("resumed run re-fetched comments/timeline for unchanged issue 1: %s", call)
		}
	}

	// Verify bundle validity after resume
	vManifest, err := adapter.ValidateBundle(staging)
	if err != nil {
		t.Fatalf("ValidateBundle after resume failed: %v", err)
	}
	if vManifest.TotalIssues != 2 {
		t.Fatalf("vManifest.TotalIssues = %d; want 2", vManifest.TotalIssues)
	}
}

// -----------------------------------------------------------------------------
// Test 5: Manifest Tampering / Mismatch Detection
// -----------------------------------------------------------------------------

func TestManifestMismatchDetection(t *testing.T) {
	tmpDir := t.TempDir()
	staging := filepath.Join(tmpDir, "staging")

	fake := newFakeGH()
	fake.set("repos/mas-bandwidth/nova-tools/issues?state=all&per_page=100", `[
		{
			"number": 1,
			"node_id": "I_kw1",
			"id": 101,
			"title": "Issue 1",
			"html_url": "https://github.com/mas-bandwidth/nova-tools/issues/1",
			"state": "open",
			"body": "Test",
			"created_at": "2026-09-20T10:00:00Z",
			"updated_at": "2026-09-20T10:00:00Z",
			"user": {"login": "glenn"},
			"labels": []
		}
	]`)

	adapter := NewGitHubAdapter(10 * time.Second)
	adapter.SetRunner(fake.run)
	ctx := context.Background()

	manifest, err := adapter.Capture(ctx, Options{
		Repo:       "mas-bandwidth/nova-tools",
		StagingDir: staging,
	})
	if err != nil {
		t.Fatalf("Capture failed: %v", err)
	}

	// Tamper 1: modify manifest total_issues to 2 (while only 1 exists)
	manifest.TotalIssues = 2
	mBytes, _ := json.MarshalIndent(manifest, "", "  ")
	os.WriteFile(filepath.Join(staging, "manifest.json"), append(mBytes, '\n'), 0o644)

	_, err = adapter.ValidateBundle(staging)
	if !errors.Is(err, ErrManifestMismatch) {
		t.Fatalf("expected ErrManifestMismatch, got: %v", err)
	}

	// Tamper 2: corrupt issue file content (checksum mismatch)
	manifest.TotalIssues = 1
	mBytes, _ = json.MarshalIndent(manifest, "", "  ")
	os.WriteFile(filepath.Join(staging, "manifest.json"), append(mBytes, '\n'), 0o644)

	issueFile := filepath.Join(staging, manifest.Issues[0].File)
	os.WriteFile(issueFile, []byte("corrupted"), 0o644)

	_, err = adapter.ValidateBundle(staging)
	if !errors.Is(err, ErrCorruptPayload) {
		t.Fatalf("expected ErrCorruptPayload, got: %v", err)
	}
}

// -----------------------------------------------------------------------------
// Test 6: Preservation of Stable Identity, Relationships & Attachments
// -----------------------------------------------------------------------------

func TestStableIdentityAndCorrespondence(t *testing.T) {
	tmpDir := t.TempDir()
	staging := filepath.Join(tmpDir, "staging")

	fake := newFakeGH()
	fake.set("repos/mas-bandwidth/nova-tools/issues?state=all&per_page=100", `[
		{
			"number": 2080,
			"node_id": "I_kwDOTxuLoc8AAAABSPEgTQ",
			"id": 5518729293,
			"title": "nova-work E09-F01",
			"html_url": "https://github.com/mas-bandwidth/nova-tools/issues/2080",
			"state": "open",
			"body": "Here is an image: https://github.com/user-attachments/assets/abcd-1234.png and text.",
			"created_at": "2026-09-20T11:00:00Z",
			"updated_at": "2026-09-20T11:30:00Z",
			"user": {"login": "rowan-claude"},
			"labels": [
				{"name": "enhancement", "color": "a2eeef", "description": "New feature"}
			]
		}
	]`)
	fake.set("repos/mas-bandwidth/nova-tools/issues/2080/comments", `[
		{
			"id": 9991,
			"html_url": "https://github.com/mas-bandwidth/nova-tools/issues/2080#issuecomment-9991",
			"body": "Another attachment: https://github.com/mas-bandwidth/nova-tools/assets/5678/shot.jpg",
			"created_at": "2026-09-20T11:05:00Z",
			"updated_at": "2026-09-20T11:05:00Z",
			"user": {"login": "emma"}
		}
	]`)
	fake.set("repos/mas-bandwidth/nova-tools/issues/2080/timeline", `[
		{
			"event": "cross-referenced",
			"created_at": "2026-09-20T11:10:00Z",
			"source": {
				"issue": {
					"number": 2085,
					"html_url": "https://github.com/mas-bandwidth/nova-tools/issues/2085",
					"node_id": "I_kwDOTxuLoc8AAAABSPEgTQ"
				}
			}
		}
	]`)

	adapter := NewGitHubAdapter(10 * time.Second)
	adapter.SetRunner(fake.run)
	ctx := context.Background()

	manifest, err := adapter.Capture(ctx, Options{
		Repo:       "mas-bandwidth/nova-tools",
		StagingDir: staging,
	})
	if err != nil {
		t.Fatalf("Capture failed: %v", err)
	}

	// 1. Check Stable Identity
	summary := manifest.Issues[0]
	if summary.Number != 2080 {
		t.Errorf("summary.Number = %d; want 2080", summary.Number)
	}
	if summary.NodeID != "I_kwDOTxuLoc8AAAABSPEgTQ" {
		t.Errorf("summary.NodeID = %s; want I_kwDOTxuLoc8AAAABSPEgTQ", summary.NodeID)
	}
	if summary.RESTID != 5518729293 {
		t.Errorf("summary.RESTID = %d; want 5518729293", summary.RESTID)
	}
	if summary.Revision != "2026-09-20T11:30:00Z" {
		t.Errorf("summary.Revision = %s; want 2026-09-20T11:30:00Z", summary.Revision)
	}
	if summary.URL != "https://github.com/mas-bandwidth/nova-tools/issues/2080" {
		t.Errorf("summary.URL = %s", summary.URL)
	}

	// 2. Read full issue and check preserved correspondence
	data, err := os.ReadFile(filepath.Join(staging, summary.File))
	if err != nil {
		t.Fatalf("reading issue file: %v", err)
	}

	var issue CapturedIssue
	if err := json.Unmarshal(data, &issue); err != nil {
		t.Fatalf("unmarshaling issue: %v", err)
	}

	if issue.Provider != "github" || issue.Host != "github.com" {
		t.Errorf("provider/host = %s/%s; want github/github.com", issue.Provider, issue.Host)
	}
	if len(issue.Comments) != 1 || issue.Comments[0].Author != "emma" {
		t.Errorf("comments mismatch: %+v", issue.Comments)
	}
	if len(issue.Labels) != 1 || issue.Labels[0].Name != "enhancement" {
		t.Errorf("labels mismatch: %+v", issue.Labels)
	}
	if len(issue.Relationships) != 1 || issue.Relationships[0].Kind != "cross-referenced" {
		t.Errorf("relationships mismatch: %+v", issue.Relationships)
	}
	if len(issue.Attachments) != 2 {
		t.Fatalf("attachments count = %d; want 2", len(issue.Attachments))
	}
	for _, att := range issue.Attachments {
		if att.Status != "not fetched" {
			t.Errorf("attachment status = %s; want 'not fetched'", att.Status)
		}
	}
}

//go:build functional

package stream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/webhook"
)

// adminMergeForge simulates GitHub for stream PR landing (#4386):
// verifies the stream PR is merged at the exact proved head SHA via admin REST PUT,
// and fails if merge_group, check-runs, workflow-runs or GraphQL are touched.
type adminMergeForge struct {
	mu        sync.Mutex
	t         *testing.T
	repo      string
	pr        int
	head      string
	mergeSHA  string
	merged    bool
	dirty     bool
	closed    bool
	wrongHead string
	merges    []map[string]string
	calls     []string
}

func newAdminMergeForge(t *testing.T, repo string, pr int, head, mergeSHA string) *adminMergeForge {
	return &adminMergeForge{
		t:        t,
		repo:     repo,
		pr:       pr,
		head:     head,
		mergeSHA: mergeSHA,
	}
}

func (f *adminMergeForge) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)

		// Verification: no merge_group polling, no check-runs or workflow-runs polling, no GraphQL.
		if strings.Contains(r.URL.Path, "merge_group") || strings.Contains(r.URL.RawQuery, "merge_group") {
			f.t.Fatalf("merge_group was polled or accessed: %s %s", r.Method, r.URL)
		}
		if strings.Contains(r.URL.Path, "check-runs") || strings.Contains(r.URL.Path, "check-suites") || strings.Contains(r.URL.Path, "actions/runs") {
			f.t.Fatalf("check state polled from GitHub instead of Redis: %s %s", r.Method, r.URL)
		}
		if strings.Contains(r.URL.Path, "graphql") {
			f.t.Fatalf("GraphQL accessed: %s %s", r.Method, r.URL)
		}

		prPath := fmt.Sprintf("/repos/%s/pulls/%d", f.repo, f.pr)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == fmt.Sprintf("/repos/%s/pulls", f.repo):
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"number": f.pr})
		case r.Method == http.MethodGet && r.URL.Path == prPath:
			h := f.head
			if f.wrongHead != "" {
				h = f.wrongHead
			}
			state := "open"
			if f.closed {
				state = "closed"
			}
			mergeableState := "clean"
			if f.dirty {
				mergeableState = "dirty"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"number":           f.pr,
				"state":            state,
				"merged":           f.merged,
				"merge_commit_sha": f.mergeSHA,
				"mergeable_state":  mergeableState,
				"head":             map[string]any{"sha": h, "ref": "stream/quack"},
				"base":             map[string]any{"ref": "dev"},
				"body":             "STREAM: quack",
			})
		case r.Method == http.MethodPut && r.URL.Path == prPath+"/merge":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.merges = append(f.merges, body)
			if body["sha"] != f.head {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]any{"message": "Head branch was modified"})
				return
			}
			f.merged = true
			_ = json.NewEncoder(w).Encode(map[string]any{"sha": f.mergeSHA, "merged": true})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comments"):
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, fmt.Sprintf("/repos/%s/pulls/", f.repo)):
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "closed"})
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, fmt.Sprintf("/repos/%s/issues/", f.repo)):
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "closed"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, fmt.Sprintf("/repos/%s/pulls/", f.repo)):
			_ = json.NewEncoder(w).Encode(map[string]any{"body": "Closes #10"})
		default:
			f.t.Errorf("unexpected call %s %s", r.Method, r.URL)
			http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
		}
	})
}

// TestStreamPRMergeAdminAtProvedSHANoMergeQueue tests Requirement 1 & 2:
// - Verifies CI ci-ok verdict from Redis at the proved SHA.
// - Executes the admin PUT merge at the head SHA directly via REST (gh.MergePR), without waiting on GitHub merge_group.
func TestStreamPRMergeAdminAtProvedSHANoMergeQueue(t *testing.T) {
	t.Parallel()

	c := newRedis(t)
	ctx := context.Background()

	head := strings.Repeat("a", 40)
	mergeSHA := strings.Repeat("m", 40)
	prNum := 99
	repoName := repo

	forge := newAdminMergeForge(t, repoName, prNum, head, mergeSHA)
	srv := httptest.NewServer(forge.handler())
	t.Cleanup(srv.Close)

	gh := &GitHub{API: srv.URL, Token: "tok-test", HTTP: srv.Client()}

	// Seed one member in merging
	memberHead := strings.Repeat("b", 40)
	seed(t, c, 1, memberHead, 100, score("rowan", memberHead, 10))

	// Record the built landing and stream PR
	l := Landing{
		Repo:    repoName,
		Slug:    "quack",
		Streams: "quack",
		Base:    "dev",
		BaseSHA: strings.Repeat("0", 40),
		Branch:  "stream/quack",
		Head:    head,
		State:   "open",
		PR:      prNum,
		Members: []Member{{N: 1, Head: memberHead, Task: "t1"}},
	}
	if _, err := SaveBuilt(ctx, c, l, "rowan"); err != nil {
		t.Fatal(err)
	}
	if _, err := Record(ctx, c, repoName, prNum, RecordFields{Head: head, Base: "dev", Stream: "quack", Mergeable: "true"}); err != nil {
		t.Fatal(err)
	}

	// Case 1: ci-ok in Redis is pending; Merge refuses without calling PUT merge.
	o := MergeOptions{Repo: repoName, Streams: []string{"quack"}, By: "rowan", GH: gh}
	rep, err := Merge(ctx, c, o)
	if err == nil || !strings.Contains(err.Error(), "REFUSED ci=pending") {
		t.Fatalf("expected REFUSED ci=pending, got %v rep=%+v", err, rep)
	}
	if len(forge.merges) != 0 {
		t.Fatalf("PUT merge was called while CI pending: %v", forge.merges)
	}

	// Case 2: ci-ok verdict in Redis is green at the proved SHA (webhook.Key).
	// Stream PR record r.CI is still pending, but ci-ok at head is green!
	if err := c.HSet(ctx, webhook.Key(repoName, head), "gh", "green", "check:ci-ok", "green 1 2026-09-26T12:00:00Z").Err(); err != nil {
		t.Fatal(err)
	}

	rep, err = Merge(ctx, c, o)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	if rep.MergeSHA != mergeSHA || rep.Already {
		t.Fatalf("rep: %+v, want MergeSHA=%s already=false", rep, mergeSHA)
	}
	if len(forge.merges) != 1 {
		t.Fatalf("expected 1 merge call, got %d", len(forge.merges))
	}
	if forge.merges[0]["sha"] != head {
		t.Fatalf("merge sha = %q, want proved head %q", forge.merges[0]["sha"], head)
	}
	if forge.merges[0]["merge_method"] != "merge" {
		t.Fatalf("merge_method = %q, want merge", forge.merges[0]["merge_method"])
	}

	// Case 3: Re-run when PR is already merged: settles without another PUT merge.
	rep, err = Merge(ctx, c, o)
	if err != nil {
		t.Fatalf("re-run Merge failed: %v", err)
	}
	if !rep.Already || rep.MergeSHA != mergeSHA {
		t.Fatalf("re-run rep: %+v, want already=true MergeSHA=%s", rep, mergeSHA)
	}
	if len(forge.merges) != 1 {
		t.Fatalf("merge call was repeated: %d", len(forge.merges))
	}
}

// TestStreamMergeRefusals tests the safety checks of the land pr path:
// red CI, dirty mergeable state, moved head on GitHub.
func TestStreamMergeRefusals(t *testing.T) {
	t.Parallel()

	c := newRedis(t)
	ctx := context.Background()

	head := strings.Repeat("c", 40)
	mergeSHA := strings.Repeat("d", 40)
	prNum := 101
	repoName := repo

	forge := newAdminMergeForge(t, repoName, prNum, head, mergeSHA)
	srv := httptest.NewServer(forge.handler())
	t.Cleanup(srv.Close)

	gh := &GitHub{API: srv.URL, Token: "tok-test", HTTP: srv.Client()}
	memberHead := strings.Repeat("e", 40)
	seed(t, c, 2, memberHead, 100, score("rowan", memberHead, 10))

	l := Landing{
		Repo:    repoName,
		Slug:    "quack",
		Streams: "quack",
		Base:    "dev",
		Branch:  "stream/quack",
		Head:    head,
		State:   "open",
		PR:      prNum,
		Members: []Member{{N: 2, Head: memberHead, Task: "t2"}},
	}
	if _, err := SaveBuilt(ctx, c, l, "rowan"); err != nil {
		t.Fatal(err)
	}
	if _, err := Record(ctx, c, repoName, prNum, RecordFields{Head: head, Base: "dev", Stream: "quack", Mergeable: "true"}); err != nil {
		t.Fatal(err)
	}

	o := MergeOptions{Repo: repoName, Streams: []string{"quack"}, By: "rowan", GH: gh}

	// Subtest 1: Redis ci-ok is red
	if err := c.HSet(ctx, webhook.Key(repoName, head), "gh", "red", "gh_fail", "check:test").Err(); err != nil {
		t.Fatal(err)
	}
	_, err := Merge(ctx, c, o)
	if err == nil || !strings.Contains(err.Error(), "REFUSED ci=red") {
		t.Fatalf("expected REFUSED ci=red, got %v", err)
	}

	// Subtest 2: Redis ci-ok is green, but PR on GitHub is dirty (conflict with base)
	if err := c.HSet(ctx, webhook.Key(repoName, head), "gh", "green").Err(); err != nil {
		t.Fatal(err)
	}
	forge.dirty = true
	_, err = Merge(ctx, c, o)
	if err == nil || !strings.Contains(err.Error(), "conflict with base (mergeable_state=dirty)") {
		t.Fatalf("expected conflict with base, got %v", err)
	}
	forge.dirty = false

	// Subtest 3: PR on GitHub has moved head
	forge.wrongHead = strings.Repeat("f", 40)
	_, err = Merge(ctx, c, o)
	if err == nil || !strings.Contains(err.Error(), "is not stream head") {
		t.Fatalf("expected head mismatch refusal, got %v", err)
	}
}

// TestStreamRunAdminMergeOutputsLandedTotalMS tests stream.Run end-to-end:
// verifies ci-ok from Redis is checked, admin REST merge at proved sha is executed,
// merge_group is not polled, and LANDED n= total_ms= line with step walls is output in the log.
func TestStreamRunAdminMergeOutputsLandedTotalMS(t *testing.T) {
	t.Parallel()

	c := newRedis(t)
	ctx := context.Background()

	f := newFixture(t)
	prNum := 200
	mergeSHA := strings.Repeat("7", 40)
	repoName := repo

	// Seed member 1 (one.txt, green)
	seed(t, c, 1, f.Head[1], 100, score("rowan", f.Head[1], 10))

	var forge *adminMergeForge
	var capturedHead string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if forge != nil {
			forge.handler().ServeHTTP(w, r)
			return
		}
		// If forge not yet initialized with head, handle initial OpenPR
		if r.Method == http.MethodPost && r.URL.Path == fmt.Sprintf("/repos/%s/pulls", repoName) {
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"number": prNum})
			return
		}
		http.Error(w, "not ready", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	gh := &GitHub{API: srv.URL, Token: "tok-run", HTTP: srv.Client()}

	var logBuf bytes.Buffer
	t0 := time.Now()
	clock := func() time.Time {
		t0 = t0.Add(50 * time.Millisecond)
		return t0
	}

	o := RunOptions{
		Options: Options{
			Repo:     repoName,
			Streams:  []string{strm},
			Base:     "dev",
			Remote:   f.URL,
			Workdir:  filepath.Join(t.TempDir(), "workdir"),
			NoTest:   true,
			MinScore: 8,
			By:       "rowan",
			Log:      &logBuf,
			GH:       gh,
			Now:      clock,
		},
		CIWait: 10 * time.Second,
		Tick:   10 * time.Millisecond,
		Request: func(ctx context.Context, r, sha string, pr int, url string) (string, error) {
			capturedHead = sha
			forge = newAdminMergeForge(t, repoName, prNum, sha, mergeSHA)
			// Simulate runner receipt writing ci-ok verdict to Redis at proved sha
			return "CREATED", c.HSet(ctx, webhook.Key(r, sha), "gh", "green", "check:ci-ok", "green 1").Err()
		},
		BaseTip: func(ctx context.Context, remote, base string) (string, error) {
			return f.Base, nil
		},
	}

	rep, err := Run(ctx, c, o)
	if err != nil {
		t.Fatalf("Run failed: %v\nlog:\n%s", err, logBuf.String())
	}
	if rep.State != "landed" {
		t.Fatalf("state = %q, want landed", rep.State)
	}
	if rep.Merge.MergeSHA != mergeSHA {
		t.Fatalf("MergeSHA = %q, want %q", rep.Merge.MergeSHA, mergeSHA)
	}
	if rep.TotalMS <= 0 {
		t.Fatalf("rep.TotalMS = %d, want > 0", rep.TotalMS)
	}

	logOut := logBuf.String()
	// Verify step walls in the log
	for _, expected := range []string{
		"PUSHED stream/landing-streams-lander head=",
		"PR #200 opened base=dev members=1 ms=",
		"BUILT stream/landing-streams-lander head=",
		"CI " + short(capturedHead) + " green pr=#200 request=CREATED ms=",
		"MERGED " + short(mergeSHA) + " pr=#200 already=false ms=",
		"LANDED n=1 stream=landing-streams-lander moved=1 total_ms=",
	} {
		if !strings.Contains(logOut, expected) {
			t.Fatalf("log missing expected step wall line %q:\n%s", expected, logOut)
		}
	}

	// Verify forge received PUT merge at the proved SHA
	if len(forge.merges) != 1 || forge.merges[0]["sha"] != capturedHead {
		t.Fatalf("merges: %v, want sha=%s", forge.merges, capturedHead)
	}
}

package land_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

type fixtureFiler struct {
	mu    sync.Mutex
	files int
	err   error
}

func (f *fixtureFiler) File(context.Context, string, string, string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files++
	if f.err != nil {
		return 0, f.err
	}
	return 42, nil
}
func (*fixtureFiler) Find(context.Context, string, string, time.Time) (int, bool, error) {
	return 0, false, nil
}

func TestFlakyObserveRedis(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	st := land.NewRedisStore(c, "c42r")
	filer := &fixtureFiler{}
	obs := land.Observation{Repo: "nova-tools", Key: "flaky:nova-tools:internal/deal.TestProbe3098", Lane: "lane-a", Title: "flaky", Body: "dedup=flaky:nova-tools:internal/deal.TestProbe3098\n"}
	r, filed, err := st.ObserveLive(ctx, obs, filer)
	if err != nil || !filed || r.Status != "FILED" || r.Issue != 42 {
		t.Fatalf("first=%+v filed=%t err=%v", r, filed, err)
	}
	obs.Lane = "lane-b"
	r, filed, err = st.ObserveLive(ctx, obs, filer)
	if err != nil || filed || r.Status != "SEEN" || r.LanesHit != 2 || r.LastLane != "lane-b" {
		t.Fatalf("second=%+v filed=%t err=%v", r, filed, err)
	}
	if filer.files != 1 {
		t.Fatalf("File calls=%d want 1", filer.files)
	}
	for _, key := range []string{"flaky:intent:nova-tools:internal/deal.TestProbe3098", "flaky:lock:nova-tools:internal/deal.TestProbe3098", "lock:flaky:nova-tools:internal/deal.TestProbe3098"} {
		if n := c.Exists(ctx, key).Val(); n != 0 {
			t.Fatalf("%s exists", key)
		}
	}

	rejected := obs
	rejected.Key = "flaky:nova-tools:internal/deal.TestRejected3098"
	bad := &fixtureFiler{err: &land.HTTPStatusError{Code: 422, Body: "rejected"}}
	if _, _, err := st.ObserveLive(ctx, rejected, bad); err == nil {
		t.Fatal("422 observe succeeded")
	}
	for _, key := range []string{rejected.Key, "flaky:intent:nova-tools:internal/deal.TestRejected3098", "flaky:lock:nova-tools:internal/deal.TestRejected3098"} {
		if c.Exists(ctx, key).Val() != 0 {
			t.Fatalf("422 left %s", key)
		}
	}

	concurrent := obs
	concurrent.Key = "flaky:nova-tools:internal/deal.TestConcurrent3098"
	one := &fixtureFiler{}
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() { defer wg.Done(); _, _, _ = st.ObserveLive(ctx, concurrent, one) }()
	}
	wg.Wait()
	one.mu.Lock()
	calls := one.files
	one.mu.Unlock()
	if calls != 1 {
		t.Fatalf("concurrent File calls=%d want 1", calls)
	}
}

func TestRESTFilerFind(t *testing.T) {
	t.Parallel()

	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode([]map[string]any{{"number": 9, "body": "dedup=k\nbody", "pull_request": map[string]any{}}, {"number": 7, "body": "dedup=k\nbody"}})
	}))
	defer srv.Close()
	n, ok, err := (land.RESTFiler{BaseURL: srv.URL, HTTP: srv.Client()}).Find(context.Background(), "acme/repo", "dedup=k", time.Unix(1, 0))
	if err != nil || !ok || n != 7 {
		t.Fatalf("Find=%d,%t,%v", n, ok, err)
	}
	if gotPath != "/repos/acme/repo/issues" || !strings.Contains(gotQuery, "per_page=100") || !strings.Contains(gotQuery, "since=") {
		t.Fatalf("request %s?%s", gotPath, gotQuery)
	}
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}

func TestMirrorForgeMergeable(t *testing.T) {
	t.Parallel()

	d := t.TempDir()
	gitRun(t, d, "init", "-q")
	gitRun(t, d, "switch", "-c", "dev")
	if err := os.WriteFile(filepath.Join(d, "f"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, d, "add", "f")
	gitRun(t, d, "commit", "-qm", "base")
	base := gitRun(t, d, "rev-parse", "HEAD")
	gitRun(t, d, "switch", "-c", "clean")
	if err := os.WriteFile(filepath.Join(d, "g"), []byte("clean\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, d, "add", "g")
	gitRun(t, d, "commit", "-qm", "clean")
	clean := gitRun(t, d, "rev-parse", "HEAD")
	gitRun(t, d, "update-ref", "refs/nova-sprint/pull/1/head", clean)
	gitRun(t, d, "switch", "dev")
	if err := os.WriteFile(filepath.Join(d, "f"), []byte("dev\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, d, "commit", "-am", "dev")
	gitRun(t, d, "switch", "--detach", base)
	if err := os.WriteFile(filepath.Join(d, "f"), []byte("pr\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, d, "commit", "-am", "pr")
	conflict := gitRun(t, d, "rev-parse", "HEAD")
	gitRun(t, d, "update-ref", "refs/nova-sprint/pull/2/head", conflict)
	fetches := 0
	f := land.MirrorForge{GitDir: d, Base: "dev", Fetch: func(context.Context, string, int) error { fetches++; return nil }}
	if got, err := f.Mergeable(context.Background(), "repo", 1); err != nil || got != "MERGEABLE" {
		t.Fatalf("clean=%s err=%v", got, err)
	}
	if got, err := f.Mergeable(context.Background(), "repo", 2); err != nil || got != "CONFLICTING" {
		t.Fatalf("conflict=%s err=%v", got, err)
	}
	if got, err := f.Mergeable(context.Background(), "repo", 3); err != nil || got != "UNKNOWN" {
		t.Fatalf("missing=%s err=%v", got, err)
	}
	if fetches != 3 {
		t.Fatalf("fetches=%d want one per read", fetches)
	}
}

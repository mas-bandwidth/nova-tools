package land_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
)

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

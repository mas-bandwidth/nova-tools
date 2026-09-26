package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

const (
	lsRepo   = "mas-bandwidth/nova-tools"
	lsStream = "landing: streams + lander"
	lsSlug   = "landing-streams-lander"
)

func lsGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// fakeGitHub records every REST call and answers the five the lander makes
// (a member's body closes issue 10<n>).
type fakeGitHub struct {
	mu    sync.Mutex
	calls []string
	srv   *httptest.Server
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	g := &fakeGitHub{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		g.mu.Lock()
		g.calls = append(g.calls, r.Method+" "+r.URL.Path+" "+body["head"]+body["sha"]+body["body"]+body["state"])
		g.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/"+lsRepo+"/pulls":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number":900}`))
		case r.Method == http.MethodPut && r.URL.Path == "/repos/"+lsRepo+"/pulls/900/merge":
			_, _ = w.Write([]byte(`{"merged":true,"sha":"` + strings.Repeat("d", 40) + `"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comments"):
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPatch:
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/"+lsRepo+"/pulls/"):
			n := strings.TrimPrefix(r.URL.Path, "/repos/"+lsRepo+"/pulls/")
			_, _ = w.Write([]byte(`{"number":` + n + `,"body":"STREAM: x\n\nCloses #10` + n + `"}`))
		default:
			http.Error(w, `{"message":"unexpected"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGitHub) Calls() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.calls...)
}

func TestLandStreamConflictStops(t *testing.T) {
	mr := miniredis.RunT(t)
	addr := mr.Addr()
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	root := t.TempDir()
	src, bare := filepath.Join(root, "src"), filepath.Join(root, "remote.git")
	_ = os.MkdirAll(src, 0o755)
	lsGit(t, src, "init", "-q", "-b", "dev")
	_ = os.WriteFile(filepath.Join(src, "a.txt"), []byte("base\n"), 0o644)
	lsGit(t, src, "add", ".")
	lsGit(t, src, "commit", "-q", "-m", "base")
	heads := map[int]string{}
	for n, body := range map[int]string{1: "one\n", 2: "two\n"} {
		lsGit(t, src, "checkout", "-q", "-b", fmt.Sprintf("m%d", n), "dev")
		_ = os.WriteFile(filepath.Join(src, "a.txt"), []byte(body), 0o644)
		lsGit(t, src, "commit", "-q", "-am", body)
		heads[n] = lsGit(t, src, "rev-parse", "HEAD")
		lsGit(t, src, "checkout", "-q", "dev")
	}
	lsGit(t, root, "clone", "-q", "--bare", src, bare)
	for n, h := range heads {
		lsGit(t, bare, "update-ref", fmt.Sprintf("refs/pull/%d/head", n), h)
		id := fmt.Sprintf("t%d", n)
		c.ZAdd(ctx, "ws:"+lsStream+":merging", redis.Z{Score: float64(n), Member: id})
		c.HSet(ctx, "task:"+id, "pr", fmt.Sprint(n))
		runSprint("pr", "record", "--redis", addr, "--repo", lsRepo, "--n", fmt.Sprint(n), "--head", h, "--base", "dev", "--stream", lsStream)
		runSprint("pr", "lines", "--redis", addr, "--repo", lsRepo, "--n", fmt.Sprint(n), "--add", "SCORE who=emma head="+h+" score=9/10")
	}
	gh := newFakeGitHub(t)
	prev := landStreamToken
	landStreamToken = func() (string, error) { return "test-token", nil }
	t.Cleanup(func() { landStreamToken = prev })
	work := filepath.Join(t.TempDir(), "clone")
	code, out, errOut := runSprint("land", "stream", "--redis", addr, "--repo", lsRepo, "--stream", lsStream,
		"--remote", "file://"+bare, "--mirror", "none", "--workdir", work, "--api", gh.srv.URL, "--test", "true")
	if code != 1 || !strings.Contains(out, "LAND STREAM CONFLICT repo="+lsRepo+" stream="+lsSlug+" member=#2 files=a.txt merged_before=1 workdir="+work) {
		t.Fatalf("conflict: %d\n%s\n%s", code, out, errOut)
	}
	if len(gh.Calls()) != 0 {
		t.Fatalf("a conflict called GitHub: %v", gh.Calls())
	}
	if _, err := os.Stat(work); err != nil {
		t.Fatalf("the workdir is Rowan's to resolve in; it was removed: %v", err)
	}
	if st, _ := c.HGet(ctx, "land:"+lsRepo+":"+lsSlug, "state").Result(); st != "conflict" {
		t.Fatalf("land state %q", st)
	}
	if n, _ := c.ZCard(ctx, "ws:"+lsStream+":merging").Result(); n != 2 {
		t.Fatalf("a conflict moved members: merging=%d", n)
	}
}

func TestLandVerbsRefuseUsage(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"land", "stream"},
		{"land", "stream", "--repo", "nova-tools", "--stream", "s", "--redis", "127.0.0.1:1"},
		{"land", "merge", "--repo", lsRepo},
		{"land", "status", "--repo", "x"},
		{"pr"},
		{"pr", "record", "--repo", lsRepo, "--n", "1", "--ci", "blue", "--redis", "127.0.0.1:1"},
		{"pr", "record", "--repo", lsRepo, "--n", "1", "--head", "xyz", "--redis", "127.0.0.1:1"},
		{"pr", "lines", "--repo", lsRepo, "--n", "1", "--redis", "127.0.0.1:1"},
	} {
		if code, _, errOut := runSprint(args...); code != 2 || strings.Count(errOut, "\n") != 1 {
			t.Errorf("%v: exit %d, stderr %q", args, code, errOut)
		}
	}
	// A new record without head/base/stream is refused by the script.
	mr := miniredis.RunT(t)
	if code, _, errOut := runSprint("pr", "record", "--redis", mr.Addr(), "--repo", lsRepo, "--n", "5", "--ci", "green"); code != 2 || !strings.Contains(errOut, "REFUSED no record pr:nova-tools:5") {
		t.Fatalf("new record without head: %d %s", code, errOut)
	}
}

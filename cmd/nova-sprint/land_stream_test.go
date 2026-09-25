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

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
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

// lsFixture is a bare repo with dev and refs/pull/<n>/head: 1 and 3 add a
// file each (green), 2 adds red.txt (red under the fixture test).
func lsFixture(t *testing.T) (url string, bare string, heads map[int]string) {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	bare = filepath.Join(root, "remote.git")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	lsGit(t, src, "init", "-q", "-b", "dev")
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lsGit(t, src, "add", ".")
	lsGit(t, src, "commit", "-q", "-m", "base")
	heads = map[int]string{}
	for n, file := range map[int]string{1: "one.txt", 2: "red.txt", 3: "three.txt"} {
		lsGit(t, src, "checkout", "-q", "-b", fmt.Sprintf("m%d", n), "dev")
		if err := os.WriteFile(filepath.Join(src, file), []byte(file+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		lsGit(t, src, "add", ".")
		lsGit(t, src, "commit", "-q", "-m", file)
		heads[n] = lsGit(t, src, "rev-parse", "HEAD")
		lsGit(t, src, "checkout", "-q", "dev")
	}
	lsGit(t, root, "clone", "-q", "--bare", src, bare)
	for n, h := range heads {
		lsGit(t, bare, "update-ref", fmt.Sprintf("refs/pull/%d/head", n), h)
	}
	return "file://" + bare, bare, heads
}

// fakeGitHub records every REST call and answers the four the lander makes.
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

func TestLandStreamEndToEnd(t *testing.T) {
	mr := miniredis.RunT(t)
	addr := mr.Addr()
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	url, bare, heads := lsFixture(t)
	gh := newFakeGitHub(t)
	prev := landStreamToken
	landStreamToken = func() (string, error) { return "test-token", nil }
	t.Cleanup(func() { landStreamToken = prev })

	// Three members in merging, pr_ready_at 3 < 1 < 2: order is #3, #1, #2.
	for n, at := range map[int]float64{1: 200, 2: 300, 3: 100} {
		id := fmt.Sprintf("t%d", n)
		c.ZAdd(ctx, "ws:"+lsStream+":merging", redis.Z{Score: at, Member: id})
		c.HSet(ctx, "task:"+id, "stream", lsStream, "state", "merging", "pr", fmt.Sprint(n))
		if code, out, errOut := runSprint("pr", "record", "--redis", addr, "--repo", lsRepo, "--n", fmt.Sprint(n),
			"--head", heads[n], "--base", "dev", "--stream", lsStream, "--task", id); code != 0 || !strings.Contains(out, "created=true") {
			t.Fatalf("pr record: %d %s %s", code, out, errOut)
		}
		line := fmt.Sprintf("SCORE who=rowan head=%s score=10/10 gates=ci:ok,base:ok,scope:ok", heads[n])
		if code, out, errOut := runSprint("pr", "lines", "--redis", addr, "--repo", lsRepo, "--n", fmt.Sprint(n), "--add", line); code != 0 || !strings.Contains(out, "lines=1 added=SCORE") {
			t.Fatalf("pr lines: %d %s %s", code, out, errOut)
		}
	}
	c.Set(ctx, "cfg:land:test:"+lsRepo, "test ! -e red.txt", 0)

	// Dry run: the order from Redis alone, no clone, no GitHub.
	code, out, errOut := runSprint("land", "stream", "--redis", addr, "--repo", lsRepo, "--stream", lsStream, "--dry-run")
	if code != 0 {
		t.Fatalf("dry run: %d %s %s", code, out, errOut)
	}
	var order []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.HasPrefix(l, "ORDER ") {
			order = append(order, strings.Fields(l)[2])
		}
	}
	if strings.Join(order, " ") != lsRepo+"#3 "+lsRepo+"#1 "+lsRepo+"#2" || !strings.Contains(out, "LAND STREAM DRY repo="+lsRepo+" stream="+lsSlug+" branch=stream/"+lsSlug+" members=3") {
		t.Fatalf("dry run order %v:\n%s", order, out)
	}
	if len(gh.Calls()) != 0 {
		t.Fatalf("dry run called GitHub: %v", gh.Calls())
	}

	// Real run: #2 is red and parked by bisect; one PR is opened.
	work := filepath.Join(t.TempDir(), "clone")
	code, out, errOut = runSprint("land", "stream", "--redis", addr, "--repo", lsRepo, "--stream", lsStream,
		"--remote", url, "--mirror", "none", "--workdir", work, "--api", gh.srv.URL)
	if code != 0 {
		t.Fatalf("land stream: %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "members=#3,#1 parked=#2:red:batch-test moved=1 tests=5 pr=#900 reused=false state=open") {
		t.Fatalf("receipt:\n%s", out)
	}
	calls := gh.Calls()
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "POST /repos/"+lsRepo+"/pulls stream/"+lsSlug) || !strings.Contains(calls[0], "STREAM: "+lsStream) {
		t.Fatalf("REST calls %v", calls)
	}
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Fatalf("workdir kept after a clean run: %v", err)
	}
	if lsGit(t, bare, "rev-parse", "refs/heads/stream/"+lsSlug) == "" {
		t.Fatal("stream branch not pushed")
	}
	if st, _ := c.HGet(ctx, "task:t2", "state").Result(); st != "working" {
		t.Fatalf("parked member task state %q", st)
	}
	if ok, _ := c.ZScore(ctx, "ws:"+lsStream+":working", "t2").Result(); ok == 0 {
		t.Fatal("t2 not in working")
	}

	// Status reads the landing and the stream PR record: ci pending.
	code, out, _ = runSprint("land", "status", "--redis", addr, "--repo", lsRepo)
	if code != 0 || !strings.Contains(out, "STREAM "+lsSlug+" streams=") || !strings.Contains(out, "pr=#900 ci=pending mergeable=-") {
		t.Fatalf("status: %d\n%s", code, out)
	}

	// Merge refuses while ci is pending, and calls nothing.
	code, _, errOut = runSprint("land", "merge", "--redis", addr, "--repo", lsRepo, "--stream", lsStream, "--api", gh.srv.URL)
	if code != 2 || !strings.Contains(errOut, "REFUSED ci=pending") {
		t.Fatalf("merge on pending: %d %s", code, errOut)
	}
	if len(gh.Calls()) != 1 {
		t.Fatalf("a refused merge called GitHub: %v", gh.Calls())
	}

	// CI green and mergeable: merge, move #3 and #1 to landed in one call, close them.
	if code, out, errOut := runSprint("pr", "record", "--redis", addr, "--repo", lsRepo, "--n", "900", "--ci", "green", "--mergeable", "true"); code != 0 {
		t.Fatalf("pr record ci: %d %s %s", code, out, errOut)
	}
	code, out, errOut = runSprint("land", "merge", "--redis", addr, "--repo", lsRepo, "--stream", lsStream, "--api", gh.srv.URL)
	if code != 0 || !strings.Contains(out, "LAND MERGE repo="+lsRepo+" stream="+lsSlug+" pr=#900") || !strings.Contains(out, "members=2 moved=2 missing=0 already=false closed=#3,#1 unclosed=- rest_calls=5") {
		t.Fatalf("merge: %d\n%s\n%s", code, out, errOut)
	}
	if n, _ := c.ZCard(ctx, "ws:"+lsStream+":landed").Result(); n != 2 {
		t.Fatalf("landed %d", n)
	}
	if n, _ := c.ZCard(ctx, "ws:"+lsStream+":merging").Result(); n != 0 {
		t.Fatalf("merging %d", n)
	}
	calls = gh.Calls()
	head := lsGit(t, bare, "rev-parse", "refs/heads/stream/"+lsSlug)
	wantClose := fmt.Sprintf("CLOSE who=rowan: in stream/%s at %s; landed with %s#900", lsSlug, head[:8], lsRepo)
	if !strings.HasPrefix(calls[1], "PUT /repos/"+lsRepo+"/pulls/900/merge "+head) ||
		calls[2] != "POST /repos/"+lsRepo+"/issues/3/comments "+wantClose || calls[3] != "PATCH /repos/"+lsRepo+"/pulls/3 closed" {
		t.Fatalf("REST calls:\n%s", strings.Join(calls, "\n"))
	}
	if log, _ := c.XLen(ctx, "ws:log").Result(); log != 4 { // park, two lands, the landing
		t.Fatalf("ws:log %d", log)
	}
	// A re-run is ALREADY and closes nothing twice.
	code, out, _ = runSprint("land", "merge", "--redis", addr, "--repo", lsRepo, "--stream", lsStream, "--api", gh.srv.URL)
	if code != 0 || !strings.Contains(out, "already=true closed=- unclosed=- rest_calls=0") {
		t.Fatalf("re-run: %d\n%s", code, out)
	}
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

// When a stream lander pushes a stream branch head it requests CI automatically (#3717).
func TestLandStreamAutoRequestsCI(t *testing.T) {
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	url, bare, heads := lsFixture(t)
	gh := newFakeGitHub(t)
	prev := landStreamToken
	landStreamToken = func() (string, error) { return "test-token", nil }
	t.Cleanup(func() { landStreamToken = prev })

	// One member in merging, score 10
	n := 1
	id := "t1"
	c.ZAdd(ctx, "ws:"+lsStream+":merging", redis.Z{Score: 100, Member: id})
	c.HSet(ctx, "task:"+id, "stream", lsStream, "state", "merging", "pr", fmt.Sprint(n))
	if code, out, errOut := runSprint("pr", "record", "--redis", addr, "--repo", lsRepo, "--n", fmt.Sprint(n),
		"--head", heads[n], "--base", "dev", "--stream", lsStream, "--task", id); code != 0 || !strings.Contains(out, "created=true") {
		t.Fatalf("pr record: %d %s %s", code, out, errOut)
	}
	line := fmt.Sprintf("SCORE who=rowan head=%s score=10/10 gates=ci:ok,base:ok,scope:ok", heads[n])
	if code, out, errOut := runSprint("pr", "lines", "--redis", addr, "--repo", lsRepo, "--n", fmt.Sprint(n), "--add", line); code != 0 || !strings.Contains(out, "lines=1 added=SCORE") {
		t.Fatalf("pr lines: %d %s %s", code, out, errOut)
	}

	work := filepath.Join(t.TempDir(), "clone")
	code, out, errOut := runSprint("land", "stream", "--redis", addr, "--repo", lsRepo, "--stream", lsStream,
		"--remote", url, "--mirror", "none", "--workdir", work, "--api", gh.srv.URL, "--test", "true")
	if code != 0 {
		t.Fatalf("land stream: %d\n%s\n%s", code, out, errOut)
	}

	streamHead := lsGit(t, bare, "rev-parse", "refs/heads/stream/"+lsSlug)
	if streamHead == "" {
		t.Fatal("stream branch not pushed")
	}

	// ci:pool holds stream head (#3717).
	score, err := c.ZScore(ctx, "ci:pool", "nova-tools:"+streamHead).Result()
	if err != nil || score <= 0 {
		t.Fatalf("ci:pool does not hold nova-tools:%s: score=%f err=%v", streamHead, score, err)
	}

	// The CI record is pending.
	rec := c.HGetAll(ctx, "ci:nova-tools:"+streamHead).Val()
	if rec["ci"] != "pending" || rec["repo"] != "nova-tools" || rec["sha"] != streamHead || rec["pr"] != "900" {
		t.Fatalf("ci record = %v, want ci=pending repo=nova-tools sha=%s pr=900", rec, streamHead)
	}
}


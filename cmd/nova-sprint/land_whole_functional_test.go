//go:build functional

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
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// mergeForge is a fake GitHub: POST pulls opens #900, PUT merge merges the
// stream branch --no-ff into dev in the bare repo at exactly the sha asked
// (the one write to dev), comments and PATCHes are accepted.
type mergeForge struct {
	mu    sync.Mutex
	calls []string
	srv   *httptest.Server
}

func newMergeForge(t *testing.T, bare string) *mergeForge {
	g := &mergeForge{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		g.mu.Lock()
		g.calls = append(g.calls, r.Method+" "+r.URL.Path)
		g.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/"+lsRepo+"/pulls":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number":900}`))
		case r.Method == http.MethodPut && r.URL.Path == "/repos/"+lsRepo+"/pulls/900/merge":
			sha, err := forgeMerge(bare, "stream/"+lsSlug, body["sha"])
			if err != nil {
				http.Error(w, `{"message":"`+strings.ReplaceAll(err.Error(), `"`, `'`)+`"}`, http.StatusConflict)
				return
			}
			_, _ = w.Write([]byte(`{"merged":true,"sha":"` + sha + `"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comments"):
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPatch:
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"body":""}`))
		default:
			http.Error(w, `{"message":"unexpected"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *mergeForge) count(prefix string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, c := range g.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// forgeMerge is the forge's merge button: --no-ff of the branch at sha into
// dev, pushed to the bare repo; it refuses when the branch moved.
func forgeMerge(bare, branch, sha string) (string, error) {
	dir, err := os.MkdirTemp("", "forge-merge-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	run := func(args ...string) (string, error) {
		full := append([]string{"-c", "user.name=Forge", "-c", "user.email=forge@example.invalid", "-c", "commit.gpgsign=false"}, args...)
		cmd := execGit(dir, full...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	}
	if _, err := run("clone", "-q", "-b", "dev", "file://"+bare, "w"); err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "w")
	head, err := run("rev-parse", "origin/"+branch)
	if err != nil {
		return "", err
	}
	if head != sha {
		return "", fmt.Errorf("head moved: %s is not %s", head, sha)
	}
	if _, err := run("merge", "-q", "--no-ff", "-m", "Merge stream", sha); err != nil {
		return "", err
	}
	if _, err := run("push", "-q", "origin", "HEAD:dev"); err != nil {
		return "", err
	}
	return run("rev-parse", "HEAD")
}

// lrFixture is the three-member stream of lsFixture (#2 red under the batch
// test) on a throwaway redis-server with the library loaded, and our own CI
// declared as one check (pass true for green, false for red), one attempt.
func lrFixture(t *testing.T, check string) (addr string, c *redis.Client, url, bare string, heads map[int]string, g *mergeForge) {
	t.Helper()
	addr = testutil.Start(t)
	c = redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	url, bare, heads = lsFixture(t)
	lsGit(t, bare, "config", "uploadpack.allowAnySHA1InWant", "true")
	for n, at := range map[int]float64{1: 200, 2: 300, 3: 100} {
		id := fmt.Sprintf("t%d", n)
		c.ZAdd(ctx, "ws:"+lsStream+":merging", redis.Z{Score: at, Member: id})
		c.HSet(ctx, "task:"+id, "stream", lsStream, "state", "merging", "pr", fmt.Sprint(n))
		if code, out, errOut := runSprint("pr", "record", "--redis", addr, "--repo", lsRepo, "--n", fmt.Sprint(n),
			"--head", heads[n], "--base", "dev", "--stream", lsStream, "--task", id); code != 0 {
			t.Fatalf("pr record: %d %s %s", code, out, errOut)
		}
		line := fmt.Sprintf("SCORE who=emma head=%s score=10/10 gates=ci:ok,base:ok,scope:ok", heads[n])
		if code, out, errOut := runSprint("pr", "lines", "--redis", addr, "--repo", lsRepo, "--n", fmt.Sprint(n), "--add", line); code != 0 {
			t.Fatalf("pr lines: %d %s %s", code, out, errOut)
		}
	}
	c.Set(ctx, "cfg:land:test:"+lsRepo, "test ! -e red.txt", 0)
	c.HSet(ctx, "cfg:ci:nova-tools", "checks", "batch", "check:batch", check)
	c.HSet(ctx, "cfg:ci", "max_attempts", "1")
	g = newMergeForge(t, bare)
	prev := landStreamToken
	landStreamToken = func() (string, error) { return "test-token", nil }
	t.Cleanup(func() { landStreamToken = prev })
	return addr, c, url, bare, heads, g
}

// benchTurn swaps the CI wait's tick for one bench pass (`ci run`), then
// before, if set; it counts the turns.
func benchTurn(t *testing.T, addr string, before func()) *int {
	t.Helper()
	root := t.TempDir()
	turns := new(int)
	prev := landRunSleep
	landRunSleep = func(ctx context.Context, _ time.Duration) error {
		*turns++
		if *turns > 5 {
			return fmt.Errorf("ci still pending after %d bench turns", *turns-1)
		}
		if before != nil {
			before()
		}
		code, out, errOut := runSprint("ci", "run", "--redis", addr, "--bench", "b1", "--results", filepath.Join(root, "results"),
			"--scratch", filepath.Join(root, fmt.Sprintf("scratch-%d", *turns)), "--mirror-root", filepath.Join(root, "no-mirror"))
		if code != 0 {
			return fmt.Errorf("ci run: %d %s %s", code, out, errOut)
		}
		t.Logf("bench turn %d: %s", *turns, strings.TrimSpace(out))
		return nil
	}
	t.Cleanup(func() { landRunSleep = prev })
	return turns
}

// landArgs runs the fixture's batch test locally (--test, a seat that is not
// the coordinator's) so the bisect parks #2; with no --test land runs none
// and CI is the batch test (#3899, land_nolocal_test.go).
func landArgs(addr, url, work string, g *mergeForge, extra ...string) []string {
	return append([]string{"land", "--redis", addr, "--repo", lsRepo, "--stream", lsStream, "--remote", url, "--ci-url", url,
		"--mirror", "none", "--workdir", work, "--api", g.srv.URL, "--tick", "1ms", "--test", "test ! -e red.txt"}, extra...)
}

func TestLandBatchMergesOldestFirst(t *testing.T) {
	addr, c, url, bare, heads, g := lrFixture(t, "true")
	ctx := context.Background()
	turns := benchTurn(t, addr, nil)
	base := lsGit(t, bare, "rev-parse", "refs/heads/dev")
	work := filepath.Join(t.TempDir(), "clone")
	code, out, errOut := runSprint(landArgs(addr, url, work, g)...)
	if code != 0 {
		t.Fatalf("land: %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "LANDED repo="+lsRepo+" stream="+lsSlug+" pr=#900 ") ||
		!strings.Contains(out, " members=#3,#1 moved=2 builds=1 resumed=false ci=green calls=2 ") {
		t.Fatalf("receipt:\n%s", out)
	}
	// The stream branch is the base plus one --no-ff merge per member,
	// oldest pr_ready_at first (#3 at 100, #1 at 200); #2 turned the batch
	// red and was parked.
	head := lsGit(t, bare, "rev-parse", "refs/heads/stream/"+lsSlug)
	var seconds []string
	for _, p := range strings.Split(lsGit(t, bare, "log", "--first-parent", "--reverse", "--format=%P", base+".."+head), "\n") {
		f := strings.Fields(p)
		if len(f) != 2 {
			t.Fatalf("stream commit parents %q: every member is a --no-ff merge", p)
		}
		seconds = append(seconds, f[1])
	}
	if strings.Join(seconds, " ") != heads[3]+" "+heads[1] {
		t.Fatalf("merge order %v, want #3 then #1", seconds)
	}
	if *turns != 1 {
		t.Fatalf("bench turns %d, want 1", *turns)
	}
	if w, _ := c.HGet(ctx, "ci:nova-tools:"+head, "ci").Result(); w != "green" {
		t.Fatalf("ci record at the stream head %q", w)
	}
	if n, _ := c.ZCard(ctx, "ws:"+lsStream+":landed").Result(); n != 2 {
		t.Fatalf("landed %d", n)
	}
	if n, _ := c.ZCard(ctx, "ws:"+lsStream+":merging").Result(); n != 0 {
		t.Fatalf("merging %d", n)
	}
	if ok, _ := c.ZScore(ctx, "ws:"+lsStream+":working", "t2").Result(); ok == 0 {
		t.Fatal("the parked member t2 is not in working")
	}
	if st, _ := c.HGet(ctx, "land:"+lsRepo+":"+lsSlug, "state").Result(); st != "merged" {
		t.Fatalf("landing state %q", st)
	}
}

func TestLandPushesDevOnce(t *testing.T) {
	addr, c, url, bare, _, g := lrFixture(t, "true")
	ctx := context.Background()
	base := lsGit(t, bare, "rev-parse", "refs/heads/dev")
	work := filepath.Join(t.TempDir(), "clone")

	// No bench and no wait: the run stops at the CI wait. The stream branch
	// and PR exist; dev is untouched.
	code, out, _ := runSprint(landArgs(addr, url, work, g, "--ci-wait", "0s")...)
	if code != 1 || !strings.Contains(out, "LAND WAITING repo="+lsRepo+" stream="+lsSlug+" pr=#900 ") || !strings.Contains(out, " ci=pending request=CREATED builds=1 ") {
		t.Fatalf("waiting: %d\n%s", code, out)
	}
	if got := lsGit(t, bare, "rev-parse", "refs/heads/dev"); got != base {
		t.Fatalf("dev moved before the merge: %s", got)
	}

	// A re-run resumes at the wait (no second build, no second PR), a bench
	// turns the head green, and the one merge is the one write to dev.
	benchTurn(t, addr, nil)
	code, out, errOut := runSprint(landArgs(addr, url, work+"-2", g)...)
	if code != 0 || !strings.Contains(out, " builds=0 resumed=true ci=green calls=1 ") {
		t.Fatalf("resume: %d\n%s\n%s", code, out, errOut)
	}
	if n := g.count("POST /repos/" + lsRepo + "/pulls"); n != 1 {
		t.Fatalf("stream PRs opened %d, want 1", n)
	}
	if n := g.count("PUT "); n != 1 {
		t.Fatalf("merge calls %d, want 1", n)
	}
	head := lsGit(t, bare, "rev-parse", "refs/heads/stream/"+lsSlug)
	if n := lsGit(t, bare, "rev-list", "--count", "--first-parent", base+"..refs/heads/dev"); n != "1" {
		t.Fatalf("dev gained %s first-parent commits, want 1", n)
	}
	if p := lsGit(t, bare, "rev-parse", "refs/heads/dev^2"); p != head {
		t.Fatalf("dev's merge brings %s, not the stream head %s", p, head)
	}
	if n, _ := c.ZCard(ctx, "ws:"+lsStream+":landed").Result(); n != 2 {
		t.Fatalf("landed %d", n)
	}
}

func TestLandRefusesRedCI(t *testing.T) {
	addr, c, url, bare, _, g := lrFixture(t, "false")
	ctx := context.Background()
	turns := benchTurn(t, addr, nil)
	base := lsGit(t, bare, "rev-parse", "refs/heads/dev")
	work := filepath.Join(t.TempDir(), "clone")
	code, out, errOut := runSprint(landArgs(addr, url, work, g)...)
	if code != 1 || !strings.Contains(out, "LAND RED repo="+lsRepo+" stream="+lsSlug+" pr=#900 ") || !strings.Contains(out, "remedy=nova-sprint ci status --repo nova-tools --sha ") {
		t.Fatalf("red: %d\n%s\n%s", code, out, errOut)
	}
	if *turns != 1 || g.count("PUT ") != 0 {
		t.Fatalf("turns %d, merge calls %d: red CI merged", *turns, g.count("PUT "))
	}
	if got := lsGit(t, bare, "rev-parse", "refs/heads/dev"); got != base {
		t.Fatalf("dev moved on red: %s", got)
	}
	if n, _ := c.ZCard(ctx, "ws:"+lsStream+":merging").Result(); n != 2 {
		t.Fatalf("members left merging on red: merging=%d", n)
	}
	// A re-run does not rebuild or re-test a red head: red again, no calls.
	calls := g.count("")
	code, out, _ = runSprint(landArgs(addr, url, work+"-2", g)...)
	if code != 1 || !strings.Contains(out, "LAND RED ") || !strings.Contains(out, " builds=0 ") || g.count("") != calls || *turns != 1 {
		t.Fatalf("re-run on red: %d calls %d->%d turns %d\n%s", code, calls, g.count(""), *turns, out)
	}
}

func TestLandRebuildsOnAMovedBase(t *testing.T) {
	addr, c, url, bare, _, g := lrFixture(t, "true")
	ctx := context.Background()
	// While the first head is in CI, a fix lands on dev: the green head is
	// on a stale base, so the run builds again on the new tip, reuses the
	// PR, and merges the head that holds the fix.
	src := filepath.Join(t.TempDir(), "fix")
	moved := false
	benchTurn(t, addr, func() {
		if moved {
			return
		}
		moved = true
		lsGit(t, filepath.Dir(src), "clone", "-q", "-b", "dev", url, src)
		if err := os.WriteFile(filepath.Join(src, "fix.txt"), []byte("fix\n"), 0o644); err != nil {
			t.Error(err)
		}
		lsGit(t, src, "add", ".")
		lsGit(t, src, "commit", "-q", "-m", "a fix on dev")
		lsGit(t, src, "push", "-q", "origin", "HEAD:dev")
	})
	work := filepath.Join(t.TempDir(), "clone")
	code, out, errOut := runSprint(landArgs(addr, url, work, g)...)
	if code != 0 || !strings.Contains(out, " builds=2 resumed=false ci=green calls=2 ") {
		t.Fatalf("land: %d\n%s\n%s", code, out, errOut)
	}
	head := lsGit(t, bare, "rev-parse", "refs/heads/stream/"+lsSlug)
	lsGit(t, bare, "cat-file", "-e", head+":fix.txt")
	if g.count("POST /repos/"+lsRepo+"/pulls") != 1 || g.count("PUT ") != 1 {
		t.Fatalf("PRs %d merges %d", g.count("POST /repos/"+lsRepo+"/pulls"), g.count("PUT "))
	}
	if n, _ := c.ZCard(ctx, "ws:"+lsStream+":landed").Result(); n != 2 {
		t.Fatalf("landed %d", n)
	}
}

func execGit(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	return cmd
}

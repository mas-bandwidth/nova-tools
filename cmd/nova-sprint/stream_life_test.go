package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// streamLifeSeed is a throwaway redis-server with the library loaded, the
// fixture remote (#1 and #3 green, #2 red) and #1 and #3 in merging with a
// 10 read each; the token is a test token and the forge is fake.
func streamLifeSeed(t *testing.T) (c *redis.Client, addr, url, bare string, gh *fakeGitHub) {
	t.Helper()
	addr = testutil.Start(t)
	c = redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	url, bare, heads := lsFixture(t)
	gh = newFakeGitHub(t)
	prev := landStreamToken
	landStreamToken = func() (string, error) { return "test-token", nil }
	t.Cleanup(func() { landStreamToken = prev })
	c.SAdd(ctx, "ws:names", lsStream)
	c.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: lsStream})
	for n, at := range map[int]float64{1: 200, 3: 100} {
		id := fmt.Sprintf("t%d", n)
		c.ZAdd(ctx, "ws:"+lsStream+":merging", redis.Z{Score: at, Member: id})
		c.HSet(ctx, "task:"+id, "stream", lsStream, "state", "merging", "pr", fmt.Sprint(n), "created_at", fmt.Sprint(at))
		if code, out, errOut := runSprint("pr", "record", "--redis", addr, "--repo", lsRepo, "--n", fmt.Sprint(n),
			"--head", heads[n], "--base", "dev", "--stream", lsStream, "--task", id); code != 0 {
			t.Fatalf("pr record: %d %s %s", code, out, errOut)
		}
		line := fmt.Sprintf("SCORE who=rowan head=%s score=10/10 gates=ci:ok,base:ok,scope:ok", heads[n])
		if code, out, errOut := runSprint("pr", "lines", "--redis", addr, "--repo", lsRepo, "--n", fmt.Sprint(n), "--add", line); code != 0 {
			t.Fatalf("pr lines: %d %s %s", code, out, errOut)
		}
	}
	c.Set(ctx, "cfg:land:test:"+lsRepo, "test ! -e red.txt", 0)
	return c, addr, url, bare, gh
}

func streamLifeArgs(sub, addr, url string, gh *fakeGitHub, t *testing.T) []string {
	return []string{"stream", sub, "--redis", addr, "--repo", lsRepo, "--stream", lsStream,
		"--remote", url, "--mirror", "none", "--workdir", filepath.Join(t.TempDir(), "clone"), "--api", gh.srv.URL}
}

// TestStreamOpenRecordsBase: stream open cuts stream/<slug> off the base tip
// and records base and base_sha on the landing; a second open is refused
// naming rebase; rebase after the base moves records the new base_sha and
// reuses the one PR.
func TestStreamOpenRecordsBase(t *testing.T) {
	c, addr, url, bare, gh := streamLifeSeed(t)
	ctx := context.Background()
	key := "land:" + lsRepo + ":" + lsSlug

	code, out, errOut := runSprint(streamLifeArgs("rebase", addr, url, gh, t)...)
	if code != 1 || !strings.HasPrefix(out, "REFUSED no open stream PR to rebase (landing absent) remedy=nova-sprint stream open") {
		t.Fatalf("rebase before open: %d\n%s\n%s", code, out, errOut)
	}

	base := lsGit(t, bare, "rev-parse", "refs/heads/dev")
	code, out, errOut = runSprint(streamLifeArgs("open", addr, url, gh, t)...)
	if code != 0 || !strings.Contains(out, "members=#3,#1 ") || !strings.Contains(out, "pr=#900 reused=false state=open") {
		t.Fatalf("open: %d\n%s\n%s", code, out, errOut)
	}
	rec := c.HGetAll(ctx, key).Val()
	if rec["base"] != "dev" || rec["base_sha"] != base || rec["branch"] != "stream/"+lsSlug || rec["state"] != "open" || rec["pr"] != "900" {
		t.Fatalf("landing after open %v, want base dev at %s", rec, base)
	}
	if head := lsGit(t, bare, "rev-parse", "refs/heads/stream/"+lsSlug); head != rec["head"] {
		t.Fatalf("stream branch %s, record head %s", head, rec["head"])
	}
	if lsGit(t, bare, "merge-base", "refs/heads/stream/"+lsSlug, "refs/heads/dev") != base {
		t.Fatal("the stream branch is not cut off the base tip")
	}

	code, out, _ = runSprint(streamLifeArgs("open", addr, url, gh, t)...)
	if code != 1 || !strings.HasPrefix(out, "REFUSED "+key+" is open as #900 on stream/"+lsSlug+" at base dev@"+base[:8]+" remedy=nova-sprint stream rebase") {
		t.Fatalf("second open: %d\n%s", code, out)
	}
	if n := len(gh.Calls()); n != 1 {
		t.Fatalf("REST calls after open and a refused open: %d, want 1", n)
	}

	// The base moves on: rebase rebuilds on the new tip and reuses the PR.
	w := filepath.Join(t.TempDir(), "w")
	lsGit(t, filepath.Dir(w), "clone", "-q", "-b", "dev", bare, w)
	if err := os.WriteFile(filepath.Join(w, "b.txt"), []byte("later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lsGit(t, w, "add", ".")
	lsGit(t, w, "commit", "-q", "-m", "base moves")
	lsGit(t, w, "push", "-q", "origin", "dev")
	base2 := lsGit(t, bare, "rev-parse", "refs/heads/dev")
	code, out, errOut = runSprint(streamLifeArgs("rebase", addr, url, gh, t)...)
	if code != 0 || !strings.Contains(out, "base=dev@"+base2[:8]) || !strings.Contains(out, "pr=#900 reused=true state=open") {
		t.Fatalf("rebase: %d\n%s\n%s", code, out, errOut)
	}
	if got := c.HGet(ctx, key, "base_sha").Val(); got != base2 {
		t.Fatalf("base_sha after rebase %s, want %s", got, base2)
	}
	if lsGit(t, bare, "merge-base", "refs/heads/stream/"+lsSlug, "refs/heads/dev") != base2 {
		t.Fatal("the rebased stream branch does not contain the new base tip")
	}
	if n := len(gh.Calls()); n != 1 {
		t.Fatalf("rebase opened a second PR: %v", gh.Calls())
	}

	code, out, _ = runSprint("stream", "status", "--redis", addr, "--repo", lsRepo)
	if code != 0 || !strings.Contains(out, "STREAM "+lsSlug+" ") || !strings.Contains(out, "state=open") || !strings.Contains(out, "pr=#900") {
		t.Fatalf("status: %d\n%s", code, out)
	}
}

// TestStreamCloseMarksEveryMemberLanded: stream close is the lander's merge,
// the one event that moves every member merging -> landed and closes the
// member PRs; a re-run closes nothing twice.
func TestStreamCloseMarksEveryMemberLanded(t *testing.T) {
	c, addr, url, _, gh := streamLifeSeed(t)
	ctx := context.Background()
	if code, out, errOut := runSprint(streamLifeArgs("open", addr, url, gh, t)...); code != 0 {
		t.Fatalf("open: %d\n%s\n%s", code, out, errOut)
	}
	closeArgs := []string{"stream", "close", "--redis", addr, "--repo", lsRepo, "--stream", lsStream, "--api", gh.srv.URL}
	if code, _, errOut := runSprint(closeArgs...); code != 2 || !strings.Contains(errOut, "REFUSED ci=") {
		t.Fatalf("close before green: %d %s", code, errOut)
	}
	if code, out, errOut := runSprint("pr", "record", "--redis", addr, "--repo", lsRepo, "--n", "900", "--ci", "green", "--mergeable", "true"); code != 0 {
		t.Fatalf("pr record ci: %d %s %s", code, out, errOut)
	}
	code, out, errOut := runSprint(closeArgs...)
	if code != 0 || !strings.Contains(out, "LAND MERGE repo="+lsRepo+" stream="+lsSlug+" pr=#900") || !strings.Contains(out, "closed=#3,#1 unclosed=-") {
		t.Fatalf("close: %d\n%s\n%s", code, out, errOut)
	}
	for _, id := range []string{"t1", "t3"} {
		if _, err := c.ZScore(ctx, "ws:"+lsStream+":landed", id).Result(); err != nil {
			t.Fatalf("%s not in landed: %v", id, err)
		}
	}
	if n := c.ZCard(ctx, "ws:"+lsStream+":merging").Val(); n != 0 {
		t.Fatalf("merging %d after close", n)
	}
	if st := c.HGet(ctx, "land:"+lsRepo+":"+lsSlug, "state").Val(); st != "merged" {
		t.Fatalf("landing state %q", st)
	}
	calls := strings.Join(gh.Calls(), "\n")
	for _, n := range []int{1, 3} {
		if !strings.Contains(calls, fmt.Sprintf("PATCH /repos/%s/pulls/%d closed", lsRepo, n)) {
			t.Fatalf("member #%d not closed:\n%s", n, calls)
		}
	}
	code, out, _ = runSprint(closeArgs...)
	if code != 0 || !strings.Contains(out, "already=true closed=- unclosed=-") {
		t.Fatalf("second close: %d\n%s", code, out)
	}
}

func TestStreamLifeUsage(t *testing.T) {
	t.Parallel()

	for _, sub := range []string{"open", "rebase", "pr", "close"} {
		if code, _, errOut := runSprint("stream", sub); code != 2 || strings.Count(errOut, "\n") != 1 {
			t.Errorf("stream %s with no flags: exit %d, stderr %q", sub, code, errOut)
		}
	}
	if code, _, errOut := runSprint("stream", "status", "--repo", "x"); code != 2 || strings.Count(errOut, "\n") != 1 {
		t.Errorf("stream status bad repo: exit %d %q", code, errOut)
	}
	if code, _, errOut := runSprint("stream", "bogus"); code != 2 || !strings.Contains(errOut, "open, rebase, pr, status or close") {
		t.Errorf("unknown subverb: %d %q", code, errOut)
	}
}
